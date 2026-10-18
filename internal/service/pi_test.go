package service

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"yousage/internal/accounting"
	"yousage/internal/domain"
	"yousage/internal/history"
	"yousage/internal/preferences"
	"yousage/internal/storage"
)

// TestPiSourceMigrationPreservesSavedChoices upgrades existing settings once, including deliberate removal.
func TestPiSourceMigrationPreservesSavedChoices(test *testing.T) {
	for _, hasPi := range []bool{false, true} {
		store, err := storage.Open(filepath.Join(test.TempDir(), "state.sqlite"))
		if err != nil {
			test.Fatal(err)
		}
		defer store.Close()
		service := &Service{Store: store, Preferences: preferences.New(store)}
		ctx := context.Background()
		original := []preferences.Source{{Id: "custom", Provider: domain.Codex, Root: "saved-path", IsEnabled: false}}
		if hasPi {
			original = append(original, preferences.Source{Id: "pi-custom", Provider: domain.Pi, Root: "custom-session-dir", IsEnabled: false})
		}
		if _, err := service.Preferences.SaveSources(ctx, original, 0); err != nil {
			test.Fatal(err)
		}
		if err := service.ensurePiHistorySource(ctx); err != nil {
			test.Fatal(err)
		}
		saved, revision, _ := service.Preferences.Sources(ctx)
		if !reflect.DeepEqual(saved[:len(original)], original) || !hasPi && (len(saved) != 2 || saved[1].Provider != domain.Pi || !saved[1].IsAutomatic || !saved[1].IsEnabled) || hasPi && len(saved) != len(original) {
			test.Fatal("saved choices changed during migration", saved)
		}
		if err := service.ensurePiHistorySource(ctx); err != nil {
			test.Fatal(err)
		}
		_, repeatedRevision, _ := service.Preferences.Sources(ctx)
		if repeatedRevision != revision {
			test.Fatal("migration wrote sources twice")
		}
		if _, err := service.Preferences.SaveSources(ctx, original[:1], revision); err != nil {
			test.Fatal(err)
		}
		if err := service.ensurePiHistorySource(ctx); err != nil {
			test.Fatal(err)
		}
		saved, _, _ = service.Preferences.Sources(ctx)
		if len(saved) != 1 {
			test.Fatal("removed source was resurrected", saved)
		}
	}
}

// TestPiMigrationKeepsFullSourceSettingsEditable reserves space for the newly introduced default source.
func TestPiMigrationKeepsFullSourceSettingsEditable(test *testing.T) {
	store, err := storage.Open(filepath.Join(test.TempDir(), "state.sqlite"))
	if err != nil {
		test.Fatal(err)
	}
	defer store.Close()
	service := &Service{Store: store, Preferences: preferences.New(store)}
	sources := []preferences.Source{}
	for index := 0; index < 64; index++ {
		sources = append(sources, preferences.Source{Id: fmt.Sprintf("saved-%d", index), Provider: domain.Codex, Root: fmt.Sprintf("saved-path-%d", index)})
	}
	ctx := context.Background()
	if _, err := service.Preferences.SaveSources(ctx, sources, 0); err != nil {
		test.Fatal(err)
	}
	if err := service.ensurePiHistorySource(ctx); err != nil {
		test.Fatal(err)
	}
	saved, revision, err := service.Preferences.Sources(ctx)
	if err != nil || len(saved) != 65 || !reflect.DeepEqual(saved[:64], sources) {
		test.Fatal(saved, err)
	}
	if _, err := service.Preferences.SaveSources(ctx, saved, revision); err != nil {
		test.Fatal("Pi upgrade made previously valid settings unsavable", err)
	}
}

// TestPiFreshDefaultsAndLegacyMigration preserves retired source settings without blocking startup.
func TestPiFreshDefaultsAndLegacyMigration(test *testing.T) {
	for _, isLegacy := range []bool{false, true} {
		store, err := storage.Open(filepath.Join(test.TempDir(), "state.sqlite"))
		if err != nil {
			test.Fatal(err)
		}
		defer store.Close()
		ctx := context.Background()
		if isLegacy {
			if _, err := store.WriteSetting(ctx, "sources", 0, []preferences.Source{{Id: "pi-auto", Provider: domain.Provider("retired"), Root: "preserved", IsEnabled: false}}); err != nil {
				test.Fatal(err)
			}
		}
		service, err := New(ctx, store, nil)
		if err != nil {
			test.Fatal(err)
		}
		sources, _, err := service.Preferences.Sources(ctx)
		service.Close()
		if err != nil || !isLegacy && (len(sources) != 3 || sources[2].Provider != domain.Pi) || isLegacy && (len(sources) != 2 || sources[0].Provider != "retired" || sources[0].Root != "preserved" || sources[1].Id != "pi-auto-1") {
			test.Fatal(sources, err)
		}
	}
}

// TestPiLossUsesNativeContextAndBackendEvidence excludes compactions, forks and unknown cache lifetimes.
func TestPiLossUsesNativeContextAndBackendEvidence(test *testing.T) {
	_, _, options, now := lossFixture()
	lines := []string{
		`{"type":"session","id":"session"}`,
		`{"type":"message","id":"first","parentId":null,"timestamp":"2026-10-06T11:00:00Z","message":{"role":"assistant","provider":"anthropic","model":"fixture","usage":{"cacheRead":100000,"output":10}}}`,
		`{"type":"message","id":"expired","parentId":"first","timestamp":"2026-10-06T11:10:00Z","message":{"role":"assistant","provider":"anthropic","model":"fixture","usage":{"cacheWrite":100000,"output":10}}}`,
		`{"type":"compaction","id":"compact","parentId":"expired","timestamp":"2026-10-06T11:11:00Z"}`,
		`{"type":"message","id":"after","parentId":"compact","timestamp":"2026-10-06T11:20:00Z","message":{"role":"assistant","provider":"anthropic","model":"fixture","usage":{"cacheWrite":100000,"output":10}}}`,
		`{"type":"message","id":"branch","parentId":"first","timestamp":"2026-10-06T11:30:00Z","message":{"role":"assistant","provider":"anthropic","model":"fixture","usage":{"cacheWrite":100000,"output":10}}}`,
		`{"type":"message","id":"backend","parentId":"branch","timestamp":"2026-10-06T11:40:00Z","message":{"role":"assistant","provider":"other","model":"fixture","usage":{"cacheRead":100000,"output":10}}}`,
		`{"type":"message","id":"unknown","parentId":"backend","timestamp":"2026-10-06T11:50:00Z","message":{"role":"assistant","provider":"other","model":"fixture","usage":{"cacheWrite":100000,"output":10}}}`,
	}
	path := filepath.Join(test.TempDir(), "pi.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0600); err != nil {
		test.Fatal(err)
	}
	read, err := history.ReadTranscript(context.Background(), path, domain.Pi, domain.Checkpoint{})
	if err != nil {
		test.Fatal(err)
	}
	report := analyzeLoss(read.Records, options, now)
	if report.CacheExpiry.Events != 1 || report.CacheExpiry.Tokens != 100000 || report.Events[0].AtMs != time.Date(2026, 10, 6, 11, 10, 0, 0, time.UTC).UnixMilli() {
		test.Fatal("Pi cache comparison crossed a native context or backend boundary", report)
	}
	first, last := read.Records[0], read.Records[1]
	first.SourcePath, last.SourcePath = "first-source", "second-source"
	if report := analyzeLoss([]domain.Record{first, last}, options, now); report.CacheExpiry.Events != 0 {
		test.Fatal("Pi compared separate physical sources", report)
	}
}

// TestPiDirectoriesAndCapabilities keeps arbitrary session directories independent of native quota accounts.
func TestPiDirectoriesAndCapabilities(test *testing.T) {
	service := testService(test)
	service.Home = test.TempDir()
	service.Platform = "windows"
	service.Scope = []domain.Provider{domain.Codex, domain.Claude, domain.Pi}
	service.Environment = map[string]string{}
	if root := service.historyRoots(domain.Pi)[0]; root != filepath.Join(service.Home, ".pi", "agent", "sessions") {
		test.Fatal(root)
	}
	service.Environment["PI_CODING_AGENT_DIR"] = "~/custom-agent"
	if root := service.historyRoots(domain.Pi)[0]; root != filepath.Join(service.Home, "custom-agent", "sessions") {
		test.Fatal(root)
	}
	custom := test.TempDir()
	service.Environment["PI_CODING_AGENT_SESSION_DIR"] = custom
	shim := filepath.Join(test.TempDir(), "pi.cmd")
	if err := os.WriteFile(shim, []byte("do not execute this launcher"), 0600); err != nil {
		test.Fatal(err)
	}
	service.Environment["PATH"] = filepath.Dir(shim)
	detections, err := service.DetectProviders(context.Background())
	if err != nil || len(detections) != 3 || !detections[2].SupportsHistory || detections[2].SupportsQuota || !detections[2].HasHistory || !detections[2].IsInstalled {
		test.Fatal(detections, err)
	}
	configuration, err := service.Configuration(context.Background())
	if err != nil || len(configuration.Value.Providers) != 2 {
		test.Fatal("Pi synthesized an unsupported quota account", configuration, err)
	}
	sources := []preferences.Source{{Id: "pi-auto", Provider: domain.Pi, IsAutomatic: true}, {Id: "pi-custom", Provider: domain.Pi, Root: custom, IsEnabled: true}}
	expanded := service.expandedSources(service.scopedSources(sources))
	if len(expanded) != 2 || expanded[0].Root != custom || expanded[1].Root != custom {
		test.Fatal("Pi locations were dropped or incorrectly expanded", expanded)
	}
}

// TestPiHistoryFeedsEveryReport exercises native files through persistent facts and payment-safe reports.
func TestPiHistoryFeedsEveryReport(test *testing.T) {
	service := testService(test)
	ctx := context.Background()
	now := time.Date(2026, 10, 18, 11, 0, 0, 0, time.UTC)
	service.Now = func() time.Time { return now }
	service.Home = test.TempDir()
	service.Environment = map[string]string{}
	service.Scope = []domain.Provider{domain.Codex, domain.Claude, domain.Pi}
	directory := test.TempDir()
	first := `{"type":"message","id":"first","parentId":null,"timestamp":"2026-10-18T10:00:00Z","message":{"role":"assistant","provider":"anthropic","model":"alias","responseModel":"claude-test","usage":{"input":70,"cacheRead":20,"cacheWrite":10,"output":40,"reasoning":15,"cost":{"total":0}}}}` + "\n"
	tool := `{"type":"message","id":"tool","parentId":"first","timestamp":"2026-10-18T10:00:01Z","message":{"role":"toolResult","usage":{"input":2,"output":3,"cost":{"total":0.5}}}}` + "\n"
	extra := `{"type":"usage","id":"extra","parentId":"first","timestamp":"2026-10-18T10:00:02Z","provider":"deepseek","model":"custom","usage":{"output":7,"cost":{"total":1.25}}}` + "\n"
	for name, data := range map[string]string{"original": `{"type":"session","id":"original"}` + "\n" + first + tool, "fork": `{"type":"session","id":"fork"}` + "\n" + first + extra} {
		if err := os.WriteFile(filepath.Join(directory, name+".jsonl"), []byte(data), 0600); err != nil {
			test.Fatal(err)
		}
	}
	_, revision, _ := service.Sources(ctx)
	if _, err := service.SaveSources(ctx, []preferences.Source{{Id: "pi-custom", Provider: domain.Pi, Root: directory, IsEnabled: true}, {Id: "pi-duplicate", Provider: domain.Pi, Root: directory, IsEnabled: true}}, revision); err != nil {
		test.Fatal(err)
	}
	if _, err := service.Store.WriteSetting(ctx, "public-model-rates", 0, RateSnapshot{Rates: accounting.RateTable{"claude-test": {TokenRates: accounting.TokenRates{Input: 1e-6, CacheRead: .2e-6, CacheCreation: 2.5e-6, Output: 10e-6}}}, FetchedAtMs: now.UnixMilli()}); err != nil {
		test.Fatal(err)
	}
	for scan := 0; scan < 2; scan++ {
		if _, err := service.scanHistory(ctx); err != nil {
			test.Fatal(err)
		}
	}
	for _, days := range []int{1, 7, 30, 90} {
		window, _ := accounting.BuildWindow(now, days, "UTC")
		summary, err := service.localSummary(ctx, window)
		if err != nil || len(summary.Sources) != 1 || len(summary.Buckets) != 3 {
			test.Fatal(days, summary, err)
		}
		var tokens int64
		var cost float64
		for _, bucket := range summary.Buckets {
			tokens += bucket.Totals.Total()
			cost += bucket.CostUsd
		}
		if tokens != 152 || math.Abs(cost-1.750499) > 1e-9 {
			test.Fatal("Pi fork or pricing totals incorrect", tokens, cost)
		}
		receipt, err := service.Receipt(ctx, days, "UTC")
		if err != nil || receipt.Tokens != tokens || math.Abs(receipt.TotalUsd-cost) > 1e-9 || receipt.PaidUsd != 0 || receipt.UnknownPaymentUsd != receipt.TotalUsd || !strings.Contains(receipt.QrText, "PAYMENT UNKNOWN") {
			test.Fatal("Pi estimated cost mislabelled as paid", receipt, err)
		}
		for _, item := range receipt.Items {
			if !item.IsPaymentUnknown || item.IsSubscription {
				test.Fatal(item)
			}
		}
		speed, err := service.SpeedReport(ctx, days, "UTC")
		if err != nil || speed.MeasuredRequests != 0 || speed.UnmeasuredRequests != 3 {
			test.Fatal("native timestamps fabricated generation timing", speed, err)
		}
		loss, err := service.LossReport(ctx, days, "UTC")
		if err != nil || loss.CacheExpiry.Events != 0 {
			test.Fatal("unrelated contexts fabricated losses", loss, err)
		}
	}
	pricing, err := service.Preferences.Pricing(ctx, service.LocalId)
	if err != nil {
		test.Fatal(err)
	}
	pricing.Value.Overrides["deepseek/custom"] = domain.PriceOverride{}
	pricing.Value.Overrides["replacement"] = domain.PriceOverride{OutputCostPerMillionTokens: 2}
	pricing.Value.Aliases["anthropic/claude-test"] = "replacement"
	if _, err := service.Preferences.SavePricing(ctx, service.LocalId, pricing); err != nil {
		test.Fatal(err)
	}
	repriced, err := service.Receipt(ctx, 7, "UTC")
	if err != nil || math.Abs(repriced.TotalUsd-0.50008) > 1e-9 || repriced.UnknownPaymentUsd != repriced.TotalUsd || repriced.Tokens != 152 {
		test.Fatal("persisted Pi facts did not reprice with aliases and explicit free overrides", repriced, err)
	}
}
