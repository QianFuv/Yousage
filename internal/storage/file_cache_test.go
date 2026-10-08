package storage

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"
	"yousage/internal/domain"
)

// TestDecodedFileCacheMatchesOrderedSql checks every filter against the original SQL read.
func TestDecodedFileCacheMatchesOrderedSql(test *testing.T) {
	store, path, id := openFixture(test)
	ctx := context.Background()
	checkpoint := domain.Checkpoint{SourceId: id, Path: "session", ModifiedNs: 200, Reducer: json.RawMessage(`{}`)}
	records := []domain.Record{}
	for index := range 12 {
		record := usageRecord("model")
		record.TimestampMs = int64(10 + (11-index)*5)
		record.QuotaBucket = "codex"
		if index%3 == 0 {
			record.QuotaBucket = "other"
		}
		if index%4 == 0 {
			record.IsQuotaOnly = true
			record.Totals = domain.Tokens{}
			record.ReportedCostUsd = nil
		}
		if index%2 == 0 {
			record.Quota = &domain.QuotaObservation{Windows: []domain.QuotaWindow{{Id: "primary", UsedPercent: 20}}}
		}
		records = append(records, record)
	}
	checkpoint, err := store.CommitFileWithTail(ctx, checkpoint, records, records[:2], true)
	if err != nil {
		test.Fatal(err)
	}
	compare := func() {
		test.Helper()
		for _, cutoff := range []int64{0, 200, 201} {
			for _, main := range []bool{false, true} {
				for _, bounds := range [][2]int64{{0, 100}, {20, 40}, {40, 20}} {
					query := fileRecordSelect + ` JOIN checkpoints c ON c.file_id=f.id WHERE f.source_id=? AND c.modified_ns>=? AND r.timestamp_ms>=? AND r.timestamp_ms<=?`
					if main {
						query += ` AND b.value IN ('','codex')`
					}
					want, err := store.fileRecords(ctx, query+` ORDER BY f.path,r.is_tail,r.ordinal`, id, cutoff, bounds[0], bounds[1])
					if err != nil {
						test.Fatal(err)
					}
					got, err := store.FileCodexRecordsBetween(ctx, id, cutoff, bounds[0], bounds[1], main)
					if err != nil || !reflect.DeepEqual(got, want) {
						test.Fatalf("cache changed SQL output: got %+v want %+v %v", got, want, err)
					}
				}
			}
			want, err := store.fileRecords(ctx, fileRecordSelect+` JOIN checkpoints c ON c.file_id=f.id WHERE f.source_id=? AND c.modified_ns>=? AND r.quota_only=0 ORDER BY f.path,r.is_tail,r.ordinal`, id, cutoff)
			if err != nil {
				test.Fatal(err)
			}
			got, err := store.FileUsageRecordsSince(ctx, id, cutoff)
			if err != nil || !reflect.DeepEqual(got, want) {
				test.Fatal("quota filter differs", got, want, err)
			}
		}
	}
	compare()
	got, _ := store.FileRecordsSince(ctx, id, 0)
	got["session"][0].Model = "mutated"
	got["session"][0].Quota.Windows[0].UsedPercent = 99
	compare()
	other, err := Open(path)
	if err != nil {
		test.Fatal(err)
	}
	defer other.Close()
	checkpoint, err = other.CommitFileWithTail(ctx, checkpoint, records[2:4], records[4:5], false)
	if err != nil {
		test.Fatal(err)
	}
	compare()
	if _, err := other.CommitFile(ctx, checkpoint, records[5:7], true); err != nil {
		test.Fatal(err)
	}
	compare()
	if err := other.PruneHistory(ctx, id, 201); err != nil {
		test.Fatal(err)
	}
	compare()
}

// TestCachedHistoryTimeBoundaries includes record retention even in a recently modified file.
func TestCachedHistoryTimeBoundaries(test *testing.T) {
	store, _, id := openFixture(test)
	ctx := context.Background()
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	record := usageRecord("retention")
	record.TimestampMs = now.Add(-90*24*time.Hour + time.Second).UnixMilli()
	future := usageRecord("future")
	future.TimestampMs = now.Add(2 * time.Second).UnixMilli()
	checkpoint := domain.Checkpoint{SourceId: id, Path: "session", ModifiedNs: now.UnixNano(), Reducer: json.RawMessage(`{}`)}
	if _, err := store.CommitFile(ctx, checkpoint, []domain.Record{record, future}, true); err != nil {
		test.Fatal(err)
	}
	if _, err := store.FileRecordsSince(ctx, id, 0); err != nil {
		test.Fatal(err)
	}
	if bound := store.CachedHistoryValidUntil([]string{id}, now); !bound.Equal(now.Add(time.Second + time.Millisecond)) {
		test.Fatal("timestamp retention not bounded", bound)
	}
}
