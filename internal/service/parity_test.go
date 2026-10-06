package service

import (
	"context"
	"encoding/json"
	"github.com/coder/websocket"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
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
		{domain.Grok, "sessions/fixture/updates.jsonl", `{"timestamp":1791280801,"params":{"sessionId":"fixture-grok","update":{"sessionUpdate":"turn_completed","prompt_id":"fixture-prompt","usage":{"costUsdTicks":10000000000,"modelUsage":{"fixture":{"inputTokens":100,"cachedReadTokens":20,"cacheCreationTokens":10,"outputTokens":40,"reasoningTokens":15}}}}}}
`, 70, 20, 10, 40, 15, 1},
		{domain.OpenCode, "storage/message/fixture/message.json", `{"role":"assistant","sessionID":"fixture-opencode","modelID":"fixture","time":{"created":1791280801000},"tokens":{"input":70,"output":25,"reasoning":15,"cache":{"read":20,"write":10}},"cost":0.25}`, 70, 20, 10, 40, 15, 0.25},
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
		if summary == nil || len(summary.Buckets) != 4 || len(summary.Sources) != 4 {
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
	free := domain.PriceOverride{}
	outcomes := service.ApplyPricing(ctx, []string{service.LocalId}, preferences.PricingPatch{Overrides: map[string]*domain.PriceOverride{"fixture": &free}})
	if !outcomes[0].IsSaved {
		test.Fatal(outcomes)
	}
	snapshot, err := service.Usage(ctx, 30, "UTC")
	if err != nil {
		test.Fatal(err)
	}
	for _, bucket := range snapshot.Environments[0].Summary.Buckets {
		if bucket.CostUsd != 0 || bucket.CostSource != "modelPriced" {
			test.Fatal("override failed to reprice retained records", bucket)
		}
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
	rates, err := service.Rates(ctx)
	if err != nil || len(rates.Rates) != 1 || rates.Provenance(service.Now()).Status != "fresh" {
		test.Fatal(rates, err)
	}
}

func TestInactiveCursorCannotExposePreviousAccountHistory(test *testing.T) {
	for _, environment := range []map[string]string{{"AGENT_CLI_CREDENTIAL_STORE": "memory"}, {"CURSOR_API_KEY": "fixture-api"}, {"CURSOR_AUTH_TOKEN": "fixture-ephemeral"}, {}} {
		test.Run(stringMustJson(environment), func(test *testing.T) {
			service := testService(test)
			ctx := context.Background()
			root := test.TempDir()
			service.Platform = "windows"
			service.Environment = environment
			fingerprint := domain.SourceFingerprint{HostId: "cursor-account", Provider: domain.Cursor, ResolvedHomePath: "fixture-account-A", VolumeId: "account"}
			record := domain.Record{Provider: domain.Cursor, SessionId: "session", Model: "fixture", Speed: "standard", TimestampMs: service.Now().Add(-time.Hour).UnixMilli(), DedupeKey: "billed-event", Totals: domain.Tokens{OutputTokens: 10}}
			if err := service.storeSnapshot(ctx, fingerprint, []domain.Record{record}, "ok", "", service.Now()); err != nil {
				test.Fatal(err)
			}
			entry := sourceIndex{ConfigurationId: "cursor", Source: remote.Source{Fingerprint: fingerprint, Status: "ok"}, IsSnapshot: true}
			if _, err := service.Store.WriteSetting(ctx, "history-index", 0, []sourceIndex{entry}); err != nil {
				test.Fatal(err)
			}
			_, revision, _ := service.Sources(ctx)
			if _, err := service.SaveSources(ctx, []preferences.Source{{Id: "cursor", Provider: domain.Cursor, Root: filepath.Join(root, "absent-auth.json"), IsEnabled: true}}, revision); err != nil {
				test.Fatal(err)
			}
			if _, err := service.scanHistory(ctx); err != nil {
				test.Fatal(err)
			}
			window, _ := accounting.BuildWindow(service.Now(), 30, "UTC")
			summary, err := service.localSummary(ctx, window)
			if err != nil || len(summary.Buckets) != 0 || len(summary.Sources) != 0 {
				test.Fatal("inactive saved login leaked old account", summary, err)
			}
			records, err := service.Store.Records(ctx, fingerprint.Id())
			if err != nil || len(records) != 1 {
				test.Fatal("suppression deleted retained account records", err)
			}
		})
	}
}

func stringMustJson(value any) string { data, _ := json.Marshal(value); return string(data) }

func TestMissingCursorOnlyOffersActualKeychainOptIn(test *testing.T) {
	service := testService(test)
	ctx := context.Background()
	root := test.TempDir()
	service.Platform = "darwin"
	service.Environment = map[string]string{}
	previousAccount := domain.SourceFingerprint{HostId: "cursor-account", Provider: domain.Cursor, ResolvedHomePath: "old-account", VolumeId: "account"}
	instant := service.Now()
	if err := service.storeSnapshot(ctx, previousAccount, []domain.Record{{Provider: domain.Cursor, SessionId: "session", Model: "fixture", Speed: "standard", TimestampMs: instant.UnixMilli(), Totals: domain.Tokens{OutputTokens: 1}}}, "ok", "", instant); err != nil {
		test.Fatal(err)
	}
	if _, err := service.Store.WriteSetting(ctx, "history-index", 0, []sourceIndex{{ConfigurationId: "cursor", Source: remote.Source{Fingerprint: previousAccount, Status: "ok"}, IsSnapshot: true}}); err != nil {
		test.Fatal(err)
	}
	_, revision, _ := service.Sources(ctx)
	if _, err := service.SaveSources(ctx, []preferences.Source{{Id: "cursor", Provider: domain.Cursor, Root: filepath.Join(root, "auth.json"), IsEnabled: true}}, revision); err != nil {
		test.Fatal(err)
	}
	if _, err := service.scanHistory(ctx); err != nil {
		test.Fatal(err)
	}
	window, _ := accounting.BuildWindow(service.Now(), 30, "UTC")
	summary, err := service.localSummary(ctx, window)
	if err != nil || len(summary.Sources) != 1 || summary.Sources[0].Action != "enableCursorKeychain" || len(summary.Buckets) != 0 {
		test.Fatal(summary, err)
	}
	service.Now = func() time.Time { return instant.Add(91 * 24 * time.Hour) }
	if _, err := service.scanHistory(ctx); err != nil {
		test.Fatal(err)
	}
	records, err := service.Store.Records(ctx, previousAccount.Id())
	if err != nil || len(records) != 0 {
		test.Fatal("orphaned previous account escaped retention", err)
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

func TestRemoteRateFailureStillRefreshesSummary(test *testing.T) {
	service := testService(test)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var summaryCalls int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/.well-known/t3/environment":
			json.NewEncoder(writer).Encode(remote.Environment{Id: "remote-fixture", Label: "Remote", Protocol: 2, Capabilities: remote.Capabilities{}})
		case "/api/auth/session":
			json.NewEncoder(writer).Encode(remote.Authorization{IsAuthenticated: true, Scopes: []string{"orchestration:read"}, Auth: &remote.AuthDescriptor{Policy: "remote-reachable", BootstrapMethods: []string{"one-time-token"}, SessionMethods: []string{"bearer-access-token"}, SessionCookieName: "fixture-cookie"}})
		case "/api/auth/websocket-ticket":
			json.NewEncoder(writer).Encode(map[string]string{"ticket": "fixture-ticket", "expiresAt": time.Now().Add(time.Minute).UTC().Format(time.RFC3339)})
		case "/ws":
			socket, err := websocket.Accept(writer, request, nil)
			if err != nil {
				return
			}
			defer socket.CloseNow()
			for {
				_, data, err := socket.Read(request.Context())
				if err != nil {
					return
				}
				var frame struct {
					Tag    string `json:"_tag"`
					Id     string `json:"id"`
					Method string `json:"tag"`
				}
				json.Unmarshal(data, &frame)
				if frame.Tag == "Ping" {
					socket.Write(request.Context(), websocket.MessageText, []byte(`{"_tag":"Pong"}`))
					continue
				}
				if frame.Tag != "Request" {
					continue
				}
				exit := map[string]any{"_tag": "Failure", "cause": []any{map[string]any{"_tag": "Fail", "error": map[string]string{"_tag": "UsageRatesError"}}}}
				if frame.Method == "server.getUsageSummary" {
					summaryCalls++
					exit = map[string]any{"_tag": "Success", "value": remote.Summary{ContractVersion: 6, ReadAt: service.Now().Format(time.RFC3339), TimeZone: "UTC", SinceDay: "2026-09-07", UntilDay: "2026-10-06", Buckets: []accounting.Bucket{}, Sources: []remote.Source{}, Pricing: remote.Pricing{Status: "cached", Source: "fixture", KnownModels: 1}, ScanDurationMs: 17}}
				}
				response, _ := json.Marshal(map[string]any{"_tag": "Exit", "requestId": frame.Id, "exit": exit})
				socket.Write(request.Context(), websocket.MessageText, response)
			}
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	connection := remote.Connection{EnvironmentId: "remote-fixture", Url: server.URL, CredentialId: "fixture"}
	connector := remote.Connector{Secrets: memoryCredentials{"fixture": "synthetic-bearer"}}
	client, err := connector.Connect(ctx, connection)
	if err != nil {
		test.Fatal(err)
	}
	defer client.Close()
	service.connections[connection.EnvironmentId] = &connectionState{client: client, cancel: func() {}}
	saved, _ := service.Configuration(ctx)
	saved.Value.Connections = []ConnectionPreference{{Connection: connection, Label: "Remote", IsEnabled: true}}
	if _, err := service.Store.WriteSetting(ctx, "configuration", saved.Revision, saved.Value); err != nil {
		test.Fatal(err)
	}
	if err := service.RefreshUsage(ctx, 30, "UTC", []string{connection.EnvironmentId}, refresh.Request{IsManual: true}); err == nil {
		test.Fatal("rate failure disappeared")
	}
	if summaryCalls != 1 {
		test.Fatal("rate failure blocked summary", summaryCalls)
	}
	window, _ := service.viewWindow(30, "UTC", false)
	var summary remote.Summary
	if _, err := service.Store.ReadSetting(ctx, "remote-usage:"+connection.EnvironmentId+":"+windowKey(window), &summary); err != nil || summary.ScanDurationMs != 17 {
		test.Fatal("latest summary not retained", summary, err)
	}
}
