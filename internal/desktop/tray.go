package desktop

import "github.com/wailsapp/wails/v3/pkg/application"

// TrayLabels carries localized notification-area menu text from the frontend.
type TrayLabels struct {
	Usage    string `json:"usage"`
	Quota    string `json:"quota"`
	Loss     string `json:"loss"`
	Speed    string `json:"speed"`
	Settings string `json:"settings"`
	Quit     string `json:"quit"`
}

// installTray adds the notification-area icon whose click restores the retained page.
func (service *App) installTray(icon []byte) {
	tray := service.nativeApp.SystemTray.New()
	tray.SetIcon(icon)
	tray.SetTooltip("Yousage")
	tray.SetMenu(service.trayMenu(TrayLabels{Usage: "Usage", Quota: "Quota", Loss: "Loss", Speed: "Speed", Settings: "Settings", Quit: "Quit"}))
	tray.OnClick(service.ShowWindow)
	service.windowMutex.Lock()
	service.tray = tray
	service.windowMutex.Unlock()
}

// trayMenu lists every top-level page followed by the quit command.
func (service *App) trayMenu(labels TrayLabels) *application.Menu {
	menu := application.NewMenu()
	for _, item := range []struct{ route, label string }{
		{"statistics", labels.Usage},
		{"quota", labels.Quota},
		{"loss", labels.Loss},
		{"speed", labels.Speed},
		{"settings", labels.Settings},
	} {
		route := item.route
		menu.Add(item.label).OnClick(func(*application.Context) {
			service.ShowWindow()
			service.nativeApp.Event.Emit("tray-navigate", route)
		})
	}
	menu.AddSeparator()
	menu.Add(labels.Quit).OnClick(func(*application.Context) { service.requestQuit() })
	return menu
}

// SetTrayLabels replaces the tray menu after the frontend resolves its language.
func (service *App) SetTrayLabels(labels TrayLabels) {
	service.windowMutex.Lock()
	tray := service.tray
	service.windowMutex.Unlock()
	if tray != nil {
		tray.SetMenu(service.trayMenu(labels))
	}
}

// ShowWindow reveals a hidden or minimized window without changing its retained page.
func (service *App) ShowWindow() {
	service.window.UnMinimise()
	service.window.Show()
	service.window.Focus()
}

// setCloseToTray caches the saved close policy for the native closing hook.
func (service *App) setCloseToTray(shouldCloseToTray bool) {
	service.windowMutex.Lock()
	service.shouldCloseToTray = shouldCloseToTray
	service.windowMutex.Unlock()
}

// requestQuit exits through the frontend draft guard when it is mounted.
func (service *App) requestQuit() {
	service.windowMutex.Lock()
	isBridged := service.isCloseBridgeActive
	if !isBridged {
		service.shouldAllowClose = true
	}
	service.windowMutex.Unlock()
	if isBridged {
		service.nativeApp.Event.Emit("close-requested")
		return
	}
	service.window.Close()
}
