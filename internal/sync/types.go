// Package sync encrypts desktop snapshots for an opaque relay.
package sync

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Settings contains the non-secret, versioned sync preferences.
type Settings struct {
	IsEnabled bool   `json:"isEnabled"`
	RelayUrl  string `json:"relayUrl"`
	SpaceId   string `json:"spaceId"`
	TimeZone  string `json:"timeZone"`
}

// Status reports the latest push without credentials or transport details.
type Status struct {
	LastPushAt string `json:"lastPushAt"`
	LastError  string `json:"lastError"`
	IsPushing  bool   `json:"isPushing"`
}

// State is the desktop's public sync settings and upload state.
type State struct {
	IsEnabled  bool   `json:"isEnabled"`
	RelayUrl   string `json:"relayUrl"`
	HasToken   bool   `json:"hasToken"`
	PairingUrl string `json:"pairingUrl"`
	Status     Status `json:"status"`
}

// SaveInput updates settings; an empty token retains the keyring value.
type SaveInput struct {
	IsEnabled bool   `json:"isEnabled"`
	RelayUrl  string `json:"relayUrl"`
	Token     string `json:"token"`
}

var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`)

// NormalizeRelayUrl accepts only an HTTP(S) origin and removes its trailing slash.
func NormalizeRelayUrl(value string) (string, error) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Hostname() == "" || parsed.Opaque != "" || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || (parsed.Path != "" && parsed.Path != "/") || parsed.RawPath != "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || strings.Contains(value, "#") {
		return "", errors.New("relay URL must be an HTTP(S) origin without a path, query or fragment")
	}
	parsed.Path = ""
	return parsed.String(), nil
}

// Validate rejects invalid persisted settings.
func (settings Settings) Validate() error {
	if settings.RelayUrl != "" {
		if _, err := NormalizeRelayUrl(settings.RelayUrl); err != nil {
			return err
		}
	} else if settings.IsEnabled {
		return errors.New("relay URL is required")
	}
	if settings.SpaceId != "" && !identifierPattern.MatchString(settings.SpaceId) {
		return errors.New("invalid space identity")
	}
	if _, err := time.LoadLocation(settings.TimeZone); err != nil {
		return errors.New("invalid sync time zone")
	}
	return nil
}
