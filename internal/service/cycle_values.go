package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"
	"yousage/internal/accounting"
	"yousage/internal/domain"
	"yousage/internal/quota"
	"yousage/internal/remote"
)

// recordQuotaSamples makes observation persistence independent of limits refresh success.
func (service *Service) recordQuotaSamples(ctx context.Context, configured []NativeProvider, providers []remote.Provider, isFresh []bool) {
	series := []quota.CycleSeries{}
	revision, err := service.Store.ReadSetting(ctx, "quota-samples", &series)
	if err != nil {
		return
	}
	active := map[string]bool{}
	for _, provider := range configured {
		active[provider.Id] = true
	}
	retained := []quota.CycleSeries{}
	for _, value := range series {
		if active[value.InstanceId] {
			retained = append(retained, value)
		}
	}
	for index, provider := range providers {
		if index >= len(isFresh) || !isFresh[index] || provider.Limits == nil || provider.Limits.Unavailable != nil || (provider.Driver != "codex" && provider.Driver != "claudeAgent") {
			continue
		}
		for _, window := range provider.Limits.Windows {
			position := -1
			for candidate, value := range retained {
				if value.InstanceId == provider.InstanceId && value.WindowId == window.Id {
					position = candidate
					break
				}
			}
			if position < 0 {
				position = len(retained)
				retained = append(retained, quota.CycleSeries{InstanceId: provider.InstanceId, WindowId: window.Id})
			}
			retained[position] = quota.AppendCycleSample(retained[position], quota.CycleSample{CheckedAt: provider.Limits.CheckedAt, UsedPercent: window.UsedPercent, ResetsAt: window.ResetsAt, WindowDurationMins: window.WindowDurationMins})
			retained[position].Observations = quota.AppendCycleObservation(retained[position].Observations, quota.CycleObservation{CycleSample: quota.CycleSample{CheckedAt: provider.Limits.CheckedAt, UsedPercent: window.UsedPercent, ResetsAt: window.ResetsAt, WindowDurationMins: window.WindowDurationMins}, WindowId: window.Id, PlanType: provider.Auth.Label, WindowStructure: quota.WindowStructure(provider.Limits.Windows), Source: "probes"})
		}
	}
	_, _ = service.Store.WriteSetting(ctx, "quota-samples", revision, retained)
}

// cycleValueCache retains fitted values until their inputs or reset validity change.
type cycleValueCache struct {
	key        [32]byte
	validUntil time.Time
	values     []quota.CycleValue
}

// cycleHistoryCache separates completed fits and priced history from changing probes.
type cycleHistoryCache struct {
	key        [32]byte
	liveKey    [32]byte
	validUntil time.Time
	histories  map[string]quota.CostHistory
	logs       map[string][]quota.CycleObservation
	completed  map[string][]quota.FittedCycle
	latest     map[string]loggedLimits
}

// persistedCycleFits retains completed fits under an estimator and input fingerprint.
type persistedCycleFits struct {
	Key        string
	ValidUntil time.Time
	Completed  []quota.FittedCycle
	Latest     loggedLimitsSnapshot
}

// loggedLimitsSnapshot serializes window metadata for a cold cache read.
type loggedLimitsSnapshot struct {
	Limits quota.Limits
	Plan   string
}

// loggedLimits supplies current window metadata before the first native probe.
type loggedLimits struct {
	limits quota.Limits
	plan   string
}

// cycleSources restricts custom homes to an exact canonical fingerprint path when possible;
// otherwise it uses all in-scope sources of that provider, matching local history filtering.
func (service *Service) cycleSources(entries []sourceIndex, target NativeProvider, provider domain.Provider) []sourceIndex {
	selected := []sourceIndex{}
	matched := []sourceIndex{}
	home := target.Home
	if home != "" {
		if resolved, err := filepath.EvalSymlinks(home); err == nil {
			home = resolved
		}
	}
	for _, entry := range entries {
		if entry.IsHidden || entry.Source.Fingerprint.Provider != provider || !service.isProviderInScope(provider) {
			continue
		}
		selected = append(selected, entry)
		path := filepath.Clean(entry.Source.Fingerprint.ResolvedHomePath)
		isMatch := path == filepath.Clean(home)
		if provider == domain.Codex {
			isMatch = isMatch || path == filepath.Join(filepath.Clean(home), "sessions")
		}
		if provider == domain.Claude {
			isMatch = isMatch || path == filepath.Join(filepath.Clean(home), "projects")
		}
		if service.Platform == "windows" {
			isMatch = isMatch || strings.EqualFold(path, filepath.Clean(home)) || provider == domain.Codex && strings.EqualFold(path, filepath.Join(filepath.Clean(home), "sessions")) || provider == domain.Claude && strings.EqualFold(path, filepath.Join(filepath.Clean(home), "projects"))
		}
		if home != "" && isMatch {
			matched = append(matched, entry)
		}
	}
	if len(matched) > 0 {
		return matched
	}
	return selected
}

// cycleHistorySince covers plausible cycle starts with a bounded history margin.
func cycleHistorySince(provider remote.Provider, series []quota.CycleSeries, now time.Time) time.Time {
	earliest := time.Time{}
	for _, window := range provider.Limits.Windows {
		starts := []time.Time{}
		reset, _ := time.Parse(time.RFC3339Nano, window.ResetsAt)
		if duration := window.WindowDurationMins; duration != nil && *duration > 0 && int64(*duration) <= int64((1<<63-1)/time.Minute) && !reset.IsZero() {
			starts = append(starts, reset.Add(-time.Duration(*duration)*time.Minute))
		} else {
			for _, stored := range series {
				if stored.InstanceId != provider.InstanceId || stored.WindowId != window.Id {
					continue
				}
				floor, _ := time.Parse(time.RFC3339Nano, stored.ResetFloorAt)
				starts = append(starts, floor)
				if len(stored.CycleBoundaries) > 0 {
					boundary, _ := time.Parse(time.RFC3339Nano, stored.CycleBoundaries[len(stored.CycleBoundaries)-1])
					starts = append(starts, boundary)
				}
				samples := stored.Samples
				if stored.CycleBoundaries != nil && len(samples) > 0 {
					samples = samples[len(samples)-1:]
				}
				samples = append(append([]quota.CycleSample{}, samples...), quota.CycleSample{CheckedAt: provider.Limits.CheckedAt, UsedPercent: window.UsedPercent, ResetsAt: window.ResetsAt})
				cycles := quota.SplitCycles(samples)
				if len(cycles) > 1 {
					previous := cycles[len(cycles)-2]
					boundary, _ := time.Parse(time.RFC3339Nano, previous[len(previous)-1].CheckedAt)
					starts = append(starts, boundary)
				}
				break
			}
		}
		for _, start := range starts {
			if !start.IsZero() && (earliest.IsZero() || start.Before(earliest)) {
				earliest = start
			}
		}
	}
	since := now.Add(-8 * 24 * time.Hour)
	if !earliest.IsZero() {
		since = earliest.Add(-time.Hour)
	}
	if retained := now.Add(-90 * 24 * time.Hour); since.Before(retained) {
		since = retained
	}
	return since
}

// localCycleValues prices each provider source set once and degrades failed reads to absent estimates.
func (service *Service) localCycleValues(ctx context.Context, config remote.Config) []quota.CycleValue {
	service.cycleMutex.Lock()
	defer service.cycleMutex.Unlock()
	now := service.Now()
	configuration, err := service.Configuration(ctx)
	if err != nil {
		return nil
	}
	entries, _, err := service.sourceIndex(ctx)
	if err != nil {
		return nil
	}
	pricing, err := service.Preferences.Pricing(ctx, service.LocalId)
	if err != nil {
		return nil
	}
	rates, err := service.Rates(ctx)
	if err != nil {
		return nil
	}
	series := []quota.CycleSeries{}
	if _, err = service.Store.ReadSetting(ctx, "quota-samples", &series); err != nil {
		series = nil
	}
	targets := map[string]NativeProvider{}
	selectedSources := map[string][]sourceIndex{}
	selection := []any{}
	selectedIds := map[string]bool{}
	completedEnds := map[string]time.Time{}
	for _, target := range configuration.Value.Providers {
		targets[target.Id] = target
		selection = append(selection, []any{target.Id, target.Driver, target.Home, target.IsEnabled})
		if target.IsEnabled && (target.Driver == "codex" || target.Driver == "claudeAgent") {
			key := target.Driver + ":" + target.Home
			fitted := service.cycleHistories.completed[key]
			if fitted == nil {
				digest := sha256.Sum256([]byte(key))
				var persisted persistedCycleFits
				if _, err := service.Store.ReadSetting(ctx, "cycle-fits:"+hex.EncodeToString(digest[:]), &persisted); err != nil {
					return nil
				}
				fitted = persisted.Completed
			}
			end := time.Time{}
			for _, cycle := range fitted {
				reset, _ := time.Parse(time.RFC3339Nano, cycle.Value.ResetsAt)
				if !reset.After(now) && reset.After(end) {
					end = reset
				}
			}
			historyProvider := domain.Codex
			if target.Driver == "claudeAgent" {
				historyProvider = domain.Claude
			}
			selectedSources[target.Id] = service.cycleSources(entries, target, historyProvider)
			for _, entry := range selectedSources[target.Id] {
				id := entry.Source.Fingerprint.Id()
				selectedIds[id] = true
				if end.After(completedEnds[id]) {
					completedEnds[id] = end
				}
			}
		}
	}
	historyInputs := []any{"robust-cycle-fit-cache-v2", selection, pricing.Value, rates.encodedRates, service.Scope}
	for _, stored := range series {
		for _, observation := range stored.Observations {
			reset, _ := time.Parse(time.RFC3339Nano, observation.ResetsAt)
			if !reset.After(now) {
				historyInputs = append(historyInputs, stored.InstanceId, observation)
			}
		}
	}
	liveInputs := []any{}
	for _, entry := range entries {
		if !selectedIds[entry.Source.Fingerprint.Id()] {
			continue
		}
		id := entry.Source.Fingerprint.Id()
		boundary := completedEnds[id]
		for _, provider := range config.Providers {
			if !provider.IsEnabled || provider.Limits == nil || provider.Driver == "codex" && entry.Source.Fingerprint.Provider != domain.Codex || provider.Driver == "claudeAgent" && entry.Source.Fingerprint.Provider != domain.Claude {
				continue
			}
			isSourceSelected := false
			for _, candidate := range selectedSources[provider.InstanceId] {
				isSourceSelected = isSourceSelected || candidate.Source.Fingerprint.Id() == id
			}
			if !isSourceSelected {
				continue
			}
			for _, window := range provider.Limits.Windows {
				reset, _ := time.Parse(time.RFC3339Nano, window.ResetsAt)
				if window.WindowDurationMins != nil && *window.WindowDurationMins > 0 && *window.WindowDurationMins <= 90*24*60 {
					start := reset.Add(-time.Duration(*window.WindowDurationMins) * time.Minute)
					if start.After(boundary) && !start.After(now) {
						boundary = start
					}
				}
			}
		}
		if entry.Source.Fingerprint.Provider == domain.Codex && !entry.ShouldExcludeRecords {
			record, latestError := service.Store.LatestCodexQuota(ctx, id, now.UnixMilli())
			if latestError != nil {
				return nil
			}
			if record != nil && record.Quota != nil {
				for _, window := range record.Quota.Windows {
					if window.WindowMinutes > 0 && window.WindowMinutes <= 90*24*60 {
						start := time.Unix(window.ResetsAt, 0).Add(-time.Duration(window.WindowMinutes) * time.Minute)
						if start.After(boundary) && !start.After(now) {
							boundary = start
						}
					}
				}
			}
		}
		if boundary.IsZero() {
			boundary = now
		}
		completedKey, completedError := service.Store.CompletedHistoryKey(ctx, id, boundary.UnixMilli())
		key, keyError := service.Store.HistoryCacheKey(ctx, id)
		observation, observationError := service.Store.Observation(ctx, id)
		if completedError != nil || keyError != nil || observationError != nil {
			return nil
		}
		historyInputs = append(historyInputs, entry.Source.Fingerprint, entry.IsSnapshot, entry.ShouldExcludeRecords, completedKey)
		liveInputs = append(liveInputs, sha256.Sum256([]byte(key)), observation)
	}
	encoded, _ := json.Marshal(historyInputs)
	historyKey := sha256.Sum256(encoded)
	encoded, _ = json.Marshal(liveInputs)
	liveKey := sha256.Sum256(encoded)
	encoded, _ = json.Marshal([]any{historyKey, liveKey, config.Providers, series})
	cacheKey := sha256.Sum256(encoded)
	if service.cycleCache.values != nil && service.cycleCache.key == cacheKey && now.Before(service.cycleCache.validUntil) {
		return service.cycleCache.values
	}
	overrides, aliases := accounting.OverrideRates(pricing.Value.Overrides), accounting.ResolveAliases(pricing.Value.Aliases)
	histories := map[string]quota.CostHistory{}
	logSeries := map[string][]quota.CycleObservation{}
	completed := map[string][]quota.FittedCycle{}
	latestLimits := map[string]loggedLimits{}
	historyValidUntil := now.Add(90 * 24 * time.Hour)
	if service.cycleHistories.histories != nil && service.cycleHistories.key == historyKey && now.Before(service.cycleHistories.validUntil) {
		completed = service.cycleHistories.completed
		if service.cycleHistories.liveKey == liveKey {
			histories, logSeries = service.cycleHistories.histories, service.cycleHistories.logs
		}
		latestLimits = service.cycleHistories.latest
		historyValidUntil = service.cycleHistories.validUntil
	}
	result := []quota.CycleValue{}
	for _, provider := range config.Providers {
		if !provider.IsEnabled || (provider.Driver != "codex" && provider.Driver != "claudeAgent") || provider.Driver == "claudeAgent" && (provider.Limits == nil || provider.Limits.Unavailable != nil) {
			continue
		}
		historyProvider := domain.Codex
		if provider.Driver == "claudeAgent" {
			historyProvider = domain.Claude
		}
		if !service.isProviderInScope(historyProvider) {
			continue
		}
		target := targets[provider.InstanceId]
		key := provider.Driver + ":" + target.Home
		costHistory, exists := histories[key]
		if !exists {
			selected := selectedSources[provider.InstanceId]
			since := now.Add(-90 * time.Hour * 24)
			persisted := persistedCycleFits{}
			settingDigest := sha256.Sum256([]byte(key))
			settingKey := "cycle-fits:" + hex.EncodeToString(settingDigest[:])
			persistedRevision, readError := service.Store.ReadSetting(ctx, settingKey, &persisted)
			isPersisted := readError == nil && persisted.Key == hex.EncodeToString(historyKey[:]) && now.Before(persisted.ValidUntil)
			if isPersisted {
				if _, exists := completed[key]; !exists {
					completed[key] = persisted.Completed
				}
				latestLimits[key] = loggedLimits{limits: persisted.Latest.Limits, plan: persisted.Latest.Plan}
				since = cycleHistorySince(remote.Provider{InstanceId: provider.InstanceId, Limits: &persisted.Latest.Limits}, series, now)
				for _, candidate := range config.Providers {
					if !candidate.IsEnabled || candidate.Driver != provider.Driver || targets[candidate.InstanceId].Home != target.Home {
						continue
					}
					if candidate.Limits == nil {
						continue
					}
					bound := cycleHistorySince(candidate, series, now)
					if bound.Before(since) {
						since = bound
					}
				}
			} else if provider.Driver == "codex" {
				earliest := now
				for _, entry := range selected {
					if entry.ShouldExcludeRecords {
						continue
					}
					record, latestError := service.Store.LatestCodexQuota(ctx, entry.Source.Fingerprint.Id(), now.UnixMilli())
					if latestError != nil {
						continue
					}
					if record == nil || record.Quota == nil {
						continue
					}
					for _, window := range record.Quota.Windows {
						if window.WindowMinutes <= 0 || window.WindowMinutes > 90*24*60 {
							continue
						}
						start := time.Unix(window.ResetsAt, 0).Add(-time.Duration(window.WindowMinutes) * time.Minute)
						age := max(7*24*time.Hour, time.Duration(quota.CycleHistoryCap)*time.Duration(window.WindowMinutes)*time.Minute)
						candidate := start.Add(-age - time.Hour)
						if candidate.Before(earliest) {
							earliest = candidate
						}
					}
				}
				if earliest.Before(now) && earliest.After(since) {
					since = earliest
				}
			}
			sources, bySource, err := service.localHistoryRecords(ctx, selected, since, now, true)
			if err != nil {
				continue
			}
			covered := now
			hasCoverage := false
			usageCount, observationCount := 0, 0
			for _, sourceRecords := range bySource {
				for _, record := range sourceRecords {
					if !record.IsQuotaOnly {
						usageCount++
					}
					if record.Quota != nil && record.QuotaBucket == "codex" {
						observationCount += len(record.Quota.Windows)
					}
				}
			}
			records := make([]quota.BucketCostRecord, 0, usageCount)
			if observationCount > 0 {
				logSeries[key] = make([]quota.CycleObservation, 0, observationCount)
			}
			for index, source := range sources {
				isExcluded := false
				for _, entry := range selected {
					if entry.Source.Fingerprint.Id() == source.Fingerprint.Id() {
						isExcluded = entry.ShouldExcludeRecords
						break
					}
				}
				if isExcluded {
					continue
				}
				hasCoverage = true
				observation, err := service.Store.Observation(ctx, source.Fingerprint.Id())
				if err != nil || observation.SucceededAt == nil {
					covered = time.Time{}
				} else if success := time.UnixMilli(*observation.SucceededAt); success.Before(covered) {
					covered = success
				}
				for _, record := range bySource[index] {
					if record.Quota != nil && record.QuotaBucket == "codex" {
						windows := []quota.Window{}
						for _, window := range record.Quota.Windows {
							minutes := window.WindowMinutes
							windows = append(windows, quota.Window{Id: window.Id, WindowDurationMins: &minutes})
						}
						structure := quota.WindowStructure(windows)
						for _, window := range record.Quota.Windows {
							minutes := window.WindowMinutes
							logSeries[key] = append(logSeries[key], quota.CycleObservation{CycleSample: quota.CycleSample{CheckedAt: time.UnixMilli(record.TimestampMs).UTC().Format(time.RFC3339Nano), UsedPercent: window.UsedPercent, ResetsAt: time.Unix(window.ResetsAt, 0).UTC().Format(time.RFC3339Nano), WindowDurationMins: &minutes}, WindowId: window.Id, PlanType: record.Quota.PlanType, WindowStructure: structure, Source: "logs"})
						}
					}
					if record.IsQuotaOnly || provider.Driver == "codex" && record.QuotaBucket != "" && record.QuotaBucket != "codex" {
						continue
					}
					if model, exists := aliases[record.Model]; exists {
						record.Model, record.RateModel = model, ""
					}
					priced := accounting.Price(rates.Rates, overrides, record)
					records = append(records, quota.BucketCostRecord{CostRecord: quota.CostRecord{At: time.UnixMilli(record.TimestampMs), CostUsd: priced.CostUsd, IsUnpriced: priced.CostSource == "unpriced"}, Bucket: record.QuotaBucket})
				}
			}
			if !hasCoverage {
				covered = time.Time{}
			}
			if provider.Driver == "codex" {
				costHistory = quota.NewBucketCostHistory(records, "codex", covered, since)
			} else {
				costs := make([]quota.CostRecord, 0, len(records))
				for _, record := range records {
					costs = append(costs, record.CostRecord)
				}
				costHistory = quota.NewCostHistory(costs, covered, since)
			}
			histories[key] = costHistory
			latest := time.Time{}
			for _, observation := range logSeries[key] {
				at, _ := time.Parse(time.RFC3339Nano, observation.CheckedAt)
				if at.After(now) || at.Before(latest) {
					continue
				}
				if at.After(latest) {
					latestLimits[key] = loggedLimits{limits: quota.Limits{Windows: []quota.Window{}}, plan: observation.PlanType}
					latest = at
				}
				value := latestLimits[key]
				window := quota.Window{Id: observation.WindowId, UsedPercent: observation.UsedPercent, ResetsAt: observation.ResetsAt, WindowDurationMins: observation.WindowDurationMins}
				hasWindow := false
				for index, previous := range value.limits.Windows {
					if previous.Id == window.Id {
						value.limits.Windows[index], hasWindow = window, true
						break
					}
				}
				if !hasWindow {
					value.limits.Windows = append(value.limits.Windows, window)
				}
				latestLimits[key] = value
			}
			allObservations := append([]quota.CycleObservation{}, logSeries[key]...)
			for _, stored := range series {
				if stored.InstanceId == provider.InstanceId {
					for _, observation := range stored.Observations {
						reset, _ := time.Parse(time.RFC3339Nano, observation.ResetsAt)
						if !reset.After(now) {
							allObservations = append(allObservations, observation)
						}
					}
				}
			}
			windowIds := map[string]bool{}
			for _, observation := range allObservations {
				windowIds[observation.WindowId] = true
			}
			for windowId := range windowIds {
				if isPersisted {
					continue
				}
				fits := quota.FitCycles(allObservations, provider.InstanceId, provider.Driver, windowId, costHistory, now, quota.DefaultCycleLag)
				for _, fit := range fits {
					reset, _ := time.Parse(time.RFC3339Nano, fit.Value.ResetsAt)
					if !reset.After(now) && !isPersisted {
						completed[key] = append(completed[key], fit)
					} else if reset.After(now) && reset.Before(historyValidUntil) {
						historyValidUntil = reset
					}
				}
			}
			currentLogs := []quota.CycleObservation{}
			for _, observation := range logSeries[key] {
				reset, _ := time.Parse(time.RFC3339Nano, observation.ResetsAt)
				if reset.After(now) {
					if reset.Before(historyValidUntil) {
						historyValidUntil = reset
					}
					currentLogs = append(currentLogs, observation)
				}
			}
			logSeries[key] = currentLogs
			if !isPersisted && readError == nil {
				latest := latestLimits[key]
				persisted = persistedCycleFits{Key: hex.EncodeToString(historyKey[:]), ValidUntil: historyValidUntil, Completed: completed[key], Latest: loggedLimitsSnapshot{Limits: latest.limits, Plan: latest.plan}}
				_, _ = service.Store.WriteSetting(ctx, settingKey, persistedRevision, persisted)
			}
		}
		if provider.Limits == nil || provider.Limits.Unavailable != nil {
			logged, hasLogs := latestLimits[key]
			if !hasLogs {
				continue
			}
			provider.Limits, provider.Auth.Label = &logged.limits, logged.plan
		}
		for _, window := range provider.Limits.Windows {
			observations := quota.CycleSeries{InstanceId: provider.InstanceId, WindowId: window.Id}
			for _, stored := range series {
				if stored.InstanceId == provider.InstanceId && stored.WindowId == window.Id {
					observations = stored
					break
				}
			}
			result = append(result, quota.EstimateCyclesWithHistory(observations, logSeries[key], completed[key], provider.Driver, window, provider.Limits.CheckedAt, provider.Auth.Label, quota.WindowStructure(provider.Limits.Windows), costHistory, now))
		}
	}
	validUntil := now.UTC().Truncate(24 * time.Hour).Add(24 * time.Hour)
	for _, value := range result {
		reset, _ := time.Parse(time.RFC3339Nano, value.ResetsAt)
		if reset.After(now) && reset.Before(validUntil) {
			validUntil = reset
		}
	}
	for _, cycles := range completed {
		for _, cycle := range cycles {
			if cycle.WindowMinutes >= quota.LongCycleMinimumMinutes {
				continue
			}
			reset, _ := time.Parse(time.RFC3339Nano, cycle.Value.ResetsAt)
			expires := reset.Add(quota.ShortCyclePriorAge + time.Nanosecond)
			if expires.After(now) && expires.Before(validUntil) {
				validUntil = expires
			}
		}
	}
	service.cycleCache = cycleValueCache{key: cacheKey, validUntil: validUntil, values: result}
	service.cycleHistories = cycleHistoryCache{key: historyKey, liveKey: liveKey, validUntil: historyValidUntil, histories: histories, logs: logSeries, completed: completed, latest: latestLimits}
	return result
}
