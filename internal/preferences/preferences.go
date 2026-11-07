// Package preferences validates authoritative saved choices independently of UI drafts.
package preferences

import (
	"context"
	"errors"
	"math"
	"strings"
	"yousage/internal/domain"
)

type Repository interface {
	ReadSetting(context.Context, string, any) (int64, error)
	WriteSetting(context.Context, string, int64, any) (int64, error)
}
type WindowBounds struct {
	X         int  `json:"x"`
	Y         int  `json:"y"`
	Width     int  `json:"width"`
	Height    int  `json:"height"`
	Maximized bool `json:"maximized"`
}
type Display struct {
	Zoom                 float64 `json:"zoom"`
	Metric               string  `json:"metric"`
	WindowDays           int     `json:"windowDays"`
	ShouldAutoRefresh    bool    `json:"shouldAutoRefresh"`
	ShouldCloseToTray    bool    `json:"shouldCloseToTray"`
	ShouldStartMinimized bool    `json:"shouldStartMinimized"`
	TimestampFormat      string  `json:"timestampFormat"`
	Breakdown            string  `json:"breakdown"`
	Language             string  `json:"language"`
}

type Source struct {
	Id          string          `json:"id"`
	Provider    domain.Provider `json:"provider"`
	Root        string          `json:"root"`
	IsEnabled   bool            `json:"isEnabled"`
	IsAutomatic bool            `json:"isAutomatic"`
}
type SavedDisplay struct {
	Revision int64   `json:"revision"`
	Value    Display `json:"value"`
}
type Service struct{ repository Repository }

func New(repository Repository) *Service { return &Service{repository: repository} }
func Defaults() Display {
	return Display{Zoom: 1, Metric: "limits", WindowDays: 30, ShouldAutoRefresh: true, ShouldCloseToTray: true, TimestampFormat: "locale", Breakdown: "model", Language: "system"}
}

func (display Display) Validate() error {
	if !domain.Finite(display.Zoom) || display.Zoom < 1 || display.Zoom > 2 {
		return errors.New("zoom must be between 100% and 200%")
	}
	if display.TimestampFormat != "locale" && display.TimestampFormat != "24h" && display.TimestampFormat != "12h" {
		return errors.New("invalid timestamp format")
	}
	if display.Language != "system" && display.Language != "zh-CN" && display.Language != "en" {
		return errors.New("invalid language")
	}
	if display.Breakdown != "model" && display.Breakdown != "time" {
		return errors.New("invalid breakdown")
	}
	if display.Metric != "cost" && display.Metric != "tokens" && display.Metric != "limits" {
		return errors.New("invalid usage metric")
	}
	if display.WindowDays != 1 && display.WindowDays != 7 && display.WindowDays != 30 && display.WindowDays != 90 {
		return errors.New("invalid usage period")
	}
	return nil
}
func (service *Service) Display(ctx context.Context) (SavedDisplay, error) {
	saved := SavedDisplay{Value: Defaults()}
	revision, err := service.repository.ReadSetting(ctx, "display", &saved.Value)
	saved.Revision = revision
	if err != nil {
		return saved, err
	}
	if saved.Value.Zoom > 0 && saved.Value.Zoom < 1 {
		saved.Value.Zoom = 1
	}
	return saved, saved.Value.Validate()
}
func (service *Service) SaveDisplay(ctx context.Context, saved SavedDisplay) (SavedDisplay, error) {
	if err := saved.Value.Validate(); err != nil {
		return saved, err
	}
	revision, err := service.repository.WriteSetting(ctx, "display", saved.Revision, saved.Value)
	if err == nil {
		saved.Revision = revision
	}
	return saved, err
}
func (service *Service) Sources(ctx context.Context) ([]Source, int64, error) {
	sources := []Source{}
	revision, err := service.repository.ReadSetting(ctx, "sources", &sources)
	return sources, revision, err
}
func (service *Service) SaveSources(ctx context.Context, sources []Source, revision int64) (int64, error) {
	if len(sources) > 65 {
		return revision, errors.New("too many history sources")
	}
	seen := map[string]bool{}
	for _, source := range sources {
		if source.Id == "" || len(source.Id) > 96 || len(source.Root) > 4096 || seen[source.Id] || !source.Provider.Valid() || (!source.IsAutomatic && strings.TrimSpace(source.Root) == "") {
			return revision, errors.New("invalid or duplicate source")
		}
		seen[source.Id] = true
	}
	return service.repository.WriteSetting(ctx, "sources", revision, sources)
}

// RestoreBounds chooses the best overlapping work area or recenters after a monitor disappears.
func RestoreBounds(saved WindowBounds, workAreas []WindowBounds) WindowBounds {
	if len(workAreas) == 0 {
		return saved
	}
	best, overlap := workAreas[0], 0
	for _, area := range workAreas {
		width := max(0, min(saved.X+saved.Width, area.X+area.Width)-max(saved.X, area.X))
		height := max(0, min(saved.Y+saved.Height, area.Y+area.Height)-max(saved.Y, area.Y))
		if width*height > overlap {
			best, overlap = area, width*height
		}
	}
	saved.Width = min(best.Width, max(480, saved.Width))
	saved.Height = min(best.Height, max(600, saved.Height))
	if overlap == 0 {
		saved.X = best.X + (best.Width-saved.Width)/2
		saved.Y = best.Y + (best.Height-saved.Height)/2
	} else {
		saved.X = int(math.Max(float64(best.X), math.Min(float64(saved.X), float64(best.X+best.Width-saved.Width))))
		saved.Y = max(best.Y, min(saved.Y, best.Y+best.Height-saved.Height))
	}
	return saved
}
