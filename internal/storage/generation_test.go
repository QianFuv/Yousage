package storage

import (
	"context"
	"encoding/json"
	"testing"
	"yousage/internal/domain"
)

// TestGenerationPersistsAcrossReopen retains duration in binary records, JSON facts and tails.
func TestGenerationPersistsAcrossReopen(test *testing.T) {
	store, path, id := openFixture(test)
	ctx := context.Background()
	record := usageRecord("measured")
	record.GenerationMs = 1234
	encoded, err := json.Marshal(record)
	if err != nil {
		test.Fatal(err)
	}
	var decoded domain.Record
	if err := json.Unmarshal(encoded, &decoded); err != nil || decoded.GenerationMs != record.GenerationMs {
		test.Fatal(decoded, err)
	}
	tail := record
	tail.GenerationMs = 4321
	checkpoint := domain.Checkpoint{SourceId: id, Path: "timing.jsonl", Reducer: json.RawMessage(`{}`)}
	if _, err := store.CommitFileWithTail(ctx, checkpoint, []domain.Record{record}, []domain.Record{tail}, true); err != nil {
		test.Fatal(err)
	}
	if err := store.Close(); err != nil {
		test.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		test.Fatal(err)
	}
	defer reopened.Close()
	records, err := reopened.Records(ctx, id)
	if err != nil || len(records) != 2 || records[0].GenerationMs != 1234 || records[1].GenerationMs != 4321 {
		test.Fatal(records, err)
	}
}
