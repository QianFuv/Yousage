package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"yousage/internal/accounting"
	"yousage/internal/domain"
	"yousage/internal/remote"
)

// Receipt presents a local trailing window with the same pricing as Usage.
type Receipt struct {
	Days              int               `json:"days"`
	Window            accounting.Window `json:"window"`
	StartsAtMs        int64             `json:"startsAtMs"`
	EndsAtMs          int64             `json:"endsAtMs"`
	Number            string            `json:"number"`
	TimeZone          string            `json:"timeZone"`
	SinceDay          string            `json:"sinceDay"`
	UntilDay          string            `json:"untilDay"`
	Items             []ReceiptItem     `json:"items"`
	TotalUsd          float64           `json:"totalUsd"`
	SubscriptionUsd   float64           `json:"subscriptionUsd"`
	PaidUsd           float64           `json:"paidUsd"`
	UnknownPaymentUsd float64           `json:"unknownPaymentUsd"`
	Tokens            int64             `json:"tokens"`
	Sessions          int               `json:"sessions"`
	CacheSavingsUsd   float64           `json:"cacheSavingsUsd"`
	UnpricedRecords   int               `json:"unpricedRecords"`
	ActiveDays        int               `json:"activeDays"`
	FirstAtMs         *int64            `json:"firstAtMs"`
	LastAtMs          *int64            `json:"lastAtMs"`
	QrText            string            `json:"qrText"`
	QrModules         []string          `json:"qrModules"`
}

// ReceiptItem combines a provider/model's priced and unpriced usage.
type ReceiptItem struct {
	Provider         string  `json:"provider"`
	Model            string  `json:"model"`
	Tokens           int64   `json:"tokens"`
	CostUsd          float64 `json:"costUsd"`
	UnpricedRecords  int     `json:"unpricedRecords"`
	IsSubscription   bool    `json:"isSubscription"`
	IsPaymentUnknown bool    `json:"isPaymentUnknown"`
}

// Receipt returns only local facts, without contacting remote environments or probing accounts.
func (service *Service) Receipt(ctx context.Context, days int, timeZone string) (Receipt, error) {
	now := service.Now()
	window, err := accounting.BuildWindow(now, days, timeZone)
	if err != nil {
		return Receipt{}, err
	}
	location := accounting.Location(window.TimeZone)
	start := time.UnixMilli(window.SinceTimeMs)
	end := time.UnixMilli(window.UntilTimeMs)
	period := "24H"
	number := end.In(location).Format("200601021504") + "-24H"
	if window.Resolution != "hour" {
		start, err = time.ParseInLocation(time.DateOnly, window.SinceDay, location)
		if err != nil {
			return Receipt{}, err
		}
		end = now
		period = fmt.Sprintf("%dD", days)
		number = strings.ReplaceAll(window.UntilDay, "-", "") + "-" + period
	}
	covered := window
	covered.SinceTimeMs, covered.UntilTimeMs = start.UnixMilli(), end.UnixMilli()
	summary, err := service.localSummary(ctx, covered)
	if err != nil {
		return Receipt{}, err
	}
	config, err := service.localConfig(ctx)
	if err != nil {
		return Receipt{}, err
	}
	result := receiptFromSummary(summary, subscriptionProviders(config))
	result.Days, result.Window, result.Number = days, window, number
	result.TimeZone, result.SinceDay, result.UntilDay = window.TimeZone, window.SinceDay, window.UntilDay
	result.StartsAtMs, result.EndsAtMs = covered.SinceTimeMs, covered.UntilTimeMs
	entries, _, err := service.sourceIndex(ctx)
	if err != nil {
		return result, err
	}
	_, records, err := service.localHistoryRecordsBetween(ctx, entries, windowStart(window).Add(-36*time.Hour), start, now, false)
	if err != nil {
		return result, err
	}
	sessions, active := map[string]bool{}, map[string]bool{}
	for _, sourceRecords := range records {
		for _, record := range sourceRecords {
			if record.IsQuotaOnly || record.ErrorKind != "" || record.TimestampMs < result.StartsAtMs || record.TimestampMs >= result.EndsAtMs {
				continue
			}
			if record.SessionId != "" {
				sessions[string(record.Provider)+"\x00"+record.SessionId] = true
			}
			if result.FirstAtMs == nil || record.TimestampMs < *result.FirstAtMs {
				instant := record.TimestampMs
				result.FirstAtMs = &instant
			}
			if result.LastAtMs == nil || record.TimestampMs > *result.LastAtMs {
				instant := record.TimestampMs
				result.LastAtMs = &instant
			}
			active[time.UnixMilli(record.TimestampMs).In(location).Format(time.DateOnly)] = true
		}
	}
	result.Sessions, result.ActiveDays = len(sessions), len(active)
	result.QrText = fmt.Sprintf("YOUSAGE %s %s..%s\nAPI $%.2f PAID $%.2f\n%s TOKENS %d SESSIONS", period, window.SinceDay, window.UntilDay, result.TotalUsd, result.PaidUsd, receiptTokenLabel(result.Tokens), result.Sessions)
	if result.UnknownPaymentUsd > 0 {
		result.QrText += fmt.Sprintf("\nPAYMENT UNKNOWN $%.2f", result.UnknownPaymentUsd)
	}
	result.QrModules, err = QrModules(result.QrText)
	return result, err
}

// subscriptionProviders uses enabled local native quota accounts with authenticated subscription identities.
// Codex ChatGPT and Claude OAuth plans cover provider usage; free, API, unknown, disabled and other drivers do not.
// This is the current saved login classification, not historical proof of a period's payment method.
func subscriptionProviders(config remote.Config) map[string]bool {
	result := map[string]bool{}
	for _, provider := range config.Providers {
		if !provider.IsEnabled || provider.Auth.Status != "authenticated" {
			continue
		}
		plan := strings.ToLower(strings.TrimSpace(provider.Auth.Label))
		if plan == "" || plan == "free" || plan == "unknown" || plan == "api" || plan == "api_key" || plan == "none" {
			continue
		}
		switch provider.Driver {
		case "codex":
			result[string(domain.Codex)] = true
		case "claudeAgent":
			result[string(domain.Claude)] = true
		}
	}
	return result
}

// receiptFromSummary combines priced buckets and subscription coverage.
func receiptFromSummary(summary remote.Summary, subscriptions map[string]bool) Receipt {
	result := Receipt{Items: []ReceiptItem{}}
	items := map[string]*ReceiptItem{}
	active := map[string]bool{}
	for _, bucket := range summary.Buckets {
		key := string(bucket.Provider) + "\x00" + bucket.Model
		item := items[key]
		if item == nil {
			item = &ReceiptItem{Provider: string(bucket.Provider), Model: bucket.Model, IsSubscription: subscriptions[string(bucket.Provider)], IsPaymentUnknown: bucket.Provider == domain.Pi}
			items[key] = item
		}
		item.Tokens += bucket.Totals.Total()
		item.CostUsd += bucket.CostUsd
		item.UnpricedRecords += bucket.UnpricedRecords
		result.TotalUsd += bucket.CostUsd
		result.Tokens += bucket.Totals.Total()
		result.CacheSavingsUsd += bucket.CacheSavingsUsd
		result.UnpricedRecords += bucket.UnpricedRecords
		if item.IsPaymentUnknown {
			result.UnknownPaymentUsd += bucket.CostUsd
		} else if item.IsSubscription {
			result.SubscriptionUsd += bucket.CostUsd
		}
		if bucket.Records > 0 {
			active[bucket.Day] = true
		}
	}
	result.PaidUsd = max(0, result.TotalUsd-result.SubscriptionUsd-result.UnknownPaymentUsd)
	result.ActiveDays = len(active)
	for _, item := range items {
		result.Items = append(result.Items, *item)
	}
	sort.Slice(result.Items, func(first, second int) bool {
		left, right := result.Items[first], result.Items[second]
		if left.CostUsd != right.CostUsd {
			return left.CostUsd > right.CostUsd
		}
		if left.Tokens != right.Tokens {
			return left.Tokens > right.Tokens
		}
		if left.Provider != right.Provider {
			return left.Provider < right.Provider
		}
		return left.Model < right.Model
	})
	return result
}

func receiptTokenLabel(tokens int64) string {
	if tokens >= 1000000000 {
		return fmt.Sprintf("%.2fB", float64(tokens)/1e9)
	}
	if tokens >= 1000000 {
		return fmt.Sprintf("%.2fM", float64(tokens)/1e6)
	}
	if tokens >= 1000 {
		return fmt.Sprintf("%.2fK", float64(tokens)/1e3)
	}
	return fmt.Sprintf("%d", tokens)
}
