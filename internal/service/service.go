// Package service owns desktop data lifetimes, saved configuration and last-good observations.
package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"yousage/internal/accounting"
	"yousage/internal/domain"
	"yousage/internal/history"
	"yousage/internal/history/antigravity"
	"yousage/internal/history/cursor"
	"yousage/internal/history/opencode"
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
type EnvironmentState struct {
	Id           string          `json:"environmentId"`
	Label        string          `json:"label"`
	IsLocal      bool            `json:"local"`
	IsConnected  bool            `json:"connected"`
	CanOperate   bool            `json:"canOperate"`
	Config       *remote.Config  `json:"serverConfig"`
	Summary      *remote.Summary `json:"summary"`
	Error        string          `json:"error,omitempty"`
	IsRefreshing bool            `json:"refreshing"`
}
type Snapshot struct {
	Window       accounting.Window  `json:"window"`
	Environments []EnvironmentState `json:"environments"`
}
type Service struct {
	Store       *storage.Store
	Preferences *preferences.Service
	Themes      *theme.Library
	Secrets     secrets.Store
	Connector   remote.Connector
	LocalId     string
	Home        string
	Platform    string
	Environment map[string]string
	mutex       sync.Mutex
	themeSync   sync.Mutex
	viewWindows map[string]accounting.Window
	RateClient  *http.Client
	owner       context.Context
	cancel      context.CancelFunc
	workers     sync.WaitGroup
	connections map[string]*connectionState
	history     *refresh.Coordinator[bool]
	limits      *refresh.Coordinator[bool]
	rates       *refresh.Coordinator[RateSnapshot]
	keychain    *secrets.CachedReader
	gate        quota.Gate
	Emit        func(string, any)
	Now         func() time.Time
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
	service := &Service{Store: store, Preferences: preferences.New(store), Themes: themes, owner: owner, cancel: cancel, connections: map[string]*connectionState{}, viewWindows: map[string]accounting.Window{}, history: refresh.New[bool](refresh.HistoryPolicy), limits: refresh.New[bool](refresh.LimitsPolicy), rates: refresh.New[RateSnapshot](refresh.RatesPolicy), Now: time.Now, Platform: runtime.GOOS, Environment: map[string]string{}}
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
		roots, err := history.DefaultRoots()
		if err != nil {
			service.Close()
			return nil, err
		}
		for _, root := range roots {
			sources = append(sources, preferences.Source{Id: string(root.Provider) + "-default", Provider: root.Provider, Root: root.Home, IsEnabled: true})
		}
		openRoots, err := opencode.DiscoverRoots(service.Home)
		if err != nil {
			service.Close()
			return nil, err
		}
		for _, root := range openRoots {
			sources = append(sources, preferences.Source{Id: "opencode-" + smallIdentity(root), Provider: domain.OpenCode, Root: root, IsEnabled: true})
		}
		gravityRoots, err := antigravity.DiscoverRoots(service.Home, nil)
		if err != nil {
			service.Close()
			return nil, err
		}
		for _, root := range gravityRoots {
			sources = append(sources, preferences.Source{Id: "antigravity-" + smallIdentity(root), Provider: domain.Antigravity, Root: root, IsEnabled: true})
		}
		credentials := cursor.DiscoverCredentials(service.Platform, service.Home, service.Environment, false)
		root := credentials.Path
		if root == "" {
			root = filepath.Join(service.Home, ".cursor", "auth.json")
		}
		sources = append(sources, preferences.Source{Id: "cursor-default", Provider: domain.Cursor, Root: root, IsEnabled: true})
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
	if value.Value.Hubs == nil {
		value.Value.Hubs = []quota.HubConfig{}
	}
	return value, err
}

// SaveConfiguration validates bounded explicit targets before replacing the saved draft atomically.
func (service *Service) SaveConfiguration(ctx context.Context, saved SavedConfiguration) (SavedConfiguration, error) {
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
		parsed, err := url.Parse(connection.Connection.Url)
		if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.RawQuery != "" || parsed.Fragment != "" || connection.Connection.CredentialId == "" {
			return saved, errors.New("invalid environment connection")
		}
		seen[identity] = true
		environments[identity] = true
	}
	for _, provider := range saved.Value.Providers {
		if provider.Id == "" || seen[provider.Id] || (provider.Driver != "codex" && provider.Driver != "claudeAgent" && provider.Driver != "cursor" && provider.Driver != "grok" && provider.Driver != "opencode" && provider.Driver != "antigravity") {
			return saved, errors.New("invalid provider target")
		}
		if (provider.Driver == "codex" || provider.Driver == "claudeAgent") && !filepath.IsAbs(provider.Executable) {
			return saved, errors.New("select an absolute native executable path")
		}
		seen[provider.Id] = true
	}
	for _, hub := range saved.Value.Hubs {
		parsed, err := url.Parse(hub.Url)
		if hub.Id == "" || seen[hub.Id] || len(hub.Label) > 128 || hub.CredentialId == "" || err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.RawQuery != "" || parsed.Fragment != "" {
			return saved, errors.New("invalid quota hub")
		}
		seen[hub.Id] = true
	}
	if !environments[saved.Value.PrimaryEnvironmentId] {
		return saved, errors.New("primary environment is missing")
	}
	revision, err := service.Store.WriteSetting(ctx, "configuration", saved.Revision, saved.Value)
	if err != nil {
		return saved, err
	}
	saved.Revision = revision
	service.ReconcileConnections()
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
	if err := service.owner.Err(); err != nil {
		service.mutex.Unlock()
		return nil, nil, err
	}
	service.workers.Add(1)
	service.mutex.Unlock()
	ctx, cancel := context.WithCancel(service.owner)
	stop := context.AfterFunc(caller, cancel)
	return ctx, func() { stop(); cancel(); service.workers.Done() }, nil
}
