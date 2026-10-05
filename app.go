package main

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/wailsapp/wails/v3/pkg/application"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Bootstrap is the fixture-only native contract; it carries no account data.
type Bootstrap struct {
	SourceCommit   string   `json:"sourceCommit"`
	WailsVersion   string   `json:"wailsVersion"`
	ReferenceTime  string   `json:"referenceTime"`
	TimeZone       string   `json:"timeZone"`
	AppearanceMode string   `json:"appearanceMode"`
	IsSystemDark   bool     `json:"isSystemDark"`
	Providers      []string `json:"providers"`
	RoleCount      int      `json:"roleCount"`
	Pid            int      `json:"pid"`
	LifecycleLog   string   `json:"lifecycleLog"`
}

// App exposes bounded fixture and native demonstration actions.
type App struct {
	context   context.Context
	nativeApp *application.App
	window    *application.WebviewWindow
	logFile   *os.File
	logMutex  sync.Mutex
	logPath   string
}

func newApp() (*App, error) {
	directory, err := os.MkdirTemp("", "yousage-native-preflight-")
	if err != nil {
		return nil, err
	}
	logPath := filepath.Join(directory, "lifecycle.jsonl")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, err
	}
	return &App{logFile: logFile, logPath: logPath}, nil
}

// ServiceStartup initializes lifecycle evidence before native windows are created.
func (service *App) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	service.context = ctx
	service.record("startup")
	return nil
}

// ServiceShutdown flushes evidence after Wails cancels the application context.
func (service *App) ServiceShutdown() error {
	service.record("shutdown")
	service.logMutex.Lock()
	defer service.logMutex.Unlock()
	return service.logFile.Close()
}

func (service *App) record(event string) {
	service.logMutex.Lock()
	defer service.logMutex.Unlock()
	json.NewEncoder(service.logFile).Encode(map[string]any{"event": event, "pid": os.Getpid(), "at": time.Now().UTC().Format(time.RFC3339Nano)})
}

func fileNames(paths []string) []string {
	names := make([]string, len(paths))
	for index, path := range paths {
		names[index] = filepath.Base(path)
	}
	return names
}

// GetBootstrap returns reference metadata after native initialization.
func (service *App) GetBootstrap() Bootstrap {
	return Bootstrap{SourceCommit: "51f7f77f87318a5329d3bab15d513fda5654200b", WailsVersion: "3.0.0-beta.28", ReferenceTime: "2026-10-06T12:00:00Z", TimeZone: "UTC", AppearanceMode: "system", IsSystemDark: service.nativeApp.Env.IsDarkMode(), Providers: []string{"codex", "claude", "grok", "cursor", "opencode", "antigravity"}, RoleCount: 57, Pid: os.Getpid(), LifecycleLog: service.logPath}
}

// FrontendReady reveals the window after the frontend applies its theme.
func (service *App) FrontendReady() {
	service.record("frontend-ready")
	service.window.Show()
	if hasNativeSmokeArgument() {
		time.AfterFunc(time.Second, service.nativeApp.Quit)
	}
}

func hasNativeSmokeArgument() bool {
	for _, argument := range os.Args[1:] {
		if argument == "--native-smoke" {
			return true
		}
	}
	return false
}

// SyncAppearance updates an opaque background and the existing native frame.
func (service *App) SyncAppearance(isDark bool, red, green, blue int) error {
	if err := service.context.Err(); err != nil {
		return err
	}
	if red < 0 || red > 255 || green < 0 || green > 255 || blue < 0 || blue > 255 {
		return errors.New("invalid background color")
	}
	service.window.SetBackgroundColour(application.NewRGBA(uint8(red), uint8(green), uint8(blue), 255))
	if err := setNativeAppearance(service.window, isDark, red, green, blue); err != nil {
		return err
	}
	service.record("appearance")
	return nil
}

// PickFixtureFiles selects public fixtures without reading selected contents.
func (service *App) PickFixtureFiles() ([]string, error) {
	paths, err := service.nativeApp.Dialog.OpenFile().SetTitle("Select synthetic theme fixtures").AddFilter("Theme JSON fixtures", "*.json").AttachToWindow(service.window).PromptForMultipleSelection()
	if err != nil && err.Error() == "cancelled by user" {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	service.record("file-picker")
	return fileNames(paths), nil
}

// ExportFixture writes the fixed fixture after a native save-path choice.
func (service *App) ExportFixture() (string, error) {
	dialog := service.nativeApp.Dialog.SaveFile()
	dialog.SetOptions(&application.SaveFileDialogOptions{Title: "Export synthetic fixture", Filename: "yousage-fixture.json", Filters: []application.FileFilter{{DisplayName: "JSON fixture", Pattern: "*.json"}}, Window: service.window})
	path, err := dialog.PromptForSingleSelection()
	if err != nil && err.Error() == "cancelled by user" {
		return "", nil
	}
	if err != nil || path == "" {
		return "", err
	}
	if err = os.WriteFile(path, fixtureTheme, 0600); err != nil {
		return "", err
	}
	service.record("fixture-export")
	return filepath.Base(path), nil
}

// OpenDocumentation opens only the fixed official documentation destination.
func (service *App) OpenDocumentation() error {
	return service.nativeApp.Browser.OpenURL("https://v3.wails.io/getting-started/installation/")
}
