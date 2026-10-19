package mobile

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestStartupClaimIsSingleAndNonblocking keeps the winning event loop blocked while
// every later invocation must return, as repeated JNI Activity launches require.
func TestStartupClaimIsSingleAndNonblocking(test *testing.T) {
	var claim startupClaim
	var invocations atomic.Int32
	started, release, returned := make(chan struct{}), make(chan struct{}), make(chan struct{}, 64)
	var workers sync.WaitGroup
	workers.Add(64)
	for range 64 {
		go func() {
			defer workers.Done()
			if claim.tryClaim() {
				invocations.Add(1)
				close(started)
				<-release
			}
			returned <- struct{}{}
		}()
	}
	defer func() { close(release); workers.Wait() }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		test.Fatal("no startup claimed")
	}
	for range 63 {
		select {
		case <-returned:
		case <-time.After(3 * time.Second):
			test.Fatal("duplicate startup waited for the active native loop")
		}
	}
	if invocations.Load() != 1 || claim.tryClaim() {
		test.Fatal("multiple native startups were admitted")
	}
}
