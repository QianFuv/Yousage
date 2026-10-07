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
	"sync"
	"yousage/internal/domain"
)

var ErrConflict = errors.New("saved state changed; reload before applying")

// Store owns transactional facts and stable decoding identities.
type Store struct {
	database          *sql.DB
	stringValues      sync.Map
	latestMutex       sync.Mutex
	latestQuotas      map[string]latestQuotaCache
	historyMutex      sync.Mutex
	historyConnection *sql.Conn
	historyVersion    int64
	historyKeys       map[string]string
}

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
	query := url.Values{"_pragma": {"foreign_keys(1)", "busy_timeout(5000)", "journal_mode(WAL)", "cache_size(-16384)"}, "_txlock": {"immediate"}}
	uri.RawQuery = query.Encode()
	database, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, err
	}
	database.SetMaxOpenConns(5)
	store := &Store{database: database}
	if err := store.migrate(context.Background()); err != nil {
		database.Close()
		return nil, err
	}
	if err := store.reclaimMigrationSpace(context.Background()); err != nil {
		database.Close()
		return nil, err
	}
	if err := store.loadStrings(context.Background()); err != nil {
		database.Close()
		return nil, err
	}
	store.historyConnection, err = database.Conn(context.Background())
	if err != nil {
		database.Close()
		return nil, err
	}
	return store, nil
}

// Close releases the revision observer and pooled database connections.
func (store *Store) Close() error {
	store.historyMutex.Lock()
	defer store.historyMutex.Unlock()
	if store.historyConnection != nil {
		store.historyConnection.Close()
	}
	return store.database.Close()
}

// migrate atomically upgrades supported schemas and preserves legacy state on failure.
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
	if version > 2 {
		return fmt.Errorf("unsupported database schema %d", version)
	}
	if version == 0 {
		for _, statement := range []string{
			`CREATE TABLE sources (id TEXT PRIMARY KEY, fingerprint TEXT NOT NULL, observation TEXT)`,
			`CREATE TABLE settings (key TEXT PRIMARY KEY, revision INTEGER NOT NULL, data TEXT NOT NULL)`,
		} {
			if _, err := transaction.ExecContext(ctx, statement); err != nil {
				return err
			}
		}
		for _, statement := range schemaV2 {
			if _, err := transaction.ExecContext(ctx, statement); err != nil {
				return err
			}
		}
		if _, err := transaction.ExecContext(ctx, "PRAGMA user_version=2"); err != nil {
			return err
		}
	} else if version == 1 {
		if err := migrateV1(ctx, transaction); err != nil {
			return err
		}
	}
	return transaction.Commit()
}

// loadStrings caches stable decoding identities for this database handle.
func (store *Store) loadStrings(ctx context.Context) error {
	return store.loadStringsFrom(ctx, store.database.QueryContext)
}

// loadStringsFrom resolves dictionary misses within the record reader's WAL snapshot.
func (store *Store) loadStringsFrom(ctx context.Context, query func(context.Context, string, ...any) (*sql.Rows, error)) error {
	rows, err := query(ctx, "SELECT id,value FROM strings")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var identity int64
		var value string
		if err := rows.Scan(&identity, &value); err != nil {
			return err
		}
		store.stringValues.Store(identity, value)
	}
	return rows.Err()
}

// lookupString resolves stable identities, including facts held by earlier WAL readers.
func (store *Store) lookupString(identity int64) (string, bool) {
	value, exists := store.stringValues.Load(identity)
	if !exists {
		return "", false
	}
	return value.(string), true
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

// Checkpoint restores completed progress for one physical source file.
func (store *Store) Checkpoint(ctx context.Context, sourceId, path string) (domain.Checkpoint, error) {
	checkpoint := domain.Checkpoint{SourceId: sourceId, Path: path, Reducer: json.RawMessage(`{}`)}
	var encoded string
	err := store.database.QueryRowContext(ctx, "SELECT c.data,c.revision FROM checkpoints c JOIN files f ON f.id=c.file_id WHERE f.source_id=? AND f.path=?", sourceId, path).Scan(&encoded, &checkpoint.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		return checkpoint, nil
	}
	if err != nil {
		return checkpoint, err
	}
	revision := checkpoint.Revision
	err = json.Unmarshal([]byte(encoded), &checkpoint)
	checkpoint.SourceId, checkpoint.Path, checkpoint.Revision = sourceId, path, revision
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
	var identity int64
	if _, err := transaction.ExecContext(ctx, `INSERT INTO files(source_id,path) VALUES(?,?) ON CONFLICT(source_id,path) DO NOTHING`, checkpoint.SourceId, checkpoint.Path); err != nil {
		return checkpoint, err
	}
	if err := transaction.QueryRowContext(ctx, `SELECT id FROM files WHERE source_id=? AND path=?`, checkpoint.SourceId, checkpoint.Path).Scan(&identity); err != nil {
		return checkpoint, err
	}
	var revision, count, sequence int64
	err = transaction.QueryRowContext(ctx, "SELECT revision FROM checkpoints WHERE file_id=?", identity).Scan(&revision)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return checkpoint, err
	}
	if revision != checkpoint.Revision {
		return checkpoint, ErrConflict
	}
	changedAt := int64(1<<63 - 1)
	for _, batch := range [][]domain.Record{records, tail} {
		for _, record := range batch {
			changedAt = min(changedAt, record.TimestampMs)
		}
	}
	var previousTime sql.NullInt64
	query := "SELECT MIN(timestamp_ms) FROM records WHERE file_id=?"
	if !shouldReplace {
		query += " AND is_tail=1"
	}
	if err := transaction.QueryRowContext(ctx, query, identity).Scan(&previousTime); err != nil {
		return checkpoint, err
	}
	if previousTime.Valid {
		changedAt = min(changedAt, previousTime.Int64)
	}
	if err := transaction.QueryRowContext(ctx, `SELECT COALESCE((SELECT value FROM storage_meta WHERE key='sequence'),0)`).Scan(&sequence); err != nil {
		return checkpoint, err
	}
	interner, err := newStringInterner(ctx, transaction)
	if err != nil {
		return checkpoint, err
	}
	defer interner.close()
	writer, err := newRecordWriter(ctx, transaction, interner)
	if err != nil {
		return checkpoint, err
	}
	defer writer.close()
	if shouldReplace {
		if err := releaseRecordStrings(ctx, transaction, interner, `r.file_id=?`, identity); err != nil {
			return checkpoint, err
		}
		if _, err := transaction.ExecContext(ctx, "DELETE FROM records WHERE file_id=?", identity); err != nil {
			return checkpoint, err
		}
	} else {
		if err := transaction.QueryRowContext(ctx, "SELECT COALESCE(MAX(ordinal)+1,0) FROM records WHERE file_id=? AND is_tail=0", identity).Scan(&count); err != nil {
			return checkpoint, err
		}
		if err := releaseRecordStrings(ctx, transaction, interner, `r.file_id=? AND r.is_tail=1`, identity); err != nil {
			return checkpoint, err
		}
		if _, err := transaction.ExecContext(ctx, "DELETE FROM records WHERE file_id=? AND is_tail=1", identity); err != nil {
			return checkpoint, err
		}
	}
	for _, record := range records {
		sequence++
		if err := writer.insert(ctx, identity, 0, count, sequence, record); err != nil {
			return checkpoint, err
		}
		count++
	}
	for ordinal, record := range tail {
		sequence++
		if err := writer.insert(ctx, identity, 1, int64(ordinal), sequence, record); err != nil {
			return checkpoint, err
		}
	}
	if _, err := transaction.ExecContext(ctx, `INSERT INTO storage_meta(key,value) VALUES('sequence',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, sequence); err != nil {
		return checkpoint, err
	}
	checkpoint.Revision = revision + 1
	checkpoint.RecordCount = count
	if err := saveCheckpoint(ctx, transaction, identity, checkpoint.Revision, checkpoint); err != nil {
		return checkpoint, err
	}

	if changedAt < 1<<63-1 {
		if err := recordCycleChange(ctx, transaction, checkpoint.SourceId, changedAt); err != nil {
			return checkpoint, err
		}
	}
	if err := interner.flush(ctx, transaction); err != nil {
		return checkpoint, err
	}
	if err := transaction.Commit(); err != nil {
		return checkpoint, err
	}
	for value, identity := range interner.values {
		store.stringValues.Store(identity, value)
	}

	return checkpoint, nil
}

// Records reads all original events in file and replay order.
func (store *Store) Records(ctx context.Context, sourceId string) ([]domain.Record, error) {
	return store.readRecords(ctx, recordSelect+` WHERE f.source_id=? ORDER BY f.path,r.is_tail,r.ordinal`, sourceId)
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

// SettingRevision identifies persisted changes without decoding the setting.
func (store *Store) SettingRevision(ctx context.Context, key string) (int64, error) {
	var revision int64
	err := store.database.QueryRowContext(ctx, "SELECT revision FROM settings WHERE key=?", key).Scan(&revision)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return revision, err
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
