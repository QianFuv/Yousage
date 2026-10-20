package diagnostics

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// changedLine mutates a synthetic record without inspecting application content.
func changedLine(t *testing.T, line []byte, key string, value any) []byte {
	t.Helper()
	var fields map[string]any
	if err := json.Unmarshal(line, &fields); err != nil {
		t.Fatal(err)
	}
	fields[key] = value
	data, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestReportRejectsUnknownFieldsEnumsAndMalformedIds(t *testing.T) {
	store := testStore(t)
	line := fixtureLine(t, store)
	var input []byte
	changes := []struct {
		key   string
		value any
	}{
		{"message", "CANARY_MESSAGE_TOKEN"}, {"error", "CANARY_ERROR_TOKEN"},
		{"stack", "CANARY_STACK_TOKEN"}, {"url", "https://CANARY_URL.invalid"},
		{"path", "CANARY_PATH"}, {"etag", "CANARY_ETAG"}, {"headers", "CANARY_HEADER_TOKEN"},
		{"key", "CANARY_PAIRING_KEY"}, {"snapshot", "CANARY_CONTENT"},
		{"runId", "CANARY_RUN_TOKEN"}, {"runId", strings.Repeat("a", 33)},
		{"runId", strings.Repeat("A", 32)}, {"operationId", "CANARY_OPERATION_TOKEN"},
		{"operationId", "g-01"}, {"operationId", "g-0"}, {"operationId", "g-9007199254740992"},
		{"operationId", "n-1"}, {"operationId", "g-1-CANARY"}, {"operationId", ""},
		{"level", "CANARY_LEVEL"}, {"source", "CANARY_SOURCE"}, {"event", "CANARY_EVENT"},
		{"outcome", "CANARY_OUTCOME"}, {"failure", "CANARY_FAILURE"},
		{"time", "CANARY_TIME"}, {"time", "2026-10-20T00:00:00Z"},
		{"status", -1}, {"status", 600}, {"count", 1.5}, {"bytes", -1},
		{"durationMs", float64(maxInteger) + 1}, {"etagPresent", "CANARY_BOOLEAN"},
		{"schemaVersion", 2}, {"conditional", nil},
	}
	for _, change := range changes {
		input = append(append(input, changedLine(t, line, change.key, change.value)...), '\n')
	}
	input = append(input, append(bytes.Replace(line, []byte(`"level":"info"`), []byte(`"level":"info","level":"error"`), 1), '\n')...)
	input = append(input, append(bytes.Replace(line, []byte(`"level"`), []byte(`"Level"`), 1), '\n')...)
	input = append(input, append(bytes.Replace(line, []byte(`"count":0,`), nil, 1), '\n')...)
	input = append(input, append(line, '\n')...)
	writeFixture(t, store.directory, "go.jsonl", input)
	report, err := store.Report()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(report, "CANARY") || strings.Count(report, `"schemaVersion"`) != 1 {
		t.Fatalf("unsafe or invalid records exported: %s", report)
	}
}

func TestDiskIdsOnlyPromiseFormatValidation(t *testing.T) {
	store := testStore(t)
	line := fixtureLine(t, store)
	line = changedLine(t, line, "runId", "00112233445566778899aabbccddeeff")
	line = changedLine(t, line, "operationId", "g-123")
	if _, ok := parseRecord(line, "g", store.now()); !ok {
		t.Fatal("valid-shaped IDs must not pretend to require authentication")
	}
}

// TestNativeLifecycleSequenceSurvivesExport keeps recovery history useful without WebView context.
func TestNativeLifecycleSequenceSurvivesExport(t *testing.T) {
	store := testStore(t)
	line := changedLine(t, fixtureLine(t, store), "operationId", "n-1")
	line = changedLine(t, line, "source", "native")
	events := []Event{EventLifecycleCreate, EventLifecycleStart, EventLifecycleResume, EventLifecyclePause, EventLifecycleStop, EventLifecycleDestroy, EventLifecycleLowMemory}
	var input []byte
	for _, event := range events {
		input = append(append(input, changedLine(t, line, "event", event)...), '\n')
	}
	writeFixture(t, store.directory, "native.jsonl", input)
	report, err := store.Report()
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if !strings.Contains(report, `"event":"`+string(event)+`"`) {
			t.Fatalf("native lifecycle event %q was lost during safe export", event)
		}
	}
}

func TestReportMergesNativeWithoutContextAndEnforcesWriterPrefix(t *testing.T) {
	store := testStore(t)
	line := fixtureLine(t, store)
	native := changedLine(t, line, "operationId", "n-1")
	native = changedLine(t, native, "source", "native")
	native = changedLine(t, native, "count", 2)
	native = changedLine(t, native, "time", store.now().Add(time.Second).Format(timeLayout))
	wrong := changedLine(t, native, "operationId", "g-2")
	writeFixture(t, store.directory, "native.jsonl", append(append(append(native, '\n'), wrong...), '\n'))
	if err := os.Remove(filepath.Join(store.directory, contextFilename)); err != nil {
		t.Fatal(err)
	}
	report, err := ReadReport(store.directory)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(report, `"schemaVersion"`) != 2 || !strings.Contains(strings.Split(report, "\n")[1], `"count":2`) {
		t.Fatalf("incorrect native merge/order: %s", report)
	}
}

func TestReportSkipsCorruptionPartialAndOversizedLines(t *testing.T) {
	store := testStore(t)
	line := fixtureLine(t, store)
	var input []byte
	input = append(input, []byte("not json\n{}\n")...)
	input = append(input, bytes.Repeat([]byte{'x'}, MaxRecordBytes*2)...)
	input = append(input, '\n')
	exact := append(append([]byte(nil), line...), bytes.Repeat([]byte{' '}, MaxRecordBytes-1-len(line))...)
	input = append(input, append(exact, '\n')...)
	tooLong := append(append([]byte(nil), exact...), ' ')
	input = append(input, append(tooLong, '\n')...)
	input = append(input, append(line, '\n')...)
	input = append(input, line...)
	writeFixture(t, store.directory, "go.jsonl", input)
	report, err := store.Report()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(report, `"schemaVersion"`) != 2 {
		t.Fatalf("line boundary parsing failed: %s", report)
	}
}

func TestReportRetentionTimestampBoundaries(t *testing.T) {
	store := testStore(t)
	line := fixtureLine(t, store)
	var input []byte
	for _, delta := range []time.Duration{-Retention - time.Millisecond, -Retention, -Retention + time.Millisecond, 5 * time.Minute, 5*time.Minute + time.Millisecond} {
		input = append(input, append(changedLine(t, line, "time", store.now().Add(delta).Format(timeLayout)), '\n')...)
	}
	writeFixture(t, store.directory, "go.jsonl", input)
	writeFixture(t, store.directory, "go.1.jsonl", append(line, '\n'))
	stale := store.now().Add(-Retention - time.Second)
	if err := os.Chtimes(filepath.Join(store.directory, "go.1.jsonl"), stale, stale); err != nil {
		t.Fatal(err)
	}
	report, err := store.Report()
	if err != nil || strings.Count(report, `"schemaVersion"`) != 3 {
		t.Fatalf("incorrect timestamp boundaries: %v %s", err, report)
	}
}

func TestReportCapacityIncludesHeaderMarkerAndNewlines(t *testing.T) {
	store := testStore(t)
	line := append(fixtureLine(t, store), '\n')
	full := bytes.Repeat(line, MaxFileBytes/len(line))
	for rotation := 0; rotation <= 3; rotation++ {
		writeFixture(t, store.directory, logFilename("go", rotation), full)
	}
	report, err := store.Report()
	if err != nil {
		t.Fatal(err)
	}
	if len(report) > MaxReportBytes || !strings.HasPrefix(report, reportHeader) || !strings.HasSuffix(report, truncationLine) {
		t.Fatal("invalid bounded report framing", len(report))
	}
	if len(report) < MaxReportBytes-len(line) {
		t.Fatal("report discarded more capacity than one whole record")
	}
	for _, row := range strings.Split(strings.TrimSuffix(strings.TrimPrefix(report, reportHeader), truncationLine), "\n") {
		if row == "" {
			continue
		}
		if _, ok := parseRecord([]byte(row), "g", store.now()); !ok {
			t.Fatal("report contains partial/unvalidated row")
		}
	}
}

func TestReportOversizedFileAndUnrelatedFiles(t *testing.T) {
	store := testStore(t)
	line := append(fixtureLine(t, store), '\n')
	data := append(append([]byte(nil), line...), bytes.Repeat([]byte{'\n'}, MaxFileBytes)...)
	writeFixture(t, store.directory, "native.jsonl", data)
	writeFixture(t, store.directory, "snapshot.json", []byte("CANARY_UNRELATED"))
	writeFixture(t, store.directory, "go.4.jsonl", []byte("CANARY_UNRELATED"))
	report, err := store.Report()
	if err != nil || len(report) > MaxReportBytes || !strings.HasSuffix(report, truncationLine) || strings.Contains(report, "CANARY") {
		t.Fatal("oversized/unrelated file handling failed", err)
	}
}

// failingReader models a disk read failure without exposing filesystem details.
type failingReader struct{}

// Read supplies a deterministic failure for the report reader's error path.
func (failingReader) Read([]byte) (int, error) { return 0, errors.New("CANARY_DISK_ERROR") }

func TestReadErrorAndSymlinkDoNotLeak(t *testing.T) {
	if _, err := readRecords(failingReader{}, "g", time.Now()); err == nil || err == io.EOF {
		t.Fatal("read failure ignored")
	}
	store := testStore(t)
	outside := filepath.Join(t.TempDir(), "private.jsonl")
	if err := os.WriteFile(outside, []byte("CANARY_OUTSIDE"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(store.directory, "go.jsonl")); err != nil {
		t.Skip("host cannot create test symlinks")
	}
	if err := store.Record(testEntry()); !errors.Is(err, ErrUnavailable) {
		t.Fatal("writer followed symlink", err)
	}
	report, err := store.Report()
	if !errors.Is(err, ErrUnavailable) || strings.Contains(report, "CANARY") {
		t.Fatal("report followed symlink", err)
	}
	data, err := os.ReadFile(outside)
	if err != nil || string(data) != "CANARY_OUTSIDE" {
		t.Fatal("diagnostics modified unrelated file", err)
	}
}
