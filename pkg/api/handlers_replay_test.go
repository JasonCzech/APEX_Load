package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	browserprotocol "github.com/jasonczech/apexload/pkg/protocol/browser"
	"github.com/jasonczech/apexload/pkg/storage"
)

type replayAPIProtocol struct {
	execute func(action *protocol.Action) (*protocol.Result, error)
}

var replayAPIProtocolCounter atomic.Uint64

func (p *replayAPIProtocol) Name() string                                   { return "replay-api" }
func (p *replayAPIProtocol) Init(_ context.Context, _ map[string]any) error { return nil }
func (p *replayAPIProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	return p.execute(action)
}
func (p *replayAPIProtocol) Teardown(_ context.Context) error    { return nil }
func (p *replayAPIProtocol) HealthCheck(_ context.Context) error { return nil }

func registerReplayAPIProtocol(t *testing.T, execute func(action *protocol.Action) (*protocol.Result, error)) string {
	t.Helper()
	name := "replay-api-" + strings.ReplaceAll(strings.ToLower(t.Name()), "/", "-") + "-" + strconv.FormatUint(replayAPIProtocolCounter.Add(1), 10)
	protocol.Register(name, func() protocol.Protocol {
		return &replayAPIProtocol{execute: execute}
	})
	return name
}

func TestHandleReplayScriptReturnsStructuredReport(t *testing.T) {
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		switch action.Name {
		case "login":
			return &protocol.Result{Success: true, StatusCode: 200, Extractions: map[string]string{"session": "s-123"}}, nil
		case "profile":
			if action.Target != "/users/s-123" {
				return &protocol.Result{Success: false, StatusCode: 500, ErrorMessage: "unexpected target"}, nil
			}
			return &protocol.Result{Success: true, StatusCode: 200}, nil
		default:
			return &protocol.Result{Success: true, StatusCode: 204}, nil
		}
	})

	server := NewServer(ServerConfig{})
	stored := server.runner.StoreScript(&dsl.Script{
		Name:     "Replayable",
		Protocol: protocolName,
		Version:  "1.0.0",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions: []dsl.Action{
			{Name: "login", Type: "POST", Method: "POST", Target: "/login"},
			{Name: "profile", Type: "GET", Method: "GET", Target: "/users/{{var.session}}"},
		},
	})

	body, _ := json.Marshal(map[string]any{"group_name": "editor-replay"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/"+stored.ID+"/replay", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var payload struct {
		ScriptID string `json:"script_id"`
		Report   struct {
			Success bool   `json:"success"`
			Group   string `json:"group"`
			Events  []struct {
				ActionName string `json:"action_name"`
				Target     string `json:"target"`
				StatusCode int    `json:"status_code"`
			} `json:"events"`
			FinalVariables map[string]string `json:"final_variables"`
		} `json:"report"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}

	if payload.ScriptID != stored.ID {
		t.Fatalf("script_id = %q, want %q", payload.ScriptID, stored.ID)
	}
	if !payload.Report.Success {
		t.Fatal("report.success = false, want true")
	}
	if payload.Report.Group != "editor-replay" {
		t.Fatalf("group = %q, want editor-replay", payload.Report.Group)
	}
	if len(payload.Report.Events) != 2 {
		t.Fatalf("event count = %d, want 2", len(payload.Report.Events))
	}
	if payload.Report.Events[1].Target != "/users/[REDACTED]" {
		t.Fatalf("target = %q, want /users/[REDACTED]", payload.Report.Events[1].Target)
	}
	if payload.Report.FinalVariables["session"] != "[REDACTED]" {
		t.Fatalf("session = %q, want [REDACTED]", payload.Report.FinalVariables["session"])
	}
}

func TestHandleReplayScriptRejectsInvalidScript(t *testing.T) {
	server := NewServer(ServerConfig{})
	stored := server.runner.StoreScript(&dsl.Script{
		Name:     "Invalid Replayable",
		Protocol: "http",
		Version:  "1.0.0",
		Actions:  []dsl.Action{{Name: "login", Type: "GET", Target: "/login"}},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/"+stored.ID+"/replay", nil)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
}

func TestHandleReplayScriptUsesDraftOverride(t *testing.T) {
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		if action.Target != "/draft" {
			return &protocol.Result{Success: false, StatusCode: 500, ErrorMessage: "did not use draft target"}, nil
		}
		return &protocol.Result{Success: true, StatusCode: 200}, nil
	})

	server := NewServer(ServerConfig{})
	stored := server.runner.StoreScript(&dsl.Script{
		Name:     "Replayable Draft",
		Protocol: protocolName,
		Version:  "1.0.0",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions:  []dsl.Action{{Name: "saved", Type: "GET", Target: "/saved"}},
	})

	body, _ := json.Marshal(map[string]any{
		"script": map[string]any{
			"name":     stored.Name,
			"version":  stored.Version,
			"protocol": protocolName,
			"config": map[string]any{
				"base_url": "https://example.com",
			},
			"actions": []map[string]any{{
				"name":   "draft",
				"type":   "GET",
				"target": "/draft",
			}},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/"+stored.ID+"/replay", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var payload struct {
		Report struct {
			Events []struct {
				ActionName string `json:"action_name"`
				Target     string `json:"target"`
			} `json:"events"`
		} `json:"report"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if len(payload.Report.Events) != 1 {
		t.Fatalf("event count = %d, want 1", len(payload.Report.Events))
	}
	if payload.Report.Events[0].ActionName != "draft" || payload.Report.Events[0].Target != "/draft" {
		t.Fatalf("unexpected draft event: %#v", payload.Report.Events[0])
	}
}

func TestTransientReplayAndDebugDraftsRequireSecretBindingsBeforeExecution(t *testing.T) {
	const secret = "transient-inline-credential"
	server := NewServer(ServerConfig{})
	script := map[string]any{
		"name":       "Unsafe transient draft",
		"version":    "1",
		"protocol":   "http",
		"project_id": "transient-secret-project",
		"config":     map[string]any{"base_url": "https://example.test"},
		"actions": []map[string]any{{
			"name":   "request",
			"type":   "request",
			"method": "GET",
			"target": "https://example.test/path?api_key=" + secret,
		}},
	}
	body := map[string]any{
		"project_id": "transient-secret-project",
		"script":     script,
	}
	for _, path := range []string{
		"/api/v1/scripts/replay",
		"/api/v1/scripts/replay/sessions",
		"/api/v1/scripts/debug/sessions",
		"/api/v1/scripts/browser-replay/sessions",
	} {
		t.Run(path, func(t *testing.T) {
			payload, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(payload))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			server.mux.ServeHTTP(response, request)
			if response.Code != http.StatusUnprocessableEntity ||
				!strings.Contains(response.Body.String(), secretBindingsRequiredCode) ||
				strings.Contains(response.Body.String(), secret) {
				t.Fatalf("%s response = %d %s", path, response.Code, response.Body.String())
			}
		})
	}

	stored := server.runner.StoreScript(&dsl.Script{
		Name: "Stored safe script", Protocol: "http", ProjectID: "transient-secret-project",
		Config:  dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{Name: "safe", Type: "request", Method: http.MethodGet, Target: "/safe"}},
	})
	if stored == nil {
		t.Fatal("failed to seed stored script")
	}
	payload, err := json.Marshal(map[string]any{"script": script})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/"+stored.ID+"/replay", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(response.Body.String(), secretBindingsRequiredCode) ||
		strings.Contains(response.Body.String(), secret) {
		t.Fatalf("stored override response = %d %s", response.Code, response.Body.String())
	}
}

func TestHandleReplayScriptRejectsStoredProjectMismatch(t *testing.T) {
	server := NewServer(ServerConfig{})
	stored := server.runner.StoreScript(&dsl.Script{
		Name:      "Replayable Project",
		ProjectID: "project-stored",
		Protocol:  "http",
		Version:   "1.0.0",
		Config:    dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions:   []dsl.Action{{Name: "saved", Type: "GET", Target: "/saved"}},
	})

	body, _ := json.Marshal(map[string]any{
		"project_id": "project-other",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/"+stored.ID+"/replay", bytes.NewReader(body))
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

func TestHandleReplayScriptAppliesCustomRedactionRules(t *testing.T) {
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{
			Success:      true,
			StatusCode:   200,
			ResponseBody: `{"customer":"customer-1234"}`,
			Snapshot: &protocol.ErrorSnapshot{
				RequestURL:     "https://example.test/customers/customer-1234",
				RequestHeaders: map[string]string{"X-Customer": "customer-1234"},
				RequestBody:    "customer=customer-1234",
				ResponseBody:   `{"customer":"customer-1234"}`,
			},
		}, nil
	})

	server := NewServer(ServerConfig{})
	stored := server.runner.StoreScript(&dsl.Script{
		Name:     "Replayable Redacted",
		Protocol: protocolName,
		Version:  "1.0.0",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions: []dsl.Action{{
			Name:    "lookup",
			Type:    "GET",
			Target:  "/customers/customer-1234",
			Headers: map[string]string{"X-Customer": "customer-1234"},
			Body:    "customer=customer-1234",
		}},
	})

	body, _ := json.Marshal(map[string]any{
		"redaction_rules": []map[string]any{{"name": "customer id", "pattern": `customer-[0-9]+`}},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/"+stored.ID+"/replay", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var payload struct {
		Report struct {
			Events []struct {
				Target   string                  `json:"target"`
				Headers  map[string]string       `json:"headers"`
				Body     string                  `json:"body"`
				Snapshot *protocol.ErrorSnapshot `json:"snapshot"`
			} `json:"events"`
		} `json:"report"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if len(payload.Report.Events) != 1 {
		t.Fatalf("event count = %d, want 1", len(payload.Report.Events))
	}
	event := payload.Report.Events[0]
	if event.Target != "/customers/[REDACTED]" {
		t.Fatalf("target = %q, want /customers/[REDACTED]", event.Target)
	}
	if event.Headers["X-Customer"] != "[REDACTED]" {
		t.Fatalf("header X-Customer = %q, want [REDACTED]", event.Headers["X-Customer"])
	}
	if event.Body != "customer=[REDACTED]" {
		t.Fatalf("body = %q, want customer=[REDACTED]", event.Body)
	}
	if event.Snapshot == nil || event.Snapshot.ResponseBody != `{"customer":"[REDACTED]"}` {
		t.Fatalf("snapshot = %#v, want redacted response body", event.Snapshot)
	}
}

func TestHandleReplayScriptInheritsProjectRedactionRules(t *testing.T) {
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{
			Success:      true,
			StatusCode:   200,
			ResponseBody: `{"customer":"customer-1234"}`,
			Snapshot: &protocol.ErrorSnapshot{
				RequestURL:     "https://example.test/customers/customer-1234",
				RequestHeaders: map[string]string{"X-Customer": "customer-1234"},
				RequestBody:    "customer=customer-1234",
				ResponseBody:   `{"customer":"customer-1234"}`,
			},
		}, nil
	})

	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	if err := store.SaveProject(&storage.Project{
		ID:   "project-1",
		Name: "Payments",
		Settings: storage.ProjectSettings{
			RedactionRules: []engine.RedactionRule{{Name: "customer id", Pattern: `customer-[0-9]+`}},
		},
	}); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}

	server := NewServer(ServerConfig{Store: store})
	stored := server.runner.StoreScript(&dsl.Script{
		Name:      "Replayable Project Redacted",
		Protocol:  protocolName,
		Version:   "1.0.0",
		ProjectID: "project-1",
		Config:    dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions: []dsl.Action{{
			Name:    "lookup",
			Type:    "GET",
			Target:  "/customers/customer-1234",
			Headers: map[string]string{"X-Customer": "customer-1234"},
			Body:    "customer=customer-1234",
		}},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/"+stored.ID+"/replay", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var payload struct {
		Report struct {
			Events []struct {
				Target   string                  `json:"target"`
				Headers  map[string]string       `json:"headers"`
				Body     string                  `json:"body"`
				Snapshot *protocol.ErrorSnapshot `json:"snapshot"`
			} `json:"events"`
		} `json:"report"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if len(payload.Report.Events) != 1 {
		t.Fatalf("event count = %d, want 1", len(payload.Report.Events))
	}
	event := payload.Report.Events[0]
	if event.Target != "/customers/[REDACTED]" {
		t.Fatalf("target = %q, want /customers/[REDACTED]", event.Target)
	}
	if event.Headers["X-Customer"] != "[REDACTED]" {
		t.Fatalf("header X-Customer = %q, want [REDACTED]", event.Headers["X-Customer"])
	}
	if event.Body != "customer=[REDACTED]" {
		t.Fatalf("body = %q, want customer=[REDACTED]", event.Body)
	}
	if event.Snapshot == nil || event.Snapshot.ResponseBody != `{"customer":"[REDACTED]"}` {
		t.Fatalf("snapshot = %#v, want redacted response body", event.Snapshot)
	}
}

func TestHandleReplayDraftScriptRunsUnsavedDefinition(t *testing.T) {
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		if action.Target != "/unsaved" {
			return &protocol.Result{Success: false, StatusCode: 500, ErrorMessage: "wrong target"}, nil
		}
		return &protocol.Result{Success: true, StatusCode: 200}, nil
	})

	server := NewServer(ServerConfig{})
	body, _ := json.Marshal(map[string]any{
		"group_name":        "draft-run",
		"ignore_think_time": true,
		"project_id":        "project-replay-draft",
		"script": map[string]any{
			"name":     "Unsaved Draft",
			"version":  "1.0.0",
			"protocol": protocolName,
			"config": map[string]any{
				"base_url": "https://example.com",
			},
			"actions": []map[string]any{{
				"name":   "draft-step",
				"type":   "GET",
				"target": "/unsaved",
			}},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/replay", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var payload struct {
		ScriptID string `json:"script_id"`
		Report   struct {
			Group  string `json:"group"`
			Events []struct {
				ActionName string `json:"action_name"`
				Target     string `json:"target"`
			} `json:"events"`
		} `json:"report"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if payload.ScriptID != "" {
		t.Fatalf("script_id = %q, want empty string", payload.ScriptID)
	}
	if payload.Report.Group != "draft-run" {
		t.Fatalf("group = %q, want draft-run", payload.Report.Group)
	}
	if len(payload.Report.Events) != 1 || payload.Report.Events[0].Target != "/unsaved" {
		t.Fatalf("unexpected events: %#v", payload.Report.Events)
	}
}

func TestHandleReplayDraftScriptRejectsProjectMismatch(t *testing.T) {
	server := NewServer(ServerConfig{})
	body, _ := json.Marshal(map[string]any{
		"project_id": "project-request",
		"script": map[string]any{
			"name":       "Mismatched Draft",
			"version":    "1.0.0",
			"protocol":   "http",
			"project_id": "project-script",
			"actions": []map[string]any{{
				"name":   "draft-step",
				"type":   "GET",
				"target": "/",
			}},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/replay", bytes.NewReader(body))
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

func TestHandleReplayDraftScriptBroadcastsProgress(t *testing.T) {
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{Success: true, StatusCode: 200}, nil
	})

	server := NewServer(ServerConfig{})
	body, _ := json.Marshal(map[string]any{
		"replay_session_id": "session-123",
		"project_id":        "project-progress",
		"script": map[string]any{
			"name":     "Unsaved Draft",
			"version":  "1.0.0",
			"protocol": protocolName,
			"config": map[string]any{
				"base_url": "https://example.com",
			},
			"actions": []map[string]any{{
				"name":   "draft-step",
				"type":   "GET",
				"target": "/unsaved",
			}},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/replay", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var payload struct {
		ReplaySessionID string `json:"replay_session_id"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if payload.ReplaySessionID != "session-123" {
		t.Fatalf("replay_session_id = %q, want session-123", payload.ReplaySessionID)
	}

	types := make([]string, 0, 5)
	for i := 0; i < 5; i++ {
		msg := <-server.hub.broadcast
		var decoded struct {
			Type            string `json:"type"`
			EventID         string `json:"event_id"`
			ReplaySessionID string `json:"replay_session_id"`
			ScriptID        string `json:"script_id"`
			ProjectID       string `json:"project_id"`
			EventIndex      *int   `json:"event_index,omitempty"`
		}
		if err := json.Unmarshal(msg.payload, &decoded); err != nil {
			t.Fatalf("json.Unmarshal broadcast returned error: %v", err)
		}
		if decoded.ReplaySessionID != "session-123" {
			t.Fatalf("broadcast replay_session_id = %q, want session-123", decoded.ReplaySessionID)
		}
		if decoded.ScriptID != "" {
			t.Fatalf("broadcast script_id = %q, want empty", decoded.ScriptID)
		}
		if decoded.ProjectID != "project-progress" {
			t.Fatalf("broadcast project_id = %q, want project-progress", decoded.ProjectID)
		}
		types = append(types, decoded.Type)
		if decoded.Type == engine.ReplayProgressEventRun || decoded.Type == engine.ReplayProgressActionStarted || decoded.Type == engine.ReplayProgressActionCompleted {
			if decoded.EventIndex == nil || *decoded.EventIndex != 0 {
				t.Fatalf("event_index = %#v, want 0", decoded.EventIndex)
			}
			if decoded.EventID == "" {
				t.Fatalf("event_id is empty for %s", decoded.Type)
			}
		} else if decoded.EventIndex != nil {
			t.Fatalf("event_index should be omitted for %s, got %#v", decoded.Type, decoded.EventIndex)
		}
	}

	if !slices.Equal(types, []string{
		engine.ReplayProgressStarted,
		engine.ReplayProgressActionStarted,
		engine.ReplayProgressActionCompleted,
		engine.ReplayProgressEventRun,
		engine.ReplayProgressComplete,
	}) {
		t.Fatalf("broadcast types = %#v", types)
	}
}

func TestValidateScript_AllowsBrowserScreenshotWithoutTarget(t *testing.T) {
	issues := validateScript(&dsl.Script{
		Name:     "Browser Screenshot",
		Protocol: "browser",
		Actions: []dsl.Action{{
			Name: "Capture current page",
			Type: "browser_screenshot",
		}},
	})

	if len(issues) != 0 {
		t.Fatalf("issues = %v, want none", issues)
	}
}

func TestValidateScriptForReplayBlocksReviewGatedJavaScriptUntilApproved(t *testing.T) {
	script := &dsl.Script{
		Name:     "Review Gated Browser Script",
		Protocol: "browser",
		Actions: []dsl.Action{{
			Name:   "Custom JS",
			Type:   "browser_evaluate_js",
			Target: "window.checkoutReady === true",
			Metadata: map[string]any{
				"review_required":   true,
				"review_gate":       "javascript",
				"promotion_blocker": true,
			},
		}},
	}

	if issues := validateScript(script); len(issues) != 0 {
		t.Fatalf("structural issues = %v, want none", issues)
	}

	issues := validateScriptForReplay(script)
	if len(issues) != 1 || !strings.Contains(issues[0], "allow_reviewed_javascript=true") {
		t.Fatalf("replay issues = %v, want reviewed JavaScript gate", issues)
	}
	preflight := buildReplayPreflight(script, nil, validateScript(script), nil)
	if preflight.Valid {
		t.Fatalf("preflight.Valid = true, want false for review-gated JavaScript")
	}
	if status := replayPreflightCheckStatus(preflight, "review_gates"); status != "fail" {
		t.Fatalf("review_gates status = %q, want fail", status)
	}
	if status := replayPreflightCheckStatus(preflight, "evidence_capture"); status != "pass" {
		t.Fatalf("evidence_capture status = %q, want pass", status)
	}

	script.Config.Browser.ReviewPolicy.AllowReviewedJavaScript = true
	if issues := validateScriptForReplay(script); len(issues) != 0 {
		t.Fatalf("approved replay issues = %v, want none", issues)
	}
	preflight = buildReplayPreflight(script, nil, validateScript(script), nil)
	if !preflight.Valid {
		t.Fatalf("approved preflight.Valid = false, issues=%v", preflight.Issues)
	}
	if status := replayPreflightCheckStatus(preflight, "review_gates"); status != "pass" {
		t.Fatalf("approved review_gates status = %q, want pass", status)
	}
	script.Config.Browser.CaptureScreenshots = "off"
	preflight = buildReplayPreflight(script, nil, validateScript(script), nil)
	if !preflight.Valid {
		t.Fatalf("screenshot warning preflight.Valid = false, issues=%v", preflight.Issues)
	}
	if status := replayPreflightCheckStatus(preflight, "evidence_capture"); status != "warn" {
		t.Fatalf("disabled screenshot evidence_capture status = %q, want warn", status)
	}
}

// TestReplayPreflightSecretMissWarnsNotInvalid preserves non-blocking import
// and authoring previews when no governed project resolver is intentionally
// available.
func TestReplayPreflightSecretMissWarnsNotInvalid(t *testing.T) {
	const secretName = "C6_UNSET_SECRET_FIXTURE"
	script := &dsl.Script{
		Name:     "Secret Replay",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://api.test"},
		Actions: []dsl.Action{{
			Name:    "Auth",
			Type:    "request",
			Method:  "GET",
			Target:  "/me",
			Headers: map[string]string{"Authorization": "Bearer {{secret." + secretName + "}}"},
		}},
	}

	preflight := buildReplayPreflight(script, nil, validateScript(script), nil)
	if !preflight.Valid {
		t.Fatalf("preflight.Valid = false for a generator-resolved secret; issues=%v", preflight.Issues)
	}
	if !stringSliceContainsSubstr(preflight.MissingSecrets, secretName) {
		t.Fatalf("MissingSecrets = %v, want it to include %q", preflight.MissingSecrets, secretName)
	}
	if status := replayPreflightCheckStatus(preflight, "secrets"); status != "warn" {
		t.Fatalf("secrets check status = %q, want warn", status)
	}
	if !anyStringContains(preflight.Warnings, secretName) {
		t.Fatalf("warnings = %v, want a secret-binding warning mentioning %q", preflight.Warnings, secretName)
	}
}

func TestReplayPreflightUsesGovernedProjectSecretResolver(t *testing.T) {
	script := &dsl.Script{
		Name:     "Managed Secret Replay",
		Protocol: "database",
		Config: dsl.ScriptConfig{
			Database: dsl.DatabaseConfig{DSN: "{{secret.DATABASE_DSN}}"},
		},
		Actions: []dsl.Action{{
			Name: "Query",
			Type: "db_query",
		}},
	}
	configured := engine.SecretResolverFunc(func(name string) (string, bool) {
		return "server-side-value", name == "DATABASE_DSN"
	})
	preflight := buildReplayPreflight(script, nil, nil, nil, configured)
	if !preflight.Valid {
		t.Fatalf("configured preflight.Valid = false: %#v", preflight)
	}
	if len(preflight.MissingSecrets) != 0 {
		t.Fatalf("configured MissingSecrets = %v", preflight.MissingSecrets)
	}
	if status := replayPreflightCheckStatus(preflight, "secrets"); status != "warn" {
		t.Fatalf("configured secrets status = %q, want warn for required binding", status)
	}

	missing := engine.SecretResolverFunc(func(string) (string, bool) {
		return "", false
	})
	preflight = buildReplayPreflight(script, nil, nil, nil, missing)
	if preflight.Valid {
		t.Fatal("missing governed project secret did not fail preflight")
	}
	if !slices.Contains(preflight.MissingSecrets, "DATABASE_DSN") {
		t.Fatalf("MissingSecrets = %v", preflight.MissingSecrets)
	}
	if status := replayPreflightCheckStatus(preflight, "secrets"); status != "fail" {
		t.Fatalf("missing secrets status = %q, want fail", status)
	}
}

func stringSliceContainsSubstr(values []string, needle string) bool {
	return anyStringContains(values, needle)
}

func anyStringContains(values []string, needle string) bool {
	for _, v := range values {
		if strings.Contains(v, needle) {
			return true
		}
	}
	return false
}

func TestReplayPreflightWalksNestedControlFlow(t *testing.T) {
	script := &dsl.Script{
		Name:     "Nested Control Replay",
		Protocol: "browser",
		Actions: []dsl.Action{
			{
				Name: "Ready Branch",
				Type: "control_if",
				Control: &dsl.ControlFlow{
					Condition: &dsl.Condition{Left: "{{var.ready}}", Op: "eq", Right: "true"},
					Steps: []dsl.Action{
						{
							Name:   "Create Cart",
							Type:   "browser_click",
							Target: "#create-cart",
							Extractors: []dsl.Extractor{{
								Name:       "cart_id",
								Source:     "body",
								Type:       "jsonpath",
								Expression: "$.cart_id",
							}},
						},
						{
							Name: "Add Items",
							Type: "control_loop",
							Control: &dsl.ControlFlow{
								Count: 3,
								Steps: []dsl.Action{{
									Name:   "Add SKU",
									Type:   "browser_click",
									Target: "#sku-{{var.sku}}-{{var.cart_id}}",
								}},
							},
						},
					},
					Else: []dsl.Action{{
						Name:   "Guest Cart",
						Type:   "browser_click",
						Target: "#guest-cart",
					}},
				},
			},
			{
				Name:   "Checkout",
				Type:   "browser_click",
				Target: "#checkout-{{var.cart_id}}",
			},
		},
	}

	preflight := buildReplayPreflight(script, map[string]string{"ready": "true"}, nil, nil)
	if preflight.ActionCount != 4 {
		t.Fatalf("ActionCount = %d, want 4 nested executable action definitions", preflight.ActionCount)
	}
	if preflight.ExpectedActionCount != 5 {
		t.Fatalf("ExpectedActionCount = %d, want 5 with fixed loop expansion", preflight.ExpectedActionCount)
	}
	if !slices.Contains(preflight.MissingVariables, "sku") {
		t.Fatalf("missing variables = %v, want nested sku", preflight.MissingVariables)
	}
	if slices.Contains(preflight.MissingVariables, "cart_id") {
		t.Fatalf("missing variables = %v, cart_id should be available from nested extractor", preflight.MissingVariables)
	}
}

func replayPreflightCheckStatus(preflight *ReplayPreflight, id string) string {
	if preflight == nil {
		return ""
	}
	for _, check := range preflight.Checks {
		if check.ID == id {
			return check.Status
		}
	}
	return ""
}

func TestValidateScript_JMSMockDoesNotRequireBaseURL(t *testing.T) {
	issues := validateScript(&dsl.Script{
		Name:     "Mock JMS",
		Protocol: "jms-solace",
		Config: dsl.ScriptConfig{
			JMS: dsl.JMSConfig{
				Destination: "orders.in",
				ReplyTo:     "orders.reply",
				Mock:        true,
			},
		},
		Actions: []dsl.Action{{
			Name: "Publish",
			Type: "jms_publish",
			Body: `{"orderId":"A-100"}`,
		}},
	})

	if len(issues) != 0 {
		t.Fatalf("issues = %v, want none", issues)
	}
}

func TestValidateScript_JMSRealAdapterUnavailable(t *testing.T) {
	issues := validateScript(&dsl.Script{
		Name:     "Real JMS",
		Protocol: "jms-solace",
		Config: dsl.ScriptConfig{
			JMS: dsl.JMSConfig{
				BrokerURL:   "tcps://broker.example.com:55443",
				MessageVPN:  "perf",
				Destination: "orders.in",
				Mock:        false,
			},
		},
		Actions: []dsl.Action{{
			Name: "Publish",
			Type: "jms_publish",
		}},
	})

	if len(issues) != 1 || !strings.Contains(issues[0], "real Solace JMS adapter is not available") {
		t.Fatalf("issues = %v, want unavailable adapter issue", issues)
	}
}

func TestStartBrowserReplayRequestIncludesWebKitWarningsOnValidationFailure(t *testing.T) {
	server := NewServer(ServerConfig{})
	body, err := json.Marshal(map[string]any{
		"script": dsl.Script{
			Name:     "Invalid WebKit Browser Draft",
			Version:  "1.0.0",
			Protocol: "browser",
			Config: dsl.ScriptConfig{
				Browser: dsl.BrowserConfig{Engine: "webkit"},
			},
			Actions: []dsl.Action{{Name: "Capture current page"}},
		},
	})
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/browser-replay/sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}

	warnings := extractValidationWarnings(t, resp.Body.Bytes())
	expected := browserprotocol.RuntimeInstallWarning("webkit")
	if expected == "" && len(warnings) != 0 {
		t.Fatalf("warnings = %#v, want none for an installed WebKit runtime", warnings)
	}
	if expected != "" && (len(warnings) != 1 || warnings[0] != expected) {
		t.Fatalf("warnings = %#v, want %#v", warnings, []string{expected})
	}
}

func TestStartBrowserReplayRequestIncludesRecordedResourceMetadataWarningsOnValidationFailure(t *testing.T) {
	server := NewServer(ServerConfig{})
	body, err := json.Marshal(map[string]any{
		"script": dsl.Script{
			Name:     "Invalid Recorded Resource Browser Draft",
			Version:  "1.0.0",
			Protocol: "browser",
			Config: dsl.ScriptConfig{
				Browser: dsl.BrowserConfig{
					Engine:            "chromium",
					DownloadResources: "recorded",
				},
			},
			Actions: []dsl.Action{{Name: "Capture current page"}},
		},
	})
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/browser-replay/sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}

	warnings := extractValidationWarnings(t, resp.Body.Bytes())
	if len(warnings) != 1 || !strings.Contains(strings.ToLower(warnings[0]), "recorded-resource metadata") {
		t.Fatalf("warnings = %#v, want recorded resource metadata guidance", warnings)
	}
}
