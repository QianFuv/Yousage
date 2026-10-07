package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"yousage/internal/domain"
)

var schemaV2 = []string{
	`CREATE TABLE files (id INTEGER PRIMARY KEY, source_id TEXT NOT NULL REFERENCES sources(id), path TEXT NOT NULL, UNIQUE(source_id,path))`,
	`CREATE TABLE checkpoints (file_id INTEGER PRIMARY KEY REFERENCES files(id) ON DELETE CASCADE, revision INTEGER NOT NULL, modified_ns INTEGER NOT NULL, data TEXT NOT NULL)`,
	`CREATE TABLE strings (id INTEGER PRIMARY KEY AUTOINCREMENT, value TEXT NOT NULL UNIQUE, refs INTEGER NOT NULL DEFAULT 0)`,
	`CREATE TABLE records (file_id INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE, is_tail INTEGER NOT NULL CHECK(is_tail IN (0,1)), ordinal INTEGER NOT NULL, timestamp_ms INTEGER NOT NULL, sequence INTEGER NOT NULL, bucket_id INTEGER NOT NULL REFERENCES strings(id), quota_only INTEGER NOT NULL CHECK(quota_only IN (0,1)), data BLOB NOT NULL, PRIMARY KEY(file_id,is_tail,ordinal)) WITHOUT ROWID`,
	`CREATE INDEX records_time ON records(timestamp_ms,sequence)`,
	`CREATE TABLE quotas (file_id INTEGER NOT NULL, is_tail INTEGER NOT NULL, ordinal INTEGER NOT NULL, data BLOB NOT NULL, PRIMARY KEY(file_id,is_tail,ordinal), FOREIGN KEY(file_id,is_tail,ordinal) REFERENCES records(file_id,is_tail,ordinal) ON DELETE CASCADE) WITHOUT ROWID`,
	`CREATE TABLE storage_meta (key TEXT PRIMARY KEY, value INTEGER NOT NULL) WITHOUT ROWID`,
	`INSERT INTO strings(id,value) VALUES(0,'')`,
}

// stringInterner owns transaction-local identities until a successful commit.
type stringInterner struct {
	statement, lookup *sql.Stmt
	values            map[string]int64
	references        map[int64]int64
}

// newStringInterner prepares transaction-local dictionary lookups and reference deltas.
func newStringInterner(ctx context.Context, transaction *sql.Tx) (*stringInterner, error) {
	lookup, err := transaction.PrepareContext(ctx, `SELECT id FROM strings WHERE value=?`)
	if err != nil {
		return nil, err
	}
	statement, err := transaction.PrepareContext(ctx, `INSERT INTO strings(value) VALUES(?) RETURNING id`)
	if err != nil {
		lookup.Close()
		return nil, err
	}
	return &stringInterner{statement: statement, lookup: lookup, values: map[string]int64{"": 0}, references: map[int64]int64{}}, nil
}

// close releases dictionary statements.
func (interner *stringInterner) close() { interner.statement.Close(); interner.lookup.Close() }

// intern records each string reference without deduplicating observations.
func (interner *stringInterner) intern(value string) (int64, error) {
	identity, exists := interner.values[value]
	if !exists {
		err := interner.lookup.QueryRow(value).Scan(&identity)
		if err == sql.ErrNoRows {
			err = interner.statement.QueryRow(value).Scan(&identity)
		}
		if err != nil {
			return 0, err
		}
		interner.values[value] = identity
	}
	interner.references[identity]++
	return identity, nil
}

// flush reclaims unreferenced strings in the same transaction as their facts.
func (interner *stringInterner) flush(ctx context.Context, transaction *sql.Tx) error {
	statement, err := transaction.PrepareContext(ctx, `UPDATE strings SET refs=refs+? WHERE id=?`)
	if err != nil {
		return err
	}
	defer statement.Close()
	for identity, delta := range interner.references {
		if _, err := statement.ExecContext(ctx, delta, identity); err != nil {
			return err
		}
	}
	_, err = transaction.ExecContext(ctx, `DELETE FROM strings WHERE refs=0 AND id<>0`)
	return err
}

// releaseRecordStrings counts identities before atomic replacement or retention deletion.
func releaseRecordStrings(ctx context.Context, transaction *sql.Tx, interner *stringInterner, condition string, arguments ...any) error {
	rows, err := transaction.QueryContext(ctx, `SELECT r.data,r.bucket_id,q.data FROM records r LEFT JOIN quotas q ON q.file_id=r.file_id AND q.is_tail=r.is_tail AND q.ordinal=r.ordinal WHERE `+condition, arguments...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var data, quota []byte
		var bucket int64
		if err := rows.Scan(&data, &bucket, &quota); err != nil {
			return err
		}
		reader := newPayloadReader(data)
		for range 7 {
			interner.references[reader.integer()]--
		}
		if reader.err != nil {
			return reader.err
		}
		interner.references[bucket]--
		if quota != nil {
			reader = newPayloadReader(quota)
			interner.references[reader.integer()]--
			count := reader.integer()
			if count < -1 || count > int64(len(reader.data)) {
				return fmt.Errorf("invalid quota window count")
			}
			for index := int64(0); index < count; index++ {
				interner.references[reader.integer()]--
				reader.number()
				reader.integer()
				reader.integer()
			}
			if err := reader.finish(); err != nil {
				return err
			}
		}
	}
	return rows.Err()
}

// migrateV1 converts legacy rows inside the caller's single atomic transaction.
func migrateV1(ctx context.Context, transaction *sql.Tx) error {
	for _, statement := range []string{`ALTER TABLE checkpoints RENAME TO checkpoints_v1`, `ALTER TABLE records RENAME TO records_v1`, `DROP INDEX records_time`} {
		if _, err := transaction.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	for _, statement := range schemaV2 {
		if _, err := transaction.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	if _, err := transaction.ExecContext(ctx, `INSERT INTO files(source_id,path) SELECT source_id,path FROM checkpoints_v1 UNION SELECT source_id,path FROM records_v1`); err != nil {
		return err
	}
	rows, err := transaction.QueryContext(ctx, `SELECT f.id,c.revision,c.data FROM checkpoints_v1 c JOIN files f ON f.source_id=c.source_id AND f.path=c.path`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var identity, revision int64
		var data string
		if err := rows.Scan(&identity, &revision, &data); err != nil {
			rows.Close()
			return err
		}
		var checkpoint domain.Checkpoint
		if err := json.Unmarshal([]byte(data), &checkpoint); err != nil {
			rows.Close()
			return fmt.Errorf("invalid legacy checkpoint: %w", err)
		}
		if err := saveCheckpoint(ctx, transaction, identity, revision, checkpoint); err != nil {
			rows.Close()
			return err
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	interner, err := newStringInterner(ctx, transaction)
	if err != nil {
		return err
	}
	defer interner.close()
	writer, err := newRecordWriter(ctx, transaction, interner)
	if err != nil {
		return err
	}
	defer writer.close()
	rows, err = transaction.QueryContext(ctx, `SELECT f.id,r.is_tail,r.ordinal,r.timestamp_ms,r.rowid,r.data FROM records_v1 r JOIN files f ON f.source_id=r.source_id AND f.path=r.path ORDER BY r.rowid`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var identity, tail, ordinal, timestamp, sequence int64
		var data string
		if err := rows.Scan(&identity, &tail, &ordinal, &timestamp, &sequence, &data); err != nil {
			rows.Close()
			return err
		}
		var record domain.Record
		if err := json.Unmarshal([]byte(data), &record); err != nil {
			rows.Close()
			return fmt.Errorf("invalid legacy record: %w", err)
		}
		if record.TimestampMs != timestamp {
			rows.Close()
			return fmt.Errorf("legacy record timestamp mismatch")
		}
		if err := writer.insert(ctx, identity, tail, ordinal, sequence, record); err != nil {
			rows.Close()
			return err
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if err := interner.flush(ctx, transaction); err != nil {
		return err
	}
	for _, statement := range []string{`INSERT INTO storage_meta SELECT 'sequence',COALESCE(MAX(rowid),0) FROM records_v1`, `DROP TABLE records_v1`, `DROP TABLE checkpoints_v1`, `INSERT INTO storage_meta VALUES('vacuum_pending',1)`, `PRAGMA user_version=2`} {
		if _, err := transaction.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

// saveCheckpoint omits the identity fields already owned by relational columns.
func saveCheckpoint(ctx context.Context, transaction *sql.Tx, identity, revision int64, checkpoint domain.Checkpoint) error {
	modified := checkpoint.ModifiedNs
	checkpoint.SourceId, checkpoint.Path, checkpoint.Revision = "", "", 0
	encoded, err := json.Marshal(checkpoint)
	if err != nil {
		return err
	}
	_, err = transaction.ExecContext(ctx, `INSERT INTO checkpoints(file_id,revision,modified_ns,data) VALUES(?,?,?,?) ON CONFLICT(file_id) DO UPDATE SET revision=excluded.revision,modified_ns=excluded.modified_ns,data=excluded.data`, identity, revision, modified, string(encoded))
	return err
}

// recordWriter shares prepared statements across a file or migration batch.
type recordWriter struct {
	records, quotas *sql.Stmt
	interner        *stringInterner
}

// newRecordWriter prepares atomic usage and observation inserts.
func newRecordWriter(ctx context.Context, transaction *sql.Tx, interner *stringInterner) (*recordWriter, error) {
	records, err := transaction.PrepareContext(ctx, `INSERT INTO records(file_id,is_tail,ordinal,timestamp_ms,sequence,bucket_id,quota_only,data) VALUES(?,?,?,?,?,?,?,?)`)
	if err != nil {
		return nil, err
	}
	quotas, err := transaction.PrepareContext(ctx, `INSERT INTO quotas(file_id,is_tail,ordinal,data) VALUES(?,?,?,?)`)
	if err != nil {
		records.Close()
		return nil, err
	}
	return &recordWriter{records: records, quotas: quotas, interner: interner}, nil
}

// close releases both prepared statements.
func (writer *recordWriter) close() { writer.records.Close(); writer.quotas.Close() }

// insert retains one record and one optional observation for each original event.
func (writer *recordWriter) insert(ctx context.Context, identity, tail, ordinal, sequence int64, record domain.Record) error {
	data, err := encodeRecord(record, writer.interner.intern)
	if err != nil {
		return err
	}
	bucket, err := writer.interner.intern(record.QuotaBucket)
	if err != nil {
		return err
	}
	if _, err := writer.records.ExecContext(ctx, identity, tail, ordinal, record.TimestampMs, sequence, bucket, record.IsQuotaOnly, data); err != nil {
		return err
	}
	if record.Quota != nil {
		data, err := encodeQuota(*record.Quota, writer.interner.intern)
		if err != nil {
			return err
		}
		_, err = writer.quotas.ExecContext(ctx, identity, tail, ordinal, data)
		return err
	}
	return nil
}

// reclaimMigrationSpace retries SQLite's crash-atomic VACUUM after a completed migration.
func (store *Store) reclaimMigrationSpace(ctx context.Context) error {
	var pending int
	if err := store.database.QueryRowContext(ctx, `SELECT COALESCE((SELECT value FROM storage_meta WHERE key='vacuum_pending'),0)`).Scan(&pending); err != nil {
		return err
	}
	if pending == 0 {
		return nil
	}
	if _, err := store.database.ExecContext(ctx, `VACUUM`); err != nil {
		return fmt.Errorf("schema migrated; space reclamation will retry on next open: %w", err)
	}
	var busy, logPages, checkpointed int
	if err := store.database.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &logPages, &checkpointed); err != nil {
		return err
	}
	if busy != 0 {
		return fmt.Errorf("schema migrated; WAL reclamation will retry on next open")
	}
	_, err := store.database.ExecContext(ctx, `DELETE FROM storage_meta WHERE key='vacuum_pending'`)
	return err
}
