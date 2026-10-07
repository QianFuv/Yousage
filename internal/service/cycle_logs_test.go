package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
	"yousage/internal/accounting"
	"yousage/internal/domain"
	"yousage/internal/preferences"
	"yousage/internal/quota"
	"yousage/internal/refresh"
	"yousage/internal/remote"
)

// writeCycleLog supplies two completed cycles and a current quota-only observation.
func writeCycleLog(test *testing.T, home string, now time.Time) {
	test.Helper()
	path := filepath.Join(home, "sessions", "fixture.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		test.Fatal(err)
	}
	data := "{\"type\":\"session_meta\",\"payload\":{\"id\":\"fixture\"}}\n{\"type\":\"turn_context\",\"payload\":{\"model\":\"priced\"}}\n"
	for index := 0; index < 3; index++ {
		start := now.Add(time.Duration(index-2)*5*time.Hour - time.Hour)
		reset := start.Add(5 * time.Hour).Unix()
		info := any(nil)
		if index < 2 {
			info = map[string]any{"last_token_usage": map[string]any{"input_tokens": 10 + index}}
		}
		usage, _ := json.Marshal(map[string]any{"timestamp": quotaAt(start.Add(10 * time.Minute)), "type": "event_msg", "payload": map[string]any{"type": "token_count", "info": info, "rate_limits": map[string]any{"limit_id": "codex", "plan_type": "plus", "primary": map[string]any{"used_percent": 25, "window_minutes": 300, "resets_at": reset}, "secondary": nil}}})
		observation, _ := json.Marshal(map[string]any{"timestamp": quotaAt(start.Add(30 * time.Minute)), "type": "event_msg", "payload": map[string]any{"type": "token_count", "info": nil, "rate_limits": map[string]any{"limit_id": "codex", "plan_type": "plus", "primary": map[string]any{"used_percent": 50, "window_minutes": 300, "resets_at": reset}}}})
		plateau, _ := json.Marshal(map[string]any{"timestamp": quotaAt(start.Add(20 * time.Minute)), "type": "event_msg", "payload": map[string]any{"type": "token_count", "info": nil, "rate_limits": map[string]any{"limit_id": "codex", "plan_type": "plus", "primary": map[string]any{"used_percent": 25, "window_minutes": 300, "resets_at": reset}}}})
		data += string(usage) + "\n" + string(plateau) + "\n" + string(observation) + "\n"
	}
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		test.Fatal(err)
	}
}

// TestUsageFitsLogsBeforeFirstProbe verifies history, provenance, unchanged usage and cache reuse.
func TestUsageFitsLogsBeforeFirstProbe(test *testing.T) {
	service := testService(test)
	ctx := context.Background()
	home := test.TempDir()
	writeCycleLog(test, home, service.Now())
	_, revision, _ := service.Sources(ctx)
	if _, err := service.SaveSources(ctx, []preferences.Source{{Id: "codex", Provider: domain.Codex, Root: home, IsEnabled: true}}, revision); err != nil {
		test.Fatal(err)
	}
	saved, _ := service.Configuration(ctx)
	saved.Value.Providers = []NativeProvider{{Id: "codex", Driver: "codex", Home: home, IsEnabled: true}}
	if _, err := service.SaveConfiguration(ctx, saved); err != nil {
		test.Fatal(err)
	}
	if _, err := service.Store.WriteSetting(ctx, "public-model-rates", 0, RateSnapshot{Rates: accounting.RateTable{"priced": accounting.ModelRate{TokenRates: accounting.TokenRates{Input: 1}}}}); err != nil {
		test.Fatal(err)
	}
	if err := service.RefreshHistory(ctx, refresh.Request{IsManual: true}); err != nil {
		test.Fatal(err)
	}
	first, err := service.Usage(ctx, 1, "UTC")
	if err != nil || len(first.Environments[0].CycleValues) != 1 {
		test.Fatal(first, err)
	}
	value := first.Environments[0].CycleValues[0]
	if value.ObservationSource != "logs" || len(value.History) != 2 || value.PriorCycleCount != 2 || value.Status != "noLocalUsage" || value.History[0].Status != "estimated" || len(value.Samples) > 120 {
		test.Fatal(value)
	}
	records, sessions, cost := 0, 0, 0.0
	for _, bucket := range first.Environments[0].Summary.Buckets {
		records += bucket.Records
		sessions = max(sessions, bucket.Sessions)
		cost += bucket.CostUsd
	}
	if records != 2 || sessions != 1 || cost != 21 {
		test.Fatalf("quota-only facts changed usage: records=%d sessions=%d cost=%v", records, sessions, cost)
	}
	cache := service.cycleHistories.completed["codex:"+home]
	second, err := service.Usage(ctx, 1, "UTC")
	if err != nil || second.Environments[0].CycleValues[0].ObservationSource != "logs" || &cache[0] != &service.cycleHistories.completed["codex:"+home][0] {
		test.Fatal("unchanged Usage rebuilt completed history", err)
	}
	minutes := 300
	limits := &quota.Limits{CheckedAt: quotaAt(service.Now().Add(-10 * time.Minute)), Windows: []quota.Window{{Id: "primary", UsedPercent: 50, ResetsAt: quotaAt(service.Now().Add(4 * time.Hour)), WindowDurationMins: &minutes}}}
	var quotas quotaSnapshot
	quotaRevision, _ := service.Store.ReadSetting(ctx, "local-quotas", &quotas)
	config, _ := service.localConfig(ctx)
	provider := config.Providers[0]
	provider.Limits, provider.Auth.Label = limits, "plus"
	if _, err := service.Store.WriteSetting(ctx, "local-quotas", quotaRevision, quotaSnapshot{Providers: []remote.Provider{provider}}); err != nil {
		test.Fatal(err)
	}
	third, err := service.Usage(ctx, 1, "UTC")
	if err != nil || third.Environments[0].CycleValues[0].ObservationSource != "both" || &cache[0] != &service.cycleHistories.completed["codex:"+home][0] {
		test.Fatal("new probe rebuilt completed fits", third, err)
	}
	service.cycleCache = cycleValueCache{}
	service.cycleHistories = cycleHistoryCache{}
	cold, err := service.Usage(ctx, 1, "UTC")
	if err != nil || len(cold.Environments[0].CycleValues[0].History) != 2 || cold.Environments[0].CycleValues[0].PriorCycleCount != 2 {
		test.Fatal("persisted completed fits lost on cold read", err)
	}
	previousValue := *cold.Environments[0].CycleValues[0].History[0].ValueUsd
	digest := sha256.Sum256([]byte("codex:" + home))
	settingKey := "cycle-fits:" + hex.EncodeToString(digest[:])
	var persisted persistedCycleFits
	fitRevision, _ := service.Store.ReadSetting(ctx, settingKey, &persisted)
	for _, shouldAppend := range []bool{false, true} {
		if shouldAppend {
			path := filepath.Join(home, "sessions", "fixture.jsonl")
			file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				test.Fatal(err)
			}
			_, err = file.WriteString(`{"timestamp":"` + quotaAt(service.Now().Add(-time.Minute)) + `","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":100}},"rate_limits":{"limit_id":"codex"}}}` + "\n")
			file.Close()
			if err != nil {
				test.Fatal(err)
			}
		}
		if err := service.RefreshHistory(ctx, refresh.Request{IsManual: true}); err != nil {
			test.Fatal(err)
		}
		updated, err := service.Usage(ctx, 1, "UTC")
		gotHistory := updated.Environments[0].CycleValues[0].History
		wantHistory := append([]quota.CycleValueHistory{}, cold.Environments[0].CycleValues[0].History...)
		for index := range gotHistory {
			gotHistory[index].IsUsedForPrior = false
			wantHistory[index].IsUsedForPrior = false
		}
		if err != nil || !reflect.DeepEqual(gotHistory, wantHistory) {
			test.Fatalf("append=%v changed completed fits: got=%+v want=%+v err=%v", shouldAppend, updated.Environments[0].CycleValues[0].History, cold.Environments[0].CycleValues[0].History, err)
		}
		if actual, _ := service.Store.ReadSetting(ctx, settingKey, &persisted); actual != fitRevision {
			test.Fatal("append or no-op refitted completed cycles")
		}
	}
	var savedRates RateSnapshot
	rateRevision, _ := service.Store.ReadSetting(ctx, "public-model-rates", &savedRates)
	savedRates.Rates["priced"] = accounting.ModelRate{TokenRates: accounting.TokenRates{Input: 2}}
	if _, err := service.Store.WriteSetting(ctx, "public-model-rates", rateRevision, savedRates); err != nil {
		test.Fatal(err)
	}
	repriced, err := service.Usage(ctx, 1, "UTC")
	if err != nil || math.Abs(*repriced.Environments[0].CycleValues[0].History[0].ValueUsd-2*previousValue) > 1e-6 {
		test.Fatalf("pricing invalidation: previous=%v current=%v error=%v", previousValue, *repriced.Environments[0].CycleValues[0].History[0].ValueUsd, err)
	}
	pricing, _ := service.Preferences.Pricing(ctx, service.LocalId)
	pricing.Value.Overrides["priced"] = domain.PriceOverride{InputCostPerMillionTokens: 3_000_000}
	if _, err := service.Preferences.SavePricing(ctx, service.LocalId, pricing); err != nil {
		test.Fatal(err)
	}
	if _, err := service.Usage(ctx, 1, "UTC"); err != nil {
		test.Fatal(err)
	}
	pricingRevision, _ := service.Store.ReadSetting(ctx, settingKey, &persisted)
	if pricingRevision <= fitRevision {
		test.Fatal("pricing did not invalidate fits")
	}
	path := filepath.Join(home, "sessions", "fixture.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		test.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), `"input_tokens":10`, `"input_tokens":15`, 1))
	if err := os.WriteFile(path, data, 0600); err != nil {
		test.Fatal(err)
	}
	if err := service.RefreshHistory(ctx, refresh.Request{IsManual: true}); err != nil {
		test.Fatal(err)
	}
	if _, err := service.Usage(ctx, 1, "UTC"); err != nil {
		test.Fatal(err)
	}
	if actual, _ := service.Store.ReadSetting(ctx, settingKey, &persisted); actual <= pricingRevision {
		test.Fatal("historical rewrite did not invalidate fits")
	}
	saved, _ = service.Configuration(ctx)
	saved.Value.Providers[0].IsEnabled = false
	if _, err := service.SaveConfiguration(ctx, saved); err != nil {
		test.Fatal(err)
	}
	disabled, err := service.Usage(ctx, 1, "UTC")
	if err != nil || len(disabled.Environments[0].CycleValues) != 0 {
		test.Fatal("disabled provider emitted cycle estimates", disabled, err)
	}
}

// TestSkippedLimitsRefreshesHistory proves cooldown cannot freeze local coverage.
func TestSkippedLimitsRefreshesHistory(test *testing.T) {
	service := testService(test)
	ctx := context.Background()
	if _, err := service.Store.WriteSetting(ctx, "public-model-rates", 0, RateSnapshot{Rates: accounting.RateTable{"fixture": accounting.ModelRate{}}, Source: ratesUrl, FetchedAtMs: service.Now().UnixMilli()}); err != nil {
		test.Fatal(err)
	}
	if _, err := service.limits.Run(ctx, "local", refresh.Request{IsManual: true}, func(context.Context) (bool, error) { return true, nil }); err != nil {
		test.Fatal(err)
	}
	if err := service.RefreshLimits(ctx, []string{service.LocalId}, refresh.Request{IsVisible: true, IsUsagePage: true, IsAutoEnabled: true}); err != nil {
		test.Fatal(err)
	}
	waitLimitsFollowup(test, service)
	if !service.history.Snapshot("local-history").HasValue {
		test.Fatal("skipped limits did not attempt history")
	}
	var entries []sourceIndex
	if revision, err := service.Store.ReadSetting(ctx, "history-index", &entries); err != nil || revision == 0 {
		test.Fatalf("history coverage did not advance: %d %v", revision, err)
	}
}

// TestOverlappingWindowChangesInvalidateFits uses completed ends after a duration change.
func TestOverlappingWindowChangesInvalidateFits(test *testing.T) {
	service := testService(test)
	ctx := context.Background()
	home := test.TempDir()
	writeCycleLog(test, home, service.Now())
	_, revision, _ := service.Sources(ctx)
	if _, err := service.SaveSources(ctx, []preferences.Source{{Id: "codex", Provider: domain.Codex, Root: home, IsEnabled: true}}, revision); err != nil {
		test.Fatal(err)
	}
	saved, _ := service.Configuration(ctx)
	saved.Value.Providers = []NativeProvider{{Id: "codex", Driver: "codex", Home: home, IsEnabled: true}}
	if _, err := service.SaveConfiguration(ctx, saved); err != nil {
		test.Fatal(err)
	}
	if _, err := service.Store.WriteSetting(ctx, "public-model-rates", 0, RateSnapshot{Rates: accounting.RateTable{"priced": accounting.ModelRate{TokenRates: accounting.TokenRates{Input: 1}}}}); err != nil {
		test.Fatal(err)
	}
	if err := service.RefreshHistory(ctx, refresh.Request{IsManual: true}); err != nil {
		test.Fatal(err)
	}
	if _, err := service.Usage(ctx, 1, "UTC"); err != nil {
		test.Fatal(err)
	}
	path := filepath.Join(home, "sessions", "fixture.jsonl")
	appendLine := func(line string) {
		test.Helper()
		file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			test.Fatal(err)
		}
		_, err = file.WriteString(line + "\n")
		file.Close()
		if err != nil {
			test.Fatal(err)
		}
	}
	minutes := 24 * 60
	reset := service.Now().Add(4 * time.Hour)
	appendLine(`{"type":"event_msg","timestamp":"` + quotaAt(service.Now().Add(-5*time.Minute)) + `","payload":{"type":"token_count","info":null,"rate_limits":{"limit_id":"codex","primary":{"used_percent":50,"window_minutes":1440,"resets_at":` + strconv.FormatInt(reset.Unix(), 10) + `}}}}`)
	if _, err := service.Store.WriteSetting(ctx, "local-quotas", 0, quotaSnapshot{Providers: []remote.Provider{{InstanceId: "codex", Limits: &quota.Limits{Windows: []quota.Window{{Id: "primary", ResetsAt: quotaAt(reset), WindowDurationMins: &minutes}}}}}}); err != nil {
		test.Fatal(err)
	}
	if err := service.RefreshHistory(ctx, refresh.Request{IsManual: true}); err != nil {
		test.Fatal(err)
	}
	if _, err := service.Usage(ctx, 1, "UTC"); err != nil {
		test.Fatal(err)
	}
	digest := sha256.Sum256([]byte("codex:" + home))
	key := "cycle-fits:" + hex.EncodeToString(digest[:])
	var persisted persistedCycleFits
	before, _ := service.Store.ReadSetting(ctx, key, &persisted)
	appendLine(`{"type":"event_msg","timestamp":"` + quotaAt(service.Now().Add(-2*time.Hour)) + `","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":999}},"rate_limits":{"limit_id":"codex"}}}`)
	if err := service.RefreshHistory(ctx, refresh.Request{IsManual: true}); err != nil {
		test.Fatal(err)
	}
	if _, err := service.Usage(ctx, 1, "UTC"); err != nil {
		test.Fatal(err)
	}
	if after, _ := service.Store.ReadSetting(ctx, key, &persisted); after <= before {
		test.Fatal("overlap before a completed end retained stale fits")
	}
}
