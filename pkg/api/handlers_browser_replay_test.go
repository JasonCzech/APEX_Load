package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/stormrunner/pkg/dsl"
	"github.com/jasonczech/stormrunner/pkg/protocol"
)

func TestHandleStartBrowserReplayDraftScriptReturnsSession(t *testing.T) {
	server := NewServer(ServerConfig{})
	body, _ := json.Marshal(map[string]any{
		"group_name": "browser-editor",
		"script": map[string]any{
			"name":     "Browser Draft",
			"version":  "1.0.0",
			"protocol": "browser",
			"config": map[string]any{
				"browser": map[string]any{
					"engine": "chromium",
				},
			},
			"actions": []map[string]any{{
				"name": "Capture current page",
				"type": "browser_screenshot",
			}},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/browser-replay/sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var payload struct {
		ReplaySessionID string `json:"replay_session_id"`
		Session         struct {
			Group    string `json:"group"`
			Protocol string `json:"protocol"`
			Status   string `json:"status"`
		} `json:"session"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if payload.ReplaySessionID == "" {
		t.Fatal("replay_session_id is empty")
	}
	if payload.Session.Group != "browser-editor" {
		t.Fatalf("group = %q, want browser-editor", payload.Session.Group)
	}
	if payload.Session.Protocol != "browser" {
		t.Fatalf("protocol = %q, want browser", payload.Session.Protocol)
	}
	if payload.Session.Status == "" {
		t.Fatal("status is empty")
	}
}

func TestHandleGetBrowserReplaySessionNotFound(t *testing.T) {
	server := NewServer(ServerConfig{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/browser-replay/sessions/missing", nil)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusNotFound, resp.Body.String())
	}
}

func TestHandleStartBrowserReplayRejectsNonBrowserScript(t *testing.T) {
	server := NewServer(ServerConfig{})
	stored := server.runner.StoreScript(&dsl.Script{
		Name:     "HTTP Script",
		Protocol: "http",
		Version:  "1.0.0",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions:  []dsl.Action{{Name: "login", Type: "GET", Target: "/login"}},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/"+stored.ID+"/browser-replay/sessions", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
}

func TestBrowserReplayManagerRetainsStreamedEvents(t *testing.T) {
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{
			ActionName:       action.Name,
			Success:          true,
			PageURL:          "https://example.com/",
			PageTitle:        "Example",
			ScreenshotBase64: "c2NyZWVu",
			DOMSummary:       &protocol.DOMSummary{Title: "Example", ReadyState: "complete"},
		}, nil
	})

	manager := NewBrowserReplayManager(NewHub())
	session := manager.Start("", &dsl.Script{
		Name:     "Browser Session",
		Protocol: protocolName,
		Version:  "1.0.0",
		Actions:  []dsl.Action{{Name: "shot", Type: "browser_screenshot"}},
	}, BrowserReplayStartRequest{ReplaySessionID: "session-events"})
	if session.ID != "session-events" {
		t.Fatalf("session.ID = %q, want session-events", session.ID)
	}

	var got *BrowserReplaySession
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var ok bool
		got, ok = manager.Get("session-events")
		if !ok {
			t.Fatal("session not found")
		}
		if got.Status == BrowserReplayCompleted {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if got.Status != BrowserReplayCompleted {
		t.Fatalf("status = %q, want completed", got.Status)
	}
	if got.CurrentEventIndex != 0 {
		t.Fatalf("current_event_index = %d, want 0", got.CurrentEventIndex)
	}
	if got.CurrentEvent == nil || got.CurrentEvent.PageTitle != "Example" {
		t.Fatalf("current_event = %#v, want page title Example", got.CurrentEvent)
	}
	if len(got.Events) != 1 || got.Events[0].Screenshot != "c2NyZWVu" {
		t.Fatalf("events = %#v, want retained screenshot event", got.Events)
	}
}

func TestApplyBrowserReplayOverrides(t *testing.T) {
	headless := false
	script := &dsl.Script{
		Name:     "Browser Script",
		Protocol: "browser",
		Config: dsl.ScriptConfig{
			Browser: dsl.BrowserConfig{
				CaptureScreenshots: "off",
			},
		},
	}

	updated := applyBrowserReplayOverrides(script, BrowserReplayStartRequest{
		Browser: &dsl.BrowserConfig{
			Headless:           &headless,
			SlowMoMs:           125,
			CaptureScreenshots: "per_action",
			Viewport:           dsl.BrowserViewport{Width: 1280, Height: 720},
		},
	})

	if updated == script {
		t.Fatal("expected copy, got original script pointer")
	}
	if updated.Config.Browser.Headless == nil || *updated.Config.Browser.Headless != false {
		t.Fatalf("headless = %#v, want false", updated.Config.Browser.Headless)
	}
	if updated.Config.Browser.SlowMoMs != 125 {
		t.Fatalf("slow_mo_ms = %d, want 125", updated.Config.Browser.SlowMoMs)
	}
	if updated.Config.Browser.CaptureScreenshots != "per_action" {
		t.Fatalf("capture_screenshots = %q, want per_action", updated.Config.Browser.CaptureScreenshots)
	}
	if updated.Config.Browser.Viewport.Width != 1280 || updated.Config.Browser.Viewport.Height != 720 {
		t.Fatalf("viewport = %#v, want 1280x720", updated.Config.Browser.Viewport)
	}
	if script.Config.Browser.CaptureScreenshots != "off" {
		t.Fatalf("original script was mutated: %#v", script.Config.Browser)
	}
}

func TestHandlePauseResumeBrowserReplaySessionNotFound(t *testing.T) {
	server := NewServer(ServerConfig{})

	pauseReq := httptest.NewRequest(http.MethodPost, "/api/v1/browser-replay/sessions/missing/pause", nil)
	pauseResp := httptest.NewRecorder()
	server.mux.ServeHTTP(pauseResp, pauseReq)
	if pauseResp.Code != http.StatusNotFound {
		t.Fatalf("pause status = %d, want %d body=%s", pauseResp.Code, http.StatusNotFound, pauseResp.Body.String())
	}

	resumeReq := httptest.NewRequest(http.MethodPost, "/api/v1/browser-replay/sessions/missing/resume", nil)
	resumeResp := httptest.NewRecorder()
	server.mux.ServeHTTP(resumeResp, resumeReq)
	if resumeResp.Code != http.StatusNotFound {
		t.Fatalf("resume status = %d, want %d body=%s", resumeResp.Code, http.StatusNotFound, resumeResp.Body.String())
	}
}

func TestHandleResumeBrowserReplaySessionFromStartPaused(t *testing.T) {
	server := NewServer(ServerConfig{})
	body, _ := json.Marshal(map[string]any{
		"start_paused": true,
		"script": map[string]any{
			"name":     "Paused Browser Draft",
			"version":  "1.0.0",
			"protocol": "browser",
			"config": map[string]any{
				"base_url": "https://example.com",
			},
			"actions": []map[string]any{{
				"name": "shot",
				"type": "browser_screenshot",
			}},
		},
	})

	startReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/browser-replay/sessions", bytes.NewReader(body))
	startReq.Header.Set("Content-Type", "application/json")
	startResp := httptest.NewRecorder()
	server.mux.ServeHTTP(startResp, startReq)
	if startResp.Code != http.StatusOK {
		t.Fatalf("start status = %d, want %d body=%s", startResp.Code, http.StatusOK, startResp.Body.String())
	}

	var startPayload struct {
		ReplaySessionID string `json:"replay_session_id"`
		Session         struct {
			Status string `json:"status"`
		} `json:"session"`
	}
	if err := json.Unmarshal(startResp.Body.Bytes(), &startPayload); err != nil {
		t.Fatalf("json.Unmarshal start returned error: %v", err)
	}
	if startPayload.ReplaySessionID == "" {
		t.Fatal("replay_session_id is empty")
	}
	if startPayload.Session.Status != BrowserReplayPaused {
		t.Fatalf("start status = %q, want %q", startPayload.Session.Status, BrowserReplayPaused)
	}

	pauseReq := httptest.NewRequest(http.MethodPost, "/api/v1/browser-replay/sessions/"+startPayload.ReplaySessionID+"/pause", nil)
	pauseResp := httptest.NewRecorder()
	server.mux.ServeHTTP(pauseResp, pauseReq)
	if pauseResp.Code != http.StatusConflict {
		t.Fatalf("pause status = %d, want %d body=%s", pauseResp.Code, http.StatusConflict, pauseResp.Body.String())
	}
	if !strings.Contains(pauseResp.Body.String(), "not running") {
		t.Fatalf("pause conflict body = %q, want not running", pauseResp.Body.String())
	}

	resumeReq := httptest.NewRequest(http.MethodPost, "/api/v1/browser-replay/sessions/"+startPayload.ReplaySessionID+"/resume", nil)
	resumeResp := httptest.NewRecorder()
	server.mux.ServeHTTP(resumeResp, resumeReq)
	if resumeResp.Code != http.StatusOK {
		t.Fatalf("resume status = %d, want %d body=%s", resumeResp.Code, http.StatusOK, resumeResp.Body.String())
	}

	var resumePayload struct {
		Session struct {
			Status string `json:"status"`
		} `json:"session"`
	}
	if err := json.Unmarshal(resumeResp.Body.Bytes(), &resumePayload); err != nil {
		t.Fatalf("json.Unmarshal resume returned error: %v", err)
	}
	if resumePayload.Session.Status != BrowserReplayRunning {
		t.Fatalf("resume status = %q, want %q", resumePayload.Session.Status, BrowserReplayRunning)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		session, ok := server.browserReplay.Get(startPayload.ReplaySessionID)
		if !ok {
			t.Fatal("session disappeared")
		}
		if session.Status == BrowserReplayCompleted || session.Status == BrowserReplayFailed || session.Status == BrowserReplayStopped {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("session did not reach terminal state after resume")
}
