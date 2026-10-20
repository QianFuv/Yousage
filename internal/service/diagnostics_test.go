package service

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
	"yousage/internal/diagnostics"
	"yousage/internal/quota"
	"yousage/internal/refresh"
)

// TestClaudeDiagnosticScanHook observes the actual pre-retention hook with a
// synthetic CLI. Failed probes retain accepted windows and create no fresh sample.
func TestClaudeDiagnosticScanHook(test *testing.T) {
	service := testService(test)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	directory := test.TempDir()
	executable := filepath.Join(directory, "claude-diagnostic-fixture")
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	command := exec.CommandContext(ctx, "go", "build", "-o", executable, "../../tools/spikes/claude")
	if output, err := command.CombinedOutput(); err != nil {
		test.Fatalf("synthetic CLI build: %s %v", output, err)
	}
	configuration, err := service.Configuration(ctx)
	if err != nil {
		test.Fatal(err)
	}
	configuration.Value.Providers = []NativeProvider{{Id: "private-target-canary", Label: "private-provider-canary", Driver: "claudeAgent", Executable: executable, Home: directory, IsEnabled: true}}
	if _, err := service.SaveConfiguration(ctx, configuration); err != nil {
		test.Fatal(err)
	}
	service.Environment["YOUSAGE_FIXTURE_SECRET"] = "private-credential-canary"
	service.Environment["YOUSAGE_FIXTURE_SCENARIO"] = "usage-auth"
	logDirectory := test.TempDir()
	if err := diagnostics.Initialize(logDirectory); err != nil {
		test.Fatal(err)
	}
	store, err := diagnostics.Open(logDirectory)
	if err != nil {
		test.Fatal(err)
	}
	service.SetDiagnostics(store)
	if _, err := service.scanLimits(ctx); err != nil {
		test.Fatal(err)
	}
	first, err := store.Report()
	if err != nil {
		test.Fatal(err)
	}
	var firstProbe map[string]any
	for _, line := range strings.Split(first, "\n")[1:] {
		var record map[string]any
		if json.Unmarshal([]byte(line), &record) == nil && record["source"] == "claude" {
			firstProbe = record
		}
	}
	if firstProbe["outcome"] != "failed" || firstProbe["failure"] != "authentication" || firstProbe["status"] != float64(0) {
		test.Fatal("fresh CLI failure lost before retention")
	}
	previous, revision, err := service.quotas(ctx)
	if err != nil {
		test.Fatal(err)
	}
	accepted := &quota.Limits{CheckedAt: service.Now().Format(time.RFC3339), Windows: []quota.Window{{Id: "session", Kind: "session", UsedPercent: 42}}}
	previous.Providers[0].Limits = accepted
	previous.Providers[0].Auth.Status = "authenticated"
	if _, err := service.Store.WriteSetting(ctx, "local-quotas", revision, previous); err != nil {
		test.Fatal(err)
	}
	if _, err := service.scanLimits(ctx); err != nil {
		test.Fatal(err)
	}
	result, _, err := service.quotas(ctx)
	if err != nil || !reflect.DeepEqual(result.Providers[0].Limits, accepted) {
		test.Fatal("logging changed last-good limits", err)
	}
	var samples []quota.CycleSeries
	if _, err := service.Store.ReadSetting(ctx, "quota-samples", &samples); err != nil || len(samples) != 0 {
		test.Fatal("retained probe created fresh samples", err)
	}
	report, err := store.Report()
	if err != nil {
		test.Fatal(err)
	}
	var retainedOperation string
	for _, line := range strings.Split(report, "\n")[1:] {
		var record map[string]any
		if json.Unmarshal([]byte(line), &record) == nil && record["source"] == "claude" && record["outcome"] == "retained" {
			retainedOperation, _ = record["operationId"].(string)
		}
	}
	if retainedOperation == "" {
		test.Fatal("retained failure missing")
	}
	count := 0
	for _, line := range strings.Split(report, "\n")[1:] {
		var record map[string]any
		if json.Unmarshal([]byte(line), &record) == nil && record["operationId"] == retainedOperation {
			count++
		}
	}
	if count != 3 {
		test.Fatal("scan operation did not correlate start, probe and completion")
	}
	for _, canary := range []string{"private-target-canary", "private-provider-canary", "private-credential-canary", directory} {
		if strings.Contains(report, canary) {
			test.Fatal("private scan metadata leaked")
		}
	}
	if err := os.Rename(filepath.Join(logDirectory, "go.jsonl"), filepath.Join(logDirectory, "go.1.jsonl")); err != nil {
		test.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(logDirectory, "go.jsonl"), 0700); err != nil {
		test.Fatal(err)
	}
	if _, err := service.scanLimits(ctx); err != nil {
		test.Fatal("log failure changed probe result", err)
	}
	result, _, err = service.quotas(ctx)
	if err != nil || !reflect.DeepEqual(result.Providers[0].Limits, accepted) {
		test.Fatal("log failure changed accepted limits", err)
	}
	if _, err := service.Store.ReadSetting(ctx, "quota-samples", &samples); err != nil || len(samples) != 0 {
		test.Fatal("log failure created fresh samples", err)
	}
}

// TestClaudeDiagnosticClassification verifies closed evidence mappings and
// preserves the distinction between API JSON failures and local CLI failures.
func TestClaudeDiagnosticClassification(test *testing.T) {
	cases := []struct {
		message string
		failure diagnostics.Failure
		status  int
	}{
		{"Claude usage API rejected the stored credential (HTTP 401).", diagnostics.FailureAuthentication, 401},
		{"Claude usage API denied access to usage limits (HTTP 403).", diagnostics.FailureHttp, 403},
		{"Claude usage API rate limit reached (HTTP 429). Try again later.", diagnostics.FailureHttp, 429},
		{"Claude usage API is temporarily unavailable (HTTP 5xx). Try again later.", diagnostics.FailureHttp, 500},
		{"Claude usage API returned an unexpected HTTP status.", diagnostics.FailureHttp, 0},
		{"Claude usage API request timed out.", diagnostics.FailureTimeout, 0},
		{"Claude Code usage probe timed out.", diagnostics.FailureTimeout, 0},
		{"Claude usage API request was cancelled.", diagnostics.FailureNone, 0},
		{"Claude Code usage probe was cancelled.", diagnostics.FailureNone, 0},
		{"Claude usage API could not be reached or read because of a network error.", diagnostics.FailureNetwork, 0},
		{"Claude Code usage probe could not communicate with the CLI.", diagnostics.FailureNetwork, 0},
		{"Claude Code stored access token has expired; usage limits could not be read.", diagnostics.FailureCredentialsExpired, 0},
		{"Claude Code stored credentials are malformed.", diagnostics.FailureProtocol, 0},
		{"Claude Code returned an invalid usage probe response.", diagnostics.FailureProtocol, 0},
		{"Claude returned invalid usage limits.", diagnostics.FailureProtocol, 0},
		{"Claude usage API returned invalid JSON.", diagnostics.FailureProtocol, 200},
		{"Claude Code rejected authentication for the usage probe.", diagnostics.FailureAuthentication, 0},
		{"Claude Code login could not be read.", diagnostics.FailureIo, 0},
		{"https://secret.invalid/key-canary HTTP 401", diagnostics.FailureUnavailable, 0},
	}
	previous := &quota.Limits{CheckedAt: "synthetic-old", Windows: []quota.Window{{Id: "private-window", UsedPercent: 33}}}
	for _, scenario := range cases {
		test.Run(scenario.message, func(test *testing.T) {
			probed := quota.Limits{Unavailable: &quota.Unavailable{Reason: "probeFailed", Message: scenario.message}}
			entry := claudeDiagnostic(probed, previous)
			if entry.Failure != scenario.failure || entry.Status != scenario.status || entry.Outcome != diagnostics.OutcomeRetained || entry.Count != 0 {
				test.Fatalf("unexpected safe classification: %+v", entry)
			}
			if result := quota.ResolveProbe(previous, probed); !reflect.DeepEqual(result, *previous) {
				test.Fatal("logging changed retention")
			}
			entry = claudeDiagnostic(probed, nil)
			if entry.Outcome == diagnostics.OutcomeRetained {
				test.Fatal("claimed nonexistent retained data")
			}
		})
	}
	for _, scenario := range []struct {
		reason  string
		failure diagnostics.Failure
	}{{"unsupported", diagnostics.FailureUnsupported}, {"missing", diagnostics.FailureCredentialsMissing}, {"credentialsMissing", diagnostics.FailureCredentialsMissing}} {
		entry := claudeDiagnostic(quota.Limits{Unavailable: &quota.Unavailable{Reason: scenario.reason}}, previous)
		if entry.Failure != scenario.failure || entry.Outcome == diagnostics.OutcomeRetained {
			test.Fatal("invalid unavailable classification")
		}
	}
	if entry := claudeDiagnostic(*previous, nil); entry.Outcome != diagnostics.OutcomeSucceeded || entry.Failure != diagnostics.FailureNone || entry.Count != 1 {
		test.Fatal("successful probe classification changed")
	}
}

// TestServiceDiagnosticFailurePreservesHistory uses only empty synthetic sources;
// a blocked log file cannot turn a successful refresh into an application failure.
func TestServiceDiagnosticFailurePreservesHistory(test *testing.T) {
	service := testService(test)
	directory := test.TempDir()
	if err := diagnostics.Initialize(directory); err != nil {
		test.Fatal(err)
	}
	store, err := diagnostics.Open(directory)
	if err != nil {
		test.Fatal(err)
	}
	service.SetDiagnostics(store)
	if err := os.Mkdir(filepath.Join(directory, "go.jsonl"), 0700); err != nil {
		test.Fatal(err)
	}
	if err := service.RefreshHistory(context.Background(), refresh.Request{IsManual: true}); err != nil {
		test.Fatal("log failure changed refresh", err)
	}
	previous := &quota.Limits{Windows: []quota.Window{{Id: "private-window", UsedPercent: 40}}}
	probed := quota.Limits{Unavailable: &quota.Unavailable{Reason: "probeFailed", Message: "https://secret.invalid/credential-canary"}}
	service.recordClaudeProbe(probed, previous, store.NewOperation(), time.Now())
	if result := quota.ResolveProbe(previous, probed); !reflect.DeepEqual(result, *previous) {
		test.Fatal("log failure changed retention")
	}
}

// TestClaudeDiagnosticReportRedactsUnknownMessage verifies exact classification
// never serializes an unknown URL, provider label, account or credential message.
func TestClaudeDiagnosticReportRedactsUnknownMessage(test *testing.T) {
	directory := test.TempDir()
	if err := diagnostics.Initialize(directory); err != nil {
		test.Fatal(err)
	}
	store, err := diagnostics.Open(directory)
	if err != nil {
		test.Fatal(err)
	}
	service := &Service{}
	service.SetDiagnostics(store)
	service.recordClaudeProbe(quota.Limits{Unavailable: &quota.Unavailable{Reason: "probeFailed", Message: "credential-canary https://secret.invalid private-provider account@example.invalid"}}, nil, store.NewOperation(), time.Now())
	report, err := store.Report()
	if err != nil || !strings.Contains(report, `"failure":"unavailable"`) {
		test.Fatal("classification missing", err)
	}
	for _, canary := range []string{"credential-canary", "secret.invalid", "private-provider", "account@example.invalid"} {
		if strings.Contains(report, canary) {
			test.Fatal("private probe data leaked")
		}
	}
}

// TestClaudeDiagnosticCancelledRetention keeps cancellation evidence alongside
// the actual retained outcome without claiming a new successful observation.
func TestClaudeDiagnosticCancelledRetention(test *testing.T) {
	directory := test.TempDir()
	if err := diagnostics.Initialize(directory); err != nil {
		test.Fatal(err)
	}
	store, err := diagnostics.Open(directory)
	if err != nil {
		test.Fatal(err)
	}
	service := &Service{}
	service.SetDiagnostics(store)
	previous := &quota.Limits{Windows: []quota.Window{{Id: "session", UsedPercent: 20}}}
	service.recordClaudeProbe(quota.Limits{Unavailable: &quota.Unavailable{Reason: "probeFailed", Message: "Claude usage API request was cancelled."}}, previous, store.NewOperation(), time.Now())
	report, err := store.Report()
	if err != nil || !strings.Contains(report, `"outcome":"cancelled"`) || !strings.Contains(report, `"outcome":"retained"`) || strings.Contains(report, `"outcome":"succeeded"`) {
		test.Fatal("cancellation or retention evidence lost", err)
	}
}
