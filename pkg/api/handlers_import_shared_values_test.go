package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/sharedvalue"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestCompleteImportDraftAttestationBatchContinuesAfterCleanupFailure(t *testing.T) {
	claims := []*importDraftAttestationClaim{{Token: "first"}, {Token: "second"}, {Token: "third"}}
	called := make([]string, 0, len(claims))
	wantErr := errors.New("cleanup unavailable")
	err := completeImportDraftAttestationBatch(claims, func(claim *importDraftAttestationClaim) error {
		called = append(called, claim.Token)
		if claim.Token == "second" {
			return wantErr
		}
		return nil
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("cleanup error = %v, want %v", err, wantErr)
	}
	if got := strings.Join(called, ","); got != "first,second,third" {
		t.Fatalf("completed claims = %q, want every claim attempted", got)
	}
}

type sharedValuesApplyResponse struct {
	Scripts      []*dsl.Script      `json:"scripts"`
	Diffs        []sharedvalue.Diff `json:"diffs"`
	AppliedCount int                `json:"applied_count"`
	Warnings     []string           `json:"warnings"`
}

// sharedValuesTestScripts models the shape a collection importer produces: one
// script per request, each repeating the collection's host and password.
func sharedValuesTestScripts() []*dsl.Script {
	build := func(name, path string) *dsl.Script {
		return &dsl.Script{
			Name:      name,
			Version:   "1.0",
			Protocol:  "http",
			ProjectID: "proj-1",
			Config:    dsl.ScriptConfig{BaseURL: "https://stage.example.com"},
			Parameters: []dsl.Parameter{
				{Name: "tenant", Type: "list", Update: "once", Values: []string{"acme"}},
				{Name: "password", Type: "list", Update: "once", Values: []string{"hunter2"}},
			},
			Actions: []dsl.Action{{
				Name:    name,
				Type:    "http_request",
				Method:  "GET",
				Target:  "https://stage.example.com" + path,
				Headers: map[string]string{"X-Tenant": "acme"},
			}},
		}
	}
	return []*dsl.Script{build("get-users", "/users"), build("get-orders", "/orders")}
}

func postSharedValuesApply(t *testing.T, server *Server, payload importSharedValuesApplyRequest) (*httptest.ResponseRecorder, sharedValuesApplyResponse) {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/shared-values/apply", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	var decoded sharedValuesApplyResponse
	if resp.Code == http.StatusOK {
		if err := json.Unmarshal(resp.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("json.Unmarshal: %v (body=%s)", err, resp.Body.String())
		}
	}
	return resp, decoded
}

func sharedValueCandidateID(t *testing.T, scripts []*dsl.Script, name string) string {
	t.Helper()
	for _, candidate := range buildImportSharedValues(scripts).Candidates {
		if strings.EqualFold(candidate.Name, name) {
			return candidate.ID
		}
	}
	t.Fatalf("no shared-value candidate named %q", name)
	return ""
}

// One value entered once must reach every script in the collection.
func TestHandleApplySharedValuesAcrossScripts(t *testing.T) {
	server := NewServer(ServerConfig{})
	scripts := sharedValuesTestScripts()
	id := sharedValueCandidateID(t, scripts, "tenant")

	resp, decoded := postSharedValuesApply(t, server, importSharedValuesApplyRequest{
		Scripts: scripts,
		Values:  map[string]string{id: "globex"},
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
	}
	if decoded.AppliedCount != 1 {
		t.Errorf("applied_count = %d, want 1", decoded.AppliedCount)
	}
	if len(decoded.Scripts) != 2 {
		t.Fatalf("scripts = %d, want both back", len(decoded.Scripts))
	}
	for _, script := range decoded.Scripts {
		index := -1
		for i, param := range script.Parameters {
			if param.Name == "tenant" {
				index = i
			}
		}
		if index < 0 || script.Parameters[index].Values[0] != "globex" {
			t.Fatalf("script %q did not receive the value: %+v", script.Name, script.Parameters)
		}
	}
	for _, diff := range decoded.Diffs {
		if diff.Before != "" || diff.After != "" {
			t.Errorf("diff exposed replacement material: %#v", diff)
		}
	}
}

// config.base_url is flattened before any VUser exists, so it must receive the
// operator's literal and never a parameter reference.
func TestHandleApplySharedValuesWritesLiteralBaseURL(t *testing.T) {
	server := NewServer(ServerConfig{})
	scripts := sharedValuesTestScripts()
	id := sharedValueCandidateID(t, scripts, "base_url")

	resp, decoded := postSharedValuesApply(t, server, importSharedValuesApplyRequest{
		Scripts: scripts,
		Values:  map[string]string{id: "https://prod.example.com"},
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
	}
	for _, script := range decoded.Scripts {
		if script.Config.BaseURL != "https://prod.example.com" {
			t.Fatalf("script %q base_url = %q", script.Name, script.Config.BaseURL)
		}
		if strings.Contains(script.Config.BaseURL, "{{") {
			t.Fatalf("script %q base_url holds a template: %q", script.Name, script.Config.BaseURL)
		}
	}
}

// A credential-shaped candidate is never offered, and forcing its ID through
// must fail rather than write a value the projection would later mask away.
func TestHandleApplySharedValuesRejectsCredentialShaped(t *testing.T) {
	server := NewServer(ServerConfig{})
	scripts := sharedValuesTestScripts()

	for _, candidate := range buildImportSharedValues(scripts).Candidates {
		if strings.EqualFold(candidate.Name, "password") {
			if !candidate.CredentialShaped {
				t.Fatal("password candidate must be flagged credential-shaped")
			}
			if candidate.CurrentValue != "" {
				t.Fatalf("credential-shaped candidate carried a value: %+v", candidate)
			}
			resp, _ := postSharedValuesApply(t, server, importSharedValuesApplyRequest{
				Scripts: scripts,
				Values:  map[string]string{candidate.ID: "letmein"},
			})
			// Credential-shaped candidates are dropped from the trusted set, so
			// the ID reads as unknown rather than reaching the applier.
			if resp.Code != http.StatusConflict && resp.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want the credential candidate refused; body=%s", resp.Code, resp.Body.String())
			}
			return
		}
	}
	t.Fatal("no password candidate was detected")
}

// A shared value becomes a parameter under the input's name, and the
// projection masks a parameter whose name it takes as sensitive. Every such
// name must be credential-shaped, or its plain value would come back
// [REDACTED] and be lost on the next save.
func TestCredentialShapedCoversEveryMaskedParameterName(t *testing.T) {
	for _, name := range []string{
		"session", "sessionId", "session_id", "jsessionid", "userSessionId", "ASP.NET_SessionId", "sid",
		"csrf", "xsrf", "XSRF-TOKEN", "csrfToken", "requestVerificationToken",
		"pass", "pwd", "password", "dsn", "connectionString", "privateKey", "signingPrivateKey",
		"accessKeyId", "s3AccessKey", "awsSecretAccessKey",
		"apiKey", "clientSecret", "jwt", "otp", "authorization",
		"traceId", "orderId", "username", "correlationId", "state",
	} {
		if isSensitiveScriptKey(name) && !sharedvalue.CredentialShaped(name) {
			t.Errorf("%q is masked by the projection but not credential-shaped", name)
		}
	}
}

func TestHandleApplySharedValuesRejectsUnknownID(t *testing.T) {
	server := NewServer(ServerConfig{})
	resp, _ := postSharedValuesApply(t, server, importSharedValuesApplyRequest{
		Scripts: sharedValuesTestScripts(),
		Values:  map[string]string{"sv_deadbeefdeadbeef": "whatever"},
	})
	if resp.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 for a stale candidate; body=%s", resp.Code, resp.Body.String())
	}
}

func TestHandleApplySharedValuesRequiresInput(t *testing.T) {
	server := NewServer(ServerConfig{})
	resp, _ := postSharedValuesApply(t, server, importSharedValuesApplyRequest{Values: map[string]string{"x": "y"}})
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 without scripts", resp.Code)
	}
	resp, _ = postSharedValuesApply(t, server, importSharedValuesApplyRequest{Scripts: sharedValuesTestScripts()})
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 without values", resp.Code)
	}
}

// Supplying the value a gate was waiting on must also clear the gate.
func TestHandleApplySharedValuesClearsMissingVariableGate(t *testing.T) {
	server := NewServer(ServerConfig{})
	scripts := []*dsl.Script{{
		Name:      "get-users",
		Version:   "1.0",
		Protocol:  "http",
		ProjectID: "proj-1",
		Config:    dsl.ScriptConfig{BaseURL: "https://stage.example.com"},
		Actions: []dsl.Action{{
			Name:   "get-users",
			Type:   "http_request",
			Method: "GET",
			Target: "/{{var.tenant}}/users",
			Metadata: map[string]any{
				"execution_blocker": "import_missing_variables",
				"missing_variables": []string{"tenant"},
			},
		}},
	}}
	id := sharedValueCandidateID(t, scripts, "tenant")

	resp, decoded := postSharedValuesApply(t, server, importSharedValuesApplyRequest{
		Scripts: scripts,
		Values:  map[string]string{id: "globex"},
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
	}
	metadata := decoded.Scripts[0].Actions[0].Metadata
	if _, blocked := metadata["execution_blocker"]; blocked {
		t.Fatalf("execution blocker survived the fix: %+v", metadata)
	}
}

// The response is derived from the same disclosure projection the apply
// endpoint re-derives from, so no imported credential can ride along.
func TestImportSharedValuesResponseHidesCredentialValues(t *testing.T) {
	result := buildImportSharedValues(sharedValuesTestScripts())
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if strings.Contains(string(payload), "hunter2") {
		t.Fatalf("shared values leaked a credential: %s", payload)
	}
}

// Import responses are asserted to carry no literal request text. The shared
// values block must hold to the same line: the browser needs a candidate ID and
// a value to edit, never the locations or the surrounding request text the
// server matches on.
func TestImportSharedValuesResponseCarriesNoRequestText(t *testing.T) {
	payload, err := json.Marshal(buildImportSharedValues(sharedValuesTestScripts()))
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	for _, field := range []string{`"locations"`, `"search_text"`, `"replace_text"`, `"sample_value"`, `"literal_value"`} {
		if strings.Contains(string(payload), field) {
			t.Fatalf("shared values serialized %s: %s", field, payload)
		}
	}
}

// Applying a value changes what the collection shares, so the response must
// carry re-derived candidates. Returning the pre-apply block would leave the
// review panel showing values the scripts no longer hold, and would invite a
// second apply against IDs the server can no longer re-derive.
func TestHandleApplySharedValuesReturnsRefreshedCandidates(t *testing.T) {
	server := NewServer(ServerConfig{})
	scripts := sharedValuesTestScripts()
	id := sharedValueCandidateID(t, scripts, "tenant")

	body, err := json.Marshal(importSharedValuesApplyRequest{
		Scripts: scripts,
		Values:  map[string]string{id: "globex"},
	})
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/shared-values/apply", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
	}

	var decoded struct {
		SharedValues *sharedvalue.Result `json:"shared_values"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if decoded.SharedValues == nil {
		t.Fatal("apply response carried no refreshed shared values")
	}
	for _, candidate := range decoded.SharedValues.Candidates {
		if strings.EqualFold(candidate.Name, "tenant") {
			if candidate.CurrentValue != "globex" {
				t.Fatalf("refreshed tenant value = %q, want the applied value", candidate.CurrentValue)
			}
			return
		}
	}
	t.Fatalf("refreshed candidates lost tenant: %+v", decoded.SharedValues.Candidates)
}

func sharedValuesInputScripts() []*dsl.Script {
	return []*dsl.Script{{
		Name:      "get-users",
		Version:   "1.0",
		Protocol:  "http",
		ProjectID: "proj-1",
		Config:    dsl.ScriptConfig{BaseURL: "https://stage.example.com"},
		Actions: []dsl.Action{{
			Name:   "get-users",
			Type:   "http_request",
			Method: "GET",
			Target: "/{{var.tenant}}/users?trace={{var.request_id}}",
			Metadata: map[string]any{
				"execution_blocker": "import_missing_variables",
				"missing_variables": []string{"tenant", "request_id"},
			},
		}},
	}}
}

// Every importer can supply an input the source left unset per virtual user:
// a data pool column each user reads its own row of, or a value generated
// each iteration. The script records how each input is supplied, and the gate
// that waited on them clears.
func TestHandleApplySharedValuesBindsInputsPerVirtualUser(t *testing.T) {
	server := NewServer(ServerConfig{})
	scripts := sharedValuesInputScripts()
	tenant := sharedValueCandidateID(t, scripts, "tenant")
	requestID := sharedValueCandidateID(t, scripts, "request_id")

	resp, decoded := postSharedValuesApply(t, server, importSharedValuesApplyRequest{
		Scripts: scripts,
		Bindings: map[string]sharedvalue.Binding{
			tenant:    {Mode: sharedvalue.BindingData, Alias: "tenants", Column: "id"},
			requestID: {Mode: sharedvalue.BindingGenerated, Generator: "uuid"},
		},
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
	}
	script := decoded.Scripts[0]
	if got := script.Actions[0].Target; got != "/{{data.tenants.id}}/users?trace={{var.request_id}}" {
		t.Fatalf("target = %q", got)
	}
	found := false
	for _, param := range script.Parameters {
		found = found || param.Name == "request_id" && param.Type == "uuid"
	}
	if !found {
		t.Fatalf("parameters = %+v, want a generated request_id", script.Parameters)
	}
	inputs := script.Metadata.Import.Inputs
	if inputs["tenant"].Mode != "data" || inputs["tenant"].Alias != "tenants" || inputs["request_id"].Mode != "generated" {
		t.Fatalf("inputs = %+v", inputs)
	}
	if _, blocked := script.Actions[0].Metadata["execution_blocker"]; blocked {
		t.Fatalf("the missing-variable gate survived: %+v", script.Actions[0].Metadata)
	}
}

func TestHandleApplySharedValuesRefusesUnusableBindings(t *testing.T) {
	server := NewServer(ServerConfig{})
	scripts := sharedValuesInputScripts()
	tenant := sharedValueCandidateID(t, scripts, "tenant")
	for name, request := range map[string]importSharedValuesApplyRequest{
		"value and binding":  {Scripts: scripts, Values: map[string]string{tenant: "acme"}, Bindings: map[string]sharedvalue.Binding{tenant: {Mode: sharedvalue.BindingGenerated, Generator: "uuid"}}},
		"incomplete binding": {Scripts: scripts, Bindings: map[string]sharedvalue.Binding{tenant: {Mode: sharedvalue.BindingData, Alias: "tenants"}}},
	} {
		if resp, _ := postSharedValuesApply(t, server, request); resp.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d body=%s, want 400", name, resp.Code, resp.Body.String())
		}
	}
	stale := importSharedValuesApplyRequest{Scripts: scripts, Bindings: map[string]sharedvalue.Binding{"sv_missing": {Mode: sharedvalue.BindingGenerated, Generator: "uuid"}}}
	if resp, _ := postSharedValuesApply(t, server, stale); resp.Code != http.StatusConflict {
		t.Errorf("stale binding: status = %d, want 409", resp.Code)
	}
	// A repeated literal (the base URL) is not an unset input.
	baseURL := sharedValueCandidateID(t, sharedValuesTestScripts(), "base_url")
	literal := importSharedValuesApplyRequest{Scripts: sharedValuesTestScripts(), Bindings: map[string]sharedvalue.Binding{baseURL: {Mode: sharedvalue.BindingGenerated, Generator: "uuid"}}}
	if resp, _ := postSharedValuesApply(t, server, literal); resp.Code != http.StatusBadRequest {
		t.Errorf("literal binding: status = %d body=%s, want 400", resp.Code, resp.Body.String())
	}
}

// A credential the source left unset is supplied from a project secret (or a
// data pool column), never as a value kept in the script, and the choice is
// recorded for a re-import.
func TestHandleApplySharedValuesSuppliesACredentialFromAProjectSecret(t *testing.T) {
	server := NewServer(ServerConfig{})
	scripts := []*dsl.Script{{
		Name: "get-orders", Version: "1.0", Protocol: "http", ProjectID: "proj-1",
		Config: dsl.ScriptConfig{BaseURL: "https://stage.example.com"},
		Actions: []dsl.Action{{
			Name: "get-orders", Type: "http_request", Method: "GET", Target: "/orders",
			HeaderList: dsl.HeaderList{{Name: "Authorization", Value: "Bearer {{var.apiToken}}"}},
			Metadata: map[string]any{
				"execution_blocker": "import_missing_variables",
				"missing_variables": []string{"apiToken"},
			},
		}},
	}}
	token := sharedValueCandidateID(t, scripts, "apiToken")
	if resp, _ := postSharedValuesApply(t, server, importSharedValuesApplyRequest{
		Scripts: scripts, Values: map[string]string{token: "abc"},
	}); resp.Code == http.StatusOK {
		t.Fatal("a credential took a value kept in the script")
	}
	if resp, _ := postSharedValuesApply(t, server, importSharedValuesApplyRequest{
		Scripts: scripts, Bindings: map[string]sharedvalue.Binding{token: {Mode: sharedvalue.BindingGenerated, Generator: "uuid"}},
	}); resp.Code != http.StatusBadRequest {
		t.Fatalf("a generated credential: status = %d, want 400", resp.Code)
	}
	resp, decoded := postSharedValuesApply(t, server, importSharedValuesApplyRequest{
		Scripts: scripts, Bindings: map[string]sharedvalue.Binding{token: {Mode: sharedvalue.BindingSecret, Secret: "API_TOKEN"}},
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
	}
	script := decoded.Scripts[0]
	if got := script.Actions[0].HeaderList[0].Value; got != "Bearer {{secret.API_TOKEN}}" {
		t.Fatalf("header = %q, want the project secret", got)
	}
	if input := script.Metadata.Import.Inputs["apiToken"]; input.Mode != "secret" || input.Reference != "{{secret.API_TOKEN}}" {
		t.Fatalf("recorded input = %+v", input)
	}
	if _, blocked := script.Actions[0].Metadata["execution_blocker"]; blocked {
		t.Fatalf("the missing-variable gate survived: %+v", script.Actions[0].Metadata)
	}
}
