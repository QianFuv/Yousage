// Package accounting implements the pinned t3code pricing and aggregation rules.
package accounting

import (
	"encoding/json"
	"sort"
	"strings"
	"yousage/internal/domain"
)

type TokenRates struct{ Input, Output, CacheRead, CacheCreation float64 }
type ModelRate struct {
	TokenRates
	Fast, Ultrafast *TokenRates
}
type RateTable map[string]ModelRate
type CategoryCost struct {
	Input      float64 `json:"input"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
	Output     float64 `json:"output"`
}
type PricedUsage struct {
	CostUsd         float64       `json:"costUsd"`
	CostSource      string        `json:"costSource"`
	CategoryCostUsd *CategoryCost `json:"categoryCostUsd,omitempty"`
	SpeedPremiumUsd float64       `json:"speedPremiumUsd"`
}

// FirstPartyProviders lists models.dev providers that publish their own models' list prices, in priority order.
var FirstPartyProviders = []string{"openai", "anthropic", "google", "xai", "deepseek", "mistral", "moonshotai", "zai", "alibaba", "minimax", "meta", "cohere"}

// ParseModelsDevCatalog reads the models.dev catalog into per-token rates keyed by lowercase bare model ID.
//
// The catalog lists the same model under many providers, often at reseller
// prices. A first-party provider's own list price wins; otherwise the price
// most providers agree on is used, ties going to the alphabetically first
// provider. Missing cache rates fall back to the input rate.
func ParseModelsDevCatalog(data []byte) (RateTable, error) {
	var catalog map[string]struct {
		Models map[string]struct {
			Cost *struct {
				Input      *float64 `json:"input"`
				Output     *float64 `json:"output"`
				CacheRead  *float64 `json:"cache_read"`
				CacheWrite *float64 `json:"cache_write"`
			} `json:"cost"`
		} `json:"models"`
	}
	if err := json.Unmarshal(data, &catalog); err != nil {
		return nil, err
	}
	type listing struct {
		provider string
		rate     ModelRate
	}
	listings := map[string][]listing{}
	providers := make([]string, 0, len(catalog))
	for provider := range catalog {
		providers = append(providers, provider)
	}
	sort.Strings(providers)
	for _, provider := range providers {
		for id, model := range catalog[provider].Models {
			cost := model.Cost
			key := bareName(strings.ToLower(strings.TrimSpace(id)))
			if cost == nil || key == "" || !validPrice(cost.Input) || !validPrice(cost.Output) {
				continue
			}
			read, write := *cost.Input, *cost.Input
			if validPrice(cost.CacheRead) {
				read = *cost.CacheRead
			}
			if validPrice(cost.CacheWrite) {
				write = *cost.CacheWrite
			}
			rate := ModelRate{TokenRates: TokenRates{*cost.Input / 1e6, *cost.Output / 1e6, read / 1e6, write / 1e6}}
			listings[key] = append(listings[key], listing{strings.ToLower(provider), rate})
		}
	}
	priority := map[string]int{}
	for index, provider := range FirstPartyProviders {
		priority[provider] = index + 1
	}
	table := RateTable{}
	for key, candidates := range listings {
		sort.SliceStable(candidates, func(first, second int) bool { return candidates[first].provider < candidates[second].provider })
		best, bestRank := -1, 0
		for index, candidate := range candidates {
			if rank := priority[candidate.provider]; rank > 0 && (best < 0 || rank < bestRank) {
				best, bestRank = index, rank
			}
		}
		if best < 0 {
			bestCount := 0
			for index, candidate := range candidates {
				count := 0
				for _, other := range candidates {
					if sameRate(candidate.rate, other.rate) {
						count++
					}
				}
				if count > bestCount {
					best, bestCount = index, count
				}
			}
		}
		table[key] = candidates[best].rate
	}
	return table, nil
}

// validPrice accepts finite, non-negative catalog prices.
func validPrice(value *float64) bool {
	return value != nil && domain.Finite(*value) && *value >= 0
}

func sameRate(first, second ModelRate) bool {
	equal := func(first, second *TokenRates) bool {
		if first == nil || second == nil {
			return first == second
		}
		return *first == *second
	}
	return first.TokenRates == second.TokenRates && equal(first.Fast, second.Fast) && equal(first.Ultrafast, second.Ultrafast)
}
func bareName(key string) string { index := strings.LastIndex(key, "/"); return key[index+1:] }
func LookupRate(table RateTable, model string) *ModelRate {
	key := strings.ToLower(strings.TrimSpace(model))
	if index := strings.IndexByte(key, '['); index >= 0 {
		key = key[:index]
	}
	switch bareName(key) {
	case "", "<synthetic>", "synthetic", "opus", "sonnet", "haiku", "fable":
		return nil
	}
	rate, exists := table[key]
	if !exists {
		// Gateway-prefixed IDs such as "gateway/gpt-5" price as their bare model.
		rate, exists = table[bareName(key)]
	}
	if !exists {
		return nil
	}
	return &rate
}
func OverrideRates(overrides map[string]domain.PriceOverride) RateTable {
	table := RateTable{}
	for model, price := range overrides {
		read, write := price.InputCostPerMillionTokens, price.InputCostPerMillionTokens
		if price.CacheReadCostPerMillionTokens != nil {
			read = *price.CacheReadCostPerMillionTokens
		}
		if price.CacheWriteCostPerMillionTokens != nil {
			write = *price.CacheWriteCostPerMillionTokens
		}
		table[strings.TrimSpace(model)] = ModelRate{TokenRates: TokenRates{price.InputCostPerMillionTokens / 1e6, price.OutputCostPerMillionTokens / 1e6, read / 1e6, write / 1e6}}
	}
	return table
}
func ratesAt(rate ModelRate, speed string) TokenRates {
	if speed == "fast" && rate.Fast != nil {
		return *rate.Fast
	}
	if speed == "ultrafast" && rate.Ultrafast != nil {
		return *rate.Ultrafast
	}
	return rate.TokenRates
}
func categoryCost(tokens domain.Tokens, rates TokenRates) CategoryCost {
	return CategoryCost{float64(tokens.UncachedInputTokens) * rates.Input, float64(tokens.CachedInputTokens) * rates.CacheRead, float64(tokens.CacheCreationTokens) * rates.CacheCreation, float64(tokens.OutputTokens) * rates.Output}
}
func (cost CategoryCost) total() float64 {
	return cost.Input + cost.CacheRead + cost.CacheWrite + cost.Output
}
func recordRate(table, overrides RateTable, record domain.Record) (*ModelRate, bool) {
	if rate, exists := overrides[strings.TrimSpace(record.Model)]; exists {
		return &rate, true
	}
	model := record.Model
	if record.RateModel != "" {
		model = record.RateModel
	}
	return LookupRate(table, model), false
}

func Price(table, overrides RateTable, record domain.Record) PricedUsage {
	rate, hasOverride := recordRate(table, overrides, record)
	reported := record.ReportedCostUsd
	if hasOverride || (reported != nil && !domain.Finite(*reported)) {
		reported = nil
	}
	if rate == nil {
		if reported != nil {
			return PricedUsage{CostUsd: *reported, CostSource: "providerReported"}
		}
		return PricedUsage{CostSource: "unpriced"}
	}
	category := categoryCost(record.Totals, ratesAt(*rate, record.Speed))
	list := category.total()
	if reported != nil && list <= 0 {
		return PricedUsage{CostUsd: *reported, CostSource: "providerReported"}
	}
	cost, source, scale, premium := list, "modelPriced", 1.0, 0.0
	if record.Speed != "standard" {
		premium = list - categoryCost(record.Totals, rate.TokenRates).total()
	}
	if reported != nil {
		cost, source, scale = *reported, "providerReported", *reported/list
	}
	category.Input *= scale
	category.CacheRead *= scale
	category.CacheWrite *= scale
	category.Output *= scale
	return PricedUsage{cost, source, &category, premium * scale}
}
func CacheSavings(table, overrides RateTable, record domain.Record) float64 {
	rate, _ := recordRate(table, overrides, record)
	if rate == nil {
		return 0
	}
	rates := ratesAt(*rate, record.Speed)
	return float64(record.Totals.CachedInputTokens) * (rates.Input - rates.CacheRead)
}
func ResolveAliases(aliases map[string]string) map[string]string {
	resolved := map[string]string{}
	for model, target := range aliases {
		seen := map[string]bool{model: true}
		for {
			next, exists := aliases[target]
			if !exists {
				resolved[model] = target
				break
			}
			if seen[target] {
				break
			}
			seen[target] = true
			target = next
		}
	}
	return resolved
}
