package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestAIAuthoringContractAndPolicyEndpoints(t *testing.T) {
	server := NewServer(ServerConfig{})

	contractReq := httptest.NewRequest(http.MethodGet, "/api/v1/ai/authoring/contract", nil)
	contractResp := httptest.NewRecorder()
	server.mux.ServeHTTP(contractResp, contractReq)
	if contractResp.Code != http.StatusOK {
		t.Fatalf("contract status = %d body=%s", contractResp.Code, contractResp.Body.String())
	}
	var contract AIAuthoringContractResponse
	if err := json.NewDecoder(contractResp.Body).Decode(&contract); err != nil {
		t.Fatalf("decode contract: %v", err)
	}
	if contract.SchemaVersion != aiAuthoringContractSchemaVersion || contract.PromotionRule == "" {
		t.Fatalf("contract = %#v, want schema and promotion rule", contract)
	}
	if !stringSliceContainsFold(contract.AllowedSources, "openapi") || !stringSliceContainsFold(contract.RequiredEvidence, "replay-before-save evidence") {
		t.Fatalf("contract sources/evidence = %#v / %#v", contract.AllowedSources, contract.RequiredEvidence)
	}

	policyReq := httptest.NewRequest(http.MethodGet, "/api/v1/ai/policy?project_id=project-ai", nil)
	policyResp := httptest.NewRecorder()
	server.mux.ServeHTTP(policyResp, policyReq)
	if policyResp.Code != http.StatusOK {
		t.Fatalf("policy status = %d body=%s", policyResp.Code, policyResp.Body.String())
	}
	var policy AIPolicyResponse
	if err := json.NewDecoder(policyResp.Body).Decode(&policy); err != nil {
		t.Fatalf("decode policy: %v", err)
	}
	if !policy.Enabled || policy.ProjectID != "project-ai" || !stringSliceContainsFold(policy.AllowedProviders, defaultAIProvider) {
		t.Fatalf("policy = %#v, want enabled project policy with deterministic provider", policy)
	}
	if len(policy.Controls) < 4 || len(policy.AuditEvents) == 0 {
		t.Fatalf("policy controls/audit = %#v / %#v", policy.Controls, policy.AuditEvents)
	}
}

func TestAIDraftGenerationCreatesBlockedReviewQueueSession(t *testing.T) {
	server := NewServer(ServerConfig{})
	body := []byte(`{
		"project_id":"project-ai",
		"provider":"mocked-provider",
		"source_type":"openapi",
		"name":"AI Checkout",
		"source_payload":{
			"openapi":"3.0.0",
			"servers":[{"url":"https://shop.example.test"}],
			"paths":{"/checkout":{"post":{"operationId":"checkout"}}}
		}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ai/drafts", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("draft status = %d body=%s", resp.Code, resp.Body.String())
	}
	var payload AIDraftResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode draft: %v", err)
	}
	if payload.Provider != mockedAIProvider || payload.SourceType != "openapi" {
		t.Fatalf("provider/source = %q/%q", payload.Provider, payload.SourceType)
	}
	if payload.Draft == nil || payload.Draft.Config.BaseURL != "https://shop.example.test" || payload.Draft.Actions[0].Method != "POST" {
		t.Fatalf("draft = %#v, want OpenAPI-derived POST checkout", payload.Draft)
	}
	if payload.Validation.Valid != true || payload.Guardrails == nil || payload.Guardrails.Status != "blocked" {
		t.Fatalf("validation/guardrails = %#v / %#v, want valid draft blocked by AI evidence", payload.Validation, payload.Guardrails)
	}
	if payload.ReviewQueueItem == nil || payload.ReviewQueueItem.SessionID == "" || payload.ReviewQueueItem.ProjectID != "project-ai" {
		t.Fatalf("review item = %#v", payload.ReviewQueueItem)
	}
	if _, ok := server.runner.GetScript(payload.Draft.ID); ok {
		t.Fatalf("AI draft %q was stored as reusable script before approval", payload.Draft.ID)
	}

	queueReq := httptest.NewRequest(http.MethodGet, "/api/v1/ai/review-queue?project_id=project-ai", nil)
	queueResp := httptest.NewRecorder()
	server.mux.ServeHTTP(queueResp, queueReq)
	if queueResp.Code != http.StatusOK {
		t.Fatalf("queue status = %d body=%s", queueResp.Code, queueResp.Body.String())
	}
	var queue AIReviewQueueResponse
	if err := json.NewDecoder(queueResp.Body).Decode(&queue); err != nil {
		t.Fatalf("decode queue: %v", err)
	}
	if queue.Count != 1 || queue.Items[0].SessionID != payload.ReviewQueueItem.SessionID {
		t.Fatalf("queue = %#v, want generated session", queue)
	}

	approveReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/authoring/sessions/"+payload.ReviewQueueItem.SessionID+"/approve", nil)
	approveResp := httptest.NewRecorder()
	server.mux.ServeHTTP(approveResp, approveReq)
	if approveResp.Code != http.StatusBadRequest {
		t.Fatalf("approval status = %d body=%s, want blocked approval", approveResp.Code, approveResp.Body.String())
	}
}

func TestAIPolicyCanDisableProjectDraftGeneration(t *testing.T) {
	t.Setenv("APEXLOAD_AI_DISABLED_PROJECTS", "project-disabled")
	server := NewServer(ServerConfig{})

	policyReq := httptest.NewRequest(http.MethodGet, "/api/v1/ai/policy?project_id=project-disabled", nil)
	policyResp := httptest.NewRecorder()
	server.mux.ServeHTTP(policyResp, policyReq)
	if policyResp.Code != http.StatusOK {
		t.Fatalf("policy status = %d body=%s", policyResp.Code, policyResp.Body.String())
	}
	var policy AIPolicyResponse
	if err := json.NewDecoder(policyResp.Body).Decode(&policy); err != nil {
		t.Fatalf("decode policy: %v", err)
	}
	if policy.Enabled || policy.ProjectOverride == nil {
		t.Fatalf("policy = %#v, want disabled project override", policy)
	}

	body := []byte(`{"project_id":"project-disabled","source_type":"user_story","text":"As a user I check out"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ai/drafts", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("disabled draft status = %d body=%s, want 403", resp.Code, resp.Body.String())
	}

	repairScript := dsl.Script{
		Name:      "Disabled Project Draft",
		Version:   "1.0.0",
		Protocol:  "http",
		ProjectID: "project-disabled",
		Config:    dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions:   []dsl.Action{{Name: "Open", Type: "request", Method: "GET", Target: "/"}},
	}
	repairBody, err := json.Marshal(AIRepairRequest{Script: &repairScript})
	if err != nil {
		t.Fatalf("marshal repair request: %v", err)
	}
	repairReq := httptest.NewRequest(http.MethodPost, "/api/v1/ai/repairs", bytes.NewReader(repairBody))
	repairReq.Header.Set("Content-Type", "application/json")
	repairResp := httptest.NewRecorder()
	server.mux.ServeHTTP(repairResp, repairReq)
	if repairResp.Code != http.StatusForbidden {
		t.Fatalf("disabled repair status = %d body=%s, want 403", repairResp.Code, repairResp.Body.String())
	}
}

func TestAIEndpointsHonorProjectScopedRBAC(t *testing.T) {
	server := NewServer(ServerConfig{})
	enableTrustedHeaderAuth(t, server)
	body := []byte(`{"project_id":"project-rbac","source_type":"user_story","text":"GET /orders for release smoke"}`)

	viewerReq := httptest.NewRequest(http.MethodPost, "/api/v1/ai/drafts", bytes.NewReader(body))
	viewerReq.Header.Set("Content-Type", "application/json")
	viewerReq.Header.Set("X-Auth-Request-User", "viewer-user")
	viewerReq.Header.Set("X-APEXLoad-Project-Roles", "project-rbac="+RoleViewer)
	viewerResp := httptest.NewRecorder()
	server.mux.ServeHTTP(viewerResp, viewerReq)
	if viewerResp.Code != http.StatusForbidden {
		t.Fatalf("viewer draft status = %d body=%s, want forbidden", viewerResp.Code, viewerResp.Body.String())
	}

	editorReq := httptest.NewRequest(http.MethodPost, "/api/v1/ai/drafts", bytes.NewReader(body))
	editorReq.Header.Set("Content-Type", "application/json")
	editorReq.Header.Set("X-Auth-Request-User", "editor-user")
	editorReq.Header.Set("X-APEXLoad-Project-Roles", "project-rbac="+RoleEditor)
	editorResp := httptest.NewRecorder()
	server.mux.ServeHTTP(editorResp, editorReq)
	if editorResp.Code != http.StatusCreated {
		t.Fatalf("editor draft status = %d body=%s, want created", editorResp.Code, editorResp.Body.String())
	}

	queueReq := httptest.NewRequest(http.MethodGet, "/api/v1/ai/review-queue?project_id=project-rbac", nil)
	queueReq.Header.Set("X-Auth-Request-User", "viewer-user")
	queueReq.Header.Set("X-APEXLoad-Project-Roles", "project-rbac="+RoleViewer)
	queueResp := httptest.NewRecorder()
	server.mux.ServeHTTP(queueResp, queueReq)
	if queueResp.Code != http.StatusOK {
		t.Fatalf("viewer queue status = %d body=%s, want ok", queueResp.Code, queueResp.Body.String())
	}
}

func TestAIRepairSuggestionsCoverAuthoringRisks(t *testing.T) {
	server := NewServer(ServerConfig{})
	script := dsl.Script{
		Name:      "Risky Draft",
		Version:   "1.0.0",
		Protocol:  "http",
		ProjectID: "project-ai",
		Config:    dsl.ScriptConfig{BaseURL: "https://example.test"},
		Parameters: []dsl.Parameter{
			{Name: "customer_id", Type: "file", DataSource: "missing_source", Column: "id", Selection: "unique", OnExhaustion: "stop"},
		},
		Actions: []dsl.Action{
			{
				Name:    "Submit",
				Type:    "request",
				Method:  "POST",
				Target:  "/submit",
				Headers: map[string]string{"Authorization": "Bearer hard-coded-token"},
				Body:    `{"session":"abc123","customer":"{{data.missing_source.id}}"}`,
			},
		},
	}
	body, err := json.Marshal(AIRepairRequest{ProjectID: "project-ai", Script: &script})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ai/repairs", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("repair status = %d body=%s", resp.Code, resp.Body.String())
	}
	var payload AIRepairResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode repair: %v", err)
	}
	categories := map[string]bool{}
	for _, suggestion := range payload.Suggestions {
		categories[suggestion.Category] = true
	}
	for _, category := range []string{"data_source", "assertions", "correlation", "hard_coded_data"} {
		if !categories[category] {
			t.Fatalf("missing category %q in suggestions %#v", category, payload.Suggestions)
		}
	}
	if payload.PreviewScript == nil || len(payload.PreviewScript.Actions[0].Assertions) == 0 {
		t.Fatalf("preview script = %#v, want status assertion preview", payload.PreviewScript)
	}
	if payload.Validation.AuthoringHealth.Status != "blocked" {
		t.Fatalf("authoring health = %#v, want blocked data-source fit", payload.Validation.AuthoringHealth)
	}
}

func TestAIRepairSuggestsWeb2SelectorRepairs(t *testing.T) {
	server := NewServer(ServerConfig{})
	script := dsl.Script{
		Name:     "Imported Web2 Draft",
		Version:  "1.0.0",
		Protocol: "browser",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.test"},
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "loadrunner",
			Preserved: map[string]any{
				"source_script_type": "TruClient 2.0 - Web",
				"trueclient_archive_inspection": map[string]any{
					"steps": []any{map[string]any{"object_descriptor_id": "obj_login"}},
				},
			},
		}},
		Actions: []dsl.Action{
			{Name: "Click Login", Type: "browser_click", Target: "", Selector: "div:nth-child(3) > button"},
			{Name: "Wait For Shell", Type: "browser_wait", Target: "", WaitFor: &dsl.WaitCondition{Type: "selector", Value: "[data-testid='shell']", TimeoutMs: 10000}},
		},
	}
	body, err := json.Marshal(AIRepairRequest{ProjectID: "project-ai", Script: &script, Categories: []string{"selector", "unstable_waits", "assertions", "data_source"}})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ai/repairs", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("repair status = %d body=%s", resp.Code, resp.Body.String())
	}
	var payload AIRepairResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode repair: %v", err)
	}
	categories := map[string]bool{}
	for _, suggestion := range payload.Suggestions {
		categories[suggestion.Category] = true
	}
	for _, category := range []string{"selector", "unstable_waits", "assertions"} {
		if !categories[category] {
			t.Fatalf("missing category %q in suggestions %#v", category, payload.Suggestions)
		}
	}
	if payload.PreviewScript == nil || payload.PreviewScript.Metadata == nil || payload.PreviewScript.Metadata.Import == nil || payload.PreviewScript.Metadata.Import.Preserved["ai_repair_preview"] != true {
		t.Fatalf("preview script metadata = %#v, want AI repair provenance", payload.PreviewScript)
	}
}

func TestAIExplainModeCitesLocalEvidence(t *testing.T) {
	server := NewServer(ServerConfig{})
	stored := server.runner.StoreScript(&dsl.Script{
		ID:        "script-ai-explain",
		Name:      "Explain Me",
		Version:   "1.0.0",
		Protocol:  "http",
		ProjectID: "project-ai",
		Config:    dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions:   []dsl.Action{{Name: "Open", Type: "request", Method: "GET", Target: "/", Assertions: []dsl.Assertion{{Type: "status_code", Expected: 200}}}},
	})
	scenario, err := server.runner.StoreScenario(CreateScenarioRequest{
		Name:     "Explain Scenario",
		Duration: "1m",
		Groups:   []CreateGroupEntry{{Name: "API", ScriptID: stored.ID, VUsers: 5}},
	})
	if err != nil {
		t.Fatalf("StoreScenario returned error: %v", err)
	}

	scriptBody := []byte(`{"project_id":"project-ai","target_type":"script","target_id":"script-ai-explain"}`)
	scriptReq := httptest.NewRequest(http.MethodPost, "/api/v1/ai/explain", bytes.NewReader(scriptBody))
	scriptReq.Header.Set("Content-Type", "application/json")
	scriptResp := httptest.NewRecorder()
	server.mux.ServeHTTP(scriptResp, scriptReq)
	if scriptResp.Code != http.StatusOK {
		t.Fatalf("script explain status = %d body=%s", scriptResp.Code, scriptResp.Body.String())
	}
	var scriptExplain AIExplainResponse
	if err := json.NewDecoder(scriptResp.Body).Decode(&scriptExplain); err != nil {
		t.Fatalf("decode script explain: %v", err)
	}
	if scriptExplain.TargetType != "script" || len(scriptExplain.Citations) == 0 || len(scriptExplain.Sections) < 2 {
		t.Fatalf("script explain = %#v, want citations and sections", scriptExplain)
	}

	rtsBody := []byte(`{"project_id":"project-ai","target_type":"runtime_settings"}`)
	rtsReq := httptest.NewRequest(http.MethodPost, "/api/v1/ai/explain", bytes.NewReader(rtsBody))
	rtsReq.Header.Set("Content-Type", "application/json")
	rtsResp := httptest.NewRecorder()
	server.mux.ServeHTTP(rtsResp, rtsReq)
	if rtsResp.Code != http.StatusOK {
		t.Fatalf("runtime explain status = %d body=%s", rtsResp.Code, rtsResp.Body.String())
	}
	var rtsExplain AIExplainResponse
	if err := json.NewDecoder(rtsResp.Body).Decode(&rtsExplain); err != nil {
		t.Fatalf("decode runtime explain: %v", err)
	}
	if rtsExplain.TargetType != "runtime_settings" || !explainHasCitation(rtsExplain.Citations, "/api/v1/runtime-settings/support-matrix") {
		t.Fatalf("runtime explain = %#v, want runtime support citation", rtsExplain)
	}

	scenarioBody := []byte(`{"project_id":"project-ai","target_type":"scenario","target_id":"` + scenario.ID + `"}`)
	scenarioReq := httptest.NewRequest(http.MethodPost, "/api/v1/ai/explain", bytes.NewReader(scenarioBody))
	scenarioReq.Header.Set("Content-Type", "application/json")
	scenarioResp := httptest.NewRecorder()
	server.mux.ServeHTTP(scenarioResp, scenarioReq)
	if scenarioResp.Code != http.StatusOK {
		t.Fatalf("scenario explain status = %d body=%s", scenarioResp.Code, scenarioResp.Body.String())
	}
}

func TestAIRootCauseWrapsArchivedEvidence(t *testing.T) {
	server := NewServer(ServerConfig{})
	releaseID := "release-ai-root"
	started := time.Date(2026, time.May, 19, 9, 0, 0, 0, time.UTC)
	completed := started.Add(5 * time.Minute)
	archive := writeReleaseTrendArchiveFixture(t, releaseID, "run-ai-root", "AI Root Cause Run", started, completed, true)
	server.runner.mu.Lock()
	server.runner.tests["run-ai-root"] = &ManagedTest{
		ID:        "run-ai-root",
		Name:      "AI Root Cause Run",
		Status:    "COMPLETE",
		ReleaseID: releaseID,
		CreatedAt: started,
		StartedAt: ptrTime(started),
		EndedAt:   ptrTime(completed),
		Archive:   archive,
	}
	server.runner.archives["run-ai-root"] = archive
	server.runner.mu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/run-ai-root/archive/ai-root-cause", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("AI root-cause status = %d body=%s", resp.Code, resp.Body.String())
	}
	var payload AIRootCauseResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode AI root-cause: %v", err)
	}
	if !payload.EvidenceBound || payload.RootCause.Status != "breached" || !explainHasCitation(payload.Citations, "/api/v1/tests/run-ai-root/archive/report") {
		t.Fatalf("AI root-cause = %#v, want evidence-bound breached summary with archive citation", payload)
	}
}

func explainHasCitation(citations []AICitation, path string) bool {
	for _, citation := range citations {
		if citation.Path == path {
			return true
		}
	}
	return false
}
