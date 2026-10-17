package history

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"yousage/internal/domain"
	"yousage/internal/storage"
)

type Root struct {
	Provider domain.Provider `json:"provider"`
	Home     string          `json:"home"`
}
type SourceResult struct {
	Fingerprint      domain.SourceFingerprint `json:"fingerprint"`
	Status           string                   `json:"status"`
	Records          []domain.Record          `json:"records"`
	ScannedFiles     int                      `json:"scannedFiles"`
	SkippedFiles     int                      `json:"skippedFiles"`
	MalformedRecords int                      `json:"malformedRecords"`
	MalformedLines   int                      `json:"malformedLines"`
	Notice           string                   `json:"notice,omitempty"`
	files            [][]domain.Record
}
type Collector struct {
	Store  *storage.Store
	HostId string
}

// DefaultRoots reads provider home overrides without reading their credentials.
func DefaultRoots() ([]Root, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	codex, claude := strings.TrimSpace(os.Getenv("CODEX_HOME")), strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR"))
	if codex == "" {
		codex = filepath.Join(home, ".codex")
	}
	if claude == "" {
		claude = filepath.Join(home, ".claude")
	}
	return []Root{{domain.Codex, codex}, {domain.Claude, claude}}, nil
}
func ExpandHome(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, "~\\") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if path == "~" {
			path = home
		} else {
			path = filepath.Join(home, path[2:])
		}
	}
	return filepath.Abs(path)
}

// Scan keeps disabled-account history readable; account enablement is a separate quota setting.
func (collector Collector) Scan(ctx context.Context, roots []Root, now time.Time) ([]SourceResult, error) {
	return collector.scan(ctx, roots, now, true)
}

// Synchronize updates incremental facts and observations without allocating a redundant full-history result.
func (collector Collector) Synchronize(ctx context.Context, roots []Root, now time.Time) ([]SourceResult, error) {
	return collector.scan(ctx, roots, now, false)
}

func (collector Collector) scan(ctx context.Context, roots []Root, now time.Time, shouldReadRecords bool) ([]SourceResult, error) {
	results := []SourceResult{}
	seen := map[string]bool{}
	for _, root := range roots {
		if root.Provider != domain.Codex && root.Provider != domain.Claude {
			return nil, errors.New("unsupported history root")
		}
		home, err := ExpandHome(root.Home)
		if err != nil {
			return nil, err
		}
		subdirectory := "sessions"
		if root.Provider == domain.Claude {
			subdirectory = "projects"
		}
		directory := filepath.Join(home, subdirectory)
		key := string(root.Provider) + ":" + directory
		var previous domain.SourceFingerprint
		revision, err := collector.Store.ReadSetting(ctx, "history-source:"+key, &previous)
		if err != nil {
			return nil, err
		}
		canonical, err := filepath.EvalSymlinks(directory)
		if err != nil {
			canonical = directory
			if previous.ResolvedHomePath != "" {
				canonical = previous.ResolvedHomePath
			}
		}
		fingerprint := domain.SourceFingerprint{HostId: collector.HostId, Provider: root.Provider, ResolvedHomePath: canonical, VolumeId: directoryVolumeId(canonical)}
		physicalKey := string(root.Provider) + ":" + canonical
		var physicalPrevious domain.SourceFingerprint
		physicalRevision, err := collector.Store.ReadSetting(ctx, "history-physical:"+physicalKey, &physicalPrevious)
		if err != nil {
			return nil, err
		}
		identityPrevious := previous
		if physicalPrevious.ResolvedHomePath == canonical {
			identityPrevious = physicalPrevious
		}
		if identityPrevious.ResolvedHomePath == canonical {
			hasHistory, err := collector.Store.HasRetainedHistory(ctx, identityPrevious.Id(), now.Add(-90*24*time.Hour).UnixNano())
			if err != nil {
				return nil, err
			}
			if hasHistory || fingerprint.VolumeId == "" {
				fingerprint.VolumeId = identityPrevious.VolumeId
			}
		}
		if _, err := collector.Store.RegisterSource(ctx, fingerprint); err != nil {
			return nil, err
		}
		if fingerprint != previous {
			if _, err := collector.Store.WriteSetting(ctx, "history-source:"+key, revision, fingerprint); err != nil {
				return nil, err
			}
		}
		if fingerprint != physicalPrevious {
			if _, err := collector.Store.WriteSetting(ctx, "history-physical:"+physicalKey, physicalRevision, fingerprint); err != nil {
				return nil, err
			}
		}
		if seen[physicalKey] {
			continue
		}
		seen[physicalKey] = true
		result, err := collector.scanSource(ctx, fingerprint, now, shouldReadRecords)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	if !shouldReadRecords {
		return results, nil
	}
	allFiles := [][]domain.Record{}
	for _, result := range results {
		allFiles = append(allFiles, result.files...)
	}
	deduped := DedupeMovedCodex(allFiles)
	fileIndex := 0
	for index := range results {
		results[index].Records = []domain.Record{}
		for range results[index].files {
			for _, record := range deduped[fileIndex] {
				record.SourcePath = results[index].Fingerprint.ResolvedHomePath
				results[index].Records = append(results[index].Records, record)
			}
			fileIndex++
		}
	}
	return results, nil
}

// scanSource reuses directory metadata and reads checkpoint state in one batch.
func (collector Collector) scanSource(ctx context.Context, fingerprint domain.SourceFingerprint, now time.Time, shouldReadRecords bool) (SourceResult, error) {
	result := SourceResult{Fingerprint: fingerprint, Status: "ok", Records: []domain.Record{}}
	id := fingerprint.Id()
	cutoff := now.Add(-90 * 24 * time.Hour)
	paths := []string{}
	fileInfo := map[string]os.FileInfo{}
	walkError := filepath.WalkDir(fingerprint.ResolvedHomePath, func(path string, entry fs.DirEntry, err error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err != nil {
			if path == fingerprint.ResolvedHomePath {
				return err
			}
			result.Status = "partial"
			result.Notice = "Some history files could not be read."
			return nil
		}
		if path == fingerprint.ResolvedHomePath && !entry.IsDir() {
			return errors.New("history root is not a directory")
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			return nil
		}
		info, err := entry.Info()
		if entry.Type()&os.ModeSymlink != 0 {
			info, err = os.Stat(path)
		}
		if err != nil {
			result.Status = "partial"
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if info.ModTime().Before(cutoff) {
			result.SkippedFiles++
			return nil
		}
		paths = append(paths, path)
		fileInfo[path] = info
		return nil
	})
	if walkError != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		result.Status = "failed"
		result.Notice = "History directory could not be read."
		if errors.Is(walkError, fs.ErrNotExist) {
			result.Status = "missing"
			result.Notice = "No transcript directory."
		}
	}
	sort.Strings(paths)
	checkpoints, err := collector.Store.Checkpoints(ctx, id)
	if err != nil {
		return result, err
	}
	previousByPath := map[string]domain.Checkpoint{}
	for _, checkpoint := range checkpoints {
		previousByPath[checkpoint.Path] = checkpoint
	}
	changed := []string{}
	for _, path := range paths {
		previous := previousByPath[path]
		info := fileInfo[path]
		if previous.Revision > 0 && previous.ParserVersion == ParserVersion && previous.FileSize == info.Size() && previous.ModifiedNs == info.ModTime().UnixNano() {
			result.ScannedFiles++
			continue
		}
		changed = append(changed, path)
	}
	for offset := 0; offset < len(changed); offset += 4 {
		batch := changed[offset:min(offset+4, len(changed))]
		reads := readTranscriptBatch(ctx, fingerprint.Provider, id, batch, previousByPath)
		for index, path := range batch {
			read, readError := reads[index].value, reads[index].err
			if readError != nil {
				if ctx.Err() != nil {
					return result, ctx.Err()
				}
				result.Status, result.Notice = "partial", "A history read failed; saved facts were retained."
				continue
			}
			seenRecords := map[string]bool{}
			if read.IsResumed {
				committed, err := collector.Store.CommittedFileRecords(ctx, id, path)
				if err != nil {
					return result, err
				}
				for _, record := range committed {
					if record.DedupeKey != "" {
						seenRecords[record.DedupeKey] = true
					}
				}
				if fingerprint.Provider == domain.Claude {
					previous := previousByPath[path]
					shouldReplay := previous.Offset < previous.FileSize
					for _, records := range [][]domain.Record{read.Records, read.Tail} {
						for _, record := range records {
							shouldReplay = shouldReplay || seenRecords[record.DedupeKey]
						}
					}
					if shouldReplay {
						previous.ParserVersion = 0
						read, err = ReadTranscript(ctx, path, fingerprint.Provider, previous)
						if err != nil {
							if ctx.Err() != nil {
								return result, ctx.Err()
							}
							result.Status, result.Notice = "partial", "A history read failed; saved facts were retained."
							continue
						}
						seenRecords = map[string]bool{}
					}
				}
			}
			unique := func(records []domain.Record) []domain.Record {
				output := []domain.Record{}
				for _, record := range records {
					if record.DedupeKey != "" {
						if seenRecords[record.DedupeKey] {
							continue
						}
						seenRecords[record.DedupeKey] = true
					}
					output = append(output, record)
				}
				return output
			}
			read.Records = unique(read.Records)
			read.Tail = unique(read.Tail)
			if _, err := collector.Store.CommitFileWithTail(ctx, read.Checkpoint, read.Records, read.Tail, !read.IsResumed); err != nil {
				if errors.Is(err, storage.ErrConflict) {
					result.Status = "partial"
					result.Notice = "A newer scan completed first; its saved usage was retained."
					continue
				}
				return result, err
			}
			result.ScannedFiles++
			result.MalformedLines += read.Malformed
		}
	}
	if err := collector.Store.PruneHistory(ctx, id, cutoff.UnixNano()); err != nil {
		return result, err
	}
	counts, err := collector.Store.FileRecordCounts(ctx, id)
	if err != nil {
		return result, err
	}
	filePaths := []string{}
	for path := range counts {
		filePaths = append(filePaths, path)
	}
	sort.Strings(filePaths)
	result.ScannedFiles = len(filePaths)
	for _, path := range paths {
		if counts[path] == 0 {
			result.SkippedFiles++
		}
	}
	if shouldReadRecords {
		files, err := collector.Store.FileRecords(ctx, id)
		if err != nil {
			return result, err
		}
		for _, path := range filePaths {
			result.files = append(result.files, files[path])
			result.Records = append(result.Records, files[path]...)
		}
	}
	if result.Status == "missing" && len(counts) > 0 {
		result.Status = "ok"
		result.Notice = "Original history removed; showing saved usage."
	}
	observation := domain.Observation{SourceId: id, Status: result.Status, AttemptedAt: now.UnixMilli(), Notice: result.Notice}
	if result.Status == "ok" {
		success := now.UnixMilli()
		observation.SucceededAt = &success
	}
	if err := collector.Store.Observe(ctx, observation); err != nil {
		return result, err
	}
	return result, nil
}

// transcriptResult retains only projected facts until the ordered commit consumes them.
type transcriptResult struct {
	value FileRead
	err   error
}

// readTranscriptBatch parses at most four files concurrently and preserves commit order.
func readTranscriptBatch(ctx context.Context, provider domain.Provider, sourceId string, paths []string, checkpoints map[string]domain.Checkpoint) []transcriptResult {
	results := make([]transcriptResult, len(paths))
	var pending sync.WaitGroup
	for index, path := range paths {
		previous, exists := checkpoints[path]
		if !exists {
			previous = domain.Checkpoint{SourceId: sourceId, Path: path, Reducer: []byte(`{}`)}
		}
		pending.Go(func() { results[index].value, results[index].err = ReadTranscript(ctx, path, provider, previous) })
	}
	pending.Wait()
	return results
}
