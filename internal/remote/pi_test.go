package remote

import (
	"encoding/json"
	"testing"
	"yousage/internal/accounting"
	"yousage/internal/domain"
)

// TestPiSummaryCompatibility retains remote Pi facts across the existing supported contract range.
func TestPiSummaryCompatibility(test *testing.T) {
	for _, version := range []int{4, 5, 6} {
		summary := Summary{ContractVersion: version, ReadAt: "2026-10-18T12:00:00Z", TimeZone: "UTC", SinceDay: "2026-10-18", UntilDay: "2026-10-18", Pricing: Pricing{Status: "cached", Source: "models.dev", KnownModels: 1}, Buckets: []accounting.Bucket{{Day: "2026-10-18", Provider: domain.Pi, Model: "deepseek/test", Totals: domain.Tokens{OutputTokens: 10}, CostUsd: 1, CostSource: "providerReported", Records: 1, Sessions: 1}}, Sources: []Source{{Fingerprint: domain.SourceFingerprint{HostId: "remote-host", Provider: domain.Pi, ResolvedHomePath: "/pi/sessions", VolumeId: "fixture"}, Status: "ok", DistinctSessions: 1}}}
		data, _ := json.Marshal(summary)
		decoded, err := DecodeSummary(data)
		if err != nil || len(decoded.Buckets) != 1 || len(decoded.Sources) != 1 || decoded.Buckets[0].Provider != domain.Pi || decoded.Sources[0].Fingerprint.Provider != domain.Pi {
			test.Fatal(version, decoded, err)
		}
	}
}
