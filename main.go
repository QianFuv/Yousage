// Yousage embeds the usage and settings interface in a native Wails v3 window.
package main

import (
	"context"
	"embed"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"log"
	"os"
	"path/filepath"
	"time"
	"yousage/internal/quota"
	"yousage/internal/secrets"
	"yousage/internal/theme"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--quota-credential-helper" {
		os.Exit(quota.RunCredentialHelper(os.Stdin, os.Stdout))
	}
	if len(os.Args) > 1 && os.Args[1] == "--credential-helper" {
		os.Exit(secrets.RunHelper(os.Stdin, os.Stdout))
	}
	if len(os.Args) > 1 && os.Args[1] == "theme" {
		attachCommandConsole()
		_, exitCode := theme.RunCommand(context.Background(), os.Args[1:], os.Stdout, os.Stderr)
		os.Exit(exitCode)
	}
	service, err := newApp()
	if err != nil {
		log.Fatal(err)
	}
	nativeApp := application.New(application.Options{
		Name: "Yousage", Description: "Usage monitoring and themes",
		Services: []application.Service{application.NewService(service)},
		Assets:   application.AssetOptions{Handler: application.BundledAssetFileServer(assets)},
		Mac:      application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true},
		Windows:  application.WindowsOptions{WebviewUserDataPath: filepath.Join(service.stateDirectory, "webview")},
	})
	service.nativeApp = nativeApp
	service.window = nativeApp.Window.NewWithOptions(application.WebviewWindowOptions{
		Name: "yousage", Title: "Yousage", URL: "/", Width: 1280, Height: 900, MinWidth: 480, MinHeight: 600,
		Hidden: true, EnableFileDrop: true, BackgroundColour: application.NewRGBA(10, 10, 10, 255),
		Windows: application.WindowsWindow{Theme: application.Light},
	})
	service.installDesktopHooks()
	nativeApp.Event.OnApplicationEvent(events.Common.ThemeChanged, func(event *application.ApplicationEvent) {
		nativeApp.Event.Emit("system-appearance", event.Context().IsDarkMode())
	})
	service.window.OnWindowEvent(events.Common.WindowFilesDropped, func(event *application.WindowEvent) {
		paths := event.Context().DroppedFiles()
		nativeApp.Event.Emit("theme-files-dropped", service.ReadThemeFiles(paths))
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
