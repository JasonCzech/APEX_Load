package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/paramize"
	"github.com/jasonczech/apexload/pkg/convert/sample"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func paramizeTestScript() *dsl.Script {
	return &dsl.Script{
		Name:     "Paramize Test",
		Version:  "1.0",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://api.example.com"},
		Actions: []dsl.Action{
			{
				Name:    "login",
				Type:    "http_request",
				Method:  "POST",
				Target:  "/login",
				Headers: map[string]string{"Authorization": "Bearer tok123"},
				Body:    `{"user_email": "jane@example.com", "password": "hunter2"}`,
			},
			{
				Name:   "orders",
				Type:   "http_request",
				Method: "GET",
				Target: "/orders?customer_id=42",
			},
		},
	}
}

func TestSampleCorrelationsToPreAppliedSkipsManualCandidates(t *testing.T) {
	converted := sampleCorrelationsToPreApplied([]sample.Correlation{
		{
			Name:        "corr_applied",
			Source:      "body",
			Expression:  "$.token",
			FoundInStep: 1,
			UsedInSteps: []int{2},
			PreApplied:  true,
		},
		{
			Name:         "corr_manual_encoding",
			Source:       "body",
			Expression:   "$.other",
			FoundInStep:  1,
			UsedInSteps:  []int{2},
			ReviewAction: "manual_value_replacement_required",
		},
	})
	if len(converted) != 1 || converted[0].Name != "corr_applied" {
		t.Fatalf("pre-applied correlations = %#v, want only executable correlation", converted)
	}
}

func postParameterizeApply(t *testing.T, server *Server, payload importParameterizeApplyRequest) (*httptest.ResponseRecorder, importParameterizeApplyResponse) {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/parameterize/apply", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	var decoded importParameterizeApplyResponse
	if resp.Code == http.StatusOK {
		if err := json.Unmarshal(resp.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("json.Unmarshal: %v (body=%s)", err, resp.Body.String())
		}
	}
	return resp, decoded
}

func TestHandleApplyImportParameterization(t *testing.T) {
	server := NewServer(ServerConfig{})
	script := paramizeTestScript()
	suggestions := buildImportParameterization(script, nil)
	if len(suggestions.Suggestions) == 0 {
		t.Fatal("expected suggestions for the test script")
	}

	var dataParamID string
	for _, s := range suggestions.Suggestions {
		if s.Kind == paramize.KindDataParam && s.VarName == "customer_id" {
			dataParamID = s.ID
		}
	}
	if dataParamID == "" {
		t.Fatalf("no customer_id data_param suggestion in %+v", suggestions.Suggestions)
	}

	resp, decoded := postParameterizeApply(t, server, importParameterizeApplyRequest{
		Script: script,
		// The wire suggestions are display-only. The server re-derives the
		// authoritative replacement from Script and the selected opaque ID.
		Suggestions: nil,
		SelectedIDs: []string{dataParamID},
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
	}
	if decoded.Blocked {
		t.Fatalf("unexpected blocked response: %s", decoded.BlockedReason)
	}
	if decoded.AppliedCount != 1 {
		t.Errorf("applied_count = %d, want 1", decoded.AppliedCount)
	}
	if decoded.Script == nil || !strings.Contains(decoded.Script.Actions[1].Target, "{{var.customer_id}}") {
		t.Errorf("target not parameterized: %+v", decoded.Script.Actions[1].Target)
	}
	if len(decoded.Script.DataSources) != 1 || len(decoded.Script.Parameters) != 1 {
		t.Errorf("expected 1 data source + 1 parameter, got %d/%d", len(decoded.Script.DataSources), len(decoded.Script.Parameters))
	}
	if len(decoded.Diffs) == 0 {
		t.Error("expected diffs in response")
	}
	for _, diff := range decoded.Diffs {
		if diff.Before != "" || diff.After != "" {
			t.Errorf("diff exposed replacement material: %#v", diff)
		}
	}
	// Credentials remain deferred and disclosure-safe while ordinary data
	// parameterization is still available.
	if decoded.Script.Actions[0].Headers["Authorization"] != "Bearer "+maskedScriptSecret {
		t.Errorf("unselected secret projection = %q", decoded.Script.Actions[0].Headers["Authorization"])
	}
	for _, secret := range []string{"tok123", "hunter2"} {
		if strings.Contains(resp.Body.String(), secret) {
			t.Fatalf("apply response leaked %q: %s", secret, resp.Body.String())
		}
	}
}

func TestHandleApplyImportParameterizationIgnoresForgedClientInstructions(t *testing.T) {
	server := NewServer(ServerConfig{})
	script := paramizeTestScript()
	trusted := buildImportParameterization(script, nil)
	var selected paramize.Suggestion
	for _, suggestion := range trusted.Suggestions {
		if suggestion.Kind == paramize.KindDataParam && suggestion.VarName == "customer_id" {
			selected = suggestion
			break
		}
	}
	if selected.ID == "" {
		t.Fatal("expected customer_id suggestion")
	}

	forged := selected
	forged.LiteralValue = "attacker-controlled-value"
	forged.SampleValue = "attacker-controlled-value"
	forged.Replacement = "{{secret.ATTACKER}}"
	forged.Locations = []paramize.Location{{
		Section: "actions", ActionIndex: 0, Field: "body",
		SearchText: "attacker-controlled-value", ReplaceText: "{{secret.ATTACKER}}",
	}}
	resp, decoded := postParameterizeApply(t, server, importParameterizeApplyRequest{
		Script: script, Suggestions: []paramize.Suggestion{forged}, SelectedIDs: []string{selected.ID},
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
	}
	if decoded.Script == nil || !strings.Contains(decoded.Script.Actions[1].Target, "{{var.customer_id}}") {
		t.Fatalf("server-derived parameterization was not applied: %#v", decoded.Script)
	}
	if strings.Contains(resp.Body.String(), "ATTACKER") || strings.Contains(resp.Body.String(), "attacker-controlled-value") {
		t.Fatalf("forged client instructions reached response: %s", resp.Body.String())
	}
}

func TestHandleApplyImportParameterizationRejectsSecretSuggestionsWithoutDisclosure(t *testing.T) {
	server := NewServer(ServerConfig{})
	script := paramizeTestScript()
	suggestions := buildImportParameterization(script, nil)

	var secretID string
	for _, s := range suggestions.Suggestions {
		if s.Kind == paramize.KindSecret {
			secretID = s.ID
			break
		}
	}
	if secretID == "" {
		t.Fatal("expected a secret suggestion")
	}

	for _, confirm := range []bool{false, true} {
		resp, _ := postParameterizeApply(t, server, importParameterizeApplyRequest{
			Script:          script,
			Suggestions:     suggestions.Suggestions,
			SelectedIDs:     []string{secretID},
			ConfirmHighRisk: confirm,
		})
		if resp.Code != http.StatusUnprocessableEntity {
			t.Fatalf("confirm_high_risk=%t status = %d body=%s", confirm, resp.Code, resp.Body.String())
		}
		for _, secret := range []string{"tok123", "hunter2"} {
			if strings.Contains(resp.Body.String(), secret) {
				t.Fatalf("confirm_high_risk=%t response leaked %q: %s", confirm, secret, resp.Body.String())
			}
		}
	}
}

func TestHandleApplyImportParameterizationValidation(t *testing.T) {
	server := NewServer(ServerConfig{})
	tests := []struct {
		name    string
		payload importParameterizeApplyRequest
		want    int
	}{
		{name: "missing script", payload: importParameterizeApplyRequest{SelectedIDs: []string{"x"}}, want: http.StatusBadRequest},
		{name: "missing selection", payload: importParameterizeApplyRequest{Script: paramizeTestScript()}, want: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, _ := postParameterizeApply(t, server, tt.payload)
			if resp.Code != tt.want {
				t.Errorf("status = %d, want %d (body=%s)", resp.Code, tt.want, resp.Body.String())
			}
		})
	}
}

// TestImportResponsesIncludeParameterization asserts the shared response
// builder always emits the parameterization block.
func TestImportResponsesIncludeParameterization(t *testing.T) {
	response := newScriptImportResponse(scriptImportResponseOptions{})
	block, ok := response["parameterization"].(*paramize.Result)
	if !ok || block == nil {
		t.Fatalf("parameterization block missing or wrong type: %T", response["parameterization"])
	}
	if block.Suggestions == nil {
		t.Error("default parameterization block must have non-nil suggestions")
	}

	withSuggestions := buildImportParameterization(paramizeTestScript(), nil)
	response = newScriptImportResponse(scriptImportResponseOptions{Script: paramizeTestScript(), Parameterization: withSuggestions})
	block, ok = response["parameterization"].(*paramize.Result)
	if !ok || len(block.Suggestions) == 0 {
		t.Fatalf("expected populated parameterization block, got %+v", response["parameterization"])
	}
	for _, suggestion := range block.Suggestions {
		if suggestion.Kind == paramize.KindSecret {
			t.Fatalf("secret parameterization suggestion reached import response: %#v", suggestion)
		}
		if suggestion.LiteralValue != "" || suggestion.SampleValue != "" {
			t.Fatalf("raw value reached parameterization descriptor: %#v", suggestion)
		}
		for _, location := range suggestion.Locations {
			if location.SearchText != "" {
				t.Fatalf("search text reached parameterization descriptor: %#v", suggestion)
			}
		}
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"tok123", "hunter2"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("parameterization response leaked %q: %s", secret, encoded)
		}
	}
	for _, field := range []string{`"literal_value"`, `"sample_value"`, `"search_text"`} {
		if strings.Contains(string(encoded), field) {
			t.Fatalf("parameterization response serialized %s: %s", field, encoded)
		}
	}
}

func TestImportResponseStripsCorrelationSourceSamplesFromEveryCopy(t *testing.T) {
	const sourceToken = "oauth-access-token-must-never-reach-browser"
	correlations := []map[string]any{{
		"name":         "access_token",
		"expression":   "$.access_token",
		"sample_value": sourceToken,
		"value":        sourceToken,
	}}
	response := newScriptImportResponse(scriptImportResponseOptions{
		Script:       paramizeTestScript(),
		Correlations: correlations,
		Result: map[string]any{
			"script":       paramizeTestScript(),
			"correlations": correlations,
		},
	})
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	wire := string(encoded)
	if strings.Contains(wire, sourceToken) {
		t.Fatalf("import response disclosed correlation source token: %s", wire)
	}
	for _, field := range []string{`"sample_value"`, `"value"`} {
		if strings.Contains(wire, field) {
			t.Fatalf("import response serialized protected correlation field %s: %s", field, wire)
		}
	}
	if strings.Count(wire, `"name":"access_token"`) != 2 {
		t.Fatalf("safe correlation descriptors were not retained in both response copies: %s", wire)
	}
}

func TestImportParameterizationDropsMisclassifiedCredentialSuggestion(t *testing.T) {
	script := &dsl.Script{
		Name: "Misclassified credential", Protocol: "http",
		Config: dsl.ScriptConfig{BaseURL: "https://api.example.com"},
		Actions: []dsl.Action{{
			Name: "login", Type: "http_request", Method: http.MethodPost, Target: "/login",
			Body: `{"user_password":"credential-never-on-wire","customer_id":"customer-93847"}`,
		}},
	}
	raw := buildImportParameterization(script, nil)
	foundMisclassified := false
	for _, suggestion := range raw.Suggestions {
		if suggestion.Kind == paramize.KindDataParam && suggestion.VarName == "user_password" {
			foundMisclassified = true
		}
	}
	if !foundMisclassified {
		t.Fatalf("test fixture did not exercise the secret-detector gap: %#v", raw.Suggestions)
	}

	response := newScriptImportResponse(scriptImportResponseOptions{Script: script, Parameterization: raw})
	block := response["parameterization"].(*paramize.Result)
	for _, suggestion := range block.Suggestions {
		if suggestion.VarName == "user_password" {
			t.Fatalf("credential-shaped data suggestion reached the wire: %#v", suggestion)
		}
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "credential-never-on-wire") {
		t.Fatalf("response disclosed a misclassified credential: %s", encoded)
	}
}

// TestRecorderImportIncludesParameterization runs a full importer round-trip
// and asserts the parameterization block reaches the wire format.
func TestRecorderImportIncludesParameterization(t *testing.T) {
	server := NewServer(ServerConfig{})
	payload := map[string]any{
		"name": "Recorder Paramize",
		"content": `{
  "title": "Login",
  "steps": [
    {"type": "navigate", "url": "https://shop.example/login"},
    {"type": "change", "value": "jane@example.com", "selectors": [["#user_email"]]},
    {"type": "change", "value": "hunter2", "selectors": [["#login_password"]]}
  ]
}`,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/recorder", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
	}

	var decoded struct {
		Parameterization paramize.Result `json:"parameterization"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	kinds := map[paramize.Kind]int{}
	for _, s := range decoded.Parameterization.Suggestions {
		kinds[s.Kind]++
		if s.LiteralValue != "" || s.SampleValue != "" {
			t.Errorf("recorder suggestion included raw values: %#v", s)
		}
		for _, location := range s.Locations {
			if location.SearchText != "" {
				t.Errorf("recorder suggestion included search text: %#v", s)
			}
		}
	}
	if kinds[paramize.KindDataParam] != 1 {
		t.Errorf("expected 1 data_param for #user_email fill, got %+v", decoded.Parameterization.Suggestions)
	}
	if kinds[paramize.KindSecret] != 0 {
		t.Errorf("secret suggestions must be represented by guided binding requirements, got %+v", decoded.Parameterization.Suggestions)
	}
	if strings.Contains(resp.Body.String(), "hunter2") {
		t.Fatalf("recorder import leaked secret parameterization material: %s", resp.Body.String())
	}
}
