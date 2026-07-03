package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestCorrelationRulesetCRUD(t *testing.T) {
	server := recordingTestServer(t)

	body := `{"project_id":"p1","name":"Checkout rules","rules":[{"name":"csrf","source":"response_body","extract_type":"boundary","expression":"x"}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/correlation-rulesets", strings.NewReader(body))
	resp := httptest.NewRecorder()
	server.handleCreateCorrelationRuleset(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("create = %d body=%s", resp.Code, resp.Body.String())
	}
	var created storage.CorrelationRuleset
	json.NewDecoder(resp.Body).Decode(&created)
	if created.ID == "" || len(created.Rules) != 1 {
		t.Fatalf("unexpected created: %#v", created)
	}

	// Reject a nameless ruleset.
	bad := httptest.NewRequest(http.MethodPost, "/api/v1/correlation-rulesets", strings.NewReader(`{"project_id":"p1"}`))
	badResp := httptest.NewRecorder()
	server.handleCreateCorrelationRuleset(badResp, bad)
	if badResp.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing name, got %d", badResp.Code)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/correlation-rulesets?project_id=p1", nil)
	listResp := httptest.NewRecorder()
	server.handleListCorrelationRulesets(listResp, listReq)
	var list struct {
		Count int `json:"count"`
	}
	json.NewDecoder(listResp.Body).Decode(&list)
	if list.Count != 1 {
		t.Fatalf("list count = %d, want 1", list.Count)
	}

	delReq := httptest.NewRequest(http.MethodDelete, "/api/v1/correlation-rulesets/"+created.ID, nil)
	delReq.SetPathValue("id", created.ID)
	delResp := httptest.NewRecorder()
	server.handleDeleteCorrelationRuleset(delResp, delReq)
	if delResp.Code != http.StatusOK {
		t.Fatalf("delete = %d", delResp.Code)
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
	if applyResp.Code != http.StatusForbidden {
		t.Fatalf("cross-project apply = %d, want 403; body=%s", applyResp.Code, applyResp.Body.String())
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
		Name:     "Login + Checkout",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{
			{Name: "login", Type: "request", Method: "POST", Target: "/login"},
			{Name: "checkout", Type: "request", Method: "POST", Target: "/checkout", Body: `{"csrf":"TOKEN1234567"}`},
		},
	})
	if err := server.store.SaveScriptRecording(&storage.ScriptRecordingRecord{
		ScriptID: stored.ID,
		Observations: []storage.ScriptRecordingObservation{
			{ID: 0, RequestURL: "/login", ResponseBody: `{"csrf_token":"TOKEN1234567"}`},
			{ID: 1, RequestURL: "/checkout", RequestBody: `{"csrf":"TOKEN1234567"}`},
		},
	}); err != nil {
		t.Fatalf("save baseline: %v", err)
	}
	return stored
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
	var ruleset storage.CorrelationRuleset
	json.NewDecoder(discoverResp.Body).Decode(&ruleset)
	if len(ruleset.Rules) == 0 {
		t.Fatalf("discover produced no rules: %#v", ruleset)
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
	var preview struct {
		MatchedRules int  `json:"matched_rules"`
		Applied      bool `json:"applied"`
		Diffs        []struct {
			Kind string `json:"kind"`
		} `json:"diffs"`
	}
	json.NewDecoder(applyResp.Body).Decode(&preview)
	if preview.MatchedRules == 0 || preview.Applied {
		t.Fatalf("preview unexpected: matched=%d applied=%v", preview.MatchedRules, preview.Applied)
	}
	if len(preview.Diffs) == 0 {
		t.Fatalf("preview produced no diffs")
	}

	// Apply (commit): writes extractor + templating into a new script version.
	commitReq := httptest.NewRequest(http.MethodPost, "/api/v1/correlation-rulesets/"+ruleset.ID+"/apply-to-script", strings.NewReader(`{"script_id":"`+stored.ID+`","apply":true}`))
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
	if strings.Contains(reloaded.Script.Actions[1].Body, "TOKEN1234567") ||
		!strings.Contains(reloaded.Script.Actions[1].Body, "{{var.") {
		t.Fatalf("second action body = %q, want recorded token templated on the used action", reloaded.Script.Actions[1].Body)
	}
}
