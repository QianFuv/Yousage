// Package grok reads durably saved Grok Build completed-turn usage.
package grok

import (
	"encoding/json"
	"math"
	"sort"
	"yousage/internal/domain"
)

const CostTicksPerDollar = 1e10

type usage struct {
	tokens    domain.Tokens
	costTicks *float64
}

func object(value any) map[string]any { result, _ := value.(map[string]any); return result }
func text(value any) string           { result, _ := value.(string); return result }
func number(value any) *float64 {
	result, ok := value.(float64)
	if !ok || !domain.Finite(result) {
		return nil
	}
	return &result
}
func integer(value any) int64 {
	result := number(value)
	if result == nil || *result <= 0 || *result >= float64(math.MaxInt64) {
		return 0
	}
	return int64(*result)
}
func readUsage(value any) *usage {
	entry := object(value)
	if entry == nil {
		return nil
	}
	input, cached, creation, output := integer(entry["inputTokens"]), integer(entry["cachedReadTokens"]), integer(entry["cacheCreationTokens"]), integer(entry["outputTokens"])
	return &usage{tokens: domain.Tokens{UncachedInputTokens: max(0, input-cached-creation), CachedInputTokens: cached, CacheCreationTokens: creation, OutputTokens: output, ReasoningTokens: min(output, integer(entry["reasoningTokens"]))}, costTicks: number(entry["costUsdTicks"])}
}
func cost(ticks *float64) *float64 {
	if ticks == nil || *ticks < 0 {
		return nil
	}
	value := *ticks / CostTicksPerDollar
	return &value
}

// Parse does not reconstruct usage from text or unfinished turns; integer cost ticks become USD.
func Parse(data []byte) []domain.Record {
	result := []domain.Record{}
	var envelope map[string]any
	if json.Unmarshal(data, &envelope) != nil {
		return result
	}
	params := object(envelope["params"])
	update := object(params["update"])
	if update["sessionUpdate"] != "turn_completed" {
		return result
	}
	rawUsage := object(update["usage"])
	totals := readUsage(rawUsage)
	if totals == nil {
		return result
	}
	instant := number(object(params["_meta"])["agentTimestampMs"])
	if instant == nil {
		instant = number(envelope["timestamp"])
		if instant != nil && *instant <= 1e12 {
			value := *instant * 1000
			instant = &value
		}
	}
	if instant == nil || *instant >= float64(math.MaxInt64) || *instant <= float64(math.MinInt64) {
		return result
	}
	sessionId := text(params["sessionId"])
	promptId, hasPromptId := update["prompt_id"].(string)
	emit := func(model string, tokens domain.Tokens, reported *float64) {
		record := domain.Record{Provider: domain.Grok, TimestampMs: int64(*instant), Model: model, SessionId: sessionId, Totals: tokens, Speed: "standard", ReportedCostUsd: reported}
		if hasPromptId {
			record.DedupeKey = sessionId + ":" + promptId + ":" + model
		}
		result = append(result, record)
	}
	models := map[string]*usage{}
	for model, value := range object(rawUsage["modelUsage"]) {
		if model == "" {
			continue
		}
		if parsed := readUsage(value); parsed != nil {
			models[model] = parsed
		}
	}
	if len(models) == 0 {
		if totals.tokens.Total() > 0 {
			emit("grok", totals.tokens, cost(totals.costTicks))
		}
		return result
	}
	usedCost, untickedTokens := 0.0, int64(0)
	for _, usage := range models {
		if usage.tokens.Total() == 0 {
			continue
		}
		if usage.costTicks != nil {
			if value := cost(usage.costTicks); value != nil {
				usedCost += *value
			}
		} else {
			untickedTokens += usage.tokens.Total()
		}
	}
	remaining := cost(totals.costTicks)
	if remaining != nil {
		value := max(0, *remaining-usedCost)
		remaining = &value
	}
	names := make([]string, 0, len(models))
	for name := range models {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, model := range names {
		usage := models[model]
		if usage.tokens.Total() == 0 {
			continue
		}
		reported := cost(usage.costTicks)
		if reported == nil && remaining != nil && untickedTokens > 0 {
			value := *remaining * float64(usage.tokens.Total()) / float64(untickedTokens)
			reported = &value
		}
		emit(model, usage.tokens, reported)
	}
	return result
}
