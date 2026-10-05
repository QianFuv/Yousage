package service

import (
	"context"
	"testing"
	"yousage/internal/quota"
)

type memoryCredentials map[string]string

func (values memoryCredentials) Get(ctx context.Context, id string) (string, error) {
	return values[id], ctx.Err()
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
