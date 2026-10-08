package service

import (
	"context"
	"fmt"
	"rsc.io/qr"
	"sort"
	"strings"
	"time"
	"yousage/internal/accounting"
	"yousage/internal/domain"
	"yousage/internal/remote"
)

// Receipt presents a local calendar period with the same pricing as Usage.
type Receipt struct {
	StartsAtMs      int64         `json:"startsAtMs"`
	EndsAtMs        int64         `json:"endsAtMs"`
	DayStartHour    int           `json:"dayStartHour"`
	Period          string        `json:"period"`
	Label           string        `json:"label"`
	Number          string        `json:"number"`
	TimeZone        string        `json:"timeZone"`
	SinceDay        string        `json:"sinceDay"`
	UntilDay        string        `json:"untilDay"`
	Items           []ReceiptItem `json:"items"`
	TotalUsd        float64       `json:"totalUsd"`
	SubscriptionUsd float64       `json:"subscriptionUsd"`
	PaidUsd         float64       `json:"paidUsd"`
	Tokens          int64         `json:"tokens"`
	Sessions        int           `json:"sessions"`
	CacheSavingsUsd float64       `json:"cacheSavingsUsd"`
	UnpricedRecords int           `json:"unpricedRecords"`
	ActiveDays      int           `json:"activeDays"`
	FirstAtMs       *int64        `json:"firstAtMs"`
	LastAtMs        *int64        `json:"lastAtMs"`
	PreviousDate    string        `json:"previousDate"`
	NextDate        string        `json:"nextDate"`
	QrText          string        `json:"qrText"`
	QrModules       []string      `json:"qrModules"`
}

// ReceiptItem combines a provider/model's priced and unpriced usage.
type ReceiptItem struct {
	Provider        string  `json:"provider"`
	Model           string  `json:"model"`
	Tokens          int64   `json:"tokens"`
	CostUsd         float64 `json:"costUsd"`
	UnpricedRecords int     `json:"unpricedRecords"`
	IsSubscription  bool    `json:"isSubscription"`
}

// Receipt returns only local facts, without contacting remote environments or probing accounts.
func (service *Service) Receipt(ctx context.Context, period, date, timeZone string, dayStartHour int) (Receipt, error) {
	now := service.Now()
	window, err := accounting.BuildReceiptWindow(now, period, date, timeZone, dayStartHour)
	if err != nil {
		return Receipt{}, err
	}
	summary, err := service.localSummary(ctx, window)
	if err != nil {
		return Receipt{}, err
	}
	config, err := service.localConfig(ctx)
	if err != nil {
		return Receipt{}, err
	}
	result := receiptFromSummary(summary, subscriptionProviders(config))
	result.Period, result.Label, result.Number = period, date, strings.ReplaceAll(date, "-", "")
	result.TimeZone, result.SinceDay, result.UntilDay = window.TimeZone, window.SinceDay, window.UntilDay
	entries, _, err := service.sourceIndex(ctx)
	if err != nil {
		return result, err
	}
	_, records, err := service.localHistoryRecordsBetween(ctx, entries, windowStart(window).Add(-36*time.Hour), time.UnixMilli(window.SinceTimeMs), now, false)
	if err != nil {
		return result, err
	}
	sessions := map[string]bool{}
	for _, sourceRecords := range records {
		for _, record := range sourceRecords {
			if record.IsQuotaOnly || record.ErrorKind != "" || record.TimestampMs < window.SinceTimeMs || record.TimestampMs >= window.UntilTimeMs {
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
		}
	}
	result.Sessions = len(sessions)
	active := map[string]bool{}
	for _, sourceRecords := range records {
		for _, record := range sourceRecords {
			if record.IsQuotaOnly || record.ErrorKind != "" || record.TimestampMs < window.SinceTimeMs || record.TimestampMs >= window.UntilTimeMs {
				continue
			}
			local := time.UnixMilli(record.TimestampMs).In(accounting.Location(window.TimeZone))
			boundary := time.Date(local.Year(), local.Month(), local.Day(), dayStartHour, 0, 0, 0, local.Location())
			if local.Before(boundary) {
				local = local.AddDate(0, 0, -1)
			}
			active[local.Format(time.DateOnly)] = true
		}
	}
	result.ActiveDays = len(active)
	layout := time.DateOnly
	if period == "month" {
		layout = "2006-01"
	}
	current, _ := time.ParseInLocation(layout, date, accounting.Location(window.TimeZone))
	start := time.Date(current.Year(), current.Month(), current.Day(), dayStartHour, 0, 0, 0, current.Location())
	end := current.AddDate(0, 0, 1)
	if period == "month" {
		end = current.AddDate(0, 1, 0)
	}
	end = time.Date(end.Year(), end.Month(), end.Day(), dayStartHour, 0, 0, 0, end.Location())
	result.StartsAtMs, result.EndsAtMs, result.DayStartHour = start.UnixMilli(), end.UnixMilli(), dayStartHour
	previous, next := current.AddDate(0, 0, -1), current.AddDate(0, 0, 1)
	if period == "month" {
		previous, next = current.AddDate(0, -1, 0), current.AddDate(0, 1, 0)
	}
	if _, err := accounting.BuildReceiptWindow(now, period, previous.Format(layout), timeZone, dayStartHour); err == nil {
		result.PreviousDate = previous.Format(layout)
	}
	if _, err := accounting.BuildReceiptWindow(now, period, next.Format(layout), timeZone, dayStartHour); err == nil {
		result.NextDate = next.Format(layout)
	}
	result.QrText = fmt.Sprintf("YOUSAGE %s\nAPI $%.2f PAID $%.2f\n%s TOKENS %d SESSIONS", date, result.TotalUsd, result.PaidUsd, receiptTokenLabel(result.Tokens), result.Sessions)
	code, err := qr.Encode(result.QrText, qr.L)
	if err != nil {
		return result, err
	}
	result.QrModules = make([]string, code.Size)
	for row := range code.Size {
		pixels := make([]byte, code.Size)
		for column := range code.Size {
			pixels[column] = '0'
			if code.Black(column, row) {
				pixels[column] = '1'
			}
		}
		result.QrModules[row] = string(pixels)
	}
	return result, nil
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

func receiptFromSummary(summary remote.Summary, subscriptions map[string]bool) Receipt {
	result := Receipt{Items: []ReceiptItem{}}
	items := map[string]*ReceiptItem{}
	active := map[string]bool{}
	for _, bucket := range summary.Buckets {
		key := string(bucket.Provider) + "\x00" + bucket.Model
		item := items[key]
		if item == nil {
			item = &ReceiptItem{Provider: string(bucket.Provider), Model: bucket.Model, IsSubscription: subscriptions[string(bucket.Provider)]}
			items[key] = item
		}
		item.Tokens += bucket.Totals.Total()
		item.CostUsd += bucket.CostUsd
		item.UnpricedRecords += bucket.UnpricedRecords
		result.TotalUsd += bucket.CostUsd
		result.Tokens += bucket.Totals.Total()
		result.CacheSavingsUsd += bucket.CacheSavingsUsd
		result.UnpricedRecords += bucket.UnpricedRecords
		if item.IsSubscription {
			result.SubscriptionUsd += bucket.CostUsd
		}
		if bucket.Records > 0 {
			active[bucket.Day] = true
		}
	}
	result.PaidUsd = max(0, result.TotalUsd-result.SubscriptionUsd)
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
