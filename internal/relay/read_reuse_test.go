package relay

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

// TestStoreReadsRebindParameters prevents reused reads from retaining a previous
// identifier, blob kind, expiry cutoff, or result after a missing row.
func TestStoreReadsRebindParameters(t *testing.T) {
	server, clock := newTestServer(t)
	now := clock.now()
	expiresAt := now.Add(time.Hour)
	spaceData, shareData := []byte{1, 2, 3}, []byte{1, 4, 5}
	if err := server.store.replaceSpace(t.Context(), testID, spaceData, now); err != nil {
		t.Fatal(err)
	}
	if err := server.store.reserveShare(t.Context(), testID, now, expiresAt); err != nil {
		t.Fatal(err)
	}
	if isFilled, err := server.store.fillShare(t.Context(), testID, shareData, now); err != nil || !isFilled {
		t.Fatalf("fill share = %t: %v", isFilled, err)
	}
	if _, err := server.store.database.Exec("UPDATE blobs SET expires_at=? WHERE kind='space' AND id=?", expiresAt.UnixNano(), testID); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		for _, scenario := range []struct {
			kind string
			id   string
			now  time.Time
			data []byte
		}{
			{"space", testID, now, spaceData},
			{"share", testID, now, shareData},
			{"space", "BBBBBBBBBBBBBBBBBBBBBB", now, nil},
			{"share", testID, expiresAt, nil},
			{"space", testID, expiresAt, nil},
			{"space", testID, expiresAt.Add(-time.Nanosecond), spaceData},
		} {
			value, err := server.store.read(t.Context(), scenario.kind, scenario.id, scenario.now)
			if scenario.data == nil {
				if !errors.Is(err, sql.ErrNoRows) {
					t.Fatalf("%s/%s at %s: expected no row, got %v", scenario.kind, scenario.id, scenario.now, err)
				}
			} else if err != nil || !bytes.Equal(value.data, scenario.data) || !value.updatedAt.Equal(now) {
				t.Fatalf("%s/%s: incorrect bytes or timestamp: %+v, %v", scenario.kind, scenario.id, value, err)
			} else if scenario.kind == "space" && value.etag != contentEtag(scenario.data) {
				t.Fatal("space read returned another row's validator")
			} else if scenario.kind == "share" && value.etag != "" {
				t.Fatal("share read retained a space validator")
			}
			if scenario.kind == "space" {
				validator, err := server.store.readSpaceValidator(t.Context(), scenario.id, scenario.now)
				if scenario.data == nil {
					if !errors.Is(err, sql.ErrNoRows) {
						t.Fatalf("expected no validator, got %+v: %v", validator, err)
					}
				} else if err != nil || validator.etag != contentEtag(scenario.data) || !validator.updatedAt.Equal(now) || len(validator.data) != 0 {
					t.Fatalf("incorrect metadata-only result: %+v, %v", validator, err)
				}
			}
		}
	}
}

// TestStoreReadsRecoverAfterCancellationAndConnectionReplacement ensures reusable
// reads acquire a fresh connection and snapshot after cancellation or pool eviction.
func TestStoreReadsRecoverAfterCancellationAndConnectionReplacement(t *testing.T) {
	server, clock := newTestServer(t)
	store := server.store
	now := clock.now()
	data := []byte{1, 2, 3}
	if err := store.replaceSpace(t.Context(), testID, data, now); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := store.read(canceled, "space", testID, now); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled body read = %v", err)
	}
	if _, err := store.readSpaceValidator(canceled, testID, now); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled metadata read = %v", err)
	}
	store.database.SetMaxIdleConns(0)
	for range 3 {
		value, err := store.read(t.Context(), "space", testID, now)
		if err != nil || !bytes.Equal(value.data, data) || value.etag != contentEtag(data) {
			t.Fatalf("body read after connection replacement: %+v, %v", value, err)
		}
		validator, err := store.readSpaceValidator(t.Context(), testID, now)
		if err != nil || validator.etag != value.etag || !validator.updatedAt.Equal(value.updatedAt) {
			t.Fatalf("metadata read after connection replacement: %+v, %v", validator, err)
		}
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.read(t.Context(), "space", testID, now); err == nil {
		t.Fatal("closed store accepted a body read")
	}
	if _, err := store.readSpaceValidator(t.Context(), testID, now); err == nil {
		t.Fatal("closed store accepted a metadata read")
	}
}
