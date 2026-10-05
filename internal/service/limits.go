package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"yousage/internal/domain"
	"yousage/internal/managedprocess"
	"yousage/internal/provider/claudeprobe"
	"yousage/internal/quota"
	"yousage/internal/refresh"
	"yousage/internal/remote"
)

type quotaSnapshot struct {
	Error     string              `json:"error,omitempty"`
	Providers []remote.Provider   `json:"providers"`
	Sources   []quota.HubSnapshot `json:"sources"`
}

func (service *Service) quotas(ctx context.Context) (quotaSnapshot, int64, error) {
	value := quotaSnapshot{Providers: []remote.Provider{}, Sources: []quota.HubSnapshot{}}
	revision, err := service.Store.ReadSetting(ctx, "local-quotas", &value)
	return value, revision, err
}
func (service *Service) localConfig(ctx context.Context) (remote.Config, error) {
	configuration, err := service.Configuration(ctx)
	if err != nil {
		return remote.Config{}, err
	}
	quotas, _, err := service.quotas(ctx)
	if err != nil {
		return remote.Config{}, err
	}
	pricing, err := service.Preferences.Pricing(ctx, service.LocalId)
	if err != nil {
		return remote.Config{}, err
	}
	config := remote.Config{Environment: remote.Environment{Id: service.LocalId, Label: "This device", Protocol: 2, Capabilities: remote.Capabilities{"environmentThemes": true, "usageLimitSources": true, "usagePriceOverrides": true, "usageModelAliases": true}}, Providers: []remote.Provider{}, Sources: []quota.HubSnapshot{}, Settings: remote.Settings{Prices: pricing.Value.Overrides, Aliases: pricing.Value.Aliases}}
	byId := map[string]remote.Provider{}
	for _, provider := range quotas.Providers {
		byId[provider.InstanceId] = provider
	}
	for _, target := range configuration.Value.Providers {
		provider := byId[target.Id]
		provider.InstanceId, provider.Driver, provider.DisplayName = target.Id, target.Driver, target.Label
		provider.IsEnabled, provider.IsInstalled = target.IsEnabled, true
		if provider.SlashCommands == nil {
			provider.SlashCommands = []remote.SlashCommand{}
		}
		if provider.Auth.Status == "" {
			provider.Auth.Status = "unknown"
		}
		config.Providers = append(config.Providers, provider)
	}
	for _, target := range configuration.Value.Hubs {
		if target.IsEnabled {
			for _, source := range quotas.Sources {
				if source.Id == target.Id {
					config.Sources = append(config.Sources, source)
				}
			}
		}
	}
	return config, nil
}
func (service *Service) nativeOptions(target NativeProvider) quota.NativeOptions {
	environment := []string{}
	for name, value := range service.Environment {
		if (target.Driver == "codex" && name == "CODEX_HOME" && target.Home != "") || (target.Driver == "claudeAgent" && name == "CLAUDE_CONFIG_DIR" && target.Home != "") {
			continue
		}
		environment = append(environment, name+"="+value)
	}
	if target.Home != "" {
		name := "CODEX_HOME"
		if target.Driver == "claudeAgent" {
			name = "CLAUDE_CONFIG_DIR"
		}
		environment = append(environment, name+"="+target.Home)
	}
	return quota.NativeOptions{Process: managedprocess.Options{Executable: target.Executable, Environment: environment}, IsEnabled: target.IsEnabled, Version: "0.1.0"}
}

// RefreshLimits applies completion-based cooldown and serializes probes with credit redemption.
func (service *Service) RefreshLimits(ctx context.Context, selected []string, request refresh.Request) error {
	configuration, err := service.Configuration(ctx)
	if err != nil {
		return err
	}
	identities := map[string]bool{}
	for _, identity := range selected {
		identities[identity] = true
	}
	isSelected := func(identity string) bool { return selected == nil || identities[identity] }
	operations := map[string]func(context.Context) (bool, error){}
	if isSelected(service.LocalId) {
		operations["local"] = service.scanLimits
	}
	for _, preference := range configuration.Value.Connections {
		identity := preference.Connection.EnvironmentId
		if !preference.IsEnabled || !isSelected(identity) {
			continue
		}
		operations["remote:"+identity] = func(owner context.Context) (bool, error) {
			err := service.gate.WithRefresh(owner, identity, func() error {
				service.mutex.Lock()
				state := service.connections[identity]
				var client *remote.Client
				if state != nil {
					client = state.client
				}
				service.mutex.Unlock()
				if client == nil || !client.Authorization.CanOperate() {
					return errors.New("Limits refresh requires a connected environment with operate permission.")
				}
				deadline, cancel := context.WithTimeout(owner, 30*time.Second)
				defer cancel()
				_, err := client.Call(deadline, "server.refreshProviders", map[string]any{}, true)
				return err
			})
			return err == nil, err
		}
	}
	failures := make(chan error, len(operations))
	var pending sync.WaitGroup
	for identity, operation := range operations {
		pending.Add(1)
		go func() {
			defer pending.Done()
			_, err := service.limits.Run(ctx, identity, request, operation)
			if err != nil && !errors.Is(err, refresh.ErrSkipped) {
				failures <- err
			}
		}()
	}
	pending.Wait()
	close(failures)
	joined := []error{}
	for err := range failures {
		joined = append(joined, err)
	}
	service.emit("usage-invalidated", nil)
	return errors.Join(joined...)
}
func (service *Service) scanLimits(ctx context.Context) (bool, error) {
	err := service.gate.WithRefresh(ctx, "local-quotas", func() error {
		configuration, err := service.Configuration(ctx)
		if err != nil {
			return err
		}
		previous, revision, err := service.quotas(ctx)
		if err != nil {
			return err
		}
		next := quotaSnapshot{Providers: []remote.Provider{}, Sources: []quota.HubSnapshot{}}
		previousProviders := map[string]remote.Provider{}
		for _, provider := range previous.Providers {
			previousProviders[provider.InstanceId] = provider
		}
		providers := make([]remote.Provider, len(configuration.Value.Providers))
		notices := make([]string, len(providers))
		var workers sync.WaitGroup
		for index, target := range configuration.Value.Providers {
			workers.Add(1)
			go func() {
				defer workers.Done()
				provider := remote.Provider{InstanceId: target.Id, Driver: target.Driver, DisplayName: target.Label, IsEnabled: target.IsEnabled, IsInstalled: true, Status: "ready"}
				provider.Auth.Status = "unknown"
				if !target.IsEnabled {
					providers[index] = provider
					return
				}
				options := service.nativeOptions(target)
				httpOptions := quota.HttpOptions{Environment: service.Environment, Home: service.Home, Platform: service.Platform, IsEnabled: true, IsKeychainAllowed: configuration.Value.IsCursorKeychainEnabled, KeychainToken: service.keychain.Read}
				var account quota.Account
				switch target.Driver {
				case "codex":
					account = quota.ProbeCodex(ctx, options)
				case "claudeAgent":
					account = quota.ProbeClaude(ctx, claudeprobe.Options{Executable: target.Executable, Environment: options.Process.Environment, ConfigDirectory: target.Home}, true)
				case "cursor":
					account.Limits = quota.ReadCursor(ctx, httpOptions)
				case "grok":
					account = quota.ReadGrok(ctx, httpOptions)
				case "opencode":
					account.Limits = quota.ReadOpenCode(ctx, httpOptions)
				case "antigravity":
					account.Limits = quota.AntigravityLimits(service.Now())
				}
				old := previousProviders[target.Id]
				isProbeFailed := account.Limits.Unavailable != nil && account.Limits.Unavailable.Reason == "probeFailed"
				if isProbeFailed {
					notices[index] = target.Label + ": " + account.Limits.Unavailable.Message + " Last successful limits are retained."
				}
				account.Limits = quota.ResolveProbe(old.Limits, account.Limits)
				if account.Email == "" && isProbeFailed {
					account.Email = old.Auth.Email
					account.Plan = old.Auth.Label
				}
				provider.Limits = &account.Limits
				provider.Auth.Email, provider.Auth.Label = account.Email, account.Plan
				if account.Limits.Unavailable == nil {
					provider.Auth.Status = "authenticated"
				}
				providers[index] = provider
			}()
		}
		workers.Wait()
		next.Providers = providers
		for _, notice := range notices {
			if notice != "" {
				next.Error = strings.TrimSpace(next.Error + " " + notice)
			}
		}
		hubClient := quota.HubClient{Key: service.Secrets.Get}
		for _, target := range configuration.Value.Hubs {
			if !target.IsEnabled {
				continue
			}
			snapshot := hubClient.Read(ctx, target)
			if snapshot.Error != "" {
				for _, old := range previous.Sources {
					if old.Id == target.Id {
						snapshot.Accounts = old.Accounts
					}
				}
			}
			next.Sources = append(next.Sources, snapshot)
		}
		_, err = service.Store.WriteSetting(ctx, "local-quotas", revision, next)
		return err
	})
	return err == nil, err
}

// ConsumeReset persists an unknown recovery marker before any external credit operation.
func (service *Service) ConsumeReset(ctx context.Context, route quota.RedeemRoute, operationId string, isExplicitRetry bool) (quota.ResetResult, error) {
	owned, done, err := service.ownOperation(ctx)
	if err != nil {
		return quota.ResetResult{Outcome: "failed", Warning: "Application is closing."}, err
	}
	defer done()
	ctx = owned

	if operationId == "" || len(operationId) > 128 {
		return quota.ResetResult{}, errors.New("invalid reset operation identity")
	}
	gateKey := route.EnvironmentId
	if gateKey == service.LocalId {
		gateKey = "local-quotas"
	}
	return service.gate.Reset(ctx, gateKey, operationId, isExplicitRetry, func() quota.ResetResult {
		key := "reset-operation:" + domain.SourceFingerprint{HostId: route.EnvironmentId, Provider: domain.Codex, ResolvedHomePath: fmt.Sprintf("%s:%s:%s:%s:%s", route.InstanceId, route.SourceId, route.AccountId, route.CreditId, operationId)}.Id()
		var previous quota.ResetResult
		revision, err := service.Store.ReadSetting(ctx, key, &previous)
		if err != nil {
			return quota.ResetResult{Outcome: "failed", Warning: "Reset recovery state could not be read."}
		}
		if revision > 0 && ((previous.Outcome != "failed" && previous.Outcome != "unknown") || !isExplicitRetry) {
			return previous
		}
		marker := quota.ResetResult{Outcome: "unknown", OperationId: operationId, Warning: "A previous reset may have been sent. Refresh limits before explicitly retrying."}
		revision, err = service.Store.WriteSetting(ctx, key, revision, marker)
		if err != nil {
			return quota.ResetResult{Outcome: "failed", Warning: "Reset recovery state could not be saved. No credit was sent."}
		}
		result := quota.ResetResult{Outcome: "failed", OperationId: operationId, Warning: "The reset target is unavailable."}
		if route.EnvironmentId == service.LocalId {
			configuration, err := service.Configuration(ctx)
			if err == nil {
				if route.SourceId != "" {
					for _, target := range configuration.Value.Hubs {
						if target.Id == route.SourceId && target.IsEnabled {
							hub := quota.HubClient{Key: service.Secrets.Get}
							result = hub.Consume(ctx, target, route.AccountId, route.CreditId)
						}
					}
				} else {
					quotas, _, _ := service.quotas(ctx)
					for _, target := range configuration.Value.Providers {
						if target.Id == route.InstanceId && target.Driver == "codex" && target.IsEnabled {
							options := service.nativeOptions(target)
							for _, provider := range quotas.Providers {
								if provider.InstanceId == target.Id {
									options.ExpectedAccountEmail = provider.Auth.Email
								}
							}
							result = quota.ConsumeCodex(ctx, options, operationId)
						}
					}
				}
			}
		} else {
			service.mutex.Lock()
			state := service.connections[route.EnvironmentId]
			var client *remote.Client
			if state != nil {
				client = state.client
			}
			service.mutex.Unlock()
			if client != nil {
				input := map[string]string{}
				if route.SourceId != "" {
					input["sourceId"], input["accountId"], input["creditId"] = route.SourceId, route.AccountId, route.CreditId
				} else {
					input["instanceId"] = route.InstanceId
				}
				var err error
				deadline, cancel := context.WithTimeout(ctx, 30*time.Second)
				result, err = client.ConsumeResetCredit(deadline, input)
				cancel()
				if err != nil {
					result = quota.ResetResult{Outcome: "failed", Warning: err.Error()}
				}
			}
		}
		result.OperationId = operationId
		if _, err = service.Store.WriteSetting(ctx, key, revision, result); err != nil {
			result.Warning = strings.TrimSpace(result.Warning + " The final outcome could not be stored; refresh limits before retrying.")
		}
		service.emit("usage-invalidated", nil)
		return result
	})
}
