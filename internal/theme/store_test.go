package theme

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeTestFile(test *testing.T, path string, data []byte) {
	test.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		test.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		test.Fatal(err)
	}
}

func TestAllBuiltinPairsRoundTripAndParserBoundaries(test *testing.T) {
	if len(Roles()) != 61 || len(Builtins()) != 5 {
		test.Fatal("pinned vocabulary incomplete")
	}
	for _, theme := range Builtins() {
		theme.Id = "fixture-" + theme.Id
		data, err := SerializeFile(theme)
		if err != nil {
			test.Fatal(err)
		}
		parsed, err := ParseFile(data)
		if err != nil {
			test.Fatal(err)
		}
		if len(parsed.Colors) != 61 || len(parsed.Variants["dark"]) != 61 {
			test.Fatal("paired roles lost")
		}
		for role, value := range theme.Colors {
			expected, _ := CanonicalColor(value)
			if parsed.Colors[role] != expected {
				test.Fatalf("round trip changed %s", role)
			}
		}
		for role, value := range theme.Variants["dark"] {
			expected, _ := CanonicalColor(value)
			if parsed.Variants["dark"][role] != expected {
				test.Fatalf("paired dark round trip changed %s", role)
			}
		}
	}
	for _, data := range []string{
		`{"version":1,"name":"Test","appearance":"light","colors":{"futureRole":"#fff"}}`,
		`{"version":1,"name":"Test","appearance":"light","colors":{"canvas":"#fff"},"variants":{"light":{"canvas":"#000"}}}`,
		`{"version":1,"name":"Test","appearance":"light","colors":null}`,
		`{"version":1,"name":"Test","appearance":"light","colors":{"canvas":"#fff"},"collection":null}`,
	} {
		if _, err := ParseFile([]byte(data)); err == nil {
			test.Fatal("invalid strict import accepted")
		}
	}
	file, err := ParsePublished([]byte(`{"name":"Future","appearance":"dark","colors":{"futureRole":"#fff","canvas":"not-a-color"}}`))
	if err != nil {
		test.Fatal("open published role rejected", err)
	}
	resolved := ResolvePublished("filename-owner", file)
	if len(resolved.Colors) != 61 || resolved.Id != "filename-owner" || resolved.Colors["futureRole"] != "" {
		test.Fatal("published normalization changed ownership/vocabulary")
	}
	if _, err := ParsePublished([]byte(`{"name":"Empty","appearance":"dark","variants":{"light":{"canvas":"#fff"}}}`)); err == nil {
		test.Fatal("variants-only file accepted")
	}
}

func TestSetGenerationClearAndFilenameOwnership(test *testing.T) {
	ctx := context.Background()
	store := Store{Directory: test.TempDir()}
	settingsPath := filepath.Join(store.Directory, "settings.json")
	writeTestFile(test, settingsPath, []byte(`{"unrelated":{"preserve":true},"selectedLight":"ocean"}`))
	if _, err := store.Set(ctx, "ocean", ""); err != nil {
		test.Fatal(err)
	}
	first, err := store.Show(ctx)
	if err != nil {
		test.Fatal(err)
	}
	if _, err := store.Set(ctx, "ocean", ""); err != nil {
		test.Fatal(err)
	}
	second, _ := store.Show(ctx)
	if first.Generation == second.Generation || second.DefaultTheme != "ocean" {
		test.Fatal("same-id set did not create a generation")
	}
	source := filepath.Join(test.TempDir(), "nightfall.json")
	writeTestFile(test, source, []byte(`{"id":"embedded-id-ignored","name":"Nightfall","appearance":"dark","canvas":"#172554","accent":"#f97316"}`))
	if identity, err := store.Set(ctx, source, ""); err != nil || identity != "nightfall" {
		test.Fatal("filename identity changed", identity, err)
	}
	snapshot, _ := store.Show(ctx)
	if len(snapshot.Themes) != 1 || snapshot.Themes[0].Id != "nightfall" || len(snapshot.Themes[0].Colors) != 61 {
		test.Fatal("published palette missing")
	}
	if err := store.Clear(ctx); err != nil {
		test.Fatal(err)
	}
	snapshot, _ = store.Show(ctx)
	if snapshot.Generation != "" || snapshot.DefaultTheme != "" || len(snapshot.Themes) != 1 {
		test.Fatal("clear removed published files or retained default")
	}
	settings, _ := store.readSettings()
	if string(settings["selectedLight"]) != `"ocean"` || !bytes.Contains(settings["unrelated"], []byte("true")) {
		test.Fatal("clear changed saved choices or unknown settings")
	}
}

func TestPublishRollbackAndConcurrentReplacement(test *testing.T) {
	for _, hasExternalReplacement := range []bool{false, true} {
		test.Run(map[bool]string{false: "restore-original", true: "preserve-newer"}[hasExternalReplacement], func(test *testing.T) {
			store := Store{Directory: test.TempDir()}
			destination := filepath.Join(store.Directory, "themes", "night.json")
			original := []byte(`{"name":"Original","appearance":"dark","colors":{"canvas":"#000"}}`)
			newer := []byte(`{"name":"External newer","appearance":"dark","colors":{"canvas":"#111"}}`)
			writeTestFile(test, destination, original)
			source := filepath.Join(test.TempDir(), "night.json")
			writeTestFile(test, source, []byte(`{"name":"Attempt","appearance":"dark","canvas":"#222","accent":"#fff"}`))
			store.replaceSettings = func(staged, path string) error {
				if hasExternalReplacement {
					replacement, err := stageFile(destination, newer)
					if err != nil {
						test.Fatal(err)
					}
					if err = replaceFile(replacement, destination); err != nil {
						test.Fatal(err)
					}
				}
				return errors.New("injected settings commit failure")
			}
			if _, err := store.Set(context.Background(), source, ""); err == nil {
				test.Fatal("failed settings commit accepted")
			}
			actual, err := os.ReadFile(destination)
			if err != nil {
				test.Fatal(err)
			}
			expected := original
			if hasExternalReplacement {
				expected = newer
			}
			if !bytes.Equal(actual, expected) {
				test.Fatalf("rollback clobbered ownership: %s", actual)
			}
			settings, err := store.readSettings()
			if err != nil || len(settings) != 0 {
				test.Fatal("failed transaction selected a theme")
			}
		})
	}
}

func TestMalformedSettingsFailBeforePublishingAndCommandIsolation(test *testing.T) {
	directory := test.TempDir()
	writeTestFile(test, filepath.Join(directory, "settings.json"), []byte(`{"broken":`))
	source := filepath.Join(test.TempDir(), "valid.json")
	writeTestFile(test, source, []byte(`{"name":"Valid","appearance":"light","canvas":"#fff","accent":"#123"}`))
	store := Store{Directory: directory}
	if _, err := store.Set(context.Background(), source, ""); err == nil {
		test.Fatal("malformed settings overwritten")
	}
	if _, err := os.Stat(filepath.Join(directory, "themes")); !errors.Is(err, os.ErrNotExist) {
		test.Fatal("preflight failure published a side effect")
	}
	var output, diagnostics bytes.Buffer
	cleanDirectory := test.TempDir()
	isHandled, exitCode := RunCommand(context.Background(), []string{"theme", "set", "ocean", "--state-dir", cleanDirectory}, &output, &diagnostics)
	if !isHandled || exitCode != 0 || !bytes.Contains(output.Bytes(), []byte("ocean")) {
		test.Fatal(output.String(), diagnostics.String())
	}
	output.Reset()
	_, exitCode = RunCommand(context.Background(), []string{"theme", "show", "--state-dir", cleanDirectory}, &output, &diagnostics)
	var snapshot Snapshot
	if exitCode != 0 || json.Unmarshal(output.Bytes(), &snapshot) != nil || snapshot.DefaultTheme != "ocean" {
		test.Fatal("headless command output invalid")
	}
}

func TestPublishedBoundsAndStrictAlphaRoundTrip(test *testing.T) {
	store := Store{Directory: test.TempDir()}
	for index := 0; index < 33; index++ {
		name := filepath.Join(store.Directory, "themes", fmt.Sprintf("fixture-%02d.json", index))
		writeTestFile(test, name, []byte(`{"name":"Bounded","appearance":"dark","colors":{"canvas":"#123"}}`))
	}
	if len(store.readPublished()) != 32 {
		test.Fatal("directory examination budget changed")
	}
	parsed, err := ParseFile([]byte(`{"version":1,"name":"Alpha","appearance":"dark","colors":{"canvas":"#ff006680"}}`))
	if err != nil {
		test.Fatal(err)
	}
	data, err := SerializeFile(parsed)
	if err != nil {
		test.Fatal(err)
	}
	roundTrip, err := ParseFile(data)
	if err != nil || !reflect.DeepEqual(parsed.Colors, roundTrip.Colors) {
		test.Fatal("alpha lost during portable round trip")
	}
}
