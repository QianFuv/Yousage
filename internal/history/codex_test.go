package history

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"yousage/internal/domain"
	"yousage/internal/storage"
)

// TestCodexPinnedAccountingFixture checks request reconciliation against hand-calculated usage.
func TestCodexPinnedAccountingFixture(test *testing.T) {
	data, err := os.ReadFile("../../tools/parity/fixtures/codex-usage.json")
	if err != nil {
		test.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name             string            `json:"name"`
			Entries          []json.RawMessage `json:"entries"`
			Expected         []domain.Tokens   `json:"expected"`
			MalformedRecords int               `json:"malformedRecords"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		test.Fatal(err)
	}
	for _, scenario := range fixture.Cases {
		test.Run(scenario.Name, func(test *testing.T) {
			state := InitialCodexState()
			actual := []domain.Tokens{}
			lines := []string{}
			for _, entry := range scenario.Entries {
				compact, _ := json.Marshal(entry)
				lines = append(lines, string(compact))
				if record := ParseCodex(entry, &state); record != nil {
					if err := record.Validate(); err != nil {
						test.Fatal(err)
					}
					actual = append(actual, record.Totals)
				}
			}
			if !reflect.DeepEqual(actual, scenario.Expected) {
				test.Fatalf("usage = %+v, want %+v", actual, scenario.Expected)
			}
			path := filepath.Join(test.TempDir(), "codex.jsonl")
			writeHistory(test, path, strings.Join(lines, "\n")+"\n")
			read, err := ReadTranscript(context.Background(), path, domain.Codex, domain.Checkpoint{})
			if err != nil || len(read.Records) != len(scenario.Expected) {
				test.Fatalf("projected usage lost reconciliation: %+v, %v", read, err)
			}
			var checkpoint struct {
				MalformedRecords int `json:"malformedRecords"`
			}
			if err := json.Unmarshal(read.Checkpoint.Reducer, &checkpoint); err != nil || checkpoint.MalformedRecords != scenario.MalformedRecords {
				test.Fatalf("malformed records = %d, want %d: %v", checkpoint.MalformedRecords, scenario.MalformedRecords, err)
			}
			for index, record := range read.Records {
				if record.Totals != scenario.Expected[index] {
					test.Fatalf("projected record %d = %+v, want %+v", index, record.Totals, scenario.Expected[index])
				}
			}
		})
	}
}

// TestCodexCumulativeCheckpointResume preserves reconciliation and diagnostics across cached scans.
func TestCodexCumulativeCheckpointResume(test *testing.T) {
	ctx := context.Background()
	home := test.TempDir()
	path := filepath.Join(home, "sessions", "codex.jsonl")
	first := `{"type":"event_msg","timestamp":"2026-10-29T12:00:01Z","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":10,"output_tokens":4},"total_token_usage":{"input_tokens":10,"output_tokens":4}}}}`
	invalid := `{"type":"event_msg","timestamp":"2026-10-29T12:00:02Z","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":10,"output_tokens":4,"reasoning_output_tokens":5},"total_token_usage":{"input_tokens":20,"output_tokens":8}}}}`
	invalidTail := strings.Replace(invalid, `,"total_token_usage":{"input_tokens":20,"output_tokens":8}`, "", 1)
	next := `{"type":"event_msg","timestamp":"2026-10-29T12:00:03Z","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":10,"output_tokens":4},"total_token_usage":{"input_tokens":30,"output_tokens":12}}}}`
	data := turnContext + "\n" + first + "\n" + invalid + "\n"
	writeHistory(test, path, data)
	store, err := storage.Open(filepath.Join(test.TempDir(), "state.sqlite"))
	if err != nil {
		test.Fatal(err)
	}
	defer store.Close()
	collector := Collector{Store: store, HostId: "fixture"}
	sourceId := ""
	for iteration := 0; iteration < 6; iteration++ {
		if iteration == 2 {
			writeHistory(test, path, data+next+"\n")
		}
		if iteration == 3 {
			writeHistory(test, path, data+next+"\n"+invalidTail)
		}
		if iteration == 5 {
			writeHistory(test, path, data+next+"\n"+invalidTail+"\n")
		}
		results, err := collector.Scan(ctx, []Root{{Provider: domain.Codex, Home: home}}, time.Date(2026, 10, 29, 13, 0, 0, 0, time.UTC))
		if err != nil || len(results) != 1 {
			test.Fatal(results, err)
		}
		sourceId = results[0].Fingerprint.Id()
		wantRecords := 1
		if iteration >= 2 {
			wantRecords = 2
		}
		wantMalformed := 1
		if iteration >= 3 {
			wantMalformed = 2
		}
		if len(results[0].Records) != wantRecords || results[0].MalformedRecords != wantMalformed || results[0].MalformedLines != 0 {
			test.Fatalf("scan %d lost cumulative accounting or diagnostics: %+v", iteration, results[0])
		}
	}
	checkpoints, err := store.Checkpoints(ctx, sourceId)
	if err != nil || len(checkpoints) != 1 {
		test.Fatal(checkpoints, err)
	}
	writeHistory(test, path, data+next+"\n"+invalidTail+"\n"+next+"\n")
	read, err := ReadTranscript(ctx, path, domain.Codex, checkpoints[0])
	if err != nil || !read.IsResumed || len(read.Records) != 0 {
		test.Fatal("checkpoint did not suppress cumulative re-emission", read, err)
	}
}
