package quota

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
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
// A login close to expiry is refreshed here and written back instead of waiting
// for Claude Code; a failed refresh only fails the reading once the token expired.
func ReadClaudeLogin(ctx context.Context, options HttpOptions, configDirectory string) Limits {
	checkedAt := checked(time.Now())
	path := filepath.Join(claudeConfigDirectory(options, configDirectory), ".credentials.json")
	data, err := options.file(path)
	if errors.Is(err, os.ErrNotExist) {
		return unavailable(checkedAt, "unsupported", "")
	}
	if err != nil {
		return unavailable(checkedAt, "probeFailed", "Claude Code login could not be read.")
	}
	var credentials *struct {
		Login *claudeStoredLogin `json:"claudeAiOauth"`
	}
	if json.Unmarshal(data, &credentials) != nil || credentials == nil {
		return unavailable(checkedAt, "probeFailed", "Claude Code stored credentials are malformed.")
	}
	if credentials.Login == nil {
		return unavailable(checkedAt, "unsupported", "")
	}
	login := credentials.Login
	if strings.TrimSpace(login.AccessToken) == "" {
		return unavailable(checkedAt, "probeFailed", "Claude Code stored credentials are malformed.")
	}
	accessToken := login.AccessToken
	if login.ExpiresAt > 0 && time.Until(time.UnixMilli(login.ExpiresAt)) < claudeRefreshMargin {
		isExpired := time.UnixMilli(login.ExpiresAt).Before(time.Now())
		if strings.TrimSpace(login.RefreshToken) == "" {
			if isExpired {
				return unavailable(checkedAt, "probeFailed", "Claude Code stored access token has expired; usage limits could not be read.")
			}
		} else if refreshed, status, err := refreshClaudeLogin(ctx, options, path, *login); err == nil {
			accessToken = refreshed
		} else if isExpired {
			return unavailable(checkedAt, "probeFailed", claudeRefreshFailure(status))
		}
	}
	endpoint := claudeUsageEndpoint
	if options.ClaudeEndpoint != "" {
		endpoint = options.ClaudeEndpoint
	}
	deadline, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	body, status, err := options.request(deadline, http.MethodGet, endpoint, accessToken, map[string]string{"anthropic-beta": "oauth-2025-04-20", "Accept": "application/json"})
	if err != nil {
		return unavailable(checkedAt, "probeFailed", claudeUsageFailure(status, err))
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || (fields["five_hour"] == nil && fields["seven_day"] == nil && fields["limits"] == nil) {
		return unavailable(checkedAt, "probeFailed", "Claude returned invalid usage limits.")
	}
	limits, err := mapHubClaude(body, checkedAt)
	if err != nil {
		return unavailable(checkedAt, "probeFailed", "Claude returned invalid usage limits.")
	}
	return limits
}

// claudeUsageFailure describes only evidence from the quota request, never the CLI's overall login state.
func claudeUsageFailure(status int, err error) string {
	switch {
	case status == http.StatusUnauthorized:
		return "Claude usage API rejected the stored credential (HTTP 401)."
	case status == http.StatusForbidden:
		return "Claude usage API denied access to usage limits (HTTP 403)."
	case status == http.StatusTooManyRequests:
		return "Claude usage API rate limit reached (HTTP 429). Try again later."
	case status >= 500 && status <= 599:
		return "Claude usage API is temporarily unavailable (HTTP 5xx). Try again later."
	case status != 0 && (status < 200 || status >= 300):
		return "Claude usage API returned an unexpected HTTP status."
	}
	var networkError net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &networkError) && networkError.Timeout()) {
		return "Claude usage API request timed out."
	}
	if errors.Is(err, context.Canceled) {
		return "Claude usage API request was cancelled."
	}
	var syntaxError *json.SyntaxError
	if errors.As(err, &syntaxError) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, errQuotaTrailingJSON) {
		return "Claude usage API returned invalid JSON."
	}
	return "Claude usage API could not be reached or read because of a network error."
}

// ReadClaudeOrganization names the organization the local Claude Code login is signed in to.
//
// The CLI keeps this account record beside its settings when a configuration
// directory is set, or in the home directory otherwise. One email can belong
// to several organizations, each with its own quota.
func ReadClaudeOrganization(options HttpOptions, configDirectory string) string {
	directory := strings.TrimSpace(configDirectory)
	if directory == "" {
		directory = strings.TrimSpace(options.Environment["CLAUDE_CONFIG_DIR"])
	}
	path := filepath.Join(options.userHome(), ".claude.json")
	if directory != "" {
		path = filepath.Join(directory, ".claude.json")
	}
	data, err := options.file(path)
	if err != nil {
		return ""
	}
	var config struct {
		Account *struct {
			OrganizationUuid string `json:"organizationUuid"`
		} `json:"oauthAccount"`
	}
	if json.Unmarshal(data, &config) != nil || config.Account == nil {
		return ""
	}
	return strings.TrimSpace(config.Account.OrganizationUuid)
}

// PreferClaudeLogin replaces an unavailable CLI result with login- or token-based limits when they are more informative.
//
// Successful fallback limits always win. A fallback failure replaces only an
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
