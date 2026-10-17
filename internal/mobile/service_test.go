package mobile

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	datasync "yousage/internal/sync"
)

// TestParseLink checks the viewer format and the local-network HTTP exception.
func TestParseLink(test *testing.T) {
	key := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	identifier := strings.Repeat("A", 22)
	for _, scenario := range []struct {
		name, link string
		isValid    bool
	}{
		{"https", " https://relay.example/v/" + identifier + "#" + key + "\n", true},
		{"localhost", "http://localhost:8080/v/" + identifier + "#" + key, true},
		{"loopback", "http://127.0.0.1:8080/v/" + identifier + "#" + key, true},
		{"ipv6 loopback", "http://[::1]:8080/v/" + identifier + "#" + key, true},
		{"lan", "http://192.168.1.10/v/" + identifier + "#" + key, true},
		{"emulator", "http://10.0.2.2:8080/v/" + identifier + "#" + key, true},
		{"private ipv6", "http://[fd00::1]/v/" + identifier + "#" + key, true},
		{"public http", "http://relay.example/v/" + identifier + "#" + key, false},
		{"public address", "http://8.8.8.8/v/" + identifier + "#" + key, false},
		{"localhost suffix", "http://localhost.example/v/" + identifier + "#" + key, false},
		{"credentials", "https://user@relay.example/v/" + identifier + "#" + key, false},
		{"short id", "https://relay.example/v/short#" + key, false},
		{"invalid id", "https://relay.example/v/" + strings.Repeat("!", 22) + "#" + key, false},
		{"encoded path", "https://relay.example/v/%41" + identifier[1:] + "#" + key, false},
		{"extra path", "https://relay.example/v/" + identifier + "/#" + key, false},
		{"share link", "https://relay.example/s/" + identifier + "#" + key, false},
		{"query", "https://relay.example/v/" + identifier + "?x=1#" + key, false},
		{"empty query", "https://relay.example/v/" + identifier + "?#" + key, false},
		{"short key", "https://relay.example/v/" + identifier + "#AAAA", false},
		{"padded key", "https://relay.example/v/" + identifier + "#" + key + "=", false},
		{"missing key", "https://relay.example/v/" + identifier, false},
		{"missing host", "https:///v/" + identifier + "#" + key, false},
		{"scheme", "file://localhost/v/" + identifier + "#" + key, false},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			pairing, err := parseLink(scenario.link)
			if (err == nil) != scenario.isValid {
				test.Fatalf("valid = %t, error = %v", scenario.isValid, err)
			}
			if scenario.isValid && (pairing.ID != identifier || pairing.Key != key) {
				test.Fatal("credentials changed during parsing")
			}
		})
	}
}

// TestPairingPersistence checks replacement, permissions, secret boundaries, and deletion.
func TestPairingPersistence(test *testing.T) {
	directory := filepath.Join(test.TempDir(), "private")
	service := newService(directory, nil)
	key, err := datasync.NewKey()
	if err != nil {
		test.Fatal(err)
	}
	envelope, err := datasync.Seal([]byte(`{"formatVersion":1}`), key, "space:"+strings.Repeat("A", 22))
	if err != nil {
		test.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) { writer.Write(envelope) }))
	defer server.Close()
	link := server.URL + "/v/" + strings.Repeat("A", 22) + "#" + key
	pairing, err := service.GetPairing()
	if err != nil || pairing.IsPaired {
		test.Fatalf("initial pairing = %+v, error = %v", pairing, err)
	}
	for range 2 {
		pairing, err = service.Pair(link)
		if err != nil || !pairing.IsPaired || pairing.RelayOrigin != server.URL {
			test.Fatalf("pairing = %+v, error = %v", pairing, err)
		}
	}
	encoded, _ := json.Marshal(pairing)
	if strings.Contains(string(encoded), key) || strings.Contains(string(encoded), strings.Repeat("A", 22)) {
		test.Fatal("frontend metadata disclosed credentials")
	}
	reopened := newService(directory, nil)
	if saved, err := reopened.GetPairing(); err != nil || saved != pairing {
		test.Fatalf("reopened pairing = %+v, error = %v", saved, err)
	}
	info, err := os.Stat(filepath.Join(directory, "pairing.json"))
	if err != nil {
		test.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		test.Fatalf("private file permissions = %o", info.Mode().Perm())
	}
	if _, err = service.Pair("invalid"); err == nil {
		test.Fatal("invalid replacement succeeded")
	}
	if saved, err := service.GetPairing(); err != nil || saved != pairing {
		test.Fatal("invalid replacement damaged existing credentials")
	}
	entries, _ := os.ReadDir(directory)
	if len(entries) != 2 {
		test.Fatalf("temporary files remain: %v", entries)
	}
	for range 2 {
		if err = reopened.Unpair(); err != nil {
			test.Fatal(err)
		}
	}
	if saved, err := service.GetPairing(); err != nil || saved.IsPaired {
		test.Fatalf("unpaired = %+v, error = %v", saved, err)
	}
}

// TestLoadSnapshot exercises conditional reads and each recoverable relay outcome.
func TestLoadSnapshot(test *testing.T) {
	key, _ := datasync.NewKey()
	wrongKey, _ := datasync.NewKey()
	identifier := strings.Repeat("A", 22)
	payload := `{"schemaVersion":1,"usage":[]}`
	envelope, err := datasync.Seal([]byte(payload), key, "space:"+identifier)
	if err != nil {
		test.Fatal(err)
	}
	version := append([]byte(nil), envelope...)
	version[0] = 2
	wrongAAD, _ := datasync.Seal([]byte(payload), key, "share:"+identifier)
	invalidJSON, _ := datasync.Seal([]byte("invalid JSON"), key, "space:"+identifier)
	for _, scenario := range []struct {
		name, status, problem string
		code                  int
		body                  []byte
		key                   string
	}{
		{"ok", "ok", "", 200, envelope, key},
		{"304", "unchanged", "", 304, nil, key},
		{"missing", "problem", "missing", 404, nil, key},
		{"bad key", "problem", "decrypt", 200, envelope, wrongKey},
		{"bad aad", "problem", "decrypt", 200, wrongAAD, key},
		{"version", "problem", "version", 200, version, key},
		{"truncated", "problem", "decrypt", 200, envelope[:12], key},
		{"empty", "problem", "decrypt", 200, nil, key},
		{"invalid json", "problem", "decrypt", 200, invalidJSON, key},
		{"oversized", "problem", "decrypt", 200, make([]byte, maxEnvelopeBytes+1), key},
		{"server failure", "problem", "network", 500, nil, key},
		{"redirect", "problem", "network", 302, nil, key},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Method != http.MethodGet || request.URL.Path != "/api/spaces/"+identifier || request.Header.Get("Cache-Control") != "no-store" || request.Header.Get("If-None-Match") != `"cached"` {
					test.Errorf("unexpected relay request: %s %s %v", request.Method, request.URL.Path, request.Header)
				}
				writer.Header().Set("ETag", `"current"`)
				writer.WriteHeader(scenario.code)
				writer.Write(scenario.body)
			}))
			defer server.Close()
			service := newService(test.TempDir(), nil)
			if _, err := seedPairing(test, service, server.URL+"/v/"+identifier+"#"+scenario.key); err != nil {
				test.Fatal(err)
			}
			result, err := service.LoadSnapshot(`"cached"`)
			if err != nil || result.Status != scenario.status || result.Problem != scenario.problem {
				test.Fatalf("result = %+v, error = %v", result, err)
			}
			if scenario.status == "ok" && (result.Payload != payload || result.ETag != `"current"`) {
				test.Fatalf("snapshot changed: %+v", result)
			}
			if scenario.status != "ok" && result.Payload != "" {
				test.Fatal("failed read disclosed a payload")
			}
		})
	}
	service := newService(test.TempDir(), nil)
	if result, err := service.LoadSnapshot(""); err != nil || result.Status != "unpaired" {
		test.Fatalf("unpaired result = %+v, error = %v", result, err)
	}
	server := httptest.NewServer(http.NotFoundHandler())
	if _, err = seedPairing(test, service, server.URL+"/v/"+identifier+"#"+key); err != nil {
		test.Fatal(err)
	}
	server.Close()
	if result, err := service.LoadSnapshot(""); err != nil || result.Problem != "network" {
		test.Fatalf("network result = %+v, error = %v", result, err)
	}
}

// TestCachedSnapshot checks that a cold start can reopen the last relay read for the same pairing only.
func TestCachedSnapshot(test *testing.T) {
	key, _ := datasync.NewKey()
	identifier := strings.Repeat("A", 22)
	payload := `{"schemaVersion":1,"usage":[]}`
	envelope, err := datasync.Seal([]byte(payload), key, "space:"+identifier)
	if err != nil {
		test.Fatal(err)
	}
	isChanged := true
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("ETag", `"current"`)
		if !isChanged {
			writer.WriteHeader(http.StatusNotModified)
			return
		}
		writer.Write(envelope)
	}))
	defer server.Close()
	directory := test.TempDir()
	service := newService(directory, nil)
	if result, err := service.CachedSnapshot(); err != nil || result.Status != "unpaired" {
		test.Fatalf("unpaired cache = %+v, error = %v", result, err)
	}
	link := server.URL + "/v/" + identifier + "#" + key
	if _, err = seedPairing(test, service, link); err != nil {
		test.Fatal(err)
	}
	if result, err := service.CachedSnapshot(); err != nil || result.Status != "empty" {
		test.Fatalf("empty cache = %+v, error = %v", result, err)
	}
	if result, err := service.LoadSnapshot(""); err != nil || result.Status != "ok" {
		test.Fatalf("load = %+v, error = %v", result, err)
	}
	isChanged = false
	if result, err := service.LoadSnapshot(`"current"`); err != nil || result.Status != "unchanged" {
		test.Fatalf("conditional load = %+v, error = %v", result, err)
	}
	cached, err := newService(directory, nil).CachedSnapshot()
	if err != nil || cached.Status != "ok" || cached.Payload != payload || cached.ETag != `"current"` {
		test.Fatalf("reopened cache = %+v, error = %v", cached, err)
	}
	stored, err := os.ReadFile(filepath.Join(directory, snapshotFile))
	if err != nil || strings.Contains(string(stored), "schemaVersion") {
		test.Fatal("cached snapshot is not kept encrypted")
	}
	if err = os.WriteFile(filepath.Join(directory, snapshotFile), []byte("{"), 0600); err != nil {
		test.Fatal(err)
	}
	if result, err := service.CachedSnapshot(); err != nil || result.Status != "empty" || result.Payload != "" {
		test.Fatalf("corrupt cache = %+v, error = %v", result, err)
	}
	isChanged = true
	service.LoadSnapshot("")
	otherKey, _ := datasync.NewKey()
	if _, err = seedPairing(test, service, server.URL+"/v/"+strings.Repeat("B", 22)+"#"+otherKey); err != nil {
		test.Fatal(err)
	}
	if result, err := service.CachedSnapshot(); err != nil || result.Status != "empty" {
		test.Fatalf("replaced pairing reused the old cache: %+v, error = %v", result, err)
	}
	if _, err = seedPairing(test, service, link); err != nil {
		test.Fatal(err)
	}
	service.LoadSnapshot("")
	if err = service.Unpair(); err != nil {
		test.Fatal(err)
	}
	if entries, _ := os.ReadDir(directory); len(entries) != 0 {
		test.Fatalf("unpair left private files: %v", entries)
	}
}

// TestSnapshotTimeout checks that a stalled relay produces a recoverable network problem.
func TestSnapshotTimeout(test *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	}))
	defer server.Close()
	service := newService(test.TempDir(), nil)
	if service.client.Timeout != 15*time.Second {
		test.Fatal("relay timeout changed")
	}
	service.client.Timeout = 20 * time.Millisecond
	key, _ := datasync.NewKey()
	if _, err := seedPairing(test, service, server.URL+"/v/"+strings.Repeat("A", 22)+"#"+key); err != nil {
		test.Fatal(err)
	}
	if result, err := service.LoadSnapshot(""); err != nil || result.Problem != "network" {
		test.Fatalf("timeout result = %+v, error = %v", result, err)
	}
}

// TestOpenURL ensures only HTTPS destinations reach the native browser.
func TestOpenURL(test *testing.T) {
	var opened string
	service := newService(test.TempDir(), func(destination string) error {
		opened = destination
		return nil
	})
	for _, destination := range []string{"http://example.com", "javascript:alert(1)", "file:///tmp/a", "https:///no-host", "https://user@example.com"} {
		if err := service.OpenURL(destination); err == nil || opened != "" {
			test.Fatalf("unsafe destination opened: %s", destination)
		}
	}
	if err := service.OpenURL("https://example.com/docs"); err != nil || opened != "https://example.com/docs" {
		test.Fatalf("HTTPS destination = %s, error = %v", opened, err)
	}
}

// seedPairing restores legacy saved credentials without requiring a successful network read.
func seedPairing(test *testing.T, service *Service, link string) (Pairing, error) {
	test.Helper()
	pairing, err := parseLink(link)
	if err != nil {
		return Pairing{}, err
	}
	data, err := json.Marshal(pairing)
	if err != nil {
		return Pairing{}, err
	}
	service.mutex.Lock()
	defer service.mutex.Unlock()
	if err := service.writePrivate(pairingFile, data); err != nil {
		return Pairing{}, err
	}
	if err := service.removePrivate(snapshotFile); err != nil {
		return Pairing{}, err
	}
	return Pairing{IsPaired: true, RelayOrigin: pairing.Origin}, nil
}
