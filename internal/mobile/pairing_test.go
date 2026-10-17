package mobile

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	datasync "yousage/internal/sync"
)

// TestPairVerifiesReplacementBeforeSaving preserves the last usable pairing until its replacement connects.
func TestPairVerifiesReplacementBeforeSaving(t *testing.T) {
	for _, scenario := range []string{"missing", "wrong-key", "offline", "success"} {
		t.Run(scenario, func(t *testing.T) {
			key, _ := datasync.NewKey()
			otherKey, _ := datasync.NewKey()
			oldId, newId := strings.Repeat("A", 22), strings.Repeat("B", 22)
			oldPayload, newPayload := `{"formatVersion":1,"label":"old"}`, `{"formatVersion":1,"label":"new"}`
			oldEnvelope, _ := datasync.Seal([]byte(oldPayload), key, "space:"+oldId)
			newEnvelope, _ := datasync.Seal([]byte(newPayload), key, "space:"+newId)
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Path == "/api/spaces/"+oldId {
					writer.Write(oldEnvelope)
					return
				}
				if scenario == "missing" {
					writer.WriteHeader(http.StatusNotFound)
					return
				}
				writer.Write(newEnvelope)
			}))
			defer server.Close()
			service := newService(t.TempDir(), nil)
			if _, err := service.Pair(server.URL + "/v/" + oldId + "#" + key); err != nil {
				t.Fatal(err)
			}
			if result, err := service.LoadSnapshot(""); err != nil || result.Status != "ok" {
				t.Fatal(result, err)
			}
			oldPairing, _ := os.ReadFile(filepath.Join(service.directory, pairingFile))
			oldCache, _ := os.ReadFile(filepath.Join(service.directory, snapshotFile))
			candidateKey := key
			if scenario == "wrong-key" {
				candidateKey = otherKey
			}
			if scenario == "offline" {
				server.Close()
			}
			_, err := service.Pair(server.URL + "/v/" + newId + "#" + candidateKey)
			if scenario == "success" {
				if err != nil {
					t.Fatal(err)
				}
				cached, err := newService(service.directory, nil).CachedSnapshot()
				if err != nil || cached.Payload != newPayload {
					t.Fatalf("new pairing has no authenticated cache: %+v %v", cached, err)
				}
				return
			}
			if err == nil {
				t.Fatal("unreachable or unauthenticated replacement was accepted")
			}
			pairing, _ := os.ReadFile(filepath.Join(service.directory, pairingFile))
			cache, _ := os.ReadFile(filepath.Join(service.directory, snapshotFile))
			if !bytes.Equal(pairing, oldPairing) || !bytes.Equal(cache, oldCache) {
				t.Fatal("failed replacement changed saved credentials or cache")
			}
			cached, err := newService(service.directory, nil).CachedSnapshot()
			if err != nil || cached.Payload != oldPayload {
				t.Fatalf("old offline snapshot lost: %+v %v", cached, err)
			}
		})
	}
}
