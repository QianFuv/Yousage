package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
	"yousage/internal/accounting"
	"yousage/internal/domain"
	"yousage/internal/preferences"
	"yousage/internal/refresh"
	"yousage/internal/storage"
)

// TestMigratedUsageAndCyclesMatchFacts compares legacy fixtures with their complete expected views.
func TestMigratedUsageAndCyclesMatchFacts(test *testing.T) {
	service := testService(test)
	ctx := context.Background()
	home := test.TempDir()
	writeCycleLog(test, home, service.Now())
	_, revision, _ := service.Sources(ctx)
	if _, err := service.SaveSources(ctx, []preferences.Source{{Id: "codex", Provider: domain.Codex, Root: home, IsEnabled: true}}, revision); err != nil {
		test.Fatal(err)
	}
	saved, _ := service.Configuration(ctx)
	saved.Value.Providers = []NativeProvider{{Id: "codex", Driver: "codex", Home: home, IsEnabled: true}}
	if _, err := service.SaveConfiguration(ctx, saved); err != nil {
		test.Fatal(err)
	}
	if _, err := service.Store.WriteSetting(ctx, "public-model-rates", 0, RateSnapshot{Rates: accounting.RateTable{"priced": accounting.ModelRate{TokenRates: accounting.TokenRates{Input: 1}}}}); err != nil {
		test.Fatal(err)
	}
	if err := service.RefreshHistory(ctx, refresh.Request{IsManual: true}); err != nil {
		test.Fatal(err)
	}
	expected := map[int]Snapshot{}
	for _, days := range []int{1, 7, 30, 90} {
		value, err := service.Usage(ctx, days, "UTC")
		if err != nil {
			test.Fatal(err)
		}
		expected[days] = value
	}
	path := filepath.Join(test.TempDir(), "legacy.sqlite")
	database, err := sql.Open("sqlite", path)
	if err != nil {
		test.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE sources (id TEXT PRIMARY KEY, fingerprint TEXT NOT NULL, observation TEXT)`,
		`CREATE TABLE checkpoints (source_id TEXT NOT NULL REFERENCES sources(id), path TEXT NOT NULL, revision INTEGER NOT NULL, data TEXT NOT NULL, PRIMARY KEY(source_id,path))`,
		`CREATE TABLE records (source_id TEXT NOT NULL REFERENCES sources(id), path TEXT NOT NULL, ordinal INTEGER NOT NULL, is_tail INTEGER NOT NULL, timestamp_ms INTEGER NOT NULL, data TEXT NOT NULL, PRIMARY KEY(source_id,path,is_tail,ordinal))`,
		`CREATE INDEX records_time ON records(timestamp_ms)`,
		`CREATE TABLE settings (key TEXT PRIMARY KEY, revision INTEGER NOT NULL, data TEXT NOT NULL)`,
		`PRAGMA user_version=1`,
	} {
		if _, err := database.Exec(statement); err != nil {
			test.Fatal(err)
		}
	}
	entries, _, err := service.sourceIndex(ctx)
	if err != nil {
		test.Fatal(err)
	}
	for _, entry := range entries {
		fingerprint := entry.Source.Fingerprint
		encoded, _ := json.Marshal(fingerprint)
		observation, err := service.Store.Observation(ctx, fingerprint.Id())
		if err != nil {
			test.Fatal(err)
		}
		observed, _ := json.Marshal(observation)
		if _, err := database.Exec(`INSERT OR IGNORE INTO sources VALUES(?,?,?)`, fingerprint.Id(), string(encoded), string(observed)); err != nil {
			test.Fatal(err)
		}
		checkpoints, err := service.Store.Checkpoints(ctx, fingerprint.Id())
		if err != nil {
			test.Fatal(err)
		}
		files, err := service.Store.FileRecords(ctx, fingerprint.Id())
		if err != nil {
			test.Fatal(err)
		}
		for _, checkpoint := range checkpoints {
			encoded, _ := json.Marshal(checkpoint)
			if _, err := database.Exec(`INSERT INTO checkpoints VALUES(?,?,?,?)`, checkpoint.SourceId, checkpoint.Path, checkpoint.Revision, string(encoded)); err != nil {
				test.Fatal(err)
			}
			for index, record := range files[checkpoint.Path] {
				ordinal, tail := int64(index), 0
				if ordinal >= checkpoint.RecordCount {
					ordinal, tail = ordinal-checkpoint.RecordCount, 1
				}
				encoded, _ := json.Marshal(record)
				if _, err := database.Exec(`INSERT INTO records VALUES(?,?,?,?,?,?)`, checkpoint.SourceId, checkpoint.Path, ordinal, tail, record.TimestampMs, string(encoded)); err != nil {
					test.Fatal(err)
				}
			}
		}
	}
	for key, destination := range map[string]any{"local-environment-id": new(string), "sources": new([]preferences.Source), "configuration": new(Configuration), "history-index": new([]sourceIndex), "public-model-rates": new(RateSnapshot)} {
		revision, err := service.Store.ReadSetting(ctx, key, destination)
		if err != nil {
			test.Fatal(err)
		}
		if revision > 0 {
			encoded, _ := json.Marshal(destination)
			if _, err := database.Exec(`INSERT INTO settings VALUES(?,?,?)`, key, revision, string(encoded)); err != nil {
				test.Fatal(err)
			}
		}
	}
	if err := database.Close(); err != nil {
		test.Fatal(err)
	}
	migrated, err := storage.Open(path)
	if err != nil {
		test.Fatal(err)
	}
	defer migrated.Close()
	actualService, err := New(ctx, migrated, nil)
	if err != nil {
		test.Fatal(err)
	}
	defer actualService.Close()
	actualService.Now = service.Now
	for days, expected := range expected {
		actual, err := actualService.Usage(ctx, days, "UTC")
		if err != nil || !reflect.DeepEqual(expected, actual) {
			test.Fatalf("usage summary or cycle values changed for %d days: %v", days, err)
		}
	}
}
