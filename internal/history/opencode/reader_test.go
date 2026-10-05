package opencode

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"yousage/internal/accounting"
	"yousage/internal/domain"
)

func message(model string, input int, cost float64) string {
	data, _ := json.Marshal(map[string]any{"role": "assistant", "modelID": model, "time": map[string]any{"created": 1000}, "tokens": map[string]any{"input": input, "output": 20, "reasoning": 5, "cache": map[string]any{"read": 30, "write": 10}}, "cost": cost, "sessionID": "json-session"})
	return string(data)
}
func fixtureDatabase(t *testing.T, path string) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { database.Close() })
	for _, statement := range []string{"PRAGMA journal_mode=WAL", "PRAGMA wal_autocheckpoint=0", `CREATE TABLE message(id TEXT,session_id TEXT,data TEXT,time_created INTEGER)`, `CREATE TABLE session_message(id TEXT,session_id TEXT,data TEXT,time_created INTEGER,type TEXT)`} {
		if _, err := database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	return database
}
func addMessage(t *testing.T, database *sql.DB, id, model string, input int) {
	t.Helper()
	if _, err := database.Exec("INSERT INTO message VALUES(?,?,?,?)", id, "sql-session", message(model, input, 0), 1000); err != nil {
		t.Fatal(err)
	}
}
func writeLegacy(t *testing.T, root, id, data string) {
	t.Helper()
	directory := filepath.Join(root, "storage", "message", "session")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, id+".json"), []byte(data), 0600); err != nil {
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

func TestNormalizationAndInvalidFallbacks(t *testing.T) {
	record := ParseMessage([]byte(message("fixture", 100, 0)), Fallback{Id: "sql-id", SessionId: "sql-session"})
	if record == nil || record.Totals.Total() != 165 || record.Totals.OutputTokens != 25 || record.Totals.ReasoningTokens != 5 || record.ReportedCostUsd != nil || record.DedupeKey != "opencode:sql-id" || record.SessionId != "sql-session" {
		t.Fatal(record)
	}
	instant := int64(2000)
	record = ParseMessage([]byte(`{"model":{"id":"fixture"},"tokens":{"input":1}}`), Fallback{TimestampMs: &instant})
	if record == nil || record.TimestampMs != 2000 {
		t.Fatal(record)
	}
	if ParseMessage([]byte(`{"modelID":"fixture","time":{"created":"bad"},"tokens":{"input":1}}`), Fallback{TimestampMs: &instant}) != nil {
		t.Fatal("invalid JSON time fell back to SQL")
	}
	if ParseMessage([]byte(`{"role":"user","modelID":"fixture","time":{"created":1000},"tokens":{"input":1}}`), Fallback{}) != nil {
		t.Fatal("user message emitted")
	}
}

func TestDatabasePriorityMigrationLegacyAndWalRefresh(t *testing.T) {
	root := t.TempDir()
	primary := fixtureDatabase(t, filepath.Join(root, "opencode.db"))
	addMessage(t, primary, "same", "primary", 100)
	secondary := fixtureDatabase(t, filepath.Join(root, "opencode-work.db"))
	addMessage(t, secondary, "same", "secondary", 200)
	if _, err := primary.Exec("INSERT INTO session_message VALUES(?,?,?,?,?)", "same", "session", message("other-table", 300, 1), 1000, "assistant"); err != nil {
		t.Fatal(err)
	}
	writeLegacy(t, root, "same", message("legacy", 400, 1))
	writeLegacy(t, root, "unique", message("legacy-only", 10, 0))
	result, err := Read(context.Background(), root, 1000)
	records := flatten(result)
	if err != nil || result.HasError || result.IsMissing || len(records) != 2 || records[0].Model != "primary" {
		t.Fatal(result, err)
	}
	addMessage(t, primary, "new", "new-wal", 20)
	result, err = Read(context.Background(), root, 1000)
	records = flatten(result)
	if err != nil || len(records) != 3 {
		t.Fatal("uncheckpointed WAL data missing", result, err)
	}
	if _, err := primary.Exec("UPDATE message SET data=? WHERE id='same'", message("updated", 111, 0)); err != nil {
		t.Fatal(err)
	}
	result, err = Read(context.Background(), root, 1000)
	if err != nil || flatten(result)[0].Model != "updated" {
		t.Fatal("row update ignored", result, err)
	}
	if _, err := primary.Exec("SELECT COUNT(*) FROM message"); err != nil {
		t.Fatal("provider DB changed or locked", err)
	}
}

func TestCrossTableSnapshotSurvivesConcurrentWriter(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "opencode.db")
	writer := fixtureDatabase(t, path)
	addMessage(t, writer, "first", "first", 1)
	if _, err := writer.Exec("INSERT INTO session_message VALUES(?,?,?,?,?)", "second", "session", message("before", 2, 0), 1000, "assistant"); err != nil {
		t.Fatal(err)
	}
	records := []domain.Record{}
	didUpdate := false
	err := readDatabase(context.Background(), path, 1000, func(record *domain.Record) {
		if record == nil {
			return
		}
		records = append(records, *record)
		if !didUpdate {
			didUpdate = true
			if _, err := writer.Exec("UPDATE session_message SET data=?", message("after", 999, 0)); err != nil {
				t.Fatal(err)
			}
		}
	})
	if err != nil || len(records) != 2 || records[1].Model != "before" {
		t.Fatal("mixed SQLite snapshots", records, err)
	}
	result, err := Read(context.Background(), root, 1000)
	if err != nil || flatten(result)[1].Model != "after" {
		t.Fatal("next snapshot failed to advance", result, err)
	}
}

func TestLegacyFallbackOldSchemaCorruptionAndMultipleRoots(t *testing.T) {
	root := t.TempDir()
	database, err := sql.Open("sqlite", filepath.Join(root, "opencode.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec("CREATE TABLE message(id TEXT,session_id TEXT,data TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("INSERT INTO message VALUES(?,?,?)", "outside", "session", `{"modelID":"outside","time":{"created":999},"tokens":{"input":1}}`); err != nil {
		t.Fatal(err)
	}
	writeLegacy(t, root, "outside", message("fallback", 100, 0))
	if err := os.WriteFile(filepath.Join(root, "opencode-corrupt.db"), []byte("not sqlite"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := Read(context.Background(), root, 1000)
	records := flatten(result)
	if err != nil || !result.HasError || result.IsMissing || len(records) != 1 || records[0].Model != "fallback" {
		t.Fatal(result, err)
	}
	other := t.TempDir()
	writeLegacy(t, other, "outside", message("copy", 100, 0))
	copied, err := Read(context.Background(), other, 1000)
	if err != nil {
		t.Fatal(err)
	}
	combined := append(records, flatten(copied)...)
	merged, err := accounting.Aggregate(combined, accounting.AggregateOptions{Window: accounting.Window{SinceDay: "1970-01-01", UntilDay: "1970-01-01", TimeZone: "UTC"}})
	if err != nil || merged.DuplicatesDropped != 1 || merged.Buckets[0].Records != 1 || merged.DistinctSessions != 1 {
		t.Fatal(merged, err)
	}
	t.Setenv("OPENCODE_DATA_DIR", root+","+other+","+root)
	roots, err := DiscoverRoots(t.TempDir())
	if err != nil || len(roots) != 2 {
		t.Fatal(roots, err)
	}
}

func TestReadOnlyMissingAndUnsupportedSchema(t *testing.T) {
	root := t.TempDir()
	database := fixtureDatabase(t, filepath.Join(root, "opencode.db"))
	addMessage(t, database, "first", "fixture", 1)
	path := filepath.Join(root, "opencode.db")
	beforeDatabase, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	beforeWal, err := os.ReadFile(path + "-wal")
	if err != nil {
		t.Fatal(err)
	}
	if err := readDatabase(context.Background(), filepath.Join(root, "opencode.db"), 0, func(*domain.Record) {}); err != nil {
		t.Fatal(err)
	}
	afterDatabase, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	afterWal, err := os.ReadFile(path + "-wal")
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(beforeDatabase) != sha256.Sum256(afterDatabase) || sha256.Sum256(beforeWal) != sha256.Sum256(afterWal) {
		t.Fatal("reading changed provider DB/WAL contents")
	}
	missing, err := Read(context.Background(), filepath.Join(root, "missing"), 0)
	if err != nil || !missing.IsMissing || missing.HasError {
		t.Fatal(missing, err)
	}
	unsupportedRoot := t.TempDir()
	unsupported, err := sql.Open("sqlite", filepath.Join(unsupportedRoot, "opencode.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := unsupported.Exec("CREATE TABLE unrelated(id TEXT)"); err != nil {
		t.Fatal(err)
	}
	unsupported.Close()
	result, err := Read(context.Background(), unsupportedRoot, 0)
	if err != nil || !result.HasError || result.IsMissing {
		t.Fatal("unsupported schema hidden", result, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Read(ctx, root, 0); err == nil {
		t.Fatal("cancelled reader continued")
	}
}
