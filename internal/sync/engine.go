package sync

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	stdsync "sync"
	"time"
	"yousage/internal/preferences"
	"yousage/internal/secrets"
	"yousage/internal/service"
)

const tokenId = "relay-token"
const spaceKeyId = "relay-space-key"
const pushDebounce = 10 * time.Second
const minimumPushInterval = time.Minute

// Timer permits deterministic debounce scheduling in tests.
type Timer interface{ Stop() bool }

// EngineOptions supplies local persistence, builders, networking and an injectable clock.
type EngineOptions struct {
	Preferences   *preferences.Service
	Secrets       secrets.Store
	BuildSnapshot func(context.Context, string) (Snapshot, error)
	BuildShare    func(context.Context, ShareRequest) (SharePayload, error)
	HttpClient    *http.Client
	Now           func() time.Time
	AfterFunc     func(time.Duration, func()) Timer
	OnChanged     func(State)
}

// Engine serializes relay writes and owns one coalesced pending push.
type Engine struct {
	mutex           stdsync.Mutex
	operation       stdsync.Mutex
	context         context.Context
	cancel          context.CancelFunc
	options         EngineOptions
	client          *Client
	settings        Settings
	revision        int64
	token           string
	spaceKey        string
	status          Status
	timer           Timer
	timerGeneration uint64
	lastUpload      time.Time
	lastHash        [sha256.Size]byte
	lastSnapshot    Snapshot
	hasHash         bool
	isClosed        bool
	workers         stdsync.WaitGroup
}

// NewEngine loads preferences and keyring values without contacting the relay.
func NewEngine(ctx context.Context, options EngineOptions) (*Engine, error) {
	if options.Preferences == nil || options.Secrets == nil || options.BuildSnapshot == nil || options.BuildShare == nil {
		return nil, errors.New("sync engine dependencies are missing")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.AfterFunc == nil {
		options.AfterFunc = func(delay time.Duration, callback func()) Timer { return time.AfterFunc(delay, callback) }
	}
	owner, cancel := context.WithCancel(ctx)
	engine := &Engine{context: owner, cancel: cancel, options: options, client: NewClient(options.HttpClient), settings: Settings{TimeZone: "UTC", Shares: []ShareRecord{}}}
	var err error
	engine.revision, err = options.Preferences.Sync(ctx, &engine.settings)
	if err == nil {
		err = engine.settings.Validate()
	}
	if err != nil {
		cancel()
		return nil, err
	}
	engine.token, err = options.Secrets.Get(ctx, tokenId)
	if err != nil && !errors.Is(err, secrets.ErrMissing) {
		engine.status.LastError = "system credential store is unavailable"
	}
	engine.spaceKey, err = options.Secrets.Get(ctx, spaceKeyId)
	if err != nil && !errors.Is(err, secrets.ErrMissing) {
		engine.status.LastError = "system credential store is unavailable"
	}
	if engine.settings.IsEnabled && (engine.token == "" || engine.spaceKey == "") {
		engine.status.LastError = "sync credentials are unavailable; save the relay token and rotate pairing"
	}
	return engine, nil
}

// Close cancels scheduled and in-flight uploads and waits before local services close.
func (engine *Engine) Close() {
	engine.mutex.Lock()
	engine.isClosed = true
	engine.cancelTimerLocked()
	engine.cancel()
	engine.mutex.Unlock()
	engine.workers.Wait()
	engine.operation.Lock()
	engine.operation.Unlock()
}

// Settings returns a copy safe for background eligibility and time-zone callbacks.
func (engine *Engine) Settings() Settings {
	engine.mutex.Lock()
	defer engine.mutex.Unlock()
	settings := engine.settings
	settings.Shares = append([]ShareRecord{}, settings.Shares...)
	return settings
}

func (engine *Engine) persistLocked(ctx context.Context, settings Settings) error {
	revision, err := engine.options.Preferences.SaveSync(ctx, settings, engine.revision)
	if err != nil {
		return errors.New("could not save sync preferences")
	}
	engine.settings, engine.revision = settings, revision
	return nil
}

func (engine *Engine) stateLocked() State {
	state := State{IsEnabled: engine.settings.IsEnabled, RelayUrl: engine.settings.RelayUrl, HasToken: engine.token != "", Status: engine.status, PairingQrModules: []string{}, Shares: []ShareView{}}
	if state.IsEnabled && engine.settings.SpaceId != "" && engine.spaceKey != "" {
		state.PairingUrl = engine.settings.RelayUrl + "/v/" + engine.settings.SpaceId + "#" + engine.spaceKey
		state.PairingQrModules, _ = service.QrModules(state.PairingUrl)
	}
	for _, record := range engine.settings.Shares {
		expires, err := time.Parse(time.RFC3339, record.ExpiresAt)
		if err != nil || !expires.After(engine.options.Now()) {
			continue
		}
		modules, _ := service.QrModules(record.Url)
		state.Shares = append(state.Shares, ShareView{record, modules})
	}
	return state
}

// State prunes expired local links before returning the current public state.
func (engine *Engine) State(ctx context.Context) (State, error) {
	engine.mutex.Lock()
	settings := engine.settings
	settings.Shares = []ShareRecord{}
	for _, record := range engine.settings.Shares {
		expires, err := time.Parse(time.RFC3339, record.ExpiresAt)
		if err == nil && expires.After(engine.options.Now()) {
			settings.Shares = append(settings.Shares, record)
		}
	}
	var err error
	hasChanged := len(settings.Shares) != len(engine.settings.Shares)
	if hasChanged {
		err = engine.persistLocked(ctx, settings)
	}
	state := engine.stateLocked()
	engine.mutex.Unlock()
	if hasChanged && err == nil && engine.options.OnChanged != nil {
		engine.options.OnChanged(state)
	}
	return state, err
}

func (engine *Engine) emit() {
	if engine.options.OnChanged == nil {
		return
	}
	engine.mutex.Lock()
	state := engine.stateLocked()
	engine.mutex.Unlock()
	engine.options.OnChanged(state)
}

func newIdentity() (string, error) {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return "", errors.New("could not create pairing identity")
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

// Save validates settings and commits secrets to the keyring before enabling sync.
func (engine *Engine) Save(ctx context.Context, input SaveInput) (State, error) {
	engine.operation.Lock()
	err := engine.save(ctx, input)
	engine.operation.Unlock()
	engine.emit()
	if err == nil && input.IsEnabled {
		engine.schedule(0)
	}
	state, stateError := engine.State(ctx)
	if err != nil {
		return state, err
	}
	return state, stateError
}

func (engine *Engine) save(ctx context.Context, input SaveInput) error {
	origin := ""
	var err error
	if input.RelayUrl != "" || input.IsEnabled {
		origin, err = NormalizeRelayUrl(input.RelayUrl)
		if err != nil {
			return err
		}
	}
	if len(input.Token) > 2048 {
		return errors.New("relay token exceeds the keyring limit")
	}
	engine.mutex.Lock()
	defer engine.mutex.Unlock()
	if engine.isClosed {
		return errors.New("sync engine is closed")
	}
	settings := engine.settings
	settings.RelayUrl, settings.IsEnabled = origin, input.IsEnabled
	token, key := engine.token, engine.spaceKey
	if input.Token != "" {
		token = input.Token
	}
	if input.IsEnabled && token == "" {
		return errors.New("a relay token is required to enable sync")
	}
	if input.IsEnabled && settings.SpaceId == "" {
		if settings.SpaceId, err = newIdentity(); err != nil {
			return err
		}
		if key, err = NewKey(); err != nil {
			return err
		}
	}
	if input.IsEnabled {
		if _, err = decodeKey(key); err != nil {
			return errors.New("pairing key is unavailable; rotate pairing")
		}
		if err = engine.options.Secrets.Put(ctx, spaceKeyId, key); err != nil {
			return errors.New("system credential store could not save the pairing key")
		}
	}
	if input.Token != "" || input.IsEnabled {
		if err = engine.options.Secrets.Put(ctx, tokenId, token); err != nil {
			engine.restoreSecrets(ctx)
			return errors.New("system credential store could not save the relay token")
		}
	}
	previous := engine.settings
	if err = engine.persistLocked(ctx, settings); err != nil {
		engine.restoreSecrets(ctx)
		return err
	}
	engine.token, engine.spaceKey = token, key
	if previous.RelayUrl != settings.RelayUrl || previous.SpaceId != settings.SpaceId {
		engine.hasHash = false
		engine.lastUpload = time.Time{}
	}
	if !input.IsEnabled {
		engine.cancelTimerLocked()
	}
	return nil
}

func (engine *Engine) restoreSecrets(ctx context.Context) {
	for id, value := range map[string]string{tokenId: engine.token, spaceKeyId: engine.spaceKey} {
		if value == "" {
			engine.options.Secrets.Delete(ctx, id)
		} else {
			engine.options.Secrets.Put(ctx, id, value)
		}
	}
}

// RecordTimeZone persists the last frontend IANA zone only when its value changes.
func (engine *Engine) RecordTimeZone(ctx context.Context, timeZone string) error {
	if timeZone == "" {
		timeZone = "UTC"
	}
	if _, err := time.LoadLocation(timeZone); err != nil {
		timeZone = "UTC"
	}
	engine.mutex.Lock()
	if engine.isClosed {
		engine.mutex.Unlock()
		return errors.New("sync engine is closed")
	}
	if engine.settings.TimeZone == timeZone {
		engine.mutex.Unlock()
		return nil
	}
	settings := engine.settings
	settings.TimeZone = timeZone
	err := engine.persistLocked(ctx, settings)
	engine.mutex.Unlock()
	if err == nil {
		engine.emit()
		engine.Trigger()
	}
	return err
}

// TestRelay checks an explicitly requested relay connection, using the saved token when omitted.
func (engine *Engine) TestRelay(ctx context.Context, relayUrl, token string) error {
	engine.operation.Lock()
	defer engine.operation.Unlock()
	engine.mutex.Lock()
	if engine.isClosed {
		engine.mutex.Unlock()
		return errors.New("sync engine is closed")
	}
	if token == "" {
		token = engine.token
	}
	engine.mutex.Unlock()
	if token == "" {
		return errors.New("a relay token is required to test authorization")
	}
	owner, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(engine.context, cancel)
	defer stop()
	defer cancel()
	return engine.client.Test(owner, relayUrl, token)
}

func (engine *Engine) cancelTimerLocked() {
	engine.timerGeneration++
	if engine.timer != nil {
		engine.timer.Stop()
		engine.timer = nil
	}
}

// Trigger coalesces refresh completions into a debounced, rate-limited push.
func (engine *Engine) Trigger() { engine.schedule(pushDebounce) }

func (engine *Engine) schedule(delay time.Duration) {
	engine.mutex.Lock()
	defer engine.mutex.Unlock()
	if engine.isClosed || !engine.settings.IsEnabled {
		return
	}
	engine.cancelTimerLocked()
	if !engine.lastUpload.IsZero() {
		if remaining := engine.lastUpload.Add(minimumPushInterval).Sub(engine.options.Now()); remaining > delay {
			delay = remaining
		}
	}
	generation := engine.timerGeneration
	engine.timer = engine.options.AfterFunc(delay, func() {
		engine.mutex.Lock()
		if engine.isClosed || generation != engine.timerGeneration {
			engine.mutex.Unlock()
			return
		}
		engine.timer = nil
		engine.workers.Add(1)
		engine.mutex.Unlock()
		defer engine.workers.Done()
		engine.push(engine.context, false)
	})
}

// SyncNow bypasses automatic scheduling while retaining the unchanged-content check.
func (engine *Engine) SyncNow(ctx context.Context) (State, error) {
	err := engine.push(ctx, true)
	state, stateError := engine.State(ctx)
	if err != nil {
		return state, err
	}
	return state, stateError
}

func (engine *Engine) push(ctx context.Context, shouldBypassInterval bool) error {
	engine.operation.Lock()
	defer engine.operation.Unlock()
	return engine.pushOwned(ctx, shouldBypassInterval)
}

func (engine *Engine) pushOwned(ctx context.Context, shouldBypassInterval bool) (pushError error) {
	engine.mutex.Lock()
	if engine.isClosed || !engine.settings.IsEnabled {
		engine.mutex.Unlock()
		return nil
	}
	if !engine.lastUpload.IsZero() && engine.options.Now().Sub(engine.lastUpload) < minimumPushInterval && !shouldBypassInterval {
		engine.mutex.Unlock()
		engine.schedule(0)
		return nil
	}
	settings, token, key := engine.settings, engine.token, engine.spaceKey
	engine.status.IsPushing = true
	engine.mutex.Unlock()
	engine.emit()
	defer func() {
		engine.mutex.Lock()
		engine.status.IsPushing = false
		engine.status.LastError = ""
		if pushError != nil {
			engine.status.LastError = pushError.Error()
		}
		engine.mutex.Unlock()
		engine.emit()
	}()
	if token == "" || key == "" {
		return errors.New("sync credentials are unavailable")
	}
	owner, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(engine.context, cancel)
	defer stop()
	defer cancel()
	snapshot, err := engine.options.BuildSnapshot(owner, settings.TimeZone)
	if err != nil {
		return errors.New("could not build a complete dashboard snapshot")
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return errors.New("could not encode dashboard snapshot")
	}
	hash := sha256.Sum256(data)
	engine.mutex.Lock()
	hasHash, previous, previousHash := engine.hasHash, engine.lastSnapshot, engine.lastHash
	engine.mutex.Unlock()
	if hasHash {
		comparison, err := json.Marshal(reuseSnapshotMetadata(snapshot, previous))
		if err != nil {
			return errors.New("could not encode dashboard snapshot")
		}
		if sha256.Sum256(comparison) == previousHash {
			return nil
		}
	}
	envelope, err := Seal(data, key, "space:"+settings.SpaceId)
	if err != nil {
		return err
	}
	if err = engine.client.put(owner, settings.RelayUrl, token, "spaces", settings.SpaceId, envelope); err != nil {
		return err
	}
	engine.mutex.Lock()
	engine.lastHash, engine.hasHash, engine.lastUpload = hash, true, engine.options.Now()
	engine.lastSnapshot = snapshot
	engine.status.LastPushAt = engine.lastUpload.UTC().Format(time.RFC3339)
	engine.mutex.Unlock()
	return nil
}

// RotatePairing replaces the private identity and key, deletes the old space best effort, and pushes.
func (engine *Engine) RotatePairing(ctx context.Context) (State, error) {
	owner, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(engine.context, cancel)
	defer stop()
	defer cancel()
	engine.operation.Lock()
	err := engine.rotate(owner)
	engine.operation.Unlock()
	engine.emit()
	state, stateError := engine.State(ctx)
	if err != nil {
		return state, err
	}
	return state, stateError
}

func (engine *Engine) rotate(ctx context.Context) error {
	engine.mutex.Lock()
	if engine.isClosed || !engine.settings.IsEnabled {
		engine.mutex.Unlock()
		return errors.New("enable sync before rotating pairing")
	}
	settings, oldKey, token := engine.settings, engine.spaceKey, engine.token
	engine.mutex.Unlock()
	oldId := settings.SpaceId
	id, err := newIdentity()
	if err != nil {
		return err
	}
	key, err := NewKey()
	if err != nil {
		return err
	}
	if err = engine.options.Secrets.Put(ctx, spaceKeyId, key); err != nil {
		return errors.New("system credential store could not save the pairing key")
	}
	engine.mutex.Lock()
	settings = engine.settings
	settings.SpaceId = id
	if err = engine.persistLocked(ctx, settings); err != nil {
		engine.mutex.Unlock()
		if oldKey != "" {
			engine.options.Secrets.Put(ctx, spaceKeyId, oldKey)
		}
		return err
	}
	engine.spaceKey, engine.hasHash, engine.lastUpload = key, false, time.Time{}
	engine.cancelTimerLocked()
	engine.mutex.Unlock()
	if oldId != "" {
		engine.client.delete(ctx, settings.RelayUrl, token, "spaces", oldId)
	}
	return engine.pushOwned(ctx, true)
}

// PreviewShare builds a local payload without using the relay or enabling sync.
func (engine *Engine) PreviewShare(ctx context.Context, request ShareRequest) (SharePayload, error) {
	if request.TimeZone == "" {
		request.TimeZone = "UTC"
	}
	if err := request.Validate(); err != nil {
		return SharePayload{}, err
	}
	payload, err := engine.options.BuildShare(ctx, request)
	if err != nil {
		return SharePayload{}, errors.New("could not build receipt share")
	}
	return payload, nil
}

// CreateShare reserves an identity, encrypts once with a fresh key, uploads, then persists its link.
func (engine *Engine) CreateShare(ctx context.Context, request ShareRequest) (ShareView, error) {
	owner, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(engine.context, cancel)
	defer stop()
	defer cancel()
	ctx = owner
	engine.operation.Lock()
	defer engine.operation.Unlock()
	engine.mutex.Lock()
	settings, token := engine.settings, engine.token
	isClosed := engine.isClosed
	engine.mutex.Unlock()
	if isClosed || !settings.IsEnabled {
		return ShareView{}, errors.New("enable sync before creating a share")
	}
	payload, err := engine.PreviewShare(ctx, request)
	if err != nil {
		return ShareView{}, err
	}
	reserved, err := engine.client.reserve(ctx, settings.RelayUrl, token, request.TtlDays)
	if err != nil {
		return ShareView{}, err
	}
	hasPersisted := false
	defer func() {
		if !hasPersisted {
			engine.client.delete(ctx, settings.RelayUrl, token, "shares", reserved.Id)
		}
	}()
	payload.CreatedAt, payload.ExpiresAt = engine.options.Now().UTC().Format(time.RFC3339), reserved.ExpiresAt
	key, err := NewKey()
	if err != nil {
		return ShareView{}, err
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return ShareView{}, errors.New("could not encode receipt share")
	}
	envelope, err := Seal(data, key, "share:"+reserved.Id)
	if err != nil {
		return ShareView{}, err
	}
	if err = engine.client.put(ctx, settings.RelayUrl, token, "shares", reserved.Id, envelope); err != nil {
		return ShareView{}, err
	}
	record := ShareRecord{reserved.Id, settings.RelayUrl + "/s/" + reserved.Id + "#" + key, request.Days, request.TtlDays, request.HideAmounts, payload.CreatedAt, payload.ExpiresAt}
	modules, err := service.QrModules(record.Url)
	if err != nil {
		return ShareView{}, errors.New("could not encode share QR")
	}
	engine.mutex.Lock()
	settings = engine.settings
	settings.Shares = append(append([]ShareRecord{}, settings.Shares...), record)
	err = engine.persistLocked(ctx, settings)
	engine.mutex.Unlock()
	if err != nil {
		return ShareView{}, err
	}
	hasPersisted = true
	engine.emit()
	return ShareView{record, modules}, nil
}

// RevokeShare deletes the selected relay blob, tolerating 404, before removing its local record.
func (engine *Engine) RevokeShare(ctx context.Context, id string) (State, error) {
	owner, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(engine.context, cancel)
	defer stop()
	defer cancel()
	engine.operation.Lock()
	err := engine.revoke(owner, id)
	engine.operation.Unlock()
	engine.emit()
	state, stateError := engine.State(ctx)
	if err != nil {
		return state, err
	}
	return state, stateError
}

func (engine *Engine) revoke(ctx context.Context, id string) error {
	engine.mutex.Lock()
	settings, token := engine.settings, engine.token
	isClosed := engine.isClosed
	engine.mutex.Unlock()
	if isClosed || !settings.IsEnabled {
		return errors.New("enable sync before revoking a share")
	}
	position := -1
	for index, record := range settings.Shares {
		if record.Id == id {
			position = index
			break
		}
	}
	if position == -1 {
		return errors.New("share is not saved on this desktop")
	}
	record := settings.Shares[position]
	if !strings.HasPrefix(record.Url, settings.RelayUrl+"/s/") {
		return errors.New("share belongs to another relay; reconnect that relay before revoking it")
	}
	if err := engine.client.delete(ctx, settings.RelayUrl, token, "shares", id); err != nil {
		return err
	}
	engine.mutex.Lock()
	defer engine.mutex.Unlock()
	settings = engine.settings
	shares := make([]ShareRecord, 0, len(settings.Shares))
	for _, record := range settings.Shares {
		if record.Id != id {
			shares = append(shares, record)
		}
	}
	settings.Shares = shares
	return engine.persistLocked(ctx, settings)
}
