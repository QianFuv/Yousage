package service

import (
	"context"
	"math"
	"testing"
	"time"
	"yousage/internal/accounting"
	"yousage/internal/domain"
)

// TestLossRolling24Hours keeps all hourly buckets and excludes exact boundary instants.
func TestLossRolling24Hours(test *testing.T) {
	service := testService(test)
	now := time.Date(2026, 11, 1, 14, 5, 42, 0, time.UTC)
	service.Now = func() time.Time { return now }
	window, _ := accounting.BuildWindow(now, 1, "America/New_York")
	start, end := window.SinceTimeMs, window.UntilTimeMs
	before := domain.Record{Provider: domain.Claude, Model: "fixture", SessionId: "session", TimestampMs: start - 10*time.Minute.Milliseconds(), Speed: "standard", Totals: domain.Tokens{CachedInputTokens: 100000}}
	write := before
	write.TimestampMs, write.Totals = start, domain.Tokens{CacheCreationTokens: 100000}
	records := []domain.Record{before, write}
	for _, instant := range []int64{start - 1, start, start + time.Hour.Milliseconds(), end - 1, end, now.UnixMilli()} {
		records = append(records, domain.Record{Provider: domain.Claude, TimestampMs: instant, ErrorKind: "server"})
	}
	outside := write
	outside.SessionId, outside.TimestampMs = "outside", end
	records = append(records, outside)
	seedReportRecords(test, service, records)
	report, err := service.LossReport(context.Background(), 1, "America/New_York")
	if err != nil || report.Window != window || len(report.Days) != 24 || report.Errors.Events != 3 || report.CacheExpiry.Events != 1 || report.CacheWriteTokens != 100000 {
		test.Fatal(report, err)
	}
	for index, day := range report.Days {
		if day.Day != window.BucketLabels()[index] {
			test.Fatal(day, index)
		}
	}
	if report.Days[0].ErrorCount != 1 || report.Days[0].CacheExpiryCount != 1 || report.Days[1].ErrorCount != 1 || report.Days[23].ErrorCount != 1 || report.Days[2].ErrorCount != 0 {
		test.Fatal(report.Days)
	}
}

func lossFixture() (domain.Record, domain.Record, accounting.AggregateOptions, time.Time) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	before := domain.Record{Provider: domain.Claude, Model: "fixture", SessionId: "session", TimestampMs: now.Add(-20 * time.Minute).UnixMilli(), Speed: "standard", Totals: domain.Tokens{CachedInputTokens: 100000, OutputTokens: 100}}
	after := before
	after.TimestampMs = now.Add(-10 * time.Minute).UnixMilli()
	after.Totals = domain.Tokens{CacheCreationTokens: 100000, OutputTokens: 100}
	window, _ := accounting.BuildWindow(now, 7, "UTC")
	options := accounting.AggregateOptions{Window: window, Rates: accounting.RateTable{"fixture": {TokenRates: accounting.TokenRates{Input: 2e-6, Output: 10e-6, CacheRead: 0.2e-6, CacheCreation: 2.5e-6}}}}
	return before, after, options, now
}

func TestCacheExpiryThresholds(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*domain.Record, *domain.Record)
		events int
	}{
		{"full rewrite", func(*domain.Record, *domain.Record) {}, 1},
		{"TTL boundary", func(before, after *domain.Record) {
			after.TimestampMs = before.TimestampMs + CacheDefaultTtl.Milliseconds()
		}, 0},
		{"TTL exceeded", func(before, after *domain.Record) {
			after.TimestampMs = before.TimestampMs + CacheDefaultTtl.Milliseconds() + 1
		}, 1},
		{"minimum write", func(before, after *domain.Record) {
			before.Totals.CachedInputTokens = CacheRewriteMinimumTokens
			after.Totals.CacheCreationTokens = CacheRewriteMinimumTokens
		}, 0},
		{"write must dominate read", func(before, after *domain.Record) { after.Totals.CachedInputTokens = after.Totals.CacheCreationTokens }, 0},
		{"small addition", func(before, after *domain.Record) { after.Totals.CacheCreationTokens = 79999 }, 0},
		{"fraction boundary", func(before, after *domain.Record) { after.Totals.CacheCreationTokens = 80000 }, 1},
		{"no cached predecessor", func(before, after *domain.Record) { before.Totals.CachedInputTokens = 0 }, 0},
		{"new model", func(before, after *domain.Record) { after.Model = "new" }, 0},
		{"new session", func(before, after *domain.Record) { after.SessionId = "new" }, 0},
		{"unknown session", func(before, after *domain.Record) { before.SessionId = ""; after.SessionId = "" }, 0},
		{"new one hour write cannot extend old cache", func(before, after *domain.Record) { after.CacheTtlMs = CacheLongTtl.Milliseconds() }, 1},
		{"one hour survives", func(before, after *domain.Record) { before.CacheTtlMs = CacheLongTtl.Milliseconds() }, 0},
		{"one hour boundary", func(before, after *domain.Record) {
			before.CacheTtlMs = CacheLongTtl.Milliseconds()
			before.TimestampMs = after.TimestampMs - CacheLongTtl.Milliseconds()
		}, 0},
		{"one hour expires", func(before, after *domain.Record) {
			before.CacheTtlMs = CacheLongTtl.Milliseconds()
			before.TimestampMs = after.TimestampMs - CacheLongTtl.Milliseconds() - 1
		}, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			before, after, options, now := lossFixture()
			test.change(&before, &after)
			report := analyzeLoss([]domain.Record{after, before}, options, now)
			if report.CacheExpiry.Events != test.events {
				t.Fatalf("threshold: %+v", report)
			}
		})
	}
	before, after, options, now := lossFixture()
	report := analyzeLoss([]domain.Record{before, after}, options, now)
	if report.CacheExpiry.Tokens != 100000 || math.Abs(report.CacheExpiry.CostUsd-0.23) > 1e-12 || report.Events[0].Precision != "exact" || report.CacheWriteTokens != 100000 || len(report.Days) != 7 || report.Days[6].CacheExpiryCount != 1 {
		t.Fatal(report)
	}
}

func TestOpenAiCacheCollapseEstimate(t *testing.T) {
	before, after, options, now := lossFixture()
	before.Provider, after.Provider = domain.Codex, domain.Codex
	before.Totals = domain.Tokens{UncachedInputTokens: 20000, CachedInputTokens: 80000}
	after.Totals = domain.Tokens{UncachedInputTokens: 100000}
	report := analyzeLoss([]domain.Record{before, after}, options, now)
	if report.CacheExpiry.Events != 1 || report.CacheExpiry.Tokens != 80000 || report.Events[0].Precision != "estimated" || math.Abs(report.CacheExpiry.CostUsd-0.144) > 1e-12 {
		t.Fatal(report)
	}
	for _, change := range []func(*domain.Record, *domain.Record){
		func(before, after *domain.Record) { after.Totals.CachedInputTokens = 30000 },
		func(before, after *domain.Record) { before.Totals.CachedInputTokens = 10000 },
		func(before, after *domain.Record) { after.Totals.UncachedInputTokens = 30000 },
	} {
		first, second := before, after
		change(&first, &second)
		if report := analyzeLoss([]domain.Record{first, second}, options, now); report.CacheExpiry.Events != 0 {
			t.Fatal(report)
		}
	}
}

func TestErrorFollowUpAndSingleAttribution(t *testing.T) {
	before, after, options, now := lossFixture()
	failure := domain.Record{Provider: before.Provider, SessionId: before.SessionId, Model: before.Model, TimestampMs: after.TimestampMs - time.Minute.Milliseconds(), ErrorKind: "server"}
	second := failure
	second.TimestampMs += 1000
	second.ErrorKind = "rateLimit"
	report := analyzeLoss([]domain.Record{after, second, before, failure}, options, now)
	if report.Errors.Events != 2 || report.Errors.Tokens != 100000 || report.CacheExpiry.Events != 0 || report.Events[0].ErrorKind != "rateLimit" || report.Events[0].FollowUpMs != 59000 || report.Events[1].Tokens != 0 || len(report.ErrorKinds) != 2 || report.Days[6].ErrorCount != 2 {
		t.Fatal(report)
	}
	after.TimestampMs = failure.TimestampMs + ErrorFollowUpLimit.Milliseconds()
	report = analyzeLoss([]domain.Record{before, failure, after}, options, now)
	if report.Errors.Tokens != 100000 || report.Events[0].FollowUpMs != ErrorFollowUpLimit.Milliseconds() {
		t.Fatal("follow-up limit should be inclusive", report)
	}
	after.TimestampMs++
	report = analyzeLoss([]domain.Record{before, failure, after}, options, now)
	if report.Errors.Tokens != 0 || report.Errors.Events != 1 || report.CacheExpiry.Events != 1 {
		t.Fatal("too late retry attributed", report)
	}
	after.SessionId = "different"
	report = analyzeLoss([]domain.Record{before, failure, after}, options, now)
	if report.Errors.Tokens != 0 || report.Errors.Events != 1 || report.Events[0].FollowUpMs != 0 {
		t.Fatal("linked another session", report)
	}
	after.SessionId = before.SessionId
	after.Totals = domain.Tokens{CachedInputTokens: 100000}
	report = analyzeLoss([]domain.Record{before, failure, after}, options, now)
	if report.Errors.Tokens != 0 || report.Errors.Events != 1 {
		t.Fatal("cached retry charged", report)
	}
}

func TestLossWindowContextAndDetailCap(t *testing.T) {
	before, after, options, now := lossFixture()
	firstDay, _ := time.Parse(time.DateOnly, options.Window.SinceDay)
	before.TimestampMs = firstDay.Add(-10 * time.Minute).UnixMilli()
	after.TimestampMs = firstDay.UnixMilli()
	records := []domain.Record{before, after}
	for index := range 205 {
		records = append(records, domain.Record{Provider: domain.Codex, TimestampMs: now.Add(-time.Duration(index) * time.Second).UnixMilli(), ErrorKind: "interrupted"})
	}
	records = append(records, domain.Record{Provider: domain.Codex, TimestampMs: now.Add(time.Hour).UnixMilli(), ErrorKind: "server"})
	report := analyzeLoss(records, options, now)
	if report.CacheExpiry.Events != 1 || report.Errors.Events != 205 || len(report.Events) != LossEventLimit || report.Events[0].AtMs != now.UnixMilli() || report.ErrorKinds[0].Kind != "interrupted" || report.ErrorKinds[0].Count != 205 || report.Days[0].CacheExpiryCount != 1 {
		t.Fatal(report)
	}
}

func TestLossReportLocalFactsAndAliases(t *testing.T) {
	service := testService(t)
	before, after, _, _ := lossFixture()
	before.Model, after.Model = "alias", "alias"
	failure := domain.Record{Provider: domain.Claude, SessionId: before.SessionId, TimestampMs: after.TimestampMs - 1000, ErrorKind: "streamDisconnected"}
	seedReportRecords(t, service, []domain.Record{before, after, failure})
	ctx := context.Background()
	pricing, _ := service.Preferences.Pricing(ctx, service.LocalId)
	pricing.Value.Aliases["alias"] = "fixture"
	read, write := 0.2, 2.5
	pricing.Value.Overrides["fixture"] = domain.PriceOverride{InputCostPerMillionTokens: 2, OutputCostPerMillionTokens: 10, CacheReadCostPerMillionTokens: &read, CacheWriteCostPerMillionTokens: &write}
	if _, err := service.Preferences.SavePricing(ctx, service.LocalId, pricing); err != nil {
		t.Fatal(err)
	}
	report, err := service.LossReport(ctx, 7, "UTC")
	if err != nil || report.Errors.Events != 1 || report.Errors.Tokens != 100000 || math.Abs(report.Errors.CostUsd-0.23) > 1e-12 || report.CacheWriteTokens != 100000 {
		t.Fatal(report, err)
	}
	if _, err := service.LossReport(ctx, 2, "UTC"); err == nil {
		t.Fatal("accepted unsupported loss window")
	}
	empty, err := service.LossReport(ctx, 30, "Asia/Shanghai")
	if err != nil || len(empty.Days) != 30 {
		t.Fatal(empty, err)
	}
}

func TestOneHourTtlSurvivesCachedRead(t *testing.T) {
	before, after, options, now := lossFixture()
	before.CacheTtlMs = CacheLongTtl.Milliseconds()
	middle := before
	middle.CacheTtlMs = 0
	middle.TimestampMs = before.TimestampMs + time.Minute.Milliseconds()
	report := analyzeLoss([]domain.Record{before, middle, after}, options, now)
	if report.CacheExpiry.Events != 0 {
		t.Fatal("one-hour context TTL lost across read", report)
	}
}
