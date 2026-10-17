// Package mobile provides the native Android read-only snapshot viewer.
package mobile

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	datasync "yousage/internal/sync"
)

const maxEnvelopeBytes = 4 << 20
const pairingFile = "pairing.json"
const snapshotFile = "snapshot.json"

var validSpaceID = regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`)

// Pairing exposes pairing state without disclosing the space ID or secret key.
type Pairing struct {
	IsPaired    bool   `json:"isPaired"`
	RelayOrigin string `json:"relayOrigin"`
}

// SnapshotResult carries decrypted JSON or a recoverable relay outcome.
type SnapshotResult struct {
	Status  string `json:"status"`
	Problem string `json:"problem"`
	Payload string `json:"payload"`
	ETag    string `json:"etag"`
}

// storedPairing holds credentials exclusively in private native storage.
type storedPairing struct {
	Origin string `json:"origin"`
	ID     string `json:"id"`
	Key    string `json:"key"`
}

// cachedSnapshot keeps the last authenticated envelope, still encrypted, for the next cold start.
type cachedSnapshot struct {
	ID       string `json:"id"`
	ETag     string `json:"etag"`
	Envelope []byte `json:"envelope"`
}

// Service binds pairing, relay reads, and external navigation to the frontend.
type Service struct {
	mutex     sync.Mutex
	directory string
	client    *http.Client
	openURL   func(string) error
}

// newService configures private persistence and bounded relay requests.
func newService(directory string, openURL func(string) error) *Service {
	return &Service{directory: directory, openURL: openURL, client: &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
}

// parseLink validates a complete viewer link and isolates its native credentials.
func parseLink(link string) (storedPairing, error) {
	parsed, err := url.Parse(strings.TrimSpace(link))
	if err != nil || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Opaque != "" {
		return storedPairing{}, errors.New("invalid pairing link")
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && isLocalHost(parsed.Hostname())) {
		return storedPairing{}, errors.New("pairing links require HTTPS outside local networks")
	}
	identifier := strings.TrimPrefix(parsed.Path, "/v/")
	if parsed.Path != "/v/"+identifier || parsed.RawPath != "" || !validSpaceID.MatchString(identifier) {
		return storedPairing{}, errors.New("invalid pairing space")
	}
	key, err := base64.RawURLEncoding.Strict().DecodeString(parsed.Fragment)
	if err != nil || len(key) != 32 || strings.ContainsAny(parsed.Fragment, "\r\n") {
		return storedPairing{}, errors.New("invalid pairing key")
	}
	return storedPairing{Origin: parsed.Scheme + "://" + parsed.Host, ID: identifier, Key: parsed.Fragment}, nil
}

// isLocalHost permits cleartext only for loopback and literal private addresses.
func isLocalHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && (address.IsLoopback() || address.IsPrivate())
}

// readPairing reads and validates native credentials while the mutex is held.
func (service *Service) readPairing() (storedPairing, error) {
	data, err := os.ReadFile(filepath.Join(service.directory, pairingFile))
	if errors.Is(err, os.ErrNotExist) {
		return storedPairing{}, nil
	}
	if err != nil {
		return storedPairing{}, err
	}
	var pairing storedPairing
	if err = json.Unmarshal(data, &pairing); err != nil {
		return storedPairing{}, errors.New("invalid saved pairing")
	}
	validated, err := parseLink(pairing.Origin + "/v/" + pairing.ID + "#" + pairing.Key)
	if err != nil || validated != pairing {
		return storedPairing{}, errors.New("invalid saved pairing")
	}
	return pairing, nil
}

// GetPairing returns saved pairing metadata without exposing credentials.
func (service *Service) GetPairing() (Pairing, error) {
	service.mutex.Lock()
	defer service.mutex.Unlock()
	pairing, err := service.readPairing()
	return Pairing{IsPaired: pairing.ID != "", RelayOrigin: pairing.Origin}, err
}

// Pair verifies a candidate snapshot before replacing the saved link and encrypted cache.
func (service *Service) Pair(link string) (Pairing, error) {
	pairing, err := parseLink(link)
	if err != nil {
		return Pairing{}, err
	}
	snapshot, envelope, err := service.loadSnapshot(pairing, "")
	if err != nil {
		return Pairing{}, err
	}
	if snapshot.Status != "ok" {
		return Pairing{}, errors.New("could not connect to the new phone link")
	}
	data, err := json.Marshal(pairing)
	if err != nil {
		return Pairing{}, err
	}
	service.mutex.Lock()
	defer service.mutex.Unlock()
	if err = service.writePrivate(pairingFile, data); err != nil {
		return Pairing{}, err
	}
	cached, err := json.Marshal(cachedSnapshot{ID: pairing.ID, ETag: snapshot.ETag, Envelope: envelope})
	if err == nil {
		_ = service.writePrivate(snapshotFile, cached)
	}
	return Pairing{IsPaired: true, RelayOrigin: pairing.Origin}, nil
}

// writePrivate atomically replaces one file in private storage while the mutex is held.
func (service *Service) writePrivate(name string, data []byte) error {
	if err := os.MkdirAll(service.directory, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(service.directory, ".private-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	closeError := file.Close()
	if err != nil {
		return err
	}
	if closeError != nil {
		return closeError
	}
	return os.Rename(file.Name(), filepath.Join(service.directory, name))
}

// removePrivate deletes one private file while the mutex is held; a missing file succeeds.
func (service *Service) removePrivate(name string) error {
	err := os.Remove(filepath.Join(service.directory, name))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Unpair deletes saved credentials and the cached snapshot; an already unpaired app succeeds.
func (service *Service) Unpair() error {
	service.mutex.Lock()
	defer service.mutex.Unlock()
	if err := service.removePrivate(snapshotFile); err != nil {
		return err
	}
	return service.removePrivate(pairingFile)
}

// CachedSnapshot opens the last snapshot read from the relay, so a cold start shows it at once.
//
// It reports "empty" when nothing usable is cached for the current pairing.
func (service *Service) CachedSnapshot() (SnapshotResult, error) {
	service.mutex.Lock()
	defer service.mutex.Unlock()
	pairing, err := service.readPairing()
	if err != nil {
		return SnapshotResult{}, err
	}
	if pairing.ID == "" {
		return SnapshotResult{Status: "unpaired"}, nil
	}
	data, err := os.ReadFile(filepath.Join(service.directory, snapshotFile))
	var cached cachedSnapshot
	if err != nil || json.Unmarshal(data, &cached) != nil || cached.ID != pairing.ID {
		return SnapshotResult{Status: "empty"}, nil
	}
	payload, err := datasync.Open(cached.Envelope, pairing.Key, "space:"+pairing.ID)
	if err != nil || !json.Valid(payload) {
		return SnapshotResult{Status: "empty"}, nil
	}
	return SnapshotResult{Status: "ok", Payload: string(payload), ETag: cached.ETag}, nil
}

// cacheSnapshot keeps an authenticated envelope unless the pairing changed during the read.
func (service *Service) cacheSnapshot(pairing storedPairing, etag string, envelope []byte) {
	data, err := json.Marshal(cachedSnapshot{ID: pairing.ID, ETag: etag, Envelope: envelope})
	if err != nil {
		return
	}
	service.mutex.Lock()
	defer service.mutex.Unlock()
	if current, err := service.readPairing(); err == nil && current == pairing {
		_ = service.writePrivate(snapshotFile, data)
	}
}

// LoadSnapshot conditionally fetches, authenticates, and decompresses the snapshot.
func (service *Service) LoadSnapshot(etag string) (SnapshotResult, error) {
	service.mutex.Lock()
	pairing, err := service.readPairing()
	service.mutex.Unlock()
	if err != nil {
		return SnapshotResult{}, err
	}
	if pairing.ID == "" {
		return SnapshotResult{Status: "unpaired"}, nil
	}
	result, envelope, err := service.loadSnapshot(pairing, etag)
	if err == nil && result.Status == "ok" {
		service.cacheSnapshot(pairing, result.ETag, envelope)
	}
	return result, err
}

// loadSnapshot authenticates a specific candidate without changing saved credentials or cache.
func (service *Service) loadSnapshot(pairing storedPairing, etag string) (SnapshotResult, []byte, error) {
	request, err := http.NewRequest(http.MethodGet, pairing.Origin+"/api/spaces/"+pairing.ID, nil)
	if err != nil {
		return SnapshotResult{}, nil, err
	}
	request.Header.Set("Cache-Control", "no-store")
	if etag != "" {
		request.Header.Set("If-None-Match", etag)
	}
	response, err := service.client.Do(request)
	if err != nil {
		return SnapshotResult{Status: "problem", Problem: "network"}, nil, nil
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusNotModified:
		return SnapshotResult{Status: "unchanged", ETag: response.Header.Get("ETag")}, nil, nil
	case http.StatusNotFound:
		return SnapshotResult{Status: "problem", Problem: "missing"}, nil, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return SnapshotResult{Status: "problem", Problem: "network"}, nil, nil
	}
	envelope, err := io.ReadAll(io.LimitReader(response.Body, maxEnvelopeBytes+1))
	if err != nil {
		return SnapshotResult{Status: "problem", Problem: "network"}, nil, nil
	}
	if len(envelope) > maxEnvelopeBytes {
		return SnapshotResult{Status: "problem", Problem: "decrypt"}, nil, nil
	}
	payload, err := datasync.Open(envelope, pairing.Key, "space:"+pairing.ID)
	if errors.Is(err, datasync.ErrEnvelopeVersion) {
		return SnapshotResult{Status: "problem", Problem: "version"}, nil, nil
	}
	if err != nil || !json.Valid(payload) {
		return SnapshotResult{Status: "problem", Problem: "decrypt"}, nil, nil
	}
	etag = response.Header.Get("ETag")
	return SnapshotResult{Status: "ok", Payload: string(payload), ETag: etag}, envelope, nil
}

// OpenURL opens an HTTPS destination through the native Android browser bridge.
func (service *Service) OpenURL(destination string) error {
	parsed, err := url.Parse(destination)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.Opaque != "" {
		return errors.New("only HTTPS links can be opened")
	}
	if service.openURL == nil {
		return errors.New("native browser is unavailable")
	}
	return service.openURL(destination)
}
