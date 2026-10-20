package diagnostics

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// testStore creates isolated synthetic diagnostics with a deterministic writer time.
func testStore(t *testing.T) *Store {
	t.Helper()
	directory := t.TempDir()
	if err := Initialize(directory); err != nil {
		t.Fatal(err)
	}
	store, err := Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	store.now = func() time.Time { return now }
	return store
}

// testEntry represents safe synthetic refresh metadata with no application content.
func testEntry() Entry {
	return Entry{Level: LevelInfo, Source: SourceMobile, Event: EventRefresh, Outcome: OutcomeSucceeded}
}

// writeFixture writes only into the test's private temporary directory.
func writeFixture(t *testing.T, directory, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(directory, name), data, 0600); err != nil {
		t.Fatal(err)
	}
}

// fixtureLine obtains the writer's actual schema for report mutation tests.
func fixtureLine(t *testing.T, store *Store) []byte {
	t.Helper()
	entry := testEntry()
	entry.Operation = store.NewOperation()
	if err := store.Record(entry); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(store.directory, "go.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Split(data, []byte{'\n'})[0]
}

func TestContextAndGeneratedCorrelation(t *testing.T) {
	store := testStore(t)
	data, err := os.ReadFile(filepath.Join(store.directory, contextFilename))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if json.Unmarshal(data, &fields) != nil || len(fields) != 2 || fields["schemaVersion"] != float64(1) || !validRunId(fields["runId"].(string)) {
		t.Fatalf("unexpected context schema: %s", data)
	}
	line := fixtureLine(t, store)
	var value record
	if json.Unmarshal(line, &value) != nil || value.RunId != store.context.RunId || value.OperationId != "g-1" || value.Time != store.now().UTC().Format(timeLayout) {
		t.Fatalf("unexpected generated record: %s", line)
	}
	if err := Initialize(store.directory); err != nil {
		t.Fatal(err)
	}
	context, err := readContext(store.directory)
	if err != nil || context.RunId == store.context.RunId {
		t.Fatal("process initialization must generate a fresh run")
	}
	other, err := Open(store.directory)
	if err != nil {
		t.Fatal(err)
	}
	entry := testEntry()
	entry.Operation = other.NewOperation()
	if err := store.Record(entry); !errors.Is(err, ErrInvalid) {
		t.Fatal("foreign operation accepted", err)
	}
}

func TestContextRejectsExtraFieldsAndMalformedIds(t *testing.T) {
	for _, content := range []string{
		`{"schemaVersion":1,"runId":"CANARY_SECRET_CONTEXT"}`,
		`{"schemaVersion":1,"runId":"00112233445566778899aabbccddeeff","key":"CANARY_SECRET_CONTEXT"}`,
		`{"schemaVersion":1,"runId":"00112233445566778899aabbccddeeff","runId":"00112233445566778899aabbccddeeff"}`,
		`{"schemaVersion":1,"RunId":"00112233445566778899aabbccddeeff"}`,
		`{"schemaVersion":1,"runId":null}`,
		strings.Repeat("x", 513),
	} {
		t.Run(content[:min(len(content), 45)], func(t *testing.T) {
			directory := t.TempDir()
			writeFixture(t, directory, contextFilename, []byte(content))
			if _, err := Open(directory); !errors.Is(err, ErrUnavailable) {
				t.Fatal("invalid context accepted", err)
			}
		})
	}
}

func TestRecordRejectsUnknownEnumsAndNumericBounds(t *testing.T) {
	store := testStore(t)
	changes := []func(*Entry){
		func(entry *Entry) { entry.Level = "CANARY_SECRET_LEVEL" },
		func(entry *Entry) { entry.Source = "CANARY_SECRET_SOURCE" },
		func(entry *Entry) { entry.Event = "CANARY_SECRET_EVENT" },
		func(entry *Entry) { entry.Outcome = "CANARY_SECRET_OUTCOME" },
		func(entry *Entry) { entry.Failure = "CANARY_SECRET_FAILURE" },
		func(entry *Entry) { entry.Status = 600 },
		func(entry *Entry) { entry.Status = -1 },
		func(entry *Entry) { entry.DurationMs = -1 },
		func(entry *Entry) { entry.Count = maxInteger + 1 },
		func(entry *Entry) { entry.Bytes = -1 },
	}
	for _, change := range changes {
		entry := testEntry()
		change(&entry)
		if err := store.Record(entry); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid metadata accepted: %v", err)
		}
	}
	if _, err := os.Stat(filepath.Join(store.directory, "go.jsonl")); !os.IsNotExist(err) {
		t.Fatal("rejected metadata reached disk")
	}
	entry := testEntry()
	entry.Status, entry.DurationMs, entry.Count, entry.Bytes = 599, maxInteger, maxInteger, maxInteger
	if err := store.Record(entry); err != nil {
		t.Fatal("valid numeric boundary rejected", err)
	}
}

func TestRotationIncludesNewlineAndLimitsFileCount(t *testing.T) {
	store := testStore(t)
	line := fixtureLine(t, store)
	length := len(line) + 1
	padding := bytes.Repeat([]byte{'\n'}, MaxFileBytes-length)
	writeFixture(t, store.directory, "go.jsonl", padding)
	entry := testEntry()
	entry.Operation = Operation{owner: store, sequence: 1}
	if err := store.Record(entry); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(store.directory, "go.jsonl"))
	if err != nil || info.Size() != MaxFileBytes {
		t.Fatal("record did not fill exact newline-inclusive boundary", info, err)
	}
	if _, err := os.Stat(filepath.Join(store.directory, "go.1.jsonl")); !os.IsNotExist(err) {
		t.Fatal("rotated too early")
	}
	for rotation := 0; rotation < 6; rotation++ {
		if rotation != 0 {
			writeFixture(t, store.directory, "go.jsonl", bytes.Repeat([]byte{'\n'}, MaxFileBytes))
		}
		if err := store.Record(entry); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(store.directory)
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	var count int
	for _, item := range entries {
		if strings.HasPrefix(item.Name(), "go") {
			count++
			info, err := item.Info()
			if err != nil || info.Size() > MaxFileBytes {
				t.Fatal("file exceeded capacity", err)
			}
			total += info.Size()
		}
	}
	if count != 4 || total > 4*MaxFileBytes {
		t.Fatalf("Go capacity exceeded: %d files, %d bytes", count, total)
	}
}

func TestRetentionOnlyTouchesExactGoFilenames(t *testing.T) {
	store := testStore(t)
	now := store.now()
	names := []string{"go.jsonl", "go.1.jsonl", "go.2.jsonl", "go.3.jsonl", "go.4.jsonl", "go.jsonl.backup", "native.jsonl", "snapshot.json"}
	for _, name := range names {
		writeFixture(t, store.directory, name, []byte("{}\n"))
		stale := now.Add(-Retention - time.Second)
		if name == "go.2.jsonl" {
			stale = now.Add(-Retention)
		}
		if name == "go.3.jsonl" {
			stale = now.Add(-Retention + time.Second)
		}
		if err := os.Chtimes(filepath.Join(store.directory, name), stale, stale); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.maintain(now); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		_, err := os.Stat(filepath.Join(store.directory, name))
		shouldDelete := name == "go.jsonl" || name == "go.1.jsonl"
		if os.IsNotExist(err) != shouldDelete {
			t.Fatalf("unexpected retention disposition for %s: %v", name, err)
		}
	}
	writeFixture(t, store.directory, "go.3.jsonl", bytes.Repeat([]byte{'\n'}, MaxFileBytes+1))
	if err := store.Record(testEntry()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(store.directory, "go.3.jsonl")); !os.IsNotExist(err) {
		t.Fatal("oversized Go file retained")
	}
}

func TestPartialTailRecoveryAndConcurrency(t *testing.T) {
	store := testStore(t)
	first := fixtureLine(t, store)
	writeFixture(t, store.directory, "go.jsonl", append(append(first, '\n'), []byte(`{"event":"CANARY_PARTIAL`)...))
	const workers = 12
	const entries = 20
	var group sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		group.Go(func() {
			for index := 0; index < entries; index++ {
				entry := testEntry()
				entry.Operation = store.NewOperation()
				if err := store.Record(entry); err != nil {
					t.Error(err)
				}
				if index%7 == 0 {
					if _, err := store.Report(); err != nil {
						t.Error(err)
					}
				}
			}
		})
	}
	group.Wait()
	data, err := os.ReadFile(filepath.Join(store.directory, "go.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "CANARY") {
		t.Fatal("partial tail was not removed")
	}
	lines := bytes.Split(bytes.TrimSuffix(data, []byte{'\n'}), []byte{'\n'})
	if len(lines) != workers*entries+1 {
		t.Fatalf("lost records: %d", len(lines))
	}
	seen := make(map[string]bool)
	for _, line := range lines {
		value, ok := parseRecord(line, "g", store.now())
		if !ok || len(line)+1 > MaxRecordBytes || seen[value.OperationId] {
			t.Fatal("corrupt, oversized or duplicate operation")
		}
		seen[value.OperationId] = true
	}
}

func TestFilesystemFailuresReturnFixedErrorsAndRecover(t *testing.T) {
	store := testStore(t)
	path := filepath.Join(store.directory, "go.jsonl")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := store.Record(testEntry()); !errors.Is(err, ErrUnavailable) {
		t.Fatal("directory obstruction was not surfaced", err)
	}
	if report, err := store.Report(); !errors.Is(err, ErrUnavailable) || strings.Contains(report, store.directory) {
		t.Fatal("unsafe filesystem failure report", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := store.Record(testEntry()); err != nil {
		t.Fatal("writer did not recover", err)
	}
	if err := Initialize(path); !errors.Is(err, ErrUnavailable) {
		t.Fatal("file accepted as directory", err)
	}
	if _, err := Open("relative"); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	var absent *Store
	for _, value := range []*Store{absent, {}} {
		if value.NewOperation() != (Operation{}) || !errors.Is(value.Record(testEntry()), ErrUnavailable) {
			t.Fatal("unavailable store was not inert")
		}
		if _, err := value.Report(); !errors.Is(err, ErrUnavailable) {
			t.Fatal(err)
		}
	}
}
