package desktop

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// systemLocale reads the user's regional locale independently of WebView language.
func systemLocale() string {
	buffer := make([]uint16, 85)
	procedure := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetUserDefaultLocaleName")
	length, _, _ := procedure.Call(uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
	if length == 0 {
		return ""
	}
	return normalizeSystemLocale(windows.UTF16ToString(buffer))
}
