package desktop

import (
	"context"
	"yousage/internal/sync"
)

func (service *App) initializeSync(ctx context.Context) error {
	builder := sync.SnapshotBuilder{
		Source:     service.usage,
		Display:    service.usage.Preferences.Display,
		Appearance: service.themes.Read,
		Bootstrap: func() any {
			if service.nativeApp == nil {
				return Bootstrap{}
			}
			return service.GetBootstrap()
		},
		Now: service.usage.Now,
	}
	engine, err := sync.NewEngine(ctx, sync.EngineOptions{
		Diagnostics:   service.diagnostics,
		Preferences:   service.usage.Preferences,
		Secrets:       service.usage.Secrets,
		BuildSnapshot: builder.Build,
		BuildShare: func(ctx context.Context, request sync.ShareRequest) (sync.SharePayload, error) {
			return sync.BuildShare(ctx, service.usage, request)
		},
		Now: service.usage.Now,
		OnChanged: func(state sync.State) {
			if service.nativeApp != nil {
				service.nativeApp.Event.Emit("sync-state", state)
			}
		},
	})
	if err != nil {
		return err
	}
	service.syncEngine = engine
	service.backgroundOptions.isSyncEnabled = func() bool { return engine.Settings().IsEnabled }
	service.backgroundOptions.timeZone = func() string { return engine.Settings().TimeZone }
	service.backgroundOptions.onRefreshed = engine.Trigger
	return nil
}

func (service *App) triggerSync() {
	if service.syncEngine != nil {
		service.syncEngine.Trigger()
	}
}

// GetSync returns sync settings, pairing QR, upload status and active share links.
func (service *App) GetSync() (sync.State, error) {
	return service.syncEngine.State(service.context)
}

// SaveSync validates sync preferences and stores credentials only in the system keyring.
func (service *App) SaveSync(input sync.SaveInput) (sync.State, error) {
	ctx, release, err := service.usage.BeginWrite(service.context)
	if err != nil {
		return sync.State{}, err
	}
	defer release()
	state, err := service.syncEngine.Save(ctx, input)
	if service.backgroundRefresh != nil {
		service.backgroundRefresh.reconcile()
	}
	return state, err
}

// TestRelay checks reachability and authorization using an explicit or stored token.
func (service *App) TestRelay(relayUrl, token string) error {
	ctx, release, err := service.usage.BeginWrite(service.context)
	if err != nil {
		return err
	}
	defer release()
	return service.syncEngine.TestRelay(ctx, relayUrl, token)
}

// SyncNow requests a snapshot push without refreshing provider data.
func (service *App) SyncNow() (sync.State, error) {
	ctx, release, err := service.usage.BeginWrite(service.context)
	if err != nil {
		return sync.State{}, err
	}
	defer release()
	return service.syncEngine.SyncNow(ctx)
}

// RotatePairing replaces the private link and publishes a new encrypted snapshot.
func (service *App) RotatePairing() (sync.State, error) {
	ctx, release, err := service.usage.BeginWrite(service.context)
	if err != nil {
		return sync.State{}, err
	}
	defer release()
	return service.syncEngine.RotatePairing(ctx)
}

// PreviewShare returns a sanitized local receipt preview without creating a relay link.
func (service *App) PreviewShare(request sync.ShareRequest) (sync.SharePayload, error) {
	return service.syncEngine.PreviewShare(service.context, request)
}

// CreateShare publishes an encrypted expiring receipt and returns its link and QR.
func (service *App) CreateShare(request sync.ShareRequest) (sync.ShareView, error) {
	ctx, release, err := service.usage.BeginWrite(service.context)
	if err != nil {
		return sync.ShareView{}, err
	}
	defer release()
	return service.syncEngine.CreateShare(ctx, request)
}

// RevokeShare removes a published share and its local record.
func (service *App) RevokeShare(id string) (sync.State, error) {
	ctx, release, err := service.usage.BeginWrite(service.context)
	if err != nil {
		return sync.State{}, err
	}
	defer release()
	return service.syncEngine.RevokeShare(ctx, id)
}
