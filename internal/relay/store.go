// Package relay serves encrypted snapshots and expiring shares without decrypting them.
package relay

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const spaceValidatorQuery = `SELECT etag, updated_at FROM blobs INDEXED BY blob_validators
	WHERE kind='space' AND id=? AND data IS NOT NULL AND etag IS NOT NULL
	AND (expires_at IS NULL OR expires_at>?)`

type blobStore struct {
	database                *sql.DB
	readStatement           *sql.Stmt
	spaceValidatorStatement *sql.Stmt
}

type blob struct {
	data      []byte
	updatedAt time.Time
	etag      string
}

// openStore upgrades the schema and prepares reusable reads for the connection pool.
func openStore(directory string) (*blobStore, error) {
	path, err := filepath.Abs(filepath.Join(directory, "relay.db"))
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	uriPath := filepath.ToSlash(path)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	uri := url.URL{Scheme: "file", Path: uriPath}
	query := url.Values{"_pragma": {"busy_timeout(5000)", "journal_mode(WAL)"}, "_txlock": {"immediate"}}
	uri.RawQuery = query.Encode()
	database, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, err
	}
	database.SetMaxOpenConns(5)
	if err := initializeStore(database); err != nil {
		database.Close()
		return nil, err
	}
	store := &blobStore{database: database}
	store.readStatement, err = database.Prepare(`SELECT data, updated_at, coalesce(etag,'') FROM blobs
		WHERE kind=? AND id=? AND data IS NOT NULL AND (expires_at IS NULL OR expires_at>?)`)
	if err != nil {
		database.Close()
		return nil, err
	}
	store.spaceValidatorStatement, err = database.Prepare(spaceValidatorQuery)
	if err != nil {
		store.readStatement.Close()
		database.Close()
		return nil, err
	}
	return store, nil
}

// close releases the prepared reads and their database after requests have drained.
func (store *blobStore) close() error {
	return errors.Join(store.readStatement.Close(), store.spaceValidatorStatement.Close(), store.database.Close())
}

// initializeStore upgrades metadata without reading or rewriting existing ciphertext.
func initializeStore(database *sql.DB) error {
	transaction, err := database.Begin()
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	_, err = transaction.Exec(`CREATE TABLE IF NOT EXISTS blobs (
		kind TEXT CHECK(kind IN ('space','share')),
		id TEXT,
		data BLOB,
		created_at INTEGER,
		updated_at INTEGER,
		expires_at INTEGER NULL,
		etag TEXT,
		PRIMARY KEY(kind,id)
	)`)
	if err != nil {
		return err
	}
	rows, err := transaction.Query("PRAGMA table_info(blobs)")
	if err != nil {
		return err
	}
	var hasEtag bool
	for rows.Next() {
		var position, required, primaryKey int
		var name, kind string
		var defaultValue any
		if err := rows.Scan(&position, &name, &kind, &required, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return err
		}
		hasEtag = hasEtag || name == "etag"
	}
	iterationError := rows.Err()
	if err := rows.Close(); err != nil {
		return err
	}
	if iterationError != nil {
		return iterationError
	}
	if !hasEtag {
		if _, err := transaction.Exec("ALTER TABLE blobs ADD COLUMN etag TEXT"); err != nil {
			return err
		}
	}
	_, err = transaction.Exec(`CREATE TRIGGER IF NOT EXISTS invalidate_space_etag
		AFTER UPDATE OF data ON blobs
		WHEN NEW.kind='space' AND NEW.etag IS OLD.etag AND NEW.data IS NOT OLD.data
		BEGIN
			UPDATE blobs SET etag=NULL WHERE kind=NEW.kind AND id=NEW.id;
		END`)
	if err != nil {
		return err
	}
	_, err = transaction.Exec(`CREATE INDEX IF NOT EXISTS blob_validators
		ON blobs(kind,id,etag,updated_at,expires_at)
		WHERE data IS NOT NULL AND etag IS NOT NULL`)
	if err != nil {
		return err
	}
	return transaction.Commit()
}

// read returns bytes and their validators from the same SQLite row snapshot.
// Legacy validators are cached only if the ciphertext still matches; cache write
// failures do not invalidate an otherwise successful read.
func (store *blobStore) read(ctx context.Context, kind, id string, now time.Time) (blob, error) {
	var value blob
	var updatedAt int64
	err := store.readStatement.QueryRowContext(ctx, kind, id, now.UnixNano()).Scan(&value.data, &updatedAt, &value.etag)
	value.updatedAt = time.Unix(0, updatedAt).UTC()
	if err == nil && kind == "space" && value.etag == "" {
		value.etag = contentEtag(value.data)
		_ = store.cacheSpaceValidator(ctx, id, value)
	}
	return value, err
}

// cacheSpaceValidator publishes legacy metadata only for the bytes actually read.
func (store *blobStore) cacheSpaceValidator(ctx context.Context, id string, value blob) error {
	_, err := store.database.ExecContext(ctx, `UPDATE blobs SET etag=?
		WHERE kind='space' AND id=? AND etag IS NULL AND data=?`, value.etag, id, value.data)
	return err
}

// readSpaceValidator reads cached validators without materializing ciphertext.
func (store *blobStore) readSpaceValidator(ctx context.Context, id string, now time.Time) (blob, error) {
	var value blob
	var updatedAt int64
	err := store.spaceValidatorStatement.QueryRowContext(ctx, id, now.UnixNano()).Scan(&value.etag, &updatedAt)
	value.updatedAt = time.Unix(0, updatedAt).UTC()
	return value, err
}

// contentEtag identifies the exact encrypted bytes using the existing wire format.
func contentEtag(data []byte) string {
	digest := sha256.Sum256(data)
	return `"` + hex.EncodeToString(digest[:]) + `"`
}

// replaceSpace publishes ciphertext and its validator in one atomic statement.
func (store *blobStore) replaceSpace(ctx context.Context, id string, data []byte, now time.Time) error {
	_, err := store.database.ExecContext(ctx, `INSERT INTO blobs(kind,id,data,created_at,updated_at,etag)
		VALUES('space',?,?,?,?,?) ON CONFLICT(kind,id) DO UPDATE SET data=excluded.data,
		updated_at=excluded.updated_at, etag=excluded.etag`,
		id, data, now.UnixNano(), now.UnixNano(), contentEtag(data))
	return err
}

func (store *blobStore) reserveShare(ctx context.Context, id string, now, expiresAt time.Time) error {
	_, err := store.database.ExecContext(ctx, `INSERT INTO blobs(kind,id,data,created_at,updated_at,expires_at)
		VALUES('share',?,NULL,?,?,?)`, id, now.UnixNano(), now.UnixNano(), expiresAt.UnixNano())
	return err
}

func (store *blobStore) fillShare(ctx context.Context, id string, data []byte, now time.Time) (bool, error) {
	result, err := store.database.ExecContext(ctx, `UPDATE blobs SET data=?,updated_at=?
		WHERE kind='share' AND id=? AND data IS NULL AND expires_at>?`, data, now.UnixNano(), id, now.UnixNano())
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	if err != nil || count > 0 {
		return count > 0, err
	}
	var exists int
	err = store.database.QueryRowContext(ctx, `SELECT 1 FROM blobs WHERE kind='share' AND id=? AND expires_at>?`, id, now.UnixNano()).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, sql.ErrNoRows
	}
	return false, err
}

func (store *blobStore) delete(ctx context.Context, kind, id string) error {
	_, err := store.database.ExecContext(ctx, "DELETE FROM blobs WHERE kind=? AND id=?", kind, id)
	return err
}

func (store *blobStore) sweep(ctx context.Context, now time.Time) error {
	_, err := store.database.ExecContext(ctx, "DELETE FROM blobs WHERE expires_at<=?", now.UnixNano())
	return err
}
