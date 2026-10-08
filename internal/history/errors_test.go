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

func TestClaudeErrorMarkersAndRedaction(t *testing.T) {
	for _, test := range []struct{ marker, errorValue, kind string }{
		{`"subtype":"api_error"`, `"rate_limit"`, "rateLimit"},
		{`"isApiErrorMessage":true`, `"server_error"`, "server"},
		{`"isApiErrorMessage":true`, `"authentication_failed"`, "auth"},
		{`"subtype":"api_error"`, `{"status":529,"message":"synthetic overloaded response"}`, "overloaded"},
		{`"subtype":"api_error"`, `{"status":429,"message":"synthetic diagnostic secret"}`, "rateLimit"},
		{`"subtype":"api_error"`, `{"status":503}`, "server"},
		{`"subtype":"api_error"`, `"unrecognized synthetic secret"`, "other"},
	} {
		data := `{"type":"assistant","timestamp":"2026-10-06T12:00:00Z","sessionId":"session",` + test.marker + `,"error":` + test.errorValue + `,"message":{"model":"fixture","content":[{"text":"synthetic private prompt"}],"usage":{"input_tokens":999999}}}`
		record := ParseClaude([]byte(data))
		if record == nil || record.ErrorKind != test.kind || record.Totals != (domain.Tokens{}) || record.Validate() != nil {
			t.Fatal(record)
		}
		path := filepath.Join(t.TempDir(), "error.jsonl")
		writeHistory(t, path, data+"\n")
		read, err := ReadTranscript(context.Background(), path, domain.Claude, domain.Checkpoint{})
		if err != nil || len(read.Records) != 1 || read.Records[0].ErrorKind != test.kind {
			t.Fatal(read, err)
		}
	}
}

func TestCodexErrorMarkers(t *testing.T) {
	for _, test := range []struct{ event, kind string }{
		{`{"type":"turn_aborted","reason":"interrupted"}`, "interrupted"},
		{`{"type":"turn_aborted"}`, "aborted"},
		{`{"type":"error","message":"stream disconnected before completion"}`, "streamDisconnected"},
		{`{"type":"task_complete","error":{"codex_error_info":"other","message":"stream disconnected: synthetic message"}}`, "streamDisconnected"},
		{`{"type":"task_complete","error":{"message":"server_error"}}`, "server"},
		{`{"type":"error","message":"rate limit reached"}`, "rateLimit"},
		{`{"type":"error","message":"authentication_failed"}`, "auth"},
		{`{"type":"error","message":"overloaded"}`, "overloaded"},
		{`{"type":"error","message":"synthetic unknown failure"}`, "other"},
	} {
		data := `{"type":"event_msg","timestamp":"2026-10-06T12:00:00Z","payload":` + test.event + `}`
		path := filepath.Join(t.TempDir(), "error.jsonl")
		writeHistory(t, path, sessionMeta+"\n"+turnContext+"\n"+data+"\n")
		read, err := ReadTranscript(context.Background(), path, domain.Codex, domain.Checkpoint{})
		if err != nil || len(read.Records) != 1 || read.Records[0].ErrorKind != test.kind || read.Records[0].SessionId != "session" || read.Records[0].Model != "fixture" || read.Records[0].Validate() != nil {
			t.Fatal(read, err)
		}
	}
	rootPath := filepath.Join(t.TempDir(), "root-error.jsonl")
	writeHistory(t, rootPath, `{"type":"turn_aborted","timestamp":"2026-10-06T12:00:00Z","reason":"interrupted"}`+"\n")
	rootRead, rootError := ReadTranscript(context.Background(), rootPath, domain.Codex, domain.Checkpoint{})
	if rootError != nil || len(rootRead.Records) != 1 || rootRead.Records[0].ErrorKind != "interrupted" {
		t.Fatal(rootRead, rootError)
	}
	state := InitialCodexState()
	if ParseCodex([]byte(`{"type":"event_msg","timestamp":"2026-10-06T12:00:00Z","payload":{"type":"task_complete","error":null}}`), &state) != nil {
		t.Fatal("successful turn recorded as error")
	}
	if record := ParseCodex([]byte(`{"type":"turn_aborted","timestamp":"2026-10-06T12:00:00Z","reason":"interrupted"}`), &state); record == nil || record.ErrorKind != "interrupted" {
		t.Fatal(record)
	}
}

func TestCacheTtlAndClaudeErrorModelAcrossAppend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude.jsonl")
	data := `{"type":"assistant","timestamp":"2026-10-06T12:00:00Z","sessionId":"session","message":{"model":"fixture","usage":{"cache_creation_input_tokens":50000,"cache_creation":{"ephemeral_1h_input_tokens":50000}}}}` + "\n"
	writeHistory(t, path, data)
	read, err := ReadTranscript(context.Background(), path, domain.Claude, domain.Checkpoint{})
	if err != nil || len(read.Records) != 1 || read.Records[0].CacheTtlMs != time.Hour.Milliseconds() {
		t.Fatal(read, err)
	}
	writeHistory(t, path, data+`{"type":"system","subtype":"api_error","timestamp":"2026-10-06T12:01:00Z","error":"server_error"}`+"\n")
	appended, err := ReadTranscript(context.Background(), path, domain.Claude, read.Checkpoint)
	if err != nil || !appended.IsResumed || len(appended.Records) != 1 || appended.Records[0].Model != "fixture" || appended.Records[0].SessionId != "session" {
		t.Fatal(appended, err)
	}
}

func TestErrorParserUpgradeReplaysUnchangedFiles(t *testing.T) {
	for _, provider := range []domain.Provider{domain.Claude, domain.Codex} {
		for _, version := range []int{1, 2} {
			t.Run(string(provider)+string(rune('0'+version)), func(t *testing.T) {
				ctx := context.Background()
				home := t.TempDir()
				directory := "projects"
				data := claudeUsage("usage") + "\n" + `{"type":"system","subtype":"api_error","timestamp":"2026-10-06T12:01:00Z","sessionId":"session","error":"server_error"}`
				if provider == domain.Codex {
					directory = "sessions"
					data = sessionMeta + "\n" + turnContext + "\n" + codexUsage(10, "2026-10-06T12:00:01Z") + "\n" + `{"type":"event_msg","timestamp":"2026-10-06T12:01:00Z","payload":{"type":"turn_aborted","reason":"interrupted"}}`
				}
				path := filepath.Join(home, directory, "synthetic.jsonl")
				writeHistory(t, path, data+"\n")
				store, err := storage.Open(filepath.Join(t.TempDir(), "state.sqlite"))
				if err != nil {
					t.Fatal(err)
				}
				defer store.Close()
				collector := Collector{Store: store, HostId: "synthetic"}
				roots := []Root{{Provider: provider, Home: home}}
				now := time.Date(2026, 10, 6, 13, 0, 0, 0, time.UTC)
				results, err := collector.Synchronize(ctx, roots, now)
				if err != nil {
					t.Fatal(err)
				}
				id := results[0].Fingerprint.Id()
				checkpoint, _ := store.Checkpoint(ctx, id, path)
				records, _ := store.Records(ctx, id)
				checkpoint.ParserVersion = version
				checkpoint, err = store.CommitFile(ctx, checkpoint, records[:1], true)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := collector.Synchronize(ctx, roots, now); err != nil {
					t.Fatal(err)
				}
				actual, _ := store.Checkpoint(ctx, id, path)
				records, _ = store.Records(ctx, id)
				if actual.ParserVersion != ParserVersion || actual.Revision != checkpoint.Revision+1 || len(records) != 2 || records[1].ErrorKind == "" {
					t.Fatal(actual, records)
				}
				if _, err := collector.Synchronize(ctx, roots, now); err != nil {
					t.Fatal(err)
				}
				stable, _ := store.Checkpoint(ctx, id, path)
				if stable.Revision != actual.Revision {
					t.Fatal("upgraded file reread twice")
				}
				encoded, _ := json.Marshal(records[1])
				if len(encoded) > 400 {
					t.Fatal("error persisted unexpected fields")
				}
			})
		}
	}
}
