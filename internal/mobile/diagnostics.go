package mobile

import (
	"time"
	"yousage/internal/diagnostics"
)

// recordDiagnostic writes only fixed metadata and never changes a viewer result.
func (service *Service) recordDiagnostic(entry diagnostics.Entry) {
	entry.Source = diagnostics.SourceMobile
	entry.Level = diagnostics.LevelInfo
	if entry.Failure != "" && entry.Failure != diagnostics.FailureNone {
		entry.Level = diagnostics.LevelWarn
	}
	_ = service.diagnostics.Record(entry)
}

// recordResult records completion without including a returned error or user input.
func (service *Service) recordResult(event diagnostics.Event, operation diagnostics.Operation, started time.Time, actionError error, failure diagnostics.Failure) {
	entry := diagnostics.Entry{Event: event, Outcome: diagnostics.OutcomeSucceeded, Operation: operation, DurationMs: max(0, time.Since(started).Milliseconds())}
	if actionError != nil {
		entry.Outcome, entry.Failure = diagnostics.OutcomeFailed, failure
	}
	service.recordDiagnostic(entry)
}
