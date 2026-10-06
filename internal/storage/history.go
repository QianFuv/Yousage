package storage

import (
	"context"
	"encoding/json"
	"yousage/internal/domain"
)

func (store *Store) HasRetainedHistory(ctx context.Context, sourceId string, cutoffNs int64) (bool, error) {
	var exists bool
	err := store.database.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM checkpoints c WHERE c.source_id=? AND CAST(json_extract(c.data,'$.modifiedNs') AS INTEGER)>=? AND EXISTS(SELECT 1 FROM records r WHERE r.source_id=c.source_id AND r.path=c.path))`, sourceId, cutoffNs).Scan(&exists)
	return exists, err
}

func (store *Store) CommittedFileRecords(ctx context.Context, sourceId, path string) ([]domain.Record, error) {
	rows, err := store.database.QueryContext(ctx, "SELECT data FROM records WHERE source_id=? AND path=? AND is_tail=0 ORDER BY ordinal", sourceId, path)
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

func (store *Store) FileRecords(ctx context.Context, sourceId string) (map[string][]domain.Record, error) {
	return store.fileRecords(ctx, "SELECT path,data FROM records WHERE source_id=? ORDER BY path,is_tail,ordinal", sourceId)
}

// FileRecordsSince excludes ineligible files before decoding, preserving complete-file deduplication order.
func (store *Store) FileRecordsSince(ctx context.Context, sourceId string, modifiedSinceNs int64) (map[string][]domain.Record, error) {
	return store.fileRecords(ctx, `SELECT path,data FROM records WHERE source_id=? AND path IN (SELECT path FROM checkpoints WHERE source_id=? AND CAST(json_extract(data,'$.modifiedNs') AS INTEGER)>=?) ORDER BY path,is_tail,ordinal`, sourceId, sourceId, modifiedSinceNs)
}

// FileRecordCounts reports cached file availability without materializing usage facts.
func (store *Store) FileRecordCounts(ctx context.Context, sourceId string) (map[string]int, error) {
	rows, err := store.database.QueryContext(ctx, "SELECT path,COUNT(*) FROM records WHERE source_id=? GROUP BY path", sourceId)
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
		counts[path] = count
	}
	return counts, rows.Err()
}

func (store *Store) fileRecords(ctx context.Context, query string, arguments ...any) (map[string][]domain.Record, error) {
	rows, err := store.database.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	files := map[string][]domain.Record{}
	for rows.Next() {
		var path, encoded string
		if err := rows.Scan(&path, &encoded); err != nil {
			return nil, err
		}
		var record domain.Record
		if err := json.Unmarshal([]byte(encoded), &record); err != nil {
			return nil, err
		}
		files[path] = append(files[path], record)
	}
	return files, rows.Err()
}

func (store *Store) Checkpoints(ctx context.Context, sourceId string) ([]domain.Checkpoint, error) {
	rows, err := store.database.QueryContext(ctx, "SELECT data FROM checkpoints WHERE source_id=? ORDER BY path", sourceId)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	checkpoints := []domain.Checkpoint{}
	for rows.Next() {
		var encoded string
		if err := rows.Scan(&encoded); err != nil {
			return nil, err
		}
		var checkpoint domain.Checkpoint
		if err := json.Unmarshal([]byte(encoded), &checkpoint); err != nil {
			return nil, err
		}
		checkpoints = append(checkpoints, checkpoint)
	}
	return checkpoints, rows.Err()
}

// PruneExpiredHistory also expires cached accounts no longer referenced by active configuration.
func (store *Store) PruneExpiredHistory(ctx context.Context, cutoffNs int64) error {
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	if _, err := transaction.ExecContext(ctx, `DELETE FROM records WHERE EXISTS (SELECT 1 FROM checkpoints WHERE checkpoints.source_id=records.source_id AND checkpoints.path=records.path AND CAST(json_extract(checkpoints.data,'$.modifiedNs') AS INTEGER) < ?)`, cutoffNs); err != nil {
		return err
	}
	if _, err := transaction.ExecContext(ctx, `DELETE FROM checkpoints WHERE CAST(json_extract(data,'$.modifiedNs') AS INTEGER) < ?`, cutoffNs); err != nil {
		return err
	}
	return transaction.Commit()
}

// PruneHistory drops only app-owned cache entries whose source files were last modified before cutoff.
func (store *Store) PruneHistory(ctx context.Context, sourceId string, cutoffNs int64) error {
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	rows, err := transaction.QueryContext(ctx, "SELECT path FROM checkpoints WHERE source_id=? AND CAST(json_extract(data,'$.modifiedNs') AS INTEGER) < ?", sourceId, cutoffNs)
	if err != nil {
		return err
	}
	paths := []string{}
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			rows.Close()
			return err
		}
		paths = append(paths, path)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, path := range paths {
		if _, err := transaction.ExecContext(ctx, "DELETE FROM records WHERE source_id=? AND path=?", sourceId, path); err != nil {
			return err
		}
		if _, err := transaction.ExecContext(ctx, "DELETE FROM checkpoints WHERE source_id=? AND path=?", sourceId, path); err != nil {
			return err
		}
	}
	return transaction.Commit()
}
