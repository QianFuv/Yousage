package quota

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

func validObject(data json.RawMessage, fields map[string]string) bool {
	var object map[string]json.RawMessage
	if json.Unmarshal(data, &object) != nil || object == nil {
		return false
	}
	for key, kind := range fields {
		value, exists := object[key]
		if !exists {
			continue
		}
		value = bytes.TrimSpace(value)
		if bytes.Equal(value, []byte("null")) {
			return false
		}
		var decoded any
		if json.Unmarshal(value, &decoded) != nil {
			return false
		}
		switch kind {
		case "number":
			if numeric(decoded) == nil {
				return false
			}
		case "string":
			if _, ok := decoded.(string); !ok {
				return false
			}
		case "numberOrString":
			if _, ok := decoded.(string); !ok && numeric(decoded) == nil {
				return false
			}
		case "object":
			if _, ok := decoded.(map[string]any); !ok {
				return false
			}
		}
	}
	return true
}

func MapCodex(data json.RawMessage, checkedAt string) (Limits, error) {
	if !validObject(data, map[string]string{"rateLimits": "object"}) {
		return Limits{}, errors.New("invalid Codex limits response")
	}
	var response struct {
		Snapshot json.RawMessage            `json:"rateLimits"`
		ById     map[string]json.RawMessage `json:"rateLimitsByLimitId"`
		Credits  *struct {
			AvailableCount int `json:"availableCount"`
			Credits        []struct {
				Status    string   `json:"status"`
				ExpiresAt *float64 `json:"expiresAt"`
			} `json:"credits"`
		} `json:"rateLimitResetCredits"`
	}
	if json.Unmarshal(data, &response) != nil || response.Snapshot == nil {
		return Limits{}, errors.New("invalid Codex limits response")
	}
	snapshot := response.Snapshot
	if main := response.ById["codex"]; main != nil && string(main) != "null" {
		snapshot = main
	}
	if !validObject(snapshot, nil) {
		return Limits{}, errors.New("invalid Codex limit snapshot")
	}
	var value struct {
		Id      string `json:"limitId"`
		Plan    string `json:"planType"`
		Primary *struct {
			Used     *float64 `json:"usedPercent"`
			Reset    *float64 `json:"resetsAt"`
			Duration *int     `json:"windowDurationMins"`
		} `json:"primary"`
		Secondary *struct {
			Used     *float64 `json:"usedPercent"`
			Reset    *float64 `json:"resetsAt"`
			Duration *int     `json:"windowDurationMins"`
		} `json:"secondary"`
	}
	if json.Unmarshal(snapshot, &value) != nil {
		return Limits{}, errors.New("invalid Codex limit snapshot")
	}
	windows := []Window{}
	if value.Id == "" || value.Id == "codex" {
		for index, window := range []*struct {
			Used     *float64 `json:"usedPercent"`
			Reset    *float64 `json:"resetsAt"`
			Duration *int     `json:"windowDurationMins"`
		}{value.Primary, value.Secondary} {
			if window == nil {
				continue
			}
			if window.Used == nil {
				return Limits{}, errors.New("invalid Codex used percentage")
			}
			id, minutes := "primary", 300
			if value.Plan == "free" || value.Plan == "go" {
				minutes = 30 * 24 * 60
			}
			if index == 1 {
				id, minutes = "secondary", 7*24*60
			}
			if window.Duration != nil {
				minutes = *window.Duration
			}
			if minutes < 0 {
				return Limits{}, errors.New("invalid Codex window duration")
			}
			kind := kindForDuration(minutes)
			windows = append(windows, Window{Id: id, Kind: kind, Label: labelForKind(kind), UsedPercent: percent(*window.Used), WindowDurationMins: duration(minutes), ResetsAt: epoch(window.Reset, true)})
		}
	}
	result := makeLimits(checkedAt, windows)
	if response.Credits != nil {
		credits := &ResetCredits{AvailableCount: max(0, response.Credits.AvailableCount)}
		var earliest *float64
		for _, credit := range response.Credits.Credits {
			if credit.Status == "available" && credit.ExpiresAt != nil && (earliest == nil || *credit.ExpiresAt < *earliest) {
				earliest = credit.ExpiresAt
			}
		}
		credits.NextExpiresAt = epoch(earliest, true)
		result.ResetCredits = credits
	}
	return result, nil
}

var scopedPattern = regexp.MustCompile(`[^a-z0-9]+`)

func scopedWindow(name string, used float64, reset string) Window {
	return Window{Id: "seven_day_" + scopedPattern.ReplaceAllString(strings.ToLower(name), "_"), Kind: "weekly", Label: "Weekly · " + name, UsedPercent: percent(used), ResetsAt: iso(reset), WindowDurationMins: duration(7 * 24 * 60)}
}
func MapClaude(data json.RawMessage, checkedAt string) (Limits, string, error) {
	var response struct {
		Available *bool                      `json:"rate_limits_available"`
		Rates     map[string]json.RawMessage `json:"rate_limits"`
	}
	if json.Unmarshal(data, &response) != nil || response.Available == nil {
		return Limits{}, "", errors.New("invalid Claude usage response")
	}
	if !*response.Available || response.Rates == nil {
		return unavailable(checkedAt, "unsupported", ""), "", nil
	}
	windows := []Window{}
	for _, id := range []string{"five_hour", "seven_day"} {
		value := numericObject(response.Rates[id])
		used := numeric(value["utilization"])
		if used == nil {
			continue
		}
		kind, minutes := "session", 300
		if id == "seven_day" {
			kind, minutes = "weekly", 7*24*60
		}
		windows = append(windows, Window{Id: id, Kind: kind, Label: labelForKind(kind), UsedPercent: percent(*used), ResetsAt: iso(text(value["resets_at"])), WindowDurationMins: duration(minutes)})
	}
	var scoped []map[string]any
	_ = json.Unmarshal(response.Rates["model_scoped"], &scoped)
	name := ""
	for _, entry := range scoped {
		display, ok := entry["display_name"].(string)
		used := numeric(entry["utilization"])
		if !ok || used == nil {
			continue
		}
		windows = append(windows, scopedWindow(display, *used, text(entry["resets_at"])))
		if name == "" {
			name = display
		}
	}
	return makeLimits(checkedAt, windows), name, nil
}
func ClaudeUpdate(data json.RawMessage, scopedName string) []Window {
	value := numericObject(data)
	kind := text(value["rateLimitType"])
	used := numeric(value["utilization"])
	if used == nil {
		return nil
	}
	reset := epoch(numeric(value["resetsAt"]), true)
	if kind == "five_hour" || kind == "seven_day" {
		label, minutes := "session", 300
		if kind == "seven_day" {
			label, minutes = "weekly", 7*24*60
		}
		return []Window{{Id: kind, Kind: label, Label: labelForKind(label), UsedPercent: percent(*used * 100), ResetsAt: reset, WindowDurationMins: duration(minutes)}}
	}
	if kind == "seven_day_overage_included" && scopedName != "" {
		window := scopedWindow(scopedName, *used*100, reset)
		return []Window{window}
	}
	return nil
}
