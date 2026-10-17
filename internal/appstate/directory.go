// Package appstate resolves the shared desktop and theme-command data directory.
package appstate

import (
	"os"
	"path/filepath"
	"runtime"
)

// Directory keeps Windows portable data beside the executable, independent of the working directory.
// Other desktop platforms retain their system user configuration directory.
func Directory() (string, error) {
	if runtime.GOOS == "windows" {
		executable, err := os.Executable()
		if err != nil {
			return "", err
		}
		return filepath.Dir(executable), nil
	}
	config, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(config, "Yousage"), nil
}
