package service

import (
	"context"
	"testing"
	"time"
	"yousage/internal/domain"
)

// TestReportsPreserveLocalDays keeps report buckets and receipt activity aligned
// across local midnight, both DST transitions, fixed offsets, and invalid zones.
func TestReportsPreserveLocalDays(test *testing.T) {
	for _, fixture := range []struct {
		name, zone, before, after, last, previousDay, currentDay string
	}{
		{"spring", "America/New_York", "2026-03-08T04:50:00Z", "2026-03-08T05:10:00Z", "2026-03-08T07:30:00Z", "2026-03-07", "2026-03-08"},
		{"fall", "America/New_York", "2026-11-01T03:50:00Z", "2026-11-01T04:10:00Z", "2026-11-01T06:30:00Z", "2026-10-31", "2026-11-01"},
		{"offset", "+05:30", "2026-10-24T18:20:00Z", "2026-10-24T18:40:00Z", "2026-10-24T20:00:00Z", "2026-10-24", "2026-10-25"},
		{"fallback", "Invalid/Zone", "2026-10-24T23:50:00Z", "2026-10-25T00:10:00Z", "2026-10-25T02:00:00Z", "2026-10-24", "2026-10-25"},
	} {
		test.Run(fixture.name, func(test *testing.T) {
			service := testService(test)
			records := []domain.Record{}
			for index, encoded := range []string{fixture.before, fixture.after, fixture.last} {
				instant, err := time.Parse(time.RFC3339, encoded)
				if err != nil {
					test.Fatal(err)
				}
				record := domain.Record{Provider: domain.Claude, Model: "fixture", SessionId: "session", TimestampMs: instant.UnixMilli(), Speed: "standard", GenerationMs: 2000, Totals: domain.Tokens{CachedInputTokens: 100000, OutputTokens: 100}}
				if index == 1 {
					record.Totals.CachedInputTokens, record.Totals.CacheCreationTokens = 0, 100000
				}
				records = append(records, record)
			}
			now := time.UnixMilli(records[2].TimestampMs).Add(time.Hour)
			service.Now = func() time.Time { return now }
			seedReportRecords(test, service, records)
			ctx := context.Background()
			loss, err := service.LossReport(ctx, 7, fixture.zone)
			if err != nil || len(loss.Events) != 1 || loss.Events[0].AtMs != records[1].TimestampMs {
				test.Fatal("cache expiry changed", loss, err)
			}
			for _, day := range loss.Days {
				want := 0
				if day.Day == fixture.currentDay {
					want = 1
				}
				if day.CacheExpiryCount != want {
					test.Fatal("cache expiry assigned to wrong local day", day)
				}
			}
			speed, err := service.SpeedReport(ctx, 7, fixture.zone)
			if err != nil || speed.MeasuredRequests != 3 {
				test.Fatal("timed requests changed", speed, err)
			}
			for _, bucket := range speed.Buckets {
				want := 0
				if bucket.Label == fixture.previousDay {
					want = 1
				} else if bucket.Label == fixture.currentDay {
					want = 2
				}
				var requests int
				for _, model := range bucket.Models {
					requests += model.Requests
				}
				if requests != want {
					test.Fatal("timed request assigned to wrong local day", bucket)
				}
			}
			receipt, err := service.Receipt(ctx, 7, fixture.zone)
			if err != nil || receipt.ActiveDays != 2 || receipt.Sessions != 1 || receipt.FirstAtMs == nil || receipt.LastAtMs == nil || *receipt.FirstAtMs != records[0].TimestampMs || *receipt.LastAtMs != records[2].TimestampMs {
				test.Fatal("receipt local activity changed", receipt, err)
			}
		})
	}
}
