package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
)

const frameworkVersion = "v3.0.0-beta.30"
const frameworkAndroidDigest = "7c5696cf63d24ae4a95772ba8d5047b1515c0600eae46a12dea016a13d2696e1"
const frameworkModulePath = "github.com/wailsapp/wails/v3"

// androidFrameworkBuild supplies only disposable inputs to one native build.
type androidFrameworkBuild struct {
	Overlay string
	Modfile string
}

// moduleResolution compares selected versions and source locations, excluding
// metadata paths that necessarily change when using a temporary modfile.
type moduleResolution struct {
	Path    string
	Version string
	Dir     string
	Replace *moduleResolution
}

// offlineGoEnvironment disables proxy bypass as well as automatic acquisition,
// without changing the user's persistent Go configuration or checksum settings.
func offlineGoEnvironment(overrides map[string]string) []string {
	values := map[string]string{}
	for name, value := range overrides {
		values[name] = value
	}
	values["GOPROXY"], values["GONOPROXY"], values["GOTOOLCHAIN"] = "off", "none", "local"
	return environment(values)
}

// offlineGoCommand forbids dependency and toolchain acquisition for this patch.
func offlineGoCommand(root string, arguments ...string) *exec.Cmd {
	command := exec.Command("go", arguments...)
	command.Dir = root
	command.Env = offlineGoEnvironment(nil)
	return command
}

// androidFrameworkSource resolves only the reviewed, unmodified module version.
func androidFrameworkSource(root string) (string, []byte, error) {
	command := offlineGoCommand(root, "list", "-mod=readonly", "-m", "-json", frameworkModulePath)
	output, err := command.Output()
	if err != nil {
		return "", nil, errors.New("could not resolve the Android framework module")
	}
	var module struct {
		Dir     string
		Version string
		Replace *json.RawMessage
	}
	if json.Unmarshal(output, &module) != nil || module.Version != frameworkVersion || module.Replace != nil || !filepath.IsAbs(module.Dir) {
		return "", nil, errors.New("Android framework changed; review the diagnostic logging overlay")
	}
	path := filepath.Join(module.Dir, "pkg", "application", "application_android.go")
	source, err := os.ReadFile(path)
	if err != nil || fmt.Sprintf("%x", sha256.Sum256(source)) != frameworkAndroidDigest {
		return "", nil, errors.New("Android framework source changed; review the diagnostic logging overlay")
	}
	return path, source, nil
}

// redactAndroidFramework changes only three reviewed log calls, never request flow.
func redactAndroidFramework(source []byte) ([]byte, error) {
	text := string(source)
	for _, replacement := range [][2]string{
		{`androidLogf("warn", "[JNI] nativeEmitSystemEvent: unknown event %q", name)`, `androidLogf("warn", "[JNI] unknown system event")`},
		{`androidLogf("error", "[JNI] timeout waiting for app to be ready, dropping request for %s", goPath)`, `androidLogf("error", "[JNI] application startup timed out")`},
		{`androidLogf("error", "[JNI] error serving asset %s: %v", goPath, err)`, `androidLogf("error", "[JNI] asset request failed")`},
	} {
		if strings.Count(text, replacement[0]) != 1 {
			return nil, errors.New("Android framework log calls changed; review the diagnostic logging overlay")
		}
		text = strings.Replace(text, replacement[0], replacement[1], 1)
	}
	return []byte(text), nil
}

// readModuleResolutions captures the offline selected graph before compilation.
func readModuleResolutions(root, modfile string) (map[string]moduleResolution, error) {
	arguments := []string{"list", "-mod=readonly"}
	if modfile != "" {
		arguments = append(arguments, "-modfile", modfile)
	}
	arguments = append(arguments, "-m", "-json", "all")
	output, err := offlineGoCommand(root, arguments...).Output()
	if err != nil {
		return nil, errors.New("offline Android module resolution unavailable; no dependencies were acquired")
	}
	modules := map[string]moduleResolution{}
	decoder := json.NewDecoder(bytes.NewReader(output))
	for {
		var module moduleResolution
		if err := decoder.Decode(&module); err == io.EOF {
			break
		} else if err != nil || module.Path == "" {
			return nil, errors.New("invalid Android module resolution")
		}
		if _, exists := modules[module.Path]; exists {
			return nil, errors.New("duplicate Android module resolution")
		}
		modules[module.Path] = module
	}
	if len(modules) == 0 {
		return nil, errors.New("empty Android module resolution")
	}
	return modules, nil
}

// verifyCachedModuleSources refuses archive-only cached dependencies, which Go
// could otherwise extract even with GOPROXY=off. Uncached archives remain
// unavailable offline and cannot supply new source code.
func verifyCachedModuleSources(modules map[string]moduleResolution, cache string) error {
	escape := func(value string) string {
		var escaped strings.Builder
		for _, character := range value {
			if character >= 'A' && character <= 'Z' {
				escaped.WriteByte('!')
				character += 'a' - 'A'
			}
			escaped.WriteRune(character)
		}
		return escaped.String()
	}
	for _, module := range modules {
		if module.Replace != nil {
			module = *module.Replace
		}
		if module.Version == "" {
			continue
		}
		escapedPath, escapedVersion := escape(module.Path), escape(module.Version)
		metadata := filepath.Join(cache, "cache", "download", filepath.FromSlash(escapedPath), "@v", escapedVersion)
		directory := filepath.Join(cache, filepath.FromSlash(escapedPath)+"@"+escapedVersion)
		if info, err := os.Stat(directory); err == nil && info.IsDir() {
			if _, err := os.Stat(metadata + ".partial"); !os.IsNotExist(err) {
				return fmt.Errorf("cached module sources are incomplete: %s %s", module.Path, module.Version)
			}
			if _, err := os.Stat(metadata + ".ziphash"); err != nil {
				return fmt.Errorf("cached module source checksum is unavailable: %s %s", module.Path, module.Version)
			}
			continue
		} else if err != nil && !os.IsNotExist(err) {
			return errors.New("could not verify cached Android module sources")
		}
		archive := metadata + ".zip"
		if _, err := os.Stat(archive); err == nil {
			return fmt.Errorf("cached module archive lacks extracted sources: %s %s", module.Path, module.Version)
		} else if !os.IsNotExist(err) {
			return errors.New("could not verify cached Android module archives")
		}
	}
	return nil
}

// verifyModuleResolutions allows only the locked Wails source-directory change.
func verifyModuleResolutions(before, after map[string]moduleResolution, copied string) error {
	if len(before) != len(after) || before[frameworkModulePath].Path != frameworkModulePath {
		return errors.New("temporary Android modfile changed module resolution")
	}
	for path, original := range before {
		current, exists := after[path]
		if !exists {
			return errors.New("temporary Android modfile removed a selected dependency")
		}
		if path == frameworkModulePath {
			if original.Version != frameworkVersion || original.Replace != nil || current.Dir != copied || current.Replace == nil || current.Replace.Path != copied || current.Replace.Dir != copied || current.Replace.Version != "" || current.Replace.Replace != nil {
				return errors.New("temporary Android modfile changed the reviewed framework resolution")
			}
			current.Dir, current.Replace = original.Dir, nil
		}
		if !reflect.DeepEqual(original, current) {
			return errors.New("temporary Android modfile changed another dependency resolution")
		}
	}
	return nil
}

// prepareAndroidFrameworkOverlay copies only the already cached module. A
// version-qualified temporary replacement makes Go overlays legal outside the
// immutable module cache; an offline graph comparison rejects other changes.
func prepareAndroidFrameworkOverlay(root string) (androidFrameworkBuild, func() error, error) {
	_, source, err := androidFrameworkSource(root)
	if err != nil {
		return androidFrameworkBuild{}, nil, err
	}
	patched, err := redactAndroidFramework(source)
	if err != nil {
		return androidFrameworkBuild{}, nil, err
	}
	before, err := readModuleResolutions(root, "")
	if err != nil {
		return androidFrameworkBuild{}, nil, err
	}
	cache, err := offlineGoCommand(root, "env", "GOMODCACHE").Output()
	if err != nil {
		return androidFrameworkBuild{}, nil, errors.New("could not locate the existing Go module cache")
	}
	if err = verifyCachedModuleSources(before, strings.TrimSpace(string(cache))); err != nil {
		return androidFrameworkBuild{}, nil, err
	}
	return writeAndroidFrameworkOverlay(root, source, patched, before)
}

// writeAndroidFrameworkOverlay publishes task-owned temporary inputs and cleans
// the full owned tree on every preparation failure after directory creation.
func writeAndroidFrameworkOverlay(root string, source, patched []byte, before map[string]moduleResolution) (androidFrameworkBuild, func() error, error) {
	temporaryRoot, err := filepath.Abs(os.TempDir())
	if err != nil {
		return androidFrameworkBuild{}, nil, err
	}
	directory, err := os.MkdirTemp(temporaryRoot, "yousage-android-overlay-")
	if err != nil {
		return androidFrameworkBuild{}, nil, err
	}
	cleanup := func() error {
		if filepath.Dir(directory) != temporaryRoot || !strings.HasPrefix(filepath.Base(directory), "yousage-android-overlay-") {
			return errors.New("refusing cleanup outside the Android build temporary directory")
		}
		return os.RemoveAll(directory)
	}
	patchedPath := filepath.Join(directory, "application_android.go")
	overlayPath := filepath.Join(directory, "overlay.json")
	modfile := filepath.Join(directory, "build.mod")
	copied := filepath.Join(directory, "wails")
	if err = os.CopyFS(copied, os.DirFS(before[frameworkModulePath].Dir)); err == nil {
		err = copyFile(filepath.Join(root, "go.mod"), modfile)
	}
	if err == nil {
		err = copyFile(filepath.Join(root, "go.sum"), filepath.Join(directory, "build.sum"))
	}
	if err == nil {
		err = offlineGoCommand(root, "mod", "edit", "-modfile", modfile, "-replace", frameworkModulePath+"@"+frameworkVersion+"="+copied).Run()
	}
	if err == nil {
		var after map[string]moduleResolution
		after, err = readModuleResolutions(root, modfile)
		if err == nil {
			err = verifyModuleResolutions(before, after, copied)
		}
	}
	if err != nil {
		return androidFrameworkBuild{}, nil, errors.Join(err, cleanup())
	}
	copiedOriginal := filepath.Join(copied, "pkg", "application", "application_android.go")
	copiedSource, err := os.ReadFile(copiedOriginal)
	if err != nil || !bytes.Equal(source, copiedSource) {
		return androidFrameworkBuild{}, nil, errors.Join(errors.New("temporary framework source differs from the reviewed module"), cleanup())
	}
	data, err := json.Marshal(struct{ Replace map[string]string }{map[string]string{copiedOriginal: patchedPath}})
	if err == nil {
		err = os.WriteFile(patchedPath, patched, 0600)
	}
	if err == nil {
		err = os.WriteFile(overlayPath, data, 0600)
	}
	if err != nil {
		return androidFrameworkBuild{}, nil, errors.Join(err, cleanup())
	}
	return androidFrameworkBuild{Overlay: overlayPath, Modfile: modfile}, cleanup, nil
}
