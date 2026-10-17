package appstate

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestPortableDirectoryIgnoresWorkingDirectoryAndAppData keeps Windows copies self-contained.
func TestPortableDirectoryIgnoresWorkingDirectoryAndAppData(test *testing.T) {
	if runtime.GOOS != "windows" {
		test.Skip("Windows portable layout")
	}
	executable, err := os.Executable()
	if err != nil {
		test.Fatal(err)
	}
	test.Chdir(test.TempDir())
	test.Setenv("APPDATA", test.TempDir())
	directory, err := Directory()
	if err != nil || directory != filepath.Join(filepath.Dir(executable), "data") {
		test.Fatal("portable directory must follow the executable", directory, err)
	}
}

// TestPortableMigrationPreservesFiles keeps the database family and other state together.
func TestPortableMigrationPreservesFiles(test *testing.T) {
	root := test.TempDir()
	directory := filepath.Join(root, "data")
	files := []string{"yousage.sqlite", "yousage.sqlite-wal", "yousage.sqlite-shm", "yousage.sqlite-journal", "themes/custom.json", "webview/Default/Local Storage/state", "logs/session/lifecycle.jsonl", "settings.json", ".theme-write-lock"}
	for _, name := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			test.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(name), 0600); err != nil {
			test.Fatal(err)
		}
	}
	for range 2 {
		if err := preparePortable(directory, true); err != nil {
			test.Fatal(err)
		}
	}
	for _, name := range files {
		destination := filepath.Join(directory, name)
		if strings.HasPrefix(name, "yousage.sqlite") {
			destination = filepath.Join(directory, "database", name)
		}
		content, err := os.ReadFile(destination)
		if err != nil || !bytes.Equal(content, []byte(name)) {
			test.Fatal("migration changed saved data", name, err)
		}
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			test.Fatal("legacy state remained outside data", name, err)
		}
	}
}

// TestPortableMigrationRejectsConflicts prevents partial moves or replacing newer state.
func TestPortableMigrationRejectsConflicts(test *testing.T) {
	for _, conflict := range []string{"database/yousage.sqlite", "themes"} {
		test.Run(conflict, func(test *testing.T) {
			root := test.TempDir()
			directory := filepath.Join(root, "data")
			for _, name := range []string{"yousage.sqlite", "themes", "logs", "data/" + conflict} {
				path := filepath.Join(root, name)
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					test.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(name), 0600); err != nil {
					test.Fatal(err)
				}
			}
			if err := preparePortable(directory, true); err == nil {
				test.Fatal("migration accepted competing state")
			}
			for _, name := range []string{"yousage.sqlite", "themes", "logs", "data/" + conflict} {
				content, err := os.ReadFile(filepath.Join(root, name))
				if err != nil || string(content) != name {
					test.Fatal("conflict changed existing data", name, err)
				}
			}
		})
	}
}

// TestCommandDoesNotMigrateLegacyData leaves migration to the desktop single-instance owner.
func TestCommandDoesNotMigrateLegacyData(test *testing.T) {
	root := test.TempDir()
	legacy := filepath.Join(root, "yousage.sqlite")
	if err := os.WriteFile(legacy, []byte("saved database"), 0600); err != nil {
		test.Fatal(err)
	}
	if err := preparePortable(filepath.Join(root, "data"), false); err == nil {
		test.Fatal("command attempted to migrate legacy data")
	}
	if content, err := os.ReadFile(legacy); err != nil || string(content) != "saved database" {
		test.Fatal("command changed the legacy database", err)
	}
}

// TestPortableMigrationResumesBeforeDatabaseMove preserves journals moved by an interrupted run.
func TestPortableMigrationResumesBeforeDatabaseMove(test *testing.T) {
	root := test.TempDir()
	directory := filepath.Join(root, "data")
	if err := os.MkdirAll(filepath.Join(directory, "database"), 0700); err != nil {
		test.Fatal(err)
	}
	databasePath := filepath.Join(root, "yousage.sqlite")
	journalPath := filepath.Join(directory, "database", "yousage.sqlite-wal")
	for _, path := range []string{databasePath, journalPath} {
		if err := os.WriteFile(path, []byte("preserved"), 0600); err != nil {
			test.Fatal(err)
		}
	}
	if err := preparePortable(directory, true); err != nil {
		test.Fatal(err)
	}
	for _, name := range []string{"yousage.sqlite", "yousage.sqlite-wal"} {
		content, err := os.ReadFile(filepath.Join(directory, "database", name))
		if err != nil || string(content) != "preserved" {
			test.Fatal("resuming lost database state", name, err)
		}
	}
}
