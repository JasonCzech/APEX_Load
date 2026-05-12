package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/jasonczech/stormrunner/pkg/dsl"
	"github.com/jasonczech/stormrunner/pkg/engine"
	"github.com/jasonczech/stormrunner/pkg/protocol"
)

type replayAPIProtocol struct {
	execute func(action *protocol.Action) (*protocol.Result, error)
}

func (p *replayAPIProtocol) Name() string                                   { return "replay-api" }
func (p *replayAPIProtocol) Init(_ context.Context, _ map[string]any) error { return nil }
func (p *replayAPIProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	return p.execute(action)
}
func (p *replayAPIProtocol) Teardown(_ context.Context) error    { return nil }
func (p *replayAPIProtocol) HealthCheck(_ context.Context) error { return nil }

func registerReplayAPIProtocol(t *testing.T, execute func(action *protocol.Action) (*protocol.Result, error)) string {
	t.Helper()
	name := "replay-api-" + strings.ReplaceAll(strings.ToLower(t.Name()), "/", "-")
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
	if payload.Report.Events[1].Target != "/users/s-123" {
		t.Fatalf("target = %q, want /users/s-123", payload.Report.Events[1].Target)
	}
	if payload.Report.FinalVariables["session"] != "s-123" {
		t.Fatalf("session = %q, want s-123", payload.Report.FinalVariables["session"])
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

func TestHandleReplayDraftScriptBroadcastsProgress(t *testing.T) {
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{Success: true, StatusCode: 200}, nil
	})

	server := NewServer(ServerConfig{})
	body, _ := json.Marshal(map[string]any{
		"replay_session_id": "session-123",
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

	types := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		msg := <-server.hub.broadcast
		var decoded struct {
			Type            string `json:"type"`
			ReplaySessionID string `json:"replay_session_id"`
			ScriptID        string `json:"script_id"`
			EventIndex      *int   `json:"event_index,omitempty"`
		}
		if err := json.Unmarshal(msg, &decoded); err != nil {
			t.Fatalf("json.Unmarshal broadcast returned error: %v", err)
		}
		if decoded.ReplaySessionID != "session-123" {
			t.Fatalf("broadcast replay_session_id = %q, want session-123", decoded.ReplaySessionID)
		}
		if decoded.ScriptID != "" {
			t.Fatalf("broadcast script_id = %q, want empty", decoded.ScriptID)
		}
		types = append(types, decoded.Type)
		if decoded.Type == engine.ReplayProgressEventRun {
			if decoded.EventIndex == nil || *decoded.EventIndex != 0 {
				t.Fatalf("event_index = %#v, want 0", decoded.EventIndex)
			}
		} else if decoded.EventIndex != nil {
			t.Fatalf("event_index should be omitted for %s, got %#v", decoded.Type, decoded.EventIndex)
		}
	}

	if !slices.Equal(types, []string{engine.ReplayProgressStarted, engine.ReplayProgressEventRun, engine.ReplayProgressComplete}) {
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
