package desktop

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"yousage/internal/diagnostics"
	"yousage/internal/domain"
	"yousage/internal/preferences"
	"yousage/internal/remote"
	dataservice "yousage/internal/service"
)

// TestDesktopDiagnosticMetadataExcludesPrivateConfiguration ensures report
// metadata is reduced to counts instead of serializing configuration or history.
func TestDesktopDiagnosticMetadataExcludesPrivateConfiguration(test *testing.T) {
	app, err := newAppInDirectory(test.TempDir(), false)
	if err != nil {
		test.Fatal(err)
	}
	defer app.store.Close()
	defer app.logFile.Close()
	app.context = context.Background()
	app.usage = &dataservice.Service{Store: app.store, Preferences: preferences.New(app.store)}
	config := dataservice.Configuration{PrimaryEnvironmentId: "account-canary", Providers: []dataservice.NativeProvider{{Id: "target-canary", Driver: "claudeAgent", Label: "provider-canary", Home: "path-canary"}}, Connections: []dataservice.ConnectionPreference{{Label: "connection-canary", Connection: remote.Connection{Url: "https://secret.invalid", EnvironmentId: "account-canary", CredentialId: "credential-canary"}}}}
	if _, err := app.store.WriteSetting(app.context, "configuration", 0, config); err != nil {
		test.Fatal(err)
	}
	if _, err := app.usage.Preferences.SaveSources(app.context, []preferences.Source{{Id: "source-canary", Provider: domain.Claude, Root: "history-path-canary"}}, 0); err != nil {
		test.Fatal(err)
	}
	report, err := app.GetDiagnostics()
	if err != nil {
		test.Fatal(err)
	}
	var decoded diagnosticReport
	if json.Unmarshal([]byte(report), &decoded) != nil || !decoded.MetadataAvailable || decoded.HistorySources != 1 || decoded.RemoteEnvironments != 1 || decoded.QuotaTargets != 1 {
		test.Fatal("whitelisted metadata missing")
	}
	for _, canary := range []string{"account-canary", "target-canary", "provider-canary", "path-canary", "connection-canary", "secret.invalid", "credential-canary", "source-canary", "history-path-canary"} {
		if strings.Contains(report, canary) {
			test.Fatal("configuration leaked")
		}
	}
}

// diagnosticApp opens an isolated synthetic writer without native windows.
func diagnosticApp(test *testing.T) (*App, string) {
	test.Helper()
	directory := test.TempDir()
	if err := diagnostics.Initialize(directory); err != nil {
		test.Fatal(err)
	}
	store, err := diagnostics.Open(directory)
	if err != nil {
		test.Fatal(err)
	}
	return &App{diagnostics: store}, directory
}

// TestFrontendDiagnosticAllowsOnlyBoundedCategories enforces the bridge boundary
// before service startup, including its rolling-minute allowance.
func TestFrontendDiagnosticAllowsOnlyBoundedCategories(test *testing.T) {
	app, _ := diagnosticApp(test)
	for _, category := range []string{"", "Render", "render\n", "https://secret.invalid", strings.Repeat("x", 1000)} {
		app.RecordFrontendDiagnostic(category)
	}
	app.frontendDiagnosticTimes = []time.Time{time.Now().Add(-2 * time.Minute)}
	for index := 0; index < 15; index++ {
		app.RecordFrontendDiagnostic("render")
	}
	report, err := app.diagnostics.Report()
	if err != nil || strings.Count(report, `"source":"frontend"`) != 10 {
		test.Fatal("frontend category bound failed", err)
	}
	if strings.Contains(report, "secret.invalid") {
		test.Fatal("input was echoed")
	}
	app.frontendDiagnosticTimes = []time.Time{time.Now().Add(-2 * time.Minute)}
	app.RecordFrontendDiagnostic("internal")
	report, _ = app.diagnostics.Report()
	if !strings.Contains(report, `"failure":"internal"`) {
		test.Fatal("internal category missing")
	}
}

// TestDesktopDiagnosticsBeforeStartupAndFailure returns a safe partial report
// without leaking file errors or requiring a successfully initialized usage service.
func TestDesktopDiagnosticsBeforeStartupAndFailure(test *testing.T) {
	app, directory := diagnosticApp(test)
	app.RecordFrontendDiagnostic("render")
	if err := os.Mkdir(filepath.Join(directory, "native.jsonl"), 0700); err != nil {
		test.Fatal(err)
	}
	report, err := app.GetDiagnostics()
	if err != nil || !strings.Contains(report, `"diagnosticsAvailable": false`) || !strings.Contains(report, "frontend_error") {
		test.Fatal("safe partial report unavailable", err)
	}
	if strings.Contains(report, directory) {
		test.Fatal("report leaked directory")
	}
	if value, err := (&App{}).GetDiagnostics(); err != nil || !strings.Contains(value, `"metadataAvailable": false`) {
		test.Fatal("pre-startup report failed", err)
	}
}

// TestDesktopDiagnosticReportBoundsAndCrossWriter keeps newest validated Go and
// native records, accounting for the JSON wrapper and escaped log newlines.
func TestDesktopDiagnosticReportBoundsAndCrossWriter(test *testing.T) {
	app, directory := diagnosticApp(test)
	entry := diagnostics.Entry{Level: diagnostics.LevelInfo, Source: diagnostics.SourceDesktop, Event: diagnostics.EventCache, Outcome: diagnostics.OutcomeSucceeded}
	for index := 0; index < 450; index++ {
		entry.Count = int64(index)
		if err := app.diagnostics.Record(entry); err != nil {
			test.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(directory, "go.jsonl"))
	if err != nil {
		test.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	var native map[string]any
	if json.Unmarshal([]byte(lines[len(lines)-1]), &native) != nil {
		test.Fatal("invalid fixture")
	}
	native["source"], native["operationId"] = "native", "n-1"
	nativeData, _ := json.Marshal(native)
	if err := os.WriteFile(filepath.Join(directory, "native.jsonl"), append(nativeData, '\n'), 0600); err != nil {
		test.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "go.1.jsonl"), []byte("https://secret.invalid/credential-canary\n"), 0600); err != nil {
		test.Fatal(err)
	}
	report, err := app.GetDiagnostics()
	if err != nil || len(report)+1 > diagnostics.MaxReportBytes {
		test.Fatal("report exceeded budget", len(report), err)
	}
	var decoded diagnosticReport
	if json.Unmarshal([]byte(report), &decoded) != nil {
		test.Fatal("invalid outer JSON")
	}
	if !strings.Contains(decoded.Logs, `"source":"native"`) || !strings.Contains(decoded.Logs, `"count":449`) || !strings.Contains(decoded.Logs, "[truncated]") {
		test.Fatal("newest cross-writer records missing")
	}
	if strings.Contains(report, "credential-canary") || strings.Contains(report, "secret.invalid") {
		test.Fatal("unvalidated data leaked")
	}
}

// TestDesktopDiagnosticFailureDoesNotBlockStartup preserves selected state and
// legacy lifecycle files when optional diagnostics cannot be initialized.
func TestDesktopDiagnosticFailureDoesNotBlockStartup(test *testing.T) {
	directory := test.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "diagnostics"), []byte("legacy-user-file"), 0600); err != nil {
		test.Fatal(err)
	}
	app, err := newAppInDirectory(directory, false)
	if err != nil {
		test.Fatal(err)
	}
	defer app.store.Close()
	defer app.logFile.Close()
	if app.diagnostics != nil || app.stateDirectory != directory {
		test.Fatal("optional failure changed startup")
	}
	data, err := os.ReadFile(filepath.Join(directory, "diagnostics"))
	if err != nil || string(data) != "legacy-user-file" {
		test.Fatal("legacy file changed")
	}
}
