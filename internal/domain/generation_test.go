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

// TestReliableTtftMs keeps a first token inside its request and away from error and quota facts.
func TestReliableTtftMs(test *testing.T) {
	for _, fixture := range []struct{ ttft, generation, expected int64 }{
		{800, 5000, 800}, {800, 0, 800}, {5000, 5000, 5000}, {5001, 5000, 0}, {0, 5000, 0}, {-1, 5000, 0}, {MaxGenerationMs + 1, 0, 0},
	} {
		if actual := ReliableTtftMs(fixture.ttft, fixture.generation); actual != fixture.expected {
			test.Error(fixture, actual)
		}
	}
	for _, record := range []Record{{Provider: Codex, Model: "model", Speed: "standard", TtftMs: -1}, {Provider: Codex, ErrorKind: "server", TtftMs: 1}, {Provider: Codex, IsQuotaOnly: true, Quota: &QuotaObservation{}, TtftMs: 1}} {
		if record.Validate() == nil {
			test.Error("invalid first token accepted", record)
		}
	}
}
