// Yousage embeds the pinned parity slice in a native Wails v3 window.
package main

import (
	"embed"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"log"
	"time"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed frontend/public/theme-fixture.json
var fixtureTheme []byte

func main() {
	service, err := newApp()
	if err != nil {
		log.Fatal(err)
	}
	nativeApp := application.New(application.Options{
		Name: "Yousage", Description: "Usage monitoring and themes",
		Services: []application.Service{application.NewService(service)},
		Assets:   application.AssetOptions{Handler: application.BundledAssetFileServer(assets)},
		Mac:      application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true},
	})
	service.nativeApp = nativeApp
	service.window = nativeApp.Window.NewWithOptions(application.WebviewWindowOptions{
		Name: "yousage", Title: "Yousage", URL: "/", Width: 1280, Height: 900, MinWidth: 480, MinHeight: 600,
		Hidden: true, EnableFileDrop: true, BackgroundColour: application.NewRGBA(10, 10, 10, 255),
		Windows: application.WindowsWindow{Theme: application.Light},
	})
	nativeApp.Event.OnApplicationEvent(events.Common.ThemeChanged, func(event *application.ApplicationEvent) {
		nativeApp.Event.Emit("system-appearance", event.Context().IsDarkMode())
	})
	service.window.OnWindowEvent(events.Common.WindowFilesDropped, func(event *application.WindowEvent) {
		nativeApp.Event.Emit("fixture-files-dropped", fileNames(event.Context().DroppedFiles()))
		service.record("file-drop")
	})
	if hasNativeSmokeArgument() {
		time.AfterFunc(30*time.Second, func() {
			service.record("smoke-deadline")
			nativeApp.Quit()
		})
	}
	if err = nativeApp.Run(); err != nil {
		log.Fatal(err)
	}
}
