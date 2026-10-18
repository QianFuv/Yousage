package sync

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"yousage/internal/accounting"
	"yousage/internal/service"
)

// TestPiSharePaymentProvenance keeps unknown cash-payment amounts private when requested.
func TestPiSharePaymentProvenance(test *testing.T) {
	window, _ := accounting.BuildWindow(time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC), 7, "UTC")
	receipt := service.Receipt{Days: 7, TimeZone: "UTC", Window: window, SinceDay: window.SinceDay, UntilDay: window.UntilDay, TotalUsd: 3, UnknownPaymentUsd: 3, Tokens: 100, Items: []service.ReceiptItem{{Provider: "pi", Model: "deepseek/custom", Tokens: 100, CostUsd: 3, IsPaymentUnknown: true}}}
	for _, shouldHide := range []bool{false, true} {
		request := shareRequest()
		request.HideAmounts = shouldHide
		payload, err := BuildShare(context.Background(), fixtureShareSource{receipt: receipt}, request)
		if err != nil || !payload.Receipt.Items[0].IsPaymentUnknown {
			test.Fatal(payload, err)
		}
		data, _ := json.Marshal(payload)
		if shouldHide && strings.Contains(string(data), "Usd") || !shouldHide && (payload.Receipt.UnknownPaymentUsd == nil || *payload.Receipt.UnknownPaymentUsd != 3) {
			test.Fatal("unknown payment amount privacy/provenance lost", string(data))
		}
	}
}

// TestPiShareUnpricedModels keeps missing rates distinct from explicitly free usage.
func TestPiShareUnpricedModels(test *testing.T) {
	receipt := service.Receipt{Tokens: 200, UnpricedRecords: 1, Items: []service.ReceiptItem{
		{Provider: "pi", Model: "backend/unknown", Tokens: 100, UnpricedRecords: 1, IsPaymentUnknown: true},
		{Provider: "pi", Model: "backend/free", Tokens: 100, IsPaymentUnknown: true},
	}}
	for _, shouldHide := range []bool{false, true} {
		request := shareRequest()
		request.HideAmounts = shouldHide
		payload, err := BuildShare(context.Background(), fixtureShareSource{receipt: receipt}, request)
		if err != nil {
			test.Fatal(err)
		}
		data, _ := json.Marshal(payload)
		var encoded struct {
			Receipt struct {
				Items []struct {
					UnpricedRecords int `json:"unpricedRecords"`
				} `json:"items"`
			} `json:"receipt"`
			Models []struct {
				Model           string `json:"model"`
				UnpricedRecords int    `json:"unpricedRecords"`
			} `json:"models"`
		}
		if err := json.Unmarshal(data, &encoded); err != nil {
			test.Fatal(err)
		}
		if encoded.Receipt.Items[0].UnpricedRecords != 1 || encoded.Receipt.Items[1].UnpricedRecords != 0 {
			test.Fatal("share receipt confused unknown and free prices", string(data))
		}
		for _, model := range encoded.Models {
			if (model.UnpricedRecords == 1) != (model.Model == "backend/unknown") {
				test.Fatal("share model lost its pricing coverage", string(data))
			}
		}
		if shouldHide && strings.Contains(string(data), "Usd") {
			test.Fatal("unpriced metadata exposed hidden amounts", string(data))
		}
	}
}
