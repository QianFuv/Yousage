package accounting

import (
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"
	"yousage/internal/domain"
)

type Window struct {
	TimeZone    string `json:"timeZone"`
	SinceDay    string `json:"sinceDay"`
	UntilDay    string `json:"untilDay"`
	Resolution  string `json:"resolution"`
	SinceTimeMs int64  `json:"sinceTimeMs,omitempty"`
	UntilTimeMs int64  `json:"untilTimeMs,omitempty"`
}

func Location(name string) *time.Location {
	if len(name) == 6 && (name[0] == '+' || name[0] == '-') && name[3] == ':' {
		hours, hourError := strconv.Atoi(name[1:3])
		minutes, minuteError := strconv.Atoi(name[4:])
		if hourError == nil && minuteError == nil && hours >= 0 && minutes >= 0 && hours < 24 && minutes < 60 {
			offset := (hours*60 + minutes) * 60
			if strings.HasPrefix(name, "-") {
				offset = -offset
			}
			return time.FixedZone(name, offset)
		}
	}
	location, err := time.LoadLocation(name)
	if err != nil {
		return time.UTC
	}
	return location
}

// BuildWindow uses calendar subtraction for daily views and a minute-anchored rolling 24h view.
func BuildWindow(now time.Time, days int, timeZone string) (Window, error) {
	if days != 1 && days != 7 && days != 30 && days != 90 {
		return Window{}, errors.New("unsupported usage period")
	}
	location := Location(timeZone)
	window := Window{TimeZone: location.String(), Resolution: "day"}
	if days == 1 {
		until := now.Truncate(time.Minute)
		since := until.Add(-24 * time.Hour)
		window.Resolution, window.SinceTimeMs, window.UntilTimeMs = "hour", since.UnixMilli(), until.UnixMilli()
		window.SinceDay, window.UntilDay = since.In(location).Format(time.DateOnly), until.In(location).Format(time.DateOnly)
	} else {
		local := now.In(location)
		day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
		window.SinceDay, window.UntilDay = day.AddDate(0, 0, -days+1).Format(time.DateOnly), day.Format(time.DateOnly)
	}
	return window, nil
}

// BucketLabels includes empty slots without using wall-clock hours across DST.
func (window Window) BucketLabels() []string {
	labels := []string{}
	if window.Resolution == "hour" {
		for instant := window.SinceTimeMs; instant < window.UntilTimeMs; instant += time.Hour.Milliseconds() {
			labels = append(labels, time.UnixMilli(instant).UTC().Format("2006-01-02T15:04:05.000Z"))
		}
		return labels
	}
	day, err := time.Parse(time.DateOnly, window.SinceDay)
	if err != nil {
		return labels
	}
	for label := day.Format(time.DateOnly); label <= window.UntilDay; label = day.Format(time.DateOnly) {
		labels = append(labels, label)
		day = day.AddDate(0, 0, 1)
	}
	return labels
}

type Bucket struct {
	Day              string          `json:"day"`
	HourStart        string          `json:"hourStart,omitempty"`
	Provider         domain.Provider `json:"provider"`
	Model            string          `json:"model"`
	SourcePath       string          `json:"sourcePath,omitempty"`
	Totals           domain.Tokens   `json:"totals"`
	CostUsd          float64         `json:"costUsd"`
	CacheSavingsUsd  float64         `json:"cacheSavingsUsd"`
	CategoryCostUsd  *CategoryCost   `json:"categoryCostUsd,omitempty"`
	FastCostUsd      float64         `json:"fastCostUsd,omitempty"`
	UltrafastCostUsd float64         `json:"ultrafastCostUsd,omitempty"`
	SpeedPremiumUsd  float64         `json:"speedPremiumUsd,omitempty"`
	CostSource       string          `json:"costSource"`
	Records          int             `json:"records"`
	UnpricedRecords  int             `json:"unpricedRecords"`
	Sessions         int             `json:"sessions"`
}
type AggregateOptions struct {
	Window           Window
	Rates, Overrides RateTable
	Aliases          map[string]string
}
type AggregateResult struct {
	Buckets           []Bucket `json:"buckets"`
	DuplicatesDropped int      `json:"duplicatesDropped"`
	OutOfWindow       int      `json:"outOfWindow"`
	DistinctSessions  int      `json:"distinctSessions"`
}
type bucketKey struct {
	day, hour     string
	provider      domain.Provider
	model, source string
}
type accumulator struct {
	Bucket
	sessions map[string]bool
	reported int
}

// Aggregate deduplicates before filtering, matching resumed/forked transcript semantics.
func Aggregate(records []domain.Record, options AggregateOptions) (AggregateResult, error) {
	result := AggregateResult{Buckets: []Bucket{}}
	window := options.Window
	if window.Resolution == "hour" && window.UntilTimeMs <= window.SinceTimeMs {
		return result, errors.New("hourly usage requires exact increasing time bounds")
	}
	location := Location(window.TimeZone)
	seen, sessions, buckets := map[string]bool{}, map[string]bool{}, map[bucketKey]*accumulator{}
	for _, record := range records {
		if record.IsQuotaOnly {
			continue
		}
		if record.DedupeKey != "" {
			if seen[record.DedupeKey] {
				result.DuplicatesDropped++
				continue
			}
			seen[record.DedupeKey] = true
		}
		instant := time.UnixMilli(record.TimestampMs)
		day := instant.In(location).Format(time.DateOnly)
		if (window.Resolution == "hour" && (record.TimestampMs < window.SinceTimeMs || record.TimestampMs >= window.UntilTimeMs)) || (window.Resolution != "hour" && (day < window.SinceDay || day > window.UntilDay)) {
			result.OutOfWindow++
			continue
		}
		if model, exists := options.Aliases[record.Model]; exists {
			record.Model, record.RateModel = model, ""
		}
		hour := ""
		if window.Resolution == "hour" {
			start := window.SinceTimeMs + ((record.TimestampMs-window.SinceTimeMs)/time.Hour.Milliseconds())*time.Hour.Milliseconds()
			hour = time.UnixMilli(start).UTC().Format("2006-01-02T15:04:05.000Z")
		}
		key := bucketKey{day, hour, record.Provider, record.Model, record.SourcePath}
		held := buckets[key]
		if held == nil {
			held = &accumulator{Bucket: Bucket{Day: day, HourStart: hour, Provider: record.Provider, Model: record.Model, SourcePath: record.SourcePath}, sessions: map[string]bool{}}
			buckets[key] = held
		}
		priced := Price(options.Rates, options.Overrides, record)
		held.Totals.Add(record.Totals)
		held.CostUsd += priced.CostUsd
		held.CacheSavingsUsd += CacheSavings(options.Rates, options.Overrides, record)
		held.SpeedPremiumUsd += priced.SpeedPremiumUsd
		if priced.CategoryCostUsd != nil {
			if held.CategoryCostUsd == nil {
				held.CategoryCostUsd = &CategoryCost{}
			}
			held.CategoryCostUsd.Input += priced.CategoryCostUsd.Input
			held.CategoryCostUsd.CacheRead += priced.CategoryCostUsd.CacheRead
			held.CategoryCostUsd.CacheWrite += priced.CategoryCostUsd.CacheWrite
			held.CategoryCostUsd.Output += priced.CategoryCostUsd.Output
		}
		if record.Speed == "fast" {
			held.FastCostUsd += priced.CostUsd
		}
		if record.Speed == "ultrafast" {
			held.UltrafastCostUsd += priced.CostUsd
		}
		held.Records++
		if priced.CostSource == "unpriced" {
			held.UnpricedRecords++
		}
		if priced.CostSource == "providerReported" {
			held.reported++
		}
		if record.SessionId != "" {
			held.sessions[record.SessionId] = true
			sessions[string(record.Provider)+"\x00"+record.SessionId] = true
		}
	}
	for _, held := range buckets {
		held.CostSource = "modelPriced"
		if held.UnpricedRecords == held.Records {
			held.CostSource = "unpriced"
		} else if held.reported == held.Records {
			held.CostSource = "providerReported"
		}
		held.Sessions = len(held.sessions)
		held.FastCostUsd = roundUsd(held.FastCostUsd)
		held.UltrafastCostUsd = roundUsd(held.UltrafastCostUsd)
		held.SpeedPremiumUsd = roundUsd(held.SpeedPremiumUsd)
		if category := held.CategoryCostUsd; category != nil {
			category.Input = roundUsd(category.Input)
			category.CacheRead = roundUsd(category.CacheRead)
			category.CacheWrite = roundUsd(category.CacheWrite)
			category.Output = roundUsd(category.Output)
		}
		result.Buckets = append(result.Buckets, held.Bucket)
	}
	sort.SliceStable(result.Buckets, func(first, second int) bool {
		left, right := result.Buckets[first], result.Buckets[second]
		if left.Day != right.Day {
			return left.Day < right.Day
		}
		if left.HourStart != right.HourStart {
			return left.HourStart < right.HourStart
		}
		if left.Provider != right.Provider {
			return left.Provider < right.Provider
		}
		if left.Model != right.Model {
			return left.Model < right.Model
		}
		return left.SourcePath < right.SourcePath
	})
	result.DistinctSessions = len(sessions)
	return result, nil
}
func roundUsd(value float64) float64 { return math.Floor(value*1e6+0.5) / 1e6 }
