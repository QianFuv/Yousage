package desktop

import (
	"context"
	"errors"
	"sync"
	"time"
	"yousage/internal/refresh"
)

const (
	visibleRefreshInterval    = 5 * time.Minute
	backgroundRefreshInterval = 15 * time.Minute
	refreshClockInterval      = 30 * time.Second
)

// RefreshState reports global refresh progress without exposing provider data.
type RefreshState struct {
	// IsRefreshing is true from cycle start through the final invalidation.
	IsRefreshing bool `json:"isRefreshing"`
	// LastCompletedAt is the last cycle's UTC RFC3339 completion time, or empty.
	LastCompletedAt string `json:"lastCompletedAt"`
	// LastError is a fixed safe failure notice, cleared by a successful cycle.
	LastError string `json:"lastError"`
}

// backgroundRefreshOptions must be configured before starting the controller.
// Callbacks must support concurrent reads and return promptly.
type backgroundRefreshOptions struct {
	isSyncEnabled  func() bool
	timeZone       func() string
	newTicker      func(time.Duration) backgroundRefreshTicker
	now            func() time.Time
	onRefreshed    func()
	onStateChanged func(RefreshState)
}

type backgroundRefreshTicker interface {
	ticks() <-chan time.Time
	stop()
}

type backgroundTicker struct{ *time.Ticker }

func (ticker backgroundTicker) ticks() <-chan time.Time { return ticker.C }
func (ticker backgroundTicker) stop()                   { ticker.Stop() }

// backgroundRefreshController owns one serial worker and one pending kick.
// Eligibility changes never overlap or abandon a running cycle; close cancels it.
type backgroundRefreshController struct {
	mutex         sync.Mutex
	context       context.Context
	options       backgroundRefreshOptions
	isAutoEnabled func() bool
	refreshCycle  func(context.Context, string, refresh.Request) error
	isVisible     bool
	isClosed      bool
	pending       bool
	isManual      bool
	cancel        context.CancelFunc
	done          chan struct{}
	wake          chan struct{}
	state         RefreshState
}

func newBackgroundRefreshController(ctx context.Context, options backgroundRefreshOptions, isAutoEnabled func() bool,
	refreshCycle func(context.Context, string, refresh.Request) error) *backgroundRefreshController {
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
	if options.now == nil {
		options.now = time.Now
	}
	if options.onRefreshed == nil {
		options.onRefreshed = func() {}
	}
	if options.onStateChanged == nil {
		options.onStateChanged = func(RefreshState) {}
	}
	return &backgroundRefreshController{context: ctx, options: options, isAutoEnabled: isAutoEnabled,
		refreshCycle: refreshCycle, isVisible: true, wake: make(chan struct{}, 1)}
}

func (controller *backgroundRefreshController) setVisibility(isVisible bool) {
	controller.mutex.Lock()
	defer controller.mutex.Unlock()
	wasVisible := controller.isVisible
	controller.isVisible = isVisible
	if isVisible && !wasVisible && controller.shouldRunLocked() {
		controller.pending = true
	}
	controller.reconcileLocked()
}

func (controller *backgroundRefreshController) reconcile() {
	controller.mutex.Lock()
	defer controller.mutex.Unlock()
	controller.reconcileLocked()
}

func (controller *backgroundRefreshController) shouldRunLocked() bool {
	return !controller.isClosed && controller.context.Err() == nil && controller.isAutoEnabled() &&
		(controller.isVisible || controller.options.isSyncEnabled())
}

// request coalesces kicks, preserving manual intent even when auto refresh is off.
func (controller *backgroundRefreshController) request(isManual bool) error {
	controller.mutex.Lock()
	defer controller.mutex.Unlock()
	if controller.isClosed || controller.context.Err() != nil {
		return errors.New("refresh is unavailable while the application is closing")
	}
	if !isManual && !controller.shouldRunLocked() {
		return nil
	}
	controller.pending = true
	controller.isManual = controller.isManual || isManual
	controller.reconcileLocked()
	return nil
}

func (controller *backgroundRefreshController) reconcileLocked() {
	if controller.isClosed || controller.context.Err() != nil {
		if controller.cancel != nil {
			controller.cancel()
		}
		return
	}
	if controller.done == nil && (controller.shouldRunLocked() || controller.isManual) {
		ctx, cancel := context.WithCancel(controller.context)
		controller.cancel = cancel
		controller.done = make(chan struct{})
		controller.pending = true
		go controller.run(ctx)
	}
	select {
	case controller.wake <- struct{}{}:
	default:
	}
}

// run checks wall time on a short heartbeat. Go timers use monotonic time, which
// may pause during sleep on some platforms; a resumed app checks overdue work
// within one heartbeat without replaying missed intervals or probing every tick.
func (controller *backgroundRefreshController) run(ctx context.Context) {
	ticker := controller.options.newTicker(refreshClockInterval)
	defer ticker.stop()
	var completedAt time.Time
	for {
		controller.mutex.Lock()
		canRun := controller.shouldRunLocked()
		if ctx.Err() != nil || (!canRun && !controller.isManual) {
			controller.cancel()
			close(controller.done)
			controller.done, controller.cancel = nil, nil
			controller.pending, controller.isManual = false, false
			controller.mutex.Unlock()
			return
		}
		interval := backgroundRefreshInterval
		if controller.isVisible {
			interval = visibleRefreshInterval
		}
		now := controller.options.now().Round(0)
		isDue := completedAt.IsZero() || !now.Before(completedAt.Add(interval))
		shouldRefresh := controller.pending || (canRun && isDue)
		request := refresh.Request{IsManual: controller.isManual, IsBackground: true, IsAutoEnabled: canRun}
		controller.pending, controller.isManual = false, false
		controller.mutex.Unlock()
		if shouldRefresh {
			controller.updateState(true, time.Time{}, "")
			err := controller.refreshCycle(ctx, controller.options.timeZone(), request)
			completedAt = controller.options.now().Round(0)
			message := ""
			if err != nil {
				message = "Usage or limits could not be fully refreshed. Last successful data is retained when available."
			}
			if ctx.Err() != nil {
				message = "Refresh was cancelled."
			}
			controller.updateState(false, completedAt, message)
			if ctx.Err() == nil {
				controller.options.onRefreshed()
			}
			continue
		}
		select {
		case <-ctx.Done():
		case <-controller.wake:
		case <-ticker.ticks():
		}
	}
}

func (controller *backgroundRefreshController) updateState(isRefreshing bool, completedAt time.Time, lastError string) {
	controller.mutex.Lock()
	controller.state.IsRefreshing = isRefreshing
	if !isRefreshing {
		controller.state.LastCompletedAt = completedAt.UTC().Format(time.RFC3339)
		controller.state.LastError = lastError
	}
	state := controller.state
	controller.mutex.Unlock()
	controller.options.onStateChanged(state)
}

func (controller *backgroundRefreshController) snapshot() RefreshState {
	controller.mutex.Lock()
	defer controller.mutex.Unlock()
	return controller.state
}

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
