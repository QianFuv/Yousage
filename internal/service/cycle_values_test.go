package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"yousage/internal/accounting"
	"yousage/internal/domain"
	"yousage/internal/preferences"
	"yousage/internal/quota"
	"yousage/internal/refresh"
	"yousage/internal/remote"
)

// quotaAt formats deterministic native quota observation timestamps.
func quotaAt(instant time.Time) string { return instant.UTC().Format("2006-01-02T15:04:05.000Z") }

// cycleRefreshFixture configures a native probe, priced history and a completion clock.
func cycleRefreshFixture(test *testing.T) *Service {
	test.Helper()
	service := testService(test)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	directory := test.TempDir()
	executable := filepath.Join(directory, "codexfixture")
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	command := exec.CommandContext(ctx, "go", "build", "-o", executable, "../quota/testdata/codexfixture")
	if output, err := command.CombinedOutput(); err != nil {
		test.Fatalf("probe fixture: %s %v", output, err)
	}
	base := time.Now().UTC().Truncate(time.Minute)
	service.Now = func() time.Time {
		limits, _, err := service.quotas(context.Background())
		if err == nil && len(limits.Providers) == 1 && limits.Providers[0].Limits != nil {
			if checkedAt, err := time.Parse(time.RFC3339Nano, limits.Providers[0].Limits.CheckedAt); err == nil {
				return checkedAt.Add(time.Second)
			}
		}
		return base
	}
	saved, err := service.Configuration(ctx)
	if err != nil {
		test.Fatal(err)
	}
	saved.Value.Providers = []NativeProvider{{Id: "codex", Driver: "codex", Executable: executable, Home: directory, IsEnabled: true}}
	if _, err := service.SaveConfiguration(ctx, saved); err != nil {
		test.Fatal(err)
	}
	service.Environment["YOUSAGE_PID_FILE"] = filepath.Join(directory, "pids")
	service.Environment["YOUSAGE_REQUEST_FILE"] = filepath.Join(directory, "requests")
	service.Environment["YOUSAGE_CODEX_SCENARIO"] = "success"
	service.Environment["YOUSAGE_CODEX_RESET_AT"] = strconv.FormatInt(base.Add(time.Hour).Unix(), 10)
	path := filepath.Join(directory, "sessions", "fixture.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		test.Fatal(err)
	}
	data := `{"type":"session_meta","payload":{"id":"fixture"}}` + "\n" +
		`{"type":"turn_context","payload":{"model":"fixture"}}` + "\n" +
		`{"type":"event_msg","timestamp":"` + quotaAt(base.Add(-30*time.Minute)) + `","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":1000000,"output_tokens":0}},"rate_limits":{"limit_id":"codex"}}}` + "\n"
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		test.Fatal(err)
	}
	_, revision, err := service.Sources(ctx)
	if err != nil {
		test.Fatal(err)
	}
	if _, err := service.SaveSources(ctx, []preferences.Source{{Id: "codex-history", Provider: domain.Codex, Root: directory, IsEnabled: true}}, revision); err != nil {
		test.Fatal(err)
	}
	service.RateClient = &http.Client{Transport: fixtureTransport(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"fixture-provider":{"models":{"fixture":{"cost":{"input":1,"output":2}}}}}`)), Header: http.Header{}}, nil
	})}
	return service
}

// TestRefreshLimitsCoversFirstSample verifies ordered history, pricing and one invalidation.
func TestRefreshLimitsCoversFirstSample(test *testing.T) {
	service := cycleRefreshFixture(test)
	ctx := context.Background()
	var invalidations atomic.Int32
	service.Emit = func(name string, value any) {
		if name == "usage-invalidated" {
			invalidations.Add(1)
		}
	}
	if err := service.RefreshLimits(ctx, []string{service.LocalId}, refresh.Request{IsVisible: true, IsUsagePage: true, IsAutoEnabled: true}); err != nil {
		test.Fatal(err)
	}
	waitLimitsFollowup(test, service)
	series := []quota.CycleSeries{}
	if _, err := service.Store.ReadSetting(ctx, "quota-samples", &series); err != nil || len(series) != 1 || len(series[0].Samples) != 1 {
		test.Fatal("first sample missing", series, err)
	}
	checkedAt, err := time.Parse(time.RFC3339Nano, series[0].Samples[0].CheckedAt)
	if err != nil {
		test.Fatal(err)
	}
	entries, _, err := service.sourceIndex(ctx)
	if err != nil || len(entries) != 1 {
		test.Fatal("history scan missing", entries, err)
	}
	observation, err := service.Store.Observation(ctx, entries[0].Source.Fingerprint.Id())
	if err != nil || observation.SucceededAt == nil || time.UnixMilli(*observation.SucceededAt).Before(checkedAt) {
		test.Fatal("history did not succeed after limits", observation, checkedAt, err)
	}
	snapshot, err := service.Usage(ctx, 1, "UTC")
	if err != nil || len(snapshot.Environments[0].CycleValues) != 1 {
		test.Fatal("cycle value missing", snapshot, err)
	}
	value := snapshot.Environments[0].CycleValues[0]
	if value.Status != "insufficientData" || value.ValueUsd != nil || value.ValueLowUsd != nil || value.ConstraintCount != 1 || len(value.Samples) != 1 || !value.Samples[0].IsUsable || value.Samples[0].ImpliedLowUsd == nil || math.Abs(*value.Samples[0].ImpliedLowUsd-3.846154) > 1e-6 {
		test.Fatalf("first sample diagnostic missing: %+v", value)
	}

	if invalidations.Load() != 1 {
		test.Fatal("refresh emitted multiple invalidations", invalidations.Load())
	}
}

// TestRefreshLimitsSkipsHiddenRequests preserves automatic foreground policy and selection.
func TestRefreshLimitsSkipsHiddenRequests(test *testing.T) {
	service := testService(test)
	ctx := context.Background()
	var rateCalls atomic.Int32
	service.RateClient = &http.Client{Transport: fixtureTransport(func(request *http.Request) (*http.Response, error) {
		rateCalls.Add(1)
		return nil, errors.New("unexpected price request")
	})}
	for _, selected := range [][]string{{service.LocalId}, {"remote-only"}} {
		if err := service.RefreshLimits(ctx, selected, refresh.Request{IsUsagePage: true, IsAutoEnabled: true}); err != nil {
			test.Fatal("skipped request failed", err)
		}
	}
	waitLimitsFollowup(test, service)
	for _, key := range []string{"local-quotas", "quota-samples", "history-index"} {
		var value json.RawMessage
		if revision, err := service.Store.ReadSetting(ctx, key, &value); err != nil || revision != 0 {
			test.Fatal("skipped refresh wrote data", key, revision, err)
		}
	}
	if rateCalls.Load() != 0 || !service.history.Snapshot("local-history").AttemptedAt.IsZero() {
		test.Fatal("skipped limits started history or rates")
	}
}

// TestRefreshLimitsRetainsFollowupPolicies preserves fresh history and cached rate skipping.
func TestRefreshLimitsRetainsFollowupPolicies(test *testing.T) {
	service := cycleRefreshFixture(test)
	ctx := context.Background()
	manual := refresh.Request{IsManual: true}
	if err := service.RefreshHistory(ctx, manual); err != nil {
		test.Fatal(err)
	}
	if err := service.RefreshRates(ctx, manual); err != nil {
		test.Fatal(err)
	}
	historyAttempt := service.history.Snapshot("local-history").AttemptedAt
	rateAttempt := service.rates.Snapshot("public").AttemptedAt
	if err := service.RefreshLimits(ctx, []string{service.LocalId}, refresh.Request{IsVisible: true, IsUsagePage: true, IsAutoEnabled: true}); err != nil {
		test.Fatal("fresh followup skip failed", err)
	}
	waitLimitsFollowup(test, service)
	if !service.limits.Snapshot("local").HasValue || service.history.Snapshot("local-history").AttemptedAt != historyAttempt || service.rates.Snapshot("public").AttemptedAt != rateAttempt {
		test.Fatal("limits did not preserve followup freshness policies")
	}
}

// TestRefreshLimitsFollowupFailuresRetainLimits surfaces background failures after a successful probe.
func TestRefreshLimitsFollowupFailuresRetainLimits(test *testing.T) {
	service := cycleRefreshFixture(test)
	ctx := context.Background()
	if _, err := service.Store.WriteSetting(ctx, "history-index", 0, "invalid history index"); err != nil {
		test.Fatal(err)
	}
	service.RateClient = &http.Client{Transport: fixtureTransport(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
	})}
	err := service.RefreshLimits(ctx, []string{service.LocalId}, refresh.Request{IsManual: true})
	waitLimitsFollowup(test, service)
	historyError := service.history.Snapshot("local-history").Error
	rateError := service.rates.Snapshot("public").Error
	if err != nil || historyError == nil || rateError == nil {
		test.Fatal("background followup failures were lost", err, historyError, rateError)
	}
	limits, _, readError := service.quotas(ctx)
	if readError != nil || len(limits.Providers) != 1 || limits.Providers[0].Limits == nil || !service.limits.Snapshot("local").HasValue || service.limits.Snapshot("local").Error != nil {
		test.Fatal("followup failures lost successful limits", limits, readError)
	}
}

// TestScanLimitsRecordsOnlyFreshProbes exercises the actual native probe and retained-limit path.
func TestScanLimitsRecordsOnlyFreshProbes(test *testing.T) {
	service := testService(test)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	directory := test.TempDir()
	executable := filepath.Join(directory, "codexfixture")
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	command := exec.CommandContext(ctx, "go", "build", "-o", executable, "../quota/testdata/codexfixture")
	if output, err := command.CombinedOutput(); err != nil {
		test.Fatalf("probe fixture: %s %v", output, err)
	}
	saved, _ := service.Configuration(ctx)
	saved.Value.Providers = []NativeProvider{{Id: "codex", Driver: "codex", Executable: executable, IsEnabled: true}}
	if _, err := service.SaveConfiguration(ctx, saved); err != nil {
		test.Fatal(err)
	}
	service.Environment["YOUSAGE_PID_FILE"] = filepath.Join(directory, "pids")
	service.Environment["YOUSAGE_REQUEST_FILE"] = filepath.Join(directory, "requests")
	service.Environment["YOUSAGE_CODEX_SCENARIO"] = "success"
	if _, err := service.scanLimits(ctx); err != nil {
		test.Fatal(err)
	}
	series := []quota.CycleSeries{}
	if _, err := service.Store.ReadSetting(ctx, "quota-samples", &series); err != nil || len(series) != 1 || len(series[0].Samples) != 1 {
		test.Fatalf("fresh observation missing: %+v %v", series, err)
	}
	first := series[0].Samples[0]
	service.Environment["YOUSAGE_CODEX_SCENARIO"] = "rpc-error"
	if _, err := service.scanLimits(ctx); err == nil {
		test.Fatal("probe failure was reported as success")
	}
	if _, err := service.Store.ReadSetting(ctx, "quota-samples", &series); err != nil || len(series[0].Samples) != 1 || series[0].Samples[0].CheckedAt != first.CheckedAt {
		test.Fatal("failed probe created a sample", err)
	}
	limits, _, _ := service.quotas(ctx)
	if limits.Providers[0].Limits.CheckedAt != first.CheckedAt || limits.Error == "" {
		test.Fatal("failed probe lost last-good limits")
	}
	service.Environment["YOUSAGE_CODEX_SCENARIO"] = "success"
	if _, err := service.scanLimits(ctx); err != nil {
		test.Fatal(err)
	}
	if _, err := service.Store.ReadSetting(ctx, "quota-samples", &series); err != nil || len(series[0].Samples) != 2 {
		test.Fatal("fresh equal-percent endpoints missing", err)
	}
	var revision int64
	revision, _ = service.Store.ReadSetting(ctx, "quota-samples", &series)
	if _, err := service.Store.WriteSetting(ctx, "quota-samples", revision, map[string]string{"invalid": "series"}); err != nil {
		test.Fatal(err)
	}
	previous, _, _ := service.quotas(ctx)
	if _, err := service.scanLimits(ctx); err != nil {
		test.Fatal("sample failure blocked limits refresh", err)
	}
	next, _, _ := service.quotas(ctx)
	if next.Providers[0].Limits.CheckedAt == previous.Providers[0].Limits.CheckedAt {
		test.Fatal("sample failure blocked local-quotas write")
	}
	if _, err := os.Stat(service.Environment["YOUSAGE_REQUEST_FILE"]); err != nil {
		test.Fatal(err)
	}
}

// TestLocalCycleHistoryWindow excludes old files and shares the earliest start across home windows.
func TestLocalCycleHistoryWindow(test *testing.T) {
	service := testService(test)
	ctx := context.Background()
	now := service.Now()
	home := test.TempDir()
	fingerprint := service.fingerprint(domain.Codex, home)
	id, err := service.Store.RegisterSource(ctx, fingerprint)
	if err != nil {
		test.Fatal(err)
	}
	for _, fixture := range []struct {
		path, key string
		at        time.Time
		tokens    int64
	}{
		{"a-old", "old-weekly", now.Add(-8 * 24 * time.Hour), 1000},
		{"b-weekly", "weekly", now.Add(-5 * 24 * time.Hour), 20},
		{"c-session", "session", now.Add(-time.Hour), 10},
	} {
		record := domain.Record{Provider: domain.Codex, QuotaBucket: "codex", TimestampMs: fixture.at.UnixMilli(), Model: "priced", Speed: "standard", SessionId: fixture.path, DedupeKey: fixture.key, Totals: domain.Tokens{UncachedInputTokens: fixture.tokens}}
		checkpoint := domain.Checkpoint{SourceId: id, Path: fixture.path, ModifiedNs: fixture.at.UnixNano(), ParserVersion: 1, Reducer: json.RawMessage(`{}`)}
		if _, err := service.Store.CommitFile(ctx, checkpoint, []domain.Record{record}, true); err != nil {
			test.Fatal(err)
		}
	}
	success := now.UnixMilli()
	if err := service.Store.Observe(ctx, domain.Observation{SourceId: id, Status: "ok", AttemptedAt: success, SucceededAt: &success}); err != nil {
		test.Fatal(err)
	}
	entries := []sourceIndex{{Source: remote.Source{Fingerprint: fingerprint, Status: "ok"}}}
	if _, err := service.Store.WriteSetting(ctx, "history-index", 0, entries); err != nil {
		test.Fatal(err)
	}
	if _, err := service.Store.WriteSetting(ctx, "public-model-rates", 0, RateSnapshot{Rates: accounting.RateTable{"priced": accounting.ModelRate{TokenRates: accounting.TokenRates{Input: 1}}}}); err != nil {
		test.Fatal(err)
	}
	saved, _ := service.Configuration(ctx)
	saved.Value.Providers = []NativeProvider{{Id: "short", Driver: "codex", Home: home, IsEnabled: true}, {Id: "weekly", Driver: "codex", Home: home, IsEnabled: true}}
	if _, err := service.SaveConfiguration(ctx, saved); err != nil {
		test.Fatal(err)
	}
	shortDuration, weeklyDuration := 300, 7*24*60
	providers := []remote.Provider{
		{InstanceId: "short", IsEnabled: true, Driver: "codex", Limits: &quota.Limits{CheckedAt: quotaAt(now.Add(-10 * time.Minute)), Windows: []quota.Window{{Id: "session", UsedPercent: 50, ResetsAt: quotaAt(now.Add(3 * time.Hour)), WindowDurationMins: &shortDuration}}}},
		{InstanceId: "weekly", IsEnabled: true, Driver: "codex", Limits: &quota.Limits{CheckedAt: quotaAt(now.Add(-10 * time.Minute)), Windows: []quota.Window{{Id: "weekly", UsedPercent: 50, ResetsAt: quotaAt(now.Add(24 * time.Hour)), WindowDurationMins: &weeklyDuration}}}},
	}
	since := now.Add(-6*24*time.Hour - time.Hour)
	_, records, err := service.localRecords(ctx, entries, since, now)
	if err != nil || len(records) != 1 || len(records[0]) != 2 {
		test.Fatal("old file loaded into the cycle read window", records, err)
	}
	for _, record := range records[0] {
		if time.UnixMilli(record.TimestampMs).Before(since) {
			test.Fatal("old record loaded", record)
		}
	}
	values := service.localCycleValues(ctx, remote.Config{Providers: providers})
	if len(values) != len(providers) {
		test.Fatal("missing cycle values", values)
	}
	history := quota.NewCostHistory([]quota.CostRecord{{At: now.Add(-5 * 24 * time.Hour), CostUsd: 20}, {At: now.Add(-time.Hour), CostUsd: 10}}, now, since)
	for index, provider := range providers {
		expected := quota.EstimateCycle(quota.CycleSeries{InstanceId: provider.InstanceId, WindowId: provider.Limits.Windows[0].Id}, provider.Driver, provider.Limits.Windows[0], provider.Limits.CheckedAt, history, now)
		if expected.Status != "insufficientData" || !reflect.DeepEqual(values[index], expected) {
			test.Fatalf("window %s: got %+v, want %+v", provider.InstanceId, values[index], expected)
		}
	}
	weeklyDuration = 100 * 24 * 60
	values = service.localCycleValues(ctx, remote.Config{Providers: providers})
	if len(values) != 2 || values[1].Status != "insufficientData" || values[1].ConstraintCount != 0 || values[1].ValueLowUsd != nil || values[1].ValueHighUsd != nil {
		test.Fatal("cycle before the read window produced bounds", values)
	}
}

// TestCycleHistorySince checks window starts, reset anchors, fallback and retention limits.
func TestCycleHistorySince(test *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	shortDuration, weeklyDuration, longDuration := 300, 7*24*60, 100*24*60
	shortWindow := quota.Window{Id: "session", ResetsAt: quotaAt(now.Add(3 * time.Hour)), WindowDurationMins: &shortDuration}
	weeklyWindow := quota.Window{Id: "weekly", ResetsAt: quotaAt(now.Add(24 * time.Hour)), WindowDurationMins: &weeklyDuration}
	for _, fixture := range []struct {
		name    string
		windows []quota.Window
		series  []quota.CycleSeries
		want    time.Time
	}{
		{"short cycle", []quota.Window{shortWindow}, nil, now.Add(-3 * time.Hour)},
		{"earliest window", []quota.Window{shortWindow, weeklyWindow}, nil, now.Add(-6*24*time.Hour - time.Hour)},
		{"reset floor", []quota.Window{{Id: "session"}}, []quota.CycleSeries{{InstanceId: "native", WindowId: "session", ResetFloorAt: quotaAt(now.Add(-4 * time.Hour))}}, now.Add(-5 * time.Hour)},
		{"last boundary", []quota.Window{{Id: "session"}}, []quota.CycleSeries{{InstanceId: "native", WindowId: "session", CycleBoundaries: []string{quotaAt(now.Add(-8 * 24 * time.Hour)), quotaAt(now.Add(-4 * time.Hour))}}}, now.Add(-5 * time.Hour)},
		{"legacy boundary", []quota.Window{{Id: "session", UsedPercent: 10, ResetsAt: quotaAt(now.Add(3 * time.Hour))}}, []quota.CycleSeries{{InstanceId: "native", WindowId: "session", Samples: []quota.CycleSample{{CheckedAt: quotaAt(now.Add(-4 * time.Hour)), UsedPercent: 50, ResetsAt: quotaAt(now.Add(-time.Hour))}}}}, now.Add(-5 * time.Hour)},
		{"live reset boundary", []quota.Window{shortWindow, {Id: "weekly", UsedPercent: 10, ResetsAt: quotaAt(now.Add(24 * time.Hour))}}, []quota.CycleSeries{{InstanceId: "native", WindowId: "weekly", CycleBoundaries: []string{}, Samples: []quota.CycleSample{{CheckedAt: quotaAt(now.Add(-5 * 24 * time.Hour)), UsedPercent: 50, ResetsAt: quotaAt(now.Add(-time.Hour))}}}}, now.Add(-5*24*time.Hour - time.Hour)},
		{"unknown cycle", []quota.Window{{Id: "session", ResetsAt: "invalid"}}, nil, now.Add(-8 * 24 * time.Hour)},
		{"no windows", nil, nil, now.Add(-8 * 24 * time.Hour)},
		{"retention clamp", []quota.Window{{Id: "weekly", ResetsAt: quotaAt(now.Add(24 * time.Hour)), WindowDurationMins: &longDuration}}, nil, now.Add(-90 * 24 * time.Hour)},
	} {
		test.Run(fixture.name, func(test *testing.T) {
			provider := remote.Provider{InstanceId: "native", Limits: &quota.Limits{CheckedAt: quotaAt(now.Add(-10 * time.Minute)), Windows: fixture.windows}}
			if since := cycleHistorySince(provider, fixture.series, now); !since.Equal(fixture.want) {
				test.Fatalf("since=%s, want %s", since, fixture.want)
			}
		})
	}
}

// TestQuotaSamplePruningAndCap preserves reset anchors while removing obsolete providers and old cycles.
func TestQuotaSamplePruningAndCap(test *testing.T) {
	service := testService(test)
	ctx := context.Background()
	now := service.Now()
	series := []quota.CycleSeries{{InstanceId: "removed", WindowId: "small"}, {InstanceId: "codex", WindowId: "small"}, {InstanceId: "codex", WindowId: "large"}}
	for index := 0; index < 3; index++ {
		sample := quota.CycleSample{CheckedAt: quotaAt(now.Add(time.Duration(index) * time.Second)), UsedPercent: 10, ResetsAt: quotaAt(now.Add(time.Duration(index+1) * time.Hour))}
		series[1].Samples = append(series[1].Samples, sample)
		series[2].Samples = append(series[2].Samples, sample)
	}
	for index := 0; index < 610; index++ {
		series[2].Samples = append(series[2].Samples, quota.CycleSample{CheckedAt: quotaAt(now.Add(time.Duration(index+3) * time.Second)), UsedPercent: 10 + float64(index)/100, ResetsAt: quotaAt(now.Add(3 * time.Hour))})
	}
	if _, err := service.Store.WriteSetting(ctx, "quota-samples", 0, series); err != nil {
		test.Fatal(err)
	}
	provider := remote.Provider{InstanceId: "codex", Driver: "codex", Limits: &quota.Limits{CheckedAt: quotaAt(now.Add(11 * time.Minute)), Windows: []quota.Window{{Id: "small", UsedPercent: 11, ResetsAt: quotaAt(now.Add(3 * time.Hour))}, {Id: "large", UsedPercent: 17, ResetsAt: quotaAt(now.Add(3 * time.Hour))}}}}
	service.recordQuotaSamples(ctx, []NativeProvider{{Id: "codex"}}, []remote.Provider{provider}, []bool{true})
	if _, err := service.Store.ReadSetting(ctx, "quota-samples", &series); err != nil {
		test.Fatal(err)
	}
	if len(series) != 2 || len(series[0].Samples) != 3 || len(series[1].Samples) != 600 || series[1].ResetFloorAt != quotaAt(now.Add(time.Second)) {
		test.Fatalf("incorrect pruning: small=%+v large=%d", series[0], len(series[1].Samples))
	}
	if series[0].Samples[0].CheckedAt != quotaAt(now.Add(time.Second)) {
		test.Fatal("oldest cycle retained")
	}
}

// TestUsageCycleValuesPricesAndCoverage verifies reset attribution, aliases, overrides and scan gating.
func TestUsageCycleValuesPricesAndCoverage(test *testing.T) {
	for _, driver := range []string{"codex", "claudeAgent"} {
		test.Run(driver, func(test *testing.T) {
			service := testService(test)
			ctx := context.Background()
			now := service.Now()
			provider := domain.Codex
			if driver == "claudeAgent" {
				provider = domain.Claude
			}
			home := test.TempDir()
			fingerprint := service.fingerprint(provider, home)
			base := now.Add(-2 * time.Hour)
			duration := 300
			records := []domain.Record{}
			for index, fixture := range []struct {
				offset time.Duration
				tokens int64
			}{{-time.Minute, 100}, {0, 1}, {20 * time.Minute, 5}, {58 * time.Minute, 2}, {61 * time.Minute, 4}, {62 * time.Minute, 8}, {62*time.Minute + time.Millisecond, 16}, {90 * time.Minute, 32}} {
				records = append(records, domain.Record{Provider: provider, QuotaBucket: "codex", TimestampMs: base.Add(fixture.offset).UnixMilli(), Model: "alias", RateModel: "wrong", Speed: "standard", SessionId: "session", DedupeKey: string(rune('a' + index)), Totals: domain.Tokens{UncachedInputTokens: fixture.tokens}})
			}
			if err := service.storeSnapshot(ctx, fingerprint, records, "ok", "", now); err != nil {
				test.Fatal(err)
			}
			entries := []sourceIndex{{Source: remote.Source{Fingerprint: fingerprint, Status: "ok"}, IsSnapshot: true}}
			other := service.fingerprint(provider, test.TempDir())
			if err := service.storeSnapshot(ctx, other, []domain.Record{{Provider: provider, QuotaBucket: "codex", TimestampMs: base.Add(20 * time.Minute).UnixMilli(), Model: "alias", Speed: "standard", Totals: domain.Tokens{UncachedInputTokens: 999}}}, "ok", "", now); err != nil {
				test.Fatal(err)
			}
			entries = append(entries, sourceIndex{Source: remote.Source{Fingerprint: other, Status: "ok"}, IsSnapshot: true})
			if _, err := service.Store.WriteSetting(ctx, "history-index", 0, entries); err != nil {
				test.Fatal(err)
			}
			if _, err := service.Store.WriteSetting(ctx, "public-model-rates", 0, RateSnapshot{Rates: accounting.RateTable{"priced": accounting.ModelRate{TokenRates: accounting.TokenRates{Input: 2}}}}); err != nil {
				test.Fatal(err)
			}
			pricing, _ := service.Preferences.Pricing(ctx, service.LocalId)
			alias := "priced"
			if _, err := service.Preferences.PatchPricing(ctx, service.LocalId, pricing.Revision, preferences.PricingPatch{Aliases: map[string]*string{"alias": &alias}, Overrides: map[string]*domain.PriceOverride{"priced": {InputCostPerMillionTokens: 1e6}}}); err != nil {
				test.Fatal(err)
			}
			saved, _ := service.Configuration(ctx)
			saved.Value.Providers = []NativeProvider{{Id: "native", Driver: driver, Home: home, IsEnabled: true}}
			if _, err := service.SaveConfiguration(ctx, saved); err != nil {
				test.Fatal(err)
			}
			limits := &quota.Limits{CheckedAt: quotaAt(base.Add(time.Hour)), Windows: []quota.Window{{Id: "session", UsedPercent: 25, ResetsAt: quotaAt(now.Add(2 * time.Hour)), WindowDurationMins: &duration}}}
			if _, err := service.Store.WriteSetting(ctx, "local-quotas", 0, quotaSnapshot{Providers: []remote.Provider{{InstanceId: "native", Driver: driver, Limits: limits}}}); err != nil {
				test.Fatal(err)
			}
			if _, err := service.Store.WriteSetting(ctx, "quota-samples", 0, []quota.CycleSeries{{InstanceId: "native", WindowId: "session", Samples: []quota.CycleSample{{CheckedAt: quotaAt(base), UsedPercent: 80, ResetsAt: quotaAt(now.Add(time.Hour)), WindowDurationMins: &duration}, {CheckedAt: quotaAt(base.Add(30 * time.Minute)), UsedPercent: 20, ResetsAt: quotaAt(now.Add(2 * time.Hour)), WindowDurationMins: &duration}}}}); err != nil {
				test.Fatal(err)
			}
			snapshot, err := service.Usage(ctx, 1, "UTC")
			if err != nil || len(snapshot.Environments[0].CycleValues) != 1 {
				test.Fatal("local estimate absent", err)
			}
			value := snapshot.Environments[0].CycleValues[0]
			if value.Status != "estimated" || !value.IsResetDetected || value.CycleStartedAt != quotaAt(now.Add(-3*time.Hour)) || value.SpentUsd != 168 || value.Records != 8 || value.ConstraintCount != 2 || len(value.Samples) != 2 || value.Samples[1].SpentUsd != 108 {
				test.Fatalf("cycle attribution: %+v", value)
			}
			if value.ValueUsd == nil || *value.ValueLowUsd >= *value.ValueUsd || *value.ValueHighUsd <= *value.ValueUsd || math.Abs(*value.RemainingLowUsd-max(0, *value.ValueLowUsd-168)) > 1e-6 || math.Abs(*value.Samples[1].ImpliedLowUsd-415.384615) > 1e-6 {
				test.Fatal("fit band or diagnostic incorrect", value)
			}
			success := base.Add(time.Hour - time.Millisecond).UnixMilli()
			if err := service.Store.Observe(ctx, domain.Observation{SourceId: fingerprint.Id(), Status: "ok", AttemptedAt: success, SucceededAt: &success}); err != nil {
				test.Fatal(err)
			}
			snapshot, err = service.Usage(ctx, 1, "UTC")
			value = snapshot.Environments[0].CycleValues[0]
			if err != nil || value.Status != "insufficientData" || value.ConstraintCount != 1 || !value.Samples[0].IsUsable || value.Samples[1].IsUsable || value.ValueUsd != nil || value.SpentUsd != 168 {
				test.Fatal("partial coverage produced a complete fit", value, err)
			}

		})
	}
}

// TestCycleSourceSelectionAndFailure checks fallback filtering and corrupt sample degradation.
func TestCycleSourceSelectionAndFailure(test *testing.T) {
	service := testService(test)
	ctx := context.Background()
	root := test.TempDir()
	entries := []sourceIndex{
		{Source: remote.Source{Fingerprint: service.fingerprint(domain.Codex, root)}},
		{Source: remote.Source{Fingerprint: service.fingerprint(domain.Codex, test.TempDir())}},
		{Source: remote.Source{Fingerprint: service.fingerprint(domain.Codex, test.TempDir())}, IsHidden: true},
		{Source: remote.Source{Fingerprint: service.fingerprint(domain.Claude, test.TempDir())}},
	}
	if selected := service.cycleSources(entries, NativeProvider{Home: root}, domain.Codex); len(selected) != 1 || selected[0].Source.Fingerprint.ResolvedHomePath != entries[0].Source.Fingerprint.ResolvedHomePath {
		test.Fatal("exact custom home not selected")
	}
	if selected := service.cycleSources(entries, NativeProvider{Home: test.TempDir()}, domain.Codex); len(selected) != 2 {
		test.Fatal("unmatched home did not fall back to visible provider sources")
	}
	service.Scope = []domain.Provider{domain.Claude}
	if len(service.cycleSources(entries, NativeProvider{}, domain.Codex)) != 0 {
		test.Fatal("out-of-scope history selected")
	}
	service.Scope = nil
	if _, err := service.Store.WriteSetting(ctx, "quota-samples", 0, map[string]string{"bad": "shape"}); err != nil {
		test.Fatal(err)
	}
	duration := 300
	limits := &quota.Limits{CheckedAt: quotaAt(service.Now().Add(-time.Hour)), Windows: []quota.Window{{Id: "session", UsedPercent: 20, ResetsAt: quotaAt(service.Now().Add(time.Hour)), WindowDurationMins: &duration}}}
	if _, err := service.Store.WriteSetting(ctx, "local-quotas", 0, quotaSnapshot{Providers: []remote.Provider{{InstanceId: "native", Driver: "codex", Limits: limits}}}); err != nil {
		test.Fatal(err)
	}
	saved, _ := service.Configuration(ctx)
	saved.Value.Providers = []NativeProvider{{Id: "native", Driver: "codex", IsEnabled: true}}
	saved.Value.Connections = []ConnectionPreference{{Connection: remote.Connection{EnvironmentId: "remote", Url: "https://fixture.invalid", CredentialId: "fixture"}, Label: "Remote"}}
	service.draftCredentials["fixture"] = draftCredential{Kind: "environment", Origin: "https://fixture.invalid", Target: "remote"}
	if _, err := service.SaveConfiguration(ctx, saved); err != nil {
		test.Fatal(err)
	}
	snapshot, err := service.Usage(ctx, 1, "UTC")
	if err != nil || len(snapshot.Environments[0].CycleValues) != 1 || snapshot.Environments[0].CycleValues[0].Status != "noLocalUsage" || snapshot.Environments[1].CycleValues != nil {
		test.Fatal("sample failure or remote isolation", snapshot, err)
	}
}
