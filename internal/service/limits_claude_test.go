package service

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
	"yousage/internal/quota"
)

// TestClaudeScanFailureNoticesAndObservations exercises actual scan persistence before and after a successful CLI reading.
func TestClaudeScanFailureNoticesAndObservations(test *testing.T) {
	const secretMarker = "SYNTHETIC-CLAUDE-SERVICE-SECRET"
	service := testService(test)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	directory := test.TempDir()
	executable := filepath.Join(directory, "claude-fixture")
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	command := exec.CommandContext(ctx, "go", "build", "-o", executable, "../../tools/spikes/claude")
	if output, err := command.CombinedOutput(); err != nil {
		test.Fatalf("synthetic CLI build: %s %v", output, err)
	}
	configuration, err := service.Configuration(ctx)
	if err != nil {
		test.Fatal(err)
	}
	configuration.Value.Providers = []NativeProvider{{Id: "claude", Label: "Claude fixture", Driver: "claudeAgent", Executable: executable, Home: directory, IsEnabled: true}}
	if _, err := service.SaveConfiguration(ctx, configuration); err != nil {
		test.Fatal(err)
	}
	service.Environment["YOUSAGE_FIXTURE_SECRET"] = secretMarker
	service.Environment["YOUSAGE_FIXTURE_SCENARIO"] = "init-error"
	for range 2 {
		if _, err := service.scanLimits(ctx); err != nil {
			test.Fatal(err)
		}
		result, _, err := service.quotas(ctx)
		if err != nil || len(result.Providers) != 1 {
			test.Fatal("scan did not persist a visible failure", err)
		}
		if !strings.Contains(result.Error, "No previous successful limits are available.") || strings.Contains(result.Error, "are retained") {
			test.Errorf("notice falsely claims retained limits: %q", result.Error)
		}
		provider := result.Providers[0]
		if provider.Limits == nil || provider.Limits.Unavailable == nil || provider.Limits.Unavailable.Reason != "probeFailed" || provider.Auth.Status == "authenticated" {
			test.Fatal("authentication rejection was hidden or promoted to authenticated", provider)
		}
		encoded, _ := json.Marshal(result)
		if strings.Contains(string(encoded), secretMarker) || strings.Contains(string(encoded), directory) {
			test.Fatal("service returned synthetic private probe data")
		}
		var series []quota.CycleSeries
		if _, err := service.Store.ReadSetting(ctx, "quota-samples", &series); err != nil || len(series) != 0 {
			test.Fatal("failed reading created a quota observation", err)
		}
	}
	service.Environment["YOUSAGE_FIXTURE_SCENARIO"] = "success"
	if _, err := service.scanLimits(ctx); err != nil {
		test.Fatal(err)
	}
	previous, _, err := service.quotas(ctx)
	if err != nil || previous.Error != "" || previous.Providers[0].Limits == nil || previous.Providers[0].Limits.Unavailable != nil || len(previous.Providers[0].Limits.Windows) != 1 {
		test.Fatal("healthy probe did not recover", previous, err)
	}
	var previousSeries []quota.CycleSeries
	if _, err := service.Store.ReadSetting(ctx, "quota-samples", &previousSeries); err != nil || len(previousSeries) != 1 || len(previousSeries[0].Samples) != 1 || len(previousSeries[0].Observations) != 1 {
		test.Fatal("healthy probe did not create one observation", err)
	}
	for _, scenario := range []string{"usage-auth", "usage-network-error", "usage-protocol"} {
		service.Environment["YOUSAGE_FIXTURE_SCENARIO"] = scenario
		if _, err := service.scanLimits(ctx); err != nil {
			test.Fatal(err)
		}
		result, _, err := service.quotas(ctx)
		if err != nil || result.Error == "" || !strings.Contains(result.Error, "Last successful limits are retained.") || strings.Contains(result.Error, "No previous") {
			test.Fatal("retained observation notice was lost", result, err)
		}
		if !reflect.DeepEqual(result.Providers[0].Limits, previous.Providers[0].Limits) {
			test.Fatal("failed probe changed last-good windows or checked timestamp")
		}
		var series []quota.CycleSeries
		if _, err := service.Store.ReadSetting(ctx, "quota-samples", &series); err != nil || !reflect.DeepEqual(series, previousSeries) {
			test.Fatal("failed probe created a fresh sample or observation", err)
		}
		encoded, _ := json.Marshal(result)
		if strings.Contains(string(encoded), secretMarker) {
			test.Fatal("retained-limit notice leaked synthetic probe data")
		}
	}
}
