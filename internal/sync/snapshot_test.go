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
	"yousage/internal/preferences"
	"yousage/internal/quota"
	"yousage/internal/remote"
	"yousage/internal/service"
	"yousage/internal/theme"
)

type reportFixture struct {
	calls         []string
	failReport    string
	failDays      int
	usage         service.Snapshot
	configuration service.Configuration
}

func (source *reportFixture) read(kind string, days int, timeZone string) error {
	source.calls = append(source.calls, kind+":"+strconv.Itoa(days)+":"+timeZone)
	if source.failReport == kind && source.failDays == days {
		return errors.New("report failed")
	}
	return nil
}

func (source *reportFixture) Usage(_ context.Context, days int, timeZone string) (service.Snapshot, error) {
	return source.usage, source.read("usage", days, timeZone)
}

func (source *reportFixture) LossReport(_ context.Context, days int, timeZone string) (service.LossReport, error) {
	return service.LossReport{}, source.read("loss", days, timeZone)
}

func (source *reportFixture) SpeedReport(_ context.Context, days int, timeZone string) (service.SpeedReport, error) {
	return service.SpeedReport{}, source.read("speed", days, timeZone)
}

func (source *reportFixture) Receipt(_ context.Context, days int, timeZone string) (service.Receipt, error) {
	return service.Receipt{Days: days, TimeZone: timeZone}, source.read("receipt", days, timeZone)
}

func (source *reportFixture) Configuration(context.Context) (service.SavedConfiguration, error) {
	return service.SavedConfiguration{Value: source.configuration}, nil
}

func TestSnapshotAllPeriodsAndNoConfiguration(test *testing.T) {
	config := remote.Config{Environment: remote.Environment{Capabilities: remote.Capabilities{}}}
	if err := json.Unmarshal([]byte(`{"providers":[{"instanceId":"codex","auth":{"label":"Personal account"},"usageLimits":{"checkedAt":"2026-10-15T15:30:00Z","windows":[{"id":"session","kind":"session","label":"Session","usedPercent":42}]}}],"usageLimitSources":[{"id":"hub","label":"Quota hub","accounts":[]}]}`), &config); err != nil {
		test.Fatal(err)
	}
	source := &reportFixture{
		usage:         service.Snapshot{Environments: []service.EnvironmentState{{Id: "local", Config: &config}}},
		configuration: service.Configuration{Hubs: []quota.HubConfig{{Id: "hub", Url: "https://private-hub.example", CredentialId: "private-credential"}}},
	}
	builder := SnapshotBuilder{Source: source, Display: func(context.Context) (preferences.SavedDisplay, error) {
		return preferences.SavedDisplay{Value: preferences.Defaults()}, nil
	}, Appearance: func(context.Context) (theme.LibrarySnapshot, error) { return theme.LibrarySnapshot{}, nil }, Bootstrap: func() any { return map[string]any{"isSystemDark": true, "systemLocale": "en"} }, Now: newFixtureClock().Now}
	snapshot, err := builder.Build(context.Background(), "Asia/Shanghai")
	if err != nil || len(snapshot.Periods) != 4 || len(source.calls) != 16 || snapshot.AppVersion == "" || snapshot.GeneratedAt == "" {
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
	for _, forbidden := range []string{`"credentialId"`, source.configuration.Hubs[0].CredentialId, `"url"`, source.configuration.Hubs[0].Url, fixtureToken, `"configuration"`} {
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
