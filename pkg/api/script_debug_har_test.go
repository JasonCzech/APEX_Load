package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

func retainedDebugHARSession() *ScriptDebugSession {
	now := time.Now().UTC()
	return &ScriptDebugSession{ID: debugAPITestSessionID, Protocol: "http", Status: ScriptDebugCompleted,
		StartedAt: &now, CompletedAt: &now, terminalAt: &now, lastActivityAt: now,
		FinalReport: &engine.ReplayReport{CompletedAt: now, Events: []engine.ReplayActionEvent{{StartedAt: now,
			Network: []protocol.NetworkEntry{{URL: "https://example.test/orders?token=private-value", Method: "GET", StatusCode: 200}}}}},
	}
}

func TestScriptDebugHARAvailability(t *testing.T) {
	for _, tc := range []struct {
		name      string
		change    func(*ScriptDebugSession)
		available bool
		eligible  bool
	}{
		{"completed", func(*ScriptDebugSession) {}, true, true},
		{"failed", func(s *ScriptDebugSession) { s.Status = ScriptDebugFailed }, true, true},
		{"stopped", func(s *ScriptDebugSession) { s.Status = ScriptDebugStopped }, true, true},
		{"paused", func(s *ScriptDebugSession) { s.Status = ScriptDebugPaused }, false, true},
		{"no final report", func(s *ScriptDebugSession) { s.FinalReport = nil }, false, true},
		{"no traffic", func(s *ScriptDebugSession) { s.FinalReport.Events = nil }, false, true},
		{"invalid traffic", func(s *ScriptDebugSession) { s.FinalReport.Events[0].Network[0].URL = "/relative" }, false, true},
		{"disclosure unavailable", func(s *ScriptDebugSession) { s.RuntimeEvidenceUnavailable = true }, false, true},
		{"report unavailable", func(s *ScriptDebugSession) { s.FinalReport.RuntimeEvidenceUnavailable = true }, false, true},
		{"non web", func(s *ScriptDebugSession) { s.Protocol = "database" }, false, false},
		{"browser", func(s *ScriptDebugSession) { s.Protocol = "browser" }, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := retainedDebugHARSession()
			tc.change(session)
			info := scriptDebugHARAvailability(session)
			if info.Available != tc.available || info.Eligible != tc.eligible {
				t.Fatalf("availability = %+v", info)
			}
			if !info.Available && info.Reason == "" {
				t.Fatal("unavailable export needs a reason")
			}
		})
	}
}

func TestScriptDebugHARExportAuthorizationExpiryAndAliases(t *testing.T) {
	for _, route := range []string{"script-debug", "replay"} {
		t.Run(route, func(t *testing.T) {
			manager := NewScriptDebugManager(NewHub())
			session := retainedDebugHARSession()
			manager.sessions[session.ID] = session
			server := &Server{scriptDebug: manager}
			request := httptest.NewRequest("GET", "/api/v1/"+route+"/sessions/"+session.ID+"/export?format=har", nil)
			request.SetPathValue("session_id", session.ID)
			recorder := httptest.NewRecorder()
			server.handleExportScriptDebugSession(recorder, request)
			if recorder.Code != 200 || !strings.Contains(recorder.Header().Get("Content-Disposition"), ".har") {
				t.Fatalf("export: %d %s", recorder.Code, recorder.Body.String())
			}
			var har struct {
				Log struct {
					Version string            `json:"version"`
					Entries []json.RawMessage `json:"entries"`
				} `json:"log"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &har); err != nil || har.Log.Version != "1.2" || len(har.Log.Entries) != 1 {
				t.Fatalf("invalid HAR: %v %s", err, recorder.Body.String())
			}
			if strings.Contains(recorder.Body.String(), "private-value") {
				t.Fatal("HAR leaked a credential")
			}
			session.ProjectID = "another-project"
			if _, _, ok := manager.ExportHARForProject(session.ID, "wrong-project"); ok {
				t.Fatal("cross-project export")
			}
			recorder = httptest.NewRecorder()
			server.handleExportScriptDebugSession(recorder, request.WithContext(context.WithValue(request.Context(), authorizedProjectContextKey{}, "wrong-project")))
			if recorder.Code != 404 {
				t.Fatalf("inaccessible: %d", recorder.Code)
			}
			session.ProjectID = ""
			session.RuntimeEvidenceUnavailable = true
			recorder = httptest.NewRecorder()
			server.handleExportScriptDebugSession(recorder, request)
			if recorder.Code != 409 {
				t.Fatalf("unavailable: %d", recorder.Code)
			}
			session.RuntimeEvidenceUnavailable = false
			session.Status = ScriptDebugRunning
			recorder = httptest.NewRecorder()
			server.handleExportScriptDebugSession(recorder, request)
			if recorder.Code != 409 {
				t.Fatalf("active: %d", recorder.Code)
			}
			session.Status = ScriptDebugCompleted
			past := time.Now().Add(-2 * manager.retention.terminalTTL)
			session.terminalAt = &past
			if _, _, ok := manager.ExportHARForProject(session.ID, ""); ok {
				t.Fatal("expired export")
			}
		})
	}
}

func TestScriptDebugHARRoutesUseSessionAuthorization(t *testing.T) {
	server, _ := baselineTestServer(t)
	enableTrustedHeaderAuth(t, server)
	session := retainedDebugHARSession()
	session.ProjectID = storage.DefaultProjectID
	server.scriptDebug.mu.Lock()
	server.scriptDebug.sessions[session.ID] = session
	server.scriptDebug.mu.Unlock()
	for _, prefix := range []string{"script-debug", "replay"} {
		path := "/api/v1/" + prefix + "/sessions/" + session.ID + "/export?format=har"
		reply := harDownloadRequest(t, server, path, "debug-har-editor", harEditor)
		if reply.Code != 200 {
			t.Fatalf("%s: %d %s", prefix, reply.Code, reply.Body.String())
		}
		missing := harDownloadRequest(t, server, "/api/v1/"+prefix+"/sessions/33333333-3333-4333-8333-333333333333/export?format=har", "debug-har-editor", harEditor)
		if missing.Code != 404 {
			t.Fatalf("missing %s: %d", prefix, missing.Code)
		}
	}
}

func TestScriptDebugHARAccessMaskPreservesOtherResponses(t *testing.T) {
	for _, tc := range []struct {
		format       string
		status, want int
	}{
		{"har", 403, 404}, {" HAR ", 403, 404}, {"json", 403, 403}, {"log", 403, 403}, {"har", 401, 401}, {"har", 409, 409}, {"har", 200, 200},
	} {
		t.Run(tc.format+strconv.Itoa(tc.status), func(t *testing.T) {
			handler := hideHARAccessDenials(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeError(w, tc.status, "original response") }))
			request := httptest.NewRequest("GET", "/export?format="+url.QueryEscape(tc.format), nil)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != tc.want {
				t.Fatalf("status=%d want=%d", recorder.Code, tc.want)
			}
			if tc.want == 404 && strings.Contains(recorder.Body.String(), "original response") {
				t.Fatal("access denial body was not masked")
			}
			if tc.want != 404 && !strings.Contains(recorder.Body.String(), "original response") {
				t.Fatal("unrelated response changed")
			}
		})
	}
}

func TestScriptDebugHAROptionsValidation(t *testing.T) {
	for _, options := range []string{`{"bodies":"invalid"}`, `{"max_body_bytes":1048577}`, `{"max_total_body_bytes":-1}`} {
		server := NewServer(ServerConfig{})
		request := httptest.NewRequest("POST", "/api/v1/scripts/replay/sessions", strings.NewReader(`{"runtime_profile":{"har_options":`+options+`},"script":{"name":"HAR","version":"1.0","protocol":"http","actions":[]}}`))
		request.Header.Set("Content-Type", "application/json")
		reply := httptest.NewRecorder()
		server.mux.ServeHTTP(reply, request)
		if reply.Code != http.StatusBadRequest || !strings.Contains(reply.Body.String(), "HAR") {
			t.Fatalf("%d %s", reply.Code, reply.Body.String())
		}
	}
}
func TestScriptDebugHAROptionsMetadataAndExport(t *testing.T) {
	manager := NewScriptDebugManager(NewHub())
	session := retainedDebugHARSession()
	options, _ := (protocol.ReplayHAROptions{Bodies: "response", Redirects: true, Timings: true}).Normalize()
	session.RuntimeProfile.HAROptions = &options
	session.FinalReport.Events[0].Network[0].HAR = protocol.NewReplayHARExchange()
	session.FinalReport.Events[0].Network[0].HAR.ResponseBody = &protocol.ReplayHARBody{State: "retained", Text: `{"visible":"response","password":"private-password"}`, MimeType: "application/json"}
	manager.sessions[session.ID] = session
	raw, info, ok := manager.ExportHARForProject(session.ID, "")
	if !ok || !info.Available || info.Options == nil || info.Options.MaxBodyBytes != 65536 || !strings.Contains(string(raw), "response") || strings.Contains(string(raw), "private-password") {
		t.Fatalf("%+v %s", info, raw)
	}
	info.Options.Bodies = "none"
	if session.RuntimeProfile.HAROptions.Bodies != "response" {
		t.Fatal("options alias")
	}
}
