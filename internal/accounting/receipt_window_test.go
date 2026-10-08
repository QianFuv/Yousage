package accounting

import (
	"reflect"
	"testing"
	"time"
	"yousage/internal/domain"
)

// TestShiftedReceiptWindows checks wall-clock boundaries, DST and midnight compatibility.
func TestShiftedReceiptWindows(test *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	for _, fixture := range []struct {
		period, date, zone, start, end string
		hour                           int
	}{
		{"day", "2026-10-08", "Asia/Shanghai", "2026-10-08T04:00:00+08:00", "2026-10-09T04:00:00+08:00", 4},
		{"month", "2026-10", "Asia/Shanghai", "2026-10-01T04:00:00+08:00", "2026-11-01T04:00:00+08:00", 4},
		{"day", "2026-10-31", "America/New_York", "2026-10-31T04:00:00-04:00", "2026-11-01T04:00:00-05:00", 4},
	} {
		window, err := BuildReceiptWindow(now, fixture.period, fixture.date, fixture.zone, fixture.hour)
		start, _ := time.Parse(time.RFC3339, fixture.start)
		end, _ := time.Parse(time.RFC3339, fixture.end)
		if err != nil || window.SinceTimeMs != start.UnixMilli() || window.UntilTimeMs != end.UnixMilli() {
			test.Fatalf("shifted boundary: %+v %v", window, err)
		}
	}
	now = time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	window, err := BuildReceiptWindow(now, "day", "2026-03-07", "America/New_York", 4)
	if err != nil || window.UntilTimeMs-window.SinceTimeMs != 23*time.Hour.Milliseconds() {
		test.Fatal("spring DST boundary", window, err)
	}
	now = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	midnight, err := BuildReceiptWindow(now, "day", "2026-10-07", "UTC", 0)
	expected := Window{TimeZone: "UTC", SinceDay: "2026-10-07", UntilDay: "2026-10-07", Resolution: "day", SinceTimeMs: time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC).UnixMilli(), UntilTimeMs: time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC).UnixMilli()}
	if err != nil || !reflect.DeepEqual(midnight, expected) {
		test.Fatal("midnight regression", midnight, err)
	}
	for _, hour := range []int{-1, 24} {
		if _, err := BuildReceiptWindow(now, "day", "2026-10-07", "UTC", hour); err == nil {
			test.Fatal("accepted hour", hour)
		}
	}
}

func TestReceiptWindowCalendarBounds(t *testing.T) {
	for _, test := range []struct {
		date  string
		hours int
	}{{"2026-03-08", 23}, {"2026-11-01", 25}} {
		day, _ := time.Parse(time.DateOnly, test.date)
		window, err := BuildReceiptWindow(day.AddDate(0, 0, 2), "day", test.date, "America/New_York", 0)
		if err != nil || window.SinceDay != test.date || window.UntilDay != test.date || window.UntilTimeMs-window.SinceTimeMs != int64(test.hours)*time.Hour.Milliseconds() {
			t.Fatalf("DST calendar day: %+v %v", window, err)
		}
	}
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	month, err := BuildReceiptWindow(now, "month", "2026-07", "UTC", 0)
	if err != nil || month.SinceDay != "2026-07-11" || month.UntilDay != "2026-07-31" {
		t.Fatalf("retention clipping: %+v %v", month, err)
	}
	current, err := BuildReceiptWindow(now, "month", "2026-10", "UTC", 0)
	if err != nil || current.SinceDay != "2026-10-01" || current.UntilDay != "2026-10-08" || current.UntilTimeMs != now.UnixMilli() {
		t.Fatalf("now clipping: %+v %v", current, err)
	}
	for _, test := range []struct{ period, date string }{{"year", "2026"}, {"day", "2026-02-30"}, {"month", "2026-13"}, {"month", "2026-1"}, {"day", "2026-10-09"}, {"month", "2026-06"}} {
		if _, err := BuildReceiptWindow(now, test.period, test.date, "UTC", 0); err == nil {
			t.Fatalf("accepted invalid/outside date: %+v", test)
		}
	}
}

func TestReceiptWindowFiltersExactBounds(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	window, _ := BuildReceiptWindow(now, "day", "2026-10-08", "Asia/Shanghai", 0)
	records := []domain.Record{}
	for _, instant := range []int64{window.SinceTimeMs - 1, window.SinceTimeMs, window.UntilTimeMs - 1, window.UntilTimeMs, window.UntilTimeMs + 1} {
		records = append(records, domain.Record{Provider: domain.Codex, TimestampMs: instant, Model: "fixture", Speed: "standard", Totals: domain.Tokens{OutputTokens: 1}})
	}
	result, err := Aggregate(records, AggregateOptions{Window: window})
	if err != nil || len(result.Buckets) != 1 || result.Buckets[0].Records != 2 {
		t.Fatalf("exact calendar bounds: %+v %v", result, err)
	}
}

func TestCacheRewritePricingUsesOverridesAndSpeed(t *testing.T) {
	record := domain.Record{Provider: domain.Codex, Model: "gpt-5.4", Speed: "fast"}
	read, write := 1.0, 4.0
	overrides := OverrideRates(map[string]domain.PriceOverride{"gpt-5.4": {InputCostPerMillionTokens: 2, OutputCostPerMillionTokens: 5, CacheReadCostPerMillionTokens: &read, CacheWriteCostPerMillionTokens: &write}})
	assertNear(t, CacheRewriteCost(nil, overrides, record, 1000000, false), 2)
	assertNear(t, CacheRewriteCost(nil, overrides, record, 1000000, true), 6)
	assertNear(t, CacheRewriteCost(nil, nil, record, 1000000, true), 0)
}
