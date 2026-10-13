// Yousage embeds desktop resources and starts the native application.
package main

import (
	"embed"

	"yousage/internal/desktop"
)

//go:embed all:frontend/dist build/appicon.png build/windows/icon.ico
var assets embed.FS

// main delegates application behavior to the desktop package.
func main() {
	desktop.Run(assets)
}
