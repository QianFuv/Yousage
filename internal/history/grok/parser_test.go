package grok_test

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"yousage/internal/domain"
	"yousage/internal/history"
	"yousage/internal/history/grok"
	"yousage/internal/storage"
)

func envelope(usage map[string]any) []byte {
	data, _ := json.Marshal(map[string]any{"timestamp": 1791288000, "params": map[string]any{"sessionId": "session", "_meta": map[string]any{"agentTimestampMs": 1791288000123}, "update": map[string]any{"sessionUpdate": "turn_completed", "prompt_id": "prompt", "usage": usage}}})
	return data
}
func assertCost(t *testing.T, cost *float64, want float64) {
	t.Helper()
	if cost == nil || math.Abs(*cost-want) > 1e-12 {
		t.Fatal(cost, want)
	}
}

func TestTickedAndUntickedModelAllocation(t *testing.T) {
	records := grok.Parse(envelope(map[string]any{"costUsdTicks": 1e10, "modelUsage": map[string]any{
		"known": map[string]any{"inputTokens": 100, "outputTokens": 10, "cachedReadTokens": 20, "cacheCreationTokens": 5, "reasoningTokens": 30, "costUsdTicks": 2e9},
		"first": map[string]any{"inputTokens": 30}, "second": map[string]any{"inputTokens": 70}, "zero": map[string]any{"costUsdTicks": 9e10},
	}}))
	if len(records) != 3 {
		t.Fatal(records)
	}
	models := map[string]domain.Record{}
	for _, record := range records {
		models[record.Model] = record
	}
	assertCost(t, models["known"].ReportedCostUsd, 0.2)
	assertCost(t, models["first"].ReportedCostUsd, 0.24)
	assertCost(t, models["second"].ReportedCostUsd, 0.56)
	if models["known"].Totals.UncachedInputTokens != 75 || models["known"].Totals.ReasoningTokens != 10 || models["known"].TimestampMs != 1791288000123 || models["known"].DedupeKey != "session:prompt:known" {
		t.Fatal(models["known"])
	}
}

func TestAggregateFallbackUnknownCostAndClamp(t *testing.T) {
	records := grok.Parse(envelope(map[string]any{"inputTokens": 10, "outputTokens": 5, "costUsdTicks": 5e9}))
	if len(records) != 1 || records[0].Model != "grok" || records[0].Totals.Total() != 15 {
		t.Fatal(records)
	}
	assertCost(t, records[0].ReportedCostUsd, 0.5)
	records = grok.Parse(envelope(map[string]any{"inputTokens": 10}))
	if len(records) != 1 || records[0].ReportedCostUsd != nil {
		t.Fatal("absent cost invented", records)
	}
	records = grok.Parse(envelope(map[string]any{"costUsdTicks": 1e9, "modelUsage": map[string]any{"known": map[string]any{"inputTokens": 5, "costUsdTicks": 2e9}, "unknown": map[string]any{"inputTokens": 5}}}))
	if len(records) != 2 {
		t.Fatal(records)
	}
	assertCost(t, records[0].ReportedCostUsd, 0.2)
	assertCost(t, records[1].ReportedCostUsd, 0)
	if len(grok.Parse([]byte(`{"params":{"update":{"sessionUpdate":"text","usage":{"inputTokens":100}}}}`))) != 0 {
		t.Fatal("unfinished text treated as billed usage")
	}
}

func TestSavedTurnDiscoveryTailResumeAndProducerNotice(t *testing.T) {
	home := t.TempDir()
	directory := filepath.Join(home, "sessions", "project", "session")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	data := envelope(map[string]any{"inputTokens": 10, "costUsdTicks": 1e9})
	path := filepath.Join(directory, "updates.jsonl")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "other.jsonl"), data, 0600); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(filepath.Join(t.TempDir(), "grok.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	collector := history.Collector{Store: store, HostId: "host"}
	root := history.Root{Provider: domain.Grok, Home: home}
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	sources, err := collector.Scan(context.Background(), []history.Root{root}, now)
	if err != nil || len(sources) != 1 || len(sources[0].Records) != 1 || sources[0].ScannedFiles != 1 {
		t.Fatal(sources, err)
	}
	if err := os.WriteFile(path, append(append(data, '\n'), append(data, '\n')...), 0600); err != nil {
		t.Fatal(err)
	}
	sources, err = collector.Scan(context.Background(), []history.Root{root}, now)
	if err != nil || len(sources[0].Records) != 1 {
		t.Fatal("tail promotion or repeated prompt doubled cost", sources, err)
	}
	emptyHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(emptyHome, "sessions"), 0700); err != nil {
		t.Fatal(err)
	}
	sources, err = collector.Scan(context.Background(), []history.Root{{Provider: domain.Grok, Home: emptyHome}}, now)
	if err != nil || sources[0].Status != "ok" || !strings.Contains(sources[0].Notice, "cannot be reconstructed") {
		t.Fatal("missing producer boundary hidden", sources, err)
	}
}
