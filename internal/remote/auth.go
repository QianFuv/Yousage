// Package remote connects to the pinned T3 authenticated Effect RPC boundary.
package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"yousage/internal/domain"
	"yousage/internal/secrets"
)

const protocolVersion = 2

type Environment struct {
	Id            string       `json:"environmentId"`
	Label         string       `json:"label"`
	ServerVersion string       `json:"serverVersion"`
	Protocol      int          `json:"orchestrationProtocolVersion"`
	Capabilities  Capabilities `json:"capabilities"`
	Platform      struct {
		Os   string `json:"os"`
		Arch string `json:"arch"`
	} `json:"platform"`
}

// Capabilities preserves boolean feature gates while ignoring unrelated structured capabilities.
type Capabilities map[string]bool

func (capabilities *Capabilities) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return errors.New("invalid environment capabilities")
	}
	result := Capabilities{}
	for name, raw := range fields {
		var enabled bool
		if string(raw) != "null" && json.Unmarshal(raw, &enabled) == nil {
			result[name] = enabled
		} else if name == "environmentThemes" || name == "usageLimitSources" || name == "usagePriceOverrides" || name == "usageModelAliases" {
			return errors.New("invalid environment feature capability")
		}

	}
	*capabilities = result
	return nil
}

type Connection struct {
	Url           string `json:"url"`
	EnvironmentId string `json:"environmentId"`
	CredentialId  string `json:"credentialId"`
}
type Authorization struct {
	IsAuthenticated bool            `json:"authenticated"`
	Auth            *AuthDescriptor `json:"auth"`
	Scopes          []string        `json:"scopes"`
	ExpiresAt       string          `json:"expiresAt,omitempty"`
}
type AuthDescriptor struct {
	Policy            string   `json:"policy"`
	BootstrapMethods  []string `json:"bootstrapMethods"`
	SessionMethods    []string `json:"sessionMethods"`
	SessionCookieName string   `json:"sessionCookieName"`
}

func decodeAuthorization(data json.RawMessage) (Authorization, error) {
	result := Authorization{}
	if json.Unmarshal(data, &result) != nil || !hasFields(data, "authenticated", "auth") || result.Auth == nil {
		return result, errors.New("invalid environment authorization state")
	}
	descriptor := result.Auth
	if descriptor.Policy != "desktop-managed-local" && descriptor.Policy != "loopback-browser" && descriptor.Policy != "remote-reachable" && descriptor.Policy != "unsafe-no-auth" {
		return result, errors.New("unsupported environment authorization policy")
	}
	if descriptor.BootstrapMethods == nil || descriptor.SessionMethods == nil || strings.TrimSpace(descriptor.SessionCookieName) == "" {
		return result, errors.New("invalid environment authorization descriptor")
	}
	for _, method := range descriptor.BootstrapMethods {
		if method != "desktop-bootstrap" && method != "one-time-token" {
			return result, errors.New("unsupported environment bootstrap method")
		}
	}
	for _, method := range descriptor.SessionMethods {
		if method != "browser-session-cookie" && method != "bearer-access-token" && method != "dpop-access-token" {
			return result, errors.New("unsupported environment session method")
		}
	}
	if result.ExpiresAt != "" {
		if _, err := time.Parse(time.RFC3339Nano, result.ExpiresAt); err != nil {
			return result, errors.New("invalid environment session expiry")
		}
	}
	return result, nil
}

func (authorization Authorization) CanOperate() bool {
	for _, scope := range authorization.Scopes {
		if scope == "orchestration:operate" {
			return authorization.IsAuthenticated
		}
	}
	return false
}

type Connector struct {
	HttpClient *http.Client
	Secrets    secrets.Store
}

func origin(raw string) (*url.URL, error) {
	value, err := url.Parse(raw)
	if err != nil || value.Host == "" || value.User != nil || value.RawQuery != "" || value.Fragment != "" || (value.Path != "" && value.Path != "/") {
		return nil, errors.New("enter the environment HTTP origin without credentials or a path")
	}
	if value.Scheme != "http" && value.Scheme != "https" {
		return nil, errors.New("environment URL must use HTTP or HTTPS")
	}
	if value.Scheme == "http" {
		address := net.ParseIP(value.Hostname())
		if value.Hostname() != "localhost" && (address == nil || !address.IsLoopback()) {
			return nil, errors.New("remote environments require HTTPS")
		}
	}
	return value, nil
}
func (connector Connector) client() *http.Client {
	client := http.Client{Timeout: 15 * time.Second}
	if connector.HttpClient != nil {
		client = *connector.HttpClient
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("environment redirects are not allowed") }
	return &client
}
func (connector Connector) request(ctx context.Context, base *url.URL, method, path, token, contentType string, body []byte, result any) error {
	endpoint := base.ResolveReference(&url.URL{Path: path})
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return errors.New("environment request could not be created")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := connector.client().Do(request)
	if err != nil {
		return errors.New("environment is unreachable")
	}
	defer response.Body.Close()
	if response.StatusCode == 401 || response.StatusCode == 403 {
		return errors.New("environment authorization failed; pair again")
	}
	if response.StatusCode == 426 {
		return errors.New("environment orchestration protocol is incompatible")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return errors.New("environment request failed")
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 4<<20))
	if decoder.Decode(result) != nil {
		return errors.New("environment response is invalid")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return errors.New("environment response contains invalid trailing data")
	}
	return nil
}

// Discover validates identity and wire version before any one-time credential exchange.
func (connector Connector) Discover(ctx context.Context, connection Connection) (Environment, error) {
	environment := Environment{}
	base, err := origin(connection.Url)
	if err != nil {
		return environment, err
	}
	if err = connector.request(ctx, base, http.MethodGet, "/.well-known/t3/environment", "", "", nil, &environment); err != nil {
		return environment, err
	}
	if strings.TrimSpace(environment.Id) == "" || (connection.EnvironmentId != "" && connection.EnvironmentId != environment.Id) {
		return environment, errors.New("environment identity does not match")
	}
	if environment.Protocol != protocolVersion {
		return environment, errors.New("update client or server to orchestration protocol 2")
	}
	return environment, nil
}

// Pair persists only the returned access token after identity, protocol and token shape validation.
func (connector Connector) Pair(ctx context.Context, connection Connection, bootstrap string, isReadOnly bool) (Environment, error) {
	if strings.TrimSpace(bootstrap) == "" {
		return Environment{}, errors.New("enter the pairing credential")
	}
	environment, err := connector.Discover(ctx, connection)
	if err != nil {
		return environment, err
	}
	if connector.Secrets == nil {
		return environment, errors.New("native credential store is unavailable")
	}
	base, _ := origin(connection.Url)
	scope := "orchestration:read orchestration:operate"
	if isReadOnly {
		scope = "orchestration:read"
	}
	form := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:token-exchange"}, "subject_token": {bootstrap}, "subject_token_type": {"urn:t3:params:oauth:token-type:environment-bootstrap"}, "requested_token_type": {"urn:ietf:params:oauth:token-type:access_token"}, "scope": {scope}}
	var token struct {
		AccessToken     string   `json:"access_token"`
		TokenType       string   `json:"token_type"`
		Scope           string   `json:"scope"`
		IssuedTokenType string   `json:"issued_token_type"`
		ExpiresIn       *float64 `json:"expires_in"`
	}
	if err = connector.request(ctx, base, http.MethodPost, "/oauth/token", "", "application/x-www-form-urlencoded", []byte(form.Encode()), &token); err != nil {
		return environment, err
	}
	if token.TokenType == "DPoP" {
		return environment, errors.New("this connection requires DPoP; use a compatible bearer pairing credential")
	}
	if strings.TrimSpace(token.AccessToken) == "" || token.TokenType != "Bearer" || token.IssuedTokenType != "urn:ietf:params:oauth:token-type:access_token" || token.ExpiresIn == nil || !domain.Finite(*token.ExpiresIn) || strings.TrimSpace(token.Scope) == "" {
		return environment, errors.New("environment returned an invalid access token")
	}
	if err = connector.Secrets.Put(ctx, connection.CredentialId, token.AccessToken); err != nil {
		return environment, err
	}
	return environment, nil
}

func (connector Connector) Connect(ctx context.Context, connection Connection) (*Client, error) {
	if strings.TrimSpace(connection.EnvironmentId) == "" {
		return nil, errors.New("saved environment identity is missing; pair again")
	}
	environment, err := connector.Discover(ctx, connection)
	if err != nil {
		return nil, err
	}
	if connector.Secrets == nil {
		return nil, errors.New("native credential store is unavailable")
	}
	token, err := connector.Secrets.Get(ctx, connection.CredentialId)
	if err != nil {
		return nil, err
	}
	base, _ := origin(connection.Url)
	var authData json.RawMessage
	if err = connector.request(ctx, base, http.MethodGet, "/api/auth/session", token, "", nil, &authData); err != nil {
		return nil, err
	}
	authorization, err := decodeAuthorization(authData)
	if err != nil {
		return nil, err
	}
	if !authorization.IsAuthenticated {
		return nil, errors.New("environment requires pairing")
	}
	var ticket struct {
		Ticket    string `json:"ticket"`
		ExpiresAt string `json:"expiresAt"`
	}
	if err = connector.request(ctx, base, http.MethodPost, "/api/auth/websocket-ticket", token, "application/json", []byte("{}"), &ticket); err != nil {
		return nil, err
	}
	if ticket.Ticket == "" {
		return nil, errors.New("environment returned no websocket ticket")
	}
	if _, err = time.Parse(time.RFC3339Nano, ticket.ExpiresAt); err != nil {
		return nil, errors.New("environment returned an invalid websocket ticket expiry")
	}
	endpoint := base.ResolveReference(&url.URL{Path: "/ws"})
	endpoint.Scheme = "wss"
	if base.Scheme == "http" {
		endpoint.Scheme = "ws"
	}
	endpoint.RawQuery = url.Values{"wsTicket": {ticket.Ticket}, "orchestrationProtocol": {"2"}}.Encode()
	return dial(ctx, endpoint.String(), connector.client(), environment, authorization)
}

// ValidateOrigin exposes the same strict origin rules used for discovery and pairing.
func ValidateOrigin(raw string) error { _, err := origin(raw); return err }
