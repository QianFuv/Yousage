package service

import (
	"context"
	"errors"
	"testing"
	"yousage/internal/quota"
	"yousage/internal/remote"
	"yousage/internal/storage"
)

type memoryCredentials map[string]string

func (values memoryCredentials) Get(ctx context.Context, id string) (string, error) {
	return values[id], ctx.Err()
}

// TestSavedCredentialRetirementDeletesOnlyUnreferencedKeys protects active, draft and sync credentials.
func TestSavedCredentialRetirementDeletesOnlyUnreferencedKeys(test *testing.T) {
	service := testService(test)
	credentials := memoryCredentials{"old-hub": "old", "old-remote": "remote", "relay-token": "relay", "relay-space-key": "space"}
	service.Secrets = credentials
	ctx := context.Background()
	saved, _ := service.Configuration(ctx)
	saved.Value.Hubs = []quota.HubConfig{
		{Id: "first", Url: "https://hub.invalid", CredentialId: "old-hub"},
		{Id: "second", Url: "https://hub.invalid", CredentialId: "old-hub"},
	}
	saved.Value.Connections = []ConnectionPreference{{Connection: remote.Connection{Url: "https://remote.invalid", EnvironmentId: "remote", CredentialId: "old-remote"}}}
	if _, err := service.Store.WriteSetting(ctx, "configuration", saved.Revision, saved.Value); err != nil {
		test.Fatal(err)
	}
	saved, _ = service.Configuration(ctx)
	draft, err := service.StageHubCredential(ctx, "https://draft.invalid", "draft")
	if err != nil {
		test.Fatal(err)
	}
	saved.Value.Hubs = saved.Value.Hubs[1:]
	saved.Value.Connections = nil
	saved, err = service.SaveConfiguration(ctx, saved)
	if err != nil || credentials["old-remote"] != "" || credentials["old-hub"] != "old" {
		test.Fatal("removed remote was retained or shared disabled hub was deleted", err)
	}
	replacement, err := service.StageHubCredential(ctx, "https://hub.invalid", "replacement")
	if err != nil {
		test.Fatal(err)
	}
	saved.Value.Hubs[0].CredentialId = replacement
	if _, err = service.SaveConfiguration(ctx, saved); err != nil {
		test.Fatal(err)
	}
	if credentials["old-hub"] != "" || credentials[replacement] != "replacement" || credentials[draft] != "draft" || credentials["relay-token"] != "relay" || credentials["relay-space-key"] != "space" {
		test.Fatal("retirement deleted an active, draft or unrelated credential, or retained the old key")
	}
}

// TestCredentialSaveConflictDoesNotDeleteLiveKey keeps external deletion after a successful commit only.
func TestCredentialSaveConflictDoesNotDeleteLiveKey(test *testing.T) {
	service := testService(test)
	credentials := memoryCredentials{"saved": "live"}
	service.Secrets = credentials
	ctx := context.Background()
	saved, _ := service.Configuration(ctx)
	saved.Value.Hubs = []quota.HubConfig{{Id: "hub", Url: "https://hub.invalid", CredentialId: "saved"}}
	if _, err := service.Store.WriteSetting(ctx, "configuration", saved.Revision, saved.Value); err != nil {
		test.Fatal(err)
	}
	saved, _ = service.Configuration(ctx)
	if _, err := service.Store.WriteSetting(ctx, "configuration", saved.Revision, saved.Value); err != nil {
		test.Fatal(err)
	}
	saved.Value.Hubs = nil
	if _, err := service.SaveConfiguration(ctx, saved); !errors.Is(err, storage.ErrConflict) || credentials["saved"] != "live" {
		test.Fatal("a stale save deleted a live credential", err)
	}
	if err := service.PrepareClose(ctx); err != nil || credentials["saved"] != "live" {
		test.Fatal("cleanup after conflict deleted a live credential", err)
	}
}

type retryCredentials struct {
	memoryCredentials
	shouldFailDelete bool
}

// Delete simulates a temporarily unavailable system credential manager.
func (store *retryCredentials) Delete(ctx context.Context, identity string) error {
	if store.shouldFailDelete {
		return errors.New("credential manager unavailable")
	}
	return store.memoryCredentials.Delete(ctx, identity)
}

// TestCredentialCleanupSurvivesRestart retries native failures without reporting a committed save as failed.
func TestCredentialCleanupSurvivesRestart(test *testing.T) {
	service := testService(test)
	credentials := &retryCredentials{memoryCredentials: memoryCredentials{"saved": "live"}, shouldFailDelete: true}
	service.Secrets = credentials
	ctx := context.Background()
	saved, _ := service.Configuration(ctx)
	saved.Value.Hubs = []quota.HubConfig{{Id: "hub", Url: "https://hub.invalid", CredentialId: "saved"}}
	if _, err := service.Store.WriteSetting(ctx, "configuration", saved.Revision, saved.Value); err != nil {
		test.Fatal(err)
	}
	saved, _ = service.Configuration(ctx)
	previousRevision := saved.Revision
	saved.Value.Hubs = nil
	saved, err := service.SaveConfiguration(ctx, saved)
	if err != nil || saved.Revision != previousRevision+1 || credentials.memoryCredentials["saved"] != "live" {
		test.Fatal("cleanup failure obscured the committed save", saved.Revision, err)
	}
	var pending []string
	if _, err := service.Store.ReadSetting(ctx, "credential-cleanup", &pending); err != nil || len(pending) != 1 || pending[0] != "saved" {
		test.Fatal("failed deletion was not journaled", pending, err)
	}
	restarted, err := New(ctx, service.Store, nil)
	if err != nil {
		test.Fatal(err)
	}
	defer restarted.Close()
	restarted.Secrets = credentials
	if err := restarted.PrepareClose(ctx); err == nil || restarted.isStopping {
		test.Fatal("failed cleanup should permit another close attempt", err)
	}
	credentials.shouldFailDelete = false
	if err := restarted.PrepareClose(ctx); err != nil || credentials.memoryCredentials["saved"] != "" {
		test.Fatal("close after restart did not retry deletion", err)
	}
	if _, err := service.Store.ReadSetting(ctx, "credential-cleanup", &pending); err != nil || len(pending) != 0 {
		test.Fatal("completed cleanup remained journaled", pending, err)
	}
}

// TestCredentialCleanupRetriesOnNextSave keeps the committed revision usable after native failure.
func TestCredentialCleanupRetriesOnNextSave(test *testing.T) {
	service := testService(test)
	credentials := &retryCredentials{memoryCredentials: memoryCredentials{"saved": "live"}, shouldFailDelete: true}
	service.Secrets = credentials
	ctx := context.Background()
	saved, _ := service.Configuration(ctx)
	saved.Value.Hubs = []quota.HubConfig{{Id: "hub", Url: "https://hub.invalid", CredentialId: "saved"}}
	if _, err := service.Store.WriteSetting(ctx, "configuration", saved.Revision, saved.Value); err != nil {
		test.Fatal(err)
	}
	saved, _ = service.Configuration(ctx)
	saved.Value.Hubs = nil
	saved, err := service.SaveConfiguration(ctx, saved)
	if err != nil {
		test.Fatal(err)
	}
	credentials.shouldFailDelete = false
	if _, err := service.SaveConfiguration(ctx, saved); err != nil || credentials.memoryCredentials["saved"] != "" {
		test.Fatal("next save did not retry deletion", err)
	}
}

// TestCredentialCleanupPreservesDrafts keeps unrelated pending edits until explicitly discarded.
func TestCredentialCleanupPreservesDrafts(test *testing.T) {
	service := testService(test)
	credentials := memoryCredentials{}
	service.Secrets = credentials
	ctx := context.Background()
	identity, err := service.StageHubCredential(ctx, "https://draft.invalid", "draft")
	if err != nil {
		test.Fatal(err)
	}
	if _, err := service.Store.WriteSetting(ctx, "credential-cleanup", 0, []string{identity}); err != nil {
		test.Fatal(err)
	}
	saved, _ := service.Configuration(ctx)
	if _, err := service.SaveConfiguration(ctx, saved); err != nil || credentials[identity] != "draft" {
		test.Fatal("cleanup deleted an unrelated draft", err)
	}
	if err := service.PrepareClose(ctx); err != nil || credentials[identity] != "" {
		test.Fatal("explicit close did not discard the draft", err)
	}
}
func (values memoryCredentials) Put(ctx context.Context, id, value string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	values[id] = value
	return nil
}
func (values memoryCredentials) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	delete(values, id)
	return nil
}

func TestCredentialDraftDiscardCannotDeleteAppliedOrRetargetSavedKey(test *testing.T) {
	service := testService(test)
	credentials := memoryCredentials{}
	service.Secrets = credentials
	ctx := context.Background()
	reference, err := service.StageHubCredential(ctx, "https://first.invalid", "fixture-key")
	if err != nil {
		test.Fatal(err)
	}
	saved, _ := service.Configuration(ctx)
	saved.Value.Hubs = []quota.HubConfig{{Id: "hub", Label: "Hub", Url: "https://first.invalid", CredentialId: reference, IsEnabled: false}}
	saved, err = service.SaveConfiguration(ctx, saved)
	if err != nil {
		test.Fatal(err)
	}
	if err := service.DiscardDraftCredentials(ctx, []string{reference}); err != nil || credentials[reference] != "fixture-key" {
		test.Fatal("applied key was discarded", err)
	}
	saved.Value.Hubs[0].Url = "https://other.invalid"
	if _, err := service.SaveConfiguration(ctx, saved); err == nil {
		test.Fatal("saved key could be transmitted to another origin")
	}
	draft, err := service.StageHubCredential(ctx, "https://first.invalid", "new-fixture-key")
	if err != nil || draft == reference {
		test.Fatal("draft replaced the live key", err)
	}
	if err := service.DiscardDraftCredentials(ctx, []string{draft, reference, "unowned"}); err != nil || credentials[draft] != "" || credentials[reference] != "fixture-key" {
		test.Fatal("discard crossed its owned draft boundary", err)
	}
}

func TestPendingWritesRemainOwnedUntilCallerSettles(test *testing.T) {
	service := testService(test)
	ctx, release, err := service.ownOperation(context.Background())
	if err != nil || !service.HasPendingWrites() {
		test.Fatal("write was not protected", err)
	}
	service.cancel()
	if ctx.Err() == nil || !service.HasPendingWrites() {
		test.Fatal("shutdown lost the unsettled write")
	}
	release()
	if service.HasPendingWrites() {
		test.Fatal("settled write still blocks close")
	}
}

func TestExplicitCloseFencesNewWritesAfterAcceptedWorkSettles(test *testing.T) {
	service := testService(test)
	ctx := context.Background()
	_, release, err := service.BeginWrite(ctx)
	if err != nil {
		test.Fatal(err)
	}
	if err := service.PrepareClose(ctx); err == nil {
		test.Fatal("close interrupted an accepted write")
	}
	release()
	if err := service.PrepareClose(ctx); err != nil {
		test.Fatal(err)
	}
	if _, release, err := service.BeginWrite(ctx); err == nil {
		release()
		test.Fatal("a late write crossed the close fence")
	}
}
