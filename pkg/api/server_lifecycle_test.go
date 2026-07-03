package api

import (
	"context"
	"net/http"
	"testing"
	"time"
)

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
