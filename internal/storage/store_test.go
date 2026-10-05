package storage

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"yousage/internal/domain"
)

func openFixture(t *testing.T) (*Store, string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "usage.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	id, err := store.RegisterSource(context.Background(), domain.SourceFingerprint{HostId: "local", Provider: domain.Codex, ResolvedHomePath: "/fixture", VolumeId: "disk"})
	if err != nil {
		t.Fatal(err)
	}
	return store, path, id
}
func usageRecord(model string) domain.Record {
	return domain.Record{Provider: domain.Codex, Model: model, Speed: "standard", Totals: domain.Tokens{OutputTokens: 10}}
}

func TestReopenAtomicAppendTailRewriteAndConflict(t *testing.T) {
	store, path, id := openFixture(t)
	ctx := context.Background()
	checkpoint := domain.Checkpoint{SourceId: id, Path: "history.jsonl", FileSize: 100, Offset: 80, Reducer: json.RawMessage(`{"model":"one"}`)}
	checkpoint, err := store.CommitFileWithTail(ctx, checkpoint, []domain.Record{usageRecord("one")}, []domain.Record{usageRecord("tail")}, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	recovered, err := store.Checkpoint(ctx, id, "history.jsonl")
	if err != nil || recovered.Offset != 80 || recovered.RecordCount != 1 || recovered.Revision != 1 {
		t.Fatal(recovered, err)
	}
	checkpoint.FileSize, checkpoint.Offset = 150, 150
	checkpoint, err = store.CommitFileWithTail(ctx, checkpoint, []domain.Record{usageRecord("tail")}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	records, err := store.Records(ctx, id)
	if err != nil || len(records) != 2 || records[1].Model != "tail" {
		t.Fatal(records, err)
	}
	if _, err := store.CommitFile(ctx, recovered, []domain.Record{usageRecord("stale")}, true); !errors.Is(err, ErrConflict) {
		t.Fatal("stale scan overwrote facts", err)
	}
	checkpoint.FileSize, checkpoint.Offset = 10, 10
	checkpoint, err = store.CommitFile(ctx, checkpoint, []domain.Record{usageRecord("rewritten")}, true)
	if err != nil {
		t.Fatal(err)
	}
	records, err = store.Records(ctx, id)
	if err != nil || len(records) != 1 || records[0].Model != "rewritten" {
		t.Fatal(records, err)
	}
	if checkpoint.RecordCount != 1 {
		t.Fatal(checkpoint)
	}
}

func TestTransactionFailurePreservesRecordsCheckpointAndSuccess(t *testing.T) {
	store, _, id := openFixture(t)
	ctx := context.Background()
	checkpoint := domain.Checkpoint{SourceId: id, Path: "file", FileSize: 10, Offset: 10, Reducer: json.RawMessage(`{}`)}
	checkpoint, err := store.CommitFile(ctx, checkpoint, []domain.Record{usageRecord("old")}, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.database.Exec(`CREATE TRIGGER reject_bad BEFORE INSERT ON records WHEN json_extract(NEW.data,'$.model')='bad' BEGIN SELECT RAISE(ABORT,'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitFile(ctx, checkpoint, []domain.Record{usageRecord("new"), usageRecord("bad")}, true); err == nil {
		t.Fatal("injected failure not reached")
	}
	records, err := store.Records(ctx, id)
	if err != nil || len(records) != 1 || records[0].Model != "old" {
		t.Fatal("transaction leaked", records, err)
	}
	recovered, err := store.Checkpoint(ctx, id, "file")
	if err != nil || recovered.Revision != checkpoint.Revision {
		t.Fatal(recovered, err)
	}
	success := int64(100)
	if err := store.Observe(ctx, domain.Observation{SourceId: id, Status: "ok", AttemptedAt: 100, SucceededAt: &success}); err != nil {
		t.Fatal(err)
	}
	if err := store.Observe(ctx, domain.Observation{SourceId: id, Status: "failed", AttemptedAt: 200, Notice: "read failed"}); err != nil {
		t.Fatal(err)
	}
	observation, err := store.Observation(ctx, id)
	if err != nil || observation.SucceededAt == nil || *observation.SucceededAt != 100 || observation.Status != "failed" {
		t.Fatal(observation, err)
	}
}

func TestConcurrentSettingsAndPerConnectionForeignKeys(t *testing.T) {
	store, _, _ := openFixture(t)
	ctx := context.Background()
	var wait sync.WaitGroup
	results := make(chan error, 2)
	for index := 0; index < 2; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := store.WriteSetting(ctx, "race", 0, map[string]int{"value": 1})
			results <- err
		}()
	}
	wait.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal(success, conflict)
	}
	connections := make([]interface{ Close() error }, 0, 4)
	for index := 0; index < 4; index++ {
		connection, err := store.database.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		connections = append(connections, connection)
		var enabled int
		if err := connection.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&enabled); err != nil || enabled != 1 {
			t.Fatal("connection invariant", enabled, err)
		}
	}
	for _, connection := range connections {
		connection.Close()
	}
}
