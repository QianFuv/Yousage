package accounting

import (
	"regexp"
	"strings"
)

// speedMultipliers scales every Standard token rate for a faster service tier; zero means the tier is not offered.
type speedMultipliers struct{ Fast, Ultrafast float64 }

// officialSpeedPricing holds published Fast and Ultrafast prices as multiples of the Standard rate.
//
// OpenAI: API pricing (https://developers.openai.com/api/docs/pricing), whose
// Fast column also scales cached input; Codex uses these API rates for
// service_tier "fast"/"priority" and "ultrafast".
// Anthropic: fast mode (https://platform.claude.com/docs/en/build-with-claude/fast-mode),
// 2x across the full context window with prompt caching multipliers stacking.
// Checked 2026-10-06. Models without an entry are priced at Standard rates.
var officialSpeedPricing = map[string]speedMultipliers{
	"gpt-6-astra":     {Fast: 2, Ultrafast: 6},
	"gpt-6.1-sol":     {Fast: 2},
	"gpt-6-sol":       {Fast: 2},
	"gpt-6-luna":      {Fast: 2},
	"gpt-5.6-sol":     {Fast: 2},
	"gpt-5.6-terra":   {Fast: 2},
	"gpt-5.6-luna":    {Fast: 2},
	"gpt-5.5":         {Fast: 2.5},
	"gpt-5.4":         {Fast: 2},
	"gpt-5.4-mini":    {Fast: 2},
	"gpt-5.3-codex":   {Fast: 2},
	"gpt-5.2":         {Fast: 2},
	"gpt-5.1":         {Fast: 2},
	"gpt-5":           {Fast: 2},
	"gpt-5-mini":      {Fast: 1.8},
	"claude-opus-5-5": {Fast: 2},
	"claude-opus-5":   {Fast: 2},
	"claude-opus-4-8": {Fast: 2},
}

var dateSuffix = regexp.MustCompile(`-\d{8}$`)

// speedModelKey normalizes a model ID to its official pricing name.
func speedModelKey(model string) string {
	key := strings.ToLower(strings.TrimSpace(model))
	if index := strings.IndexByte(key, '['); index >= 0 {
		key = key[:index]
	}
	return dateSuffix.ReplaceAllString(bareName(key), "")
}

// withOfficialSpeeds fills Fast and Ultrafast rates the rate source does not provide.
func withOfficialSpeeds(rate ModelRate, model string) ModelRate {
	multipliers, exists := officialSpeedPricing[speedModelKey(model)]
	if !exists {
		return rate
	}
	scaled := func(factor float64) *TokenRates {
		return &TokenRates{rate.Input * factor, rate.Output * factor, rate.CacheRead * factor, rate.CacheCreation * factor}
	}
	if rate.Fast == nil && multipliers.Fast > 0 {
		rate.Fast = scaled(multipliers.Fast)
	}
	if rate.Ultrafast == nil && multipliers.Ultrafast > 0 {
		rate.Ultrafast = scaled(multipliers.Ultrafast)
	}
	return rate
}
