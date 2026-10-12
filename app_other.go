//go:build !windows

package main

import "github.com/wailsapp/wails/v3/pkg/application"

// setNativeAppearance leaves platform-managed decorations to the system.
func setNativeAppearance(window *application.WebviewWindow, isDark bool, red, green, blue int) error {
	return nil
}

// allowForegroundHandoff is unnecessary where activation is not restricted to the foreground process.
func allowForegroundHandoff() {}
