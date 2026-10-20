package service

import (
	"context"
	"errors"
	"time"
	"yousage/internal/diagnostics"
	"yousage/internal/quota"
)

// SetDiagnostics shares the application-owned writer before refresh work starts.
// A nil store disables logging without affecting service operations.
func (service *Service) SetDiagnostics(store *diagnostics.Store) {
	service.diagnostics = store
}

// recordRefreshResult records completion without source identities or errors.
func (service *Service) recordRefreshResult(event diagnostics.Event, operation diagnostics.Operation, started time.Time, resultError error) {
	entry := diagnostics.Entry{Level: diagnostics.LevelInfo, Source: diagnostics.SourceDesktop, Event: event, Outcome: diagnostics.OutcomeSucceeded, Operation: operation, DurationMs: max(0, time.Since(started).Milliseconds())}
	if resultError != nil {
		entry.Level, entry.Outcome, entry.Failure = diagnostics.LevelWarn, diagnostics.OutcomeFailed, diagnostics.FailureIo
		if errors.Is(resultError, context.Canceled) {
			entry.Outcome, entry.Failure = diagnostics.OutcomeCancelled, diagnostics.FailureNone
		}
		if errors.Is(resultError, context.DeadlineExceeded) {
			entry.Failure = diagnostics.FailureTimeout
		}
	}
	_ = service.diagnostics.Record(entry)
}

// claudeDiagnostic classifies only exact known safe messages and reason codes.
// Unknown text is represented as unavailable and is never copied into a record.
func claudeDiagnostic(probed quota.Limits, previous *quota.Limits) diagnostics.Entry {
	entry := diagnostics.Entry{Level: diagnostics.LevelInfo, Source: diagnostics.SourceClaude, Event: diagnostics.EventQuota, Outcome: diagnostics.OutcomeSucceeded, Failure: diagnostics.FailureNone}
	if probed.Unavailable == nil {
		entry.Count = int64(len(probed.Windows))
		return entry
	}
	entry.Level, entry.Outcome, entry.Failure = diagnostics.LevelWarn, diagnostics.OutcomeFailed, diagnostics.FailureUnavailable
	switch probed.Unavailable.Reason {
	case "unsupported":
		entry.Outcome, entry.Failure = diagnostics.OutcomeSkipped, diagnostics.FailureUnsupported
	case "missing", "credentialsMissing":
		entry.Failure = diagnostics.FailureCredentialsMissing
	case "probeFailed":
		if previous != nil && previous.Unavailable == nil {
			entry.Outcome = diagnostics.OutcomeRetained
		}
	}
	switch probed.Unavailable.Message {
	case "Claude usage API rejected the stored credential (HTTP 401).":
		entry.Failure, entry.Status = diagnostics.FailureAuthentication, 401
	case "Claude usage API denied access to usage limits (HTTP 403).":
		entry.Failure, entry.Status = diagnostics.FailureHttp, 403
	case "Claude usage API rate limit reached (HTTP 429). Try again later.":
		entry.Failure, entry.Status = diagnostics.FailureHttp, 429
	case "Claude usage API is temporarily unavailable (HTTP 5xx). Try again later.":
		entry.Failure, entry.Status = diagnostics.FailureHttp, 500
	case "Claude usage API returned an unexpected HTTP status.":
		entry.Failure = diagnostics.FailureHttp
	case "Claude usage API request timed out.", "Claude Code usage probe timed out.":
		entry.Failure = diagnostics.FailureTimeout
	case "Claude usage API request was cancelled.", "Claude Code usage probe was cancelled.":
		if entry.Outcome != diagnostics.OutcomeRetained {
			entry.Outcome = diagnostics.OutcomeCancelled
		}
		entry.Failure = diagnostics.FailureNone
	case "Claude usage API could not be reached or read because of a network error.", "Claude Code usage probe could not communicate with the CLI.":
		entry.Failure = diagnostics.FailureNetwork
	case "Claude Code stored access token has expired; usage limits could not be read.":
		entry.Failure = diagnostics.FailureCredentialsExpired
	case "Claude Code stored credentials are malformed.", "Claude Code returned an invalid usage probe response.", "Claude returned invalid usage limits.":
		entry.Failure = diagnostics.FailureProtocol
	case "Claude usage API returned invalid JSON.":
		entry.Failure, entry.Status = diagnostics.FailureProtocol, 200
	case "Claude Code rejected authentication for the usage probe.":
		entry.Failure = diagnostics.FailureAuthentication
	case "Claude Code login could not be read.":
		entry.Failure = diagnostics.FailureIo
	}
	return entry
}

// recordClaudeProbe observes the pre-retention result without altering limits or samples.
func (service *Service) recordClaudeProbe(probed quota.Limits, previous *quota.Limits, operation diagnostics.Operation, started time.Time) {
	entry := claudeDiagnostic(probed, previous)
	entry.Operation = operation
	entry.DurationMs = max(0, time.Since(started).Milliseconds())
	if entry.Outcome == diagnostics.OutcomeRetained && entry.Failure == diagnostics.FailureNone {
		cancelled := entry
		cancelled.Outcome = diagnostics.OutcomeCancelled
		_ = service.diagnostics.Record(cancelled)
	}
	_ = service.diagnostics.Record(entry)
}
