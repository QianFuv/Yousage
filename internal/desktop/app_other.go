//go:build !windows

package desktop

import "github.com/wailsapp/wails/v3/pkg/application"

// setNativeAppearance leaves platform-managed decorations to the system.
func setNativeAppearance(window *application.WebviewWindow, isDark bool, red, green, blue int) error {
	return nil
}

// interceptWindowMessage is unused outside Windows, where the window message loop is not exposed.
func (service *App) interceptWindowMessage(hwnd uintptr, message uint32, wParam, lParam uintptr) (uintptr, bool) {
	return 0, false
}

// allowForegroundHandoff is unnecessary where activation is not restricted to the foreground process.
func allowForegroundHandoff() {}
