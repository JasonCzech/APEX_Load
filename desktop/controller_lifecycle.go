package main

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/api"
	"github.com/jasonczech/apexload/pkg/storage"
	"github.com/jasonczech/apexload/pkg/telemetry"
)

// desktopAPIServer is the lifecycle surface the native shell needs from the
// embedded controller. Keeping this small also makes startup and shutdown
// failure paths testable without a live Wails runtime.
type desktopAPIServer interface {
	Start() error
	StartScheduler(context.Context) error
	Shutdown(context.Context) error
	BoundAddrReady() bool
	BoundAddr() string
	BrowserURL() string
	NewDesktopBrowserURL(devViewMode, projectID string) (string, error)
}

// desktopControllerLifecycle exclusively owns publication, readiness and
// shutdown of the embedded controller, its store and its process lock. It has
// no native-window dependency; App supplies the two ordered native callbacks.
type desktopControllerLifecycle struct {
	server       desktopAPIServer
	store        storage.Store
	appDataPath  string
	logPath      string
	instanceLock desktopProcessLock
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
	// startupErr records a fatal startup failure (storage/server init). It is
	// surfaced to the dashboard via GetStartupError so the embedded controller's
	// failure can be shown in-app — the HTTP /desktop/status endpoint can't carry
	// it because a failed server isn't listening.
	startupErr string
}

type desktopControllerSetup struct {
	Capability              string
	RecorderProxyToken      string
	BeforeServerPublication func()
}
type desktopShutdownHooks struct {
	Begin            func()
	BeforeController func(context.Context, bool)
}

func newDesktopControllerLifecycle() *desktopControllerLifecycle {
	return &desktopControllerLifecycle{apiReadyWait: defaultAPIReadyTimeout, shutdownWait: defaultShutdownTimeout, initialised: make(chan struct{}), startupDone: make(chan struct{})}
}

// publishStartupPaths makes successfully-created private directories visible
// to callbacks that can race the Wails startup goroutine.
func (a *desktopControllerLifecycle) publishAppDataPath(path string) {
	a.lifecycleMu.Lock()
	a.appDataPath = path
	a.lifecycleMu.Unlock()
}

func (a *desktopControllerLifecycle) publishLogPath(path string) {
	a.lifecycleMu.Lock()
	a.logPath = path
	a.lifecycleMu.Unlock()
}

func (a *desktopControllerLifecycle) appDataDirectory() string {
	a.lifecycleMu.RLock()
	defer a.lifecycleMu.RUnlock()
	return a.appDataPath
}

func (a *desktopControllerLifecycle) logDirectory() string {
	a.lifecycleMu.RLock()
	defer a.lifecycleMu.RUnlock()
	return a.logPath
}

// startDesktopServices keeps the embedded controller's background services in
// parity with cmd/controller. In particular, schedules created from the native
// app must be driven by the same persisted scheduler as schedules created from
// the hosted dashboard. Start the scheduler before publishing the HTTP server
// so the renderer can never observe a partially initialized controller.
func (a *desktopControllerLifecycle) startDesktopServices(ctx context.Context, server desktopAPIServer) bool {
	if err := server.StartScheduler(ctx); err != nil {
		a.failStartup("failed to start scenario scheduler", err)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), defaultShutdownTimeout)
		defer cancel()
		if shutdownErr := server.Shutdown(shutdownCtx); shutdownErr != nil {
			slog.Warn("failed to clean up embedded controller after scheduler startup failure", "err", shutdownErr)
		}
		return false
	}
	if a.startAPIServer(server) {
		return true
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), defaultShutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Warn("failed to clean up unpublished embedded controller", "err", err)
	}
	return false
}

// beginStartup/finishStartup delimit the complete Wails OnStartup callback.
// Shutdown waits for this boundary before inspecting server/store ownership,
// and startAPIServer checks the same shutdown flag. Together those guarantees
// prevent a slow storage initialization from publishing a server after the
// shutdown path has already joined what it believed was an empty WaitGroup.
func (a *desktopControllerLifecycle) beginStartup() bool {
	a.lifecycleMu.Lock()
	defer a.lifecycleMu.Unlock()
	if a.startupStarted || a.shuttingDown {
		return false
	}
	a.startupStarted = true
	return true
}

func (a *desktopControllerLifecycle) finishStartup() {
	a.startupOnce.Do(func() {
		if a.startupDone != nil {
			close(a.startupDone)
		}
	})
}

// startAPIServer owns the one Server.Start goroutine and publishes its terminal
// result to every waiter. Server.Start blocks while serving, so readiness is
// observed independently through BoundAddrReady.
func (a *desktopControllerLifecycle) startAPIServer(server desktopAPIServer) bool {
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

// failStartup logs a fatal startup failure and records a user-facing message
// the dashboard can show via GetStartupError.
func (a *desktopControllerLifecycle) failStartup(msg string, err error) {
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

func (a *desktopControllerLifecycle) publishInitialisation() {
	a.initialiseOnce.Do(func() {
		if a.initialised != nil {
			close(a.initialised)
		}
	})
}

// GetStartupError returns a non-empty message when the embedded controller
// failed to start (storage/server init). Surfaced in the dashboard so the user
// sees why local features are unavailable instead of a silent failure.
func (a *desktopControllerLifecycle) GetStartupError() string {
	a.lifecycleMu.RLock()
	defer a.lifecycleMu.RUnlock()
	return a.startupErr
}

func (a *desktopControllerLifecycle) waitForServerExit(ctx context.Context) bool {
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

func (a *desktopControllerLifecycle) GetAPIOrigin() (string, error) {
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

func (a *desktopControllerLifecycle) apiServerStoppedError() error {
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

func (a *desktopControllerLifecycle) abortServerStartup(server desktopAPIServer) {
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

func (a *desktopControllerLifecycle) initializeController(ctx context.Context, setup desktopControllerSetup) bool {
	appDataPath, err := desktopAppDataPath()
	if err != nil {
		a.failStartup("failed to resolve app data path", err)
		return false
	}
	if err := ensurePrivateDirectory(appDataPath); err != nil {
		a.failStartup("failed to create app data folder", err)
		return false
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
		return false
	}
	a.lifecycleMu.Lock()
	if a.shuttingDown {
		a.lifecycleMu.Unlock()
		_ = instanceLock.Close()
		return false
	}
	a.instanceLock = instanceLock
	a.lifecycleMu.Unlock()
	logPath := filepath.Join(appDataPath, "logs")
	if err := ensurePrivateDirectory(logPath); err != nil {
		a.failStartup("failed to create log folder", err)
		return false
	}
	a.publishLogPath(logPath)
	recorderStatePath := filepath.Join(appDataPath, "recorder")
	if err := ensurePrivateDirectory(recorderStatePath); err != nil {
		a.failStartup("failed to create recording proxy state folder", err)
		return false
	}

	// Initialize the database storage
	storeConfig := desktopStoreConfig(appDataPath)
	privateSQLite := storeConfig.Backend == storage.StoreBackendSQLite && pathWithinDirectory(appDataPath, storeConfig.SQLitePath)
	if privateSQLite {
		if err := ensurePrivateDirectory(filepath.Dir(storeConfig.SQLitePath)); err != nil {
			a.failStartup("failed to create local storage folder", err)
			return false
		}
	}
	store, err := storage.NewConfiguredStore(storeConfig)
	if err != nil {
		a.failStartup("failed to initialize local storage", err)
		return false
	}
	if privateSQLite {
		if err := ensurePrivateFile(storeConfig.SQLitePath); err != nil {
			_ = store.Close()
			a.failStartup("failed to protect local storage", err)
			return false
		}
	}
	if err := hardenPrivateStateFiles(appDataPath); err != nil {
		_ = store.Close()
		a.failStartup("failed to protect desktop state", err)
		return false
	}
	// Shutdown may begin while the storage constructor or migration is running.
	// Publish the store only while holding the same lifecycle lock that prevents
	// a late startup from resurrecting the embedded server after shutdown.
	a.lifecycleMu.Lock()
	if a.shuttingDown {
		a.lifecycleMu.Unlock()
		_ = store.Close()
		return false
	}
	a.store = store
	a.lifecycleMu.Unlock()

	// Restore the persisted route/window state before publishing the API server
	// to the renderer. The route persistence hook starts after GetAPIOrigin and
	// must never overwrite an as-yet-unrestored route with its default hash.
	if setup.BeforeServerPublication != nil {
		setup.BeforeServerPublication()
	}

	// Initialize the API Server
	capability := setup.Capability
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
		ProxyAuthToken:     setup.RecorderProxyToken,
		RecorderCACertPath: filepath.Join(recorderStatePath, "ca-cert.pem"),
		RecorderCAKeyPath:  filepath.Join(recorderStatePath, "ca-key.pem"),
		// The native desktop process intentionally remains correlation-only: it
		// can generate/retain W3C IDs but never exports local operator activity.
		TracingStatus: telemetry.Status{CorrelationOnly: true},
	})
	if !a.startDesktopServices(ctx, server) {
		a.lifecycleMu.Lock()
		if a.store == store {
			a.store = nil
		}
		a.lifecycleMu.Unlock()
		_ = store.Close()
		return false
	}

	return true
}

func (a *desktopControllerLifecycle) shutdownController(ctx context.Context, hooks desktopShutdownHooks) {
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
	if hooks.Begin != nil {
		hooks.Begin()
	}
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

	if hooks.BeforeController != nil {
		hooks.BeforeController(shutdownCtx, startupJoined)
	}

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
