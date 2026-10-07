package history

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
	"yousage/internal/accounting"
	"yousage/internal/domain"
	"yousage/internal/quota"
	"yousage/internal/storage"
)

// TestQuotaReparseHorizon preserves old checkpoints until their files change.
func TestQuotaReparseHorizon(test *testing.T) {
	ctx := context.Background()
	now := time.Now()
	for _, age := range []time.Duration{quota.CycleFitHorizon + time.Hour, quota.CycleFitHorizon - time.Hour} {
		home := test.TempDir()
		path := filepath.Join(home, "sessions", "quota.jsonl")
		writeHistory(test, path, turnContext+"\n"+quotaEvent("codex", nil, nil)+"\n")
		modified := now.Add(-age)
		if err := os.Chtimes(path, modified, modified); err != nil {
			test.Fatal(err)
		}
		store, err := storage.Open(filepath.Join(test.TempDir(), "state.sqlite"))
		if err != nil {
			test.Fatal(err)
		}
		defer store.Close()
		collector := Collector{Store: store, HostId: "fixture"}
		roots := []Root{{Provider: domain.Codex, Home: home}}
		results, err := collector.Synchronize(ctx, roots, now)
		if err != nil {
			test.Fatal(err)
		}
		id := results[0].Fingerprint.Id()
		checkpoint, _ := store.Checkpoint(ctx, id, path)
		checkpoint.ParserVersion = 1
		checkpoint, err = store.CommitFile(ctx, checkpoint, nil, true)
		if err != nil {
			test.Fatal(err)
		}
		if _, err := collector.Synchronize(ctx, roots, now); err != nil {
			test.Fatal(err)
		}
		actual, _ := store.Checkpoint(ctx, id, path)
		if age > quota.CycleFitHorizon {
			if actual.Revision != checkpoint.Revision || actual.ParserVersion != 1 {
				test.Fatal("old unchanged file was reparsed")
			}
			writeHistory(test, path, turnContext+"\n"+quotaEvent("codex", nil, nil)+"\n{}\n")
			if _, err := collector.Synchronize(ctx, roots, now); err != nil {
				test.Fatal(err)
			}
			actual, _ = store.Checkpoint(ctx, id, path)
		}
		if actual.ParserVersion != ParserVersion || actual.Revision != checkpoint.Revision+1 {
			test.Fatal("eligible or later modified file was not reparsed")
		}
	}
}

// quotaEvent supplies projected quota facts with optional billable usage.
func quotaEvent(bucket string, info any, secondary any) string {
	data, _ := json.Marshal(map[string]any{"type": "event_msg", "timestamp": "2026-10-06T12:00:00Z", "payload": map[string]any{"type": "token_count", "info": info, "rate_limits": map[string]any{"limit_id": bucket, "plan_type": "plus", "primary": map[string]any{"used_percent": 10.0, "window_minutes": 300, "resets_at": 1791288000}, "secondary": secondary}}})
	return string(data)
}

// TestCodexQuotaExtractionPreservesUsage verifies all event variants and aggregate invariance.
func TestCodexQuotaExtractionPreservesUsage(test *testing.T) {
	info := map[string]any{"last_token_usage": map[string]any{"input_tokens": 10, "output_tokens": 4}}
	secondary := map[string]any{"used_percent": 20.5, "window_minutes": 10080, "resets_at": 1791888000}
	state := InitialCodexState()
	quotaOnly := ParseCodex([]byte(quotaEvent("codex", nil, nil)), &state)
	if quotaOnly == nil || !quotaOnly.IsQuotaOnly || len(quotaOnly.Quota.Windows) != 1 || quotaOnly.Validate() != nil {
		test.Fatal(quotaOnly)
	}
	ParseCodex([]byte(turnContext), &state)
	usage := ParseCodex([]byte(quotaEvent("codex", info, secondary)), &state)
	if usage == nil || usage.IsQuotaOnly || usage.QuotaBucket != "codex" || usage.Quota.PlanType != "plus" || len(usage.Quota.Windows) != 2 || usage.Quota.Windows[0].Id != "primary" || usage.Quota.Windows[1].Id != "secondary" || usage.Quota.Windows[1].UsedPercent != 20.5 {
		test.Fatal(usage)
	}
	duplicate := ParseCodex([]byte(quotaEvent("codex", info, nil)), &state)
	if duplicate == nil || !duplicate.IsQuotaOnly {
		test.Fatal("duplicate usage lost its quota observation", duplicate)
	}
	other := ParseCodex([]byte(quotaEvent("codex_bengalfox", info, nil)), &state)
	if other == nil || !other.IsQuotaOnly || other.QuotaBucket != "codex_bengalfox" {
		test.Fatal("usage mixed buckets", other)
	}
	info["last_token_usage"] = map[string]any{"input_tokens": 11, "output_tokens": 4}
	otherUsage := ParseCodex([]byte(quotaEvent("codex_bengalfox", info, nil)), &state)
	if otherUsage == nil || otherUsage.IsQuotaOnly || otherUsage.QuotaBucket != "codex_bengalfox" {
		test.Fatal("usage bucket attribution was lost", otherUsage)
	}
	options := accounting.AggregateOptions{Window: accounting.Window{SinceDay: "2026-10-06", UntilDay: "2026-10-06", TimeZone: "UTC"}, Rates: accounting.RateTable{"fixture": accounting.ModelRate{TokenRates: accounting.TokenRates{Input: 1, Output: 2}}}}
	baseline, _ := accounting.Aggregate([]domain.Record{*usage, *otherUsage}, options)
	actual, _ := accounting.Aggregate([]domain.Record{*quotaOnly, *usage, *duplicate, *other, *otherUsage}, options)
	if !reflect.DeepEqual(actual, baseline) {
		test.Fatalf("quota-only records changed usage: got %+v want %+v", actual, baseline)
	}
	path := filepath.Join(test.TempDir(), "quota.jsonl")
	writeHistory(test, path, quotaEvent("codex", nil, secondary)+"\n")
	read, err := ReadTranscript(context.Background(), path, domain.Codex, domain.Checkpoint{})
	if err != nil || len(read.Records) != 1 || len(read.Records[0].Quota.Windows) != 2 {
		test.Fatal("projection removed quota fields", read, err)
	}
}

// TestQuotaParserVersionReplacesOnceAndResumes proves atomic migration and incremental replay.
func TestQuotaParserVersionReplacesOnceAndResumes(test *testing.T) {
	ctx := context.Background()
	home := test.TempDir()
	path := filepath.Join(home, "sessions", "quota.jsonl")
	data := turnContext + "\n" + quotaEvent("codex", nil, nil) + "\n"
	writeHistory(test, path, data)
	store, err := storage.Open(filepath.Join(test.TempDir(), "state.sqlite"))
	if err != nil {
		test.Fatal(err)
	}
	defer store.Close()
	collector := Collector{Store: store, HostId: "fixture"}
	root := Root{Provider: domain.Codex, Home: home}
	now := time.Date(2026, 10, 6, 13, 0, 0, 0, time.UTC)
	sources, err := collector.Synchronize(ctx, []Root{root}, now)
	if err != nil {
		test.Fatal(err)
	}
	id := sources[0].Fingerprint.Id()
	checkpoint, _ := store.Checkpoint(ctx, id, path)
	checkpoint.ParserVersion = ParserVersion - 1
	checkpoint, err = store.CommitFile(ctx, checkpoint, nil, true)
	if err != nil {
		test.Fatal(err)
	}
	if _, err := collector.Synchronize(ctx, []Root{root}, now); err != nil {
		test.Fatal(err)
	}
	reparsed, _ := store.Checkpoint(ctx, id, path)
	records, _ := store.Records(ctx, id)
	if reparsed.Revision != checkpoint.Revision+1 || reparsed.ParserVersion != ParserVersion || len(records) != 1 {
		test.Fatal("migration did not replace", reparsed, records)
	}
	if _, err := collector.Synchronize(ctx, []Root{root}, now); err != nil {
		test.Fatal(err)
	}
	unchanged, _ := store.Checkpoint(ctx, id, path)
	if unchanged.Revision != reparsed.Revision {
		test.Fatal("unchanged file was reparsed")
	}
	writeHistory(test, path, data+quotaEvent("codex", nil, nil))
	tail, err := ReadTranscript(ctx, path, domain.Codex, reparsed)
	if err != nil || !tail.IsResumed || len(tail.Records) != 0 || len(tail.Tail) != 1 {
		test.Fatal("quota tail did not resume", tail, err)
	}
	if _, err := collector.Synchronize(ctx, []Root{root}, now); err != nil {
		test.Fatal(err)
	}
	if err := store.PruneHistory(ctx, id, now.AddDate(0, 0, 100).UnixNano()); err != nil {
		test.Fatal(err)
	}
	records, _ = store.Records(ctx, id)
	if len(records) != 0 {
		test.Fatal("observations escaped retention", records)
	}
}

// TestQuotaDedupeSeparatesBuckets preserves distinct observations in copied sessions.
func TestQuotaDedupeSeparatesBuckets(test *testing.T) {
	state := InitialCodexState()
	state.SessionId = "shared"
	main := *ParseCodex([]byte(quotaEvent("codex", nil, nil)), &state)
	other := *ParseCodex([]byte(quotaEvent("codex_bengalfox", nil, nil)), &state)
	files := DedupeMovedCodex([][]domain.Record{{main, other}, {main, other}})
	if files[0][0].DedupeKey == files[0][1].DedupeKey || files[0][0].DedupeKey != files[1][0].DedupeKey {
		test.Fatal("bucket identity was lost", files)
	}
}
