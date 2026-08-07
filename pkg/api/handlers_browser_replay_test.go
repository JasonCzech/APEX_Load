package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestHandleStartBrowserReplayDraftScriptReturnsSession(t *testing.T) {
	server := NewServer(ServerConfig{})
	body, _ := json.Marshal(map[string]any{
		"group_name": "browser-editor",
		"project_id": "project-browser-draft",
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
			Group     string `json:"group"`
			ProjectID string `json:"project_id"`
			Protocol  string `json:"protocol"`
			Status    string `json:"status"`
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
	if payload.Session.ProjectID != "project-browser-draft" {
		t.Fatalf("project_id = %q, want project-browser-draft", payload.Session.ProjectID)
	}
	if payload.Session.Protocol != "browser" {
		t.Fatalf("protocol = %q, want browser", payload.Session.Protocol)
	}
	if payload.Session.Status == "" {
		t.Fatal("status is empty")
	}
}

func TestHandleStartBrowserReplayRejectsInvalidAndCrossProjectCollidingSessionIDs(t *testing.T) {
	t.Run("invalid id", func(t *testing.T) {
		server := NewServer(ServerConfig{})
		body, _ := json.Marshal(map[string]any{
			"replay_session_id": "predictable-browser-id",
			"project_id":        "project-b",
			"script":            map[string]any{"name": "Draft", "protocol": "browser"},
		})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/browser-replay/sessions", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "canonical UUID v4") {
			t.Fatalf("status = %d body=%s, want invalid session-id rejection", resp.Code, resp.Body.String())
		}
	})

	t.Run("cross project collision", func(t *testing.T) {
		const sessionID = "66666666-6666-4666-8666-666666666666"
		server := NewServer(ServerConfig{})
		now := time.Now().UTC()
		cancelled := make(chan struct{})
		server.browserReplay.mu.Lock()
		server.browserReplay.sessions[sessionID] = &BrowserReplaySession{
			ID: sessionID, ProjectID: "project-a", Status: BrowserReplayRunning, StartedAt: &now,
			lastActivityAt: now, cancel: func() { close(cancelled) },
		}
		server.browserReplay.mu.Unlock()
		body, _ := json.Marshal(map[string]any{
			"replay_session_id": sessionID,
			"project_id":        "project-b",
			"start_paused":      true,
			"script": map[string]any{
				"name": "Draft", "version": "1.0.0", "protocol": "browser",
				"config":  map[string]any{"browser": map[string]any{"engine": "chromium"}},
				"actions": []map[string]any{{"name": "capture", "type": "browser_screenshot"}},
			},
		})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/browser-replay/sessions", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		if resp.Code != http.StatusConflict {
			t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusConflict, resp.Body.String())
		}
		select {
		case <-cancelled:
			t.Fatal("cross-project colliding start cancelled the established session")
		default:
		}
		existing, ok := server.browserReplay.Get(sessionID)
		if !ok || existing.ProjectID != "project-a" || existing.Status != BrowserReplayRunning {
			t.Fatalf("existing session = %#v, want untouched project-a session", existing)
		}
		_, _ = server.browserReplay.Stop(sessionID)
	})

	t.Run("cross project capacity", func(t *testing.T) {
		const (
			establishedSessionID = "77777777-7777-4777-8777-777777777777"
			newSessionID         = "88888888-8888-4888-8888-888888888888"
		)
		server := NewServer(ServerConfig{})
		now := time.Now().UTC()
		cancelled := make(chan struct{})
		server.browserReplay.mu.Lock()
		server.browserReplay.retention.maxSessions = 1
		server.browserReplay.sessions[establishedSessionID] = &BrowserReplaySession{
			ID: establishedSessionID, ProjectID: "project-a", Status: BrowserReplayRunning, StartedAt: &now,
			lastActivityAt: now, cancel: func() { close(cancelled) }, stateChanged: make(chan struct{}),
		}
		server.browserReplay.mu.Unlock()

		body, _ := json.Marshal(map[string]any{
			"replay_session_id": newSessionID,
			"project_id":        "project-b",
			"start_paused":      true,
			"script": map[string]any{
				"name": "Draft", "version": "1.0.0", "protocol": "browser",
				"config":  map[string]any{"browser": map[string]any{"engine": "chromium"}},
				"actions": []map[string]any{{"name": "capture", "type": "browser_screenshot"}},
			},
		})
		start := func() *httptest.ResponseRecorder {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/browser-replay/sessions", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			server.mux.ServeHTTP(resp, req)
			return resp
		}

		resp := start()
		if resp.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d body=%s, want browser replay capacity rejection", resp.Code, resp.Body.String())
		}
		select {
		case <-cancelled:
			t.Fatal("cross-project capacity rejection cancelled the established session")
		default:
		}
		existing, ok := server.browserReplay.Get(establishedSessionID)
		if !ok || existing.ProjectID != "project-a" || existing.Status != BrowserReplayRunning {
			t.Fatalf("existing session = %#v, want untouched project-a session", existing)
		}
		if _, ok := server.browserReplay.Get(newSessionID); ok {
			t.Fatal("capacity-rejected browser replay session was retained")
		}

		_, _ = server.browserReplay.Stop(establishedSessionID)
		resp = start()
		if resp.Code != http.StatusOK {
			t.Fatalf("retry status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
		}
		_, _ = server.browserReplay.Stop(newSessionID)
	})
}

func TestHandleStartBrowserReplayDraftScriptRejectsProjectMismatch(t *testing.T) {
	server := NewServer(ServerConfig{})
	body, _ := json.Marshal(map[string]any{
		"project_id": "project-request",
		"script": map[string]any{
			"name":       "Browser Draft",
			"version":    "1.0.0",
			"protocol":   "browser",
			"project_id": "project-script",
			"config": map[string]any{
				"browser": map[string]any{"engine": "chromium"},
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

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "project_id does not match script.project_id") {
		t.Fatalf("body = %q, want project mismatch", resp.Body.String())
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

func TestHandleGetBrowserReplaySessionDoesNotRecordStopAudit(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store, Addr: ":8080"})
	script := &dsl.Script{
		Name:      "Browser Draft",
		ProjectID: "project-browser-audit",
		Version:   "1.0.0",
		Protocol:  "browser",
		Config:    dsl.ScriptConfig{Browser: dsl.BrowserConfig{Engine: "chromium"}},
		Actions:   []dsl.Action{{Name: "Capture", Type: "browser_screenshot"}},
	}
	session, err := server.browserReplay.Start("", script, BrowserReplayStartRequest{
		ReplaySessionID: "browser-get-audit",
		StartPaused:     true,
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/browser-replay/sessions/"+session.ID, nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	events, err := store.ListAuditEvents(storage.AuditEventFilter{Action: "browser_replay.session.stop", Limit: 10})
	if err != nil {
		t.Fatalf("ListAuditEvents returned error: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("stop audit event count = %d, want 0", len(events))
	}
	_, _ = server.browserReplay.Stop(session.ID)
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

func TestHandleStartBrowserReplayUsesDurableRetainedImportCredential(t *testing.T) {
	const password = "browser-import-retained-password"
	server := NewServer(ServerConfig{})
	source := &dsl.Script{
		ID:       "browser-retained-import",
		Name:     "Browser retained import",
		Protocol: "browser",
		Version:  "1.0.0",
		Config: dsl.ScriptConfig{Browser: dsl.BrowserConfig{
			Engine: "chromium",
			HTTPCredentials: dsl.BrowserHTTPCredentials{
				Username: "operator",
				Password: password,
			},
		}},
		Actions: []dsl.Action{{
			Name:   "Open private page",
			Type:   "browser_navigate",
			Target: "https://origin.example.test/private",
		}},
	}
	if _, _, err := server.runner.CreateImportedScriptDurableReportingReplay(source, "enterprise_script_archive"); err != nil {
		t.Fatalf("create imported browser script: %v", err)
	}
	stored, ok := server.runner.GetScript(source.ID)
	if !ok || stored == nil || stored.Script == nil {
		t.Fatalf("stored imported browser script = %#v, found=%t", stored, ok)
	}
	projected := maskScriptSecrets(stored.Script)
	if projected == nil || projected.Config.Browser.HTTPCredentials.Password != maskedScriptSecret {
		t.Fatalf("browser disclosure projection did not mask retained password: %#v", projected)
	}

	body, _ := json.Marshal(map[string]any{
		"start_paused": true,
		// The editor sends the disclosure-safe GET projection back with bounded
		// replay overrides. The handler must recognize that unchanged echo and
		// execute the server-owned durable credential snapshot.
		"script": projected,
		"browser": map[string]any{
			"viewport": map[string]any{"width": 1200, "height": 800},
		},
	})
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts/"+source.ID+"/browser-replay/sessions",
		bytes.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want retained import replay accepted", resp.Code, resp.Body.String())
	}
	if strings.Contains(resp.Body.String(), password) {
		t.Fatalf("browser replay response disclosed retained password: %s", resp.Body.String())
	}
	var payload BrowserReplayStartResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode replay response: %v", err)
	}
	session, ok := server.browserReplay.Get(payload.ReplaySessionID)
	if !ok || session == nil {
		t.Fatal("browser replay session was not retained")
	}
	if _, ok := server.browserReplay.Stop(payload.ReplaySessionID); !ok {
		t.Fatal("stop browser replay returned not found")
	}
}

func TestHandleStartBrowserReplayScriptRejectsStoredProjectMismatch(t *testing.T) {
	server := NewServer(ServerConfig{})
	stored := server.runner.StoreScript(&dsl.Script{
		Name:      "Browser Script",
		ProjectID: "project-browser-stored",
		Protocol:  "browser",
		Version:   "1.0.0",
		Config:    dsl.ScriptConfig{Browser: dsl.BrowserConfig{Engine: "chromium"}},
		Actions:   []dsl.Action{{Name: "Capture current page", Type: "browser_screenshot"}},
	})

	body, _ := json.Marshal(map[string]any{
		"project_id": "project-other",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/"+stored.ID+"/browser-replay/sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "project_id does not match stored script project") {
		t.Fatalf("body = %q, want stored project mismatch", resp.Body.String())
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
	session, err := manager.Start("", &dsl.Script{
		Name:     "Browser Session",
		Protocol: protocolName,
		Version:  "1.0.0",
		Actions:  []dsl.Action{{Name: "shot", Type: "browser_screenshot"}},
	}, BrowserReplayStartRequest{ReplaySessionID: "session-events"})
	if err != nil {
		t.Fatal(err)
	}
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
	if got.CurrentEvent != nil || len(got.Events) != 0 {
		t.Fatalf("completed session duplicated terminal event payload: current=%#v events=%#v", got.CurrentEvent, got.Events)
	}
	if got.FinalReport == nil || len(got.FinalReport.Events) != 1 || got.FinalReport.Events[0].Screenshot != "c2NyZWVu" || got.FinalReport.Events[0].PageTitle != "Example" {
		t.Fatalf("report events = %#v, want authoritative retained screenshot event", got.FinalReport)
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

func TestBrowserReplayManagerPrunesTerminalSessions(t *testing.T) {
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	terminalAt := now.Add(-2 * time.Hour)
	manager := NewBrowserReplayManager(nil)
	manager.retention = replaySessionRetentionConfig{
		terminalTTL: time.Hour,
		idleCancel:  time.Hour,
		maxSessions: 250,
		now:         func() time.Time { return now },
	}
	manager.sessions["old"] = &BrowserReplaySession{
		ID:             "old",
		Status:         BrowserReplayCompleted,
		StartedAt:      &terminalAt,
		CompletedAt:    &terminalAt,
		lastActivityAt: terminalAt,
		terminalAt:     &terminalAt,
	}

	if removed := manager.PruneExpired(); removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if _, ok := manager.Get("old"); ok {
		t.Fatal("expected old terminal session to be pruned")
	}
}

// Pruning a retained tombstone must perform the durable lease release after
// dropping the manager mutex: in HA mode the release is a synchronous store
// call and running it under m.mu freezes every handler and progress path.
func TestBrowserReplayManagerPruneReleasesRetainedLeaseOffManagerLock(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	terminalAt := now.Add(-2 * time.Hour)
	manager := NewBrowserReplayManager(nil)
	manager.retention = replaySessionRetentionConfig{
		terminalTTL: time.Hour,
		idleCancel:  time.Hour,
		maxSessions: 250,
		now:         func() time.Time { return now },
	}
	released := false
	releasedOffLock := false
	signal := newReplaySessionLeaseSignal()
	signal.releaseRetainedIDFn = func() {
		released = true
		if manager.mu.TryLock() {
			releasedOffLock = true
			manager.mu.Unlock()
		}
	}
	manager.sessions["old"] = &BrowserReplaySession{
		ID:             "old",
		Status:         BrowserReplayCompleted,
		StartedAt:      &terminalAt,
		CompletedAt:    &terminalAt,
		lastActivityAt: terminalAt,
		terminalAt:     &terminalAt,
		reservation:    signal,
	}

	if removed := manager.PruneExpired(); removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if !released {
		t.Fatal("retained lease was not released by prune")
	}
	if !releasedOffLock {
		t.Fatal("retained lease release ran while the manager mutex was held")
	}
}

// The completion hook performs storage writes and must run without the manager
// lock held; acquiring the write lock from inside the hook deadlocks if the
// hook is still invoked under m.mu.RLock.
func TestBrowserReplayManagerCompletionHookRunsOffManagerLock(t *testing.T) {
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})
	manager := NewBrowserReplayManager(NewHub())
	t.Cleanup(manager.Close)
	hookDone := make(chan struct{})
	manager.setCompletionHook(func(_ context.Context, projectID string, script *dsl.Script, report *engine.ReplayReport) {
		manager.mu.Lock() //nolint:staticcheck // deadlocks (and fails the test) if the hook runs under m.mu.RLock
		manager.mu.Unlock()
		close(hookDone)
	})

	if _, err := manager.Start("hook-script", &dsl.Script{
		Name: "hook", Protocol: protocolName, ProjectID: "project-a",
		Actions: []dsl.Action{{Name: "step", Type: "request", Target: "/step"}},
	}, BrowserReplayStartRequest{ReplaySessionID: "hook-session"}); err != nil {
		t.Fatalf("start browser replay: %v", err)
	}
	select {
	case <-hookDone:
	case <-time.After(2 * time.Second):
		t.Fatal("completion hook did not run off the manager lock")
	}
}

// A worker that ignores cancellation past the abandon grace period must lose
// its slot and lease. The failed terminal transition is pushed before the
// capability is retired, and a late finalization must not corrupt a replacement
// that reuses the released ID.
func TestBrowserReplayManagerAbandonBackstopFreesSlotAndLease(t *testing.T) {
	started := make(chan struct{})
	unblock := make(chan struct{})
	var unblockOnce sync.Once
	releaseWorker := func() { unblockOnce.Do(func() { close(unblock) }) }
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		close(started)
		<-unblock // Deliberately ignore cancellation.
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})
	hub := NewHub()
	manager := NewBrowserReplayManager(hub)
	manager.retention.maxSessions = 1
	manager.retention.workerAbandonGrace = 50 * time.Millisecond
	t.Cleanup(manager.Close)
	t.Cleanup(releaseWorker) // run before Close so the bounded drain is fast

	var activeReleased, allReleased atomic.Bool
	manager.setSessionIDReserver(func(_ context.Context, requested, projectID string) (string, func(), *replaySessionLeaseSignal, error) {
		signal := newReplaySessionLeaseSignal()
		signal.releaseAllFn = func() { allReleased.Store(true) }
		return requested, func() { activeReleased.Store(true) }, signal, nil
	})

	snapshot, err := manager.Start("wedged-script", &dsl.Script{
		Name: "wedged", Protocol: protocolName, ProjectID: "project-a",
		Actions: []dsl.Action{{Name: "blocked", Type: "request", Target: "/blocked"}},
	}, BrowserReplayStartRequest{ReplaySessionID: "wedged-session"})
	if err != nil {
		t.Fatalf("start wedged replay: %v", err)
	}
	<-started
	manager.mu.RLock()
	runtime := manager.sessions[snapshot.ID]
	manager.mu.RUnlock()
	if runtime == nil {
		t.Fatal("runtime session was not retained by manager")
	}

	if _, ok := manager.Stop(snapshot.ID); !ok {
		t.Fatal("stop wedged replay returned not found")
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		runtime.mu.RLock()
		status, errMsg, abandoned := runtime.Status, runtime.Error, runtime.workerAbandoned
		runtime.mu.RUnlock()
		if abandoned && status == BrowserReplayFailed &&
			strings.Contains(errMsg, "ignored cancellation") &&
			activeReleased.Load() && allReleased.Load() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("session status=%q error=%q abandoned=%t active_released=%t lease_released=%t; backstop never fully orphaned the wedged worker",
				status, errMsg, abandoned, activeReleased.Load(), allReleased.Load())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, ok := manager.Get(snapshot.ID); ok {
		t.Fatal("orphaned session remained addressable after its global capability was released")
	}

	terminalPublished := false
	for !terminalPublished {
		select {
		case message := <-hub.broadcast:
			var envelope struct {
				Type            string                `json:"type"`
				ReplaySessionID string                `json:"replay_session_id"`
				Session         *BrowserReplaySession `json:"session"`
			}
			if err := json.Unmarshal(message.payload, &envelope); err != nil {
				t.Fatalf("decode browser replay broadcast: %v", err)
			}
			if envelope.Type != "browser_replay_failed" || envelope.ReplaySessionID != snapshot.ID {
				continue
			}
			if envelope.Session == nil || envelope.Session.Status != BrowserReplayFailed ||
				!strings.Contains(envelope.Session.Error, "ignored cancellation") {
				t.Fatalf("terminal broadcast = %#v, want orphaned failure snapshot", envelope)
			}
			terminalPublished = true
		case <-time.After(2 * time.Second):
			t.Fatal("orphaned terminal state was not published before capability retirement")
		}
	}

	// While the worker is still wedged, both its capacity slot and externally
	// supplied ID must already be reusable without a restart.
	replacement, err := manager.Start("replacement-script", &dsl.Script{
		Name: "replacement", Protocol: protocolName, ProjectID: "project-a",
	}, BrowserReplayStartRequest{ReplaySessionID: snapshot.ID, StartPaused: true})
	if err != nil {
		t.Fatalf("start exact-ID replacement after backstop = %v, want a free slot and released capability", err)
	}
	if replacement.ID != snapshot.ID {
		t.Fatalf("replacement id = %q, want released id %q", replacement.ID, snapshot.ID)
	}

	// A late-finishing worker must detect the takeover and no-op.
	releaseWorker()
	select {
	case <-runtime.workerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("wedged worker did not finish after release")
	}
	runtime.mu.RLock()
	status, errMsg, finalReport := runtime.Status, runtime.Error, runtime.FinalReport
	runtime.mu.RUnlock()
	if status != BrowserReplayFailed || !strings.Contains(errMsg, "ignored cancellation") {
		t.Fatalf("late worker overwrote orphaned state: status=%q error=%q", status, errMsg)
	}
	if finalReport != nil {
		t.Fatal("late worker attached its report to the orphaned session")
	}
	if current, ok := manager.Get(snapshot.ID); !ok || current.Status != BrowserReplayPaused {
		t.Fatalf("late worker corrupted replacement: session=%#v addressable=%t", current, ok)
	}
}

func TestBrowserReplayManagerSessionCapEvictsOldestTerminal(t *testing.T) {
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	manager := NewBrowserReplayManager(nil)
	manager.retention = replaySessionRetentionConfig{
		terminalTTL: time.Hour,
		idleCancel:  time.Hour,
		maxSessions: 2,
		now:         func() time.Time { return now },
	}
	for i, id := range []string{"oldest", "middle", "newest"} {
		terminalAt := now.Add(-time.Duration(3-i) * time.Minute)
		manager.sessions[id] = &BrowserReplaySession{
			ID:             id,
			Status:         BrowserReplayCompleted,
			StartedAt:      &terminalAt,
			CompletedAt:    &terminalAt,
			lastActivityAt: terminalAt,
			terminalAt:     &terminalAt,
		}
	}

	if removed := manager.PruneExpired(); removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if _, ok := manager.sessions["oldest"]; ok {
		t.Fatal("expected oldest terminal session to be evicted")
	}
	if len(manager.sessions) != 2 {
		t.Fatalf("session count = %d, want 2", len(manager.sessions))
	}
}

func TestBrowserReplayManagerGetDoesNotSweepUnrelatedSessions(t *testing.T) {
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	expiredAt := now.Add(-2 * time.Hour)
	manager := NewBrowserReplayManager(nil)
	manager.retention = replaySessionRetentionConfig{
		terminalTTL: time.Hour,
		idleCancel:  time.Hour,
		maxSessions: 250,
		now:         func() time.Time { return now },
	}
	manager.sessions["active"] = &BrowserReplaySession{
		ID:             "active",
		Status:         BrowserReplayRunning,
		lastActivityAt: now.Add(-time.Minute),
	}
	manager.sessions["expired"] = &BrowserReplaySession{
		ID:             "expired",
		Status:         BrowserReplayCompleted,
		CompletedAt:    &expiredAt,
		terminalAt:     &expiredAt,
		lastActivityAt: expiredAt,
	}

	if session, ok := manager.Get("active"); !ok || session == nil || session.ID != "active" {
		t.Fatalf("Get(active) = %#v, %t", session, ok)
	}
	if _, exists := manager.sessions["expired"]; !exists {
		t.Fatal("Get(active) swept an unrelated expired session")
	}
	if got := manager.sessions["active"].lastActivityAt; !got.Equal(now) {
		t.Fatalf("active session last activity = %s, want %s", got, now)
	}
	if removed := manager.PruneExpired(); removed != 1 {
		t.Fatalf("janitor prune removed %d sessions, want 1", removed)
	}
	if _, exists := manager.sessions["expired"]; exists {
		t.Fatal("janitor prune retained expired session")
	}
}

func TestBrowserReplayManagerSessionCapPreservesNonTerminalSessionsWhenOverCap(t *testing.T) {
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	cancelled := make(chan string, 1)
	manager := NewBrowserReplayManager(nil)
	manager.retention = replaySessionRetentionConfig{
		terminalTTL: time.Hour,
		idleCancel:  time.Hour,
		maxSessions: 2,
		now:         func() time.Time { return now },
	}
	for i, id := range []string{"oldest", "middle", "newest"} {
		lastActivity := now.Add(-time.Duration(3-i) * time.Minute)
		sessionID := id
		manager.sessions[id] = &BrowserReplaySession{
			ID:             id,
			Status:         BrowserReplayRunning,
			StartedAt:      &lastActivity,
			lastActivityAt: lastActivity,
			cancel:         func() { cancelled <- sessionID },
			stateChanged:   make(chan struct{}),
		}
	}

	if removed := manager.PruneExpired(); removed != 0 {
		t.Fatalf("removed = %d, want 0", removed)
	}
	if len(manager.sessions) != 3 {
		t.Fatalf("session count = %d, want 3", len(manager.sessions))
	}
	for _, id := range []string{"oldest", "middle", "newest"} {
		if _, ok := manager.sessions[id]; !ok {
			t.Fatalf("active session %q was evicted", id)
		}
	}
	select {
	case id := <-cancelled:
		t.Fatalf("active session %q was cancelled to enforce the cap", id)
	default:
	}
}

func TestBrowserReplayManagerCancelsIdleRunningSession(t *testing.T) {
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	lastActivity := now.Add(-2 * time.Minute)
	cancelled := make(chan struct{})
	manager := NewBrowserReplayManager(nil)
	manager.retention = replaySessionRetentionConfig{
		terminalTTL: time.Hour,
		idleCancel:  time.Minute,
		maxSessions: 250,
		now:         func() time.Time { return now },
	}
	manager.sessions["idle"] = &BrowserReplaySession{
		ID:             "idle",
		Status:         BrowserReplayRunning,
		StartedAt:      &lastActivity,
		lastActivityAt: lastActivity,
		cancel:         func() { close(cancelled) },
	}

	if removed := manager.PruneExpired(); removed != 0 {
		t.Fatalf("removed = %d, want 0", removed)
	}
	select {
	case <-cancelled:
	default:
		t.Fatal("expected idle session cancel function to be called")
	}
	session, ok := manager.Get("idle")
	if !ok {
		t.Fatal("idle session should remain fetchable until terminal TTL expires")
	}
	if session.Status != BrowserReplayStopped {
		t.Fatalf("status = %q, want stopped", session.Status)
	}
}

func TestBrowserReplayManagerCancelsIdlePausedSession(t *testing.T) {
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	lastActivity := now.Add(-2 * time.Minute)
	cancelled := make(chan struct{})
	manager := NewBrowserReplayManager(nil)
	manager.retention = replaySessionRetentionConfig{
		terminalTTL: time.Hour,
		idleCancel:  time.Minute,
		maxSessions: 250,
		now:         func() time.Time { return now },
	}
	manager.sessions["paused-idle"] = &BrowserReplaySession{
		ID:             "paused-idle",
		Status:         BrowserReplayPaused,
		StartedAt:      &lastActivity,
		lastActivityAt: lastActivity,
		cancel:         func() { close(cancelled) },
		stateChanged:   make(chan struct{}),
	}

	if removed := manager.PruneExpired(); removed != 0 {
		t.Fatalf("removed = %d, want 0", removed)
	}
	select {
	case <-cancelled:
	default:
		t.Fatal("expected paused idle session cancel function to be called")
	}
	session, ok := manager.Get("paused-idle")
	if !ok {
		t.Fatal("paused idle session should remain fetchable until terminal TTL expires")
	}
	if session.Status != BrowserReplayStopped {
		t.Fatalf("status = %q, want stopped", session.Status)
	}
}

func TestBrowserReplayManagerDoesNotCancelRecentlyActivePausedSession(t *testing.T) {
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	lastActivity := now.Add(-30 * time.Second)
	cancelled := make(chan struct{})
	manager := NewBrowserReplayManager(nil)
	manager.retention = replaySessionRetentionConfig{
		terminalTTL: time.Hour,
		idleCancel:  time.Minute,
		maxSessions: 250,
		now:         func() time.Time { return now },
	}
	manager.sessions["paused-active"] = &BrowserReplaySession{
		ID:             "paused-active",
		Status:         BrowserReplayPaused,
		StartedAt:      &lastActivity,
		lastActivityAt: lastActivity,
		cancel:         func() { close(cancelled) },
		stateChanged:   make(chan struct{}),
	}

	if removed := manager.PruneExpired(); removed != 0 {
		t.Fatalf("removed = %d, want 0", removed)
	}
	select {
	case <-cancelled:
		t.Fatal("recently active paused session should not be cancelled")
	default:
	}
	session, ok := manager.Get("paused-active")
	if !ok {
		t.Fatal("paused active session should remain fetchable")
	}
	if session.Status != BrowserReplayPaused {
		t.Fatalf("status = %q, want paused", session.Status)
	}
}

func TestBrowserReplayManagerStartReusedIDRejectsCollision(t *testing.T) {
	const sessionID = "33333333-3333-4333-8333-333333333333"
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	cancelled := make(chan struct{})
	manager := NewBrowserReplayManager(nil)
	manager.retention = replaySessionRetentionConfig{
		terminalTTL: time.Hour,
		idleCancel:  time.Hour,
		maxSessions: 250,
		now:         func() time.Time { return now },
	}
	manager.sessions[sessionID] = &BrowserReplaySession{
		ID:             sessionID,
		ProjectID:      "project-a",
		Status:         BrowserReplayRunning,
		StartedAt:      &now,
		lastActivityAt: now,
		cancel:         func() { close(cancelled) },
	}

	started, err := manager.Start("", &dsl.Script{Name: "Replacement", Version: "1.0.0", Protocol: "browser"}, BrowserReplayStartRequest{
		ReplaySessionID: sessionID,
		StartPaused:     true,
	})
	if !errors.Is(err, errBrowserReplaySessionIDConflict) || started != nil {
		t.Fatalf("Start collision = session %#v, error %v", started, err)
	}
	select {
	case <-cancelled:
		t.Fatal("existing session was cancelled by colliding start")
	default:
	}
	session, ok := manager.Get(sessionID)
	if !ok {
		t.Fatal("existing session not found")
	}
	if session.Status != BrowserReplayRunning {
		t.Fatalf("existing status = %q, want running", session.Status)
	}
	_, _ = manager.Stop(sessionID)
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
	session, err := manager.Start("", &dsl.Script{
		Name:     "Browser Session",
		Protocol: protocolName,
		Version:  "1.0.0",
		Actions: []dsl.Action{
			{Name: "first", Type: "browser_wait_for_timeout", Value: "25"},
			{Name: "second", Type: "browser_wait_for_timeout", Value: "25"},
		},
	}, BrowserReplayStartRequest{ReplaySessionID: "session-paused-stop"})
	if err != nil {
		t.Fatal(err)
	}
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

	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
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
	session, err := server.browserReplay.Start("", script, BrowserReplayStartRequest{
		ReplaySessionID: "browser-replay-start-paused",
		StartPaused:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
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
