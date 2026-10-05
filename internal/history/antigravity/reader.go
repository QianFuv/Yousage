package antigravity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	_ "modernc.org/sqlite"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"yousage/internal/domain"
)

type File struct {
	Root    string          `json:"root"`
	Path    string          `json:"path"`
	Records []domain.Record `json:"records"`
}
type Result struct {
	Files  []File   `json:"files"`
	Errors []string `json:"errors"`
}
type candidate struct {
	record  domain.Record
	keys    []string
	quality int
}
type indexedMetadata struct {
	index int64
	value metadata
}

func readDatabase(ctx context.Context, path string, fallback int64) ([]candidate, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	uri := &url.URL{Scheme: "file", Path: filepath.ToSlash(absolute)}
	if !strings.HasPrefix(uri.Path, "/") {
		uri.Path = "/" + uri.Path
	}
	query := url.Values{"mode": {"ro"}, "_pragma": {"query_only(1)", "busy_timeout(100)"}, "_txlock": {"deferred"}}
	uri.RawQuery = query.Encode()
	database, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, err
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	transaction, err := database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer transaction.Rollback()
	tables := map[string]bool{}
	rows, err := transaction.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type='table'")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			rows.Close()
			return nil, err
		}
		tables[name] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if !tables["gen_metadata"] && !tables["steps"] {
		return nil, errors.New("missing usage tables")
	}
	read := func(query string, isStep bool) ([]indexedMetadata, error) {
		entries := []indexedMetadata{}
		rows, err := transaction.QueryContext(ctx, query)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var index, blob any
			if err = rows.Scan(&index, &blob); err != nil {
				return nil, err
			}
			ordinal, ok := index.(int64)
			if !ok {
				return nil, errors.New("invalid metadata index")
			}
			data, ok := blob.([]byte)
			if !ok {
				return nil, errors.New("invalid metadata blob")
			}
			value, err := parseMetadata(data, isStep)
			if err != nil {
				return nil, err
			}
			entries = append(entries, indexedMetadata{ordinal, value})
		}
		return entries, rows.Err()
	}
	var generations, steps []indexedMetadata
	if tables["gen_metadata"] {
		generations, err = read("SELECT idx,data FROM gen_metadata ORDER BY idx", false)
	}
	if err != nil {
		return nil, err
	}
	var trajectory *int64
	if tables["trajectory_metadata_blob"] {
		rows, err := transaction.QueryContext(ctx, "SELECT data FROM trajectory_metadata_blob")
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var blob any
			if err = rows.Scan(&blob); err != nil {
				rows.Close()
				return nil, err
			}
			data, ok := blob.([]byte)
			if !ok {
				rows.Close()
				return nil, errors.New("invalid trajectory blob")
			}
			value, err := parseFields(data)
			if err != nil {
				rows.Close()
				return nil, err
			}
			value, err = nested(value, 2)
			if err != nil {
				rows.Close()
				return nil, err
			}
			if trajectory == nil {
				trajectory = timestamp(value)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	if tables["steps"] {
		steps, err = read("SELECT idx,metadata FROM steps WHERE metadata IS NOT NULL ORDER BY idx", true)
	}
	if err != nil {
		return nil, err
	}
	models := map[int64]string{}
	for _, entry := range generations {
		models[entry.index] = entry.value.model
	}
	session := strings.TrimSuffix(filepath.Base(path), ".db")
	result := []candidate{}
	for sourceIndex, entries := range [][]indexedMetadata{steps, generations} {
		source := "step"
		if sourceIndex == 1 {
			source = "generation"
		}
		for index, entry := range entries {
			for usageIndex, usage := range entry.value.usages {
				output := max(numberAt(usage, 3), numberAt(usage, 9)+numberAt(usage, 10))
				totals := domain.Tokens{UncachedInputTokens: numberAt(usage, 2), CachedInputTokens: numberAt(usage, 5), CacheCreationTokens: numberAt(usage, 4), OutputTokens: output, ReasoningTokens: min(output, numberAt(usage, 9))}
				if totals.Total() == 0 {
					continue
				}
				keys := []string{}
				for _, key := range []uint64{11, 12, 7} {
					identity, err := textAt(usage, key)
					if err != nil {
						return nil, err
					}
					if identity != "" {
						keys = append(keys, fmt.Sprintf("antigravity:%d:%s", key, identity))
					}
				}
				model := modelIds[numberAt(usage, 1)]
				if model == "" {
					model = entry.value.model
				}
				if model == "" && sourceIndex == 0 {
					model = models[entry.index]
				}
				if model == "" {
					model = modelName("", numberAt(usage, 1))
				}
				if model == "" {
					model = "antigravity-unknown"
				}
				instant, quality := fallback, 0
				if trajectory != nil {
					instant, quality = *trajectory, 1
				}
				if entry.value.instant != nil {
					instant, quality = *entry.value.instant, 2
				}
				dedupe := fmt.Sprintf("antigravity:%s:%s:%d:%d", session, source, index, usageIndex)
				if len(keys) > 0 {
					dedupe = keys[0]
				}
				result = append(result, candidate{domain.Record{Provider: domain.Antigravity, SessionId: session, TimestampMs: instant, Model: model, Totals: totals, Speed: "standard", DedupeKey: dedupe}, keys, quality})
			}
		}
	}
	if err = transaction.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

type group struct {
	candidate
	parent, size, owner, fileIndex int
}

// Read merges transitive aliases across all roots before applying the date boundary.
func Read(ctx context.Context, roots []string, sinceMs int64) (Result, error) {
	result := Result{Files: []File{}, Errors: []string{}}
	groups := []group{}
	identities := map[string]int{}
	find := func(index int) int {
		root := index
		for groups[root].parent != root {
			root = groups[root].parent
		}
		for index != root {
			parent := groups[index].parent
			groups[index].parent = root
			index = parent
		}
		return root
	}
	merge := func(left, right int) {
		firstRoot, secondRoot := find(left), find(right)
		if firstRoot == secondRoot {
			return
		}
		if groups[firstRoot].size < groups[secondRoot].size {
			firstRoot, secondRoot = secondRoot, firstRoot
		}
		target, source := groups[firstRoot], groups[secondRoot]
		first, other := target, source
		if source.owner < target.owner {
			first, other = source, target
		}
		bestTime := target
		if source.quality > target.quality || (source.quality == target.quality && source.record.TimestampMs < target.record.TimestampMs) {
			bestTime = source
		}
		record := first.record
		if record.Model == "antigravity-unknown" {
			record.Model = other.record.Model
		}
		record.TimestampMs = bestTime.record.TimestampMs
		x, y := target.record.Totals, source.record.Totals
		record.Totals = domain.Tokens{UncachedInputTokens: max(x.UncachedInputTokens, y.UncachedInputTokens), CachedInputTokens: max(x.CachedInputTokens, y.CachedInputTokens), CacheCreationTokens: max(x.CacheCreationTokens, y.CacheCreationTokens), OutputTokens: max(x.OutputTokens, y.OutputTokens), ReasoningTokens: max(x.ReasoningTokens, y.ReasoningTokens)}
		target.record, target.quality, target.owner, target.fileIndex = record, bestTime.quality, first.owner, first.fileIndex
		target.size += source.size
		source.parent = firstRoot
		groups[firstRoot], groups[secondRoot] = target, source
	}
	visited := map[string]bool{}
	var walk func(string, string) error
	walk = func(directory, root string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		entries, err := os.ReadDir(directory)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				result.Errors = append(result.Errors, directory)
			}
			return nil
		}
		for _, entry := range entries {
			path := filepath.Join(directory, entry.Name())
			if entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			if entry.IsDir() {
				if err := walk(path, root); err != nil {
					return err
				}
				continue
			}
			if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".db") {
				continue
			}
			canonical, err := filepath.EvalSymlinks(path)
			if err != nil {
				result.Errors = append(result.Errors, path)
				continue
			}
			if visited[canonical] {
				continue
			}
			visited[canonical] = true
			info, err := entry.Info()
			if err != nil {
				result.Errors = append(result.Errors, path)
				continue
			}
			candidates, err := readDatabase(ctx, path, info.ModTime().UnixMilli())
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				result.Errors = append(result.Errors, path)
				continue
			}
			fileIndex := len(result.Files)
			result.Files = append(result.Files, File{root, path, []domain.Record{}})
			for _, candidate := range candidates {
				index := len(groups)
				groups = append(groups, group{candidate, index, 1, index, fileIndex})
				for _, key := range candidate.keys {
					if existing, ok := identities[key]; ok {
						merge(index, existing)
					}
					identities[key] = index
				}
			}
		}
		return nil
	}
	for _, root := range roots {
		if err := walk(root, root); err != nil {
			return result, err
		}
	}
	for index, group := range groups {
		if group.parent == index && group.record.TimestampMs >= sinceMs {
			result.Files[group.fileIndex].Records = append(result.Files[group.fileIndex].Records, group.record)
		}
	}
	return result, nil
}

// DiscoverRoots accepts already resolved T3 profile directories and never creates provider data.
func DiscoverRoots(home string, profileDirectories []string) ([]string, error) {
	candidates := []string{}
	for _, value := range strings.Split(os.Getenv("ANTIGRAVITY_DATA_DIR"), ",") {
		if value = strings.TrimSpace(value); value != "" {
			candidates = append(candidates, value)
		}
	}
	if len(candidates) == 0 {
		for _, name := range []string{"antigravity", "antigravity-cli", "antigravity-ide", "antigravity-backup"} {
			candidates = append(candidates, filepath.Join(home, ".gemini", name))
		}
		candidates = append(candidates, filepath.Join(home, ".config", "antigravity"))
	}
	for _, profile := range profileDirectories {
		candidates = append(candidates, filepath.Join(profile, "antigravity-acp"))
	}
	result := []string{}
	seen := map[string]bool{}
	for _, path := range candidates {
		if path == "~" {
			path = home
		} else if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, "~\\") {
			path = filepath.Join(home, path[2:])
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		if canonical, err := filepath.EvalSymlinks(absolute); err == nil {
			absolute = canonical
		}
		nested := filepath.Join(absolute, "conversations")
		if _, err := os.Stat(nested); err == nil {
			absolute = nested
		}
		if canonical, err := filepath.EvalSymlinks(absolute); err == nil {
			absolute = canonical
		}
		if !seen[absolute] {
			result = append(result, absolute)
			seen[absolute] = true
		}
	}
	return result, nil
}
