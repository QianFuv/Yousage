package mobile

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"yousage/internal/accounting"
	"yousage/internal/preferences"
	"yousage/internal/service"
	datasync "yousage/internal/sync"
	"yousage/internal/theme"
)

// snapshotFixture encodes an empty, complete desktop report using only synthetic metadata.
func snapshotFixture(test *testing.T, label string) string {
	test.Helper()
	now := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	snapshot := datasync.Snapshot{
		FormatVersion: 1, GeneratedAt: now.Format(time.RFC3339), AppVersion: label, TimeZone: "UTC",
		Periods: map[string]datasync.Period{}, Display: preferences.SavedDisplay{Value: preferences.Defaults()},
		Appearance: theme.LibrarySnapshot{Value: theme.LibraryValue{Preferences: theme.AppearancePreferences{
			Mode: "system", BaseThemeId: "system", Typography: theme.Typography{InterfaceSize: 16, CodeSize: 14},
			Contrast: 100, GlassOpacity: 80, AppliedGenerations: map[string]string{},
		}}}, Bootstrap: map[string]any{"isSystemDark": false, "systemLocale": "en"},
	}
	for _, days := range []int{1, 7, 30, 90} {
		window, err := accounting.BuildWindow(now, days, "UTC")
		if err != nil {
			test.Fatal(err)
		}
		snapshot.Periods[strconv.Itoa(days)] = datasync.Period{
			Usage: service.Snapshot{Window: window, Environments: []service.EnvironmentState{}},
			Loss:  service.LossReport{Window: window}, Speed: service.SpeedReport{Window: window},
			Receipt: service.Receipt{Window: window, Days: days, TimeZone: "UTC", SinceDay: window.SinceDay, UntilDay: window.UntilDay, Items: []service.ReceiptItem{}},
		}
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		test.Fatal(err)
	}
	return string(encoded)
}

// changedSnapshot modifies one JSON field without weakening the source-schema fixture.
func changedSnapshot(test *testing.T, payload string, change func(map[string]any)) string {
	test.Helper()
	var value map[string]any
	if err := json.Unmarshal([]byte(payload), &value); err != nil {
		test.Fatal(err)
	}
	change(value)
	encoded, err := json.Marshal(value)
	if err != nil {
		test.Fatal(err)
	}
	return string(encoded)
}

// TestInvalidSnapshotShapePreservesAccepted also runs unchanged on the disposable baseline.
// Authentication alone must never replace an accepted cache or a working pairing.
func TestInvalidSnapshotShapePreservesAccepted(test *testing.T) {
	for _, scenario := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"unsupported format", func(value map[string]any) { value["formatVersion"] = 99 }},
		{"null period", func(value map[string]any) { value["periods"].(map[string]any)["30"] = nil }},
		{"missing report", func(value map[string]any) { delete(value["periods"].(map[string]any)["7"].(map[string]any), "receipt") }},
		{"null display value", func(value map[string]any) { value["display"].(map[string]any)["value"] = nil }},
		{"missing appearance", func(value map[string]any) { delete(value, "appearance") }},
		{"invalid metadata", func(value map[string]any) { value["generatedAt"] = false }},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			key, err := datasync.NewKey()
			if err != nil {
				test.Fatal(err)
			}
			identifier := strings.Repeat("A", 22)
			accepted := snapshotFixture(test, "accepted-A")
			bad := changedSnapshot(test, accepted, scenario.change)
			var responseMutex sync.Mutex
			payload, etag := accepted, `"A"`
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				responseMutex.Lock()
				defer responseMutex.Unlock()
				envelope, sealError := datasync.Seal([]byte(payload), key, "space:"+identifier)
				if sealError != nil {
					test.Error(sealError)
					writer.WriteHeader(500)
					return
				}
				writer.Header().Set("ETag", etag)
				writer.Write(envelope)
			}))
			defer server.Close()
			viewer := newService(test.TempDir(), nil)
			link := server.URL + "/v/" + identifier + "#" + key
			if _, err := viewer.Pair(link); err != nil {
				test.Fatal(err)
			}
			oldPairing, err := os.ReadFile(filepath.Join(viewer.directory, pairingFile))
			if err != nil {
				test.Fatal(err)
			}
			oldCache, err := os.ReadFile(filepath.Join(viewer.directory, snapshotFile))
			if err != nil {
				test.Fatal(err)
			}
			responseMutex.Lock()
			payload, etag = bad, `"B"`
			responseMutex.Unlock()
			for range 2 {
				result, err := viewer.LoadSnapshot(`"A"`)
				if err != nil || result.Status != "problem" || (result.Problem != "data" && result.Problem != "version") || result.Payload != "" {
					test.Errorf("authenticated unusable snapshot was accepted: status=%s problem=%s error=%v", result.Status, result.Problem, err)
				}
			}
			if _, err := viewer.Pair(link); err == nil {
				test.Error("incompatible replacement pairing was accepted")
			}
			pairing, _ := os.ReadFile(filepath.Join(viewer.directory, pairingFile))
			cache, _ := os.ReadFile(filepath.Join(viewer.directory, snapshotFile))
			if !bytes.Equal(pairing, oldPairing) || !bytes.Equal(cache, oldCache) {
				test.Error("rejected B changed accepted pairing or encrypted cache")
			}
			reopened, err := newService(viewer.directory, nil).CachedSnapshot()
			if err != nil || reopened.Status != "ok" || reopened.Payload != accepted || reopened.ETag != `"A"` {
				test.Errorf("accepted A did not survive restart: status=%s etag=%s error=%v", reopened.Status, reopened.ETag, err)
			}
		})
	}
}
