// Package mobile provides the native Android read-only snapshot viewer.
package mobile

import (
	"crypto/rand"
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
	Status      string `json:"status"`
	Problem     string `json:"problem"`
	Payload     string `json:"payload"`
	ETag        string `json:"etag"`
	CandidateId string `json:"candidateId"`
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

// snapshotCandidate retains only one bounded encrypted envelope until viewer acceptance.
type snapshotCandidate struct {
	id         string
	pairing    storedPairing
	generation uint64
	cached     cachedSnapshot
}

// Service binds pairing, relay reads, and external navigation to the frontend.
type Service struct {
	mutex             sync.Mutex
	directory         string
	client            *http.Client
	openURL           func(string) error
	candidate         *snapshotCandidate
	generation        uint64
	requestGeneration uint64
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
	service.mutex.Lock()
	generation := service.generation
	service.mutex.Unlock()
	snapshot, envelope, err := service.loadSnapshot(pairing, "")
	if err != nil {
		return Pairing{}, err
	}
	if snapshot.Status != "ok" {
		return Pairing{}, errors.New("could not connect to the new phone link")
	}
	data, _ := json.Marshal(pairing)
	service.mutex.Lock()
	defer service.mutex.Unlock()
	if service.generation != generation {
		return Pairing{}, errors.New("pairing changed; retry the phone link")
	}
	cached, _ := json.Marshal(cachedSnapshot{ID: pairing.ID, ETag: snapshot.ETag, Envelope: envelope})
	previous, readError := os.ReadFile(filepath.Join(service.directory, snapshotFile))
	if readError != nil && !errors.Is(readError, os.ErrNotExist) {
		return Pairing{}, errors.New("could not save the phone snapshot")
	}
	if err = service.writePrivate(snapshotFile, cached); err != nil {
		return Pairing{}, errors.New("could not save the phone snapshot")
	}
	if err = service.writePrivate(pairingFile, data); err != nil {
		if readError == nil {
			if service.writePrivate(snapshotFile, previous) != nil {
				return Pairing{}, errors.New("could not restore the saved phone snapshot")
			}
		}
		return Pairing{}, errors.New("could not save the phone link")
	}
	service.generation++
	service.candidate = nil
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
	service.generation++
	service.candidate = nil
	if err := service.removePrivate(snapshotFile); err != nil {
		return err
	}
	return service.removePrivate(pairingFile)
}

// CachedSnapshot opens only the accepted last-good snapshot, without exposing a candidate ID.
//
// Invalid saved data reports a problem or empty result with no ETag, allowing an
// unconditional refetch while preserving the saved file and pairing.
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
	if err != nil {
		return SnapshotResult{Status: "empty"}, nil
	}
	if problem := snapshotProblem(payload); problem != "" {
		return SnapshotResult{Status: "problem", Problem: problem}, nil
	}
	return SnapshotResult{Status: "ok", Payload: string(payload), ETag: cached.ETag}, nil
}

// CommitSnapshot persists exactly the current candidate after successful viewer application.
// A failed write leaves it retryable and never advances the accepted envelope or ETag.
func (service *Service) CommitSnapshot(candidateId string) error {
	service.mutex.Lock()
	defer service.mutex.Unlock()
	candidate := service.candidate
	if candidateId == "" || candidate == nil || candidate.id != candidateId {
		return errors.New("snapshot candidate is no longer available")
	}
	current, err := service.readPairing()
	if err != nil || current != candidate.pairing || candidate.generation != service.generation {
		service.candidate = nil
		return errors.New("pairing changed; reload the phone snapshot")
	}
	data, _ := json.Marshal(candidate.cached)
	if service.writePrivate(snapshotFile, data) != nil {
		return errors.New("could not save the phone snapshot")
	}
	service.candidate = nil
	return nil
}

// LoadSnapshot validates and stages one candidate without changing the accepted cache.
// Every new request supersedes the previous candidate, independently of relay ETags.
func (service *Service) LoadSnapshot(etag string) (SnapshotResult, error) {
	service.mutex.Lock()
	pairing, err := service.readPairing()
	service.requestGeneration++
	requestGeneration := service.requestGeneration
	generation := service.generation
	service.candidate = nil
	service.mutex.Unlock()
	if err != nil {
		return SnapshotResult{}, err
	}
	if pairing.ID == "" {
		return SnapshotResult{Status: "unpaired"}, nil
	}
	result, envelope, err := service.loadSnapshot(pairing, etag)
	if err != nil {
		return result, err
	}
	service.mutex.Lock()
	defer service.mutex.Unlock()
	current, readError := service.readPairing()
	if readError != nil || current != pairing || service.generation != generation || service.requestGeneration != requestGeneration {
		return SnapshotResult{Status: "problem", Problem: "stale"}, nil
	}
	if result.Status != "ok" {
		return result, nil
	}
	var identity [16]byte
	if _, err := rand.Read(identity[:]); err != nil {
		return SnapshotResult{}, errors.New("could not stage the phone snapshot")
	}
	result.CandidateId = base64.RawURLEncoding.EncodeToString(identity[:])
	service.candidate = &snapshotCandidate{
		id: result.CandidateId, pairing: pairing, generation: generation,
		cached: cachedSnapshot{ID: pairing.ID, ETag: result.ETag, Envelope: envelope},
	}
	return result, nil
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
	if err != nil {
		return SnapshotResult{Status: "problem", Problem: "decrypt"}, nil, nil
	}
	if problem := snapshotProblem(payload); problem != "" {
		return SnapshotResult{Status: "problem", Problem: problem}, nil, nil
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
