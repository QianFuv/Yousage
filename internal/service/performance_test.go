package service

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"
	"yousage/internal/accounting"
	"yousage/internal/domain"
	"yousage/internal/preferences"
	"yousage/internal/remote"
	"yousage/internal/storage"
)

// BenchmarkCachedUsageWindow measures a retained 90-day history without network or provider processes.
func BenchmarkCachedUsageWindow(benchmark *testing.B) {
	ctx := context.Background()
	store, err := storage.Open(filepath.Join(benchmark.TempDir(), "state.sqlite"))
	if err != nil {
		benchmark.Fatal(err)
	}
	defer store.Close()
	if _, err = preferences.New(store).SaveSources(ctx, []preferences.Source{}, 0); err != nil {
		benchmark.Fatal(err)
	}
	service, err := New(ctx, store, nil)
	if err != nil {
		benchmark.Fatal(err)
	}
	defer service.Close()
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	service.Now = func() time.Time { return now }
	fingerprint := service.fingerprint(domain.Claude, benchmark.TempDir())
	id, err := store.RegisterSource(ctx, fingerprint)
	if err != nil {
		benchmark.Fatal(err)
	}
	for day := range 90 {
		instant := now.Add(-time.Duration(day) * 24 * time.Hour)
		records := make([]domain.Record, 100)
		for index := range records {
			records[index] = domain.Record{Provider: domain.Claude, TimestampMs: instant.Add(-time.Hour).UnixMilli(), Model: "model", SessionId: fmt.Sprintf("session-%d", day), Speed: "standard", Totals: domain.Tokens{UncachedInputTokens: 10}}
		}
		checkpoint := domain.Checkpoint{SourceId: id, Path: fmt.Sprintf("file-%03d", day), ModifiedNs: instant.UnixNano(), ParserVersion: 1, Reducer: json.RawMessage(`{}`)}
		if _, err := store.CommitFile(ctx, checkpoint, records, true); err != nil {
			benchmark.Fatal(err)
		}
	}
	if _, err := store.WriteSetting(ctx, "history-index", 0, []sourceIndex{{Source: remote.Source{Fingerprint: fingerprint, Status: "ok"}}}); err != nil {
		benchmark.Fatal(err)
	}
	for _, days := range []int{1, 7, 30, 90} {
		benchmark.Run(fmt.Sprintf("%dd", days), func(benchmark *testing.B) {
			window, _ := accounting.BuildWindow(now, days, "UTC")
			benchmark.ReportAllocs()
			benchmark.ResetTimer()
			for range benchmark.N {
				summary, err := service.localSummary(ctx, window)
				if err != nil || len(summary.Buckets) == 0 {
					benchmark.Fatal("missing usage", err)
				}
			}
		})
	}
}
