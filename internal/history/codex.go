package history

import (
	"encoding/json"
	"math"
	"yousage/internal/domain"
)

const maxCodexTokenCount = 1<<53 - 1

type codexTokenUsage struct {
	Input     int64 `json:"input_tokens"`
	Cached    int64 `json:"cached_input_tokens"`
	Creation  int64 `json:"cache_write_input_tokens"`
	Output    int64 `json:"output_tokens"`
	Reasoning int64 `json:"reasoning_output_tokens"`
	Total     int64 `json:"total_tokens"`
}

// readCodexTokenUsage defaults omitted counters and validates their inclusive categories.
func readCodexTokenUsage(value any) *codexTokenUsage {
	raw := object(value)
	input, hasInput := raw["input_tokens"].(float64)
	output, hasOutput := raw["output_tokens"].(float64)
	if !hasInput || !hasOutput {
		return nil
	}
	values := [6]int64{}
	for index, key := range []string{"input_tokens", "cached_input_tokens", "cache_write_input_tokens", "output_tokens", "reasoning_output_tokens", "total_tokens"} {
		value, exists := raw[key]
		if !exists {
			value = float64(0)
			if key == "total_tokens" {
				value = input + output
			}
		}
		number, ok := value.(float64)
		if !ok || !domain.Finite(number) || number < 0 || number > maxCodexTokenCount || math.Trunc(number) != number {
			return nil
		}
		values[index] = int64(number)
	}
	usage := codexTokenUsage{values[0], values[1], values[2], values[3], values[4], values[5]}
	if usage.Cached > usage.Input || usage.Creation > usage.Input-usage.Cached || usage.Reasoning > usage.Output || usage.Total < usage.Input || usage.Output > usage.Total-usage.Input {
		return nil
	}
	return &usage
}

// combineCodexUsage validates a cumulative sum or unseen prefix using the same counter invariants.
func combineCodexUsage(previous, last *codexTokenUsage, direction float64) *codexTokenUsage {
	if previous == nil {
		previous = &codexTokenUsage{}
	}
	return readCodexTokenUsage(map[string]any{
		"input_tokens":             float64(previous.Input) + direction*float64(last.Input),
		"cached_input_tokens":      float64(previous.Cached) + direction*float64(last.Cached),
		"cache_write_input_tokens": float64(previous.Creation) + direction*float64(last.Creation),
		"output_tokens":            float64(previous.Output) + direction*float64(last.Output),
		"reasoning_output_tokens":  float64(previous.Reasoning) + direction*float64(last.Reasoning),
		"total_tokens":             float64(previous.Total) + direction*float64(last.Total),
	})
}

// reconcileCodexUsage bills only consistent requests and checkpoints even unbillable snapshots.
func reconcileCodexUsage(info map[string]any, instant int64, state *CodexState) *codexTokenUsage {
	last := readCodexTokenUsage(info["last_token_usage"])
	rawCumulative := info["total_token_usage"]
	hasCumulative := rawCumulative != nil
	cumulative := readCodexTokenUsage(rawCumulative)
	signature := ""
	if last != nil {
		encoded, _ := json.Marshal(last)
		signature = string(encoded)
	}
	if state.SuppressingForkCopies {
		if instant-state.ForkCopyAnchorMs < 1000 {
			clearCodexTiming(state)
			state.ForkCopyAnchorMs = instant
			if cumulative != nil {
				state.LastCumulativeUsage = cumulative
			} else if !hasCumulative && last != nil && signature != state.LastUsageSignature {
				state.LastCumulativeUsage = combineCodexUsage(state.LastCumulativeUsage, last, 1)
			}
			state.LastUsageSignature = signature
			return nil
		}
		state.SuppressingForkCopies = false
		state.LastUsageSignature = ""
	}
	previous := state.LastCumulativeUsage
	if cumulative != nil {
		state.LastCumulativeUsage = cumulative
	}
	if last == nil || (hasCumulative && cumulative == nil) {
		state.MalformedRecords++
		return nil
	}
	if last.Input == 0 && last.Output == 0 {
		state.LastUsageSignature = ""
		return nil
	}
	if cumulative != nil && previous != nil && *cumulative == *previous {
		return nil
	}
	if last.Total != last.Input+last.Output {
		state.MalformedRecords++
		return nil
	}
	if cumulative != nil {
		prefix := combineCodexUsage(cumulative, last, -1)
		isReconciled := prefix != nil && (previous == nil || *prefix == *previous)
		if *cumulative != *last && !isReconciled {
			state.MalformedRecords++
			return nil
		}
	} else {
		if signature == state.LastUsageSignature {
			return nil
		}
		advanced := combineCodexUsage(previous, last, 1)
		if advanced == nil {
			state.MalformedRecords++
			return nil
		}
		state.LastCumulativeUsage = advanced
	}
	state.LastUsageSignature = signature
	return last
}
