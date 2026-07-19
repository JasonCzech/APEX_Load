package main

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

var errTest = errors.New("boom")

// TestWailsWebviewOriginsCoverPlatforms guards the allow-list passed to the
// embedded API server. The dashboard runs inside the Wails webview and makes
// cross-origin requests to the loopback API; if these origins drift the CORS
// preflight is rejected and the dashboard reports "Authentication check failed".
func TestWailsWebviewOriginsCoverPlatforms(t *testing.T) {
	want := map[string]bool{
		"wails://wails":          false, // macOS (WKWebView) — confirmed location.origin
		"http://wails.localhost": false, // Windows (WebView2) / Linux (WebKitGTK)
		"http://localhost:3000":  false, // Wails dev Vite origin
		"http://127.0.0.1:3000":  false, // Wails dev explicit loopback origin
	}
	for _, origin := range wailsWebviewOrigins {
		if _, ok := want[origin]; ok {
			want[origin] = true
		}
	}
	for origin, seen := range want {
		if !seen {
			t.Errorf("wailsWebviewOrigins missing required origin %q", origin)
		}
	}
}

func TestDesktopAllowedOriginsIncludesValidCustomDevPort(t *testing.T) {
	t.Setenv("PORT", "4317")
	origins := desktopAllowedOrigins()
	for _, want := range []string{"http://localhost:4317", "http://127.0.0.1:4317"} {
		found := false
		for _, origin := range origins {
			if origin == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("desktopAllowedOrigins missing %q: %#v", want, origins)
		}
	}
}

func TestDesktopAllowedOriginsRejectsInvalidCustomDevPort(t *testing.T) {
	for _, value := range []string{"not-a-port", "0", "65536"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("PORT", value)
			if got, want := desktopAllowedOrigins(), wailsWebviewOrigins; !reflect.DeepEqual(got, want) {
				t.Fatalf("desktopAllowedOrigins(%q) = %#v, want %#v", value, got, want)
			}
		})
	}
}

func TestGetAPIOriginWithoutServer(t *testing.T) {
	app := NewApp()
	app.apiReadyWait = 20 * time.Millisecond
	if got, err := app.GetAPIOrigin(); err == nil || got != "" {
		t.Fatalf("GetAPIOrigin with no server = %q, %v; want empty plus error", got, err)
	}
}

func TestGetAPIOriginWaitsForStartupPublication(t *testing.T) {
	app := NewApp()
	app.apiReadyWait = time.Second
	type result struct {
		origin string
		err    error
	}
	resultCh := make(chan result, 1)
	go func() {
		origin, err := app.GetAPIOrigin()
		resultCh <- result{origin: origin, err: err}
	}()
	select {
	case got := <-resultCh:
		t.Fatalf("GetAPIOrigin returned before startup published state: %+v", got)
	case <-time.After(20 * time.Millisecond):
	}

	server := &fakeDesktopServer{
		ready:      true,
		origin:     "http://127.0.0.1:43124",
		startBlock: make(chan struct{}),
	}
	app.startAPIServer(server)
	select {
	case got := <-resultCh:
		if got.err != nil || got.origin != server.origin {
			t.Fatalf("GetAPIOrigin = %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("GetAPIOrigin did not wake after startup publication")
	}
	app.shutdownWait = time.Second
	app.shutdown(context.Background())
}

func TestGetAPIOriginWaitsForRouteRestoreBoundary(t *testing.T) {
	app := NewApp()
	app.apiReadyWait = time.Second
	if !app.beginStartup() {
		t.Fatal("beginStartup unexpectedly refused")
	}
	server := &fakeDesktopServer{
		ready:      true,
		origin:     "http://127.0.0.1:43125",
		startBlock: make(chan struct{}),
	}
	if !app.startAPIServer(server) {
		t.Fatal("startAPIServer unexpectedly refused")
	}
	type result struct {
		origin string
		err    error
	}
	resultCh := make(chan result, 1)
	go func() {
		origin, err := app.GetAPIOrigin()
		resultCh <- result{origin: origin, err: err}
	}()
	select {
	case got := <-resultCh:
		t.Fatalf("API became ready before route restore completed: %+v", got)
	case <-time.After(20 * time.Millisecond):
	}
	app.SetLastRoute("#/restored")
	app.finishStartup()
	select {
	case got := <-resultCh:
		if got.err != nil || got.origin != server.origin {
			t.Fatalf("GetAPIOrigin = %+v", got)
		}
		if route := app.GetLastRoute(); route != "#/restored" {
			t.Fatalf("restored route = %q", route)
		}
	case <-time.After(time.Second):
		t.Fatal("GetAPIOrigin did not wake after startup completed")
	}
	app.shutdownWait = time.Second
	app.shutdown(context.Background())
}

func TestGetStartupErrorDefaultEmpty(t *testing.T) {
	app := NewApp()
	if got := app.GetStartupError(); got != "" {
		t.Fatalf("GetStartupError on fresh app = %q, want empty", got)
	}
}

func TestFailStartupRecordsMessage(t *testing.T) {
	app := NewApp()
	app.failStartup("failed to initialize local storage", errTest)
	got := app.GetStartupError()
	if !strings.Contains(got, "failed to initialize local storage") {
		t.Errorf("startup error %q should contain the failure context", got)
	}
}

func TestLastRouteRoundTrip(t *testing.T) {
	app := NewApp()
	if got := app.GetLastRoute(); got != "" {
		t.Fatalf("GetLastRoute on fresh app = %q, want empty", got)
	}
	app.SetLastRoute("  #/scripts  ")
	if got := app.GetLastRoute(); got != "#/scripts" {
		t.Fatalf("GetLastRoute = %q, want trimmed %q", got, "#/scripts")
	}
}

func TestBeforeCloseCachesLiveWindowStateWithoutPreventingClose(t *testing.T) {
	app := NewApp()
	app.markWindowStateRestored()
	want := windowState{
		Width: 1280, Height: 800, X: 120, Y: 90,
		LastRoute: "#/scripts/s1",
	}
	app.SetLastRoute(want.LastRoute)
	captures := 0
	app.windowStateCapture = func(context.Context) (windowState, bool) {
		captures++
		return want, true
	}

	if prevent := app.beforeClose(context.Background()); prevent {
		t.Fatal("beforeClose prevented an ordinary application close")
	}
	got, ok := app.cachedClosingWindowState()
	if !ok || got != want {
		t.Fatalf("cached closing state = %+v, %v; want %+v, true", got, ok, want)
	}
	if captures != 1 {
		t.Fatalf("window capture count = %d, want 1", captures)
	}
}

func TestLateRouteUpdateMergesIntoClosingWindowSnapshot(t *testing.T) {
	app := NewApp()
	app.markWindowStateRestored()
	app.SetLastRoute("#/results/previous")
	captureStarted := make(chan struct{})
	releaseCapture := make(chan struct{})
	app.windowStateCapture = func(context.Context) (windowState, bool) {
		close(captureStarted)
		<-releaseCapture
		return windowState{
			Width: 1280, Height: 800, X: 120, Y: 90,
			LastRoute: "#/results/previous",
		}, true
	}

	closeDone := make(chan struct{})
	go func() {
		app.beforeClose(context.Background())
		close(closeDone)
	}()
	select {
	case <-captureStarted:
	case <-time.After(time.Second):
		t.Fatal("window-state capture did not start")
	}
	app.SetLastRoute("  #/results/latest  ")
	close(releaseCapture)
	select {
	case <-closeDone:
	case <-time.After(time.Second):
		t.Fatal("beforeClose did not finish")
	}

	got, ok := app.cachedClosingWindowState()
	if !ok || got.LastRoute != "#/results/latest" {
		t.Fatalf("cached route after racing update = %q, %v; want %q, true", got.LastRoute, ok, "#/results/latest")
	}
	app.SetLastRoute("#/results/after-close")
	got, ok = app.cachedClosingWindowState()
	if !ok || got.LastRoute != "#/results/after-close" {
		t.Fatalf("cached route after close snapshot = %q, %v; want %q, true", got.LastRoute, ok, "#/results/after-close")
	}
}

func TestRuntimeContextAndStartupPathsAreSafeToPublishConcurrently(t *testing.T) {
	app := NewApp()
	contexts := []context.Context{context.Background(), context.TODO()}
	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func(offset int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				app.setRuntimeContext(contexts[(i+offset)%len(contexts)])
				app.publishAppDataPath(filepath.Join("/tmp", "apexload-data"))
				app.publishLogPath(filepath.Join("/tmp", "apexload-logs"))
				_ = app.runtimeContext()
				_ = app.appDataDirectory()
				_ = app.logDirectory()
			}
		}(worker)
	}
	wg.Wait()
	if app.runtimeContext() == nil || app.appDataDirectory() == "" || app.logDirectory() == "" {
		t.Fatal("published startup state was lost")
	}
}

func TestCaptureFirstLaunchMaximisedWindowUsesNormalDefaults(t *testing.T) {
	app := NewApp()
	app.appDataPath = t.TempDir()
	app.SetLastRoute("#/dashboard")
	app.nativeWindowCapture = func(context.Context) (nativeWindowGeometry, bool) {
		return nativeWindowGeometry{Width: 2560, Height: 1440, Maximised: true}, true
	}

	got, ok := app.captureWindowState(context.Background())
	want := windowState{
		Width: defaultWindowWidth, Height: defaultWindowHeight,
		Maximised: true, LastRoute: "#/dashboard",
	}
	if !ok || got != want {
		t.Fatalf("first-launch maximized snapshot = %+v, %v; want %+v, true", got, ok, want)
	}
}

func TestCaptureMaximisedWindowUsesCurrentNativeRestoreBounds(t *testing.T) {
	dir := t.TempDir()
	prior := windowState{Width: 1100, Height: 700, X: 10, Y: 20}
	if err := saveWindowState(dir, prior); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	app.appDataPath = dir
	app.SetLastRoute("#/results/current")
	app.nativeWindowCapture = func(context.Context) (nativeWindowGeometry, bool) {
		return nativeWindowGeometry{
			Width: 1500, Height: 900, X: 140, Y: 80,
			Maximised: true, HasRestoreBounds: true,
		}, true
	}

	got, ok := app.captureWindowState(context.Background())
	want := windowState{
		Width: 1500, Height: 900, X: 140, Y: 80,
		Maximised: true, LastRoute: "#/results/current",
	}
	if !ok || got != want {
		t.Fatalf("maximized native restore bounds = %+v, %v; want %+v, true", got, ok, want)
	}
}

func TestShutdownPersistsOnlyThePreCloseWindowSnapshot(t *testing.T) {
	dir := t.TempDir()
	app := NewApp()
	app.appDataPath = dir
	want := windowState{
		Width: 1440, Height: 900, X: 200, Y: 100,
		Maximised: true, LastRoute: "#/results/run-1",
	}
	app.SetLastRoute(want.LastRoute)
	captures := 0
	app.windowStateCapture = func(context.Context) (windowState, bool) {
		captures++
		return want, true
	}
	app.markWindowStateRestored()
	app.beforeClose(context.Background())
	// If shutdown regresses to reading the already-destroyed native window, this
	// replacement seam makes the test fail without calling any Wails runtime.
	app.windowStateCapture = func(context.Context) (windowState, bool) {
		t.Fatal("shutdown attempted to recapture destroyed native window state")
		return windowState{}, false
	}

	app.shutdown(context.Background())
	got, ok := loadWindowState(dir)
	if !ok || got != want {
		t.Fatalf("persisted closing state = %+v, %v; want %+v, true", got, ok, want)
	}
	if captures != 1 {
		t.Fatalf("window capture count = %d, want 1", captures)
	}
}

func TestShutdownWithoutPreCloseSnapshotPreservesPriorWindowState(t *testing.T) {
	dir := t.TempDir()
	prior := windowState{Width: 1200, Height: 760, X: 75, Y: 40, LastRoute: "#/tests"}
	if err := saveWindowState(dir, prior); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	app.appDataPath = dir
	app.windowStateCapture = func(context.Context) (windowState, bool) {
		return windowState{}, false
	}
	app.markWindowStateRestored()
	app.beforeClose(context.Background())

	app.shutdown(context.Background())
	got, ok := loadWindowState(dir)
	if !ok || got != prior {
		t.Fatalf("shutdown without snapshot changed prior state: %+v, %v", got, ok)
	}
}

func TestCloseBeforeWindowRestorePreservesPriorSessionState(t *testing.T) {
	dir := t.TempDir()
	prior := windowState{Width: 1360, Height: 820, X: 90, Y: 45, LastRoute: "#/results/prior"}
	if err := saveWindowState(dir, prior); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	app.appDataPath = dir
	captures := 0
	app.windowStateCapture = func(context.Context) (windowState, bool) {
		captures++
		return windowState{Width: defaultWindowWidth, Height: defaultWindowHeight}, true
	}

	if prevent := app.beforeClose(context.Background()); prevent {
		t.Fatal("quick close was unexpectedly prevented")
	}
	if captures != 0 {
		t.Fatalf("window was captured %d time(s) before restore completed", captures)
	}
	if _, ok := app.cachedClosingWindowState(); ok {
		t.Fatal("quick close cached pre-restore default window state")
	}
	app.shutdown(context.Background())

	got, ok := loadWindowState(dir)
	if !ok || got != prior {
		t.Fatalf("quick close changed prior state: %+v, %v; want %+v, true", got, ok, prior)
	}
}

type fakeDesktopServer struct {
	mu             sync.Mutex
	ready          bool
	origin         string
	startErr       error
	startBlock     chan struct{}
	shutdownBlock  chan struct{}
	shutdownErr    error
	shutdown       sync.Once
	browserURLErr  error
	browserMode    string
	browserProject string
}

func (s *fakeDesktopServer) Start() error {
	if s.startBlock != nil {
		<-s.startBlock
	}
	return s.startErr
}

func (s *fakeDesktopServer) Shutdown(context.Context) error {
	if s.shutdownBlock != nil {
		<-s.shutdownBlock
	}
	s.shutdown.Do(func() {
		if s.startBlock != nil {
			close(s.startBlock)
		}
	})
	return s.shutdownErr
}

func TestShutdownIsBoundedWhenServerIgnoresContext(t *testing.T) {
	app := NewApp()
	server := &fakeDesktopServer{
		startBlock:    make(chan struct{}),
		shutdownBlock: make(chan struct{}),
	}
	app.startAPIServer(server)
	app.shutdownWait = 20 * time.Millisecond
	started := time.Now()
	app.shutdown(context.Background())
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("shutdown exceeded its bound: %s", elapsed)
	}
	close(server.shutdownBlock)
	select {
	case <-app.serverDone:
	case <-time.After(time.Second):
		t.Fatal("server did not exit after releasing blocked shutdown")
	}
}

func (s *fakeDesktopServer) BoundAddrReady() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ready
}

func (s *fakeDesktopServer) BoundAddr() string  { return strings.TrimPrefix(s.origin, "http://") }
func (s *fakeDesktopServer) BrowserURL() string { return s.origin }
func (s *fakeDesktopServer) NewDesktopBrowserURL(mode, projectID string) (string, error) {
	s.mu.Lock()
	s.browserMode = mode
	s.browserProject = projectID
	err := s.browserURLErr
	s.mu.Unlock()
	if err != nil {
		return "", err
	}
	return s.origin + "/desktop/bootstrap", nil
}

func TestOpenInBrowserTransfersRendererPreviewContext(t *testing.T) {
	app := NewApp()
	app.ctx = context.Background()
	server := &fakeDesktopServer{
		ready: true, origin: "http://127.0.0.1:43123", browserURLErr: errors.New("stop before runtime launch"),
	}
	app.server = server
	if err := app.OpenInBrowser("user", "project-b"); err == nil || !strings.Contains(err.Error(), "stop before runtime launch") {
		t.Fatalf("OpenInBrowser error = %v", err)
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.browserMode != "user" || server.browserProject != "project-b" {
		t.Fatalf("browser preview = (%q, %q), want (user, project-b)", server.browserMode, server.browserProject)
	}
}

func TestGetAPIOriginReturnsOnlyReadyLoopbackAddress(t *testing.T) {
	app := NewApp()
	app.server = &fakeDesktopServer{ready: true, origin: "http://127.0.0.1:43123"}
	got, err := app.GetAPIOrigin()
	if err != nil || got != "http://127.0.0.1:43123" {
		t.Fatalf("GetAPIOrigin = %q, %v", got, err)
	}

	app = NewApp()
	app.server = &fakeDesktopServer{ready: true, origin: "http://127.0.0.1:0"}
	if got, err := app.GetAPIOrigin(); err == nil || got != "" {
		t.Fatalf("invalid :0 origin = %q, %v; want rejection", got, err)
	}

	app = NewApp()
	app.server = &fakeDesktopServer{ready: true, origin: "http://192.0.2.1:8080"}
	if _, err := app.GetAPIOrigin(); err == nil {
		t.Fatal("non-loopback API origin should be rejected")
	}
}

func TestGetAPIOriginPropagatesStartFailure(t *testing.T) {
	app := NewApp()
	server := &fakeDesktopServer{startErr: errors.New("bind denied")}
	app.startAPIServer(server)
	if got, err := app.GetAPIOrigin(); err == nil || got != "" || !strings.Contains(err.Error(), "bind denied") {
		t.Fatalf("GetAPIOrigin after start failure = %q, %v", got, err)
	}
	app.serverWG.Wait()
	if got := app.GetStartupError(); !strings.Contains(got, "bind denied") {
		t.Fatalf("startup error = %q, want bind failure", got)
	}
}

func TestGetAPIOriginRejectsStaleReadyAddressAfterServerExit(t *testing.T) {
	app := NewApp()
	server := &fakeDesktopServer{
		ready: true, origin: "http://127.0.0.1:43123", startErr: errors.New("serve failed after bind"),
	}
	app.startAPIServer(server)
	select {
	case <-app.serverDone:
	case <-time.After(time.Second):
		t.Fatal("server did not publish its terminal result")
	}

	got, err := app.GetAPIOrigin()
	if err == nil || got != "" || !strings.Contains(err.Error(), "serve failed after bind") {
		t.Fatalf("GetAPIOrigin after ready server exit = %q, %v", got, err)
	}
}

func TestAppendDesktopBrowserRoute(t *testing.T) {
	const bootstrapURL = "http://127.0.0.1:43123/desktop/bootstrap?nonce=secret"
	longRoute := "#/" + strings.Repeat("a", 2048)
	tests := []struct {
		name  string
		base  string
		route string
		want  string
		fail  bool
	}{
		{name: "settings", base: bootstrapURL, route: "#/settings", want: bootstrapURL + "#/settings"},
		{name: "query in fragment", base: bootstrapURL, route: "#/settings?tab=github", want: bootstrapURL + "#/settings?tab=github"},
		{name: "empty", base: bootstrapURL, route: "", want: bootstrapURL},
		{name: "external URL", base: bootstrapURL, route: "https://evil.example", want: bootstrapURL},
		{name: "scheme relative fragment", base: bootstrapURL, route: "#//evil.example", want: bootstrapURL},
		{name: "control character", base: bootstrapURL, route: "#/settings\nattack", want: bootstrapURL},
		{name: "backslash", base: bootstrapURL, route: "#/settings\\attack", want: bootstrapURL},
		{name: "oversized", base: bootstrapURL, route: longRoute, want: bootstrapURL},
		{name: "remote base", base: "https://evil.example/desktop/bootstrap", route: "#/settings", fail: true},
		{name: "unbound base", base: "http://127.0.0.1:0/desktop/bootstrap", route: "#/settings", fail: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := appendDesktopBrowserRoute(test.base, test.route)
			if test.fail {
				if err == nil {
					t.Fatalf("appendDesktopBrowserRoute(%q, %q) = %q, want error", test.base, test.route, got)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("appendDesktopBrowserRoute(%q, %q) = %q, %v; want %q", test.base, test.route, got, err, test.want)
			}
		})
	}
}

func TestGetAPIOriginTimeoutStopsUnreadyServer(t *testing.T) {
	app := NewApp()
	app.apiReadyWait = 20 * time.Millisecond
	server := &fakeDesktopServer{
		origin:     "http://127.0.0.1:0",
		startBlock: make(chan struct{}),
	}
	app.startAPIServer(server)
	if _, err := app.GetAPIOrigin(); err == nil || !strings.Contains(err.Error(), "did not become ready") {
		t.Fatalf("unready server should time out, got %v", err)
	}
	select {
	case <-app.serverDone:
	case <-time.After(time.Second):
		t.Fatal("timed-out server was not stopped")
	}
	if got := app.GetStartupError(); !strings.Contains(got, "timed out") {
		t.Fatalf("startup error = %q", got)
	}
}

func TestShutdownJoinsServerStartGoroutine(t *testing.T) {
	app := NewApp()
	server := &fakeDesktopServer{startBlock: make(chan struct{})}
	app.startAPIServer(server)
	app.shutdownWait = time.Second
	app.shutdown(context.Background())
	select {
	case <-app.serverDone:
	default:
		t.Fatal("shutdown returned before Server.Start exited")
	}
	if got := app.GetStartupError(); got != "" {
		t.Fatalf("orderly shutdown recorded a startup error: %q", got)
	}
}

func TestShutdownDoesNotCloseStoreWhenServerShutdownFails(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	app := NewApp()
	app.store = store
	server := &fakeDesktopServer{shutdownErr: errors.New("background workers still active")}
	app.startAPIServer(server)
	app.shutdownWait = time.Second
	app.shutdown(context.Background())
	if app.store != store {
		t.Fatal("store was cleared after an incomplete server shutdown")
	}
	if err := store.SaveSetting("shutdown-test", "usable"); err != nil {
		t.Fatalf("store was closed after an incomplete server shutdown: %v", err)
	}
}

func TestShutdownClosesStoreAfterSuccessfulServerJoin(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	app.store = store
	app.startAPIServer(&fakeDesktopServer{})
	app.shutdownWait = time.Second
	app.shutdown(context.Background())
	if app.store != nil {
		t.Fatal("store was not cleared after a complete server shutdown")
	}
	if err := store.SaveSetting("shutdown-test", "closed"); err == nil {
		t.Fatal("store remained usable after a complete server shutdown")
	}
}

func TestShutdownWaitsForCompleteStartupAndPreventsLateServer(t *testing.T) {
	app := NewApp()
	app.shutdownWait = time.Second
	if !app.beginStartup() {
		t.Fatal("beginStartup unexpectedly refused")
	}

	shutdownDone := make(chan struct{})
	go func() {
		app.shutdown(context.Background())
		close(shutdownDone)
	}()
	deadline := time.Now().Add(time.Second)
	for {
		app.lifecycleMu.RLock()
		shuttingDown := app.shuttingDown
		app.lifecycleMu.RUnlock()
		if shuttingDown {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("shutdown did not publish its state")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case <-shutdownDone:
		t.Fatal("shutdown returned before the full startup boundary completed")
	default:
	}

	server := &fakeDesktopServer{startBlock: make(chan struct{})}
	if app.startAPIServer(server) {
		t.Fatal("server was published after shutdown began")
	}
	app.finishStartup()
	select {
	case <-shutdownDone:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not finish after startup completed")
	}
	app.lifecycleMu.RLock()
	published := app.server
	app.lifecycleMu.RUnlock()
	if published != nil {
		t.Fatal("shutdown race resurrected the embedded API server")
	}
}

func TestStartAPIServerNeverResetsShutdownState(t *testing.T) {
	app := NewApp()
	app.lifecycleMu.Lock()
	app.shuttingDown = true
	app.lifecycleMu.Unlock()
	if app.startAPIServer(&fakeDesktopServer{}) {
		t.Fatal("startAPIServer accepted a server during shutdown")
	}
	app.lifecycleMu.RLock()
	stillShuttingDown := app.shuttingDown
	app.lifecycleMu.RUnlock()
	if !stillShuttingDown {
		t.Fatal("startAPIServer cleared the shutdown state")
	}
}

func TestDesktopCapabilityIsHighEntropyAndPerLaunch(t *testing.T) {
	first := NewApp()
	second := NewApp()
	a := first.GetAPICapability()
	b := second.GetAPICapability()
	if len(a) < 40 || a == b {
		t.Fatalf("capabilities must be long and unique: %q %q", a, b)
	}
	if len(first.recorderProxyToken) < 40 || first.recorderProxyToken == a || first.recorderProxyToken == second.recorderProxyToken {
		t.Fatal("recording proxy token must be high-entropy, per-launch, and distinct from the API capability")
	}
}

func TestDesktopStoreConfigNormalizesDesktopDefaults(t *testing.T) {
	appData := t.TempDir()
	clearStorageEnv := func(t *testing.T) {
		t.Helper()
		for _, key := range []string{"APEXLOAD_STORE", "APEXLOAD_DB", "APEXLOAD_DATABASE_URL"} {
			t.Setenv(key, "")
		}
	}
	t.Run("private SQLite default", func(t *testing.T) {
		clearStorageEnv(t)
		got := desktopStoreConfig(appData)
		if got.Backend != storage.StoreBackendSQLite || got.SQLitePath != filepath.Join(appData, "apexload.db") {
			t.Fatalf("config = %+v", got)
		}
	})
	t.Run("database URL implies Postgres", func(t *testing.T) {
		clearStorageEnv(t)
		t.Setenv("APEXLOAD_DATABASE_URL", " postgres://db/apex ")
		got := desktopStoreConfig(appData)
		if got.Backend != storage.StoreBackendPostgres || got.DatabaseURL != "postgres://db/apex" {
			t.Fatalf("config = %+v", got)
		}
	})
	t.Run("PostgreSQL alias and whitespace", func(t *testing.T) {
		clearStorageEnv(t)
		t.Setenv("APEXLOAD_STORE", " PostgreSQL ")
		got := desktopStoreConfig(appData)
		if got.Backend != storage.StoreBackendPostgres {
			t.Fatalf("backend = %q", got.Backend)
		}
	})
	t.Run("relative SQLite path rooted in app data", func(t *testing.T) {
		clearStorageEnv(t)
		t.Setenv("APEXLOAD_DB", "data/custom.db")
		got := desktopStoreConfig(appData)
		if got.SQLitePath != filepath.Join(appData, "data/custom.db") {
			t.Fatalf("SQLitePath = %q", got.SQLitePath)
		}
	})
}

func TestPathWithinDirectoryRejectsSiblingPrefix(t *testing.T) {
	base := filepath.Join(t.TempDir(), "app")
	if !pathWithinDirectory(base, filepath.Join(base, "data", "apex.db")) {
		t.Fatal("nested path should be inside app data")
	}
	if pathWithinDirectory(base, base+"-other/apex.db") {
		t.Fatal("sibling sharing a string prefix must not count as app data")
	}
}
