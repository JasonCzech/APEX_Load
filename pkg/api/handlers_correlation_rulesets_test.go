package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/convert/paramize"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestCorrelationRulesetCRUD(t *testing.T) {
	server := recordingTestServer(t)

	const createSample = "TOKEN-CREATE-1111"
	body := `{"project_id":"p1","name":"Checkout rules","source":"script:source","rules":[{"name":"csrf","source":"response_body","extract_type":"boundary","expression":"x","sample_value":"` + createSample + `"}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/correlation-rulesets", strings.NewReader(body))
	resp := httptest.NewRecorder()
	server.handleCreateCorrelationRuleset(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("create = %d body=%s", resp.Code, resp.Body.String())
	}
	createResponseJSON := resp.Body.String()
	var created storage.CorrelationRuleset
	if err := json.Unmarshal([]byte(createResponseJSON), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.ID == "" || len(created.Rules) != 1 {
		t.Fatalf("unexpected created: %#v", created)
	}
	if created.Rules[0].SampleValue != "" || strings.Contains(createResponseJSON, createSample) || strings.Contains(createResponseJSON, "sample_value") {
		t.Fatalf("create response disclosed a learned sample: %s", createResponseJSON)
	}
	persisted, err := server.store.GetCorrelationRuleset(created.ID)
	if err != nil {
		t.Fatalf("load created ruleset: %v", err)
	}
	if persisted.Rules[0].SampleValue != "" {
		t.Fatalf("create persisted learned sample %q", persisted.Rules[0].SampleValue)
	}
	createdUpdatedAt := persisted.UpdatedAt

	// Reject a nameless ruleset.
	bad := httptest.NewRequest(http.MethodPost, "/api/v1/correlation-rulesets", strings.NewReader(`{"project_id":"p1"}`))
	badResp := httptest.NewRecorder()
	server.handleCreateCorrelationRuleset(badResp, bad)
	if badResp.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing name, got %d", badResp.Code)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/correlation-rulesets?project_id=p1&limit=1000", nil)
	listResp := httptest.NewRecorder()
	server.handleListCorrelationRulesets(listResp, listReq)
	var list struct {
		Count int `json:"count"`
		Limit int `json:"limit"`
	}
	listResponseJSON := listResp.Body.String()
	if err := json.Unmarshal([]byte(listResponseJSON), &list); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if list.Count != 1 {
		t.Fatalf("list count = %d, want 1", list.Count)
	}
	if list.Limit != maxCorrelationRulesetListLimit {
		t.Fatalf("list limit = %d, want bounded %d", list.Limit, maxCorrelationRulesetListLimit)
	}
	if strings.Contains(listResponseJSON, createSample) || strings.Contains(listResponseJSON, "sample_value") {
		t.Fatalf("list disclosed a learned sample: %s", listResponseJSON)
	}

	const updateSample = "TOKEN-UPDATE-2222"
	updateReq := httptest.NewRequest(http.MethodPut, "/api/v1/correlation-rulesets/"+created.ID,
		strings.NewReader(`{"expected_updated_at":"`+createdUpdatedAt.Format(time.RFC3339Nano)+`","id":"attacker-id","project_id":"attacker-project","name":"Checkout rules v2","description":"updated description","source":"script:attacker","created_at":"2000-01-01T00:00:00Z","updated_at":"2000-01-01T00:00:00Z","rules":[{"name":"updated","source":"response_body","extract_type":"jsonpath","expression":"$.token","sample_value":"`+updateSample+`"}]}`))
	updateReq.SetPathValue("id", created.ID)
	updateResp := httptest.NewRecorder()
	server.handleUpdateCorrelationRuleset(updateResp, updateReq)
	if updateResp.Code != http.StatusOK {
		t.Fatalf("update = %d body=%s", updateResp.Code, updateResp.Body.String())
	}
	updateResponseJSON := updateResp.Body.String()
	var updated storage.CorrelationRuleset
	if err := json.Unmarshal([]byte(updateResponseJSON), &updated); err != nil {
		t.Fatalf("decode update response: %v", err)
	}
	if updated.ID != created.ID || updated.ProjectID != "p1" || updated.Name != "Checkout rules v2" || updated.Description != "updated description" || updated.Source != "script:source" || !updated.CreatedAt.Equal(created.CreatedAt) {
		t.Fatalf("update did not preserve immutable provenance: %#v", updated)
	}
	if len(updated.Rules) != 1 || updated.Rules[0].SampleValue != "" || strings.Contains(updateResponseJSON, updateSample) || strings.Contains(updateResponseJSON, "sample_value") {
		t.Fatalf("update response disclosed a learned sample: %s", updateResponseJSON)
	}
	persisted, err = server.store.GetCorrelationRuleset(created.ID)
	if err != nil {
		t.Fatalf("load updated ruleset: %v", err)
	}
	if persisted.Rules[0].SampleValue != "" {
		t.Fatalf("update persisted learned sample %q", persisted.Rules[0].SampleValue)
	}
	staleUpdateReq := httptest.NewRequest(http.MethodPut, "/api/v1/correlation-rulesets/"+created.ID,
		strings.NewReader(`{"expected_updated_at":"`+createdUpdatedAt.Format(time.RFC3339Nano)+`","name":"Stale overwrite","rules":[]}`))
	staleUpdateReq.SetPathValue("id", created.ID)
	staleUpdateResp := httptest.NewRecorder()
	server.handleUpdateCorrelationRuleset(staleUpdateResp, staleUpdateReq)
	if staleUpdateResp.Code != http.StatusConflict {
		t.Fatalf("stale update = %d, want 409 body=%s", staleUpdateResp.Code, staleUpdateResp.Body.String())
	}

	delReq := httptest.NewRequest(http.MethodDelete, "/api/v1/correlation-rulesets/"+created.ID, nil)
	delReq.SetPathValue("id", created.ID)
	delResp := httptest.NewRecorder()
	server.handleDeleteCorrelationRuleset(delResp, delReq)
	if delResp.Code != http.StatusOK {
		t.Fatalf("delete = %d", delResp.Code)
	}
}

type racingCorrelationRulesetService struct {
	correlationRulesetService
	runner         *TestRunner
	raced          bool
	expectedAt     time.Time
	expectedDigest string
}

type missingTargetCorrelationRulesetService struct {
	correlationRulesetService
}

type replacementCorrelationRulesetService struct {
	correlationRulesetService
	replacement *storage.CorrelationRuleset
}

func (service *replacementCorrelationRulesetService) GetRuleset(string) (*storage.CorrelationRuleset, error) {
	return service.replacement, nil
}

func (service *missingTargetCorrelationRulesetService) UpdateScriptIfUnchangedAndRulesetCurrent(
	string,
	*dsl.Script,
	string,
	time.Time,
	string,
	string,
	time.Time,
) (bool, error) {
	return false, nil
}

func (service *racingCorrelationRulesetService) UpdateScriptIfUnchangedAndRulesetCurrent(
	id string,
	script *dsl.Script,
	projectID string,
	expectedAt time.Time,
	expectedDigest string,
	rulesetID string,
	expectedRulesetUpdatedAt time.Time,
) (bool, error) {
	service.expectedAt = expectedAt
	service.expectedDigest = expectedDigest
	if !service.raced {
		service.raced = true
		current, ok := service.runner.GetScript(id)
		if !ok || current == nil || current.Script == nil {
			return false, errors.New("target script disappeared before race")
		}
		concurrent := cloneScriptDefinition(current.Script)
		concurrent.Name = "Concurrent editor won"
		if _, updated, err := service.runner.UpdateScriptDurableInProject(id, concurrent, projectID); err != nil || !updated {
			return false, fmt.Errorf("inject concurrent edit: updated=%t err=%w", updated, err)
		}
	}
	return service.correlationRulesetService.UpdateScriptIfUnchangedAndRulesetCurrent(
		id,
		script,
		projectID,
		expectedAt,
		expectedDigest,
		rulesetID,
		expectedRulesetUpdatedAt,
	)
}

type racingCorrelationRulesetDeleteService struct {
	correlationRulesetService
	raced bool
}

type racingCorrelationRulesetApplyRevisionService struct {
	correlationRulesetService
	raced bool
}

func (service *racingCorrelationRulesetApplyRevisionService) UpdateScriptIfUnchangedAndRulesetCurrent(
	id string,
	script *dsl.Script,
	projectID string,
	expectedAt time.Time,
	expectedDigest string,
	rulesetID string,
	expectedRulesetUpdatedAt time.Time,
) (bool, error) {
	if !service.raced {
		service.raced = true
		current, err := service.GetRuleset(rulesetID)
		if err != nil {
			return false, err
		}
		current.Name = "Concurrent ruleset editor won"
		if updated, err := service.UpdateRulesetIfUnchanged(current, current.UpdatedAt); err != nil || !updated {
			return false, fmt.Errorf("inject concurrent ruleset edit: updated=%t err=%w", updated, err)
		}
	}
	return service.correlationRulesetService.UpdateScriptIfUnchangedAndRulesetCurrent(
		id,
		script,
		projectID,
		expectedAt,
		expectedDigest,
		rulesetID,
		expectedRulesetUpdatedAt,
	)
}

func (service *racingCorrelationRulesetDeleteService) DeleteRulesetIfUnchanged(ruleset *storage.CorrelationRuleset, projectID string) (bool, error) {
	if !service.raced {
		service.raced = true
		current, err := service.GetRuleset(ruleset.ID)
		if err != nil {
			return false, err
		}
		current.Name = "Concurrent editor won"
		if updated, err := service.UpdateRulesetIfUnchanged(current, current.UpdatedAt); err != nil || !updated {
			return false, fmt.Errorf("inject concurrent ruleset edit: updated=%t err=%w", updated, err)
		}
	}
	return service.correlationRulesetService.DeleteRulesetIfUnchanged(ruleset, projectID)
}

func TestCorrelationRulesetDeleteRejectsConcurrentUpdate(t *testing.T) {
	server := recordingTestServer(t)
	ruleset := &storage.CorrelationRuleset{ID: "delete-race", ProjectID: "p1", Name: "original"}
	if err := server.store.SaveCorrelationRuleset(ruleset); err != nil {
		t.Fatalf("SaveCorrelationRuleset: %v", err)
	}
	service := &racingCorrelationRulesetDeleteService{
		correlationRulesetService: newTestRunnerCorrelationRulesetService(server.runner, server.store),
	}
	server.correlationRulesetService = service
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/correlation-rulesets/"+ruleset.ID, nil)
	req.SetPathValue("id", ruleset.ID)
	resp := httptest.NewRecorder()
	server.handleDeleteCorrelationRuleset(resp, req)
	if resp.Code != http.StatusConflict {
		t.Fatalf("delete race status = %d body=%s, want 409", resp.Code, resp.Body.String())
	}
	persisted, err := server.store.GetCorrelationRuleset(ruleset.ID)
	if err != nil {
		t.Fatalf("GetCorrelationRuleset: %v", err)
	}
	if persisted.Name != "Concurrent editor won" {
		t.Fatalf("delete race lost concurrent row: %#v", persisted)
	}
}

func TestCorrelationRulesetHandlersRejectCrossProjectReplacementSnapshot(t *testing.T) {
	server := recordingTestServer(t)
	server.correlationRulesetService = &replacementCorrelationRulesetService{
		correlationRulesetService: server.correlationRulesetDomainService(),
		replacement: &storage.CorrelationRuleset{
			ID:          "replacement-race",
			ProjectID:   "p2",
			Name:        "private-foreign-ruleset",
			Description: "private-foreign-description",
			Rules: []storage.CorrelationRule{{
				Name:       "private-foreign-rule",
				Expression: "private-foreign-expression",
			}},
			UpdatedAt: time.Now().UTC(),
		},
	}

	tests := []struct {
		name   string
		method string
		body   string
		handle func(http.ResponseWriter, *http.Request)
	}{
		{name: "get", method: http.MethodGet, handle: server.handleGetCorrelationRuleset},
		{
			name:   "update",
			method: http.MethodPut,
			body:   `{"name":"attacker-update","rules":[],"expected_updated_at":"2030-01-01T00:00:00Z"}`,
			handle: server.handleUpdateCorrelationRuleset,
		},
		{
			name:   "apply",
			method: http.MethodPost,
			body:   `{"script_id":"attacker-target","apply":true}`,
			handle: server.handleApplyCorrelationRulesetToScript,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(
				test.method,
				"/api/v1/correlation-rulesets/replacement-race",
				strings.NewReader(test.body),
			)
			request.SetPathValue("id", "replacement-race")
			// This is the old row/project snapshot the route middleware
			// authorized immediately before the same ID was replaced in p2.
			bindAuthorizedProjectToRequest(request, "p1")
			response := httptest.NewRecorder()
			test.handle(response, request)
			if response.Code != http.StatusNotFound {
				t.Fatalf("replacement-race %s = %d body=%s, want 404", test.name, response.Code, response.Body.String())
			}
			for _, leaked := range []string{
				"private-foreign-ruleset",
				"private-foreign-description",
				"private-foreign-rule",
				"private-foreign-expression",
			} {
				if strings.Contains(response.Body.String(), leaked) {
					t.Fatalf("replacement-race %s leaked %q: %s", test.name, leaked, response.Body.String())
				}
			}
		})
	}
}

// Sample literals are suppressed at both the storage and response boundaries.
// Viewer-scoped GET/list still must not perform durable writes or advance the
// optimistic version.
func TestCorrelationRulesetSamplesAreNeverSerializedOrVersionedByReads(t *testing.T) {
	server := recordingTestServer(t)
	const getSecret = "LEGACY-GET-SAMPLE-1111"
	const listSecret = "LEGACY-LIST-SAMPLE-2222"
	for _, ruleset := range []*storage.CorrelationRuleset{
		{
			ID: "legacy-get", ProjectID: "p1", Name: "Legacy get",
			Rules: []storage.CorrelationRule{{Name: "csrf", Source: "response_body", ExtractType: "jsonpath", Expression: "$.csrf", SampleValue: getSecret}},
		},
		{
			ID: "legacy-list", ProjectID: "p1", Name: "Legacy list",
			Rules: []storage.CorrelationRule{{Name: "session", Source: "response_body", ExtractType: "jsonpath", Expression: "$.session", SampleValue: listSecret}},
		},
	} {
		if err := server.store.SaveCorrelationRuleset(ruleset); err != nil {
			t.Fatalf("seed %s: %v", ruleset.ID, err)
		}
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/correlation-rulesets/legacy-get", nil)
	getReq.SetPathValue("id", "legacy-get")
	getResp := httptest.NewRecorder()
	server.handleGetCorrelationRuleset(getResp, getReq)
	if getResp.Code != http.StatusOK || strings.Contains(getResp.Body.String(), getSecret) || strings.Contains(getResp.Body.String(), "sample_value") {
		t.Fatalf("legacy get was not disclosure-safe: status=%d body=%s", getResp.Code, getResp.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/correlation-rulesets?project_id=p1", nil)
	listResp := httptest.NewRecorder()
	server.handleListCorrelationRulesets(listResp, listReq)
	if listResp.Code != http.StatusOK || strings.Contains(listResp.Body.String(), getSecret) || strings.Contains(listResp.Body.String(), listSecret) || strings.Contains(listResp.Body.String(), "sample_value") {
		t.Fatalf("legacy list was not disclosure-safe: status=%d body=%s", listResp.Code, listResp.Body.String())
	}

	// Read paths must not write: the GET response reports the durable updated_at
	// rather than a phantom one. The storage boundary has already omitted the
	// legacy sample from the persisted rule shape.
	seededGet, err := server.store.GetCorrelationRuleset("legacy-get")
	if err != nil {
		t.Fatalf("reload legacy-get: %v", err)
	}
	if len(seededGet.Rules) != 1 || seededGet.Rules[0].SampleValue != "" {
		t.Fatalf("storage read exposed a learned sample: %#v", seededGet.Rules)
	}
	var getBody struct {
		UpdatedAt time.Time `json:"updated_at"`
	}
	if err := json.Unmarshal(getResp.Body.Bytes(), &getBody); err != nil {
		t.Fatalf("decode get response: %v", err)
	}
	if !getBody.UpdatedAt.Equal(seededGet.UpdatedAt) {
		t.Fatalf("GET updated_at = %s, stored row has %s (phantom CAS timestamp)", getBody.UpdatedAt, seededGet.UpdatedAt)
	}

	// A real write remains sample-free and uses the unchanged durable version.
	updatePayload, err := json.Marshal(map[string]any{
		"name":                "Legacy get",
		"rules":               []map[string]any{{"name": "csrf", "source": "response_body", "extract_type": "jsonpath", "expression": "$.csrf"}},
		"expected_updated_at": seededGet.UpdatedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	updateReq := httptest.NewRequest(http.MethodPut, "/api/v1/correlation-rulesets/legacy-get", bytes.NewReader(updatePayload))
	updateReq.SetPathValue("id", "legacy-get")
	updateResp := httptest.NewRecorder()
	server.handleUpdateCorrelationRuleset(updateResp, updateReq)
	if updateResp.Code != http.StatusOK {
		t.Fatalf("update = %d body=%s", updateResp.Code, updateResp.Body.String())
	}
	persisted, err := server.store.GetCorrelationRuleset("legacy-get")
	if err != nil {
		t.Fatalf("reload after update: %v", err)
	}
	if len(persisted.Rules) != 1 || persisted.Rules[0].SampleValue != "" {
		t.Fatalf("real write did not scrub the persisted sample: %#v", persisted.Rules)
	}
}

// TestCorrelationRulesetApplyRejectsCrossProjectScript verifies that an
// apply-to-script request authorized against the ruleset's project cannot read
// or mutate a target script that belongs to a different project.
func TestCorrelationRulesetApplyRejectsCrossProjectScript(t *testing.T) {
	server := recordingTestServer(t)

	// Ruleset lives in project p1.
	createBody := `{"project_id":"p1","name":"Checkout rules","rules":[{"name":"csrf","source":"response_body","extract_type":"boundary","expression":"x"}]}`
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/correlation-rulesets", strings.NewReader(createBody))
	createResp := httptest.NewRecorder()
	server.handleCreateCorrelationRuleset(createResp, createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create ruleset = %d body=%s", createResp.Code, createResp.Body.String())
	}
	var ruleset storage.CorrelationRuleset
	json.NewDecoder(createResp.Body).Decode(&ruleset)

	// Target script lives in a DIFFERENT project (p2).
	target := server.runner.StoreScript(&dsl.Script{
		Name:      "Other project script",
		Protocol:  "http",
		ProjectID: "p2",
		Config:    dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions:   []dsl.Action{{Name: "login", Type: "request", Method: "POST", Target: "/login"}},
	})

	applyReq := httptest.NewRequest(http.MethodPost, "/api/v1/correlation-rulesets/"+ruleset.ID+"/apply-to-script", strings.NewReader(`{"script_id":"`+target.ID+`","apply":true}`))
	applyReq.SetPathValue("id", ruleset.ID)
	applyResp := httptest.NewRecorder()
	server.handleApplyCorrelationRulesetToScript(applyResp, applyReq)
	if applyResp.Code != http.StatusNotFound {
		t.Fatalf("cross-project apply = %d, want 404; body=%s", applyResp.Code, applyResp.Body.String())
	}

	// The target script in the other project must be untouched.
	reloaded, ok := server.runner.GetScript(target.ID)
	if !ok {
		t.Fatal("target script missing")
	}
	for _, action := range reloaded.Script.Actions {
		if len(action.Extractors) != 0 {
			t.Fatalf("cross-project apply mutated the target script")
		}
	}
}

// seedScriptWithCorrelatedBaseline stores an HTTP script whose second action
// reuses a token that the first action's response produced, plus a matching
// recorded baseline, so correlate.Detect finds exactly one correlation.
func seedScriptWithCorrelatedBaseline(t *testing.T, server *Server) *StoredScript {
	t.Helper()
	stored := server.runner.StoreScript(&dsl.Script{
		Name:      "Login + Checkout",
		Protocol:  "http",
		ProjectID: "p1",
		Config:    dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{
			{Name: "login", Type: "request", Method: "POST", Target: "/login"},
			{Name: "checkout", Type: "request", Method: "POST", Target: "/checkout", Body: `{"order_id":"ORDER1234567"}`},
		},
	})
	digest, err := scriptCorrelationActionGraphDigest(stored.Script)
	if err != nil {
		t.Fatalf("digest action graph: %v", err)
	}
	saveEncryptedRecordingTestBaseline(t, server, stored.ID, digest, 2, []storage.ScriptRecordingObservation{
		{ID: 0, RequestURL: "/login", ResponseBody: `{"order_id":"ORDER1234567"}`},
		{ID: 1, RequestURL: "/checkout", RequestBody: `{"order_id":"ORDER1234567"}`},
	})
	return stored
}

func seedScriptWithOversizedCorrelationBaseline(t *testing.T, server *Server) *StoredScript {
	t.Helper()
	stored := server.runner.StoreScript(&dsl.Script{
		Name:      "Oversized correlation baseline",
		Protocol:  "http",
		ProjectID: "p1",
		Config:    dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions:   []dsl.Action{{Name: "load", Type: "request", Method: http.MethodGet, Target: "/load"}},
	})
	digest, err := scriptCorrelationActionGraphDigest(stored.Script)
	if err != nil {
		t.Fatal(err)
	}
	var responseBody strings.Builder
	responseBody.WriteByte('{')
	for index := 0; index <= maxCorrelationCandidatesPerObservationForAPITest(); index++ {
		if index > 0 {
			responseBody.WriteByte(',')
		}
		fmt.Fprintf(&responseBody, `"token_%d":"token-value-%06d"`, index, index)
	}
	responseBody.WriteByte('}')
	// The baseline is structurally valid but its response candidate set is
	// truncated. Ruleset discovery/apply must not persist or use that prefix.
	saveEncryptedRecordingTestBaseline(t, server, stored.ID, digest, 1, []storage.ScriptRecordingObservation{
		{ID: 0, ResponseBody: responseBody.String()},
	})
	return stored
}

func TestCorrelationRulesetHandlersRejectDeterministicWorkLimit(t *testing.T) {
	t.Run("discover", func(t *testing.T) {
		server := recordingTestServer(t)
		stored := seedScriptWithOversizedCorrelationBaseline(t, server)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/correlation-rulesets/discover-from-script", strings.NewReader(`{"script_id":"`+stored.ID+`"}`))
		resp := httptest.NewRecorder()
		server.handleDiscoverCorrelationRulesetFromScript(resp, req)
		if resp.Code != http.StatusUnprocessableEntity || !strings.Contains(resp.Body.String(), "work limits") {
			t.Fatalf("discover = %d body=%s, want deterministic work-limit rejection", resp.Code, resp.Body.String())
		}
	})

	t.Run("apply", func(t *testing.T) {
		server := recordingTestServer(t)
		stored := seedScriptWithOversizedCorrelationBaseline(t, server)
		ruleset := &storage.CorrelationRuleset{
			ID: "rules-work-limit", ProjectID: "p1", Name: "Rules",
			Rules: []storage.CorrelationRule{{Name: "token", RuleID: "json_token", Source: "response_body", ExtractType: "jsonpath", Expression: "$.token"}},
		}
		if err := server.store.SaveCorrelationRuleset(ruleset); err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/correlation-rulesets/"+ruleset.ID+"/apply-to-script", strings.NewReader(`{"script_id":"`+stored.ID+`","apply":true}`))
		req.SetPathValue("id", ruleset.ID)
		resp := httptest.NewRecorder()
		server.handleApplyCorrelationRulesetToScript(resp, req)
		if resp.Code != http.StatusUnprocessableEntity || !strings.Contains(resp.Body.String(), "work limits") {
			t.Fatalf("apply = %d body=%s, want deterministic work-limit rejection", resp.Code, resp.Body.String())
		}
	})
}

func TestCorrelationRulesetApplyRejectsIncompleteSuggestionAnalysis(t *testing.T) {
	response := httptest.NewRecorder()
	if !rejectIncompleteCorrelationSuggestions(response, paramize.Result{
		Warnings: []string{"Correlation analysis was not completed and no correlation suggestions were produced: deterministic correlation work limit exceeded"},
	}) {
		t.Fatal("warning-bearing correlation result was accepted")
	}
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "work limits") {
		t.Fatalf("response = %d body=%s, want fail-closed 422", response.Code, response.Body.String())
	}

	clean := httptest.NewRecorder()
	if rejectIncompleteCorrelationSuggestions(clean, paramize.Result{}) {
		t.Fatal("clean correlation result was rejected")
	}
}

func TestRecordingBaselineCompleteForAutomation(t *testing.T) {
	completeRecord := func() *storage.ScriptRecordingRecord {
		return &storage.ScriptRecordingRecord{
			RequestCount: 2,
			Observations: []storage.ScriptRecordingObservation{
				{ID: 0},
				{ID: 1},
			},
		}
	}
	completeBaseline := []correlate.Observation{{ID: 0}, {ID: 1}}

	tests := []struct {
		name     string
		mutate   func(*storage.ScriptRecordingRecord, []correlate.Observation) (*storage.ScriptRecordingRecord, []correlate.Observation)
		complete bool
	}{
		{name: "complete", complete: true},
		{
			name: "retained prefix",
			mutate: func(record *storage.ScriptRecordingRecord, baseline []correlate.Observation) (*storage.ScriptRecordingRecord, []correlate.Observation) {
				record.RequestCount = 3
				return record, baseline
			},
		},
		{
			name: "legacy missing original count",
			mutate: func(record *storage.ScriptRecordingRecord, baseline []correlate.Observation) (*storage.ScriptRecordingRecord, []correlate.Observation) {
				record.RequestCount = 0
				return record, baseline
			},
		},
		{
			name: "count smaller than sidecar",
			mutate: func(record *storage.ScriptRecordingRecord, baseline []correlate.Observation) (*storage.ScriptRecordingRecord, []correlate.Observation) {
				record.RequestCount = 1
				return record, baseline
			},
		},
		{
			name: "structural projection shorter than sidecar",
			mutate: func(record *storage.ScriptRecordingRecord, baseline []correlate.Observation) (*storage.ScriptRecordingRecord, []correlate.Observation) {
				record.Observations = record.Observations[:1]
				return record, baseline
			},
		},
		{
			name: "structural projection id mismatch",
			mutate: func(record *storage.ScriptRecordingRecord, baseline []correlate.Observation) (*storage.ScriptRecordingRecord, []correlate.Observation) {
				record.Observations[1].ID = 7
				return record, baseline
			},
		},
		{
			name: "empty",
			mutate: func(record *storage.ScriptRecordingRecord, _ []correlate.Observation) (*storage.ScriptRecordingRecord, []correlate.Observation) {
				record.RequestCount = 0
				record.Observations = nil
				return record, nil
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := completeRecord()
			baseline := append([]correlate.Observation(nil), completeBaseline...)
			if test.mutate != nil {
				record, baseline = test.mutate(record, baseline)
			}
			if got := recordingBaselineCompleteForAutomation(record, baseline); got != test.complete {
				t.Fatalf("complete = %t, want %t", got, test.complete)
			}
		})
	}
}

func TestCorrelationRulesetAutomationRejectsTruncatedRecordingBaseline(t *testing.T) {
	t.Run("discover", func(t *testing.T) {
		server := recordingTestServer(t)
		stored := seedScriptWithCorrelatedBaseline(t, server)
		record, err := server.store.GetScriptRecording(stored.ID)
		if err != nil {
			t.Fatal(err)
		}
		record.RequestCount = len(record.Observations) + 1
		if err := server.store.SaveScriptRecording(record); err != nil {
			t.Fatal(err)
		}

		req := httptest.NewRequest(http.MethodPost, "/api/v1/correlation-rulesets/discover-from-script", strings.NewReader(`{"script_id":"`+stored.ID+`"}`))
		resp := httptest.NewRecorder()
		server.handleDiscoverCorrelationRulesetFromScript(resp, req)
		if resp.Code != http.StatusConflict || !strings.Contains(resp.Body.String(), "incomplete or inconsistent") || !strings.Contains(resp.Body.String(), "capture a new recording") {
			t.Fatalf("discover = %d body=%s, want fail-closed 409", resp.Code, resp.Body.String())
		}
		rulesets, err := server.store.ListCorrelationRulesets("p1", 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(rulesets) != 0 {
			t.Fatalf("truncated discovery persisted %d rulesets", len(rulesets))
		}
	})

	t.Run("apply", func(t *testing.T) {
		server := recordingTestServer(t)
		stored := seedScriptWithCorrelatedBaseline(t, server)
		record, err := server.store.GetScriptRecording(stored.ID)
		if err != nil {
			t.Fatal(err)
		}
		record.RequestCount = len(record.Observations) + 1
		if err := server.store.SaveScriptRecording(record); err != nil {
			t.Fatal(err)
		}
		ruleset := &storage.CorrelationRuleset{
			ID: "rules-truncated", ProjectID: "p1", Name: "Rules",
			Rules: []storage.CorrelationRule{{
				Name: "order", RuleID: "json_order_id", Source: "response_body", ExtractType: "jsonpath", Expression: "$.order_id",
			}},
		}
		if err := server.store.SaveCorrelationRuleset(ruleset); err != nil {
			t.Fatal(err)
		}
		before := stored.Script.Actions[1].Body

		req := httptest.NewRequest(http.MethodPost, "/api/v1/correlation-rulesets/"+ruleset.ID+"/apply-to-script", strings.NewReader(`{"script_id":"`+stored.ID+`","apply":true}`))
		req.SetPathValue("id", ruleset.ID)
		resp := httptest.NewRecorder()
		server.handleApplyCorrelationRulesetToScript(resp, req)
		if resp.Code != http.StatusConflict || !strings.Contains(resp.Body.String(), "incomplete or inconsistent") || !strings.Contains(resp.Body.String(), "capture a new recording") {
			t.Fatalf("apply = %d body=%s, want fail-closed 409", resp.Code, resp.Body.String())
		}
		current, ok := server.runner.GetScript(stored.ID)
		if !ok || current.Script.Actions[1].Body != before {
			t.Fatalf("truncated apply mutated target script: %#v", current)
		}
	})
}

func TestCorrelationRulesetAutomationRequiresCurrentBoundActionGraph(t *testing.T) {
	t.Run("legacy recording", func(t *testing.T) {
		server := recordingTestServer(t)
		stored := seedScriptWithCorrelatedBaseline(t, server)
		record, err := server.store.GetScriptRecording(stored.ID)
		if err != nil {
			t.Fatal(err)
		}
		record.ActionGraphDigest = ""
		if err := server.store.SaveScriptRecording(record); err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/correlation-rulesets/discover-from-script", strings.NewReader(`{"script_id":"`+stored.ID+`"}`))
		resp := httptest.NewRecorder()
		server.handleDiscoverCorrelationRulesetFromScript(resp, req)
		if resp.Code != http.StatusConflict || !strings.Contains(resp.Body.String(), "does not match") || !strings.Contains(resp.Body.String(), "capture a new recording") {
			t.Fatalf("legacy discover = %d body=%s, want 409 recapture guidance", resp.Code, resp.Body.String())
		}
	})

	t.Run("script edited after capture", func(t *testing.T) {
		server := recordingTestServer(t)
		stored := seedScriptWithCorrelatedBaseline(t, server)
		discoverReq := httptest.NewRequest(http.MethodPost, "/api/v1/correlation-rulesets/discover-from-script", strings.NewReader(`{"script_id":"`+stored.ID+`"}`))
		discoverResp := httptest.NewRecorder()
		server.handleDiscoverCorrelationRulesetFromScript(discoverResp, discoverReq)
		if discoverResp.Code != http.StatusCreated {
			t.Fatalf("seed discover = %d body=%s", discoverResp.Code, discoverResp.Body.String())
		}
		var ruleset storage.CorrelationRuleset
		if err := json.Unmarshal(discoverResp.Body.Bytes(), &ruleset); err != nil {
			t.Fatal(err)
		}

		changed := *stored.Script
		changed.Actions = append([]dsl.Action(nil), stored.Script.Actions...)
		changed.Actions[1].Body = `{"order_id":"edited"}`
		if _, ok := server.runner.UpdateScript(stored.ID, &changed); !ok {
			t.Fatal("failed to edit target script")
		}

		discoverReq = httptest.NewRequest(http.MethodPost, "/api/v1/correlation-rulesets/discover-from-script", strings.NewReader(`{"script_id":"`+stored.ID+`"}`))
		discoverResp = httptest.NewRecorder()
		server.handleDiscoverCorrelationRulesetFromScript(discoverResp, discoverReq)
		if discoverResp.Code != http.StatusConflict || !strings.Contains(discoverResp.Body.String(), "capture a new recording") {
			t.Fatalf("changed discover = %d body=%s, want 409", discoverResp.Code, discoverResp.Body.String())
		}

		applyReq := httptest.NewRequest(http.MethodPost, "/api/v1/correlation-rulesets/"+ruleset.ID+"/apply-to-script", strings.NewReader(`{"script_id":"`+stored.ID+`","apply":true}`))
		applyReq.SetPathValue("id", ruleset.ID)
		applyResp := httptest.NewRecorder()
		server.handleApplyCorrelationRulesetToScript(applyResp, applyReq)
		if applyResp.Code != http.StatusConflict || !strings.Contains(applyResp.Body.String(), "capture a new recording") {
			t.Fatalf("changed apply = %d body=%s, want 409", applyResp.Code, applyResp.Body.String())
		}
	})
}

func TestCorrelationRulesetDiscoverAndApply(t *testing.T) {
	server := recordingTestServer(t)
	stored := seedScriptWithCorrelatedBaseline(t, server)

	// Discover: learn correlations from the baseline.
	discoverReq := httptest.NewRequest(http.MethodPost, "/api/v1/correlation-rulesets/discover-from-script", strings.NewReader(`{"script_id":"`+stored.ID+`"}`))
	discoverResp := httptest.NewRecorder()
	server.handleDiscoverCorrelationRulesetFromScript(discoverResp, discoverReq)
	if discoverResp.Code != http.StatusCreated {
		t.Fatalf("discover = %d body=%s", discoverResp.Code, discoverResp.Body.String())
	}
	discoverResponseJSON := discoverResp.Body.String()
	var ruleset storage.CorrelationRuleset
	if err := json.Unmarshal([]byte(discoverResponseJSON), &ruleset); err != nil {
		t.Fatalf("decode discover response: %v", err)
	}
	if len(ruleset.Rules) == 0 {
		t.Fatalf("discover produced no rules: %#v", ruleset)
	}
	if ruleset.Rules[0].SampleValue != "" || strings.Contains(discoverResponseJSON, "ORDER1234567") || strings.Contains(discoverResponseJSON, "sample_value") {
		t.Fatalf("discover response disclosed a baseline sample: %s", discoverResponseJSON)
	}
	persistedRuleset, err := server.store.GetCorrelationRuleset(ruleset.ID)
	if err != nil {
		t.Fatalf("load discovered ruleset: %v", err)
	}
	for _, rule := range persistedRuleset.Rules {
		if rule.SampleValue != "" {
			t.Fatalf("discover persisted a baseline sample: %#v", persistedRuleset.Rules)
		}
	}

	// Apply (preview): matches the ruleset against the script's correlations.
	applyBody := `{"script_id":"` + stored.ID + `"}`
	applyReq := httptest.NewRequest(http.MethodPost, "/api/v1/correlation-rulesets/"+ruleset.ID+"/apply-to-script", strings.NewReader(applyBody))
	applyReq.SetPathValue("id", ruleset.ID)
	applyResp := httptest.NewRecorder()
	server.handleApplyCorrelationRulesetToScript(applyResp, applyReq)
	if applyResp.Code != http.StatusOK {
		t.Fatalf("apply preview = %d body=%s", applyResp.Code, applyResp.Body.String())
	}
	previewJSON := applyResp.Body.String()
	var preview struct {
		MatchedRules int    `json:"matched_rules"`
		Applied      bool   `json:"applied"`
		PreviewToken string `json:"preview_token"`
		Diffs        []struct {
			Kind string `json:"kind"`
		} `json:"diffs"`
	}
	if err := json.Unmarshal([]byte(previewJSON), &preview); err != nil {
		t.Fatalf("decode apply preview: %v", err)
	}
	if preview.MatchedRules == 0 || preview.Applied {
		t.Fatalf("preview unexpected: matched=%d applied=%v", preview.MatchedRules, preview.Applied)
	}
	if len(preview.Diffs) == 0 {
		t.Fatalf("preview produced no diffs")
	}
	if preview.PreviewToken == "" {
		t.Fatal("preview did not return an opaque apply token")
	}
	if strings.Contains(previewJSON, "ORDER1234567") || strings.Contains(previewJSON, `"suggestions"`) ||
		strings.Contains(previewJSON, `"before"`) || strings.Contains(previewJSON, `"after"`) {
		t.Fatalf("preview disclosed decrypted baseline detail: %s", previewJSON)
	}
	// A supplied-but-stale token still fails closed: the two-step protocol
	// guards against applying a preview the caller has not actually seen.
	staleTokenReq := httptest.NewRequest(http.MethodPost, "/api/v1/correlation-rulesets/"+ruleset.ID+"/apply-to-script", strings.NewReader(`{"script_id":"`+stored.ID+`","apply":true,"preview_token":"crp1_stale-token"}`))
	staleTokenReq.SetPathValue("id", ruleset.ID)
	staleTokenResp := httptest.NewRecorder()
	server.handleApplyCorrelationRulesetToScript(staleTokenResp, staleTokenReq)
	if staleTokenResp.Code != http.StatusConflict {
		t.Fatalf("apply with stale preview token = %d, want 409 body=%s", staleTokenResp.Code, staleTokenResp.Body.String())
	}

	// Apply (commit): writes extractor + templating into a new script version.
	commitPayload, err := json.Marshal(map[string]any{"script_id": stored.ID, "apply": true, "preview_token": preview.PreviewToken})
	if err != nil {
		t.Fatal(err)
	}
	commitReq := httptest.NewRequest(http.MethodPost, "/api/v1/correlation-rulesets/"+ruleset.ID+"/apply-to-script", strings.NewReader(string(commitPayload)))
	commitReq.SetPathValue("id", ruleset.ID)
	commitResp := httptest.NewRecorder()
	server.handleApplyCorrelationRulesetToScript(commitResp, commitReq)
	if commitResp.Code != http.StatusOK {
		t.Fatalf("apply commit = %d", commitResp.Code)
	}
	var commit struct {
		Applied bool `json:"applied"`
	}
	json.NewDecoder(commitResp.Body).Decode(&commit)
	if !commit.Applied {
		t.Fatalf("expected applied=true")
	}
	reloaded, ok := server.runner.GetScript(stored.ID)
	if !ok {
		t.Fatal("script missing after apply")
	}
	extractorCount := 0
	for _, action := range reloaded.Script.Actions {
		extractorCount += len(action.Extractors)
	}
	if extractorCount == 0 {
		t.Fatalf("apply did not add any extractor to the script")
	}
	if len(reloaded.Script.Actions[0].Extractors) != 1 {
		t.Fatalf("first action extractors = %#v, want recorder-baseline extractor on source action", reloaded.Script.Actions[0].Extractors)
	}
	if strings.Contains(reloaded.Script.Actions[1].Body, "ORDER1234567") ||
		!strings.Contains(reloaded.Script.Actions[1].Body, "{{var.") {
		t.Fatalf("second action body = %q, want recorded token templated on the used action", reloaded.Script.Actions[1].Body)
	}
}

// One-shot API automation: apply=true without a preview_token recomputes the
// preview server-side within the same request and applies against exactly that
// state (the previously published single-call contract).
func TestCorrelationRulesetOneShotApplyWithoutPreviewToken(t *testing.T) {
	server := recordingTestServer(t)
	stored := seedScriptWithCorrelatedBaseline(t, server)
	discoverReq := httptest.NewRequest(http.MethodPost, "/api/v1/correlation-rulesets/discover-from-script", strings.NewReader(`{"script_id":"`+stored.ID+`"}`))
	discoverResp := httptest.NewRecorder()
	server.handleDiscoverCorrelationRulesetFromScript(discoverResp, discoverReq)
	if discoverResp.Code != http.StatusCreated {
		t.Fatalf("discover = %d body=%s", discoverResp.Code, discoverResp.Body.String())
	}
	var ruleset storage.CorrelationRuleset
	if err := json.Unmarshal(discoverResp.Body.Bytes(), &ruleset); err != nil {
		t.Fatal(err)
	}

	applyReq := httptest.NewRequest(http.MethodPost, "/api/v1/correlation-rulesets/"+ruleset.ID+"/apply-to-script", strings.NewReader(`{"script_id":"`+stored.ID+`","apply":true}`))
	applyReq.SetPathValue("id", ruleset.ID)
	applyResp := httptest.NewRecorder()
	server.handleApplyCorrelationRulesetToScript(applyResp, applyReq)
	if applyResp.Code != http.StatusOK {
		t.Fatalf("one-shot apply = %d body=%s", applyResp.Code, applyResp.Body.String())
	}
	var result struct {
		Applied      bool `json:"applied"`
		MatchedRules int  `json:"matched_rules"`
	}
	if err := json.Unmarshal(applyResp.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Applied || result.MatchedRules == 0 {
		t.Fatalf("one-shot apply result = %+v, want applied with matches", result)
	}
	reloaded, ok := server.runner.GetScript(stored.ID)
	if !ok {
		t.Fatal("script missing after one-shot apply")
	}
	if len(reloaded.Script.Actions[0].Extractors) == 0 {
		t.Fatal("one-shot apply did not add the source extractor")
	}
}

func TestCorrelationRulesetAutomationResolvesRefKeyToCanonicalScriptID(t *testing.T) {
	server := recordingTestServer(t)
	stored := seedScriptWithCorrelatedBaseline(t, server)
	if stored.RefKey == "" {
		t.Fatalf("stored script has no reference key: %#v", stored)
	}

	discoverReq := httptest.NewRequest(http.MethodPost, "/api/v1/correlation-rulesets/discover-from-script", strings.NewReader(`{"script_id":"`+stored.RefKey+`"}`))
	discoverResp := httptest.NewRecorder()
	server.handleDiscoverCorrelationRulesetFromScript(discoverResp, discoverReq)
	if discoverResp.Code != http.StatusCreated {
		t.Fatalf("reference-key discover = %d body=%s", discoverResp.Code, discoverResp.Body.String())
	}
	var ruleset storage.CorrelationRuleset
	if err := json.Unmarshal(discoverResp.Body.Bytes(), &ruleset); err != nil {
		t.Fatal(err)
	}
	if ruleset.Source != "script:"+stored.ID {
		t.Fatalf("ruleset source = %q, want canonical script id %q", ruleset.Source, stored.ID)
	}

	applyReq := httptest.NewRequest(http.MethodPost, "/api/v1/correlation-rulesets/"+ruleset.ID+"/apply-to-script", strings.NewReader(`{"script_id":"`+stored.RefKey+`"}`))
	applyReq.SetPathValue("id", ruleset.ID)
	applyResp := httptest.NewRecorder()
	server.handleApplyCorrelationRulesetToScript(applyResp, applyReq)
	if applyResp.Code != http.StatusOK {
		t.Fatalf("reference-key apply preview = %d body=%s", applyResp.Code, applyResp.Body.String())
	}
	var preview struct {
		ScriptID string `json:"script_id"`
	}
	if err := json.Unmarshal(applyResp.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.ScriptID != stored.ID {
		t.Fatalf("preview script_id = %q, want canonical %q", preview.ScriptID, stored.ID)
	}
}

func TestCorrelationRulesetApplyRejectsConcurrentScriptChangeFromOriginalSnapshot(t *testing.T) {
	server := recordingTestServer(t)
	stored := seedScriptWithCorrelatedBaseline(t, server)
	discoverReq := httptest.NewRequest(http.MethodPost, "/api/v1/correlation-rulesets/discover-from-script", strings.NewReader(`{"script_id":"`+stored.ID+`"}`))
	discoverResp := httptest.NewRecorder()
	server.handleDiscoverCorrelationRulesetFromScript(discoverResp, discoverReq)
	if discoverResp.Code != http.StatusCreated {
		t.Fatalf("discover = %d body=%s", discoverResp.Code, discoverResp.Body.String())
	}
	var ruleset storage.CorrelationRuleset
	if err := json.Unmarshal(discoverResp.Body.Bytes(), &ruleset); err != nil {
		t.Fatal(err)
	}
	original, ok := server.runner.GetScript(stored.ID)
	if !ok || original == nil || original.Script == nil {
		t.Fatal("original script snapshot not found")
	}
	originalDigest, err := scriptCorrelationActionGraphDigest(original.Script)
	if err != nil {
		t.Fatal(err)
	}
	service := &racingCorrelationRulesetService{
		correlationRulesetService: server.correlationRulesetDomainService(),
		runner:                    server.runner,
	}
	previewReq := httptest.NewRequest(http.MethodPost, "/api/v1/correlation-rulesets/"+ruleset.ID+"/apply-to-script", strings.NewReader(`{"script_id":"`+stored.ID+`"}`))
	previewReq.SetPathValue("id", ruleset.ID)
	previewResp := httptest.NewRecorder()
	server.handleApplyCorrelationRulesetToScript(previewResp, previewReq)
	if previewResp.Code != http.StatusOK {
		t.Fatalf("preview = %d body=%s", previewResp.Code, previewResp.Body.String())
	}
	var preview struct {
		PreviewToken string `json:"preview_token"`
	}
	if err := json.Unmarshal(previewResp.Body.Bytes(), &preview); err != nil || preview.PreviewToken == "" {
		t.Fatalf("decode preview token: token=%q err=%v", preview.PreviewToken, err)
	}
	server.correlationRulesetService = service

	applyPayload, err := json.Marshal(map[string]any{"script_id": stored.ID, "apply": true, "preview_token": preview.PreviewToken})
	if err != nil {
		t.Fatal(err)
	}
	applyReq := httptest.NewRequest(http.MethodPost, "/api/v1/correlation-rulesets/"+ruleset.ID+"/apply-to-script", strings.NewReader(string(applyPayload)))
	applyReq.SetPathValue("id", ruleset.ID)
	applyResp := httptest.NewRecorder()
	server.handleApplyCorrelationRulesetToScript(applyResp, applyReq)
	if applyResp.Code != http.StatusConflict {
		t.Fatalf("apply = %d, want %d body=%s", applyResp.Code, http.StatusConflict, applyResp.Body.String())
	}
	if !service.expectedAt.Equal(original.Script.UpdatedAt) || service.expectedDigest != originalDigest {
		t.Fatalf("CAS fence = at:%v digest:%q, want original at:%v digest:%q", service.expectedAt, service.expectedDigest, original.Script.UpdatedAt, originalDigest)
	}
	reloaded, ok := server.runner.GetScript(stored.ID)
	if !ok || reloaded == nil || reloaded.Script == nil {
		t.Fatal("script missing after concurrent edit")
	}
	if reloaded.Name != "Concurrent editor won" {
		t.Fatalf("script name = %q, concurrent edit was not preserved", reloaded.Name)
	}
	for _, action := range reloaded.Script.Actions {
		if len(action.Extractors) != 0 {
			t.Fatalf("stale ruleset clone overwrote concurrent edit: %#v", reloaded.Script.Actions)
		}
	}
}

func TestCorrelationRulesetApplyRejectsConcurrentRulesetRevisionAtCommit(t *testing.T) {
	server := recordingTestServer(t)
	stored := seedScriptWithCorrelatedBaseline(t, server)
	discoverReq := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/correlation-rulesets/discover-from-script",
		strings.NewReader(`{"script_id":"`+stored.ID+`"}`),
	)
	discoverResp := httptest.NewRecorder()
	server.handleDiscoverCorrelationRulesetFromScript(discoverResp, discoverReq)
	if discoverResp.Code != http.StatusCreated {
		t.Fatalf("discover = %d body=%s", discoverResp.Code, discoverResp.Body.String())
	}
	var ruleset storage.CorrelationRuleset
	if err := json.Unmarshal(discoverResp.Body.Bytes(), &ruleset); err != nil {
		t.Fatal(err)
	}
	service := &racingCorrelationRulesetApplyRevisionService{
		correlationRulesetService: server.correlationRulesetDomainService(),
	}
	server.correlationRulesetService = service

	applyReq := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/correlation-rulesets/"+ruleset.ID+"/apply-to-script",
		strings.NewReader(`{"script_id":"`+stored.ID+`","apply":true}`),
	)
	applyReq.SetPathValue("id", ruleset.ID)
	applyResp := httptest.NewRecorder()
	server.handleApplyCorrelationRulesetToScript(applyResp, applyReq)
	if applyResp.Code != http.StatusConflict {
		t.Fatalf("apply after concurrent ruleset edit = %d body=%s, want 409", applyResp.Code, applyResp.Body.String())
	}
	if !service.raced {
		t.Fatal("concurrent ruleset edit was not injected at the commit boundary")
	}
	reloadedScript, ok := server.runner.GetScript(stored.ID)
	if !ok || reloadedScript == nil || reloadedScript.Script == nil {
		t.Fatal("target script missing after rejected ruleset apply")
	}
	for _, action := range reloadedScript.Script.Actions {
		if len(action.Extractors) != 0 {
			t.Fatalf("stale ruleset revision mutated target script: %#v", reloadedScript.Script.Actions)
		}
	}
	reloadedRuleset, err := server.store.GetCorrelationRuleset(ruleset.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloadedRuleset.Name != "Concurrent ruleset editor won" {
		t.Fatalf("concurrent ruleset edit was lost: %#v", reloadedRuleset)
	}
}

func TestCorrelationRulesetApplyRejectsMissingTargetAtCommit(t *testing.T) {
	server := recordingTestServer(t)
	stored := seedScriptWithCorrelatedBaseline(t, server)
	discoverReq := httptest.NewRequest(http.MethodPost, "/api/v1/correlation-rulesets/discover-from-script", strings.NewReader(`{"script_id":"`+stored.ID+`"}`))
	discoverResp := httptest.NewRecorder()
	server.handleDiscoverCorrelationRulesetFromScript(discoverResp, discoverReq)
	if discoverResp.Code != http.StatusCreated {
		t.Fatalf("discover = %d body=%s", discoverResp.Code, discoverResp.Body.String())
	}
	var ruleset storage.CorrelationRuleset
	if err := json.Unmarshal(discoverResp.Body.Bytes(), &ruleset); err != nil {
		t.Fatal(err)
	}
	server.correlationRulesetService = &missingTargetCorrelationRulesetService{
		correlationRulesetService: server.correlationRulesetDomainService(),
	}

	applyReq := httptest.NewRequest(http.MethodPost, "/api/v1/correlation-rulesets/"+ruleset.ID+"/apply-to-script", strings.NewReader(`{"script_id":"`+stored.ID+`","apply":true}`))
	applyReq.SetPathValue("id", ruleset.ID)
	applyResp := httptest.NewRecorder()
	server.handleApplyCorrelationRulesetToScript(applyResp, applyReq)
	if applyResp.Code != http.StatusConflict {
		t.Fatalf("apply = %d, want %d body=%s", applyResp.Code, http.StatusConflict, applyResp.Body.String())
	}
}
