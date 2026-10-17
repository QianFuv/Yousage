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

// TestClaudeIncrementalFinalBlock preserves usage and timing across scan and EOF boundaries.
func TestClaudeIncrementalFinalBlock(t *testing.T) {
	for _, ending := range []string{"\n", ""} {
		t.Run("ending="+ending, func(t *testing.T) {
			ctx := context.Background()
			home := t.TempDir()
			path := filepath.Join(home, "projects", "session.jsonl")
			lines := []string{
				claudeEntry("user", "prompt", "", "2026-10-06T12:00:00Z"),
				claudeTimedBlock("thinking", "prompt", "message", "2026-10-06T12:00:05Z", 0, 3000),
				claudeTimedBlock("answer", "thinking", "message", "2026-10-06T12:00:09Z", 1, 0),
			}
			database, err := storage.Open(filepath.Join(t.TempDir(), "test.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			collector := Collector{Store: database, HostId: "test"}
			roots := []Root{{Provider: domain.Claude, Home: home}}
			now := time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)
			writeHistory(t, path, strings.Join(lines[:2], "\n")+"\n")
			if _, err := collector.Scan(ctx, roots, now); err != nil {
				t.Fatal(err)
			}
			writeHistory(t, path, strings.Join(lines, "\n")+ending)
			scanned, err := collector.Scan(ctx, roots, now)
			if err != nil {
				t.Fatal(err)
			}
			if len(scanned) != 1 || len(scanned[0].Records) != 1 {
				t.Fatal(scanned)
			}
			if record := scanned[0].Records[0]; record.GenerationMs != 9000 || record.Totals.OutputTokens != 900 || record.TtftMs != 2000 {
				t.Fatalf("final block not retained: %+v", record)
			}
			if ending == "" {
				writeHistory(t, path, strings.Join(lines[:2], "\n")+"\n"+strings.Repeat(" ", len(lines[2])+20)+"broken\n")
				scanned, err = collector.Scan(ctx, roots, now)
				if err != nil {
					t.Fatal(err)
				}
				if len(scanned[0].Records) != 1 || scanned[0].Records[0].GenerationMs != 5000 {
					t.Fatalf("rewritten EOF did not restore committed block: %+v", scanned)
				}
			}
		})
	}
}
