package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"
)

const bridgeTestOrigin = "https://controller.example.test"
const bridgeTestToken = "0123456789abcdef0123456789abcdef0123456789abcdef"

func newBrowserRecorderBridgeTestServer(t *testing.T, fake *fakeBrowserRecorderBackend) *Server {
	t.Helper()
	stubBrowserRecordingDependencyProbe(t, nil)
	if runtime.GOOS == "linux" {
		t.Setenv("DISPLAY", ":0")
	}
	server := NewBrowserRecorderBridgeServer(BrowserRecorderBridgeConfig{
		Addr:         "127.0.0.1:0",
		PairingToken: bridgeTestToken,
		PairingTTL:   time.Hour,
		SessionTTL:   time.Hour,
	})
	if fake != nil {
		fake.install(server.browserRecording)
	}
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

func TestBrowserRecorderBridgePairingAndPNA(t *testing.T) {
	server := newBrowserRecorderBridgeTestServer(t, newFakeBrowserRecorder(nil))

	bad := httptest.NewRequest(http.MethodPost, "/api/v1/bridge/pair", bytes.NewBufferString(`{"token":"wrong"}`))
	bad.Header.Set("Origin", bridgeTestOrigin)
	badResp := httptest.NewRecorder()
	server.mux.ServeHTTP(badResp, bad)
	if badResp.Code != http.StatusUnauthorized {
		t.Fatalf("bad pair status = %d, want 401", badResp.Code)
	}

	sessionID := pairBridgeForTest(t, server, bridgeTestOrigin)

	preflight := httptest.NewRequest(http.MethodOptions, "/api/v1/browser-recorder/devices", nil)
	preflight.Header.Set("Origin", bridgeTestOrigin)
	preflight.Header.Set("Access-Control-Request-Private-Network", "true")
	preflightResp := httptest.NewRecorder()
	server.mux.ServeHTTP(preflightResp, preflight)
	if preflightResp.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d", preflightResp.Code)
	}
	if got := preflightResp.Header().Get("Access-Control-Allow-Origin"); got != bridgeTestOrigin {
		t.Errorf("allow-origin = %q, want %q", got, bridgeTestOrigin)
	}
	if got := preflightResp.Header().Get("Access-Control-Allow-Private-Network"); got != "true" {
		t.Errorf("allow-private-network = %q, want true", got)
	}
	if got := preflightResp.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(got, bridgeSessionHeader) {
		t.Errorf("allow-headers = %q, want bridge session header", got)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/browser-recorder/devices", nil)
	req.Header.Set("Origin", bridgeTestOrigin)
	req.Header.Set(bridgeTokenHeader, bridgeTestToken)
	req.Header.Set(bridgeSessionHeader, sessionID)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("authorized devices status = %d body=%s", resp.Code, resp.Body.String())
	}

	wsReq := httptest.NewRequest(http.MethodGet, "/api/v1/ws?"+bridgeSessionQueryParam+"="+sessionID, nil)
	wsReq.Header.Set("Origin", bridgeTestOrigin)
	if !server.recordingBridge.authorizeWebSocket(wsReq) {
		t.Fatal("paired websocket request was not authorized")
	}
}

func TestBrowserRecorderBridgeRejectsUnpairedAccess(t *testing.T) {
	server := newBrowserRecorderBridgeTestServer(t, newFakeBrowserRecorder(nil))
	sessionID := pairBridgeForTest(t, server, bridgeTestOrigin)

	missing := httptest.NewRequest(http.MethodGet, "/api/v1/browser-recorder/devices", nil)
	missing.Header.Set("Origin", bridgeTestOrigin)
	missingResp := httptest.NewRecorder()
	server.mux.ServeHTTP(missingResp, missing)
	if missingResp.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status = %d, want 401", missingResp.Code)
	}

	unpaired := httptest.NewRequest(http.MethodGet, "/api/v1/browser-recorder/devices", nil)
	unpaired.Header.Set("Origin", "https://other.example.test")
	unpaired.Header.Set(bridgeTokenHeader, bridgeTestToken)
	unpaired.Header.Set(bridgeSessionHeader, sessionID)
	unpairedResp := httptest.NewRecorder()
	server.mux.ServeHTTP(unpairedResp, unpaired)
	if unpairedResp.Code != http.StatusUnauthorized {
		t.Fatalf("unpaired origin status = %d, want 401", unpairedResp.Code)
	}
}

func TestBrowserRecorderBridgeGetsBrowserReplaySession(t *testing.T) {
	server := newBrowserRecorderBridgeTestServer(t, newFakeBrowserRecorder(nil))
	bridgeSessionID := pairBridgeForTest(t, server, bridgeTestOrigin)
	replaySession := &BrowserReplaySession{
		ID:       "bridge-replay-1",
		Status:   BrowserReplayCompleted,
		Group:    "browser",
		Protocol: "browser",
	}
	server.browserReplay.mu.Lock()
	server.browserReplay.sessions[replaySession.ID] = replaySession
	server.browserReplay.mu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/browser-replay/sessions/"+replaySession.ID, nil)
	req.Header.Set("Origin", bridgeTestOrigin)
	req.Header.Set(bridgeTokenHeader, bridgeTestToken)
	req.Header.Set(bridgeSessionHeader, bridgeSessionID)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("browser replay status = %d, want 200 body=%s", resp.Code, resp.Body.String())
	}
	if got := resp.Header().Get("Access-Control-Allow-Origin"); got != bridgeTestOrigin {
		t.Errorf("allow-origin = %q, want %q", got, bridgeTestOrigin)
	}
	var payload struct {
		Session BrowserReplaySession `json:"session"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal browser replay response: %v", err)
	}
	if payload.Session.ID != replaySession.ID || payload.Session.Status != BrowserReplayCompleted {
		t.Fatalf("browser replay session id=%q status=%q, want id=%q status=%q", payload.Session.ID, payload.Session.Status, replaySession.ID, BrowserReplayCompleted)
	}
}

func TestBrowserRecorderBridgeRejectsUnauthenticatedBrowserReplaySessionGet(t *testing.T) {
	server := newBrowserRecorderBridgeTestServer(t, newFakeBrowserRecorder(nil))
	pairBridgeForTest(t, server, bridgeTestOrigin)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/browser-replay/sessions/bridge-replay-1", nil)
	req.Header.Set("Origin", bridgeTestOrigin)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated browser replay status = %d, want 401 body=%s", resp.Code, resp.Body.String())
	}
	if got := resp.Header().Get("Access-Control-Allow-Origin"); got != bridgeTestOrigin {
		t.Errorf("allow-origin = %q, want %q", got, bridgeTestOrigin)
	}
}

func TestBrowserRecorderBridgeSessionAccessOutlivesPairingWindow(t *testing.T) {
	server := newBrowserRecorderBridgeTestServer(t, newFakeBrowserRecorder(nil))
	sessionID := pairBridgeForTest(t, server, bridgeTestOrigin)

	server.recordingBridge.mu.Lock()
	server.recordingBridge.pairingExpires = time.Now().Add(-time.Second)
	server.recordingBridge.mu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/browser-recorder/devices", nil)
	req.Header.Set("Origin", bridgeTestOrigin)
	req.Header.Set(bridgeTokenHeader, bridgeTestToken)
	req.Header.Set(bridgeSessionHeader, sessionID)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("session access after pairing expiry status = %d, want 200 body=%s", resp.Code, resp.Body.String())
	}
}

func TestBrowserRecorderBridgeTokenOnlyAccessExpiresWithPairingWindow(t *testing.T) {
	server := newBrowserRecorderBridgeTestServer(t, newFakeBrowserRecorder(nil))
	pairBridgeForTest(t, server, bridgeTestOrigin)

	server.recordingBridge.mu.Lock()
	server.recordingBridge.pairingExpires = time.Now().Add(-time.Second)
	server.recordingBridge.mu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/browser-recorder/devices", nil)
	req.Header.Set("Origin", bridgeTestOrigin)
	req.Header.Set(bridgeTokenHeader, bridgeTestToken)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("expired token-only access status = %d, want 401 body=%s", resp.Code, resp.Body.String())
	}
}

func TestBrowserRecorderBridgeRateLimitsInvalidPairing(t *testing.T) {
	server := newBrowserRecorderBridgeTestServer(t, newFakeBrowserRecorder(nil))

	for attempt := 1; attempt < bridgePairMaxFailures; attempt++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/bridge/pair", bytes.NewBufferString(`{"token":"wrong-token"}`))
		req.Header.Set("Origin", bridgeTestOrigin)
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		if resp.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d, want 401 body=%s", attempt, resp.Code, resp.Body.String())
		}
	}

	locked := httptest.NewRequest(http.MethodPost, "/api/v1/bridge/pair", bytes.NewBufferString(`{"token":"wrong-token"}`))
	locked.Header.Set("Origin", bridgeTestOrigin)
	locked.Header.Set("Content-Type", "application/json")
	lockedResp := httptest.NewRecorder()
	server.mux.ServeHTTP(lockedResp, locked)
	if lockedResp.Code != http.StatusTooManyRequests {
		t.Fatalf("lockout status = %d, want 429 body=%s", lockedResp.Code, lockedResp.Body.String())
	}
	if lockedResp.Header().Get("Retry-After") == "" {
		t.Fatalf("lockout response missing Retry-After header")
	}

	good := httptest.NewRequest(http.MethodPost, "/api/v1/bridge/pair", bytes.NewBufferString(`{"token":"`+bridgeTestToken+`"}`))
	good.Header.Set("Origin", bridgeTestOrigin)
	good.Header.Set("Content-Type", "application/json")
	goodResp := httptest.NewRecorder()
	server.mux.ServeHTTP(goodResp, good)
	if goodResp.Code != http.StatusTooManyRequests {
		t.Fatalf("good token during lockout status = %d, want 429 body=%s", goodResp.Code, goodResp.Body.String())
	}
}

func TestBrowserRecorderBridgeRejectsWeakConfiguredToken(t *testing.T) {
	defer func() {
		if recovered := recover(); recovered == nil {
			t.Fatalf("expected weak configured bridge token to panic")
		}
	}()
	_ = NewBrowserRecorderBridgeServer(BrowserRecorderBridgeConfig{
		Addr:         "127.0.0.1:0",
		PairingToken: "short",
	})
}

func TestBrowserRecorderBridgeMountsOnlyBridgeSurface(t *testing.T) {
	server := newBrowserRecorderBridgeTestServer(t, newFakeBrowserRecorder(nil))
	pairBridgeForTest(t, server, bridgeTestOrigin)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tests", nil)
	req.Header.Set("Origin", bridgeTestOrigin)
	req.Header.Set(bridgeTokenHeader, bridgeTestToken)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code == http.StatusOK || resp.Code == http.StatusUnauthorized {
		t.Fatalf("bridge /tests status = %d, route should not be exposed", resp.Code)
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
