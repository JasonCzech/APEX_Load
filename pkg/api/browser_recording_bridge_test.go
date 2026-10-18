package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/testlog"
)

const bridgeTestOrigin = "https://controller.example.test"
const bridgeTestToken = "0123456789abcdef0123456789abcdef0123456789abcdef"

func newBrowserRecorderBridgeTestServer(t *testing.T, fake *fakeBrowserRecorderBackend) *Server {
	t.Helper()
	if runtime.GOOS == "linux" {
		t.Setenv("DISPLAY", ":0")
	}
	cfg := BrowserRecorderBridgeConfig{Addr: "127.0.0.1:0", PairingToken: bridgeTestToken, PairingTTL: time.Hour, SessionTTL: time.Hour, DependencyProbe: func() error { return nil }}
	if fake != nil {
		cfg.Recording.RecorderFactory = fake.factory()
	}
	server := NewBrowserRecorderBridgeServer(cfg)
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	return server
}
func pairBridgeForTest(t *testing.T, server *Server, origin string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/bridge/pair", bytes.NewBufferString(`{"token":"`+bridgeTestToken+`"}`))
	req.Header.Set("Origin", origin)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("pair status = %d body=%s", resp.Code, resp.Body.String())
	}
	var payload struct {
		Bridge struct {
			SessionID string `json:"session_id"`
		} `json:"bridge"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal pair response: %v", err)
	}
	if payload.Bridge.SessionID == "" {
		t.Fatalf("pair response missing bridge session id: %s", resp.Body.String())
	}
	return payload.Bridge.SessionID
}

// The agent has no store and so no audit trail; recording actions there must
// not log an audit failure each time.
func TestBrowserRecorderBridgeSkipsAuditWithoutWarning(t *testing.T) {
	server := newBrowserRecorderBridgeTestServer(t, nil)
	logs := &lockedBuffer{}
	testlog.SetDefault(t, slog.New(slog.NewJSONHandler(logs, nil)))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/browser-recorder/sessions", nil)
	server.recordAuditEvent(req, "browser_recording.agent_audit_test", "success", "browser_recording", "rec-agent-audit-test", "", nil)
	server.recordAuditEventWithProjectSnapshot(req, "browser_recording.agent_audit_test", "success", "browser_recording", "rec-agent-audit-test", "", "", nil)
	if strings.Contains(logs.String(), "rec-agent-audit-test") {
		t.Fatalf("agent audit call logged: %s", logs.String())
	}
}

func TestBrowserRecorderBridgeAllowsProductionAvailability(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Setenv("DISPLAY", ":0")
	}
	t.Setenv("APEXLOAD_ENV", "production")
	t.Setenv("APEXLOAD_BROWSER_RECORDER", "")

	controller := NewServer(ServerConfig{})
	if got := controller.browserRecordingAvailability(); got.Available || !strings.Contains(got.Reason, "production") {
		t.Fatalf("controller availability = %+v, want production-disabled", got)
	}

	bridge := newBrowserRecorderBridgeTestServer(t, newFakeBrowserRecorder(nil))
	if got := bridge.browserRecordingAvailability(); !got.Available {
		t.Fatalf("bridge availability = %+v, want available", got)
	}
}
