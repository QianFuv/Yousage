package mobile

import "testing"

// TestSnapshotContractRejectsUnsafeValues covers roots, metadata, and nested binding inputs.
func TestSnapshotContractRejectsUnsafeValues(test *testing.T) {
	fixture := snapshotFixture(test, "test")
	for _, scenario := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"missing version", func(value map[string]any) { delete(value, "formatVersion") }},
		{"null version", func(value map[string]any) { value["formatVersion"] = nil }},
		{"missing metadata", func(value map[string]any) { delete(value, "appVersion") }},
		{"null metadata", func(value map[string]any) { value["timeZone"] = nil }},
		{"empty metadata", func(value map[string]any) { value["appVersion"] = "" }},
		{"invalid timestamp", func(value map[string]any) { value["generatedAt"] = "yesterday" }},
		{"null periods", func(value map[string]any) { value["periods"] = nil }},
		{"array periods", func(value map[string]any) { value["periods"] = []any{} }},
		{"missing period", func(value map[string]any) { delete(value["periods"].(map[string]any), "90") }},
		{"null report", func(value map[string]any) { value["periods"].(map[string]any)["30"].(map[string]any)["usage"] = nil }},
		{"array report", func(value map[string]any) {
			value["periods"].(map[string]any)["30"].(map[string]any)["receipt"] = []any{}
		}},
		{"array display", func(value map[string]any) { value["display"] = []any{} }},
		{"missing display value", func(value map[string]any) { delete(value["display"].(map[string]any), "value") }},
		{"null appearance", func(value map[string]any) { value["appearance"] = nil }},
		{"null appearance value", func(value map[string]any) { value["appearance"].(map[string]any)["value"] = nil }},
		{"null bootstrap", func(value map[string]any) { value["bootstrap"] = nil }},
		{"invalid bootstrap", func(value map[string]any) { value["bootstrap"] = map[string]any{"isSystemDark": "false"} }},
		{"null scalar", func(value map[string]any) { value["display"].(map[string]any)["revision"] = nil }},
		{"wrong scalar", func(value map[string]any) {
			value["display"].(map[string]any)["value"].(map[string]any)["zoom"] = "large"
		}},
		{"null window", func(value map[string]any) {
			value["periods"].(map[string]any)["1"].(map[string]any)["usage"].(map[string]any)["window"] = nil
		}},
		{"null array element", func(value map[string]any) {
			value["periods"].(map[string]any)["1"].(map[string]any)["usage"].(map[string]any)["environments"] = []any{nil}
		}},
		{"wrong collection", func(value map[string]any) {
			value["periods"].(map[string]any)["1"].(map[string]any)["loss"].(map[string]any)["days"] = map[string]any{}
		}},
		{"null summary object", func(value map[string]any) {
			value["periods"].(map[string]any)["1"].(map[string]any)["usage"].(map[string]any)["environments"] = []any{map[string]any{"summary": map[string]any{"pricing": nil}}}
		}},
		{"invalid nested number", func(value map[string]any) {
			value["periods"].(map[string]any)["1"].(map[string]any)["speed"].(map[string]any)["models"] = []any{map[string]any{"requests": "many"}}
		}},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			if problem := snapshotProblem([]byte(changedSnapshot(test, fixture, scenario.change))); problem != "data" {
				test.Fatalf("invalid data accepted or misclassified: %s", problem)
			}
		})
	}
	for _, payload := range []string{"null", "[]", "true", "broken"} {
		if snapshotProblem([]byte(payload)) != "data" {
			test.Fatalf("nonobject snapshot accepted: %s", payload)
		}
	}
}

// TestSnapshotContractCompatibility preserves binding defaults and open provider identifiers.
func TestSnapshotContractCompatibility(test *testing.T) {
	fixture := snapshotFixture(test, "test")
	for _, scenario := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"current empty reports", func(value map[string]any) {}},
		{"older omitted scalars", func(value map[string]any) {
			value["display"].(map[string]any)["value"] = map[string]any{}
			value["appearance"] = map[string]any{}
			for _, period := range value["periods"].(map[string]any) {
				receipt := period.(map[string]any)["receipt"].(map[string]any)
				delete(receipt, "unknownPaymentUsd")
				receipt["items"] = []any{map[string]any{"provider": "codex", "model": "synthetic", "tokens": 0}}
			}
		}},
		{"nullable environment and collections", func(value map[string]any) {
			for _, period := range value["periods"].(map[string]any) {
				period.(map[string]any)["usage"].(map[string]any)["environments"] = []any{map[string]any{"environmentId": "synthetic", "summary": nil, "serverConfig": nil, "cycleValues": nil}}
				period.(map[string]any)["receipt"].(map[string]any)["items"] = nil
			}
		}},
		{"future provider and fields", func(value map[string]any) {
			value["futureField"] = map[string]any{"arbitrary": []any{nil, true}}
			for _, period := range value["periods"].(map[string]any) {
				period.(map[string]any)["usage"].(map[string]any)["environments"] = []any{map[string]any{
					"summary":      map[string]any{"buckets": []any{map[string]any{"provider": "future-provider", "model": "synthetic", "totals": map[string]any{}}}, "sources": nil},
					"serverConfig": map[string]any{"providers": []any{map[string]any{"driver": "future-driver", "auth": map[string]any{}}}},
				}}
				period.(map[string]any)["loss"].(map[string]any)["providers"] = []any{map[string]any{"provider": "future-provider"}}
				period.(map[string]any)["speed"].(map[string]any)["models"] = []any{map[string]any{"provider": "future-provider"}}
				period.(map[string]any)["receipt"].(map[string]any)["items"] = []any{map[string]any{"provider": "future-provider"}}
			}
		}},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			payload := changedSnapshot(test, fixture, scenario.change)
			if problem := snapshotProblem([]byte(payload)); problem != "" {
				test.Fatalf("compatible snapshot rejected: %s", problem)
			}
		})
	}
}
