package relay

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSweepPreservesExpiryBoundaries covers both blob kinds, reservations, NULL
// expiry, and old writers before and after an existing database is upgraded.
func TestSweepPreservesExpiryBoundaries(t *testing.T) {
	directory := legacyStore(t)
	writer, err := sql.Open("sqlite", filepath.Join(directory, "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	now := time.Unix(1700000000, 123456789)
	wanted := map[string]bool{"space/" + testID: true}
	for _, kind := range []string{"space", "share"} {
		for _, hasData := range []bool{false, true} {
			for _, expiry := range []struct {
				name  string
				value any
				keep  bool
			}{
				{"permanent", nil, true},
				{"before", now.Add(-time.Nanosecond).UnixNano(), false},
				{"exact", now.UnixNano(), false},
				{"after", now.Add(time.Nanosecond).UnixNano(), true},
			} {
				id := fmt.Sprintf("%t-%s", hasData, expiry.name)
				var data []byte
				if hasData {
					data = []byte{1, 2, 3}
				}
				if _, err := writer.Exec(`INSERT INTO blobs(kind,id,data,created_at,updated_at,expires_at)
					VALUES(?,?,?,1,1,?)`, kind, id, data, expiry.value); err != nil {
					t.Fatal(err)
				}
				if expiry.keep {
					wanted[kind+"/"+id] = true
				}
			}
		}
	}
	for pass := range 2 {
		store, err := openStore(directory)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.sweep(t.Context(), now); err != nil {
			store.close()
			t.Fatal(err)
		}
		rows, err := store.database.Query("SELECT kind,id FROM blobs")
		if err != nil {
			store.close()
			t.Fatal(err)
		}
		count := 0
		for rows.Next() {
			var kind, id string
			if err := rows.Scan(&kind, &id); err != nil {
				t.Error(err)
			}
			if !wanted[kind+"/"+id] {
				t.Errorf("unexpected surviving row %s/%s", kind, id)
			}
			count++
		}
		if err := rows.Err(); err != nil {
			t.Error(err)
		}
		rows.Close()
		if count != len(wanted) {
			t.Errorf("surviving rows = %d, want %d", count, len(wanted))
		}
		if err := store.close(); err != nil {
			t.Fatal(err)
		}
		if pass == 0 {
			if _, err := writer.Exec("UPDATE blobs SET expires_at=? WHERE id='true-permanent'", now.UnixNano()); err != nil {
				t.Fatal(err)
			}
			delete(wanted, "space/true-permanent")
			delete(wanted, "share/true-permanent")
			if _, err := writer.Exec("UPDATE blobs SET expires_at=NULL WHERE id='true-after'"); err != nil {
				t.Fatal(err)
			}
			now = now.Add(time.Nanosecond)
			delete(wanted, "space/false-after")
			delete(wanted, "share/false-after")
		}
	}
}

// TestSweepUsesExpiryRange requires cleanup to seek expired rows rather than
// scanning every live ciphertext record while holding SQLite's writer lock.
func TestSweepUsesExpiryRange(t *testing.T) {
	server, clock := newTestServer(t)
	rows, err := server.store.database.Query("EXPLAIN QUERY PLAN DELETE FROM blobs WHERE expires_at<=?", clock.now().UnixNano())
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	hasExpiryRange := false
	for rows.Next() {
		var identity, parent, unused int
		var detail string
		if err := rows.Scan(&identity, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		t.Log(detail)
		if strings.Contains(detail, "SCAN blobs") {
			t.Fatal("expiry cleanup scans live blob rows")
		}
		hasExpiryRange = hasExpiryRange || (strings.Contains(detail, "SEARCH blobs") && strings.Contains(detail, "expires_at<?"))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !hasExpiryRange {
		t.Fatal("expiry cleanup has no bounded expiry range")
	}
}
