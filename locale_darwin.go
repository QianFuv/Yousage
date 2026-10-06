package main

import (
	"context"
	"os/exec"
	"time"
)

// systemLocale reads macOS regional preferences without relying on shell language.
func systemLocale() string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "/usr/bin/defaults", "read", "-g", "AppleLocale").Output()
	if err != nil {
		return ""
	}
	return normalizeSystemLocale(string(output))
}
