package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"yousage/internal/domain"
)

// cycleChange tracks the earliest affected record for a source revision.
type cycleChange struct {
	AtMs     int64
	Revision int64
}

// recordCycleChange atomically records a compact history mutation frontier.
func recordCycleChange(ctx context.Context, transaction *sql.Tx, sourceId string, atMs int64) error {
	key := "cycle-changes:" + sourceId
	var revision int64
	var data string
	changes := []cycleChange{}
	err := transaction.QueryRowContext(ctx, "SELECT revision,data FROM settings WHERE key=?", key).Scan(&revision, &data)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		if err := json.Unmarshal([]byte(data), &changes); err != nil {
			return err
		}
	}
	for len(changes) > 0 && changes[len(changes)-1].AtMs >= atMs {
		changes = changes[:len(changes)-1]
	}
	changes = append(changes, cycleChange{AtMs: atMs, Revision: revision + 1})
	encoded, err := json.Marshal(changes)
	if err != nil {
		return err
	}
	_, err = transaction.ExecContext(ctx, "INSERT INTO settings(key,revision,data) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET revision=excluded.revision,data=excluded.data", key, revision+1, string(encoded))
	return err
}

// CompletedHistoryKey excludes mutations wholly after the completed-cycle boundary.
func (store *Store) CompletedHistoryKey(ctx context.Context, sourceId string, beforeMs int64) (int64, error) {
	changes := []cycleChange{}
	_, err := store.ReadSetting(ctx, "cycle-changes:"+sourceId, &changes)
	var revision int64
	for _, change := range changes {
		if change.AtMs < beforeMs {
			revision = change.Revision
		}
	}
	return revision, err
}

// HasRetainedHistory includes quota-only files eligible by file modification time.
func (store *Store) HasRetainedHistory(ctx context.Context, sourceId string, cutoffNs int64) (bool, error) {
	var exists bool
	err := store.database.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM files f JOIN checkpoints c ON c.file_id=f.id WHERE f.source_id=? AND c.modified_ns>=? AND EXISTS(SELECT 1 FROM records r WHERE r.file_id=f.id))`, sourceId, cutoffNs).Scan(&exists)
	return exists, err
}

const recordColumns = `r.data,r.timestamp_ms,b.value,r.quota_only,q.data`
const recordJoins = ` JOIN files f ON f.id=r.file_id JOIN strings b ON b.id=r.bucket_id LEFT JOIN quotas q ON q.file_id=r.file_id AND q.is_tail=r.is_tail AND q.ordinal=r.ordinal`
const recordSelect = `SELECT ` + recordColumns + ` FROM records r` + recordJoins
const fileRecordSelect = `SELECT f.path,` + recordColumns + ` FROM records r` + recordJoins

// scanRecord reconstructs the domain record from compact relational facts.
func (store *Store) scanRecord(ctx context.Context, connection *sql.Conn, rows *sql.Rows, path *string) (domain.Record, error) {
	var data, quota []byte
	var timestamp int64
	var bucket string
	var isQuotaOnly bool
	destinations := []any{&data, &timestamp, &bucket, &isQuotaOnly, &quota}
	if path != nil {
		destinations = append([]any{path}, destinations...)
	}
	if err := rows.Scan(destinations...); err != nil {
		return domain.Record{}, err
	}
	record, err := decodeRecord(data, timestamp, bucket, isQuotaOnly, quota, store.lookupString)
	if errors.Is(err, errUnknownString) {
		if err := store.loadStringsFrom(ctx, connection.QueryContext); err != nil {
			return record, err
		}
		return decodeRecord(data, timestamp, bucket, isQuotaOnly, quota, store.lookupString)
	}
	return record, err
}

// readRecords decodes an ordered sequence with its observation payloads.
func (store *Store) readRecords(ctx context.Context, query string, arguments ...any) ([]domain.Record, error) {
	connection, err := store.database.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer connection.Close()
	rows, err := connection.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := []domain.Record{}
	for rows.Next() {
		record, err := store.scanRecord(ctx, connection, rows, nil)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

// CommittedFileRecords excludes the replayable EOF tail.
func (store *Store) CommittedFileRecords(ctx context.Context, sourceId, path string) ([]domain.Record, error) {
	return store.readRecords(ctx, recordSelect+` WHERE f.source_id=? AND f.path=? AND r.is_tail=0 ORDER BY r.ordinal`, sourceId, path)
}

// FileRecords retains every event grouped by its physical file.
func (store *Store) FileRecords(ctx context.Context, sourceId string) (map[string][]domain.Record, error) {
	return store.fileRecords(ctx, fileRecordSelect+` WHERE f.source_id=? ORDER BY f.path,r.is_tail,r.ordinal`, sourceId)
}

// FileRecordsSince filters by file modification time before decoding facts.
func (store *Store) FileRecordsSince(ctx context.Context, sourceId string, modifiedSinceNs int64) (map[string][]domain.Record, error) {
	return store.cachedFileRecords(ctx, sourceId, modifiedSinceNs, 0, 0, false, false, false)
}

// FileUsageRecordsSince excludes quota-only events before decoding usage facts.
func (store *Store) FileUsageRecordsSince(ctx context.Context, sourceId string, modifiedSinceNs int64) (map[string][]domain.Record, error) {
	return store.cachedFileRecords(ctx, sourceId, modifiedSinceNs, 0, 0, false, true, false)
}

// FileCodexRecordsBetween preserves replay order within the requested timestamp range.
func (store *Store) FileCodexRecordsBetween(ctx context.Context, sourceId string, modifiedSinceNs, sinceMs, untilMs int64, isMainBucketOnly bool) (map[string][]domain.Record, error) {
	return store.cachedFileRecords(ctx, sourceId, modifiedSinceNs, sinceMs, untilMs, true, false, isMainBucketOnly)
}

// latestQuotaCache remains valid until facts change or a future observation becomes eligible.
type latestQuotaCache struct {
	key              string
	sinceMs, untilMs int64
	record           *domain.Record
}

// LatestCodexQuota reuses the latest observation within its unchanged eligibility interval.
func (store *Store) LatestCodexQuota(ctx context.Context, sourceId string, untilMs int64) (*domain.Record, error) {
	store.latestMutex.Lock()
	defer store.latestMutex.Unlock()
	key, err := store.HistoryCacheKey(ctx, sourceId)
	if err != nil {
		return nil, err
	}
	mutation, err := store.CompletedHistoryKey(ctx, sourceId, math.MaxInt64)
	if err != nil {
		return nil, err
	}
	key = fmt.Sprintf("%s:%d", key, mutation)
	cached, exists := store.latestQuotas[sourceId]
	if exists && cached.key == key && untilMs >= cached.sinceMs && untilMs < cached.untilMs {
		return cloneQuotaRecord(cached.record), nil
	}
	records, err := store.readRecords(ctx, `SELECT `+recordColumns+` FROM records r INDEXED BY records_time`+recordJoins+` WHERE f.source_id=? AND r.timestamp_ms<=? AND b.value='codex' AND q.data IS NOT NULL ORDER BY r.timestamp_ms DESC,r.sequence DESC LIMIT 1`, sourceId, untilMs)
	if err != nil {
		return nil, err
	}
	var next sql.NullInt64
	err = store.database.QueryRowContext(ctx, `SELECT MIN(r.timestamp_ms) FROM records r INDEXED BY records_time`+recordJoins+` WHERE f.source_id=? AND r.timestamp_ms>? AND b.value='codex' AND q.data IS NOT NULL`, sourceId, untilMs).Scan(&next)
	if err != nil {
		return nil, err
	}
	cached = latestQuotaCache{key: key, sinceMs: untilMs, untilMs: math.MaxInt64}
	if next.Valid {
		cached.untilMs = next.Int64
	}
	if len(records) > 0 {
		cached.record = &records[0]
	}
	if store.latestQuotas == nil {
		store.latestQuotas = map[string]latestQuotaCache{}
	}
	store.latestQuotas[sourceId] = cached
	return cloneQuotaRecord(cached.record), nil
}

// cloneQuotaRecord keeps cached observation windows private to the reader.
func cloneQuotaRecord(record *domain.Record) *domain.Record {
	if record == nil {
		return nil
	}
	cloned := cloneRecord(*record)
	return &cloned
}

// cloneRecord copies mutable optional fields without allocating a second record container.
func cloneRecord(record domain.Record) domain.Record {
	cloned := record
	if record.ReportedCostUsd != nil {
		cost := *record.ReportedCostUsd
		cloned.ReportedCostUsd = &cost
	}
	if record.Quota != nil {
		observation := *record.Quota
		if observation.Windows != nil {
			observation.Windows = append([]domain.QuotaWindow{}, observation.Windows...)
		}
		cloned.Quota = &observation
	}
	return cloned
}

// HistoryCacheKey identifies file revisions without decoding their usage or observations.
func (store *Store) HistoryCacheKey(ctx context.Context, sourceId string) (string, error) {
	store.historyMutex.Lock()
	defer store.historyMutex.Unlock()
	var version int64
	if err := store.historyConnection.QueryRowContext(ctx, `PRAGMA data_version`).Scan(&version); err != nil {
		return "", err
	}
	if store.historyKeys == nil || store.historyVersion != version {
		store.historyKeys = map[string]string{}
		store.historyVersion = version
	}
	if key, exists := store.historyKeys[sourceId]; exists {
		return key, nil
	}
	rows, err := store.historyConnection.QueryContext(ctx, `SELECT f.path,c.revision FROM checkpoints c JOIN files f ON f.id=c.file_id WHERE f.source_id=? ORDER BY f.path`, sourceId)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var key strings.Builder
	for rows.Next() {
		var path string
		var revision int64
		if err := rows.Scan(&path, &revision); err != nil {
			return "", err
		}
		fmt.Fprintf(&key, "%d:%s:%d;", len(path), path, revision)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	store.historyKeys[sourceId] = key.String()
	return key.String(), nil
}

// FileRecordCounts reports cached file availability without materializing usage facts.
func (store *Store) FileRecordCounts(ctx context.Context, sourceId string) (map[string]int, error) {
	rows, err := store.database.QueryContext(ctx, "SELECT f.path,(SELECT COUNT(*) FROM records r WHERE r.file_id=f.id) FROM files f WHERE f.source_id=?", sourceId)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var path string
		var count int
		if err := rows.Scan(&path, &count); err != nil {
			return nil, err
		}
		if count > 0 {
			counts[path] = count
		}
	}
	return counts, rows.Err()
}

// fileRecords decodes only selected rows and preserves their SQL order.
func (store *Store) fileRecords(ctx context.Context, query string, arguments ...any) (map[string][]domain.Record, error) {
	connection, err := store.database.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer connection.Close()
	rows, err := connection.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	files := map[string][]domain.Record{}
	for rows.Next() {
		var path string
		record, err := store.scanRecord(ctx, connection, rows, &path)
		if err != nil {
			return nil, err
		}
		files[path] = append(files[path], record)
	}
	return files, rows.Err()
}

// Checkpoints reads completed progress in physical file order.
func (store *Store) Checkpoints(ctx context.Context, sourceId string) ([]domain.Checkpoint, error) {
	rows, err := store.database.QueryContext(ctx, "SELECT f.path,c.revision,c.data FROM checkpoints c JOIN files f ON f.id=c.file_id WHERE f.source_id=? ORDER BY f.path", sourceId)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	checkpoints := []domain.Checkpoint{}
	for rows.Next() {
		var encoded, path string
		var revision int64
		if err := rows.Scan(&path, &revision, &encoded); err != nil {
			return nil, err
		}
		var checkpoint domain.Checkpoint
		if err := json.Unmarshal([]byte(encoded), &checkpoint); err != nil {
			return nil, err
		}
		checkpoint.SourceId, checkpoint.Path, checkpoint.Revision = sourceId, path, revision
		checkpoints = append(checkpoints, checkpoint)
	}
	return checkpoints, rows.Err()
}

// PruneExpiredHistory expires all cached files strictly before the mtime cutoff.
func (store *Store) PruneExpiredHistory(ctx context.Context, cutoffNs int64) error {
	return store.pruneHistory(ctx, "", cutoffNs, true)
}

// PruneHistory expires only the selected source's files by modification time.
func (store *Store) PruneHistory(ctx context.Context, sourceId string, cutoffNs int64) error {
	return store.pruneHistory(ctx, sourceId, cutoffNs, false)
}

// pruneHistory atomically removes file facts, observations and checkpoints.
func (store *Store) pruneHistory(ctx context.Context, sourceId string, cutoffNs int64, isGlobal bool) error {
	query := `SELECT EXISTS(SELECT 1 FROM checkpoints c JOIN files f ON f.id=c.file_id WHERE c.modified_ns<?`
	arguments := []any{cutoffNs}
	if !isGlobal {
		query += ` AND f.source_id=?`
		arguments = append(arguments, sourceId)
	}
	var hasExpired bool
	if err := store.database.QueryRowContext(ctx, query+`)`, arguments...).Scan(&hasExpired); err != nil || !hasExpired {
		return err
	}
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	if err := recordPrunedCycleChanges(ctx, transaction, sourceId, cutoffNs); err != nil {
		return err
	}
	query = `DELETE FROM files WHERE id IN (SELECT f.id FROM files f JOIN checkpoints c ON c.file_id=f.id WHERE c.modified_ns<?`
	arguments = []any{cutoffNs}
	if !isGlobal {
		query += ` AND f.source_id=?`
		arguments = append(arguments, sourceId)
	}
	interner, err := newStringInterner(ctx, transaction)
	if err != nil {
		return err
	}
	defer interner.close()
	condition := `r.file_id IN (SELECT f.id FROM files f JOIN checkpoints c ON c.file_id=f.id WHERE c.modified_ns<?`
	if !isGlobal {
		condition += ` AND f.source_id=?`
	}
	if err := releaseRecordStrings(ctx, transaction, interner, condition+`)`, arguments...); err != nil {
		return err
	}
	if _, err := transaction.ExecContext(ctx, query+`)`, arguments...); err != nil {
		return err
	}
	if err := interner.flush(ctx, transaction); err != nil {
		return err
	}
	if err := transaction.Commit(); err != nil {
		return err
	}
	store.fileCacheMutex.Lock()
	for identity, file := range store.fileCache {
		if file.modifiedNs < cutoffNs && (isGlobal || file.sourceId == sourceId) {
			delete(store.fileCache, identity)
		}
	}
	store.fileCacheMutex.Unlock()
	return nil
}

// recordPrunedCycleChanges records removal boundaries before facts are deleted.
func recordPrunedCycleChanges(ctx context.Context, transaction *sql.Tx, sourceId string, cutoffNs int64) error {
	query := `SELECT f.source_id,MIN(r.timestamp_ms) FROM files f JOIN checkpoints c ON c.file_id=f.id CROSS JOIN records r ON r.file_id=f.id WHERE c.modified_ns<?`
	arguments := []any{cutoffNs}
	if sourceId != "" {
		query += " AND f.source_id=?"
		arguments = append(arguments, sourceId)
	}
	rows, err := transaction.QueryContext(ctx, query+" GROUP BY f.source_id", arguments...)
	if err != nil {
		return err
	}
	changes := map[string]int64{}
	for rows.Next() {
		var id string
		var atMs int64
		if err := rows.Scan(&id, &atMs); err != nil {
			rows.Close()
			return err
		}
		changes[id] = atMs
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for id, atMs := range changes {
		if err := recordCycleChange(ctx, transaction, id, atMs); err != nil {
			return err
		}
	}
	return nil
}
