package service

import (
	"context"
	"errors"
	"slices"
	"sort"
)

const credentialCleanupKey = "credential-cleanup"

// credentialReferences includes disabled targets because they still own saved credentials.
func credentialReferences(configuration Configuration) map[string]bool {
	references := map[string]bool{}
	for _, connection := range configuration.Connections {
		references[connection.Connection.CredentialId] = true
	}
	for _, hub := range configuration.Hubs {
		references[hub.CredentialId] = true
	}
	delete(references, "")
	return references
}

// queueCredentialCleanup journals retirement candidates before the configuration write.
// Cleanup rechecks committed references, so an interrupted or rejected save cannot delete live keys.
// The caller holds credentialMutex throughout the configuration change.
func (service *Service) queueCredentialCleanup(ctx context.Context, previous, next Configuration) error {
	candidates := credentialReferences(previous)
	for identity := range credentialReferences(next) {
		delete(candidates, identity)
	}
	if len(candidates) == 0 {
		return nil
	}
	var pending []string
	revision, err := service.Store.ReadSetting(ctx, credentialCleanupKey, &pending)
	if err != nil {
		return err
	}
	for _, identity := range pending {
		candidates[identity] = true
	}
	identities := make([]string, 0, len(candidates))
	for identity := range candidates {
		identities = append(identities, identity)
	}
	sort.Strings(identities)
	if slices.Equal(pending, identities) {
		return nil
	}
	_, err = service.Store.WriteSetting(ctx, credentialCleanupKey, revision, identities)
	return err
}

// cleanupRetiredCredentials retries journaled deletions without scanning the shared system keyring.
// Saved and draft references are protected; failures remain journaled for another save or close.
// The caller holds credentialMutex.
func (service *Service) cleanupRetiredCredentials(ctx context.Context) error {
	var pending []string
	revision, err := service.Store.ReadSetting(ctx, credentialCleanupKey, &pending)
	if err != nil || len(pending) == 0 {
		return err
	}
	saved, err := service.Configuration(ctx)
	if err != nil {
		return err
	}
	references := credentialReferences(saved.Value)
	remaining := []string{}
	var failures []error
	for _, identity := range pending {
		if references[identity] {
			continue
		}
		service.mutex.Lock()
		_, isDraft := service.draftCredentials[identity]
		service.mutex.Unlock()
		if isDraft {
			remaining = append(remaining, identity)
			continue
		}
		if err := service.Secrets.Delete(ctx, identity); err != nil {
			failures = append(failures, err)
			remaining = append(remaining, identity)
		}
	}
	if !slices.Equal(pending, remaining) {
		_, err = service.Store.WriteSetting(ctx, credentialCleanupKey, revision, remaining)
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}
