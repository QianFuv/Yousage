package storage

import (
	"context"
	"encoding/json"
	"testing"
	"yousage/internal/domain"
)

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
	if _, err := store.database.Exec("UPDATE records SET data='invalid-json' WHERE path='expired'"); err != nil {
		test.Fatal(err)
	}
	if _, err := store.FileRecords(ctx, id); err == nil {
		test.Fatal("regression fixture did not exercise eager decoding")
	}
	if _, err := store.FileRecordsSince(ctx, id, 100); err != nil {
		test.Fatal("excluded file was decoded before eligibility filtering", err)
	}
}
