package quota

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"yousage/internal/provider/claudeprobe"
)

const claudeSecretMarker = "SYNTHETIC-CLAUDE-SECRET-DO-NOT-EXPOSE"

type claudeFixtureTransport func(*http.Request) (*http.Response, error)

type claudeFixtureReadFailure struct{ cause error }

// Read simulates failure while reading the HTTP response body.
func (failure claudeFixtureReadFailure) Read([]byte) (int, error) { return 0, failure.cause }

// RoundTrip supplies synthetic quota responses without contacting an account or network.
func (transport claudeFixtureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

// assertClaudeDiagnostic verifies visible evidence and redaction through the serialized result.
func assertClaudeDiagnostic(test *testing.T, limits Limits, reason, message string) {
	test.Helper()
	if limits.Unavailable == nil || limits.Unavailable.Reason != reason || limits.Unavailable.Message != message {
		test.Errorf("diagnostic = %+v; want %s / %q", limits.Unavailable, reason, message)
	}
	encoded, err := json.Marshal(limits)
	if err != nil || strings.Contains(string(encoded), claudeSecretMarker) {
		test.Fatal("diagnostic leaked synthetic private data or could not be serialized")
	}
	if limits.Unavailable != nil && len(limits.Windows) != 0 {
		test.Fatal("failed reading invented quota windows")
	}
}

// TestClaudeStoredCredentialDiagnostics separates absent API-key credentials from malformed, unreadable or expired OAuth data.
func TestClaudeStoredCredentialDiagnostics(test *testing.T) {
	for _, fixture := range []struct {
		name, credentials, reason, message string
		readError                          error
	}{
		{name: "missing", readError: os.ErrNotExist, reason: "unsupported"},
		{name: "api-key", credentials: `{"apiKey":"` + claudeSecretMarker + `"}`, reason: "unsupported"},
		{name: "unreadable", readError: errors.New(claudeSecretMarker), reason: "probeFailed", message: "Claude Code login could not be read."},
		{name: "malformed", credentials: `{` + claudeSecretMarker, reason: "probeFailed", message: "Claude Code stored credentials are malformed."},
		{name: "trailing", credentials: `{} {"secret":"` + claudeSecretMarker + `"}`, reason: "probeFailed", message: "Claude Code stored credentials are malformed."},
		{name: "null", credentials: `null`, reason: "probeFailed", message: "Claude Code stored credentials are malformed."},
		{name: "missing-token", credentials: `{"claudeAiOauth":{"secret":"` + claudeSecretMarker + `"}}`, reason: "probeFailed", message: "Claude Code stored credentials are malformed."},
		{name: "blank-token", credentials: `{"claudeAiOauth":{"accessToken":" "}}`, reason: "probeFailed", message: "Claude Code stored credentials are malformed."},
		{name: "expired", credentials: `{"claudeAiOauth":{"accessToken":"` + claudeSecretMarker + `","expiresAt":1}}`, reason: "probeFailed", message: "Claude Code stored access token has expired; usage limits could not be read."},
	} {
		test.Run(fixture.name, func(test *testing.T) {
			options := HttpOptions{
				ReadFile: func(string) ([]byte, error) { return []byte(fixture.credentials), fixture.readError },
				Client: &http.Client{Transport: claudeFixtureTransport(func(*http.Request) (*http.Response, error) {
					test.Error("unusable stored credentials must not contact the usage API")
					return nil, errors.New(claudeSecretMarker)
				})},
			}
			assertClaudeDiagnostic(test, ReadClaudeLogin(context.Background(), options, "synthetic"), fixture.reason, fixture.message)
		})
	}
}

// TestClaudeUsageFailureEvidence exercises fallback after a null CLI response without inventing global login expiry.
func TestClaudeUsageFailureEvidence(test *testing.T) {
	for _, fixture := range []struct {
		name, body, message string
		status              int
		transportError      error
		bodyError           error
		isCancelled         bool
	}{
		{name: "unauthorized", status: 401, message: "Claude usage API rejected the stored credential (HTTP 401)."},
		{name: "forbidden", status: 403, message: "Claude usage API denied access to usage limits (HTTP 403)."},
		{name: "rate-limited", status: 429, message: "Claude usage API rate limit reached (HTTP 429). Try again later."},
		{name: "server-error", status: 500, message: "Claude usage API is temporarily unavailable (HTTP 5xx). Try again later."},
		{name: "unavailable", status: 503, message: "Claude usage API is temporarily unavailable (HTTP 5xx). Try again later."},
		{name: "other-status", status: 418, message: "Claude usage API returned an unexpected HTTP status."},
		{name: "redirect", status: 302, message: "Claude usage API returned an unexpected HTTP status."},
		{name: "network", transportError: errors.New(claudeSecretMarker), message: "Claude usage API could not be reached or read because of a network error."},
		{name: "deadline", transportError: context.DeadlineExceeded, message: "Claude usage API request timed out."},
		{name: "transport-timeout", transportError: &net.DNSError{Err: claudeSecretMarker, IsTimeout: true}, message: "Claude usage API request timed out."},
		{name: "cancelled", isCancelled: true, message: "Claude usage API request was cancelled."},
		{name: "body-network", status: 200, bodyError: errors.New(claudeSecretMarker), message: "Claude usage API could not be reached or read because of a network error."},
		{name: "body-timeout", status: 200, bodyError: context.DeadlineExceeded, message: "Claude usage API request timed out."},
		{name: "body-cancelled", status: 200, bodyError: context.Canceled, message: "Claude usage API request was cancelled."},
		{name: "invalid-json", status: 200, body: `{` + claudeSecretMarker, message: "Claude usage API returned invalid JSON."},
		{name: "trailing-json", status: 200, body: `{} {"secret":"` + claudeSecretMarker + `"}`, message: "Claude usage API returned invalid JSON."},
		{name: "trailing-garbage", status: 200, body: `{} ` + claudeSecretMarker, message: "Claude usage API returned invalid JSON."},
		{name: "empty-body", status: 200, message: "Claude usage API returned invalid JSON."},
		{name: "truncated-json", status: 200, body: `{"secret":"` + claudeSecretMarker, message: "Claude usage API returned invalid JSON."},
		{name: "invalid-window", status: 200, body: `{"five_hour":{"utilization":"` + claudeSecretMarker + `","resets_at":null}}`, message: "Claude returned invalid usage limits."},
		{name: "missing-utilization", status: 200, body: `{"five_hour":{"resets_at":null}}`, message: "Claude returned invalid usage limits."},
		{name: "invalid-reset", status: 200, body: `{"five_hour":{"utilization":10,"resets_at":4}}`, message: "Claude returned invalid usage limits."},
		{name: "null-schema", status: 200, body: `null`, message: "Claude returned invalid usage limits."},
		{name: "missing-schema", status: 200, body: `{}`, message: "Claude returned invalid usage limits."},
		{name: "unexpected-schema", status: 200, body: `{"secret":"` + claudeSecretMarker + `"}`, message: "Claude returned invalid usage limits."},
	} {
		test.Run(fixture.name, func(test *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if fixture.isCancelled {
				cancel()
			}
			options := HttpOptions{
				ReadFile: func(string) ([]byte, error) {
					return []byte(`{"claudeAiOauth":{"accessToken":"` + claudeSecretMarker + `"}}`), nil
				},
				Client: &http.Client{Transport: claudeFixtureTransport(func(request *http.Request) (*http.Response, error) {
					if request.Header.Get("Authorization") != "Bearer "+claudeSecretMarker {
						test.Error("selected stored credential was not used")
					}
					if err := request.Context().Err(); err != nil {
						return nil, err
					}
					if fixture.transportError != nil {
						return nil, fixture.transportError
					}
					if fixture.bodyError != nil {
						return &http.Response{StatusCode: fixture.status, Body: io.NopCloser(claudeFixtureReadFailure{cause: fixture.bodyError})}, nil
					}
					body := fixture.body
					if fixture.status >= 300 {
						body = claudeSecretMarker
					}
					headers := http.Header{}
					if fixture.status == http.StatusFound {
						headers.Set("Location", "https://synthetic.invalid/"+claudeSecretMarker)
					}
					return &http.Response{StatusCode: fixture.status, Header: headers, Body: io.NopCloser(strings.NewReader(body))}, nil
				})},
			}
			cli, _, err := MapClaude([]byte(`{"rate_limits_available":true,"rate_limits":null}`), "old")
			if err != nil {
				test.Fatal(err)
			}
			limits := PreferClaudeLogin(cli, ReadClaudeLogin(ctx, options, claudeSecretMarker))
			assertClaudeDiagnostic(test, limits, "probeFailed", fixture.message)
		})
	}
}

// TestClaudeProbeDiagnostics verifies the real adapter against an isolated synthetic CLI process.
func TestClaudeProbeDiagnostics(test *testing.T) {
	directory := test.TempDir()
	executable := filepath.Join(directory, "claude-fixture")
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "build", "-o", executable, "../../tools/spikes/claude")
	if output, err := command.CombinedOutput(); err != nil {
		test.Fatalf("synthetic CLI build: %s %v", output, err)
	}
	for _, fixture := range []struct {
		name, reason, message string
		isCancelled           bool
	}{
		{name: "unknown-method", reason: "unsupported"},
		{name: "usage-hang", reason: "probeFailed", message: "Claude Code usage probe timed out."},
		{name: "invalid-window", reason: "probeFailed", message: "Claude Code returned an invalid usage probe response."},
		{name: "missing-utilization", reason: "probeFailed", message: "Claude Code returned an invalid usage probe response."},
		{name: "usage-protocol", reason: "probeFailed", message: "Claude Code returned an invalid usage probe response."},
		{name: "usage-auth", reason: "probeFailed", message: "Claude Code rejected authentication for the usage probe."},
		{name: "usage-network-error", reason: "probeFailed", message: "Claude Code usage probe could not communicate with the CLI."},
		{name: "init-error", reason: "probeFailed", message: "Claude Code rejected authentication for the usage probe."},
		{name: "init-protocol", reason: "probeFailed", message: "Claude Code returned an invalid usage probe response."},
		{name: "init-hang", reason: "probeFailed", message: "Claude Code usage probe timed out."},
		{name: "cancelled", isCancelled: true, reason: "probeFailed", message: "Claude Code usage probe was cancelled."},
		{name: "null-limits", reason: "unsupported"},
		{name: "missing-executable", reason: "probeFailed", message: "Claude Code usage probe could not communicate with the CLI."},
	} {
		test.Run(fixture.name, func(test *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if fixture.isCancelled {
				cancel()
			}
			options := claudeprobe.Options{Executable: executable, ConfigDirectory: test.TempDir(), InitializeLimit: 2 * time.Second, UsageLimit: time.Second, Environment: []string{"YOUSAGE_FIXTURE_SCENARIO=" + fixture.name, "YOUSAGE_FIXTURE_SECRET=" + claudeSecretMarker}}
			if fixture.name == "init-hang" {
				options.InitializeLimit = 200 * time.Millisecond
			}
			if fixture.name == "usage-hang" {
				options.UsageLimit = 200 * time.Millisecond
			}
			if fixture.name == "missing-executable" {
				options.Executable = filepath.Join(directory, claudeSecretMarker+".exe")
			}
			account := ProbeClaude(ctx, options, true)
			assertClaudeDiagnostic(test, account.Limits, fixture.reason, fixture.message)
			if fixture.name == "usage-auth" && account.Email == "" {
				test.Fatal("separately observed initialization metadata was lost")
			}
		})
	}
}

// TestClaudeSuccessfulLoginFallbackReplacesCLIError preserves successful fallback even after an actual CLI failure.
func TestClaudeSuccessfulLoginFallbackReplacesCLIError(test *testing.T) {
	options := HttpOptions{
		ReadFile: func(string) ([]byte, error) {
			return []byte(`{"claudeAiOauth":{"accessToken":"` + claudeSecretMarker + `"}}`), nil
		},
		Client: &http.Client{Transport: claudeFixtureTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"five_hour":{"utilization":0,"resets_at":null},"seven_day":null}`))}, nil
		})},
	}
	login := ReadClaudeLogin(context.Background(), options, "synthetic")
	if login.Unavailable != nil || len(login.Windows) != 1 || login.Windows[0].UsedPercent != 0 {
		test.Fatalf("successful zero-percent reading was lost: %+v", login)
	}
	for _, reason := range []string{"unsupported", "probeFailed"} {
		result := PreferClaudeLogin(unavailable("old", reason, "CLI failure"), login)
		if result.Unavailable != nil || result.CheckedAt != login.CheckedAt || len(result.Windows) != 1 {
			test.Fatalf("successful login fallback did not win: %+v", result)
		}
		encoded, _ := json.Marshal(result)
		if strings.Contains(string(encoded), claudeSecretMarker) {
			test.Fatal("successful fallback leaked its credential")
		}
	}
}
