// Package history collects normalized facts without changing provider histories.
package history

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"time"
	"yousage/internal/domain"
)

type CodexState struct {
	Model                 string `json:"model"`
	Speed                 string `json:"speed"`
	SessionId             string `json:"sessionId"`
	LastUsageSignature    string `json:"lastUsageSignature"`
	SawSessionMeta        bool   `json:"sawSessionMeta"`
	SuppressingForkCopies bool   `json:"suppressingForkCopies"`
	ForkCopyAnchorMs      int64  `json:"forkCopyAnchorMs"`
}

func InitialCodexState() CodexState   { return CodexState{Speed: "standard"} }
func object(value any) map[string]any { result, _ := value.(map[string]any); return result }
func text(value any) string           { result, _ := value.(string); return result }
func integer(value any) int64 {
	number, ok := value.(float64)
	if !ok || !domain.Finite(number) || number <= 0 || number >= float64(math.MaxInt64) {
		return 0
	}
	return int64(number)
}
func timestamp(value any) (int64, bool) {
	input, ok := value.(string)
	if !ok {
		return 0, false
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04Z07:00", time.DateOnly} {
		parsed, err := time.Parse(layout, input)
		if err == nil {
			return parsed.UnixMilli(), true
		}
	}
	for _, layout := range []string{"2006-01-02T15:04:05.999999999", "2006-01-02T15:04", "2006-01-02 15:04:05.999999999", time.RFC1123, time.RFC1123Z, time.RFC822, time.RFC822Z, time.ANSIC} {
		parsed, err := time.ParseInLocation(layout, input, time.Local)
		if err == nil {
			return parsed.UnixMilli(), true
		}
	}
	return 0, false
}

// ParseClaude retains billable usage or a redacted error classification, including explicit cache TTL.
func ParseClaude(data []byte) *domain.Record {
	var record map[string]any
	if json.Unmarshal(data, &record) != nil {
		return nil
	}
	if record["subtype"] == "api_error" || record["isApiErrorMessage"] == true {
		instant, ok := timestamp(record["timestamp"])
		if !ok {
			return nil
		}
		message := object(record["message"])
		return &domain.Record{Provider: domain.Claude, TimestampMs: instant, SessionId: text(record["sessionId"]), Model: text(message["model"]), ErrorKind: classifyError(errorDetail(record["error"]), "other")}
	}
	if record["type"] != "assistant" {
		return nil
	}
	message := object(record["message"])
	usage := object(message["usage"])
	instant, hasTimestamp := timestamp(record["timestamp"])
	model := text(message["model"])
	if usage == nil || !hasTimestamp || model == "" {
		return nil
	}
	result := &domain.Record{Provider: domain.Claude, TimestampMs: instant, Model: model, SessionId: text(record["sessionId"]), Speed: "standard", Totals: domain.Tokens{UncachedInputTokens: integer(usage["input_tokens"]), CachedInputTokens: integer(usage["cache_read_input_tokens"]), CacheCreationTokens: integer(usage["cache_creation_input_tokens"]), OutputTokens: integer(usage["output_tokens"])}}
	creation := object(usage["cache_creation"])
	if integer(creation["ephemeral_1h_input_tokens"]) > 0 {
		result.CacheTtlMs = time.Hour.Milliseconds()
	} else if integer(creation["ephemeral_5m_input_tokens"]) > 0 {
		result.CacheTtlMs = (5 * time.Minute).Milliseconds()
	}
	messageId, hasMessageId := message["id"].(string)
	requestId, hasRequestId := record["requestId"].(string)
	if hasMessageId || hasRequestId {
		result.DedupeKey = messageId + ":" + requestId
	}
	if cost, ok := record["costUSD"].(float64); ok && domain.Finite(cost) && cost >= 0 {
		result.ReportedCostUsd = &cost
	}
	if usage["speed"] == "fast" {
		result.Speed = "fast"
	}
	return result
}

// ParseCodex retains quota observations independently of billable usage.
func ParseCodex(data []byte, state *CodexState) (result *domain.Record) {
	var record map[string]any
	if json.Unmarshal(data, &record) != nil {
		return nil
	}
	payload := object(record["payload"])
	if payload == nil {
		if record["type"] != "turn_aborted" {
			return nil
		}
		payload = record
	}
	if record["type"] == "session_meta" {
		if state.SawSessionMeta {
			return nil
		}
		state.SawSessionMeta = true
		id := payload["id"]
		if id == nil {
			id = payload["session_id"]
		}
		if value, ok := id.(string); ok {
			state.SessionId = value
		}
		_, hasFork := payload["forked_from_id"].(string)
		if !hasFork {
			source := object(payload["source"])
			subagent := object(source["subagent"])
			spawn := object(subagent["thread_spawn"])
			_, hasFork = spawn["parent_thread_id"].(string)
		}
		if instant, ok := timestamp(record["timestamp"]); ok && hasFork {
			state.SuppressingForkCopies, state.ForkCopyAnchorMs = true, instant
		}
		return nil
	}
	if record["type"] == "turn_context" {
		if model, ok := payload["model"].(string); ok {
			state.Model = model
		}
		return nil
	}
	if payload["type"] == "thread_settings_applied" {
		if settings := object(payload["thread_settings"]); settings != nil {
			state.Speed = "standard"
			switch settings["service_tier"] {
			case "priority", "fast":
				state.Speed = "fast"
			case "ultrafast":
				state.Speed = "ultrafast"
			}
		}
		return nil
	}
	if record["type"] == "turn_aborted" || payload["type"] == "turn_aborted" || payload["type"] == "error" || ((payload["type"] == "task_complete" || payload["type"] == "turn_complete") && payload["error"] != nil) {
		instant, ok := timestamp(record["timestamp"])
		if !ok {
			return nil
		}
		fallback := "other"
		if record["type"] == "turn_aborted" || payload["type"] == "turn_aborted" {
			fallback = "aborted"
		}
		detail := text(payload["reason"]) + " " + text(payload["message"]) + " " + errorDetail(payload["error"])
		return &domain.Record{Provider: domain.Codex, TimestampMs: instant, SessionId: state.SessionId, Model: state.Model, ErrorKind: classifyError(detail, fallback)}
	}
	if payload["type"] != "token_count" {
		return nil
	}
	instant, hasTimestamp := timestamp(record["timestamp"])
	limits := object(payload["rate_limits"])
	if limits != nil && hasTimestamp {
		observation := codexQuota(limits)
		defer func() {
			if result == nil {
				result = &domain.Record{Provider: domain.Codex, TimestampMs: instant, SessionId: state.SessionId, IsQuotaOnly: true}
			}
			result.QuotaBucket, result.Quota = text(limits["limit_id"]), observation
		}()
	}
	info := object(payload["info"])
	usage := object(info["last_token_usage"])
	if usage == nil || !hasTimestamp || state.Model == "" {
		return nil
	}
	var raw struct {
		Payload struct {
			Info struct {
				Usage json.RawMessage `json:"last_token_usage"`
			} `json:"info"`
		} `json:"payload"`
	}
	json.Unmarshal(data, &raw)
	signature := orderedSignature(raw.Payload.Info.Usage)
	if signature == state.LastUsageSignature {
		return nil
	}
	state.LastUsageSignature = signature
	if state.SuppressingForkCopies {
		if instant-state.ForkCopyAnchorMs < 1000 {
			state.ForkCopyAnchorMs = instant
			return nil
		}
		state.SuppressingForkCopies = false
	}
	input, cached, creation, output := integer(usage["input_tokens"]), integer(usage["cached_input_tokens"]), integer(usage["cache_write_input_tokens"]), integer(usage["output_tokens"])
	totals := domain.Tokens{UncachedInputTokens: max(0, input-cached-creation), CachedInputTokens: cached, CacheCreationTokens: creation, OutputTokens: output, ReasoningTokens: min(output, integer(usage["reasoning_output_tokens"]))}
	if totals.Total() == 0 {
		return nil
	}
	return &domain.Record{Provider: domain.Codex, TimestampMs: instant, Model: state.Model, SessionId: state.SessionId, Speed: state.Speed, Totals: totals}
}

// codexQuota maps positional log windows to their live probe identities.
func codexQuota(limits map[string]any) *domain.QuotaObservation {
	result := &domain.QuotaObservation{PlanType: text(limits["plan_type"]), Windows: []domain.QuotaWindow{}}
	for _, id := range []string{"primary", "secondary"} {
		window := object(limits[id])
		used, hasUsed := window["used_percent"].(float64)
		minutes, reset := integer(window["window_minutes"]), integer(window["resets_at"])
		if window == nil || !hasUsed || !domain.Finite(used) || used < 0 || used > 100 || minutes <= 0 || minutes > int64((1<<63-1)/time.Minute) || reset <= 0 {
			continue
		}
		result.Windows = append(result.Windows, domain.QuotaWindow{Id: id, UsedPercent: used, WindowMinutes: int(minutes), ResetsAt: reset})
	}
	return result
}

// orderedSignature retains property order while normalizing JSON numeric spellings like JSON.stringify.
func orderedSignature(data []byte) string {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var encode func() []byte
	encode = func() []byte {
		token, err := decoder.Token()
		if err != nil {
			return nil
		}
		if delimiter, ok := token.(json.Delim); ok {
			result := []byte{byte(delimiter)}
			didWrite := false
			for decoder.More() {
				if didWrite {
					result = append(result, ',')
				}
				if delimiter == '{' {
					key, _ := decoder.Token()
					encoded, _ := json.Marshal(key)
					result = append(result, encoded...)
					result = append(result, ':')
				}
				result = append(result, encode()...)
				didWrite = true
			}
			closing, _ := decoder.Token()
			result = append(result, byte(closing.(json.Delim)))
			return result
		}
		if value, ok := token.(float64); ok && value == 0 {
			return []byte("0")
		}
		encoded, _ := json.Marshal(token)
		return encoded
	}
	return string(encode())
}

// DedupeMovedCodex matches occurrences across files while keeping repeated events inside each file.
func DedupeMovedCodex(files [][]domain.Record) [][]domain.Record {
	owners := map[string]int{}
	shared := map[string]bool{}
	for index, records := range files {
		seen := map[string]bool{}
		for _, record := range records {
			if record.Provider != domain.Codex || record.SessionId == "" || seen[record.SessionId] {
				continue
			}
			seen[record.SessionId] = true
			if owner, exists := owners[record.SessionId]; exists && owner != index {
				shared[record.SessionId] = true
			} else {
				owners[record.SessionId] = index
			}
		}
	}
	result := make([][]domain.Record, len(files))
	for index, records := range files {
		hasShared := false
		for _, record := range records {
			if record.Provider == domain.Codex && shared[record.SessionId] {
				hasShared = true
				break
			}
		}
		if !hasShared {
			result[index] = records
			continue
		}
		if len(records) > 0 {
			result[index] = make([]domain.Record, len(records))
		}
		occurrences := map[string]int{}
		for recordIndex, record := range records {
			if record.Provider == domain.Codex && shared[record.SessionId] {
				identity := []any{record.Provider, record.SessionId, record.TimestampMs, record.Model, record.Totals, record.QuotaBucket, record.IsQuotaOnly, record.ErrorKind, record.CacheTtlMs}
				if record.IsQuotaOnly {
					identity = append(identity, record.Quota)
				}
				key, _ := json.Marshal(identity)
				occurrences[string(key)]++
				encoded, _ := json.Marshal([]any{string(key), occurrences[string(key)]})
				record.DedupeKey = string(encoded)
			}
			result[index][recordIndex] = record
		}
	}
	return result
}

// classifyError reduces transport diagnostics to a closed vocabulary without retaining their text.
func classifyError(detail, fallback string) string {
	detail = strings.ToLower(detail)
	switch {
	case strings.Contains(detail, "stream disconnected"):
		return "streamDisconnected"
	case strings.Contains(detail, "rate_limit"), strings.Contains(detail, "rate limit"), strings.Contains(detail, "429"):
		return "rateLimit"
	case strings.Contains(detail, "overloaded"), strings.Contains(detail, "529"):
		return "overloaded"
	case strings.Contains(detail, "authentication"), strings.Contains(detail, "unauthorized"), strings.Contains(detail, "401"):
		return "auth"
	case strings.Contains(detail, "server_error"), strings.Contains(detail, "server error"), strings.Contains(detail, "500"), strings.Contains(detail, "503"):
		return "server"
	case strings.Contains(detail, "interrupt"), strings.Contains(detail, "cancel"), strings.Contains(detail, "user_requested"):
		return "interrupted"
	}
	return fallback
}

// errorDetail inspects only error classification fields and is never persisted.
func errorDetail(value any) string {
	if detail, ok := value.(string); ok {
		return detail
	}
	details := object(value)
	status := integer(details["status"])
	code := ""
	switch status {
	case 401, 403:
		code = "authentication_failed"
	case 429:
		code = "rate_limit"
	case 529:
		code = "overloaded"
	case 500, 502, 503, 504:
		code = "server_error"
	}
	return code + " " + text(details["type"]) + " " + text(details["code"]) + " " + text(details["message"])
}
