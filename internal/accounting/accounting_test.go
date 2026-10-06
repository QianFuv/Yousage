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

func TestModelsDevCatalogPrefersFirstPartyAndConsensus(t *testing.T) {
	table, err := ParseModelsDevCatalog([]byte(`{
		"reseller":{"models":{"gpt-x":{"cost":{"input":5,"output":30}},"vendor/open-model":{"cost":{"input":1,"output":2}},"free-model":{"cost":{"input":0,"output":0}}}},
		"openai":{"models":{"gpt-x":{"cost":{"input":4,"output":20,"cache_read":0.4}}}},
		"gateway-a":{"models":{"open-model":{"cost":{"input":0.5,"output":1}}}},
		"gateway-b":{"models":{"Open-Model":{"cost":{"input":0.5,"output":1,"cache_read":0.05,"cache_write":0.6}}}},
		"gateway-c":{"models":{"open-model":{"cost":{"input":0.5,"output":1}}}},
		"incomplete":{"models":{"half":{"cost":{"input":1}},"unpriced":{},"synthetic":{"cost":{"input":1,"output":1}}}}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	first := LookupRate(table, "gpt-x")
	if first == nil {
		t.Fatal("first-party model missing")
	}
	assertNear(t, first.Input, 4e-6)
	assertNear(t, first.Output, 2e-5)
	assertNear(t, first.CacheRead, 4e-7)
	assertNear(t, first.CacheCreation, 4e-6)
	gateway := LookupRate(table, "AL/gpt-x")
	if gateway == nil || gateway.Input != first.Input {
		t.Fatal("gateway-prefixed ID did not use the bare model price", gateway)
	}
	consensus := LookupRate(table, "open-model")
	if consensus == nil {
		t.Fatal("reseller-only model missing")
	}
	assertNear(t, consensus.Input, 5e-7)
	assertNear(t, consensus.Output, 1e-6)
	if free := LookupRate(table, "free-model"); free == nil || free.Input != 0 {
		t.Fatal("free model must price at zero rather than be unpriced", free)
	}
	if LookupRate(table, "half") != nil || LookupRate(table, "unpriced") != nil || LookupRate(table, "synthetic") != nil || LookupRate(table, "missing") != nil {
		t.Fatal("incomplete, synthetic or unknown model was priced")
	}
}

func TestSpeedTiersAndExactOverrides(t *testing.T) {
	table := RateTable{"tier": {TokenRates: TokenRates{2e-6, 1e-5, 2e-7, 2.5e-6}, Fast: &TokenRates{4e-6, 2e-5, 4e-7, 5e-6}}}
	record := fixtureRecord()
	record.Model, record.Speed = "tier", "fast"
	priced := Price(table, nil, record)
	assertNear(t, priced.CostUsd, 0.0093)
	assertNear(t, priced.SpeedPremiumUsd, 0.00465)
	if LookupRate(table, " Tier[1m] ") == nil {
		t.Fatal("context suffix or case prevented lookup")
	}
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

func TestOfficialSpeedPricingWithoutCatalogSpeedRates(t *testing.T) {
	standard := TokenRates{4e-6, 2e-5, 4e-7, 5e-6}
	table := RateTable{"gpt-5.6-sol": {TokenRates: standard}, "gpt-6-astra": {TokenRates: standard}, "gpt-5.5": {TokenRates: standard}, "claude-opus-4-8-20260115": {TokenRates: standard}, "deepseek-v4-flash": {TokenRates: standard}}
	record := fixtureRecord()
	record.Speed = "standard"
	record.Model = "gpt-5.6-sol"
	base := Price(table, nil, record).CostUsd
	cases := []struct {
		model, speed string
		factor       float64
	}{
		{"gpt-5.6-sol", "fast", 2},
		{"AL/gpt-5.6-sol", "fast", 2},
		{"gpt-5.5", "fast", 2.5},
		{"gpt-6-astra", "ultrafast", 6},
		{"claude-opus-4-8-20260115", "fast", 2},
		{"deepseek-v4-flash", "fast", 1},
		{"gpt-5.6-sol", "ultrafast", 1},
	}
	for _, item := range cases {
		record.Model, record.Speed = item.model, item.speed
		priced := Price(table, nil, record)
		assertNear(t, priced.CostUsd, base*item.factor)
		assertNear(t, priced.SpeedPremiumUsd, base*(item.factor-1))
	}
	record.Model, record.Speed = "gpt-5.6-sol", "fast"
	overrides := OverrideRates(map[string]domain.PriceOverride{"gpt-5.6-sol": {InputCostPerMillionTokens: 1, OutputCostPerMillionTokens: 1}})
	overridden := Price(table, overrides, record)
	record.Speed = "standard"
	assertNear(t, overridden.CostUsd, 2*Price(table, overrides, record).CostUsd)
}
