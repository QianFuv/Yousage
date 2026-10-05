// Package refresh coalesces operations and retains last-success values across failed attempts.
package refresh

import (
	"context"
	"errors"
	"sync"
	"time"
)

var ErrSkipped = errors.New("automatic refresh skipped")

type Policy struct{ StaleAfter, CompletionCooldown, ManualFloor time.Duration }

var HistoryPolicy = Policy{StaleAfter: time.Minute}
var LimitsPolicy = Policy{CompletionCooldown: 5 * time.Minute}
var RatesPolicy = Policy{StaleAfter: 24 * time.Hour, ManualFloor: time.Minute}

type Request struct{ IsManual, IsVisible, IsUsagePage, IsAutoEnabled, AfterPending bool }
type Snapshot[T any] struct {
	Value                                 T
	HasValue                              bool
	AttemptedAt, SucceededAt, CompletedAt time.Time
	Error                                 error
}
type flight[T any] struct {
	done     chan struct{}
	snapshot Snapshot[T]
}
type entry[T any] struct {
	snapshot Snapshot[T]
	pending  *flight[T]
}
type Coordinator[T any] struct {
	mutex    sync.Mutex
	entries  map[string]*entry[T]
	policy   Policy
	clock    func() time.Time
	context  context.Context
	cancel   context.CancelFunc
	isClosed bool
	wait     sync.WaitGroup
}

func New[T any](policy Policy) *Coordinator[T] {
	ctx, cancel := context.WithCancel(context.Background())
	return &Coordinator[T]{entries: map[string]*entry[T]{}, policy: policy, clock: time.Now, context: ctx, cancel: cancel}
}

// Run never starts background work while hidden or in Settings; explicit requests bypass cooldown.
func (coordinator *Coordinator[T]) Run(ctx context.Context, key string, request Request, operation func(context.Context) (T, error)) (Snapshot[T], error) {
	if err := ctx.Err(); err != nil {
		return Snapshot[T]{}, err
	}
	coordinator.mutex.Lock()
	if coordinator.isClosed {
		coordinator.mutex.Unlock()
		return Snapshot[T]{}, context.Canceled
	}
	held := coordinator.entries[key]
	if held == nil {
		held = &entry[T]{}
		coordinator.entries[key] = held
	}
	if pending := held.pending; pending != nil {
		coordinator.mutex.Unlock()
		if !request.IsManual && !request.AfterPending {
			return Snapshot[T]{}, ErrSkipped
		}
		select {
		case <-ctx.Done():
			return Snapshot[T]{}, ctx.Err()
		case <-pending.done:
		}
		if request.AfterPending {
			request.IsManual = true
			return coordinator.Run(ctx, key, request, operation)
		}
		return pending.snapshot, pending.snapshot.Error
	}
	now := coordinator.clock()
	if request.IsManual && coordinator.policy.ManualFloor > 0 && held.snapshot.HasValue && now.Sub(held.snapshot.SucceededAt) < coordinator.policy.ManualFloor {
		snapshot := held.snapshot
		coordinator.mutex.Unlock()
		return snapshot, nil
	}
	if !request.IsManual && (!request.IsVisible || !request.IsUsagePage || !request.IsAutoEnabled || (coordinator.policy.CompletionCooldown > 0 && !held.snapshot.CompletedAt.IsZero() && now.Sub(held.snapshot.CompletedAt) < coordinator.policy.CompletionCooldown) || (coordinator.policy.StaleAfter > 0 && held.snapshot.HasValue && now.Sub(held.snapshot.SucceededAt) < coordinator.policy.StaleAfter)) {
		snapshot := held.snapshot
		coordinator.mutex.Unlock()
		return snapshot, ErrSkipped
	}
	pending := &flight[T]{done: make(chan struct{})}
	held.pending = pending
	held.snapshot.AttemptedAt = now
	coordinator.wait.Add(1)
	coordinator.mutex.Unlock()
	go func() {
		defer coordinator.wait.Done()
		value, err := operation(coordinator.context)
		coordinator.mutex.Lock()
		defer coordinator.mutex.Unlock()
		held.snapshot.CompletedAt = coordinator.clock()
		held.snapshot.Error = err
		if err == nil {
			held.snapshot.Value, held.snapshot.HasValue, held.snapshot.SucceededAt = value, true, held.snapshot.CompletedAt
		}
		pending.snapshot = held.snapshot
		held.pending = nil
		close(pending.done)
	}()
	select {
	case <-ctx.Done():
		return Snapshot[T]{}, ctx.Err()
	case <-pending.done:
		return pending.snapshot, pending.snapshot.Error
	}
}
func (coordinator *Coordinator[T]) Snapshot(key string) Snapshot[T] {
	coordinator.mutex.Lock()
	defer coordinator.mutex.Unlock()
	if held := coordinator.entries[key]; held != nil {
		return held.snapshot
	}
	return Snapshot[T]{}
}
func (coordinator *Coordinator[T]) Close() {
	coordinator.mutex.Lock()
	coordinator.isClosed = true
	coordinator.cancel()
	coordinator.mutex.Unlock()
	coordinator.wait.Wait()
}
