package storage

import (
	"math"
	"math/rand"
	"reflect"
	"testing"
	"yousage/internal/domain"
)

// TestPayloadRoundTrip covers all fields, pointer presence and real-shaped quota windows.
func TestPayloadRoundTrip(test *testing.T) {
	random := rand.New(rand.NewSource(41))
	values := map[int64]string{0: ""}
	identities := map[string]int64{"": 0}
	intern := func(value string) (int64, error) {
		if identity, exists := identities[value]; exists {
			return identity, nil
		}
		identity := int64(len(identities))
		identities[value], values[identity] = identity, value
		return identity, nil
	}
	for index := range 2000 {
		record := domain.Record{Provider: []domain.Provider{domain.Codex, domain.Claude, domain.Grok, domain.OpenCode, domain.Antigravity, domain.Cursor}[index%6], TimestampMs: random.Int63(), Model: "gpt-5.4", RateModel: "rate-model", SessionId: "session-α", Speed: []string{"standard", "fast", "ultrafast"}[index%3], DedupeKey: "dedupe", SourcePath: "/different/路径", QuotaBucket: "codex", IsQuotaOnly: index%7 == 0}
		record.GenerationMs = random.Int63()
		record.Totals = domain.Tokens{UncachedInputTokens: random.Int63(), CachedInputTokens: random.Int63(), CacheCreationTokens: random.Int63(), OutputTokens: random.Int63(), ReasoningTokens: random.Int63()}
		if index%3 != 0 {
			cost := math.Float64frombits(random.Uint64())
			if index%3 == 1 {
				cost = math.Copysign(0, -1)
			}
			record.ReportedCostUsd = &cost
		}
		if index%4 != 0 {
			record.Quota = &domain.QuotaObservation{PlanType: "plus"}
			if index%4 == 2 {
				record.Quota.Windows = []domain.QuotaWindow{}
			} else if index%4 == 3 {
				record.Quota.Windows = []domain.QuotaWindow{{Id: "primary", UsedPercent: 12.345, WindowMinutes: 300, ResetsAt: random.Int63()}, {Id: "secondary", UsedPercent: math.Copysign(0, -1), WindowMinutes: -1, ResetsAt: -1}}
			}
		}
		data, err := encodeRecord(record, intern)
		if err != nil {
			test.Fatal(err)
		}
		var quota []byte
		if record.Quota != nil {
			quota, err = encodeQuota(*record.Quota, intern)
			if err != nil {
				test.Fatal(err)
			}
		}
		actual, err := decodeRecord(data, record.TimestampMs, record.QuotaBucket, record.IsQuotaOnly, quota, func(identity int64) (string, bool) { value, exists := values[identity]; return value, exists })
		if err != nil {
			test.Fatal(err)
		}
		if record.ReportedCostUsd != nil {
			if actual.ReportedCostUsd == nil || math.Float64bits(*actual.ReportedCostUsd) != math.Float64bits(*record.ReportedCostUsd) {
				test.Fatal("cost pointer or bits changed")
			}
			record.ReportedCostUsd, actual.ReportedCostUsd = nil, nil
		}
		if record.Quota != nil {
			for windowIndex, window := range record.Quota.Windows {
				if math.Float64bits(window.UsedPercent) != math.Float64bits(actual.Quota.Windows[windowIndex].UsedPercent) {
					test.Fatal("quota percentage bits changed")
				}
			}
		}
		if !reflect.DeepEqual(record, actual) {
			test.Fatalf("record fields changed at sample %d", index)
		}
	}
}

// TestPayloadRejectsCorruption covers truncation, versions, identities and trailing bytes.
func TestPayloadRejectsCorruption(test *testing.T) {
	data, err := encodeRecord(usageRecord("model"), func(string) (int64, error) { return 0, nil })
	if err != nil {
		test.Fatal(err)
	}
	for length := range len(data) {
		if _, err := decodeRecord(data[:length], 0, "", false, nil, func(identity int64) (string, bool) { return "", identity == 0 }); err == nil {
			test.Fatalf("accepted truncated payload of length %d", length)
		}
	}
	for _, invalid := range [][]byte{append(append([]byte{}, data...), 0), {2}, data} {
		if _, err := decodeRecord(invalid, 0, "", false, nil, func(int64) (string, bool) { return "", false }); err == nil {
			test.Fatal("accepted invalid payload")
		}
	}
}
