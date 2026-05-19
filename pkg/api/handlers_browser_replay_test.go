package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/stormrunner/pkg/dsl"
	"github.com/jasonczech/stormrunner/pkg/engine"
	"github.com/jasonczech/stormrunner/pkg/protocol"
	"github.com/jasonczech/stormrunner/pkg/storage"
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

func TestBrowserReplaySessionSnapshotReturnsDetachedCopy(t *testing.T) {
	now := time.Now()
	session := &BrowserReplaySession{
		ID:                "session-detached",
		Status:            BrowserReplayRunning,
		StartedAt:         &now,
		CurrentEventIndex: 0,
		CurrentEvent: &engine.ReplayActionEvent{
			ActionName:    "step-1",
			Headers:       map[string]string{"Authorization": "Bearer original"},
			ClientMetrics: &protocol.ClientMetrics{LCPMs: 240},
		},
		Events: []engine.ReplayActionEvent{{
			ActionName:    "step-1",
			Headers:       map[string]string{"Authorization": "Bearer original"},
			ClientMetrics: &protocol.ClientMetrics{LCPMs: 240},
		}},
		FinalReport: &engine.ReplayReport{
			Group:          "browser",
			FinalVariables: map[string]string{"token": "original"},
			Events: []engine.ReplayActionEvent{{
				ActionName:    "step-1",
				Headers:       map[string]string{"Authorization": "Bearer original"},
				ClientMetrics: &protocol.ClientMetrics{LCPMs: 240},
			}},
		},
	}

	snapshot := session.snapshot()
	if snapshot == nil {
		t.Fatal("snapshot returned nil")
	}

	session.CurrentEvent.Headers["Authorization"] = "Bearer updated"
	session.CurrentEvent.ClientMetrics.LCPMs = 999
	session.Events[0].Headers["Authorization"] = "Bearer updated"
	session.Events[0].ClientMetrics.LCPMs = 999
	session.FinalReport.FinalVariables["token"] = "updated"
	session.FinalReport.Events[0].Headers["Authorization"] = "Bearer updated"
	session.FinalReport.Events[0].ClientMetrics.LCPMs = 999

	if got := snapshot.CurrentEvent.Headers["Authorization"]; got != "Bearer original" {
		t.Fatalf("snapshot current event header = %q, want original value", got)
	}
	if got := snapshot.Events[0].Headers["Authorization"]; got != "Bearer original" {
		t.Fatalf("snapshot event header = %q, want original value", got)
	}
	if got := snapshot.FinalReport.FinalVariables["token"]; got != "original" {
		t.Fatalf("snapshot final variable = %q, want original value", got)
	}
	if got := snapshot.FinalReport.Events[0].Headers["Authorization"]; got != "Bearer original" {
		t.Fatalf("snapshot final report header = %q, want original value", got)
	}
	if snapshot.CurrentEvent.ClientMetrics == nil || snapshot.CurrentEvent.ClientMetrics.LCPMs != 240 {
		t.Fatalf("snapshot current event client metrics = %#v, want detached LCP 240", snapshot.CurrentEvent.ClientMetrics)
	}
	if snapshot.Events[0].ClientMetrics == nil || snapshot.Events[0].ClientMetrics.LCPMs != 240 {
		t.Fatalf("snapshot event client metrics = %#v, want detached LCP 240", snapshot.Events[0].ClientMetrics)
	}
	if snapshot.FinalReport.Events[0].ClientMetrics == nil || snapshot.FinalReport.Events[0].ClientMetrics.LCPMs != 240 {
		t.Fatalf("snapshot report client metrics = %#v, want detached LCP 240", snapshot.FinalReport.Events[0].ClientMetrics)
	}
}

func TestBrowserReplayManagerStopCancelsPausedSession(t *testing.T) {
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		time.Sleep(150 * time.Millisecond)
		return &protocol.Result{
			ActionName: action.Name,
			Success:    true,
		}, nil
	})

	manager := NewBrowserReplayManager(NewHub())
	session := manager.Start("", &dsl.Script{
		Name:     "Browser Session",
		Protocol: protocolName,
		Version:  "1.0.0",
		Actions: []dsl.Action{
			{Name: "first", Type: "browser_wait_for_timeout", Value: "25"},
			{Name: "second", Type: "browser_wait_for_timeout", Value: "25"},
		},
	}, BrowserReplayStartRequest{ReplaySessionID: "session-paused-stop"})
	if session.ID != "session-paused-stop" {
		t.Fatalf("session.ID = %q, want session-paused-stop", session.ID)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		current, ok := manager.Get("session-paused-stop")
		if !ok {
			t.Fatal("session not found")
		}
		if current.CurrentEventIndex >= 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	paused, ok, changed := manager.Pause("session-paused-stop")
	if !ok {
		t.Fatal("session not found while pausing")
	}
	if !changed {
		t.Fatalf("expected pause to change state, got %#v", paused)
	}
	if paused.Status != BrowserReplayPaused {
		t.Fatalf("paused status = %q, want paused", paused.Status)
	}

	stopped, ok := manager.Stop("session-paused-stop")
	if !ok {
		t.Fatal("session not found while stopping")
	}
	if stopped.Status != BrowserReplayStopped {
		t.Fatalf("stopped status = %q, want stopped", stopped.Status)
	}

	for time.Now().Before(deadline) {
		current, ok := manager.Get("session-paused-stop")
		if !ok {
			t.Fatal("session not found after stop")
		}
		if current.Status == BrowserReplayStopped && current.CompletedAt != nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	current, ok := manager.Get("session-paused-stop")
	if !ok {
		t.Fatal("session not found after stop deadline")
	}
	t.Fatalf("session = %#v, want stopped session with completion timestamp", current)
}

func TestApplyBrowserReplayOverrides(t *testing.T) {
	headless := false
	touchMode := true
	clientMetrics := true
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
			Engine:             "webkit",
			Headless:           &headless,
			SlowMoMs:           125,
			UserAgent:          "Mozilla/5.0 Test Browser",
			DeviceProfile:      "iphone_15_pro",
			TouchMode:          &touchMode,
			Orientation:        "portrait",
			ClientMetrics:      &clientMetrics,
			CaptureScreenshots: "per_action",
			Viewport:           dsl.BrowserViewport{Width: 1280, Height: 720},
		},
	})

	if updated == script {
		t.Fatal("expected copy, got original script pointer")
	}
	if updated.Config.Browser.Engine != "webkit" {
		t.Fatalf("engine = %q, want webkit", updated.Config.Browser.Engine)
	}
	if updated.Config.Browser.Headless == nil || *updated.Config.Browser.Headless != false {
		t.Fatalf("headless = %#v, want false", updated.Config.Browser.Headless)
	}
	if updated.Config.Browser.SlowMoMs != 125 {
		t.Fatalf("slow_mo_ms = %d, want 125", updated.Config.Browser.SlowMoMs)
	}
	if updated.Config.Browser.UserAgent != "Mozilla/5.0 Test Browser" {
		t.Fatalf("user_agent = %q, want test browser UA", updated.Config.Browser.UserAgent)
	}
	if updated.Config.Browser.DeviceProfile != "iphone_15_pro" {
		t.Fatalf("device_profile = %q, want iphone_15_pro", updated.Config.Browser.DeviceProfile)
	}
	if updated.Config.Browser.TouchMode == nil || *updated.Config.Browser.TouchMode != true {
		t.Fatalf("touch_mode = %#v, want true", updated.Config.Browser.TouchMode)
	}
	if updated.Config.Browser.Orientation != "portrait" {
		t.Fatalf("orientation = %q, want portrait", updated.Config.Browser.Orientation)
	}
	if updated.Config.Browser.ClientMetrics == nil || *updated.Config.Browser.ClientMetrics != true {
		t.Fatalf("client_metrics = %#v, want true", updated.Config.Browser.ClientMetrics)
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

func TestHandleStartBrowserReplayDraftScriptAcceptsMobileBrowserOverrides(t *testing.T) {
	server := NewServer(ServerConfig{})
	body, _ := json.Marshal(map[string]any{
		"script": map[string]any{
			"name":     "Browser Draft",
			"version":  "1.0.0",
			"protocol": "browser",
			"config": map[string]any{
				"browser": map[string]any{
					"engine": "webkit",
				},
			},
			"actions": []map[string]any{{
				"name": "Capture current page",
				"type": "browser_screenshot",
			}},
		},
		"browser": map[string]any{
			"engine":              "webkit",
			"device_profile":      "iphone_15_pro",
			"touch_mode":          true,
			"orientation":         "portrait",
			"client_metrics":      true,
			"capture_screenshots": "per_action",
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/browser-replay/sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
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
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{
			ActionName: action.Name,
			Success:    true,
			PageURL:    "https://example.com/",
			PageTitle:  "Example",
		}, nil
	})

	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "stormrunner.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store, Addr: ":8080"})
	script := &dsl.Script{
		Name:      "Paused Browser Draft",
		ProjectID: "project-browser",
		Version:   "1.0.0",
		Protocol:  protocolName,
		Config: dsl.ScriptConfig{
			BaseURL: "https://example.com",
		},
		Actions: []dsl.Action{{
			Name:   "shot",
			Type:   "GET",
			Method: "GET",
			Target: "/",
		}},
	}
	session := server.browserReplay.Start("", script, BrowserReplayStartRequest{
		ReplaySessionID: "browser-replay-start-paused",
		StartPaused:     true,
	})
	if session.ID == "" {
		t.Fatal("replay_session_id is empty")
	}
	if session.Status != BrowserReplayPaused {
		t.Fatalf("start status = %q, want %q", session.Status, BrowserReplayPaused)
	}
	startReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/browser-replay/sessions", nil)
	server.recordAuditEvent(startReq, "browser_replay.session.start", "success", "browser_replay_session", session.ID, session.ProjectID, map[string]any{"script_id": session.ScriptID, "script_name": script.Name, "status": session.Status, "start_paused": true})

	pauseReq := httptest.NewRequest(http.MethodPost, "/api/v1/browser-replay/sessions/"+session.ID+"/pause", nil)
	pauseResp := httptest.NewRecorder()
	server.mux.ServeHTTP(pauseResp, pauseReq)
	if pauseResp.Code != http.StatusConflict {
		t.Fatalf("pause status = %d, want %d body=%s", pauseResp.Code, http.StatusConflict, pauseResp.Body.String())
	}
	if !strings.Contains(pauseResp.Body.String(), "not running") {
		t.Fatalf("pause conflict body = %q, want not running", pauseResp.Body.String())
	}

	resumeReq := httptest.NewRequest(http.MethodPost, "/api/v1/browser-replay/sessions/"+session.ID+"/resume", nil)
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

	events, err := store.ListAuditEvents(storage.AuditEventFilter{Limit: 10, ProjectID: "project-browser"})
	if err != nil {
		t.Fatalf("ListAuditEvents returned error: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("audit event count = %d, want 2", len(events))
	}
	if events[0].Action != "browser_replay.session.resume" || events[1].Action != "browser_replay.session.start" {
		t.Fatalf("audit actions = [%s %s], want resume/start", events[0].Action, events[1].Action)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		session, ok := server.browserReplay.Get(session.ID)
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
