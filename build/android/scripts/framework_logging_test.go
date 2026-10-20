package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// literalFrameworkLogs rejects arguments that could expose request or error data.
func literalFrameworkLogs(source []byte) bool {
	file, err := parser.ParseFile(token.NewFileSet(), "fixture.go", source, 0)
	if err != nil {
		return false
	}
	isSafe := true
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		name, ok := call.Fun.(*ast.Ident)
		if !ok || name.Name != "androidLogf" {
			return true
		}
		if len(call.Args) != 2 {
			isSafe = false
			return false
		}
		for _, argument := range call.Args {
			literal, ok := argument.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				isSafe = false
				continue
			}
			value, err := strconv.Unquote(literal.Value)
			if err != nil || strings.Contains(value, "CANARY_SECRET") {
				isSafe = false
			}
		}
		return false
	})
	return isSafe
}

// TestAndroidFrameworkLogRedaction covers all three raw-data branches and keeps
// request work unchanged; the original fixture is the negative control.
func TestAndroidFrameworkLogRedaction(test *testing.T) {
	source := []byte(`package fixture
func handle(name, goPath string, err error) {
 androidLogf("warn", "[JNI] nativeEmitSystemEvent: unknown event %q", name)
 androidLogf("error", "[JNI] timeout waiting for app to be ready, dropping request for %s", goPath)
 androidLogf("error", "[JNI] error serving asset %s: %v", goPath, err)
 serveAssetForAndroid(app, goMethod, goPath)
}
`)
	if literalFrameworkLogs(source) {
		test.Fatal("negative control accepted raw request and error arguments")
	}
	patched, err := redactAndroidFramework(source)
	if err != nil || !literalFrameworkLogs(patched) {
		test.Fatal("framework logs can still format private inputs", err)
	}
	if !strings.Contains(string(patched), "serveAssetForAndroid(app, goMethod, goPath)") {
		test.Fatal("redaction changed request handling")
	}
	for _, changed := range [][]byte{[]byte(strings.Replace(string(source), "unknown event %q", "changed event %q", 1)), append(source, source...)} {
		if result, err := redactAndroidFramework(changed); err == nil || result != nil {
			test.Fatal("changed or duplicate log call silently skipped strict matching")
		}
	}
}

// TestAndroidFrameworkOverlayPreservesModule checks the real pinned dependency,
// overlay mapping, exact change scope, module immutability and temporary cleanup.
func TestAndroidFrameworkOverlayPreservesModule(test *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		test.Fatal(err)
	}
	original, before, err := androidFrameworkSource(root)
	if err != nil {
		test.Fatal(err)
	}
	if fmt.Sprintf("%x", sha256.Sum256(before)) != frameworkAndroidDigest {
		test.Fatal("reviewed framework source fingerprint changed")
	}
	build, cleanup, err := prepareAndroidFrameworkOverlay(root)
	if err != nil {
		test.Fatal(err)
	}
	defer cleanup()
	data, err := os.ReadFile(build.Overlay)
	if err != nil {
		test.Fatal(err)
	}
	var configuration struct{ Replace map[string]string }
	if json.Unmarshal(data, &configuration) != nil || len(configuration.Replace) != 1 {
		test.Fatal("overlay must replace exactly one reviewed framework file")
	}
	copied := filepath.Join(filepath.Dir(build.Overlay), "wails")
	patchedPath := configuration.Replace[filepath.Join(copied, "pkg", "application", "application_android.go")]
	patched, err := os.ReadFile(patchedPath)
	if err != nil {
		test.Fatal(err)
	}
	expected, err := redactAndroidFramework(before)
	if err != nil || string(patched) != string(expected) {
		test.Fatal("overlay contains changes beyond reviewed log calls", err)
	}
	after, err := os.ReadFile(original)
	if err != nil || string(after) != string(before) {
		test.Fatal("overlay modified the module cache", err)
	}
	originalModule := filepath.Dir(filepath.Dir(filepath.Dir(original)))
	if err := fs.WalkDir(os.DirFS(originalModule), ".", func(path string, entry fs.DirEntry, walkError error) error {
		if walkError != nil || entry.IsDir() {
			return walkError
		}
		originalData, readError := os.ReadFile(filepath.Join(originalModule, path))
		if readError != nil {
			return readError
		}
		copiedData, readError := os.ReadFile(filepath.Join(copied, path))
		if readError != nil || string(originalData) != string(copiedData) {
			test.Fatalf("temporary framework copy changed %s", path)
		}
		return nil
	}); err != nil {
		test.Fatal(err)
	}
	for _, pair := range [][2]string{{filepath.Join(root, "go.sum"), strings.TrimSuffix(build.Modfile, ".mod") + ".sum"}} {
		originalData, originalError := os.ReadFile(pair[0])
		copiedData, copiedError := os.ReadFile(pair[1])
		if originalError != nil || copiedError != nil || string(originalData) != string(copiedData) {
			test.Fatal("temporary build changed locked checksums")
		}
	}
	if err := cleanup(); err != nil {
		test.Fatal(err)
	}
	for _, path := range []string{build.Overlay, build.Modfile, copied, patchedPath, filepath.Dir(build.Overlay)} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			test.Fatal("temporary overlay was not removed")
		}
	}
}

// TestAndroidModuleResolutionRejectsDrift allows only the intended source copy,
// rejecting dependency additions, removals, version changes and replacements.
func TestAndroidModuleResolutionRejectsDrift(test *testing.T) {
	copied := filepath.Join(test.TempDir(), "wails")
	before := map[string]moduleResolution{
		frameworkModulePath: {Path: frameworkModulePath, Version: frameworkVersion, Dir: "original-wails"},
		"fixture":           {Path: "fixture", Version: "v1.0.0", Dir: "original-fixture"},
	}
	newGraph := func() map[string]moduleResolution {
		return map[string]moduleResolution{
			frameworkModulePath: {Path: frameworkModulePath, Version: frameworkVersion, Dir: copied, Replace: &moduleResolution{Path: copied, Dir: copied}},
			"fixture":           before["fixture"],
		}
	}
	if err := verifyModuleResolutions(before, newGraph(), copied); err != nil {
		test.Fatal("exact source-only replacement rejected", err)
	}
	for _, scenario := range []string{"added", "removed", "version", "source", "replacement", "framework-version", "framework-source"} {
		test.Run(scenario, func(test *testing.T) {
			after := newGraph()
			module := after["fixture"]
			switch scenario {
			case "added":
				after["unexpected"] = moduleResolution{Path: "unexpected", Version: "v1.0.0"}
			case "removed":
				delete(after, "fixture")
			case "version":
				module.Version = "v2.0.0"
				after["fixture"] = module
			case "source":
				module.Dir = "unexpected-source"
				after["fixture"] = module
			case "replacement":
				module.Replace = &moduleResolution{Path: "unexpected"}
				after["fixture"] = module
			case "framework-version":
				module = after[frameworkModulePath]
				module.Version = "v3.0.0-beta.31"
				after[frameworkModulePath] = module
			case "framework-source":
				after[frameworkModulePath].Replace.Path = "unexpected"
			}
			if err := verifyModuleResolutions(before, after, copied); err == nil {
				test.Fatal("temporary modfile accepted an unauthorized resolution change")
			}
		})
	}
}

// TestAndroidCachedArchiveRequiresExtractedSources protects the immutable source
// cache even when Go could unpack an already cached ZIP without using a proxy.
func TestAndroidCachedArchiveRequiresExtractedSources(test *testing.T) {
	cache := test.TempDir()
	modules := map[string]moduleResolution{"fixture": {Path: "example.com/Upper/fixture", Version: "v1.0.0"}}
	if err := verifyCachedModuleSources(modules, cache); err != nil {
		test.Fatal("offline-unavailable module must not be mistaken for an extractable archive", err)
	}
	archive := filepath.Join(cache, "cache", "download", "example.com", "!upper", "fixture", "@v", "v1.0.0.zip")
	if err := os.MkdirAll(filepath.Dir(archive), 0700); err != nil {
		test.Fatal(err)
	}
	if err := os.WriteFile(archive, []byte("synthetic-archive"), 0600); err != nil {
		test.Fatal(err)
	}
	if err := verifyCachedModuleSources(modules, cache); err == nil {
		test.Fatal("Go could extract an archive into the original module cache")
	}
	if err := os.MkdirAll(filepath.Join(cache, "example.com", "!upper", "fixture@v1.0.0"), 0700); err != nil {
		test.Fatal(err)
	}
	if err := verifyCachedModuleSources(modules, cache); err == nil {
		test.Fatal("missing source checksum could cause Go to rewrite the cache")
	}
	metadata := strings.TrimSuffix(archive, ".zip")
	if err := os.WriteFile(metadata+".ziphash", []byte("synthetic-checksum"), 0600); err != nil {
		test.Fatal(err)
	}
	if err := os.WriteFile(metadata+".partial", nil, 0600); err != nil {
		test.Fatal(err)
	}
	if err := verifyCachedModuleSources(modules, cache); err == nil {
		test.Fatal("incomplete source extraction could rewrite the original cache")
	}
	if err := os.Remove(metadata + ".partial"); err != nil {
		test.Fatal(err)
	}
	if err := verifyCachedModuleSources(modules, cache); err != nil {
		test.Fatal("existing extracted source rejected", err)
	}
}

// TestAndroidOverlayFailureCleansTemporaryCopy forces failure after copying
// starts, without changing module files, module cache or compiler configuration.
func TestAndroidOverlayFailureCleansTemporaryCopy(test *testing.T) {
	temporary := test.TempDir()
	for _, variable := range []string{"TMPDIR", "TMP", "TEMP"} {
		test.Setenv(variable, temporary)
	}
	root, fixture := test.TempDir(), test.TempDir()
	if err := os.WriteFile(filepath.Join(fixture, "fixture.go"), []byte("package fixture\n"), 0600); err != nil {
		test.Fatal(err)
	}
	before := map[string]moduleResolution{frameworkModulePath: {Path: frameworkModulePath, Version: frameworkVersion, Dir: fixture}}
	if build, cleanup, err := writeAndroidFrameworkOverlay(root, nil, nil, before); err == nil || cleanup != nil || build != (androidFrameworkBuild{}) {
		test.Fatal("missing root go.mod did not fail preparation")
	}
	entries, err := os.ReadDir(temporary)
	if err != nil {
		test.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "yousage-android-overlay-") {
			test.Fatal("failed preparation leaked its source copy")
		}
	}
}

// TestAndroidOfflineEnvironmentRejectsProxyBypass preserves native compiler
// options while overriding inherited direct-fetch settings for private modules.
func TestAndroidOfflineEnvironmentRejectsProxyBypass(test *testing.T) {
	test.Setenv("GONOPROXY", "*")
	test.Setenv("GOPRIVATE", "*")
	for _, overrides := range []map[string]string{nil, {"GOOS": "android", "GOPROXY": "direct", "GONOPROXY": "*"}} {
		values := map[string]string{}
		for _, setting := range offlineGoEnvironment(overrides) {
			name, value, found := strings.Cut(setting, "=")
			if found {
				values[name] = value
			}
		}
		if values["GOPROXY"] != "off" || values["GONOPROXY"] != "none" || values["GOTOOLCHAIN"] != "local" {
			test.Fatal("inherited proxy bypass permits dependency or toolchain acquisition")
		}
		if overrides != nil && values["GOOS"] != "android" {
			test.Fatal("offline environment lost native compilation options")
		}
		if values["GOPRIVATE"] != "*" {
			test.Fatal("offline environment changed the user's private module classification")
		}
	}
}
