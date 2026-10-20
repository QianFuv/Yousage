package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"yousage/internal/diagnostics"
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
		if target.Driver == "codex" || target.Driver == "claudeAgent" {
			provider.IsInstalled = service.isNativeInstalled(target)
		}
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
		key := name
		if service.Platform == "windows" {
			key = strings.ToUpper(name)
		}
		if (target.Driver == "codex" && key == "CODEX_HOME" && target.Home != "") || (target.Driver == "claudeAgent" && key == "CLAUDE_CONFIG_DIR" && target.Home != "") {
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
	return quota.NativeOptions{Process: managedprocess.Options{Executable: service.resolveExecutable(target.Driver, target.Executable), Environment: environment}, IsEnabled: target.IsEnabled, Version: "0.1.0"}
}

// deferredUsageInvalidationKey leaves notification to the enclosing limits refresh.
type deferredUsageInvalidationKey struct{}

// RefreshLimits returns after limits work and schedules coalesced local history and rates.
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
		operations["local"] = func(owner context.Context) (bool, error) {
			value, err := service.scanLimits(owner)
			service.startLimitsFollowup(request)
			return value, err
		}
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
			if identity == "local" && errors.Is(err, refresh.ErrSkipped) {
				service.startLimitsFollowup(request)
			}
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
	if !isSelected(service.LocalId) {
		service.emit("usage-invalidated", nil)
	}
	return errors.Join(joined...)
}

// startLimitsFollowup coalesces local scans under the service lifetime.
func (service *Service) startLimitsFollowup(request refresh.Request) {
	service.mutex.Lock()
	if service.limitsFollowup != nil || service.isStopping || service.owner.Err() != nil {
		service.mutex.Unlock()
		return
	}
	done := make(chan struct{})
	service.limitsFollowup = done
	service.workers.Add(1)
	service.pendingWrites++
	service.mutex.Unlock()
	go func() {
		defer service.workers.Done()
		defer func() {
			service.mutex.Lock()
			service.limitsFollowup = nil
			service.pendingWrites--
			close(done)
			service.mutex.Unlock()
		}()
		ctx := context.WithValue(service.owner, deferredUsageInvalidationKey{}, true)
		historyError := service.RefreshHistory(ctx, request)
		service.RefreshRates(ctx, request)
		if ctx.Err() == nil {
			service.recordFollowupFailure(ctx, historyError)
			service.emit("usage-invalidated", nil)
		}
	}()
}

// recordFollowupFailure retains history failures alongside coordinator notices.
func (service *Service) recordFollowupFailure(ctx context.Context, historyError error) {
	if historyError == nil {
		return
	}
	entries, _, err := service.sourceIndex(ctx)
	if err == nil {
		for _, entry := range entries {
			if !entry.IsHidden && service.isProviderInScope(entry.Source.Fingerprint.Provider) {
				_ = service.Store.Observe(ctx, domain.Observation{SourceId: entry.Source.Fingerprint.Id(), Status: "failed", AttemptedAt: service.Now().UnixMilli(), Notice: "Local history could not be refreshed. Cached history is retained."})
			}
		}
	}
}

// scanLimits persists resolved limits and records only fresh native quota observations.
func (service *Service) scanLimits(ctx context.Context) (bool, error) {
	operation := service.diagnostics.NewOperation()
	started := time.Now()
	_ = service.diagnostics.Record(diagnostics.Entry{Level: diagnostics.LevelInfo, Source: diagnostics.SourceDesktop, Event: diagnostics.EventQuota, Outcome: diagnostics.OutcomeStarted, Operation: operation})
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
		isFresh := make([]bool, len(providers))
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
				httpOptions := quota.HttpOptions{Environment: service.Environment, Home: service.Home}
				if target.Home != "" {
					httpOptions.Home = target.Home
					httpOptions.Environment = make(map[string]string, len(service.Environment))
					for name, value := range service.Environment {
						httpOptions.Environment[name] = value
					}
					httpOptions.Environment["HOME"], httpOptions.Environment["USERPROFILE"] = target.Home, target.Home
				}
				var account quota.Account
				probeStarted := time.Now()
				switch target.Driver {
				case "codex":
					account = quota.ProbeCodex(ctx, options)
				case "claudeAgent":
					account = quota.ProbeClaude(ctx, claudeprobe.Options{Executable: service.resolveExecutable(target.Driver, target.Executable), Environment: options.Process.Environment, ConfigDirectory: target.Home}, true)
					if account.Limits.Unavailable != nil {
						account.Limits = quota.PreferClaudeLogin(account.Limits, quota.ReadClaudeLogin(ctx, httpOptions, target.Home))
					}
					account.WorkspaceId = quota.ReadClaudeOrganization(httpOptions, target.Home)
				}
				old := previousProviders[target.Id]
				isProbeFailed := account.Limits.Unavailable != nil && account.Limits.Unavailable.Reason == "probeFailed"
				if isProbeFailed {
					notices[index] = target.Label + ": " + account.Limits.Unavailable.Message + " Last successful limits are retained."
					if target.Driver == "claudeAgent" && (old.Limits == nil || old.Limits.Unavailable != nil) {
						notices[index] = target.Label + ": " + account.Limits.Unavailable.Message + " No previous successful limits are available."
					}
				}
				isFresh[index] = !isProbeFailed && account.Limits.Unavailable == nil
				if target.Driver == "claudeAgent" {
					service.recordClaudeProbe(account.Limits, old.Limits, operation, probeStarted)
				}
				account.Limits = quota.ResolveProbe(old.Limits, account.Limits)
				if account.Email == "" && isProbeFailed {
					account.Email = old.Auth.Email
					account.Plan = old.Auth.Label
				}
				if account.WorkspaceId == "" && isProbeFailed {
					account.WorkspaceId = old.Auth.WorkspaceId
				}
				provider.Limits = &account.Limits
				provider.Auth.Email, provider.Auth.Label, provider.Auth.WorkspaceId = account.Email, account.Plan, account.WorkspaceId
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
		if err == nil {
			service.recordQuotaSamples(ctx, configuration.Value.Providers, providers, isFresh)
		}
		return err
	})
	service.recordRefreshResult(diagnostics.EventQuota, operation, started, err)
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
									options.ExpectedWorkspaceId = provider.Auth.WorkspaceId
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
