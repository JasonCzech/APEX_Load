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
