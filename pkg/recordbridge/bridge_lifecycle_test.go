package recordbridge

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestBridgeRuntimeServesAndStopsOwnedResources(t *testing.T) {
	bridge := newBrowserRecorderBridgeTestServer(t, nil)
	done := make(chan error, 1)
	go func() { done <- bridge.Start() }()
	deadline := time.After(2 * time.Second)
	for !bridge.BoundAddrReady() {
		select {
		case err := <-done:
			t.Fatalf("start: %v", err)
		case <-deadline:
			t.Fatal("bridge did not bind")
		case <-time.After(time.Millisecond):
		}
	}
	response, err := http.Get(bridge.BrowserURL() + bridgeSystemHealthPath)
	if err != nil {
		t.Fatal(err)
	}
	var health map[string]any
	if err = json.NewDecoder(response.Body).Decode(&health); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if health["status"] != "healthy" || health["test_count"] != float64(0) {
		t.Fatalf("health=%#v", health)
	}
	if !bridge.service.Recording.BackgroundRunning() || !bridge.service.Replay.BackgroundRunning() {
		t.Fatal("bridge managers not started")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err = bridge.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("Start did not return after shutdown")
	}
	if bridge.BoundAddrReady() || bridge.service.Recording.BackgroundRunning() || bridge.service.Replay.BackgroundRunning() {
		t.Fatal("bridge retained resources after shutdown")
	}
	if err = bridge.Start(); !errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("restarted closed bridge: %v", err)
	}
	if err = bridge.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestBridgeBindFailureDoesNotStartBackgroundOwners(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	bridge := NewBrowserRecorderBridgeServer(BrowserRecorderBridgeConfig{Addr: listener.Addr().String(), DependencyProbe: func() error { return nil }})
	defer bridge.Shutdown(context.Background())
	if err = bridge.Start(); err == nil {
		t.Fatal("bound occupied address")
	}
	if bridge.BoundAddrReady() || bridge.service.Recording.BackgroundRunning() || bridge.service.Replay.BackgroundRunning() {
		t.Fatal("failed startup started background owners")
	}
}

func TestBridgeHubFiltersRunAndProjectAndClosesSockets(t *testing.T) {
	hub := newBridgeHub()
	hub.SetAllowedOrigins([]string{bridgeTestOrigin})
	server := httptest.NewServer(hub)
	defer server.Close()
	defer hub.Stop()
	// Non-browser clients historically have no Origin header.
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"?project_id=p&test_id=run", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	hub.BroadcastScopedJSON("other", "p", map[string]string{"event": "wrong run"})
	hub.BroadcastScopedJSON("run", "other", map[string]string{"event": "wrong project"})
	hub.BroadcastScopedJSON("run", "p", map[string]string{"event": "matching"})
	conn.SetReadDeadline(time.Now().Add(time.Second))
	var event map[string]string
	if err = conn.ReadJSON(&event); err != nil {
		t.Fatal(err)
	}
	if event["event"] != "matching" {
		t.Fatalf("scope leak: %#v", event)
	}
	hub.Stop()
	if _, _, err = conn.ReadMessage(); err == nil {
		t.Fatal("shutdown left socket open")
	}
	if hub.ClientCount() != 0 {
		t.Fatal("shutdown retained clients")
	}
	refused := httptest.NewRecorder()
	hub.ServeHTTP(refused, httptest.NewRequest("GET", "/", nil))
	if refused.Code != 503 {
		t.Fatalf("admission after close=%d", refused.Code)
	}
}

func TestBridgeFailureMiddlewarePreservesResponseControllerAndPanics(t *testing.T) {
	response := httptest.NewRecorder()
	handler := bridgeFailureLoggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Disposition", "attachment")
		panic("failed")
	}))
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/", nil))
	if response.Code != 500 || response.Header().Get("Content-Disposition") != "" || response.Header().Get("X-Request-ID") == "" {
		t.Fatalf("panic response=%d headers=%v", response.Code, response.Header())
	}
	flushed := httptest.NewRecorder()
	wrapped := &bridgeResponseWriter{ResponseWriter: flushed}
	if err := http.NewResponseController(wrapped).Flush(); err != nil || !flushed.Flushed {
		t.Fatalf("flush forwarding: %v", err)
	}
}
