package service

import (
	"context"
	"math"
	"sort"
	"time"
	"yousage/internal/accounting"
	"yousage/internal/domain"
)

// SpeedReport summarizes output throughput, time to first token, latency and context from local
// retained request facts. Codex uses explicit request boundaries and model response timestamps,
// excluding intervening tool execution; Claude runs from the prompt or tool result that started a
// message to its last content block. These elapsed intervals include time to first token, which
// Codex and Claude measure separately where they record when output began streaming. Missing,
// nonpositive, over-ten-minute and implausibly fast intervals are unmeasured.
type SpeedReport struct {
	Window             accounting.Window `json:"window"`
	Overall            SpeedStats        `json:"overall"`
	Models             []SpeedModel      `json:"models"`
	Providers          []SpeedProvider   `json:"providers"`
	Buckets            []SpeedBucket     `json:"buckets"`
	MeasuredRequests   int               `json:"measuredRequests"`
	UnmeasuredRequests int               `json:"unmeasuredRequests"`
}

// SpeedStats aggregates the requests of one scope: a model and billing tier, a provider or the window.
// Requests and the rates count timed requests only; TotalTokens, the cache hit rate and the per-request
// averages count every included request. The generation rate excludes time to first token and uses only
// requests with both measurements. Percentiles weight every request equally.
type SpeedStats struct {
	Requests                int     `json:"requests"`
	UnmeasuredRequests      int     `json:"unmeasuredRequests"`
	TotalTokens             int64   `json:"totalTokens"`
	OutputTokens            int64   `json:"outputTokens"`
	GenerationMs            int64   `json:"generationMs"`
	TokensPerSecond         float64 `json:"tokensPerSecond"`
	MedianTokensPerSecond   float64 `json:"medianTokensPerSecond"`
	P10TokensPerSecond      float64 `json:"p10TokensPerSecond"`
	P90TokensPerSecond      float64 `json:"p90TokensPerSecond"`
	TtftRequests            int     `json:"ttftRequests"`
	MedianTtftMs            float64 `json:"medianTtftMs"`
	P90TtftMs               float64 `json:"p90TtftMs"`
	DecodeTokensPerSecond   float64 `json:"decodeTokensPerSecond"`
	MedianLatencyMs         float64 `json:"medianLatencyMs"`
	P90LatencyMs            float64 `json:"p90LatencyMs"`
	InputTokens             int64   `json:"inputTokens"`
	CachedInputTokens       int64   `json:"cachedInputTokens"`
	CacheHitRate            float64 `json:"cacheHitRate"`
	ContextTokensPerRequest float64 `json:"contextTokensPerRequest"`
	OutputTokensPerRequest  float64 `json:"outputTokensPerRequest"`
}

// SpeedModel separates billing tiers of one provider's model.
type SpeedModel struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Speed    string `json:"speed"`
	SpeedStats
}

// SpeedProvider aggregates every model and billing tier of one provider, like the provider rows of Usage.
type SpeedProvider struct {
	Provider string `json:"provider"`
	SpeedStats
}

// SpeedBucket includes every Usage time bucket, including empty ones.
type SpeedBucket struct {
	Label     string                `json:"label"`
	Models    []SpeedBucketModel    `json:"models"`
	Providers []SpeedBucketProvider `json:"providers"`
}

// SpeedBucketProvider contains only measured requests of one provider in a bucket.
type SpeedBucketProvider struct {
	Provider        string  `json:"provider"`
	Requests        int     `json:"requests"`
	OutputTokens    int64   `json:"outputTokens"`
	GenerationMs    int64   `json:"generationMs"`
	TokensPerSecond float64 `json:"tokensPerSecond"`
}

// SpeedBucketModel contains only measured requests for a provider/model/billing tier.
type SpeedBucketModel struct {
	Provider        string  `json:"provider"`
	Model           string  `json:"model"`
	Speed           string  `json:"speed"`
	Requests        int     `json:"requests"`
	OutputTokens    int64   `json:"outputTokens"`
	GenerationMs    int64   `json:"generationMs"`
	TokensPerSecond float64 `json:"tokensPerSecond"`
}

// SpeedReport reads local deduplicated history without remote calls or account probes.
func (service *Service) SpeedReport(ctx context.Context, days int, timeZone string) (SpeedReport, error) {
	now := service.Now()
	window, err := accounting.BuildWindow(now, days, timeZone)
	if err != nil {
		return SpeedReport{}, err
	}
	entries, _, err := service.sourceIndex(ctx)
	if err != nil {
		return SpeedReport{}, err
	}
	_, bySource, err := service.localHistoryRecordsBetween(ctx, entries, windowStart(window).Add(-36*time.Hour), windowStart(window).Add(-36*time.Hour), now, false)
	if err != nil {
		return SpeedReport{}, err
	}
	pricing, err := service.Preferences.Pricing(ctx, service.LocalId)
	if err != nil {
		return SpeedReport{}, err
	}
	records := []domain.Record{}
	for _, sourceRecords := range bySource {
		records = append(records, sourceRecords...)
	}
	return analyzeSpeed(records, window, accounting.ResolveAliases(pricing.Value.Aliases), now), nil
}

// speedKey identifies a model at one provider and billing tier.
type speedKey struct{ provider, model, speed string }

// speedAccumulator retains per-request samples of one scope for interpolated percentiles.
type speedAccumulator struct {
	stats                        SpeedStats
	rates, ttfts, latencies      []float64
	decodeOutputTokens, decodeMs int64
	allOutputTokens, allRequests int64
}

// add counts one included request; duration is its reliable generation interval, or zero when untimed.
func (held *speedAccumulator) add(record domain.Record, duration int64) {
	totals := record.Totals
	held.stats.TotalTokens += totals.Total()
	held.stats.InputTokens += totals.UncachedInputTokens + totals.CachedInputTokens + totals.CacheCreationTokens
	held.stats.CachedInputTokens += totals.CachedInputTokens
	held.allOutputTokens += totals.OutputTokens
	held.allRequests++
	ttft := domain.ReliableTtftMs(record.TtftMs, duration)
	if ttft > 0 {
		held.stats.TtftRequests++
		held.ttfts = append(held.ttfts, float64(ttft))
	}
	if duration == 0 {
		held.stats.UnmeasuredRequests++
		return
	}
	held.stats.Requests++
	held.stats.OutputTokens += totals.OutputTokens
	held.stats.GenerationMs += duration
	held.rates = append(held.rates, float64(totals.OutputTokens)*1000/float64(duration))
	held.latencies = append(held.latencies, float64(duration))
	if ttft > 0 && duration > ttft {
		held.decodeOutputTokens += totals.OutputTokens
		held.decodeMs += duration - ttft
	}
}

// finish derives the weighted rates, ratios and request percentiles of an accumulated scope.
func (held *speedAccumulator) finish() SpeedStats {
	stats := held.stats
	if stats.GenerationMs > 0 {
		stats.TokensPerSecond = float64(stats.OutputTokens) * 1000 / float64(stats.GenerationMs)
	}
	if held.decodeMs > 0 {
		stats.DecodeTokensPerSecond = float64(held.decodeOutputTokens) * 1000 / float64(held.decodeMs)
	}
	for _, samples := range [][]float64{held.rates, held.ttfts, held.latencies} {
		sort.Float64s(samples)
	}
	stats.MedianTokensPerSecond = speedPercentile(held.rates, 0.5)
	stats.P10TokensPerSecond = speedPercentile(held.rates, 0.1)
	stats.P90TokensPerSecond = speedPercentile(held.rates, 0.9)
	stats.MedianTtftMs, stats.P90TtftMs = speedPercentile(held.ttfts, 0.5), speedPercentile(held.ttfts, 0.9)
	stats.MedianLatencyMs, stats.P90LatencyMs = speedPercentile(held.latencies, 0.5), speedPercentile(held.latencies, 0.9)
	if stats.InputTokens > 0 {
		stats.CacheHitRate = float64(stats.CachedInputTokens) / float64(stats.InputTokens)
	}
	if held.allRequests > 0 {
		stats.ContextTokensPerRequest = float64(stats.InputTokens) / float64(held.allRequests)
		stats.OutputTokensPerRequest = float64(held.allOutputTokens) / float64(held.allRequests)
	}
	return stats
}

// analyzeSpeed computes weighted aggregate rates and equally weighted request percentiles,
// per model and billing tier and per provider.
func analyzeSpeed(records []domain.Record, window accounting.Window, aliases map[string]string, now time.Time) SpeedReport {
	result := SpeedReport{Window: window, Models: []SpeedModel{}, Providers: []SpeedProvider{}, Buckets: []SpeedBucket{}}
	positions := map[string]int{}
	buckets := []map[speedKey]*SpeedBucketModel{}
	for _, label := range window.BucketLabels() {
		positions[label] = len(result.Buckets)
		result.Buckets = append(result.Buckets, SpeedBucket{Label: label, Models: []SpeedBucketModel{}, Providers: []SpeedBucketProvider{}})
		buckets = append(buckets, map[speedKey]*SpeedBucketModel{})
	}
	models := map[speedKey]*speedAccumulator{}
	providers := map[string]*speedAccumulator{}
	overall := &speedAccumulator{}
	seen := map[string]bool{}
	for _, record := range records {
		if record.IsQuotaOnly || record.ErrorKind != "" {
			continue
		}
		if record.DedupeKey != "" {
			if seen[record.DedupeKey] {
				continue
			}
			seen[record.DedupeKey] = true
		}
		label, isIncluded := reportBucket(window, record.TimestampMs, now)
		if !isIncluded {
			continue
		}
		if model, exists := aliases[record.Model]; exists {
			record.Model = model
		}
		key := speedKey{string(record.Provider), record.Model, record.Speed}
		held := models[key]
		if held == nil {
			held = &speedAccumulator{}
			models[key] = held
		}
		provider := providers[key.provider]
		if provider == nil {
			provider = &speedAccumulator{}
			providers[key.provider] = provider
		}
		duration := domain.ReliableGenerationMs(record.GenerationMs, record.Totals.OutputTokens)
		if duration == 0 {
			result.UnmeasuredRequests++
		} else {
			result.MeasuredRequests++
		}
		for _, row := range []*speedAccumulator{held, provider, overall} {
			row.add(record, duration)
		}
		if duration == 0 {
			continue
		}
		bucket := buckets[positions[label]]
		row := bucket[key]
		if row == nil {
			row = &SpeedBucketModel{Provider: key.provider, Model: key.model, Speed: key.speed}
			bucket[key] = row
		}
		row.Requests++
		row.OutputTokens += record.Totals.OutputTokens
		row.GenerationMs += duration
	}
	result.Overall = overall.finish()
	for key, held := range models {
		result.Models = append(result.Models, SpeedModel{Provider: key.provider, Model: key.model, Speed: key.speed, SpeedStats: held.finish()})
	}
	for provider, held := range providers {
		result.Providers = append(result.Providers, SpeedProvider{Provider: provider, SpeedStats: held.finish()})
	}
	sort.Slice(result.Providers, func(first, second int) bool {
		left, right := result.Providers[first], result.Providers[second]
		return speedLess(left.TotalTokens, left.Provider, "", "", right.TotalTokens, right.Provider, "", "")
	})
	sort.Slice(result.Models, func(first, second int) bool {
		left, right := result.Models[first], result.Models[second]
		return speedLess(left.TotalTokens, left.Provider, left.Model, left.Speed, right.TotalTokens, right.Provider, right.Model, right.Speed)
	})
	for index, bucket := range buckets {
		byProvider := map[string]*SpeedBucketProvider{}
		for _, row := range bucket {
			row.TokensPerSecond = float64(row.OutputTokens) * 1000 / float64(row.GenerationMs)
			result.Buckets[index].Models = append(result.Buckets[index].Models, *row)
			provider := byProvider[row.Provider]
			if provider == nil {
				provider = &SpeedBucketProvider{Provider: row.Provider}
				byProvider[row.Provider] = provider
			}
			provider.Requests += row.Requests
			provider.OutputTokens += row.OutputTokens
			provider.GenerationMs += row.GenerationMs
		}
		for _, provider := range byProvider {
			provider.TokensPerSecond = float64(provider.OutputTokens) * 1000 / float64(provider.GenerationMs)
			result.Buckets[index].Providers = append(result.Buckets[index].Providers, *provider)
		}
		sort.Slice(result.Buckets[index].Providers, func(first, second int) bool {
			return result.Buckets[index].Providers[first].Provider < result.Buckets[index].Providers[second].Provider
		})
		sort.Slice(result.Buckets[index].Models, func(first, second int) bool {
			left, right := result.Buckets[index].Models[first], result.Buckets[index].Models[second]
			return speedLess(left.OutputTokens, left.Provider, left.Model, left.Speed, right.OutputTokens, right.Provider, right.Model, right.Speed)
		})
	}
	return result
}

// speedLess provides deterministic descending-token ordering with lexical tie breakers.
func speedLess(leftTokens int64, leftProvider, leftModel, leftSpeed string, rightTokens int64, rightProvider, rightModel, rightSpeed string) bool {
	if leftTokens != rightTokens {
		return leftTokens > rightTokens
	}
	if leftProvider != rightProvider {
		return leftProvider < rightProvider
	}
	if leftModel != rightModel {
		return leftModel < rightModel
	}
	return leftSpeed < rightSpeed
}

// speedPercentile linearly interpolates adjacent sorted request rates at (count-1)*fraction.
func speedPercentile(rates []float64, fraction float64) float64 {
	if len(rates) == 0 {
		return 0
	}
	position := float64(len(rates)-1) * fraction
	lower, upper := int(math.Floor(position)), int(math.Ceil(position))
	return rates[lower] + (rates[upper]-rates[lower])*(position-float64(lower))
}
