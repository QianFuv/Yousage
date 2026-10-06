package service

import (
	"slices"
	"yousage/internal/domain"
	"yousage/internal/preferences"
)

// scopedDriver describes the native quota account paired with a history provider.
type scopedDriver struct {
	Provider domain.Provider
	Driver   string
	Label    string
}

// ScopedDrivers lists the local providers the desktop application monitors, in display order.
var ScopedDrivers = []scopedDriver{
	{Provider: domain.Codex, Driver: "codex", Label: "Codex"},
	{Provider: domain.Claude, Driver: "claudeAgent", Label: "Claude Code"},
}

// isProviderInScope reports whether history from the provider is monitored; an empty scope monitors all providers.
func (service *Service) isProviderInScope(provider domain.Provider) bool {
	return len(service.Scope) == 0 || slices.Contains(service.Scope, provider)
}

// isDriverInScope reports whether a native quota driver belongs to a monitored provider.
func (service *Service) isDriverInScope(driver string) bool {
	if len(service.Scope) == 0 {
		return true
	}
	for _, entry := range ScopedDrivers {
		if entry.Driver == driver && slices.Contains(service.Scope, entry.Provider) {
			return true
		}
	}
	return false
}

// scopedSources keeps the first history source of each monitored provider.
func (service *Service) scopedSources(sources []preferences.Source) []preferences.Source {
	if len(service.Scope) == 0 {
		return sources
	}
	result := []preferences.Source{}
	seen := map[domain.Provider]bool{}
	for _, source := range sources {
		if service.isProviderInScope(source.Provider) && !seen[source.Provider] {
			seen[source.Provider] = true
			result = append(result, source)
		}
	}
	return result
}

// scopedProviders keeps exactly one quota account per monitored provider, labelled by its driver and enabled by default when absent.
func (service *Service) scopedProviders(providers []NativeProvider) []NativeProvider {
	if len(service.Scope) == 0 {
		return providers
	}
	result := []NativeProvider{}
	for _, entry := range ScopedDrivers {
		if !slices.Contains(service.Scope, entry.Provider) {
			continue
		}
		account := NativeProvider{Id: entry.Driver + "-default", Driver: entry.Driver, IsEnabled: true}
		if index := slices.IndexFunc(providers, func(value NativeProvider) bool { return value.Driver == entry.Driver }); index >= 0 {
			account = providers[index]
		}
		account.Label = entry.Label
		result = append(result, account)
	}
	return result
}
