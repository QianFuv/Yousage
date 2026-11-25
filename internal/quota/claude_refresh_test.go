package quota

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// claudeRefreshFixture serves the OAuth token and usage endpoints for one stored login.
type claudeRefreshFixture struct {
	path          string
	options       HttpOptions
	refreshBody   map[string]string
	authorization string
	isUsageCalled bool
}

// newClaudeRefreshFixture stores a login expiring at expiresAt and answers refreshes with status and response.
func newClaudeRefreshFixture(test *testing.T, expiresAt int64, status int, response string, duringRefresh func(path string)) *claudeRefreshFixture {
	test.Helper()
	fixture := &claudeRefreshFixture{path: filepath.Join(test.TempDir(), ".credentials.json")}
	stored := `{"claudeAiOauth":{"accessToken":"old-access","refreshToken":"old-refresh","expiresAt":` + strconv.FormatInt(expiresAt, 10) + `,"scopes":["user:inference","user:profile"],"subscriptionType":"max"},"other":{"kept":true}}`
	if err := os.WriteFile(fixture.path, []byte(stored), 0o600); err != nil {
		test.Fatal(err)
	}
	tokenServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_ = json.NewDecoder(request.Body).Decode(&fixture.refreshBody)
		if duringRefresh != nil {
			duringRefresh(fixture.path)
		}
		writer.WriteHeader(status)
		writer.Write([]byte(response))
	}))
	usageServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fixture.isUsageCalled, fixture.authorization = true, request.Header.Get("Authorization")
		writer.Write([]byte(`{"five_hour":{"utilization":21.0,"resets_at":"2026-10-06T10:30:00+00:00"}}`))
	}))
	test.Cleanup(tokenServer.Close)
	test.Cleanup(usageServer.Close)
	fixture.options = HttpOptions{ClaudeEndpoint: usageServer.URL, ClaudeTokenEndpoint: tokenServer.URL}
	return fixture
}

// stored reads the login fields Claude Code would see after the reading.
func (fixture *claudeRefreshFixture) stored(test *testing.T) map[string]any {
	test.Helper()
	data, err := os.ReadFile(fixture.path)
	if err != nil {
		test.Fatal(err)
	}
	var document map[string]map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		test.Fatal(err)
	}
	if document["other"]["kept"] != true {
		test.Fatal("unrelated stored data was lost")
	}
	return document["claudeAiOauth"]
}

const claudeRefreshed = `{"access_token":"new-access","refresh_token":"new-refresh","expires_in":28800,"refresh_token_expires_in":2592000,"scope":"user:inference user:profile"}`

// TestClaudeLoginRefreshesBeforeExpiry refreshes a login close to expiry and hands the rotated tokens back to Claude Code.
func TestClaudeLoginRefreshesBeforeExpiry(test *testing.T) {
	fixture := newClaudeRefreshFixture(test, time.Now().Add(5*time.Minute).UnixMilli(), http.StatusOK, claudeRefreshed, nil)
	limits := ReadClaudeLogin(context.Background(), fixture.options, filepath.Dir(fixture.path))
	if limits.Unavailable != nil || len(limits.Windows) != 1 || fixture.authorization != "Bearer new-access" {
		test.Fatalf("refreshed token was not used: %+v %q", limits, fixture.authorization)
	}
	request := fixture.refreshBody
	if request["grant_type"] != "refresh_token" || request["refresh_token"] != "old-refresh" || request["client_id"] != claudeOauthClientId || request["scope"] != "user:inference user:profile" {
		test.Fatalf("unexpected refresh request: %v", request)
	}
	login := fixture.stored(test)
	expiresAt, _ := login["expiresAt"].(float64)
	if login["accessToken"] != "new-access" || login["refreshToken"] != "new-refresh" || login["subscriptionType"] != "max" || time.UnixMilli(int64(expiresAt)).Before(time.Now().Add(7*time.Hour)) || login["refreshTokenExpiresAt"] == nil {
		test.Fatalf("rotated login was not stored: %v", login)
	}
}

// TestClaudeLoginRefreshFailures fails only an expired login and never overwrites a login Claude Code refreshed itself.
func TestClaudeLoginRefreshFailures(test *testing.T) {
	expired := newClaudeRefreshFixture(test, 1, http.StatusBadRequest, `{"error":"invalid_grant"}`, nil)
	limits := ReadClaudeLogin(context.Background(), expired.options, filepath.Dir(expired.path))
	if limits.Unavailable == nil || limits.Unavailable.Message != "Claude Code stored login has expired and could not be refreshed. Sign in again with /login." || expired.isUsageCalled {
		test.Fatalf("rejected refresh of an expired login: %+v", limits)
	}
	if expired.stored(test)["accessToken"] != "old-access" {
		test.Fatal("rejected refresh changed the stored login")
	}

	valid := newClaudeRefreshFixture(test, time.Now().Add(5*time.Minute).UnixMilli(), http.StatusInternalServerError, `{}`, nil)
	limits = ReadClaudeLogin(context.Background(), valid.options, filepath.Dir(valid.path))
	if limits.Unavailable != nil || valid.authorization != "Bearer old-access" || valid.stored(test)["refreshToken"] != "old-refresh" {
		test.Fatalf("still-valid login did not survive a failed refresh: %+v %q", limits, valid.authorization)
	}

	concurrent := newClaudeRefreshFixture(test, 1, http.StatusOK, claudeRefreshed, func(path string) {
		_ = os.WriteFile(path, []byte(`{"claudeAiOauth":{"accessToken":"cli-access","refreshToken":"cli-refresh","expiresAt":1},"other":{"kept":true}}`), 0o600)
	})
	limits = ReadClaudeLogin(context.Background(), concurrent.options, filepath.Dir(concurrent.path))
	if limits.Unavailable != nil || concurrent.authorization != "Bearer new-access" || concurrent.stored(test)["refreshToken"] != "cli-refresh" {
		test.Fatalf("login refreshed by Claude Code was overwritten: %+v", concurrent.stored(test))
	}
}
