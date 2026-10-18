package service

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"yousage/internal/domain"
	"yousage/internal/preferences"
)

// ensurePiHistorySource migrates saved sources once without restoring a source removed later.
// Writing the marker last makes interrupted migrations safe to retry without duplicating sources.
func (service *Service) ensurePiHistorySource(ctx context.Context) error {
	var version int
	revision, err := service.Store.ReadSetting(ctx, "pi-history-source-version", &version)
	if err != nil || version >= 1 {
		return err
	}
	sources, sourceRevision, err := service.Preferences.Sources(ctx)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(sources, func(source preferences.Source) bool { return source.Provider == domain.Pi }) {
		identity := "pi-auto"
		for suffix := 1; slices.ContainsFunc(sources, func(source preferences.Source) bool { return source.Id == identity }); suffix++ {
			identity = "pi-auto-" + strconv.Itoa(suffix)
		}
		sources = append(sources, preferences.Source{Id: identity, Provider: domain.Pi, IsEnabled: true, IsAutomatic: true})
		if _, err := service.Store.WriteSetting(ctx, "sources", sourceRevision, sources); err != nil {
			return err
		}
	}
	_, err = service.Store.WriteSetting(ctx, "pi-history-source-version", revision, 1)
	return err
}

// isPiInstalled recognizes the Node-based CLI's Windows launchers without executing scripts.
func (service *Service) isPiInstalled() bool {
	if service.resolveExecutable("pi", "") != "" {
		return true
	}
	if service.Platform != "windows" {
		return false
	}
	directories := strings.Split(service.environmentValue("PATH"), ";")
	directories = append(directories, filepath.Join(service.environmentValue("APPDATA"), "npm"), filepath.Join(service.environmentValue("LOCALAPPDATA"), "pnpm"), service.environmentValue("PNPM_HOME"), filepath.Join(service.Home, ".bun", "bin"))
	for _, directory := range directories {
		directory = strings.Trim(strings.TrimSpace(directory), "\"")
		if !filepath.IsAbs(directory) {
			continue
		}
		for _, name := range []string{"pi.cmd", "pi.ps1"} {
			if info, err := os.Stat(filepath.Join(directory, name)); err == nil && info.Mode().IsRegular() {
				return true
			}
		}
	}
	return false
}
