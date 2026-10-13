// Package domain defines provider-neutral usage records and physical source identities.
package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strings"
)

type Provider string

const (
	Codex  Provider = "codex"
	Claude Provider = "claude"
)

// SourceFingerprint identifies physical history independently of its environment connection.
type SourceFingerprint struct {
	HostId           string   `json:"hostId"`
	Provider         Provider `json:"provider"`
	ResolvedHomePath string   `json:"resolvedHomePath"`
	VolumeId         string   `json:"volumeId"`
}

func (source SourceFingerprint) Id() string {
	encoded, _ := json.Marshal(source)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func (provider Provider) Valid() bool {
	switch provider {
	case Codex, Claude:
		return true
	}
	return false
}

// Tokens separates cache categories; reasoning is already included in output.
type Tokens struct {
	UncachedInputTokens int64 `json:"uncachedInputTokens"`
	CachedInputTokens   int64 `json:"cachedInputTokens"`
	CacheCreationTokens int64 `json:"cacheCreationTokens"`
	OutputTokens        int64 `json:"outputTokens"`
	ReasoningTokens     int64 `json:"reasoningTokens"`
}

func (tokens Tokens) Total() int64 {
	return tokens.UncachedInputTokens + tokens.CachedInputTokens + tokens.CacheCreationTokens + tokens.OutputTokens
}
func (tokens *Tokens) Add(other Tokens) {
	tokens.UncachedInputTokens += other.UncachedInputTokens
	tokens.CachedInputTokens += other.CachedInputTokens
	tokens.CacheCreationTokens += other.CacheCreationTokens
	tokens.OutputTokens += other.OutputTokens
	tokens.ReasoningTokens += other.ReasoningTokens
}

// Record contains usage facts or a redacted error variant, never transcript text or credentials.
// ContextId separates native transcript incarnations and explicit context resets within a session.
// Error facts retain the same context identity and a closed error kind without usage.
type Record struct {
	Provider        Provider          `json:"provider"`
	TimestampMs     int64             `json:"timestampMs"`
	Model           string            `json:"model"`
	RateModel       string            `json:"rateModel,omitempty"`
	SessionId       string            `json:"sessionId"`
	ContextId       string            `json:"contextId,omitempty"`
	Totals          Tokens            `json:"totals"`
	ReportedCostUsd *float64          `json:"reportedCostUsd"`
	Speed           string            `json:"speed"`
	DedupeKey       string            `json:"dedupeKey,omitempty"`
	SourcePath      string            `json:"sourcePath,omitempty"`
	QuotaBucket     string            `json:"quotaBucket,omitempty"`
	Quota           *QuotaObservation `json:"quota,omitempty"`
	ErrorKind       string            `json:"errorKind,omitempty"`
	CacheTtlMs      int64             `json:"cacheTtlMs,omitempty"`
	GenerationMs    int64             `json:"generationMs,omitempty"`
	TtftMs          int64             `json:"ttftMs,omitempty"`
	IsQuotaOnly     bool              `json:"quotaOnly,omitempty"`
}

// QuotaWindow retains the native identity and reset of a logged window.
type QuotaWindow struct {
	Id            string  `json:"id"`
	UsedPercent   float64 `json:"usedPercent"`
	WindowMinutes int     `json:"windowMinutes"`
	ResetsAt      int64   `json:"resetsAt"`
}

// QuotaObservation contains quota facts without transcript content.
type QuotaObservation struct {
	PlanType string        `json:"planType,omitempty"`
	Windows  []QuotaWindow `json:"windows"`
}

// Validate checks usage facts and the separate quota-only record variant.
func (record Record) Validate() error {
	if record.GenerationMs < 0 {
		return errors.New("negative generation duration")
	}
	if record.TtftMs < 0 || (record.TtftMs > 0 && (record.ErrorKind != "" || record.IsQuotaOnly)) {
		return errors.New("invalid time to first token")
	}
	if record.ErrorKind != "" {
		if (record.Provider != Codex && record.Provider != Claude) || !ValidErrorKind(record.ErrorKind) || record.Totals != (Tokens{}) || record.ReportedCostUsd != nil || record.Quota != nil || record.IsQuotaOnly || record.RateModel != "" || record.Speed != "" || record.CacheTtlMs != 0 || record.GenerationMs != 0 {
			return errors.New("invalid error fact")
		}
		return nil
	}
	if record.CacheTtlMs != 0 && record.CacheTtlMs != 300000 && record.CacheTtlMs != 3600000 {
		return errors.New("invalid prompt cache TTL")
	}
	if record.IsQuotaOnly {
		if record.Provider != Codex || record.Quota == nil || record.Totals != (Tokens{}) || record.ReportedCostUsd != nil || record.GenerationMs != 0 {
			return errors.New("invalid quota-only record")
		}
		return nil
	}
	if !record.Provider.Valid() || strings.TrimSpace(record.Model) == "" {
		return errors.New("invalid usage provider or model")
	}
	if record.Speed != "standard" && record.Speed != "fast" && record.Speed != "ultrafast" {
		return errors.New("invalid billing speed")
	}
	tokens := record.Totals
	if tokens.UncachedInputTokens < 0 || tokens.CachedInputTokens < 0 || tokens.CacheCreationTokens < 0 || tokens.OutputTokens < 0 || tokens.ReasoningTokens < 0 {
		return errors.New("negative token count")
	}
	if record.ReportedCostUsd != nil && (!Finite(*record.ReportedCostUsd) || *record.ReportedCostUsd < 0) {
		return errors.New("invalid reported cost")
	}
	return nil
}

func Finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

type PriceOverride struct {
	InputCostPerMillionTokens      float64  `json:"inputCostPerMillionTokens"`
	OutputCostPerMillionTokens     float64  `json:"outputCostPerMillionTokens"`
	CacheReadCostPerMillionTokens  *float64 `json:"cacheReadCostPerMillionTokens,omitempty"`
	CacheWriteCostPerMillionTokens *float64 `json:"cacheWriteCostPerMillionTokens,omitempty"`
}

func (price PriceOverride) Validate() error {
	values := []float64{price.InputCostPerMillionTokens, price.OutputCostPerMillionTokens}
	if price.CacheReadCostPerMillionTokens != nil {
		values = append(values, *price.CacheReadCostPerMillionTokens)
	}
	if price.CacheWriteCostPerMillionTokens != nil {
		values = append(values, *price.CacheWriteCostPerMillionTokens)
	}
	for _, value := range values {
		if !Finite(value) || value < 0 {
			return errors.New("prices must be finite nonnegative numbers")
		}
	}
	return nil
}

// Checkpoint contains only completed-line progress and a provider reducer snapshot.
type Checkpoint struct {
	SourceId      string          `json:"sourceId"`
	Path          string          `json:"path"`
	Revision      int64           `json:"revision"`
	Offset        int64           `json:"offset"`
	FileSize      int64           `json:"fileSize"`
	ModifiedNs    int64           `json:"modifiedNs"`
	Guard         string          `json:"guard"`
	GuardLength   int             `json:"guardLength"`
	ParserVersion int             `json:"parserVersion"`
	Reducer       json.RawMessage `json:"reducer"`
	RecordCount   int64           `json:"recordCount"`
}

// Observation distinguishes cached success, partial results and read failures.
type Observation struct {
	SourceId    string `json:"sourceId"`
	Status      string `json:"status"`
	AttemptedAt int64  `json:"attemptedAt"`
	SucceededAt *int64 `json:"succeededAt"`
	Notice      string `json:"notice,omitempty"`
}

func AccountId(provider Provider, email string) string {
	return string(provider) + ":" + strings.ToLower(strings.TrimSpace(email))
}

// ErrorKinds is the closed set of redacted history error classifications.
var ErrorKinds = [...]string{"rateLimit", "overloaded", "server", "streamDisconnected", "interrupted", "aborted", "auth", "other"}

// ValidErrorKind rejects arbitrary provider error text.
func ValidErrorKind(kind string) bool {
	for _, candidate := range ErrorKinds {
		if kind == candidate {
			return true
		}
	}
	return false
}
