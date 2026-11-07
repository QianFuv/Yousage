package sync

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	stdsync "sync"
	"sync/atomic"
	"testing"
	"time"
	"yousage/internal/preferences"
	"yousage/internal/relay"
	"yousage/internal/remote"
	"yousage/internal/secrets"
	"yousage/internal/service"
	"yousage/internal/storage"
)

const fixtureToken = "synthetic-relay-token-at-least-24-characters"

type memorySecrets struct {
	values        map[string]string
	isUnavailable bool
	failId        string
}

func (store *memorySecrets) Get(_ context.Context, id string) (string, error) {
	if store.isUnavailable {
		return "", errors.New(fixtureToken)
	}
	value := store.values[id]
	if value == "" {
		return "", secrets.ErrMissing
	}
	return value, nil
}

func (store *memorySecrets) Put(_ context.Context, id, value string) error {
	if store.isUnavailable || store.failId == id {
		return errors.New(fixtureToken)
	}
	store.values[id] = value
	return nil
}

func (store *memorySecrets) Delete(_ context.Context, id string) error {
	delete(store.values, id)
	return nil
}

type clockTimer struct {
	clock     *fixtureClock
	due       time.Time
	callback  func()
	isStopped bool
}

func (timer *clockTimer) Stop() bool {
	timer.clock.mutex.Lock()
	defer timer.clock.mutex.Unlock()
	wasActive := !timer.isStopped
	timer.isStopped = true
	return wasActive
}

type fixtureClock struct {
	mutex  stdsync.Mutex
	now    time.Time
	timers []*clockTimer
}

func newFixtureClock() *fixtureClock {
	return &fixtureClock{now: time.Date(2026, 10, 15, 15, 30, 0, 0, time.UTC)}
}

func (clock *fixtureClock) Now() time.Time {
	clock.mutex.Lock()
	defer clock.mutex.Unlock()
	return clock.now
}

func (clock *fixtureClock) afterFunc(delay time.Duration, callback func()) Timer {
	clock.mutex.Lock()
	defer clock.mutex.Unlock()
	timer := &clockTimer{clock: clock, due: clock.now.Add(delay), callback: callback}
	clock.timers = append(clock.timers, timer)
	return timer
}

func (clock *fixtureClock) advance(duration time.Duration) {
	deadline := clock.Now().Add(duration)
	for {
		clock.mutex.Lock()
		var next *clockTimer
		for _, timer := range clock.timers {
			if !timer.isStopped && !timer.due.After(deadline) && (next == nil || timer.due.Before(next.due)) {
				next = timer
			}
		}
		if next == nil {
			clock.now = deadline
			clock.mutex.Unlock()
			return
		}
		clock.now, next.isStopped = next.due, true
		clock.mutex.Unlock()
		next.callback()
	}
}

type engineFixture struct {
	engine     *Engine
	clock      *fixtureClock
	store      *storage.Store
	secrets    *memorySecrets
	server     *httptest.Server
	requests   atomic.Int64
	puts       atomic.Int64
	snapshot   Snapshot
	buildError error
	events     []State
}

func newEngineFixture(test *testing.T) *engineFixture {
	test.Helper()
	fixture := &engineFixture{clock: newFixtureClock(), secrets: &memorySecrets{values: map[string]string{}}, snapshot: Snapshot{FormatVersion: 1, GeneratedAt: "2026-10-15T15:30:00Z", TimeZone: "UTC", Periods: map[string]Period{}}}
	server, err := relay.New(relay.Config{Token: fixtureToken, DataDir: test.TempDir(), Now: fixture.clock.Now})
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { server.Close() })
	fixture.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fixture.requests.Add(1)
		if request.Method == http.MethodPut && strings.HasPrefix(request.URL.Path, "/api/spaces/") {
			fixture.puts.Add(1)
		}
		server.ServeHTTP(writer, request)
	}))
	test.Cleanup(fixture.server.Close)
	fixture.store, err = storage.Open(filepath.Join(test.TempDir(), "sync.sqlite"))
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { fixture.store.Close() })
	fixture.engine, err = NewEngine(context.Background(), EngineOptions{
		Preferences: preferences.New(fixture.store), Secrets: fixture.secrets, Now: fixture.clock.Now, AfterFunc: fixture.clock.afterFunc,
		BuildSnapshot: func(context.Context, string) (Snapshot, error) { return fixture.snapshot, fixture.buildError },
		OnChanged:     func(state State) { fixture.events = append(fixture.events, state) },
	})
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(fixture.engine.Close)
	return fixture
}

func (fixture *engineFixture) enable(test *testing.T) State {
	test.Helper()
	state, err := fixture.engine.Save(context.Background(), SaveInput{IsEnabled: true, RelayUrl: fixture.server.URL + "/", Token: fixtureToken})
	if err != nil {
		test.Fatal(err)
	}
	if state.RelayUrl != fixture.server.URL || state.PairingUrl == "" || !state.HasToken {
		test.Fatal("first enable did not pair", state)
	}
	fixture.clock.advance(0)
	return state
}

func readBlob(test *testing.T, address string) ([]byte, int) {
	test.Helper()
	response, err := http.Get(address)
	if err != nil {
		test.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		test.Fatal(err)
	}
	return data, response.StatusCode
}

func TestEngineDisabledMakesNoRelayRequests(test *testing.T) {
	fixture := newEngineFixture(test)
	fixture.engine.Trigger()
	fixture.clock.advance(2 * time.Hour)
	fixture.engine.SyncNow(context.Background())
	if _, err := fixture.engine.RotatePairing(context.Background()); err == nil {
		test.Fatal("rotation enabled disabled sync")
	}
	if fixture.requests.Load() != 0 {
		test.Fatal("disabled sync contacted relay")
	}
	fixture.enable(test)
	if _, err := fixture.engine.Save(context.Background(), SaveInput{RelayUrl: fixture.server.URL}); err != nil {
		test.Fatal(err)
	}
	count := fixture.requests.Load()
	fixture.engine.Trigger()
	fixture.clock.advance(2 * time.Hour)
	fixture.engine.SyncNow(context.Background())
	if fixture.requests.Load() != count {
		test.Fatal("disabling did not stop relay requests")
	}
}

func TestEngineDebounceMinimumIntervalAndUnchangedHash(test *testing.T) {
	fixture := newEngineFixture(test)
	fixture.enable(test)
	if fixture.puts.Load() != 1 {
		test.Fatal("first enable must push immediately")
	}
	fixture.snapshot.AppVersion = "changed"
	fixture.engine.Trigger()
	fixture.clock.advance(5 * time.Second)
	fixture.engine.Trigger()
	fixture.clock.advance(54 * time.Second)
	if fixture.puts.Load() != 1 {
		test.Fatal("uploaded before minimum interval")
	}
	fixture.clock.advance(time.Second)
	if fixture.puts.Load() != 2 {
		test.Fatal("coalesced change was not uploaded once")
	}
	fixture.clock.advance(time.Minute)
	fixture.engine.SyncNow(context.Background())
	if fixture.puts.Load() != 2 {
		test.Fatal("unchanged plaintext uploaded")
	}
	fixture.snapshot.AppVersion = "second change"
	fixture.engine.Trigger()
	fixture.clock.advance(5 * time.Second)
	fixture.engine.Trigger()
	fixture.clock.advance(9 * time.Second)
	if fixture.puts.Load() != 2 {
		test.Fatal("debounce did not restart")
	}
	fixture.clock.advance(time.Second)
	if fixture.puts.Load() != 3 {
		test.Fatal("debounced snapshot was not pushed")
	}
	state, err := fixture.engine.State(context.Background())
	if err != nil || state.Status.IsPushing || state.Status.LastPushAt != fixture.clock.Now().Format(time.RFC3339) || state.Status.LastError != "" {
		test.Fatal(state.Status, err)
	}
	hasPushingEvent := false
	for _, event := range fixture.events {
		if event.Status.IsPushing {
			hasPushingEvent = true
		}
	}
	if !hasPushingEvent {
		test.Fatal("sync-state did not expose upload progress")
	}
}

func TestEngineFailedSnapshotRetainsPreviousUploadAndRetries(test *testing.T) {
	fixture := newEngineFixture(test)
	state := fixture.enable(test)
	parsed, _ := url.Parse(state.PairingUrl)
	previous, _ := readBlob(test, fixture.server.URL+"/api/spaces/"+fixture.engine.Settings().SpaceId)
	fixture.clock.advance(time.Minute)
	fixture.snapshot.AppVersion = "changed"
	fixture.buildError = errors.New(fixtureToken)
	state, err := fixture.engine.SyncNow(context.Background())
	if err == nil || state.Status.LastError == "" || strings.Contains(err.Error(), fixtureToken) || strings.Contains(state.Status.LastError, fixtureToken) {
		test.Fatal("snapshot error leaked or went unreported", state.Status, err)
	}
	retained, _ := readBlob(test, fixture.server.URL+"/api/spaces/"+fixture.engine.Settings().SpaceId)
	if string(previous) != string(retained) || fixture.puts.Load() != 1 {
		test.Fatal("partial snapshot replaced last good upload")
	}
	fixture.buildError = nil
	if _, err = fixture.engine.SyncNow(context.Background()); err != nil {
		test.Fatal(err)
	}
	data, _ := readBlob(test, fixture.server.URL+"/api/spaces/"+fixture.engine.Settings().SpaceId)
	plaintext, err := Open(data, parsed.Fragment, "space:"+fixture.engine.Settings().SpaceId)
	if err != nil || !strings.Contains(string(plaintext), "changed") {
		test.Fatal("retry failed", err)
	}
}

func TestSyncNowPublishesImmediatelyButStillSkipsUnchangedContent(test *testing.T) {
	fixture := newEngineFixture(test)
	fixture.enable(test)
	fixture.clock.advance(time.Second)
	fixture.snapshot.AppVersion = "manual change"
	fixture.engine.Trigger()
	if _, err := fixture.engine.SyncNow(context.Background()); err != nil {
		test.Fatal(err)
	}
	if fixture.puts.Load() != 2 {
		test.Fatal("explicit sync did not bypass automatic scheduling")
	}
	if _, err := fixture.engine.SyncNow(context.Background()); err != nil {
		test.Fatal(err)
	}
	if fixture.puts.Load() != 2 {
		test.Fatal("explicit sync reuploaded unchanged data")
	}
}

func TestEngineSkipsObservationMetadataOnlyChanges(test *testing.T) {
	fixture := newEngineFixture(test)
	fixture.snapshot.Periods["7"] = Period{Usage: service.Snapshot{Environments: []service.EnvironmentState{{Id: "local", Summary: &remote.Summary{ReadAt: "2026-10-15T15:30:00Z", ScanDurationMs: 5}}}}}
	fixture.enable(test)
	fixture.clock.advance(time.Minute)
	fixture.snapshot.GeneratedAt = fixture.clock.Now().Format(time.RFC3339)
	fixture.snapshot.Periods = map[string]Period{"7": {Usage: service.Snapshot{Environments: []service.EnvironmentState{{Id: "local", Summary: &remote.Summary{ReadAt: fixture.clock.Now().Format(time.RFC3339), ScanDurationMs: 9}}}}}}
	if _, err := fixture.engine.SyncNow(context.Background()); err != nil {
		test.Fatal(err)
	}
	if fixture.puts.Load() != 1 {
		test.Fatal("observation metadata alone uploaded unchanged data")
	}
	fixture.snapshot.AppVersion = "new"
	if _, err := fixture.engine.SyncNow(context.Background()); err != nil {
		test.Fatal(err)
	}
	if fixture.puts.Load() != 2 {
		test.Fatal("substantive change was skipped")
	}
}

func TestEngineRotatePairing(test *testing.T) {
	fixture := newEngineFixture(test)
	oldPairing := fixture.enable(test).PairingUrl
	oldId := fixture.engine.Settings().SpaceId
	state, err := fixture.engine.RotatePairing(context.Background())
	if err != nil || state.PairingUrl == oldPairing || fixture.engine.Settings().SpaceId == oldId {
		test.Fatal("pairing not replaced", err)
	}
	_, status := readBlob(test, fixture.server.URL+"/api/spaces/"+oldId)
	if status != 404 {
		test.Fatal("old pairing space was not deleted")
	}
	parsed, _ := url.Parse(state.PairingUrl)
	data, status := readBlob(test, fixture.server.URL+"/api/spaces/"+fixture.engine.Settings().SpaceId)
	if _, err := Open(data, parsed.Fragment, "space:"+fixture.engine.Settings().SpaceId); err != nil || status != 200 {
		test.Fatal("new pairing did not push", err)
	}
}

func TestEngineSecretsNeverPersistOrLeak(test *testing.T) {
	for _, failure := range []string{"unavailable", tokenId, spaceKeyId, "none"} {
		test.Run(failure, func(test *testing.T) {
			fixture := newEngineFixture(test)
			fixture.secrets.isUnavailable = failure == "unavailable"
			if failure == tokenId || failure == spaceKeyId {
				fixture.secrets.failId = failure
			}
			state, err := fixture.engine.Save(context.Background(), SaveInput{IsEnabled: true, RelayUrl: fixture.server.URL, Token: fixtureToken})
			if (err == nil) != (failure == "none") {
				test.Fatal("unexpected keyring result", err)
			}
			if failure != "none" && (fixture.engine.Settings().IsEnabled || fixture.requests.Load() != 0) {
				test.Fatal("keyring failure enabled sync")
			}
			var persisted json.RawMessage
			fixture.store.ReadSetting(context.Background(), "sync", &persisted)
			if strings.Contains(string(persisted), fixtureToken) || strings.Contains(string(persisted), fixture.secrets.values[spaceKeyId]) && fixture.secrets.values[spaceKeyId] != "" {
				test.Fatal("private secret in SQLite")
			}
			encoded, _ := json.Marshal(state)
			if strings.Contains(string(encoded), fixtureToken) || (err != nil && strings.Contains(err.Error(), fixtureToken)) {
				test.Fatal("token leaked through error or state")
			}
		})
	}
}

func TestEngineTimeZonePersistsOnlyChanges(test *testing.T) {
	fixture := newEngineFixture(test)
	for _, scenario := range []struct {
		zone, expected string
		revision       int64
	}{{"Asia/Shanghai", "Asia/Shanghai", 1}, {"Asia/Shanghai", "Asia/Shanghai", 1}, {"", "UTC", 2}, {"invalid", "UTC", 2}} {
		if err := fixture.engine.RecordTimeZone(context.Background(), scenario.zone); err != nil {
			test.Fatal(err)
		}
		settings := Settings{}
		revision, err := preferences.New(fixture.store).Sync(context.Background(), &settings)
		if err != nil || settings.TimeZone != scenario.expected || revision != scenario.revision {
			test.Fatal(settings.TimeZone, revision, err)
		}
	}
}

// TestEngineIgnoresLegacyShareRecords loads sync settings saved while share links existed.
func TestEngineIgnoresLegacyShareRecords(test *testing.T) {
	store, err := storage.Open(filepath.Join(test.TempDir(), "sync.sqlite"))
	if err != nil {
		test.Fatal(err)
	}
	defer store.Close()
	legacy := map[string]any{"isEnabled": false, "relayUrl": "https://relay.example", "spaceId": "", "timeZone": "UTC", "shares": []map[string]any{{"id": "AAAAAAAAAAAAAAAAAAAAAA", "url": "https://relay.example/s/AAAAAAAAAAAAAAAAAAAAAA#key"}}}
	if _, err = store.WriteSetting(context.Background(), "sync", 0, legacy); err != nil {
		test.Fatal(err)
	}
	engine, err := NewEngine(context.Background(), EngineOptions{Preferences: preferences.New(store), Secrets: &memorySecrets{values: map[string]string{}}, BuildSnapshot: func(context.Context, string) (Snapshot, error) { return Snapshot{}, nil }})
	if err != nil {
		test.Fatal("legacy share records blocked sync settings", err)
	}
	defer engine.Close()
	if err = engine.RecordTimeZone(context.Background(), "Asia/Shanghai"); err != nil {
		test.Fatal(err)
	}
	var persisted map[string]json.RawMessage
	if _, err = store.ReadSetting(context.Background(), "sync", &persisted); err != nil || persisted["shares"] != nil {
		test.Fatal("legacy share records survived the next save", persisted, err)
	}
}

type cancellationTransport struct{ started chan struct{} }

func (transport cancellationTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	close(transport.started)
	<-request.Context().Done()
	return nil, errors.New(fixtureToken)
}

func TestEngineCloseCancelsInFlightPush(test *testing.T) {
	fixture := newEngineFixture(test)
	fixture.enable(test)
	fixture.clock.advance(time.Minute)
	fixture.snapshot.AppVersion = "changed"
	fixture.engine.options.OnChanged = nil
	started := make(chan struct{})
	fixture.engine.client = NewClient(&http.Client{Transport: cancellationTransport{started}})
	completed := make(chan error, 1)
	go func() { _, err := fixture.engine.SyncNow(context.Background()); completed <- err }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		test.Fatal("push did not start")
	}
	fixture.engine.Close()
	select {
	case err := <-completed:
		if err == nil || strings.Contains(err.Error(), fixtureToken) {
			test.Fatal("cancelled push returned unsafe result", err)
		}
	case <-time.After(3 * time.Second):
		test.Fatal("shutdown did not drain push")
	}
	fixture.engine.Trigger()
	fixture.clock.advance(time.Hour)
}

type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New(fixtureToken)
}

func TestRelayClientRedactsErrorsAndTestsSavedToken(test *testing.T) {
	fixture := newEngineFixture(test)
	fixture.enable(test)
	if err := fixture.engine.TestRelay(context.Background(), fixture.server.URL, ""); err != nil {
		test.Fatal(err)
	}
	for _, client := range []*Client{NewClient(&http.Client{Transport: failingTransport{}}), fixture.engine.client} {
		err := client.Test(context.Background(), fixture.server.URL, "wrong-token")
		if err == nil || strings.Contains(err.Error(), fixtureToken) || strings.Contains(err.Error(), "wrong-token") {
			test.Fatal("unsafe relay error", err)
		}
	}
	fixture.clock.advance(time.Minute)
	fixture.snapshot.AppVersion = "changed"
	fixture.engine.client = NewClient(&http.Client{Transport: failingTransport{}})
	state, err := fixture.engine.SyncNow(context.Background())
	if err == nil || state.Status.LastError == "" || strings.Contains(state.Status.LastError, fixtureToken) {
		test.Fatal("unsafe push error", state.Status, err)
	}
}
