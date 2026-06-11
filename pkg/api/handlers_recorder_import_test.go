package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const chromeRecorderFixture = `{
  "title": "Login",
  "steps": [
    {"type": "navigate", "url": "https://shop.example/login"},
    {"type": "change", "value": "jason", "selectors": [["#username"]]},
    {"type": "click", "selectors": [["aria/Sign in[role=\"button\"]"], ["[data-testid='login-btn']"]],
     "assertedEvents": [{"type": "navigation", "url": "https://shop.example/account"}]}
  ]
}`

type recorderImportResponse struct {
	Script struct {
		Name     string `json:"name"`
		Protocol string `json:"protocol"`
		Actions  []struct {
			Type     string `json:"type"`
			Selector string `json:"selector"`
			Value    string `json:"value"`
			Sync     *struct {
				Type string `json:"type"`
			} `json:"sync"`
			Metadata map[string]any `json:"metadata"`
		} `json:"actions"`
	} `json:"script"`
	Stats struct {
		Format           string `json:"format"`
		ConvertedActions int    `json:"converted_actions"`
	} `json:"stats"`
}

func TestHandleImportRecorderChromeJSON(t *testing.T) {
	server := NewServer(ServerConfig{})
	payload := map[string]any{
		"name":    "Recorder Import",
		"content": chromeRecorderFixture,
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
	var result recorderImportResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if result.Script.Name != "Recorder Import" {
		t.Errorf("name = %q", result.Script.Name)
	}
	if result.Script.Protocol != "browser" {
		t.Errorf("protocol = %q, want browser", result.Script.Protocol)
	}
	if result.Stats.Format != "chrome_devtools_recorder" || result.Stats.ConvertedActions != 3 {
		t.Errorf("stats = %+v", result.Stats)
	}
	click := result.Script.Actions[2]
	if click.Type != "browser_click" || click.Selector != `[data-testid='login-btn']` {
		t.Errorf("click = %+v", click)
	}
	if click.Sync == nil || click.Sync.Type != "load" {
		t.Errorf("click sync = %+v", click.Sync)
	}
	if click.Metadata["source_selector_candidates"] == nil {
		t.Error("click missing selector candidates metadata")
	}
}

func TestHandleImportRecorderPlaywrightMultipart(t *testing.T) {
	server := NewServer(ServerConfig{})
	script := "await page.goto('https://shop.example/');\nawait page.getByTestId('go').click();\n"

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "recording.spec.js")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write([]byte(script)); err != nil {
		t.Fatalf("write part: %v", err)
	}
	_ = writer.WriteField("name", "PW Multipart")
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/recorder", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
	}
	var result recorderImportResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if result.Stats.Format != "playwright_codegen" || result.Stats.ConvertedActions != 2 {
		t.Errorf("stats = %+v", result.Stats)
	}
	if result.Script.Name != "PW Multipart" {
		t.Errorf("name = %q", result.Script.Name)
	}
}

func TestHandleImportRecorderRawBody(t *testing.T) {
	server := NewServer(ServerConfig{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/recorder?filename=flow.json", strings.NewReader(chromeRecorderFixture))
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
	}
	var result recorderImportResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if result.Script.Name != "flow" {
		t.Errorf("name from filename = %q", result.Script.Name)
	}
}

func TestHandleImportRecorderErrors(t *testing.T) {
	server := NewServer(ServerConfig{})
	tests := []struct {
		name        string
		contentType string
		body        string
		wantStatus  int
	}{
		{name: "empty json content", contentType: "application/json", body: `{"content":""}`, wantStatus: http.StatusBadRequest},
		{name: "invalid json body", contentType: "application/json", body: `{broken`, wantStatus: http.StatusBadRequest},
		{name: "unconvertible payload", contentType: "text/plain", body: "DROP TABLE recordings;", wantStatus: http.StatusUnprocessableEntity},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/recorder", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", tt.contentType)
			resp := httptest.NewRecorder()
			server.mux.ServeHTTP(resp, req)
			if resp.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d (body=%s)", resp.Code, tt.wantStatus, resp.Body.String())
			}
		})
	}
}
