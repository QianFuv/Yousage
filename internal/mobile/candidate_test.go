package mobile

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	datasync "yousage/internal/sync"
)

// snapshotRelay serves synthetic authenticated snapshots with independently chosen ETags.
type snapshotRelay struct {
	mutex       sync.Mutex
	key         string
	identifier  string
	payload     string
	etag        string
	conditional string
	server      *httptest.Server
}

// newSnapshotRelay pairs a temporary viewer with accepted A and registers server cleanup.
func newSnapshotRelay(test *testing.T) (*Service, *snapshotRelay) {
	test.Helper()
	key, err := datasync.NewKey()
	if err != nil {
		test.Fatal(err)
	}
	relay := &snapshotRelay{key: key, identifier: strings.Repeat("A", 22), payload: snapshotFixture(test, "A"), etag: `"A"`}
	relay.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		relay.mutex.Lock()
		defer relay.mutex.Unlock()
		relay.conditional = request.Header.Get("If-None-Match")
		envelope, err := datasync.Seal([]byte(relay.payload), relay.key, "space:"+relay.identifier)
		if err != nil {
			test.Error(err)
			writer.WriteHeader(500)
			return
		}
		writer.Header().Set("ETag", relay.etag)
		writer.Write(envelope)
	}))
	test.Cleanup(relay.server.Close)
	viewer := newService(test.TempDir(), nil)
	if _, err := viewer.Pair(relay.link()); err != nil {
		test.Fatal(err)
	}
	return viewer, relay
}

// link returns a synthetic local relay link for this fixture only.
func (relay *snapshotRelay) link() string {
	return relay.server.URL + "/v/" + relay.identifier + "#" + relay.key
}

// set changes the relay response under its handler lock.
func (relay *snapshotRelay) set(payload, etag string) {
	relay.mutex.Lock()
	defer relay.mutex.Unlock()
	relay.payload, relay.etag = payload, etag
}

// acceptedSnapshot checks cold-start persistence independently of in-memory candidates.
func acceptedSnapshot(test *testing.T, viewer *Service, payload, etag string) {
	test.Helper()
	result, err := newService(viewer.directory, nil).CachedSnapshot()
	if err != nil || result.Status != "ok" || result.Payload != payload || result.ETag != etag || result.CandidateId != "" {
		test.Fatalf("accepted cache mismatch: status=%s etag=%s candidate=%s error=%v", result.Status, result.ETag, result.CandidateId, err)
	}
}

// loadCandidate requires a usable candidate identity without persisting the snapshot.
func loadCandidate(test *testing.T, viewer *Service) SnapshotResult {
	test.Helper()
	result, err := viewer.LoadSnapshot("")
	if err != nil || result.Status != "ok" || result.CandidateId == "" {
		test.Fatalf("candidate missing: status=%s problem=%s error=%v", result.Status, result.Problem, err)
	}
	return result
}

// TestCandidateRequiresAcknowledgement covers A -> unaccepted B -> invalid B -> accepted C.
func TestCandidateRequiresAcknowledgement(test *testing.T) {
	viewer, relay := newSnapshotRelay(test)
	accepted := snapshotFixture(test, "A")
	before, err := os.ReadFile(filepath.Join(viewer.directory, snapshotFile))
	if err != nil {
		test.Fatal(err)
	}
	relay.set(snapshotFixture(test, "B"), `"B"`)
	candidate := loadCandidate(test, viewer)
	after, err := os.ReadFile(filepath.Join(viewer.directory, snapshotFile))
	if err != nil || !bytes.Equal(before, after) {
		test.Fatal("unacknowledged B changed persistent cache")
	}
	acceptedSnapshot(test, viewer, accepted, `"A"`)
	for _, identity := range []string{"", "unknown"} {
		if viewer.CommitSnapshot(identity) == nil {
			test.Fatal("invalid acknowledgement succeeded")
		}
	}
	relay.set(`{"formatVersion":99}`, `"B"`)
	if result, err := viewer.LoadSnapshot(`"A"`); err != nil || result.Problem != "version" || result.CandidateId != "" {
		test.Fatalf("invalid B: status=%s problem=%s error=%v", result.Status, result.Problem, err)
	}
	if viewer.CommitSnapshot(candidate.CandidateId) == nil {
		test.Fatal("rejected replacement left old staged B committable")
	}
	acceptedSnapshot(test, viewer, accepted, `"A"`)
	relay.set(snapshotFixture(test, "C"), `"C"`)
	candidate = loadCandidate(test, viewer)
	if err := viewer.CommitSnapshot(candidate.CandidateId); err != nil {
		test.Fatal(err)
	}
	if viewer.CommitSnapshot(candidate.CandidateId) == nil {
		test.Fatal("candidate acknowledgement was reusable")
	}
	acceptedSnapshot(test, viewer, snapshotFixture(test, "C"), `"C"`)
}

// TestCandidateIdentityIgnoresETag rejects superseded generations with missing or reused tags.
func TestCandidateIdentityIgnoresETag(test *testing.T) {
	for _, etag := range []string{"", `"reused"`} {
		test.Run(etag, func(test *testing.T) {
			viewer, relay := newSnapshotRelay(test)
			relay.set(snapshotFixture(test, "B"), etag)
			first := loadCandidate(test, viewer)
			relay.set(snapshotFixture(test, "C"), etag)
			second := loadCandidate(test, viewer)
			if first.CandidateId == second.CandidateId {
				test.Fatal("ETag reused candidate identity")
			}
			if viewer.CommitSnapshot(first.CandidateId) == nil {
				test.Fatal("superseded generation accepted")
			}
			if newService(viewer.directory, nil).CommitSnapshot(second.CandidateId) == nil {
				test.Fatal("candidate survived a service restart")
			}
			if err := viewer.CommitSnapshot(second.CandidateId); err != nil {
				test.Fatal(err)
			}
			acceptedSnapshot(test, viewer, snapshotFixture(test, "C"), etag)
		})
	}
}

// TestInvalidSavedCacheRecovery retains invalid bytes until an unconditional valid fetch is accepted.
func TestInvalidSavedCacheRecovery(test *testing.T) {
	viewer, relay := newSnapshotRelay(test)
	bad, err := datasync.Seal([]byte(`{"formatVersion":1,"periods":null}`), relay.key, "space:"+relay.identifier)
	if err != nil {
		test.Fatal(err)
	}
	saved, err := json.Marshal(cachedSnapshot{ID: relay.identifier, ETag: `"bad"`, Envelope: bad})
	if err != nil {
		test.Fatal(err)
	}
	path := filepath.Join(viewer.directory, snapshotFile)
	if err := os.WriteFile(path, saved, 0600); err != nil {
		test.Fatal(err)
	}
	beforePairing, err := os.ReadFile(filepath.Join(viewer.directory, pairingFile))
	if err != nil {
		test.Fatal(err)
	}
	result, err := viewer.CachedSnapshot()
	if err != nil || result.Status != "problem" || result.Problem != "data" || result.ETag != "" || result.Payload != "" || result.CandidateId != "" {
		test.Fatalf("invalid cache not recoverable: %+v %v", result, err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(saved, after) {
		test.Fatal("invalid cache read deleted or changed saved data")
	}
	relay.set(snapshotFixture(test, "C"), `"C"`)
	candidate := loadCandidate(test, viewer)
	relay.mutex.Lock()
	conditional := relay.conditional
	relay.mutex.Unlock()
	if conditional != "" {
		test.Fatal("recovery sent a rejected cache ETag")
	}
	if err := viewer.CommitSnapshot(candidate.CandidateId); err != nil {
		test.Fatal(err)
	}
	acceptedSnapshot(test, viewer, snapshotFixture(test, "C"), `"C"`)
	afterPairing, err := os.ReadFile(filepath.Join(viewer.directory, pairingFile))
	if err != nil || !bytes.Equal(beforePairing, afterPairing) {
		test.Fatal("recovery changed pairing")
	}
}

// TestCandidateWriteFailure returns a safe failure and leaves A accepted with B retryable.
func TestCandidateWriteFailure(test *testing.T) {
	viewer, relay := newSnapshotRelay(test)
	relay.set(snapshotFixture(test, "B"), `"B"`)
	candidate := loadCandidate(test, viewer)
	path := filepath.Join(viewer.directory, snapshotFile)
	accepted, err := os.ReadFile(path)
	if err != nil {
		test.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		test.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		test.Fatal(err)
	}
	if err := viewer.CommitSnapshot(candidate.CandidateId); err == nil || err.Error() != "could not save the phone snapshot" {
		test.Fatalf("write failure was not safe: %v", err)
	}
	if err := os.Remove(path); err != nil {
		test.Fatal(err)
	}
	if err := os.WriteFile(path, accepted, 0600); err != nil {
		test.Fatal(err)
	}
	acceptedSnapshot(test, viewer, snapshotFixture(test, "A"), `"A"`)
	if err := viewer.CommitSnapshot(candidate.CandidateId); err != nil {
		test.Fatal(err)
	}
	acceptedSnapshot(test, viewer, snapshotFixture(test, "B"), `"B"`)
}

// TestPairingChangesInvalidateCandidate includes an ABA change back to identical credentials.
func TestPairingChangesInvalidateCandidate(test *testing.T) {
	for _, operation := range []string{"pair", "unpair and pair", "external key change"} {
		test.Run(operation, func(test *testing.T) {
			viewer, relay := newSnapshotRelay(test)
			candidate := loadCandidate(test, viewer)
			switch operation {
			case "pair":
				if _, err := viewer.Pair(relay.link()); err != nil {
					test.Fatal(err)
				}
			case "unpair and pair":
				if err := viewer.Unpair(); err != nil {
					test.Fatal(err)
				}
				if _, err := viewer.Pair(relay.link()); err != nil {
					test.Fatal(err)
				}
			case "external key change":
				key, err := datasync.NewKey()
				if err != nil {
					test.Fatal(err)
				}
				pairing := storedPairing{Origin: relay.server.URL, ID: relay.identifier, Key: key}
				encoded, _ := json.Marshal(pairing)
				if err := os.WriteFile(filepath.Join(viewer.directory, pairingFile), encoded, 0600); err != nil {
					test.Fatal(err)
				}
			}
			if viewer.CommitSnapshot(candidate.CandidateId) == nil {
				test.Fatal("changed pairing accepted old candidate")
			}
		})
	}
}
