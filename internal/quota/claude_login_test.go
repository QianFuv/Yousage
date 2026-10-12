package quota

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// TestClaudeLoginReadsWindowsWhenControlUsageIsNull covers CLI releases whose get_usage omits rate limits.
func TestClaudeLoginReadsWindowsWhenControlUsageIsNull(test *testing.T) {
	cli, _, err := MapClaude([]byte(`{"rate_limits_available":true,"rate_limits":null}`), "checked")
	if err != nil || cli.Unavailable == nil || cli.Unavailable.Reason != "unsupported" {
		test.Fatalf("precondition: null control limits must be unsupported, got %+v %v", cli, err)
	}
	var authorization, beta string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		authorization, beta = request.Header.Get("Authorization"), request.Header.Get("anthropic-beta")
		writer.Write([]byte(`{"five_hour":{"utilization":21.0,"resets_at":"2026-10-06T10:30:00+00:00"},"seven_day":{"utilization":3.0,"resets_at":"2026-10-12T17:00:00+00:00"},"seven_day_opus":null,"limits":[{"kind":"session","percent":21,"resets_at":"2026-10-06T10:30:00+00:00","scope":null}]}`))
	}))
	defer server.Close()
	directory := test.TempDir()
	expiresAt := strconv.FormatInt(time.Now().Add(time.Hour).UnixMilli(), 10)
	if err := os.WriteFile(filepath.Join(directory, ".credentials.json"), []byte(`{"claudeAiOauth":{"accessToken":"login-token","expiresAt":`+expiresAt+`}}`), 0o600); err != nil {
		test.Fatal(err)
	}
	login := ReadClaudeLogin(context.Background(), HttpOptions{ClaudeEndpoint: server.URL}, directory)
	limits := PreferClaudeLogin(cli, login)
	if limits.Unavailable != nil || len(limits.Windows) != 2 || limits.Windows[0].UsedPercent != 21 || limits.Windows[1].Kind != "weekly" || limits.Windows[1].UsedPercent != 3 {
		test.Fatalf("login usage was not mapped: %+v", limits)
	}
	if authorization != "Bearer login-token" || beta != "oauth-2025-04-20" {
		test.Fatalf("unexpected request headers: %q %q", authorization, beta)
	}
}

// TestClaudeLoginFailuresKeepTheMostInformativeResult keeps API-key users quiet and surfaces expired logins.
func TestClaudeLoginFailuresKeepTheMostInformativeResult(test *testing.T) {
	missing := ReadClaudeLogin(context.Background(), HttpOptions{}, test.TempDir())
	if missing.Unavailable == nil || missing.Unavailable.Reason != "unsupported" {
		test.Fatalf("missing login must be unsupported: %+v", missing)
	}
	directory := test.TempDir()
	if err := os.WriteFile(filepath.Join(directory, ".credentials.json"), []byte(`{"claudeAiOauth":{"accessToken":"old","expiresAt":1}}`), 0o600); err != nil {
		test.Fatal(err)
	}
	expired := ReadClaudeLogin(context.Background(), HttpOptions{ClaudeEndpoint: "http://127.0.0.1:1"}, directory)
	if expired.Unavailable == nil || expired.Unavailable.Reason != "probeFailed" {
		test.Fatalf("expired login must fail visibly: %+v", expired)
	}
	unsupported := unavailable("checked", "unsupported", "")
	if result := PreferClaudeLogin(unsupported, missing); result.Unavailable.Reason != "unsupported" {
		test.Fatalf("missing login replaced the CLI answer: %+v", result)
	}
	if result := PreferClaudeLogin(unsupported, expired); result.Unavailable.Reason != "probeFailed" {
		test.Fatalf("expired login was hidden: %+v", result)
	}
	failed := unavailable("checked", "probeFailed", "Claude could not initialize its usage probe.")
	if result := PreferClaudeLogin(failed, expired); result.Unavailable.Message != failed.Unavailable.Message {
		test.Fatalf("CLI failure message was replaced: %+v", result)
	}
}

// TestClaudeOrganizationFollowsTheAccountRecord reads the organization beside the settings or in the home directory.
func TestClaudeOrganizationFollowsTheAccountRecord(test *testing.T) {
	configured, home := test.TempDir(), test.TempDir()
	if err := os.WriteFile(filepath.Join(configured, ".claude.json"), []byte(`{"oauthAccount":{"organizationUuid":" org-configured "}}`), 0o600); err != nil {
		test.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"oauthAccount":{"organizationUuid":"org-home"}}`), 0o600); err != nil {
		test.Fatal(err)
	}
	options := HttpOptions{Environment: map[string]string{"HOME": home}}
	if organization := ReadClaudeOrganization(options, configured); organization != "org-configured" {
		test.Fatalf("configured directory: got %q", organization)
	}
	if organization := ReadClaudeOrganization(options, ""); organization != "org-home" {
		test.Fatalf("home directory: got %q", organization)
	}
	if organization := ReadClaudeOrganization(options, test.TempDir()); organization != "" {
		test.Fatalf("missing record: got %q", organization)
	}
}
