package main

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"os"
	"runtime"
	"time"
	"yousage/internal/preferences"
)

// installDesktopHooks uses cancellable native hooks before Wails' closing listener.
func (service *App) installDesktopHooks() {
	service.window.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		service.windowMutex.Lock()
		shouldGuard := service.isCloseBridgeActive && !service.shouldAllowClose
		shouldHide := service.shouldCloseToTray && service.tray != nil && !service.shouldAllowClose
		service.windowMutex.Unlock()
		if shouldHide {
			event.Cancel()
			service.captureBounds()
			service.window.Hide()
			return
		}
		if shouldGuard {
			event.Cancel()
			service.nativeApp.Event.Emit("close-requested")
			return
		}
		service.captureBounds()
	})
	for _, event := range []events.WindowEventType{events.Common.WindowDidMove, events.Common.WindowDidResize, events.Common.WindowMaximise, events.Common.WindowUnMaximise} {
		service.window.OnWindowEvent(event, func(*application.WindowEvent) { service.captureBounds() })
	}
	for _, event := range []events.WindowEventType{events.Common.WindowShow, events.Common.WindowHide, events.Common.WindowMinimise, events.Common.WindowUnMinimise, events.Common.WindowRestore} {
		service.window.OnWindowEvent(event, func(*application.WindowEvent) {
			service.nativeApp.Event.Emit("window-visibility", windowVisibility(event, service.window.IsVisible(), service.window.IsMinimised()))
		})
	}
}

// windowVisibility trusts show/hide events over IsVisible, which still reports a hidden window while Windows shows it.
func windowVisibility(event events.WindowEventType, isVisible, isMinimised bool) bool {
	switch event {
	case events.Common.WindowShow:
		return !isMinimised
	case events.Common.WindowHide, events.Common.WindowMinimise:
		return false
	}
	return isVisible && !isMinimised
}

// captureBounds retains only normal DIP geometry; maximized/minimized rectangles never replace it.
func (service *App) captureBounds() {
	service.windowMutex.Lock()
	isShuttingDown := service.isShuttingDown
	service.windowMutex.Unlock()
	if isShuttingDown {
		return
	}
	var bounds preferences.WindowBounds
	hasBounds, didCapture := false, false
	application.InvokeSync(func() {
		if service.window.IsMinimised() || service.window.IsFullscreen() {
			return
		}
		isMaximized := service.window.IsMaximised()
		if !isMaximized {
			rect := service.window.Bounds()
			if rect.Width <= 0 || rect.Height <= 0 {
				return
			}
			bounds = preferences.WindowBounds{X: rect.X, Y: rect.Y, Width: rect.Width, Height: rect.Height}
			hasBounds = true
		}
		bounds.Maximized = isMaximized
		didCapture = true
	})
	service.windowMutex.Lock()
	defer service.windowMutex.Unlock()
	if service.isShuttingDown || !didCapture {
		return
	}
	if hasBounds {
		service.normalBounds = bounds
		service.hasNormalBounds = true
	} else {
		service.normalBounds.Maximized = bounds.Maximized
	}
}

// persistBounds uses a separate revision so display changes cannot race with native geometry.
func (service *App) persistBounds() {
	service.windowMutex.Lock()
	saved, hasBounds := service.normalBounds, service.hasNormalBounds
	service.windowMutex.Unlock()
	if !hasBounds {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var previous preferences.WindowBounds
	revision, err := service.store.ReadSetting(ctx, "window-bounds", &previous)
	if err == nil {
		_, _ = service.store.WriteSetting(ctx, "window-bounds", revision, saved)
	}
}

// restoreWindow clamps saved normal bounds to current monitor work areas before revealing the window.
func (service *App) restoreWindow() {
	areas := []preferences.WindowBounds{}
	for _, screen := range service.nativeApp.Screen.GetAll() {
		rect := screen.WorkArea
		areas = append(areas, preferences.WindowBounds{X: rect.X, Y: rect.Y, Width: rect.Width, Height: rect.Height})
	}
	if len(areas) == 0 {
		service.captureBounds()
		return
	}
	area := areas[0]
	if screen := service.nativeApp.Screen.GetPrimary(); screen != nil {
		rect := screen.WorkArea
		area = preferences.WindowBounds{X: rect.X, Y: rect.Y, Width: rect.Width, Height: rect.Height}
	}
	saved := preferences.WindowBounds{X: area.X + (area.Width-1280)/2, Y: area.Y + (area.Height-900)/2, Width: 1280, Height: 900}
	display, err := service.usage.Preferences.Display(service.context)
	if err == nil && display.Value.ShouldRestoreWindow {
		var previous preferences.WindowBounds
		revision, err := service.store.ReadSetting(service.context, "window-bounds", &previous)
		if err == nil && revision > 0 && previous.Width > 0 && previous.Height > 0 {
			saved = previous
		}
	}
	saved = preferences.RestoreBounds(saved, areas)
	service.window.SetMinSize(min(480, saved.Width), min(600, saved.Height))
	service.window.SetBounds(application.Rect{X: saved.X, Y: saved.Y, Width: saved.Width, Height: saved.Height})
	service.windowMutex.Lock()
	service.normalBounds, service.hasNormalBounds = saved, true
	service.windowMutex.Unlock()
	if saved.Maximized {
		service.window.Maximise()
	}
}

// RecenterWindow recovers an offscreen window without changing saved scale or display preferences.
func (service *App) RecenterWindow() {
	service.window.Restore()
	if screen := service.nativeApp.Screen.GetPrimary(); screen != nil {
		service.window.SetScreen(screen)
	}
	service.captureBounds()
}

// SetCloseBridge activates only after a mounted frontend subscribes to closing requests.
func (service *App) SetCloseBridge(isActive bool) {
	service.windowMutex.Lock()
	service.isCloseBridgeActive = isActive
	service.windowMutex.Unlock()
}

// ConfirmClose refuses exit while external writes are pending, then allows the native close hook once.
func (service *App) ConfirmClose() error {
	if service.usage.HasPendingWrites() {
		return errors.New("An account change is still running. Wait for its result before closing.")
	}
	if err := service.usage.PrepareClose(service.context); err != nil {
		return err
	}
	service.windowMutex.Lock()
	service.shouldAllowClose = true
	service.windowMutex.Unlock()
	service.window.Close()
	return nil
}

// CopyText checks native clipboard success and bounds the explicit user-selected content.
func (service *App) CopyText(value string) error {
	if len(value) > 16384 {
		return errors.New("clipboard content is too large")
	}
	if !service.nativeApp.Clipboard.SetText(value) {
		return errors.New("clipboard is unavailable")
	}
	return nil
}

// StageHubCredential stores a fresh native reference; the frontend never reads a saved key.
func (service *App) StageHubCredential(origin, value string) (string, error) {
	return service.usage.StageHubCredential(service.context, origin, value)
}

// DiscardDraftCredentials can delete only unsaved references created by this session.
func (service *App) DiscardDraftCredentials(identities []string) error {
	return service.usage.DiscardDraftCredentials(service.context, identities)
}

// GetDiagnostics emits an explicit whitelist without credentials, URLs, account identities, paths or raw errors.
func (service *App) GetDiagnostics() (string, error) {
	config, err := service.usage.Configuration(service.context)
	if err != nil {
		return "", err
	}
	sources, _, err := service.usage.Preferences.Sources(service.context)
	if err != nil {
		return "", err
	}
	value := struct {
		Application        string `json:"application"`
		Wails              string `json:"wails"`
		Go                 string `json:"go"`
		Platform           string `json:"platform"`
		SourceCommit       string `json:"sourceCommit"`
		HistorySources     int    `json:"historySources"`
		RemoteEnvironments int    `json:"remoteEnvironments"`
		QuotaTargets       int    `json:"quotaTargets"`
		QuotaHubs          int    `json:"quotaHubs"`
	}{"Yousage", "3.0.0-beta.28", runtime.Version(), runtime.GOOS + "/" + runtime.GOARCH, "51f7f77f87318a5329d3bab15d513fda5654200b", len(sources), len(config.Value.Connections), len(config.Value.Providers), len(config.Value.Hubs)}
	data, err := json.MarshalIndent(value, "", "  ")
	return string(data), err
}

// ExportDiagnostics writes only the same whitelisted report after an explicit native Save dialog.
func (service *App) ExportDiagnostics() (bool, error) {
	value, err := service.GetDiagnostics()
	if err != nil {
		return false, err
	}
	path, err := service.nativeApp.Dialog.SaveFile().SetFilename("yousage-diagnostics.json").AddFilter("JSON", "*.json").AttachToWindow(service.window).PromptForSingleSelection()
	if err != nil && err.Error() == "cancelled by user" {
		return false, nil
	}
	if err != nil || path == "" {
		return false, err
	}
	return true, os.WriteFile(path, []byte(value+"\n"), 0600)
}

// GetLocalEnvironmentId supplies stable local selection independently of summary availability.
func (service *App) GetLocalEnvironmentId() string { return service.usage.LocalId }
