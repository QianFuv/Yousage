package accounting

import (
	"errors"
	"time"
)

// BuildReceiptWindow builds a calendar receipt clipped to the retained 90-day view and now.
// Exact time bounds preserve DST days while the labels remain calendar dates.
func BuildReceiptWindow(now time.Time, period, date, timeZone string, dayStartHour int) (Window, error) {
	if dayStartHour < 0 || dayStartHour > 23 {
		return Window{}, errors.New("receipt day start hour must be between 0 and 23")
	}
	layout := time.DateOnly
	if period == "month" {
		layout = "2006-01"
	} else if period != "day" {
		return Window{}, errors.New("unsupported receipt period")
	}
	location := Location(timeZone)
	start, err := time.ParseInLocation(layout, date, location)
	if err != nil || start.Format(layout) != date {
		return Window{}, errors.New("invalid receipt date")
	}
	start = time.Date(start.Year(), start.Month(), start.Day(), dayStartHour, 0, 0, 0, location)
	end := start.AddDate(0, 0, 1)
	if period == "month" {
		end = start.AddDate(0, 1, 0)
	}
	end = time.Date(end.Year(), end.Month(), end.Day(), dayStartHour, 0, 0, 0, location)
	retention, _ := BuildWindow(now, 90, timeZone)
	retainedStart, _ := time.ParseInLocation(time.DateOnly, retention.SinceDay, location)
	if !end.After(retainedStart) || start.After(now) {
		return Window{}, errors.New("receipt date is outside retained history")
	}
	start = maxTime(start, retainedStart)
	end = minTime(end, now)
	until := end.Add(-time.Nanosecond).In(location).Format(time.DateOnly)
	if end.Equal(now) {
		until = now.In(location).Format(time.DateOnly)
	}
	return Window{TimeZone: location.String(), SinceDay: start.Format(time.DateOnly), UntilDay: until, Resolution: "day", SinceTimeMs: start.UnixMilli(), UntilTimeMs: end.UnixMilli()}, nil
}

func maxTime(first, second time.Time) time.Time {
	if first.After(second) {
		return first
	}
	return second
}
func minTime(first, second time.Time) time.Time {
	if first.Before(second) {
		return first
	}
	return second
}
