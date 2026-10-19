package mobile

import (
	"bytes"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	datasync "yousage/internal/sync"
)

// snapshotTransport allows deterministic HTTP completion order without real accounts.
type snapshotTransport func(*http.Request) (*http.Response, error)

// RoundTrip runs one synthetic relay request.
func (transport snapshotTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

// TestInFlightSnapshotCannotOutlivePairingOrNewerRead exercises stale completion races.
func TestInFlightSnapshotCannotOutlivePairingOrNewerRead(test *testing.T) {
	for _, operation := range []string{"newer read", "repaired same link", "unpaired"} {
		test.Run(operation, func(test *testing.T) {
			viewer, relay := newSnapshotRelay(test)
			olderEnvelope, err := datasync.Seal([]byte(snapshotFixture(test, "older-B")), relay.key, "space:"+relay.identifier)
			if err != nil {
				test.Fatal(err)
			}
			newerEnvelope, err := datasync.Seal([]byte(snapshotFixture(test, "newer-C")), relay.key, "space:"+relay.identifier)
			if err != nil {
				test.Fatal(err)
			}
			started, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			defer releaseOnce.Do(func() { close(release) })
			var requests atomic.Int32
			viewer.client.Transport = snapshotTransport(func(request *http.Request) (*http.Response, error) {
				envelope := newerEnvelope
				if requests.Add(1) == 1 {
					close(started)
					select {
					case <-release:
					case <-request.Context().Done():
						return nil, request.Context().Err()
					}
					envelope = olderEnvelope
				}
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Etag": []string{`"reused"`}}, Body: io.NopCloser(bytes.NewReader(envelope))}, nil
			})
			type completion struct {
				result SnapshotResult
				err    error
			}
			completed := make(chan completion, 1)
			go func() { result, err := viewer.LoadSnapshot(""); completed <- completion{result, err} }()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				test.Fatal("older request did not start")
			}
			var candidate SnapshotResult
			switch operation {
			case "newer read":
				candidate = loadCandidate(test, viewer)
			case "repaired same link":
				if _, err := viewer.Pair(relay.link()); err != nil {
					test.Fatal(err)
				}
			case "unpaired":
				if err := viewer.Unpair(); err != nil {
					test.Fatal(err)
				}
			}
			releaseOnce.Do(func() { close(release) })
			select {
			case older := <-completed:
				if older.err != nil || older.result.Status != "problem" || older.result.Problem != "stale" || older.result.CandidateId != "" || older.result.Payload != "" {
					test.Fatalf("stale response was staged: status=%s problem=%s error=%v", older.result.Status, older.result.Problem, older.err)
				}
			case <-time.After(3 * time.Second):
				test.Fatal("older request did not finish")
			}
			switch operation {
			case "newer read":
				if err := viewer.CommitSnapshot(candidate.CandidateId); err != nil {
					test.Fatal(err)
				}
				acceptedSnapshot(test, viewer, snapshotFixture(test, "newer-C"), `"reused"`)
			case "repaired same link":
				acceptedSnapshot(test, viewer, snapshotFixture(test, "newer-C"), `"reused"`)
			case "unpaired":
				if result, err := viewer.CachedSnapshot(); err != nil || result.Status != "unpaired" {
					test.Fatalf("late response restored pairing: %s %v", result.Status, err)
				}
			}
		})
	}
}
