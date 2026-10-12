package service

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"yousage/internal/accounting"
	"yousage/internal/domain"
	"yousage/internal/history"
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

// lossTranscriptRecords reads synthetic metadata through the production streaming parser.
func lossTranscriptRecords(test *testing.T, provider domain.Provider, name string, lines ...string) []domain.Record {
	test.Helper()
	path := filepath.Join(test.TempDir(), name)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0600); err != nil {
		test.Fatal(err)
	}
	read, err := history.ReadTranscript(context.Background(), path, provider, domain.Checkpoint{})
	if err != nil {
		test.Fatal(err)
	}
	return append(read.Records, read.Tail...)
}

// lossUsageLine creates content-free usage with a large context and a final Claude block.
func lossUsageLine(provider domain.Provider, instant, identity string, cached int64) string {
	line := map[string]any{"timestamp": instant}
	if provider == domain.Claude {
		line["type"], line["sessionId"], line["requestId"] = "assistant", "session", identity
		line["message"] = map[string]any{"id": identity, "model": "fixture", "stop_reason": "end_turn", "usage": map[string]any{"input_tokens": 2, "cache_read_input_tokens": cached, "cache_creation_input_tokens": 100000 - cached, "output_tokens": 10}}
	} else {
		line["type"] = "event_msg"
		line["payload"] = map[string]any{"type": "token_count", "info": map[string]any{"last_token_usage": map[string]any{"input_tokens": 100000, "cached_input_tokens": cached, "output_tokens": 10}}}
	}
	encoded, _ := json.Marshal(line)
	return string(encoded)
}

// TestLossSeparatesTranscriptContexts excludes first writes without losing later idle expiries.
func TestLossSeparatesTranscriptContexts(test *testing.T) {
	for _, provider := range []domain.Provider{domain.Codex, domain.Claude} {
		test.Run(string(provider), func(test *testing.T) {
			_, _, options, now := lossFixture()
			header := []string{}
			if provider == domain.Codex {
				header = []string{`{"type":"session_meta","payload":{"id":"session","session_id":"session","history_mode":"paginated","history_base":{"thread_id":"session","end_ordinal_exclusive":10772,"end_byte_offset":61073059}}}`, `{"type":"turn_context","payload":{"model":"fixture"}}`}
			}
			parent := lossTranscriptRecords(test, provider, "session.jsonl", append(header, lossUsageLine(provider, "2026-10-06T11:00:00Z", "parent", 100000))...)
			childLines := append([]string{}, header...)
			first := lossUsageLine(provider, "2026-10-06T11:10:00Z", "first", 0)
			if provider == domain.Claude {
				first = strings.Replace(first, `"type":"assistant"`, `"agentId":"agent-new","isSidechain":true,"type":"assistant"`, 1)
			}
			childLines = append(childLines, first, lossUsageLine(provider, "2026-10-06T11:11:00Z", "cached", 100000), lossUsageLine(provider, "2026-10-06T11:21:00Z", "expired", 0))
			child := lossTranscriptRecords(test, provider, "session_new.jsonl", childLines...)
			report := analyzeLoss(append(parent, child...), options, now)
			if report.CacheExpiry.Events != 1 || report.Events[0].AtMs != now.Add(-39*time.Minute).UnixMilli() {
				test.Fatalf("new transcript counted as an expiry: %+v", report.CacheExpiry)
			}
		})
	}
}

// TestLossSeparatesCompactedContexts excludes reset writes and retains continuing-context retries.
func TestLossSeparatesCompactedContexts(test *testing.T) {
	for _, provider := range []domain.Provider{domain.Codex, domain.Claude} {
		test.Run(string(provider), func(test *testing.T) {
			_, _, options, now := lossFixture()
			lines := []string{}
			boundary := `{"type":"system","subtype":"compact_boundary","timestamp":"2026-10-06T11:09:00Z","uuid":"compact-new"}`
			if provider == domain.Codex {
				lines = []string{`{"type":"session_meta","payload":{"id":"session"}}`, `{"type":"turn_context","payload":{"model":"fixture"}}`}
				boundary = `{"type":"compacted","timestamp":"2026-10-06T11:09:00Z","payload":{"window_number":2,"window_id":"window-new"}}`
			}
			lines = append(lines, lossUsageLine(provider, "2026-10-06T11:00:00Z", "before", 100000), boundary, lossUsageLine(provider, "2026-10-06T11:10:00Z", "reset", 0), lossUsageLine(provider, "2026-10-06T11:11:00Z", "cached", 100000), lossUsageLine(provider, "2026-10-06T11:21:00Z", "expired", 0))
			records := lossTranscriptRecords(test, provider, "session.jsonl", lines...)
			report := analyzeLoss(records, options, now)
			if report.CacheExpiry.Events != 1 || report.Events[0].AtMs != now.Add(-39*time.Minute).UnixMilli() {
				test.Fatalf("compacted context counted as an expiry: %+v", report.CacheExpiry)
			}
		})
	}
}

// TestLossDoesNotLinkRetriesAcrossContextStart charges a rewrite only to its own context's failure.
func TestLossDoesNotLinkRetriesAcrossContextStart(test *testing.T) {
	_, _, options, now := lossFixture()
	header := []string{`{"type":"session_meta","payload":{"id":"session"}}`, `{"type":"turn_context","payload":{"model":"fixture"}}`}
	failure := `{"type":"event_msg","timestamp":"2026-10-06T11:09:00Z","payload":{"type":"error","message":"server_error"}}`
	parent := lossTranscriptRecords(test, domain.Codex, "parent.jsonl", append(header, lossUsageLine(domain.Codex, "2026-10-06T11:00:00Z", "before", 100000), failure)...)
	child := lossTranscriptRecords(test, domain.Codex, "child.jsonl", append(header,
		lossUsageLine(domain.Codex, "2026-10-06T11:10:00Z", "first", 0),
		lossUsageLine(domain.Codex, "2026-10-06T11:11:00Z", "cached", 100000),
		strings.Replace(failure, "11:09:00", "11:20:00", 1),
		lossUsageLine(domain.Codex, "2026-10-06T11:21:00Z", "retry", 0))...)
	report := analyzeLoss(append(parent, child...), options, now)
	if report.CacheExpiry.Events != 0 || report.Errors.Events != 2 || report.Errors.Tokens != 100000 {
		test.Fatalf("retry loss crossed context start: %+v", report)
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
		records = append(records, domain.Record{Provider: domain.Codex, TimestampMs: now.Add(-time.Duration(index) * time.Second).UnixMilli(), ErrorKind: "rateLimit"})
	}
	records = append(records, domain.Record{Provider: domain.Codex, TimestampMs: now.Add(time.Hour).UnixMilli(), ErrorKind: "server"})
	report := analyzeLoss(records, options, now)
	if report.CacheExpiry.Events != 1 || report.Errors.Events != 205 || len(report.Events) != LossEventLimit || report.Events[0].AtMs != now.UnixMilli() || report.ErrorKinds[0].Kind != "rateLimit" || report.ErrorKinds[0].Count != 205 || report.Days[0].CacheExpiryCount != 1 {
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

// TestLossOmitsClientStops keeps user interruptions out of the report while the cache they idle past still counts.
func TestLossOmitsClientStops(t *testing.T) {
	before, after, options, now := lossFixture()
	interrupted := domain.Record{Provider: before.Provider, Model: before.Model, SessionId: before.SessionId, TimestampMs: after.TimestampMs - time.Minute.Milliseconds(), ErrorKind: "interrupted"}
	aborted := interrupted
	aborted.TimestampMs -= time.Second.Milliseconds()
	aborted.ErrorKind = "aborted"
	report := analyzeLoss([]domain.Record{before, aborted, interrupted, after}, options, now)
	if report.Errors.Events != 0 || len(report.ErrorKinds) != 0 || report.CacheExpiry.Events != 1 || len(report.Events) != 1 || report.Events[0].Kind != "cacheExpiry" {
		t.Fatal("client stops were reported or absorbed the cache expiry", report)
	}
}
