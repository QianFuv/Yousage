package mobile

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"yousage/internal/diagnostics"
)

// attachMobileDiagnostics uses a synthetic native-owned context for one Go writer.
func attachMobileDiagnostics(test *testing.T, viewer *Service) string {
	test.Helper()
	directory := filepath.Join(viewer.directory, "diagnostics")
	if err := diagnostics.Initialize(directory); err != nil {
		test.Fatal(err)
	}
	store, err := diagnostics.Open(directory)
	if err != nil {
		test.Fatal(err)
	}
	viewer.diagnostics = store
	return directory
}

// mobileDiagnosticRecords reads only reconstructed synthetic report records.
func mobileDiagnosticRecords(test *testing.T, viewer *Service) []map[string]any {
	test.Helper()
	report, err := viewer.diagnostics.Report()
	if err != nil {
		test.Fatal(err)
	}
	var records []map[string]any
	for _, line := range strings.Split(report, "\n")[1:] {
		if line == "" {
			continue
		}
		var record map[string]any
		if json.Unmarshal([]byte(line), &record) != nil {
			test.Fatal("invalid record")
		}
		records = append(records, record)
	}
	return records
}

// TestMobileDiagnosticsCorrelateWithoutPayload verifies the acceptance transaction
// shares one opaque operation and never exports links, ETags or snapshot contents.
func TestMobileDiagnosticsCorrelateWithoutPayload(test *testing.T) {
	viewer, relay := newSnapshotRelay(test)
	attachMobileDiagnostics(test, viewer)
	relay.set(snapshotFixture(test, "credential-canary"), `"etag-canary"`)
	candidate := loadCandidate(test, viewer)
	if err := viewer.CommitSnapshot(candidate.CandidateId); err != nil {
		test.Fatal(err)
	}
	var operation string
	seen := map[string]bool{}
	for _, record := range mobileDiagnosticRecords(test, viewer) {
		identity, _ := record["operationId"].(string)
		if operation == "" {
			operation = identity
		}
		if identity != operation || identity == "" {
			test.Fatal("transaction correlation changed")
		}
		seen[record["event"].(string)] = true
	}
	for _, event := range []string{"refresh", "validation", "stage", "commit"} {
		if !seen[event] {
			test.Fatalf("missing %s", event)
		}
	}
	report, _ := viewer.diagnostics.Report()
	for _, canary := range []string{relay.link(), relay.key, relay.identifier, candidate.CandidateId, "credential-canary", "etag-canary", relay.server.URL} {
		if strings.Contains(report, canary) {
			test.Fatal("private data entered diagnostics")
		}
	}
}

// TestMobileDiagnosticFailurePreservesTransactions makes only the log path fail;
// load, commit, pairing and last-good cache must retain their ordinary behavior.
func TestMobileDiagnosticFailurePreservesTransactions(test *testing.T) {
	viewer, relay := newSnapshotRelay(test)
	directory := attachMobileDiagnostics(test, viewer)
	if err := os.Mkdir(filepath.Join(directory, "go.jsonl"), 0700); err != nil {
		test.Fatal(err)
	}
	relay.set(snapshotFixture(test, "B"), `"B"`)
	candidate := loadCandidate(test, viewer)
	acceptedSnapshot(test, viewer, snapshotFixture(test, "A"), `"A"`)
	if err := viewer.CommitSnapshot(candidate.CandidateId); err != nil {
		test.Fatal(err)
	}
	acceptedSnapshot(test, viewer, snapshotFixture(test, "B"), `"B"`)
	if _, err := viewer.Pair(relay.link()); err != nil {
		test.Fatal(err)
	}
	if _, err := viewer.Pair("secret-invalid-link"); err == nil {
		test.Fatal("invalid pairing accepted")
	}
	acceptedSnapshot(test, viewer, snapshotFixture(test, "B"), `"B"`)
	if err := viewer.Unpair(); err != nil {
		test.Fatal(err)
	}
}

// TestMobileDiagnosticRejectionRetainsCache distinguishes invalid shapes and
// transport failures while keeping accepted bytes and ETag unchanged.
func TestMobileDiagnosticRejectionRetainsCache(test *testing.T) {
	viewer, relay := newSnapshotRelay(test)
	attachMobileDiagnostics(test, viewer)
	before, err := os.ReadFile(filepath.Join(viewer.directory, snapshotFile))
	if err != nil {
		test.Fatal(err)
	}
	relay.set(`{"formatVersion":1}`, `"bad-shape"`)
	if result, err := viewer.LoadSnapshot(`"A"`); err != nil || result.Problem != "data" {
		test.Fatal(result.Status, err)
	}
	viewer.client = &http.Client{Transport: diagnosticTransport{}}
	if result, err := viewer.LoadSnapshot(`"A"`); err != nil || result.Problem != "network" {
		test.Fatal(result.Status, err)
	}
	after, err := os.ReadFile(filepath.Join(viewer.directory, snapshotFile))
	if err != nil || !bytes.Equal(before, after) {
		test.Fatal("diagnostic rejection changed accepted cache")
	}
	seen := map[string]bool{}
	for _, record := range mobileDiagnosticRecords(test, viewer) {
		seen[record["failure"].(string)] = true
	}
	if !seen["invalid_data"] || !seen["network"] {
		test.Fatal("failure categories missing")
	}
}

// diagnosticTransport fails locally without contacting a real relay.
type diagnosticTransport struct{}

// RoundTrip supplies a credential-bearing canary error that must never be logged.
func (diagnosticTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("https://secret.invalid/credential-canary")
}

// TestMobileReadsNativeContext preserves native process identity across Go startup.
func TestMobileReadsNativeContext(test *testing.T) {
	directory := test.TempDir()
	contextDirectory := filepath.Join(directory, "diagnostics")
	if err := diagnostics.Initialize(contextDirectory); err != nil {
		test.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(contextDirectory, "context.json"))
	if err != nil {
		test.Fatal(err)
	}
	viewer := newService(directory, nil)
	if viewer.diagnostics == nil {
		test.Fatal("native context was not opened")
	}
	after, err := os.ReadFile(filepath.Join(contextDirectory, "context.json"))
	if err != nil || !bytes.Equal(before, after) {
		test.Fatal("Go replaced native run identity")
	}
}
