package quota

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	claudeTokenEndpoint = "https://platform.claude.com/v1/oauth/token"
	// claudeOauthClientId is the OAuth client Claude Code signs in and refreshes with.
	claudeOauthClientId = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	// claudeRefreshMargin covers the background refresh interval, so the stored login never lapses between scans.
	claudeRefreshMargin = 15 * time.Minute
)

var errClaudeLoginChanged = errors.New("claude login changed during refresh")

// claudeStoredLogin is the part of Claude Code's stored OAuth login that reading and refreshing need.
type claudeStoredLogin struct {
	AccessToken  string   `json:"accessToken"`
	RefreshToken string   `json:"refreshToken"`
	ExpiresAt    int64    `json:"expiresAt"`
	Scopes       []string `json:"scopes"`
}

// refreshClaudeLogin exchanges the stored refresh token and writes the new login back for Claude Code.
//
// The refresh token rotates, so the new one must be stored. It returns the new
// access token even when storing fails, because the exchange already happened.
func refreshClaudeLogin(ctx context.Context, options HttpOptions, path string, login claudeStoredLogin) (string, int, error) {
	endpoint := claudeTokenEndpoint
	if options.ClaudeTokenEndpoint != "" {
		endpoint = options.ClaudeTokenEndpoint
	}
	payload := map[string]string{"grant_type": "refresh_token", "refresh_token": login.RefreshToken, "client_id": claudeOauthClientId}
	if len(login.Scopes) > 0 {
		payload["scope"] = strings.Join(login.Scopes, " ")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", 0, err
	}
	deadline, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(deadline, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", 0, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := options.client().Do(request)
	if err != nil {
		return "", 0, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", response.StatusCode, errors.New("claude login refresh failed")
	}
	var token struct {
		AccessToken           string   `json:"access_token"`
		RefreshToken          string   `json:"refresh_token"`
		ExpiresIn             float64  `json:"expires_in"`
		RefreshTokenExpiresIn *float64 `json:"refresh_token_expires_in"`
		Scope                 string   `json:"scope"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&token); err != nil || strings.TrimSpace(token.AccessToken) == "" || token.ExpiresIn <= 0 {
		return "", response.StatusCode, errors.New("invalid claude login refresh response")
	}
	if token.RefreshToken == "" {
		token.RefreshToken = login.RefreshToken
	}
	now := time.Now()
	fields := map[string]any{"accessToken": token.AccessToken, "refreshToken": token.RefreshToken, "expiresAt": now.Add(time.Duration(token.ExpiresIn * float64(time.Second))).UnixMilli()}
	if token.RefreshTokenExpiresIn != nil {
		fields["refreshTokenExpiresAt"] = now.Add(time.Duration(*token.RefreshTokenExpiresIn * float64(time.Second))).UnixMilli()
	}
	if scopes := strings.Fields(token.Scope); len(scopes) > 0 {
		fields["scopes"] = scopes
	}
	_ = storeClaudeLogin(options, path, login.RefreshToken, fields)
	return token.AccessToken, response.StatusCode, nil
}

// storeClaudeLogin replaces only the refreshed fields and keeps everything else Claude Code stored.
//
// A login whose refresh token changed meanwhile was refreshed by Claude Code
// and is left untouched. The file is replaced atomically with owner-only permissions.
func storeClaudeLogin(options HttpOptions, path, previousRefreshToken string, fields map[string]any) error {
	data, err := options.file(path)
	if err != nil {
		return err
	}
	var document map[string]json.RawMessage
	var login map[string]json.RawMessage
	var current string
	if err := json.Unmarshal(data, &document); err != nil {
		return err
	}
	if err := json.Unmarshal(document["claudeAiOauth"], &login); err != nil || login == nil {
		return errors.New("claude login is missing")
	}
	if json.Unmarshal(login["refreshToken"], &current) != nil || current != previousRefreshToken {
		return errClaudeLoginChanged
	}
	for name, value := range fields {
		if login[name], err = json.Marshal(value); err != nil {
			return err
		}
	}
	if document["claudeAiOauth"], err = json.Marshal(login); err != nil {
		return err
	}
	output, err := json.Marshal(document)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".credentials-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if _, err := temporary.Write(output); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporary.Name(), path)
}

// claudeRefreshFailure separates a login that must be signed in again from a transient refresh failure.
func claudeRefreshFailure(status int) string {
	if status == http.StatusBadRequest || status == http.StatusUnauthorized || status == http.StatusForbidden {
		return "Claude Code stored login has expired and could not be refreshed. Sign in again with /login."
	}
	return "Claude Code stored login could not be refreshed. Try again later."
}
