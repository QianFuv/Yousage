// Package opencode reads native SQLite snapshots and pre-migration JSON messages.
package opencode

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	_ "modernc.org/sqlite"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"yousage/internal/domain"
)

type Fallback struct {
	Id, SessionId string
	TimestampMs   *int64
}
type File struct {
	Path    string          `json:"path"`
	Records []domain.Record `json:"records"`
}
type Result struct {
	Files     []File   `json:"files"`
	IsMissing bool     `json:"missing"`
	HasError  bool     `json:"error"`
	Notices   []string `json:"notices"`
}

var databaseName = regexp.MustCompile(`^opencode(?:-[a-zA-Z0-9_-]+)?\.db$`)

func object(value any) map[string]any { result, _ := value.(map[string]any); return result }
func text(value any) string           { result, _ := value.(string); return result }
func tokens(value any) int64 {
	number, ok := value.(float64)
	if !ok || !domain.Finite(number) || number <= 0 || number >= float64(math.MaxInt64) {
		return 0
	}
	return int64(number)
}

// ParseMessage adds separately reported reasoning into the inclusive normalized output category.
func ParseMessage(data []byte, fallback Fallback) *domain.Record {
	var message map[string]any
	if json.Unmarshal(data, &message) != nil {
		return nil
	}
	if role, exists := message["role"]; exists && role != "assistant" {
		return nil
	}
	usage := object(message["tokens"])
	cache := object(usage["cache"])
	modelReference := object(message["model"])
	model := text(modelReference["id"])
	if model == "" {
		model = text(modelReference["modelID"])
	}
	if model == "" {
		model = text(message["modelID"])
	}
	created := object(message["time"])["created"]
	if created == nil && fallback.TimestampMs != nil {
		created = float64(*fallback.TimestampMs)
	}
	instant, ok := created.(float64)
	if !ok || !domain.Finite(instant) || instant >= float64(math.MaxInt64) || instant <= float64(math.MinInt64) || model == "" {
		return nil
	}
	reasoning := tokens(usage["reasoning"])
	totals := domain.Tokens{UncachedInputTokens: tokens(usage["input"]), CachedInputTokens: tokens(cache["read"]), CacheCreationTokens: tokens(cache["write"]), OutputTokens: tokens(usage["output"]) + reasoning, ReasoningTokens: reasoning}
	if totals.Total() == 0 {
		return nil
	}
	id, sessionId := fallback.Id, fallback.SessionId
	if id == "" {
		id = text(message["id"])
	}
	if sessionId == "" {
		sessionId = text(message["sessionID"])
	}
	record := &domain.Record{Provider: domain.OpenCode, TimestampMs: int64(instant), Model: model, SessionId: sessionId, Totals: totals, Speed: "standard"}
	if id != "" {
		record.DedupeKey = "opencode:" + id
	}
	if cost, ok := message["cost"].(float64); ok && domain.Finite(cost) && cost > 0 {
		record.ReportedCostUsd = &cost
	}
	return record
}

// Read opens provider databases read-only; a deferred transaction keeps all tables in one WAL snapshot.
func Read(ctx context.Context, root string, sinceMs int64) (Result, error) {
	result := Result{Files: []File{}, Notices: []string{}}
	seen := map[string]bool{}
	hasFound := false
	appendRecord := func(file *File, record *domain.Record) {
		if record == nil || record.TimestampMs < sinceMs {
			return
		}
		if record.DedupeKey != "" {
			if seen[record.DedupeKey] {
				return
			}
			seen[record.DedupeKey] = true
		}
		file.Records = append(file.Records, *record)
	}
	entries, err := os.ReadDir(root)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		result.HasError = true
		result.Notices = append(result.Notices, "OpenCode root could not be listed.")
	}
	names := []string{}
	for _, entry := range entries {
		if entry.Type().IsRegular() && databaseName.MatchString(entry.Name()) {
			names = append(names, entry.Name())
		}
	}
	sort.Slice(names, func(first, second int) bool {
		if names[first] == "opencode.db" {
			return names[second] != "opencode.db"
		}
		if names[second] == "opencode.db" {
			return false
		}
		return names[first] < names[second]
	})
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		hasFound = true
		file := File{Path: filepath.Join(root, name), Records: []domain.Record{}}
		if err := readDatabase(ctx, file.Path, sinceMs, func(record *domain.Record) { appendRecord(&file, record) }); err != nil {
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			result.HasError = true
			result.Notices = append(result.Notices, fmt.Sprintf("%s: SQLite history unavailable (%v)", name, err))
		}
		result.Files = append(result.Files, file)
	}
	directories := []string{filepath.Join(root, "storage", "message")}
	for len(directories) > 0 {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		directory := directories[len(directories)-1]
		directories = directories[:len(directories)-1]
		entries, err := os.ReadDir(directory)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				result.HasError = true
				result.Notices = append(result.Notices, "A legacy OpenCode directory could not be read.")
			}
			continue
		}
		for _, entry := range entries {
			path := filepath.Join(directory, entry.Name())
			if entry.IsDir() {
				directories = append(directories, path)
				continue
			}
			if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			hasFound = true
			id := strings.TrimSuffix(entry.Name(), ".json")
			if seen["opencode:"+id] {
				continue
			}
			file := File{Path: path, Records: []domain.Record{}}
			data, err := os.ReadFile(path)
			if err != nil {
				if !errors.Is(err, os.ErrNotExist) {
					result.HasError = true
					result.Notices = append(result.Notices, "A legacy OpenCode message could not be read.")
				}
			} else {
				appendRecord(&file, ParseMessage(data, Fallback{Id: id}))
			}
			result.Files = append(result.Files, file)
		}
	}
	result.IsMissing = !hasFound && !result.HasError
	return result, nil
}

func readDatabase(ctx context.Context, path string, sinceMs int64, appendRecord func(*domain.Record)) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	uriPath := filepath.ToSlash(absolute)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	uri := url.URL{Scheme: "file", Path: uriPath}
	query := url.Values{"mode": {"ro"}, "_txlock": {"deferred"}, "_pragma": {"busy_timeout(100)", "query_only(1)"}}
	uri.RawQuery = query.Encode()
	database, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return err
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	transaction, err := database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	rows, err := transaction.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type='table'")
	if err != nil {
		return err
	}
	tables := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		tables[name] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if !tables["message"] && !tables["session_message"] {
		return errors.New("no supported message table")
	}
	for _, table := range []string{"message", "session_message"} {
		if !tables[table] {
			continue
		}
		columns, err := tableColumns(ctx, transaction, table)
		if err != nil {
			return err
		}
		for _, required := range []string{"id", "session_id", "data"} {
			if !columns[required] {
				return fmt.Errorf("%s missing %s column", table, required)
			}
		}
		created := "NULL"
		if columns["time_created"] {
			created = "time_created"
		}
		predicates := []string{}
		parameters := []any{}
		if table == "session_message" {
			predicates = append(predicates, "type='assistant'")
		}
		if created != "NULL" {
			predicates = append(predicates, "time_created>=?")
			parameters = append(parameters, sinceMs)
		}
		where := ""
		if len(predicates) > 0 {
			where = " WHERE " + strings.Join(predicates, " AND ")
		}
		rows, err := transaction.QueryContext(ctx, "SELECT id,session_id,data,"+created+" FROM "+table+where, parameters...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id, sessionId, data sql.NullString
			var created any
			if err := rows.Scan(&id, &sessionId, &data, &created); err != nil {
				rows.Close()
				return err
			}
			fallback := Fallback{Id: id.String, SessionId: sessionId.String}
			switch value := created.(type) {
			case int64:
				fallback.TimestampMs = &value
			case float64:
				if domain.Finite(value) && value < float64(math.MaxInt64) && value > float64(math.MinInt64) {
					instant := int64(value)
					fallback.TimestampMs = &instant
				}
			}
			appendRecord(ParseMessage([]byte(data.String), fallback))
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
	}
	return transaction.Commit()
}
func tableColumns(ctx context.Context, transaction *sql.Tx, table string) (map[string]bool, error) {
	rows, err := transaction.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns := map[string]bool{}
	for rows.Next() {
		var ordinal, notNull, primary int
		var name, kind string
		var defaultValue any
		if err := rows.Scan(&ordinal, &name, &kind, &notNull, &defaultValue, &primary); err != nil {
			return nil, err
		}
		columns[name] = true
	}
	return columns, rows.Err()
}

func DefaultRoot(home string) string {
	dataHome := strings.TrimSpace(os.Getenv("XDG_DATA_HOME"))
	if dataHome == "" || !filepath.IsAbs(dataHome) {
		dataHome = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dataHome, "opencode")
}

func DiscoverRoots(home string) ([]string, error) {
	roots := []string{}
	seen := map[string]bool{}
	configured := strings.TrimSpace(os.Getenv("OPENCODE_DATA_DIR"))
	candidates := strings.Split(configured, ",")
	if configured == "" {
		candidates = []string{DefaultRoot(home)}
	}
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if candidate == "~" {
			candidate = home
		} else if strings.HasPrefix(candidate, "~/") || strings.HasPrefix(candidate, "~\\") {
			candidate = filepath.Join(home, candidate[2:])
		}
		absolute, err := filepath.Abs(candidate)
		if err != nil {
			return nil, err
		}
		canonical, err := filepath.EvalSymlinks(absolute)
		if err != nil {
			canonical = absolute
		}
		if !seen[canonical] {
			seen[canonical] = true
			roots = append(roots, canonical)
		}
	}
	if len(roots) == 0 && configured != "" {
		return []string{DefaultRoot(home)}, nil
	}
	return roots, nil
}
