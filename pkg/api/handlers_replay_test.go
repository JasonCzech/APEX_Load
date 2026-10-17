package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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

const replayHandlerTestSessionID = "77777777-7777-4777-8777-777777777777"

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

func TestHandleClassicReplayHonorsRequestCancellation(t *testing.T) {
	server := NewServer(ServerConfig{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	response := httptest.NewRecorder()

	server.handleReplayRequestContext(ctx, response, "script", &dsl.Script{}, ReplayScriptRequest{})

	if response.Code != http.StatusRequestTimeout {
		t.Fatalf("status = %d, want %d body=%s", response.Code, http.StatusRequestTimeout, response.Body.String())
	}
}

func TestHandleClassicReplayExtendsWriteDeadline(t *testing.T) {
	server := NewServer(ServerConfig{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	response := &archiveDeadlineRecorder{}
	started := time.Now()

	server.handleReplayRequestContext(ctx, response, "script", &dsl.Script{}, ReplayScriptRequest{})

	minimum := started.Add(classicReplayRequestTimeout)
	maximum := time.Now().Add(classicReplayRequestTimeout + classicReplayErrorWriteGrace)
	if response.deadline.Before(minimum) || response.deadline.After(maximum) {
		t.Fatalf("classic replay write deadline = %s, want within [%s, %s]", response.deadline, minimum, maximum)
	}
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

	body, _ := json.Marshal(map[string]any{
		"group_name": "editor-replay",
		"variables":  map[string]string{"session": "s-123"},
	})
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

func TestPrepareClassicReplayResolvesProjectScopedObjectRefOnlyAction(t *testing.T) {
	server := recordingTestServer(t)
	for _, repo := range []*storage.ObjectRepository{
		{
			ID: "object-repo-project", ProjectID: "p1", Name: "Project objects",
			Objects: []storage.RepositoryObject{{LogicalName: "login", CSS: "#project-login", BestConfidence: 80}},
		},
		{
			ID: "object-repo-foreign", ProjectID: "p2", Name: "Foreign objects",
			Objects: []storage.RepositoryObject{{LogicalName: "login", CSS: "#foreign-login", BestConfidence: 100}},
		},
	} {
		if err := server.store.SaveObjectRepository(repo); err != nil {
			t.Fatal(err)
		}
	}
	script := &dsl.Script{
		Name: "Classic object replay", Protocol: "browser", ProjectID: "p1",
		Actions: []dsl.Action{{Name: "Login", Type: "browser_click", ObjectRef: "login"}},
	}
	response := httptest.NewRecorder()
	prepared, issues, warnings, ok := server.prepareClassicReplayScript(response, script)
	if !ok {
		t.Fatalf("preparation failed: status=%d body=%s", response.Code, response.Body.String())
	}
	if len(issues) != 0 || len(warnings) != 0 {
		t.Fatalf("issues=%v warnings=%v", issues, warnings)
	}
	if prepared == script {
		t.Fatal("classic preparation mutated or reused the caller-owned script")
	}
	action := prepared.Actions[0]
	if action.Selector != "#project-login" {
		t.Fatalf("resolved selector = %q, want project-scoped selector", action.Selector)
	}
	if action.Metadata[objectRefRepositoryMetadataKey] != "object-repo-project" ||
		action.Metadata[objectRefLogicalNameMetadataKey] != "login" {
		t.Fatalf("resolved binding attestation = %#v", action.Metadata)
	}
	if script.Actions[0].Selector != "" || script.Actions[0].Metadata != nil {
		t.Fatalf("input script was mutated: %#v", script.Actions[0])
	}
}

func TestPrepareClassicReplayObjectRefFailuresAndFallback(t *testing.T) {
	server := recordingTestServer(t)
	if err := server.store.SaveObjectRepository(&storage.ObjectRepository{
		ID: "object-repo-foreign-only", ProjectID: "p2", Name: "Foreign only",
		Objects: []storage.RepositoryObject{{LogicalName: "foreign-login", CSS: "#foreign-login", BestConfidence: 90}},
	}); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name         string
		action       dsl.Action
		wantIssues   int
		wantWarnings int
		wantSelector string
	}{
		{
			name:       "missing object without fallback",
			action:     dsl.Action{Name: "Login", Type: "browser_click", ObjectRef: "missing"},
			wantIssues: 1,
		},
		{
			name:       "foreign project object without fallback",
			action:     dsl.Action{Name: "Login", Type: "browser_click", ObjectRef: "foreign-login"},
			wantIssues: 1,
		},
		{
			name: "unresolved object with inline fallback",
			action: dsl.Action{
				Name: "Login", Type: "browser_click", ObjectRef: "missing", Selector: "#inline-login",
			},
			wantWarnings: 1,
			wantSelector: "#inline-login",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			prepared, issues, warnings, ok := server.prepareClassicReplayScript(response, &dsl.Script{
				Name: "Classic object replay", Protocol: "browser", ProjectID: "p1",
				Actions: []dsl.Action{test.action},
			})
			if !ok {
				t.Fatalf("preparation failed before object-ref preflight: status=%d body=%s", response.Code, response.Body.String())
			}
			if len(issues) != test.wantIssues || len(warnings) != test.wantWarnings {
				t.Fatalf("issues=%v warnings=%v, want counts %d/%d", issues, warnings, test.wantIssues, test.wantWarnings)
			}
			if got := prepared.Actions[0].Selector; got != test.wantSelector {
				t.Fatalf("selector = %q, want %q", got, test.wantSelector)
			}
		})
	}
}

func TestHandleClassicReplayPersistsResolvedObjectRefSelfHeal(t *testing.T) {
	server := recordingTestServer(t)
	repo := &storage.ObjectRepository{
		ID:        "object-repo-classic-heal",
		ProjectID: "p1",
		Name:      "Classic replay objects",
		Objects: []storage.RepositoryObject{{
			LogicalName:    "login",
			BestConfidence: 70,
			Candidates: []storage.RepositoryObjectCandidate{{
				Kind: "css", Value: "#login-old", Confidence: 70,
			}},
		}},
	}
	if err := server.store.SaveObjectRepository(repo); err != nil {
		t.Fatal(err)
	}
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{
			ActionName: action.Name,
			Success:    true,
			StatusCode: http.StatusOK,
			ProtocolEvidence: map[string]any{
				"selector_self_heal": map[string]any{
					"action":                  action.Name,
					"healed_selector":         "#login-healed",
					"candidate_kind":          "css",
					"confidence":              88,
					"reason":                  "fallback matched",
					"object_ref_repository":   repo.ID,
					"object_ref_logical_name": "login",
				},
			},
		}, nil
	})
	script := &dsl.Script{
		Name: "Classic self-heal", Protocol: protocolName, ProjectID: "p1",
		Config: dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{
			Name: "Login", Type: "request", Target: "/login", ObjectRef: repo.ID + ":login",
		}},
	}
	response := httptest.NewRecorder()
	server.handleReplayRequest(response, "classic-heal-script", script, ReplayScriptRequest{})
	if response.Code != http.StatusOK {
		t.Fatalf("classic replay status = %d, want 200 body=%s", response.Code, response.Body.String())
	}
	updated, err := server.store.GetObjectRepository(repo.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, candidate := range updated.Objects[0].Candidates {
		if candidate.Kind == "css" && candidate.Value == "#login-healed" && candidate.Confidence == 88 {
			found = true
		}
	}
	if !found {
		t.Fatalf("classic replay did not persist the resolved object self-heal: %#v", updated.Objects[0].Candidates)
	}
}

func TestHandleClassicReplayFencesSelfHealWhenExecutionEpochEndsDuringPersistence(t *testing.T) {
	baseStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "classic-replay-fenced-heal.db"))
	if err != nil {
		t.Fatalf("open classic replay store: %v", err)
	}
	startedCAS := make(chan struct{})
	blockingStore := &blockingObjectRepositoryContextCASStore{SQLiteStore: baseStore, started: startedCAS}
	server := newAdmittedHAReplayServerWithStore(t, sqliteOverlay(blockingStore))
	now := time.Now().UTC()
	if err := server.store.SaveProject(&storage.Project{ID: "p1", Name: "p1", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	repo := &storage.ObjectRepository{
		ID:        "object-repo-classic-fenced-heal",
		ProjectID: "p1",
		Name:      "Classic fenced objects",
		Objects: []storage.RepositoryObject{{
			LogicalName:    "login",
			BestConfidence: 70,
			Candidates: []storage.RepositoryObjectCandidate{{
				Kind: "css", Value: "#login-old", Confidence: 70,
			}},
		}},
	}
	if err := server.store.SaveObjectRepository(repo); err != nil {
		t.Fatal(err)
	}

	// Install the blocking lease-term CAS before constructing the server so the
	// leadership worker and repository writer share the same immutable HA
	// configuration. The test can then pause inside the production durable fence
	// and revoke the epoch without racing a background reconciler.

	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{
			ActionName: action.Name,
			Success:    true,
			StatusCode: http.StatusOK,
			ProtocolEvidence: map[string]any{
				"selector_self_heal": map[string]any{
					"action":                  action.Name,
					"healed_selector":         "#must-not-persist",
					"candidate_kind":          "css",
					"confidence":              99,
					"object_ref_repository":   repo.ID,
					"object_ref_logical_name": "login",
				},
			},
		}, nil
	})
	script := &dsl.Script{
		Name: "Classic fenced self-heal", Protocol: protocolName, ProjectID: "p1",
		Config: dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{
			Name: "Login", Type: "request", Target: "/login", ObjectRef: repo.ID + ":login",
		}},
	}
	response := httptest.NewRecorder()
	handlerDone := make(chan struct{})
	go func() {
		defer close(handlerDone)
		server.handleReplayRequest(response, "classic-fenced-heal-script", script, ReplayScriptRequest{})
	}()
	select {
	case <-startedCAS:
	case <-time.After(2 * time.Second):
		t.Fatal("classic replay never reached object-repository persistence")
	}
	server.leadership.relinquish()
	select {
	case <-handlerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("classic replay did not stop after execution-epoch loss")
	}
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("classic replay status after persistence fence loss = %d, want 503 body=%s", response.Code, response.Body.String())
	}
	unchanged, err := server.store.GetObjectRepository(repo.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(unchanged.Objects[0].Candidates) != 1 || unchanged.Objects[0].Candidates[0].Value != "#login-old" {
		t.Fatalf("lost-epoch classic self-heal mutated the repository: %#v", unchanged.Objects[0].Candidates)
	}
}

func TestHandleClassicReplayBlocksUnresolvedObjectRefBeforeExecution(t *testing.T) {
	server := recordingTestServer(t)
	body, err := json.Marshal(map[string]any{
		"project_id": "p1",
		"script": map[string]any{
			"name":       "Unresolved object replay",
			"version":    "1",
			"protocol":   "browser",
			"project_id": "p1",
			"actions": []map[string]any{{
				"name": "Login", "type": "browser_click", "object_ref": "missing",
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/replay", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusBadRequest ||
		!strings.Contains(resp.Body.String(), "object_ref") ||
		!strings.Contains(resp.Body.String(), "no selector/target fallback") {
		t.Fatalf("status=%d body=%s, want unresolved object-ref preflight rejection", resp.Code, resp.Body.String())
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

func TestHandleReplayDraftScriptRejectsInvalidCallerSessionID(t *testing.T) {
	tests := []struct {
		name      string
		sessionID string
	}{
		{name: "predictable", sessionID: "session-123"},
		{name: "wrong version", sessionID: "6ba7b810-9dad-11d1-80b4-00c04fd430c8"},
		{name: "uppercase", sessionID: "77777777-7777-4777-8777-77777777777A"},
		{name: "surrounding whitespace", sessionID: " " + replayHandlerTestSessionID + " "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := NewServer(ServerConfig{})
			body, err := json.Marshal(map[string]any{
				"replay_session_id": tt.sessionID,
				"script": map[string]any{
					"name":     "Unsaved Draft",
					"version":  "1.0.0",
					"protocol": "http",
					"actions": []map[string]any{{
						"name":   "draft-step",
						"type":   "GET",
						"target": "/unsaved",
					}},
				},
			})
			if err != nil {
				t.Fatalf("json.Marshal returned error: %v", err)
			}
			req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/replay", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()

			server.mux.ServeHTTP(resp, req)

			if resp.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
			}
			if !strings.Contains(resp.Body.String(), "canonical UUID v4") {
				t.Fatalf("body = %q, want canonical UUID v4 validation error", resp.Body.String())
			}
		})
	}
}

func TestHandleReplayDraftScriptBroadcastsProgress(t *testing.T) {
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{Success: true, StatusCode: 200}, nil
	})

	server := NewServer(ServerConfig{})
	body, _ := json.Marshal(map[string]any{
		"replay_session_id": replayHandlerTestSessionID,
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
	if payload.ReplaySessionID != replayHandlerTestSessionID {
		t.Fatalf("replay_session_id = %q, want %s", payload.ReplaySessionID, replayHandlerTestSessionID)
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
		if decoded.ReplaySessionID != replayHandlerTestSessionID {
			t.Fatalf("broadcast replay_session_id = %q, want %s", decoded.ReplaySessionID, replayHandlerTestSessionID)
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

func TestHandleReplayDraftScriptRejectsConcurrentSessionIDReuseAcrossProjects(t *testing.T) {
	started := make(chan struct{})
	releaseFirst := make(chan struct{})
	var calls atomic.Int32
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-releaseFirst
		}
		return &protocol.Result{Success: true, StatusCode: 200}, nil
	})

	server := NewServer(ServerConfig{})
	bodyForProject := func(projectID string) []byte {
		body, err := json.Marshal(map[string]any{
			"replay_session_id": replayHandlerTestSessionID,
			"project_id":        projectID,
			"script": map[string]any{
				"name":     "Concurrent draft",
				"version":  "1.0.0",
				"protocol": protocolName,
				"config":   map[string]any{"base_url": "https://example.com"},
				"actions": []map[string]any{{
					"name": "draft-step", "type": "GET", "target": "/unsaved",
				}},
			},
		})
		if err != nil {
			t.Fatalf("json.Marshal returned error: %v", err)
		}
		return body
	}
	replay := func(projectID string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/replay", bytes.NewReader(bodyForProject(projectID)))
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		return resp
	}

	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { firstDone <- replay("project-a") }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("first replay did not reach protocol execution")
	}

	conflict := replay("project-b")
	if conflict.Code != http.StatusConflict || !strings.Contains(conflict.Body.String(), "already in use") {
		t.Fatalf("concurrent status/body = %d/%q, want 409 session-id conflict", conflict.Code, conflict.Body.String())
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("protocol calls after conflict = %d, want 1", got)
	}

	close(releaseFirst)
	select {
	case first := <-firstDone:
		if first.Code != http.StatusOK {
			t.Fatalf("first status = %d, want 200 body=%s", first.Code, first.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first replay did not finish after release")
	}

	reused := replay("project-b")
	if reused.Code != http.StatusOK {
		t.Fatalf("reused status = %d, want 200 body=%s", reused.Code, reused.Body.String())
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("protocol calls after released reuse = %d, want 2", got)
	}
}

func TestReplaySessionIDReservationIsSharedAcrossReplayModes(t *testing.T) {
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{Success: true, StatusCode: 200}, nil
	})
	server := NewServer(ServerConfig{})
	now := time.Now()
	server.browserReplay.retention.terminalTTL = time.Second
	server.browserReplay.retention.now = func() time.Time { return now }

	browserSession, err := server.browserReplay.Start("browser-script", &dsl.Script{
		Name: "Browser", Protocol: "browser", ProjectID: "project-a",
	}, BrowserReplayStartRequest{ReplaySessionID: replayHandlerTestSessionID, StartPaused: true})
	if err != nil {
		t.Fatalf("start browser replay: %v", err)
	}
	server.browserReplay.mu.RLock()
	browserRuntime := server.browserReplay.sessions[browserSession.ID]
	server.browserReplay.mu.RUnlock()
	if browserRuntime == nil {
		t.Fatal("browser replay runtime was not registered")
	}

	debugReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/debug/sessions", nil)
	debugResp := httptest.NewRecorder()
	server.handleStartScriptDebugRequest(debugResp, debugReq, "debug-script", &dsl.Script{
		ID: "debug-script", Name: "Debug", Protocol: protocolName, ProjectID: "project-b",
		Config:  dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions: []dsl.Action{{Name: "step", Type: "GET", Target: "/debug"}},
	}, ScriptDebugStartRequest{ReplaySessionID: replayHandlerTestSessionID, StartPaused: true})
	if debugResp.Code != http.StatusConflict {
		t.Fatalf("debug collision status = %d, want 409 body=%s", debugResp.Code, debugResp.Body.String())
	}

	classicBody, err := json.Marshal(map[string]any{
		"replay_session_id": replayHandlerTestSessionID,
		"project_id":        "project-c",
		"script": map[string]any{
			"name": "Classic", "version": "1.0.0", "protocol": protocolName,
			"config":  map[string]any{"base_url": "https://example.com"},
			"actions": []map[string]any{{"name": "step", "type": "GET", "target": "/classic"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	classicReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/replay", bytes.NewReader(classicBody))
	classicReq.Header.Set("Content-Type", "application/json")
	classicResp := httptest.NewRecorder()
	server.mux.ServeHTTP(classicResp, classicReq)
	if classicResp.Code != http.StatusConflict {
		t.Fatalf("classic collision status = %d, want 409 body=%s", classicResp.Code, classicResp.Body.String())
	}

	if _, ok := server.browserReplay.Stop(browserSession.ID); !ok {
		t.Fatal("failed to stop browser replay")
	}
	debugResp = httptest.NewRecorder()
	server.handleStartScriptDebugRequest(debugResp, debugReq, "debug-script", &dsl.Script{
		ID: "debug-script", Name: "Debug", Protocol: protocolName, ProjectID: "project-b",
		Config:  dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions: []dsl.Action{{Name: "step", Type: "GET", Target: "/debug"}},
	}, ScriptDebugStartRequest{ReplaySessionID: replayHandlerTestSessionID, StartPaused: true})
	if debugResp.Code != http.StatusConflict {
		t.Fatalf("terminal-session collision status = %d, want 409 body=%s", debugResp.Code, debugResp.Body.String())
	}

	// Stop only requests cancellation; terminal retention begins after the
	// worker has drained and finalized its observable artifacts. Waiting here
	// also orders the fake-clock advance after the worker's final clock read.
	select {
	case <-browserRuntime.workerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("browser replay worker did not drain after stop")
	}
	now = now.Add(2 * time.Second)
	if removed := server.browserReplay.PruneExpired(); removed != 1 {
		t.Fatalf("pruned browser sessions = %d, want 1", removed)
	}
	debugResp = httptest.NewRecorder()
	server.handleStartScriptDebugRequest(debugResp, debugReq, "debug-script", &dsl.Script{
		ID: "debug-script", Name: "Debug", Protocol: protocolName, ProjectID: "project-b",
		Config:  dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions: []dsl.Action{{Name: "step", Type: "GET", Target: "/debug"}},
	}, ScriptDebugStartRequest{ReplaySessionID: replayHandlerTestSessionID, StartPaused: true})
	if debugResp.Code != http.StatusOK {
		t.Fatalf("debug reuse after prune status = %d, want 200 body=%s", debugResp.Code, debugResp.Body.String())
	}
	if _, ok := server.scriptDebug.Stop(replayHandlerTestSessionID); !ok {
		t.Fatal("failed to stop debug replay")
	}
}

func TestReplaySessionIDReservationKeepsCapacityForOtherProjects(t *testing.T) {
	server := NewServer(ServerConfig{})
	releases := make([]func(), 0, maxReplaySessionReservationsPerProject)
	defer func() {
		for _, release := range releases {
			release()
		}
	}()
	for i := 0; i < maxReplaySessionReservationsPerProject; i++ {
		_, release, _, err := server.reserveReplaySessionID("", "project-a")
		if err != nil {
			t.Fatalf("project-a reservation %d: %v", i, err)
		}
		releases = append(releases, release)
	}
	if _, _, _, err := server.reserveReplaySessionID("", "project-a"); !errors.Is(err, errReplaySessionProjectCapacity) {
		t.Fatalf("project-a over-cap error = %v, want %v", err, errReplaySessionProjectCapacity)
	}
	_, releaseB, _, err := server.reserveReplaySessionID("", "project-b")
	if err != nil {
		t.Fatalf("project-b reservation after project-a saturation: %v", err)
	}
	releaseB()
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
	// cart_id is extracted only by the then branch, so it is not guaranteed at
	// checkout when the else branch remains reachable.
	if !slices.Equal(preflight.MissingVariables, []string{"cart_id", "sku"}) {
		t.Fatalf("missing variables = %v, want nested sku and branch-local cart_id", preflight.MissingVariables)
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

func TestHandleReplayDraftScriptRejectsMalformedAndMultiJSONBodies(t *testing.T) {
	server := NewServer(ServerConfig{})
	cases := []struct {
		name          string
		body          string
		expectedError string
	}{
		{
			name:          "empty body",
			body:          "",
			expectedError: "request body is required",
		},
		{
			name:          "malformed json",
			body:          "{",
			expectedError: "invalid request body",
		},
		{
			name:          "multiple json values",
			body:          `{"project_id":"project-a","script":{"name":"Draft","version":"1.0.0","protocol":"http","actions":[{"name":"load","type":"GET","method":"GET","target":"/"}]}} {"project_id":"project-a"}`,
			expectedError: "multiple JSON values are not allowed",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/replay", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()

			server.mux.ServeHTTP(resp, req)
			if resp.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
			}
			if !strings.Contains(resp.Body.String(), tc.expectedError) {
				t.Fatalf("body = %q, want %q", resp.Body.String(), tc.expectedError)
			}
		})
	}
}
