//go:build !windows

package main

import _ "embed"

// trayIcon reuses the application artwork for platform status menus.
//
//go:embed build/appicon.png
var trayIcon []byte
