package claudeprobe

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// TestControlFailuresPreserveSafeEvidence verifies classification and redaction at the public transport boundary.
func TestControlFailuresPreserveSafeEvidence(test *testing.T) {
	const secretMarker = "SYNTHETIC-CONTROL-SECRET"
	for _, fixture := range []struct{ scenario, kind string }{
		{"unknown-method", "unsupported_method"},
		{"usage-auth", "authentication"},
		{"usage-network-error", "transport"},
		{"usage-protocol", "protocol"},
		{"missing-utilization", "protocol"},
		{"invalid-window", "protocol"},
		{"usage-hang", "timeout"},
		{"init-error", "authentication"},
		{"init-protocol", "protocol"},
	} {
		test.Run(fixture.scenario, func(test *testing.T) {
			options := fixtureOptions(test, fixture.scenario)
			options.Environment = append(options.Environment, "YOUSAGE_FIXTURE_SECRET="+secretMarker)
			result, err := Probe(context.Background(), options)
			if strings.HasPrefix(fixture.scenario, "init-") {
				var failure *Failure
				if !errors.As(err, &failure) || failure.Kind != fixture.kind {
					test.Fatal("initialization classification lost", err)
				}
				if strings.Contains(err.Error(), secretMarker) || strings.Contains(err.Error(), options.Executable) {
					test.Fatal("initialization error exposed synthetic private data")
				}
				return
			}
			if err != nil || result.State != "failed" || result.FailureKind != fixture.kind {
				test.Fatalf("usage classification = %+v, %v; want %s", result, err, fixture.kind)
			}
			encoded, _ := json.Marshal(result)
			if strings.Contains(string(encoded), secretMarker) {
				test.Fatal("failure result exposed synthetic CLI output")
			}
		})
	}
	options := fixtureOptions(test, "success")
	options.Executable = filepath.Join(options.ConfigDirectory, secretMarker+".exe")
	if _, err := Probe(context.Background(), options); err == nil || strings.Contains(err.Error(), secretMarker) {
		test.Fatal("process start did not return a safe failure")
	}
}
