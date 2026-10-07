package service

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
	"yousage/internal/accounting"
	"yousage/internal/refresh"
)

// waitLimitsFollowup waits for the accepted worker without starting another scan.
func waitLimitsFollowup(test *testing.T, service *Service) {
	test.Helper()
	service.mutex.Lock()
	done := service.limitsFollowup
	service.mutex.Unlock()
	if done == nil {
		return
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		test.Fatal("followup did not finish")
	}
}

// TestLimitsFollowupReturnsAndCoalesces keeps foreground refresh independent of a slow scan.
func TestLimitsFollowupReturnsAndCoalesces(test *testing.T) {
	service := testService(test)
	ctx := context.Background()
	if _, err := service.Store.WriteSetting(ctx, "public-model-rates", 0, RateSnapshot{Rates: accounting.RateTable{"fixture": accounting.ModelRate{}}, Source: ratesUrl, FetchedAtMs: service.Now().UnixMilli()}); err != nil {
		test.Fatal(err)
	}
	started, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(finished)
		_, _ = service.history.Run(ctx, "local-history", refresh.Request{IsManual: true}, func(owner context.Context) (bool, error) {
			close(started)
			select {
			case <-release:
				return true, nil
			case <-owner.Done():
				return false, owner.Err()
			}
		})
	}()
	<-started
	var invalidations atomic.Int32
	service.Emit = func(name string, value any) {
		if name == "usage-invalidated" {
			invalidations.Add(1)
		}
	}
	for index := 0; index < 5; index++ {
		returned := make(chan error, 1)
		go func() {
			returned <- service.RefreshLimits(ctx, []string{service.LocalId}, refresh.Request{IsManual: true})
		}()
		select {
		case err := <-returned:
			if err != nil {
				test.Fatal(err)
			}
		case <-time.After(time.Second):
			test.Fatal("limits waited for history")
		}
	}
	if invalidations.Load() != 0 || !service.HasPendingWrites() {
		test.Fatal("followup completed before history or lost write ownership")
	}
	close(release)
	<-finished
	waitLimitsFollowup(test, service)
	if invalidations.Load() != 1 || service.HasPendingWrites() {
		test.Fatal("followups stacked or ownership leaked", invalidations.Load())
	}
}

// TestLimitsFollowupShutdown cancels accepted work and waits for coordinator completion.
func TestLimitsFollowupShutdown(test *testing.T) {
	service := testService(test)
	started, finished := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(finished)
		_, _ = service.history.Run(context.Background(), "local-history", refresh.Request{IsManual: true}, func(owner context.Context) (bool, error) {
			close(started)
			<-owner.Done()
			return false, owner.Err()
		})
	}()
	<-started
	if err := service.RefreshLimits(context.Background(), []string{service.LocalId}, refresh.Request{IsManual: true}); err != nil {
		test.Fatal(err)
	}
	closed := make(chan struct{})
	go func() { service.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		test.Fatal("shutdown did not cancel followup")
	}
	<-finished
	if service.HasPendingWrites() {
		test.Fatal("shutdown leaked pending writes")
	}
}

// TestCanceledLimitsWaiterPreservesOrdering does not start scans before a pending probe ends.
func TestCanceledLimitsWaiterPreservesOrdering(test *testing.T) {
	service := testService(test)
	started, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(finished)
		_, _ = service.limits.Run(context.Background(), "local", refresh.Request{IsManual: true}, func(owner context.Context) (bool, error) {
			close(started)
			select {
			case <-release:
				return true, nil
			case <-owner.Done():
				return false, owner.Err()
			}
		})
	}()
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := service.RefreshLimits(ctx, []string{service.LocalId}, refresh.Request{IsManual: true}); err == nil {
		test.Fatal("canceled caller waited for limits")
	}
	service.mutex.Lock()
	hasFollowup := service.limitsFollowup != nil
	service.mutex.Unlock()
	close(release)
	<-finished
	if hasFollowup || !service.history.Snapshot("local-history").AttemptedAt.IsZero() {
		test.Fatal("history started before the local probe ended")
	}
}
