package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestServerBackgroundLifecycleRejectsNewWorkAndWaitsForOwnedWorker(t *testing.T) {
	server := &Server{}
	started := make(chan struct{})
	release := make(chan struct{})
	if !server.launchBackground(func() {
		close(started)
		<-release
	}) {
		t.Fatal("initial background worker was rejected")
	}
	<-started
	server.beginBackgroundShutdown()
	if server.launchBackground(func() {}) {
		t.Fatal("background worker was admitted after shutdown began")
	}

	shortCtx, shortCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer shortCancel()
	if err := server.waitForBackground(shortCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waitForBackground before release = %v, want deadline exceeded", err)
	}
	close(release)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.waitForBackground(ctx); err != nil {
		t.Fatalf("waitForBackground after release: %v", err)
	}
}

func TestNewServerDoesNotStartReplayRetentionJanitors(t *testing.T) {
	server := NewServer(ServerConfig{})
	if browserRunning, scriptRunning, recordingRunning := retentionJanitorsRunning(server); browserRunning || scriptRunning || recordingRunning {
		t.Fatalf("NewServer started janitors = browser:%v script:%v recording:%v, want all false", browserRunning, scriptRunning, recordingRunning)
	}

	server.startReplayRetentionJanitors()
	server.startBrowserRecordingRetentionJanitor()
	t.Cleanup(func() {
		if server.browserReplay != nil {
			server.browserReplay.Close()
		}
		if server.scriptDebug != nil {
			server.scriptDebug.Close()
		}
		if server.browserRecording != nil {
			server.browserRecording.Close()
		}
	})

	if browserRunning, scriptRunning, recordingRunning := retentionJanitorsRunning(server); !browserRunning || !scriptRunning || !recordingRunning {
		t.Fatalf("started janitors = browser:%v script:%v recording:%v, want all true", browserRunning, scriptRunning, recordingRunning)
	}
}

func TestStartContinuesWhenRecorderProxyDisabledInProduction(t *testing.T) {
	t.Setenv("APEXLOAD_ENV", "production")
	t.Setenv("APEXLOAD_RECORDER_PROXY_ALLOW_PRODUCTION", "")

	server := NewServer(ServerConfig{Addr: "127.0.0.1:0"})
	settings := server.runner.GetSystemSettings()
	settings.Auth = AuthSettings{
		Mode:                authModeOIDC,
		IssuerURL:           "https://issuer.example.com",
		ClientID:            "client",
		ClientSecretEnvVar:  "APEXLOAD_OIDC_SECRET",
		RedirectURL:         "https://app.example.com/callback",
		SessionSecretEnvVar: "APEXLOAD_AUTH_SESSION_SECRET",
		Scopes:              []string{"openid"},
	}
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Start()
	}()

	deadline := time.After(3 * time.Second)
	for !server.BoundAddrReady() {
		select {
		case err := <-errCh:
			t.Fatalf("Start returned before binding: %v", err)
		case <-deadline:
			t.Fatal("server did not bind after recorder proxy startup failed")
		case <-time.After(10 * time.Millisecond):
		}
	}

	resp, err := http.Get(server.BrowserURL() + "/api/v1/system/health")
	if err != nil {
		t.Fatalf("health request returned error: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown returned error: %v", err)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Start returned error after shutdown: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop after shutdown")
	}
}

func TestStartBindFailureRollsBackStartedComponents(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve API address: %v", err)
	}
	defer occupied.Close()

	server := NewServer(ServerConfig{
		Addr:        occupied.Addr().String(),
		MetricsAddr: "127.0.0.1:0",
	})
	if err := server.Start(); err == nil {
		t.Fatal("Start succeeded despite occupied API address")
	}

	if server.metricsServer != nil {
		t.Fatal("failed Start retained the dedicated metrics server")
	}
	if got := server.MetricsBoundAddr(); got != "" {
		t.Fatalf("MetricsBoundAddr after failed Start = %q, want empty", got)
	}
	if server.BoundAddrReady() {
		t.Fatalf("BoundAddrReady after failed Start = true, address %q", server.BoundAddr())
	}
	if browserRunning, scriptRunning, recordingRunning := retentionJanitorsRunning(server); browserRunning || scriptRunning || recordingRunning {
		t.Fatalf("failed Start retained janitors = browser:%v script:%v recording:%v", browserRunning, scriptRunning, recordingRunning)
	}
}

func TestStartBindFailureDoesNotRaceMetricsServeShutdown(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve API address: %v", err)
	}
	defer occupied.Close()

	// A failed API bind starts and then immediately rolls back the dedicated
	// metrics listener. Repeat the boundary so the race detector exercises the
	// Serve goroutine both before and after shutdown detaches the shared field.
	for i := 0; i < 32; i++ {
		server := NewServer(ServerConfig{
			Addr:        occupied.Addr().String(),
			MetricsAddr: "127.0.0.1:0",
		})
		if err := server.Start(); err == nil {
			t.Fatalf("iteration %d: Start succeeded despite occupied API address", i)
		}
		server.metricsServerMu.Lock()
		metricsServer := server.metricsServer
		server.metricsServerMu.Unlock()
		if metricsServer != nil {
			t.Fatalf("iteration %d: failed Start retained the dedicated metrics server", i)
		}
	}
}

func TestShutdownBeforeStartPermanentlyPreventsBinding(t *testing.T) {
	server := NewServer(ServerConfig{Addr: "127.0.0.1:0"})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown before Start: %v", err)
	}
	if err := server.Start(); !errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("Start after Shutdown = %v, want http.ErrServerClosed", err)
	}
	if server.BoundAddrReady() {
		t.Fatalf("server bound after shutdown: %s", server.BoundAddr())
	}
}

func TestConcurrentStartShutdownCannotResurrectListener(t *testing.T) {
	server := NewServer(ServerConfig{Addr: "127.0.0.1:0"})
	startResult := make(chan error, 1)
	go func() { startResult <- server.Start() }()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatalf("concurrent Shutdown: %v", err)
	}
	select {
	case err := <-startResult:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("concurrent Start = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Start did not return after concurrent shutdown")
	}
	if server.BoundAddrReady() {
		t.Fatalf("listener remained bound after concurrent shutdown: %s", server.BoundAddr())
	}
}

func retentionJanitorsRunning(server *Server) (bool, bool, bool) {
	browserRunning := false
	if server != nil && server.browserReplay != nil {
		server.browserReplay.mu.RLock()
		browserRunning = server.browserReplay.janitorCancel != nil
		server.browserReplay.mu.RUnlock()
	}
	scriptRunning := false
	if server != nil && server.scriptDebug != nil {
		server.scriptDebug.mu.RLock()
		scriptRunning = server.scriptDebug.janitorCancel != nil
		server.scriptDebug.mu.RUnlock()
	}
	recordingRunning := false
	if server != nil && server.browserRecording != nil {
		server.browserRecording.mu.RLock()
		recordingRunning = server.browserRecording.janitorCancel != nil
		server.browserRecording.mu.RUnlock()
	}
	return browserRunning, scriptRunning, recordingRunning
}
