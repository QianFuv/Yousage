package service

import (
	"context"
	"math"
	"sort"
	"time"
	"yousage/internal/accounting"
	"yousage/internal/domain"
)

// SpeedReport summarizes inclusive output throughput from local retained request facts.
// OpenCode uses native assistant creation/completion times; Codex uses explicit request
// boundaries and model response timestamps, excluding intervening tool execution; Claude
// runs from the prompt or tool result that started a message to its final content block.
// These elapsed intervals include time to first token. Missing, nonpositive, over-ten-minute
// and implausibly fast intervals are unmeasured; providers without reliable timing stay unmeasured.
type SpeedReport struct {
	Window             accounting.Window `json:"window"`
	Models             []SpeedModel      `json:"models"`
	Buckets            []SpeedBucket     `json:"buckets"`
	MeasuredRequests   int               `json:"measuredRequests"`
	UnmeasuredRequests int               `json:"unmeasuredRequests"`
}

// SpeedModel separates billing tiers and includes request-rate percentiles.
// TotalTokens counts input and output of every included request, measured or not.
type SpeedModel struct {
	Provider              string  `json:"provider"`
	Model                 string  `json:"model"`
	Speed                 string  `json:"speed"`
	Requests              int     `json:"requests"`
	UnmeasuredRequests    int     `json:"unmeasuredRequests"`
	TotalTokens           int64   `json:"totalTokens"`
	OutputTokens          int64   `json:"outputTokens"`
	GenerationMs          int64   `json:"generationMs"`
	TokensPerSecond       float64 `json:"tokensPerSecond"`
	MedianTokensPerSecond float64 `json:"medianTokensPerSecond"`
	P10TokensPerSecond    float64 `json:"p10TokensPerSecond"`
	P90TokensPerSecond    float64 `json:"p90TokensPerSecond"`
}

// SpeedBucket includes every Usage time bucket, including empty ones.
type SpeedBucket struct {
	Label  string             `json:"label"`
	Models []SpeedBucketModel `json:"models"`
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

// speedAccumulator retains measured request rates for interpolated percentiles.
type speedAccumulator struct {
	model SpeedModel
	rates []float64
}

// analyzeSpeed computes weighted aggregate rates and equally weighted request percentiles.
func analyzeSpeed(records []domain.Record, window accounting.Window, aliases map[string]string, now time.Time) SpeedReport {
	result := SpeedReport{Window: window, Models: []SpeedModel{}, Buckets: []SpeedBucket{}}
	positions := map[string]int{}
	buckets := []map[speedKey]*SpeedBucketModel{}
	for _, label := range window.BucketLabels() {
		positions[label] = len(result.Buckets)
		result.Buckets = append(result.Buckets, SpeedBucket{Label: label, Models: []SpeedBucketModel{}})
		buckets = append(buckets, map[speedKey]*SpeedBucketModel{})
	}
	models := map[speedKey]*speedAccumulator{}
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
			held = &speedAccumulator{model: SpeedModel{Provider: key.provider, Model: key.model, Speed: key.speed}}
			models[key] = held
		}
		held.model.TotalTokens += record.Totals.Total()
		duration := domain.ReliableGenerationMs(record.GenerationMs, record.Totals.OutputTokens)
		if duration == 0 {
			result.UnmeasuredRequests++
			held.model.UnmeasuredRequests++
			continue
		}
		result.MeasuredRequests++
		held.model.Requests++
		held.model.OutputTokens += record.Totals.OutputTokens
		held.model.GenerationMs += duration
		held.rates = append(held.rates, float64(record.Totals.OutputTokens)*1000/float64(duration))
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
	for _, held := range models {
		if held.model.GenerationMs > 0 {
			held.model.TokensPerSecond = float64(held.model.OutputTokens) * 1000 / float64(held.model.GenerationMs)
		}
		sort.Float64s(held.rates)
		held.model.MedianTokensPerSecond = speedPercentile(held.rates, 0.5)
		held.model.P10TokensPerSecond = speedPercentile(held.rates, 0.1)
		held.model.P90TokensPerSecond = speedPercentile(held.rates, 0.9)
		result.Models = append(result.Models, held.model)
	}
	sort.Slice(result.Models, func(first, second int) bool {
		left, right := result.Models[first], result.Models[second]
		return speedLess(left.TotalTokens, left.Provider, left.Model, left.Speed, right.TotalTokens, right.Provider, right.Model, right.Speed)
	})
	for index, bucket := range buckets {
		for _, row := range bucket {
			row.TokensPerSecond = float64(row.OutputTokens) * 1000 / float64(row.GenerationMs)
			result.Buckets[index].Models = append(result.Buckets[index].Models, *row)
		}
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
