package sync

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"yousage/internal/diagnostics"
)

// attachSyncDiagnostics installs one synthetic application writer before work.
func attachSyncDiagnostics(test *testing.T, fixture *engineFixture) string {
	test.Helper()
	directory := test.TempDir()
	if err := diagnostics.Initialize(directory); err != nil {
		test.Fatal(err)
	}
	store, err := diagnostics.Open(directory)
	if err != nil {
		test.Fatal(err)
	}
	fixture.engine.options.Diagnostics = store
	return directory
}

// TestSyncDiagnosticsPreserveCacheAndRedact logs phases without exporting relay
// targets, credentials, content hashes or dashboard payloads.
func TestSyncDiagnosticsPreserveCacheAndRedact(test *testing.T) {
	fixture := newEngineFixture(test)
	attachSyncDiagnostics(test, fixture)
	fixture.snapshot.AppVersion = "snapshot-credential-canary"
	state := fixture.enable(test)
	puts := fixture.puts.Load()
	if _, err := fixture.engine.SyncNow(context.Background()); err != nil || fixture.puts.Load() != puts {
		test.Fatal("unchanged cache behavior changed", err)
	}
	report, err := fixture.engine.options.Diagnostics.Report()
	if err != nil {
		test.Fatal(err)
	}
	for _, event := range []string{"validation", "stage", "commit", "cache"} {
		if !strings.Contains(report, `"event":"`+event+`"`) {
			test.Fatalf("missing %s", event)
		}
	}
	for _, canary := range []string{fixtureToken, state.PairingUrl, fixture.server.URL, fixture.engine.settings.SpaceId, fixture.engine.spaceKey, "snapshot-credential-canary"} {
		if strings.Contains(report, canary) {
			test.Fatal("sync data leaked")
		}
	}
}

// TestSyncDiagnosticFailureDoesNotChangePush makes only the log path unavailable.
func TestSyncDiagnosticFailureDoesNotChangePush(test *testing.T) {
	fixture := newEngineFixture(test)
	directory := attachSyncDiagnostics(test, fixture)
	if err := os.Mkdir(filepath.Join(directory, "go.jsonl"), 0700); err != nil {
		test.Fatal(err)
	}
	fixture.enable(test)
	if !fixture.engine.hasHash || fixture.puts.Load() != 1 {
		test.Fatal("logging failure prevented upload")
	}
	if _, err := fixture.engine.SyncNow(context.Background()); err != nil || fixture.puts.Load() != 1 {
		test.Fatal("logging failure invalidated accepted cache", err)
	}
}

// TestSyncDiagnosticsClassifyFailuresRetainAcceptedCache covers unavailable
// builders, encoding failures and synthetic offline transport failures.
func TestSyncDiagnosticsClassifyFailuresRetainAcceptedCache(test *testing.T) {
	fixture := newEngineFixture(test)
	attachSyncDiagnostics(test, fixture)
	fixture.enable(test)
	acceptedHash, acceptedUpload := fixture.engine.lastHash, fixture.engine.lastUpload
	fixture.buildError = errors.New("https://secret.invalid/credential-canary")
	if _, err := fixture.engine.SyncNow(context.Background()); err == nil {
		test.Fatal("build failure was ignored")
	}
	fixture.buildError = nil
	fixture.snapshot.Bootstrap = make(chan int)
	if _, err := fixture.engine.SyncNow(context.Background()); err == nil {
		test.Fatal("invalid shape encoding was ignored")
	}
	fixture.snapshot.Bootstrap = "changed"
	fixture.engine.client = NewClient(&http.Client{Transport: offlineDiagnosticTransport{}})
	if _, err := fixture.engine.SyncNow(context.Background()); err == nil {
		test.Fatal("network failure was ignored")
	}
	if fixture.engine.lastHash != acceptedHash || !fixture.engine.lastUpload.Equal(acceptedUpload) {
		test.Fatal("failed push changed accepted cache")
	}
	report, err := fixture.engine.options.Diagnostics.Report()
	if err != nil {
		test.Fatal(err)
	}
	for _, failure := range []string{"unavailable", "protocol", "network"} {
		if !strings.Contains(report, `"failure":"`+failure+`"`) {
			test.Fatalf("missing %s", failure)
		}
	}
	if strings.Contains(report, "credential-canary") || strings.Contains(report, "secret.invalid") {
		test.Fatal("raw error leaked")
	}
}

// offlineDiagnosticTransport refuses requests without contacting any remote host.
type offlineDiagnosticTransport struct{}

// RoundTrip injects a private raw error to verify closed classification.
func (offlineDiagnosticTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("https://secret.invalid/credential-canary")
}
