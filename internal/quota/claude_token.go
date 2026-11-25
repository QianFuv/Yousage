package quota

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	claudeMessagesEndpoint = "https://api.anthropic.com/v1/messages"
	// claudeTokenProbeBody asks for zero output tokens; Claude rejects OAuth requests without the Claude Code system prompt.
	claudeTokenProbeBody = `{"model":"claude-haiku-5-5","max_tokens":0,"system":"You are Claude Code, Anthropic's official CLI for Claude.","messages":[{"role":"user","content":"quota"}]}`
	// claudeRateLimitHeader prefixes the subscription windows every messages response reports.
	claudeRateLimitHeader = "Anthropic-Ratelimit-Unified-"
)

// claudeConfigDirectory resolves the directory that holds Claude Code's settings and stored login.
func claudeConfigDirectory(options HttpOptions, configDirectory string) string {
	if directory := strings.TrimSpace(configDirectory); directory != "" {
		return directory
	}
	if directory := strings.TrimSpace(options.Environment["CLAUDE_CONFIG_DIR"]); directory != "" {
		return directory
	}
	return filepath.Join(options.userHome(), ".claude")
}

// claudeLongLivedToken finds the CLAUDE_CODE_OAUTH_TOKEN Claude Code uses.
//
// Claude Code applies the user settings' env block over its process
// environment, so the settings file wins over Yousage's own environment.
func claudeLongLivedToken(options HttpOptions, directory string) string {
	if data, err := options.file(filepath.Join(directory, "settings.json")); err == nil {
		var settings struct {
			Environment map[string]string `json:"env"`
		}
		if json.Unmarshal(data, &settings) == nil {
			if token := strings.TrimSpace(settings.Environment[claudeLongLivedTokenSource]); token != "" {
				return token
			}
		}
	}
	return strings.TrimSpace(options.Environment[claudeLongLivedTokenSource])
}

// ReadClaudeToken reads the session and weekly windows for a long-lived Claude Code token.
//
// The token only carries inference scope, so the usage endpoint rejects it.
// A messages request with max_tokens 0 produces no output and still returns
// the unified rate-limit headers. A token that cannot be found is unsupported.
func ReadClaudeToken(ctx context.Context, options HttpOptions, configDirectory string) Limits {
	checkedAt := checked(time.Now())
	token := claudeLongLivedToken(options, claudeConfigDirectory(options, configDirectory))
	if token == "" {
		return unavailable(checkedAt, "unsupported", "")
	}
	endpoint := claudeMessagesEndpoint
	if options.ClaudeMessagesEndpoint != "" {
		endpoint = options.ClaudeMessagesEndpoint
	}
	deadline, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(deadline, http.MethodPost, endpoint, strings.NewReader(claudeTokenProbeBody))
	if err != nil {
		return unavailable(checkedAt, "probeFailed", claudeUsageFailure(0, err))
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("anthropic-version", "2023-06-01")
	request.Header.Set("anthropic-beta", "oauth-2025-04-20")
	response, err := options.client().Do(request)
	if err != nil {
		return unavailable(checkedAt, "probeFailed", claudeUsageFailure(0, err))
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return unavailable(checkedAt, "probeFailed", claudeUsageFailure(response.StatusCode, errors.New("quota request failed")))
	}
	windows := []Window{}
	for _, window := range []struct {
		header, id, kind string
		minutes          int
	}{{"5h", "five_hour", "session", 300}, {"7d", "seven_day", "weekly", 7 * 24 * 60}} {
		used, err := strconv.ParseFloat(response.Header.Get(claudeRateLimitHeader+window.header+"-Utilization"), 64)
		if err != nil {
			continue
		}
		var resetsAt *float64
		if reset, err := strconv.ParseFloat(response.Header.Get(claudeRateLimitHeader+window.header+"-Reset"), 64); err == nil {
			resetsAt = &reset
		}
		windows = append(windows, Window{Id: window.id, Kind: window.kind, Label: labelForKind(window.kind), UsedPercent: percent(used * 100), ResetsAt: epoch(resetsAt, true), WindowDurationMins: duration(window.minutes)})
	}
	if len(windows) == 0 {
		return unavailable(checkedAt, "probeFailed", "Claude returned invalid usage limits.")
	}
	return makeLimits(checkedAt, windows)
}
