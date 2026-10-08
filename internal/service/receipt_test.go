package service

import (
	"context"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
	"yousage/internal/accounting"
	"yousage/internal/domain"
	"yousage/internal/preferences"
	"yousage/internal/remote"
)

// TestReceiptTrailingWindows prices the exact covered interval and counts local active days.
func TestReceiptTrailingWindows(test *testing.T) {
	for _, days := range []int{1, 7, 30, 90} {
		for _, zone := range []string{"UTC", "Asia/Shanghai", "America/New_York"} {
			service := testService(test)
			ctx := context.Background()
			now := time.Date(2026, 10, 9, 14, 5, 37, 0, time.UTC)
			service.Now = func() time.Time { return now }
			window, _ := accounting.BuildWindow(now, days, zone)
			start, _ := time.ParseInLocation(time.DateOnly, window.SinceDay, accounting.Location(zone))
			end := now
			if days == 1 {
				start, end = time.UnixMilli(window.SinceTimeMs), time.UnixMilli(window.UntilTimeMs)
			}
			records := []domain.Record{}
			for index, instant := range []time.Time{start.Add(-time.Millisecond), start, end.Add(-time.Millisecond), end, now.Add(time.Hour)} {
				cost := float64(index + 1)
				records = append(records, domain.Record{Provider: domain.Codex, Model: "reported", SessionId: "shared", TimestampMs: instant.UnixMilli(), Speed: "standard", ReportedCostUsd: &cost, Totals: domain.Tokens{OutputTokens: 1}})
			}
			seedReportRecords(test, service, records)
			receipt, err := service.Receipt(ctx, days, zone)
			if err != nil || receipt.TotalUsd != 5 || receipt.Tokens != 2 || receipt.Sessions != 1 || receipt.StartsAtMs != start.UnixMilli() || receipt.EndsAtMs != end.UnixMilli() || receipt.Days != days || receipt.Window != window {
				test.Fatal(days, zone, receipt, err)
			}
			activeDays := 1
			if start.In(accounting.Location(zone)).Format(time.DateOnly) != end.Add(-time.Millisecond).In(accounting.Location(zone)).Format(time.DateOnly) {
				activeDays = 2
			}
			if receipt.ActiveDays != activeDays || receipt.FirstAtMs == nil || *receipt.FirstAtMs != start.UnixMilli() || receipt.LastAtMs == nil || *receipt.LastAtMs != end.Add(-time.Millisecond).UnixMilli() {
				test.Fatal(days, zone, receipt)
			}
			expectedNumber := strings.ReplaceAll(window.UntilDay, "-", "") + fmt.Sprintf("-%dD", days)
			if days == 1 {
				expectedNumber = end.In(accounting.Location(zone)).Format("200601021504") + "-24H"
			}
			if receipt.Number != expectedNumber {
				test.Fatal(receipt.Number, expectedNumber)
			}
			covered := window
			covered.SinceTimeMs, covered.UntilTimeMs = start.UnixMilli(), end.UnixMilli()
			aggregate, err := accounting.Aggregate(records, accounting.AggregateOptions{Window: covered})
			total := 0.0
			for _, bucket := range aggregate.Buckets {
				total += bucket.CostUsd
			}
			if err != nil || total != receipt.TotalUsd {
				test.Fatal("Usage pricing differs", total, receipt.TotalUsd, err)
			}
			assertQrFinderPatterns(test, receipt.QrModules)
		}
	}
}

func seedReportRecords(t *testing.T, service *Service, records []domain.Record) {
	t.Helper()
	ctx := context.Background()
	groups := map[domain.Provider][]domain.Record{}
	for _, record := range records {
		groups[record.Provider] = append(groups[record.Provider], record)
	}
	entries := []sourceIndex{}
	for _, provider := range []domain.Provider{domain.Claude, domain.Codex, domain.Cursor} {
		if len(groups[provider]) == 0 {
			continue
		}
		fingerprint := service.fingerprint(provider, t.TempDir())
		if err := service.storeSnapshot(ctx, fingerprint, groups[provider], "ok", "", service.Now()); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, sourceIndex{Source: remote.Source{Fingerprint: fingerprint, Status: "ok"}, IsSnapshot: true})
	}
	if _, err := service.Store.WriteSetting(ctx, "history-index", 0, entries); err != nil {
		t.Fatal(err)
	}
}

func TestReceiptMatchesUsageAndSubscriptionSplit(t *testing.T) {
	service := testService(t)
	ctx := context.Background()
	now := service.Now()
	cost := 3.0
	records := []domain.Record{
		{Provider: domain.Claude, Model: "alias", SessionId: "shared", TimestampMs: now.Add(-time.Hour).UnixMilli(), Speed: "fast", Totals: domain.Tokens{UncachedInputTokens: 50000, CachedInputTokens: 100000, OutputTokens: 1000}},
		{Provider: domain.Claude, Model: "unpriced", SessionId: "shared", TimestampMs: now.Add(-2 * time.Hour).UnixMilli(), Speed: "standard", Totals: domain.Tokens{OutputTokens: 20}},
		{Provider: domain.Codex, Model: "gpt-5.4", SessionId: "shared", TimestampMs: now.Add(-3 * time.Hour).UnixMilli(), Speed: "ultrafast", Totals: domain.Tokens{UncachedInputTokens: 10000, CachedInputTokens: 40000, OutputTokens: 1000}},
		{Provider: domain.Cursor, Model: "reported", SessionId: "cursor", TimestampMs: now.Add(-4 * time.Hour).UnixMilli(), Speed: "standard", ReportedCostUsd: &cost, Totals: domain.Tokens{OutputTokens: 5}},
		{Provider: domain.Claude, Model: "claude-opus-4-8", SessionId: "future", TimestampMs: now.Add(time.Hour).UnixMilli(), Speed: "standard", Totals: domain.Tokens{UncachedInputTokens: 999999}},
		{Provider: domain.Claude, SessionId: "error-only", TimestampMs: now.Add(-time.Minute).UnixMilli(), ErrorKind: "server"},
	}
	seedReportRecords(t, service, records)
	pricing, _ := service.Preferences.Pricing(ctx, service.LocalId)
	read, write := 0.2, 2.5
	pricing.Value = preferences.Pricing{Aliases: map[string]string{"alias": "claude-opus-4-8"}, Overrides: map[string]domain.PriceOverride{"claude-opus-4-8": {InputCostPerMillionTokens: 2, OutputCostPerMillionTokens: 10, CacheReadCostPerMillionTokens: &read, CacheWriteCostPerMillionTokens: &write}}}
	if _, err := service.Preferences.SavePricing(ctx, service.LocalId, pricing); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Store.WriteSetting(ctx, "public-model-rates", 0, RateSnapshot{Rates: accounting.RateTable{"gpt-5.4": {TokenRates: accounting.TokenRates{Input: 2e-6, Output: 10e-6, CacheRead: 0.2e-6}}}}); err != nil {
		t.Fatal(err)
	}
	configuration := Configuration{Providers: []NativeProvider{{Id: "claude", Driver: "claudeAgent", Label: "Claude", IsEnabled: true}}, PrimaryEnvironmentId: service.LocalId}
	if _, err := service.Store.WriteSetting(ctx, "configuration", 0, configuration); err != nil {
		t.Fatal(err)
	}
	provider := remote.Provider{InstanceId: "claude"}
	provider.Auth.Status, provider.Auth.Label = "authenticated", "max"
	if _, err := service.Store.WriteSetting(ctx, "local-quotas", 0, quotaSnapshot{Providers: []remote.Provider{provider}}); err != nil {
		t.Fatal(err)
	}
	receipt, err := service.Receipt(ctx, 7, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	window, _ := accounting.BuildWindow(now, 7, "UTC")
	start, _ := time.ParseInLocation(time.DateOnly, window.SinceDay, time.UTC)
	window.SinceTimeMs, window.UntilTimeMs = start.UnixMilli(), now.UnixMilli()
	summary, err := service.localSummary(ctx, window)
	if err != nil {
		t.Fatal(err)
	}
	total, tokens, savings := 0.0, int64(0), 0.0
	for _, bucket := range summary.Buckets {
		total += bucket.CostUsd
		tokens += bucket.Totals.Total()
		savings += bucket.CacheSavingsUsd
	}
	if receipt.TotalUsd != total || receipt.Tokens != tokens || receipt.CacheSavingsUsd != savings || receipt.Sessions != 3 || receipt.ActiveDays != 1 || receipt.UnpricedRecords != 1 {
		t.Fatalf("receipt disagrees with Usage: %+v", receipt)
	}
	if math.Abs(receipt.SubscriptionUsd-0.26) > 1e-12 || math.Abs(receipt.PaidUsd-(receipt.TotalUsd-0.26)) > 1e-12 {
		t.Fatalf("subscription split: %+v", receipt)
	}
	if receipt.FirstAtMs == nil || *receipt.FirstAtMs != records[3].TimestampMs || receipt.LastAtMs == nil || *receipt.LastAtMs != records[0].TimestampMs {
		t.Fatal(receipt)
	}
	if len(receipt.Items) != 4 || receipt.Items[0].Provider != "cursor" || !receipt.Items[1].IsSubscription {
		t.Fatal(receipt.Items)
	}
	assertQrFinderPatterns(t, receipt.QrModules)
	if receipt.QrText != "YOUSAGE 7D 2026-09-30..2026-10-06\nAPI $3.30 PAID $3.04\n202.03K TOKENS 3 SESSIONS" {
		t.Fatal(receipt.QrText)
	}
}

func assertQrFinderPatterns(t *testing.T, rows []string) {
	t.Helper()
	size := len(rows)
	if size < 21 || (size-21)%4 != 0 {
		t.Fatalf("invalid QR size: %d", size)
	}
	for _, row := range rows {
		if len(row) != size {
			t.Fatal("QR is not square")
		}
		for _, pixel := range row {
			if pixel != '0' && pixel != '1' {
				t.Fatal("invalid module")
			}
		}
	}
	for _, corner := range [][2]int{{0, 0}, {size - 7, 0}, {0, size - 7}} {
		for row := range 7 {
			for column := range 7 {
				dark := row == 0 || row == 6 || column == 0 || column == 6 || (row >= 2 && row <= 4 && column >= 2 && column <= 4)
				if (rows[corner[1]+row][corner[0]+column] == '1') != dark {
					t.Fatal("QR finder pattern missing")
				}
			}
		}
	}
}

// TestEmptyReceiptTrailingWindows keeps an empty receipt renderable for every supported period.
func TestEmptyReceiptTrailingWindows(t *testing.T) {
	service := testService(t)
	for _, days := range []int{1, 7, 30, 90} {
		result, err := service.Receipt(context.Background(), days, "UTC")
		if err != nil || result.Items == nil || len(result.Items) != 0 || result.Tokens != 0 || result.TotalUsd != 0 || result.FirstAtMs != nil || result.LastAtMs != nil || result.ActiveDays != 0 {
			t.Fatal(result, err)
		}
		assertQrFinderPatterns(t, result.QrModules)
	}
	for _, days := range []int{0, 2, 31} {
		if _, err := service.Receipt(context.Background(), days, "UTC"); err == nil {
			t.Fatal("invalid period accepted", days)
		}
	}
}

func TestSubscriptionClassification(t *testing.T) {
	for _, test := range []struct {
		driver, plan, status string
		enabled, expected    bool
	}{{"codex", "plus", "authenticated", true, true}, {"claudeAgent", "max", "authenticated", true, true}, {"codex", "free", "authenticated", true, false}, {"claudeAgent", "api", "authenticated", true, false}, {"cursor", "pro", "authenticated", true, false}, {"codex", "plus", "unknown", true, false}, {"codex", "plus", "authenticated", false, false}, {"claudeAgent", "", "authenticated", true, false}} {
		provider := remote.Provider{Driver: test.driver, IsEnabled: test.enabled}
		provider.Auth.Label, provider.Auth.Status = test.plan, test.status
		got := subscriptionProviders(remote.Config{Providers: []remote.Provider{provider}})
		if (len(got) > 0) != test.expected {
			t.Fatalf("subscription classification: %+v => %+v", test, got)
		}
	}
}
