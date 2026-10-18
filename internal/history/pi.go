package history

import (
	"encoding/json"
	"path/filepath"
	"time"
	"yousage/internal/domain"
)

// PiState checkpoints identities and context boundaries without retaining conversation content.
type PiState struct {
	SessionId    string `json:"sessionId,omitempty"`
	TranscriptId string `json:"transcriptId,omitempty"`
	ContextEpoch int64  `json:"contextEpoch,omitempty"`
	LastEntryId  string `json:"lastEntryId,omitempty"`
	LastModel    string `json:"lastModel,omitempty"`
}

// ParsePi reads native billed entries, including tools and summaries, independently of the active branch.
// Native input and cache counters are disjoint; zero catalog costs require pricing fallback.
// Fork copies retain their original entry identity and therefore share a deduplication key.
func ParsePi(data []byte, state *PiState) *domain.Record {
	var entry map[string]any
	if json.Unmarshal(data, &entry) != nil {
		return nil
	}
	kind, identity := text(entry["type"]), text(entry["id"])
	if kind == "session" {
		if identity != "" {
			state.SessionId = identity
		}
		return nil
	}
	parent, hasParent := entry["parentId"]
	if hasParent && state.LastEntryId != "" && text(parent) != state.LastEntryId {
		state.ContextEpoch++
	}
	if kind == "compaction" || kind == "branch_summary" || kind == "model_change" {
		state.ContextEpoch++
	}
	if identity != "" {
		state.LastEntryId = identity
	}
	source := entry
	if kind == "message" {
		source = object(entry["message"])
		if role := text(source["role"]); role != "assistant" && role != "toolResult" {
			return nil
		}
	} else if kind != "usage" && kind != "compaction" && kind != "branch_summary" {
		return nil
	}
	isFailed := source["stopReason"] == "error" || source["stopReason"] == "aborted"
	if isFailed {
		state.ContextEpoch++
	}
	instant, hasTimestamp := timestamp(entry["timestamp"])
	usage := object(source["usage"])
	if !hasTimestamp || identity == "" || usage == nil {
		return nil
	}
	model, rateModel := "Tools/summaries", ""
	backend := text(source["provider"])
	if kind == "usage" || kind == "message" && source["role"] == "assistant" {
		rateModel = text(source["responseModel"])
		if rateModel == "" {
			rateModel = text(source["model"])
		}
		if backend == "" || rateModel == "" {
			return nil
		}
		model = backend + "/" + rateModel
		if state.LastModel != "" && model != state.LastModel {
			state.ContextEpoch++
		}
		state.LastModel = model
	}
	output := integer(usage["output"])
	record := &domain.Record{Provider: domain.Pi, TimestampMs: instant, SessionId: state.SessionId, Model: model, RateModel: rateModel, Speed: "standard", ContextId: transcriptContextId(state.TranscriptId, state.ContextEpoch), Totals: domain.Tokens{UncachedInputTokens: integer(usage["input"]), CachedInputTokens: integer(usage["cacheRead"]), CacheCreationTokens: integer(usage["cacheWrite"]), OutputTokens: output, ReasoningTokens: min(output, integer(usage["reasoning"]))}}
	if backend == "anthropic" {
		record.CacheTtlMs = (5 * time.Minute).Milliseconds()
	}
	if model == "Tools/summaries" || isFailed {
		record.ContextId += "\x00isolated:" + identity
	}
	if isFailed {
		state.ContextEpoch++
	}
	if cost, ok := object(usage["cost"])["total"].(float64); ok && domain.Finite(cost) && cost > 0 {
		record.ReportedCostUsd = &cost
	}
	if record.Totals.Total() == 0 && record.ReportedCostUsd == nil {
		return nil
	}
	key, _ := json.Marshal([]string{"pi", identity, text(entry["timestamp"]), model})
	record.DedupeKey = string(key)
	return record
}

// Directory resolves a source's actual transcript directory without changing existing home semantics.
func (root Root) Directory() string {
	if root.Provider == domain.Pi {
		return root.Home
	}
	if root.Provider == domain.Claude {
		return filepath.Join(root.Home, "projects")
	}
	return filepath.Join(root.Home, "sessions")
}
