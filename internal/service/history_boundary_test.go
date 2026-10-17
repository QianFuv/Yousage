package service

import (
	"context"
	"testing"
	"time"
	"yousage/internal/domain"
	"yousage/internal/refresh"
)

// TestClaudeUsageExcludesFutureRecordsUntilEligible keeps daily usage aligned with report time bounds.
func TestClaudeUsageExcludesFutureRecordsUntilEligible(t *testing.T) {
	service := testService(t)
	ctx := context.Background()
	now := service.Now()
	service.Now = func() time.Time { return now }
	seedReportRecords(t, service, []domain.Record{{Provider: domain.Claude, Model: "future", SessionId: "session", DedupeKey: "request", Speed: "standard", TimestampMs: now.Add(time.Hour).UnixMilli(), Totals: domain.Tokens{OutputTokens: 100}, GenerationMs: 1000}})
	for _, expected := range []int64{0, 100} {
		for _, days := range []int{7, 30, 90} {
			if err := service.RefreshUsage(ctx, days, "UTC", []string{}, refresh.Request{}); err != nil {
				t.Fatal(err)
			}
			snapshot, err := service.Usage(ctx, days, "UTC")
			if err != nil {
				t.Fatal(err)
			}
			var output int64
			for _, environment := range snapshot.Environments {
				if environment.Summary != nil {
					for _, bucket := range environment.Summary.Buckets {
						output += bucket.Totals.OutputTokens
					}
				}
			}
			if output != expected {
				t.Fatalf("%d-day output = %d, want %d at %v", days, output, expected, now)
			}
		}
		now = now.Add(2 * time.Hour)
	}
}
