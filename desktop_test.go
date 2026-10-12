package main

import (
	"github.com/wailsapp/wails/v3/pkg/events"
	"testing"
	"yousage/internal/preferences"
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

// TestWindowLimitsScaleWithZoomUntilTheScreenIsFull keeps the desktop layout's CSS size at any zoom that fits.
func TestWindowLimitsScaleWithZoomUntilTheScreenIsFull(t *testing.T) {
	large := preferences.WindowBounds{Width: 2560, Height: 1400}
	if width, height, zoom := windowLimits(1.5, large); width != 1536 || height != 1020 || zoom != 1.5 {
		t.Error("zoom that fits must scale the minimum window", width, height, zoom)
	}
	laptop := preferences.WindowBounds{Width: 1280, Height: 672}
	if width, height, zoom := windowLimits(2, laptop); zoom != 1 || width != DesktopMinWidth || height != 672 {
		t.Error("zoom must drop to what the work area holds, never below 100%", width, height, zoom)
	}
	wide := preferences.WindowBounds{Width: 1920, Height: 1032}
	if width, height, zoom := windowLimits(2, wide); zoom != 1032.0/DesktopMinHeight || width != 1554 || height != 1032 {
		t.Error("the tighter work-area dimension must bound zoom", width, height, zoom)
	}
	if width, height, zoom := windowLimits(1.25, preferences.WindowBounds{}); width != 1280 || height != 850 || zoom != 1.25 {
		t.Error("unknown work areas must not clamp", width, height, zoom)
	}
}

// TestConstrainSizingRejectsNarrowAndFlatWindows adjusts only the dimension the user is not dragging.
func TestConstrainSizingRejectsNarrowAndFlatWindows(t *testing.T) {
	aspect := func(left, top, right, bottom int32) float64 { return float64(right-left) / float64(bottom-top) }
	for _, test := range []struct {
		name                     string
		edge                     int
		left, top, right, bottom int32
		want                     [4]int32
	}{
		{"normal shape is untouched", sizingRight, 0, 0, 1600, 900, [4]int32{0, 0, 1600, 900}},
		{"flat right drag raises height", sizingRight, 0, 0, 3000, 700, [4]int32{0, 0, 3000, 1286}},
		{"narrow left drag shortens the bottom", sizingLeft, 500, 0, 1500, 2000, [4]int32{500, 0, 1500, 1333}},
		{"flat bottom drag narrows width", sizingBottom, 0, 0, 3000, 700, [4]int32{0, 0, 1633, 700}},
		{"tall top drag widens width", sizingTop, 100, 0, 1100, 2000, [4]int32{100, 0, 1600, 2000}},
		{"top corner keeps the bottom edge", sizingTopLeft, 0, 0, 1000, 2000, [4]int32{0, 667, 1000, 2000}},
		{"bottom corner keeps the top edge", sizingBottomRight, 0, 0, 1000, 2000, [4]int32{0, 0, 1000, 1333}},
		{"unknown edges are ignored", 9, 0, 0, 3000, 100, [4]int32{0, 0, 3000, 100}},
	} {
		left, top, right, bottom := constrainSizing(test.edge, test.left, test.top, test.right, test.bottom)
		if [4]int32{left, top, right, bottom} != test.want {
			t.Errorf("%s: got %v, want %v", test.name, [4]int32{left, top, right, bottom}, test.want)
		}
		if test.edge != 9 {
			if ratio := aspect(left, top, right, bottom); ratio < MinWindowAspect-0.01 || ratio > MaxWindowAspect+0.01 {
				t.Errorf("%s: aspect %.3f outside bounds", test.name, ratio)
			}
		}
	}
}
