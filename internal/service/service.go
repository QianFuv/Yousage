// Package service owns desktop data lifetimes, saved configuration and last-good observations.
package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"yousage/internal/accounting"
	"yousage/internal/domain"
	"yousage/internal/preferences"
	"yousage/internal/quota"
	"yousage/internal/refresh"
	"yousage/internal/remote"
	"yousage/internal/secrets"
	"yousage/internal/storage"
	"yousage/internal/theme"
)

type NativeProvider struct {
	Id         string `json:"id"`
	Driver     string `json:"driver"`
	Label      string `json:"label"`
	Executable string `json:"executable"`
	Home       string `json:"home"`
	IsEnabled  bool   `json:"enabled"`
}
type ConnectionPreference struct {
	Connection remote.Connection `json:"connection"`
	Label      string            `json:"label"`
	IsEnabled  bool              `json:"enabled"`
}
type Configuration struct {
	Connections             []ConnectionPreference `json:"connections"`
	Providers               []NativeProvider       `json:"providers"`
	Hubs                    []quota.HubConfig      `json:"hubs"`
	PrimaryEnvironmentId    string                 `json:"primaryEnvironmentId"`
	IsCursorKeychainEnabled bool                   `json:"cursorKeychainEnabled"`
}
type SavedConfiguration struct {
	Revision int64         `json:"revision"`
	Value    Configuration `json:"value"`
}

// EnvironmentState presents local or remote usage and local cycle estimates.
type EnvironmentState struct {
	Id           string             `json:"environmentId"`
	Label        string             `json:"label"`
	IsLocal      bool               `json:"local"`
	IsConnected  bool               `json:"connected"`
	CanOperate   bool               `json:"canOperate"`
	Config       *remote.Config     `json:"serverConfig"`
	Summary      *remote.Summary    `json:"summary"`
	Error        string             `json:"error,omitempty"`
	IsRefreshing bool               `json:"refreshing"`
	CycleValues  []quota.CycleValue `json:"cycleValues,omitempty"`
}
type Snapshot struct {
	Window       accounting.Window  `json:"window"`
	Environments []EnvironmentState `json:"environments"`
}
type Service struct {
	Store             *storage.Store
	Preferences       *preferences.Service
	Themes            *theme.Library
	Secrets           secrets.Store
	Connector         remote.Connector
	LocalId           string
	Home              string
	Platform          string
	Environment       map[string]string
	Scope             []domain.Provider
	mutex             sync.Mutex
	themeSync         sync.Mutex
	credentialMutex   sync.Mutex
	cycleMutex        sync.Mutex
	cycleCache        cycleValueCache
	cycleHistories    cycleHistoryCache
	summaryMutex      sync.Mutex
	summaryCaches     map[[32]byte]localSummaryCache
	rateMutex         sync.Mutex
	savedRates        savedRateCache
	installationMutex sync.Mutex
	installations     map[string]nativeInstallation
	viewWindows       map[string]accounting.Window
	RateClient        *http.Client
	owner             context.Context
	cancel            context.CancelFunc
	workers           sync.WaitGroup
	pendingWrites     int
	limitsFollowup    chan struct{}
	isStopping        bool
	draftCredentials  map[string]draftCredential
	connections       map[string]*connectionState
	history           *refresh.Coordinator[bool]
	limits            *refresh.Coordinator[bool]
	rates             *refresh.Coordinator[RateSnapshot]
	keychain          *secrets.CachedReader
	gate              quota.Gate
	Emit              func(string, any)
	Now               func() time.Time
}
type connectionState struct {
	preference ConnectionPreference
	client     *remote.Client
	projection remote.Projection
	cancel     context.CancelFunc
	error      string
}

// New loads durable identity and defaults without inspecting provider credentials or launching probes.
func New(ctx context.Context, store *storage.Store, themes *theme.Library) (*Service, error) {
	owner, cancel := context.WithCancel(ctx)
	service := &Service{Store: store, Preferences: preferences.New(store), Themes: themes, owner: owner, cancel: cancel, connections: map[string]*connectionState{}, draftCredentials: map[string]draftCredential{}, viewWindows: map[string]accounting.Window{}, history: refresh.New[bool](refresh.HistoryPolicy), limits: refresh.New[bool](refresh.LimitsPolicy), rates: refresh.New[RateSnapshot](refresh.RatesPolicy), Now: time.Now, Platform: runtime.GOOS, Environment: map[string]string{}}
	var identity string
	revision, err := store.ReadSetting(ctx, "local-environment-id", &identity)
	if err != nil {
		service.Close()
		return nil, err
	}
	if identity == "" {
		bytes := make([]byte, 16)
		if _, err = rand.Read(bytes); err != nil {
			service.Close()
			return nil, err
		}
		identity = "local-" + hex.EncodeToString(bytes)
		if _, err = store.WriteSetting(ctx, "local-environment-id", revision, identity); err != nil {
			service.Close()
			return nil, err
		}
	}
	service.LocalId = identity
	service.Home, err = os.UserHomeDir()
	if err != nil {
		service.Close()
		return nil, err
	}
	for _, entry := range os.Environ() {
		name, value, found := strings.Cut(entry, "=")
		if found {
			service.Environment[name] = value
		}
	}
	owned, err := secrets.NewOwnedStore()
	if err != nil {
		service.Close()
		return nil, err
	}
	service.Secrets = owned
	service.Connector = remote.Connector{Secrets: owned}
	service.keychain = secrets.NewCachedReader(owned.CursorToken)
	sources, revision, err := service.Preferences.Sources(ctx)
	if err != nil {
		service.Close()
		return nil, err
	}
	if revision == 0 && len(sources) == 0 {
		for _, provider := range []domain.Provider{domain.Codex, domain.Claude} {
			sources = append(sources, preferences.Source{Id: string(provider) + "-auto", Provider: provider, IsEnabled: true, IsAutomatic: true})
		}
		if _, err = service.Preferences.SaveSources(ctx, sources, revision); err != nil {
			service.Close()
			return nil, err
		}
	}
	return service, nil
}
func smallIdentity(root string) string {
	return domain.SourceFingerprint{HostId: "configuration", Provider: domain.OpenCode, ResolvedHomePath: root}.Id()[:12]
}
func (service *Service) emit(name string, value any) {
	if service.Emit != nil && service.owner.Err() == nil {
		service.Emit(name, value)
	}
}

func (service *Service) Configuration(ctx context.Context) (SavedConfiguration, error) {
	value := SavedConfiguration{Value: Configuration{Connections: []ConnectionPreference{}, Providers: []NativeProvider{}, Hubs: []quota.HubConfig{}, PrimaryEnvironmentId: service.LocalId}}
	revision, err := service.Store.ReadSetting(ctx, "configuration", &value.Value)
	value.Revision = revision
	if value.Value.Connections == nil {
		value.Value.Connections = []ConnectionPreference{}
	}
	if value.Value.Providers == nil {
		value.Value.Providers = []NativeProvider{}
	}
	value.Value.Providers = service.scopedProviders(value.Value.Providers)
	if value.Value.Hubs == nil {
		value.Value.Hubs = []quota.HubConfig{}
	}
	return value, err
}

// SaveConfiguration validates bounded explicit targets before replacing the saved draft atomically.
func (service *Service) SaveConfiguration(ctx context.Context, saved SavedConfiguration) (SavedConfiguration, error) {
	ctx, release, err := service.ownOperation(ctx)
	if err != nil {
		return saved, err
	}
	defer release()
	if len(saved.Value.Connections) > 32 || len(saved.Value.Providers) > 32 || len(saved.Value.Hubs) > 32 {
		return saved, errors.New("too many connections or providers")
	}
	seen := map[string]bool{}
	environments := map[string]bool{service.LocalId: true}
	for _, connection := range saved.Value.Connections {
		identity := connection.Connection.EnvironmentId
		if identity == "" || identity == service.LocalId || seen[identity] || len(connection.Label) > 128 {
			return saved, errors.New("invalid or duplicate environment")
		}
		if err := remote.ValidateOrigin(connection.Connection.Url); err != nil {
			return saved, err
		}
		if connection.Connection.CredentialId == "" {
			return saved, errors.New("pair the environment before applying")
		}
		seen[identity] = true
		environments[identity] = true
	}
	for _, provider := range saved.Value.Providers {
		if provider.Id == "" || len(provider.Id) > 96 || len(provider.Label) > 128 || len(provider.Home) > 4096 || (provider.Home != "" && !filepath.IsAbs(provider.Home)) || seen[provider.Id] || (provider.Driver != "codex" && provider.Driver != "claudeAgent" && provider.Driver != "cursor" && provider.Driver != "grok" && provider.Driver != "opencode" && provider.Driver != "antigravity") {
			return saved, errors.New("invalid provider target")
		}
		if !service.isDriverInScope(provider.Driver) {
			return saved, errors.New("provider is not monitored")
		}
		if (provider.Driver == "codex" || provider.Driver == "claudeAgent") && provider.Executable != "" && !filepath.IsAbs(provider.Executable) {
			return saved, errors.New("select an absolute native executable path")
		}
		seen[provider.Id] = true
	}
	for _, hub := range saved.Value.Hubs {
		if hub.Id == "" || seen[hub.Id] || len(hub.Label) > 128 || hub.CredentialId == "" {
			return saved, errors.New("invalid quota hub")
		}
		if err := remote.ValidateOrigin(hub.Url); err != nil {
			return saved, err
		}
		seen[hub.Id] = true
	}
	if !environments[saved.Value.PrimaryEnvironmentId] {
		return saved, errors.New("primary environment is missing")
	}
	service.credentialMutex.Lock()
	defer service.credentialMutex.Unlock()
	previous, err := service.Configuration(ctx)
	if err != nil {
		return saved, err
	}
	existing := map[string]draftCredential{}
	for _, connection := range previous.Value.Connections {
		existing[connection.Connection.CredentialId] = draftCredential{Kind: "environment", Origin: connection.Connection.Url, Target: connection.Connection.EnvironmentId}
	}
	for _, hub := range previous.Value.Hubs {
		existing[hub.CredentialId] = draftCredential{Kind: "hub", Origin: hub.Url}
	}
	service.mutex.Lock()
	validCredential := func(id string, expected draftCredential) bool {
		return existing[id] == expected || service.draftCredentials[id] == expected
	}
	isValid := true
	for _, connection := range saved.Value.Connections {
		isValid = isValid && validCredential(connection.Connection.CredentialId, draftCredential{Kind: "environment", Origin: connection.Connection.Url, Target: connection.Connection.EnvironmentId})
	}
	for _, hub := range saved.Value.Hubs {
		isValid = isValid && validCredential(hub.CredentialId, draftCredential{Kind: "hub", Origin: hub.Url})
	}
	service.mutex.Unlock()
	if !isValid {
		return saved, errors.New("pair or provide a new credential for the changed target")
	}
	revision, err := service.Store.WriteSetting(ctx, "configuration", saved.Revision, saved.Value)
	if err != nil {
		return saved, err
	}
	saved.Revision = revision
	service.mutex.Lock()
	for _, connection := range saved.Value.Connections {
		delete(service.draftCredentials, connection.Connection.CredentialId)
	}
	for _, hub := range saved.Value.Hubs {
		delete(service.draftCredentials, hub.CredentialId)
	}
	service.mutex.Unlock()
	service.ReconcileConnections()
	service.emit("usage-invalidated", nil)
	return saved, nil
}

// Close cancels owned connections and shared refresh work before the application closes its database.
func (service *Service) Close() {
	service.cancel()
	service.mutex.Lock()
	for _, state := range service.connections {
		if state.cancel != nil {
			state.cancel()
		}
		if state.client != nil {
			state.client.Close()
		}
	}
	service.mutex.Unlock()
	service.history.Close()
	service.limits.Close()
	service.rates.Close()
	if service.keychain != nil {
		service.keychain.Close()
	}
	service.workers.Wait()
}

// ownOperation binds foreground writes to shutdown and waits before the database is closed.
func (service *Service) ownOperation(caller context.Context) (context.Context, func(), error) {
	service.mutex.Lock()
	if service.isStopping {
		service.mutex.Unlock()
		return nil, nil, errors.New("application is closing")
	}
	if err := service.owner.Err(); err != nil {
		service.mutex.Unlock()
		return nil, nil, err
	}
	service.workers.Add(1)
	service.pendingWrites++
	service.mutex.Unlock()
	ctx, cancel := context.WithCancel(service.owner)
	stop := context.AfterFunc(caller, cancel)
	return ctx, func() {
		stop()
		cancel()
		service.mutex.Lock()
		service.pendingWrites--
		service.mutex.Unlock()
		service.workers.Done()
	}, nil
}

// HasPendingWrites prevents a deliberate exit from interrupting external mutations.
func (service *Service) HasPendingWrites() bool {
	service.mutex.Lock()
	defer service.mutex.Unlock()
	return service.pendingWrites > 0
}

type draftCredential struct {
	Kind   string
	Origin string
	Target string
}

// BeginWrite tracks local settings writes alongside external mutations during explicit close.
func (service *Service) BeginWrite(ctx context.Context) (context.Context, func(), error) {
	return service.ownOperation(ctx)
}

// PrepareClose atomically refuses new writes after all accepted writes settle.
func (service *Service) PrepareClose(ctx context.Context) error {
	service.mutex.Lock()
	if service.isStopping {
		service.mutex.Unlock()
		return errors.New("application is already closing")
	}
	if service.pendingWrites > 0 {
		service.mutex.Unlock()
		return errors.New("An account or settings change is still running. Wait for its result before closing.")
	}
	service.isStopping = true
	service.mutex.Unlock()
	service.credentialMutex.Lock()
	defer service.credentialMutex.Unlock()
	service.mutex.Lock()
	ids := make([]string, 0, len(service.draftCredentials))
	for id := range service.draftCredentials {
		ids = append(ids, id)
	}
	service.mutex.Unlock()
	for _, id := range ids {
		if err := service.Secrets.Delete(ctx, id); err != nil {
			service.mutex.Lock()
			service.isStopping = false
			service.mutex.Unlock()
			return err
		}
		service.mutex.Lock()
		delete(service.draftCredentials, id)
		service.mutex.Unlock()
	}
	return nil
}
