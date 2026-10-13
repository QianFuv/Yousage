package quota

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
	"yousage/internal/managedprocess"
)

var codexExecutable string

func TestMain(runner *testing.M) {
	directory, err := os.MkdirTemp("", "yousage-quota-fixture-")
	if err != nil {
		panic(err)
	}
	codexExecutable = filepath.Join(directory, "fixture")
	if runtime.GOOS == "windows" {
		codexExecutable += ".exe"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	command := exec.CommandContext(ctx, "go", "build", "-o", codexExecutable, "./testdata/codexfixture")
	output, err := command.CombinedOutput()
	cancel()
	if err != nil {
		panic(string(output) + err.Error())
	}
	code := runner.Run()
	_ = os.RemoveAll(directory)
	os.Exit(code)
}

func TestCodexDurationMainBucketCreditsAndPartialNull(t *testing.T) {
	limits, err := MapCodex([]byte(`{"rateLimits":{"limitId":"spark","primary":{"usedPercent":90}},"rateLimitsByLimitId":{"codex":{"planType":"free","primary":{"usedPercent":120},"secondary":{"usedPercent":-10}}},"rateLimitResetCredits":{"availableCount":2,"credits":[{"status":"used","expiresAt":1},{"status":"available","expiresAt":1800000200},{"status":"available","expiresAt":1800000100}]}}`), "checked")
	if err != nil || len(limits.Windows) != 2 || limits.Windows[0].Id != "secondary" || limits.Windows[1].Kind != "monthly" || limits.Windows[1].UsedPercent != 100 || limits.Windows[0].UsedPercent != 0 || limits.ResetCredits == nil || limits.ResetCredits.AvailableCount != 2 || limits.ResetCredits.NextExpiresAt != epoch(number(1800000100), true) {
		t.Fatal(limits, err)
	}
	previous := json.RawMessage(`{"planType":"plus","primary":{"usedPercent":20},"secondary":{"usedPercent":30}}`)
	merged := MergeCodexSnapshot(previous, []byte(`{"primary":null}`))
	if !strings.Contains(string(merged), `"primary":null`) || !strings.Contains(string(merged), `"secondary"`) {
		t.Fatal(string(merged))
	}
	if string(MergeCodexSnapshot(merged, []byte(`{"limitId":"spark","secondary":null}`))) != string(merged) {
		t.Fatal("Spark replaced main allowance")
	}
}
func number(value float64) *float64 { return &value }

func TestClaudeScopedWindowsAndFractionUpdate(t *testing.T) {
	limits, name, err := MapClaude([]byte(`{"rate_limits_available":true,"rate_limits":{"five_hour":{"utilization":20,"resets_at":"2026-10-06T10:00:00Z"},"seven_day":null,"model_scoped":[{"display_name":"Fable 5.1","utilization":40,"resets_at":"2026-10-10T10:00:00Z"},{"display_name":"Skipped","utilization":null}]}}`), "checked")
	if err != nil || name != "Fable 5.1" || len(limits.Windows) != 2 || limits.Windows[1].Id != "seven_day_fable_5_1" {
		t.Fatal(limits, name, err)
	}
	update := ClaudeUpdate([]byte(`{"rateLimitType":"seven_day_overage_included","utilization":0.6,"resetsAt":1800000000}`), name)
	if len(update) != 1 || update[0].Id != limits.Windows[1].Id || update[0].UsedPercent != 60 {
		t.Fatal(update)
	}
	if len(ClaudeUpdate([]byte(`{"rateLimitType":"seven_day_overage_included","utilization":0.6}`), "")) != 0 {
		t.Fatal("unnamed scoped bucket invented")
	}
}

func TestProbeFailureRetentionAndUnsupportedAuthority(t *testing.T) {
	previous := makeLimits("old", []Window{{Id: "primary", Kind: "session", Label: "Session", UsedPercent: 30, ResetsAt: "reset", WindowDurationMins: duration(300)}})
	failed := unavailable("new", "probeFailed", "failed")
	if result := ResolveProbe(&previous, failed); result.CheckedAt != "old" || len(result.Windows) != 1 {
		t.Fatal(result)
	}
	unsupported := ResolveProbe(&previous, unavailable("new", "unsupported", ""))
	if len(unsupported.Windows) != 0 || unsupported.Unavailable.Reason != "unsupported" {
		t.Fatal(unsupported)
	}
	if ApplyUpdate(&unsupported, []Window{{Id: "primary", UsedPercent: 5}}, "later") != &unsupported {
		t.Fatal("unsupported recovered from runtime")
	}
	updated := ApplyUpdate(&previous, []Window{{Id: "primary", Kind: "session", Label: "Session", UsedPercent: 50}}, "new")
	if updated.Windows[0].ResetsAt != "reset" || *updated.Windows[0].WindowDurationMins != 300 {
		t.Fatal(updated)
	}
	if ApplyUpdate(&previous, nil, "new") != &previous {
		t.Fatal("empty update changed snapshot")
	}
	if ApplyUpdate(&previous, []Window{{Id: "primary", Kind: "session", Label: "Session", UsedPercent: 30}}, "new") != &previous {
		t.Fatal("unchanged tick replaced checked time")
	}
}

func TestOptionalQuotaFieldsRejectExplicitNullAndWrongShapes(t *testing.T) {
	for _, data := range []string{`null`, `[]`, `{}`, `{"rateLimits":null}`, `{"rateLimits":{"primary":{}}}`} {
		if _, err := MapCodex([]byte(data), "checked"); err == nil {
			t.Fatal(data)
		}
	}
}

func TestCodexNativeHandshakeDeadlineAndAccountIdentity(t *testing.T) {
	for _, scenario := range []string{"success", "quota-hang", "api-key", "rpc-error"} {
		t.Run(scenario, func(t *testing.T) {
			directory := t.TempDir()
			pidFile := filepath.Join(directory, "pids")
			requestFile := filepath.Join(directory, "requests")
			options := NativeOptions{IsEnabled: true, ProbeLimit: time.Second, EnrichmentLimit: 100 * time.Millisecond, Process: managedprocess.Options{Executable: codexExecutable, Directory: directory, Environment: append(os.Environ(), "YOUSAGE_CODEX_SCENARIO="+scenario, "YOUSAGE_PID_FILE="+pidFile, "YOUSAGE_REQUEST_FILE="+requestFile)}}
			account := ProbeCodex(context.Background(), options)
			if scenario == "success" {
				if account.Email != "fixture@example.invalid" || account.Limits.Unavailable != nil || len(account.Limits.Windows) != 1 || account.Limits.Windows[0].UsedPercent != 25 {
					t.Fatal(account)
				}
			} else if scenario == "api-key" {
				if account.Limits.Unavailable == nil || account.Limits.Unavailable.Reason != "unsupported" {
					t.Fatal(account)
				}
			} else {
				if account.Email != "fixture@example.invalid" || account.Limits.Unavailable == nil || account.Limits.Unavailable.Reason != "probeFailed" || strings.Contains(account.Limits.Unavailable.Message, "secret") {
					t.Fatal(account)
				}
			}
			data, err := os.ReadFile(requestFile)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			if len(lines) < 3 {
				t.Fatal(lines)
			}
			if !strings.Contains(lines[0], `"method":"initialize"`) || !strings.Contains(lines[1], `"method":"initialized"`) || !strings.Contains(lines[2], `"method":"account/read"`) || strings.Contains(string(data), `"jsonrpc"`) || strings.Contains(string(data), "thread/") {
				t.Fatal(string(data))
			}
			if scenario != "api-key" && !strings.Contains(string(data), `"method":"account/rateLimits/read","params":null`) {
				t.Fatal(string(data))
			}
			pidData, _ := os.ReadFile(pidFile)
			pids := []int{}
			for _, value := range strings.Fields(string(pidData)) {
				pid, _ := strconv.Atoi(value)
				pids = append(pids, pid)
			}
			if len(pids) != 3 {
				t.Fatal("fixture did not spawn tree", pids)
			}
			assertProcessTreeStopped(t, pids)
		})
	}
}

func TestNativeResetKeepsIdempotencyKeyAndStopsWholeTree(t *testing.T) {
	for _, scenario := range []string{"success", "reset-invalid", "reset-hang", "quota-hang"} {
		t.Run(scenario, func(t *testing.T) {
			directory := t.TempDir()
			pidFile := filepath.Join(directory, "pids")
			requestFile := filepath.Join(directory, "requests")
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			result := ConsumeCodex(ctx, NativeOptions{IsEnabled: true, ExpectedAccountEmail: "fixture@example.invalid", Process: managedprocess.Options{Executable: codexExecutable, Directory: directory, Environment: append(os.Environ(), "YOUSAGE_CODEX_SCENARIO="+scenario, "YOUSAGE_PID_FILE="+pidFile, "YOUSAGE_REQUEST_FILE="+requestFile)}}, "stable-owned-operation")
			if (scenario == "success" || scenario == "quota-hang") && result.Outcome != "reset" {
				t.Fatal(result)
			}
			if (scenario == "reset-invalid" || scenario == "reset-hang") && result.Outcome != "unknown" {
				t.Fatal(result)
			}
			if scenario == "quota-hang" && result.Warning == "" {
				t.Fatal("applied reset lost confirmation warning")
			}
			requests, _ := os.ReadFile(requestFile)
			if strings.Count(string(requests), "account/rateLimitResetCredit/consume") != 1 || !strings.Contains(string(requests), `"idempotencyKey":"stable-owned-operation"`) {
				t.Fatal(string(requests))
			}
			pidsData, _ := os.ReadFile(pidFile)
			pids := []int{}
			for _, value := range strings.Fields(string(pidsData)) {
				pid, _ := strconv.Atoi(value)
				pids = append(pids, pid)
			}
			assertProcessTreeStopped(t, pids)
		})
	}
}

func TestNativeResetRejectsChangedSelectedAccount(t *testing.T) {
	directory := t.TempDir()
	requestFile := filepath.Join(directory, "requests")
	pidFile := filepath.Join(directory, "pids")
	result := ConsumeCodex(context.Background(), NativeOptions{IsEnabled: true, ExpectedAccountEmail: "different@example.invalid", Process: managedprocess.Options{Executable: codexExecutable, Environment: append(os.Environ(), "YOUSAGE_PID_FILE="+pidFile, "YOUSAGE_REQUEST_FILE="+requestFile)}}, "unchanged-operation")
	requests, _ := os.ReadFile(requestFile)
	if result.Outcome != "failed" || !strings.Contains(result.Warning, "account changed") || strings.Contains(string(requests), "/consume") {
		t.Fatal(result, string(requests))
	}
	data, _ := os.ReadFile(pidFile)
	pids := []int{}
	for _, value := range strings.Fields(string(data)) {
		pid, _ := strconv.Atoi(value)
		pids = append(pids, pid)
	}
	assertProcessTreeStopped(t, pids)
}
