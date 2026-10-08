package storage

import (
	"context"
	"encoding/json"
	"math"
	"sync"
	"testing"
	"yousage/internal/domain"
)

// TestLatestQuotaCacheSkipsDecode preserves warm reads, future eligibility and append invalidation.
func TestLatestQuotaCacheSkipsDecode(test *testing.T) {
	store, _, id := openFixture(test)
	ctx := context.Background()
	checkpoint := domain.Checkpoint{SourceId: id, Path: "session", Reducer: json.RawMessage(`{}`)}
	record := usageRecord("cached-model")
	record.TimestampMs, record.QuotaBucket = 10, "codex"
	record.Quota = &domain.QuotaObservation{Windows: []domain.QuotaWindow{{Id: "primary", UsedPercent: 10}}}
	future := record
	future.TimestampMs = 30
	checkpoint, err := store.CommitFile(ctx, checkpoint, []domain.Record{record, future}, true)
	if err != nil {
		test.Fatal(err)
	}
	latest, err := store.LatestCodexQuota(ctx, id, 20)
	if err != nil || latest.TimestampMs != 10 {
		test.Fatal("future quota became eligible early", err)
	}
	latest.Quota.Windows[0].UsedPercent = 99
	store.stringValues = sync.Map{}
	for _, until := range []int64{20, 21, 29} {
		warm, err := store.LatestCodexQuota(ctx, id, until)
		if err != nil || warm.TimestampMs != 10 || warm.Quota.Windows[0].UsedPercent != 10 {
			test.Fatal("warm quota changed or caller mutated the cache", err)
		}
	}
	hasDecoded := false
	store.stringValues.Range(func(key, value any) bool { hasDecoded = true; return false })
	if hasDecoded {
		test.Fatal("warm quota read decoded record strings")
	}
	latest, err = store.LatestCodexQuota(ctx, id, 30)
	if err != nil || latest.TimestampMs != 30 {
		test.Fatal("future eligibility did not invalidate the cache", err)
	}
	record.TimestampMs = 25
	if _, err := store.CommitFile(ctx, checkpoint, []domain.Record{record}, false); err != nil {
		test.Fatal(err)
	}
	latest, err = store.LatestCodexQuota(ctx, id, 29)
	if err != nil || latest.TimestampMs != 25 {
		test.Fatal("append or backwards clock reused stale quota", err)
	}
}

// TestHistoryKeysObserveOtherWriters keeps cached revisions coherent across database handles.
func TestHistoryKeysObserveOtherWriters(test *testing.T) {
	store, path, id := openFixture(test)
	ctx := context.Background()
	checkpoint := domain.Checkpoint{SourceId: id, Path: "session", ModifiedNs: 10, Reducer: json.RawMessage(`{}`)}
	checkpoint, err := store.CommitFile(ctx, checkpoint, []domain.Record{usageRecord("first")}, true)
	if err != nil {
		test.Fatal(err)
	}
	first, err := store.HistoryCacheKey(ctx, id)
	if err != nil {
		test.Fatal(err)
	}
	firstMutation, err := store.CompletedHistoryKey(ctx, id, math.MaxInt64)
	if err != nil {
		test.Fatal(err)
	}
	for range 5 {
		warm, err := store.HistoryCacheKey(ctx, id)
		if err != nil || warm != first {
			test.Fatal("unchanged history key changed", err)
		}
	}
	other, err := Open(path)
	if err != nil {
		test.Fatal(err)
	}
	defer other.Close()
	if _, err := other.CommitFile(ctx, checkpoint, []domain.Record{usageRecord("appended")}, false); err != nil {
		test.Fatal(err)
	}
	appended, err := store.HistoryCacheKey(ctx, id)
	if err != nil || appended == first {
		test.Fatal("external append was hidden by the revision cache", err)
	}
	if err := other.PruneHistory(ctx, id, 11); err != nil {
		test.Fatal(err)
	}
	pruned, err := store.HistoryCacheKey(ctx, id)
	if err != nil || pruned != "" {
		test.Fatal("external retention was hidden by the revision cache", err)
	}
	checkpoint.Revision = 0
	if _, err := other.CommitFile(ctx, checkpoint, []domain.Record{usageRecord("recreated")}, true); err != nil {
		test.Fatal(err)
	}
	recreatedMutation, err := store.CompletedHistoryKey(ctx, id, math.MaxInt64)
	if err != nil || recreatedMutation <= firstMutation {
		test.Fatal("recreated checkpoint reused an old mutation identity", err)
	}
}
