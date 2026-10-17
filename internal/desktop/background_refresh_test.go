package desktop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"yousage/internal/refresh"
	datasync "yousage/internal/sync"
)

type fakeBackgroundTicker struct {
	channel chan time.Time
	stopped chan struct{}
	once    sync.Once
}

// TestBackgroundRefreshAdvancesEveryPublishedWindow checks a real snapshot after a hidden-day rollover.
func TestBackgroundRefreshAdvancesEveryPublishedWindow(t *testing.T) {
	app := syncTestApp(t)
	now := app.usage.Now()
	app.usage.Now = func() time.Time { return now }
	for _, days := range []int{1, 7, 30, 90} {
		if _, err := app.usage.Usage(app.context, days, "UTC"); err != nil {
			t.Fatal(err)
		}
	}
	now = now.Add(25 * time.Hour)
	created := make(chan *fakeBackgroundTicker, 1)
	completed := make(chan datasync.Snapshot, 1)
	builder := datasync.SnapshotBuilder{Source: app.usage, Display: app.usage.Preferences.Display, Appearance: app.themes.Read, Bootstrap: func() any { return Bootstrap{} }, Now: app.usage.Now}
	controller := newBackgroundRefreshController(app.context, backgroundRefreshOptions{
		isSyncEnabled: func() bool { return true },
		newTicker:     backgroundTickerFactory(t, created),
		onRefreshed: func() {
			snapshot, err := builder.Build(app.context, "UTC")
			if err != nil {
				t.Error(err)
			}
			completed <- snapshot
		},
	}, func() bool { return true }, func(ctx context.Context, days int, zone string, _ []string, request refresh.Request) error {
		return app.usage.RefreshUsage(ctx, days, zone, []string{}, request)
	}, func(context.Context, []string, refresh.Request) error { return nil })
	defer controller.close()
	controller.setVisibility(false)
	awaitBackgroundValue(t, created).channel <- time.Time{}
	snapshot := awaitBackgroundValue(t, completed)
	if len(snapshot.Periods) != 4 {
		t.Fatalf("published %d periods, want 4", len(snapshot.Periods))
	}
	for name, period := range snapshot.Periods {
		if period.Usage.Window != period.Speed.Window || period.Usage.Window != period.Loss.Window {
			t.Errorf("period %s has inconsistent usage/report windows: %+v / %+v", name, period.Usage.Window, period.Speed.Window)
		}
	}
}

func (ticker *fakeBackgroundTicker) ticks() <-chan time.Time { return ticker.channel }
func (ticker *fakeBackgroundTicker) stop() {
	ticker.once.Do(func() { close(ticker.stopped) })
}

func awaitBackgroundValue[T any](t *testing.T, channel <-chan T) T {
	t.Helper()
	select {
	case value := <-channel:
		return value
	case <-time.After(3 * time.Second):
		t.Fatal("background worker did not settle")
		var zero T
		return zero
	}
}

func backgroundTickerFactory(t *testing.T, created chan<- *fakeBackgroundTicker) func(time.Duration) backgroundRefreshTicker {
	t.Helper()
	return func(interval time.Duration) backgroundRefreshTicker {
		if interval != 15*time.Minute {
			t.Errorf("refresh interval = %v, want 15m", interval)
		}
		ticker := &fakeBackgroundTicker{channel: make(chan time.Time, 4), stopped: make(chan struct{})}
		created <- ticker
		return ticker
	}
}

func TestBackgroundRefreshRequiresSyncAutoRefreshAndHiddenWindow(t *testing.T) {
	for _, isSyncEnabled := range []bool{false, true} {
		for _, isAutoEnabled := range []bool{false, true} {
			for _, isVisible := range []bool{false, true} {
				t.Run(fmt.Sprintf("sync=%v/auto=%v/visible=%v", isSyncEnabled, isAutoEnabled, isVisible), func(t *testing.T) {
					created := make(chan *fakeBackgroundTicker, 2)
					completed := make(chan struct{}, 1)
					calls := make(chan string, 5)
					controller := newBackgroundRefreshController(context.Background(), backgroundRefreshOptions{
						isSyncEnabled: func() bool { return isSyncEnabled },
						timeZone:      func() string { return "Asia/Shanghai" },
						newTicker:     backgroundTickerFactory(t, created),
						onRefreshed:   func() { completed <- struct{}{} },
					}, func() bool { return isAutoEnabled },
						func(ctx context.Context, days int, zone string, selected []string, request refresh.Request) error {
							if ctx.Err() != nil || zone != "Asia/Shanghai" || selected != nil || request != (refresh.Request{IsBackground: true, IsAutoEnabled: true}) {
								t.Errorf("wrong usage request: days=%d zone=%s selected=%v request=%+v", days, zone, selected, request)
							}
							calls <- fmt.Sprintf("usage:%d", days)
							return errors.New("partial failure")
						}, func(ctx context.Context, selected []string, request refresh.Request) error {
							if ctx.Err() != nil || selected != nil || request != (refresh.Request{IsBackground: true, IsAutoEnabled: true}) {
								t.Errorf("wrong limits request: selected=%v request=%+v", selected, request)
							}
							calls <- "limits"
							return errors.New("partial failure")
						})
					t.Cleanup(controller.close)
					controller.setVisibility(isVisible)
					if !isSyncEnabled || !isAutoEnabled || isVisible {
						if len(created) != 0 || len(calls) != 0 {
							t.Fatalf("ineligible worker started: sync=%v auto=%v visible=%v", isSyncEnabled, isAutoEnabled, isVisible)
						}
						return
					}
					ticker := awaitBackgroundValue(t, created)
					if len(calls) != 0 {
						t.Fatal("refresh started before the first interval")
					}
					ticker.channel <- time.Time{}
					awaitBackgroundValue(t, completed)
					for _, expected := range []string{"usage:1", "usage:7", "usage:30", "usage:90", "limits"} {
						if actual := <-calls; actual != expected {
							t.Fatalf("refresh order = %s, want %s", actual, expected)
						}
					}
				})
			}
		}
	}
}

func TestBackgroundRefreshDefaultsToDisabledSync(t *testing.T) {
	created := make(chan *fakeBackgroundTicker, 1)
	controller := newBackgroundRefreshController(context.Background(), backgroundRefreshOptions{
		newTicker: backgroundTickerFactory(t, created),
	}, func() bool { return true },
		func(context.Context, int, string, []string, refresh.Request) error {
			t.Error("disabled sync refreshed usage")
			return nil
		},
		func(context.Context, []string, refresh.Request) error {
			t.Error("disabled sync refreshed limits")
			return nil
		})
	defer controller.close()
	controller.setVisibility(false)
	controller.reconcile()
	if len(created) != 0 || controller.done != nil {
		t.Fatal("disabled sync created a ticker or worker")
	}
	if controller.options.timeZone() != "UTC" {
		t.Fatal("default time zone must be UTC")
	}
}

func TestBackgroundRefreshReconcilesPreferenceChangesAndRechecksFlags(t *testing.T) {
	for _, shouldChangeSync := range []bool{false, true} {
		var isSyncEnabled, isAutoEnabled atomic.Bool
		isSyncEnabled.Store(true)
		isAutoEnabled.Store(true)
		created := make(chan *fakeBackgroundTicker, 2)
		controller := newBackgroundRefreshController(context.Background(), backgroundRefreshOptions{
			isSyncEnabled: isSyncEnabled.Load,
			newTicker:     backgroundTickerFactory(t, created),
		}, isAutoEnabled.Load,
			func(context.Context, int, string, []string, refresh.Request) error {
				t.Error("disabled flags refreshed usage")
				return nil
			},
			func(context.Context, []string, refresh.Request) error {
				t.Error("disabled flags refreshed limits")
				return nil
			})
		controller.setVisibility(false)
		ticker := awaitBackgroundValue(t, created)
		if shouldChangeSync {
			isSyncEnabled.Store(false)
		} else {
			isAutoEnabled.Store(false)
		}
		ticker.channel <- time.Time{}
		awaitBackgroundValue(t, ticker.stopped)
		controller.mutex.Lock()
		done := controller.done
		controller.mutex.Unlock()
		if done != nil {
			awaitBackgroundValue(t, done)
		}
		isSyncEnabled.Store(true)
		isAutoEnabled.Store(true)
		controller.reconcile()
		awaitBackgroundValue(t, created)
		isAutoEnabled.Store(false)
		controller.reconcile()
		controller.close()
	}
}

func TestBackgroundRefreshShowHideDoesNotOverlapCancelledRun(t *testing.T) {
	created := make(chan *fakeBackgroundTicker, 2)
	started, release, completed := make(chan struct{}, 5), make(chan struct{}), make(chan struct{}, 2)
	var calls atomic.Int32
	controller := newBackgroundRefreshController(context.Background(), backgroundRefreshOptions{
		isSyncEnabled: func() bool { return true },
		newTicker:     backgroundTickerFactory(t, created),
		onRefreshed:   func() { completed <- struct{}{} },
	}, func() bool { return true }, func(context.Context, int, string, []string, refresh.Request) error {
		calls.Add(1)
		started <- struct{}{}
		<-release
		return nil
	}, func(context.Context, []string, refresh.Request) error { return nil })
	t.Cleanup(controller.close)
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	controller.setVisibility(false)
	ticker := awaitBackgroundValue(t, created)
	ticker.channel <- time.Time{}
	awaitBackgroundValue(t, started)
	ticker.channel <- time.Time{}
	ticker.channel <- time.Time{}
	controller.setVisibility(true)
	awaitBackgroundValue(t, ticker.stopped)
	controller.setVisibility(false)
	if calls.Load() != 1 || len(created) != 0 {
		t.Fatal("another worker overlapped the pending cancelled refresh")
	}
	once.Do(func() { close(release) })
	restarted := awaitBackgroundValue(t, created)
	if len(completed) != 0 {
		t.Fatal("cancelled cycle called the completion hook")
	}
	restarted.channel <- time.Time{}
	awaitBackgroundValue(t, completed)
	controller.setVisibility(true)
	controller.close()
	if calls.Load() != 5 {
		t.Fatalf("queued ticks escaped stopped worker: calls=%d", calls.Load())
	}
}

func TestServiceShutdownCancelsAndJoinsBackgroundRefresh(t *testing.T) {
	created := make(chan *fakeBackgroundTicker, 1)
	started := make(chan struct{}, 1)
	controller := newBackgroundRefreshController(context.Background(), backgroundRefreshOptions{
		isSyncEnabled: func() bool { return true },
		newTicker:     backgroundTickerFactory(t, created),
	}, func() bool { return true }, func(ctx context.Context, days int, zone string, selected []string, request refresh.Request) error {
		started <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	}, func(context.Context, []string, refresh.Request) error { t.Error("shutdown started limits"); return nil })
	t.Cleanup(controller.close)
	logFile, err := os.Create(filepath.Join(t.TempDir(), "lifecycle.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	service := &App{backgroundRefresh: controller, logFile: logFile}
	controller.setVisibility(false)
	ticker := awaitBackgroundValue(t, created)
	ticker.channel <- time.Time{}
	awaitBackgroundValue(t, started)
	if err := service.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}
	awaitBackgroundValue(t, ticker.stopped)
	controller.setVisibility(false)
	controller.reconcile()
	if controller.done != nil || len(created) != 0 {
		t.Fatal("shutdown left a worker or restarted scheduling")
	}
}
