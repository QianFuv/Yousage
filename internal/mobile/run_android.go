//go:build android

package mobile

import (
	"io/fs"
	"log"
	"path/filepath"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// Run starts one Android webview backed exclusively by the mobile service.
func Run(assets fs.FS) {
	frontend, err := fs.Sub(assets, "frontend/dist-android")
	if err != nil {
		log.Fatal(err)
	}
	directory := application.Android.StoragePath()
	if directory == "" || !filepath.IsAbs(directory) {
		log.Fatal("Android private storage is unavailable")
	}
	service := newService(directory, func(destination string) error {
		application.Android.OpenURL(destination)
		return nil
	})
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
		log.Fatal(err)
	}
}
