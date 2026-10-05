// Package quota normalizes independently observed subscription allowances.
package quota

import (
	"encoding/json"
	"math"
	"sort"
	"time"
)

type Window struct {
	Id                 string  `json:"id"`
	Kind               string  `json:"kind"`
	Label              string  `json:"label"`
	UsedPercent        float64 `json:"usedPercent"`
	ResetsAt           string  `json:"resetsAt,omitempty"`
	WindowDurationMins *int    `json:"windowDurationMins,omitempty"`
}
type ResetCredits struct {
	AvailableCount int    `json:"availableCount"`
	NextExpiresAt  string `json:"nextExpiresAt,omitempty"`
	NextCreditId   string `json:"nextCreditId,omitempty"`
}
type Unavailable struct {
	Reason  string `json:"reason"`
	Message string `json:"message,omitempty"`
}
type ExternalUsage struct {
	Label string `json:"label"`
	Url   string `json:"url"`
}
type Limits struct {
	CheckedAt             string         `json:"checkedAt"`
	Windows               []Window       `json:"windows"`
	CredentialFingerprint string         `json:"credentialFingerprint,omitempty"`
	ResetCredits          *ResetCredits  `json:"resetCredits,omitempty"`
	ExternalUsage         *ExternalUsage `json:"externalUsage,omitempty"`
	Unavailable           *Unavailable   `json:"unavailable,omitempty"`
}
type Account struct {
	Email  string `json:"email,omitempty"`
	Plan   string `json:"plan,omitempty"`
	Limits Limits `json:"usageLimits"`
}

func checked(now time.Time) string { return now.UTC().Format("2006-01-02T15:04:05.000Z") }
func percent(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	return max(0, min(100, value))
}
func duration(value int) *int { return &value }
func makeLimits(checkedAt string, windows []Window) Limits {
	if windows == nil {
		windows = []Window{}
	}
	order := map[string]int{"session": 0, "weekly": 1, "monthly": 2, "other": 3}
	sort.SliceStable(windows, func(first, second int) bool {
		left, right := windows[first], windows[second]
		if order[left.Kind] != order[right.Kind] {
			return order[left.Kind] < order[right.Kind]
		}
		return left.Id < right.Id
	})
	return Limits{CheckedAt: checkedAt, Windows: windows}
}
func unavailable(checkedAt, reason, message string) Limits {
	result := makeLimits(checkedAt, nil)
	result.Unavailable = &Unavailable{reason, message}
	return result
}
func iso(value string) string {
	instant, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return ""
	}
	return checked(instant)
}
func epoch(value *float64, isSeconds bool) string {
	if value == nil || *value <= 0 || math.IsNaN(*value) || math.IsInf(*value, 0) {
		return ""
	}
	milliseconds := *value
	if isSeconds {
		milliseconds *= 1000
	}
	if milliseconds > 8.64e15 {
		return ""
	}
	return checked(time.UnixMilli(int64(milliseconds)))
}
func numericObject(data json.RawMessage) map[string]any {
	var value map[string]any
	_ = json.Unmarshal(data, &value)
	return value
}
func text(value any) string { result, _ := value.(string); return result }
func numeric(value any) *float64 {
	number, ok := value.(float64)
	if !ok || math.IsNaN(number) || math.IsInf(number, 0) {
		return nil
	}
	return &number
}

// ResolveProbe preserves previously successful bars on probe failure; unsupported replaces them.
func ResolveProbe(previous *Limits, probed Limits) Limits {
	if probed.Unavailable != nil && probed.Unavailable.Reason == "probeFailed" && previous != nil && previous.Unavailable == nil {
		return *previous
	}
	return probed
}

// ApplyUpdate upserts sparse windows while keeping resolved reset times and durations.
func ApplyUpdate(previous *Limits, windows []Window, checkedAt string) *Limits {
	if len(windows) == 0 || (previous != nil && previous.Unavailable != nil && previous.Unavailable.Reason == "unsupported") {
		return previous
	}
	merged := map[string]Window{}
	isChanged := false
	if previous != nil {
		for _, window := range previous.Windows {
			merged[window.Id] = window
		}
	}
	for _, window := range windows {
		if existing, ok := merged[window.Id]; ok {
			if window.ResetsAt == "" {
				window.ResetsAt = existing.ResetsAt
			}
			if window.WindowDurationMins == nil {
				window.WindowDurationMins = existing.WindowDurationMins
			}
		}
		window.UsedPercent = percent(window.UsedPercent)
		existing, exists := merged[window.Id]
		if !exists || !windowEquals(existing, window) {
			isChanged = true
		}
		merged[window.Id] = window
	}
	if !isChanged && previous != nil && previous.Unavailable == nil {
		return previous
	}
	result := []Window{}
	for _, window := range merged {
		result = append(result, window)
	}
	limits := makeLimits(checkedAt, result)
	if previous != nil {
		limits.ResetCredits = previous.ResetCredits
	}
	return &limits
}

func windowEquals(first, second Window) bool {
	isDurationEqual := first.WindowDurationMins == nil && second.WindowDurationMins == nil
	if first.WindowDurationMins != nil && second.WindowDurationMins != nil {
		isDurationEqual = *first.WindowDurationMins == *second.WindowDurationMins
	}
	return first.Id == second.Id && first.Kind == second.Kind && first.Label == second.Label && first.UsedPercent == second.UsedPercent && first.ResetsAt == second.ResetsAt && isDurationEqual
}

// MergeCodexSnapshot preserves omitted fields, applies explicit nulls and rejects model-specific buckets.
func MergeCodexSnapshot(previous, update json.RawMessage) json.RawMessage {
	patch := numericObject(update)
	if id := text(patch["limitId"]); id != "" && id != "codex" {
		return previous
	}
	current := numericObject(previous)
	if current == nil {
		current = map[string]any{}
	}
	for _, key := range []string{"limitId", "planType", "rateLimitReachedType", "primary", "secondary"} {
		if value, exists := patch[key]; exists {
			current[key] = value
		}
	}
	result, _ := json.Marshal(current)
	return result
}

func kindForDuration(minutes int) string {
	if minutes >= 30*24*60 {
		return "monthly"
	}
	if minutes >= 7*24*60 {
		return "weekly"
	}
	return "session"
}
func labelForKind(kind string) string {
	if kind == "monthly" {
		return "Monthly"
	}
	if kind == "weekly" {
		return "Weekly"
	}
	return "Session"
}
