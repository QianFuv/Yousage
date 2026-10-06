package service

import (
	"context"
	"errors"
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

// RefreshHistory coalesces scans and keeps full retention independent of the current viewer window.
func (service *Service) RefreshHistory(ctx context.Context, request refresh.Request) error {
	_, err := service.history.Run(ctx, "local-history", request, service.scanHistory)
	if errors.Is(err, refresh.ErrSkipped) {
		return nil
	}
	return err
}
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
	service.emit("usage-invalidated", nil)
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
	options := accounting.AggregateOptions{Window: window, Rates: rates.Rates, Overrides: accounting.OverrideRates(pricing.Value.Overrides), Aliases: accounting.ResolveAliases(pricing.Value.Aliases)}
	files := [][]domain.Record{}
	owners := []int{}
	seen := map[string]bool{}
	cutoff := max(windowStart(window).Add(-36*time.Hour).UnixNano(), now.Add(-90*24*time.Hour).UnixNano())
	for _, entry := range entries {
		if entry.IsHidden || !service.isProviderInScope(entry.Source.Fingerprint.Provider) {
			continue
		}
		id := entry.Source.Fingerprint.Id()
		if seen[id] {
			continue
		}
		seen[id] = true
		position := len(summary.Sources)
		summary.Sources = append(summary.Sources, entry.Source)
		if entry.ShouldExcludeRecords {
			summary.Sources[position].DistinctSessions = 0
			continue
		}
		modifiedSince := cutoff
		if entry.IsSnapshot {
			modifiedSince = now.Add(-90 * 24 * time.Hour).UnixNano()
		}
		fileRecords, err := service.Store.FileRecordsSince(ctx, id, modifiedSince)
		if err != nil {
			return summary, err
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
	bySource := make([][]domain.Record, len(summary.Sources))
	seenRecords := map[string]bool{}
	for index, records := range deduped {
		owner := owners[index]
		for _, record := range records {
			if record.DedupeKey != "" {
				if seenRecords[record.DedupeKey] {
					continue
				}
				seenRecords[record.DedupeKey] = true
			}
			record.SourcePath = summary.Sources[owner].Fingerprint.ResolvedHomePath
			bySource[owner] = append(bySource[owner], record)
		}
	}
	for index, records := range bySource {
		aggregate, err := accounting.Aggregate(records, options)
		if err != nil {
			return summary, err
		}
		summary.Sources[index].DistinctSessions = aggregate.DistinctSessions
		summary.Buckets = append(summary.Buckets, aggregate.Buckets...)
	}
	summary.ScanDurationMs = max(0, service.Now().Sub(now).Milliseconds())
	return summary, nil
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
