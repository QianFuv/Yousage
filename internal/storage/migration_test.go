package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
	"yousage/internal/accounting"
	"yousage/internal/domain"
)

// legacyFixture creates the actual v1 layout with interleaved quota facts and tails.
func legacyFixture(test *testing.T) (string, map[string]map[string][]domain.Record, map[string][]domain.Checkpoint) {
	test.Helper()
	path := filepath.Join(test.TempDir(), "legacy.sqlite")
	database, err := sql.Open("sqlite", path)
	if err != nil {
		test.Fatal(err)
	}
	defer database.Close()
	for _, statement := range []string{
		`PRAGMA journal_mode=WAL`,
		`CREATE TABLE sources (id TEXT PRIMARY KEY, fingerprint TEXT NOT NULL, observation TEXT)`,
		`CREATE TABLE checkpoints (source_id TEXT NOT NULL REFERENCES sources(id), path TEXT NOT NULL, revision INTEGER NOT NULL, data TEXT NOT NULL, PRIMARY KEY(source_id,path))`,
		`CREATE TABLE records (source_id TEXT NOT NULL REFERENCES sources(id), path TEXT NOT NULL, ordinal INTEGER NOT NULL, is_tail INTEGER NOT NULL, timestamp_ms INTEGER NOT NULL, data TEXT NOT NULL, PRIMARY KEY(source_id,path,is_tail,ordinal))`,
		`CREATE INDEX records_time ON records(timestamp_ms)`,
		`CREATE TABLE settings (key TEXT PRIMARY KEY, revision INTEGER NOT NULL, data TEXT NOT NULL)`,
		`INSERT INTO settings VALUES('theme-library',3,'{"theme":"ocean"}')`,
		`PRAGMA user_version=1`,
	} {
		if _, err := database.Exec(statement); err != nil {
			test.Fatal(err)
		}
	}
	files := map[string]map[string][]domain.Record{}
	checkpoints := map[string][]domain.Checkpoint{}
	for _, source := range []string{"first", "second"} {
		if _, err := database.Exec(`INSERT INTO sources VALUES(?,'{}','{"status":"ok","attemptedAt":100,"succeededAt":90}')`, source); err != nil {
			test.Fatal(err)
		}
		files[source] = map[string][]domain.Record{}
		for _, name := range []string{"a", "b"} {
			checkpoint := domain.Checkpoint{SourceId: source, Path: name, Revision: 7, Offset: 80, FileSize: 100, ModifiedNs: 200, Guard: "guard", GuardLength: 5, ParserVersion: 2, RecordCount: 3, Reducer: json.RawMessage(`{"model":"gpt-5.4"}`)}
			if name == "b" {
				checkpoint.ModifiedNs = 99
			}
			data, _ := json.Marshal(checkpoint)
			if _, err := database.Exec(`INSERT INTO checkpoints VALUES(?,?,?,?)`, source, name, checkpoint.Revision, string(data)); err != nil {
				test.Fatal(err)
			}
			checkpoints[source] = append(checkpoints[source], checkpoint)
			for index := range 4 {
				record := usageRecord("gpt-5.4")
				record.TimestampMs, record.SessionId, record.SourcePath = 1000, source+name, "/source"
				record.RateModel, record.DedupeKey, record.QuotaBucket = "rate", name, "codex"
				if index != 0 {
					record.Quota = &domain.QuotaObservation{PlanType: "plus", Windows: []domain.QuotaWindow{{Id: "primary", UsedPercent: float64(index) * 12.5, WindowMinutes: 300, ResetsAt: 19000}, {Id: "secondary", WindowMinutes: 10080, ResetsAt: 20000}}}
				}
				if index == 2 || index == 3 {
					record.Totals, record.IsQuotaOnly = domain.Tokens{}, true
				} else if index == 1 {
					zero := 0.0
					record.ReportedCostUsd = &zero
				}
				tail, ordinal := 0, index
				if index == 3 {
					tail, ordinal = 1, 0
				}
				data, _ := json.Marshal(record)
				if _, err := database.Exec(`INSERT INTO records VALUES(?,?,?,?,?,?)`, source, name, ordinal, tail, record.TimestampMs, string(data)); err != nil {
					test.Fatal(err)
				}
				files[source][name] = append(files[source][name], record)
			}
		}
	}
	return path, files, checkpoints
}

// TestMigrationPreservesFactsAndAPIs verifies conversion without reparsing or cache invalidation.
func TestMigrationPreservesFactsAndAPIs(test *testing.T) {
	path, expected, checkpoints := legacyFixture(test)
	store, err := Open(path)
	if err != nil {
		test.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	var version, observations int
	if err := store.database.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != 2 {
		test.Fatal("schema version", version, err)
	}
	if err := store.database.QueryRow(`SELECT COUNT(*) FROM quotas`).Scan(&observations); err != nil || observations != 12 {
		test.Fatal("quota observations lost", observations, err)
	}
	for source, files := range expected {
		actual, err := store.FileRecords(ctx, source)
		if err != nil || !reflect.DeepEqual(files, actual) {
			test.Fatal("migrated record fields differ", err)
		}
		actualCheckpoints, err := store.Checkpoints(ctx, source)
		if err != nil || !reflect.DeepEqual(checkpoints[source], actualCheckpoints) {
			test.Fatal("checkpoint or parser version changed", err)
		}
		key, err := store.HistoryCacheKey(ctx, source)
		if err != nil || key != "1:a:7;1:b:7;" {
			test.Fatal("cache key changed", err)
		}
		latest, err := store.LatestCodexQuota(ctx, source, 1000)
		if err != nil || !reflect.DeepEqual(latest, &files["b"][3]) {
			test.Fatal("equal timestamp observation ordering changed", err)
		}
		eligible, err := store.FileRecordsSince(ctx, source, 100)
		if err != nil || !reflect.DeepEqual(eligible, map[string][]domain.Record{"a": files["a"]}) {
			test.Fatal("file mtime selection changed", err)
		}
		usage, err := store.FileUsageRecordsSince(ctx, source, 100)
		if err != nil || !reflect.DeepEqual(usage["a"], files["a"][:2]) {
			test.Fatal("quota-only usage filtering changed", err)
		}
		window, err := accounting.BuildWindow(time.UnixMilli(2000), 90, "UTC")
		if err != nil {
			test.Fatal(err)
		}
		before, err := accounting.Aggregate(files["a"], accounting.AggregateOptions{Window: window})
		if err != nil {
			test.Fatal(err)
		}
		after, err := accounting.Aggregate(eligible["a"], accounting.AggregateOptions{Window: window})
		if err != nil || !reflect.DeepEqual(before, after) {
			test.Fatal("accounting changed", err)
		}
		checkpoint := checkpoints[source][0]
		checkpoint, err = store.CommitFile(ctx, checkpoint, files["a"][2:3], false)
		if err != nil || checkpoint.RecordCount != 4 {
			test.Fatal("append did not count quota-only events", err)
		}
		committed, err := store.CommittedFileRecords(ctx, source, "a")
		if err != nil || !reflect.DeepEqual(committed, append(append([]domain.Record{}, files["a"][:3]...), files["a"][2])) {
			test.Fatal("append or tail replacement changed facts", err)
		}
		if err := store.PruneHistory(ctx, source, 100); err != nil {
			test.Fatal(err)
		}
		remaining, err := store.FileRecords(ctx, source)
		if err != nil || len(remaining) != 1 || len(remaining["a"]) != 4 {
			test.Fatal("mtime retention or quota cascade failed", err)
		}
	}
	var setting map[string]string
	if revision, err := store.ReadSetting(ctx, "theme-library", &setting); err != nil || revision != 3 || setting["theme"] != "ocean" {
		test.Fatal("settings changed", err)
	}
	if observation, err := store.Observation(ctx, "first"); err != nil || observation.SucceededAt == nil || *observation.SucceededAt != 90 {
		test.Fatal("observation changed", err)
	}
}

// TestFailedMigrationLeavesV1Intact injects failure after prior rows have been converted.
func TestFailedMigrationLeavesV1Intact(test *testing.T) {
	path, _, _ := legacyFixture(test)
	database, err := sql.Open("sqlite", path)
	if err != nil {
		test.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`UPDATE records SET data='invalid' WHERE rowid=(SELECT MAX(rowid) FROM records)`); err != nil {
		test.Fatal(err)
	}
	expected := legacyDigest(test, database)
	if store, err := Open(path); err == nil {
		store.Close()
		test.Fatal("injected migration failure was not reached")
	}
	assertLegacyIntact(test, database)
	if legacyDigest(test, database) != expected {
		test.Fatal("failed migration changed legacy contents")
	}
}

// assertLegacyIntact verifies rollback of DDL, rows, checkpoints and indexes.
func assertLegacyIntact(test *testing.T, database *sql.DB) {
	test.Helper()
	var version, records, checkpoints, newTables int
	if err := database.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != 1 {
		test.Fatal("legacy schema lost", err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM records INDEXED BY records_time`).Scan(&records); err != nil || records != 16 {
		test.Fatal("legacy records or index lost", err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM checkpoints WHERE revision=7 AND json_extract(data,'$.parserVersion')=2`).Scan(&checkpoints); err != nil || checkpoints != 4 {
		test.Fatal("legacy checkpoints changed", err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name IN ('files','quotas','strings','records_v1','checkpoints_v1','storage_meta')`).Scan(&newTables); err != nil || newTables != 0 {
		test.Fatal("partial schema published", err)
	}
}

// TestInterruptedMigrationLeavesV1Intact kills a process after conversion before commit.
func TestInterruptedMigrationLeavesV1Intact(test *testing.T) {
	path, _, _ := legacyFixture(test)
	database, err := sql.Open("sqlite", path)
	if err != nil {
		test.Fatal(err)
	}
	expected := legacyDigest(test, database)
	database.Close()
	marker := filepath.Join(test.TempDir(), "converted")
	command := exec.Command(os.Args[0], "-test.run=^TestMigrationCrashHelper$")
	command.Env = append(os.Environ(), "YOUSAGE_MIGRATION_CRASH_DB="+path, "YOUSAGE_MIGRATION_CRASH_MARKER="+marker)
	if err := command.Start(); err != nil {
		test.Fatal(err)
	}
	defer command.Process.Kill()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			test.Fatal("migration child did not reach uncommitted conversion")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := command.Process.Kill(); err != nil {
		test.Fatal(err)
	}
	_ = command.Wait()
	database, err = sql.Open("sqlite", path)
	if err != nil {
		test.Fatal(err)
	}
	defer database.Close()
	assertLegacyIntact(test, database)
	if legacyDigest(test, database) != expected {
		test.Fatal("interrupted migration changed legacy contents")
	}
}

// TestMigrationCrashHelper leaves a completed conversion transaction uncommitted.
func TestMigrationCrashHelper(test *testing.T) {
	path := os.Getenv("YOUSAGE_MIGRATION_CRASH_DB")
	if path == "" {
		return
	}
	database, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
	if err != nil {
		test.Fatal(err)
	}
	transaction, err := database.Begin()
	if err != nil {
		test.Fatal(err)
	}
	if err := migrateV1(context.Background(), transaction); err != nil {
		test.Fatal(err)
	}
	if err := os.WriteFile(os.Getenv("YOUSAGE_MIGRATION_CRASH_MARKER"), []byte("ready"), 0600); err != nil {
		test.Fatal(err)
	}
	time.Sleep(time.Minute)
	test.Fatal("crash helper was not terminated")
}

// legacyDigest fingerprints logical v1 contents without exposing any record fields.
func legacyDigest(test *testing.T, database *sql.DB) [32]byte {
	test.Helper()
	digest := sha256.New()
	for _, query := range []string{
		`SELECT rowid,* FROM records ORDER BY rowid`,
		`SELECT * FROM checkpoints ORDER BY source_id,path`,
		`SELECT * FROM sources ORDER BY id`,
		`SELECT * FROM settings ORDER BY key`,
		`SELECT type,name,sql FROM sqlite_master ORDER BY name`,
	} {
		rows, err := database.Query(query)
		if err != nil {
			test.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			test.Fatal(err)
		}
		for rows.Next() {
			values := make([]any, len(columns))
			destinations := make([]any, len(columns))
			for index := range values {
				destinations[index] = &values[index]
			}
			if err := rows.Scan(destinations...); err != nil {
				test.Fatal(err)
			}
			for _, value := range values {
				fmt.Fprintf(digest, "%T:%v;", value, value)
			}
		}
		if err := rows.Err(); err != nil {
			test.Fatal(err)
		}
		rows.Close()
	}
	var result [32]byte
	copy(result[:], digest.Sum(nil))
	return result
}
