package remote

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
	"yousage/internal/domain"
	"yousage/internal/quota"
	"yousage/internal/theme"
)

type Provider struct {
	InstanceId   string `json:"instanceId"`
	Driver       string `json:"driver"`
	DisplayName  string `json:"displayName"`
	IsEnabled    bool   `json:"enabled"`
	IsInstalled  bool   `json:"installed"`
	Availability string `json:"availability,omitempty"`
	Status       string `json:"status"`
	Auth         struct {
		Status              string          `json:"status"`
		Email               string          `json:"email,omitempty"`
		Label               string          `json:"label,omitempty"`
		SubscriptionSharing json.RawMessage `json:"subscriptionSharing,omitempty"`
		WorkspaceId         string          `json:"workspaceId,omitempty"`
	} `json:"auth"`
	Limits             *quota.Limits  `json:"usageLimits,omitempty"`
	SlashCommands      []SlashCommand `json:"slashCommands"`
	WorkspaceSnapshots []struct {
		SlashCommands []SlashCommand `json:"slashCommands"`
	} `json:"workspaceSnapshots,omitempty"`
}
type SlashCommand struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	ArgumentHint string `json:"argumentHint,omitempty"`
}
type Settings struct {
	DefaultTheme      string                          `json:"defaultTheme"`
	DefaultThemeSetAt string                          `json:"defaultThemeSetAt"`
	Prices            map[string]domain.PriceOverride `json:"usagePriceOverrides"`
	Aliases           map[string]string               `json:"usageModelAliases"`
}
type Config struct {
	Environment Environment         `json:"environment"`
	Providers   []Provider          `json:"providers"`
	Settings    Settings            `json:"settings"`
	Themes      []theme.Definition  `json:"environmentThemes"`
	Sources     []quota.HubSnapshot `json:"usageLimitSources"`
}
type Projection struct {
	Config  Config `json:"config"`
	IsReady bool   `json:"ready"`
	IsLive  bool   `json:"live"`
}

var publishedIdentity = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,47}$`)

func decodeSettings(raw json.RawMessage) (Settings, error) {
	value := Settings{}
	if json.Unmarshal(raw, &value) != nil || string(raw) == "null" {
		return value, errors.New("invalid environment settings")
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	if fields == nil {
		return value, errors.New("invalid environment settings")
	}
	if value.Prices == nil {
		value.Prices = map[string]domain.PriceOverride{}
	}
	if value.Aliases == nil {
		value.Aliases = map[string]string{}
	}
	var priceFields map[string]json.RawMessage
	if prices, exists := fields["usagePriceOverrides"]; exists {
		if string(prices) == "null" || json.Unmarshal(prices, &priceFields) != nil {
			return value, errors.New("invalid environment prices")
		}
	}
	for model, price := range value.Prices {
		if model == "" || strings.TrimSpace(model) != model || !validPriceFields(priceFields[model]) || price.Validate() != nil {
			return value, errors.New("invalid environment prices")
		}
	}
	if aliases, exists := fields["usageModelAliases"]; exists && string(aliases) == "null" {
		return value, errors.New("invalid environment model mappings")
	}
	for model, target := range value.Aliases {
		if model == "" || target == "" || strings.TrimSpace(model) != model || strings.TrimSpace(target) != target {
			return value, errors.New("invalid environment model mappings")
		}
	}
	return value, nil
}

func decodeProviders(raw json.RawMessage) ([]Provider, error) {
	var entries []json.RawMessage
	if json.Unmarshal(raw, &entries) != nil || entries == nil {
		return nil, errors.New("invalid provider configuration")
	}
	providers := []Provider{}
	for _, entry := range entries {
		var provider Provider
		if json.Unmarshal(entry, &provider) != nil || !hasFields(entry, "instanceId", "driver", "enabled", "installed", "auth") || strings.TrimSpace(provider.InstanceId) == "" || strings.TrimSpace(provider.Driver) == "" {
			continue
		}
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(entry, &fields)
		if !stringField(fields["instanceId"]) || !stringField(fields["driver"]) || (string(fields["enabled"]) != "true" && string(fields["enabled"]) != "false") || (string(fields["installed"]) != "true" && string(fields["installed"]) != "false") {
			continue
		}
		if limits, exists := fields["usageLimits"]; exists {
			validated, err := decodeLimits(limits)
			if err != nil {
				continue
			}
			provider.Limits = &validated
		}
		if provider.SlashCommands == nil {
			provider.SlashCommands = []SlashCommand{}
		}
		providers = append(providers, provider)
	}
	return providers, nil
}
func decodeLimits(raw json.RawMessage) (quota.Limits, error) {
	var value quota.Limits
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || !hasFields(raw, "checkedAt", "windows") || !validInstant(fields["checkedAt"]) {
		return value, errors.New("invalid limits")
	}
	var windows []json.RawMessage
	if json.Unmarshal(fields["windows"], &windows) != nil || windows == nil {
		return value, errors.New("invalid limit windows")
	}
	fields["windows"] = json.RawMessage(`[]`)
	base, _ := json.Marshal(fields)
	if json.Unmarshal(base, &value) != nil {
		return value, errors.New("invalid limit metadata")
	}
	for _, entry := range windows {
		var window quota.Window
		var windowFields map[string]json.RawMessage
		_ = json.Unmarshal(entry, &windowFields)
		if json.Unmarshal(entry, &window) != nil || !hasFields(entry, "id", "kind", "label", "usedPercent") || !numericFields(windowFields, "usedPercent") || window.Id == "" || window.Label == "" || !domain.Finite(window.UsedPercent) || window.UsedPercent < 0 || window.UsedPercent > 100 {
			continue
		}
		if window.Kind != "session" && window.Kind != "weekly" && window.Kind != "monthly" && window.Kind != "other" {
			continue
		}
		if value, exists := windowFields["resetsAt"]; exists && !validInstant(value) {
			continue
		}
		if window.WindowDurationMins != nil && *window.WindowDurationMins < 0 {
			continue
		}
		value.Windows = append(value.Windows, window)
	}
	return value, nil
}
func decodeSources(raw json.RawMessage) ([]quota.HubSnapshot, error) {
	var entries []json.RawMessage
	if json.Unmarshal(raw, &entries) != nil || entries == nil {
		return nil, errors.New("invalid quota source event")
	}
	result := []quota.HubSnapshot{}
	for _, entry := range entries {
		var fields map[string]json.RawMessage
		if json.Unmarshal(entry, &fields) != nil || !hasFields(entry, "id", "kind", "label", "checkedAt", "accounts") {
			continue
		}
		var accounts []json.RawMessage
		if json.Unmarshal(fields["accounts"], &accounts) != nil || accounts == nil {
			continue
		}
		fields["accounts"] = json.RawMessage(`[]`)
		base, _ := json.Marshal(fields)
		var source quota.HubSnapshot
		if json.Unmarshal(base, &source) != nil || source.Id == "" || source.Kind != "cliproxy" {
			continue
		}
		for _, rawAccount := range accounts {
			var account quota.HubAccount
			if json.Unmarshal(rawAccount, &account) != nil || account.Id == "" || (account.Driver != "codex" && account.Driver != "claudeAgent") {
				continue
			}
			var accountFields map[string]json.RawMessage
			_ = json.Unmarshal(rawAccount, &accountFields)
			limits, err := decodeLimits(accountFields["usageLimits"])
			if err != nil {
				continue
			}
			account.Limits = limits
			source.Accounts = append(source.Accounts, account)
		}
		result = append(result, source)
	}
	return result, nil
}

func decodeConfig(raw json.RawMessage, expectedId string) (Config, error) {
	var config Config
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || !hasFields(raw, "environment", "providers", "settings") || json.Unmarshal(fields["environment"], &config.Environment) != nil || config.Environment.Id != expectedId || config.Environment.Protocol != protocolVersion {
		return Config{}, errors.New("invalid environment configuration")
	}
	var err error
	config.Providers, err = decodeProviders(fields["providers"])
	if err != nil {
		return Config{}, err
	}
	config.Settings, err = decodeSettings(fields["settings"])
	if err != nil {
		return Config{}, err
	}
	if config.Settings.Prices == nil {
		config.Settings.Prices = map[string]domain.PriceOverride{}
	}
	if config.Settings.Aliases == nil {
		config.Settings.Aliases = map[string]string{}
	}
	config.Themes, config.Sources = []theme.Definition{}, []quota.HubSnapshot{}
	return config, nil
}

// Apply consumes whole-field replacements only after a verified initial snapshot.
func (projection *Projection) Apply(raw json.RawMessage, expectedId string) error {
	var event struct {
		Version int             `json:"version"`
		Type    string          `json:"type"`
		Config  json.RawMessage `json:"config"`
		Payload json.RawMessage `json:"payload"`
	}
	if json.Unmarshal(raw, &event) != nil || event.Version != 1 {
		return errors.New("invalid environment configuration event")
	}
	next := *projection
	if event.Type == "snapshot" {
		config, err := decodeConfig(event.Config, expectedId)
		if err != nil {
			return err
		}
		if projection.IsReady && projection.Config.Environment.Id == expectedId {
			if config.Environment.Capabilities["environmentThemes"] {
				config.Themes = projection.Config.Themes
			}
			if config.Environment.Capabilities["usageLimitSources"] {
				config.Sources = projection.Config.Sources
			}
		}
		next.Config, next.IsReady, next.IsLive = config, true, true
	} else {
		if !projection.IsReady || !projection.IsLive {
			return nil
		}
		switch event.Type {
		case "providerStatuses":
			var payload map[string]json.RawMessage
			if json.Unmarshal(event.Payload, &payload) != nil {
				return errors.New("invalid provider event")
			}
			providers, err := decodeProviders(payload["providers"])
			if err != nil {
				return err
			}
			next.Config.Providers = providers
		case "settingsUpdated":
			var payload map[string]json.RawMessage
			if json.Unmarshal(event.Payload, &payload) != nil {
				return errors.New("invalid settings event")
			}
			settings, err := decodeSettings(payload["settings"])
			if err != nil {
				return err
			}
			next.Config.Settings = settings
			if next.Config.Settings.Prices == nil {
				next.Config.Settings.Prices = map[string]domain.PriceOverride{}
			}
			if next.Config.Settings.Aliases == nil {
				next.Config.Settings.Aliases = map[string]string{}
			}
		case "environmentThemesUpdated":
			var payload struct {
				Themes []json.RawMessage `json:"themes"`
			}
			if json.Unmarshal(event.Payload, &payload) != nil || payload.Themes == nil {
				return errors.New("invalid published theme event")
			}
			if next.Config.Environment.Capabilities["environmentThemes"] {
				themes := []theme.Definition{}
				for _, entry := range payload.Themes {
					var identity struct {
						Id string `json:"id"`
					}
					if json.Unmarshal(entry, &identity) != nil || !publishedIdentity.MatchString(identity.Id) || identity.Id == "system" || identity.Id == "light" || identity.Id == "dark" {
						continue
					}
					file, err := theme.ParsePublished(entry)
					if err != nil {
						continue
					}
					themes = append(themes, theme.ResolvePublished(identity.Id, file))
				}
				next.Config.Themes = themes
			}
		case "usageLimitSourcesUpdated":
			var payload map[string]json.RawMessage
			if json.Unmarshal(event.Payload, &payload) != nil {
				return errors.New("invalid quota source event")
			}
			sources, err := decodeSources(payload["sources"])
			if err != nil {
				return err
			}
			if next.Config.Environment.Capabilities["usageLimitSources"] {
				next.Config.Sources = sources
			}
		}
	}
	*projection = next
	return nil
}

// Cached removes replay-only published fields and never establishes a live connection.
func (projection Projection) Cached() Projection {
	projection.IsLive = false
	projection.Config.Themes = []theme.Definition{}
	projection.Config.Sources = []quota.HubSnapshot{}
	return projection
}

func (client *Client) Config(ctx context.Context) (Config, error) {
	data, err := client.Call(ctx, "server.getConfig", map[string]any{}, false)
	if err != nil {
		return Config{}, err
	}
	return decodeConfig(data, client.Environment.Id)
}

func validPriceFields(raw json.RawMessage) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return false
	}
	for _, key := range []string{"inputCostPerMillionTokens", "outputCostPerMillionTokens"} {
		value, ok := fields[key]
		if !ok || string(value) == "null" {
			return false
		}
	}
	for _, key := range []string{"cacheReadCostPerMillionTokens", "cacheWriteCostPerMillionTokens"} {
		if value, ok := fields[key]; ok && string(value) == "null" {
			return false
		}
	}
	return true
}

func validInstant(raw json.RawMessage) bool {
	var value string
	if !stringField(raw) || json.Unmarshal(raw, &value) != nil {
		return false
	}
	_, err := time.Parse(time.RFC3339Nano, value)
	return err == nil
}
