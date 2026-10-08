package service

import (
	"context"
	"errors"
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

// ErrorFollowUpLimit links only the next request within ten minutes of an error.
const ErrorFollowUpLimit = 10 * time.Minute

// LossEventLimit caps the newest detail rows without limiting report totals.
const LossEventLimit = 200

// LossReport summarizes local cache rewrites and redacted errors.
type LossReport struct {
	Window           accounting.Window `json:"window"`
	CacheExpiry      LossTotals        `json:"cacheExpiry"`
	Errors           LossTotals        `json:"errors"`
	ErrorKinds       []LossKindCount   `json:"errorKinds"`
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

// LossKindCount separates user interruptions from provider failure kinds.
type LossKindCount struct {
	Kind    string  `json:"kind"`
	Count   int     `json:"count"`
	CostUsd float64 `json:"costUsd"`
}

// LossDay includes every calendar day in a report, even when no loss was observed.
type LossDay struct {
	Day              string  `json:"day"`
	CacheExpiryUsd   float64 `json:"cacheExpiryUsd"`
	ErrorUsd         float64 `json:"errorUsd"`
	CacheExpiryCount int     `json:"cacheExpiryCount"`
	ErrorCount       int     `json:"errorCount"`
}

// LossEvent contains only request metadata and derived costs, never diagnostic or transcript text.
type LossEvent struct {
	Kind       string  `json:"kind"`
	ErrorKind  string  `json:"errorKind,omitempty"`
	Provider   string  `json:"provider"`
	Model      string  `json:"model"`
	SessionId  string  `json:"sessionId"`
	AtMs       int64   `json:"atMs"`
	IdleMs     int64   `json:"idleMs"`
	Tokens     int64   `json:"tokens"`
	CostUsd    float64 `json:"costUsd"`
	FollowUpMs int64   `json:"followUpMs"`
	Precision  string  `json:"precision"`
}

// LossReport reads the same local retained and deduplicated facts as Usage, with errors included.
func (service *Service) LossReport(ctx context.Context, days int, timeZone string) (LossReport, error) {
	if days != 7 && days != 30 && days != 90 {
		return LossReport{}, errors.New("unsupported loss period")
	}
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
	_, bySource, err := service.localHistoryRecords(ctx, entries, now.Add(-90*24*time.Hour), now, true)
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

// cacheRewrite recognizes context reuse that collapsed, independently of an idle/error cause.
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

func analyzeLoss(records []domain.Record, options accounting.AggregateOptions, now time.Time) LossReport {
	result := LossReport{Window: options.Window, ErrorKinds: []LossKindCount{}, Days: []LossDay{}, Events: []LossEvent{}}
	location := accounting.Location(options.Window.TimeZone)
	pricer := accounting.NewPricer(options.Rates, options.Overrides)
	dayPositions := map[string]int{}
	for _, label := range options.Window.BucketLabels() {
		dayPositions[label] = len(result.Days)
		result.Days = append(result.Days, LossDay{Day: label})
	}
	inWindow := func(record domain.Record) bool {
		day := time.UnixMilli(record.TimestampMs).In(location).Format(time.DateOnly)
		return day >= options.Window.SinceDay && day <= options.Window.UntilDay && record.TimestampMs <= now.UnixMilli()
	}
	sessions := map[string][]domain.Record{}
	for _, record := range records {
		if record.IsQuotaOnly || record.TimestampMs > now.UnixMilli() {
			continue
		}
		if model, exists := options.Aliases[record.Model]; exists {
			record.Model, record.RateModel = model, ""
		}
		if record.ErrorKind == "" && inWindow(record) {
			result.CacheWriteTokens += record.Totals.CacheCreationTokens
		}
		// Unknown session identities cannot safely share predecessors or follow-ups.
		key := string(record.Provider) + "\x00" + record.SessionId
		if record.SessionId == "" {
			if record.ErrorKind != "" && inWindow(record) {
				result.Events = append(result.Events, LossEvent{Kind: "error", ErrorKind: record.ErrorKind, Provider: string(record.Provider), Model: record.Model, AtMs: record.TimestampMs, Precision: "exact"})
			}
			continue
		}
		sessions[key] = append(sessions[key], record)
	}
	for _, session := range sessions {
		sort.SliceStable(session, func(first, second int) bool { return session[first].TimestampMs < session[second].TimestampMs })
		var previous *domain.Record
		pending := []domain.Record{}
		for _, record := range session {
			if record.ErrorKind != "" {
				pending = append(pending, record)
				continue
			}
			tokens, precision, isRewrite := int64(0), "exact", false
			if previous != nil {
				tokens, precision, isRewrite = cacheRewrite(*previous, record)
			}
			cost := float64(0)
			if isRewrite {
				cost = pricer.CacheRewriteCost(record, tokens, record.Totals.CacheCreationTokens > 0)
			}
			hasLinkedLoss := false
			for index, failure := range pending {
				if !inWindow(failure) {
					continue
				}
				event := LossEvent{Kind: "error", ErrorKind: failure.ErrorKind, Provider: string(failure.Provider), Model: failure.Model, SessionId: failure.SessionId, AtMs: failure.TimestampMs, Precision: "exact"}
				delay := record.TimestampMs - failure.TimestampMs
				if delay >= 0 && delay <= ErrorFollowUpLimit.Milliseconds() {
					event.FollowUpMs = delay
					// Several errors can precede one retry. Attribute its loss once, to the latest error.
					if index == len(pending)-1 && isRewrite {
						event.Tokens, event.CostUsd, event.Precision = tokens, cost, precision
						hasLinkedLoss = true
					}
				}
				result.Events = append(result.Events, event)
			}
			pending = pending[:0]
			if previous != nil && isRewrite && !hasLinkedLoss && inWindow(record) {
				idle := record.TimestampMs - previous.TimestampMs
				if idle > cacheTtl(*previous) {
					result.Events = append(result.Events, LossEvent{Kind: "cacheExpiry", Provider: string(record.Provider), Model: record.Model, SessionId: record.SessionId, AtMs: record.TimestampMs, IdleMs: idle, Tokens: tokens, CostUsd: cost, Precision: precision})
				}
			}
			current := record
			if previous != nil && current.Model == previous.Model && current.CacheTtlMs == 0 && current.Totals.CachedInputTokens > 0 {
				current.CacheTtlMs = previous.CacheTtlMs
			}
			previous = &current
		}
		for _, failure := range pending {
			if inWindow(failure) {
				result.Events = append(result.Events, LossEvent{Kind: "error", ErrorKind: failure.ErrorKind, Provider: string(failure.Provider), Model: failure.Model, SessionId: failure.SessionId, AtMs: failure.TimestampMs, Precision: "exact"})
			}
		}
	}
	kinds := map[string]*LossKindCount{}
	for _, event := range result.Events {
		day := time.UnixMilli(event.AtMs).In(location).Format(time.DateOnly)
		daily := &result.Days[dayPositions[day]]
		totals := &result.CacheExpiry
		if event.Kind == "error" {
			totals = &result.Errors
			daily.ErrorUsd += event.CostUsd
			daily.ErrorCount++
			kind := kinds[event.ErrorKind]
			if kind == nil {
				kind = &LossKindCount{Kind: event.ErrorKind}
				kinds[event.ErrorKind] = kind
			}
			kind.Count++
			kind.CostUsd += event.CostUsd
		} else {
			daily.CacheExpiryUsd += event.CostUsd
			daily.CacheExpiryCount++
		}
		totals.Events++
		totals.Tokens += event.Tokens
		totals.CostUsd += event.CostUsd
	}
	for _, kind := range kinds {
		result.ErrorKinds = append(result.ErrorKinds, *kind)
	}
	sort.Slice(result.ErrorKinds, func(first, second int) bool {
		left, right := result.ErrorKinds[first], result.ErrorKinds[second]
		if left.Count != right.Count {
			return left.Count > right.Count
		}
		return left.Kind < right.Kind
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
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		return left.ErrorKind < right.ErrorKind
	})
	if len(result.Events) > LossEventLimit {
		result.Events = result.Events[:LossEventLimit]
	}
	return result
}
