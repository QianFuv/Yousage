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

// CodexState checkpoints usage deduplication and scalar request timing without transcript content.
type CodexState struct {
	Model                 string      `json:"model"`
	Speed                 string      `json:"speed"`
	SessionId             string      `json:"sessionId"`
	LastUsageSignature    string      `json:"lastUsageSignature"`
	SawSessionMeta        bool        `json:"sawSessionMeta"`
	SuppressingForkCopies bool        `json:"suppressingForkCopies"`
	ForkCopyAnchorMs      int64       `json:"forkCopyAnchorMs"`
	GenerationStartMs     int64       `json:"generationStartMs,omitempty"`
	ResponseEndMs         int64       `json:"responseEndMs,omitempty"`
	NextGenerationMs      int64       `json:"nextGenerationMs,omitempty"`
	IsTimingAmbiguous     bool        `json:"isTimingAmbiguous,omitempty"`
	Claude                ClaudeState `json:"claude,omitzero"`
}

// ClaudeState checkpoints request starts and unfinished messages without transcript content,
// so Claude usage and timing come from each message's final block.
type ClaudeState struct {
	Entries     []ClaudeEntry   `json:"entries,omitempty"`
	Starts      []ClaudeStart   `json:"starts,omitempty"`
	Pending     []domain.Record `json:"pending,omitempty"`
	LastErrorMs int64           `json:"lastErrorMs,omitempty"`
}

// ClaudeEntry links a recent non-assistant entry to its parent for request start lookups.
type ClaudeEntry struct {
	Uuid        string `json:"uuid"`
	ParentUuid  string `json:"parentUuid,omitempty"`
	IsUser      bool   `json:"isUser,omitempty"`
	TimestampMs int64  `json:"timestampMs"`
}

// ClaudeStart is the request start of a message, or zero when it cannot be established.
type ClaudeStart struct {
	Key     string `json:"key"`
	StartMs int64  `json:"startMs,omitempty"`
}

// claudeEntryLimit bounds the retained parent chain; a request's starting entry is always recent.
const claudeEntryLimit = 64

// claudeStartLimit bounds the messages whose request start is remembered.
const claudeStartLimit = 16

// claudeParentHops bounds the walk through attachments to the user entry that started a request.
const claudeParentHops = 16

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

// ParseClaudeLine emits each assistant message from its final block, timed from the user prompt or
// tool result that started the request through that block. Claude Code writes blocks streamed before
// the message ends with a null stop reason and partial usage; those wait for the final block and are
// emitted untimed only when another message begins without one. A request that hit an API error is
// not timed, because the interval would include the failed attempt and its retry delay.
func ParseClaudeLine(data []byte, state *ClaudeState) []domain.Record {
	var line struct {
		Type       string `json:"type"`
		Uuid       string `json:"uuid"`
		ParentUuid string `json:"parentUuid"`
		Timestamp  string `json:"timestamp"`
		Message    struct {
			StopReason json.RawMessage `json:"stop_reason"`
		} `json:"message"`
	}
	json.Unmarshal(data, &line)
	record := ParseClaude(data)
	if record != nil && record.ErrorKind != "" {
		state.LastErrorMs = max(state.LastErrorMs, record.TimestampMs)
		return []domain.Record{*record}
	}
	if line.Type != "assistant" {
		if instant, ok := timestamp(line.Timestamp); ok && line.Uuid != "" {
			entries := append(state.Entries, ClaudeEntry{Uuid: line.Uuid, ParentUuid: line.ParentUuid, IsUser: line.Type == "user", TimestampMs: instant})
			state.Entries = entries[max(0, len(entries)-claudeEntryLimit):]
		}
		return nil
	}
	if record == nil {
		return nil
	}
	key := record.DedupeKey
	if key == "" {
		return []domain.Record{*record}
	}
	result, pending := []domain.Record{}, []domain.Record{}
	for _, held := range state.Pending {
		if held.DedupeKey == key {
			pending = append(pending, held)
		} else {
			result = append(result, held)
		}
	}
	start, isKnown := int64(0), false
	for _, known := range state.Starts {
		if known.Key == key {
			start, isKnown = known.StartMs, true
		}
	}
	if !isKnown {
		start = state.requestStart(line.ParentUuid)
		starts := append(state.Starts, ClaudeStart{Key: key, StartMs: start})
		state.Starts = starts[max(0, len(starts)-claudeStartLimit):]
	}
	if string(line.Message.StopReason) == "null" {
		state.Pending = []domain.Record{*record}
		return result
	}
	state.Pending = nil
	if start > 0 && (state.LastErrorMs < start || state.LastErrorMs > record.TimestampMs) {
		record.GenerationMs = domain.ReliableGenerationMs(record.TimestampMs-start, record.Totals.OutputTokens)
	}
	return append(result, *record)
}

// requestStart follows a message's parent chain through attachments to the user entry that sent it.
func (state *ClaudeState) requestStart(parentUuid string) int64 {
	for hop := 0; hop < claudeParentHops && parentUuid != ""; hop++ {
		var parent *ClaudeEntry
		for index := len(state.Entries) - 1; index >= 0; index-- {
			if state.Entries[index].Uuid == parentUuid {
				parent = &state.Entries[index]
				break
			}
		}
		if parent == nil {
			return 0
		}
		if parent.IsUser {
			return parent.TimestampMs
		}
		parentUuid = parent.ParentUuid
	}
	return 0
}

// ParseCodex retains quota observations independently of billable usage and measures
// timestamped request boundaries through model response items, excluding tool execution.
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
		if instant, ok := timestamp(record["timestamp"]); ok && state.GenerationStartMs > 0 && state.ResponseEndMs == 0 {
			state.GenerationStartMs = max(state.GenerationStartMs, instant)
		}
		if model, ok := payload["model"].(string); ok {
			state.Model = model
		}
		return nil
	}
	updateCodexTiming(record, payload, state)
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
			clearCodexTiming(state)
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
	duration := int64(0)
	if !state.IsTimingAmbiguous && state.GenerationStartMs > 0 && state.ResponseEndMs <= instant {
		duration = domain.ReliableGenerationMs(state.ResponseEndMs-state.GenerationStartMs, output)
	}
	if state.IsTimingAmbiguous {
		state.GenerationStartMs = 0
	} else {
		state.GenerationStartMs = state.NextGenerationMs
	}
	state.ResponseEndMs, state.NextGenerationMs, state.IsTimingAmbiguous = 0, 0, false
	return &domain.Record{Provider: domain.Codex, TimestampMs: instant, Model: state.Model, SessionId: state.SessionId, Speed: state.Speed, Totals: totals, GenerationMs: duration}
}

// clearCodexTiming invalidates timing when a turn ends or fails.
func clearCodexTiming(state *CodexState) {
	state.GenerationStartMs, state.ResponseEndMs, state.NextGenerationMs, state.IsTimingAmbiguous = 0, 0, 0, false
}

// updateCodexTiming preserves a finished response while tool outputs establish the next request.
// Usage emitted after a tool result belongs to the preceding model response, not the tool wait.
func updateCodexTiming(record, payload map[string]any, state *CodexState) {
	kind := text(payload["type"])
	if text(record["type"]) == "turn_aborted" || kind == "turn_aborted" || kind == "error" || kind == "task_complete" || kind == "turn_complete" {
		clearCodexTiming(state)
		return
	}
	instant, ok := timestamp(record["timestamp"])
	if !ok {
		return
	}
	if kind == "task_started" || kind == "user_message" || (record["type"] == "response_item" && kind == "message" && payload["role"] == "user") {
		clearCodexTiming(state)
		state.GenerationStartMs = instant
		return
	}
	if record["type"] != "response_item" {
		return
	}
	switch kind {
	case "function_call_output", "custom_tool_call_output":
		if state.ResponseEndMs > 0 {
			state.NextGenerationMs = max(state.NextGenerationMs, instant)
		} else {
			state.GenerationStartMs = max(state.GenerationStartMs, instant)
		}
	case "function_call", "custom_tool_call", "reasoning", "message":
		if kind == "message" && payload["role"] != "assistant" {
			return
		}
		if state.NextGenerationMs > 0 {
			state.IsTimingAmbiguous = true
		}
		state.ResponseEndMs = max(state.ResponseEndMs, instant)
	}
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
