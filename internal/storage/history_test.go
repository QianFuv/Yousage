package storage

import (
	"context"
	"encoding/json"
	"testing"
	"yousage/internal/domain"
)

// TestCompletedHistoryKeyTracksHistoricalMutations covers appends, tails and rewrites.
func TestCompletedHistoryKeyTracksHistoricalMutations(test *testing.T) {
	store, _, id := openFixture(test)
	ctx := context.Background()
	checkpoint := domain.Checkpoint{SourceId: id, Path: "session", Reducer: json.RawMessage(`{}`)}
	old := usageRecord("old")
	old.TimestampMs = 10
	checkpoint, err := store.CommitFile(ctx, checkpoint, []domain.Record{old}, true)
	if err != nil {
		test.Fatal(err)
	}
	key, err := store.CompletedHistoryKey(ctx, id, 20)
	if err != nil || key == 0 {
		test.Fatal("historical addition was not tracked", err)
	}
	active := usageRecord("active")
	active.TimestampMs = 30
	checkpoint, err = store.CommitFile(ctx, checkpoint, []domain.Record{active}, false)
	if err != nil {
		test.Fatal(err)
	}
	if actual, _ := store.CompletedHistoryKey(ctx, id, 20); actual != key {
		test.Fatal("active append invalidated completed history")
	}
	checkpoint, err = store.CommitFileWithTail(ctx, checkpoint, nil, []domain.Record{old}, false)
	if err != nil {
		test.Fatal(err)
	}
	tailKey, _ := store.CompletedHistoryKey(ctx, id, 20)
	if tailKey <= key {
		test.Fatal("historical tail was not tracked")
	}
	checkpoint, err = store.CommitFile(ctx, checkpoint, nil, false)
	if err != nil {
		test.Fatal(err)
	}
	removedKey, _ := store.CompletedHistoryKey(ctx, id, 20)
	if removedKey <= tailKey {
		test.Fatal("historical tail removal was not tracked")
	}
	if _, err := store.CommitFile(ctx, checkpoint, []domain.Record{active}, true); err != nil {
		test.Fatal(err)
	}
	if actual, _ := store.CompletedHistoryKey(ctx, id, 20); actual <= removedKey {
		test.Fatal("rewrite removal of old records was not tracked")
	}
}

// TestPruningInvalidatesCompletedHistory covers both retention entry points.
func TestPruningInvalidatesCompletedHistory(test *testing.T) {
	for _, isGlobal := range []bool{false, true} {
		store, _, id := openFixture(test)
		ctx := context.Background()
		checkpoint := domain.Checkpoint{SourceId: id, Path: "expired", ModifiedNs: 10, Reducer: json.RawMessage(`{}`)}
		if _, err := store.CommitFile(ctx, checkpoint, []domain.Record{usageRecord("old")}, true); err != nil {
			test.Fatal(err)
		}
		key, _ := store.CompletedHistoryKey(ctx, id, 1<<62)
		var err error
		if isGlobal {
			err = store.PruneExpiredHistory(ctx, 11)
		} else {
			err = store.PruneHistory(ctx, id, 11)
		}
		if err != nil {
			test.Fatal(err)
		}
		if actual, _ := store.CompletedHistoryKey(ctx, id, 1<<62); actual <= key {
			test.Fatal("pruning retained stale completed fits")
		}
	}
}

func TestEligibleHistoryReadsKeepCompleteFilesAndTails(test *testing.T) {
	store, _, id := openFixture(test)
	ctx := context.Background()
	for _, path := range []string{"eligible", "expired"} {
		modified := int64(200)
		if path == "expired" {
			modified = 99
		}
		checkpoint := domain.Checkpoint{SourceId: id, Path: path, ModifiedNs: modified, Reducer: json.RawMessage(`{}`)}
		if _, err := store.CommitFileWithTail(ctx, checkpoint, []domain.Record{usageRecord("committed")}, []domain.Record{usageRecord("tail")}, true); err != nil {
			test.Fatal(err)
		}
	}
	files, err := store.FileRecordsSince(ctx, id, 100)
	if err != nil || len(files) != 1 || len(files["eligible"]) != 2 || files["eligible"][0].Model != "committed" || files["eligible"][1].Model != "tail" {
		test.Fatal("mtime eligibility or complete-file ordering changed", files, err)
	}
	counts, err := store.FileRecordCounts(ctx, id)
	if err != nil || counts["eligible"] != 2 || counts["expired"] != 2 {
		test.Fatal("metadata lost committed or tail facts", counts, err)
	}
	if _, err := store.database.Exec("UPDATE records SET data='invalid-json' WHERE file_id IN (SELECT id FROM files WHERE path='expired')"); err != nil {
		test.Fatal(err)
	}
	if _, err := store.FileRecords(ctx, id); err == nil {
		test.Fatal("regression fixture did not exercise eager decoding")
	}
	if _, err := store.FileRecordsSince(ctx, id, 100); err != nil {
		test.Fatal("excluded file was decoded before eligibility filtering", err)
	}
}

// TestCodexRangeReadsPreserveOrderAndSkipUnrelatedFacts covers timestamp, bucket and tail filtering.
func TestCodexRangeReadsPreserveOrderAndSkipUnrelatedFacts(test *testing.T) {
	store, _, id := openFixture(test)
	ctx := context.Background()
	records := []domain.Record{}
	for index, model := range []string{"old", "later", "first", "other"} {
		record := usageRecord(model)
		record.TimestampMs = []int64{10, 30, 20, 25}[index]
		record.QuotaBucket = "codex"
		if model == "other" {
			record.QuotaBucket = "codex_bengalfox"
		}
		records = append(records, record)
	}
	tail := usageRecord("tail")
	tail.TimestampMs = 20
	tail.QuotaBucket = "codex"
	checkpoint := domain.Checkpoint{SourceId: id, Path: "eligible", ModifiedNs: 200, Reducer: json.RawMessage(`{}`)}
	if _, err := store.CommitFileWithTail(ctx, checkpoint, records, []domain.Record{tail}, true); err != nil {
		test.Fatal(err)
	}
	if _, err := store.database.Exec("UPDATE records SET data='invalid-json' WHERE timestamp_ms=10"); err != nil {
		test.Fatal(err)
	}
	files, err := store.FileCodexRecordsBetween(ctx, id, 100, 20, 30, true)
	if err != nil || len(files["eligible"]) != 3 {
		test.Fatal("unrelated facts decoded", err)
	}
	for index, want := range []string{"later", "first", "tail"} {
		if files["eligible"][index].Model != want {
			test.Fatal("committed ordinal or tail order changed")
		}
	}
	files, err = store.FileCodexRecordsBetween(ctx, id, 100, 20, 30, false)
	if err != nil || len(files["eligible"]) != 4 {
		test.Fatal("usage summary lost other buckets", err)
	}
}

// TestEmptySourcePruningDoesNotBecomeGlobal preserves source-scoped deletion semantics.
func TestEmptySourcePruningDoesNotBecomeGlobal(test *testing.T) {
	store, _, id := openFixture(test)
	ctx := context.Background()
	checkpoint := domain.Checkpoint{SourceId: id, Path: "retained", ModifiedNs: 1, Reducer: json.RawMessage(`{}`)}
	if _, err := store.CommitFile(ctx, checkpoint, []domain.Record{usageRecord("retained")}, true); err != nil {
		test.Fatal(err)
	}
	if err := store.PruneHistory(ctx, "", 2); err != nil {
		test.Fatal(err)
	}
	records, err := store.Records(ctx, id)
	if err != nil || len(records) != 1 {
		test.Fatal("source-scoped prune deleted unrelated history", err)
	}
}
