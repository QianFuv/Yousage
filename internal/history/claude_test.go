package history

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"yousage/internal/domain"
	"yousage/internal/storage"
)

// TestClaudeSyntheticUsageIsExcluded removes local placeholders even when they carry tokens and cost.
func TestClaudeSyntheticUsageIsExcluded(test *testing.T) {
	synthetic := strings.Replace(claudeUsage("synthetic"), `"model":"fixture"`, `"model":"<synthetic>"`, 1)
	if record := ParseClaude([]byte(synthetic)); record != nil {
		test.Fatal("local placeholder counted as API usage", record)
	}
	ctx := context.Background()
	home := test.TempDir()
	path := filepath.Join(home, "projects", "claude.jsonl")
	writeHistory(test, path, claudeUsage("before")+"\n"+synthetic+"\n"+claudeUsage("after")+"\n")
	store, err := storage.Open(filepath.Join(test.TempDir(), "state.sqlite"))
	if err != nil {
		test.Fatal(err)
	}
	defer store.Close()
	collector := Collector{Store: store, HostId: "fixture"}
	roots := []Root{{Provider: domain.Claude, Home: home}}
	now := time.Date(2026, 10, 29, 13, 0, 0, 0, time.UTC)
	results, err := collector.Scan(ctx, roots, now)
	if err != nil || len(results) != 1 || len(results[0].Records) != 2 {
		test.Fatal("projected reader retained synthetic usage", results, err)
	}
	sourceId := results[0].Fingerprint.Id()
	checkpoints, err := store.Checkpoints(ctx, sourceId)
	if err != nil || len(checkpoints) != 1 {
		test.Fatal(checkpoints, err)
	}
	checkpoint := checkpoints[0]
	checkpoint.ParserVersion = ParserVersion - 1
	oldRecords := append([]domain.Record{}, results[0].Records...)
	placeholder := oldRecords[0]
	placeholder.Model, placeholder.DedupeKey = "<synthetic>", "synthetic:request"
	oldRecords = append(oldRecords, placeholder)
	if _, err := store.CommitFile(ctx, checkpoint, oldRecords, true); err != nil {
		test.Fatal(err)
	}
	results, err = collector.Scan(ctx, roots, now)
	if err != nil || len(results) != 1 || len(results[0].Records) != 2 {
		test.Fatal("parser upgrade did not replace old synthetic facts", results, err)
	}
	for _, record := range results[0].Records {
		if record.Model == "<synthetic>" {
			test.Fatal("synthetic fact survived parser upgrade")
		}
	}
}
