package desktop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"yousage/internal/accounting"
	"yousage/internal/preferences"
	"yousage/internal/refresh"
	dataservice "yousage/internal/service"
	datasync "yousage/internal/sync"
)

type fakeBackgroundTicker struct {
	channel chan time.Time
	stopped chan struct{}
	once    sync.Once
}

// TestGlobalRefreshStateIncludesPersistedProbeFailure checks the service-to-desktop event boundary.
func TestGlobalRefreshStateIncludesPersistedProbeFailure(t *testing.T) {
	app := syncTestApp(t)
	_, revision, err := app.usage.Sources(app.context)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.usage.SaveSources(app.context, []preferences.Source{}, revision); err != nil {
		t.Fatal(err)
	}
	configuration, err := app.usage.Configuration(app.context)
	if err != nil {
		t.Fatal(err)
	}
	configuration.Value.Providers = []dataservice.NativeProvider{{Id: "fixture", Label: "private@example.invalid", Driver: "codex", Executable: filepath.Join(t.TempDir(), "missing-codex.exe"), IsEnabled: true}}
	if _, err := app.usage.SaveConfiguration(app.context, configuration); err != nil {
		t.Fatal(err)
	}
	if _, err := app.store.WriteSetting(app.context, "public-model-rates", 0, dataservice.RateSnapshot{Rates: accounting.RateTable{"fixture": {}}, Source: "https://models.dev/api.json", FetchedAtMs: app.usage.Now().UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	completed := make(chan struct{}, 1)
	app.backgroundRefresh = newBackgroundRefreshController(app.context, backgroundRefreshOptions{
		onRefreshed: func() { completed <- struct{}{} },
	}, func() bool { return false }, app.usage.RefreshCycle)
	var invalidations atomic.Int32
	app.usage.Emit = func(name string, value any) {
		if name == "usage-invalidated" {
			invalidations.Add(1)
			if !app.GetRefreshState().IsRefreshing {
				t.Error("invalidation followed refreshing=false")
			}
		}
	}
	if err := app.RequestRefresh(); err != nil {
		t.Fatal(err)
	}
	awaitBackgroundValue(t, completed)
	state := app.GetRefreshState()
	if invalidations.Load() == 0 || state.LastError == "" || state.IsRefreshing {
		t.Fatal("persisted probe notice did not reach refresh state", state)
	}
	if strings.Contains(state.LastError, "private") || strings.Contains(state.LastError, "@") || strings.Contains(state.LastError, "missing-codex") {
		t.Fatal("refresh state exposed private probe data")
	}
}

func (ticker *fakeBackgroundTicker) ticks() <-chan time.Time { return ticker.channel }
func (ticker *fakeBackgroundTicker) stop()                   { ticker.once.Do(func() { close(ticker.stopped) }) }

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
		if interval != refreshClockInterval {
			t.Errorf("heartbeat = %v, want %v", interval, refreshClockInterval)
		}
		ticker := &fakeBackgroundTicker{channel: make(chan time.Time, 4), stopped: make(chan struct{})}
		created <- ticker
		return ticker
	}
}

// TestBackgroundRefreshEligibilityAndImmediateStart covers the entire preference matrix.
func TestBackgroundRefreshEligibilityAndImmediateStart(t *testing.T) {
	for _, isSyncEnabled := range []bool{false, true} {
		for _, isAutoEnabled := range []bool{false, true} {
			for _, isVisible := range []bool{false, true} {
				t.Run(fmt.Sprintf("sync=%v/auto=%v/visible=%v", isSyncEnabled, isAutoEnabled, isVisible), func(t *testing.T) {
					created := make(chan *fakeBackgroundTicker, 1)
					completed := make(chan struct{}, 1)
					controller := newBackgroundRefreshController(context.Background(), backgroundRefreshOptions{
						isSyncEnabled: func() bool { return isSyncEnabled }, newTicker: backgroundTickerFactory(t, created),
						onRefreshed: func() { completed <- struct{}{} },
					}, func() bool { return isAutoEnabled }, func(ctx context.Context, zone string, request refresh.Request) error {
						if ctx.Err() != nil || zone != "UTC" || request.IsManual || !request.IsBackground || !request.IsAutoEnabled {
							t.Errorf("wrong automatic request: %+v", request)
						}
						return nil
					})
					defer controller.close()
					controller.setVisibility(isVisible)
					if !isAutoEnabled || (!isVisible && !isSyncEnabled) {
						controller.mutex.Lock()
						hasWorker := controller.done != nil
						controller.mutex.Unlock()
						if hasWorker {
							t.Fatal("ineligible worker started")
						}
						return
					}
					awaitBackgroundValue(t, created)
					awaitBackgroundValue(t, completed)
				})
			}
		}
	}
}

// TestBackgroundRefreshCadenceShowAndResume verifies visible, hidden and overdue wall-clock work.
func TestBackgroundRefreshCadenceShowAndResume(t *testing.T) {
	var clock atomic.Int64
	clock.Store(time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC).UnixNano())
	created := make(chan *fakeBackgroundTicker, 1)
	completed := make(chan struct{}, 8)
	controller := newBackgroundRefreshController(context.Background(), backgroundRefreshOptions{
		isSyncEnabled: func() bool { return true }, newTicker: backgroundTickerFactory(t, created),
		now: func() time.Time { return time.Unix(0, clock.Load()) }, onRefreshed: func() { completed <- struct{}{} },
	}, func() bool { return true }, func(context.Context, string, refresh.Request) error { return nil })
	defer controller.close()
	controller.reconcile()
	ticker := awaitBackgroundValue(t, created)
	awaitBackgroundValue(t, completed)
	clock.Add(int64(5 * time.Minute))
	ticker.channel <- time.Time{}
	awaitBackgroundValue(t, completed)
	controller.setVisibility(false)
	clock.Add(int64(15 * time.Minute))
	ticker.channel <- time.Time{}
	awaitBackgroundValue(t, completed)
	controller.setVisibility(true)
	awaitBackgroundValue(t, completed)
	clock.Add(int64(8 * time.Hour))
	ticker.channel <- time.Time{}
	awaitBackgroundValue(t, completed)
	controller.close()
	if len(completed) != 0 {
		t.Fatal("replayed missed ticks")
	}
}

// TestBackgroundRefreshKicksCoalesceAndManualWorksWhenDisabled exercises prompt manual scheduling.
func TestBackgroundRefreshKicksCoalesceAndManualWorksWhenDisabled(t *testing.T) {
	created := make(chan *fakeBackgroundTicker, 1)
	started := make(chan refresh.Request, 2)
	completed := make(chan struct{}, 2)
	release := make(chan struct{})
	var calls atomic.Int32
	controller := newBackgroundRefreshController(context.Background(), backgroundRefreshOptions{
		newTicker: backgroundTickerFactory(t, created), onRefreshed: func() { completed <- struct{}{} },
	}, func() bool { return false }, func(ctx context.Context, zone string, request refresh.Request) error {
		calls.Add(1)
		started <- request
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
		return nil
	})
	defer controller.close()
	app := &App{backgroundRefresh: controller}
	if err := app.RequestRefresh(); err != nil {
		t.Fatal(err)
	}
	if request := awaitBackgroundValue(t, started); !request.IsManual {
		t.Fatal("manual intent lost")
	}
	for range 50 {
		if err := app.RequestRefresh(); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("overlapping cycles")
	}
	close(release)
	awaitBackgroundValue(t, completed)
	if request := awaitBackgroundValue(t, started); !request.IsManual {
		t.Fatal("coalesced manual intent lost")
	}
	awaitBackgroundValue(t, completed)
	controller.close()
	if calls.Load() != 2 {
		t.Fatalf("calls=%d, want 2", calls.Load())
	}
	if err := app.RequestRefresh(); err == nil {
		t.Fatal("closed controller accepted a kick")
	}
}

// TestBackgroundRefreshAutomaticConnectionKicksCoalesce preserves automatic cooldown semantics.
func TestBackgroundRefreshAutomaticConnectionKicksCoalesce(t *testing.T) {
	started := make(chan refresh.Request, 2)
	completed := make(chan struct{}, 2)
	release := make(chan struct{})
	controller := newBackgroundRefreshController(context.Background(), backgroundRefreshOptions{
		onRefreshed: func() { completed <- struct{}{} },
	}, func() bool { return true }, func(ctx context.Context, zone string, request refresh.Request) error {
		started <- request
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
		return nil
	})
	defer controller.close()
	controller.reconcile()
	awaitBackgroundValue(t, started)
	for range 50 {
		if err := controller.request(false); err != nil {
			t.Fatal(err)
		}
	}
	close(release)
	awaitBackgroundValue(t, completed)
	if request := awaitBackgroundValue(t, started); request.IsManual || !request.IsAutoEnabled {
		t.Fatal("connection kick bypassed automatic policy")
	}
	awaitBackgroundValue(t, completed)
	controller.close()
	if len(started) != 0 {
		t.Fatal("connection flaps escaped coalescing")
	}
}

// TestRefreshStateFailureOrderAndRecovery ensures invalidation precedes completion and errors are redacted.
func TestRefreshStateFailureOrderAndRecovery(t *testing.T) {
	events := make(chan string, 12)
	var calls atomic.Int32
	controller := newBackgroundRefreshController(context.Background(), backgroundRefreshOptions{
		onStateChanged: func(state RefreshState) {
			if state.IsRefreshing {
				events <- "start"
			} else {
				events <- "end"
			}
		},
		onRefreshed: func() { events <- "sync" },
	}, func() bool { return false }, func(context.Context, string, refresh.Request) error {
		events <- "invalidate"
		if calls.Add(1) == 1 {
			return errors.New("secret fixture@example.invalid C:/private/token")
		}
		return nil
	})
	defer controller.close()
	app := &App{backgroundRefresh: controller}
	if state := (&App{}).GetRefreshState(); state != (RefreshState{}) {
		t.Fatal("startup state is not zero")
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := app.RequestRefresh(); err != nil {
			t.Fatal(err)
		}
		for _, expected := range []string{"start", "invalidate", "end", "sync"} {
			if actual := awaitBackgroundValue(t, events); actual != expected {
				t.Fatalf("event %s, want %s", actual, expected)
			}
		}
		state := app.GetRefreshState()
		if state.IsRefreshing || state.LastCompletedAt == "" {
			t.Fatal(state)
		}
		if _, err := time.Parse(time.RFC3339, state.LastCompletedAt); err != nil {
			t.Fatal(err)
		}
		if attempt == 0 && state.LastError != "Usage or limits could not be fully refreshed. Last successful data is retained when available." {
			t.Fatal("failure not safely represented", state)
		}
		if attempt == 1 && state.LastError != "" {
			t.Fatal("recovery did not clear failure", state)
		}
	}
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
	completed := make(chan datasync.Snapshot, 1)
	builder := datasync.SnapshotBuilder{Source: app.usage, Display: app.usage.Preferences.Display, Appearance: app.themes.Read, Bootstrap: func() any { return Bootstrap{} }, Now: app.usage.Now}
	controller := newBackgroundRefreshController(app.context, backgroundRefreshOptions{
		isSyncEnabled: func() bool { return true }, onRefreshed: func() {
			snapshot, err := builder.Build(app.context, "UTC")
			if err != nil {
				t.Error(err)
			}
			completed <- snapshot
		},
	}, func() bool { return true }, func(ctx context.Context, zone string, request refresh.Request) error {
		for _, days := range []int{1, 7, 30, 90} {
			if err := app.usage.RefreshUsage(ctx, days, zone, []string{}, request); err != nil {
				return err
			}
		}
		return nil
	})
	defer controller.close()
	controller.setVisibility(false)
	snapshot := awaitBackgroundValue(t, completed)
	if len(snapshot.Periods) != 4 {
		t.Fatalf("published %d periods, want 4", len(snapshot.Periods))
	}
	for name, period := range snapshot.Periods {
		if period.Usage.Window != period.Speed.Window || period.Usage.Window != period.Loss.Window {
			t.Errorf("period %s windows disagree", name)
		}
	}
}

// TestServiceShutdownCancelsAndJoinsBackgroundRefresh verifies cancellation and worker ownership.
func TestServiceShutdownCancelsAndJoinsBackgroundRefresh(t *testing.T) {
	created := make(chan *fakeBackgroundTicker, 1)
	started := make(chan struct{}, 1)
	controller := newBackgroundRefreshController(context.Background(), backgroundRefreshOptions{
		newTicker: backgroundTickerFactory(t, created), onRefreshed: func() { t.Error("shutdown triggered sync") },
	}, func() bool { return true }, func(ctx context.Context, zone string, request refresh.Request) error {
		started <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	})
	defer controller.close()
	logFile, err := os.Create(filepath.Join(t.TempDir(), "lifecycle.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	app := &App{backgroundRefresh: controller, logFile: logFile}
	controller.reconcile()
	ticker := awaitBackgroundValue(t, created)
	awaitBackgroundValue(t, started)
	if err := app.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}
	awaitBackgroundValue(t, ticker.stopped)
	controller.reconcile()
	if controller.done != nil {
		t.Fatal("shutdown restarted worker")
	}
}
