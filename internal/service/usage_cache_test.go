package service

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"
	"yousage/internal/accounting"
	"yousage/internal/domain"
	"yousage/internal/preferences"
	"yousage/internal/refresh"
	"yousage/internal/remote"
)

// TestUsageCachesMatchColdRebuilds covers window changes, future facts and incremental repricing.
func TestUsageCachesMatchColdRebuilds(test *testing.T) {
	service := testService(test)
	ctx := context.Background()
	now := service.Now()
	service.Now = func() time.Time { return now }
	records := []domain.Record{}
	for index, offset := range []time.Duration{-80 * 24 * time.Hour, -25 * 24 * time.Hour, -time.Hour, time.Minute} {
		records = append(records, domain.Record{Provider: domain.Codex, Model: "alias", SessionId: "shared", DedupeKey: string(rune('a' + index)), Speed: "fast", TimestampMs: now.Add(offset).UnixMilli(), Totals: domain.Tokens{UncachedInputTokens: 1234567, CachedInputTokens: 12345, OutputTokens: 34}})
	}
	seedReportRecords(test, service, records)
	pricing, _ := service.Preferences.Pricing(ctx, service.LocalId)
	pricing.Value = preferences.Pricing{Aliases: map[string]string{"alias": "gpt-5.4"}, Overrides: map[string]domain.PriceOverride{"gpt-5.4": {InputCostPerMillionTokens: 2.3, OutputCostPerMillionTokens: 7.9}}}
	pricing, err := service.Preferences.SavePricing(ctx, service.LocalId, pricing)
	if err != nil {
		test.Fatal(err)
	}
	compare := func() {
		test.Helper()
		cold, err := New(ctx, service.Store, nil)
		if err != nil {
			test.Fatal(err)
		}
		defer cold.Close()
		cold.Now = service.Now
		for _, days := range []int{30, 90, 1, 7, 30} {
			window, _ := accounting.BuildWindow(now, days, "UTC")
			got, err := service.localSummary(ctx, window)
			if err != nil {
				test.Fatal(err)
			}
			want, err := cold.localSummary(ctx, window)
			if err != nil {
				test.Fatal(err)
			}
			got.ScanDurationMs, want.ScanDurationMs = 0, 0
			if !reflect.DeepEqual(got, want) {
				test.Fatalf("warm/cold %dd differs: %+v %+v", days, got, want)
			}
		}
	}
	compare()
	now = now.Add(2 * time.Minute)
	compare()
	pricing.Value.Overrides["gpt-5.4"] = domain.PriceOverride{InputCostPerMillionTokens: 3.7, OutputCostPerMillionTokens: 9.2}
	if _, err := service.Preferences.SavePricing(ctx, service.LocalId, pricing); err != nil {
		test.Fatal(err)
	}
	compare()
	entries, _, _ := service.sourceIndex(ctx)
	checkpoint, err := service.Store.Checkpoint(ctx, entries[0].Source.Fingerprint.Id(), "complete-snapshot")
	if err != nil {
		test.Fatal(err)
	}
	appendRecord := records[0]
	appendRecord.TimestampMs, appendRecord.DedupeKey = now.Add(-time.Minute).UnixMilli(), "appended"
	if _, err := service.Store.CommitFile(ctx, checkpoint, []domain.Record{appendRecord}, false); err != nil {
		test.Fatal(err)
	}
	compare()
	now = now.Add(-3 * time.Minute)
	compare()
}

// TestUsageServesStoredFactsWhileHistoryRefreshIsRunning prevents joining a slow rescan.
func TestUsageServesStoredFactsWhileHistoryRefreshIsRunning(test *testing.T) {
	service := testService(test)
	ctx := context.Background()
	record := domain.Record{Provider: domain.Codex, Model: "stored", SessionId: "saved", Speed: "standard", TimestampMs: service.Now().Add(-time.Hour).UnixMilli(), Totals: domain.Tokens{OutputTokens: 42}}
	seedReportRecords(test, service, []domain.Record{record})
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		_, err := service.history.Run(ctx, "local-history", refresh.Request{IsManual: true, IsVisible: true, IsUsagePage: true}, func(owner context.Context) (bool, error) {
			close(entered)
			select {
			case <-release:
				return service.scanHistory(owner)
			case <-owner.Done():
				return false, owner.Err()
			}
		})
		finished <- err
	}()
	<-entered
	defer func() {
		close(release)
		if err := <-finished; err != nil {
			test.Error(err)
		}
	}()
	deadline, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	snapshot, err := service.Usage(deadline, 30, "UTC")
	if err != nil || len(snapshot.Environments) == 0 || len(snapshot.Environments[0].Summary.Buckets) != 1 || snapshot.Environments[0].Summary.Buckets[0].Totals.OutputTokens != 42 {
		test.Fatal("stored facts unavailable during rescan", snapshot, err)
	}
}

// TestCycleCoverageRefreshReusesPricedHistory separates scan coverage from record revisions.
func TestCycleCoverageRefreshReusesPricedHistory(test *testing.T) {
	service := testService(test)
	ctx := context.Background()
	now := service.Now()
	service.Now = func() time.Time { return now }
	duration := 300
	fingerprint := service.fingerprint(domain.Codex, test.TempDir())
	id, err := service.Store.RegisterSource(ctx, fingerprint)
	if err != nil {
		test.Fatal(err)
	}
	reset := now.Add(2 * time.Hour)
	record := domain.Record{Provider: domain.Codex, Model: "fixture", SessionId: "session", Speed: "standard", TimestampMs: now.Add(-time.Hour).UnixMilli(), QuotaBucket: "codex", Totals: domain.Tokens{OutputTokens: 100}, Quota: &domain.QuotaObservation{Windows: []domain.QuotaWindow{{Id: "session", UsedPercent: 20, WindowMinutes: duration, ResetsAt: reset.Unix()}}}}
	checkpoint := domain.Checkpoint{SourceId: id, Path: "session", ModifiedNs: now.UnixNano(), Reducer: json.RawMessage(`{}`)}
	if _, err := service.Store.CommitFile(ctx, checkpoint, []domain.Record{record}, true); err != nil {
		test.Fatal(err)
	}
	if _, err := service.Store.WriteSetting(ctx, "history-index", 0, []sourceIndex{{Source: remote.Source{Fingerprint: fingerprint, Status: "ok"}}}); err != nil {
		test.Fatal(err)
	}
	saved, _ := service.Configuration(ctx)
	saved.Value.Providers = []NativeProvider{{Id: "codex", Driver: "codex", IsEnabled: true}}
	if _, err := service.Store.WriteSetting(ctx, "configuration", saved.Revision, saved.Value); err != nil {
		test.Fatal(err)
	}
	success := now.UnixMilli()
	if err := service.Store.Observe(ctx, domain.Observation{SourceId: id, Status: "ok", SucceededAt: &success}); err != nil {
		test.Fatal(err)
	}
	config, err := service.localConfig(ctx)
	if err != nil {
		test.Fatal(err)
	}
	service.localCycleValues(ctx, config)
	history := service.cycleHistories.histories["codex:"]
	before := reflect.ValueOf(history).FieldByName("records").Pointer()
	now = now.Add(time.Second)
	success = now.UnixMilli()
	if err := service.Store.Observe(ctx, domain.Observation{SourceId: id, Status: "ok", SucceededAt: &success}); err != nil {
		test.Fatal(err)
	}
	service.localCycleValues(ctx, config)
	history = service.cycleHistories.histories["codex:"]
	if before == 0 || reflect.ValueOf(history).FieldByName("records").Pointer() != before || !history.CoveredThrough.Equal(now) {
		test.Fatal("no-change scan rebuilt history or lost new coverage", history.CoveredThrough)
	}
}
