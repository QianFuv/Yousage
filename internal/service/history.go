package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"math"
	"os"
	"slices"
	"sort"
	"time"
	"yousage/internal/accounting"
	"yousage/internal/diagnostics"
	"yousage/internal/domain"
	"yousage/internal/history"
	"yousage/internal/preferences"
	"yousage/internal/refresh"
	"yousage/internal/remote"
)

type sourceIndex struct {
	ConfigurationId      string        `json:"configurationId"`
	Source               remote.Source `json:"source"`
	IsSnapshot           bool          `json:"snapshot"`
	IsHidden             bool          `json:"hidden,omitempty"`
	ShouldExcludeRecords bool          `json:"excludeRecords,omitempty"`
}

// localSummaryCache bounds unchanged aggregates by revision and time eligibility.
type localSummaryCache struct {
	value        remote.Summary
	since, until time.Time
}

func (service *Service) sourceIndex(ctx context.Context) ([]sourceIndex, int64, error) {
	entries := []sourceIndex{}
	revision, err := service.Store.ReadSetting(ctx, "history-index", &entries)
	return entries, revision, err
}
func sourceMessage(text string) *string {
	if text == "" {
		return nil
	}
	return &text
}
func (service *Service) hostId() string {
	name, err := os.Hostname()
	if err != nil {
		return service.LocalId
	}
	return name
}
func (service *Service) fingerprint(provider domain.Provider, path string) domain.SourceFingerprint {
	return history.PhysicalFingerprint(service.hostId(), provider, path)
}

// RefreshHistory coalesces full-retention scans and preserves deferred notifications.
func (service *Service) RefreshHistory(ctx context.Context, request refresh.Request) error {
	_, err := service.history.Run(ctx, "local-history", request, func(owner context.Context) (bool, error) {
		owner, cancel := context.WithCancel(owner)
		stop := context.AfterFunc(service.owner, cancel)
		defer stop()
		defer cancel()
		if ctx.Value(deferredUsageInvalidationKey{}) == true {
			owner = context.WithValue(owner, deferredUsageInvalidationKey{}, true)
		}
		return service.scanHistory(owner)
	})
	if errors.Is(err, refresh.ErrSkipped) {
		return nil
	}
	return err
}

// scanHistory persists source coverage and emits unless an enclosing refresh defers it.
func (service *Service) scanHistory(ctx context.Context) (hasChanged bool, scanError error) {
	operation := service.diagnostics.NewOperation()
	started := time.Now()
	_ = service.diagnostics.Record(diagnostics.Entry{Level: diagnostics.LevelInfo, Source: diagnostics.SourceDesktop, Event: diagnostics.EventRefresh, Outcome: diagnostics.OutcomeStarted, Operation: operation})
	defer func() { service.recordRefreshResult(diagnostics.EventRefresh, operation, started, scanError) }()
	sources, _, err := service.Preferences.Sources(ctx)
	if err != nil {
		return false, err
	}
	sources = service.expandedSources(service.scopedSources(sources))
	entries, revision, err := service.sourceIndex(ctx)
	if err != nil {
		return false, err
	}
	entries = slices.DeleteFunc(entries, func(entry sourceIndex) bool { return !entry.Source.Fingerprint.Provider.Valid() })
	now := service.Now()
	byId := map[string]int{}
	for index, entry := range entries {
		byId[entry.ConfigurationId] = index
	}
	put := func(identity string, source remote.Source) {
		entry := sourceIndex{ConfigurationId: identity, Source: source}
		if index, exists := byId[identity]; exists {
			entries[index] = entry
		} else {
			byId[identity] = len(entries)
			entries = append(entries, entry)
		}
	}
	jsonRoots := []history.Root{}
	jsonIds := map[string]string{}
	for _, source := range sources {
		if !source.IsEnabled || !source.Provider.Valid() {
			continue
		}
		root, err := history.ExpandHome(source.Root)
		if err != nil {
			return false, err
		}
		jsonRoots = append(jsonRoots, history.Root{Provider: source.Provider, Home: root})
		fingerprint := service.fingerprint(source.Provider, (history.Root{Provider: source.Provider, Home: root}).Directory())
		jsonIds[string(source.Provider)+":"+fingerprint.ResolvedHomePath] = source.Id
	}
	results, err := (history.Collector{Store: service.Store, HostId: service.hostId()}).Synchronize(ctx, jsonRoots, now)
	if err != nil {
		return false, err
	}
	_ = service.diagnostics.Record(diagnostics.Entry{Level: diagnostics.LevelInfo, Source: diagnostics.SourceDesktop, Event: diagnostics.EventCache, Outcome: diagnostics.OutcomeSucceeded, Count: int64(len(results)), Operation: operation})
	for _, result := range results {
		put(jsonIds[string(result.Fingerprint.Provider)+":"+result.Fingerprint.ResolvedHomePath], remote.Source{Fingerprint: result.Fingerprint, Status: result.Status, ScannedFiles: result.ScannedFiles, SkippedFiles: result.SkippedFiles, MalformedRecords: result.MalformedRecords, Message: sourceMessage(result.Notice)})
	}
	if err := service.Store.PruneExpiredHistory(ctx, now.Add(-90*24*time.Hour).UnixNano()); err != nil {
		return false, err
	}
	for index := range entries {
		id := entries[index].Source.Fingerprint.Id()
		if entries[index].Source.Status == "missing" && !entries[index].ShouldExcludeRecords {
			hasHistory, err := service.Store.HasRetainedHistory(ctx, id, now.Add(-90*24*time.Hour).UnixNano())
			if err != nil {
				return false, err
			}
			if hasHistory {
				entries[index].Source.Status = "partial"
			}
		}
	}
	if _, err = service.Store.WriteSetting(ctx, "history-index", revision, entries); err != nil {
		return false, err
	}
	if ctx.Value(deferredUsageInvalidationKey{}) != true {
		service.emit("usage-invalidated", nil)
	}
	return true, nil
}

func windowStart(window accounting.Window) time.Time {
	if window.Resolution == "hour" {
		return time.UnixMilli(window.SinceTimeMs)
	}
	instant, _ := time.Parse(time.DateOnly, window.SinceDay)
	return instant
}

// localSummary applies window mtime rules before cross-file deduplication and per-source sessions.
func (service *Service) localSummary(ctx context.Context, window accounting.Window) (remote.Summary, error) {
	service.summaryMutex.Lock()
	defer service.summaryMutex.Unlock()
	now := service.Now()
	summary := remote.Summary{ContractVersion: 6, ReadAt: now.UTC().Format(time.RFC3339Nano), TimeZone: window.TimeZone, SinceDay: window.SinceDay, UntilDay: window.UntilDay, Buckets: []accounting.Bucket{}, Sources: []remote.Source{}}
	entries, _, err := service.sourceIndex(ctx)
	if err != nil {
		return summary, err
	}
	pricing, err := service.Preferences.Pricing(ctx, service.LocalId)
	if err != nil {
		return summary, err
	}
	rates := service.Rates(ctx)
	summary.Pricing = rates.Provenance(now)
	inputs := []any{window, entries, pricing, rates.encodedRates, service.Scope, now.UTC().Format(time.DateOnly)}
	for _, entry := range entries {
		key, err := service.Store.HistoryCacheKey(ctx, entry.Source.Fingerprint.Id())
		if err != nil {
			return summary, err
		}
		mutation, err := service.Store.CompletedHistoryKey(ctx, entry.Source.Fingerprint.Id(), math.MaxInt64)
		if err != nil {
			return summary, err
		}
		inputs = append(inputs, sha256.Sum256([]byte(key)), mutation)
	}
	encoded, _ := json.Marshal(inputs)
	key := sha256.Sum256(encoded)
	if service.summaryCaches != nil && service.summaryCaches[key].value.ContractVersion != 0 && !now.Before(service.summaryCaches[key].since) && now.Before(service.summaryCaches[key].until) {
		cached := service.summaryCaches[key].value
		cached.ReadAt, cached.Pricing = summary.ReadAt, summary.Pricing
		cached.ScanDurationMs = max(0, service.Now().Sub(now).Milliseconds())
		return cached, nil
	}
	options := accounting.AggregateOptions{Window: window, Rates: rates.Rates, Overrides: accounting.OverrideRates(pricing.Value.Overrides), Aliases: accounting.ResolveAliases(pricing.Value.Aliases)}
	since := windowStart(window).Add(-36 * time.Hour)
	recordSince := since
	if window.Resolution == "hour" {
		recordSince = windowStart(window)
	}
	sources, bySource, err := service.localHistoryRecordsBetween(ctx, entries, since, recordSince, now, false)
	if err != nil {
		return summary, err
	}
	summary.Sources = sources
	for index, records := range bySource {
		aggregate, err := accounting.Aggregate(records, options)
		if err != nil {
			return summary, err
		}
		summary.Sources[index].DistinctSessions = aggregate.DistinctSessions
		summary.Buckets = append(summary.Buckets, aggregate.Buckets...)
	}
	summary.ScanDurationMs = max(0, service.Now().Sub(now).Milliseconds())
	if service.summaryCaches == nil || len(service.summaryCaches) >= 16 {
		service.summaryCaches = map[[32]byte]localSummaryCache{}
	}
	ids := []string{}
	for _, entry := range entries {
		ids = append(ids, entry.Source.Fingerprint.Id())
	}
	service.summaryCaches[key] = localSummaryCache{value: summary, since: now, until: service.Store.CachedHistoryValidUntil(ids, now)}
	return summary, nil
}

// localRecords shares source filtering and cross-file deduplication with cycle accounting.
func (service *Service) localRecords(ctx context.Context, entries []sourceIndex, since, now time.Time) ([]remote.Source, [][]domain.Record, error) {
	return service.localHistoryRecords(ctx, entries, since, now, false)
}

// localHistoryRecords optionally includes quota-only facts for cycle estimation.
func (service *Service) localHistoryRecords(ctx context.Context, entries []sourceIndex, since, now time.Time, shouldIncludeQuota bool) ([]remote.Source, [][]domain.Record, error) {
	return service.localHistoryRecordsBetween(ctx, entries, since, since, now, shouldIncludeQuota)
}

// localHistoryRecordsBetween preserves file eligibility while bounding Codex timestamp reads.
func (service *Service) localHistoryRecordsBetween(ctx context.Context, entries []sourceIndex, since, recordSince, now time.Time, shouldIncludeQuota bool) ([]remote.Source, [][]domain.Record, error) {
	sources := []remote.Source{}
	cutoff := max(since.UnixNano(), now.Add(-90*24*time.Hour).UnixNano())
	recordCutoff := max(recordSince.UnixMilli(), now.Add(-90*24*time.Hour).UnixMilli())
	files := [][]domain.Record{}
	owners := []int{}
	seen := map[string]bool{}
	for _, entry := range entries {
		if entry.IsHidden || !entry.Source.Fingerprint.Provider.Valid() || !service.isProviderInScope(entry.Source.Fingerprint.Provider) {
			continue
		}
		id := entry.Source.Fingerprint.Id()
		if seen[id] {
			continue
		}
		seen[id] = true
		position := len(sources)
		sources = append(sources, entry.Source)
		if entry.ShouldExcludeRecords {
			sources[position].DistinctSessions = 0
			continue
		}
		modifiedSince := cutoff
		if entry.IsSnapshot {
			modifiedSince = now.Add(-90 * 24 * time.Hour).UnixNano()
		}
		read := service.Store.FileUsageRecordsSince
		if shouldIncludeQuota {
			read = service.Store.FileRecordsSince
		}
		var fileRecords map[string][]domain.Record
		var err error
		if entry.Source.Fingerprint.Provider == domain.Codex {
			fileRecords, err = service.Store.FileCodexRecordsBetween(ctx, id, modifiedSince, recordCutoff, now.UnixMilli(), shouldIncludeQuota)
		} else {
			fileRecords, err = read(ctx, id, modifiedSince)
		}
		if err != nil {
			return nil, nil, err
		}
		paths := make([]string, 0, len(fileRecords))
		for path := range fileRecords {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		for _, path := range paths {
			files = append(files, fileRecords[path])
			owners = append(owners, position)
		}
	}
	deduped := history.DedupeMovedCodex(files)
	bySource := make([][]domain.Record, len(sources))
	counts := make([]int, len(sources))
	for index, records := range deduped {
		counts[owners[index]] += len(records)
	}
	for index, count := range counts {
		if count > 0 {
			bySource[index] = make([]domain.Record, 0, count)
		}
	}
	seenRecords := map[string]bool{}
	for index, records := range deduped {
		owner := owners[index]
		for _, record := range records {
			if record.TimestampMs > now.UnixMilli() {
				continue
			}
			if !shouldIncludeQuota && (record.IsQuotaOnly || record.ErrorKind != "") {
				continue
			}
			if record.DedupeKey != "" {
				if seenRecords[record.DedupeKey] {
					continue
				}
				seenRecords[record.DedupeKey] = true
			}
			record.SourcePath = sources[owner].Fingerprint.ResolvedHomePath
			bySource[owner] = append(bySource[owner], record)
		}
	}
	return sources, bySource, nil
}

// Sources reports configured roots; source paths remain private to the local settings and aggregate contract.
func (service *Service) Sources(ctx context.Context) ([]preferences.Source, int64, error) {
	sources, revision, err := service.Preferences.Sources(ctx)
	return service.scopedSources(sources), revision, err
}

func (service *Service) SaveSources(ctx context.Context, sources []preferences.Source, revision int64) (int64, error) {
	ctx, release, err := service.ownOperation(ctx)
	if err != nil {
		return revision, err
	}
	defer release()
	sources = service.scopedSources(sources)
	for index := range sources {
		if sources[index].IsAutomatic {
			sources[index].Root = ""
			continue
		}
		absolute, err := history.ExpandHome(sources[index].Root)
		if err != nil {
			return revision, err
		}
		sources[index].Root = absolute
	}
	next, err := service.Preferences.SaveSources(ctx, sources, revision)
	if err == nil {
		service.emit("usage-invalidated", nil)
	}
	return next, err
}
