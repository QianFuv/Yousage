package remote

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func configEvent(capabilities string) json.RawMessage {
	return json.RawMessage(`{"version":1,"type":"snapshot","config":{"environment":{"environmentId":"fixture","orchestrationProtocolVersion":2,"capabilities":` + capabilities + `},"providers":[],"settings":{"usagePriceOverrides":{},"usageModelAliases":{}}}}`)
}

// TestProjectionReplayAndCapabilityReplacement keeps replay data live only while its capabilities permit it.
func TestProjectionReplayAndCapabilityReplacement(test *testing.T) {
	projection := Projection{}
	publication := json.RawMessage(`{"version":1,"type":"usageLimitSourcesUpdated","payload":{"sources":[]}}`)
	if err := projection.Apply(publication, "fixture"); err != nil || projection.IsReady {
		test.Fatal("delta established a connection", err)
	}
	if err := projection.Apply(configEvent(`{"environmentThemes":true,"usageLimitSources":true,"shell":{"terminal":true}}`), "fixture"); err != nil || !projection.IsLive {
		test.Fatal(err)
	}
	for _, event := range []string{
		`{"version":1,"type":"environmentThemesUpdated","payload":{"themes":[{"id":"fixture-theme","name":"Fixture","appearance":"dark","colors":{"canvas":"#123456"}}]}}`,
		`{"version":1,"type":"usageLimitSourcesUpdated","payload":{"sources":[{"id":"fixture-hub","kind":"cliproxy","label":"Fixture hub","checkedAt":"2026-10-18T00:00:00Z","accounts":[]}]}}`,
	} {
		if err := projection.Apply(json.RawMessage(event), "fixture"); err != nil {
			test.Fatal(err)
		}
	}
	if len(projection.Config.Themes) != 1 || len(projection.Config.Sources) != 1 {
		test.Fatal("replay fixtures did not populate the live projection")
	}
	live := projection
	cached := projection.Cached()
	if cached.IsLive || len(cached.Config.Themes) != 0 || len(cached.Config.Sources) != 0 {
		test.Fatal("replay-only data survived caching")
	}
	if !reflect.DeepEqual(projection, live) {
		test.Fatal("caching changed the live projection")
	}
	if err := projection.Apply(configEvent(`{"environmentThemes":true,"usageLimitSources":true,"shell":{"terminal":true}}`), "fixture"); err != nil || !reflect.DeepEqual(projection, live) {
		test.Fatal("enabled capabilities lost live replay data", err)
	}
	if err := projection.Apply(configEvent(`{"environmentThemes":"yes"}`), "fixture"); err == nil {
		test.Fatal("malformed known capability accepted")
	}
	if !reflect.DeepEqual(projection, live) {
		test.Fatal("invalid event changed last-good projection")
	}
	if err := projection.Apply(configEvent(`{"environmentThemes":false,"usageLimitSources":false}`), "fixture"); err != nil || len(projection.Config.Themes) != 0 || len(projection.Config.Sources) != 0 {
		test.Fatal("downgrade did not replace replay fields", err)
	}
}

func TestSettingsRejectNullPricesAndAliases(test *testing.T) {
	for _, raw := range []string{`{"usagePriceOverrides":{"model":{"inputCostPerMillionTokens":null,"outputCostPerMillionTokens":1}}}`, `{"usageModelAliases":null}`, `{"usagePriceOverrides":{" model":{"inputCostPerMillionTokens":0,"outputCostPerMillionTokens":1}}}`} {
		if _, err := decodeSettings(json.RawMessage(raw)); err == nil {
			test.Fatal("invalid settings accepted", raw)
		}
	}
	if _, err := decodeSettings(json.RawMessage(`{"usagePriceOverrides":{"model":{"inputCostPerMillionTokens":0,"outputCostPerMillionTokens":1e-8}},"usageModelAliases":{}}`)); err != nil {
		test.Fatal("valid zero/scientific price rejected", err)
	}
	if publishedIdentity.MatchString(strings.Repeat("a", 49)) {
		test.Fatal("published identity exceeds pinned 48-character limit")
	}
}
