// Package relay serves encrypted snapshots and expiring shares without decrypting them.
package relay

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type blobStore struct {
	database *sql.DB
}

type blob struct {
	data      []byte
	updatedAt time.Time
}

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
	_, err = database.Exec(`CREATE TABLE IF NOT EXISTS blobs (
		kind TEXT CHECK(kind IN ('space','share')),
		id TEXT,
		data BLOB,
		created_at INTEGER,
		updated_at INTEGER,
		expires_at INTEGER NULL,
		PRIMARY KEY(kind,id)
	)`)
	if err != nil {
		database.Close()
		return nil, err
	}
	return &blobStore{database: database}, nil
}

func (store *blobStore) read(ctx context.Context, kind, id string, now time.Time) (blob, error) {
	var value blob
	var updatedAt int64
	err := store.database.QueryRowContext(ctx, `SELECT data, updated_at FROM blobs
		WHERE kind=? AND id=? AND data IS NOT NULL AND (expires_at IS NULL OR expires_at>?)`,
		kind, id, now.UnixNano()).Scan(&value.data, &updatedAt)
	value.updatedAt = time.Unix(0, updatedAt).UTC()
	return value, err
}

func (store *blobStore) replaceSpace(ctx context.Context, id string, data []byte, now time.Time) error {
	_, err := store.database.ExecContext(ctx, `INSERT INTO blobs(kind,id,data,created_at,updated_at)
		VALUES('space',?,?,?,?) ON CONFLICT(kind,id) DO UPDATE SET data=excluded.data, updated_at=excluded.updated_at`,
		id, data, now.UnixNano(), now.UnixNano())
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
