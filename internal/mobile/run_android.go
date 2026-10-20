//go:build android

package mobile

import (
	"context"
	"io/fs"
	"log"
	"path/filepath"
	"yousage/internal/diagnostics"

	"github.com/wailsapp/wails/v3/pkg/application"
)

var androidStartup startupClaim

// Run starts one Android webview backed exclusively by the mobile service.
func Run(assets fs.FS) {
	if !androidStartup.tryClaim() {
		return
	}
	directory := application.Android.StoragePath()
	if directory == "" || !filepath.IsAbs(directory) {
		log.Print("Android private storage is unavailable")
		return
	}
	service := newService(directory, func(destination string) error {
		application.Android.OpenURL(destination)
		return nil
	})
	operation := service.diagnostics.NewOperation()
	service.startupOperation = operation
	service.recordDiagnostic(diagnostics.Entry{Event: diagnostics.EventStartup, Outcome: diagnostics.OutcomeStarted, Operation: operation})
	frontend, err := fs.Sub(assets, "frontend/dist-android")
	if err != nil {
		service.recordDiagnostic(diagnostics.Entry{Event: diagnostics.EventStartup, Outcome: diagnostics.OutcomeFailed, Failure: diagnostics.FailureUnavailable, Operation: operation})
		log.Print("Android frontend is unavailable")
		return
	}
	nativeApp := application.New(application.Options{
		Name: "Yousage", Description: "Read-only encrypted usage viewer",
		Services: []application.Service{application.NewService(service)},
		Assets:   application.AssetOptions{Handler: application.BundledAssetFileServer(frontend)},
	})
	nativeApp.Window.NewWithOptions(application.WebviewWindowOptions{
		Name: "yousage", Title: "Yousage", URL: "/",
		BackgroundColour: application.NewRGBA(10, 10, 10, 255),
	})
	if err = nativeApp.Run(); err != nil {
		service.recordDiagnostic(diagnostics.Entry{Event: diagnostics.EventStartup, Outcome: diagnostics.OutcomeFailed, Failure: diagnostics.FailureInternal, Operation: operation})
		log.Print("Android application startup failed")
	}
}

// ServiceStartup records successful Go service registration before native rendering.
func (service *Service) ServiceStartup(context.Context, application.ServiceOptions) error {
	service.recordDiagnostic(diagnostics.Entry{Event: diagnostics.EventStartup, Outcome: diagnostics.OutcomeSucceeded, Operation: service.startupOperation})
	return nil
}
