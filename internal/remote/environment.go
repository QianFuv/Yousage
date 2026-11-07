// Package remote defines the environment wire types shared by local usage reports.
package remote

import (
	"encoding/json"
	"errors"
	"net"
	"net/url"
)

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
		} else if name == "environmentThemes" || name == "usageLimitSources" {
			return errors.New("invalid environment feature capability")
		}

	}
	*capabilities = result
	return nil
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

// ValidateOrigin enforces a credential-free HTTP(S) origin, allowing plain HTTP only on loopback.
func ValidateOrigin(raw string) error { _, err := origin(raw); return err }
