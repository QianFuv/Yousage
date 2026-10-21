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
	"yousage/internal/service"
	datasync "yousage/internal/sync"
)

// snapshotFixture uses the shared synthetic business fixture, never private runtime data.
// Its strict decoder catches fixture/schema drift before mobile validation is exercised.
func snapshotFixture(test *testing.T, label string) string {
	test.Helper()
	source, err := os.ReadFile("../sync/snapshot_test.go")
	if err != nil {
		test.Fatal(err)
	}
	_, fixture, found := strings.Cut(string(source), "const reportParityJSON = `")
	fixture, _, closed := strings.Cut(fixture, "`")
	if !found || !closed {
		test.Fatal("shared report parity fixture is missing")
	}
	var snapshot datasync.Snapshot
	periods := map[string]datasync.Period{}
	for _, days := range []int{1, 7, 30, 90} {
		name := strconv.Itoa(days)
		window, err := accounting.BuildWindow(time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC), days, "UTC")
		if err != nil {
			test.Fatal(err)
		}
		windowJSON, err := json.Marshal(window)
		if err != nil {
			test.Fatal(err)
		}
		start, err := time.Parse(time.DateOnly, window.SinceDay)
		if err != nil {
			test.Fatal(err)
		}
		payload := strings.NewReplacer("__DAYS__", name, "__SINCE__", window.SinceDay, "__WINDOW__", string(windowJSON), "__START_MS__", strconv.FormatInt(start.UnixMilli(), 10)).Replace(fixture)
		decoder := json.NewDecoder(strings.NewReader(payload))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&snapshot); err != nil {
			test.Fatal(err)
		}
		periods[name] = snapshot.Periods[name]
	}
	snapshot.Periods, snapshot.AppVersion = periods, label
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		test.Fatal(err)
	}
	return string(encoded)
}

// TestEmptyReportsSurviveCandidateAndCache preserves valid zero-data reports for every period.
func TestEmptyReportsSurviveCandidateAndCache(test *testing.T) {
	var snapshot datasync.Snapshot
	if err := json.Unmarshal([]byte(snapshotFixture(test, "empty")), &snapshot); err != nil {
		test.Fatal(err)
	}
	for name, period := range snapshot.Periods {
		snapshot.Periods[name] = datasync.Period{
			Usage: service.Snapshot{Window: period.Usage.Window, Environments: []service.EnvironmentState{}},
			Loss:  service.LossReport{Window: period.Loss.Window},
			Speed: service.SpeedReport{Window: period.Speed.Window},
			Receipt: service.Receipt{
				Window: period.Receipt.Window, Days: period.Receipt.Days, TimeZone: period.Receipt.TimeZone,
				SinceDay: period.Receipt.SinceDay, UntilDay: period.Receipt.UntilDay, Items: []service.ReceiptItem{},
			},
		}
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		test.Fatal(err)
	}
	if problem := snapshotProblem(encoded); problem != "" {
		test.Fatalf("valid empty reports rejected: %s", problem)
	}
	viewer, relay := newSnapshotRelay(test)
	payload := string(encoded)
	relay.set(payload, `"empty"`)
	candidate := loadCandidate(test, viewer)
	if candidate.Payload != payload || candidate.ETag != `"empty"` {
		test.Fatal("empty reports changed during candidate loading")
	}
	if err := viewer.CommitSnapshot(candidate.CandidateId); err != nil {
		test.Fatal(err)
	}
	acceptedSnapshot(test, viewer, payload, `"empty"`)
}

// TestBusinessFieldsSurviveCandidateAndCache retains all supplied rows and aggregate totals.
// The desktop already caps Loss events; mobile must not apply another projection or cap.
func TestBusinessFieldsSurviveCandidateAndCache(test *testing.T) {
	viewer, relay := newSnapshotRelay(test)
	accepted := snapshotFixture(test, "A")
	acceptedSnapshot(test, viewer, accepted, `"A"`)
	var snapshot datasync.Snapshot
	if err := json.Unmarshal([]byte(snapshotFixture(test, "C")), &snapshot); err != nil {
		test.Fatal(err)
	}
	for name, period := range snapshot.Periods {
		first := period.Loss.Events[0]
		period.Loss.Events = nil
		for index := range service.LossEventLimit {
			event := first
			event.AtMs -= int64(index) * 1000
			period.Loss.Events = append(period.Loss.Events, event)
		}
		period.Loss.CacheExpiry.Events = service.LossEventLimit + period.Receipt.Days
		period.Receipt.Items[0].Model += "/recovered-C"
		snapshot.Periods[name] = period
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		test.Fatal(err)
	}
	recovered := string(encoded)
	bad := changedSnapshot(test, recovered, func(value map[string]any) {
		value["periods"].(map[string]any)["7"].(map[string]any)["speed"] = nil
	})
	relay.set(bad, `"B"`)
	if result, err := viewer.LoadSnapshot(`"A"`); err != nil || result.Problem != "data" || result.Payload != "" {
		test.Fatal("bad B was exposed to the viewer", result.Status, err)
	}
	acceptedSnapshot(test, viewer, accepted, `"A"`)
	relay.set(recovered, `"C"`)
	candidate := loadCandidate(test, viewer)
	if candidate.Payload != recovered || candidate.ETag != `"C"` {
		test.Fatal("validation/candidate projection changed business fields")
	}
	acceptedSnapshot(test, viewer, accepted, `"A"`)
	if err := viewer.CommitSnapshot(candidate.CandidateId); err != nil {
		test.Fatal(err)
	}
	acceptedSnapshot(test, viewer, recovered, `"C"`)
	stored, err := os.ReadFile(filepath.Join(viewer.directory, snapshotFile))
	if err != nil {
		test.Fatal(err)
	}
	if bytes.Contains(stored, []byte("recovered-C")) || bytes.Contains(stored, []byte("fixture@example.invalid")) {
		test.Fatal("business fields were stored in plaintext")
	}
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
