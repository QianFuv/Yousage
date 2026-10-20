package diagnostics

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"
)

const (
	// MaxFileBytes includes all JSONL newlines in one active or rotated file.
	MaxFileBytes = 256 * 1024
	// MaxRecordBytes includes the record's terminating newline.
	MaxRecordBytes = 2 * 1024
	// MaxReportBytes includes the header and any truncation indicator.
	MaxReportBytes = 128 * 1024
	// Retention is the maximum age of files and exported records.
	Retention       = 7 * 24 * time.Hour
	maxInteger      = int64(9007199254740991)
	timeLayout      = "2006-01-02T15:04:05.000Z"
	contextFilename = "context.json"
	reportHeader    = "Yousage diagnostics v1\n"
	truncationLine  = "[truncated]\n"
)

var (
	// ErrUnavailable represents a diagnostic filesystem or context failure without paths or raw errors.
	ErrUnavailable = errors.New("diagnostics unavailable")
	// ErrInvalid represents rejected metadata without echoing caller input.
	ErrInvalid = errors.New("invalid diagnostic metadata")
)

// Level is a closed diagnostic severity.
type Level string

const (
	LevelInfo  Level = "info"
	LevelWarn  Level = "warn"
	LevelError Level = "error"
)

// Source identifies a fixed application subsystem.
type Source string

const (
	SourceNative   Source = "native"
	SourceMobile   Source = "mobile"
	SourceFrontend Source = "frontend"
	SourceDesktop  Source = "desktop"
	SourceSync     Source = "sync"
	SourceClaude   Source = "claude"
)

// Event identifies a fixed diagnostic action, never arbitrary content.
type Event string

const (
	EventStartup            Event = "startup"
	EventLifecycle          Event = "lifecycle"
	EventLifecycleCreate    Event = "lifecycle_create"
	EventLifecycleStart     Event = "lifecycle_start"
	EventLifecycleResume    Event = "lifecycle_resume"
	EventLifecyclePause     Event = "lifecycle_pause"
	EventLifecycleStop      Event = "lifecycle_stop"
	EventLifecycleDestroy   Event = "lifecycle_destroy"
	EventLifecycleLowMemory Event = "lifecycle_low_memory"
	EventRenderer           Event = "renderer"
	EventFrontendError      Event = "frontend_error"
	EventRefresh            Event = "refresh"
	EventValidation         Event = "validation"
	EventStage              Event = "stage"
	EventCommit             Event = "commit"
	EventRollback           Event = "rollback"
	EventCache              Event = "cache"
	EventEtag               Event = "etag"
	EventPairing            Event = "pairing"
	EventQuota              Event = "quota"
	EventReport             Event = "report"
)

// Outcome is a closed result category.
type Outcome string

const (
	OutcomeStarted   Outcome = "started"
	OutcomeSucceeded Outcome = "succeeded"
	OutcomeFailed    Outcome = "failed"
	OutcomeRejected  Outcome = "rejected"
	OutcomeSkipped   Outcome = "skipped"
	OutcomeRetained  Outcome = "retained"
	OutcomeCancelled Outcome = "cancelled"
)

// Failure identifies a fixed failure category without error strings.
type Failure string

const (
	FailureNone               Failure = "none"
	FailureUnavailable        Failure = "unavailable"
	FailureIo                 Failure = "io"
	FailureNetwork            Failure = "network"
	FailureTimeout            Failure = "timeout"
	FailureHttp               Failure = "http"
	FailureAuthentication     Failure = "authentication"
	FailureCredentialsMissing Failure = "credentials_missing"
	FailureCredentialsExpired Failure = "credentials_expired"
	FailureUnsupported        Failure = "unsupported"
	FailureProtocol           Failure = "protocol"
	FailureInvalidData        Failure = "invalid_data"
	FailureStale              Failure = "stale"
	FailureRender             Failure = "render"
	FailureInternal           Failure = "internal"
)

// Operation is an opaque Store-generated correlation handle. Its zero value omits correlation.
type Operation struct {
	owner    *Store
	sequence uint64
}

// Entry accepts only bounded metadata and an optional generated operation handle.
// A zero Failure means none. Level, Source, Event and Outcome must be explicit.
type Entry struct {
	Level       Level
	Source      Source
	Event       Event
	Outcome     Outcome
	Failure     Failure
	Status      int
	DurationMs  int64
	Count       int64
	Bytes       int64
	EtagPresent bool
	Conditional bool
	Operation   Operation
}

type processContext struct {
	SchemaVersion int    `json:"schemaVersion"`
	RunId         string `json:"runId"`
}

type record struct {
	SchemaVersion int     `json:"schemaVersion"`
	Time          string  `json:"time"`
	Level         Level   `json:"level"`
	Source        Source  `json:"source"`
	Event         Event   `json:"event"`
	Outcome       Outcome `json:"outcome"`
	Failure       Failure `json:"failure"`
	Status        int     `json:"status"`
	DurationMs    int64   `json:"durationMs"`
	Count         int64   `json:"count"`
	Bytes         int64   `json:"bytes"`
	EtagPresent   bool    `json:"etagPresent"`
	Conditional   bool    `json:"conditional"`
	RunId         string  `json:"runId"`
	OperationId   string  `json:"operationId,omitempty"`
}

// validMetadata checks every string and number before serialization or export.
func validMetadata(value record) bool {
	return oneOf(value.Level, LevelInfo, LevelWarn, LevelError) &&
		oneOf(value.Source, SourceNative, SourceMobile, SourceFrontend, SourceDesktop, SourceSync, SourceClaude) &&
		oneOf(value.Event, EventStartup, EventLifecycle, EventLifecycleCreate, EventLifecycleStart, EventLifecycleResume, EventLifecyclePause, EventLifecycleStop, EventLifecycleDestroy, EventLifecycleLowMemory, EventRenderer, EventFrontendError, EventRefresh, EventValidation, EventStage, EventCommit, EventRollback, EventCache, EventEtag, EventPairing, EventQuota, EventReport) &&
		oneOf(value.Outcome, OutcomeStarted, OutcomeSucceeded, OutcomeFailed, OutcomeRejected, OutcomeSkipped, OutcomeRetained, OutcomeCancelled) &&
		oneOf(value.Failure, FailureNone, FailureUnavailable, FailureIo, FailureNetwork, FailureTimeout, FailureHttp, FailureAuthentication, FailureCredentialsMissing, FailureCredentialsExpired, FailureUnsupported, FailureProtocol, FailureInvalidData, FailureStale, FailureRender, FailureInternal) &&
		value.Status >= 0 && value.Status <= 599 &&
		value.DurationMs >= 0 && value.DurationMs <= maxInteger &&
		value.Count >= 0 && value.Count <= maxInteger && value.Bytes >= 0 && value.Bytes <= maxInteger
}

// oneOf performs closed vocabulary membership without an extensible registry.
func oneOf[valueType ~string](value valueType, allowed ...valueType) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

// strictObject rejects unknown, duplicate, case-aliased, missing and null fields.
func strictObject(data []byte, target any, required []string, optional ...string) bool {
	decoder := json.NewDecoder(bytes.NewReader(data))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return false
	}
	seen := make(map[string]bool, len(required)+len(optional))
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return false
		}
		key, ok := token.(string)
		if !ok || seen[key] || (!oneOf(key, required...) && !oneOf(key, optional...)) {
			return false
		}
		var raw json.RawMessage
		if decoder.Decode(&raw) != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return false
		}
		if oneOf(key, optional...) && bytes.Equal(bytes.TrimSpace(raw), []byte(`""`)) {
			return false
		}
		seen[key] = true
	}
	if end, err := decoder.Token(); err != nil || end != json.Delim('}') {
		return false
	}
	if _, err := decoder.Token(); err != io.EOF {
		return false
	}
	for _, key := range required {
		if !seen[key] {
			return false
		}
	}
	return json.Unmarshal(data, target) == nil
}

// lowerHex decodes a fixed-size canonical lowercase hex value.
func lowerHex(value string, size int) ([]byte, bool) {
	if len(value) != size*2 {
		return nil, false
	}
	decoded, err := hex.DecodeString(value)
	return decoded, err == nil && hex.EncodeToString(decoded) == value
}

// validRunId validates only shape; disk metadata has no authenticity guarantee.
func validRunId(value string) bool {
	_, ok := lowerHex(value, 16)
	return ok
}

// operationId derives a writer-specific ID from a generated sequence.
func operationId(writer string, sequence uint64) string {
	return writer + "-" + strconv.FormatUint(sequence, 10)
}

// validOperationId rejects malformed and cross-writer identifiers.
func validOperationId(writer, value string) bool {
	parts := strings.Split(value, "-")
	if len(parts) != 2 || parts[0] != writer {
		return false
	}
	sequence, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil || sequence == 0 || sequence > uint64(maxInteger) || strconv.FormatUint(sequence, 10) != parts[1] {
		return false
	}
	return true
}
