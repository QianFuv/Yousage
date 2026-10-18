package sync

import (
	"context"
	"sort"
	"time"
	"yousage/internal/accounting"
	"yousage/internal/service"
)

// ShareWindow describes the receipt's exact covered interval.
type ShareWindow struct {
	SinceDay   string `json:"sinceDay"`
	UntilDay   string `json:"untilDay"`
	Resolution string `json:"resolution"`
	StartsAtMs int64  `json:"startsAtMs"`
	EndsAtMs   int64  `json:"endsAtMs"`
}

// ShareItem exposes only a receipt model's aggregate counts and optional amount.
type ShareItem struct {
	Provider         string   `json:"provider"`
	Model            string   `json:"model"`
	Tokens           int64    `json:"tokens"`
	CostUsd          *float64 `json:"costUsd,omitempty"`
	IsSubscription   bool     `json:"isSubscription"`
	IsPaymentUnknown bool     `json:"isPaymentUnknown,omitempty"`
	UnpricedRecords  int      `json:"unpricedRecords,omitempty"`
}

// ShareReceipt carries the receipt totals without private source or session identities.
type ShareReceipt struct {
	Number            string      `json:"number"`
	TotalUsd          *float64    `json:"totalUsd,omitempty"`
	SubscriptionUsd   *float64    `json:"subscriptionUsd,omitempty"`
	PaidUsd           *float64    `json:"paidUsd,omitempty"`
	UnknownPaymentUsd *float64    `json:"unknownPaymentUsd,omitempty"`
	CacheSavingsUsd   *float64    `json:"cacheSavingsUsd,omitempty"`
	Tokens            int64       `json:"tokens"`
	Sessions          int         `json:"sessions"`
	ActiveDays        int         `json:"activeDays"`
	UnpricedRecords   int         `json:"unpricedRecords"`
	Items             []ShareItem `json:"items"`
}

// ShareValue aggregates one provider in a time slot, including explicit zeros.
type ShareValue struct {
	Provider string   `json:"provider"`
	Tokens   int64    `json:"tokens"`
	CostUsd  *float64 `json:"costUsd,omitempty"`
}

// SharePoint contains every provider's aggregate for one window slot.
type SharePoint struct {
	StartMs int64        `json:"startMs"`
	Values  []ShareValue `json:"values"`
}

// ShareModel is a model's fraction of visible costs or, for hidden amounts, tokens.
type ShareModel struct {
	Provider        string   `json:"provider"`
	Model           string   `json:"model"`
	Tokens          int64    `json:"tokens"`
	CostUsd         *float64 `json:"costUsd,omitempty"`
	Share           float64  `json:"share"`
	UnpricedRecords int      `json:"unpricedRecords,omitempty"`
}

// SharePayload is the version-one public, local-environment-only receipt payload.
type SharePayload struct {
	FormatVersion int          `json:"formatVersion"`
	CreatedAt     string       `json:"createdAt"`
	ExpiresAt     string       `json:"expiresAt"`
	Days          int          `json:"days"`
	TimeZone      string       `json:"timeZone"`
	Window        ShareWindow  `json:"window"`
	HideAmounts   bool         `json:"hideAmounts"`
	Receipt       ShareReceipt `json:"receipt"`
	Series        []SharePoint `json:"series"`
	Models        []ShareModel `json:"models"`
	Palette       Palette      `json:"palette"`
}

// ShareSource supplies the receipt and exact local accounting buckets together.
type ShareSource interface {
	ReceiptData(context.Context, int, string) (service.Receipt, []accounting.Bucket, error)
}

// BuildShare builds a validated preview with no timestamps and no network requests.
func BuildShare(ctx context.Context, source ShareSource, request ShareRequest) (SharePayload, error) {
	if request.TimeZone == "" {
		request.TimeZone = "UTC"
	}
	if err := request.Validate(); err != nil {
		return SharePayload{}, err
	}
	receipt, buckets, err := source.ReceiptData(ctx, request.Days, request.TimeZone)
	if err != nil {
		return SharePayload{}, err
	}
	amount := func(value float64) *float64 {
		if request.HideAmounts {
			return nil
		}
		return &value
	}
	payload := SharePayload{FormatVersion: 1, Days: request.Days, TimeZone: receipt.TimeZone, HideAmounts: request.HideAmounts,
		Window:  ShareWindow{receipt.SinceDay, receipt.UntilDay, receipt.Window.Resolution, receipt.StartsAtMs, receipt.EndsAtMs},
		Receipt: ShareReceipt{Number: receipt.Number, TotalUsd: amount(receipt.TotalUsd), SubscriptionUsd: amount(receipt.SubscriptionUsd), PaidUsd: amount(receipt.PaidUsd), CacheSavingsUsd: amount(receipt.CacheSavingsUsd), Tokens: receipt.Tokens, Sessions: receipt.Sessions, ActiveDays: receipt.ActiveDays, UnpricedRecords: receipt.UnpricedRecords, Items: []ShareItem{}},
		Series:  []SharePoint{}, Models: []ShareModel{}, Palette: Palette{Appearance: request.Palette.Appearance, Colors: map[string]string{}}}
	for key, value := range request.Palette.Colors {
		payload.Palette.Colors[key] = value
	}
	if receipt.UnknownPaymentUsd > 0 {
		payload.Receipt.UnknownPaymentUsd = amount(receipt.UnknownPaymentUsd)
	}
	weightTotal := receipt.TotalUsd
	shouldUseTokens := request.HideAmounts || weightTotal <= 0
	if shouldUseTokens {
		weightTotal = float64(receipt.Tokens)
	}
	for _, item := range receipt.Items {
		payload.Receipt.Items = append(payload.Receipt.Items, ShareItem{Provider: item.Provider, Model: item.Model, Tokens: item.Tokens, CostUsd: amount(item.CostUsd), IsSubscription: item.IsSubscription, IsPaymentUnknown: item.IsPaymentUnknown, UnpricedRecords: item.UnpricedRecords})
		weight := item.CostUsd
		if shouldUseTokens {
			weight = float64(item.Tokens)
		}
		share := 1.0 / float64(len(receipt.Items))
		if weightTotal > 0 {
			share = weight / weightTotal
		}
		payload.Models = append(payload.Models, ShareModel{Provider: item.Provider, Model: item.Model, Tokens: item.Tokens, CostUsd: amount(item.CostUsd), Share: share, UnpricedRecords: item.UnpricedRecords})
	}
	sort.Slice(payload.Models, func(first, second int) bool {
		left, right := payload.Models[first], payload.Models[second]
		if left.Share != right.Share {
			return left.Share > right.Share
		}
		if left.Provider != right.Provider {
			return left.Provider < right.Provider
		}
		return left.Model < right.Model
	})
	providers := map[string]bool{}
	totals := map[string]map[string]ShareValue{}
	for _, bucket := range buckets {
		provider := string(bucket.Provider)
		providers[provider] = true
		label := bucket.Day
		if receipt.Window.Resolution == "hour" {
			label = bucket.HourStart
		}
		if totals[label] == nil {
			totals[label] = map[string]ShareValue{}
		}
		value := totals[label][provider]
		cost := bucket.CostUsd
		if value.CostUsd != nil {
			cost += *value.CostUsd
		}
		value.Provider, value.Tokens, value.CostUsd = provider, value.Tokens+bucket.Totals.Total(), &cost
		totals[label][provider] = value
	}
	providerNames := make([]string, 0, len(providers))
	for provider := range providers {
		providerNames = append(providerNames, provider)
	}
	sort.Strings(providerNames)
	for _, label := range receipt.Window.BucketLabels() {
		instant, err := time.ParseInLocation(time.DateOnly, label, accounting.Location(receipt.TimeZone))
		if receipt.Window.Resolution == "hour" {
			instant, err = time.Parse(time.RFC3339, label)
		}
		if err != nil {
			return SharePayload{}, err
		}
		point := SharePoint{StartMs: instant.UnixMilli(), Values: []ShareValue{}}
		for _, provider := range providerNames {
			value := totals[label][provider]
			cost := 0.0
			if value.CostUsd != nil {
				cost = *value.CostUsd
			}
			point.Values = append(point.Values, ShareValue{provider, value.Tokens, amount(cost)})
		}
		payload.Series = append(payload.Series, point)
	}
	return payload, nil
}
