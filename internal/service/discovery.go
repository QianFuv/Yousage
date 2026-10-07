package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"yousage/internal/domain"
	"yousage/internal/history/cursor"
	"yousage/internal/preferences"
)

// nativeInstallation bounds repeated executable discovery during usage reads.
type nativeInstallation struct {
	path       string
	validUntil time.Time
}

// isNativeInstalled revalidates discovered executables and expires PATH searches.
func (service *Service) isNativeInstalled(target NativeProvider) bool {
	inputs := []string{target.Driver, target.Executable, service.Platform, service.Home}
	for _, name := range []string{"PATH", "APPDATA", "LOCALAPPDATA", "PNPM_HOME"} {
		inputs = append(inputs, service.environmentValue(name))
	}
	encoded, _ := json.Marshal(inputs)
	key := string(encoded)
	service.installationMutex.Lock()
	defer service.installationMutex.Unlock()
	previous, exists := service.installations[key]
	if exists && service.Now().Before(previous.validUntil) && (previous.path == "" || nativeExecutable(previous.path, service.Platform)) {
		return previous.path != ""
	}
	path := service.resolveExecutable(target.Driver, target.Executable)
	if service.installations == nil {
		service.installations = map[string]nativeInstallation{}
	}
	service.installations[key] = nativeInstallation{path: path, validUntil: service.Now().Add(time.Minute)}
	return path != ""
}

// ProviderDetection separates filesystem availability from account authorization.
type ProviderDetection struct {
	Driver      string          `json:"driver"`
	Provider    domain.Provider `json:"provider"`
	IsInstalled bool            `json:"isInstalled"`
	HasHistory  bool            `json:"hasHistory"`
	Message     string          `json:"message"`
}

func (service *Service) environmentValue(name string) string {
	if value, exists := service.Environment[name]; exists {
		return strings.TrimSpace(value)
	}
	if service.Platform == "windows" {
		for key, value := range service.Environment {
			if strings.EqualFold(key, name) {
				return strings.TrimSpace(value)
			}
		}
	}
	return ""
}
func (service *Service) absoluteHome(value string) string {
	if value == "~" {
		value = service.Home
	} else if strings.HasPrefix(value, "~/") || strings.HasPrefix(value, "~\\") {
		value = filepath.Join(service.Home, value[2:])
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return value
	}
	return absolute
}
func (service *Service) rootList(values string) []string {
	roots := []string{}
	seen := map[string]bool{}
	for _, value := range strings.Split(values, ",") {
		if value = strings.TrimSpace(value); value != "" {
			root := service.absoluteHome(value)
			if !seen[root] {
				roots = append(roots, root)
				seen[root] = true
			}
		}
	}
	return roots
}
func (service *Service) historyRoots(provider domain.Provider) []string {
	defaultRoot := func(variable, directory string) string {
		if value := service.environmentValue(variable); value != "" {
			return service.absoluteHome(value)
		}
		return filepath.Join(service.Home, directory)
	}
	switch provider {
	case domain.Codex:
		return []string{defaultRoot("CODEX_HOME", ".codex")}
	case domain.Claude:
		return []string{defaultRoot("CLAUDE_CONFIG_DIR", ".claude")}
	case domain.Grok:
		return []string{defaultRoot("GROK_HOME", ".grok")}
	case domain.Cursor:
		credentials := cursor.DiscoverCredentials(service.Platform, service.Home, service.Environment, false)
		if credentials.Path != "" {
			return []string{credentials.Path}
		}
		return []string{filepath.Join(service.Home, ".cursor", "auth.json")}
	case domain.OpenCode:
		if roots := service.rootList(service.environmentValue("OPENCODE_DATA_DIR")); len(roots) > 0 {
			return roots
		}
		base := service.environmentValue("XDG_DATA_HOME")
		if !filepath.IsAbs(base) {
			base = filepath.Join(service.Home, ".local", "share")
		}
		return []string{filepath.Join(service.absoluteHome(base), "opencode")}
	case domain.Antigravity:
		if roots := service.rootList(service.environmentValue("ANTIGRAVITY_DATA_DIR")); len(roots) > 0 {
			return roots
		}
		roots := []string{}
		for _, name := range []string{"antigravity", "antigravity-cli", "antigravity-ide", "antigravity-backup"} {
			roots = append(roots, filepath.Join(service.Home, ".gemini", name))
		}
		return append(roots, filepath.Join(service.Home, ".config", "antigravity"))
	}
	return nil
}
func nativeExecutable(path, platform string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || !filepath.IsAbs(path) {
		return false
	}
	if platform == "windows" {
		return strings.EqualFold(filepath.Ext(path), ".exe")
	}
	return info.Mode().Perm()&0111 != 0
}

// resolveExecutable follows registered commands and native npm payloads without executing shims.
func (service *Service) resolveExecutable(driver, explicit string) string {
	if explicit != "" {
		if nativeExecutable(explicit, service.Platform) {
			return explicit
		}
		return ""
	}
	command := driver
	if driver == "claudeAgent" {
		command = "claude"
	}
	separator := ":"
	if service.Platform == "windows" {
		separator = ";"
	}
	directories := []string{}
	for _, directory := range strings.Split(service.environmentValue("PATH"), separator) {
		directory = strings.Trim(strings.TrimSpace(directory), "\"")
		if filepath.IsAbs(directory) {
			directories = append(directories, directory)
		}
	}
	if service.Platform == "windows" {
		for _, directory := range []string{filepath.Join(service.environmentValue("APPDATA"), "npm"), filepath.Join(service.environmentValue("LOCALAPPDATA"), "Programs", "nodejs"), filepath.Join(service.environmentValue("LOCALAPPDATA"), "Volta", "bin"), filepath.Join(service.environmentValue("LOCALAPPDATA"), "pnpm"), filepath.Join(service.Home, ".volta", "bin"), filepath.Join(service.Home, ".local", "bin"), filepath.Join(service.Home, ".bun", "bin"), filepath.Join(service.Home, "scoop", "shims"), service.environmentValue("PNPM_HOME")} {
			if filepath.IsAbs(directory) {
				directories = append(directories, directory)
			}
		}
	} else {
		directories = append(directories, filepath.Join(service.Home, ".local", "bin"), "/opt/homebrew/bin", "/usr/local/bin", "/usr/bin")
	}
	seen := map[string]bool{}
	for _, directory := range directories {
		if seen[directory] {
			continue
		}
		seen[directory] = true
		name := command
		if service.Platform == "windows" {
			name += ".exe"
		}
		if path := filepath.Join(directory, name); nativeExecutable(path, service.Platform) {
			return path
		}
		if service.Platform != "windows" {
			continue
		}
		if driver == "claudeAgent" {
			path := filepath.Join(directory, "node_modules", "@anthropic-ai", "claude-code", "bin", "claude.exe")
			if nativeExecutable(path, service.Platform) {
				return path
			}
		} else if driver == "codex" {
			packageDirectory := filepath.Join(directory, "node_modules", "@openai")
			if info, err := os.Stat(packageDirectory); err != nil || !info.IsDir() {
				continue
			}
			architecture, triple := "x64", "x86_64-pc-windows-msvc"
			if runtime.GOARCH == "arm64" {
				architecture, triple = "arm64", "aarch64-pc-windows-msvc"
			}
			root := filepath.Join(directory, "node_modules", "@openai", "codex")
			if canonical, err := filepath.EvalSymlinks(root); err == nil {
				root = canonical
			}
			platformPackage := "codex-win32-" + architecture
			bases := []string{filepath.Join(root, "node_modules", "@openai", platformPackage), filepath.Join(filepath.Dir(root), platformPackage), filepath.Join(directory, "node_modules", "@openai", platformPackage)}
			for _, base := range bases {
				for _, subdirectory := range []string{"bin", "codex"} {
					path := filepath.Join(base, "vendor", triple, subdirectory, "codex.exe")
					if nativeExecutable(path, service.Platform) {
						return path
					}
				}
			}
			for _, subdirectory := range []string{"bin", "codex"} {
				path := filepath.Join(root, "vendor", triple, subdirectory, "codex.exe")
				if nativeExecutable(path, service.Platform) {
					return path
				}
			}
		}
	}
	return ""
}
func (service *Service) expandedSources(sources []preferences.Source) []preferences.Source {
	result := []preferences.Source{}
	for _, source := range sources {
		if !source.IsAutomatic {
			result = append(result, source)
			continue
		}
		for index, root := range service.historyRoots(source.Provider) {
			expanded := source
			expanded.Root = root
			if index > 0 {
				expanded.Id = source.Id + ":" + smallIdentity(root)
			}
			result = append(result, expanded)
		}
	}
	return result
}

// DetectProviders reads installation metadata without credentials or enabled-account probes.
func (service *Service) DetectProviders(ctx context.Context) ([]ProviderDetection, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	detections := []ProviderDetection{}
	for _, pair := range []struct {
		driver   string
		provider domain.Provider
	}{{"codex", domain.Codex}, {"claudeAgent", domain.Claude}, {"cursor", domain.Cursor}, {"grok", domain.Grok}, {"opencode", domain.OpenCode}, {"antigravity", domain.Antigravity}} {
		if !service.isProviderInScope(pair.provider) {
			continue
		}
		detection := ProviderDetection{Driver: pair.driver, Provider: pair.provider}
		for _, root := range service.historyRoots(pair.provider) {
			if _, err := os.Stat(root); err == nil {
				detection.IsInstalled = true
			}
			historyRoot := root
			if pair.provider == domain.Codex || pair.provider == domain.Grok {
				historyRoot = filepath.Join(root, "sessions")
			} else if pair.provider == domain.Claude {
				historyRoot = filepath.Join(root, "projects")
			}
			if _, err := os.Stat(historyRoot); err == nil {
				detection.HasHistory = true
			}
		}
		if pair.driver == "codex" || pair.driver == "claudeAgent" {
			detection.IsInstalled = service.resolveExecutable(pair.driver, "") != ""
		}
		if pair.driver == "antigravity" {
			detection.Message = "History supported; native quotas unavailable."
		} else if detection.IsInstalled {
			detection.Message = "Detected automatically. Account access is checked only after enabling quotas."
		} else {
			detection.Message = "Not detected. Install the provider or configure its standard environment location, then detect again."
		}
		detections = append(detections, detection)
	}
	return detections, nil
}
