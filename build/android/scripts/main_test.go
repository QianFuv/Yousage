package main

import (
	"slices"
	"testing"
)

// TestAndroidCompilationKeepsPayloadLoggingDisabled protects debug APK privacy too.
func TestAndroidCompilationKeepsPayloadLoggingDisabled(test *testing.T) {
	arguments := androidCompileArguments("synthetic-output.so", androidFrameworkBuild{Overlay: "synthetic-overlay.json", Modfile: "synthetic.mod"})
	tags := slices.Index(arguments, "-tags")
	if tags < 0 || tags+1 >= len(arguments) || arguments[tags+1] != "android,production" {
		test.Fatal("Android compilation must disable framework raw request and response logging")
	}
	if !slices.Contains(arguments, "synthetic-output.so") {
		test.Fatal("Android compilation lost its selected output")
	}
	overlay := slices.Index(arguments, "-overlay")
	if overlay < 0 || overlay+1 >= len(arguments) || arguments[overlay+1] != "synthetic-overlay.json" {
		test.Fatal("Android compilation must apply the redacted framework overlay")
	}
	modfile := slices.Index(arguments, "-modfile")
	if !slices.Contains(arguments, "-mod=readonly") || modfile < 0 || modfile+1 >= len(arguments) || arguments[modfile+1] != "synthetic.mod" {
		test.Fatal("Android compilation must keep the temporary module graph read-only")
	}
}

// TestAndroidVersionCode keeps every newer release installable over the previous one.
func TestAndroidVersionCode(test *testing.T) {
	for version, expected := range map[string]int{"0.1.0": 100, "0.2.0": 200, "1.0.0": 10000, "1.2.3": 10203, "12.99.99": 129999} {
		code, err := androidVersionCode(version)
		if err != nil || code != expected {
			test.Errorf("androidVersionCode(%q) = %d, %v; want %d", version, code, err, expected)
		}
	}
	for _, version := range []string{"1.2", "1.2.3.4", "1.100.0", "01.2.3", "v1.2.3", "1.2.3-beta"} {
		if _, err := androidVersionCode(version); err == nil {
			test.Errorf("androidVersionCode(%q) accepted an unsupported version", version)
		}
	}
}
