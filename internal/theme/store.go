package theme

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Store publishes only into the explicitly selected Yousage-owned state directory.
type Store struct {
	Directory       string
	replaceSettings func(string, string) error
}

// Snapshot separates a published default generation from the client's saved selection.
type Snapshot struct {
	DefaultTheme string       `json:"defaultTheme,omitempty"`
	Generation   string       `json:"defaultThemeSetAt,omitempty"`
	Themes       []Definition `json:"themes"`
}

// Set selects a resolvable ID or atomically publishes a supplied file and selects its filename ID.
func (store Store) Set(ctx context.Context, target, explicitId string) (string, error) {
	unlock, err := store.lock(ctx)
	if err != nil {
		return "", err
	}
	defer unlock()
	settings, err := store.readSettings()
	if err != nil {
		return "", err
	}
	target = strings.TrimSpace(target)
	if strings.HasPrefix(target, "~/") || strings.HasPrefix(target, "~\\") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		target = filepath.Join(home, target[2:])
	}
	isPath := strings.HasSuffix(target, ".json") || strings.ContainsAny(target, "/\\") || strings.HasPrefix(target, "~")
	identity := target
	var publishedData []byte
	if isPath {
		resolved, err := filepath.EvalSymlinks(target)
		if err != nil {
			return "", err
		}
		publishedData, err = readRegularFile(resolved, 32*1024)
		if err != nil {
			return "", err
		}
		if _, err = ParsePublished(publishedData); err != nil {
			return "", err
		}
		identity = strings.TrimSuffix(filepath.Base(target), ".json")
		if explicitId != "" {
			identity = explicitId
		}
		if !themeIdPattern.MatchString(identity) || reservedId(identity, true) {
			return "", errors.New("invalid or reserved publishing id")
		}
	} else {
		if explicitId != "" {
			return "", errors.New("--id requires a theme file")
		}
		isKnown := false
		for _, theme := range Builtins() {
			if theme.Id == identity {
				isKnown = true
			}
		}
		for _, theme := range store.readPublished() {
			if theme.Id == identity {
				isKnown = true
			}
		}
		if !isKnown {
			return "", fmt.Errorf("unknown theme id %q", identity)
		}
	}
	settings["defaultTheme"], _ = json.Marshal(identity)
	generation := time.Now().UTC().Format(time.RFC3339Nano)
	if string(settings["defaultThemeSetAt"]) == strconvQuote(generation) {
		generation = time.Now().UTC().Add(time.Nanosecond).Format(time.RFC3339Nano)
	}
	settings["defaultThemeSetAt"], _ = json.Marshal(generation)
	settingsData, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return "", err
	}
	settingsPath := filepath.Join(store.Directory, "settings.json")
	settingsStaged, err := stageFile(settingsPath, append(settingsData, '\n'))
	if err != nil {
		return "", err
	}
	defer os.Remove(settingsStaged)
	var cleanupPublish func(bool) error
	if publishedData != nil {
		cleanupPublish, err = store.publish(identity, publishedData)
		if err != nil {
			return "", err
		}
	}
	replace := store.replaceSettings
	if replace == nil {
		replace = replaceFile
	}
	if err = replace(settingsStaged, settingsPath); err != nil {
		if cleanupPublish != nil {
			if rollbackError := cleanupPublish(false); rollbackError != nil {
				return "", errors.Join(err, rollbackError)
			}
		}
		return "", err
	}
	if cleanupPublish != nil {
		if err = cleanupPublish(true); err != nil {
			return "", err
		}
	}
	return identity, nil
}

func strconvQuote(value string) string { data, _ := json.Marshal(value); return string(data) }

// Clear removes only the published default fields, leaving selected themes and files intact.
func (store Store) Clear(ctx context.Context) error {
	unlock, err := store.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	settings, err := store.readSettings()
	if err != nil {
		return err
	}
	delete(settings, "defaultTheme")
	delete(settings, "defaultThemeSetAt")
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(store.Directory, "settings.json")
	staged, err := stageFile(path, append(data, '\n'))
	if err != nil {
		return err
	}
	defer os.Remove(staged)
	return replaceFile(staged, path)
}

// Show reads the current published set and generation without launching the GUI.
func (store Store) Show(ctx context.Context) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	settings, err := store.readSettings()
	if err != nil {
		return Snapshot{}, err
	}
	snapshot := Snapshot{Themes: store.readPublished()}
	json.Unmarshal(settings["defaultTheme"], &snapshot.DefaultTheme)
	json.Unmarshal(settings["defaultThemeSetAt"], &snapshot.Generation)
	return snapshot, nil
}

func (store Store) readSettings() (map[string]json.RawMessage, error) {
	data, err := os.ReadFile(filepath.Join(store.Directory, "settings.json"))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]json.RawMessage{}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return map[string]json.RawMessage{}, nil
	}
	var settings map[string]json.RawMessage
	if json.Unmarshal(data, &settings) != nil || settings == nil {
		return nil, errors.New("theme settings require a readable JSON object")
	}
	return settings, nil
}

func stageFile(path string, data []byte) (string, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".yousage-theme-staging-")
	if err != nil {
		return "", err
	}
	staged := file.Name()
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	closeError := file.Close()
	if err == nil {
		err = closeError
	}
	if err != nil {
		os.Remove(staged)
		return "", err
	}
	return staged, nil
}

func (store Store) publish(identity string, data []byte) (func(bool) error, error) {
	destination := filepath.Join(store.Directory, "themes", identity+".json")
	staged, err := stageFile(destination, data)
	if err != nil {
		return nil, err
	}
	defer os.Remove(staged)
	stagedFile, err := os.Open(staged)
	if err != nil {
		return nil, err
	}
	publishedInfo, err := stagedFile.Stat()
	stagedFile.Close()
	if err != nil {
		return nil, err
	}
	backup := staged + ".rollback"
	hasPrevious := false
	if err = os.Rename(destination, backup); err == nil {
		hasPrevious = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err = replaceFile(staged, destination); err != nil {
		if hasPrevious {
			err = errors.Join(err, replaceFile(backup, destination))
		}
		return nil, err
	}
	return func(isCommitted bool) error {
		if isCommitted {
			if hasPrevious {
				return os.Remove(backup)
			}
			return nil
		}
		currentInfo, currentError := os.Stat(destination)
		if currentError == nil && !os.SameFile(publishedInfo, currentInfo) {
			if hasPrevious {
				return os.Remove(backup)
			}
			return nil
		}
		if currentError != nil && !errors.Is(currentError, os.ErrNotExist) {
			return currentError
		}
		if hasPrevious {
			return replaceFile(backup, destination)
		}
		if errors.Is(currentError, os.ErrNotExist) {
			return nil
		}
		return os.Remove(destination)
	}, nil
}

func (store Store) readPublished() []Definition {
	directory := filepath.Join(store.Directory, "themes")
	entries, err := os.ReadDir(directory)
	themes := []Definition{}
	if err != nil {
		return themes
	}
	examined, total := 0, 0
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		identity := strings.TrimSuffix(entry.Name(), ".json")
		if !themeIdPattern.MatchString(identity) || reservedId(identity, true) {
			continue
		}
		examined++
		if examined > 32 {
			break
		}
		data, err := readRegularFile(filepath.Join(directory, entry.Name()), 32*1024)
		if err != nil {
			continue
		}
		file, err := ParsePublished(data)
		if err != nil {
			continue
		}
		total += len(data)
		if total > 192*1024 {
			break
		}
		themes = append(themes, ResolvePublished(identity, file))
	}
	return themes
}

func readRegularFile(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("theme file must be a bounded regular file")
	}
	file, err := openRegularFile(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !openedInfo.Mode().IsRegular() || openedInfo.Size() > limit {
		return nil, errors.New("opened theme file is not a bounded regular file")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("theme file exceeds limit")
	}
	return data, nil
}

func (store Store) lock(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	if err := os.MkdirAll(store.Directory, 0700); err != nil {
		cancel()
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(store.Directory, ".theme-write-lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		cancel()
		return nil, err
	}
	for {
		isLocked, err := tryLockFile(file)
		if err != nil {
			file.Close()
			cancel()
			return nil, err
		}
		if isLocked {
			cancel()
			return func() { unlockFile(file); file.Close() }, nil
		}
		select {
		case <-ctx.Done():
			file.Close()
			cancel()
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}
