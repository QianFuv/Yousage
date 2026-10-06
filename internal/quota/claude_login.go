package quota

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const claudeUsageEndpoint = "https://api.anthropic.com/api/oauth/usage"

// ReadClaudeLogin reads subscription windows with the local Claude Code login.
//
// Recent Claude Code releases answer the control-protocol get_usage request with
// null rate limits, so this reads the same account usage endpoint that Claude
// Code's /usage command uses. A missing login is unsupported rather than failed.
func ReadClaudeLogin(ctx context.Context, options HttpOptions, configDirectory string) Limits {
	checkedAt := checked(time.Now())
	directory := strings.TrimSpace(configDirectory)
	if directory == "" {
		directory = strings.TrimSpace(options.Environment["CLAUDE_CONFIG_DIR"])
	}
	if directory == "" {
		directory = filepath.Join(options.userHome(), ".claude")
	}
	data, err := options.file(filepath.Join(directory, ".credentials.json"))
	if errors.Is(err, os.ErrNotExist) {
		return unavailable(checkedAt, "unsupported", "")
	}
	if err != nil {
		return unavailable(checkedAt, "probeFailed", "Claude Code login could not be read.")
	}
	var credentials struct {
		Login *struct {
			AccessToken string `json:"accessToken"`
			ExpiresAt   int64  `json:"expiresAt"`
		} `json:"claudeAiOauth"`
	}
	if json.Unmarshal(data, &credentials) != nil || credentials.Login == nil || credentials.Login.AccessToken == "" {
		return unavailable(checkedAt, "unsupported", "")
	}
	expired := unavailable(checkedAt, "probeFailed", "Claude Code login has expired. Open Claude Code to renew it.")
	if credentials.Login.ExpiresAt > 0 && time.UnixMilli(credentials.Login.ExpiresAt).Before(time.Now()) {
		return expired
	}
	endpoint := claudeUsageEndpoint
	if options.ClaudeEndpoint != "" {
		endpoint = options.ClaudeEndpoint
	}
	deadline, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	body, status, err := options.request(deadline, http.MethodGet, endpoint, credentials.Login.AccessToken, map[string]string{"anthropic-beta": "oauth-2025-04-20", "Accept": "application/json"})
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return expired
	}
	if err != nil {
		return unavailable(checkedAt, "probeFailed", "Claude could not read usage limits.")
	}
	limits, err := mapHubClaude(body, checkedAt)
	if err != nil {
		return unavailable(checkedAt, "probeFailed", "Claude returned invalid usage limits.")
	}
	return limits
}

// PreferClaudeLogin replaces an unavailable CLI result with login-based limits when they are more informative.
//
// Successful login limits always win. A login failure replaces only an
// "unsupported" CLI answer, so a genuine CLI probe failure keeps its message.
func PreferClaudeLogin(cli, login Limits) Limits {
	if cli.Unavailable == nil || login.Unavailable == nil {
		if login.Unavailable == nil {
			return login
		}
		return cli
	}
	if cli.Unavailable.Reason == "unsupported" && login.Unavailable.Reason == "probeFailed" {
		return login
	}
	return cli
}
