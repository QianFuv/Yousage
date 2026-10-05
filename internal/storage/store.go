// Package storage owns the app SQLite database and atomic collector checkpoints.
package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	_ "modernc.org/sqlite"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"yousage/internal/domain"
)

var ErrConflict = errors.New("saved state changed; reload before applying")

type Store struct{ database *sql.DB }

// Open configures every pooled connection and refuses unknown future schema versions.
func Open(path string) (*Store, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("database path must be absolute")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	uriPath := filepath.ToSlash(path)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	uri := url.URL{Scheme: "file", Path: uriPath}
	query := url.Values{"_pragma": {"foreign_keys(1)", "busy_timeout(5000)", "journal_mode(WAL)"}, "_txlock": {"immediate"}}
	uri.RawQuery = query.Encode()
	database, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, err
	}
	database.SetMaxOpenConns(4)
	store := &Store{database: database}
	if err := store.migrate(context.Background()); err != nil {
		database.Close()
		return nil, err
	}
	return store, nil
}
func (store *Store) Close() error { return store.database.Close() }

func (store *Store) migrate(ctx context.Context) error {
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	var version int
	if err := transaction.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > 1 {
		return fmt.Errorf("unsupported database schema %d", version)
	}
	if version == 0 {
		statements := []string{
			`CREATE TABLE sources (id TEXT PRIMARY KEY, fingerprint TEXT NOT NULL, observation TEXT)`,
			`CREATE TABLE checkpoints (source_id TEXT NOT NULL REFERENCES sources(id), path TEXT NOT NULL, revision INTEGER NOT NULL, data TEXT NOT NULL, PRIMARY KEY(source_id,path))`,
			`CREATE TABLE records (source_id TEXT NOT NULL REFERENCES sources(id), path TEXT NOT NULL, ordinal INTEGER NOT NULL, is_tail INTEGER NOT NULL CHECK(is_tail IN (0,1)), timestamp_ms INTEGER NOT NULL, data TEXT NOT NULL, PRIMARY KEY(source_id,path,is_tail,ordinal))`,
			`CREATE INDEX records_time ON records(timestamp_ms)`,
			`CREATE TABLE settings (key TEXT PRIMARY KEY, revision INTEGER NOT NULL, data TEXT NOT NULL)`,
			`PRAGMA user_version=1`,
		}
		for _, statement := range statements {
			if _, err := transaction.ExecContext(ctx, statement); err != nil {
				return err
			}
		}
	}
	return transaction.Commit()
}

func (store *Store) RegisterSource(ctx context.Context, source domain.SourceFingerprint) (string, error) {
	if !source.Provider.Valid() || source.HostId == "" || source.ResolvedHomePath == "" {
		return "", errors.New("invalid source identity")
	}
	encoded, err := json.Marshal(source)
	if err != nil {
		return "", err
	}
	id := source.Id()
	_, err = store.database.ExecContext(ctx, "INSERT INTO sources(id,fingerprint) VALUES(?,?) ON CONFLICT(id) DO NOTHING", id, string(encoded))
	return id, err
}

func (store *Store) Checkpoint(ctx context.Context, sourceId, path string) (domain.Checkpoint, error) {
	checkpoint := domain.Checkpoint{SourceId: sourceId, Path: path, Reducer: json.RawMessage(`{}`)}
	var encoded string
	err := store.database.QueryRowContext(ctx, "SELECT data FROM checkpoints WHERE source_id=? AND path=?", sourceId, path).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return checkpoint, nil
	}
	if err != nil {
		return checkpoint, err
	}
	err = json.Unmarshal([]byte(encoded), &checkpoint)
	return checkpoint, err
}

// CommitFile commits normalized facts and completed progress together; replacing handles rewrites.
func (store *Store) CommitFile(ctx context.Context, checkpoint domain.Checkpoint, records []domain.Record, shouldReplace bool) (domain.Checkpoint, error) {
	return store.CommitFileWithTail(ctx, checkpoint, records, nil, shouldReplace)
}

// CommitFileWithTail replaces replayable EOF facts without advancing the committed reducer past LF.
func (store *Store) CommitFileWithTail(ctx context.Context, checkpoint domain.Checkpoint, records, tail []domain.Record, shouldReplace bool) (domain.Checkpoint, error) {
	if checkpoint.SourceId == "" || checkpoint.Path == "" || checkpoint.Offset < 0 || checkpoint.Offset > checkpoint.FileSize || checkpoint.GuardLength < 0 || checkpoint.GuardLength > 64 || checkpoint.ParserVersion < 0 || checkpoint.Revision < 0 || !json.Valid(checkpoint.Reducer) {
		return checkpoint, errors.New("invalid checkpoint")
	}
	for _, record := range records {
		if err := record.Validate(); err != nil {
			return checkpoint, err
		}
	}
	for _, record := range tail {
		if err := record.Validate(); err != nil {
			return checkpoint, err
		}
	}
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return checkpoint, err
	}
	defer transaction.Rollback()
	var revision, count int64
	err = transaction.QueryRowContext(ctx, "SELECT revision FROM checkpoints WHERE source_id=? AND path=?", checkpoint.SourceId, checkpoint.Path).Scan(&revision)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return checkpoint, err
	}
	if revision != checkpoint.Revision {
		return checkpoint, ErrConflict
	}
	if shouldReplace {
		if _, err := transaction.ExecContext(ctx, "DELETE FROM records WHERE source_id=? AND path=?", checkpoint.SourceId, checkpoint.Path); err != nil {
			return checkpoint, err
		}
	} else {
		if err := transaction.QueryRowContext(ctx, "SELECT COALESCE(MAX(ordinal)+1,0) FROM records WHERE source_id=? AND path=? AND is_tail=0", checkpoint.SourceId, checkpoint.Path).Scan(&count); err != nil {
			return checkpoint, err
		}
	}
	if _, err := transaction.ExecContext(ctx, "DELETE FROM records WHERE source_id=? AND path=? AND is_tail=1", checkpoint.SourceId, checkpoint.Path); err != nil {
		return checkpoint, err
	}
	for _, record := range records {
		encoded, err := json.Marshal(record)
		if err != nil {
			return checkpoint, err
		}
		if _, err := transaction.ExecContext(ctx, "INSERT INTO records(source_id,path,ordinal,is_tail,timestamp_ms,data) VALUES(?,?,?,0,?,?)", checkpoint.SourceId, checkpoint.Path, count, record.TimestampMs, string(encoded)); err != nil {
			return checkpoint, err
		}
		count++
	}
	for ordinal, record := range tail {
		encoded, err := json.Marshal(record)
		if err != nil {
			return checkpoint, err
		}
		if _, err := transaction.ExecContext(ctx, "INSERT INTO records(source_id,path,ordinal,is_tail,timestamp_ms,data) VALUES(?,?,?,1,?,?)", checkpoint.SourceId, checkpoint.Path, ordinal, record.TimestampMs, string(encoded)); err != nil {
			return checkpoint, err
		}
	}
	checkpoint.Revision = revision + 1
	checkpoint.RecordCount = count
	encoded, err := json.Marshal(checkpoint)
	if err != nil {
		return checkpoint, err
	}
	_, err = transaction.ExecContext(ctx, "INSERT INTO checkpoints(source_id,path,revision,data) VALUES(?,?,?,?) ON CONFLICT(source_id,path) DO UPDATE SET revision=excluded.revision,data=excluded.data", checkpoint.SourceId, checkpoint.Path, checkpoint.Revision, string(encoded))
	if err != nil {
		return checkpoint, err
	}
	if err := transaction.Commit(); err != nil {
		return checkpoint, err
	}
	return checkpoint, nil
}

func (store *Store) Records(ctx context.Context, sourceId string) ([]domain.Record, error) {
	rows, err := store.database.QueryContext(ctx, "SELECT data FROM records WHERE source_id=? ORDER BY path,is_tail,ordinal", sourceId)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := []domain.Record{}
	for rows.Next() {
		var encoded string
		if err := rows.Scan(&encoded); err != nil {
			return nil, err
		}
		var record domain.Record
		if err := json.Unmarshal([]byte(encoded), &record); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

// Observe stores attempt status without removing cached facts or their last success.
func (store *Store) Observe(ctx context.Context, observation domain.Observation) error {
	switch observation.Status {
	case "ok", "missing", "partial", "failed":
	default:
		return errors.New("invalid source status")
	}
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	var previous sql.NullString
	if err := transaction.QueryRowContext(ctx, "SELECT observation FROM sources WHERE id=?", observation.SourceId).Scan(&previous); err != nil {
		return err
	}
	if observation.SucceededAt == nil && previous.Valid {
		var old domain.Observation
		if err := json.Unmarshal([]byte(previous.String), &old); err != nil {
			return err
		}
		observation.SucceededAt = old.SucceededAt
	}
	encoded, err := json.Marshal(observation)
	if err != nil {
		return err
	}
	if _, err := transaction.ExecContext(ctx, "UPDATE sources SET observation=? WHERE id=?", string(encoded), observation.SourceId); err != nil {
		return err
	}
	return transaction.Commit()
}
func (store *Store) Observation(ctx context.Context, sourceId string) (domain.Observation, error) {
	var encoded sql.NullString
	if err := store.database.QueryRowContext(ctx, "SELECT observation FROM sources WHERE id=?", sourceId).Scan(&encoded); err != nil {
		return domain.Observation{}, err
	}
	observation := domain.Observation{SourceId: sourceId}
	if encoded.Valid {
		if err := json.Unmarshal([]byte(encoded.String), &observation); err != nil {
			return observation, err
		}
	}
	return observation, nil
}

// ReadSetting returns revision zero when absent; callers own typed validation and defaults.
func (store *Store) ReadSetting(ctx context.Context, key string, destination any) (int64, error) {
	var revision int64
	var encoded string
	err := store.database.QueryRowContext(ctx, "SELECT revision,data FROM settings WHERE key=?", key).Scan(&revision, &encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return revision, json.Unmarshal([]byte(encoded), destination)
}
func (store *Store) WriteSetting(ctx context.Context, key string, revision int64, value any) (int64, error) {
	if strings.TrimSpace(key) == "" {
		return revision, errors.New("empty setting key")
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return revision, err
	}
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return revision, err
	}
	defer transaction.Rollback()
	var current int64
	err = transaction.QueryRowContext(ctx, "SELECT revision FROM settings WHERE key=?", key).Scan(&current)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return revision, err
	}
	if current != revision {
		return revision, ErrConflict
	}
	_, err = transaction.ExecContext(ctx, "INSERT INTO settings(key,revision,data) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET revision=excluded.revision,data=excluded.data", key, current+1, string(encoded))
	if err != nil {
		return revision, err
	}
	if err := transaction.Commit(); err != nil {
		return revision, err
	}
	return current + 1, nil
}
