package service

import (
	"time"
	"yousage/internal/accounting"
)

// reportBucket applies Usage's exact rolling-hour bounds or local calendar-day bounds.
func reportBucket(window accounting.Window, timestampMs int64, now time.Time) (string, bool) {
	if timestampMs > now.UnixMilli() {
		return "", false
	}
	if window.Resolution == "hour" {
		if timestampMs < window.SinceTimeMs || timestampMs >= window.UntilTimeMs {
			return "", false
		}
		start := window.SinceTimeMs + (timestampMs-window.SinceTimeMs)/time.Hour.Milliseconds()*time.Hour.Milliseconds()
		return time.UnixMilli(start).UTC().Format("2006-01-02T15:04:05.000Z"), true
	}
	day := time.UnixMilli(timestampMs).In(accounting.Location(window.TimeZone)).Format(time.DateOnly)
	return day, day >= window.SinceDay && day <= window.UntilDay
}
