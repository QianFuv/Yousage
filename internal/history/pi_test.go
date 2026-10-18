package history

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"yousage/internal/domain"
	"yousage/internal/storage"
)

// TestPiTranscriptAccounting preserves disjoint counters, response models and auxiliary billing.
func TestPiTranscriptAccounting(test *testing.T) {
	path := filepath.Join(test.TempDir(), "pi.jsonl")
	data := `{"type":"session","id":"pi-session"}
{"type":"message","id":"first","parentId":null,"timestamp":"2026-10-18T10:00:00Z","message":{"role":"assistant","provider":"anthropic","model":"alias","responseModel":"claude-test","content":[{"text":"PRIVATE CONTENT"}],"usage":{"input":70,"cacheRead":20,"cacheWrite":10,"output":40,"reasoning":90,"cost":{"total":0}}}}
{"type":"message","id":"tool","parentId":"first","timestamp":"2026-10-18T10:00:01Z","message":{"role":"toolResult","usage":{"input":2,"output":3,"cost":{"total":0.5}}}}
{"type":"compaction","id":"compact","parentId":"tool","timestamp":"2026-10-18T10:00:02Z","summary":"PRIVATE SUMMARY","usage":{"input":4,"output":5}}
{"type":"usage","id":"extra","parentId":"compact","timestamp":"2026-10-18T10:00:03Z","provider":"deepseek","model":"deepseek-test","usage":{"input":6,"output":7,"cost":{"total":1.25}}}
{"type":"branch_summary","id":"branch","parentId":"first","timestamp":"2026-10-18T10:00:04Z","usage":{"input":8,"output":9}}
{"type":"message","id":"ignored","timestamp":"2026-10-18T10:00:05Z","message":{"role":"user","content":"PRIVATE PROMPT","usage":{"input":999}}}
`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		test.Fatal(err)
	}
	result, err := ReadTranscript(context.Background(), path, domain.Provider("pi"), domain.Checkpoint{})
	if err != nil {
		test.Fatal(err)
	}
	if len(result.Records) != 5 {
		test.Fatalf("lost Pi usage entries: %+v", result)
	}
	first := result.Records[0]
	if first.Model != "anthropic/claude-test" || first.RateModel != "claude-test" || first.SessionId != "pi-session" || first.Totals != (domain.Tokens{UncachedInputTokens: 70, CachedInputTokens: 20, CacheCreationTokens: 10, OutputTokens: 40, ReasoningTokens: 40}) || first.ReportedCostUsd != nil || first.GenerationMs != 0 || first.TtftMs != 0 {
		test.Fatalf("wrong Pi billing semantics: %+v", first)
	}
	if result.Records[1].Model != "Tools/summaries" || *result.Records[1].ReportedCostUsd != 0.5 || result.Records[3].Model != "deepseek/deepseek-test" || *result.Records[3].ReportedCostUsd != 1.25 {
		test.Fatalf("auxiliary or backend costs lost: %+v", result.Records)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "PRIVATE") {
		test.Fatal("transcript text leaked into persisted facts/checkpoint")
	}
	for _, record := range result.Records {
		if err := record.Validate(); err != nil {
			test.Fatal(record, err)
		}
	}
}

// TestPiCollectorRestart restores durable checkpoints and continues reading an arbitrary sessions directory.
func TestPiCollectorRestart(test *testing.T) {
	ctx := context.Background()
	directory := test.TempDir()
	path := filepath.Join(directory, "session.jsonl")
	database := filepath.Join(test.TempDir(), "state.sqlite")
	first := `{"type":"message","id":"first","timestamp":"2026-10-18T10:00:00Z","message":{"role":"assistant","provider":"backend","model":"fixture","usage":{"output":5}}}`
	second := `{"type":"message","id":"second","timestamp":"2026-10-18T10:01:00Z","message":{"role":"assistant","provider":"backend","model":"fixture","usage":{"output":6}}}`
	writeHistory(test, path, `{"type":"session","id":"session"}`+"\n"+first+"\n")
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	var sourceId string
	for iteration := 0; iteration < 2; iteration++ {
		store, err := storage.Open(database)
		if err != nil {
			test.Fatal(err)
		}
		if iteration == 1 {
			file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				test.Fatal(err)
			}
			_, err = file.WriteString(first + "\n{bad json}\n" + second + "\n")
			file.Close()
			if err != nil {
				test.Fatal(err)
			}
		}
		results, err := (Collector{Store: store, HostId: "fixture"}).Synchronize(ctx, []Root{{Provider: domain.Pi, Home: directory}, {Provider: domain.Pi, Home: directory}}, now)
		if err != nil || len(results) != 1 {
			store.Close()
			test.Fatal(results, err)
		}
		currentId := results[0].Fingerprint.Id()
		records, err := store.Records(ctx, currentId)
		if err != nil || len(records) != iteration+1 || iteration == 1 && (sourceId != currentId || results[0].MalformedLines != 1) {
			store.Close()
			test.Fatal("durable Pi history was lost, duplicated or reidentified", records, results, err)
		}
		sourceId = currentId
		store.Close()
	}
}

// TestPiPinnedAccountingFixture shares independently expected billing fields with the upstream oracle.
func TestPiPinnedAccountingFixture(test *testing.T) {
	data, err := os.ReadFile("../../tools/parity/fixtures/pi-usage.json")
	if err != nil {
		test.Fatal(err)
	}
	var fixture struct {
		Entries  []json.RawMessage `json:"entries"`
		Expected []struct {
			Model      string   `json:"model"`
			RateModel  string   `json:"rateModel"`
			Input      int64    `json:"input"`
			CacheRead  int64    `json:"cacheRead"`
			CacheWrite int64    `json:"cacheWrite"`
			Output     int64    `json:"output"`
			Reasoning  int64    `json:"reasoning"`
			Cost       *float64 `json:"cost"`
		} `json:"expected"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		test.Fatal(err)
	}
	state := PiState{TranscriptId: "fixture"}
	records := []domain.Record{}
	for _, entry := range fixture.Entries {
		if record := ParsePi(entry, &state); record != nil {
			records = append(records, *record)
		}
	}
	if len(records) != len(fixture.Expected) {
		test.Fatal("native Pi entries lost", records)
	}
	for index, expected := range fixture.Expected {
		actual := records[index]
		if actual.Model != expected.Model || actual.RateModel != expected.RateModel || actual.Totals != (domain.Tokens{UncachedInputTokens: expected.Input, CachedInputTokens: expected.CacheRead, CacheCreationTokens: expected.CacheWrite, OutputTokens: expected.Output, ReasoningTokens: expected.Reasoning}) || actual.SessionId != "pi-parity" || (actual.ReportedCostUsd == nil) != (expected.Cost == nil) || expected.Cost != nil && *actual.ReportedCostUsd != *expected.Cost {
			test.Fatalf("fixture %d differs: %+v", index, actual)
		}
	}
}

// TestPiAppendTailRewrite preserves replay boundaries and context identity through native session edits.
func TestPiAppendTailRewrite(test *testing.T) {
	path := filepath.Join(test.TempDir(), "pi.jsonl")
	header := `{"type":"session","id":"session"}` + "\n"
	first := `{"type":"message","id":"first","parentId":null,"timestamp":"2026-10-18T10:00:00Z","message":{"role":"assistant","provider":"anthropic","model":"model","usage":{"output":5}}}`
	boundary := `{"type":"compaction","id":"compact","parentId":"first","timestamp":"2026-10-18T10:01:00Z"}`
	second := `{"type":"message","id":"second","parentId":"compact","timestamp":"2026-10-18T10:02:00Z","message":{"role":"assistant","provider":"anthropic","model":"model","usage":{"output":6}}}`
	writeHistory(test, path, header+first+"\n")
	initial, err := ReadTranscript(context.Background(), path, domain.Provider("pi"), domain.Checkpoint{})
	if err != nil || len(initial.Records) != 1 {
		test.Fatal(initial, err)
	}
	writeHistory(test, path, header+first+"\n"+boundary+"\n"+second)
	appended, err := ReadTranscript(context.Background(), path, domain.Provider("pi"), initial.Checkpoint)
	if err != nil || !appended.IsResumed || len(appended.Tail) != 1 || appended.Tail[0].ContextId == initial.Records[0].ContextId {
		test.Fatal("Pi append/compaction boundary lost", appended, err)
	}
	writeHistory(test, path, header+first+"\n"+boundary+"\n"+second+"\n")
	completed, err := ReadTranscript(context.Background(), path, domain.Provider("pi"), appended.Checkpoint)
	if err != nil || !completed.IsResumed || len(completed.Records) != 1 || completed.Records[0].DedupeKey != appended.Tail[0].DedupeKey {
		test.Fatal("Pi EOF replay changed identity", completed, err)
	}
	writeHistory(test, path, header+first+"\n")
	rewritten, err := ReadTranscript(context.Background(), path, domain.Provider("pi"), completed.Checkpoint)
	if err != nil || rewritten.IsResumed || len(rewritten.Records) != 1 || rewritten.Records[0].DedupeKey != initial.Records[0].DedupeKey {
		test.Fatal("Pi rewrite failed", rewritten, err)
	}
}
