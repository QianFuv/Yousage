//go:build android

// Yousage embeds the read-only Android viewer and registers its native entry.
package main

import (
	"embed"

	"github.com/wailsapp/wails/v3/pkg/application"
	"yousage/internal/mobile"
)

//go:embed all:frontend/dist-android
var assets embed.FS

// init registers the entry called by the Android JNI bridge.
func init() {
	application.RegisterAndroidMain(main)
}

// main starts the native read-only viewer.
func main() {
	mobile.Run(assets)
}
