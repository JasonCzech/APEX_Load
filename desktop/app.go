package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const (
	defaultAPIReadyTimeout = 10 * time.Second
	defaultShutdownTimeout = 10 * time.Second
	defaultWindowWidth     = 1280
	defaultWindowHeight    = 800
)

// wailsWebviewOrigins are the page origins the Wails webview serves the
// dashboard from across platforms. The embedded API server binds a loopback
// random port, so the dashboard's requests to it are cross-origin and must be
// allow-listed or the browser blocks the CORS preflight (and the WebSocket
// upgrade), surfacing in the dashboard as "Authentication check failed" when
// GET /api/v1/auth/me never resolves.
//
// The exact origin differs per webview/Wails version, so all known variants are
// listed. macOS (WKWebView) is "wails://wails" — host "wails", not
// "wails.localhost" — confirmed from the webview's own location.origin; the
// .localhost and http(s) forms cover Windows (WebView2), Linux (WebKitGTK), and
// older Wails releases. Wails dev injects the native bridge into Vite at
// localhost:3000, so both loopback spellings are allowed; API access still
// requires the per-launch desktop capability.
var wailsWebviewOrigins = []string{
	"wails://wails",
	"wails://wails.localhost",
	"http://wails.localhost",
	"https://wails.localhost",
	// Must track dashboard/vite.config.ts (strictPort, default 4000). 3000 is
	// kept for older local setups that still pin it via PORT.
	"http://localhost:4000",
	"http://127.0.0.1:4000",
	"http://localhost:3000",
	"http://127.0.0.1:3000",
}

// desktopAllowedOrigins includes the configured Vite development port while
// keeping the packaged-webview allow-list fixed. `wails dev` inherits PORT,
// so a custom development port must be reflected in the embedded API CORS
// policy or the native bridge will initialize successfully while every browser
// request is rejected by CORS.
func desktopAllowedOrigins() []string {
	origins := append([]string(nil), wailsWebviewOrigins...)
	rawPort := strings.TrimSpace(os.Getenv("PORT"))
	if rawPort == "" {
		return origins
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil || port < 1 || port > 65535 {
		return origins
	}
	for _, host := range []string{"localhost", "127.0.0.1"} {
		origin := fmt.Sprintf("http://%s:%d", host, port)
		seen := false
		for _, existing := range origins {
			if existing == origin {
				seen = true
				break
			}
		}
		if !seen {
			origins = append(origins, origin)
		}
	}
	return origins
}

// App struct
type App struct {
	*desktopControllerLifecycle
	ctx context.Context
	// apiCapability is a high-entropy, per-launch bearer capability. It is
	// delivered only through the native binding and required by the loopback API.
	apiCapability      string
	recorderProxyToken string
	openMu             sync.Mutex
	rendererReady      bool
	rendererSession    string
	retiredSessions    map[string]struct{}
	retiredSessionFIFO []string
	pendingEvents      []pendingRendererEvent
	pendingDelivery    *rendererDelivery
	nextDeliveryID     uint64

	// lastRoute is the dashboard hash route to restore on next launch. It is
	// updated live via SetLastRoute and persisted with the window geometry on
	// shutdown.
	routeMu   sync.RWMutex
	lastRoute string
	// closingWindowState is captured by OnBeforeClose while the native window is
	// still alive. Wails destroys the platform window before OnShutdown, so the
	// shutdown callback must never call WindowGet* runtime methods.
	windowStateMu       sync.Mutex
	closingWindowState  windowState
	windowStateCaptured bool
	windowStateRestored bool
	windowStateCapture  func(context.Context) (windowState, bool)
	nativeWindowCapture func(context.Context) (nativeWindowGeometry, bool)
	// readPaths/writePaths are the absolute paths the OS file picker/open-with
	// handoff returned for this session. The base64 bridge keeps read and write
	// grants separate so opening/importing a file never authorizes write-back.
	pathMu           sync.Mutex
	readPaths        map[string]readPathGrant
	readDirectories  map[string]readDirectoryGrant
	droppedPaths     map[string]struct{}
	writePaths       map[string]writePathGrant
	readGrantChanged chan struct{}
	dropGrantWait    time.Duration
	recentsMu        sync.Mutex
	// keepAwake inhibits OS sleep while load tests are running (driven by the
	// dashboard's active-run count via SetKeepAwake).
	keepAwake               keepAwake
	notificationMu          sync.Mutex
	notificationClosing     atomic.Bool
	notificationCleanupWait time.Duration
	notificationInitialized bool
	notificationInitErr     error
	notificationAuthOnce    sync.Once
	notificationAuthErr     error
	// closeGuard* hold a native close while the renderer resolves unsaved work
	// (see closeguard.go). closeConfirmed lets the confirmed quit through;
	// closeGuardSetAside is a registration an unanswered reload took out of
	// service, which a late answer from its page restores.
	closeGuardMu       sync.Mutex
	closeGuardToken    string
	closeGuardSetAside string
	closeRequests      uint64
	closeRequestsAcked uint64
	closeConfirmed     bool
	closeAckWait       time.Duration
	quitApp            func(context.Context)
	emitCloseRequest   func(context.Context, string)
	emitReloadRequest  func(context.Context, string)
}

// NewApp creates a new App application struct
func NewApp() *App {
	app := &App{
		desktopControllerLifecycle: newDesktopControllerLifecycle(),
		readPaths:                  make(map[string]readPathGrant),
		writePaths:                 make(map[string]writePathGrant),
		readGrantChanged:           make(chan struct{}),
		dropGrantWait:              2 * time.Second,
		notificationCleanupWait:    defaultNotificationCleanupTimeout,
		closeAckWait:               defaultCloseRequestAckWait,
		quitApp:                    runtime.Quit,
		emitCloseRequest:           emitCloseRequestEvent,
		emitReloadRequest:          emitReloadRequestEvent,
		retiredSessions:            make(map[string]struct{}),
	}
	app.windowStateCapture = app.captureWindowState
	app.nativeWindowCapture = captureNativeWindowGeometry
	capability, err := newDesktopCapability()
	if err != nil {
		app.failStartup("failed to initialize desktop API security", err)
	} else {
		app.apiCapability = capability
	}
	proxyToken, err := newDesktopCapability()
	if err != nil {
		app.failStartup("failed to initialize recording proxy security", err)
	} else {
		app.recorderProxyToken = proxyToken
	}
	return app
}

// runtimeContext returns the Wails context published by startup. Native menu,
// bridge, and shutdown callbacks can run on different goroutines, so every
// access uses the same lock as the renderer-delivery state that also consumes
// this context.
func (a *App) runtimeContext() context.Context {
	a.openMu.Lock()
	defer a.openMu.Unlock()
	return a.ctx
}

func (a *App) setRuntimeContext(ctx context.Context) {
	a.openMu.Lock()
	a.ctx = ctx
	a.openMu.Unlock()
}

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	if !a.beginStartup() {
		a.publishInitialisation()
		return
	}
	defer a.finishStartup()

	a.setRuntimeContext(ctx)
	a.captureInitialOpenRequests()
	if a.GetStartupError() != "" {
		return
	}
	runtime.OnFileDrop(ctx, func(_, _ int, paths []string) {
		for _, path := range paths {
			if isImportableFile(path) {
				a.rememberReadPath(path)
			}
			// A dropped folder (a Bruno collection) is read through
			// ReadDroppedDirectory, even one named like a file type
			// (orders.yml/); anything that is not a folder is ignored.
			a.rememberDroppedDirectory(path)
		}
		a.rememberDropEvent(paths)
	})

	if !a.initializeController(ctx, desktopControllerSetup{Capability: a.GetAPICapability(), RecorderProxyToken: a.recorderProxyToken, BeforeServerPublication: a.restoreWindowState}) {
		return
	}

	// Prepare native notifications (best-effort; stays unavailable on failure).
	a.initNotifications()

	// Populate the native "Open Recent" submenu now that the app data path is
	// resolved (the menu built in main.go was created before recents loaded).
	// Linux keeps its initial Wails menu static; recents remain in the dashboard.
	a.rebuildMenu()
}

// restoreWindowState applies persisted window geometry after the Wails context
// is published. Wails runs OnStartup on a goroutine; on Linux, native window
// mutations must therefore be dispatched to the GTK main loop explicitly.
func (a *App) restoreWindowState() {
	defer a.markWindowStateRestored()
	ctx := a.runtimeContext()
	appDataPath := a.appDataDirectory()
	if ctx == nil || appDataPath == "" {
		return
	}
	st, ok := loadWindowState(appDataPath)
	if !ok {
		return
	}
	a.routeMu.Lock()
	a.lastRoute = st.LastRoute
	a.routeMu.Unlock()

	// Apply the (preserved, non-maximized) geometry first so that un-maximizing a
	// window that was closed while maximized restores the user's previous size.
	g := sanitizeWindowGeometryInBounds(st, a.desktopBounds(ctx))
	if (g.ApplySize || st.Maximised) && !applyRestoredWindowState(ctx, g, st.Maximised) {
		slog.Warn("desktop window state could not be dispatched to the native UI thread")
	}
}

var (
	dispatchWindowStateMutation = dispatchNativeUI
	windowSetSize               = runtime.WindowSetSize
	windowSetPosition           = setNativeWindowPosition
	windowCenter                = runtime.WindowCenter
	windowMaximise              = runtime.WindowMaximise
)

// applyRestoredWindowState keeps the restore sequence in one native-UI
// dispatch. This is required for WindowSetSize on Wails' Linux backend, which
// calls gtk_window_resize directly, and also preserves size/position/maximise
// ordering across every platform.
func applyRestoredWindowState(ctx context.Context, g sanitizedGeometry, maximised bool) bool {
	return dispatchWindowStateMutation(func() {
		if g.ApplySize {
			windowSetSize(ctx, g.Width, g.Height)
			if g.ApplyPosition {
				windowSetPosition(ctx, g.X, g.Y)
			} else {
				// Valid size but untrusted position → center on the usable desktop.
				windowCenter(ctx)
			}
		}
		if maximised {
			windowMaximise(ctx)
		}
	})
}

// desktopBounds returns conservative usable-desktop bounds. Wails' Screen type
// exposes logical size but not per-monitor offsets, so it cannot distinguish a
// horizontal layout from a vertical one (or detect that the same displays were
// rearranged between launches). Trusting a synthesized virtual rectangle can
// therefore restore a window entirely into empty space. Validate against the
// primary display instead; a secondary-display position is re-centered, but the
// application always remains reachable on every platform.
func (a *App) desktopBounds(ctx context.Context) virtualDesktopBounds {
	screens, err := runtime.ScreenGetAll(ctx)
	return desktopBoundsFromScreenResult(screens, err)
}

func desktopBoundsFromScreenResult(screens []runtime.Screen, _ error) virtualDesktopBounds {
	// Wails on Windows can return usable displays together with an aggregate
	// error when DPI or monitor-info lookup fails for only one display. Preserve
	// every valid entry; desktopBoundsFromScreens already ignores zero-sized
	// partial records and returns unknown bounds if none remain.
	return desktopBoundsFromScreens(screens)
}

func desktopBoundsFromScreens(screens []runtime.Screen) virtualDesktopBounds {
	primaryW, primaryH := 0, 0
	fallbackW, fallbackH := 0, 0
	for _, s := range screens {
		// Wails sets Size whenever it sets the deprecated Width and Height,
		// and reports a screen it could not measure as entirely zero.
		w, h := s.Size.Width, s.Size.Height
		if w <= 0 || h <= 0 {
			continue
		}
		if fallbackW == 0 {
			fallbackW, fallbackH = w, h
		}
		if s.IsPrimary {
			primaryW = w
			primaryH = h
		}
	}
	if primaryW <= 0 {
		primaryW, primaryH = fallbackW, fallbackH
	}
	if primaryW <= 0 || primaryH <= 0 {
		return virtualDesktopBounds{}
	}
	return virtualDesktopBounds{Width: primaryW, Height: primaryH}
}

// captureWindowState reads live native geometry for persistence. It is called
// only from OnBeforeClose; after that callback returns Wails may destroy the
// window on every supported platform.
func (a *App) captureWindowState(ctx context.Context) (windowState, bool) {
	st := windowState{LastRoute: a.GetLastRoute()}
	if ctx == nil {
		return windowState{}, false
	}
	geometry, ok := a.nativeWindowCapture(ctx)
	if !ok {
		return windowState{}, false
	}
	if geometry.Maximised {
		st.Maximised = true
		if geometry.HasRestoreBounds {
			// Windows GetWindowPlacement provides the current session's normal
			// bounds even while the window is maximized or minimized.
			st.Width, st.Height = geometry.Width, geometry.Height
			st.X, st.Y = geometry.X, geometry.Y
		} else if prev, ok := loadWindowState(a.appDataDirectory()); ok {
			// Wails has no portable restore-bounds getter, and a maximized size
			// reports the maximized frame. Preserve the last persisted normal
			// geometry on macOS/Linux until their native APIs expose it here.
			st.Width, st.Height, st.X, st.Y = prev.Width, prev.Height, prev.X, prev.Y
		}
		if st.Width <= 0 || st.Height <= 0 {
			// A maximized first launch has no prior restore bounds. Persist the
			// configured normal size so the next launch still restores maximized and
			// un-maximizing lands on a useful frame.
			st.Width, st.Height = defaultWindowWidth, defaultWindowHeight
			st.X, st.Y = 0, 0
		}
		return st, true
	}
	st.Width, st.Height = geometry.Width, geometry.Height
	st.X, st.Y = geometry.X, geometry.Y
	if st.Width <= 0 || st.Height <= 0 {
		return windowState{}, false
	}
	return st, true
}

// beforeClose snapshots geometry while Wails still owns a live native window.
// Returning false permits the close on macOS, Linux, and Windows; a close the
// renderer must resolve first (unsaved work) is held instead.
func (a *App) beforeClose(ctx context.Context) bool {
	if a.holdCloseForRenderer(ctx) {
		return true
	}
	a.windowStateMu.Lock()
	restored := a.windowStateRestored
	a.windowStateMu.Unlock()
	if !restored {
		// A close can race slow storage initialization. Capturing Wails' default
		// frame and empty route before persisted state is restored would overwrite
		// the prior session during shutdown, so preserve the existing file instead.
		return false
	}
	st, ok := a.windowStateCapture(ctx)
	if ok {
		// Commit geometry and the latest route under a consistent route -> window
		// lock order. SetLastRoute uses the same order, so a bridge update that
		// races this snapshot is either included here or merged immediately after.
		a.routeMu.RLock()
		a.windowStateMu.Lock()
		st.LastRoute = a.lastRoute
		a.closingWindowState = st
		a.windowStateCaptured = true
		a.windowStateMu.Unlock()
		a.routeMu.RUnlock()
	}
	return false
}

func (a *App) markWindowStateRestored() {
	a.windowStateMu.Lock()
	a.windowStateRestored = true
	a.windowStateMu.Unlock()
}

func (a *App) cachedClosingWindowState() (windowState, bool) {
	a.windowStateMu.Lock()
	defer a.windowStateMu.Unlock()
	return a.closingWindowState, a.windowStateCaptured
}

// SetLastRoute records the dashboard's current hash route so it can be restored
// next launch. Called from the dashboard on navigation in desktop mode.
func (a *App) SetLastRoute(route string) {
	a.routeMu.Lock()
	trimmed := strings.TrimSpace(route)
	a.lastRoute = trimmed
	// A navigation bridge call can already be queued when OnBeforeClose takes
	// its native-geometry snapshot. Merge any late route into that cached state;
	// shutdown intentionally never touches the destroyed native window again.
	a.windowStateMu.Lock()
	if a.windowStateCaptured {
		a.closingWindowState.LastRoute = trimmed
	}
	a.windowStateMu.Unlock()
	a.routeMu.Unlock()
}

// GetLastRoute returns the route restored from the previous session (empty on
// first launch). The dashboard reads this once on mount to restore navigation.
func (a *App) GetLastRoute() string {
	a.routeMu.RLock()
	defer a.routeMu.RUnlock()
	return a.lastRoute
}

// shutdown is called at application termination
func (a *App) shutdown(ctx context.Context) {
	a.shutdownController(ctx, desktopShutdownHooks{Begin: func() { a.notificationClosing.Store(true) }, BeforeController: func(ctx context.Context, startupJoined bool) {
		// Persist window geometry + last route before tearing down. A hard kill
		// won't reach this, which is acceptable. Do not race startup's publication
		// of the app-data directory when its bounded join timed out.
		appDataPath := a.appDataDirectory()
		if st, ok := a.cachedClosingWindowState(); startupJoined && appDataPath != "" && ok {
			if err := saveWindowState(appDataPath, st); err != nil {
				slog.Warn("failed to persist window state", "err", err)
			}
		}
		a.keepAwake.set(false) // release any sleep inhibitor
		a.SetDockBadge("")     // clear the dock/taskbar badge
		a.cleanupNotifications(ctx)
	}})
}

// SelectFile opens a native OS file dialog and returns the selected file path
func (a *App) SelectFile(title string) (string, error) {
	ctx := a.runtimeContext()
	if ctx == nil {
		return "", fmt.Errorf("desktop runtime is not ready")
	}
	return runtime.OpenFileDialog(ctx, runtime.OpenDialogOptions{
		Title: title,
	})
}

func (a *App) OpenInBrowser(devViewMode, projectID string) error {
	ctx := a.runtimeContext()
	if ctx == nil {
		return fmt.Errorf("desktop runtime is not ready")
	}
	if _, err := a.GetAPIOrigin(); err != nil {
		return err
	}
	a.lifecycleMu.RLock()
	server := a.server
	a.lifecycleMu.RUnlock()
	if server == nil {
		return fmt.Errorf("API server is not running")
	}
	// NewDesktopBrowserURL validates this renderer-supplied, non-secret context
	// before retaining it behind the one-time nonce. It is never appended to the
	// browser URL, so project state cannot become history/referrer material.
	browserURL, err := server.NewDesktopBrowserURL(devViewMode, projectID)
	if err != nil {
		return fmt.Errorf("create authenticated desktop browser URL: %w", err)
	}
	browserURL, err = appendDesktopBrowserRoute(browserURL, a.GetLastRoute())
	if err != nil {
		return fmt.Errorf("validate authenticated desktop browser URL: %w", err)
	}
	runtime.BrowserOpenURL(ctx, browserURL)
	return nil
}

func appendDesktopBrowserRoute(browserURL, route string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(browserURL))
	if err != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.Hostname() == "" {
		return "", fmt.Errorf("desktop browser URL must use a loopback HTTP origin")
	}
	host := parsed.Hostname()
	ip := net.ParseIP(host)
	if !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) {
		return "", fmt.Errorf("desktop browser URL must use a loopback HTTP origin")
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil || port < 1 || port > 65535 {
		return "", fmt.Errorf("desktop browser URL must use a bound port")
	}

	route = strings.TrimSpace(route)
	if route == "" || len(route) > 2048 || !strings.HasPrefix(route, "#/") || strings.HasPrefix(route, "#//") {
		return parsed.String(), nil
	}
	for _, character := range route {
		if character < 0x20 || character == 0x7f || character == '\\' {
			return parsed.String(), nil
		}
	}
	parsed.Fragment = strings.TrimPrefix(route, "#")
	return parsed.String(), nil
}

// GetAPICapability returns the per-launch loopback API capability to the
// trusted Wails renderer. It must be sent on REST requests and WebSocket setup.
func (a *App) GetAPICapability() string {
	a.lifecycleMu.RLock()
	defer a.lifecycleMu.RUnlock()
	return a.apiCapability
}

func (a *App) OpenLogsFolder() error {
	return a.openFolder(a.logDirectory())
}

func (a *App) OpenAppDataFolder() error {
	return a.openFolder(a.appDataDirectory())
}

func (a *App) openFolder(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("folder path is not available")
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", path)
	}
	return launchAndReap(folderOpenCommand(path))
}

func desktopAppDataPathCandidates() (current, legacy string, err error) {
	if dir := stringsTrimmedEnv("APEXLOAD_DESKTOP_APP_DATA"); dir != "" {
		current, err = filepath.Abs(filepath.Clean(dir))
		return current, "", err
	}
	base, legacyBase, err := platformDesktopAppDataBases()
	if err != nil {
		return "", "", err
	}
	current, err = filepath.Abs(filepath.Join(base, "APEX Load"))
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(legacyBase) == "" {
		return current, "", nil
	}
	legacy, err = filepath.Abs(filepath.Join(legacyBase, "APEX Load"))
	if err != nil {
		return "", "", err
	}
	return current, legacy, nil
}

func desktopAppDataPath() (string, error) {
	current, legacy, err := desktopAppDataPathCandidates()
	if err != nil {
		return "", err
	}
	if legacy == "" {
		return current, nil
	}
	return migrateDesktopAppDataPath(current, legacy, os.Rename)
}

// desktopAppDataPathForInitialMenu locates existing recents without performing
// Windows' legacy-directory migration. It runs before Wails single-instance
// coordination, so any filesystem mutation here could reintroduce a dual-launch
// race. OnStartup later performs the migration while the process is coordinated.
func desktopAppDataPathForInitialMenu() (string, error) {
	current, legacy, err := desktopAppDataPathCandidates()
	if err != nil {
		return "", err
	}
	return selectInitialMenuAppDataPath(current, legacy)
}

func selectInitialMenuAppDataPath(current, legacy string) (string, error) {
	if info, err := os.Stat(current); err == nil {
		if !info.IsDir() {
			return "", fmt.Errorf("desktop app data path is not a directory: %s", current)
		}
		return current, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if strings.TrimSpace(legacy) == "" {
		return current, nil
	}
	if info, err := os.Stat(legacy); err == nil {
		if !info.IsDir() {
			return "", fmt.Errorf("legacy desktop app data path is not a directory: %s", legacy)
		}
		return legacy, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	return current, nil
}

// migrateDesktopAppDataPath moves a legacy roaming-profile directory into the
// platform's local app-data directory when possible. If a redirected profile
// makes the rename cross-volume (or any other safe move fails), it deliberately
// falls back to the intact legacy directory rather than silently launching with
// an empty database and a newly generated recorder CA.
func migrateDesktopAppDataPath(current, legacy string, rename func(string, string) error) (string, error) {
	if filepath.Clean(current) == filepath.Clean(legacy) {
		return current, nil
	}
	if info, err := os.Stat(current); err == nil {
		if !info.IsDir() {
			return "", fmt.Errorf("desktop app data path is not a directory: %s", current)
		}
		return current, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	legacyInfo, err := os.Stat(legacy)
	if os.IsNotExist(err) {
		return current, nil
	}
	if err != nil {
		return "", err
	}
	if !legacyInfo.IsDir() {
		return "", fmt.Errorf("legacy desktop app data path is not a directory: %s", legacy)
	}
	if err := os.MkdirAll(filepath.Dir(current), privateDirectoryMode); err != nil {
		return "", err
	}
	if err := rename(legacy, current); err == nil {
		return current, nil
	}
	// Another process may have won the migration race after our initial stat.
	if info, statErr := os.Stat(current); statErr == nil && info.IsDir() {
		return current, nil
	}
	return legacy, nil
}

func stringsTrimmedEnv(name string) string {
	return strings.TrimSpace(os.Getenv(name))
}

func newDesktopCapability() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func validateLoopbackAPIOrigin(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("parse API origin: %w", err)
	}
	if u.Scheme != "http" || u.Hostname() == "" || u.Port() == "" {
		return fmt.Errorf("invalid loopback API origin %q", raw)
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("API origin is not loopback: %q", raw)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port <= 0 || port > 65535 {
		return fmt.Errorf("API origin has invalid port: %q", raw)
	}
	if u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return fmt.Errorf("API origin contains unexpected components: %q", raw)
	}
	return nil
}

// desktopStoreConfig makes desktop defaults deterministic while preserving
// explicit operator configuration. A database URL implies PostgreSQL when no
// backend was named; a relative SQLite path is rooted in private app data, not
// the launcher's arbitrary working directory.
func desktopStoreConfig(appDataPath string) storage.StoreConfig {
	cfg := storage.StoreConfigFromEnv()
	backend := strings.ToLower(stringsTrimmedEnv("APEXLOAD_STORE"))
	sqlitePath := stringsTrimmedEnv("APEXLOAD_DB")
	databaseURL := stringsTrimmedEnv("APEXLOAD_DATABASE_URL")
	if backend == "postgresql" {
		backend = storage.StoreBackendPostgres
	}
	if backend == "" {
		if databaseURL != "" {
			backend = storage.StoreBackendPostgres
		} else {
			backend = storage.StoreBackendSQLite
		}
	}
	cfg.Backend = backend
	cfg.DatabaseURL = databaseURL
	if backend == storage.StoreBackendSQLite {
		if sqlitePath == "" {
			sqlitePath = filepath.Join(appDataPath, "apexload.db")
		} else if !filepath.IsAbs(sqlitePath) {
			sqlitePath = filepath.Join(appDataPath, sqlitePath)
		}
		cfg.SQLitePath = filepath.Clean(sqlitePath)
	}
	return cfg
}

func pathWithinDirectory(base, path string) bool {
	base = cleanBridgePath(base)
	path = cleanBridgePath(path)
	if base == "" || path == "" {
		return false
	}
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// GetAPIOrigin publishes only a ready embedded controller whose startup boundary completed.
func (a *App) GetAPIOrigin() (string, error) { return a.desktopControllerLifecycle.GetAPIOrigin() }

// GetStartupError retains the native binding used to display startup failures.
func (a *App) GetStartupError() string { return a.desktopControllerLifecycle.GetStartupError() }
