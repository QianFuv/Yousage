// Package sync encrypts desktop snapshots and revocable receipt shares for an opaque relay.
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
	IsEnabled bool          `json:"isEnabled"`
	RelayUrl  string        `json:"relayUrl"`
	SpaceId   string        `json:"spaceId"`
	TimeZone  string        `json:"timeZone"`
	Shares    []ShareRecord `json:"shares"`
}

// ShareRecord retains an intentionally distributable share link locally.
type ShareRecord struct {
	Id          string `json:"id"`
	Url         string `json:"url"`
	Days        int    `json:"days"`
	TtlDays     int    `json:"ttlDays"`
	HideAmounts bool   `json:"hideAmounts"`
	CreatedAt   string `json:"createdAt"`
	ExpiresAt   string `json:"expiresAt"`
}

// ShareView adds QR modules to a persisted share record.
type ShareView struct {
	ShareRecord
	QrModules []string `json:"qrModules"`
}

// Status reports the latest push without credentials or transport details.
type Status struct {
	LastPushAt string `json:"lastPushAt"`
	LastError  string `json:"lastError"`
	IsPushing  bool   `json:"isPushing"`
}

// State is the desktop's public sync settings and upload state.
type State struct {
	IsEnabled        bool        `json:"isEnabled"`
	RelayUrl         string      `json:"relayUrl"`
	HasToken         bool        `json:"hasToken"`
	PairingUrl       string      `json:"pairingUrl"`
	PairingQrModules []string    `json:"pairingQrModules"`
	Status           Status      `json:"status"`
	Shares           []ShareView `json:"shares"`
}

// SaveInput updates settings; an empty token retains the keyring value.
type SaveInput struct {
	IsEnabled bool   `json:"isEnabled"`
	RelayUrl  string `json:"relayUrl"`
	Token     string `json:"token"`
}

// Palette carries the validated desktop colors into a public share.
type Palette struct {
	Appearance string            `json:"appearance"`
	Colors     map[string]string `json:"colors"`
}

// ShareRequest selects a receipt window, lifetime, privacy option and palette.
type ShareRequest struct {
	Days        int     `json:"days"`
	TtlDays     int     `json:"ttlDays"`
	HideAmounts bool    `json:"hideAmounts"`
	TimeZone    string  `json:"timeZone"`
	Palette     Palette `json:"palette"`
}

var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`)
var paletteKeyPattern = regexp.MustCompile(`^--[a-z0-9-]{1,64}$`)

// NormalizeRelayUrl accepts only an HTTP(S) origin and removes its trailing slash.
func NormalizeRelayUrl(value string) (string, error) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Hostname() == "" || parsed.Opaque != "" || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || (parsed.Path != "" && parsed.Path != "/") || parsed.RawPath != "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || strings.Contains(value, "#") {
		return "", errors.New("relay URL must be an HTTP(S) origin without a path, query or fragment")
	}
	parsed.Path = ""
	return parsed.String(), nil
}

// Validate rejects invalid persisted settings, including malformed share records.
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
	for _, record := range settings.Shares {
		if !identifierPattern.MatchString(record.Id) || !validDays(record.Days) || !validTtl(record.TtlDays) {
			return errors.New("invalid saved share")
		}
		if _, err := time.Parse(time.RFC3339, record.CreatedAt); err != nil {
			return errors.New("invalid share creation time")
		}
		if _, err := time.Parse(time.RFC3339, record.ExpiresAt); err != nil {
			return errors.New("invalid share expiry")
		}
		parsed, err := url.Parse(record.Url)
		if err != nil {
			return errors.New("invalid share URL")
		}
		if _, err = NormalizeRelayUrl(parsed.Scheme + "://" + parsed.Host); err != nil || parsed.User != nil || parsed.Path != "/s/"+record.Id || parsed.RawQuery != "" {
			return errors.New("invalid share URL")
		}
		if _, err = decodeKey(parsed.Fragment); err != nil {
			return errors.New("invalid share URL key")
		}
	}
	return nil
}

// Validate enforces the supported periods, lifetimes and safe CSS palette limits.
func (request ShareRequest) Validate() error {
	if !validDays(request.Days) || !validTtl(request.TtlDays) {
		return errors.New("unsupported share period or lifetime")
	}
	if _, err := time.LoadLocation(request.TimeZone); err != nil {
		return errors.New("invalid share time zone")
	}
	return request.Palette.Validate()
}

// Validate restricts palette entries to bounded CSS custom properties and values.
func (palette Palette) Validate() error {
	if (palette.Appearance != "dark" && palette.Appearance != "light") || len(palette.Colors) > 128 {
		return errors.New("invalid share palette")
	}
	for key, value := range palette.Colors {
		if !paletteKeyPattern.MatchString(key) || len([]rune(value)) > 128 || strings.Contains(strings.ToLower(value), "url(") || strings.ContainsAny(value, ";<") {
			return errors.New("invalid share palette color")
		}
	}
	return nil
}

func validDays(days int) bool { return days == 1 || days == 7 || days == 30 || days == 90 }
func validTtl(days int) bool  { return days == 1 || days == 7 || days == 14 || days == 30 }
