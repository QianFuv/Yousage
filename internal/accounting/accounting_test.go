package accounting

import (
	"math"
	"testing"
	"time"
	"yousage/internal/domain"
)

func fixtureRecord() domain.Record {
	return domain.Record{Provider: domain.Claude, Model: "fixture", SessionId: "session", TimestampMs: time.Date(2026, 10, 1, 2, 0, 0, 0, time.UTC).UnixMilli(), Speed: "standard", Totals: domain.Tokens{UncachedInputTokens: 1000, CachedInputTokens: 2000, CacheCreationTokens: 500, OutputTokens: 100, ReasoningTokens: 40}}
}
func fixtureRates() RateTable {
	return RateTable{"fixture": {TokenRates: TokenRates{0.000002, 0.00001, 0.0000002, 0.0000025}}}
}
func assertNear(t *testing.T, actual, expected float64) {
	t.Helper()
	if math.Abs(actual-expected) > 1e-12 {
		t.Fatalf("got %.12f want %.12f", actual, expected)
	}
}

func TestHandCalculatedPricingAndProvenance(t *testing.T) {
	record := fixtureRecord()
	table := fixtureRates()
	priced := Price(table, nil, record)
	assertNear(t, priced.CostUsd, 0.00465)
	assertNear(t, CacheSavings(table, nil, record), 0.0036)
	if priced.CostSource != "modelPriced" {
		t.Fatal(priced)
	}
	reported := 0.0093
	record.ReportedCostUsd = &reported
	priced = Price(table, nil, record)
	assertNear(t, priced.CategoryCostUsd.Input, 0.004)
	if priced.CostSource != "providerReported" {
		t.Fatal(priced)
	}
	unknown := Price(nil, nil, record)
	if unknown.CategoryCostUsd != nil || unknown.CostUsd != reported {
		t.Fatal(unknown)
	}
	overrides := OverrideRates(map[string]domain.PriceOverride{"fixture": {InputCostPerMillionTokens: 2, OutputCostPerMillionTokens: 10}})
	assertNear(t, Price(table, overrides, record).CostUsd, 0.008)
	zero := OverrideRates(map[string]domain.PriceOverride{"fixture": {}})
	free := Price(table, zero, record)
	if free.CostSource != "modelPriced" || free.CostUsd != 0 {
		t.Fatal("free override confused with unpriced", free)
	}
	record.ReportedCostUsd = nil
	record.Model = "missing"
	if Price(table, nil, record).CostSource != "unpriced" {
		t.Fatal("unknown price guessed")
	}
}

func TestPublicRatesAmbiguityTiersAndExactOverrides(t *testing.T) {
	table, err := ParseRateTable([]byte(`{"a/model":{"input_cost_per_token":1,"output_cost_per_token":2},"b/model":{"input_cost_per_token":2,"output_cost_per_token":2},"a/same":{"input_cost_per_token":1,"output_cost_per_token":2},"b/same":{"input_cost_per_token":1,"output_cost_per_token":2},"tier":{"input_cost_per_token":2e-6,"output_cost_per_token":1e-5,"cache_read_input_token_cost":2e-7,"cache_creation_input_token_cost":2.5e-6,"input_cost_per_token_priority":4e-6,"output_cost_per_token_priority":2e-5},"half":{"input_cost_per_token":1},"synthetic":{"input_cost_per_token":1,"output_cost_per_token":1}}`))
	if err != nil {
		t.Fatal(err)
	}
	if LookupRate(table, "model") != nil || LookupRate(table, "same") == nil || LookupRate(table, "half") != nil || LookupRate(table, "synthetic") != nil {
		t.Fatal("public alias ambiguity or synthetic handling")
	}
	rate := LookupRate(table, " Tier[1m] ")
	assertNear(t, rate.Fast.CacheRead, 4e-7)
	assertNear(t, rate.Fast.CacheCreation, 5e-6)
	record := fixtureRecord()
	record.Model, record.Speed = "tier", "fast"
	priced := Price(table, nil, record)
	assertNear(t, priced.CostUsd, 0.0093)
	assertNear(t, priced.SpeedPremiumUsd, 0.00465)
	overrides := OverrideRates(map[string]domain.PriceOverride{"Tier[1m]": {InputCostPerMillionTokens: 0, OutputCostPerMillionTokens: 0}})
	if Price(table, overrides, record).CostUsd == 0 {
		t.Fatal("case-sensitive override applied to another ID")
	}
	record.Model = "Tier[1m]"
	if Price(table, overrides, record).CostUsd != 0 {
		t.Fatal("exact override ignored")
	}
}

func TestAggregationAliasesDedupSessionsAndExclusiveEnd(t *testing.T) {
	aliases := ResolveAliases(map[string]string{"a": "b", "b": "fixture", "c": "d", "d": "c", "e": "c"})
	if aliases["a"] != "fixture" || len(aliases) != 2 {
		t.Fatal(aliases)
	}
	record := fixtureRecord()
	record.Model, record.RateModel, record.DedupeKey = "a", "wrong", "first"
	second := record
	second.TimestampMs += 24 * time.Hour.Milliseconds()
	second.DedupeKey = "second"
	result, err := Aggregate([]domain.Record{record, record, second}, AggregateOptions{Window: Window{TimeZone: "UTC", SinceDay: "2026-10-01", UntilDay: "2026-10-02"}, Rates: fixtureRates(), Aliases: aliases})
	if err != nil || len(result.Buckets) != 2 || result.DuplicatesDropped != 1 || result.DistinctSessions != 1 {
		t.Fatal(result, err)
	}
	if result.Buckets[0].Model != "fixture" {
		t.Fatal("alias missing")
	}
	assertNear(t, result.Buckets[0].CostUsd, 0.00465)
	window := Window{TimeZone: "UTC", Resolution: "hour", SinceTimeMs: record.TimestampMs, UntilTimeMs: second.TimestampMs}
	result, err = Aggregate([]domain.Record{record, second}, AggregateOptions{Window: window})
	if err != nil || result.OutOfWindow != 1 || len(result.Buckets) != 1 {
		t.Fatal(result, err)
	}
	if len(window.BucketLabels()) != 24 {
		t.Fatal("zero hours missing")
	}
}

func TestCalendarAndRollingWindowsAcrossDst(t *testing.T) {
	for _, instant := range []string{"2026-03-08T10:37:59Z", "2026-11-01T10:37:59Z"} {
		now, err := time.Parse(time.RFC3339, instant)
		if err != nil {
			t.Fatal(err)
		}
		window, err := BuildWindow(now, 1, "America/Los_Angeles")
		if err != nil {
			t.Fatal(err)
		}
		if window.UntilTimeMs-window.SinceTimeMs != 24*time.Hour.Milliseconds() || len(window.BucketLabels()) != 24 || window.UntilTimeMs%60000 != 0 {
			t.Fatal(window)
		}
		daily, err := BuildWindow(now, 7, "America/Los_Angeles")
		if err != nil || len(daily.BucketLabels()) != 7 {
			t.Fatal(daily, err)
		}
	}
	record := fixtureRecord()
	for zone, want := range map[string]string{"UTC": "2026-10-01", "Asia/Shanghai": "2026-10-01", "America/Los_Angeles": "2026-09-30", "unknown": "2026-10-01"} {
		result, err := Aggregate([]domain.Record{record}, AggregateOptions{Window: Window{TimeZone: zone, SinceDay: "2026-09-30", UntilDay: "2026-10-01"}})
		if err != nil || result.Buckets[0].Day != want {
			t.Fatal(zone, result, err)
		}
	}
	if Location("+00:01").String() != "+00:01" {
		t.Fatal("fixed offset lost")
	}
}

func TestPriceFormNumbers(t *testing.T) {
	for _, text := range []string{"0", ".5", "1.", "1e-3", "2E+4"} {
		if _, err := ParsePriceField(text, true); err != nil {
			t.Fatal(text, err)
		}
	}
	for _, text := range []string{"", "+1", "-1", "NaN", "Infinity", "0x10", "1 e3", "1e999"} {
		if _, err := ParsePriceField(text, true); err == nil {
			t.Fatal("invalid form value accepted", text)
		}
	}
	if value, err := ParsePriceField("", false); err != nil || value != nil {
		t.Fatal("blank cache became zero", value, err)
	}
	value, err := ParsePriceField("0", false)
	if err != nil || value == nil || *value != 0 {
		t.Fatal(value, err)
	}
}
