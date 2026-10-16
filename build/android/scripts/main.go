// Command androidbuild compiles and packages the native viewer on supported hosts.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
)

// main resolves host tools and runs the requested Android build phases.
func main() {
	mode := flag.String("mode", "debug", "debug or release")
	architecture := flag.String("arch", "amd64", "arm64 or amd64")
	isCompileOnly := flag.Bool("compile-only", false, "skip Gradle assembly")
	isAssembleOnly := flag.Bool("assemble-only", false, "skip Go compilation")
	flag.Parse()
	if err := build(*mode, *architecture, *isCompileOnly, *isAssembleOnly); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// build validates options and executes each phase in dependency order.
func build(mode, architecture string, isCompileOnly, isAssembleOnly bool) error {
	if mode != "debug" && mode != "release" {
		return errors.New("mode must be debug or release")
	}
	if architecture != "arm64" && architecture != "amd64" {
		return errors.New("arch must be arm64 or amd64")
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	if _, err = os.Stat(filepath.Join(root, "build", "config.yml")); err != nil {
		return errors.New("run from the repository root (use wails3 task android:package)")
	}
	sdk, err := sdkDirectory()
	if err != nil {
		return err
	}
	if !isAssembleOnly {
		if err = compile(root, sdk, mode, architecture); err != nil {
			return err
		}
	}
	if isCompileOnly {
		return nil
	}
	return assemble(root, sdk, mode, architecture)
}

// sdkDirectory prefers explicit Android environment settings over OS defaults.
func sdkDirectory() (string, error) {
	for _, name := range []string{"ANDROID_HOME", "ANDROID_SDK_ROOT"} {
		if directory := os.Getenv(name); directory != "" {
			return existingDirectory(directory)
		}
	}
	userDirectory, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	directory := filepath.Join(userDirectory, "Android", "Sdk")
	switch runtime.GOOS {
	case "windows":
		directory = filepath.Join(os.Getenv("LOCALAPPDATA"), "Android", "Sdk")
	case "darwin":
		directory = filepath.Join(userDirectory, "Library", "Android", "sdk")
	}
	return existingDirectory(directory)
}

// existingDirectory returns an absolute directory or an actionable error.
func existingDirectory(directory string) (string, error) {
	info, err := os.Stat(directory)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("tool directory not found: %s", directory)
	}
	return filepath.Abs(directory)
}

// ndkDirectory selects an explicit NDK or the newest installed SDK side-by-side NDK.
func ndkDirectory(sdk string) (string, error) {
	for _, name := range []string{"ANDROID_NDK_HOME", "ANDROID_NDK_ROOT", "NDK_HOME"} {
		if directory := os.Getenv(name); directory != "" {
			return existingDirectory(directory)
		}
	}
	candidates, _ := filepath.Glob(filepath.Join(sdk, "ndk", "*"))
	slices.SortFunc(candidates, compareVersions)
	for index := len(candidates) - 1; index >= 0; index-- {
		if directory, err := existingDirectory(candidates[index]); err == nil {
			return directory, nil
		}
	}
	return "", errors.New("Android NDK not found; install a side-by-side NDK or set ANDROID_NDK_HOME")
}

// compareVersions sorts numeric NDK directory names without lexicographic surprises.
func compareVersions(first, second string) int {
	left := strings.Split(filepath.Base(first), ".")
	right := strings.Split(filepath.Base(second), ".")
	for index := 0; index < max(len(left), len(right)); index++ {
		var firstPart, secondPart int
		if index < len(left) {
			firstPart, _ = strconv.Atoi(left[index])
		}
		if index < len(right) {
			secondPart, _ = strconv.Atoi(right[index])
		}
		if firstPart != secondPart {
			return firstPart - secondPart
		}
	}
	return 0
}

// compile uses the selected NDK's host compiler to produce one JNI shared library.
func compile(root, sdk, mode, architecture string) error {
	ndk, err := ndkDirectory(sdk)
	if err != nil {
		return err
	}
	host := map[string]string{"windows": "windows-x86_64", "linux": "linux-x86_64", "darwin": "darwin-x86_64"}[runtime.GOOS]
	if host == "" {
		return fmt.Errorf("unsupported Android build host: %s", runtime.GOOS)
	}
	target, abi := "aarch64-linux-android", "arm64-v8a"
	if architecture == "amd64" {
		target, abi = "x86_64-linux-android", "x86_64"
	}
	toolchain := filepath.Join(ndk, "toolchains", "llvm", "prebuilt", host, "bin")
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".cmd"
	}
	compiler := filepath.Join(toolchain, target+"21-clang"+suffix)
	if _, err = os.Stat(compiler); err != nil {
		return fmt.Errorf("NDK compiler not found: %s", compiler)
	}
	destination := filepath.Join(root, "build", "android", "app", "src", "main", "jniLibs", abi, "libwails.so")
	if err = os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return err
	}
	tags := "android,debug"
	if mode == "release" {
		tags = "android,production"
	}
	command := exec.Command("go", "build", "-buildmode=c-shared", "-tags", tags, "-trimpath", "-buildvcs=false", "-ldflags=-w -s", "-o", destination, ".")
	command.Dir = root
	command.Env = environment(map[string]string{
		"GOOS": "android", "GOARCH": architecture, "CGO_ENABLED": "1",
		"CC":  strconv.Quote(filepath.ToSlash(compiler)),
		"CXX": strconv.Quote(filepath.ToSlash(filepath.Join(toolchain, target+"21-clang++"+suffix))),
	})
	return run(command)
}

// javaMajor reads a JDK's release metadata without starting an incompatible JVM.
func javaMajor(directory string) int {
	data, err := os.ReadFile(filepath.Join(directory, "release"))
	if err != nil {
		return 0
	}
	match := regexp.MustCompile(`(?m)^JAVA_VERSION="(\d+)`).FindSubmatch(data)
	if len(match) < 2 {
		return 0
	}
	version, _ := strconv.Atoi(string(match[1]))
	return version
}

// javaDirectory respects JAVA_HOME, then finds a Gradle-compatible JDK 17 or 21.
func javaDirectory() (string, error) {
	if directory := os.Getenv("JAVA_HOME"); directory != "" {
		return existingDirectory(directory)
	}
	var candidates []string
	for _, name := range []string{"JAVA_HOME_21_X64", "JAVA_HOME_17_X64", "JDK_HOME"} {
		if directory := os.Getenv(name); directory != "" {
			candidates = append(candidates, directory)
		}
	}
	if executable, err := exec.LookPath("java"); err == nil {
		if resolved, resolveError := filepath.EvalSymlinks(executable); resolveError == nil {
			executable = resolved
		}
		directory := filepath.Dir(filepath.Dir(executable))
		candidates = append(candidates, directory)
		siblings, _ := filepath.Glob(filepath.Join(filepath.Dir(directory), "*"))
		candidates = append(candidates, siblings...)
	}
	patterns := []string{"/usr/lib/jvm/*", "/Library/Java/JavaVirtualMachines/*/Contents/Home"}
	if runtime.GOOS == "windows" {
		patterns = []string{
			filepath.Join(os.Getenv("ProgramFiles"), "Eclipse Adoptium", "*"),
			filepath.Join(os.Getenv("ProgramFiles"), "Java", "*"),
			filepath.Join(os.Getenv("ProgramFiles"), "Microsoft", "jdk-*"),
		}
	}
	for _, pattern := range patterns {
		matches, _ := filepath.Glob(pattern)
		candidates = append(candidates, matches...)
	}
	for _, version := range []int{21, 17} {
		for _, directory := range candidates {
			if javaMajor(directory) == version {
				return filepath.Abs(directory)
			}
		}
	}
	return "", errors.New("JDK 17 or 21 not found; set JAVA_HOME to a compatible JDK")
}

// assemble runs the host Gradle wrapper and publishes stable and ABI-specific APK paths.
func assemble(root, sdk, mode, architecture string) error {
	java, err := javaDirectory()
	if err != nil {
		return err
	}
	config, err := os.ReadFile(filepath.Join(root, "build", "config.yml"))
	if err != nil {
		return err
	}
	match := regexp.MustCompile(`(?m)^\s+version:\s*([^\s]+)`).FindSubmatch(config)
	if len(match) != 2 {
		return errors.New("app version missing from build/config.yml")
	}
	version := strings.Trim(string(match[1]), `"'`)
	variant := "Debug"
	if mode == "release" {
		variant = "Release"
	}
	abi := "arm64-v8a"
	if architecture == "amd64" {
		abi = "x86_64"
	}
	project := filepath.Join(root, "build", "android")
	arguments := []string{"--no-daemon", "assemble" + variant, "-PyousageVersion=" + version, "-PyousageAbi=" + abi}
	var command *exec.Cmd
	if runtime.GOOS == "windows" {
		command = exec.Command("cmd.exe", append([]string{"/d", "/c", `.\gradlew.bat`}, arguments...)...)
	} else {
		command = exec.Command("sh", append([]string{"./gradlew"}, arguments...)...)
	}
	command.Dir = project
	command.Env = environment(map[string]string{"JAVA_HOME": java, "ANDROID_HOME": sdk, "ANDROID_SDK_ROOT": sdk})
	fmt.Printf("Android SDK: %s\nJava: %s\n", sdk, java)
	if err = run(command); err != nil {
		return err
	}
	source := filepath.Join(project, "app", "build", "outputs", "apk", mode, "app-"+mode+".apk")
	for _, name := range []string{"yousage.apk", "yousage-" + mode + "-" + architecture + ".apk"} {
		destination := filepath.Join(root, "bin", name)
		if err = copyFile(source, destination); err != nil {
			return err
		}
		info, err := os.Stat(destination)
		if err != nil {
			return err
		}
		fmt.Printf("APK: %s (%d bytes)\n", destination, info.Size())
	}
	return nil
}

// environment replaces selected process variables without duplicate entries.
func environment(overrides map[string]string) []string {
	result := make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		hasOverride := false
		for key := range overrides {
			if strings.EqualFold(name, key) {
				hasOverride = true
				break
			}
		}
		if !hasOverride {
			result = append(result, entry)
		}
	}
	for name, value := range overrides {
		result = append(result, name+"="+value)
	}
	return result
}

// run streams build output and preserves subprocess failures.
func run(command *exec.Cmd) error {
	command.Stdout, command.Stderr, command.Stdin = os.Stdout, os.Stderr, os.Stdin
	fmt.Println(command.String())
	return command.Run()
}

// copyFile publishes an APK without requiring platform-specific shell commands.
func copyFile(source, destination string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.Create(destination)
	if err != nil {
		return err
	}
	_, err = io.Copy(output, input)
	closeError := output.Close()
	if err != nil {
		return err
	}
	return closeError
}
