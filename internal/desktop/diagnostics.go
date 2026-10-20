package desktop

import (
	"encoding/json"
	"runtime"
	"strings"
	"time"
	"yousage/internal/diagnostics"
)

// RecordFrontendDiagnostic accepts two fixed categories, with at most ten events
// in any rolling minute. It never records arbitrary frontend text or errors.
func (service *App) RecordFrontendDiagnostic(category string) {
	if len(category) > 16 || (category != "render" && category != "internal") {
		return
	}
	service.frontendDiagnosticMutex.Lock()
	defer service.frontendDiagnosticMutex.Unlock()
	now := time.Now()
	retained := service.frontendDiagnosticTimes[:0]
	for _, observed := range service.frontendDiagnosticTimes {
		if now.Sub(observed) < time.Minute {
			retained = append(retained, observed)
		}
	}
	service.frontendDiagnosticTimes = retained
	if len(retained) >= 10 {
		return
	}
	service.frontendDiagnosticTimes = append(retained, now)
	failure := diagnostics.FailureInternal
	if category == "render" {
		failure = diagnostics.FailureRender
	}
	service.recordDiagnostic(diagnostics.Entry{Level: diagnostics.LevelError, Source: diagnostics.SourceFrontend, Event: diagnostics.EventFrontendError, Outcome: diagnostics.OutcomeFailed, Failure: failure})
}

// recordDiagnostic keeps optional local write failures separate from app results.
func (service *App) recordDiagnostic(entry diagnostics.Entry) {
	if service.diagnostics.Record(entry) != nil {
		service.diagnosticUnavailable.Store(true)
	}
}

// recordRefreshResult records only completion and duration for a desktop action.
func (service *App) recordRefreshResult(event diagnostics.Event, operation diagnostics.Operation, started time.Time, actionError error) {
	entry := diagnostics.Entry{Level: diagnostics.LevelInfo, Source: diagnostics.SourceDesktop, Event: event, Outcome: diagnostics.OutcomeSucceeded, Operation: operation, DurationMs: max(0, time.Since(started).Milliseconds())}
	if actionError != nil {
		entry.Level, entry.Outcome, entry.Failure = diagnostics.LevelWarn, diagnostics.OutcomeFailed, diagnostics.FailureUnavailable
	}
	service.recordDiagnostic(entry)
}

// diagnosticReport contains only fixed build metadata, counts, availability and
// revalidated local records. Paths, labels and service errors never enter it.
type diagnosticReport struct {
	Application          string `json:"application"`
	Wails                string `json:"wails"`
	Go                   string `json:"go"`
	Platform             string `json:"platform"`
	SourceCommit         string `json:"sourceCommit"`
	HistorySources       int    `json:"historySources"`
	RemoteEnvironments   int    `json:"remoteEnvironments"`
	QuotaTargets         int    `json:"quotaTargets"`
	QuotaHubs            int    `json:"quotaHubs"`
	MetadataAvailable    bool   `json:"metadataAvailable"`
	DiagnosticsAvailable bool   `json:"diagnosticsAvailable"`
	Logs                 string `json:"logs"`
}

// GetDiagnostics returns a bounded safe report even before service startup or
// after a diagnostic failure. Availability is fixed metadata, never a raw error.
func (service *App) GetDiagnostics() (string, error) {
	service.recordDiagnostic(diagnostics.Entry{Level: diagnostics.LevelInfo, Source: diagnostics.SourceDesktop, Event: diagnostics.EventReport, Outcome: diagnostics.OutcomeStarted})
	value := diagnosticReport{Application: "Yousage", Wails: "3.0.0-beta.30", Go: runtime.Version(), Platform: runtime.GOOS + "/" + runtime.GOARCH, SourceCommit: "51f7f77f87318a5329d3bab15d513fda5654200b"}
	if service.usage != nil && service.context != nil {
		config, configError := service.usage.Configuration(service.context)
		sources, _, sourcesError := service.usage.Preferences.Sources(service.context)
		value.MetadataAvailable = configError == nil && sourcesError == nil
		if value.MetadataAvailable {
			value.HistorySources, value.RemoteEnvironments, value.QuotaTargets, value.QuotaHubs = len(sources), len(config.Value.Connections), len(config.Value.Providers), len(config.Value.Hubs)
		}
	}
	logs, reportError := service.diagnostics.Report()
	value.DiagnosticsAvailable = service.diagnostics != nil && reportError == nil && !service.diagnosticUnavailable.Load()
	value.Logs = logs
	return boundedDiagnosticReport(value), nil
}

// boundedDiagnosticReport drops oldest complete records until JSON escaping and
// metadata fit the same 128 KiB report budget, reserving the export newline.
func boundedDiagnosticReport(value diagnosticReport) string {
	const marker = "[truncated]\n"
	for {
		data, _ := json.MarshalIndent(value, "", "  ")
		if len(data)+1 <= diagnostics.MaxReportBytes {
			return string(data)
		}
		logs := strings.TrimSuffix(strings.TrimSuffix(value.Logs, marker), "\n")
		last := strings.LastIndexByte(logs, '\n')
		if last < 0 {
			value.Logs = marker
		} else {
			value.Logs = logs[:last+1] + marker
		}
	}
}
