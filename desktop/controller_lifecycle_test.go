package main

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// These lifecycle and race regressions moved unchanged with the owner.
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

func TestStartDesktopServicesStartsSchedulerBeforeServer(t *testing.T) {
	app := NewApp()
	server := &fakeDesktopServer{startBlock: make(chan struct{})}
	if !app.startDesktopServices(context.Background(), server) {
		t.Fatal("startDesktopServices unexpectedly failed")
	}
	deadline := time.Now().Add(time.Second)
	for {
		server.mu.Lock()
		started := append([]string(nil), server.started...)
		server.mu.Unlock()
		if len(started) == 2 {
			if want := []string{"scheduler", "server"}; !reflect.DeepEqual(started, want) {
				t.Fatalf("service startup order = %#v, want %#v", started, want)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("service startup did not complete: %#v", started)
		}
		time.Sleep(time.Millisecond)
	}
	app.shutdownWait = time.Second
	app.shutdown(context.Background())
}

func TestStartDesktopServicesRejectsSchedulerFailure(t *testing.T) {
	app := NewApp()
	server := &fakeDesktopServer{schedulerErr: errors.New("scheduler unavailable")}
	if app.startDesktopServices(context.Background(), server) {
		t.Fatal("startDesktopServices accepted a scheduler startup failure")
	}
	server.mu.Lock()
	started := append([]string(nil), server.started...)
	server.mu.Unlock()
	if want := []string{"scheduler"}; !reflect.DeepEqual(started, want) {
		t.Fatalf("services started after scheduler failure = %#v, want %#v", started, want)
	}
	if got := app.GetStartupError(); !strings.Contains(got, "scheduler unavailable") {
		t.Fatalf("startup error = %q, want scheduler failure", got)
	}
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

type lifecycleTestLock struct{ closed bool }

func (l *lifecycleTestLock) Close() error { l.closed = true; return nil }

func TestControllerLifecycleOrdersNativeCleanupBeforeStoreAndLockRelease(t *testing.T) {
	owner := newDesktopControllerLifecycle()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	owner.store = store
	lock := &lifecycleTestLock{}
	owner.instanceLock = lock
	if !owner.beginStartup() {
		t.Fatal("startup refused")
	}
	begun := make(chan struct{})
	beforeController := make(chan struct{})
	shutdownDone := make(chan struct{})
	go func() {
		owner.shutdownController(context.Background(), desktopShutdownHooks{Begin: func() { close(begun) }, BeforeController: func(_ context.Context, joined bool) {
			if !joined || lock.closed || store.SaveSetting("native-cleanup", "usable") != nil {
				t.Error("native cleanup ran after ownership release or before startup joined")
			}
			close(beforeController)
		}})
		close(shutdownDone)
	}()
	<-begun
	select {
	case <-beforeController:
		t.Fatal("native shutdown bypassed startup boundary")
	default:
	}
	if owner.startAPIServer(&fakeDesktopServer{}) {
		t.Fatal("late startup published after shutdown began")
	}
	owner.finishStartup()
	select {
	case <-shutdownDone:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not finish")
	}
	if !lock.closed || owner.instanceLock != nil || owner.store != nil {
		t.Fatal("owner did not release store and process lock")
	}
	if err := store.SaveSetting("closed", "yes"); err == nil {
		t.Fatal("released store remained open")
	}
}

func TestControllerLifecycleKeepsProcessLockWhileWorkersMayUseStore(t *testing.T) {
	owner := newDesktopControllerLifecycle()
	lock := &lifecycleTestLock{}
	owner.instanceLock = lock
	owner.startAPIServer(&fakeDesktopServer{shutdownErr: errors.New("worker cleanup incomplete")})
	owner.shutdownController(context.Background(), desktopShutdownHooks{})
	if lock.closed || owner.instanceLock != lock {
		t.Fatal("process lock released before successful worker shutdown")
	}
}
