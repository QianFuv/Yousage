package desktop

import "testing"

func TestNormalizeSystemLocale(t *testing.T) {
	for input, expected := range map[string]string{"en_US.UTF-8": "en-US", "de_DE@euro": "de-DE", " zh-CN ": "zh-CN", "C.UTF-8": "", "POSIX": "", "": ""} {
		if actual := normalizeSystemLocale(input); actual != expected {
			t.Errorf("locale %q: got %q, want %q", input, actual, expected)
		}
	}
}
