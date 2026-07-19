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

	"github.com/jasonczech/apexload/pkg/api"
	"github.com/jasonczech/apexload/pkg/storage"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const (
	defaultAPIReadyTimeout = 10 * time.Second
	defaultShutdownTimeout = 10 * time.Second
	defaultWindowWidth     = 1280
	defaultWindowHeight    = 800
)

// desktopAPIServer is the lifecycle surface the native shell needs from the
// embedded controller. Keeping this small also makes startup and shutdown
// failure paths testable without a live Wails runtime.
type desktopAPIServer interface {
	Start() error
	Shutdown(context.Context) error
	BoundAddrReady() bool
	BoundAddr() string
	BrowserURL() string
	NewDesktopBrowserURL(devViewMode, projectID string) (string, error)
}

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
	ctx         context.Context
	server      desktopAPIServer
	store       storage.Store
	appDataPath string
	logPath     string
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

	// lifecycleMu protects server startup state because Wails bridge calls can
	// race the goroutine running Server.Start and the shutdown callback.
	lifecycleMu    sync.RWMutex
	initialised    chan struct{}
	initialiseOnce sync.Once
	startupDone    chan struct{}
	startupOnce    sync.Once
	startupStarted bool
	serverDone     chan struct{}
	serverExitErr  error
	serverWG       sync.WaitGroup
	shuttingDown   bool
	apiReadyWait   time.Duration
	shutdownWait   time.Duration
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
	// startupErr records a fatal startup failure (storage/server init). It is
	// surfaced to the dashboard via GetStartupError so the embedded controller's
	// failure can be shown in-app — the HTTP /desktop/status endpoint can't carry
	// it because a failed server isn't listening.
	startupErr string
	// readPaths/writePaths are the absolute paths the OS file picker/open-with
	// handoff returned for this session. The base64 bridge keeps read and write
	// grants separate so opening/importing a file never authorizes write-back.
	pathMu           sync.Mutex
	readPaths        map[string]readPathGrant
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
	instanceLock            desktopProcessLock
}

// NewApp creates a new App application struct
func NewApp() *App {
	app := &App{
		readPaths:               make(map[string]readPathGrant),
		writePaths:              make(map[string]writePathGrant),
		readGrantChanged:        make(chan struct{}),
		dropGrantWait:           2 * time.Second,
		apiReadyWait:            defaultAPIReadyTimeout,
		shutdownWait:            defaultShutdownTimeout,
		notificationCleanupWait: defaultNotificationCleanupTimeout,
		initialised:             make(chan struct{}),
		startupDone:             make(chan struct{}),
		retiredSessions:         make(map[string]struct{}),
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

// publishStartupPaths makes successfully-created private directories visible
// to callbacks that can race the Wails startup goroutine.
func (a *App) publishAppDataPath(path string) {
	a.lifecycleMu.Lock()
	a.appDataPath = path
	a.lifecycleMu.Unlock()
}

func (a *App) publishLogPath(path string) {
	a.lifecycleMu.Lock()
	a.logPath = path
	a.lifecycleMu.Unlock()
}

func (a *App) appDataDirectory() string {
	a.lifecycleMu.RLock()
	defer a.lifecycleMu.RUnlock()
	return a.appDataPath
}

func (a *App) logDirectory() string {
	a.lifecycleMu.RLock()
	defer a.lifecycleMu.RUnlock()
	return a.logPath
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
		}
	})

	appDataPath, err := desktopAppDataPath()
	if err != nil {
		a.failStartup("failed to resolve app data path", err)
		return
	}
	if err := ensurePrivateDirectory(appDataPath); err != nil {
		a.failStartup("failed to create app data folder", err)
		return
	}
	a.publishAppDataPath(appDataPath)
	instanceLock, err := acquireDesktopProcessLockWithRetry(
		appDataPath,
		desktopProcessLockStartupWait(),
		desktopProcessLockRetryInterval,
		acquireDesktopProcessLock,
	)
	if err != nil {
		a.failStartup("another APEX Load desktop process owns the local controller state", err)
		return
	}
	a.lifecycleMu.Lock()
	if a.shuttingDown {
		a.lifecycleMu.Unlock()
		_ = instanceLock.Close()
		return
	}
	a.instanceLock = instanceLock
	a.lifecycleMu.Unlock()
	logPath := filepath.Join(appDataPath, "logs")
	if err := ensurePrivateDirectory(logPath); err != nil {
		a.failStartup("failed to create log folder", err)
		return
	}
	a.publishLogPath(logPath)
	recorderStatePath := filepath.Join(appDataPath, "recorder")
	if err := ensurePrivateDirectory(recorderStatePath); err != nil {
		a.failStartup("failed to create recording proxy state folder", err)
		return
	}

	// Initialize the database storage
	storeConfig := desktopStoreConfig(appDataPath)
	privateSQLite := storeConfig.Backend == storage.StoreBackendSQLite && pathWithinDirectory(appDataPath, storeConfig.SQLitePath)
	if privateSQLite {
		if err := ensurePrivateDirectory(filepath.Dir(storeConfig.SQLitePath)); err != nil {
			a.failStartup("failed to create local storage folder", err)
			return
		}
	}
	store, err := storage.NewConfiguredStore(storeConfig)
	if err != nil {
		a.failStartup("failed to initialize local storage", err)
		return
	}
	if privateSQLite {
		if err := ensurePrivateFile(storeConfig.SQLitePath); err != nil {
			_ = store.Close()
			a.failStartup("failed to protect local storage", err)
			return
		}
	}
	if err := hardenPrivateStateFiles(appDataPath); err != nil {
		_ = store.Close()
		a.failStartup("failed to protect desktop state", err)
		return
	}
	// Shutdown may begin while the storage constructor or migration is running.
	// Publish the store only while holding the same lifecycle lock that prevents
	// a late startup from resurrecting the embedded server after shutdown.
	a.lifecycleMu.Lock()
	if a.shuttingDown {
		a.lifecycleMu.Unlock()
		_ = store.Close()
		return
	}
	a.store = store
	a.lifecycleMu.Unlock()

	// Restore the persisted route/window state before publishing the API server
	// to the renderer. The route persistence hook starts after GetAPIOrigin and
	// must never overwrite an as-yet-unrestored route with its default hash.
	a.restoreWindowState()

	// Initialize the API Server
	capability := a.GetAPICapability()
	server := api.NewServer(api.ServerConfig{
		Addr:               "127.0.0.1:0",
		MaxConcurrent:      10,
		Store:              store,
		LibraryCredentials: api.NewKeyringLibraryCredentialStore(),
		AllowedOrigins:     desktopAllowedOrigins(),
		DesktopMode:        true,
		DesktopCapability:  capability,
		DesktopAppDataPath: appDataPath,
		DesktopLogPath:     logPath,
		ProxyAddr:          "127.0.0.1:0",
		ProxyAuthToken:     a.recorderProxyToken,
		RecorderCACertPath: filepath.Join(recorderStatePath, "ca-cert.pem"),
		RecorderCAKeyPath:  filepath.Join(recorderStatePath, "ca-key.pem"),
	})
	if !a.startAPIServer(server) {
		a.lifecycleMu.Lock()
		if a.store == store {
			a.store = nil
		}
		a.lifecycleMu.Unlock()
		_ = store.Close()
		return
	}

	// Prepare native notifications (best-effort; stays unavailable on failure).
	a.initNotifications()

	// Populate the native "Open Recent" submenu now that the app data path is
	// resolved (the menu built in main.go was created before recents loaded).
	// Linux keeps its initial Wails menu static; recents remain in the dashboard.
	a.rebuildMenu()
}

// beginStartup/finishStartup delimit the complete Wails OnStartup callback.
// Shutdown waits for this boundary before inspecting server/store ownership,
// and startAPIServer checks the same shutdown flag. Together those guarantees
// prevent a slow storage initialization from publishing a server after the
// shutdown path has already joined what it believed was an empty WaitGroup.
func (a *App) beginStartup() bool {
	a.lifecycleMu.Lock()
	defer a.lifecycleMu.Unlock()
	if a.startupStarted || a.shuttingDown {
		return false
	}
	a.startupStarted = true
	return true
}

func (a *App) finishStartup() {
	a.startupOnce.Do(func() {
		if a.startupDone != nil {
			close(a.startupDone)
		}
	})
}

// startAPIServer owns the one Server.Start goroutine and publishes its terminal
// result to every waiter. Server.Start blocks while serving, so readiness is
// observed independently through BoundAddrReady.
func (a *App) startAPIServer(server desktopAPIServer) bool {
	done := make(chan struct{})
	a.lifecycleMu.Lock()
	if a.startupErr != "" || a.shuttingDown {
		a.lifecycleMu.Unlock()
		a.publishInitialisation()
		return false
	}
	// Add before publishing a.server/a.serverDone. shutdown observes those
	// fields under lifecycleMu, so it can never call Wait while the counter is
	// still zero and then race a later Add.
	a.serverWG.Add(1)
	a.server = server
	a.serverDone = done
	a.serverExitErr = nil
	a.lifecycleMu.Unlock()
	a.publishInitialisation()

	go func() {
		defer a.serverWG.Done()
		err := server.Start()
		a.lifecycleMu.Lock()
		a.serverExitErr = err
		shuttingDown := a.shuttingDown
		close(done)
		a.lifecycleMu.Unlock()

		if shuttingDown {
			return
		}
		if err == nil {
			err = fmt.Errorf("embedded API server stopped unexpectedly")
		}
		a.failStartup("embedded API server failed", err)
	}()
	return true
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
		w := s.Size.Width
		if w == 0 {
			w = s.Width
		}
		h := s.Size.Height
		if h == 0 {
			h = s.Height
		}
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
// Returning false permits the close on macOS, Linux, and Windows.
func (a *App) beforeClose(ctx context.Context) bool {
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

// failStartup logs a fatal startup failure and records a user-facing message
// the dashboard can show via GetStartupError.
func (a *App) failStartup(msg string, err error) {
	slog.Error(msg, "err", err)
	a.lifecycleMu.Lock()
	// Preserve the earliest fatal error; later rollback errors are useful in the
	// log but should not replace the actionable root cause shown in the UI.
	if a.startupErr == "" {
		if err != nil {
			a.startupErr = fmt.Sprintf("%s: %v", msg, err)
		} else {
			a.startupErr = msg
		}
	}
	a.lifecycleMu.Unlock()
	a.publishInitialisation()
}

func (a *App) publishInitialisation() {
	a.initialiseOnce.Do(func() {
		if a.initialised != nil {
			close(a.initialised)
		}
	})
}

// GetStartupError returns a non-empty message when the embedded controller
// failed to start (storage/server init). Surfaced in the dashboard so the user
// sees why local features are unavailable instead of a silent failure.
func (a *App) GetStartupError() string {
	a.lifecycleMu.RLock()
	defer a.lifecycleMu.RUnlock()
	return a.startupErr
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
	base := ctx
	if base == nil {
		base = context.Background()
	}
	timeout := a.shutdownWait
	if timeout <= 0 {
		timeout = defaultShutdownTimeout
	}
	shutdownCtx, cancel := context.WithTimeout(base, timeout)
	defer cancel()

	// Announce shutdown before doing any other work. In particular,
	// startAPIServer must never clear this flag and publish a new server after a
	// shutdown that raced slow storage initialization.
	a.lifecycleMu.Lock()
	a.shuttingDown = true
	startupStarted := a.startupStarted
	startupDone := a.startupDone
	a.lifecycleMu.Unlock()
	a.notificationClosing.Store(true)
	a.publishInitialisation()

	startupJoined := !startupStarted
	if startupStarted && startupDone != nil {
		select {
		case <-startupDone:
			startupJoined = true
		case <-shutdownCtx.Done():
			slog.Warn("desktop startup did not finish before the shutdown deadline")
		}
	}

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
	a.cleanupNotifications(shutdownCtx)

	a.lifecycleMu.RLock()
	server := a.server
	store := a.store
	a.lifecycleMu.RUnlock()

	joined := server == nil && startupJoined
	if server != nil {
		shutdownResult := make(chan error, 1)
		go func() { shutdownResult <- server.Shutdown(shutdownCtx) }()
		var shutdownErr error
		shutdownReturned := false
		select {
		case shutdownErr = <-shutdownResult:
			shutdownReturned = true
		case <-shutdownCtx.Done():
			shutdownErr = shutdownCtx.Err()
		}
		if shutdownErr != nil {
			slog.Warn("embedded API server shutdown was incomplete", "err", shutdownErr)
		}
		serverJoined := a.waitForServerExit(shutdownCtx)
		joined = shutdownReturned && shutdownErr == nil && serverJoined
		if !serverJoined {
			slog.Warn("embedded API server did not stop before the shutdown deadline")
		}
	}
	// The server and its background workers own the store. Never close it while
	// that goroutine may still be using it; process exit will reclaim it if the
	// bounded shutdown deadline was exhausted.
	if store != nil && joined {
		if err := store.Close(); err != nil {
			slog.Warn("failed to close desktop storage", "err", err)
		}
		a.lifecycleMu.Lock()
		if a.store == store {
			a.store = nil
		}
		a.lifecycleMu.Unlock()
	}
	if joined {
		a.lifecycleMu.Lock()
		instanceLock := a.instanceLock
		a.instanceLock = nil
		a.lifecycleMu.Unlock()
		if instanceLock != nil {
			if err := instanceLock.Close(); err != nil {
				slog.Warn("failed to release desktop process lock", "err", err)
			}
		}
	}
}

func (a *App) waitForServerExit(ctx context.Context) bool {
	done := make(chan struct{})
	go func() {
		a.serverWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
	}
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

func (a *App) GetAPIOrigin() (string, error) {
	a.lifecycleMu.RLock()
	server := a.server
	done := a.serverDone
	startupErr := a.startupErr
	shuttingDown := a.shuttingDown
	startupStarted := a.startupStarted
	startupDone := a.startupDone
	wait := a.apiReadyWait
	initialised := a.initialised
	a.lifecycleMu.RUnlock()
	if wait <= 0 {
		wait = defaultAPIReadyTimeout
	}
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	if server == nil && startupErr == "" && initialised != nil {
		select {
		case <-initialised:
			a.lifecycleMu.RLock()
			server = a.server
			done = a.serverDone
			startupErr = a.startupErr
			shuttingDown = a.shuttingDown
			startupStarted = a.startupStarted
			startupDone = a.startupDone
			a.lifecycleMu.RUnlock()
		case <-deadline.C:
			err := fmt.Errorf("desktop runtime initialization did not complete within %s", wait)
			a.failStartup("desktop runtime initialization timed out", err)
			return "", err
		}
	}
	// Binding the random loopback port is only part of desktop readiness. Wait
	// for the complete OnStartup callback so restoreWindowState has published
	// the saved route before the renderer can read and persist route state.
	if startupStarted && startupDone != nil {
		select {
		case <-startupDone:
		case <-deadline.C:
			err := fmt.Errorf("desktop runtime startup did not complete within %s", wait)
			a.failStartup("desktop runtime startup timed out", err)
			if server != nil {
				a.abortServerStartup(server)
			}
			return "", err
		}
		a.lifecycleMu.RLock()
		server = a.server
		done = a.serverDone
		startupErr = a.startupErr
		shuttingDown = a.shuttingDown
		a.lifecycleMu.RUnlock()
	}
	if server == nil {
		if startupErr != "" {
			return "", fmt.Errorf("%s", startupErr)
		}
		if shuttingDown {
			return "", fmt.Errorf("desktop application is shutting down")
		}
		return "", fmt.Errorf("embedded API server is not initialized")
	}
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		// A server that bound successfully can still have exited before the bridge
		// asks for its origin. Prefer the terminal result over a stale readiness
		// flag, and recheck after reading/validating the address below.
		select {
		case <-done:
			return "", a.apiServerStoppedError()
		default:
		}
		if server.BoundAddrReady() {
			origin := server.BrowserURL()
			if err := validateLoopbackAPIOrigin(origin); err != nil {
				a.failStartup("embedded API server returned an invalid address", err)
				return "", err
			}
			select {
			case <-done:
				return "", a.apiServerStoppedError()
			default:
			}
			return origin, nil
		}
		select {
		case <-done:
			return "", a.apiServerStoppedError()
		case <-deadline.C:
			err := fmt.Errorf("embedded API server did not become ready within %s", wait)
			a.failStartup("embedded API server startup timed out", err)
			a.abortServerStartup(server)
			return "", err
		case <-ticker.C:
		}
	}
}

func (a *App) apiServerStoppedError() error {
	a.lifecycleMu.RLock()
	err := a.serverExitErr
	startupErr := a.startupErr
	shuttingDown := a.shuttingDown
	a.lifecycleMu.RUnlock()
	if startupErr != "" {
		return fmt.Errorf("%s", startupErr)
	}
	if err != nil {
		return fmt.Errorf("embedded API server failed: %w", err)
	}
	if shuttingDown {
		return fmt.Errorf("desktop application is shutting down")
	}
	return fmt.Errorf("embedded API server stopped before becoming ready")
}

func (a *App) abortServerStartup(server desktopAPIServer) {
	a.lifecycleMu.Lock()
	a.shuttingDown = true
	a.lifecycleMu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), defaultShutdownTimeout)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			slog.Warn("failed to stop timed-out embedded API server", "err", err)
		}
	}()
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
