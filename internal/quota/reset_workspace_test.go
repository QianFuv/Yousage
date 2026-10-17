package quota

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"yousage/internal/managedprocess"
)

// TestNativeResetVerifiesWorkspaceBeforeSpending protects independently billed workspaces sharing an email.
func TestNativeResetVerifiesWorkspaceBeforeSpending(t *testing.T) {
	for _, test := range []struct {
		name, actual, scenario string
		wantConsumes           int
	}{
		{"matching", "personal", "success", 1},
		{"changed", "team", "success", 0},
		{"missing", "", "success", 0},
		{"unavailable", "personal", "rpc-error", 0},
		{"timeout", "personal", "quota-hang", 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			requestFile := filepath.Join(directory, "requests")
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			options := NativeOptions{IsEnabled: true, ExpectedAccountEmail: "fixture@example.invalid", Process: managedprocess.Options{Executable: codexExecutable, Directory: directory, Environment: append(os.Environ(), "YOUSAGE_CODEX_WORKSPACE="+test.actual, "YOUSAGE_CODEX_SCENARIO="+test.scenario, "YOUSAGE_PID_FILE="+filepath.Join(directory, "pids"), "YOUSAGE_REQUEST_FILE="+requestFile)}}
			options.ExpectedWorkspaceId = "personal"
			result := ConsumeCodex(ctx, options, "workspace-bound-operation")
			requests, err := os.ReadFile(requestFile)
			if err != nil {
				t.Fatal(err)
			}
			if count := strings.Count(string(requests), "account/rateLimitResetCredit/consume"); count != test.wantConsumes {
				t.Fatalf("consume calls = %d, want %d; result=%+v", count, test.wantConsumes, result)
			}
			if test.wantConsumes == 0 && result.Outcome != "failed" || test.wantConsumes == 1 && result.Outcome != "reset" {
				t.Fatal(result)
			}
		})
	}
}
