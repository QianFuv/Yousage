package sync

import (
	"context"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"
	"yousage/internal/accounting"
	"yousage/internal/domain"
	"yousage/internal/service"
)

type fixtureShareSource struct {
	receipt service.Receipt
	buckets []accounting.Bucket
}

func (source fixtureShareSource) ReceiptData(context.Context, int, string) (service.Receipt, []accounting.Bucket, error) {
	return source.receipt, source.buckets, nil
}

func shareRequest() ShareRequest {
	return ShareRequest{Days: 7, TtlDays: 14, TimeZone: "UTC", Palette: Palette{Appearance: "dark", Colors: map[string]string{"--background": "#1e1e2e"}}}
}

func TestSharePayloadFullWindowsAndPrivacy(test *testing.T) {
	for _, days := range []int{1, 7, 30, 90} {
		for _, timeZone := range []string{"UTC", "Asia/Shanghai", "America/New_York"} {
			for _, shouldHide := range []bool{false, true} {
				now := time.Date(2026, 11, 3, 14, 5, 37, 0, time.UTC)
				window, _ := accounting.BuildWindow(now, days, timeZone)
				labels := window.BucketLabels()
				receipt := service.Receipt{Days: days, TimeZone: timeZone, Window: window, SinceDay: window.SinceDay, UntilDay: window.UntilDay, TotalUsd: 4, Tokens: 100, Sessions: 2, Items: []service.ReceiptItem{{Provider: "codex", Model: "first", Tokens: 10, CostUsd: 3}, {Provider: "claude", Model: "second", Tokens: 90, CostUsd: 1}}}
				buckets := []accounting.Bucket{
					{Provider: domain.Codex, Day: labels[0], HourStart: labels[0], Totals: domain.Tokens{OutputTokens: 5, ReasoningTokens: 4}, CostUsd: 1},
					{Provider: domain.Codex, Day: labels[0], HourStart: labels[0], Totals: domain.Tokens{OutputTokens: 5}, CostUsd: 2},
					{Provider: domain.Claude, Day: labels[len(labels)-1], HourStart: labels[len(labels)-1], Totals: domain.Tokens{CachedInputTokens: 50, CacheCreationTokens: 20, OutputTokens: 20}, CostUsd: 1},
				}
				request := shareRequest()
				request.Days, request.TimeZone, request.HideAmounts = days, timeZone, shouldHide
				payload, err := BuildShare(context.Background(), fixtureShareSource{receipt, buckets}, request)
				if err != nil {
					test.Fatal(err)
				}
				wantedSlots := days
				if days == 1 {
					wantedSlots = 24
				}
				if len(payload.Series) != wantedSlots || payload.CreatedAt != "" || payload.ExpiresAt != "" {
					test.Fatalf("window not fully populated: %+v", payload)
				}
				var tokens int64
				var cost float64
				for _, point := range payload.Series {
					if len(point.Values) != 2 {
						test.Fatal("provider zero slots are missing")
					}
					for _, value := range point.Values {
						tokens += value.Tokens
						if value.CostUsd != nil {
							cost += *value.CostUsd
						}
					}
				}
				if tokens != receipt.Tokens || (!shouldHide && cost != receipt.TotalUsd) {
					test.Fatal("series differs from receipt", tokens, cost)
				}
				if payload.Series[1].Values[0].Tokens != 0 || payload.Series[1].Values[1].Tokens != 0 {
					test.Fatal("empty slots must contain zeros")
				}
				data, _ := json.Marshal(payload)
				if strings.Contains(string(data), "Usd") == shouldHide {
					test.Fatal("amount privacy is not enforced on JSON", string(data))
				}
				wantedModel, wantedShare := "first", 0.75
				if shouldHide {
					wantedModel, wantedShare = "second", 0.9
				}
				if payload.Models[0].Model != wantedModel || payload.Models[0].Share != wantedShare || math.Abs(payload.Models[0].Share+payload.Models[1].Share-1) > 1e-9 {
					test.Fatal("incorrect sorted model fractions", payload.Models)
				}
				request.Palette.Colors["--background"] = "changed"
				if payload.Palette.Colors["--background"] != "#1e1e2e" {
					test.Fatal("palette was not copied")
				}
				if days == 7 && timeZone == "America/New_York" {
					hasLongDay := false
					for index := 1; index < len(payload.Series); index++ {
						if payload.Series[index].StartMs-payload.Series[index-1].StartMs == (25 * time.Hour).Milliseconds() {
							hasLongDay = true
						}
					}
					if !hasLongDay {
						test.Fatal("daily slots must follow DST calendar boundaries")
					}
				}
			}
		}
	}
}

func TestShareZeroCostModelsStillSumToOne(test *testing.T) {
	for _, tokens := range []int64{0, 4} {
		receipt := service.Receipt{Tokens: tokens, Items: []service.ReceiptItem{{Model: "a", Tokens: tokens}, {Model: "b"}}}
		payload, err := BuildShare(context.Background(), fixtureShareSource{receipt: receipt}, shareRequest())
		if err != nil || len(payload.Models) != 2 || math.Abs(payload.Models[0].Share+payload.Models[1].Share-1) > 1e-9 {
			test.Fatal(payload.Models, err)
		}
	}
}

// TestSettingsAndShareValidation checks each request boundary with otherwise valid input.
func TestSettingsAndShareValidation(test *testing.T) {
	for _, scenario := range []struct {
		value      string
		shouldFail bool
	}{
		{"https://relay.example/", false}, {"http://127.0.0.1:8080", false}, {"ftp://relay.example", true}, {"https:///missing", true}, {"https://relay.example/path", true}, {"https://relay.example/?", true}, {"https://relay.example/#", true}, {"https://user:password@relay.example", true},
	} {
		value, err := NormalizeRelayUrl(scenario.value)
		if (err != nil) != scenario.shouldFail {
			test.Fatalf("URL %q: %v", scenario.value, err)
		}
		if err == nil && strings.HasSuffix(value, "/") {
			test.Fatal("trailing slash retained")
		}
	}
	for _, value := range []string{"url(https://external)", "URL(foo)", "red; display:none", "<script>", strings.Repeat("a", 129)} {
		request := shareRequest()
		request.Palette.Colors["--background"] = value
		if request.Validate() == nil {
			test.Fatal("unsafe palette accepted", value)
		}
	}
	for _, key := range []string{"color", "--Upper", "--", "--" + strings.Repeat("a", 65)} {
		request := shareRequest()
		request.Palette.Colors = map[string]string{key: "red"}
		if request.Validate() == nil {
			test.Fatal("invalid CSS key accepted", key)
		}
	}
	request := shareRequest()
	request.Palette.Colors = map[string]string{}
	for index := range 128 {
		request.Palette.Colors["--color-"+strconv.Itoa(index)] = "red"
	}
	if err := request.Validate(); err != nil {
		test.Fatal("maximum valid palette rejected", err)
	}
	request.Palette.Colors["--one-more"] = "red"
	if request.Validate() == nil {
		test.Fatal("oversized palette accepted")
	}
	for _, mutate := range []func(*ShareRequest){
		func(request *ShareRequest) { request.Days = 14 },
		func(request *ShareRequest) { request.TtlDays = 90 },
		func(request *ShareRequest) { request.TimeZone = "bad-zone" },
		func(request *ShareRequest) { request.Palette.Appearance = "system" },
	} {
		request := shareRequest()
		mutate(&request)
		if request.Validate() == nil {
			test.Fatal("invalid share request accepted")
		}
	}
}
