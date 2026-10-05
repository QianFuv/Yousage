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

// ParseRateTable ignores incomplete models and ambiguous provider-qualified aliases.
func ParseRateTable(data []byte) (RateTable, error) {
	var document map[string]json.RawMessage
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, err
	}
	table := RateTable{}
	keys := make([]string, 0, len(document))
	for key := range document {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, name := range keys {
		var entry map[string]json.RawMessage
		if json.Unmarshal(document[name], &entry) != nil || entry == nil {
			continue
		}
		standard := readRates(entry, "", nil)
		key := strings.ToLower(strings.TrimSpace(name))
		if standard == nil || key == "" {
			continue
		}
		rate := ModelRate{TokenRates: *standard, Fast: readRates(entry, "_priority", standard), Ultrafast: readRates(entry, "_ultrafast", standard)}
		var specific map[string]json.RawMessage
		if json.Unmarshal(entry["provider_specific_entry"], &specific) == nil {
			if multiple := number(specific["fast"]); multiple != nil && *multiple > 0 {
				rate.Fast = &TokenRates{standard.Input * *multiple, standard.Output * *multiple, standard.CacheRead * *multiple, standard.CacheCreation * *multiple}
			}
		}
		table[key] = rate
	}
	candidates := map[string]*ModelRate{}
	for key, rate := range table {
		alias := bareName(key)
		if alias == "" || alias == key {
			continue
		}
		if _, exists := table[alias]; exists {
			continue
		}
		held, exists := candidates[alias]
		if !exists {
			copy := rate
			candidates[alias] = &copy
		} else if held != nil && !sameRate(*held, rate) {
			candidates[alias] = nil
		}
	}
	for alias, rate := range candidates {
		if rate != nil {
			table[alias] = *rate
		}
	}
	return table, nil
}

func number(raw json.RawMessage) *float64 {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var value float64
	if json.Unmarshal(raw, &value) != nil || !domain.Finite(value) {
		return nil
	}
	return &value
}
func readRates(entry map[string]json.RawMessage, suffix string, standard *TokenRates) *TokenRates {
	input, output := number(entry["input_cost_per_token"+suffix]), number(entry["output_cost_per_token"+suffix])
	if input == nil || output == nil {
		return nil
	}
	cache := func(name string, standardValue float64) float64 {
		if value := number(entry[name+suffix]); value != nil {
			return *value
		}
		if standard != nil && standard.Input > 0 {
			return standardValue / standard.Input * *input
		}
		return *input
	}
	var read, creation float64
	if standard != nil {
		read, creation = standard.CacheRead, standard.CacheCreation
	}
	return &TokenRates{*input, *output, cache("cache_read_input_token_cost", read), cache("cache_creation_input_token_cost", creation)}
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
