package sync

import (
	"context"
	"strconv"
	"time"
	"yousage/internal/preferences"
	"yousage/internal/service"
	"yousage/internal/theme"
)

// AppVersion matches the desktop provider version when no application build version is available.
const AppVersion = "0.1.7"

// SnapshotSource supplies the same report reads exposed by the desktop.
type SnapshotSource interface {
	Usage(context.Context, int, string) (service.Snapshot, error)
	LossReport(context.Context, int, string) (service.LossReport, error)
	SpeedReport(context.Context, int, string) (service.SpeedReport, error)
	Receipt(context.Context, int, string) (service.Receipt, error)
}

// Period contains all four reports for one supported dashboard window.
type Period struct {
	Usage   service.Snapshot    `json:"usage"`
	Loss    service.LossReport  `json:"loss"`
	Speed   service.SpeedReport `json:"speed"`
	Receipt service.Receipt     `json:"receipt"`
}

// Snapshot contains read-only dashboard data without connection configuration.
type Snapshot struct {
	FormatVersion int                      `json:"formatVersion"`
	GeneratedAt   string                   `json:"generatedAt"`
	AppVersion    string                   `json:"appVersion"`
	TimeZone      string                   `json:"timeZone"`
	Periods       map[string]Period        `json:"periods"`
	Display       preferences.SavedDisplay `json:"display"`
	Appearance    theme.LibrarySnapshot    `json:"appearance"`
	Bootstrap     any                      `json:"bootstrap"`
}

// SnapshotBuilder collects the desktop responses as one all-or-nothing snapshot.
type SnapshotBuilder struct {
	Source     SnapshotSource
	Display    func(context.Context) (preferences.SavedDisplay, error)
	Appearance func(context.Context) (theme.LibrarySnapshot, error)
	Bootstrap  func() any
	Now        func() time.Time
}

// Build aborts on any report failure, preserving the relay's last complete snapshot.
func (builder SnapshotBuilder) Build(ctx context.Context, timeZone string) (Snapshot, error) {
	now := time.Now
	if builder.Now != nil {
		now = builder.Now
	}
	if timeZone == "" {
		timeZone = "UTC"
	}
	snapshot := Snapshot{FormatVersion: 1, GeneratedAt: now().UTC().Format(time.RFC3339), AppVersion: AppVersion, TimeZone: timeZone, Periods: map[string]Period{}}
	for _, days := range []int{1, 7, 30, 90} {
		var period Period
		var err error
		if period.Usage, err = builder.Source.Usage(ctx, days, timeZone); err != nil {
			return Snapshot{}, err
		}
		if period.Loss, err = builder.Source.LossReport(ctx, days, timeZone); err != nil {
			return Snapshot{}, err
		}
		if period.Speed, err = builder.Source.SpeedReport(ctx, days, timeZone); err != nil {
			return Snapshot{}, err
		}
		if period.Receipt, err = builder.Source.Receipt(ctx, days, timeZone); err != nil {
			return Snapshot{}, err
		}
		snapshot.Periods[strconv.Itoa(days)] = period
	}
	var err error
	if snapshot.Display, err = builder.Display(ctx); err != nil {
		return Snapshot{}, err
	}
	if snapshot.Appearance, err = builder.Appearance(ctx); err != nil {
		return Snapshot{}, err
	}
	snapshot.Bootstrap = builder.Bootstrap()
	return snapshot, nil
}

// reuseSnapshotMetadata compares report content using the last upload's observation
// timestamps. Window bounds remain significant because they define displayed coverage.
func reuseSnapshotMetadata(snapshot, previous Snapshot) Snapshot {
	snapshot.GeneratedAt = previous.GeneratedAt
	periods := make(map[string]Period, len(snapshot.Periods))
	for name, period := range snapshot.Periods {
		period.Usage.Environments = append([]service.EnvironmentState{}, period.Usage.Environments...)
		for index, environment := range period.Usage.Environments {
			if environment.Summary == nil {
				continue
			}
			for _, oldEnvironment := range previous.Periods[name].Usage.Environments {
				if oldEnvironment.Id != environment.Id || oldEnvironment.Summary == nil {
					continue
				}
				summary := *environment.Summary
				summary.ReadAt, summary.ScanDurationMs = oldEnvironment.Summary.ReadAt, oldEnvironment.Summary.ScanDurationMs
				period.Usage.Environments[index].Summary = &summary
				break
			}
		}
		periods[name] = period
	}
	snapshot.Periods = periods
	return snapshot
}
