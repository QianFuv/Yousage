package service

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
	"yousage/internal/domain"
	"yousage/internal/preferences"
)

// TestInstallationCacheRevalidatesPaths bounds lookup caching without hiding removal.
func TestInstallationCacheRevalidatesPaths(test *testing.T) {
	service := testService(test)
	service.Platform = "windows"
	service.Environment = map[string]string{}
	path := filepath.Join(test.TempDir(), "codex.exe")
	if err := os.WriteFile(path, []byte("fixture"), 0700); err != nil {
		test.Fatal(err)
	}
	target := NativeProvider{Driver: "codex", Executable: path}
	if !service.isNativeInstalled(target) {
		test.Fatal("installation missing")
	}
	if err := os.Remove(path); err != nil {
		test.Fatal(err)
	}
	if service.isNativeInstalled(target) {
		test.Fatal("removed executable remained installed")
	}
	if err := os.WriteFile(path, []byte("fixture"), 0700); err != nil {
		test.Fatal(err)
	}
	instant := service.Now().Add(2 * time.Minute)
	service.Now = func() time.Time { return instant }
	if !service.isNativeInstalled(target) {
		test.Fatal("installation lookup did not expire")
	}
}

func TestAutomaticSourcesFollowEnvironmentWithoutChangingLegacyRoots(test *testing.T) {
	service := testService(test)
	service.Home = test.TempDir()
	first, second := test.TempDir(), test.TempDir()
	service.Environment = map[string]string{"CODEX_HOME": first}
	sources := []preferences.Source{{Id: "auto", Provider: domain.Codex, IsAutomatic: true}, {Id: "legacy", Provider: domain.Claude, Root: second}}
	expanded := service.expandedSources(sources)
	if len(expanded) != 2 || expanded[0].Root != first || expanded[1].Root != second {
		test.Fatalf("automatic roots or legacy compatibility lost: %+v", expanded)
	}
	service.Environment["CODEX_HOME"] = second
	if service.expandedSources(sources)[0].Root != second || sources[0].Root != "" {
		test.Fatal("automatic resolution was frozen into saved configuration")
	}
	if _, err := service.SaveSources(context.Background(), []preferences.Source{{Id: "auto", Provider: domain.Codex, Root: first, IsAutomatic: true, IsEnabled: true}}, 1); err != nil {
		test.Fatal(err)
	}
	saved, _, err := service.Sources(context.Background())
	if err != nil || saved[0].Root != "" || !saved[0].IsAutomatic {
		test.Fatal("automatic source persisted a concrete path", saved, err)
	}
}

func TestWindowsDiscoveryUsesNativePayloadWithoutExecutingShims(test *testing.T) {
	service := testService(test)
	service.Platform = "windows"
	service.Home = test.TempDir()
	root := test.TempDir()
	service.Environment = map[string]string{"Path": " \"" + root + "\" "}
	write := func(path string) {
		test.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			test.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("fixture"), 0700); err != nil {
			test.Fatal(err)
		}
	}
	write(filepath.Join(root, "codex.cmd"))
	write(filepath.Join(root, "codex.ps1"))
	if service.resolveExecutable("codex", "") != "" {
		test.Fatal("script shim treated as a native executable")
	}
	architecture, triple := "x64", "x86_64-pc-windows-msvc"
	if runtime.GOARCH == "arm64" {
		architecture, triple = "arm64", "aarch64-pc-windows-msvc"
	}
	older := filepath.Join(root, "node_modules", "@openai", "codex-win32-"+architecture, "vendor", triple, "codex", "codex.exe")
	current := filepath.Join(root, "node_modules", "@openai", "codex", "node_modules", "@openai", "codex-win32-"+architecture, "vendor", triple, "bin", "codex.exe")
	write(older)
	write(current)
	assertSameExecutable(test, service.resolveExecutable("codex", ""), current)
	if service.resolveExecutable("codex", filepath.Join(root, "missing.exe")) != "" {
		test.Fatal("missing explicit installation silently switched account context")
	}
	if service.resolveExecutable("codex", older) != older {
		test.Fatal("valid explicit installation was ignored")
	}
	if err := os.Remove(current); err != nil {
		test.Fatal(err)
	}
	nestedLegacy := filepath.Join(filepath.Dir(filepath.Dir(current)), "codex", "codex.exe")
	write(nestedLegacy)
	newSibling := filepath.Join(filepath.Dir(filepath.Dir(older)), "bin", "codex.exe")
	write(newSibling)
	assertSameExecutable(test, service.resolveExecutable("codex", ""), nestedLegacy)
	detected, err := service.DetectProviders(context.Background())
	if err != nil || !detected[0].IsInstalled || detected[0].HasHistory {
		test.Fatal("installation and history availability were conflated", detected, err)
	}
}

func TestUnixEnvironmentKeysStayCaseSensitive(test *testing.T) {
	service := testService(test)
	service.Home = test.TempDir()
	service.Platform = "linux"
	service.Environment = map[string]string{"Path": "wrong", "PATH": "correct", "codex_home": "wrong"}
	if service.environmentValue("PATH") != "correct" || service.environmentValue("CODEX_HOME") != "" {
		test.Fatal("Unix environment keys became case-insensitive")
	}
}

func TestWindowsDiscoveryRecognizesDesktopFallbackAndCanonicalPnpmDependency(test *testing.T) {
	service := testService(test)
	service.Platform = "windows"
	service.Home = test.TempDir()
	local := test.TempDir()
	service.Environment = map[string]string{"LOCALAPPDATA": local}
	claude := filepath.Join(local, "Volta", "bin", "claude.exe")
	if err := os.MkdirAll(filepath.Dir(claude), 0700); err != nil {
		test.Fatal(err)
	}
	if err := os.WriteFile(claude, []byte("fixture"), 0700); err != nil {
		test.Fatal(err)
	}
	if service.resolveExecutable("claudeAgent", "") != claude {
		test.Fatal("desktop fallback installation was missed")
	}
	shim := test.TempDir()
	canonical := filepath.Join(test.TempDir(), "node_modules", "@openai", "codex")
	if err := os.MkdirAll(canonical, 0700); err != nil {
		test.Fatal(err)
	}
	link := filepath.Join(shim, "node_modules", "@openai", "codex")
	if err := os.MkdirAll(filepath.Dir(link), 0700); err != nil {
		test.Fatal(err)
	}
	if err := os.Symlink(canonical, link); err != nil {
		test.Skip("directory symlinks unavailable: " + err.Error())
	}
	architecture, triple := "x64", "x86_64-pc-windows-msvc"
	if runtime.GOARCH == "arm64" {
		architecture, triple = "arm64", "aarch64-pc-windows-msvc"
	}
	payload := filepath.Join(filepath.Dir(canonical), "codex-win32-"+architecture, "vendor", triple, "bin", "codex.exe")
	if err := os.MkdirAll(filepath.Dir(payload), 0700); err != nil {
		test.Fatal(err)
	}
	if err := os.WriteFile(payload, []byte("fixture"), 0700); err != nil {
		test.Fatal(err)
	}
	service.Environment["PATH"] = shim
	assertSameExecutable(test, service.resolveExecutable("codex", ""), payload)
}

// assertSameExecutable compares installation identity across canonical path aliases.
func assertSameExecutable(test *testing.T, actual, expected string) {
	test.Helper()
	actualFile, err := os.Stat(actual)
	if err != nil {
		test.Fatal("resolved executable missing", actual, err)
	}
	expectedFile, err := os.Stat(expected)
	if err != nil {
		test.Fatal("expected executable missing", expected, err)
	}
	if !os.SameFile(actualFile, expectedFile) {
		test.Fatalf("resolved executable %q does not identify expected installation %q", actual, expected)
	}
}
