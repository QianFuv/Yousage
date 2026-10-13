package service

import (
	"context"
	"math"
	"testing"
	"time"
	"yousage/internal/accounting"
	"yousage/internal/domain"
)

// TestSpeedAggregation separates billing tiers, aliases models, excludes unmeasured output from rates
// and orders models by total tokens of every request, so unmeasured usage still ranks a model.
func TestSpeedAggregation(test *testing.T) {
	now := time.Date(2026, 10, 9, 14, 5, 30, 0, time.UTC)
	window, _ := accounting.BuildWindow(now, 7, "Asia/Shanghai")
	records := []domain.Record{}
	for index, output := range []int64{10, 40, 90} {
		records = append(records, domain.Record{Provider: domain.Codex, Model: "alias", Speed: "standard", TimestampMs: now.Add(-time.Hour).UnixMilli(), Totals: domain.Tokens{OutputTokens: output, ReasoningTokens: output / 2}, GenerationMs: int64(index+1) * 1000})
	}
	records = append(records,
		domain.Record{Provider: domain.Codex, Model: "model", Speed: "standard", TimestampMs: now.Add(-time.Hour).UnixMilli(), Totals: domain.Tokens{OutputTokens: 999}},
		domain.Record{Provider: domain.Codex, Model: "model", Speed: "fast", TimestampMs: now.Add(-time.Hour).UnixMilli(), Totals: domain.Tokens{OutputTokens: 140}, GenerationMs: 1000, DedupeKey: "same"},
		domain.Record{Provider: domain.Codex, Model: "model", Speed: "fast", TimestampMs: now.Add(-time.Hour).UnixMilli(), Totals: domain.Tokens{OutputTokens: 140}, GenerationMs: 1000, DedupeKey: "same"},
		domain.Record{Provider: domain.Claude, Model: "model", Speed: "ultrafast", TimestampMs: now.Add(-time.Hour).UnixMilli(), Totals: domain.Tokens{OutputTokens: 5}},
		domain.Record{Provider: domain.Codex, Model: "model", Speed: "ultrafast", TimestampMs: now.Add(time.Hour).UnixMilli(), Totals: domain.Tokens{OutputTokens: 500}, GenerationMs: 1000},
		domain.Record{Provider: domain.Claude, TimestampMs: now.Add(-time.Hour).UnixMilli(), ErrorKind: "server"},
		domain.Record{Provider: domain.Codex, TimestampMs: now.Add(-time.Hour).UnixMilli(), IsQuotaOnly: true},
	)
	report := analyzeSpeed(records, window, map[string]string{"alias": "model"}, now)
	if report.MeasuredRequests != 4 || report.UnmeasuredRequests != 2 || len(report.Models) != 3 || len(report.Buckets) != 7 {
		test.Fatal(report)
	}
	standard := report.Models[0]
	if report.Models[1].Speed != "fast" || report.Models[1].TotalTokens != 140 || standard.Model != "model" || standard.Speed != "standard" || standard.Requests != 3 || standard.UnmeasuredRequests != 1 || standard.TotalTokens != 1139 || standard.OutputTokens != 140 || standard.GenerationMs != 6000 || math.Abs(standard.TokensPerSecond-140.0/6) > 1e-12 || standard.MedianTokensPerSecond != 20 || standard.P10TokensPerSecond != 12 || standard.P90TokensPerSecond != 28 {
		test.Fatal(report.Models)
	}
	if report.Models[2].Requests != 0 || report.Models[2].TokensPerSecond != 0 || report.Models[2].MedianTokensPerSecond != 0 {
		test.Fatal(report.Models[2])
	}
	for index, bucket := range report.Buckets {
		if bucket.Label != window.BucketLabels()[index] || bucket.Models == nil {
			test.Fatal(bucket)
		}
		if index < 6 && len(bucket.Models) != 0 {
			test.Fatal(bucket)
		}
	}
	if len(report.Buckets[6].Models) != 2 || report.Buckets[6].Models[1].TokensPerSecond != standard.TokensPerSecond {
		test.Fatal(report.Buckets)
	}
	if len(report.Providers) != 2 || report.Providers[1].Provider != "claude" || report.Providers[1].Requests != 0 || report.Providers[1].UnmeasuredRequests != 1 || report.Providers[1].TokensPerSecond != 0 {
		test.Fatal("providers must include unmeasured-only providers after measured ones", report.Providers)
	}
	codex := report.Providers[0]
	if codex.Provider != "codex" || codex.Requests != 4 || codex.UnmeasuredRequests != 1 || codex.TotalTokens != 1279 || codex.OutputTokens != 280 || codex.GenerationMs != 7000 || codex.TokensPerSecond != 40 || codex.MedianTokensPerSecond != 25 || math.Abs(codex.P10TokensPerSecond-13) > 1e-12 || math.Abs(codex.P90TokensPerSecond-107) > 1e-12 {
		test.Fatal("provider rates must weight every model and tier of the provider", codex)
	}
	if providers := report.Buckets[6].Providers; len(providers) != 1 || providers[0].Provider != "codex" || providers[0].Requests != 4 || providers[0].TokensPerSecond != 40 || report.Buckets[0].Providers == nil {
		test.Fatal("bucket providers must combine measured models", report.Buckets)
	}
}

// TestSpeedWindowBuckets applies exact half-open hourly bounds and local daily assignment across DST.
func TestSpeedWindowBuckets(test *testing.T) {
	now := time.Date(2026, 11, 1, 14, 5, 42, 0, time.UTC)
	for _, days := range []int{1, 7} {
		window, _ := accounting.BuildWindow(now, days, "America/New_York")
		start, _ := time.ParseInLocation(time.DateOnly, window.SinceDay, accounting.Location(window.TimeZone))
		end := now.Add(time.Millisecond)
		if days == 1 {
			start, end = time.UnixMilli(window.SinceTimeMs), time.UnixMilli(window.UntilTimeMs)
		}
		records := []domain.Record{}
		for _, instant := range []time.Time{start.Add(-time.Millisecond), start, start.Add(time.Hour), end.Add(-time.Millisecond), end} {
			records = append(records, domain.Record{Provider: domain.Codex, Model: "model", Speed: "standard", TimestampMs: instant.UnixMilli(), Totals: domain.Tokens{OutputTokens: 10}, GenerationMs: 1000})
		}
		report := analyzeSpeed(records, window, nil, now)
		expectedBuckets := days
		if days == 1 {
			expectedBuckets = 24
		}
		if report.MeasuredRequests != 3 || len(report.Buckets) != expectedBuckets {
			test.Fatal(days, report)
		}
		if days == 1 && (report.Buckets[0].Models[0].Requests != 1 || report.Buckets[1].Models[0].Requests != 1 || report.Buckets[23].Models[0].Requests != 1) {
			test.Fatal(report.Buckets)
		}
		if days == 7 && (report.Buckets[0].Models[0].Requests != 2 || report.Buckets[6].Models[0].Requests != 1) {
			test.Fatal(report.Buckets)
		}
	}
}

// TestSpeedReportLocalHistory verifies persistence, source deduplication and pricing aliases through the service.
func TestSpeedReportLocalHistory(test *testing.T) {
	service := testService(test)
	ctx := context.Background()
	record := domain.Record{Provider: domain.Codex, Model: "alias", Speed: "fast", SessionId: "session", TimestampMs: service.Now().Add(-time.Hour).UnixMilli(), GenerationMs: 2000, Totals: domain.Tokens{OutputTokens: 100}, DedupeKey: "request"}
	seedReportRecords(test, service, []domain.Record{record, record})
	pricing, _ := service.Preferences.Pricing(ctx, service.LocalId)
	pricing.Value.Aliases["alias"] = "model"
	if _, err := service.Preferences.SavePricing(ctx, service.LocalId, pricing); err != nil {
		test.Fatal(err)
	}
	report, err := service.SpeedReport(ctx, 1, "UTC")
	if err != nil || report.MeasuredRequests != 1 || len(report.Models) != 1 || report.Models[0].Model != "model" || report.Models[0].TokensPerSecond != 50 || len(report.Buckets) != 24 {
		test.Fatal(report, err)
	}
	if _, err := service.SpeedReport(ctx, 2, "UTC"); err == nil {
		test.Fatal("invalid period accepted")
	}
}

// TestSpeedLatencyAndContextMetrics separates time to first token from generation, keeps first tokens of
// untimed requests, and averages context and output over every request of each scope.
func TestSpeedLatencyAndContextMetrics(test *testing.T) {
	now := time.Date(2026, 10, 9, 14, 5, 30, 0, time.UTC)
	window, _ := accounting.BuildWindow(now, 7, "UTC")
	at := now.Add(-time.Hour).UnixMilli()
	codex := func(output, uncached, cached, generation, ttft int64) domain.Record {
		return domain.Record{Provider: domain.Codex, Model: "model", Speed: "standard", TimestampMs: at, Totals: domain.Tokens{UncachedInputTokens: uncached, CachedInputTokens: cached, OutputTokens: output}, GenerationMs: generation, TtftMs: ttft}
	}
	records := []domain.Record{
		codex(100, 400, 600, 2000, 500),
		codex(300, 1000, 0, 4000, 1000),
		codex(50, 200, 0, 0, 0),
		codex(40, 800, 0, 0, 700),
		{Provider: domain.Claude, Model: "claude", Speed: "standard", TimestampMs: at, Totals: domain.Tokens{UncachedInputTokens: 1000, OutputTokens: 200}, GenerationMs: 1000},
	}
	report := analyzeSpeed(records, window, nil, now)
	near := func(actual, expected float64) bool { return math.Abs(actual-expected) < 1e-9 }
	model := report.Models[0].SpeedStats
	if report.Models[0].Provider != "codex" || model.Requests != 2 || model.UnmeasuredRequests != 2 || model.TtftRequests != 3 || model.MedianTtftMs != 700 || !near(model.P90TtftMs, 940) {
		test.Fatal("first tokens of untimed requests still count", model)
	}
	if !near(model.DecodeTokensPerSecond, 400000.0/4500) || !near(model.TokensPerSecond, 400000.0/6000) || model.MedianLatencyMs != 3000 || !near(model.P90LatencyMs, 3800) {
		test.Fatal("generation must exclude time to first token", model)
	}
	if model.InputTokens != 3000 || !near(model.CacheHitRate, 0.2) || model.ContextTokensPerRequest != 750 || model.OutputTokensPerRequest != 122.5 {
		test.Fatal("context averages must cover every request", model)
	}
	overall := report.Overall
	if overall.Requests != 3 || overall.TtftRequests != 3 || overall.MedianLatencyMs != 2000 || !near(overall.DecodeTokensPerSecond, model.DecodeTokensPerSecond) || !near(overall.CacheHitRate, 0.15) || overall.ContextTokensPerRequest != 800 || overall.OutputTokensPerRequest != 138 {
		test.Fatal("overall must combine every provider", overall)
	}
	if len(report.Providers) != 2 || report.Providers[1].Provider != "claude" || report.Providers[1].TtftRequests != 0 || report.Providers[1].DecodeTokensPerSecond != 0 {
		test.Fatal("providers without first-token timing stay unmeasured", report.Providers)
	}
}
