package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/api"
	"github.com/jasonczech/apexload/pkg/storage"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// wailsWebviewOrigins are the page origins the Wails webview serves the
// dashboard from across platforms. The embedded API server binds a loopback
// random port, so the dashboard's requests to it are cross-origin and must be
// allow-listed or the browser blocks the CORS preflight (and the WebSocket
// upgrade), surfacing in the dashboard as "Authentication check failed" when
// GET /api/v1/auth/me never resolves.
//
// The exact origin differs per webview/Wails version, so all known variants are
// listed. macOS (WKWebView, Wails v2.12) is "wails://wails" — host "wails", not
// "wails.localhost" — confirmed from the webview's own location.origin; the
// .localhost and http(s) forms cover Windows (WebView2), Linux (WebKitGTK), and
// older Wails releases.
var wailsWebviewOrigins = []string{
	"wails://wails",
	"wails://wails.localhost",
	"http://wails.localhost",
	"https://wails.localhost",
}

// App struct
type App struct {
	ctx         context.Context
	server      *api.Server
	store       storage.Store
	appDataPath string
	logPath     string
	// lastRoute is the dashboard hash route to restore on next launch. It is
	// updated live via SetLastRoute and persisted with the window geometry on
	// shutdown.
	lastRoute string
	// startupErr records a fatal startup failure (storage/server init). It is
	// surfaced to the dashboard via GetStartupError so the embedded controller's
	// failure can be shown in-app — the HTTP /desktop/status endpoint can't carry
	// it because a failed server isn't listening.
	startupErr string
	// readPaths/writePaths are the absolute paths the OS file picker/open-with
	// handoff returned for this session. The base64 bridge keeps read and write
	// grants separate so opening/importing a file never authorizes write-back.
	pathMu     sync.Mutex
	readPaths  map[string]bool
	writePaths map[string]bool
	recentsMu  sync.Mutex
	// keepAwake inhibits OS sleep while load tests are running (driven by the
	// dashboard's active-run count via SetKeepAwake).
	keepAwake keepAwake
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{}
}

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	runtime.OnFileDrop(ctx, func(_, _ int, paths []string) {
		for _, path := range paths {
			if isImportableFile(path) {
				a.rememberReadPath(path)
			}
		}
	})

	appDataPath, err := desktopAppDataPath()
	if err != nil {
		a.failStartup("failed to resolve app data path", err)
		return
	}
	a.appDataPath = appDataPath
	if err := os.MkdirAll(appDataPath, 0o755); err != nil {
		a.failStartup("failed to create app data folder", err)
		return
	}
	logPath := filepath.Join(appDataPath, "logs")
	if err := os.MkdirAll(logPath, 0o755); err != nil {
		a.failStartup("failed to create log folder", err)
		return
	}
	a.logPath = logPath

	// Initialize the database storage
	storeConfig := storage.StoreConfigFromEnv()
	if stringsTrimmedEnv("APEXLOAD_DB") == "" && stringsTrimmedEnv("APEXLOAD_DATABASE_URL") == "" {
		storeConfig.Backend = storage.StoreBackendSQLite
		storeConfig.SQLitePath = filepath.Join(appDataPath, "apexload.db")
	}
	store, err := storage.NewConfiguredStore(storeConfig)
	if err != nil {
		a.failStartup("failed to initialize local storage", err)
		return
	}
	a.store = store

	// Initialize the API Server
	a.server = api.NewServer(api.ServerConfig{
		Addr:               "127.0.0.1:0",
		MaxConcurrent:      10,
		Store:              store,
		LibraryCredentials: api.NewKeyringLibraryCredentialStore(),
		AllowedOrigins:     wailsWebviewOrigins,
		DesktopMode:        true,
		DesktopAppDataPath: appDataPath,
		DesktopLogPath:     logPath,
	})

	// Start the API server in the background. Nothing below depends on the server
	// being up — the dashboard discovers the bound port via GetAPIOrigin(), which
	// polls BoundAddrReady() — so there is no readiness wait here.
	go func() {
		if err := a.server.Start(); err != nil {
			slog.Error("server error", "err", err)
		}
	}()

	// Restore window size/position/maximized state from the previous session.
	a.restoreWindowState()

	// Prepare native notifications (best-effort; stays unavailable on failure).
	a.initNotifications()

	// Populate the native "Open Recent" submenu now that the app data path is
	// resolved (the menu built in main.go was created before recents loaded).
	a.rebuildMenu()
}

// restoreWindowState applies persisted window geometry. Runs on the Wails
// runtime thread (after ctx is set), never the server goroutine.
func (a *App) restoreWindowState() {
	if a.ctx == nil || a.appDataPath == "" {
		return
	}
	st, ok := loadWindowState(a.appDataPath)
	if !ok {
		return
	}
	a.lastRoute = st.LastRoute

	// Apply the (preserved, non-maximized) geometry first so that un-maximizing a
	// window that was closed while maximized restores the user's previous size.
	g := sanitizeWindowGeometryInBounds(st, a.desktopBounds())
	if g.ApplySize {
		runtime.WindowSetSize(a.ctx, g.Width, g.Height)
		if g.ApplyPosition {
			runtime.WindowSetPosition(a.ctx, g.X, g.Y)
		} else {
			// Valid size but untrusted position → center on the usable desktop.
			runtime.WindowCenter(a.ctx)
		}
	}
	if st.Maximised {
		runtime.WindowMaximise(a.ctx)
	}
}

// desktopBounds returns conservative usable-desktop bounds. Wails' Screen type
// exposes logical size but not per-monitor offset, so for multi-monitor setups
// we allow the non-primary aggregate space to exist left of or above the primary
// before declaring a negative saved position off-screen.
func (a *App) desktopBounds() virtualDesktopBounds {
	screens, err := runtime.ScreenGetAll(a.ctx)
	if err != nil || len(screens) == 0 {
		return virtualDesktopBounds{}
	}
	totalW, totalH, maxH := 0, 0, 0
	primaryW, primaryH := 0, 0
	for _, s := range screens {
		w := s.Size.Width
		if w == 0 {
			w = s.Width
		}
		h := s.Size.Height
		if h == 0 {
			h = s.Height
		}
		totalW += w
		totalH += h
		if h > maxH {
			maxH = h
		}
		if s.IsPrimary {
			primaryW = w
			primaryH = h
		}
	}
	if len(screens) == 1 {
		return virtualDesktopBounds{Width: totalW, Height: maxH}
	}
	if primaryW <= 0 {
		primaryW = totalW / len(screens)
	}
	if primaryH <= 0 {
		primaryH = maxH
	}
	minX := -(totalW - primaryW)
	minY := -(totalH - primaryH)
	return virtualDesktopBounds{MinX: minX, MinY: minY, Width: totalW, Height: totalH}
}

// captureWindowState reads the current geometry for persistence.
func (a *App) captureWindowState() windowState {
	st := windowState{LastRoute: a.lastRoute}
	if a.ctx == nil {
		return st
	}
	if runtime.WindowIsMaximised(a.ctx) {
		st.Maximised = true
		// Wails has no "restore bounds" getter, and a maximized WindowGetSize
		// reports the maximized frame. Preserve the last persisted non-maximized
		// geometry so an un-maximize after restore lands on the user's prior size.
		if prev, ok := loadWindowState(a.appDataPath); ok {
			st.Width, st.Height, st.X, st.Y = prev.Width, prev.Height, prev.X, prev.Y
		}
		return st
	}
	st.Width, st.Height = runtime.WindowGetSize(a.ctx)
	st.X, st.Y = runtime.WindowGetPosition(a.ctx)
	return st
}

// failStartup logs a fatal startup failure and records a user-facing message
// the dashboard can show via GetStartupError.
func (a *App) failStartup(msg string, err error) {
	slog.Error(msg, "err", err)
	if err != nil {
		a.startupErr = fmt.Sprintf("%s: %v", msg, err)
	} else {
		a.startupErr = msg
	}
}

// GetStartupError returns a non-empty message when the embedded controller
// failed to start (storage/server init). Surfaced in the dashboard so the user
// sees why local features are unavailable instead of a silent failure.
func (a *App) GetStartupError() string {
	return a.startupErr
}

// SetLastRoute records the dashboard's current hash route so it can be restored
// next launch. Called from the dashboard on navigation in desktop mode.
func (a *App) SetLastRoute(route string) {
	a.lastRoute = strings.TrimSpace(route)
}

// GetLastRoute returns the route restored from the previous session (empty on
// first launch). The dashboard reads this once on mount to restore navigation.
func (a *App) GetLastRoute() string {
	return a.lastRoute
}

// shutdown is called at application termination
func (a *App) shutdown(ctx context.Context) {
	// Persist window geometry + last route before tearing down. A hard kill
	// won't reach this, which is acceptable.
	if a.appDataPath != "" {
		if err := saveWindowState(a.appDataPath, a.captureWindowState()); err != nil {
			slog.Warn("failed to persist window state", "err", err)
		}
	}
	a.keepAwake.set(false) // release any sleep inhibitor
	a.SetDockBadge("")     // clear the dock/taskbar badge
	a.cleanupNotifications()
	if a.server != nil {
		a.server.Shutdown(context.Background())
	}
	if a.store != nil {
		a.store.Close()
	}
}

// SelectFile opens a native OS file dialog and returns the selected file path
func (a *App) SelectFile(title string) (string, error) {
	return runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: title,
	})
}

func (a *App) GetAPIOrigin() string {
	if a.server == nil {
		return ""
	}
	// The API server binds its random loopback port asynchronously (Start runs
	// in a goroutine). Wait for the listener to come up so the dashboard is
	// pointed at the real port rather than the unbound ":0" placeholder, which
	// would make every API request — including the startup auth check — fail.
	deadline := time.Now().Add(5 * time.Second)
	for !a.server.BoundAddrReady() {
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	return a.server.BrowserURL()
}

func (a *App) OpenInBrowser() error {
	if a.server == nil {
		return fmt.Errorf("API server is not running")
	}
	runtime.BrowserOpenURL(a.ctx, a.server.BrowserURL())
	return nil
}

func (a *App) OpenLogsFolder() error {
	return a.openFolder(a.logPath)
}

func (a *App) OpenAppDataFolder() error {
	return a.openFolder(a.appDataPath)
}

func (a *App) openFolder(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("folder path is not available")
	}
	runtime.BrowserOpenURL(a.ctx, (&url.URL{Scheme: "file", Path: path}).String())
	return nil
}

func desktopAppDataPath() (string, error) {
	if dir := stringsTrimmedEnv("APEXLOAD_DESKTOP_APP_DATA"); dir != "" {
		return dir, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "APEX Load"), nil
}

func stringsTrimmedEnv(name string) string {
	return strings.TrimSpace(os.Getenv(name))
}
