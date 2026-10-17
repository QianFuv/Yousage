// Package desktop hosts the native application, its frontend bridge, and platform integration.
package desktop

import (
	"context"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"time"
	"yousage/internal/appstate"
	"yousage/internal/secrets"
	"yousage/internal/theme"
)

// Run dispatches headless commands or starts the desktop application with embedded resources.
func Run(assets fs.FS) {
	if len(os.Args) > 1 && os.Args[1] == "--credential-helper" {
		os.Exit(secrets.RunHelper(os.Stdin, os.Stdout))
	}
	if len(os.Args) > 1 && os.Args[1] == "theme" {
		attachCommandConsole()
		_, exitCode := theme.RunCommand(context.Background(), os.Args[1:], os.Stdout, os.Stderr)
		os.Exit(exitCode)
	}
	frontendAssets, err := fs.Sub(assets, "frontend/dist")
	if err != nil {
		log.Fatal(err)
	}
	iconPath := "build/appicon.png"
	if runtime.GOOS == "windows" {
		iconPath = "build/windows/icon.ico"
	}
	trayIcon, err := fs.ReadFile(assets, iconPath)
	if err != nil {
		log.Fatal(err)
	}
	stateDirectory, err := appstate.Directory()
	if err != nil {
		log.Fatal(err)
	}
	var service *App
	var instanceService atomic.Pointer[App]
	if hasNativeSmokeArgument() {
		service, err = newApp()
		if err != nil {
			log.Fatal(err)
		}
		stateDirectory = service.stateDirectory
	}
	var singleInstance *application.SingleInstanceOptions
	if !hasNativeSmokeArgument() {
		allowForegroundHandoff()
		singleInstance = &application.SingleInstanceOptions{
			UniqueID: "app.yousage.desktop",
			OnSecondInstanceLaunch: func(application.SecondInstanceData) {
				if currentService := instanceService.Load(); currentService != nil {
					currentService.ShowWindow()
				}
			},
		}
	}
	nativeApp := application.New(application.Options{
		Name: "Yousage", Description: "Usage monitoring and themes",
		SingleInstance: singleInstance,
		Assets:         application.AssetOptions{Handler: application.BundledAssetFileServer(frontendAssets)},
		Mac:            application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true},
		Windows: application.WindowsOptions{
			WebviewUserDataPath: filepath.Join(stateDirectory, "webview"),
			WndProcInterceptor: func(hwnd uintptr, message uint32, wParam, lParam uintptr) (uintptr, bool) {
				if service == nil {
					return 0, false
				}
				return service.interceptWindowMessage(hwnd, message, wParam, lParam)
			},
		},
	})
	if service == nil {
		service, err = newApp()
		if err != nil {
			log.Fatal(err)
		}
	}
	nativeApp.RegisterService(application.NewService(service))
	service.nativeApp = nativeApp
	service.window = nativeApp.Window.NewWithOptions(application.WebviewWindowOptions{
		Name: "yousage", Title: "Yousage", URL: "/", Width: 1280, Height: 900, MinWidth: DesktopMinWidth, MinHeight: DesktopMinHeight,
		Hidden: true, EnableFileDrop: true, BackgroundColour: application.NewRGBA(10, 10, 10, 255),
		Windows: application.WindowsWindow{Theme: application.Light},
	})
	service.installDesktopHooks()
	service.installTray(trayIcon)
	instanceService.Store(service)
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
