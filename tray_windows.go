package main

import _ "embed"

// trayIcon reuses the multi-resolution application icon for the notification area.
//
//go:embed build/windows/icon.ico
var trayIcon []byte
