package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"yousage/internal/accounting"
	"yousage/internal/refresh"
	"yousage/internal/remote"
)

// Usage returns local aggregates and configuration, never raw records, tokens or transcripts.
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
	result.Environments[0].CycleValues = service.localCycleValues(ctx, config)
	if observation, _, err := service.quotas(ctx); err == nil {
		result.Environments[0].Error = observation.Error
	}
	if service.history.Snapshot("local-history").Error != nil {
		result.Environments[0].Error = strings.TrimSpace(result.Environments[0].Error + " Local history could not be refreshed. Cached history is retained.")
	}
	if service.rates.Snapshot("public").Error != nil {
		result.Environments[0].Error = strings.TrimSpace(result.Environments[0].Error + " Public model prices could not be refreshed. Cached prices are retained.")
	}
	return result, nil
}

// RefreshUsage refreshes local history and rates and preserves the last-good summary.
func (service *Service) RefreshUsage(ctx context.Context, days int, zone string, selected []string, request refresh.Request) error {
	if _, err := service.viewWindow(days, zone, true); err != nil {
		return err
	}
	identities := map[string]bool{}
	for _, id := range selected {
		identities[id] = true
	}
	isSelected := func(id string) bool { return selected == nil || identities[id] }
	errorsChannel := make(chan error, 2)
	var pending sync.WaitGroup
	start := func(operation func() error) {
		pending.Add(1)
		go func() { defer pending.Done(); errorsChannel <- operation() }()
	}
	if isSelected(service.LocalId) {
		start(func() error { return service.RefreshHistory(ctx, request) })
		start(func() error { return service.RefreshRates(ctx, request) })
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
