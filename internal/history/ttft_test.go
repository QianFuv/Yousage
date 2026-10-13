package history

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"yousage/internal/domain"
)

// codexItemCompleted creates a content-free item completion with its native stream start.
func codexItemCompleted(itemType, instant string, startedAt time.Time) string {
	data, _ := json.Marshal(map[string]any{"type": "event_msg", "timestamp": instant, "payload": map[string]any{"type": "item_completed", "started_at_ms": startedAt.UnixMilli(), "completed_at_ms": startedAt.Add(time.Second).UnixMilli(), "item": map[string]any{"type": itemType}}})
	return string(data)
}

// TestCodexTtftFromFirstOutputItem measures each request to its first reasoning or reply item, including
// items that complete after the previous request's tool output but before its delayed usage.
func TestCodexTtftFromFirstOutputItem(test *testing.T) {
	base := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	at := func(milliseconds int) string {
		return base.Add(time.Duration(milliseconds) * time.Millisecond).Format(time.RFC3339Nano)
	}
	state := InitialCodexState()
	ParseCodex([]byte(sessionMeta), &state)
	ParseCodex([]byte(turnContext), &state)
	for _, event := range []string{
		codexTimingEvent("event_msg", "task_started", "", at(0)),
		codexTimingEvent("response_item", "message", "user", at(1000)),
		codexItemCompleted("UserMessage", at(1000), base.Add(1000*time.Millisecond)),
		codexItemCompleted("AgentMessage", at(6000), base.Add(3500*time.Millisecond)),
		codexTimingEvent("response_item", "message", "assistant", at(6000)),
		codexTimingEvent("response_item", "custom_tool_call", "", at(7000)),
		codexTimingEvent("response_item", "custom_tool_call_output", "", at(10000)),
		codexItemCompleted("Reasoning", at(12500), base.Add(12000*time.Millisecond)),
	} {
		ParseCodex([]byte(event), &state)
	}
	first := ParseCodex([]byte(codexUsage(100, at(12600))), &state)
	if first == nil || first.GenerationMs != 6000 || first.TtftMs != 2500 {
		test.Fatal("first request must end at its tool call and start streaming at its reply", first)
	}
	ParseCodex([]byte(codexTimingEvent("response_item", "reasoning", "", at(13000))), &state)
	ParseCodex([]byte(codexTimingEvent("response_item", "function_call", "", at(14000))), &state)
	second := ParseCodex([]byte(codexUsage(101, at(14500))), &state)
	if second == nil || second.GenerationMs != 4000 || second.TtftMs != 2000 {
		test.Fatal("an item completed before the previous usage belongs to the next request", second)
	}
	ParseCodex([]byte(codexTimingEvent("response_item", "function_call_output", "", at(15000))), &state)
	ParseCodex([]byte(codexItemCompleted("CommandExecution", at(15500), base.Add(15100*time.Millisecond))), &state)
	ParseCodex([]byte(codexTimingEvent("response_item", "function_call", "", at(17000))), &state)
	toolOnly := ParseCodex([]byte(codexUsage(102, at(17500))), &state)
	if toolOnly == nil || toolOnly.GenerationMs != 2000 || toolOnly.TtftMs != 0 {
		test.Fatal("tool executions and tool-call-only responses have no first output item", toolOnly)
	}
}

// claudeTimedBlock creates one content-free assistant block with its native block index and thinking duration.
func claudeTimedBlock(uuid, parent, id, instant string, index int, thinkingMs int64) string {
	entry := map[string]any{"type": "assistant", "uuid": uuid, "parentUuid": parent, "timestamp": instant, "sessionId": "session", "requestId": "request-" + id, "apiBlockIndex": index, "message": map[string]any{"id": id, "model": "fixture", "stop_reason": "end_turn", "usage": map[string]any{"input_tokens": 10, "output_tokens": 900}}}
	if thinkingMs > 0 {
		entry["thinkingDurationMs"] = thinkingMs
	}
	data, _ := json.Marshal(entry)
	return string(data)
}

// TestClaudeTtftFromThinkingBlock dates the first token from a leading thinking block and ends the request
// at the message's last block, which replaces earlier blocks of the same message.
func TestClaudeTtftFromThinkingBlock(test *testing.T) {
	lines := []string{
		claudeEntry("user", "prompt", "", "2026-10-06T12:00:00Z"),
		claudeTimedBlock("thinking", "prompt", "message", "2026-10-06T12:00:05Z", 0, 3000),
		claudeTimedBlock("answer", "thinking", "message", "2026-10-06T12:00:09Z", 1, 0),
		claudeEntry("user", "next", "answer", "2026-10-06T12:01:00Z"),
		claudeTimedBlock("tool", "next", "untimed", "2026-10-06T12:01:04Z", 0, 0),
		claudeTimedBlock("late", "tool", "untimed", "2026-10-06T12:01:05Z", 1, 2000),
	}
	records := parseClaudeLines(lines...)
	if len(records) != 4 || records[0].TtftMs != 2000 || records[1].TtftMs != 2000 || records[1].GenerationMs != 9000 || records[2].TtftMs != 0 || records[3].TtftMs != 0 {
		test.Fatal("only a leading thinking block establishes the first token", records)
	}
	path := filepath.Join(test.TempDir(), "claude.jsonl")
	writeHistory(test, path, strings.Join(lines, "\n")+"\n")
	read, err := ReadTranscript(context.Background(), path, domain.Claude, domain.Checkpoint{})
	if err != nil || len(read.Records) != 2 || read.Records[0].GenerationMs != 9000 || read.Records[0].TtftMs != 2000 || read.Records[1].GenerationMs != 5000 {
		test.Fatal("the last block of a message must end its request", read.Records, err)
	}
}
