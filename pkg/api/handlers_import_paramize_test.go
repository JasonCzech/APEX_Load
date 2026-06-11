package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/paramize"
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
		Script:      script,
		Suggestions: suggestions.Suggestions,
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
	// The secret must remain untouched because it was not selected.
	if decoded.Script.Actions[0].Headers["Authorization"] != "Bearer tok123" {
		t.Errorf("unselected secret was modified: %q", decoded.Script.Actions[0].Headers["Authorization"])
	}
}

func TestHandleApplyImportParameterizationBlocksHighRisk(t *testing.T) {
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

	// Without confirm_high_risk the request is blocked and nothing changes.
	resp, decoded := postParameterizeApply(t, server, importParameterizeApplyRequest{
		Script:      script,
		Suggestions: suggestions.Suggestions,
		SelectedIDs: []string{secretID},
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
	}
	if !decoded.Blocked {
		t.Fatal("expected blocked=true for unconfirmed high-risk selection")
	}
	if decoded.AppliedCount != 0 {
		t.Errorf("applied_count = %d, want 0 when blocked", decoded.AppliedCount)
	}

	// With confirm_high_risk the secret externalizes.
	resp, decoded = postParameterizeApply(t, server, importParameterizeApplyRequest{
		Script:          script,
		Suggestions:     suggestions.Suggestions,
		SelectedIDs:     []string{secretID},
		ConfirmHighRisk: true,
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
	response = newScriptImportResponse(scriptImportResponseOptions{Parameterization: withSuggestions})
	block, ok = response["parameterization"].(*paramize.Result)
	if !ok || len(block.Suggestions) == 0 {
		t.Fatalf("expected populated parameterization block, got %+v", response["parameterization"])
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
	}
	if kinds[paramize.KindDataParam] != 1 {
		t.Errorf("expected 1 data_param for #user_email fill, got %+v", decoded.Parameterization.Suggestions)
	}
	if kinds[paramize.KindSecret] != 1 {
		t.Errorf("expected 1 secret for #login_password fill, got %+v", decoded.Parameterization.Suggestions)
	}
}
