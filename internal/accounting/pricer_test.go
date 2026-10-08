package accounting

import (
	"math"
	"reflect"
	"testing"
	"yousage/internal/domain"
)

// TestMemoizedPricingIsBitIdentical preserves model, speed, override and reported-price precedence.
func TestMemoizedPricingIsBitIdentical(test *testing.T) {
	table := RateTable{"gpt-5.4": {TokenRates: TokenRates{2e-6, 1e-5, 2e-7, 2.5e-6}}, "plain": {TokenRates: TokenRates{1e-6, 1e-5, 2e-7, 3e-6}}}
	overrides := RateTable{"override": {TokenRates: TokenRates{3e-6, 2e-5, 1e-7, 4e-6}}}
	pricer := NewPricer(table, overrides)
	for range 3 {
		for _, model := range []string{"gpt-5.4", "gateway/gpt-5.4[1m]", "override", "plain", "unpriced"} {
			for _, rateModel := range []string{"", "gpt-5.4", "plain", "unknown"} {
				for _, speed := range []string{"standard", "fast", "ultrafast"} {
					for _, reported := range []*float64{nil, new(0.0), new(1.23456789), new(math.NaN())} {
						record := domain.Record{Model: model, RateModel: rateModel, Speed: speed, ReportedCostUsd: reported, Totals: domain.Tokens{UncachedInputTokens: 123456, CachedInputTokens: 54321, CacheCreationTokens: 23456, OutputTokens: 7890}}
						if got, want := pricer.Price(record), Price(table, overrides, record); !reflect.DeepEqual(got, want) {
							test.Fatal("pricing bits changed", got, want)
						}
						if got, want := pricer.CacheSavings(record), CacheSavings(table, overrides, record); math.Float64bits(got) != math.Float64bits(want) {
							test.Fatal("savings bits changed")
						}
						for _, creation := range []bool{false, true} {
							if got, want := pricer.CacheRewriteCost(record, 30000, creation), CacheRewriteCost(table, overrides, record, 30000, creation); math.Float64bits(got) != math.Float64bits(want) {
								test.Fatal("rewrite bits changed")
							}
						}
					}
				}
			}
		}
	}
}
