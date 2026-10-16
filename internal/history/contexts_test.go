package history

import (
	"context"
	"path/filepath"
	"testing"
	"time"
	"yousage/internal/domain"
	"yousage/internal/storage"
)

// TestContextCheckpointReplayAndAppend persists context boundaries across upgrades, tails and scans.
func TestContextCheckpointReplayAndAppend(test *testing.T) {
	for _, provider := range []domain.Provider{domain.Codex, domain.Claude} {
		test.Run(string(provider), func(test *testing.T) {
			ctx := context.Background()
			home := test.TempDir()
			directory, data := "projects", claudeUsage("before")+"\n"
			boundary := `{"type":"system","subtype":"compact_boundary","timestamp":"2026-10-06T12:01:00Z"}`
			usage, nextUsage := claudeUsage("after"), claudeUsage("next")
			if provider == domain.Codex {
				directory = "sessions"
				data = sessionMeta + "\n" + turnContext + "\n" + codexUsage(10, "2026-10-06T12:00:01Z") + "\n"
				boundary = `{"type":"compacted","timestamp":"2026-10-06T12:01:00Z","payload":{}}`
				usage, nextUsage = codexUsage(11, "2026-10-06T12:02:00Z"), codexUsage(12, "2026-10-06T12:03:00Z")
			}
			path := filepath.Join(home, directory, "context.jsonl")
			writeHistory(test, path, data)
			store, err := storage.Open(filepath.Join(test.TempDir(), "state.sqlite"))
			if err != nil {
				test.Fatal(err)
			}
			defer store.Close()
			collector := Collector{Store: store, HostId: "fixture"}
			roots := []Root{{Provider: provider, Home: home}}
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
			checkpoint.ParserVersion = ParserVersion - 1
			records[0].ContextId = ""
			checkpoint, err = store.CommitFile(ctx, checkpoint, records, true)
			if err != nil {
				test.Fatal(err)
			}
			if _, err := collector.Synchronize(ctx, roots, now); err != nil {
				test.Fatal(err)
			}
			upgraded, _ := store.Checkpoint(ctx, id, path)
			records, _ = store.Records(ctx, id)
			if upgraded.ParserVersion != ParserVersion || upgraded.Revision != checkpoint.Revision+1 || len(records) != 1 || records[0].ContextId != "context.jsonl" {
				test.Fatal(upgraded, records)
			}
			data += boundary + "\n"
			writeHistory(test, path, data)
			if _, err := collector.Synchronize(ctx, roots, now); err != nil {
				test.Fatal(err)
			}
			data += usage
			writeHistory(test, path, data)
			if _, err := collector.Synchronize(ctx, roots, now); err != nil {
				test.Fatal(err)
			}
			data += "\n" + nextUsage + "\n"
			writeHistory(test, path, data)
			if _, err := collector.Synchronize(ctx, roots, now); err != nil {
				test.Fatal(err)
			}
			records, err = store.Records(ctx, id)
			if err != nil || len(records) != 3 || records[0].ContextId == records[1].ContextId || records[1].ContextId != records[2].ContextId {
				test.Fatal("context lost across incremental replay", records, err)
			}
		})
	}
}
