package history

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"yousage/internal/accounting"
	"yousage/internal/domain"
	"yousage/internal/storage"
)

const sessionMeta = `{"type":"session_meta","timestamp":"2026-10-06T12:00:00Z","payload":{"id":"session"}}`
const turnContext = `{"type":"turn_context","payload":{"model":"fixture"}}`

func codexUsage(input int, timestamp string) string {
	data, _ := json.Marshal(map[string]any{"timestamp": timestamp, "type": "event_msg", "payload": map[string]any{"type": "token_count", "info": map[string]any{"last_token_usage": map[string]any{"input_tokens": input, "cached_input_tokens": 2, "cache_write_input_tokens": 1, "output_tokens": 4, "reasoning_output_tokens": 9}}}})
	return string(data)
}
func claudeUsage(id string) string {
	return `{"type":"assistant","timestamp":"2026-10-06T12:00:00.123Z","sessionId":"session","requestId":"request","costUSD":0.5,"message":{"id":"` + id + `","model":"fixture","usage":{"input_tokens":10,"cache_read_input_tokens":3,"cache_creation_input_tokens":2,"output_tokens":5,"speed":"fast"}}}`
}
func writeHistory(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestCodexStateDuplicateAndForkBoundaries(t *testing.T) {
	state := InitialCodexState()
	usage := codexUsage(10, "2026-10-06T12:00:00Z")
	if ParseCodex([]byte(usage), &state) != nil {
		t.Fatal("missing model emitted")
	}
	ParseCodex([]byte(sessionMeta), &state)
	ParseCodex([]byte(turnContext), &state)
	record := ParseCodex([]byte(usage), &state)
	if record == nil || record.Totals.UncachedInputTokens != 7 || record.Totals.ReasoningTokens != 4 || record.SessionId != "session" {
		t.Fatal(record)
	}
	if ParseCodex([]byte(usage), &state) != nil {
		t.Fatal("consecutive usage repeated")
	}
	ParseCodex([]byte(`{"type":"event_msg","payload":{"type":"thread_settings_applied","thread_settings":{"service_tier":"ultrafast"}}}`), &state)
	if ParseCodex([]byte(codexUsage(11, "2026-10-06T12:00:02Z")), &state).Speed != "ultrafast" {
		t.Fatal("tier not applied")
	}
	state = InitialCodexState()
	ParseCodex([]byte(`{"type":"session_meta","timestamp":"2026-10-06T12:00:00Z","payload":{"id":"child","forked_from_id":"parent"}}`), &state)
	ParseCodex([]byte(sessionMeta), &state)
	ParseCodex([]byte(turnContext), &state)
	if ParseCodex([]byte(codexUsage(10, "2026-10-06T12:00:00.999Z")), &state) != nil {
		t.Fatal("copied parent counted")
	}
	record = ParseCodex([]byte(codexUsage(11, "2026-10-06T12:00:01.999Z")), &state)
	if record == nil || record.SessionId != "child" {
		t.Fatal("1000ms boundary suppressed or ancestor took ownership", record)
	}
}

func TestReaderAppendTailRewriteGuardAndCrLf(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.jsonl")
	first := sessionMeta + "\r\n" + turnContext + "\r\n" + codexUsage(10, "2026-10-06T12:00:01Z")
	writeHistory(t, path, first)
	read, err := ReadTranscript(context.Background(), path, domain.Codex, domain.Checkpoint{})
	if err != nil || len(read.Records) != 0 || len(read.Tail) != 1 || read.Checkpoint.Offset != int64(len(sessionMeta+"\r\n"+turnContext+"\r\n")) {
		t.Fatal(read, err)
	}
	var committed CodexState
	json.Unmarshal(read.Checkpoint.Reducer, &committed)
	if committed.LastUsageSignature != "" {
		t.Fatal("tail advanced reducer")
	}
	writeHistory(t, path, first+"\n"+codexUsage(11, "2026-10-06T12:00:02Z")+"\n")
	appended, err := ReadTranscript(context.Background(), path, domain.Codex, read.Checkpoint)
	if err != nil || !appended.IsResumed || len(appended.Records) != 2 || len(appended.Tail) != 0 {
		t.Fatal(appended, err)
	}
	previous := appended.Checkpoint
	previous.Guard = "mismatch"
	writeHistory(t, path, first+"\n"+codexUsage(11, "2026-10-06T12:00:02Z")+"\n"+codexUsage(12, "2026-10-06T12:00:03Z")+"\n")
	full, err := ReadTranscript(context.Background(), path, domain.Codex, previous)
	if err != nil || full.IsResumed || len(full.Records) != 3 {
		t.Fatal(full, err)
	}
	writeHistory(t, path, turnContext+"\n"+codexUsage(20, "2026-10-06T12:00:10Z")+"\n")
	shrunk, err := ReadTranscript(context.Background(), path, domain.Codex, full.Checkpoint)
	if err != nil || shrunk.IsResumed || len(shrunk.Records) != 1 {
		t.Fatal(shrunk, err)
	}
}

func TestStreamingProjectionLargeToolContentAndMalformedRecovery(t *testing.T) {
	data := `{"type":"assistant","timestamp":"2026-10-06T12:00:00Z","message":{"content":[{"text":"` + strings.Repeat("x", 8*1024*1024) + `"}],"id":"message","model":"fixture","usage":{"input_tokens":5,"output_tokens":7}}}`
	stream := &lineStream{reader: bufio.NewReader(strings.NewReader(data)), ctx: context.Background()}
	projected, err := projectValue(stream, transcriptFields)
	if err != nil || len(projected) > 300 || ParseClaude(projected) == nil {
		t.Fatal("large unselected content lost usage", len(projected), err)
	}
	path := filepath.Join(t.TempDir(), "claude.jsonl")
	writeHistory(t, path, claudeUsage("first")+"\n"+`{"message":"bad\q"}`+"\n"+claudeUsage("second")+"\n")
	read, err := ReadTranscript(context.Background(), path, domain.Claude, domain.Checkpoint{})
	if err != nil || len(read.Records) != 2 || read.Malformed != 1 {
		t.Fatal(read, err)
	}
	if read.Records[0].ReportedCostUsd == nil || *read.Records[0].ReportedCostUsd != 0.5 || read.Records[0].Speed != "fast" {
		t.Fatal(read.Records[0])
	}
}

func TestCollectorDedupRetentionDeletionAndRootRecovery(t *testing.T) {
	home := t.TempDir()
	store, err := storage.Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	collector := Collector{Store: store, HostId: "fixture-host"}
	root := Root{Provider: domain.Codex, Home: home}
	data := sessionMeta + "\n" + turnContext + "\n" + codexUsage(11, "2026-10-06T12:00:01Z") + "\n" + codexUsage(12, "2026-10-06T12:00:01Z") + "\n" + codexUsage(11, "2026-10-06T12:00:01Z") + "\n"
	writeHistory(t, filepath.Join(home, "sessions", "first.jsonl"), data)
	writeHistory(t, filepath.Join(home, "sessions", "copy.jsonl"), data)
	writeHistory(t, filepath.Join(home, "archived_sessions", "ignored.jsonl"), data)
	writeHistory(t, filepath.Join(home, "sessions", "ignored.JSONL"), data)
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	sources, err := collector.Scan(context.Background(), []Root{root, root}, now)
	if err != nil || len(sources) != 1 || sources[0].ScannedFiles != 2 {
		t.Fatal(sources, err)
	}
	result, err := accounting.Aggregate(sources[0].Records, accounting.AggregateOptions{Window: accounting.Window{SinceDay: "2026-10-06", UntilDay: "2026-10-06", TimeZone: "UTC"}})
	if err != nil || result.DuplicatesDropped != 3 || result.Buckets[0].Totals.UncachedInputTokens != 25 || result.Buckets[0].Totals.OutputTokens != 12 {
		t.Fatal(result, err)
	}
	identity := sources[0].Fingerprint
	if err := os.RemoveAll(filepath.Join(home, "sessions")); err != nil {
		t.Fatal(err)
	}
	sources, err = collector.Scan(context.Background(), []Root{root}, now)
	if err != nil || sources[0].Status != "ok" || len(sources[0].Records) != 6 || sources[0].Fingerprint != identity {
		t.Fatal(sources, err)
	}
	if err := os.MkdirAll(filepath.Join(home, "sessions"), 0700); err != nil {
		t.Fatal(err)
	}
	sources, err = collector.Scan(context.Background(), []Root{root}, now)
	if err != nil || sources[0].Fingerprint != identity {
		t.Fatal("recreated root lost retained ownership", sources, err)
	}
	if err := store.PruneHistory(context.Background(), identity.Id(), now.AddDate(0, 0, 91).UnixNano()); err != nil {
		t.Fatal(err)
	}
	records, err := store.Records(context.Background(), identity.Id())
	if err != nil || len(records) != 0 {
		t.Fatal("old cache retained indefinitely", records, err)
	}
}

func TestTimestampFormatsAndOrderedSignatures(t *testing.T) {
	for _, value := range []string{"2026-10-06T12:00:00Z", "2026-10-06T20:00:00+08:00", "2026-10-06"} {
		if _, ok := timestamp(value); !ok {
			t.Fatal(value)
		}
	}
	if _, ok := timestamp(float64(1000)); ok {
		t.Fatal("numeric transcript timestamp accepted")
	}
	if orderedSignature([]byte(`{"a":1e1,"b":2}`)) != `{"a":10,"b":2}` {
		t.Fatal("numeric signature not normalized")
	}
	if orderedSignature([]byte(`{"a":1,"b":2}`)) == orderedSignature([]byte(`{"b":2,"a":1}`)) {
		t.Fatal("property order discarded")
	}
}

func TestSameSizeRewriteCancellationAndFailedRootPreserveCache(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "projects", "session.jsonl")
	writeHistory(t, path, claudeUsage("old")+"\n")
	first, err := ReadTranscript(context.Background(), path, domain.Claude, domain.Checkpoint{})
	if err != nil {
		t.Fatal(err)
	}
	writeHistory(t, path, claudeUsage("new")+"\n")
	second, err := ReadTranscript(context.Background(), path, domain.Claude, first.Checkpoint)
	if err != nil || second.IsResumed || second.Records[0].DedupeKey != "new:request" {
		t.Fatal(second, err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ReadTranscript(cancelled, path, domain.Claude, second.Checkpoint); err == nil {
		t.Fatal("cancelled read completed")
	}
	store, err := storage.Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	collector := Collector{Store: store, HostId: "host"}
	root := Root{Provider: domain.Claude, Home: home}
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	sources, err := collector.Scan(context.Background(), []Root{root}, now)
	if err != nil {
		t.Fatal(err)
	}
	identity := sources[0].Fingerprint
	if err := os.RemoveAll(filepath.Join(home, "projects")); err != nil {
		t.Fatal(err)
	}
	writeHistory(t, filepath.Join(home, "projects"), "not a directory")
	sources, err = collector.Scan(context.Background(), []Root{root}, now.Add(time.Minute))
	if err != nil || sources[0].Status != "failed" || len(sources[0].Records) != 1 || sources[0].Fingerprint != identity {
		t.Fatal("failed root replaced cached facts", sources, err)
	}
	observation, err := store.Observation(context.Background(), identity.Id())
	if err != nil || observation.SucceededAt == nil || *observation.SucceededAt != now.UnixMilli() {
		t.Fatal(observation, err)
	}
}

func TestPersistentResumeAndRetentionBoundary(t *testing.T) {
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "usage.sqlite")
	path := filepath.Join(directory, "history.jsonl")
	store, err := storage.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.RegisterSource(context.Background(), domain.SourceFingerprint{HostId: "host", Provider: domain.Codex, ResolvedHomePath: directory})
	if err != nil {
		t.Fatal(err)
	}
	data := sessionMeta + "\n" + turnContext + "\n" + codexUsage(10, "2026-10-06T12:00:01Z") + "\n"
	writeHistory(t, path, data)
	read, err := ReadTranscript(context.Background(), path, domain.Codex, domain.Checkpoint{SourceId: id})
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := store.CommitFileWithTail(context.Background(), read.Checkpoint, read.Records, read.Tail, true)
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	store, err = storage.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	checkpoint, err = store.Checkpoint(context.Background(), id, path)
	if err != nil {
		t.Fatal(err)
	}
	writeHistory(t, path, data+codexUsage(11, "2026-10-06T12:00:02Z")+"\n")
	appended, err := ReadTranscript(context.Background(), path, domain.Codex, checkpoint)
	if err != nil || !appended.IsResumed || len(appended.Records) != 1 || appended.Records[0].SessionId != "session" {
		t.Fatal(appended, err)
	}
	if err := store.PruneHistory(context.Background(), id, checkpoint.ModifiedNs); err != nil {
		t.Fatal(err)
	}
	records, err := store.Records(context.Background(), id)
	if err != nil || len(records) != 1 {
		t.Fatal("equal retention cutoff pruned", records, err)
	}
	if err := store.PruneHistory(context.Background(), id, checkpoint.ModifiedNs+1); err != nil {
		t.Fatal(err)
	}
	records, err = store.Records(context.Background(), id)
	if err != nil || len(records) != 0 {
		t.Fatal(records, err)
	}
}

func TestDirectoryIdentityAndHomeExpansion(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	id := directoryVolumeId(root)
	if id == "" {
		t.Fatal("directory identity missing")
	}
	t.Log("Repository dev:ino identity", id)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	expanded, err := ExpandHome("~/~")
	if err != nil || expanded != filepath.Join(home, "~") {
		t.Fatal(expanded, err)
	}
}

func TestTailOnlySourceSurvivesRecreationAndNewAlias(t *testing.T) {
	home := t.TempDir()
	directory := filepath.Join(home, "projects")
	writeHistory(t, filepath.Join(directory, "tail.jsonl"), claudeUsage("tail"))
	store, err := storage.Open(filepath.Join(t.TempDir(), "tail.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	collector := Collector{Store: store, HostId: "host"}
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	root := Root{Provider: domain.Claude, Home: home}
	sources, err := collector.Scan(context.Background(), []Root{root}, now)
	if err != nil || len(sources[0].Records) != 1 {
		t.Fatal(sources, err)
	}
	identity := sources[0].Fingerprint
	if err := os.Rename(directory, directory+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	sources, err = collector.Scan(context.Background(), []Root{root}, now)
	if err != nil || sources[0].Fingerprint != identity || len(sources[0].Records) != 1 {
		t.Fatal("tail-only cache lost its source", sources, err)
	}
	aliasHome := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(home, aliasHome); err != nil {
		t.Log("Directory symlink unavailable; recreation assertion already verified", err)
		return
	}
	sources, err = collector.Scan(context.Background(), []Root{{Provider: domain.Claude, Home: aliasHome}, root}, now)
	if err != nil || len(sources) != 1 || sources[0].Fingerprint != identity || len(sources[0].Records) != 1 {
		t.Fatal("new alias duplicated or lost retained history", sources, err)
	}
}

func TestClaudeContentBlockDuplicatesStayOutOfPersistentFacts(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "projects", "session.jsonl")
	line := claudeUsage("message")
	writeHistory(t, path, line+"\n"+line+"\n"+line)
	store, err := storage.Open(filepath.Join(t.TempDir(), "claude.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	collector := Collector{Store: store, HostId: "host"}
	root := Root{Provider: domain.Claude, Home: home}
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	sources, err := collector.Scan(context.Background(), []Root{root}, now)
	if err != nil || len(sources[0].Records) != 1 {
		t.Fatal("content blocks persisted repeats", sources, err)
	}
	writeHistory(t, path, line+"\n"+line+"\n"+line+"\n"+claudeUsage("new")+"\n")
	sources, err = collector.Scan(context.Background(), []Root{root}, now)
	if err != nil || len(sources[0].Records) != 2 {
		t.Fatal("append repeated already committed message", sources, err)
	}
	result, err := accounting.Aggregate(sources[0].Records, accounting.AggregateOptions{Window: accounting.Window{SinceDay: "2026-10-06", UntilDay: "2026-10-06", TimeZone: "UTC"}})
	if err != nil || result.DuplicatesDropped != 0 || result.Buckets[0].CostUsd != 1 {
		t.Fatal(result, err)
	}
}
