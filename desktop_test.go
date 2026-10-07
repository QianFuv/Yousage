package main

import (
	"github.com/wailsapp/wails/v3/pkg/events"
	"testing"
)

func TestWindowVisibilityTrustsShowWhileWindowsStillReportsHidden(t *testing.T) {
	if !windowVisibility(events.Common.WindowShow, false, false) {
		t.Error("a window being shown was reported hidden, which blocks the launch refresh")
	}
	if windowVisibility(events.Common.WindowShow, false, true) {
		t.Error("a minimized window was reported visible")
	}
	if windowVisibility(events.Common.WindowHide, true, false) || windowVisibility(events.Common.WindowMinimise, true, false) {
		t.Error("a window being hidden or minimized was reported visible")
	}
	if !windowVisibility(events.Common.WindowRestore, true, false) || windowVisibility(events.Common.WindowUnMinimise, false, false) {
		t.Error("restored windows must follow the native visibility state")
	}
}
