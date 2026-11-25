package quota

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// TestClaudeTokenReadsWindowsFromRateLimitHeaders covers long-lived tokens, whose scope cannot read the usage endpoint.
func TestClaudeTokenReadsWindowsFromRateLimitHeaders(test *testing.T) {
	var authorization string
	var maxTokens any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		authorization = request.Header.Get("Authorization")
		var body map[string]any
		_ = json.NewDecoder(request.Body).Decode(&body)
		maxTokens = body["max_tokens"]
		writer.Header().Set("anthropic-ratelimit-unified-5h-utilization", "0.33")
		writer.Header().Set("anthropic-ratelimit-unified-5h-reset", "1795598400")
		writer.Header().Set("anthropic-ratelimit-unified-7d-utilization", "0.09")
		writer.Header().Set("anthropic-ratelimit-unified-7d-reset", "1796058000")
		writer.Write([]byte(`{"type":"message","content":[]}`))
	}))
	defer server.Close()
	directory := test.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "settings.json"), []byte(`{"env":{"CLAUDE_CODE_OAUTH_TOKEN":"settings-token"}}`), 0o600); err != nil {
		test.Fatal(err)
	}
	options := HttpOptions{ClaudeMessagesEndpoint: server.URL, Environment: map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "environment-token"}}
	limits := ReadClaudeToken(context.Background(), options, directory)
	if limits.Unavailable != nil || len(limits.Windows) != 2 {
		test.Fatalf("header windows were not mapped: %+v", limits)
	}
	session, weekly := limits.Windows[0], limits.Windows[1]
	if session.Id != "five_hour" || session.UsedPercent != 33 || session.ResetsAt != "2026-11-25T09:20:00.000Z" || weekly.Id != "seven_day" || weekly.Kind != "weekly" || weekly.UsedPercent != 9 {
		test.Fatalf("unexpected windows: %+v", limits.Windows)
	}
	if authorization != "Bearer settings-token" {
		test.Fatalf("settings token must win over the process environment, got %q", authorization)
	}
	if maxTokens != float64(0) {
		test.Fatalf("probe must not request output tokens, got %v", maxTokens)
	}
}

// TestClaudeTokenFailuresStayDistinct keeps a missing token quiet and surfaces a rejected one.
func TestClaudeTokenFailuresStayDistinct(test *testing.T) {
	missing := ReadClaudeToken(context.Background(), HttpOptions{}, test.TempDir())
	if missing.Unavailable == nil || missing.Unavailable.Reason != "unsupported" {
		test.Fatalf("missing token must be unsupported: %+v", missing)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") == "Bearer rejected" {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		writer.Write([]byte(`{}`))
	}))
	defer server.Close()
	for token, message := range map[string]string{
		"rejected": "Claude usage API rejected the stored credential (HTTP 401).",
		"headless": "Claude returned invalid usage limits.",
	} {
		options := HttpOptions{ClaudeMessagesEndpoint: server.URL, Environment: map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": token}}
		limits := ReadClaudeToken(context.Background(), options, test.TempDir())
		if limits.Unavailable == nil || limits.Unavailable.Reason != "probeFailed" || limits.Unavailable.Message != message {
			test.Fatalf("%s token: unexpected result %+v", token, limits)
		}
	}
}
