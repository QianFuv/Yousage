package refresh

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type interleavingContext struct {
	context.Context
	onDone       func()
	onSecondDone func()
	calls        int
}

func (ctx *interleavingContext) Done() <-chan struct{} {
	ctx.calls++
	if ctx.calls == 1 {
		ctx.onDone()
	}
	if ctx.calls == 2 && ctx.onSecondDone != nil {
		ctx.onSecondDone()
	}
	return nil
}

func automatic() Request { return Request{IsVisible: true, IsUsagePage: true, IsAutoEnabled: true} }

func TestBackgroundOnlyBypassesVisibilityAndPageGates(t *testing.T) {
	for _, test := range []struct {
		name    string
		request Request
		canRun  bool
	}{
		{"background while hidden outside Usage", Request{IsBackground: true, IsAutoEnabled: true}, true},
		{"background with auto refresh disabled", Request{IsBackground: true}, false},
		{"ordinary hidden request", Request{IsUsagePage: true, IsAutoEnabled: true}, false},
		{"ordinary request outside Usage", Request{IsVisible: true, IsAutoEnabled: true}, false},
		{"ordinary request with auto refresh disabled", Request{IsVisible: true, IsUsagePage: true}, false},
		{"ordinary visible Usage request", automatic(), true},
		{"manual request remains unconditional", Request{IsManual: true}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			coordinator := New[int](HistoryPolicy)
			defer coordinator.Close()
			calls := 0
			_, err := coordinator.Run(context.Background(), "local", test.request, func(context.Context) (int, error) {
				calls++
				return 7, nil
			})
			if test.canRun {
				if err != nil || calls != 1 {
					t.Fatalf("eligible request: calls=%d, error=%v", calls, err)
				}
			} else if !errors.Is(err, ErrSkipped) || calls != 0 {
				t.Fatalf("ineligible request started work: calls=%d, error=%v", calls, err)
			}
		})
	}
}

func TestBackgroundRespectsFreshnessAndCompletionCooldown(t *testing.T) {
	for _, test := range []struct {
		name           string
		policy         Policy
		operationError error
	}{
		{"fresh success", HistoryPolicy, nil},
		{"success cooldown", LimitsPolicy, nil},
		{"failure cooldown", LimitsPolicy, errors.New("offline")},
	} {
		t.Run(test.name, func(t *testing.T) {
			coordinator := New[int](test.policy)
			defer coordinator.Close()
			now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
			coordinator.clock = func() time.Time { return now }
			calls := 0
			operation := func(context.Context) (int, error) {
				calls++
				now = now.Add(time.Minute)
				return calls, test.operationError
			}
			request := Request{IsBackground: true, IsAutoEnabled: true}
			if _, err := coordinator.Run(context.Background(), "local", request, operation); !errors.Is(err, test.operationError) {
				t.Fatal(err)
			}
			interval := max(test.policy.StaleAfter, test.policy.CompletionCooldown)
			now = now.Add(interval - time.Second)
			if _, err := coordinator.Run(context.Background(), "local", request, operation); !errors.Is(err, ErrSkipped) || calls != 1 {
				t.Fatalf("completion gate ignored: calls=%d, error=%v", calls, err)
			}
			now = now.Add(time.Second)
			if _, err := coordinator.Run(context.Background(), "local", request, operation); !errors.Is(err, test.operationError) || calls != 2 {
				t.Fatalf("expired gate still blocked: calls=%d, error=%v", calls, err)
			}
		})
	}
}

func TestCooldownAtCompletionFailureRetentionAndManualBypass(t *testing.T) {
	coordinator := New[int](LimitsPolicy)
	defer coordinator.Close()
	now := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	coordinator.clock = func() time.Time { return now }
	ctx := context.Background()
	success := func(context.Context) (int, error) { now = now.Add(2 * time.Minute); return 42, nil }
	snapshot, err := coordinator.Run(ctx, "local", automatic(), success)
	if err != nil || snapshot.Value != 42 || snapshot.CompletedAt != now {
		t.Fatal(snapshot, err)
	}
	now = now.Add(4 * time.Minute)
	if _, err := coordinator.Run(ctx, "local", automatic(), success); !errors.Is(err, ErrSkipped) {
		t.Fatal("cooldown started at request rather than completion", err)
	}
	failure := errors.New("offline")
	snapshot, err = coordinator.Run(ctx, "local", Request{IsManual: true}, func(context.Context) (int, error) { return 0, failure })
	if !errors.Is(err, failure) || snapshot.Value != 42 || !snapshot.HasValue || snapshot.SucceededAt == snapshot.CompletedAt {
		t.Fatal("failed refresh destroyed success", snapshot, err)
	}
	if _, err := coordinator.Run(ctx, "local", automatic(), success); !errors.Is(err, ErrSkipped) {
		t.Fatal("failure cooldown missing", err)
	}
	now = now.Add(5 * time.Minute)
	if _, err := coordinator.Run(ctx, "local", automatic(), func(context.Context) (int, error) { return 43, nil }); err != nil {
		t.Fatal(err)
	}
}

func TestPendingCoalescingHiddenPolicyAndOwnedCancellation(t *testing.T) {
	coordinator := New[int](HistoryPolicy)
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	operation := func(ctx context.Context) (int, error) {
		calls.Add(1)
		close(started)
		select {
		case <-release:
			return 7, nil
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	first := make(chan error, 1)
	go func() {
		_, err := coordinator.Run(context.Background(), "local", Request{IsManual: true}, operation)
		first <- err
	}()
	<-started
	if _, err := coordinator.Run(context.Background(), "local", automatic(), operation); !errors.Is(err, ErrSkipped) {
		t.Fatal("automatic pending should skip", err)
	}
	waiter, stopWaiting := context.WithTimeout(context.Background(), 20*time.Millisecond)
	if _, err := coordinator.Run(waiter, "local", Request{IsManual: true}, operation); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("manual waiter did not share pending operation", err)
	}
	stopWaiting()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := coordinator.Run(cancelled, "local", Request{IsManual: true}, operation); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal(calls.Load())
	}
	request := automatic()
	request.IsUsagePage = false
	if _, err := coordinator.Run(context.Background(), "new", request, operation); !errors.Is(err, ErrSkipped) {
		t.Fatal("Settings started auto request", err)
	}
	if _, err := coordinator.Run(context.Background(), "local", automatic(), operation); !errors.Is(err, ErrSkipped) {
		t.Fatal("history stale interval ignored", err)
	}
	coordinator.Close()
	if _, err := coordinator.Run(context.Background(), "local", Request{IsManual: true}, operation); !errors.Is(err, context.Canceled) {
		t.Fatal("closed coordinator started process", err)
	}
}

func TestAfterPendingRunsAgainEvenAfterFailure(t *testing.T) {
	coordinator := New[int](LimitsPolicy)
	defer coordinator.Close()
	started, release := make(chan struct{}), make(chan struct{})
	go coordinator.Run(context.Background(), "local", Request{IsManual: true}, func(context.Context) (int, error) { close(started); <-release; return 0, errors.New("first failed") })
	<-started
	result := make(chan int, 1)
	go func() {
		snapshot, err := coordinator.Run(context.Background(), "local", Request{IsManual: true, AfterPending: true}, func(context.Context) (int, error) { return 9, nil })
		if err != nil {
			result <- -1
		} else {
			result <- snapshot.Value
		}
	}()
	close(release)
	if value := <-result; value != 9 {
		t.Fatal(value)
	}
}

func TestRateManualFloorDoesNotAdvanceLastSuccess(t *testing.T) {
	coordinator := New[int](RatesPolicy)
	defer coordinator.Close()
	now := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	coordinator.clock = func() time.Time { return now }
	var calls int
	operation := func(context.Context) (int, error) { calls++; return calls, nil }
	first, err := coordinator.Run(context.Background(), "rates", Request{IsManual: true}, operation)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(30 * time.Second)
	second, err := coordinator.Run(context.Background(), "rates", Request{IsManual: true}, operation)
	if err != nil || calls != 1 || second.SucceededAt != first.SucceededAt {
		t.Fatal(second, err, calls)
	}
	now = now.Add(30 * time.Second)
	if _, err := coordinator.Run(context.Background(), "rates", Request{IsManual: true}, operation); err != nil || calls != 2 {
		t.Fatal(err, calls)
	}
}

func TestConfigurationRefreshWaitsPastAnInterveningOldRequest(t *testing.T) {
	coordinator := New[int](LimitsPolicy)
	defer coordinator.Close()
	completed := &flight[int]{done: make(chan struct{}), snapshot: Snapshot[int]{Value: 1, HasValue: true}}
	close(completed.done)
	held := &entry[int]{pending: completed}
	coordinator.entries["local"] = held
	ctx := &interleavingContext{Context: context.Background(), onSecondDone: func() { coordinator.mutex.Lock(); held.pending = nil; coordinator.mutex.Unlock() }, onDone: func() {
		older := &flight[int]{done: make(chan struct{}), snapshot: Snapshot[int]{Value: 2, HasValue: true}}
		close(older.done)
		coordinator.mutex.Lock()
		held.pending = older
		coordinator.mutex.Unlock()
	}}
	didReadNewConfiguration := false
	snapshot, err := coordinator.Run(ctx, "local", Request{IsManual: true, AfterPending: true}, func(context.Context) (int, error) { didReadNewConfiguration = true; return 3, nil })
	if err != nil || !didReadNewConfiguration || snapshot.Value != 3 {
		t.Fatal("new configuration joined old work", snapshot, err)
	}
}
