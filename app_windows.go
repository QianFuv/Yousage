package main

import (
	"fmt"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/w32"
	"unsafe"
)

// setNativeAppearance updates DWM on the UI thread without recreating the window.
func setNativeAppearance(window *application.WebviewWindow, isDark bool, red, green, blue int) error {
	return application.InvokeSyncWithError(func() error {
		handle := window.NativeWindow()
		if handle == nil {
			return nil
		}
		var enabled int32
		if isDark {
			enabled = 1
		}
		result := w32.DwmSetWindowAttribute(w32.HWND(uintptr(handle)), 20, unsafe.Pointer(&enabled), unsafe.Sizeof(enabled))
		if int32(result) < 0 {
			return fmt.Errorf("native frame appearance failed: 0x%08x", uint32(result))
		}
		caption := uint32(red | green<<8 | blue<<16)
		text := uint32(0x171717)
		if isDark {
			text = 0xf5f5f5
		}
		for _, attribute := range []struct {
			id    w32.DWMWINDOWATTRIBUTE
			color *uint32
		}{{35, &caption}, {36, &text}} {
			result = w32.DwmSetWindowAttribute(w32.HWND(uintptr(handle)), attribute.id, unsafe.Pointer(attribute.color), unsafe.Sizeof(caption))
			if int32(result) < 0 {
				return fmt.Errorf("native frame color failed: 0x%08x", uint32(result))
			}
		}
		return nil
	})
}
