package remote

import (
	"encoding/json"
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
	DefaultTheme      string `json:"defaultTheme"`
	DefaultThemeSetAt string `json:"defaultThemeSetAt"`
}
type Config struct {
	Environment Environment         `json:"environment"`
	Providers   []Provider          `json:"providers"`
	Settings    Settings            `json:"settings"`
	Themes      []theme.Definition  `json:"environmentThemes"`
	Sources     []quota.HubSnapshot `json:"usageLimitSources"`
}
