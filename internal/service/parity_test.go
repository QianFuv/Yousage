package service

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"yousage/internal/accounting"
	"yousage/internal/domain"
	"yousage/internal/preferences"
	"yousage/internal/refresh"
	"yousage/internal/remote"
)

func TestRawHistoryToDesktopSummaryAndRepricing(test *testing.T) {
	service := testService(test)
	ctx := context.Background()
	fixtures := []struct {
		provider                                   domain.Provider
		path                                       string
		data                                       string
		input, cached, creation, output, reasoning int64
		cost                                       float64
	}{
		{domain.Codex, "sessions/fixture.jsonl", `{"type":"session_meta","timestamp":"2026-10-06T10:00:00Z","payload":{"id":"fixture-codex"}}
{"type":"turn_context","payload":{"model":"fixture"}}
{"type":"event_msg","timestamp":"2026-10-06T10:00:01Z","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":100,"cached_input_tokens":20,"cache_write_input_tokens":10,"output_tokens":40,"reasoning_output_tokens":15}}}}
`, 70, 20, 10, 40, 15, 0.00047},
		{domain.Claude, "projects/fixture.jsonl", `{"type":"assistant","timestamp":"2026-10-06T10:00:01Z","sessionId":"fixture-claude","requestId":"fixture-request","costUSD":0.5,"message":{"id":"fixture-message","model":"fixture","usage":{"input_tokens":70,"cache_read_input_tokens":20,"cache_creation_input_tokens":10,"output_tokens":40}}}
`, 70, 20, 10, 40, 0, 0.5},
	}
	sources := []preferences.Source{}
	for _, fixture := range fixtures {
		root := test.TempDir()
		path := filepath.Join(root, fixture.path)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			test.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(fixture.data), 0600); err != nil {
			test.Fatal(err)
		}
		sources = append(sources, preferences.Source{Id: string(fixture.provider), Provider: fixture.provider, Root: root, IsEnabled: true})
	}
	_, revision, _ := service.Sources(ctx)
	if _, err := service.SaveSources(ctx, sources, revision); err != nil {
		test.Fatal(err)
	}
	rates := RateSnapshot{Rates: accounting.RateTable{"fixture": {TokenRates: accounting.TokenRates{Input: 0.000001, Output: 0.00001, CacheRead: 0, CacheCreation: 0}}}, FetchedAtMs: service.Now().UnixMilli()}
	if _, err := service.Store.WriteSetting(ctx, "public-model-rates", 0, rates); err != nil {
		test.Fatal(err)
	}
	if _, err := service.scanHistory(ctx); err != nil {
		test.Fatal(err)
	}
	for _, days := range []int{1, 7, 30, 90} {
		snapshot, err := service.Usage(ctx, days, "UTC")
		if err != nil {
			test.Fatal(err)
		}
		summary := snapshot.Environments[0].Summary
		if summary == nil || len(summary.Buckets) != 2 || len(summary.Sources) != 2 {
			test.Fatalf("%dd snapshot missing sources: %+v", days, summary)
		}
		for _, fixture := range fixtures {
			var matches int
			for _, bucket := range summary.Buckets {
				if bucket.Provider != fixture.provider {
					continue
				}
				matches++
				if bucket.Totals.UncachedInputTokens != fixture.input || bucket.Totals.CachedInputTokens != fixture.cached || bucket.Totals.CacheCreationTokens != fixture.creation || bucket.Totals.OutputTokens != fixture.output || bucket.Totals.ReasoningTokens != fixture.reasoning || math.Abs(bucket.CostUsd-fixture.cost) > 1e-12 || bucket.Sessions != 1 {
					test.Fatalf("%s totals/cost/session: %+v", fixture.provider, bucket)
				}
			}
			if matches != 1 {
				test.Fatalf("%s lost or duplicated", fixture.provider)
			}
		}
	}
	snapshot, err := service.Usage(ctx, 30, "UTC")
	if err != nil {
		test.Fatal(err)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil || !json.Valid(encoded) {
		test.Fatal("desktop wire invalid", err)
	}
}

func TestPriceCacheFailureDoesNotBlockHistoryOrFreshRates(test *testing.T) {
	service := testService(test)
	ctx := context.Background()
	if _, err := service.Store.WriteSetting(ctx, "public-model-rates", 0, "corrupt fixture"); err != nil {
		test.Fatal(err)
	}
	window, _ := accounting.BuildWindow(service.Now(), 30, "UTC")
	if _, err := service.localSummary(ctx, window); err != nil {
		test.Fatal("optional corrupt rate cache blocked history", err)
	}
	service.RateClient = &http.Client{Transport: fixtureTransport(func(request *http.Request) (*http.Response, error) {
		if err := service.Store.Close(); err != nil {
			test.Fatal(err)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"fixture-provider":{"models":{"fixture":{"cost":{"input":1,"output":2}}}}}`)), Header: http.Header{}}, nil
	})}
	if err := service.RefreshRates(ctx, refresh.Request{IsManual: true}); err != nil {
		test.Fatal("cache write failure rejected valid fresh download", err)
	}
	rates := service.Rates(ctx)
	if len(rates.Rates) != 1 || rates.Provenance(service.Now()).Status != "fresh" {
		test.Fatal(rates)
	}
}

func TestRetiredProviderStateIsIgnored(test *testing.T) {
	service := testService(test)
	ctx := context.Background()
	retired := domain.SourceFingerprint{HostId: "fixture-host", Provider: domain.Provider("cursor"), ResolvedHomePath: "fixture-account", VolumeId: "account"}
	if _, err := service.Store.WriteSetting(ctx, "history-index", 0, []sourceIndex{{ConfigurationId: "cursor", Source: remote.Source{Fingerprint: retired, Status: "ok"}, IsSnapshot: true}}); err != nil {
		test.Fatal(err)
	}
	_, revision, _ := service.Preferences.Sources(ctx)
	if _, err := service.Store.WriteSetting(ctx, "sources", revision, []preferences.Source{{Id: "grok", Provider: domain.Provider("grok"), Root: test.TempDir(), IsEnabled: true}}); err != nil {
		test.Fatal(err)
	}
	window, _ := accounting.BuildWindow(service.Now(), 30, "UTC")
	if summary, err := service.localSummary(ctx, window); err != nil || len(summary.Sources) != 0 {
		test.Fatal("retired provider appeared before rescanning", summary, err)
	}
	if _, err := service.scanHistory(ctx); err != nil {
		test.Fatal("retired provider blocked history scanning", err)
	}
	entries, _, err := service.sourceIndex(ctx)
	if err != nil || len(entries) != 0 {
		test.Fatal("retired provider remained in the source index", entries, err)
	}
}

func TestSummaryDurationMeasuresElapsedTimeAndClampsClockRegression(test *testing.T) {
	service := testService(test)
	ctx := context.Background()
	instant := service.Now()
	for _, elapsed := range []time.Duration{13 * time.Millisecond, -time.Millisecond} {
		calls := 0
		service.Now = func() time.Time {
			calls++
			if calls == 1 {
				return instant
			}
			return instant.Add(elapsed)
		}
		window, _ := accounting.BuildWindow(instant, 30, "UTC")
		summary, err := service.localSummary(ctx, window)
		if err != nil || summary.ScanDurationMs != max(0, elapsed.Milliseconds()) {
			test.Fatal(summary.ScanDurationMs, err)
		}
	}
}
