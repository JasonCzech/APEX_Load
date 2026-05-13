package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/stormrunner/pkg/dsl"
)

func TestScriptValidationParityAcrossCurrentEndpoints(t *testing.T) {
	server := NewServer(ServerConfig{})

	invalidScript := func() dsl.Script {
		return dsl.Script{
			Name:     "Invalid Browser Draft",
			Version:  "1.0.0",
			Protocol: "browser",
			Config: dsl.ScriptConfig{
				Browser: dsl.BrowserConfig{Engine: "chromium"},
			},
			Actions: []dsl.Action{{Name: "Capture current page"}},
		}
	}
	validBrowserScript := &dsl.Script{
		Name:     "Valid Browser Script",
		Version:  "1.0.0",
		Protocol: "browser",
		Config: dsl.ScriptConfig{
			Browser: dsl.BrowserConfig{Engine: "chromium"},
		},
		Actions: []dsl.Action{{Name: "Capture current page", Type: "browser_screenshot"}},
	}
	storedValid := server.runner.StoreScript(validBrowserScript)
	storedInvalid := server.runner.StoreScript(func() *dsl.Script {
		script := invalidScript()
		return &script
	}())

	expectedIssues := validateScript(func() *dsl.Script {
		script := invalidScript()
		return &script
	}())
	if len(expectedIssues) == 0 {
		t.Fatal("expected invalid script to produce validation issues")
	}

	invalidScriptBody := func(t *testing.T) []byte {
		t.Helper()
		body, err := json.Marshal(map[string]any{"script": invalidScript()})
		if err != nil {
			t.Fatalf("json.Marshal returned error: %v", err)
		}
		return body
	}

	testCases := []struct {
		name       string
		method     string
		path       string
		body       func(*testing.T) []byte
		wantStatus int
	}{
		{name: "create", method: http.MethodPost, path: "/api/v1/scripts", body: invalidScriptBody, wantStatus: http.StatusBadRequest},
		{name: "update", method: http.MethodPut, path: "/api/v1/scripts/" + storedValid.ID, body: invalidScriptBody, wantStatus: http.StatusBadRequest},
		{name: "validate stored", method: http.MethodPost, path: "/api/v1/scripts/" + storedInvalid.ID + "/validate", wantStatus: http.StatusOK},
		{name: "replay draft", method: http.MethodPost, path: "/api/v1/scripts/replay", body: invalidScriptBody, wantStatus: http.StatusBadRequest},
		{name: "replay stored", method: http.MethodPost, path: "/api/v1/scripts/" + storedInvalid.ID + "/replay", wantStatus: http.StatusBadRequest},
		{name: "browser replay draft", method: http.MethodPost, path: "/api/v1/scripts/browser-replay/sessions", body: invalidScriptBody, wantStatus: http.StatusBadRequest},
		{name: "browser replay stored", method: http.MethodPost, path: "/api/v1/scripts/" + storedInvalid.ID + "/browser-replay/sessions", wantStatus: http.StatusBadRequest},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var bodyReader *bytes.Reader
			if tc.body != nil {
				bodyReader = bytes.NewReader(tc.body(t))
			} else {
				bodyReader = bytes.NewReader(nil)
			}

			req := httptest.NewRequest(tc.method, tc.path, bodyReader)
			if tc.body != nil {
				req.Header.Set("Content-Type", "application/json")
			}
			resp := httptest.NewRecorder()
			server.mux.ServeHTTP(resp, req)

			if resp.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d body=%s", resp.Code, tc.wantStatus, resp.Body.String())
			}

			issues := extractValidationIssues(t, resp.Body.Bytes())
			if !reflect.DeepEqual(issues, expectedIssues) {
				t.Fatalf("issues = %#v, want %#v body=%s", issues, expectedIssues, resp.Body.String())
			}
		})
	}
}

func extractValidationIssues(t *testing.T, body []byte) []string {
	t.Helper()

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}

	if rawIssues, ok := payload["issues"]; ok {
		items, ok := rawIssues.([]any)
		if !ok {
			t.Fatalf("issues payload = %#v, want []any", rawIssues)
		}
		issues := make([]string, 0, len(items))
		for _, item := range items {
			text, ok := item.(string)
			if !ok {
				t.Fatalf("issue item = %#v, want string", item)
			}
			issues = append(issues, text)
		}
		return issues
	}

	rawError, ok := payload["error"]
	if !ok {
		t.Fatalf("validation payload missing issues and error: %#v", payload)
	}
	text, ok := rawError.(string)
	if !ok {
		t.Fatalf("error payload = %#v, want string", rawError)
	}
	return strings.Split(text, "; ")
}