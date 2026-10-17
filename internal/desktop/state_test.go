package desktop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"yousage/internal/preferences"
)

// TestPortableRestartPreservesDatabase verifies upgrades reopen saved data instead of resetting it.
func TestPortableRestartPreservesDatabase(test *testing.T) {
	directory := test.TempDir()
	first, err := newAppInDirectory(directory, false)
	if err != nil {
		test.Fatal(err)
	}
	ctx := context.Background()
	if _, err = first.store.WriteSetting(ctx, "upgrade-sentinel", 0, "preserved"); err != nil {
		test.Fatal(err)
	}
	if err = first.store.Close(); err != nil {
		test.Fatal(err)
	}
	first.logFile.Close()
	second, err := newAppInDirectory(directory, false)
	if err != nil {
		test.Fatal(err)
	}
	defer second.store.Close()
	defer second.logFile.Close()
	var value string
	revision, err := second.store.ReadSetting(ctx, "upgrade-sentinel", &value)
	if err != nil || value != "preserved" || revision != 1 {
		test.Fatal("restart lost saved data", value, revision, err)
	}
	if second.stateDirectory != directory || filepath.Dir(filepath.Dir(second.logPath)) != filepath.Join(directory, "logs") {
		test.Fatal("state or logs escaped the portable directory", second.stateDirectory, second.logPath)
	}
	if _, err := os.Stat(filepath.Join(directory, "yousage.sqlite")); err != nil {
		test.Fatal("database is not beside the executable", err)
	}
}

// TestPortableSmokeDoesNotResetRealPreferences protects the database during native verification.
func TestPortableSmokeDoesNotResetRealPreferences(test *testing.T) {
	directory := test.TempDir()
	app, err := newAppInDirectory(directory, false)
	if err != nil {
		test.Fatal(err)
	}
	defer app.store.Close()
	defer app.logFile.Close()
	ctx := context.Background()
	sources := []preferences.Source{{Id: "saved", Provider: "codex", IsEnabled: true, IsAutomatic: true}}
	if _, err = preferences.New(app.store).SaveSources(ctx, sources, 0); err != nil {
		test.Fatal(err)
	}
	smoke, err := newAppInDirectory(directory, true)
	if err != nil {
		test.Fatal(err)
	}
	defer smoke.store.Close()
	defer smoke.logFile.Close()
	if smoke.stateDirectory == directory || smoke.stateDirectory != filepath.Join(filepath.Dir(smoke.logPath), "isolated-state") {
		test.Fatal("smoke state was not isolated", smoke.stateDirectory)
	}
	actual, _, err := preferences.New(app.store).Sources(ctx)
	if err != nil || len(actual) != 1 || actual[0].Id != "saved" {
		test.Fatal("smoke startup overwrote real sources", actual, err)
	}
	actual, _, err = preferences.New(smoke.store).Sources(ctx)
	if err != nil || len(actual) != 0 {
		test.Fatal("smoke startup did not isolate sources", actual, err)
	}
}

// TestPortableWriteFailureDoesNotFallBack prevents hidden data in another user directory.
func TestPortableWriteFailureDoesNotFallBack(test *testing.T) {
	directory := test.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "logs"), []byte("blocked"), 0600); err != nil {
		test.Fatal(err)
	}
	appData := test.TempDir()
	test.Setenv("APPDATA", appData)
	if app, err := newAppInDirectory(directory, false); err == nil {
		app.store.Close()
		app.logFile.Close()
		test.Fatal("startup ignored a blocked portable directory")
	}
	if _, err := os.Stat(filepath.Join(appData, "Yousage")); !errors.Is(err, os.ErrNotExist) {
		test.Fatal("failed startup fell back to AppData", err)
	}
}
