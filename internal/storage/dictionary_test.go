package storage

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
	"yousage/internal/domain"
)

// TestDictionaryReferencesFollowReplacementAndRetention prevents permanent history string growth.
func TestDictionaryReferencesFollowReplacementAndRetention(test *testing.T) {
	store, _, id := openFixture(test)
	ctx := context.Background()
	checkpoint := domain.Checkpoint{SourceId: id, Path: "session", ModifiedNs: 10, Reducer: json.RawMessage(`{}`)}
	old := usageRecord("retired-model")
	old.SessionId, old.DedupeKey, old.QuotaBucket = "retired-session", "retired-key", "codex"
	old.Quota = &domain.QuotaObservation{PlanType: "retired-plan", Windows: []domain.QuotaWindow{{Id: "retired-window"}}}
	checkpoint, err := store.CommitFileWithTail(ctx, checkpoint, []domain.Record{old}, []domain.Record{old}, true)
	if err != nil {
		test.Fatal(err)
	}
	var refs int
	if err := store.database.QueryRow(`SELECT refs FROM strings WHERE value='retired-model'`).Scan(&refs); err != nil || refs != 2 {
		test.Fatal("references not counted per event", refs, err)
	}
	checkpoint, err = store.CommitFile(ctx, checkpoint, []domain.Record{usageRecord("replacement")}, true)
	if err != nil {
		test.Fatal(err)
	}
	var count int
	if err := store.database.QueryRow(`SELECT COUNT(*) FROM strings WHERE value LIKE 'retired-%'`).Scan(&count); err != nil || count != 0 {
		test.Fatal("rewrite retained orphan identities", count, err)
	}
	if err := store.PruneHistory(ctx, id, 11); err != nil {
		test.Fatal(err)
	}
	if err := store.database.QueryRow(`SELECT COUNT(*) FROM strings`).Scan(&count); err != nil || count != 1 {
		test.Fatal("retention retained unused identities", count, err)
	}
	if err := store.database.QueryRow(`SELECT COUNT(*) FROM quotas`).Scan(&count); err != nil || count != 0 {
		test.Fatal("orphan quota observations", count, err)
	}
}

// TestObservationWriteFailureRollsBackEveryTable exercises failure after usage insertion.
func TestObservationWriteFailureRollsBackEveryTable(test *testing.T) {
	store, _, id := openFixture(test)
	ctx := context.Background()
	checkpoint := domain.Checkpoint{SourceId: id, Path: "session", Reducer: json.RawMessage(`{}`)}
	checkpoint, err := store.CommitFile(ctx, checkpoint, []domain.Record{usageRecord("original")}, true)
	if err != nil {
		test.Fatal(err)
	}
	key, _ := store.HistoryCacheKey(ctx, id)
	if _, err := store.database.Exec(`CREATE TRIGGER reject_quota BEFORE INSERT ON quotas BEGIN SELECT RAISE(ABORT,'injected failure'); END`); err != nil {
		test.Fatal(err)
	}
	bad := usageRecord("uncommitted-identity")
	bad.Quota = &domain.QuotaObservation{PlanType: "uncommitted-plan"}
	if _, err := store.CommitFile(ctx, checkpoint, []domain.Record{bad}, true); err == nil {
		test.Fatal("injected failure not reached")
	}
	records, err := store.Records(ctx, id)
	if err != nil || len(records) != 1 || records[0].Model != "original" {
		test.Fatal("usage replacement leaked", err)
	}
	actualKey, _ := store.HistoryCacheKey(ctx, id)
	if actualKey != key {
		test.Fatal("failed write changed checkpoint revision")
	}
	var count int
	if err := store.database.QueryRow(`SELECT COUNT(*) FROM strings WHERE value LIKE 'uncommitted-%'`).Scan(&count); err != nil || count != 0 {
		test.Fatal("failed write leaked dictionary", err)
	}
}

// TestConcurrentReadersReloadExternalDictionary avoids pool exhaustion on another store's writes.
func TestConcurrentReadersReloadExternalDictionary(test *testing.T) {
	store, path, id := openFixture(test)
	other, err := Open(path)
	if err != nil {
		test.Fatal(err)
	}
	defer other.Close()
	checkpoint := domain.Checkpoint{SourceId: id, Path: "session", Reducer: json.RawMessage(`{}`)}
	if _, err := other.CommitFile(context.Background(), checkpoint, []domain.Record{usageRecord("external-model")}, true); err != nil {
		test.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var workers sync.WaitGroup
	failures := make(chan error, 8)
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			records, err := store.Records(ctx, id)
			if err == nil && (len(records) != 1 || records[0].Model != "external-model") {
				err = errors.New("external write was not decoded")
			}
			failures <- err
		}()
	}
	workers.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			test.Fatal(err)
		}
	}
}

// TestDictionaryMissUsesRecordSnapshot preserves a read across another store's rewrite.
func TestDictionaryMissUsesRecordSnapshot(test *testing.T) {
	store, path, id := openFixture(test)
	ctx := context.Background()
	checkpoint := domain.Checkpoint{SourceId: id, Path: "session", Reducer: json.RawMessage(`{}`)}
	original := usageRecord("snapshot-model")
	original.Quota = &domain.QuotaObservation{PlanType: "snapshot-plan", Windows: []domain.QuotaWindow{{Id: "snapshot-window", UsedPercent: 12.5}}}
	checkpoint, err := store.CommitFile(ctx, checkpoint, []domain.Record{original}, true)
	if err != nil {
		test.Fatal(err)
	}
	other, err := Open(path)
	if err != nil {
		test.Fatal(err)
	}
	defer other.Close()
	connection, err := store.database.Conn(ctx)
	if err != nil {
		test.Fatal(err)
	}
	defer connection.Close()
	rows, err := connection.QueryContext(ctx, recordSelect+` WHERE f.source_id=?`, id)
	if err != nil {
		test.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		test.Fatal("missing snapshot fixture")
	}
	if _, err := other.CommitFile(ctx, checkpoint, []domain.Record{usageRecord("new-model")}, true); err != nil {
		test.Fatal(err)
	}
	store.stringValues = sync.Map{}
	actual, err := store.scanRecord(ctx, connection, rows, nil)
	if err != nil || actual.Model != original.Model || actual.Quota == nil || actual.Quota.PlanType != original.Quota.PlanType || actual.Quota.Windows[0] != original.Quota.Windows[0] {
		test.Fatal("dictionary reload left the record's WAL snapshot", err)
	}
}
