package service

import (
	"context"
	"errors"
	"yousage/internal/refresh"
)

// RefreshCycle refreshes local history once, all published remote windows and
// limits, preserving independent failures and emitting a final invalidation.
// Its returned error contains only fixed, user-safe descriptions.
func (service *Service) RefreshCycle(ctx context.Context, zone string, request refresh.Request) error {
	defer service.emit("usage-invalidated", nil)
	ctx = context.WithValue(ctx, deferredUsageInvalidationKey{}, true)
	request.WaitForPending = true
	var failures []error
	configuration, err := service.Configuration(ctx)
	if err != nil {
		failures = append(failures, errors.New("refresh configuration could not be read"))
	}
	remoteIDs := []string{}
	for _, preference := range configuration.Value.Connections {
		if preference.IsEnabled {
			remoteIDs = append(remoteIDs, preference.Connection.EnvironmentId)
		}
	}
	for index, days := range []int{1, 7, 30, 90} {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		selected := remoteIDs
		if index == 0 {
			selected = nil
		}
		if err := service.RefreshUsage(ctx, days, zone, selected, request); err != nil {
			failures = append(failures, errors.New("usage could not be fully refreshed"))
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := service.refreshLimits(ctx, nil, request, false); err != nil {
		failures = append(failures, errors.New("limits could not be fully refreshed"))
	}
	quotas, _, err := service.quotas(ctx)
	if err != nil || quotas.Error != "" {
		failures = append(failures, errors.New("limits freshness could not be confirmed; last successful limits are retained when available"))
	}
	for _, source := range quotas.Sources {
		if source.Error != "" {
			failures = append(failures, errors.New("a limits source failed; last successful limits are retained when available"))
		}
	}
	for _, snapshot := range []refresh.Snapshot[bool]{service.history.Snapshot("local-history"), service.limits.Snapshot("local")} {
		if snapshot.Error != nil {
			failures = append(failures, errors.New("a previous refresh failed; cached data is retained"))
		}
	}
	if service.rates.Snapshot("public").Error != nil {
		failures = append(failures, errors.New("pricing rates could not be refreshed"))
	}
	return errors.Join(failures...)
}
