package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/testlog"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/recordbridge"
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
		startPausedBrowserReplayFixture(t, server, sessionID, "project-a")
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

		existing, ok := server.browserReplay.Get(sessionID)
		if !ok || existing.ProjectID != "project-a" || existing.Status != BrowserReplayPaused {
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
		retention := recordbridge.DefaultReplayRetentionConfig()
		retention.MaxSessions = 1
		replaceReplayFixtureManager(t, server, recordbridge.ReplayManagerOptions{Retention: &retention})
		startPausedBrowserReplayFixture(t, server, establishedSessionID, "project-a")

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

		existing, ok := server.browserReplay.Get(establishedSessionID)
		if !ok || existing.ProjectID != "project-a" || existing.Status != BrowserReplayPaused {
			t.Fatalf("existing session = %#v, want untouched project-a session", existing)
		}
		if _, ok := server.browserReplay.Get(newSessionID); ok {
			t.Fatal("capacity-rejected browser replay session was retained")
		}

		_, _ = server.browserReplay.Stop(establishedSessionID)
		waitForBrowserReplayFixture(t, server.browserReplay, establishedSessionID)
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

// A refusal from the script's review gates lists each gate, so the dashboard
// can group them instead of showing one long sentence.
func TestWriteBrowserReplayStartErrorListsTheBlockingGates(t *testing.T) {
	script := &dsl.Script{Name: "HAR", Protocol: "http", Actions: []dsl.Action{
		{Name: "POST ctp", Type: "http_post", Target: "https://a.example.test/ctp", Metadata: map[string]any{"execution_blocker": "credential_json_invalid_utf8"}},
		{Name: "GET page", Type: "http_get", Target: "https://www.example.test/"},
		{Name: "POST collect", Type: "http_post", Target: "https://b.example.test/collect", Metadata: map[string]any{"execution_blockers": []any{"import_review_required"}}},
	}}
	err := replayReviewGateError(script)
	if !errors.Is(err, errScriptReplayBlocked) {
		t.Fatalf("gate error = %v, want errScriptReplayBlocked", err)
	}
	recorder := httptest.NewRecorder()
	writeBrowserReplayStartError(recorder, err)
	var body struct {
		Error  string   `json:"error"`
		Code   string   `json:"code"`
		Issues []string `json:"issues"`
	}
	if decodeErr := json.Unmarshal(recorder.Body.Bytes(), &body); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if recorder.Code != http.StatusConflict || body.Code != "script_replay_blocked" || len(body.Issues) != 2 {
		t.Fatalf("response = %d %+v, want 409 script_replay_blocked with 2 issues", recorder.Code, body)
	}
	if !strings.HasPrefix(body.Issues[0], `actions[0] "POST ctp": execution blocker credential_json_invalid_utf8`) ||
		!strings.HasPrefix(body.Issues[1], `actions[2] "POST collect": execution blocker import_review_required`) {
		t.Fatalf("issues = %q", body.Issues)
	}
	// Older clients still read the whole refusal from error.
	if !strings.Contains(body.Error, "script replay is blocked: actions[0]") {
		t.Fatalf("error = %q, want the full message", body.Error)
	}
}

// A replay the script's own review gates refuse is the caller's to resolve
// (409 with the reason); any other start failure is a 500 whose cause is
// logged under the response's ref and never sent.
func TestWriteBrowserReplayStartErrorExplainsTheCause(t *testing.T) {
	blocked := httptest.NewRecorder()
	writeBrowserReplayStartError(blocked, fmt.Errorf("%w: execution blocker import_review_required blocks replay", errScriptReplayBlocked))
	if blocked.Code != http.StatusConflict || !strings.Contains(blocked.Body.String(), "import_review_required") {
		t.Fatalf("blocked start = %d %s, want 409 with the blocker", blocked.Code, blocked.Body.String())
	}

	logs := &lockedBuffer{}
	testlog.SetDefault(t, slog.New(slog.NewJSONHandler(logs, nil)))
	failed := httptest.NewRecorder()
	writeBrowserReplayStartError(failed, errors.New("browser runtime missing"))
	if failed.Code != http.StatusInternalServerError || strings.Contains(failed.Body.String(), "runtime missing") {
		t.Fatalf("failed start = %d %s, want a 500 without the cause", failed.Code, failed.Body.String())
	}
	if !strings.Contains(logs.String(), "failed to start browser replay: browser runtime missing") {
		t.Fatalf("logs = %s, want the cause logged", logs.String())
	}
}
