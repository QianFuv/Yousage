package appstate

import (
	"os"
	"path/filepath"
	"runtime"
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
	if err != nil || directory != filepath.Dir(executable) {
		test.Fatal("portable directory must follow the executable", directory, err)
	}
}
