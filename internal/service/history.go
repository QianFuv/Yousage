package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"yousage/internal/accounting"
	"yousage/internal/domain"
	"yousage/internal/history"
	"yousage/internal/history/antigravity"
	"yousage/internal/history/cursor"
	"yousage/internal/history/opencode"
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
func (service *Service) scanHistory(ctx context.Context) (bool, error) {
	sources, _, err := service.Preferences.Sources(ctx)
	if err != nil {
		return false, err
	}
	sources = service.expandedSources(service.scopedSources(sources))
	entries, revision, err := service.sourceIndex(ctx)
	if err != nil {
		return false, err
	}
	configuration, err := service.Configuration(ctx)
	if err != nil {
		return false, err
	}
	now := service.Now()
	byId := map[string]int{}
	for index, entry := range entries {
		byId[entry.ConfigurationId] = index
	}
	put := func(identity string, source remote.Source, isSnapshot bool) {
		entry := sourceIndex{ConfigurationId: identity, Source: source, IsSnapshot: isSnapshot}
		if index, exists := byId[identity]; exists {
			entries[index] = entry
		} else {
			byId[identity] = len(entries)
			entries = append(entries, entry)
		}
	}
	jsonRoots := []history.Root{}
	jsonIds := map[string]string{}
	gravityRoots := []string{}
	gravityIds := map[string]string{}
	for _, source := range sources {
		if !source.IsEnabled {
			continue
		}
		root, err := history.ExpandHome(source.Root)
		if err != nil {
			return false, err
		}
		switch source.Provider {
		case domain.Codex, domain.Claude, domain.Grok:
			jsonRoots = append(jsonRoots, history.Root{Provider: source.Provider, Home: root})
			directory := "sessions"
			if source.Provider == domain.Claude {
				directory = "projects"
			}
			fingerprint := service.fingerprint(source.Provider, filepath.Join(root, directory))
			jsonIds[string(source.Provider)+":"+fingerprint.ResolvedHomePath] = source.Id
		case domain.Antigravity:
			gravityRoots = append(gravityRoots, root)
			gravityIds[root] = source.Id
		}
	}
	results, err := (history.Collector{Store: service.Store, HostId: service.hostId()}).Synchronize(ctx, jsonRoots, now)
	if err != nil {
		return false, err
	}
	for _, result := range results {
		put(jsonIds[string(result.Fingerprint.Provider)+":"+result.Fingerprint.ResolvedHomePath], remote.Source{Fingerprint: result.Fingerprint, Status: result.Status, ScannedFiles: result.ScannedFiles, SkippedFiles: result.SkippedFiles, MalformedRecords: result.MalformedRecords, Message: sourceMessage(result.Notice)}, false)
	}
	if len(gravityRoots) > 0 {
		result, readError := antigravity.Read(ctx, gravityRoots, now.Add(-90*24*time.Hour).UnixMilli())
		for _, root := range gravityRoots {
			fingerprint := service.fingerprint(domain.Antigravity, root)
			records := []domain.Record{}
			fileCount := 0
			for _, file := range result.Files {
				if file.Root == root {
					records = append(records, file.Records...)
					fileCount++
				}
			}
			status, notice := "ok", ""
			hasRootError := readError != nil
			for _, failedPath := range result.Errors {
				if failedPath == root || strings.HasPrefix(failedPath, root+string(filepath.Separator)) {
					hasRootError = true
				}
			}
			if hasRootError {
				status, notice = "partial", "Some Antigravity databases could not be read. Cached history is retained."
			}
			if fileCount == 0 && status == "ok" {
				status, notice = "missing", "No Antigravity conversation databases."
			}
			if err := service.storeSnapshot(ctx, fingerprint, records, status, notice, now); err != nil {
				return false, err
			}
			put(gravityIds[root], remote.Source{Fingerprint: fingerprint, Status: status, ScannedFiles: fileCount, Message: sourceMessage(notice)}, true)
		}
	}
	for _, source := range sources {
		if !source.IsEnabled {
			continue
		}
		root, err := history.ExpandHome(source.Root)
		if err != nil {
			return false, err
		}
		switch source.Provider {
		case domain.OpenCode:
			result, readError := opencode.Read(ctx, root, now.Add(-90*24*time.Hour).UnixMilli())
			fingerprint := service.fingerprint(source.Provider, root)
			records := []domain.Record{}
			for _, file := range result.Files {
				records = append(records, file.Records...)
			}
			status, notice := "ok", strings.Join(result.Notices, " ")
			if readError != nil || result.HasError {
				status = "partial"
				notice = "Some OpenCode history could not be read. Cached history is retained."
			} else if result.IsMissing {
				status = "missing"
			}
			if err := service.storeSnapshot(ctx, fingerprint, records, status, notice, now); err != nil {
				return false, err
			}
			put(source.Id, remote.Source{Fingerprint: fingerprint, Status: status, ScannedFiles: len(result.Files), Message: sourceMessage(notice)}, true)
		case domain.Cursor:
			credentials := cursor.DiscoverCredentials(service.Platform, service.Home, service.Environment, configuration.Value.IsCursorKeychainEnabled)
			if !credentials.IsDisabled && !credentials.IsKeychain {
				credentials.Path = root
			}
			result := cursor.Read(ctx, cursor.Options{Credentials: credentials, KeychainToken: service.keychain.Read}, now.Add(-90*24*time.Hour).UnixMilli(), now.UnixMilli())
			if result.IsMissing && result.Error == "" {
				if credentials.NeedsKeychainOptIn {
					put(source.Id, remote.Source{Fingerprint: service.fingerprint(domain.Cursor, root), Status: "missing", Message: sourceMessage(result.Notice), Action: "enableCursorKeychain"}, true)
					entries[byId[source.Id]].ShouldExcludeRecords = true
				} else if previous, exists := byId[source.Id]; exists {
					entries[previous].IsHidden = true
					entries[previous].ShouldExcludeRecords = true
				}
				continue
			}
			fingerprint := result.SourceIdentity()
			if result.AccountKey == "" {
				if previous, exists := byId[source.Id]; exists {
					fingerprint = entries[previous].Source.Fingerprint
				} else {
					fingerprint = service.fingerprint(domain.Cursor, root)
				}
			}
			status, notice := "ok", result.Notice
			if result.Error != "" {
				status, notice = "failed", result.Error
			} else if result.IsMissing || credentials.IsDisabled {
				status = "missing"
			}
			if err := service.storeSnapshot(ctx, fingerprint, result.Records, status, notice, now); err != nil {
				return false, err
			}
			action := ""
			if service.Platform == "darwin" && credentials.IsDisabled && !configuration.Value.IsCursorKeychainEnabled {
				action = "enableCursorKeychain"
			}
			put(source.Id, remote.Source{Fingerprint: fingerprint, Status: status, ScannedFiles: 1, Message: sourceMessage(notice), Action: action}, true)
		}
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

// storeSnapshot replaces mutable database/dashboard facts only after a complete successful read.
func (service *Service) storeSnapshot(ctx context.Context, fingerprint domain.SourceFingerprint, records []domain.Record, status, notice string, now time.Time) error {
	id, err := service.Store.RegisterSource(ctx, fingerprint)
	if err != nil {
		return err
	}
	if status == "ok" {
		checkpoint, err := service.Store.Checkpoint(ctx, id, "complete-snapshot")
		if err != nil {
			return err
		}
		for index := range records {
			records[index].SourcePath = fingerprint.ResolvedHomePath
		}
		checkpoint.ModifiedNs = now.UnixNano()
		checkpoint.ParserVersion = 1
		if _, err = service.Store.CommitFile(ctx, checkpoint, records, true); err != nil {
			return err
		}
	}
	observation := domain.Observation{SourceId: id, Status: status, AttemptedAt: now.UnixMilli(), Notice: notice}
	if status == "ok" {
		success := now.UnixMilli()
		observation.SucceededAt = &success
	}
	return service.Store.Observe(ctx, observation)
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
	rates, err := service.Rates(ctx)
	if err != nil {
		return summary, err
	}
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
		if entry.IsHidden || !service.isProviderInScope(entry.Source.Fingerprint.Provider) {
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
