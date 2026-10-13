package desktop

import "strings"

// normalizeSystemLocale converts POSIX locale identifiers to Unicode language tags.
func normalizeSystemLocale(value string) string {
	value = strings.TrimSpace(value)
	value, _, _ = strings.Cut(value, ".")
	value, _, _ = strings.Cut(value, "@")
	if value == "C" || value == "POSIX" {
		return ""
	}
	return strings.ReplaceAll(value, "_", "-")
}
