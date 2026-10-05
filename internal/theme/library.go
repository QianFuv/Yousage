package theme

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/fsnotify/fsnotify"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type SettingsRepository interface {
	ReadSetting(context.Context, string, any) (int64, error)
	WriteSetting(context.Context, string, int64, any) (int64, error)
}
type Typography struct {
	Sans          string `json:"sans"`
	Code          string `json:"code"`
	InterfaceSize int    `json:"interfaceSize"`
	CodeSize      int    `json:"codeSize"`
	Smoothing     bool   `json:"smoothing"`
}
type AppearancePreferences struct {
	Mode               string            `json:"mode"`
	BaseThemeId        string            `json:"baseThemeId"`
	LightOwner         string            `json:"lightOwner,omitempty"`
	DarkOwner          string            `json:"darkOwner,omitempty"`
	Typography         Typography        `json:"typography"`
	Contrast           float64           `json:"contrast"`
	GlassOpacity       float64           `json:"glassOpacity"`
	MotionMs           int               `json:"motionMs"`
	AppliedGenerations map[string]string `json:"appliedGenerations"`
}
type LibraryValue struct {
	Preferences  AppearancePreferences `json:"preferences"`
	CustomThemes []Definition          `json:"customThemes"`
	Attributions []Attribution         `json:"attributions"`
}
type Attribution struct {
	CollectionId string `json:"collectionId"`
	Name         string `json:"name"`
	Publisher    string `json:"publisher"`
	Version      string `json:"version"`
	License      string `json:"license"`
	Url          string `json:"url"`
}
type LibrarySnapshot struct {
	Revision  int64        `json:"revision"`
	Value     LibraryValue `json:"value"`
	Published Snapshot     `json:"published"`
	Error     string       `json:"error,omitempty"`
}
type Library struct {
	Repository           SettingsRepository
	PublishedStore       Store
	mutex                sync.Mutex
	lastPublished        Snapshot
	externalPublished    *Snapshot
	primaryEnvironmentId string
}

func defaultLibraryValue() LibraryValue {
	return LibraryValue{Preferences: AppearancePreferences{Mode: "system", BaseThemeId: "system", Typography: Typography{InterfaceSize: 16, CodeSize: 13}, Contrast: 100, GlassOpacity: 80, MotionMs: 200, AppliedGenerations: map[string]string{}}, CustomThemes: []Definition{}}
}
func (preferences AppearancePreferences) validate() error {
	if preferences.Mode != "system" && preferences.Mode != "light" && preferences.Mode != "dark" {
		return errors.New("invalid appearance mode")
	}
	if strings.TrimSpace(preferences.BaseThemeId) == "" || len(preferences.BaseThemeId) > 64 || len(preferences.LightOwner) > 64 || len(preferences.DarkOwner) > 64 {
		return errors.New("invalid theme preference")
	}
	if preferences.Typography.InterfaceSize < 12 || preferences.Typography.InterfaceSize > 20 || preferences.Typography.CodeSize < 10 || preferences.Typography.CodeSize > 18 || len(preferences.Typography.Sans) > 256 || len(preferences.Typography.Code) > 256 {
		return errors.New("invalid typography preference")
	}
	if math.IsNaN(preferences.Contrast) || math.IsInf(preferences.Contrast, 0) || preferences.Contrast < 50 || preferences.Contrast > 200 || math.IsNaN(preferences.GlassOpacity) || math.IsInf(preferences.GlassOpacity, 0) || preferences.GlassOpacity < 40 || preferences.GlassOpacity > 100 || preferences.MotionMs < 0 || preferences.MotionMs > 400 {
		return errors.New("invalid appearance effect")
	}
	if len(preferences.AppliedGenerations) > 256 {
		return errors.New("too many published theme generations")
	}
	return nil
}
func normalizeLibrary(value LibraryValue) (LibraryValue, error) {
	if err := value.Preferences.validate(); err != nil {
		return value, err
	}
	if value.Preferences.AppliedGenerations == nil {
		value.Preferences.AppliedGenerations = map[string]string{}
	}
	if len(value.CustomThemes) > 256 {
		return value, errors.New("too many custom themes")
	}
	if value.CustomThemes == nil {
		value.CustomThemes = []Definition{}
	}
	if value.Attributions == nil {
		value.Attributions = []Attribution{}
	}
	if len(value.Attributions) > 256 {
		return value, errors.New("too many theme attribution records")
	}
	for _, attribution := range value.Attributions {
		if !collectionIdPattern.MatchString(attribution.CollectionId) || len(attribution.Name) > 256 || len(attribution.Publisher) > 128 || len(attribution.Version) > 128 || len(attribution.License) > 128 || len(attribution.Url) > 2048 {
			return value, errors.New("invalid theme attribution record")
		}
	}
	seen := map[string]bool{}
	for index, definition := range value.CustomThemes {
		if seen[definition.Id] || reservedId(definition.Id, false) || !themeIdPattern.MatchString(definition.Id) {
			return value, errors.New("duplicate, invalid or protected theme identity")
		}
		seen[definition.Id] = true
		data, err := json.Marshal(File{Version: 1, Id: definition.Id, Name: definition.Label, Appearance: definition.Appearance, Colors: definition.Colors, Variants: definition.Variants, Collection: definition.Collection, Managed: definition.Managed})
		if err != nil {
			return value, err
		}
		normalized, err := ParseFile(data)
		if err != nil {
			return value, err
		}
		normalized.SidebarArtwork = false
		value.CustomThemes[index] = normalized
	}
	return value, nil
}
func (library *Library) snapshot(ctx context.Context, shouldAdopt bool) (LibrarySnapshot, error) {
	value := defaultLibraryValue()
	revision, err := library.Repository.ReadSetting(ctx, "theme-library", &value)
	if err != nil {
		return LibrarySnapshot{}, err
	}
	value, err = normalizeLibrary(value)
	if err != nil {
		return LibrarySnapshot{}, err
	}
	result := LibrarySnapshot{Revision: revision, Value: value}
	published, err := library.PublishedStore.Show(ctx)
	environmentId := "local"
	if library.externalPublished != nil {
		published, err = *library.externalPublished, nil
		environmentId = library.primaryEnvironmentId
	}
	if err != nil {
		result.Published = library.lastPublished
		result.Error = "Published themes could not be read. Last known themes are retained."
	} else {
		library.lastPublished = published
		result.Published = published
	}
	generation := published.DefaultTheme
	if published.Generation != "" {
		generation += "@" + published.Generation
	}
	if shouldAdopt && result.Error == "" && published.DefaultTheme != "" && value.Preferences.AppliedGenerations[environmentId] != generation {
		var selected *Definition
		for _, definition := range append(append(Builtins(), published.Themes...), value.CustomThemes...) {
			if definition.Id == published.DefaultTheme {
				copy := definition
				selected = &copy
			}
		}
		if selected != nil {
			value.Preferences.BaseThemeId = selected.Id
			value.Preferences.LightOwner, value.Preferences.DarkOwner = "", ""
			if len(selected.Variants) == 0 {
				value.Preferences.Mode = selected.Appearance
			}
			value.Preferences.AppliedGenerations[environmentId] = generation
			revision, err = library.Repository.WriteSetting(ctx, "theme-library", revision, value)
			if err != nil {
				return result, err
			}
			result.Revision, result.Value = revision, value
		}
	}
	return result, nil
}

// SetPrimaryPublished scopes published palettes and one-time adoption to the chosen primary environment.
// A nil snapshot returns authority to the local CLI publisher.
func (library *Library) SetPrimaryPublished(ctx context.Context, environmentId string, published *Snapshot) (LibrarySnapshot, error) {
	library.mutex.Lock()
	defer library.mutex.Unlock()
	library.primaryEnvironmentId = environmentId
	library.externalPublished = published
	return library.snapshot(ctx, true)
}

// Read adopts each resolvable published default generation once, preserving later manual choices.
func (library *Library) Read(ctx context.Context) (LibrarySnapshot, error) {
	library.mutex.Lock()
	defer library.mutex.Unlock()
	return library.snapshot(ctx, true)
}

// Save atomically replaces the intended library and preferences under the observed revision.
func (library *Library) Save(ctx context.Context, revision int64, value LibraryValue) (LibrarySnapshot, error) {
	library.mutex.Lock()
	defer library.mutex.Unlock()
	value, err := normalizeLibrary(value)
	if err != nil {
		return LibrarySnapshot{}, err
	}
	current, err := library.snapshot(ctx, false)
	if err != nil {
		return LibrarySnapshot{}, err
	}
	if revision != current.Revision {
		return current, errors.New("Appearance changed. Reload before saving this draft.")
	}
	value.Preferences.AppliedGenerations = current.Value.Preferences.AppliedGenerations
	next, err := library.Repository.WriteSetting(ctx, "theme-library", revision, value)
	if err != nil {
		return current, err
	}
	current.Revision, current.Value = next, value
	return current, nil
}

// Watch emits complete structurally changed snapshots with a 100ms settling window and owned cancellation.
func (library *Library) Watch(ctx context.Context, emit func(LibrarySnapshot)) error {
	directory := library.PublishedStore.Directory
	if err := os.MkdirAll(filepath.Join(directory, "themes"), 0700); err != nil {
		return errors.New("Published theme directory could not be watched")
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return errors.New("Published theme watcher is unavailable")
	}
	defer watcher.Close()
	if watcher.Add(directory) != nil || watcher.Add(filepath.Join(directory, "themes")) != nil {
		return errors.New("Published theme directory could not be watched")
	}
	previous := ""
	read := func() {
		value, err := library.Read(ctx)
		if err == nil {
			data, _ := json.Marshal(value)
			serialized := string(data)
			if serialized != previous {
				previous = serialized
				emit(value)
			}
		}
	}
	read()
	var settle *time.Timer
	var settled <-chan time.Time
	defer func() {
		if settle != nil {
			settle.Stop()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return nil
		case event, isOpen := <-watcher.Events:
			if !isOpen {
				return nil
			}
			if event.Name != filepath.Join(directory, "settings.json") && filepath.Dir(event.Name) != filepath.Join(directory, "themes") && event.Name != filepath.Join(directory, "themes") {
				continue
			}
			if event.Name == filepath.Join(directory, "themes") {
				_ = watcher.Add(event.Name)
			}
			if settle != nil {
				settle.Stop()
			}
			settle = time.NewTimer(100 * time.Millisecond)
			settled = settle.C
		case <-settled:
			settled = nil
			read()
		case _, isOpen := <-watcher.Errors:
			if !isOpen {
				return nil
			}
			read()
		}
	}
}
