package sync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// Client sends bounded relay requests without returning transport errors or response bodies.
type Client struct{ http *http.Client }

// NewClient applies a thirty-second timeout and disables credential-bearing redirects.
func NewClient(client *http.Client) *Client {
	if client == nil {
		client = &http.Client{}
	}
	copy := *client
	if copy.Timeout <= 0 || copy.Timeout > 30*time.Second {
		copy.Timeout = 30 * time.Second
	}
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{http: &copy}
}

func (client *Client) request(ctx context.Context, origin, token, method, path string, body []byte, expectedStatus int, shouldAllowMissing bool, output any) error {
	origin, err := NormalizeRelayUrl(origin)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, origin+path, bytes.NewReader(body))
	if err != nil {
		return errors.New("could not create relay request")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/octet-stream")
	}
	response, err := client.http.Do(request)
	if err != nil {
		return errors.New("relay request failed; check the connection and relay URL")
	}
	defer response.Body.Close()
	if shouldAllowMissing && response.StatusCode == http.StatusNotFound {
		return nil
	}
	if response.StatusCode != expectedStatus {
		return fmt.Errorf("relay returned HTTP %d", response.StatusCode)
	}
	if output != nil && json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(output) != nil {
		return errors.New("relay returned an invalid response")
	}
	return nil
}

// Test checks both relay reachability and owner authorization.
func (client *Client) Test(ctx context.Context, origin, token string) error {
	var health struct {
		Status string `json:"status"`
	}
	if err := client.request(ctx, origin, "", http.MethodGet, "/healthz", nil, http.StatusOK, false, &health); err != nil {
		return err
	}
	if health.Status != "ok" {
		return errors.New("relay returned an invalid health response")
	}
	return client.request(ctx, origin, token, http.MethodGet, "/api/auth", nil, http.StatusNoContent, false, nil)
}

func (client *Client) put(ctx context.Context, origin, token, kind, id string, body []byte) error {
	if !identifierPattern.MatchString(id) {
		return errors.New("invalid relay identity")
	}
	if len(body) > 4<<20 {
		return errors.New("encrypted payload exceeds the relay's 4 MiB limit")
	}
	return client.request(ctx, origin, token, http.MethodPut, "/api/"+kind+"/"+id, body, http.StatusNoContent, false, nil)
}

func (client *Client) delete(ctx context.Context, origin, token, kind, id string) error {
	if !identifierPattern.MatchString(id) {
		return errors.New("invalid relay identity")
	}
	return client.request(ctx, origin, token, http.MethodDelete, "/api/"+kind+"/"+id, nil, http.StatusNoContent, true, nil)
}

type reservation struct {
	Id        string `json:"id"`
	ExpiresAt string `json:"expiresAt"`
}

func (client *Client) reserve(ctx context.Context, origin, token string, ttlDays int) (reservation, error) {
	result := reservation{}
	if !validTtl(ttlDays) {
		return result, errors.New("unsupported share lifetime")
	}
	err := client.request(ctx, origin, token, http.MethodPost, "/api/shares/reserve?ttlDays="+strconv.Itoa(ttlDays), nil, http.StatusCreated, false, &result)
	if err != nil {
		return result, err
	}
	if !identifierPattern.MatchString(result.Id) {
		return reservation{}, errors.New("relay returned an invalid share identity")
	}
	if _, err = time.Parse(time.RFC3339, result.ExpiresAt); err != nil {
		return reservation{}, errors.New("relay returned an invalid share expiry")
	}
	return result, nil
}
