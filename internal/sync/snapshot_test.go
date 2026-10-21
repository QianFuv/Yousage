package sync

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
	"yousage/internal/accounting"
	"yousage/internal/preferences"
	"yousage/internal/quota"
	"yousage/internal/remote"
	"yousage/internal/service"
	"yousage/internal/theme"
)

type reportFixture struct {
	calls              []string
	failReport         string
	failDays           int
	usage              service.Snapshot
	configuration      service.Configuration
	configurationReads int
	periods            map[string]Period
}

func (source *reportFixture) read(kind string, days int, timeZone string) error {
	source.calls = append(source.calls, kind+":"+strconv.Itoa(days)+":"+timeZone)
	if source.failReport == kind && source.failDays == days {
		return errors.New("report failed")
	}
	return nil
}

func (source *reportFixture) Usage(_ context.Context, days int, timeZone string) (service.Snapshot, error) {
	if source.periods != nil {
		return source.periods[strconv.Itoa(days)].Usage, source.read("usage", days, timeZone)
	}
	return source.usage, source.read("usage", days, timeZone)
}

func (source *reportFixture) LossReport(_ context.Context, days int, timeZone string) (service.LossReport, error) {
	return source.periods[strconv.Itoa(days)].Loss, source.read("loss", days, timeZone)
}

func (source *reportFixture) SpeedReport(_ context.Context, days int, timeZone string) (service.SpeedReport, error) {
	return source.periods[strconv.Itoa(days)].Speed, source.read("speed", days, timeZone)
}

func (source *reportFixture) Receipt(_ context.Context, days int, timeZone string) (service.Receipt, error) {
	if source.periods != nil {
		return source.periods[strconv.Itoa(days)].Receipt, source.read("receipt", days, timeZone)
	}
	return service.Receipt{Days: days, TimeZone: timeZone}, source.read("receipt", days, timeZone)
}

func (source *reportFixture) Configuration(context.Context) (service.SavedConfiguration, error) {
	source.configurationReads++
	return service.SavedConfiguration{Value: source.configuration}, nil
}

func TestSnapshotAllPeriodsAndNoConfiguration(test *testing.T) {
	config := remote.Config{Environment: remote.Environment{Capabilities: remote.Capabilities{}}}
	if err := json.Unmarshal([]byte(`{"providers":[{"instanceId":"codex","auth":{"label":"Personal account"},"usageLimits":{"checkedAt":"2026-10-15T15:30:00Z","windows":[{"id":"session","kind":"session","label":"Session","usedPercent":42}]}}],"usageLimitSources":[{"id":"hub","label":"Quota hub","accounts":[]}]}`), &config); err != nil {
		test.Fatal(err)
	}
	source := &reportFixture{
		usage: service.Snapshot{Environments: []service.EnvironmentState{{Id: "local", Config: &config}}},
		configuration: service.Configuration{
			Hubs:        []quota.HubConfig{{Id: "hub", Url: "https://private-hub.example", CredentialId: "private-credential"}},
			Connections: []service.ConnectionPreference{{Connection: remote.Connection{Url: "https://private-environment.example", CredentialId: "private-session-credential"}}},
			Providers:   []service.NativeProvider{{Home: "/private-provider-home", Executable: "/private-provider-executable"}},
		},
	}
	builder := SnapshotBuilder{Source: source, Display: func(context.Context) (preferences.SavedDisplay, error) {
		return preferences.SavedDisplay{Value: preferences.Defaults()}, nil
	}, Appearance: func(context.Context) (theme.LibrarySnapshot, error) { return theme.LibrarySnapshot{}, nil }, Bootstrap: func() any { return map[string]any{"isSystemDark": true, "systemLocale": "en"} }, Now: newFixtureClock().Now}
	snapshot, err := builder.Build(context.Background(), "Asia/Shanghai")
	if err != nil || len(snapshot.Periods) != 4 || len(source.calls) != 16 || source.configurationReads != 0 || snapshot.AppVersion == "" || snapshot.GeneratedAt == "" {
		test.Fatal(snapshot, err)
	}
	for _, days := range []int{1, 7, 30, 90} {
		period, hasPeriod := snapshot.Periods[strconv.Itoa(days)]
		if !hasPeriod || period.Receipt.Days != days || period.Receipt.TimeZone != "Asia/Shanghai" || !reflect.DeepEqual(period.Usage, source.usage) {
			test.Fatal("missing period or modified usage", days)
		}
	}
	for _, call := range source.calls {
		if !strings.HasSuffix(call, ":Asia/Shanghai") {
			test.Fatal("stored time zone was not used", call)
		}
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		test.Fatal(err)
	}
	for _, forbidden := range []string{`"credentialId"`, source.configuration.Hubs[0].CredentialId, `"url"`, source.configuration.Hubs[0].Url, fixtureToken, `"configuration"`, "private-session-credential", "private-environment.example", "/private-provider-home", "/private-provider-executable"} {
		if strings.Contains(string(data), forbidden) {
			test.Fatal("snapshot leaked configuration", forbidden)
		}
	}
	var decoded Snapshot
	if err := json.Unmarshal(data, &decoded); err != nil {
		test.Fatal(err)
	}
	for name, period := range decoded.Periods {
		if !reflect.DeepEqual(period.Usage, source.usage) {
			test.Fatal("usage did not round-trip unchanged", name)
		}
		provider := period.Usage.Environments[0].Config.Providers[0]
		if provider.Auth.Label != "Personal account" || provider.Limits == nil || len(provider.Limits.Windows) != 1 || provider.Limits.Windows[0].UsedPercent != 42 {
			test.Fatal("provider quota or auth label missing", name, provider)
		}
	}
	for _, report := range []string{"usage", "loss", "speed", "receipt"} {
		for _, days := range []int{1, 7, 30, 90} {
			source.failReport, source.failDays = report, days
			failed, err := builder.Build(context.Background(), "UTC")
			if err == nil || len(failed.Periods) != 0 {
				test.Fatal("partial snapshot returned", report, days)
			}
		}
	}
}

func TestSnapshotUtcFallbackAndSettingsFailures(test *testing.T) {
	builder := SnapshotBuilder{Source: &reportFixture{}, Display: func(context.Context) (preferences.SavedDisplay, error) { return preferences.SavedDisplay{}, nil }, Appearance: func(context.Context) (theme.LibrarySnapshot, error) { return theme.LibrarySnapshot{}, nil }, Bootstrap: func() any { return struct{}{} }, Now: func() time.Time { return time.Unix(0, 0) }}
	snapshot, err := builder.Build(context.Background(), "")
	if err != nil || snapshot.TimeZone != "UTC" {
		test.Fatal(snapshot.TimeZone, err)
	}
	builder.Appearance = func(context.Context) (theme.LibrarySnapshot, error) {
		return theme.LibrarySnapshot{}, errors.New("appearance unavailable")
	}
	if _, err = builder.Build(context.Background(), "UTC"); err == nil {
		test.Fatal("settings failure published partial snapshot")
	}
}

// reportParityJSON is the single synthetic wire fixture also read by mobile and frontend tests.
// Keep this raw-string declaration stable: those tests extract it, never execute Go source.
// __DAYS__ gives every period distinct business values, not just a different map key.
const reportParityJSON = `{
  "formatVersion": 1, "generatedAt": "2026-10-20T00:00:00Z", "appVersion": "synthetic",
  "timeZone": "UTC",
  "display": {"revision": 13, "value": {"zoom": 1.25, "metric": "tokens", "windowDays": 7,
    "shouldAutoRefresh": true, "shouldRestoreWindow": true, "shouldCloseToTray": true,
    "shouldStartMinimized": true, "timestampFormat": "24h", "breakdown": "model", "language": "zh-CN"}},
  "appearance": {"revision": 17, "value": {"preferences": {"mode": "dark", "baseThemeId": "system",
    "typography": {"interfaceSize": 17, "codeSize": 15}, "contrast": 110, "glassOpacity": 85,
    "appliedGenerations": {"system": "synthetic-generation"}}}},
  "bootstrap": {"isSystemDark": false, "systemLocale": "zh-CN"},
  "periods": {"__DAYS__": {
    "usage": {"window": __WINDOW__,
      "environments": [{"environmentId": "desktop-__DAYS__", "label": "Synthetic desktop __DAYS__",
        "local": true, "connected": true, "canOperate": true, "refreshing": true,
        "error": "Synthetic partial source notice",
        "summary": {"contractVersion": 6, "readAt": "2026-10-20T00:00:00Z", "timeZone": "UTC",
          "sinceDay": "__SINCE__", "untilDay": "2026-10-20", "scanDurationMs": __DAYS__07,
          "pricing": {"status": "cached", "source": "synthetic prices", "fetchedAt": "2026-10-19T00:00:00Z", "knownModels": __DAYS__},
          "sources": [{"fingerprint": {"hostId": "synthetic-host", "provider": "codex", "resolvedHomePath": "/synthetic/identity-only", "volumeId": "synthetic-volume"},
            "status": "partial", "scannedFiles": __DAYS__03, "skippedFiles": __DAYS__, "malformedRecords": 2,
            "distinctSessions": __DAYS__02, "message": "Synthetic incomplete source"},
            {"fingerprint": {"hostId": "synthetic-host", "provider": "pi", "resolvedHomePath": "/synthetic/pi-identity", "volumeId": "synthetic-volume"},
             "status": "ok", "scannedFiles": 3, "skippedFiles": 0, "malformedRecords": 0, "distinctSessions": 2, "message": null}],
          "buckets": [{"day": "2026-10-19", "provider": "codex", "model": "synthetic/vendor/complete-model-identifier-__DAYS__",
            "totals": {"uncachedInputTokens": __DAYS__01, "cachedInputTokens": __DAYS__02, "cacheCreationTokens": __DAYS__03, "outputTokens": __DAYS__04, "reasoningTokens": __DAYS__05},
            "costUsd": __DAYS__.25, "cacheSavingsUsd": __DAYS__.5,
            "categoryCostUsd": {"input": __DAYS__.1, "cacheRead": 0.02, "cacheWrite": 0.03, "output": 0.1},
            "fastCostUsd": 0.1, "ultrafastCostUsd": 0.2, "speedPremiumUsd": 0.05,
            "costSource": "modelPriced", "records": __DAYS__06, "unpricedRecords": __DAYS__, "sessions": __DAYS__02},
            {"day": "2026-10-19", "provider": "pi", "model": "synthetic/untimed-__DAYS__",
             "totals": {"uncachedInputTokens": 51, "cachedInputTokens": 52, "cacheCreationTokens": 53, "outputTokens": 54, "reasoningTokens": 5},
             "costUsd": 2.75, "cacheSavingsUsd": 0.5, "costSource": "providerReported", "records": 3, "unpricedRecords": 0, "sessions": 2}]},
        "serverConfig": {"environment": {"environmentId": "desktop-__DAYS__", "label": "Synthetic producer",
          "serverVersion": "synthetic", "orchestrationProtocolVersion": 2,
          "capabilities": {"usageLimitSources": true, "usagePriceOverrides": true}, "platform": {"os": "windows", "arch": "amd64"}},
          "settings": {"defaultTheme": "system", "defaultThemeSetAt": "2026-10-19T00:00:00Z", "usagePriceOverrides": {}, "usageModelAliases": {"synthetic-alias": "synthetic/vendor/complete-model-identifier-__DAYS__"}},
          "providers": [{"instanceId": "codex", "driver": "codex", "displayName": "Synthetic Codex", "enabled": true,
            "installed": true, "availability": "available", "status": "ready", "auth": {"status": "authenticated", "email": "fixture@example.invalid", "label": "Synthetic account", "workspaceId": "synthetic-workspace"},
            "slashCommands": [{"name": "synthetic", "description": "Synthetic command", "argumentHint": "value"}],
            "usageLimits": {"checkedAt": "2026-10-20T00:00:00Z", "credentialFingerprint": "synthetic-fingerprint",
              "windows": [{"id": "session", "kind": "session", "label": "Session", "usedPercent": __DAYS__, "resetsAt": "2026-10-20T05:00:00Z", "windowDurationMins": 300}],
              "resetCredits": {"availableCount": __DAYS__, "nextExpiresAt": "2026-11-01T00:00:00Z", "nextCreditId": "synthetic-credit-__DAYS__"},
              "externalUsage": {"label": "Provider usage", "url": "https://example.invalid/usage"},
              "unavailable": {"reason": "probeFailed", "message": "Synthetic stale quota notice"}}}],
          "usageLimitSources": [{"id": "synthetic-hub", "kind": "hub", "label": "Synthetic quota source", "checkedAt": "2026-10-20T00:00:00Z", "error": "Synthetic source notice",
            "accounts": [{"id": "synthetic-account", "driver": "codex", "email": "quota@example.invalid", "plan": "pro", "workspaceId": "synthetic-workspace",
              "usageLimits": {"checkedAt": "2026-10-20T00:00:00Z", "windows": [{"id": "weekly", "kind": "weekly", "label": "Weekly", "usedPercent": 37, "resetsAt": "2026-10-27T00:00:00Z", "windowDurationMins": 10080}],
                "resetCredits": {"availableCount": __DAYS__01, "nextExpiresAt": "2026-11-02T00:00:00Z", "nextCreditId": "synthetic-hub-credit"}}}]}]},
        "cycleValues": [{"instanceId": "codex", "driver": "codex", "windowId": "session", "status": "estimated",
          "cycleStartedAt": "2026-10-20T00:00:00Z", "resetsAt": "2026-10-20T05:00:00Z", "resetDetected": true,
          "usedPercent": 37, "usedLowPercent": 36.5, "usedHighPercent": 38, "spentUsd": __DAYS__.25,
          "records": __DAYS__06, "unpricedRecords": __DAYS__, "valueUsd": __DAYS__01, "valueLowUsd": __DAYS__00,
          "valueHighUsd": __DAYS__02, "remainingLowUsd": __DAYS__.5, "remainingHighUsd": __DAYS__.75,
          "constraintCount": 4, "observationSource": "both", "priorUsed": true, "priorCycleCount": 2, "adjustmentSuspected": true,
          "samples": [{"at": "2026-10-20T00:00:00Z", "usedPercent": 37, "spentUsd": __DAYS__.25, "impliedLowUsd": __DAYS__00, "impliedHighUsd": __DAYS__02, "usable": true}],
          "history": [{"cycleStartedAt": "2026-10-19T19:00:00Z", "resetsAt": "2026-10-20T00:00:00Z", "status": "estimated", "valueUsd": __DAYS__01, "valueLowUsd": __DAYS__00, "valueHighUsd": __DAYS__02, "planType": "pro", "usedForPrior": true}]}]}]},
    "loss": {"window": __WINDOW__,
      "cacheExpiry": {"events": __DAYS__01, "tokens": __DAYS__500, "costUsd": __DAYS__.75}, "cacheWriteTokens": __DAYS__900,
      "providers": [{"provider": "codex", "events": __DAYS__01, "tokens": __DAYS__500, "costUsd": __DAYS__.75}],
      "days": [{"day": "2026-10-19", "cacheExpiryUsd": __DAYS__.75, "cacheExpiryCount": __DAYS__01, "providers": [{"provider": "codex", "costUsd": __DAYS__.75, "count": __DAYS__01}]}],
      "events": [{"kind": "cacheExpiry", "provider": "codex", "model": "synthetic/vendor/complete-model-identifier-__DAYS__", "sessionId": "synthetic-session-__DAYS__", "atMs": 1792368000000, "idleMs": __DAYS__000000, "tokens": __DAYS__500, "costUsd": __DAYS__.75, "precision": "estimated"}]},
    "speed": {"window": __WINDOW__,
      "measuredRequests": __DAYS__06, "unmeasuredRequests": __DAYS__,
      "overall": {"requests": __DAYS__06, "unmeasuredRequests": __DAYS__, "totalTokens": __DAYS__810, "outputTokens": __DAYS__04,
        "generationMs": __DAYS__500, "tokensPerSecond": __DAYS__.2, "medianTokensPerSecond": __DAYS__.3, "p10TokensPerSecond": __DAYS__.1, "p90TokensPerSecond": __DAYS__.9,
        "ttftRequests": __DAYS__05, "medianTtftMs": __DAYS__10, "p90TtftMs": __DAYS__20, "decodeTokensPerSecond": __DAYS__.8,
        "medianLatencyMs": __DAYS__50, "p90LatencyMs": __DAYS__90, "inputTokens": __DAYS__606, "cachedInputTokens": __DAYS__02,
        "cacheHitRate": 0.37, "contextTokensPerRequest": __DAYS__20, "outputTokensPerRequest": __DAYS__30},
      "models": [{"provider": "codex", "model": "synthetic/vendor/complete-model-identifier-__DAYS__", "speed": "fast",
        "requests": __DAYS__06, "unmeasuredRequests": __DAYS__, "totalTokens": __DAYS__810, "outputTokens": __DAYS__04,
        "generationMs": __DAYS__500, "tokensPerSecond": __DAYS__.2, "medianTokensPerSecond": __DAYS__.3, "p10TokensPerSecond": __DAYS__.1, "p90TokensPerSecond": __DAYS__.9,
        "ttftRequests": __DAYS__05, "medianTtftMs": __DAYS__10, "p90TtftMs": __DAYS__20, "decodeTokensPerSecond": __DAYS__.8,
        "medianLatencyMs": __DAYS__50, "p90LatencyMs": __DAYS__90, "inputTokens": __DAYS__606, "cachedInputTokens": __DAYS__02,
        "cacheHitRate": 0.37, "contextTokensPerRequest": __DAYS__20, "outputTokensPerRequest": __DAYS__30},
        {"provider": "pi", "model": "synthetic/untimed-__DAYS__", "speed": "standard", "requests": 0, "unmeasuredRequests": __DAYS__, "totalTokens": __DAYS__210, "inputTokens": __DAYS__156, "cachedInputTokens": 52, "cacheHitRate": 0.25, "contextTokensPerRequest": 156, "outputTokensPerRequest": 54}],
      "providers": [{"provider": "codex", "requests": __DAYS__06, "unmeasuredRequests": __DAYS__, "totalTokens": __DAYS__810, "tokensPerSecond": __DAYS__.2}],
      "buckets": [{"label": "2026-10-19", "models": [{"provider": "codex", "model": "synthetic/vendor/complete-model-identifier-__DAYS__", "speed": "fast", "requests": __DAYS__06, "outputTokens": __DAYS__04, "generationMs": __DAYS__500, "tokensPerSecond": __DAYS__.2}],
        "providers": [{"provider": "codex", "requests": __DAYS__06, "outputTokens": __DAYS__04, "generationMs": __DAYS__500, "tokensPerSecond": __DAYS__.2}]}]},
    "receipt": {"days": __DAYS__, "window": __WINDOW__,
      "startsAtMs": __START_MS__, "endsAtMs": 1792454400000, "number": "synthetic-__DAYS__", "timeZone": "UTC", "sinceDay": "__SINCE__", "untilDay": "2026-10-20",
      "items": [{"provider": "codex", "model": "synthetic/vendor/complete-model-identifier-__DAYS__", "tokens": __DAYS__410, "costUsd": __DAYS__.25, "unpricedRecords": __DAYS__, "isSubscription": true, "isPaymentUnknown": false},
        {"provider": "pi", "model": "synthetic/untimed-__DAYS__", "tokens": 210, "costUsd": 2.75, "unpricedRecords": 0, "isSubscription": false, "isPaymentUnknown": true}],
      "totalUsd": __DAYS__.99, "subscriptionUsd": __DAYS__.25, "paidUsd": 0.25, "unknownPaymentUsd": 2.75,
      "tokens": __DAYS__620, "sessions": __DAYS__04, "cacheSavingsUsd": __DAYS__.5, "unpricedRecords": __DAYS__, "activeDays": __DAYS__,
      "firstAtMs": 1792368000001, "lastAtMs": 1792454399999, "qrText": "synthetic receipt __DAYS__", "qrModules": ["101", "010", "101"]}
  }}
}`

// paritySnapshot rejects invented wire fields and expands the shared fixture for all periods.
func paritySnapshot(test *testing.T) Snapshot {
	test.Helper()
	var snapshot Snapshot
	periods := map[string]Period{}
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
		payload := strings.NewReplacer("__DAYS__", name, "__SINCE__", window.SinceDay, "__WINDOW__", string(windowJSON), "__START_MS__", strconv.FormatInt(start.UnixMilli(), 10)).Replace(reportParityJSON)
		decoder := json.NewDecoder(strings.NewReader(payload))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&snapshot); err != nil {
			test.Fatal(err)
		}
		periods[name] = snapshot.Periods[name]
	}
	snapshot.Periods = periods
	return snapshot
}

// TestSnapshotBusinessFieldPreservation compares the builder to independent source reports.
func TestSnapshotBusinessFieldPreservation(test *testing.T) {
	expected := paritySnapshot(test)
	input := paritySnapshot(test)
	source := &reportFixture{periods: input.Periods}
	builder := SnapshotBuilder{Source: source,
		Display:    func(context.Context) (preferences.SavedDisplay, error) { return input.Display, nil },
		Appearance: func(context.Context) (theme.LibrarySnapshot, error) { return input.Appearance, nil },
		Bootstrap:  func() any { return input.Bootstrap },
		Now:        func() time.Time { return time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC) },
	}
	actual, err := builder.Build(context.Background(), expected.TimeZone)
	if err != nil {
		test.Fatal(err)
	}
	expected.AppVersion = AppVersion
	if !reflect.DeepEqual(actual, expected) || len(source.calls) != 16 {
		test.Fatal("builder changed source business fields or skipped a report")
	}
	encoded, err := json.Marshal(actual)
	if err != nil {
		test.Fatal(err)
	}
	var decoded Snapshot
	if err := json.Unmarshal(encoded, &decoded); err != nil || !reflect.DeepEqual(decoded, expected) {
		test.Fatal("JSON changed source business fields", err)
	}
}
