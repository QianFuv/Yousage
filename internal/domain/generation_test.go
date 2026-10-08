package domain

import "testing"

// TestReliableGenerationRejectsIdleAndImpossibleRates bounds measurements without discarding usage.
func TestReliableGenerationRejectsIdleAndImpossibleRates(test *testing.T) {
	for _, fixture := range []struct{ duration, output, expected int64 }{
		{1000, 100, 1000}, {MaxGenerationMs, 100, MaxGenerationMs}, {MaxGenerationMs + 1, 100, 0}, {0, 100, 0}, {-1, 100, 0}, {1000, 0, 0}, {1, 10, 1}, {1, 11, 0},
	} {
		if actual := ReliableGenerationMs(fixture.duration, fixture.output); actual != fixture.expected {
			test.Fatal(fixture, actual)
		}
	}
	record := Record{Provider: Codex, Model: "model", Speed: "standard", GenerationMs: -1}
	if record.Validate() == nil {
		test.Fatal("negative generation duration accepted")
	}
	for _, record := range []Record{{Provider: Codex, ErrorKind: "server", GenerationMs: 1}, {Provider: Codex, IsQuotaOnly: true, Quota: &QuotaObservation{}, GenerationMs: 1}} {
		if record.Validate() == nil {
			test.Fatal("non-usage duration accepted", record)
		}
	}
}
