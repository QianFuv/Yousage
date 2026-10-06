package theme

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
	"yousage/internal/storage"
)

type failingRepository struct {
	*storage.Store
	shouldFail bool
}

func TestDefaultMotionDoesNotAnimatePanels(test *testing.T) {
	if defaultLibraryValue().Preferences.MotionMs != 0 {
		test.Fatal("fresh installation should not animate panels")
	}
}

func (repository *failingRepository) WriteSetting(ctx context.Context, key string, revision int64, value any) (int64, error) {
	if repository.shouldFail {
		return revision, errors.New("fixture write failed")
	}
	return repository.Store.WriteSetting(ctx, key, revision, value)
}
func testLibrary(test *testing.T) (*Library, *failingRepository, string) {
	test.Helper()
	directory := test.TempDir()
	database, err := storage.Open(filepath.Join(directory, "state.sqlite"))
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { database.Close() })
	repository := &failingRepository{Store: database}
	return &Library{Repository: repository, PublishedStore: Store{Directory: directory}}, repository, directory
}

func TestPrimaryPublishedAdoptsOncePerEnvironmentAndWaitsForResolvableTheme(test *testing.T) {
	library, _, _ := testLibrary(test)
	ctx := context.Background()
	first, err := library.SetPrimaryPublished(ctx, "remote-a", &Snapshot{DefaultTheme: "late", Generation: "one", Themes: []Definition{}})
	if err != nil || first.Value.Preferences.AppliedGenerations["remote-a"] != "" {
		test.Fatal("unresolved publication consumed adoption", err)
	}
	custom := Builtins()[0]
	custom.Id = "late"
	adopted, err := library.SetPrimaryPublished(ctx, "remote-a", &Snapshot{DefaultTheme: "late", Generation: "one", Themes: []Definition{custom}})
	if err != nil || adopted.Value.Preferences.BaseThemeId != "late" {
		test.Fatal("late publication not adopted", err)
	}
	adopted.Value.Preferences.BaseThemeId = "dark"
	manual, err := library.Save(ctx, adopted.Revision, adopted.Value)
	if err != nil {
		test.Fatal(err)
	}
	unchanged, err := library.SetPrimaryPublished(ctx, "remote-a", &Snapshot{DefaultTheme: "late", Generation: "one", Themes: []Definition{custom}})
	if err != nil || unchanged.Value.Preferences.BaseThemeId != manual.Value.Preferences.BaseThemeId {
		test.Fatal("same generation replaced manual choice", err)
	}
	second, err := library.SetPrimaryPublished(ctx, "remote-b", &Snapshot{DefaultTheme: "late", Generation: "one", Themes: []Definition{custom}})
	if err != nil || second.Value.Preferences.BaseThemeId != "late" || second.Value.Preferences.AppliedGenerations["remote-b"] != "late@one" {
		test.Fatal("environment generations share authority", err)
	}
}

func TestLibraryAtomicSaveConflictAndReopen(test *testing.T) {
	library, repository, directory := testLibrary(test)
	ctx := context.Background()
	first, err := library.Read(ctx)
	if err != nil {
		test.Fatal(err)
	}
	custom := Builtins()[0]
	custom.Id = "fixture-custom"
	first.Value.CustomThemes = []Definition{custom}
	first.Value.Preferences.LightOwner = "missing-published"
	first.Value.Preferences.BaseThemeId = custom.Id
	saved, err := library.Save(ctx, first.Revision, first.Value)
	if err != nil || saved.Revision != 1 {
		test.Fatal(saved, err)
	}
	stale := first.Value
	stale.Preferences.Mode = "dark"
	if _, err = library.Save(ctx, 0, stale); err == nil {
		test.Fatal("stale writer replaced a newer library")
	}
	unchanged, _ := library.Read(ctx)
	if unchanged.Value.Preferences.Mode != "system" || unchanged.Value.Preferences.LightOwner != "missing-published" {
		test.Fatal("failed save changed persisted state")
	}
	repository.shouldFail = true
	failed := unchanged.Value
	failed.CustomThemes = []Definition{}
	failed.Preferences.BaseThemeId = "dark"
	if _, err = library.Save(ctx, unchanged.Revision, failed); err == nil {
		test.Fatal("failed repository acknowledged a save")
	}
	repository.shouldFail = false
	if err = repository.Store.Close(); err != nil {
		test.Fatal(err)
	}
	database, err := storage.Open(filepath.Join(directory, "state.sqlite"))
	if err != nil {
		test.Fatal(err)
	}
	defer database.Close()
	reopened := Library{Repository: database, PublishedStore: Store{Directory: directory}}
	actual, err := reopened.Read(ctx)
	if err != nil || len(actual.Value.CustomThemes) != 1 || actual.Value.Preferences.BaseThemeId != custom.Id {
		test.Fatal("palette and selection did not persist together", actual, err)
	}
}

func TestLibraryGenerationWaitsForResolutionAndPreservesManualChoice(test *testing.T) {
	library, repository, directory := testLibrary(test)
	ctx := context.Background()
	writeTestFile(test, filepath.Join(directory, "settings.json"), []byte(`{"defaultTheme":"late-theme","defaultThemeSetAt":"generation-1"}`))
	missing, err := library.Read(ctx)
	if err != nil || missing.Value.Preferences.AppliedGenerations["local"] != "" {
		test.Fatal("unresolved generation consumed", err)
	}
	writeTestFile(test, filepath.Join(directory, "themes", "late-theme.json"), []byte(`{"name":"Late","appearance":"dark","canvas":"#639","accent":"#fc0"}`))
	repository.shouldFail = true
	if _, err = library.Read(ctx); err == nil {
		test.Fatal("adoption acknowledged failed persistence")
	}
	repository.shouldFail = false
	adopted, err := library.Read(ctx)
	if err != nil || adopted.Value.Preferences.BaseThemeId != "late-theme" || adopted.Value.Preferences.Mode != "dark" {
		test.Fatal("late palette was not adopted", err)
	}
	manual := adopted.Value
	manual.Preferences.BaseThemeId = "ocean"
	manual.Preferences.LightOwner = "unknown-owner"
	saved, err := library.Save(ctx, adopted.Revision, manual)
	if err != nil {
		test.Fatal(err)
	}
	repeated, _ := library.Read(ctx)
	if repeated.Revision != saved.Revision || repeated.Value.Preferences.BaseThemeId != "ocean" {
		test.Fatal("same generation replayed over manual choice")
	}
	if err = os.Remove(filepath.Join(directory, "themes", "late-theme.json")); err != nil {
		test.Fatal(err)
	}
	removed, _ := library.Read(ctx)
	if removed.Value.Preferences.LightOwner != "unknown-owner" {
		test.Fatal("unresolved owner was pruned")
	}
	writeTestFile(test, filepath.Join(directory, "settings.json"), []byte(`{"defaultTheme":"grove"}`))
	legacy, err := library.Read(ctx)
	if err != nil || legacy.Value.Preferences.BaseThemeId != "grove" {
		test.Fatal("legacy once-per-ID default not adopted", err)
	}
	legacy.Value.Preferences.BaseThemeId = "ocean"
	_, err = library.Save(ctx, legacy.Revision, legacy.Value)
	if err != nil {
		test.Fatal(err)
	}
	legacyAgain, _ := library.Read(ctx)
	if legacyAgain.Value.Preferences.BaseThemeId != "ocean" {
		test.Fatal("legacy default replayed")
	}
}

func TestLibraryProtectedIdentityAndCustomPublishedPrecedence(test *testing.T) {
	library, _, directory := testLibrary(test)
	ctx := context.Background()
	initial, _ := library.Read(ctx)
	for _, identity := range []string{"ocean", "system", "light", "dark"} {
		custom := Builtins()[0]
		custom.Id = identity
		value := initial.Value
		value.CustomThemes = []Definition{custom}
		if _, err := library.Save(ctx, initial.Revision, value); err == nil {
			test.Fatal("protected identity accepted", identity)
		}
	}
	custom := Builtins()[0]
	custom.Id = "t3-code"
	value := initial.Value
	value.CustomThemes = []Definition{custom}
	if _, err := library.Save(ctx, initial.Revision, value); err != nil {
		test.Fatal("portable parser's t3-code custom ID rejected", err)
	}
	writeTestFile(test, filepath.Join(directory, "themes", "same-owner.json"), []byte(`{"name":"Published","appearance":"dark","canvas":"#000","accent":"#fff"}`))
	current, _ := library.Read(ctx)
	custom.Id = "same-owner"
	custom.Appearance = "light"
	custom.Variants = nil
	current.Value.CustomThemes = append(current.Value.CustomThemes, custom)
	_, err := library.Save(ctx, current.Revision, current.Value)
	if err != nil {
		test.Fatal(err)
	}
	writeTestFile(test, filepath.Join(directory, "settings.json"), []byte(`{"defaultTheme":"same-owner","defaultThemeSetAt":"next"}`))
	selected, err := library.Read(ctx)
	if err != nil || selected.Value.Preferences.Mode != "light" {
		test.Fatal("published copy overrode custom owner", err)
	}
}

func TestLibraryWatcherSettlesChangesAndStops(test *testing.T) {
	library, _, directory := testLibrary(test)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	snapshots := make(chan LibrarySnapshot, 8)
	done := make(chan error, 1)
	go func() { done <- library.Watch(ctx, func(snapshot LibrarySnapshot) { snapshots <- snapshot }) }()
	await := func(predicate func(LibrarySnapshot) bool) LibrarySnapshot {
		test.Helper()
		timer := time.NewTimer(3 * time.Second)
		defer timer.Stop()
		for {
			select {
			case snapshot := <-snapshots:
				if predicate(snapshot) {
					return snapshot
				}
			case <-timer.C:
				test.Fatal("watcher did not observe complete changed state")
				return LibrarySnapshot{}
			}
		}
	}
	await(func(value LibrarySnapshot) bool { return value.Revision == 0 })
	writeTestFile(test, filepath.Join(directory, "settings.json"), []byte(`{"defaultTheme":"watch-theme","defaultThemeSetAt":"watch-generation"}`))
	writeTestFile(test, filepath.Join(directory, "themes", "watch-theme.json"), []byte(`{"name":"Watch","appearance":"dark","canvas":"#111","accent":"#aaf"}`))
	await(func(value LibrarySnapshot) bool { return value.Value.Preferences.BaseThemeId == "watch-theme" })
	writeTestFile(test, filepath.Join(directory, "settings.json"), []byte(`not-json`))
	last := await(func(value LibrarySnapshot) bool { return value.Error != "" })
	if len(last.Published.Themes) != 1 {
		test.Fatal("malformed external settings removed last-good published palette")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			test.Fatal(err)
		}
	case <-time.After(time.Second):
		test.Fatal("owned watcher failed to stop")
	}
}

func TestCssCanonicalVectors(test *testing.T) {
	data, err := os.ReadFile("testdata/css-colors.json")
	if err != nil {
		test.Fatal(err)
	}
	var vectors []struct {
		Input    string  `json:"input"`
		Expected *string `json:"expected"`
	}
	if err = json.Unmarshal(data, &vectors); err != nil {
		test.Fatal(err)
	}
	for _, vector := range vectors {
		actual, err := CanonicalColor(vector.Input)
		if vector.Expected == nil {
			if err == nil {
				test.Fatal("invalid literal accepted", vector.Input)
			}
			continue
		}
		if err != nil || actual != *vector.Expected {
			test.Fatalf("literal %q: got %q/%v want %q", vector.Input, actual, err, *vector.Expected)
		}
	}
}
