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
		TotalSteps       int    `json:"total_steps"`
		SkippedSteps     int    `json:"skipped_steps"`
		DegradedSteps    int    `json:"degraded_steps"`
	} `json:"stats"`
	Summary         map[string]any `json:"summary"`
	ReplaySessionID string         `json:"replay_session_id"`
}

func TestHandleImportRecorderChromeJSON(t *testing.T) {
	server := NewServer(ServerConfig{})
	closeBatchTestServer(t, server)
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
	stepMapping, ok := result.Summary["step_mapping"].([]any)
	if !ok || len(stepMapping) != 3 {
		t.Fatalf("step mapping = %#v, want 3 mapped steps", result.Summary["step_mapping"])
	}
	firstMapping, ok := stepMapping[0].(map[string]any)
	if !ok || firstMapping["disposition"] != "mapped" {
		t.Fatalf("first mapping = %#v", stepMapping[0])
	}
}

func TestHandleImportRecorderStepMappingDispositions(t *testing.T) {
	server := NewServer(ServerConfig{})
	closeBatchTestServer(t, server)
	fixture := `{
  "title": "Review",
  "steps": [
    {"type": "navigate", "url": "https://shop.example/"},
    {"type": "scroll", "x": 0, "y": 300, "distancePercentage": 25},
    {"type": "customStep", "name": "Manual"}
  ]
}`
	payload := map[string]any{"name": "Review", "content": fixture}
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
	stepMapping, ok := result.Summary["step_mapping"].([]any)
	if !ok || len(stepMapping) != 3 {
		t.Fatalf("step mapping = %#v, want 3 source-step mappings", result.Summary["step_mapping"])
	}
	counts := map[string]int{}
	reasons := map[string]string{}
	for _, raw := range stepMapping {
		mapping, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("mapping = %#v", raw)
		}
		disposition, _ := mapping["disposition"].(string)
		counts[disposition]++
		if reason, _ := mapping["reason"].(string); reason != "" {
			reasons[disposition] = reason
		}
	}
	if counts["mapped"] != 1 || counts["degraded"] != 1 || counts["unsupported"] != 1 {
		t.Fatalf("disposition counts = %#v", counts)
	}
	if reasons["degraded"] == "" || reasons["unsupported"] == "" {
		t.Fatalf("mapping reasons = %#v", stepMapping)
	}
	if result.Stats.TotalSteps != 3 || result.Stats.SkippedSteps != 1 || result.Stats.DegradedSteps != 1 {
		t.Fatalf("stats = %+v, want total=3 skipped=1 degraded=1", result.Stats)
	}
	if confidence, _ := result.Summary["conversion_confidence"].(float64); confidence != 0.5 {
		t.Fatalf("conversion confidence = %#v, want 0.5 for mapped+degraded+unsupported", result.Summary["conversion_confidence"])
	}
}

func TestHandleImportRecorderPlaywrightMultipart(t *testing.T) {
	server := NewServer(ServerConfig{})
	closeBatchTestServer(t, server)
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
	closeBatchTestServer(t, server)
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

func TestHandleImportRecorderValidateAndReplayRoutes(t *testing.T) {
	server := NewServer(ServerConfig{})
	closeBatchTestServer(t, server)
	payload := map[string]any{
		"name":    "Recorder Import",
		"content": chromeRecorderFixture,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	validateReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/recorder/validate", bytes.NewReader(body))
	validateReq.Header.Set("Content-Type", "application/json")
	validateResp := httptest.NewRecorder()
	server.mux.ServeHTTP(validateResp, validateReq)
	if validateResp.Code != http.StatusOK {
		t.Fatalf("validate status = %d body=%s", validateResp.Code, validateResp.Body.String())
	}
	var validateResult recorderImportResponse
	if err := json.Unmarshal(validateResp.Body.Bytes(), &validateResult); err != nil {
		t.Fatalf("validate json.Unmarshal: %v", err)
	}
	if validateResult.Summary["replay_required"] != true || validateResult.Summary["replay_supported"] != true {
		t.Fatalf("validate summary = %+v", validateResult.Summary)
	}
	if validateResult.Summary["source_format"] != "chrome_devtools_recorder" {
		t.Fatalf("source format = %+v", validateResult.Summary)
	}

	replayReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/recorder/replay", bytes.NewReader(body))
	replayReq.Header.Set("Content-Type", "application/json")
	replayResp := httptest.NewRecorder()
	server.mux.ServeHTTP(replayResp, replayReq)
	if replayResp.Code != http.StatusCreated {
		t.Fatalf("replay status = %d body=%s", replayResp.Code, replayResp.Body.String())
	}
	var replayResult recorderImportResponse
	if err := json.Unmarshal(replayResp.Body.Bytes(), &replayResult); err != nil {
		t.Fatalf("replay json.Unmarshal: %v", err)
	}
	if replayResult.ReplaySessionID == "" {
		t.Fatalf("replay session id missing: %+v", replayResult)
	}
}

func TestHandleImportRecorderErrors(t *testing.T) {
	server := NewServer(ServerConfig{})
	closeBatchTestServer(t, server)
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
