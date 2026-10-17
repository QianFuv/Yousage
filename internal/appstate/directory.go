// Package appstate resolves the shared desktop and theme-command data directory.
package appstate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// Directory resolves Windows portable data inside the executable's data subdirectory.
// Other desktop platforms retain their system user configuration directory.
func Directory() (string, error) {
	if runtime.GOOS == "windows" {
		executable, err := os.Executable()
		if err != nil {
			return "", err
		}
		return filepath.Join(filepath.Dir(executable), "data"), nil
	}
	config, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(config, "Yousage"), nil
}

// DatabasePath groups the Windows database and its journals within the data directory.
// Other platforms retain their existing database location.
func DatabasePath(directory string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(directory, "database", "yousage.sqlite")
	}
	return filepath.Join(directory, "yousage.sqlite")
}

// Prepare relocates legacy Windows portable state without replacing existing data.
// Only the desktop instance that owns the single-instance lock may migrate;
// command-line callers must ask the user to start the desktop app first.
func Prepare(directory string, canMigrate bool) error {
	if runtime.GOOS != "windows" {
		return nil
	}
	return preparePortable(directory, canMigrate)
}

// preparePortable preflights all legacy paths before moving their contents unchanged.
// The database is moved last so an interrupted migration can resume before opening it.
func preparePortable(directory string, canMigrate bool) error {
	legacyDirectory := filepath.Dir(directory)
	for _, parent := range []string{directory, filepath.Join(directory, "database")} {
		info, err := os.Lstat(parent)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
			return fmt.Errorf("portable data directory is not a regular directory: %s", parent)
		}
	}
	legacyNames := []string{"themes", "webview", "logs", "settings.json", ".theme-write-lock", "yousage.sqlite-wal", "yousage.sqlite-shm", "yousage.sqlite-journal", "yousage.sqlite"}
	type relocation struct{ source, destination string }
	var relocations []relocation
	for _, name := range legacyNames {
		source := filepath.Join(legacyDirectory, name)
		info, err := os.Lstat(source)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("cannot migrate linked portable data: %s", source)
		}
		destination := filepath.Join(directory, name)
		if name == "yousage.sqlite" || name == "yousage.sqlite-wal" || name == "yousage.sqlite-shm" || name == "yousage.sqlite-journal" {
			destination = filepath.Join(directory, "database", name)
			if _, err := os.Lstat(filepath.Join(directory, "database", "yousage.sqlite")); err == nil {
				return fmt.Errorf("both legacy and current databases exist; resolve the portable data conflict before starting: %s", source)
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		if _, err := os.Lstat(destination); err == nil {
			return fmt.Errorf("both legacy and current portable data exist: %s and %s", source, destination)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		relocations = append(relocations, relocation{source: source, destination: destination})
	}
	if len(relocations) > 0 && !canMigrate {
		return errors.New("legacy portable data found; exit the previous version and start the desktop app to migrate it into data")
	}
	for _, move := range relocations {
		if err := os.MkdirAll(filepath.Dir(move.destination), 0700); err != nil {
			return err
		}
		if err := os.Rename(move.source, move.destination); err != nil {
			return fmt.Errorf("migrate portable data (exit the previous version before retrying): %w", err)
		}
	}
	return nil
}
