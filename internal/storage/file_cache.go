package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"yousage/internal/domain"
)

// decodedFile retains immutable facts under the physical checkpoint identity.
type decodedFile struct {
	key        string
	sourceId   string
	modifiedNs int64
	records    []*cachedRecord
}

// cachedRecord delays decoding until the fact is eligible for the requested view.
type cachedRecord struct {
	data, quota []byte
	timestamp   int64
	bucket      string
	isQuotaOnly bool
	record      *domain.Record
}

// cachedFileRecords filters files before records, preserving replay and moved-file deduplication order.
// A deferred read transaction keeps checkpoint revisions and decoded facts in one WAL snapshot.
func (store *Store) cachedFileRecords(ctx context.Context, sourceId string, modifiedSinceNs, sinceMs, untilMs int64, shouldFilterTime, shouldExcludeQuota, isMainBucketOnly bool) (map[string][]domain.Record, error) {
	store.fileCacheMutex.Lock()
	defer store.fileCacheMutex.Unlock()
	connection, err := store.database.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer connection.Close()
	if _, err = connection.ExecContext(ctx, "BEGIN"); err != nil {
		return nil, err
	}
	defer connection.ExecContext(context.Background(), "ROLLBACK")
	rows, err := connection.QueryContext(ctx, `SELECT f.id,f.path,c.revision,c.data,c.modified_ns,COALESCE((SELECT r.sequence FROM records r WHERE r.file_id=f.id ORDER BY r.is_tail DESC,r.ordinal DESC LIMIT 1),0) FROM files f JOIN checkpoints c ON c.file_id=f.id WHERE f.source_id=? AND c.modified_ns>=? ORDER BY f.path`, sourceId, modifiedSinceNs)
	if err != nil {
		return nil, err
	}
	type selectedFile struct {
		identity   int64
		modifiedNs int64
		path, key  string
	}
	selected := []selectedFile{}
	missing := []selectedFile{}
	if store.fileCache == nil {
		store.fileCache = map[int64]decodedFile{}
	}
	for rows.Next() {
		var file selectedFile
		var revision, sequence int64
		var checkpoint string
		if err = rows.Scan(&file.identity, &file.path, &revision, &checkpoint, &file.modifiedNs, &sequence); err != nil {
			rows.Close()
			return nil, err
		}
		file.key = fmt.Sprintf("%s\x00%s\x00%d\x00%s\x00%d", sourceId, file.path, revision, checkpoint, sequence)
		selected = append(selected, file)
		if cached, exists := store.fileCache[file.identity]; !exists || cached.key != file.key {
			missing = append(missing, file)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for offset := 0; offset < len(missing); offset += 256 {
		batch := missing[offset:min(offset+256, len(missing))]
		arguments := make([]any, len(batch))
		decoded := map[int64][]*cachedRecord{}
		for index, file := range batch {
			arguments[index] = file.identity
		}
		query := `SELECT r.file_id,r.data,r.timestamp_ms,r.bucket_id,r.quota_only,q.data FROM records r LEFT JOIN quotas q ON q.file_id=r.file_id AND q.is_tail=r.is_tail AND q.ordinal=r.ordinal WHERE r.file_id IN (` + strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",") + `) ORDER BY r.file_id,r.is_tail,r.ordinal`
		rows, err := connection.QueryContext(ctx, query, arguments...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var identity, bucketId int64
			record := &cachedRecord{}
			err := rows.Scan(&identity, &record.data, &record.timestamp, &bucketId, &record.isQuotaOnly, &record.quota)
			if err != nil {
				rows.Close()
				return nil, err
			}
			record.bucket, _ = store.lookupString(bucketId)
			if _, exists := store.lookupString(bucketId); !exists {
				if err := store.loadStringsFrom(ctx, connection.QueryContext); err != nil {
					rows.Close()
					return nil, err
				}
				var exists bool
				record.bucket, exists = store.lookupString(bucketId)
				if !exists {
					rows.Close()
					return nil, errUnknownString
				}
			}
			decoded[identity] = append(decoded[identity], record)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		for _, file := range batch {
			store.fileCache[file.identity] = decodedFile{key: file.key, sourceId: sourceId, modifiedNs: file.modifiedNs, records: decoded[file.identity]}
		}
	}
	files := map[string][]domain.Record{}
	for _, file := range selected {
		cached := store.fileCache[file.identity].records
		isEligible := func(record *cachedRecord) bool {
			return !(shouldFilterTime && (record.timestamp < sinceMs || record.timestamp > untilMs) || shouldExcludeQuota && record.isQuotaOnly || isMainBucketOnly && record.bucket != "" && record.bucket != "codex")
		}
		count := 0
		for _, record := range cached {
			if isEligible(record) {
				count++
			}
		}
		if count == 0 {
			continue
		}
		selectedRecords := make([]domain.Record, 0, count)
		for _, record := range store.fileCache[file.identity].records {
			if !isEligible(record) {
				continue
			}
			if record.record == nil {
				decoded, err := decodeRecord(record.data, record.timestamp, record.bucket, record.isQuotaOnly, record.quota, store.lookupString)
				if errors.Is(err, errUnknownString) {
					if err := store.loadStringsFrom(ctx, connection.QueryContext); err != nil {
						return nil, err
					}
					decoded, err = decodeRecord(record.data, record.timestamp, record.bucket, record.isQuotaOnly, record.quota, store.lookupString)
				}
				if err != nil {
					return nil, err
				}
				record.record = &decoded
				record.data, record.quota = nil, nil
			}
			selectedRecords = append(selectedRecords, cloneRecord(*record.record))
		}
		if len(selectedRecords) > 0 {
			files[file.path] = selectedRecords
		}
	}
	return files, nil
}

// CachedHistoryValidUntil bounds memoized views by future facts and exact retention expiry.
// Checkpoint keys still detect writes; this boundary detects clock-driven eligibility changes.
func (store *Store) CachedHistoryValidUntil(sourceIds []string, now time.Time) time.Time {
	store.fileCacheMutex.Lock()
	defer store.fileCacheMutex.Unlock()
	selected := map[string]bool{}
	for _, id := range sourceIds {
		selected[id] = true
	}
	until := now.UTC().Truncate(24 * time.Hour).Add(24 * time.Hour)
	for _, file := range store.fileCache {
		if !selected[file.sourceId] {
			continue
		}
		expires := time.Unix(0, file.modifiedNs).Add(90*24*time.Hour + time.Nanosecond)
		if expires.After(now) && expires.Before(until) {
			until = expires
		}
		for _, record := range file.records {
			instant := time.UnixMilli(record.timestamp)
			expires := instant.Add(90*24*time.Hour + time.Millisecond)
			if expires.After(now) && expires.Before(until) {
				until = expires
			}
			if instant.After(now) && instant.Before(until) {
				until = instant
			}
		}
	}
	return until
}
