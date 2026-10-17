package main

import "testing"

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
