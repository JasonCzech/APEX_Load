package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleRAMLSupportMatrix(t *testing.T) {
	server := NewServer(ServerConfig{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/import/raml/support-matrix", nil)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var result struct {
		SupportMatrix []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"support_matrix"`
		Notes []string `json:"notes"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	found := false
	for _, entry := range result.SupportMatrix {
		if entry.ID == "raml-resources" {
			found = true
		}
	}
	if !found {
		t.Fatalf("support matrix missing raml-resources: %#v", result.SupportMatrix)
	}
	if len(result.Notes) == 0 {
		t.Fatal("expected support matrix notes")
	}
}

func TestHandleImportRAMLDetectsMuleSoftAndPolicy(t *testing.T) {
	server := NewServer(ServerConfig{})
	ramlContent := `#%RAML 1.0
title: Accounts API
version: v1
baseUri: https://accounts.us-e2.cloudhub.io/api
securitySchemes:
  client_id_required:
    type: Pass Through
    describedBy:
      headers:
        client_id:
          type: string
        client_secret:
          type: string
/accounts:
  get:
    displayName: List Accounts
`
	payload := map[string]any{
		"name":       "Accounts API",
		"project_id": "proj-mule",
		"content":    ramlContent,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/raml", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var result struct {
		Script struct {
			ProjectID string `json:"project_id"`
			Actions   []struct {
				Headers map[string]string `json:"headers"`
			} `json:"actions"`
			Metadata struct {
				Import struct {
					Preserved map[string]any `json:"preserved"`
				} `json:"import"`
			} `json:"metadata"`
		} `json:"script"`
		ReviewSections []struct {
			Key      string   `json:"key"`
			Severity string   `json:"severity"`
			Items    []string `json:"items"`
		} `json:"review_sections"`
		Requirements []SecretBindingRequirement `json:"secret_binding_requirements"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	sections := map[string]bool{}
	for _, section := range result.ReviewSections {
		sections[section.Key] = true
	}
	if !sections["raml_import"] || !sections["mulesoft_review"] {
		t.Fatalf("review sections = %#v, want raml_import + mulesoft_review", result.ReviewSections)
	}
	if result.Script.Metadata.Import.Preserved["mulesoft_review"] == nil {
		t.Fatalf("script metadata missing preserved mulesoft_review: %#v", result.Script.Metadata.Import.Preserved)
	}
	if len(result.Script.Actions) == 0 || result.Script.Actions[0].Headers["client_id"] != "{{env.RAML_CLIENT_ID}}" {
		t.Fatalf("actions = %#v, want client_id env placeholder header", result.Script.Actions)
	}
}

func TestHandleImportRAMLDetectsAgentforce(t *testing.T) {
	server := NewServer(ServerConfig{})
	ramlContent := `#%RAML 1.0
title: Agent API
version: v1
baseUri: https://api.salesforce.com/einstein/ai-agent/v1/agents/0XxAGENT
/sessions:
  post:
    displayName: Start Session
/sessions/{sessionId}/messages/stream:
  post:
    displayName: Stream Message
`
	payload := map[string]any{
		"name":       "Agent API",
		"project_id": "proj-agent",
		"content":    ramlContent,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/raml", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var result struct {
		Script struct {
			Metadata struct {
				Import struct {
					Preserved map[string]any `json:"preserved"`
				} `json:"import"`
			} `json:"metadata"`
		} `json:"script"`
		Requirements   []SecretBindingRequirement `json:"secret_binding_requirements"`
		ReviewSections []struct {
			Key string `json:"key"`
		} `json:"review_sections"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if result.Script.Metadata.Import.Preserved["agentforce_review"] == nil {
		t.Fatalf("preserved metadata = %#v, want agentforce_review", result.Script.Metadata.Import.Preserved)
	}
	for _, section := range result.ReviewSections {
		if section.Key == "agentforce_review" {
			return
		}
	}
	t.Fatalf("review sections = %#v, want agentforce_review", result.ReviewSections)
}

func TestHandleImportRAMLAppliesAgentforceTransformWhenRequested(t *testing.T) {
	ramlContent := `#%RAML 1.0
title: Agent API
version: v1
baseUri: https://api.salesforce.com/einstein/ai-agent/v1/agents/0XxAGENT
/sessions:
  post:
    displayName: Start Session
/sessions/{sessionId}/messages/stream:
  post:
    displayName: Stream Message
`
	type importedScript struct {
		Script struct {
			Init []struct {
				Type   string         `json:"type"`
				Params map[string]any `json:"params"`
			} `json:"init"`
			Actions []struct {
				Target  string            `json:"target"`
				Headers map[string]string `json:"headers"`
			} `json:"actions"`
			Teardown []struct {
				Method string `json:"method"`
				Target string `json:"target"`
			} `json:"teardown"`
			Metadata struct {
				Import struct {
					Preserved map[string]any `json:"preserved"`
				} `json:"import"`
			} `json:"metadata"`
		} `json:"script"`
		ReviewSections []struct {
			Key   string   `json:"key"`
			Items []string `json:"items"`
		} `json:"review_sections"`
		Requirements []SecretBindingRequirement `json:"secret_binding_requirements"`
	}

	doImport := func(t *testing.T, url string) importedScript {
		t.Helper()
		server := NewServer(ServerConfig{})
		body, err := json.Marshal(map[string]any{"name": "Agent API", "project_id": "proj-agent", "content": ramlContent})
		if err != nil {
			t.Fatalf("json.Marshal returned error: %v", err)
		}
		req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
		}
		var result importedScript
		if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
			t.Fatalf("json.Unmarshal returned error: %v", err)
		}
		return result
	}

	hasTokenInit := func(s importedScript) bool {
		for _, action := range s.Script.Init {
			if action.Type == "http_oauth2_token" {
				return true
			}
		}
		return false
	}

	// Default import is non-destructive: review attached, but no transform.
	base := doImport(t, "/api/v1/scripts/import/raml")
	if hasTokenInit(base) {
		t.Fatalf("default import added a token action without opt-in: %#v", base.Script.Init)
	}
	if base.Script.Metadata.Import.Preserved["agentforce_transform"] != nil {
		t.Fatal("default import recorded a transform without opt-in")
	}

	// Opt-in transform rewrites the script to be replay-ready.
	transformed := doImport(t, "/api/v1/scripts/import/raml?agentforce_transform=true")
	if !hasTokenInit(transformed) {
		t.Fatalf("opt-in transform did not prepend a token action: %#v", transformed.Script.Init)
	}
	if transformed.Script.Metadata.Import.Preserved["agentforce_transform"] == nil {
		t.Fatalf("preserved metadata missing agentforce_transform: %#v", transformed.Script.Metadata.Import.Preserved)
	}
	var clientSecret *SecretBindingRequirement
	for index := range transformed.Requirements {
		if transformed.Requirements[index].SuggestedName == "SALESFORCE_CLIENT_SECRET" {
			clientSecret = &transformed.Requirements[index]
			break
		}
	}
	if clientSecret == nil || clientSecret.Label != "Salesforce client secret" ||
		!strings.Contains(clientSecret.Context, "Agentforce") ||
		!strings.HasPrefix(clientSecret.Path, "/init/") ||
		clientSecret.SourceValueAvailable || clientSecret.DestinationValuePreserved {
		t.Fatalf("Agentforce client-secret guidance = %#v", clientSecret)
	}
	streamRewritten := false
	for _, action := range transformed.Script.Actions {
		if strings.Contains(action.Target, "/messages/stream") {
			if !strings.Contains(action.Target, "{{var.agent_session_id}}") {
				t.Fatalf("stream action target = %q, want session variable", action.Target)
			}
			if action.Headers["Accept"] != "text/event-stream" {
				t.Fatalf("stream action Accept = %q, want text/event-stream", action.Headers["Accept"])
			}
			streamRewritten = true
		}
	}
	if !streamRewritten {
		t.Fatalf("no streaming message action found in transformed script: %#v", transformed.Script.Actions)
	}
	if len(transformed.Script.Teardown) == 0 || transformed.Script.Teardown[0].Method != "DELETE" {
		t.Fatalf("teardown = %#v, want a DELETE end-session action", transformed.Script.Teardown)
	}
	appliedItem := false
	for _, section := range transformed.ReviewSections {
		if section.Key != "agentforce_review" {
			continue
		}
		for _, item := range section.Items {
			if strings.HasPrefix(item, "Applied (") {
				appliedItem = true
			}
		}
	}
	if !appliedItem {
		t.Fatalf("agentforce review section missing applied-change items: %#v", transformed.ReviewSections)
	}
}

func TestHandleImportRAMLZipResolvesIncludesAndNamesFromMainEntry(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	entries := map[string]string{
		"api.raml": `#%RAML 1.0
title: Bundle API
baseUri: https://api.example.test
/widgets:
  post:
    body:
      application/json:
        example: !include examples/widget.json
`,
		"examples/widget.json": `{"name":"Widget"}`,
	}
	for name, content := range entries {
		f, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := f.Write([]byte(content)); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}

	server := NewServer(ServerConfig{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/raml?filename=bundle.zip", bytes.NewReader(buf.Bytes()))
	req.Header.Set("Content-Type", "application/octet-stream")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var result struct {
		Script struct {
			Name    string `json:"name"`
			Actions []struct {
				Target string `json:"target"`
				Body   string `json:"body"`
			} `json:"actions"`
		} `json:"script"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if result.Script.Name == "bundle" {
		t.Fatalf("script name = %q, want the main RAML entry name, not the zip basename", result.Script.Name)
	}
	if len(result.Script.Actions) == 0 || !strings.Contains(result.Script.Actions[0].Body, `"name":"Widget"`) {
		t.Fatalf("actions = %#v, want included widget.json body resolved", result.Script.Actions)
	}
}
