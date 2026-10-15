package service

import (
	"context"
	"testing"
	"time"
	"yousage/internal/domain"
)

func TestReceiptShareDataUsesExactLocalReceiptBounds(test *testing.T) {
	for _, days := range []int{1, 7, 30, 90} {
		service := testService(test)
		now := service.Now()
		cost := 3.0
		seedReportRecords(test, service, []domain.Record{
			{Provider: domain.Codex, Model: "reported", Speed: "standard", TimestampMs: now.Add(-time.Hour).UnixMilli(), ReportedCostUsd: &cost, Totals: domain.Tokens{OutputTokens: 10, ReasoningTokens: 8}},
			{Provider: domain.Codex, Model: "reported", Speed: "standard", TimestampMs: now.Add(time.Hour).UnixMilli(), ReportedCostUsd: &cost, Totals: domain.Tokens{OutputTokens: 100}},
		})
		receipt, buckets, err := service.ReceiptData(context.Background(), days, "Asia/Shanghai")
		if err != nil {
			test.Fatal(err)
		}
		var tokens int64
		var total float64
		for _, bucket := range buckets {
			tokens += bucket.Totals.Total()
			total += bucket.CostUsd
		}
		if tokens != receipt.Tokens || total != receipt.TotalUsd || tokens != 10 {
			test.Fatal("share source differs from local receipt", days, tokens, receipt.Tokens, total, receipt.TotalUsd)
		}
		modules, err := QrModules("https://relay.example/s/AAAAAAAAAAAAAAAAAAAAAA#encoded-key")
		if err != nil {
			test.Fatal(err)
		}
		assertQrFinderPatterns(test, modules)
	}
}
