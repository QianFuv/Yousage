package history

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
	"yousage/internal/domain"
	"yousage/internal/storage"
)

// codexTimingEvent creates content-free timestamped provider events for request timing fixtures.
func codexTimingEvent(kind, payloadKind, role, instant string) string {
	data, _ := json.Marshal(map[string]any{"type": kind, "timestamp": instant, "payload": map[string]any{"type": payloadKind, "role": role}})
	return string(data)
}

// TestCodexGenerationExcludesToolExecution associates delayed usage with its preceding response.
func TestCodexGenerationExcludesToolExecution(test *testing.T) {
	state := InitialCodexState()
	ParseCodex([]byte(sessionMeta), &state)
	ParseCodex([]byte(turnContext), &state)
	for _, event := range []string{
		codexTimingEvent("event_msg", "task_started", "", "2026-10-06T12:00:00Z"),
		codexTimingEvent("turn_context", "", "", "2026-10-06T12:00:01Z"),
		codexTimingEvent("response_item", "reasoning", "", "2026-10-06T12:00:02Z"),
		codexTimingEvent("response_item", "custom_tool_call", "", "2026-10-06T12:00:04Z"),
		codexTimingEvent("response_item", "custom_tool_call_output", "", "2026-10-06T12:00:20Z"),
		codexTimingEvent("response_item", "function_call_output", "", "2026-10-06T12:00:21Z"),
	} {
		ParseCodex([]byte(event), &state)
	}
	record := ParseCodex([]byte(codexUsage(10, "2026-10-06T12:00:21.003Z")), &state)
	if record == nil || record.GenerationMs != 3000 {
		test.Fatal(record, state)
	}
	if ParseCodex([]byte(codexUsage(10, "2026-10-06T12:00:21.005Z")), &state) != nil {
		test.Fatal("duplicate usage counted")
	}
	ParseCodex([]byte(codexTimingEvent("response_item", "message", "assistant", "2026-10-06T12:00:23Z")), &state)
	record = ParseCodex([]byte(codexUsage(11, "2026-10-06T12:00:24Z")), &state)
	if record == nil || record.GenerationMs != 2000 {
		test.Fatal(record, state)
	}
}

// TestCodexUnreliableTiming retains usage while rejecting absent, overlapping, idle and failed intervals.
func TestCodexUnreliableTiming(test *testing.T) {
	for _, events := range [][]string{
		{},
		{codexTimingEvent("event_msg", "task_started", "", "2026-10-06T12:00:00Z")},
		{codexTimingEvent("response_item", "message", "assistant", "2026-10-06T12:00:02Z")},
		{codexTimingEvent("event_msg", "task_started", "", "2026-10-06T12:00:02Z"), codexTimingEvent("response_item", "message", "assistant", "2026-10-06T12:00:01Z")},
		{codexTimingEvent("event_msg", "task_started", "", "2026-10-06T11:40:00Z"), codexTimingEvent("response_item", "message", "assistant", "2026-10-06T12:00:02Z")},
		{codexTimingEvent("event_msg", "task_started", "", "2026-10-06T12:00:00Z"), codexTimingEvent("response_item", "message", "assistant", "2026-10-06T12:00:02Z"), codexTimingEvent("event_msg", "turn_aborted", "", "2026-10-06T12:00:03Z")},
		{codexTimingEvent("event_msg", "task_started", "", "2026-10-06T12:00:00Z"), codexTimingEvent("response_item", "function_call", "", "2026-10-06T12:00:01Z"), codexTimingEvent("response_item", "function_call_output", "", "2026-10-06T12:00:02Z"), codexTimingEvent("response_item", "message", "assistant", "2026-10-06T12:00:03Z")},
	} {
		state := InitialCodexState()
		ParseCodex([]byte(turnContext), &state)
		for _, event := range events {
			ParseCodex([]byte(event), &state)
		}
		record := ParseCodex([]byte(codexUsage(10, "2026-10-06T12:00:04Z")), &state)
		if record == nil || record.GenerationMs != 0 {
			test.Fatal(record, events)
		}
	}
}

// TestCodexAmbiguousTimingRequiresFreshBoundary prevents uncertain requests from poisoning later intervals.
func TestCodexAmbiguousTimingRequiresFreshBoundary(test *testing.T) {
	state := InitialCodexState()
	ParseCodex([]byte(turnContext), &state)
	for _, event := range []string{
		codexTimingEvent("event_msg", "task_started", "", "2026-10-06T12:00:00Z"),
		codexTimingEvent("response_item", "function_call", "", "2026-10-06T12:00:01Z"),
		codexTimingEvent("response_item", "function_call_output", "", "2026-10-06T12:00:02Z"),
		codexTimingEvent("response_item", "message", "assistant", "2026-10-06T12:00:03Z"),
	} {
		ParseCodex([]byte(event), &state)
	}
	record := ParseCodex([]byte(codexUsage(10, "2026-10-06T12:00:04Z")), &state)
	if record == nil || record.GenerationMs != 0 {
		test.Fatal(record)
	}
	ParseCodex([]byte(codexTimingEvent("response_item", "message", "assistant", "2026-10-06T12:00:05Z")), &state)
	record = ParseCodex([]byte(codexUsage(11, "2026-10-06T12:00:06Z")), &state)
	if record == nil || record.GenerationMs != 0 {
		test.Fatal("ambiguous timing leaked into next request", record)
	}
	ParseCodex([]byte(codexTimingEvent("response_item", "function_call_output", "", "2026-10-06T12:00:07Z")), &state)
	ParseCodex([]byte(codexTimingEvent("response_item", "message", "assistant", "2026-10-06T12:00:09Z")), &state)
	record = ParseCodex([]byte(codexUsage(12, "2026-10-06T12:00:10Z")), &state)
	if record == nil || record.GenerationMs != 2000 {
		test.Fatal("fresh boundary did not recover", record)
	}
}

// TestGenerationCheckpointReplayAndAppend upgrades unchanged files and preserves request state across scans.
func TestGenerationCheckpointReplayAndAppend(test *testing.T) {
	ctx := context.Background()
	home := test.TempDir()
	path := filepath.Join(home, "sessions", "timing.jsonl")
	start := codexTimingEvent("response_item", "message", "user", "2026-10-06T12:00:00Z")
	end := codexTimingEvent("response_item", "message", "assistant", "2026-10-06T12:00:02Z")
	data := sessionMeta + "\n" + turnContext + "\n" + start + "\n" + end + "\n" + codexUsage(10, "2026-10-06T12:00:03Z") + "\n"
	writeHistory(test, path, data)
	store, err := storage.Open(filepath.Join(test.TempDir(), "state.sqlite"))
	if err != nil {
		test.Fatal(err)
	}
	defer store.Close()
	collector := Collector{Store: store, HostId: "fixture"}
	roots := []Root{{Provider: domain.Codex, Home: home}}
	now := time.Date(2026, 10, 6, 13, 0, 0, 0, time.UTC)
	sources, err := collector.Synchronize(ctx, roots, now)
	if err != nil {
		test.Fatal(err)
	}
	id := sources[0].Fingerprint.Id()
	path = filepath.Join(sources[0].Fingerprint.ResolvedHomePath, filepath.Base(path))
	checkpoint, err := store.Checkpoint(ctx, id, path)
	if err != nil || checkpoint.Revision == 0 {
		test.Fatal("initial canonical checkpoint missing", checkpoint, err)
	}
	records, _ := store.Records(ctx, id)
	if len(records) != 1 || records[0].GenerationMs != 2000 {
		test.Fatal(records)
	}
	checkpoint.ParserVersion = ParserVersion - 1
	records[0].GenerationMs = 0
	checkpoint, err = store.CommitFile(ctx, checkpoint, records, true)
	if err != nil {
		test.Fatal(err)
	}
	if _, err := collector.Synchronize(ctx, roots, now); err != nil {
		test.Fatal(err)
	}
	actual, _ := store.Checkpoint(ctx, id, path)
	records, _ = store.Records(ctx, id)
	if actual.ParserVersion != ParserVersion || actual.Revision != checkpoint.Revision+1 || records[0].GenerationMs != 2000 {
		test.Fatal(actual, records)
	}
	if _, err := collector.Synchronize(ctx, roots, now); err != nil {
		test.Fatal(err)
	}
	stable, _ := store.Checkpoint(ctx, id, path)
	if stable.Revision != actual.Revision {
		test.Fatal("unchanged upgraded file reread")
	}
	data += codexTimingEvent("response_item", "function_call_output", "", "2026-10-06T12:01:00Z") + "\n"
	writeHistory(test, path, data)
	if _, err := collector.Synchronize(ctx, roots, now); err != nil {
		test.Fatal(err)
	}
	data += codexTimingEvent("response_item", "message", "assistant", "2026-10-06T12:01:04Z") + "\n" + codexUsage(11, "2026-10-06T12:01:05Z")
	writeHistory(test, path, data)
	if _, err := collector.Synchronize(ctx, roots, now); err != nil {
		test.Fatal(err)
	}
	records, _ = store.Records(ctx, id)
	if len(records) != 2 || records[1].GenerationMs != 4000 {
		test.Fatal(records)
	}
	writeHistory(test, path, data+"\n")
	if _, err := collector.Synchronize(ctx, roots, now); err != nil {
		test.Fatal(err)
	}
	records, _ = store.Records(ctx, id)
	if len(records) != 2 || records[1].GenerationMs != 4000 {
		test.Fatal("tail replay changed timing", records)
	}
}

// claudeEntry creates a content-free Claude Code entry linked to its parent.
func claudeEntry(kind, uuid, parent, instant string) string {
	data, _ := json.Marshal(map[string]any{"type": kind, "uuid": uuid, "parentUuid": parent, "timestamp": instant, "sessionId": "session"})
	return string(data)
}

// claudeBlock creates one assistant content block; a nil stop reason marks a block streamed before the message ended.
func claudeBlock(uuid, parent, id, instant string, output int64, stopReason any) string {
	data, _ := json.Marshal(map[string]any{"type": "assistant", "uuid": uuid, "parentUuid": parent, "timestamp": instant, "sessionId": "session", "requestId": "request-" + id, "message": map[string]any{"id": id, "model": "fixture", "stop_reason": stopReason, "usage": map[string]any{"input_tokens": 10, "output_tokens": output}}})
	return string(data)
}

// parseClaudeLines feeds lines through one Claude reducer state.
func parseClaudeLines(lines ...string) []domain.Record {
	state, records := ClaudeState{}, []domain.Record{}
	for _, line := range lines {
		records = append(records, ParseClaudeLine([]byte(line), &state)...)
	}
	return records
}

// TestClaudeGenerationFollowsParentChain times each request from its prompt or tool result through attachments.
func TestClaudeGenerationFollowsParentChain(test *testing.T) {
	records := parseClaudeLines(
		claudeEntry("user", "prompt", "", "2026-10-06T12:00:00Z"),
		claudeEntry("attachment", "attachment", "prompt", "2026-10-06T12:00:00.010Z"),
		claudeBlock("thinking", "attachment", "first", "2026-10-06T12:00:09.990Z", 500, "tool_use"),
		claudeBlock("tool", "thinking", "first", "2026-10-06T12:00:10Z", 500, "tool_use"),
		claudeEntry("user", "result", "tool", "2026-10-06T12:00:12Z"),
		claudeBlock("answer", "result", "second", "2026-10-06T12:00:14.500Z", 250, "end_turn"),
	)
	if len(records) != 3 || records[0].GenerationMs != 9990 || records[0].DedupeKey != records[1].DedupeKey || records[2].GenerationMs != 2500 {
		test.Fatal(records)
	}
}

// TestClaudeStreamedBlocksUseFinalUsage counts a streamed message once, with its final usage and end time.
func TestClaudeStreamedBlocksUseFinalUsage(test *testing.T) {
	records := parseClaudeLines(
		claudeEntry("user", "prompt", "", "2026-10-06T12:00:00Z"),
		claudeBlock("tool", "prompt", "message", "2026-10-06T12:00:05Z", 20, nil),
		claudeEntry("user", "result", "tool", "2026-10-06T12:00:06Z"),
		claudeBlock("answer", "tool", "message", "2026-10-06T12:00:08Z", 285, "tool_use"),
	)
	if len(records) != 1 || records[0].Totals.OutputTokens != 285 || records[0].GenerationMs != 8000 {
		test.Fatal(records)
	}
}

// TestClaudeUnfinishedMessageIsKept retains an interrupted message's partial usage without timing it.
func TestClaudeUnfinishedMessageIsKept(test *testing.T) {
	records := parseClaudeLines(
		claudeEntry("user", "prompt", "", "2026-10-06T12:00:00Z"),
		claudeBlock("partial", "prompt", "interrupted", "2026-10-06T12:00:05Z", 20, nil),
		claudeEntry("user", "next", "partial", "2026-10-06T12:01:00Z"),
		claudeBlock("answer", "next", "finished", "2026-10-06T12:01:04Z", 400, "end_turn"),
	)
	if len(records) != 2 || records[0].Totals.OutputTokens != 20 || records[0].GenerationMs != 0 || records[1].GenerationMs != 4000 {
		test.Fatal(records)
	}
}

// TestClaudeUnreliableTiming keeps usage while leaving retried, orphaned and idle requests untimed.
func TestClaudeUnreliableTiming(test *testing.T) {
	apiError := `{"type":"assistant","isApiErrorMessage":true,"timestamp":"2026-10-06T12:00:03Z","sessionId":"session","error":"overloaded_error","message":{"model":"fixture"}}`
	for _, lines := range [][]string{
		{claudeEntry("user", "prompt", "", "2026-10-06T12:00:00Z"), apiError, claudeBlock("answer", "prompt", "message", "2026-10-06T12:00:09Z", 300, "end_turn")},
		{claudeBlock("answer", "missing", "message", "2026-10-06T12:00:09Z", 300, "end_turn")},
		{claudeEntry("user", "prompt", "", "2026-10-06T11:40:00Z"), claudeBlock("answer", "prompt", "message", "2026-10-06T12:00:09Z", 300, "end_turn")},
	} {
		records := parseClaudeLines(lines...)
		usage := records[len(records)-1]
		if usage.ErrorKind != "" || usage.Totals.OutputTokens != 300 || usage.GenerationMs != 0 {
			test.Fatal(records)
		}
	}
}

// TestClaudeTimingResumesFromCheckpoint finishes a streamed message across incremental reads.
func TestClaudeTimingResumesFromCheckpoint(test *testing.T) {
	path := filepath.Join(test.TempDir(), "claude.jsonl")
	first := claudeEntry("user", "prompt", "", "2026-10-06T12:00:00Z") + "\n" + claudeBlock("tool", "prompt", "message", "2026-10-06T12:00:05Z", 20, nil) + "\n"
	writeHistory(test, path, first)
	read, err := ReadTranscript(context.Background(), path, domain.Claude, domain.Checkpoint{})
	if err != nil || len(read.Records) != 0 {
		test.Fatal(read, err)
	}
	writeHistory(test, path, first+claudeBlock("answer", "tool", "message", "2026-10-06T12:00:07.500Z", 285, "tool_use")+"\n")
	resumed, err := ReadTranscript(context.Background(), path, domain.Claude, read.Checkpoint)
	if err != nil || !resumed.IsResumed || len(resumed.Records) != 1 || resumed.Records[0].Totals.OutputTokens != 285 || resumed.Records[0].GenerationMs != 7500 {
		test.Fatal(resumed, err)
	}
}
