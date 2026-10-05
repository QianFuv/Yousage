package remote

import (
	"encoding/json"
	"strings"
	"testing"
)

func configEvent(capabilities string) json.RawMessage {
	return json.RawMessage(`{"version":1,"type":"snapshot","config":{"environment":{"environmentId":"fixture","orchestrationProtocolVersion":2,"capabilities":` + capabilities + `},"providers":[],"settings":{"usagePriceOverrides":{},"usageModelAliases":{}}}}`)
}

func TestProjectionReplayAndCapabilityReplacement(test *testing.T) {
	projection := Projection{}
	publication := json.RawMessage(`{"version":1,"type":"usageLimitSourcesUpdated","payload":{"sources":[]}}`)
	if err := projection.Apply(publication, "fixture"); err != nil || projection.IsReady {
		test.Fatal("delta established a connection", err)
	}
	if err := projection.Apply(configEvent(`{"environmentThemes":true,"usageLimitSources":true,"shell":{"terminal":true}}`), "fixture"); err != nil || !projection.IsLive {
		test.Fatal(err)
	}
	cached := projection.Cached()
	if cached.IsLive || len(cached.Config.Themes) != 0 || len(cached.Config.Sources) != 0 {
		test.Fatal("replay-only data survived caching")
	}
	if err := projection.Apply(configEvent(`{"environmentThemes":"yes"}`), "fixture"); err == nil {
		test.Fatal("malformed known capability accepted")
	}
	if !projection.IsLive || !projection.Config.Environment.Capabilities["environmentThemes"] {
		test.Fatal("invalid event changed last-good projection")
	}
	if err := projection.Apply(configEvent(`{"environmentThemes":false,"usageLimitSources":false}`), "fixture"); err != nil || len(projection.Config.Themes) != 0 {
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
