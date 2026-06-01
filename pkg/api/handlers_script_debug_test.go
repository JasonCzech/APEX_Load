package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestScriptDebugManagerBreakpointsAndStep(t *testing.T) {
	var mu sync.Mutex
	var executed []string
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		mu.Lock()
		executed = append(executed, action.Name)
		mu.Unlock()
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})

	manager := NewScriptDebugManager(NewHub())
	session := manager.Start("", &dsl.Script{
		Name:     "Debugger Session",
		Protocol: protocolName,
		Version:  "1.0.0",
		Actions: []dsl.Action{
			{Name: "first", Type: "GET", Target: "/first"},
			{Name: "second", Type: "GET", Target: "/second"},
			{Name: "third", Type: "GET", Target: "/third"},
		},
	}, ScriptDebugStartRequest{
		ReplaySessionID: "debug-step",
		Breakpoints:     []ScriptDebugBreakpoint{{Phase: "action", ActionIndex: 1}},
		LogLevel:        "debug",
	})
	if session.ID != "debug-step" {
		t.Fatalf("session.ID = %q, want debug-step", session.ID)
	}

	paused := waitForScriptDebugStatus(t, manager, "debug-step", ScriptDebugPaused)
	if paused.CurrentAction == nil || paused.CurrentAction.ActionIndex != 1 {
		t.Fatalf("current action = %#v, want action index 1", paused.CurrentAction)
	}
	mu.Lock()
	gotExecuted := append([]string(nil), executed...)
	mu.Unlock()
	if !slices.Equal(gotExecuted, []string{"first"}) {
		t.Fatalf("executed = %#v, want only first before breakpoint", gotExecuted)
	}

	stepped, ok, changed := manager.Step("debug-step")
	if !ok || !changed {
		t.Fatalf("Step ok=%v changed=%v session=%#v, want changed", ok, changed, stepped)
	}
	paused = waitForScriptDebugEventCount(t, manager, "debug-step", 2, ScriptDebugPaused)
	mu.Lock()
	gotExecuted = append([]string(nil), executed...)
	mu.Unlock()
	if !slices.Equal(gotExecuted, []string{"first", "second"}) {
		t.Fatalf("executed after step = %#v, want first second", gotExecuted)
	}
	if len(paused.RawOutput) == 0 || !strings.Contains(strings.Join(paused.RawOutput, "\n"), "second completed") {
		t.Fatalf("raw output = %#v, want second completion evidence", paused.RawOutput)
	}

	resumed, ok, changed := manager.Resume("debug-step")
	if !ok || !changed {
		t.Fatalf("Resume ok=%v changed=%v session=%#v, want changed", ok, changed, resumed)
	}
	completed := waitForScriptDebugStatus(t, manager, "debug-step", ScriptDebugCompleted)
	if len(completed.Events) != 3 {
		t.Fatalf("events = %d, want 3", len(completed.Events))
	}
}

func TestHandleStartScriptDebugDraftScriptReturnsSession(t *testing.T) {
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})
	server := NewServer(ServerConfig{})
	body, _ := json.Marshal(map[string]any{
		"group_name":        "debug-editor",
		"replay_session_id": "debug-api",
		"start_paused":      true,
		"log_level":         "trace",
		"breakpoints": []map[string]any{{
			"phase":        "action",
			"action_index": 0,
		}},
		"script": map[string]any{
			"name":     "HTTP Draft",
			"version":  "1.0.0",
			"protocol": protocolName,
			"config": map[string]any{
				"base_url": "https://example.com",
			},
			"actions": []map[string]any{{
				"name":   "Get home",
				"type":   "GET",
				"method": "GET",
				"target": "/",
			}},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/debug/sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var payload struct {
		DebugSessionID string             `json:"debug_session_id"`
		Session        ScriptDebugSession `json:"session"`
		Preflight      ReplayPreflight    `json:"preflight"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if payload.DebugSessionID != "debug-api" || payload.Session.Status != ScriptDebugPaused || payload.Session.LogLevel != "trace" {
		t.Fatalf("payload = %#v, want paused trace session debug-api", payload)
	}
	if payload.Preflight.ActionCount != 1 || payload.Session.Preflight == nil || payload.Session.RuntimeProfile.LogLevel != "trace" {
		t.Fatalf("preflight/runtime = %#v/%#v, want preflight and trace runtime profile", payload.Preflight, payload.Session.RuntimeProfile)
	}

	exportReq := httptest.NewRequest(http.MethodGet, "/api/v1/script-debug/sessions/debug-api/export?format=log", nil)
	exportResp := httptest.NewRecorder()
	server.mux.ServeHTTP(exportResp, exportReq)
	if exportResp.Code != http.StatusOK {
		t.Fatalf("export status = %d, want %d body=%s", exportResp.Code, http.StatusOK, exportResp.Body.String())
	}
	if !strings.Contains(exportResp.Body.String(), "Debugger session started") {
		t.Fatalf("export body = %q, want debugger log", exportResp.Body.String())
	}

	stopReq := httptest.NewRequest(http.MethodPost, "/api/v1/script-debug/sessions/debug-api/stop", nil)
	stopResp := httptest.NewRecorder()
	server.mux.ServeHTTP(stopResp, stopReq)
	if stopResp.Code != http.StatusOK {
		t.Fatalf("stop status = %d, want %d body=%s", stopResp.Code, http.StatusOK, stopResp.Body.String())
	}
}

func TestHandleStartReplaySessionDraftScriptReturnsUnifiedPreflight(t *testing.T) {
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})
	server := NewServer(ServerConfig{})
	body, _ := json.Marshal(map[string]any{
		"replay_session_id": "unified-replay-api",
		"start_paused":      true,
		"log_level":         "debug",
		"script": map[string]any{
			"name":     "Unified Replay",
			"version":  "1.0.0",
			"protocol": protocolName,
			"config": map[string]any{
				"base_url": "https://example.com",
			},
			"actions": []map[string]any{{
				"name":   "Get account",
				"type":   "GET",
				"method": "GET",
				"target": "/accounts/{{var.account_id}}",
			}},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/replay/sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var payload struct {
		ReplaySessionID string             `json:"replay_session_id"`
		Session         ScriptDebugSession `json:"session"`
		Preflight       ReplayPreflight    `json:"preflight"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if payload.ReplaySessionID != "unified-replay-api" || payload.Session.Mode != "replay" || payload.Session.Preflight == nil {
		t.Fatalf("payload = %#v, want unified replay session with preflight", payload)
	}
	if !slices.Contains(payload.Preflight.MissingVariables, "account_id") || payload.Preflight.ActionCount != 1 {
		t.Fatalf("preflight = %#v, want missing account_id and one action", payload.Preflight)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/replay/sessions/unified-replay-api", nil)
	getResp := httptest.NewRecorder()
	server.mux.ServeHTTP(getResp, getReq)
	if getResp.Code != http.StatusOK {
		t.Fatalf("unified get status = %d, want %d body=%s", getResp.Code, http.StatusOK, getResp.Body.String())
	}

	stopReq := httptest.NewRequest(http.MethodPost, "/api/v1/replay/sessions/unified-replay-api/stop", nil)
	stopResp := httptest.NewRecorder()
	server.mux.ServeHTTP(stopResp, stopReq)
	if stopResp.Code != http.StatusOK {
		t.Fatalf("unified stop status = %d, want %d body=%s", stopResp.Code, http.StatusOK, stopResp.Body.String())
	}
}

func TestHandleUpdateScriptDebugBreakpoints(t *testing.T) {
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})
	server := NewServer(ServerConfig{})
	server.scriptDebug.Start("", &dsl.Script{
		Name:     "Breakpoint Draft",
		Protocol: protocolName,
		Version:  "1.0.0",
		Actions:  []dsl.Action{{Name: "first", Type: "GET", Target: "/first"}},
	}, ScriptDebugStartRequest{ReplaySessionID: "debug-breakpoints", StartPaused: true})

	body := []byte(`{"breakpoints":[{"phase":"action","action_index":0},{"phase":"action","action_index":0},{"phase":"init","action_index":-1}]}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/script-debug/sessions/debug-breakpoints/breakpoints", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var payload struct {
		Session ScriptDebugSession `json:"session"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if len(payload.Session.Breakpoints) != 1 || payload.Session.Breakpoints[0].ActionIndex != 0 {
		t.Fatalf("breakpoints = %#v, want single normalized action breakpoint", payload.Session.Breakpoints)
	}
	_, _ = server.scriptDebug.Stop("debug-breakpoints")
}

func waitForScriptDebugStatus(t *testing.T, manager *ScriptDebugManager, sessionID string, status string) *ScriptDebugSession {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		session, ok := manager.Get(sessionID)
		if !ok {
			t.Fatalf("session %s not found", sessionID)
		}
		if session.Status == status {
			return session
		}
		time.Sleep(10 * time.Millisecond)
	}
	session, _ := manager.Get(sessionID)
	t.Fatalf("session status = %#v, want %s", session, status)
	return nil
}

func waitForScriptDebugEventCount(t *testing.T, manager *ScriptDebugManager, sessionID string, count int, status string) *ScriptDebugSession {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		session, ok := manager.Get(sessionID)
		if !ok {
			t.Fatalf("session %s not found", sessionID)
		}
		if len(session.Events) >= count && session.Status == status {
			return session
		}
		time.Sleep(10 * time.Millisecond)
	}
	session, _ := manager.Get(sessionID)
	t.Fatalf("session = %#v, want %d events and status %s", session, count, status)
	return nil
}
