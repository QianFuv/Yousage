package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/wailsapp/wails/v3/pkg/application"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"
	"yousage/internal/appstate"
	"yousage/internal/domain"
	"yousage/internal/preferences"
	"yousage/internal/quota"
	"yousage/internal/refresh"
	"yousage/internal/remote"
	dataservice "yousage/internal/service"
	"yousage/internal/storage"
	datasync "yousage/internal/sync"
	"yousage/internal/theme"
)

// Bootstrap supplies native appearance and regional preferences before rendering.
type Bootstrap struct {
	IsSystemDark bool   `json:"isSystemDark"`
	SystemLocale string `json:"systemLocale"`
}

// App bridges authoritative local services to the desktop interface.
type App struct {
	context             context.Context
	nativeApp           *application.App
	window              *application.WebviewWindow
	tray                *application.SystemTray
	logFile             *os.File
	logMutex            sync.Mutex
	logPath             string
	stateDirectory      string
	store               *storage.Store
	themes              *theme.Library
	themeCancel         context.CancelFunc
	themeDone           chan struct{}
	usage               *dataservice.Service
	syncEngine          *datasync.Engine
	backgroundRefresh   *backgroundRefreshController
	backgroundOptions   backgroundRefreshOptions
	windowMutex         sync.Mutex
	normalBounds        preferences.WindowBounds
	hasNormalBounds     bool
	isShuttingDown      bool
	isCloseBridgeActive bool
	shouldAllowClose    bool
	shouldCloseToTray   bool
	isWaitingInTray     bool
	zoom                float64
}

// newApp opens the shared desktop state directory without falling back to another location.
func newApp() (*App, error) {
	stateDirectory, err := appstate.Directory()
	if err != nil {
		return nil, err
	}
	if !hasNativeSmokeArgument() {
		if err := appstate.Prepare(stateDirectory, true); err != nil {
			return nil, err
		}
	}
	return newAppInDirectory(stateDirectory, hasNativeSmokeArgument())
}

// newAppInDirectory keeps logs and smoke-test state inside the selected application directory.
func newAppInDirectory(stateDirectory string, isSmoke bool) (*App, error) {
	logsDirectory := filepath.Join(stateDirectory, "logs")
	if isSmoke {
		logsDirectory = filepath.Join(stateDirectory, "smoke", "logs")
	}
	if err := os.MkdirAll(logsDirectory, 0700); err != nil {
		return nil, err
	}
	directory, err := os.MkdirTemp(logsDirectory, "yousage-native-lifecycle-")
	if err != nil {
		return nil, err
	}
	logPath := filepath.Join(directory, "lifecycle.jsonl")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, err
	}
	if isSmoke {
		stateDirectory = filepath.Join(directory, "isolated-state")
	}
	database, err := storage.Open(appstate.DatabasePath(stateDirectory))
	if err != nil {
		logFile.Close()
		return nil, err
	}
	if isSmoke {
		preferenceService := preferences.New(database)
		if _, err = preferenceService.SaveSources(context.Background(), []preferences.Source{}, 0); err == nil {
			display := preferences.Defaults()
			display.ShouldAutoRefresh = false
			_, err = preferenceService.SaveDisplay(context.Background(), preferences.SavedDisplay{Value: display})
		}
		if err != nil {
			database.Close()
			logFile.Close()
			return nil, err
		}
	}
	return &App{logFile: logFile, logPath: logPath, stateDirectory: stateDirectory, store: database, themes: &theme.Library{Repository: database, PublishedStore: theme.Store{Directory: stateDirectory}}}, nil
}

// ServiceStartup initializes lifecycle evidence before native windows are created.
func (service *App) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	service.context = ctx
	service.record("startup")
	usage, err := dataservice.New(ctx, service.store, service.themes)
	if err != nil {
		return err
	}
	usage.Scope = []domain.Provider{domain.Codex, domain.Claude, domain.Pi}
	service.usage = usage
	if err = service.initializeSync(ctx); err != nil {
		usage.Close()
		return err
	}
	service.backgroundRefresh = newBackgroundRefreshController(ctx, service.backgroundOptions, func() bool {
		display, err := usage.Preferences.Display(ctx)
		return err == nil && display.Value.ShouldAutoRefresh
	}, usage.RefreshUsage, usage.RefreshLimits)
	if service.window != nil {
		service.backgroundRefresh.setVisibility(service.window.IsVisible() && !service.window.IsMinimised())
	}
	usage.Emit = func(name string, value any) { service.nativeApp.Event.Emit(name, value) }
	usage.ReconcileConnections()
	if display, err := usage.Preferences.Display(ctx); err == nil {
		service.setCloseToTray(display.Value.ShouldCloseToTray)
		service.windowMutex.Lock()
		service.isWaitingInTray = display.Value.ShouldStartMinimized && !hasNativeSmokeArgument()
		service.windowMutex.Unlock()
	}
	watcher, cancel := context.WithCancel(ctx)
	service.themeCancel = cancel
	service.themeDone = make(chan struct{})
	go func() {
		defer close(service.themeDone)
		if err := service.themes.Watch(watcher, func(snapshot theme.LibrarySnapshot) {
			service.nativeApp.Event.Emit("appearance-snapshot", snapshot)
			service.triggerSync()
		}); err != nil && watcher.Err() == nil {
			if snapshot, readError := service.themes.Read(watcher); readError == nil {
				snapshot.Error = err.Error()
				service.nativeApp.Event.Emit("appearance-snapshot", snapshot)
			}
		}
	}()
	return nil
}

// ServiceShutdown flushes evidence after Wails cancels the application context.
func (service *App) ServiceShutdown() error {
	service.windowMutex.Lock()
	service.isShuttingDown = true
	service.windowMutex.Unlock()
	if service.backgroundRefresh != nil {
		service.backgroundRefresh.close()
	}
	if service.syncEngine != nil {
		service.syncEngine.Close()
	}
	service.persistBounds()
	if service.usage != nil {
		service.usage.Close()
	}
	if service.themeCancel != nil {
		service.themeCancel()
		<-service.themeDone
	}
	if service.store != nil {
		defer service.store.Close()
	}
	service.record("shutdown")
	service.logMutex.Lock()
	defer service.logMutex.Unlock()
	return service.logFile.Close()
}

// GetUsage returns the requested live/cached environment aggregates.
func (service *App) GetUsage(days int, timeZone string) (dataservice.Snapshot, error) {
	if service.syncEngine != nil {
		if timeZone == "" {
			timeZone = "UTC"
		}
		if _, err := time.LoadLocation(timeZone); err != nil {
			timeZone = "UTC"
		}
		_ = service.syncEngine.RecordTimeZone(service.context, timeZone)
	}
	return service.usage.Usage(service.context, days, timeZone)
}

// GetReceipt returns a local trailing-window receipt using the Usage pricing pipeline.
func (service *App) GetReceipt(days int, timeZone string) (dataservice.Receipt, error) {
	return service.usage.Receipt(service.context, days, timeZone)
}

// GetSpeedReport returns local generation throughput separated by billing speed tier.
func (service *App) GetSpeedReport(days int, timeZone string) (dataservice.SpeedReport, error) {
	return service.usage.SpeedReport(service.context, days, timeZone)
}

// GetLossReport returns local cache-expiry observations.
func (service *App) GetLossReport(days int, timeZone string) (dataservice.LossReport, error) {
	return service.usage.LossReport(service.context, days, timeZone)
}

// RefreshUsage coalesces selected scans and queues sync after successful completion.
func (service *App) RefreshUsage(days int, timeZone string, selected []string, request refresh.Request) error {
	err := service.usage.RefreshUsage(service.context, days, timeZone, selected, request)
	if err == nil {
		service.triggerSync()
	}
	return err
}

// RefreshLimits refreshes quota observations and queues sync after successful completion.
func (service *App) RefreshLimits(selected []string, request refresh.Request) error {
	err := service.usage.RefreshLimits(service.context, selected, request)
	if err == nil {
		service.triggerSync()
	}
	return err
}

// ApplyPricing saves each destination independently and reports ambiguous remote writes.
func (service *App) ApplyPricing(identities []string, patch preferences.PricingPatch) []remote.SaveOutcome {
	return service.usage.ApplyPricing(service.context, identities, patch)
}

// ConsumeResetCredit uses an explicit caller operation identity and durable recovery before sending.
func (service *App) ConsumeResetCredit(route quota.RedeemRoute, operationId string, isExplicitRetry bool) (quota.ResetResult, error) {
	return service.usage.ConsumeReset(service.context, route, operationId, isExplicitRetry)
}

// GetDisplay returns ordinary saved display preferences independently of theme drafts.
func (service *App) GetDisplay() (preferences.SavedDisplay, error) {
	saved, err := service.usage.Preferences.Display(service.context)
	if err == nil && service.window != nil {
		service.applyZoom(saved.Value.Zoom)
	}
	return saved, err
}

// SaveDisplay atomically persists a validated ordinary preference update.
func (service *App) SaveDisplay(saved preferences.SavedDisplay) (preferences.SavedDisplay, error) {
	ctx, release, err := service.usage.BeginWrite(service.context)
	if err != nil {
		return saved, err
	}
	defer release()
	value, err := service.usage.Preferences.SaveDisplay(ctx, saved)
	if err == nil {
		service.applyZoom(value.Value.Zoom)
		service.setCloseToTray(value.Value.ShouldCloseToTray)
		if service.backgroundRefresh != nil {
			service.backgroundRefresh.reconcile()
		}
		service.triggerSync()
	}
	return value, err
}

// GetConfiguration returns connection metadata and native target references without credentials.
func (service *App) GetConfiguration() (dataservice.SavedConfiguration, error) {
	return service.usage.Configuration(service.context)
}

// SaveConfiguration applies an explicit connection/provider draft.
func (service *App) SaveConfiguration(saved dataservice.SavedConfiguration) (dataservice.SavedConfiguration, error) {
	return service.usage.SaveConfiguration(service.context, saved)
}

// PairEnvironment stores only a native secret reference; the bootstrap is transient.
func (service *App) PairEnvironment(origin, label, bootstrap string, isReadOnly bool) (dataservice.ConnectionPreference, error) {
	return service.usage.Pair(service.context, origin, label, bootstrap, isReadOnly)
}

type SavedSources struct {
	Revision int64                `json:"revision"`
	Value    []preferences.Source `json:"value"`
}

// DetectProviders checks registered installation locations without executing account probes.
func (service *App) DetectProviders() ([]dataservice.ProviderDetection, error) {
	return service.usage.DetectProviders(service.context)
}

// GetSources returns saved source choices independently of quota enablement.
func (service *App) GetSources() (SavedSources, error) {
	value, revision, err := service.usage.Sources(service.context)
	return SavedSources{revision, value}, err
}

// SaveSources persists a complete explicitly applied roots draft.
func (service *App) SaveSources(saved SavedSources) (SavedSources, error) {
	revision, err := service.usage.SaveSources(service.context, saved.Value, saved.Revision)
	saved.Revision = revision
	return saved, err
}

// OpenExternal permits a user-selected public HTTP(S) account or documentation destination.
func (service *App) OpenExternal(destination string) error {
	parsed, err := url.Parse(destination)
	if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return errors.New("Only public HTTP(S) links can be opened.")
	}
	return service.nativeApp.Browser.OpenURL(destination)
}

// GetAppearance returns saved authoritative state and current published palettes before first paint.
func (service *App) GetAppearance() (theme.LibrarySnapshot, error) {
	return service.themes.Read(service.context)
}

// SaveAppearance commits the complete intended library and selection atomically.
func (service *App) SaveAppearance(revision int64, value theme.LibraryValue) (theme.LibrarySnapshot, error) {
	ctx, release, err := service.usage.BeginWrite(service.context)
	if err != nil {
		return theme.LibrarySnapshot{}, err
	}
	defer release()
	snapshot, err := service.themes.Save(ctx, revision, value)
	if err == nil {
		service.nativeApp.Event.Emit("appearance-snapshot", snapshot)
		service.triggerSync()
	}
	return snapshot, err
}

type ThemeFileInput struct {
	Name  string `json:"name"`
	Text  string `json:"text"`
	Size  int64  `json:"size"`
	Error string `json:"error,omitempty"`
}

// ReadThemeFiles reads only explicitly picked/dropped paths after checking size and type.
func (service *App) ReadThemeFiles(paths []string) []ThemeFileInput {
	files := []ThemeFileInput{}
	for _, path := range paths {
		entry := ThemeFileInput{Name: filepath.Base(path)}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			entry.Error = "The theme file could not be read."
		} else if info.Size() > 256*1024 {
			entry.Size = info.Size()
			entry.Error = "Theme files must be no larger than 256 KiB."
		} else {
			file, err := os.Open(path)
			if err == nil {
				var data []byte
				data, err = io.ReadAll(io.LimitReader(file, 256*1024+1))
				file.Close()
				if len(data) > 256*1024 {
					err = errors.New("oversized file")
				}
				if err == nil {
					entry.Text, entry.Size = string(data), int64(len(data))
				}
			}
			if err != nil {
				entry.Error = "The theme file could not be read."
			}
		}
		files = append(files, entry)
	}
	return files
}

// PickThemeFiles opens the native picker; cancellation leaves the import draft untouched.
func (service *App) PickThemeFiles() ([]ThemeFileInput, error) {
	paths, err := service.nativeApp.Dialog.OpenFile().SetTitle("Import themes").AddFilter("Theme JSON", "*.json").AttachToWindow(service.window).PromptForMultipleSelection()
	if err != nil && err.Error() == "cancelled by user" {
		return []ThemeFileInput{}, nil
	}
	if err != nil {
		return nil, err
	}
	return service.ReadThemeFiles(paths), nil
}

// ExportTheme uses the native save dialog for a portable validated version-one theme.
func (service *App) ExportTheme(definition theme.Definition) (bool, error) {
	data, err := theme.SerializeFile(definition)
	if err != nil {
		return false, err
	}
	dialog := service.nativeApp.Dialog.SaveFile()
	dialog.SetOptions(&application.SaveFileDialogOptions{Title: "Export theme", Filename: definition.Id + ".json", Filters: []application.FileFilter{{DisplayName: "Theme JSON", Pattern: "*.json"}}, Window: service.window})
	path, err := dialog.PromptForSingleSelection()
	if err != nil && err.Error() == "cancelled by user" {
		return false, nil
	}
	if err != nil || path == "" {
		return false, err
	}
	if err = os.WriteFile(path, data, 0600); err != nil {
		return false, err
	}
	return true, nil
}

func (service *App) record(event string) {
	service.logMutex.Lock()
	defer service.logMutex.Unlock()
	json.NewEncoder(service.logFile).Encode(map[string]any{"event": event, "pid": os.Getpid(), "at": time.Now().UTC().Format(time.RFC3339Nano)})
}

// GetBootstrap returns native preferences after application initialization.
func (service *App) GetBootstrap() Bootstrap {
	return Bootstrap{IsSystemDark: service.nativeApp.Env.IsDarkMode(), SystemLocale: systemLocale()}
}

// FrontendReady reveals the window after the frontend applies its theme, unless the launch starts minimized.
func (service *App) FrontendReady() {
	service.record("frontend-ready")
	service.windowMutex.Lock()
	isWaitingInTray := service.isWaitingInTray
	service.windowMutex.Unlock()
	if isWaitingInTray {
		return
	}
	service.restoreWindow()
	service.window.Show()
	if hasNativeSmokeArgument() {
		time.AfterFunc(time.Second, service.nativeApp.Quit)
	}
}

func hasNativeSmokeArgument() bool {
	for _, argument := range os.Args[1:] {
		if argument == "--native-smoke" {
			return true
		}
	}
	return false
}

// SyncAppearance updates an opaque background and the native frame caption, which matches the sidebar.
func (service *App) SyncAppearance(isDark bool, red, green, blue, captionRed, captionGreen, captionBlue int) error {
	if err := service.context.Err(); err != nil {
		return err
	}
	for _, channel := range []int{red, green, blue, captionRed, captionGreen, captionBlue} {
		if channel < 0 || channel > 255 {
			return errors.New("invalid background color")
		}
	}
	service.window.SetBackgroundColour(application.NewRGBA(uint8(red), uint8(green), uint8(blue), 255))
	if err := setNativeAppearance(service.window, isDark, captionRed, captionGreen, captionBlue); err != nil {
		return err
	}
	service.record("appearance")
	return nil
}

// OpenDocumentation opens only the fixed official documentation destination.
func (service *App) OpenDocumentation() error {
	return service.nativeApp.Browser.OpenURL("https://v3.wails.io/getting-started/installation/")
}
