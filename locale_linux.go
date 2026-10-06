package main

import "os"

// systemLocale respects the POSIX precedence for regional time preferences.
func systemLocale() string {
	for _, name := range []string{"LC_ALL", "LC_TIME", "LANG"} {
		if value := os.Getenv(name); value != "" {
			return normalizeSystemLocale(value)
		}
	}
	return ""
}
