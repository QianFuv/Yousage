package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"yousage/internal/accounting"
	"yousage/internal/domain"
	"yousage/internal/preferences"
	"yousage/internal/refresh"
	"yousage/internal/remote"
	"yousage/internal/storage"
)

func testService(test *testing.T) *Service {
	test.Helper()
	store, err := storage.Open(filepath.Join(test.TempDir(), "state.sqlite"))
	if err != nil {
		test.Fatal(err)
	}
	if _, err = preferences.New(store).SaveSources(context.Background(), []preferences.Source{}, 0); err != nil {
		test.Fatal(err)
	}
	service, err := New(context.Background(), store, nil)
	if err != nil {
		test.Fatal(err)
	}
	service.Now = func() time.Time { return time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC) }
	test.Cleanup(func() { service.Close(); store.Close() })
	return service
}

func TestRollingWindowRetainsRemoteCacheAcrossMinute(test *testing.T) {
	service := testService(test)
	ctx := context.Background()
	saved, _ := service.Configuration(ctx)
	service.draftCredentials["fixture"] = draftCredential{Kind: "environment", Origin: "https://fixture.invalid", Target: "remote"}
	saved.Value.Connections = []ConnectionPreference{{Connection: remote.Connection{EnvironmentId: "remote", Url: "https://fixture.invalid", CredentialId: "fixture"}, Label: "Remote", IsEnabled: false}}
	if _, err := service.SaveConfiguration(ctx, saved); err != nil {
		test.Fatal(err)
	}
	first, err := service.Usage(ctx, 1, "UTC")
	if err != nil {
		test.Fatal(err)
	}
	summary := remote.Summary{ContractVersion: 6, Buckets: []accounting.Bucket{}, Sources: []remote.Source{}}
	if _, err := service.Store.WriteSetting(ctx, "remote-usage:remote:"+windowKey(first.Window), 0, summary); err != nil {
		test.Fatal(err)
	}
	service.Now = func() time.Time { return time.Date(2026, 10, 6, 12, 1, 0, 0, time.UTC) }
	second, err := service.Usage(ctx, 1, "UTC")
	if err != nil {
		test.Fatal(err)
	}
	if second.Window != first.Window || second.Environments[1].Summary == nil {
		test.Fatal("rolling cache disappeared during completion/reload")
	}
	advanced, err := service.viewWindow(1, "UTC", true)
	if err != nil || advanced == first.Window {
		test.Fatal("explicit refresh did not advance its anchor", err)
	}
}

func TestCrossRootFactsDeduplicateBeforeSourceSessions(test *testing.T) {
	service := testService(test)
	ctx := context.Background()
	now := service.Now()
	entries := []sourceIndex{}
	for _, root := range []string{test.TempDir(), test.TempDir()} {
		fingerprint := service.fingerprint(domain.OpenCode, root)
		record := domain.Record{Provider: domain.OpenCode, SessionId: "one-session", Model: "model", DedupeKey: "same-message", TimestampMs: now.Add(-time.Hour).UnixMilli(), Speed: "standard", Totals: domain.Tokens{UncachedInputTokens: 10}}
		if err := service.storeSnapshot(ctx, fingerprint, []domain.Record{record}, "ok", "", now); err != nil {
			test.Fatal(err)
		}
		entries = append(entries, sourceIndex{ConfigurationId: root, Source: remote.Source{Fingerprint: fingerprint, Status: "ok"}, IsSnapshot: true})
	}
	if _, err := service.Store.WriteSetting(ctx, "history-index", 0, entries); err != nil {
		test.Fatal(err)
	}
	window, _ := accounting.BuildWindow(now, 7, "UTC")
	summary, err := service.localSummary(ctx, window)
	if err != nil {
		test.Fatal(err)
	}
	var input, sessions int64
	for _, bucket := range summary.Buckets {
		input += bucket.Totals.UncachedInputTokens
	}
	for _, source := range summary.Sources {
		sessions += int64(source.DistinctSessions)
	}
	if input != 10 || sessions != 1 {
		test.Fatalf("copied fact counted more than once: input=%d sessions=%d", input, sessions)
	}
	service.Now = func() time.Time { return now.Add(91 * 24 * time.Hour) }
	expired, _ := accounting.BuildWindow(service.Now(), 90, "UTC")
	summary, err = service.localSummary(ctx, expired)
	if err != nil || len(summary.Buckets) != 0 {
		test.Fatal("expired cached snapshot leaked into retention", err)
	}
}

func TestConfigurationRejectsProviderPrimary(test *testing.T) {
	service := testService(test)
	ctx := context.Background()
	saved, _ := service.Configuration(ctx)
	saved.Value.Providers = []NativeProvider{{Id: "provider", Driver: "cursor"}}
	saved.Value.PrimaryEnvironmentId = "provider"
	if _, err := service.SaveConfiguration(ctx, saved); err == nil {
		test.Fatal("provider accepted as primary environment")
	}
}

func TestMissingSnapshotRetainsCoverageAndDisabledSourcesExpire(test *testing.T) {
	service := testService(test)
	ctx := context.Background()
	root := test.TempDir()
	now := service.Now()
	fingerprint := service.fingerprint(domain.OpenCode, root)
	record := domain.Record{Provider: domain.OpenCode, SessionId: "session", Model: "model", Speed: "standard", DedupeKey: "message", TimestampMs: now.Add(-time.Hour).UnixMilli(), Totals: domain.Tokens{UncachedInputTokens: 10}}
	if err := service.storeSnapshot(ctx, fingerprint, []domain.Record{record}, "ok", "", now); err != nil {
		test.Fatal(err)
	}
	entries := []sourceIndex{{ConfigurationId: "source", Source: remote.Source{Fingerprint: fingerprint, Status: "ok"}, IsSnapshot: true}}
	if _, err := service.Store.WriteSetting(ctx, "history-index", 0, entries); err != nil {
		test.Fatal(err)
	}
	_, revision, _ := service.Sources(ctx)
	revision, err := service.SaveSources(ctx, []preferences.Source{{Id: "source", Provider: domain.OpenCode, Root: root, IsEnabled: true}}, revision)
	if err != nil {
		test.Fatal(err)
	}
	if _, err = service.scanHistory(ctx); err != nil {
		test.Fatal(err)
	}
	retained, _, _ := service.sourceIndex(ctx)
	if retained[0].Source.Status != "partial" {
		test.Fatal("missing reader excluded retained facts from merge coverage")
	}
	if _, err = service.SaveSources(ctx, []preferences.Source{{Id: "source", Provider: domain.OpenCode, Root: root, IsEnabled: false}}, revision); err != nil {
		test.Fatal(err)
	}
	service.Now = func() time.Time { return now.Add(91 * 24 * time.Hour) }
	if _, err = service.scanHistory(ctx); err != nil {
		test.Fatal(err)
	}
	files, err := service.Store.FileRecords(ctx, fingerprint.Id())
	if err != nil || len(files) != 0 {
		test.Fatal("disabled source escaped cache retention", err)
	}
}

type fixtureTransport func(*http.Request) (*http.Response, error)

func (transport fixtureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestRatesKeepLastGoodOnFailedRefreshAndExpireFreshness(test *testing.T) {
	service := testService(test)
	ctx := context.Background()
	calls := 0
	service.RateClient = &http.Client{Transport: fixtureTransport(func(request *http.Request) (*http.Response, error) {
		calls++
		if calls > 1 {
			return nil, errors.New("fixture network failure")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"fixture-provider":{"models":{"model":{"cost":{"input":1,"output":2}}}}}`)), Header: http.Header{}}, nil
	})}
	request := refresh.Request{IsManual: true, IsVisible: true, IsUsagePage: true, IsAutoEnabled: true}
	if err := service.RefreshRates(ctx, request); err != nil {
		test.Fatal(err)
	}
	first, _ := service.Rates(ctx)
	if len(first.Rates) != 1 || first.Provenance(service.Now()).Status != "fresh" {
		test.Fatal(first)
	}
	if err := service.RefreshRates(ctx, request); err != nil || calls != 1 {
		test.Fatal("manual floor failed", err, calls)
	}
	instant := service.Now().Add(25 * time.Hour)
	service.Now = func() time.Time { return instant }
	if first.Provenance(instant).Status != "cached" {
		test.Fatal("expired table described as fresh")
	}
	service.rates.Close()
	service.rates = refresh.New[RateSnapshot](refresh.RatesPolicy)
	request.AfterPending = true
	if err := service.RefreshRates(ctx, request); err == nil {
		test.Fatal("fixture failure not surfaced")
	}
	retained, _ := service.Rates(ctx)
	if len(retained.Rates) != 1 || retained.Provenance(instant).Status != "cached" {
		test.Fatal("failure replaced last-good rates")
	}
}
