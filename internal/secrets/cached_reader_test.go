package secrets

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestSharedPromptSurvivesCallerTimeoutAndCaches(t *testing.T) {
	var calls atomic.Int32
	started, finish := make(chan struct{}), make(chan struct{})
	reader := NewCachedReader(func(ctx context.Context) (string, error) {
		calls.Add(1)
		close(started)
		select {
		case <-finish:
			return "owned-token", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	})
	defer reader.Close()
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { _, err := reader.Read(ctx); first <- err }()
	<-started
	cancel()
	if err := <-first; err != context.Canceled {
		t.Fatal(err)
	}
	second := make(chan string, 1)
	go func() {
		token, err := reader.Read(context.Background())
		if err != nil {
			second <- "error"
		} else {
			second <- token
		}
	}()
	close(finish)
	if token := <-second; token != "owned-token" || calls.Load() != 1 {
		t.Fatal(token, calls.Load())
	}
	if token, err := reader.Read(context.Background()); err != nil || token != "owned-token" || calls.Load() != 1 {
		t.Fatal(token, err)
	}
}

func TestCloseStopsPendingReadAndRejectsNewReads(t *testing.T) {
	started := make(chan struct{})
	reader := NewCachedReader(func(ctx context.Context) (string, error) { close(started); <-ctx.Done(); return "", ctx.Err() })
	finished := make(chan error, 1)
	go func() { _, err := reader.Read(context.Background()); finished <- err }()
	<-started
	reader.Close()
	select {
	case err := <-finished:
		if err != context.Canceled {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("read survived shutdown")
	}
	if _, err := reader.Read(context.Background()); err != context.Canceled {
		t.Fatal(err)
	}
}

func TestFiveMinuteExpiryAndEmptyResultNotCached(t *testing.T) {
	now := time.Unix(0, 0)
	var calls int
	reader := NewCachedReader(func(context.Context) (string, error) {
		calls++
		if calls == 1 {
			return "", nil
		}
		return "token", nil
	})
	reader.now = func() time.Time { return now }
	defer reader.Close()
	if token, err := reader.Read(context.Background()); err != nil || token != "" {
		t.Fatal(token, err)
	}
	if token, err := reader.Read(context.Background()); err != nil || token != "token" || calls != 2 {
		t.Fatal(token, err, calls)
	}
	now = now.Add(5*time.Minute - time.Nanosecond)
	if _, err := reader.Read(context.Background()); err != nil || calls != 2 {
		t.Fatal(err, calls)
	}
	now = now.Add(time.Nanosecond)
	if _, err := reader.Read(context.Background()); err != nil || calls != 3 {
		t.Fatal(err, calls)
	}
}
