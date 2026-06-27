package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
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
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{}
}

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

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

	// Start the API server in the background
	go func() {
		if err := a.server.Start(); err != nil {
			slog.Error("server error", "err", err)
		}
	}()

	// Wait briefly for the server to be ready
	time.Sleep(200 * time.Millisecond)

	// Restore window size/position/maximized state from the previous session.
	a.restoreWindowState()

	// Prepare native notifications (best-effort; stays unavailable on failure).
	a.initNotifications()
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
	if st.Maximised {
		runtime.WindowMaximise(a.ctx)
		return
	}
	deskW, deskH := a.desktopBounds()
	g := sanitizeWindowGeometry(st, deskW, deskH)
	if g.ApplySize {
		runtime.WindowSetSize(a.ctx, g.Width, g.Height)
	}
	if g.ApplyPosition {
		runtime.WindowSetPosition(a.ctx, g.X, g.Y)
	} else if g.ApplySize {
		// Valid size but untrusted position → center on the usable desktop.
		runtime.WindowCenter(a.ctx)
	}
}

// desktopBounds returns a conservative usable-desktop size (origin 0,0) modeled
// as the sum of screen widths × the tallest screen height. Wails' Screen type
// exposes logical size but not per-monitor offset, so this is a heuristic used
// only to detect a fully off-screen restore; it returns 0,0 when unknown.
func (a *App) desktopBounds() (int, int) {
	screens, err := runtime.ScreenGetAll(a.ctx)
	if err != nil || len(screens) == 0 {
		return 0, 0
	}
	totalW, maxH := 0, 0
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
		if h > maxH {
			maxH = h
		}
	}
	return totalW, maxH
}

// captureWindowState reads the current geometry for persistence.
func (a *App) captureWindowState() windowState {
	st := windowState{LastRoute: a.lastRoute}
	if a.ctx == nil {
		return st
	}
	if runtime.WindowIsMaximised(a.ctx) {
		st.Maximised = true
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
