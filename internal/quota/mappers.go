package quota

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
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

func MapCursor(data json.RawMessage, checkedAt string) (Limits, error) {
	if !validObject(data, map[string]string{"billingCycleEnd": "numberOrString", "planUsage": "object"}) {
		return Limits{}, errors.New("invalid Cursor usage response")
	}
	var object map[string]json.RawMessage
	_ = json.Unmarshal(data, &object)
	if plan, exists := object["planUsage"]; exists && !validObject(plan, map[string]string{"totalPercentUsed": "number", "autoPercentUsed": "number", "apiPercentUsed": "number"}) {
		return Limits{}, errors.New("invalid Cursor usage response")
	}
	var response struct {
		Billing json.RawMessage `json:"billingCycleEnd"`
		Plan    *struct {
			Total *float64 `json:"totalPercentUsed"`
			Auto  *float64 `json:"autoPercentUsed"`
			Api   *float64 `json:"apiPercentUsed"`
		} `json:"planUsage"`
	}
	if json.Unmarshal(data, &response) != nil {
		return Limits{}, errors.New("invalid Cursor usage response")
	}
	var billing any
	_ = json.Unmarshal(response.Billing, &billing)
	reset := numeric(billing)
	if value, ok := billing.(string); ok {
		if number, err := strconv.ParseFloat(value, 64); err == nil {
			reset = &number
		}
	}
	windows := []Window{}
	if response.Plan != nil {
		for _, entry := range []struct {
			id, label string
			used      *float64
		}{{"totalPercentUsed", "Overall", response.Plan.Total}, {"autoPercentUsed", "Cursor Models", response.Plan.Auto}, {"apiPercentUsed", "Other Models", response.Plan.Api}} {
			if entry.used != nil {
				windows = append(windows, Window{Id: entry.id, Kind: "monthly", Label: entry.label, UsedPercent: percent(*entry.used), ResetsAt: epoch(reset, false)})
			}
		}
	}
	if len(windows) == 0 {
		return unavailable(checkedAt, "unsupported", ""), nil
	}
	return makeLimits(checkedAt, windows), nil
}
func MapGrok(data json.RawMessage, checkedAt string) (Limits, error) {
	if !validObject(data, map[string]string{"config": "object"}) {
		return Limits{}, errors.New("invalid Grok billing response")
	}
	var object map[string]json.RawMessage
	_ = json.Unmarshal(data, &object)
	if config, exists := object["config"]; exists {
		if !validObject(config, map[string]string{"creditUsagePercent": "number", "currentPeriod": "object"}) {
			return Limits{}, errors.New("invalid Grok billing response")
		}
		var configuration map[string]json.RawMessage
		_ = json.Unmarshal(config, &configuration)
		if period, exists := configuration["currentPeriod"]; exists && !validObject(period, map[string]string{"type": "string", "end": "string"}) {
			return Limits{}, errors.New("invalid Grok billing response")
		}
	}
	var response struct {
		Config *struct {
			Used   *float64 `json:"creditUsagePercent"`
			Period *struct {
				Type string `json:"type"`
				End  string `json:"end"`
			} `json:"currentPeriod"`
		} `json:"config"`
	}
	if json.Unmarshal(data, &response) != nil {
		return Limits{}, errors.New("invalid Grok billing response")
	}
	if response.Config == nil || response.Config.Used == nil {
		return makeLimits(checkedAt, nil), nil
	}
	kind, label, reset := "other", "Subscription", ""
	if response.Config.Period != nil {
		period := strings.TrimPrefix(response.Config.Period.Type, "USAGE_PERIOD_TYPE_")
		if period == "WEEKLY" {
			kind, label = "weekly", "Weekly"
		}
		if period == "MONTHLY" {
			kind, label = "monthly", "Monthly"
		}
		reset = iso(response.Config.Period.End)
	}
	return makeLimits(checkedAt, []Window{{Id: "subscription", Kind: kind, Label: label, UsedPercent: percent(*response.Config.Used), ResetsAt: reset}}), nil
}
func MapOpenCode(data json.RawMessage, checkedAt string) (Limits, error) {
	var response struct {
		Usage map[string]json.RawMessage `json:"usage"`
	}
	if json.Unmarshal(data, &response) != nil {
		return Limits{}, errors.New("invalid OpenCode Go usage response")
	}
	windows := []Window{}
	for index, id := range []string{"rolling", "weekly", "monthly"} {
		var value struct {
			Percent  *float64 `json:"percent"`
			ResetsAt string   `json:"resetsAt"`
		}
		if json.Unmarshal(response.Usage[id], &value) != nil || value.Percent == nil || iso(value.ResetsAt) == "" {
			return Limits{}, errors.New("invalid OpenCode Go usage window")
		}
		kind := []string{"session", "weekly", "monthly"}[index]
		window := Window{Id: "go_" + id, Kind: kind, Label: "Go · " + labelForKind(kind), UsedPercent: percent(*value.Percent), ResetsAt: iso(value.ResetsAt)}
		if index == 0 {
			window.WindowDurationMins = duration(300)
		}
		if index == 1 {
			window.WindowDurationMins = duration(7 * 24 * 60)
		}
		windows = append(windows, window)
	}
	return makeLimits(checkedAt, windows), nil
}
