package service

import (
	"context"
	"slices"
	"sort"
	"time"
	"yousage/internal/accounting"
	"yousage/internal/domain"
)

// CacheDefaultTtl is Anthropic's default cache lifetime and the conservative minimum idle gap for estimates.
const CacheDefaultTtl = 5 * time.Minute

// CacheLongTtl honors explicitly reported one-hour Claude cache writes.
const CacheLongTtl = time.Hour

// CacheRewriteMinimumTokens excludes small incremental context additions (strictly more than 20,000).
const CacheRewriteMinimumTokens int64 = 20000

// CacheRewriteContextFraction requires a rewrite to cover at least 80% of the preceding cached context.
const CacheRewriteContextFraction = 0.8

// CachePreviousReadFraction requires at least half of the previous OpenAI input to have been cached.
const CachePreviousReadFraction = 0.5

// CacheCollapsedReadFraction defines a cache collapse as no more than 20% cached input.
const CacheCollapsedReadFraction = 0.2

// LossEventLimit caps the newest detail rows without limiting report totals.
const LossEventLimit = 200

// LossReport summarizes local cache-expiry rewrites.
type LossReport struct {
	Window           accounting.Window `json:"window"`
	CacheExpiry      LossTotals        `json:"cacheExpiry"`
	Providers        []LossProvider    `json:"providers"`
	Days             []LossDay         `json:"days"`
	Events           []LossEvent       `json:"events"`
	CacheWriteTokens int64             `json:"cacheWriteTokens"`
}

// LossTotals counts events including zero-cost and unpriced observations.
type LossTotals struct {
	Events  int     `json:"events"`
	Tokens  int64   `json:"tokens"`
	CostUsd float64 `json:"costUsd"`
}

// LossProvider totals one provider's cache-expiry rewrites, like the provider rows of Usage.
type LossProvider struct {
	Provider string  `json:"provider"`
	Events   int     `json:"events"`
	Tokens   int64   `json:"tokens"`
	CostUsd  float64 `json:"costUsd"`
}

// LossDay includes every daily or hourly bucket, even when no loss was observed.
type LossDay struct {
	Day              string            `json:"day"`
	CacheExpiryUsd   float64           `json:"cacheExpiryUsd"`
	CacheExpiryCount int               `json:"cacheExpiryCount"`
	Providers        []LossProviderDay `json:"providers"`
}

// LossProviderDay is one provider's share of a bucket; providers without loss in it are omitted.
type LossProviderDay struct {
	Provider string  `json:"provider"`
	CostUsd  float64 `json:"costUsd"`
	Count    int     `json:"count"`
}

// LossEvent contains only request metadata and derived costs, never diagnostic or transcript text.
type LossEvent struct {
	Kind      string  `json:"kind"`
	Provider  string  `json:"provider"`
	Model     string  `json:"model"`
	SessionId string  `json:"sessionId"`
	AtMs      int64   `json:"atMs"`
	IdleMs    int64   `json:"idleMs"`
	Tokens    int64   `json:"tokens"`
	CostUsd   float64 `json:"costUsd"`
	Precision string  `json:"precision"`
}

// LossReport reads the same local retained and deduplicated usage facts as Usage.
func (service *Service) LossReport(ctx context.Context, days int, timeZone string) (LossReport, error) {
	now := service.Now()
	window, err := accounting.BuildWindow(now, days, timeZone)
	if err != nil {
		return LossReport{}, err
	}
	entries, _, err := service.sourceIndex(ctx)
	if err != nil {
		return LossReport{}, err
	}
	// Retained context before the window is needed to classify its first request.
	_, bySource, err := service.localHistoryRecords(ctx, entries, now.Add(-90*24*time.Hour), now, false)
	if err != nil {
		return LossReport{}, err
	}
	pricing, err := service.Preferences.Pricing(ctx, service.LocalId)
	if err != nil {
		return LossReport{}, err
	}
	rates, err := service.Rates(ctx)
	if err != nil {
		return LossReport{}, err
	}
	records := []domain.Record{}
	for _, sourceRecords := range bySource {
		records = append(records, sourceRecords...)
	}
	return analyzeLoss(records, accounting.AggregateOptions{Window: window, Rates: rates.Rates, Overrides: accounting.OverrideRates(pricing.Value.Overrides), Aliases: accounting.ResolveAliases(pricing.Value.Aliases)}, now), nil
}

// cacheRewrite recognizes context reuse that collapsed, independently of its cause.
func cacheRewrite(previous, current domain.Record) (int64, string, bool) {
	if previous.Model != current.Model || current.SessionId == "" {
		return 0, "exact", false
	}
	before, after := previous.Totals, current.Totals
	previousContext := before.CachedInputTokens + before.CacheCreationTokens
	if current.Provider == domain.Claude || after.CacheCreationTokens > 0 {
		written := after.CacheCreationTokens
		if written <= CacheRewriteMinimumTokens || written <= after.CachedInputTokens || previousContext <= CacheRewriteMinimumTokens || float64(written) < float64(previousContext)*CacheRewriteContextFraction {
			return 0, "exact", false
		}
		return min(written, previousContext), "exact", true
	}
	if current.Provider != domain.Codex {
		return 0, "estimated", false
	}
	beforeInput := before.UncachedInputTokens + before.CachedInputTokens
	afterInput := after.UncachedInputTokens + after.CachedInputTokens
	resent := min(after.UncachedInputTokens, max(0, before.CachedInputTokens-after.CachedInputTokens))
	if resent <= CacheRewriteMinimumTokens || float64(before.CachedInputTokens) < float64(beforeInput)*CachePreviousReadFraction || float64(after.CachedInputTokens) > float64(afterInput)*CacheCollapsedReadFraction || float64(afterInput) < float64(beforeInput)*CacheRewriteContextFraction {
		return 0, "estimated", false
	}
	return resent, "estimated", true
}

func cacheTtl(previous domain.Record) int64 {
	if previous.CacheTtlMs == CacheLongTtl.Milliseconds() {
		return CacheLongTtl.Milliseconds()
	}
	return CacheDefaultTtl.Milliseconds()
}

// analyzeLoss compares requests only within one native transcript context and assigns losses to window buckets.
func analyzeLoss(records []domain.Record, options accounting.AggregateOptions, now time.Time) LossReport {
	result := LossReport{Window: options.Window, Providers: []LossProvider{}, Days: []LossDay{}, Events: []LossEvent{}}
	pricer := accounting.NewPricer(options.Rates, options.Overrides)
	dayPositions := map[string]int{}
	for _, label := range options.Window.BucketLabels() {
		dayPositions[label] = len(result.Days)
		result.Days = append(result.Days, LossDay{Day: label, Providers: []LossProviderDay{}})
	}
	inWindow := func(record domain.Record) bool {
		_, isIncluded := reportBucket(options.Window, record.TimestampMs, now)
		return isIncluded
	}
	sessions := map[string][]domain.Record{}
	for _, record := range records {
		if record.IsQuotaOnly || record.TimestampMs > now.UnixMilli() || record.ErrorKind != "" {
			continue
		}
		if model, exists := options.Aliases[record.Model]; exists {
			record.Model, record.RateModel = model, ""
		}
		if inWindow(record) {
			result.CacheWriteTokens += record.Totals.CacheCreationTokens
		}
		// Unknown session identities cannot safely share predecessors.
		key := string(record.Provider) + "\x00" + record.SessionId + "\x00" + record.ContextId
		if record.SessionId == "" {
			continue
		}
		sessions[key] = append(sessions[key], record)
	}
	for _, session := range sessions {
		sort.SliceStable(session, func(first, second int) bool { return session[first].TimestampMs < session[second].TimestampMs })
		var previous *domain.Record
		for _, record := range session {
			if previous != nil && inWindow(record) {
				idle := record.TimestampMs - previous.TimestampMs
				if tokens, precision, isRewrite := cacheRewrite(*previous, record); isRewrite && idle > cacheTtl(*previous) {
					cost := pricer.CacheRewriteCost(record, tokens, record.Totals.CacheCreationTokens > 0)
					result.Events = append(result.Events, LossEvent{Kind: "cacheExpiry", Provider: string(record.Provider), Model: record.Model, SessionId: record.SessionId, AtMs: record.TimestampMs, IdleMs: idle, Tokens: tokens, CostUsd: cost, Precision: precision})
				}
			}
			current := record
			if previous != nil && current.Model == previous.Model && current.CacheTtlMs == 0 && current.Totals.CachedInputTokens > 0 {
				current.CacheTtlMs = previous.CacheTtlMs
			}
			previous = &current
		}
	}
	providers := map[string]*LossProvider{}
	for _, event := range result.Events {
		day, _ := reportBucket(options.Window, event.AtMs, now)
		daily := &result.Days[dayPositions[day]]
		daily.CacheExpiryUsd += event.CostUsd
		daily.CacheExpiryCount++
		result.CacheExpiry.Events++
		result.CacheExpiry.Tokens += event.Tokens
		result.CacheExpiry.CostUsd += event.CostUsd
		provider := providers[event.Provider]
		if provider == nil {
			provider = &LossProvider{Provider: event.Provider}
			providers[event.Provider] = provider
		}
		provider.Events++
		provider.Tokens += event.Tokens
		provider.CostUsd += event.CostUsd
		position := sort.Search(len(daily.Providers), func(index int) bool { return daily.Providers[index].Provider >= event.Provider })
		if position == len(daily.Providers) || daily.Providers[position].Provider != event.Provider {
			daily.Providers = slices.Insert(daily.Providers, position, LossProviderDay{Provider: event.Provider})
		}
		daily.Providers[position].CostUsd += event.CostUsd
		daily.Providers[position].Count++
	}
	for _, provider := range providers {
		result.Providers = append(result.Providers, *provider)
	}
	sort.Slice(result.Providers, func(first, second int) bool {
		left, right := result.Providers[first], result.Providers[second]
		if left.CostUsd != right.CostUsd {
			return left.CostUsd > right.CostUsd
		}
		return left.Provider < right.Provider
	})
	sort.Slice(result.Events, func(first, second int) bool {
		left, right := result.Events[first], result.Events[second]
		if left.AtMs != right.AtMs {
			return left.AtMs > right.AtMs
		}
		if left.Provider != right.Provider {
			return left.Provider < right.Provider
		}
		if left.SessionId != right.SessionId {
			return left.SessionId < right.SessionId
		}
		if left.Model != right.Model {
			return left.Model < right.Model
		}
		if left.IdleMs != right.IdleMs {
			return left.IdleMs > right.IdleMs
		}
		return left.Tokens > right.Tokens
	})
	if len(result.Events) > LossEventLimit {
		result.Events = result.Events[:LossEventLimit]
	}
	return result
}
