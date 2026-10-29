package relay

import (
	"bytes"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// legacyStore creates a pre-validator database without using the current initializer.
func legacyStore(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	database, err := sql.Open("sqlite", filepath.Join(directory, "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec("PRAGMA journal_mode=WAL"); err != nil {
		t.Fatal(err)
	}
	_, err = database.Exec(`CREATE TABLE blobs (
		kind TEXT CHECK(kind IN ('space','share')), id TEXT, data BLOB,
		created_at INTEGER, updated_at INTEGER, expires_at INTEGER NULL,
		PRIMARY KEY(kind,id)
	)`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO blobs(kind,id,data,created_at,updated_at)
		VALUES('space',?,?,1,1)`, testID, []byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	return directory
}

// conditionalRequest applies the existing wire-level validator syntax.
func conditionalRequest(server *Server, path, etag string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("If-None-Match", etag)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	return response
}

// TestLegacySpaceValidatorsUpgradeLazily preserves old rows across repeated opens.
func TestLegacySpaceValidatorsUpgradeLazily(t *testing.T) {
	directory := legacyStore(t)
	path := "/api/spaces/" + testID
	expected := `"039058c6f2c0cb492c533b0a4d14ef77cc0f78abccced5287d84a1a2011cfb81"`
	for pass := range 2 {
		server, err := New(Config{Token: testToken, DataDir: directory})
		if err != nil {
			t.Fatal(err)
		}
		var cached sql.NullString
		if err := server.store.database.QueryRow("SELECT etag FROM blobs WHERE id=?", testID).Scan(&cached); err != nil {
			t.Fatal(err)
		}
		if pass == 0 && cached.Valid {
			t.Fatal("opening a legacy database must not load and hash all ciphertext")
		}
		response := conditionalRequest(server, path, expected)
		requireStatus(t, response, http.StatusNotModified)
		if response.Body.Len() != 0 || response.Header().Get("ETag") != expected {
			t.Fatal("legacy conditional read changed validators or returned a body")
		}
		if err := server.store.database.QueryRow("SELECT etag FROM blobs WHERE id=?", testID).Scan(&cached); err != nil || cached.String != expected {
			t.Fatalf("cached validator = %q: %v", cached.String, err)
		}
		if err := server.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

// TestLegacyStoreConcurrentOpen serializes schema inspection and upgrades.
func TestLegacyStoreConcurrentOpen(t *testing.T) {
	directory := legacyStore(t)
	failures := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() {
			store, err := openStore(directory)
			if err == nil {
				err = store.database.Close()
			}
			failures <- err
		})
	}
	workers.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Error(err)
		}
	}
}

// TestLegacyValidatorCompareAndSwapRejectsChangedRows covers the read/cache gap.
func TestLegacyValidatorCompareAndSwapRejectsChangedRows(t *testing.T) {
	for _, shouldRecreate := range []bool{false, true} {
		t.Run(fmt.Sprint(shouldRecreate), func(t *testing.T) {
			server, clock := newTestServer(t)
			previous := blob{data: []byte{1, 2, 3}, etag: contentEtag([]byte{1, 2, 3})}
			next := []byte{1, 4, 5}
			if err := server.store.replaceSpace(t.Context(), testID, previous.data, clock.now()); err != nil {
				t.Fatal(err)
			}
			if shouldRecreate {
				if err := server.store.delete(t.Context(), "space", testID); err != nil {
					t.Fatal(err)
				}
				if _, err := server.store.database.Exec(`INSERT INTO blobs(kind,id,data,created_at,updated_at)
					VALUES('space',?,?,?,?)`, testID, next, clock.now().UnixNano(), clock.now().UnixNano()); err != nil {
					t.Fatal(err)
				}
			} else if _, err := server.store.database.Exec("UPDATE blobs SET data=? WHERE kind='space' AND id=?", next, testID); err != nil {
				t.Fatal(err)
			}
			if err := server.store.cacheSpaceValidator(t.Context(), testID, previous); err != nil {
				t.Fatal(err)
			}
			var cached sql.NullString
			if err := server.store.database.QueryRow("SELECT etag FROM blobs WHERE id=?", testID).Scan(&cached); err != nil || cached.Valid {
				t.Fatalf("stale read cached a validator for changed bytes: %q, %v", cached.String, err)
			}
			response := conditionalRequest(server, "/api/spaces/"+testID, previous.etag)
			requireStatus(t, response, http.StatusOK)
			if !bytes.Equal(response.Body.Bytes(), next) || response.Header().Get("ETag") != contentEtag(next) {
				t.Fatal("changed bytes acquired stale metadata")
			}
		})
	}
}

// TestSpaceValidatorQueryAvoidsBlobPages requires index-only metadata access.
func TestSpaceValidatorQueryAvoidsBlobPages(t *testing.T) {
	server, clock := newTestServer(t)
	var tableRoot int
	if err := server.store.database.QueryRow("SELECT rootpage FROM sqlite_schema WHERE name='blobs'").Scan(&tableRoot); err != nil {
		t.Fatal(err)
	}
	rows, err := server.store.database.Query("EXPLAIN "+spaceValidatorQuery, testID, clock.now().UnixNano())
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	tableCursors := make(map[int]bool)
	for rows.Next() {
		var address, first, second, third, fifth int
		var operation string
		var fourth, comment any
		if err := rows.Scan(&address, &operation, &first, &second, &third, &fourth, &fifth, &comment); err != nil {
			t.Fatal(err)
		}
		if operation == "OpenRead" && second == tableRoot {
			tableCursors[first] = true
		}
		if operation == "Column" && tableCursors[first] {
			t.Fatal("validator query reads the blob table instead of covering metadata")
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

// TestLegacyWriterInvalidatesCachedValidator prevents stale 304s after older writes.
func TestLegacyWriterInvalidatesCachedValidator(t *testing.T) {
	server, clock := newTestServer(t)
	path := "/api/spaces/" + testID
	first, second := []byte{1, 2, 3}, []byte{1, 4, 5}
	requireStatus(t, performRequest(server, http.MethodPut, path, testToken, first), http.StatusNoContent)
	if _, err := server.store.database.Exec(`UPDATE blobs SET data=?,updated_at=? WHERE kind='space' AND id=?`, second, clock.now().UnixNano(), testID); err != nil {
		t.Fatal(err)
	}
	var cached sql.NullString
	if err := server.store.database.QueryRow("SELECT etag FROM blobs WHERE id=?", testID).Scan(&cached); err != nil || cached.Valid {
		t.Fatalf("legacy write retained validator %q: %v", cached.String, err)
	}
	response := conditionalRequest(server, path, contentEtag(first))
	requireStatus(t, response, http.StatusOK)
	if !bytes.Equal(response.Body.Bytes(), second) || response.Header().Get("ETag") != contentEtag(second) {
		t.Fatal("legacy replacement returned stale ciphertext or validator")
	}
	requireStatus(t, conditionalRequest(server, path, contentEtag(second)), http.StatusNotModified)
}

// TestLegacyValidatorCacheFailurePreservesReads keeps opportunistic caching ancillary.
func TestLegacyValidatorCacheFailurePreservesReads(t *testing.T) {
	server, err := New(Config{Token: testToken, DataDir: legacyStore(t)})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	if _, err := server.store.database.Exec(`CREATE TRIGGER reject_validator_cache
		BEFORE UPDATE OF etag ON blobs BEGIN SELECT RAISE(ABORT,'fixture cache failure'); END`); err != nil {
		t.Fatal(err)
	}
	response := conditionalRequest(server, "/api/spaces/"+testID, `"different"`)
	requireStatus(t, response, http.StatusOK)
	if !bytes.Equal(response.Body.Bytes(), []byte{1, 2, 3}) || response.Header().Get("ETag") != contentEtag([]byte{1, 2, 3}) {
		t.Fatal("cache failure changed the successfully read version")
	}
}

// TestSpaceValidatorsStayConsistentDuringReplace protects row-snapshot identity.
func TestSpaceValidatorsStayConsistentDuringReplace(t *testing.T) {
	server, _ := newTestServer(t)
	path := "/api/spaces/" + testID
	first := bytes.Repeat([]byte{1}, 16<<10)
	second := bytes.Repeat([]byte{2}, 16<<10)
	second[0] = 1
	requireStatus(t, performRequest(server, http.MethodPut, path, testToken, first), http.StatusNoContent)
	var workers sync.WaitGroup
	failures := make(chan error, 3)
	workers.Go(func() {
		for attempt := range 100 {
			data := first
			if attempt%2 != 0 {
				data = second
			}
			response := performRequest(server, http.MethodPut, path, testToken, data)
			if response.Code != http.StatusNoContent {
				failures <- fmt.Errorf("replace status = %d", response.Code)
				return
			}
		}
	})
	for _, condition := range []string{contentEtag(first), `"unmatched"`} {
		workers.Go(func() {
			for range 100 {
				response := conditionalRequest(server, path, condition)
				if response.Code == http.StatusNotModified {
					if response.Body.Len() != 0 || response.Header().Get("ETag") != condition {
						failures <- fmt.Errorf("invalid 304 validator or body")
						return
					}
					continue
				}
				if response.Code != http.StatusOK || contentEtag(response.Body.Bytes()) != response.Header().Get("ETag") {
					failures <- fmt.Errorf("body and validator differ: status %d", response.Code)
					return
				}
			}
		})
	}
	workers.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
}

// TestConditionalReadsRetainShareAndExpiryRules keeps validators space-specific.
func TestConditionalReadsRetainShareAndExpiryRules(t *testing.T) {
	server, clock := newTestServer(t)
	id, expiresAt := reserveTestShare(t, server, 1)
	path := "/api/shares/" + id
	data := []byte{1, 2, 3}
	requireStatus(t, performRequest(server, http.MethodPut, path, testToken, data), http.StatusNoContent)
	response := conditionalRequest(server, path, "*")
	requireStatus(t, response, http.StatusOK)
	if !bytes.Equal(response.Body.Bytes(), data) || response.Header().Get("ETag") != "" {
		t.Fatal("share response acquired space validators")
	}
	clock.set(expiresAt)
	requireStatus(t, conditionalRequest(server, path, "*"), http.StatusNotFound)
	spacePath := "/api/spaces/" + testID
	requireStatus(t, performRequest(server, http.MethodPut, spacePath, testToken, data), http.StatusNoContent)
	if _, err := server.store.database.Exec("UPDATE blobs SET expires_at=? WHERE kind='space' AND id=?", clock.now().Add(-time.Nanosecond).UnixNano(), testID); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, conditionalRequest(server, spacePath, contentEtag(data)), http.StatusNotFound)
}
