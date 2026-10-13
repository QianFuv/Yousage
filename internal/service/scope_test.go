package service

import (
	"context"
	"testing"
	"yousage/internal/domain"
	"yousage/internal/preferences"
)

// TestScopeSeedsFixedQuotaAccountsWithDriverLabels keeps Codex and Claude Code present and named by driver.
func TestScopeSeedsFixedQuotaAccountsWithDriverLabels(test *testing.T) {
	service := testService(test)
	service.Scope = []domain.Provider{domain.Codex, domain.Claude}
	configuration, err := service.Configuration(context.Background())
	if err != nil {
		test.Fatal(err)
	}
	providers := configuration.Value.Providers
	if len(providers) != 2 || providers[0].Driver != "codex" || providers[1].Driver != "claudeAgent" || !providers[0].IsEnabled || !providers[1].IsEnabled {
		test.Fatalf("default quota accounts missing: %+v", providers)
	}
	stored := []NativeProvider{{Id: "kept", Driver: "claudeAgent", Label: "Codex", Home: "C:\\claude", IsEnabled: false}, {Id: "other", Driver: "grok", Label: "Grok"}}
	providers = service.scopedProviders(stored)
	if len(providers) != 2 || providers[1].Id != "kept" || providers[1].Label != "Claude Code" || providers[1].IsEnabled || providers[1].Home != "C:\\claude" {
		test.Fatalf("saved Claude account lost or mislabelled: %+v", providers)
	}
	configuration.Value.Providers = append(configuration.Value.Providers, NativeProvider{Id: "grok", Driver: "grok", Label: "Grok"})
	if _, err := service.SaveConfiguration(context.Background(), configuration); err == nil {
		test.Fatal("an unmonitored quota driver was saved")
	}
}

// TestScopeFiltersHistorySources hides unmonitored providers while an empty scope keeps every source.
func TestScopeFiltersHistorySources(test *testing.T) {
	service := testService(test)
	sources := []preferences.Source{{Id: "retired", Provider: domain.Provider("grok")}, {Id: "codex", Provider: domain.Codex}, {Id: "codex-2", Provider: domain.Codex}, {Id: "claude", Provider: domain.Claude}}
	if len(service.scopedSources(sources)) != 4 {
		test.Fatal("an empty scope filtered sources")
	}
	service.Scope = []domain.Provider{domain.Codex, domain.Claude}
	scoped := service.scopedSources(sources)
	if len(scoped) != 2 || scoped[0].Id != "codex" || scoped[1].Id != "claude" {
		test.Fatalf("history scope not applied: %+v", scoped)
	}
	if !service.isProviderInScope(domain.Claude) || service.isProviderInScope(domain.Provider("grok")) || service.isDriverInScope("cursor") || !service.isDriverInScope("claudeAgent") {
		test.Fatal("scope membership is wrong")
	}
}
