package remote

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"sync"
	"time"
	"yousage/internal/accounting"
	"yousage/internal/domain"
)

type Source struct {
	Fingerprint      domain.SourceFingerprint `json:"fingerprint"`
	Status           string                   `json:"status"`
	ScannedFiles     int                      `json:"scannedFiles"`
	SkippedFiles     int                      `json:"skippedFiles"`
	MalformedRecords int                      `json:"malformedRecords"`
	DistinctSessions int                      `json:"distinctSessions"`
	Message          *string                  `json:"message"`
}
type Pricing struct {
	Status      string  `json:"status"`
	Source      string  `json:"source"`
	FetchedAt   *string `json:"fetchedAt"`
	KnownModels int     `json:"knownModels"`
}
type Summary struct {
	ContractVersion int                 `json:"contractVersion"`
	ReadAt          string              `json:"readAt"`
	TimeZone        string              `json:"timeZone"`
	SinceDay        string              `json:"sinceDay"`
	UntilDay        string              `json:"untilDay"`
	Buckets         []accounting.Bucket `json:"buckets"`
	Sources         []Source            `json:"sources"`
	Pricing         Pricing             `json:"pricing"`
	ScanDurationMs  int64               `json:"scanDurationMs"`
}

var dayPattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

func hasFields(raw json.RawMessage, names ...string) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return false
	}
	for _, name := range names {
		if _, exists := fields[name]; !exists {
			return false
		}
	}
	return true
}
func numericFields(fields map[string]json.RawMessage, names ...string) bool {
	for _, name := range names {
		value, exists := fields[name]
		if !exists || string(value) == "null" {
			return false
		}
		var number float64
		if json.Unmarshal(value, &number) != nil || !domain.Finite(number) {
			return false
		}
	}
	return true
}
func stringField(raw json.RawMessage) bool {
	if string(raw) == "null" {
		return false
	}
	var text string
	return json.Unmarshal(raw, &text) == nil
}
func validBucket(raw json.RawMessage, bucket accounting.Bucket) bool {
	if !hasFields(raw, "day", "provider", "model", "totals", "costUsd", "cacheSavingsUsd", "costSource", "records", "unpricedRecords", "sessions") || !dayPattern.MatchString(bucket.Day) || !bucket.Provider.Valid() || strings.TrimSpace(bucket.Model) == "" {
		return false
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	if !numericFields(fields, "costUsd", "cacheSavingsUsd", "records", "unpricedRecords", "sessions") {
		return false
	}
	for _, name := range []string{"fastCostUsd", "ultrafastCostUsd", "speedPremiumUsd"} {
		if _, exists := fields[name]; exists && !numericFields(fields, name) {
			return false
		}
	}
	if !hasFields(fields["totals"], "uncachedInputTokens", "cachedInputTokens", "cacheCreationTokens", "outputTokens", "reasoningTokens") {
		return false
	}
	var tokenFields map[string]json.RawMessage
	_ = json.Unmarshal(fields["totals"], &tokenFields)
	if !numericFields(tokenFields, "uncachedInputTokens", "cachedInputTokens", "cacheCreationTokens", "outputTokens", "reasoningTokens") {
		return false
	}
	totals := bucket.Totals
	if totals.UncachedInputTokens < 0 || totals.CachedInputTokens < 0 || totals.CacheCreationTokens < 0 || totals.OutputTokens < 0 || totals.ReasoningTokens < 0 || bucket.Records < 0 || bucket.UnpricedRecords < 0 || bucket.Sessions < 0 {
		return false
	}
	if bucket.CostSource != "providerReported" && bucket.CostSource != "modelPriced" && bucket.CostSource != "unpriced" {
		return false
	}
	if _, exists := fields["hourStart"]; exists && strings.TrimSpace(bucket.HourStart) == "" {
		return false
	}
	if _, exists := fields["sourcePath"]; exists && strings.TrimSpace(bucket.SourcePath) == "" {
		return false
	}
	if category, exists := fields["categoryCostUsd"]; exists && !hasFields(category, "input", "cacheRead", "cacheWrite", "output") {
		return false
	}
	if category, exists := fields["categoryCostUsd"]; exists {
		var categoryFields map[string]json.RawMessage
		_ = json.Unmarshal(category, &categoryFields)
		if !numericFields(categoryFields, "input", "cacheRead", "cacheWrite", "output") {
			return false
		}
	}
	return true
}
func validSource(raw json.RawMessage, source Source) bool {
	if !hasFields(raw, "fingerprint", "status", "scannedFiles", "skippedFiles", "malformedRecords", "distinctSessions", "message") {
		return false
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	if !numericFields(fields, "scannedFiles", "skippedFiles", "malformedRecords", "distinctSessions") {
		return false
	}
	if !hasFields(fields["fingerprint"], "hostId", "provider", "resolvedHomePath", "volumeId") {
		return false
	}
	var fingerprintFields map[string]json.RawMessage
	_ = json.Unmarshal(fields["fingerprint"], &fingerprintFields)
	if !stringField(fingerprintFields["volumeId"]) {
		return false
	}
	fingerprint := source.Fingerprint
	if !fingerprint.Provider.Valid() || strings.TrimSpace(fingerprint.HostId) == "" || strings.TrimSpace(fingerprint.ResolvedHomePath) == "" {
		return false
	}
	if source.Status != "ok" && source.Status != "missing" && source.Status != "partial" && source.Status != "failed" {
		return false
	}
	if source.ScannedFiles < 0 || source.SkippedFiles < 0 || source.MalformedRecords < 0 || source.DistinctSessions < 0 {
		return false
	}
	if source.Message != nil && strings.TrimSpace(*source.Message) == "" {
		return false
	}
	return true
}

// DecodeSummary drops unknown array entries individually, matching ForwardCompatibleArray.
func DecodeSummary(data json.RawMessage) (Summary, error) {
	var raw struct {
		ContractVersion int               `json:"contractVersion"`
		ReadAt          string            `json:"readAt"`
		TimeZone        string            `json:"timeZone"`
		SinceDay        string            `json:"sinceDay"`
		UntilDay        string            `json:"untilDay"`
		Buckets         []json.RawMessage `json:"buckets"`
		Sources         []json.RawMessage `json:"sources"`
		Pricing         Pricing           `json:"pricing"`
		ScanDurationMs  int64             `json:"scanDurationMs"`
	}
	result := Summary{}
	if json.Unmarshal(data, &raw) != nil || !hasFields(data, "contractVersion", "readAt", "timeZone", "sinceDay", "untilDay", "buckets", "sources", "pricing", "scanDurationMs") {
		return result, errors.New("invalid usage summary")
	}
	if raw.ContractVersion < 4 {
		return result, errors.New("update server to statistics contract 4 or newer")
	}
	if raw.ContractVersion > 6 {
		return result, errors.New("update Yousage to support this statistics contract")
	}
	if raw.Buckets == nil || raw.Sources == nil || strings.TrimSpace(raw.TimeZone) == "" || !dayPattern.MatchString(raw.SinceDay) || !dayPattern.MatchString(raw.UntilDay) || raw.ScanDurationMs < 0 {
		return result, errors.New("invalid usage summary envelope")
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(data, &fields)
	if !numericFields(fields, "contractVersion", "scanDurationMs") || !stringField(fields["readAt"]) {
		return result, errors.New("invalid usage summary scalar fields")
	}
	if !hasFields(fields["pricing"], "status", "source", "fetchedAt", "knownModels") || raw.Pricing.KnownModels < 0 || strings.TrimSpace(raw.Pricing.Source) == "" || (raw.Pricing.Status != "fresh" && raw.Pricing.Status != "cached" && raw.Pricing.Status != "unavailable") {
		return result, errors.New("invalid usage pricing provenance")
	}
	var pricingFields map[string]json.RawMessage
	_ = json.Unmarshal(fields["pricing"], &pricingFields)
	if !numericFields(pricingFields, "knownModels") {
		return result, errors.New("invalid pricing model count")
	}
	result = Summary{ContractVersion: raw.ContractVersion, ReadAt: raw.ReadAt, TimeZone: raw.TimeZone, SinceDay: raw.SinceDay, UntilDay: raw.UntilDay, Pricing: raw.Pricing, ScanDurationMs: raw.ScanDurationMs, Buckets: []accounting.Bucket{}, Sources: []Source{}}
	for _, entry := range raw.Buckets {
		var bucket accounting.Bucket
		if json.Unmarshal(entry, &bucket) == nil && validBucket(entry, bucket) {
			result.Buckets = append(result.Buckets, bucket)
		}
	}
	for _, entry := range raw.Sources {
		var source Source
		if json.Unmarshal(entry, &source) == nil && validSource(entry, source) {
			result.Sources = append(result.Sources, source)
		}
	}
	return result, nil
}

func (client *Client) Summary(ctx context.Context, window accounting.Window) (Summary, error) {
	payload := map[string]any{"sinceDay": window.SinceDay, "untilDay": window.UntilDay, "timeZone": window.TimeZone, "resolution": window.Resolution}
	if window.Resolution == "hour" {
		payload["sinceTime"] = time.UnixMilli(window.SinceTimeMs).UTC().Format(time.RFC3339Nano)
		payload["untilTime"] = time.UnixMilli(window.UntilTimeMs).UTC().Format(time.RFC3339Nano)
	}
	data, err := client.Call(ctx, "server.getUsageSummary", payload, false)
	if err != nil {
		return Summary{}, err
	}
	return DecodeSummary(data)
}

type SettingsPatch struct {
	Prices  map[string]*domain.PriceOverride `json:"usagePriceOverrides,omitempty"`
	Aliases map[string]*string               `json:"usageModelAliases,omitempty"`
}

func (client *Client) UpdateSettings(ctx context.Context, patch SettingsPatch) (json.RawMessage, error) {
	if len(patch.Prices) > 0 {
		if err := client.requireCapability("usagePriceOverrides"); err != nil {
			return nil, err
		}
	}
	if len(patch.Aliases) > 0 {
		if err := client.requireCapability("usageModelAliases"); err != nil {
			return nil, err
		}
	}
	for model, price := range patch.Prices {
		if strings.TrimSpace(model) == "" {
			return nil, errors.New("price model is empty")
		}
		if price != nil {
			if err := price.Validate(); err != nil {
				return nil, err
			}
		}
	}
	for model, target := range patch.Aliases {
		if strings.TrimSpace(model) == "" || (target != nil && strings.TrimSpace(*target) == "") {
			return nil, errors.New("model alias is empty")
		}
	}
	return client.Call(ctx, "server.updateSettings", map[string]any{"patch": patch}, true)
}

type SaveTarget struct {
	Id     string
	Client *Client
}
type SaveOutcome struct {
	Id               string `json:"id"`
	IsSaved          bool   `json:"saved"`
	Error            string `json:"error,omitempty"`
	IsOutcomeUnknown bool   `json:"outcomeUnknown"`
}

// SaveSettings settles environments independently; the caller retries only explicitly selected failed targets.
func SaveSettings(ctx context.Context, targets []SaveTarget, patch SettingsPatch) []SaveOutcome {
	outcomes := make([]SaveOutcome, len(targets))
	var workers sync.WaitGroup
	for index, target := range targets {
		workers.Add(1)
		go func() {
			defer workers.Done()
			deadline, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			outcome := SaveOutcome{Id: target.Id}
			if target.Client == nil {
				outcome.Error = "environment is offline"
			} else {
				_, err := target.Client.UpdateSettings(deadline, patch)
				if err == nil {
					outcome.IsSaved = true
				} else {
					outcome.Error = err.Error()
					var failure *CallError
					if errors.As(err, &failure) {
						outcome.IsOutcomeUnknown = failure.IsOutcomeUnknown
					}
				}
			}
			outcomes[index] = outcome
		}()
	}
	workers.Wait()
	return outcomes
}
