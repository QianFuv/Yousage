package desktop

import (
	"context"
	"sync"
	"time"
	"yousage/internal/refresh"
)

const backgroundRefreshInterval = 15 * time.Minute

// backgroundRefreshOptions supplies sync eligibility. Configure callbacks before
// ServiceStartup; callbacks must support concurrent reads. After changing sync
// settings, call backgroundRefresh.reconcile to apply eligibility immediately.
// The hook runs after all period and limits refresh attempts, including partial failures, so sync
// can publish retained last-good values. It must return promptly.
type backgroundRefreshOptions struct {
	isSyncEnabled func() bool
	timeZone      func() string
	newTicker     func(time.Duration) backgroundRefreshTicker
	onRefreshed   func()
}

// backgroundRefreshTicker permits deterministic scheduling without a wall clock.
type backgroundRefreshTicker interface {
	ticks() <-chan time.Time
	stop()
}

type backgroundTicker struct{ *time.Ticker }

func (ticker backgroundTicker) ticks() <-chan time.Time { return ticker.C }
func (ticker backgroundTicker) stop()                   { ticker.Stop() }

// backgroundRefreshController owns at most one serial worker, including while a
// cancelled refresh finishes. Disabled sync creates neither a worker nor a ticker.
type backgroundRefreshController struct {
	mutex         sync.Mutex
	context       context.Context
	options       backgroundRefreshOptions
	isAutoEnabled func() bool
	refreshUsage  func(context.Context, int, string, []string, refresh.Request) error
	refreshLimits func(context.Context, []string, refresh.Request) error
	isVisible     bool
	isClosed      bool
	cancel        context.CancelFunc
	done          chan struct{}
	ticker        backgroundRefreshTicker
}

// newBackgroundRefreshController defaults to disabled sync and UTC without saved
// settings. No work starts until visibility and eligibility are reconciled.
func newBackgroundRefreshController(ctx context.Context, options backgroundRefreshOptions, isAutoEnabled func() bool,
	refreshUsage func(context.Context, int, string, []string, refresh.Request) error,
	refreshLimits func(context.Context, []string, refresh.Request) error) *backgroundRefreshController {
	if options.isSyncEnabled == nil {
		options.isSyncEnabled = func() bool { return false }
	}
	if options.timeZone == nil {
		options.timeZone = func() string { return "UTC" }
	}
	if options.newTicker == nil {
		options.newTicker = func(interval time.Duration) backgroundRefreshTicker {
			return backgroundTicker{time.NewTicker(interval)}
		}
	}
	if options.onRefreshed == nil {
		options.onRefreshed = func() {}
	}
	return &backgroundRefreshController{context: ctx, options: options, isAutoEnabled: isAutoEnabled,
		refreshUsage: refreshUsage, refreshLimits: refreshLimits, isVisible: true}
}

// setVisibility applies the same effective visibility emitted to the frontend.
func (controller *backgroundRefreshController) setVisibility(isVisible bool) {
	controller.mutex.Lock()
	defer controller.mutex.Unlock()
	controller.isVisible = isVisible
	controller.reconcileLocked()
}

// reconcile applies sync and auto-refresh changes without waiting for a tick.
func (controller *backgroundRefreshController) reconcile() {
	controller.mutex.Lock()
	defer controller.mutex.Unlock()
	controller.reconcileLocked()
}

func (controller *backgroundRefreshController) shouldRunLocked() bool {
	return !controller.isClosed && controller.context.Err() == nil && !controller.isVisible &&
		controller.options.isSyncEnabled() && controller.isAutoEnabled()
}

func (controller *backgroundRefreshController) reconcileLocked() {
	if !controller.shouldRunLocked() {
		if controller.cancel != nil {
			controller.cancel()
			controller.ticker.stop()
		}
		return
	}
	if controller.done != nil {
		return
	}
	ctx, cancel := context.WithCancel(controller.context)
	controller.cancel = cancel
	controller.done = make(chan struct{})
	controller.ticker = controller.options.newTicker(backgroundRefreshInterval)
	go controller.run(ctx, controller.ticker)
}

// run waits a full interval before querying, rechecks flags on every tick, and
// never starts another cycle until every published window, limits, and the hook have returned.
func (controller *backgroundRefreshController) run(ctx context.Context, ticker backgroundRefreshTicker) {
	defer func() {
		controller.mutex.Lock()
		defer controller.mutex.Unlock()
		ticker.stop()
		controller.cancel()
		close(controller.done)
		controller.done, controller.cancel, controller.ticker = nil, nil, nil
		controller.reconcileLocked()
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.ticks():
			controller.mutex.Lock()
			shouldRun := ctx.Err() == nil && controller.shouldRunLocked()
			controller.mutex.Unlock()
			if !shouldRun {
				return
			}
			request := refresh.Request{IsBackground: true, IsAutoEnabled: true}
			zone := controller.options.timeZone()
			for _, days := range []int{1, 7, 30, 90} {
				_ = controller.refreshUsage(ctx, days, zone, nil, request)
				if ctx.Err() != nil {
					return
				}
			}
			_ = controller.refreshLimits(ctx, nil, request)
			if ctx.Err() != nil {
				return
			}
			controller.options.onRefreshed()
		}
	}
}

// close cancels scheduling and joins the worker before services are torn down.
func (controller *backgroundRefreshController) close() {
	controller.mutex.Lock()
	controller.isClosed = true
	controller.reconcileLocked()
	done := controller.done
	controller.mutex.Unlock()
	if done != nil {
		<-done
	}
}
