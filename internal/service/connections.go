package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"yousage/internal/accounting"
	"yousage/internal/preferences"
	"yousage/internal/refresh"
	"yousage/internal/remote"
	"yousage/internal/theme"
)

// ReconcileConnections owns one cancellable stream per enabled verified environment.
func (service *Service) ReconcileConnections() {
	saved, err := service.Configuration(service.owner)
	if err != nil {
		return
	}
	service.mutex.Lock()
	if service.owner.Err() != nil {
		service.mutex.Unlock()
		return
	}
	selected := map[string]ConnectionPreference{}
	for _, value := range saved.Value.Connections {
		if value.IsEnabled {
			selected[value.Connection.EnvironmentId] = value
		}
	}
	for identity, state := range service.connections {
		if preference, exists := selected[identity]; !exists || preference.Connection != state.preference.Connection {
			state.cancel()
			if state.client != nil {
				state.client.Close()
			}
			delete(service.connections, identity)
		}
	}
	for identity, value := range selected {
		if _, exists := service.connections[identity]; exists {
			continue
		}
		ctx, cancel := context.WithCancel(service.owner)
		state := &connectionState{cancel: cancel, preference: value}
		_, _ = service.Store.ReadSetting(ctx, "remote-config:"+identity, &state.projection)
		state.projection = state.projection.Cached()
		service.connections[identity] = state
		service.workers.Add(1)
		go service.connectionLoop(ctx, value, state)
	}
	service.mutex.Unlock()
	service.syncPrimaryTheme()
}

func (service *Service) connectionLoop(ctx context.Context, preference ConnectionPreference, state *connectionState) {
	defer service.workers.Done()
	delay := time.Second
	for ctx.Err() == nil {
		deadline, cancel := context.WithTimeout(ctx, 20*time.Second)
		client, err := service.Connector.Connect(deadline, preference.Connection)
		cancel()
		if err == nil {
			service.mutex.Lock()
			if ctx.Err() != nil || service.connections[preference.Connection.EnvironmentId] != state {
				service.mutex.Unlock()
				client.Close()
				return
			}
			state.client = client
			state.error = ""
			service.mutex.Unlock()
			service.emit("usage-invalidated", nil)
			err = client.Subscribe(ctx, func(raw json.RawMessage) error {
				service.mutex.Lock()
				if ctx.Err() != nil || service.connections[preference.Connection.EnvironmentId] != state {
					service.mutex.Unlock()
					return ctx.Err()
				}
				failure := state.projection.Apply(raw, preference.Connection.EnvironmentId)
				cached := state.projection.Cached()
				if failure != nil {
					state.error = failure.Error()
				} else {
					state.error = ""
				}
				service.mutex.Unlock()
				if failure != nil {
					service.emit("usage-invalidated", nil)
					return failure
				}
				var previous remote.Projection
				revision, readError := service.Store.ReadSetting(ctx, "remote-config:"+preference.Connection.EnvironmentId, &previous)
				if readError == nil {
					_, readError = service.Store.WriteSetting(ctx, "remote-config:"+preference.Connection.EnvironmentId, revision, cached)
				}
				if readError != nil {
					return errors.New("Environment configuration could not be cached.")
				}
				service.syncPrimaryTheme()
				service.emit("usage-invalidated", nil)
				return nil
			})
			client.Close()
			delay = time.Second
		}
		service.mutex.Lock()
		state.client = nil
		state.projection.IsLive = false
		if err != nil {
			state.error = err.Error()
		}
		service.mutex.Unlock()
		service.emit("usage-invalidated", nil)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		delay = min(30*time.Second, delay*2)
	}
}

func (service *Service) syncPrimaryTheme() {
	service.themeSync.Lock()
	defer service.themeSync.Unlock()
	if service.Themes == nil || service.owner.Err() != nil {
		return
	}
	saved, err := service.Configuration(service.owner)
	if err != nil {
		return
	}
	identity := saved.Value.PrimaryEnvironmentId
	if identity == service.LocalId || identity == "" {
		appearance, err := service.Themes.SetPrimaryPublished(service.owner, "local", nil)
		if err == nil {
			service.emit("appearance-snapshot", appearance)
		}
		return
	}
	service.mutex.Lock()
	state := service.connections[identity]
	published := theme.Snapshot{Themes: []theme.Definition{}}
	if state != nil && state.projection.IsReady {
		config := state.projection.Config
		published.Themes = config.Themes
		published.DefaultTheme = config.Settings.DefaultTheme
		published.Generation = config.Settings.DefaultThemeSetAt
	}
	service.mutex.Unlock()
	appearance, err := service.Themes.SetPrimaryPublished(service.owner, identity, &published)
	if err == nil {
		service.emit("appearance-snapshot", appearance)
	}
}

// Pair exchanges a transient explicit bootstrap credential and returns a saved-secret reference only.
func (service *Service) Pair(ctx context.Context, origin, label, bootstrap string, isReadOnly bool) (ConnectionPreference, error) {
	ctx, release, err := service.ownOperation(ctx)
	if err != nil {
		return ConnectionPreference{}, err
	}
	defer release()
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	if len(label) > 128 {
		return ConnectionPreference{}, errors.New("Environment label is too long.")
	}
	descriptor, err := service.Connector.Discover(ctx, remote.Connection{Url: origin})
	if err != nil {
		return ConnectionPreference{}, err
	}
	credential, err := service.newDraftCredential("environment")
	if err != nil {
		return ConnectionPreference{}, err
	}
	connection := remote.Connection{Url: origin, EnvironmentId: descriptor.Id, CredentialId: credential}
	if _, err = service.Connector.Pair(ctx, connection, bootstrap, isReadOnly); err != nil {
		return ConnectionPreference{}, err
	}
	service.mutex.Lock()
	service.draftCredentials[credential] = draftCredential{Kind: "environment", Origin: origin, Target: descriptor.Id}
	service.mutex.Unlock()
	if strings.TrimSpace(label) == "" {
		label = descriptor.Label
	}
	return ConnectionPreference{Connection: connection, Label: label, IsEnabled: true}, nil
}

func windowKey(window accounting.Window) string { data, _ := json.Marshal(window); return string(data) }

// Usage returns aggregates and last-good configuration, never raw records, tokens or transcripts.
func (service *Service) Usage(ctx context.Context, days int, zone string) (Snapshot, error) {
	window, err := service.viewWindow(days, zone, false)
	if err != nil {
		return Snapshot{}, err
	}
	result := Snapshot{Window: window, Environments: []EnvironmentState{}}
	local, err := service.localSummary(ctx, window)
	if err != nil {
		return result, err
	}
	config, err := service.localConfig(ctx)
	if err != nil {
		return result, err
	}
	result.Environments = append(result.Environments, EnvironmentState{Id: service.LocalId, Label: "This device", IsLocal: true, IsConnected: true, CanOperate: true, Config: &config, Summary: &local})
	if observation, _, err := service.quotas(ctx); err == nil {
		result.Environments[0].Error = observation.Error
	}
	saved, err := service.Configuration(ctx)
	if err != nil {
		return result, err
	}
	for _, preference := range saved.Value.Connections {
		state := EnvironmentState{Id: preference.Connection.EnvironmentId, Label: preference.Label}
		service.mutex.Lock()
		if connection := service.connections[state.Id]; connection != nil {
			state.IsConnected = connection.client != nil && connection.client.IsOnline() && connection.projection.IsLive
			state.CanOperate = state.IsConnected && connection.client.Authorization.CanOperate()
			if connection.projection.IsReady {
				config := connection.projection.Config
				state.Config = &config
			}
			state.Error = connection.error
		}
		service.mutex.Unlock()
		var summary remote.Summary
		_, err := service.Store.ReadSetting(ctx, "remote-usage:"+state.Id+":"+windowKey(window), &summary)
		if err != nil {
			state.Error = "Cached usage could not be read."
		} else if summary.ContractVersion != 0 {
			state.Summary = &summary
		}
		if !preference.IsEnabled {
			state.Error = "Connection is disabled. Cached usage is retained."
		}
		result.Environments = append(result.Environments, state)
	}
	return result, nil
}

// RefreshUsage refreshes chosen environments independently and preserves each last-good summary.
func (service *Service) RefreshUsage(ctx context.Context, days int, zone string, selected []string, request refresh.Request) error {
	window, err := service.viewWindow(days, zone, true)
	if err != nil {
		return err
	}
	configuration, err := service.Configuration(ctx)
	if err != nil {
		return err
	}
	identities := map[string]bool{}
	for _, id := range selected {
		identities[id] = true
	}
	isSelected := func(id string) bool { return selected == nil || identities[id] }
	errorsChannel := make(chan error, len(configuration.Value.Connections)+2)
	var pending sync.WaitGroup
	start := func(operation func() error) {
		pending.Add(1)
		go func() { defer pending.Done(); errorsChannel <- operation() }()
	}
	if isSelected(service.LocalId) {
		start(func() error { return service.RefreshHistory(ctx, request) })
		start(func() error { return service.RefreshRates(ctx, request) })
	}
	for _, preference := range configuration.Value.Connections {
		identity := preference.Connection.EnvironmentId
		if !preference.IsEnabled || !isSelected(identity) {
			continue
		}
		start(func() error {
			_, err := service.history.Run(ctx, "remote:"+identity+":"+windowKey(window), request, func(owner context.Context) (bool, error) {
				service.mutex.Lock()
				state := service.connections[identity]
				var client *remote.Client
				if state != nil {
					client = state.client
				}
				service.mutex.Unlock()
				if client == nil {
					return false, errors.New("Environment is offline.")
				}
				deadline, cancel := context.WithTimeout(owner, 20*time.Second)
				defer cancel()
				var rateError error
				if request.IsManual {
					_, rateError = client.Call(deadline, "server.refreshUsageRates", map[string]any{}, false)
					if rateError != nil && (deadline.Err() != nil || !client.IsOnline()) {
						return false, rateError
					}
				}
				summary, err := client.Summary(deadline, window)
				if err != nil {
					service.mutex.Lock()
					state.error = err.Error()
					service.mutex.Unlock()
					service.emit("usage-invalidated", nil)
					return false, err
				}
				var previous remote.Summary
				key := "remote-usage:" + identity + ":" + windowKey(window)
				revision, err := service.Store.ReadSetting(owner, key, &previous)
				if err != nil {
					return false, err
				}
				if _, err = service.Store.WriteSetting(owner, key, revision, summary); err != nil {
					return false, err
				}
				service.mutex.Lock()
				state.error = ""
				if rateError != nil {
					state.error = rateError.Error()
				}
				service.mutex.Unlock()
				service.emit("usage-invalidated", nil)
				return true, rateError
			})
			if errors.Is(err, refresh.ErrSkipped) {
				return nil
			}
			return err
		})
	}
	pending.Wait()
	close(errorsChannel)
	var failures []error
	for err := range errorsChannel {
		if err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// ApplyPricing settles the exact target list independently; callers retain only failed target drafts.
func (service *Service) ApplyPricing(ctx context.Context, identities []string, patch preferences.PricingPatch) []remote.SaveOutcome {
	owned, done, err := service.ownOperation(ctx)
	if err != nil {
		outcomes := make([]remote.SaveOutcome, len(identities))
		for index, identity := range identities {
			outcomes[index] = remote.SaveOutcome{Id: identity, Error: "Application is closing."}
		}
		return outcomes
	}
	defer done()
	ctx = owned

	results := make([]remote.SaveOutcome, len(identities))
	var pending sync.WaitGroup
	for index, identity := range identities {
		pending.Add(1)
		go func() {
			defer pending.Done()
			outcome := remote.SaveOutcome{Id: identity}
			if identity == service.LocalId {
				saved, err := service.Preferences.Pricing(ctx, identity)
				if err == nil {
					_, err = service.Preferences.PatchPricing(ctx, identity, saved.Revision, patch)
				}
				if err != nil {
					outcome.Error = err.Error()
				} else {
					outcome.IsSaved = true
				}
			} else {
				service.mutex.Lock()
				state := service.connections[identity]
				var client *remote.Client
				if state != nil {
					client = state.client
				}
				service.mutex.Unlock()
				settled := remote.SaveSettings(ctx, []remote.SaveTarget{{Id: identity, Client: client}}, remote.SettingsPatch{Prices: patch.Overrides, Aliases: patch.Aliases})
				outcome = settled[0]
			}
			results[index] = outcome
		}()
	}
	pending.Wait()
	service.emit("usage-invalidated", nil)
	return results
}

// viewWindow anchors rolling queries across completion, cache reads and offline navigation.
func (service *Service) viewWindow(days int, zone string, shouldAdvance bool) (accounting.Window, error) {
	service.mutex.Lock()
	defer service.mutex.Unlock()
	key := fmt.Sprintf("%d:%s", days, zone)
	if current, ok := service.viewWindows[key]; ok && !shouldAdvance {
		return current, nil
	}
	var cached accounting.Window
	revision, readError := service.Store.ReadSetting(service.owner, "view-window:"+key, &cached)
	if readError != nil {
		return accounting.Window{}, readError
	}
	if !shouldAdvance && cached.TimeZone == zone && (cached.Resolution == "day" || cached.Resolution == "hour") {
		service.viewWindows[key] = cached
		return cached, nil
	}
	window, err := accounting.BuildWindow(service.Now(), days, zone)
	if err == nil {
		_, err = service.Store.WriteSetting(service.owner, "view-window:"+key, revision, window)
	}
	if err == nil {
		service.viewWindows[key] = window
	}
	return window, err
}

// newDraftCredential isolates uncommitted authentication from saved live connections.
func (service *Service) newDraftCredential(prefix string) (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return prefix + "-" + hex.EncodeToString(value), nil
}

// StageHubCredential saves a fresh native credential without replacing a saved account.
func (service *Service) StageHubCredential(ctx context.Context, origin, value string) (string, error) {
	ctx, release, err := service.ownOperation(ctx)
	if err != nil {
		return "", err
	}
	defer release()
	if err := remote.ValidateOrigin(origin); err != nil {
		return "", err
	}
	identity, err := service.newDraftCredential("hub")
	if err != nil {
		return "", err
	}
	if err := service.Secrets.Put(ctx, identity, value); err != nil {
		return "", err
	}
	service.mutex.Lock()
	service.draftCredentials[identity] = draftCredential{Kind: "hub", Origin: origin}
	service.mutex.Unlock()
	return identity, nil
}

// DiscardDraftCredentials removes only this running session's unused draft references.
func (service *Service) DiscardDraftCredentials(ctx context.Context, identities []string) error {
	ctx, release, err := service.ownOperation(ctx)
	if err != nil {
		return err
	}
	defer release()
	service.credentialMutex.Lock()
	defer service.credentialMutex.Unlock()
	service.mutex.Lock()
	defer service.mutex.Unlock()
	var failures []error
	for _, identity := range identities {
		if _, exists := service.draftCredentials[identity]; !exists {
			continue
		}
		if err := service.Secrets.Delete(ctx, identity); err != nil {
			failures = append(failures, err)
		} else {
			delete(service.draftCredentials, identity)
		}
	}
	return errors.Join(failures...)
}
