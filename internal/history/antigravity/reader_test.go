package antigravity

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"yousage/internal/domain"
)

func numeric(key, value uint64) []byte {
	result := binary.AppendUvarint(nil, key*8)
	return binary.AppendUvarint(result, value)
}
func embedded(key uint64, value []byte) []byte {
	result := binary.AppendUvarint(nil, key*8+2)
	result = binary.AppendUvarint(result, uint64(len(value)))
	return append(result, value...)
}
func join(parts ...[]byte) []byte {
	result := []byte{}
	for _, part := range parts {
		result = append(result, part...)
	}
	return result
}
func usage(input uint64, identities map[uint64]string) []byte {
	result := join(numeric(2, input), numeric(3, 10), numeric(4, 2), numeric(5, 3), numeric(9, 12), numeric(10, 8))
	for _, key := range []uint64{11, 12, 7} {
		if text := identities[key]; text != "" {
			result = append(result, embedded(key, []byte(text))...)
		}
	}
	return result
}
func step(data []byte, seconds uint64, model string) []byte {
	result := embedded(9, data)
	if seconds > 0 {
		result = append(result, embedded(8, numeric(1, seconds))...)
	}
	if model != "" {
		result = append(result, embedded(24, embedded(12, []byte(model)))...)
	}
	return result
}
func databaseFixture(t *testing.T, path string) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { database.Close() })
	for _, statement := range []string{"PRAGMA journal_mode=WAL", "PRAGMA wal_autocheckpoint=0", "CREATE TABLE steps(idx INTEGER,metadata BLOB)", "CREATE TABLE gen_metadata(idx INTEGER,data BLOB)", "CREATE TABLE trajectory_metadata_blob(data BLOB)"} {
		if _, err = database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	return database
}
func addStep(t *testing.T, database *sql.DB, index int, data []byte) {
	t.Helper()
	if _, err := database.Exec("INSERT INTO steps VALUES(?,?)", index, data); err != nil {
		t.Fatal(err)
	}
}
func flatten(result Result) []domain.Record {
	records := []domain.Record{}
	for _, file := range result.Files {
		records = append(records, file.Records...)
	}
	return records
}

func TestProtobufValidationAndModelNames(t *testing.T) {
	for _, data := range [][]byte{{0}, {8, 128}, {8, 255, 255, 255, 255, 255, 255, 255, 255, 255, 2}, {10, 3, 1}, {11}, {9, 0}, {13, 0}} {
		if _, err := parseFields(data); err == nil {
			t.Fatalf("accepted malformed %x", data)
		}
	}
	parsed, err := parseFields(join(numeric(1, maxSafeInteger+1), numeric(2, 42), []byte{9, 0, 0, 0, 0, 0, 0, 0, 0}, embedded(3, []byte{255})))
	if err != nil || numberAt(parsed, 1) != 0 || numberAt(parsed, 2) != 42 {
		t.Fatal(parsed, err)
	}
	if _, err = textAt(parsed, 3); err == nil {
		t.Fatal("invalid UTF-8 accepted")
	}
	if _, err = parseMetadata(numeric(3, 1), false); err == nil {
		t.Fatal("missing generation wrapper accepted")
	}
	if _, err = parseMetadata(join(step(usage(1, nil), 1, ""), numeric(28, 1)), true); err == nil {
		t.Fatal("nonblob retry accepted")
	}
	if modelName("Claude 4.5 Sonnet (thinking)", 0) != "claude-sonnet-4-5" || modelName("Gemini 2.5 Pro", 0) != "gemini-2.5-pro" || modelName("", 333) != "claude-sonnet-4-5" || modelName("", 999) != "antigravity-model-999" {
		t.Fatal("model normalization")
	}
	instant := timestamp(fields{1: {uint64(2)}, 2: {uint64(123456789)}})
	if instant == nil || *instant != 2123 {
		t.Fatal(instant)
	}
}

func TestWalRetriesFallbackAndReadOnly(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "session.db")
	database := databaseFixture(t, path)
	generation := embedded(1, join(embedded(19, []byte("Claude 4.5 Sonnet")), embedded(4, usage(40, map[uint64]string{11: "same"})), embedded(9, embedded(4, numeric(1, 3)))))
	if _, err := database.Exec("INSERT INTO gen_metadata VALUES(?,?)", 1, generation); err != nil {
		t.Fatal(err)
	}
	addStep(t, database, 1, join(step(usage(20, map[uint64]string{11: "same"}), 0, ""), embedded(28, embedded(2, join(numeric(1, 999), usage(7, nil))))))
	if _, err := database.Exec("INSERT INTO trajectory_metadata_blob VALUES(?)", embedded(2, numeric(1, 4))); err != nil {
		t.Fatal(err)
	}
	before := map[string][32]byte{}
	for _, file := range []string{path, path + "-wal"} {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		before[file] = sha256.Sum256(data)
	}
	result, err := Read(context.Background(), []string{root}, 0)
	records := flatten(result)
	if err != nil || len(result.Errors) != 0 || len(records) != 2 {
		t.Fatal(result, err)
	}
	sort.Slice(records, func(first, second int) bool { return records[first].TimestampMs < records[second].TimestampMs })
	if records[0].Model != "claude-sonnet-4-5" || records[0].TimestampMs != 3000 || records[0].Totals.UncachedInputTokens != 40 || records[0].Totals.OutputTokens != 20 || records[0].Totals.ReasoningTokens != 12 || records[0].ReportedCostUsd != nil || records[0].SessionId != "session" {
		t.Fatal(records)
	}
	if records[1].Model != "claude-sonnet-4-5" || records[1].TimestampMs != 4000 {
		t.Fatal(records[1])
	}
	for file, digest := range before {
		data, err := os.ReadFile(file)
		if err != nil || sha256.Sum256(data) != digest {
			t.Fatal("source changed", file, err)
		}
	}
	addStep(t, database, 2, step(join(numeric(1, 999), usage(8, nil)), 5, ""))
	result, err = Read(context.Background(), []string{root}, 5000)
	if err != nil || len(flatten(result)) != 1 || flatten(result)[0].Model != "antigravity-model-999" {
		t.Fatal(result, err)
	}
}

func TestTransitiveAliasMergeBeforeFilteringAndFirstOwner(t *testing.T) {
	firstRoot, secondRoot := t.TempDir(), t.TempDir()
	first := databaseFixture(t, filepath.Join(firstRoot, "first.db"))
	second := databaseFixture(t, filepath.Join(secondRoot, "second.db"))
	addStep(t, first, 1, step(usage(20, map[uint64]string{11: "a"}), 0, ""))
	addStep(t, second, 1, step(usage(30, map[uint64]string{12: "b"}), 8, "Known Model"))
	addStep(t, second, 2, step(usage(40, map[uint64]string{11: "a", 12: "b"}), 7, "Bridge Model"))
	result, err := Read(context.Background(), []string{firstRoot, secondRoot, firstRoot}, 7500)
	if err != nil || len(result.Files) != 2 || len(flatten(result)) != 0 {
		t.Fatal("filtered before merging", result, err)
	}
	result, err = Read(context.Background(), []string{firstRoot, secondRoot}, 7000)
	records := flatten(result)
	if err != nil || len(records) != 1 || len(result.Files[0].Records) != 1 || records[0].SessionId != "first" || records[0].TimestampMs != 7000 || records[0].Totals.UncachedInputTokens != 40 || records[0].Model != "bridge-model" || records[0].DedupeKey != "antigravity:11:a" {
		t.Fatal(result, err)
	}
}

func TestPartialFailureMissingCancellationAndDiscovery(t *testing.T) {
	root := t.TempDir()
	good := databaseFixture(t, filepath.Join(root, "good.db"))
	addStep(t, good, 1, step(usage(1, nil), 1, "fixture"))
	bad := databaseFixture(t, filepath.Join(root, "bad.db"))
	addStep(t, bad, 1, step(usage(1, nil), 1, "valid-before-error"))
	addStep(t, bad, 2, []byte{0})
	if err := os.WriteFile(filepath.Join(root, "broken.db"), []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := Read(context.Background(), []string{root, filepath.Join(root, "missing")}, 0)
	if err != nil || len(result.Errors) != 2 || len(flatten(result)) != 1 {
		t.Fatal(result, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = Read(ctx, []string{root}, 0); err == nil {
		t.Fatal("cancel ignored")
	}
	home := t.TempDir()
	conversations := filepath.Join(home, "custom", "conversations")
	if err = os.MkdirAll(conversations, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANTIGRAVITY_DATA_DIR", " ~/custom, "+filepath.Join(home, "custom"))
	roots, err := DiscoverRoots(home, []string{filepath.Join(home, "profile")})
	if err != nil || len(roots) != 2 || roots[0] != conversations || roots[1] != filepath.Join(home, "profile", "antigravity-acp") {
		t.Fatal(roots, err)
	}
	if _, err = os.Stat(roots[1]); !os.IsNotExist(err) {
		t.Fatal("created profile data", err)
	}
	t.Setenv("ANTIGRAVITY_DATA_DIR", "")
	roots, err = DiscoverRoots(home, nil)
	if err != nil || len(roots) != 5 {
		t.Fatal(roots, err)
	}
}
