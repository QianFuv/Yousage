package service

import (
	"context"
	"errors"
	"time"
	"yousage/internal/remote"
)

// EnableCursorKeychain applies an explicit opt-in only to the chosen owning environments.
func (service *Service) EnableCursorKeychain(ctx context.Context, identities []string) []remote.SaveOutcome {
	owned, done, err := service.ownOperation(ctx)
	if err != nil {
		return []remote.SaveOutcome{{Error: "Application is closing."}}
	}
	defer done()
	ctx = owned
	outcomes := make([]remote.SaveOutcome, 0, len(identities))
	for _, identity := range identities {
		outcome := remote.SaveOutcome{Id: identity}
		var err error
		if identity == service.LocalId {
			var saved SavedConfiguration
			saved, err = service.Configuration(ctx)
			if err == nil {
				saved.Value.IsCursorKeychainEnabled = true
				_, err = service.SaveConfiguration(ctx, saved)
			}
		} else {
			service.mutex.Lock()
			state := service.connections[identity]
			var client *remote.Client
			if state != nil {
				client = state.client
			}
			service.mutex.Unlock()
			if client == nil {
				err = errors.New("Environment is offline.")
			} else {
				deadline, cancel := context.WithTimeout(ctx, 20*time.Second)
				_, err = client.Call(deadline, "server.updateSettings", map[string]any{"patch": map[string]bool{"cursorKeychainUsageEnabled": true}}, true)
				cancel()
			}
		}
		if err == nil {
			outcome.IsSaved = true
		} else {
			outcome.Error = err.Error()
			var failure *remote.CallError
			if errors.As(err, &failure) {
				outcome.IsOutcomeUnknown = failure.IsOutcomeUnknown
			}
		}
		outcomes = append(outcomes, outcome)
	}
	return outcomes
}
