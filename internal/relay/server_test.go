package relay

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"
)

const testToken = "relay-test-owner-token-at-least-24-characters"
const testID = "AAAAAAAAAAAAAAAAAAAAAA"

type testClock struct {
	mutex sync.Mutex
	value time.Time
}

func (clock *testClock) now() time.Time {
	clock.mutex.Lock()
	defer clock.mutex.Unlock()
	return clock.value
}

func (clock *testClock) set(value time.Time) {
	clock.mutex.Lock()
	defer clock.mutex.Unlock()
	clock.value = value
}

func newTestServer(t *testing.T) (*Server, *testClock) {
	t.Helper()
	clock := &testClock{value: time.Date(2026, 10, 15, 12, 30, 0, 123456789, time.UTC)}
	server, err := New(Config{Token: testToken, DataDir: t.TempDir(), Now: clock.now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := server.Close(); err != nil {
			t.Error(err)
		}
	})
	return server, clock
}

func performRequest(handler http.Handler, method, path, token string, body []byte) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func requireStatus(t *testing.T, response *httptest.ResponseRecorder, expected int) {
	t.Helper()
	if response.Code != expected {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, expected, response.Body)
	}
}

func reserveTestShare(t *testing.T, server *Server, ttlDays int) (string, time.Time) {
	t.Helper()
	response := performRequest(server, http.MethodPost, fmt.Sprintf("/api/shares/reserve?ttlDays=%d", ttlDays), testToken, nil)
	requireStatus(t, response, http.StatusCreated)
	var reservation struct {
		ID        string    `json:"id"`
		ExpiresAt time.Time `json:"expiresAt"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &reservation); err != nil {
		t.Fatal(err)
	}
	if !validID.MatchString(reservation.ID) {
		t.Fatalf("invalid generated share id %q", reservation.ID)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(reservation.ID)
	if err != nil || len(decoded) != 16 {
		t.Fatalf("share id must encode 16 bytes: %q, %v", reservation.ID, err)
	}
	return reservation.ID, reservation.ExpiresAt
}

// TestWritesRequireOwnerToken rejects missing and incorrect owner credentials.
func TestWritesRequireOwnerToken(t *testing.T) {
	server, _ := newTestServer(t)
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/auth"},
		{http.MethodPut, "/api/spaces/" + testID},
		{http.MethodDelete, "/api/spaces/" + testID},
		{http.MethodPost, "/api/shares/reserve?ttlDays=14"},
		{http.MethodPut, "/api/shares/" + testID},
		{http.MethodDelete, "/api/shares/" + testID},
	} {
		for _, token := range []string{"", "wrong-owner-token"} {
			t.Run(route.method+route.path+token, func(t *testing.T) {
				response := performRequest(server, route.method, route.path, token, []byte{1})
				requireStatus(t, response, http.StatusUnauthorized)
				if response.Header().Get("WWW-Authenticate") != "Bearer" {
					t.Fatal("missing bearer challenge")
				}
			})
		}
	}
}

// TestUnknownReadsReturnNotFound keeps absent blobs and routes undiscoverable.
func TestUnknownReadsReturnNotFound(t *testing.T) {
	server, _ := newTestServer(t)
	for _, path := range []string{"/api/spaces/" + testID, "/api/shares/" + testID, "/api/unknown", "/unknown"} {
		t.Run(path, func(t *testing.T) {
			requireStatus(t, performRequest(server, http.MethodGet, path, "", nil), http.StatusNotFound)
		})
	}
	requireStatus(t, performRequest(server, http.MethodPut, "/api/shares/"+testID, testToken, []byte{1}), http.StatusNotFound)
}

// TestSpaceReplaceReturnsIdenticalBytesAndValidators verifies replacement and conditional reads.
func TestSpaceReplaceReturnsIdenticalBytesAndValidators(t *testing.T) {
	server, clock := newTestServer(t)
	path := "/api/spaces/" + testID
	previousETag := ""
	for _, data := range [][]byte{{1, 2, 3, 0, 255}, {1, 9, 8, 0}} {
		requireStatus(t, performRequest(server, http.MethodPut, path, testToken, data), http.StatusNoContent)
		response := performRequest(server, http.MethodGet, path, "", nil)
		requireStatus(t, response, http.StatusOK)
		if !bytes.Equal(response.Body.Bytes(), data) {
			t.Fatalf("body = %v, want %v", response.Body.Bytes(), data)
		}
		etag := response.Header().Get("ETag")
		if etag == "" || etag == previousETag {
			t.Fatalf("missing or unchanged ETag %q", etag)
		}
		if response.Header().Get("Last-Modified") != clock.now().Format(http.TimeFormat) {
			t.Fatalf("Last-Modified = %q", response.Header().Get("Last-Modified"))
		}
		if response.Header().Get("Content-Type") != "application/octet-stream" {
			t.Fatal("wrong ciphertext content type")
		}
		for _, condition := range []struct {
			header string
			status int
		}{
			{etag, http.StatusNotModified}, {"W/" + etag, http.StatusNotModified},
			{`"unrelated", ` + etag, http.StatusNotModified}, {"*", http.StatusNotModified},
			{`"unrelated"`, http.StatusOK},
		} {
			request := httptest.NewRequest(http.MethodGet, path, nil)
			request.Header.Set("If-None-Match", condition.header)
			conditional := httptest.NewRecorder()
			server.ServeHTTP(conditional, request)
			requireStatus(t, conditional, condition.status)
			if condition.status == http.StatusNotModified && conditional.Body.Len() != 0 {
				t.Fatal("304 must have an empty body")
			}
			if conditional.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("conditional response lacks no-store")
			}
		}
		previousETag = etag
		clock.set(clock.now().Add(time.Minute))
	}
	for range 2 {
		requireStatus(t, performRequest(server, http.MethodDelete, path, testToken, nil), http.StatusNoContent)
	}
	requireStatus(t, performRequest(server, http.MethodGet, path, "", nil), http.StatusNotFound)
}

// TestShareReservationFillOnceAndExactExpiry checks the two-step share lifecycle for every TTL.
func TestShareReservationFillOnceAndExactExpiry(t *testing.T) {
	for _, ttlDays := range []int{1, 7, 14, 30} {
		t.Run(fmt.Sprint(ttlDays), func(t *testing.T) {
			server, clock := newTestServer(t)
			createdAt := clock.now()
			id, expiresAt := reserveTestShare(t, server, ttlDays)
			if expected := createdAt.Add(time.Duration(ttlDays) * 24 * time.Hour); !expiresAt.Equal(expected) {
				t.Fatalf("expiry = %s, want %s", expiresAt, expected)
			}
			path := "/api/shares/" + id
			requireStatus(t, performRequest(server, http.MethodGet, path, "", nil), http.StatusNotFound)
			data := []byte{1, 11, 0, 255}
			clock.set(createdAt.Add(time.Hour))
			requireStatus(t, performRequest(server, http.MethodPut, path, testToken, data), http.StatusNoContent)
			requireStatus(t, performRequest(server, http.MethodPut, path, testToken, []byte{1, 99}), http.StatusConflict)
			response := performRequest(server, http.MethodGet, path, "", nil)
			requireStatus(t, response, http.StatusOK)
			if !bytes.Equal(response.Body.Bytes(), data) {
				t.Fatal("share was overwritten or corrupted")
			}
			clock.set(expiresAt.Add(-time.Nanosecond))
			requireStatus(t, performRequest(server, http.MethodGet, path, "", nil), http.StatusOK)
			clock.set(expiresAt)
			requireStatus(t, performRequest(server, http.MethodGet, path, "", nil), http.StatusNotFound)
			requireStatus(t, performRequest(server, http.MethodPut, path, testToken, data), http.StatusNotFound)
		})
	}
}

// TestShareConcurrentFillHasOneWinner prevents concurrent uploads from overwriting shares.
func TestShareConcurrentFillHasOneWinner(t *testing.T) {
	server, _ := newTestServer(t)
	id, _ := reserveTestShare(t, server, 14)
	results := make(chan *httptest.ResponseRecorder, 2)
	for _, data := range [][]byte{{1, 2}, {1, 3}} {
		go func() {
			results <- performRequest(server, http.MethodPut, "/api/shares/"+id, testToken, data)
		}()
	}
	statuses := map[int]int{}
	for range 2 {
		response := <-results
		statuses[response.Code]++
	}
	if statuses[http.StatusNoContent] != 1 || statuses[http.StatusConflict] != 1 {
		t.Fatalf("fill results = %v", statuses)
	}
}

// TestSweeperDeletesExpiredRowsOnly removes expired filled and empty shares while preserving live data.
func TestSweeperDeletesExpiredRowsOnly(t *testing.T) {
	server, clock := newTestServer(t)
	id, _ := reserveTestShare(t, server, 1)
	requireStatus(t, performRequest(server, http.MethodPut, "/api/shares/"+id, testToken, []byte{1}), http.StatusNoContent)
	reserveTestShare(t, server, 1)
	activeID, _ := reserveTestShare(t, server, 7)
	requireStatus(t, performRequest(server, http.MethodPut, "/api/spaces/"+testID, testToken, []byte{1}), http.StatusNoContent)
	clock.set(clock.now().Add(24 * time.Hour))
	if err := server.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := server.store.database.QueryRow("SELECT count(*) FROM blobs").Scan(&count); err != nil || count != 2 {
		t.Fatalf("rows after sweep = %d, want 2: %v", count, err)
	}
	requireStatus(t, performRequest(server, http.MethodPut, "/api/shares/"+activeID, testToken, []byte{1}), http.StatusNoContent)
	requireStatus(t, performRequest(server, http.MethodGet, "/api/spaces/"+testID, "", nil), http.StatusOK)
}

// TestExpiredEmptyShareCannotBeFilledAndRevocationIsIdempotent checks expiry and explicit revocation.
func TestExpiredEmptyShareCannotBeFilledAndRevocationIsIdempotent(t *testing.T) {
	server, clock := newTestServer(t)
	id, expiresAt := reserveTestShare(t, server, 1)
	clock.set(expiresAt)
	requireStatus(t, performRequest(server, http.MethodPut, "/api/shares/"+id, testToken, []byte{1}), http.StatusNotFound)
	activeID, _ := reserveTestShare(t, server, 14)
	path := "/api/shares/" + activeID
	requireStatus(t, performRequest(server, http.MethodPut, path, testToken, []byte{1}), http.StatusNoContent)
	for range 2 {
		requireStatus(t, performRequest(server, http.MethodDelete, path, testToken, nil), http.StatusNoContent)
	}
	requireStatus(t, performRequest(server, http.MethodGet, path, "", nil), http.StatusNotFound)
}

// TestInvalidShareTTL rejects missing, malformed, and unsupported lifetimes.
func TestInvalidShareTTL(t *testing.T) {
	server, _ := newTestServer(t)
	for _, ttl := range []string{"", "0", "-1", "2", "31", "1.0", "invalid", "999999999999999999999999"} {
		t.Run(ttl, func(t *testing.T) {
			requireStatus(t, performRequest(server, http.MethodPost, "/api/shares/reserve?ttlDays="+ttl, testToken, nil), http.StatusBadRequest)
		})
	}
}

// TestEnvelopeBodyLimit permits exactly 4 MiB and rejects larger streamed uploads.
func TestEnvelopeBodyLimit(t *testing.T) {
	server, _ := newTestServer(t)
	id, _ := reserveTestShare(t, server, 14)
	for _, path := range []string{"/api/spaces/" + testID, "/api/shares/" + id} {
		for _, size := range []int{maxBodyBytes + 1, maxBodyBytes} {
			t.Run(fmt.Sprintf("%s/%d", path, size), func(t *testing.T) {
				data := make([]byte, size)
				data[0] = 1
				request := httptest.NewRequest(http.MethodPut, path, bytes.NewReader(data))
				request.ContentLength = -1
				request.Header.Set("Authorization", "Bearer "+testToken)
				response := httptest.NewRecorder()
				server.ServeHTTP(response, request)
				expected := http.StatusNoContent
				if size > maxBodyBytes {
					expected = http.StatusRequestEntityTooLarge
				}
				requireStatus(t, response, expected)
			})
		}
	}
}

// TestInvalidEnvelopeVersionAndID rejects invalid envelope versions and path identifiers.
func TestInvalidEnvelopeVersionAndID(t *testing.T) {
	server, _ := newTestServer(t)
	id, _ := reserveTestShare(t, server, 14)
	for _, path := range []string{"/api/spaces/" + testID, "/api/shares/" + id} {
		for _, data := range [][]byte{nil, {0}, {2, 1, 2}} {
			requireStatus(t, performRequest(server, http.MethodPut, path, testToken, data), http.StatusBadRequest)
		}
	}
	for _, id := range []string{"short", strings.Repeat("A", 21), strings.Repeat("A", 23), strings.Repeat("A", 21) + ".", strings.Repeat("A", 21) + "!", strings.Repeat("A", 21) + "é"} {
		for _, route := range []struct{ method, prefix string }{
			{http.MethodGet, "/api/spaces/"}, {http.MethodPut, "/api/spaces/"}, {http.MethodDelete, "/api/spaces/"},
			{http.MethodGet, "/api/shares/"}, {http.MethodPut, "/api/shares/"}, {http.MethodDelete, "/api/shares/"},
			{http.MethodGet, "/v/"}, {http.MethodGet, "/s/"},
		} {
			requireStatus(t, performRequest(server, route.method, route.prefix+id, testToken, []byte{1}), http.StatusBadRequest)
		}
	}
}

// TestAPIAndHTMLSecurityHeaders checks cache prevention and browser security policy.
func TestAPIAndHTMLSecurityHeaders(t *testing.T) {
	server, _ := newTestServer(t)
	for _, route := range []struct{ method, path, token string }{
		{http.MethodGet, "/api/auth", testToken}, {http.MethodGet, "/api/auth", ""},
		{http.MethodGet, "/api/spaces/" + testID, ""}, {http.MethodPut, "/api/spaces/bad", testToken},
		{http.MethodPut, "/api/spaces/" + testID, testToken}, {http.MethodPost, "/api/shares/reserve?ttlDays=14", testToken},
		{http.MethodGet, "/api/unknown", ""}, {http.MethodPatch, "/api/auth", testToken},
	} {
		response := performRequest(server, route.method, route.path, route.token, []byte{1})
		if response.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s %s missing no-store", route.method, route.path)
		}
	}
	for _, prefix := range []string{"/v/", "/s/"} {
		response := performRequest(server, http.MethodGet, prefix+testID, "", nil)
		requireStatus(t, response, http.StatusOK)
		for name, expected := range map[string]string{
			"Content-Security-Policy": contentSecurityPolicy, "Referrer-Policy": "no-referrer",
			"X-Content-Type-Options": "nosniff", "Content-Type": "text/html; charset=utf-8",
		} {
			if response.Header().Get(name) != expected {
				t.Errorf("%s = %q, want %q", name, response.Header().Get(name), expected)
			}
		}
	}
}

// TestPlaceholderServedWhenDistAbsent works independently of concurrent frontend builds.
func TestPlaceholderServedWhenDistAbsent(t *testing.T) {
	files := fstest.MapFS{"web/README.md": &fstest.MapFile{Data: []byte("build web assets")}}
	for _, page := range []string{"viewer.html", "share.html"} {
		t.Run(page, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/v/"+testID, nil)
			request.SetPathValue("id", testID)
			response := httptest.NewRecorder()
			servePage(page, files)(response, request)
			requireStatus(t, response, http.StatusOK)
			if response.Body.String() != placeholderHTML {
				t.Fatal("missing placeholder without dist")
			}
		})
	}
}

// TestBuiltPagesAndImmutableAssets verifies embedded entry selection and static asset caching.
func TestBuiltPagesAndImmutableAssets(t *testing.T) {
	files := fstest.MapFS{
		"web/dist/viewer.html":   {Data: []byte("<html>viewer</html>")},
		"web/dist/share.html":    {Data: []byte("<html>share</html>")},
		"web/dist/assets/app.js": {Data: []byte("export const app = true;")},
	}
	for _, page := range []string{"viewer.html", "share.html"} {
		request := httptest.NewRequest(http.MethodGet, "/v/"+testID, nil)
		request.SetPathValue("id", testID)
		response := httptest.NewRecorder()
		servePage(page, files)(response, request)
		requireStatus(t, response, http.StatusOK)
		if !bytes.Equal(response.Body.Bytes(), files["web/dist/"+page].Data) {
			t.Fatalf("wrong embedded page %s", page)
		}
	}
	response := performRequest(serveAsset(files), http.MethodGet, "/assets/app.js", "", nil)
	requireStatus(t, response, http.StatusOK)
	if response.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatal("asset cache policy missing")
	}
	if !strings.Contains(response.Header().Get("Content-Type"), "javascript") {
		t.Fatal("asset MIME type missing")
	}
	for _, path := range []string{"/assets/missing.js", "/assets/", "/assets/../viewer.html"} {
		requireStatus(t, performRequest(serveAsset(files), http.MethodGet, path, "", nil), http.StatusNotFound)
	}
}

// TestHealthAndAuth checks the public health probe and authenticated connection probe.
func TestHealthAndAuth(t *testing.T) {
	server, _ := newTestServer(t)
	response := performRequest(server, http.MethodGet, "/healthz", "", nil)
	requireStatus(t, response, http.StatusOK)
	if strings.TrimSpace(response.Body.String()) != `{"status":"ok"}` {
		t.Fatalf("health body = %s", response.Body)
	}
	requireStatus(t, performRequest(server, http.MethodGet, "/api/auth", testToken, nil), http.StatusNoContent)
}

// TestSQLiteWALTimeoutAndPersistence checks SQLite configuration and durable ciphertext storage.
func TestSQLiteWALTimeoutAndPersistence(t *testing.T) {
	directory := t.TempDir()
	server, err := New(Config{Token: testToken, DataDir: directory})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	var mode string
	var timeout int
	if err := server.store.database.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal mode = %q: %v", mode, err)
	}
	if err := server.store.database.QueryRow("PRAGMA busy_timeout").Scan(&timeout); err != nil || timeout != 5000 {
		t.Fatalf("busy timeout = %d: %v", timeout, err)
	}
	data := []byte{1, 3, 4, 5}
	requireStatus(t, performRequest(server, http.MethodPut, "/api/spaces/"+testID, testToken, data), http.StatusNoContent)
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(Config{Token: testToken, DataDir: directory})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	response := performRequest(reopened, http.MethodGet, "/api/spaces/"+testID, "", nil)
	requireStatus(t, response, http.StatusOK)
	if !bytes.Equal(response.Body.Bytes(), data) {
		t.Fatal("data lost after reopen")
	}
}

// TestServeShutsDownOnCancellation checks a live listener and graceful cancellation.
func TestServeShutsDownOnCancellation(t *testing.T) {
	server, _ := newTestServer(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- server.Serve(ctx, listener) }()
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Get("http://" + listener.Addr().String() + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", response.StatusCode)
	}
	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server failed to shut down")
	}
}
