package desktop

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
	"yousage/internal/preferences"
	"yousage/internal/refresh"
	"yousage/internal/secrets"
	dataservice "yousage/internal/service"
	"yousage/internal/storage"
	datasync "yousage/internal/sync"
	"yousage/internal/theme"
)

type syncTestSecrets map[string]string

func (store syncTestSecrets) Get(_ context.Context, id string) (string, error) {
	if store[id] == "" {
		return "", secrets.ErrMissing
	}
	return store[id], nil
}

func (store syncTestSecrets) Put(_ context.Context, id, value string) error {
	store[id] = value
	return nil
}
func (store syncTestSecrets) Delete(_ context.Context, id string) error {
	delete(store, id)
	return nil
}

type syncTestTimer struct{}

func (syncTestTimer) Stop() bool { return true }

func syncTestApp(test *testing.T) *App {
	test.Helper()
	ctx := context.Background()
	directory := test.TempDir()
	store, err := storage.Open(filepath.Join(directory, "desktop.sqlite"))
	if err != nil {
		test.Fatal(err)
	}
	preferencesService := preferences.New(store)
	if _, err = preferencesService.SaveSources(ctx, []preferences.Source{}, 0); err != nil {
		test.Fatal(err)
	}
	library := &theme.Library{Repository: store, PublishedStore: theme.Store{Directory: directory}}
	usage, err := dataservice.New(ctx, store, library)
	if err != nil {
		test.Fatal(err)
	}
	usage.Now = func() time.Time { return time.Date(2026, 10, 15, 15, 30, 0, 0, time.UTC) }
	usage.Secrets = syncTestSecrets{}
	app := &App{context: ctx, usage: usage, store: store, themes: library}
	test.Cleanup(func() {
		if app.backgroundRefresh != nil {
			app.backgroundRefresh.close()
		}
		if app.syncEngine != nil {
			app.syncEngine.Close()
		}
		usage.Close()
		store.Close()
	})
	return app
}

func TestDesktopSyncRecordsUsageTimeZoneAndBackgroundCallbacks(test *testing.T) {
	app := syncTestApp(test)
	if err := app.initializeSync(app.context); err != nil {
		test.Fatal(err)
	}
	if app.backgroundOptions.isSyncEnabled() || app.backgroundOptions.timeZone() != "UTC" || app.backgroundOptions.onRefreshed == nil {
		test.Fatal("sync eligibility callbacks not initialized")
	}
	for _, zone := range []string{"Asia/Shanghai", "Asia/Shanghai", ""} {
		if _, err := app.GetUsage(7, zone); err != nil {
			test.Fatal(err)
		}
	}
	settings := datasync.Settings{}
	revision, err := app.usage.Preferences.Sync(app.context, &settings)
	if err != nil || revision != 2 || settings.TimeZone != "UTC" || app.backgroundOptions.timeZone() != "UTC" {
		test.Fatal("usage zone was not persisted only on change", settings, revision, err)
	}
	app.backgroundOptions.onRefreshed()
	if state, err := app.GetSync(); err != nil || state.IsEnabled {
		test.Fatal("disabled background callback enabled sync", state, err)
	}
}

type syncFailingRepository struct {
	preferences.Repository
	failedWrites int
}

func (repository *syncFailingRepository) WriteSetting(ctx context.Context, key string, revision int64, value any) (int64, error) {
	if key == "sync" {
		repository.failedWrites++
		return revision, errors.New("sync preferences unavailable")
	}
	return repository.Repository.WriteSetting(ctx, key, revision, value)
}

func TestDesktopGetUsageSurvivesTimeZonePersistenceFailure(test *testing.T) {
	app := syncTestApp(test)
	repository := &syncFailingRepository{Repository: app.store}
	app.usage.Preferences = preferences.New(repository)
	if err := app.initializeSync(app.context); err != nil {
		test.Fatal(err)
	}
	expected, err := app.usage.Usage(app.context, 7, "Asia/Shanghai")
	if err != nil {
		test.Fatal(err)
	}
	usage, err := app.GetUsage(7, "Asia/Shanghai")
	if err != nil {
		test.Fatal("sync bookkeeping prevented usage read", err)
	}
	if repository.failedWrites != 1 || app.syncEngine.Settings().TimeZone != "UTC" {
		test.Fatal("time-zone persistence did not fail as expected", repository.failedWrites)
	}
	if usage.Window != expected.Window || len(usage.Environments) != 1 || usage.Environments[0].Id != app.usage.LocalId || usage.Environments[0].Config == nil || usage.Environments[0].Summary == nil {
		test.Fatal("usage result was not returned", usage)
	}
}

func TestDesktopRefreshCompletionTriggersSyncAndSaveReconcilesBackground(test *testing.T) {
	app := syncTestApp(test)
	var scheduled []time.Duration
	engine, err := datasync.NewEngine(app.context, datasync.EngineOptions{
		Preferences: app.usage.Preferences, Secrets: app.usage.Secrets,
		BuildSnapshot: func(context.Context, string) (datasync.Snapshot, error) { return datasync.Snapshot{}, nil },
		BuildShare: func(context.Context, datasync.ShareRequest) (datasync.SharePayload, error) {
			return datasync.SharePayload{}, nil
		},
		Now: app.usage.Now,
		AfterFunc: func(delay time.Duration, callback func()) datasync.Timer {
			scheduled = append(scheduled, delay)
			return syncTestTimer{}
		},
	})
	if err != nil {
		test.Fatal(err)
	}
	app.syncEngine = engine
	created := make(chan *fakeBackgroundTicker, 2)
	app.backgroundRefresh = newBackgroundRefreshController(app.context, backgroundRefreshOptions{
		isSyncEnabled: func() bool { return engine.Settings().IsEnabled },
		timeZone:      func() string { return engine.Settings().TimeZone },
		newTicker:     backgroundTickerFactory(test, created),
	}, func() bool { return true }, func(context.Context, string, refresh.Request) error { return nil })
	app.backgroundRefresh.setVisibility(false)
	if _, err = app.SaveSync(datasync.SaveInput{IsEnabled: true, RelayUrl: "https://relay.example", Token: "synthetic-token"}); err != nil {
		test.Fatal(err)
	}
	ticker := awaitBackgroundValue(test, created)
	if len(scheduled) != 1 || scheduled[0] != 0 {
		test.Fatal("enable did not schedule immediate push", scheduled)
	}
	if err = app.RefreshUsage(7, "UTC", []string{}, refresh.Request{IsManual: true}); err != nil {
		test.Fatal(err)
	}
	if err = app.RefreshLimits([]string{}, refresh.Request{IsManual: true}); err != nil {
		test.Fatal(err)
	}
	if len(scheduled) != 3 || scheduled[1] != 10*time.Second || scheduled[2] != 10*time.Second {
		test.Fatal("refresh completions did not debounce sync", scheduled)
	}
	if _, err = app.SaveSync(datasync.SaveInput{RelayUrl: "https://relay.example"}); err != nil {
		test.Fatal(err)
	}
	awaitBackgroundValue(test, ticker.stopped)
	before := len(scheduled)
	app.RefreshUsage(7, "UTC", []string{}, refresh.Request{IsManual: true})
	if len(scheduled) != before {
		test.Fatal("disabled refresh scheduled sync")
	}
}
