package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/importartifact"
	"github.com/jasonczech/apexload/pkg/importsession"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestNormalizeImportCommitModes(t *testing.T) {
	t.Parallel()

	spec := importCommitSpec{
		sourceProfile:       "postman",
		auditProfile:        "postman",
		displayName:         "Postman",
		allowedReplaceModes: []string{"full", "merge_by_item"},
		actionKey:           func(dsl.Action) string { return "" },
		diff: func(*dsl.Script, *dsl.Script, string, func(dsl.Action) string) openAPIImportDiff {
			return openAPIImportDiff{}
		},
		merge: func(_ *dsl.Script, imported *dsl.Script, _ func(dsl.Action) string) *dsl.Script {
			return imported
		},
		mapCompatibleResponse: identityImportCommitResponse,
	}
	tests := []struct {
		name        string
		request     openAPIImportCommitRequest
		wantMode    string
		wantReplace string
		wantError   string
	}{
		{name: "defaults", request: openAPIImportCommitRequest{}, wantMode: "create", wantReplace: "full"},
		{
			name:        "target implies replace",
			request:     openAPIImportCommitRequest{TargetScriptID: "script-1"},
			wantMode:    "replace",
			wantReplace: "full",
		},
		{
			name:        "source merge mode",
			request:     openAPIImportCommitRequest{Mode: " Replace ", ReplaceMode: " MERGE_BY_ITEM "},
			wantMode:    "replace",
			wantReplace: "merge_by_item",
		},
		{name: "invalid mode", request: openAPIImportCommitRequest{Mode: "append"}, wantError: "mode must be create or replace"},
		{
			name:      "invalid replace mode",
			request:   openAPIImportCommitRequest{ReplaceMode: "merge_by_operation_id"},
			wantError: "replace_mode must be full or merge_by_item",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			mode, replaceMode, err := normalizeImportCommitModes(test.request, spec)
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("error = %v, want substring %q", err, test.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalize modes: %v", err)
			}
			if mode != test.wantMode || replaceMode != test.wantReplace {
				t.Fatalf("modes = (%q, %q), want (%q, %q)", mode, replaceMode, test.wantMode, test.wantReplace)
			}
		})
	}
}

func TestImportCommitSpecValidation(t *testing.T) {
	t.Parallel()

	valid := importCommitSpec{
		sourceProfile:       "openapi",
		auditProfile:        "openapi",
		displayName:         "OpenAPI",
		allowedReplaceModes: []string{"full", "merge_by_operation_id"},
		actionKey:           func(dsl.Action) string { return "" },
		diff: func(*dsl.Script, *dsl.Script, string, func(dsl.Action) string) openAPIImportDiff {
			return openAPIImportDiff{}
		},
		merge: func(_ *dsl.Script, imported *dsl.Script, _ func(dsl.Action) string) *dsl.Script {
			return imported
		},
		mapCompatibleResponse: identityImportCommitResponse,
	}
	if err := valid.validate(); err != nil {
		t.Fatalf("valid spec: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*importCommitSpec)
	}{
		{name: "missing audit profile", mutate: func(spec *importCommitSpec) { spec.auditProfile = "" }},
		{name: "missing replace modes", mutate: func(spec *importCommitSpec) { spec.allowedReplaceModes = nil }},
		{name: "missing full mode", mutate: func(spec *importCommitSpec) {
			spec.allowedReplaceModes = []string{"merge_by_operation_id"}
		}},
		{name: "duplicate mode", mutate: func(spec *importCommitSpec) {
			spec.allowedReplaceModes = []string{"full", "FULL"}
		}},
		{name: "missing action key", mutate: func(spec *importCommitSpec) { spec.actionKey = nil }},
		{name: "missing diff", mutate: func(spec *importCommitSpec) { spec.diff = nil }},
		{name: "missing merge", mutate: func(spec *importCommitSpec) { spec.merge = nil }},
		{name: "missing response mapper", mutate: func(spec *importCommitSpec) {
			spec.mapCompatibleResponse = nil
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			invalid := valid
			test.mutate(&invalid)
			if err := invalid.validate(); err == nil {
				t.Fatal("incomplete spec unexpectedly validated")
			}
		})
	}
}

func TestImportCommitSpecAppliesResponseCompatibilityMapper(t *testing.T) {
	t.Parallel()

	spec := importCommitSpec{
		mapCompatibleResponse: func(payload map[string]any) map[string]any {
			payload["legacy_envelope"] = true
			return payload
		},
	}
	response := httptest.NewRecorder()
	spec.writeResponse(response, http.StatusAccepted, map[string]any{"dry_run": true})
	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusAccepted)
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload["legacy_envelope"] != true || payload["dry_run"] != true {
		t.Fatalf("compatibility-mapped response = %#v", payload)
	}
}

func TestLegacyImportCommitFinalizesUnsafeHeadersIntoBlockedDrafts(t *testing.T) {
	tests := []struct {
		name   string
		script func(string) *dsl.Script
		assert func(*testing.T, *dsl.Script)
	}{
		{
			name: "action header",
			script: func(id string) *dsl.Script {
				script := legacyUnsafeHeaderCommitScript(id)
				script.Actions[0].Headers = map[string]string{
					"Content-Length": "7",
					"X-Safe":         "preserved",
				}
				return script
			},
			assert: func(t *testing.T, script *dsl.Script) {
				t.Helper()
				action := script.Actions[0]
				if action.Headers["X-Safe"] != "preserved" {
					t.Fatalf("safe action header was not preserved: %#v", action.Headers)
				}
				if _, exists := action.Headers["Content-Length"]; exists {
					t.Fatalf("transport-owned action header survived finalization: %#v", action.Headers)
				}
				assertImportReviewBlocked(t, action.Metadata)
			},
		},
		{
			name: "default header",
			script: func(id string) *dsl.Script {
				script := legacyUnsafeHeaderCommitScript(id)
				script.Config.HTTP.DefaultHeaders = map[string]string{
					"Transfer-Encoding": "chunked",
					"X-Safe":            "preserved",
				}
				return script
			},
			assert: func(t *testing.T, script *dsl.Script) {
				t.Helper()
				if !reflect.DeepEqual(
					script.Config.HTTP.DefaultHeaders,
					map[string]string{"X-Safe": "preserved"},
				) {
					t.Fatalf("finalized default headers = %#v", script.Config.HTTP.DefaultHeaders)
				}
				assertImportReviewBlocked(t, script.Actions[0].Metadata)
			},
		},
		{
			name: "sequence child header",
			script: func(id string) *dsl.Script {
				script := legacyUnsafeHeaderCommitScript(id)
				script.Actions = []dsl.Action{{
					Name:   "Imported sequence",
					Type:   "http_sequence",
					Method: "SEQUENCE",
					Target: "/",
					Params: map[string]any{"requests": []any{map[string]any{
						"name":   "Child request",
						"type":   "request",
						"method": http.MethodGet,
						"target": "/child",
						"headers": map[string]any{
							"Content-Length": "7",
							"X-Safe":         "preserved",
						},
					}}},
				}}
				return script
			},
			assert: func(t *testing.T, script *dsl.Script) {
				t.Helper()
				requests, ok := script.Actions[0].Params["requests"].([]any)
				if !ok || len(requests) != 1 {
					t.Fatalf("sequence requests = %#v", script.Actions[0].Params["requests"])
				}
				child, ok := requests[0].(map[string]any)
				if !ok {
					t.Fatalf("sequence child = %#v", requests[0])
				}
				headers, ok := child["headers"].(map[string]any)
				if !ok || headers["X-Safe"] != "preserved" {
					t.Fatalf("finalized sequence headers = %#v", child["headers"])
				}
				if _, exists := headers["Content-Length"]; exists {
					t.Fatalf("transport-owned sequence header survived finalization: %#v", headers)
				}
				metadata, _ := child["metadata"].(map[string]any)
				assertImportReviewBlocked(t, metadata)
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			server := NewServer(ServerConfig{})
			scriptID := "legacy-unsafe-" + strings.ReplaceAll(test.name, " ", "-")
			payload, err := json.Marshal(map[string]any{"script": test.script(scriptID)})
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(
				http.MethodPost,
				"/api/v1/scripts/import/postman/save",
				bytes.NewReader(payload),
			)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			server.mux.ServeHTTP(response, request)
			if response.Code != http.StatusCreated {
				t.Fatalf("commit status = %d, want 201 body=%s", response.Code, response.Body.String())
			}

			var result struct {
				StoredScript *StoredScript   `json:"stored_script"`
				Preflight    ReplayPreflight `json:"preflight"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.StoredScript == nil || result.StoredScript.Script == nil {
				t.Fatalf("commit response omitted stored script: %s", response.Body.String())
			}
			stored := result.StoredScript.Script
			if issues := dsl.ValidateScriptIssues(stored); len(issues) != 0 {
				t.Fatalf("stored blocked draft is not DSL-valid: %v", issues)
			}
			if result.Preflight.Valid {
				t.Fatalf("blocked draft reported executable preflight: %+v", result.Preflight)
			}
			test.assert(t, stored)

			persisted, ok := server.runner.GetScript(scriptID)
			if !ok || persisted == nil || persisted.Script == nil {
				t.Fatal("blocked draft was not persisted")
			}
			if issues := dsl.ValidateScriptIssues(persisted.Script); len(issues) != 0 {
				t.Fatalf("persisted blocked draft is not DSL-valid: %v", issues)
			}
		})
	}
}

func legacyUnsafeHeaderCommitScript(id string) *dsl.Script {
	return &dsl.Script{
		ID:        id,
		Name:      "Legacy unsafe header",
		Version:   "1.0.0",
		Protocol:  "http",
		ProjectID: "proj-legacy-unsafe",
		Config:    dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		Actions: []dsl.Action{{
			Name: "Imported request", Type: "request", Method: http.MethodGet, Target: "/",
		}},
	}
}

func assertImportReviewBlocked(t *testing.T, metadata map[string]any) {
	t.Helper()
	if metadata["execution_blocker"] != "import_review_required" {
		t.Fatalf("execution blocker = %#v", metadata)
	}
	review, ok := metadata["import_review"].(map[string]any)
	if !ok {
		t.Fatalf("import review metadata = %#v", metadata)
	}
	reasons, ok := review["reason_codes"].([]any)
	if !ok || len(reasons) == 0 {
		t.Fatalf("import review reasons = %#v", review["reason_codes"])
	}
}

func TestSharedImportCommitPreservesJSONBodyWireBytesAcrossLegacyRoutes(t *testing.T) {
	const canary = "commit-json-wire-canary-5f3e"
	const body = "{\n  \"count\": 1.00e+2,\n  \"password\": \"" + canary + "\",\n  \"ok\": true\n}"
	routes := []struct {
		name string
		path string
	}{
		{name: "openapi", path: "/api/v1/scripts/import/openapi/commit"},
		{name: "postman", path: "/api/v1/scripts/import/postman/save"},
		{name: "asyncapi", path: "/api/v1/scripts/import/asyncapi/commit"},
	}
	for _, route := range routes {
		route := route
		t.Run(route.name, func(t *testing.T) {
			server := NewServer(ServerConfig{})
			scriptID := "wire-bytes-" + route.name
			script := &dsl.Script{
				ID:        scriptID,
				Name:      "Wire bytes " + route.name,
				Version:   "1.0.0",
				Protocol:  "http",
				ProjectID: "proj-wire",
				Config:    dsl.ScriptConfig{BaseURL: "https://api.example.test"},
				Actions: []dsl.Action{{
					Name:   "Create record",
					Type:   "request",
					Method: http.MethodPost,
					Target: "/records",
					Headers: map[string]string{
						"Content-Type": "application/json; charset=utf-8",
					},
					Body: body,
				}},
			}
			payload, err := json.Marshal(map[string]any{"script": script})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, route.path, bytes.NewReader(payload))
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			server.mux.ServeHTTP(resp, req)
			if resp.Code != http.StatusCreated {
				t.Fatalf("commit status = %d body=%s", resp.Code, resp.Body.String())
			}
			var result struct {
				StoredScript StoredScript `json:"stored_script"`
			}
			if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			got := result.StoredScript.Script.Actions[0].Body
			if strings.Contains(got, canary) {
				t.Fatalf("credential remained in response body: %s", got)
			}
			start := strings.Index(got, "{{secret.")
			if start < 0 {
				t.Fatalf("projected body contains no secret reference: %s", got)
			}
			end := strings.Index(got[start:], "}}")
			if end < 0 {
				t.Fatalf("projected body contains an incomplete secret reference: %s", got)
			}
			reference := got[start : start+end+2]
			if _, ok := credential.ParseReference(reference); !ok {
				t.Fatalf("body reference = %q, want governed secret", reference)
			}
			want := strings.Replace(body, canary, reference, 1)
			if got != want {
				t.Fatalf("response body bytes changed outside credential occurrence:\n got: %q\nwant: %q", got, want)
			}
			persisted, ok := server.runner.GetScript(scriptID)
			if !ok || persisted == nil || persisted.Script == nil {
				t.Fatal("committed script was not persisted")
			}
			if persisted.Script.Actions[0].Body != want {
				t.Fatalf("persisted body bytes changed:\n got: %q\nwant: %q", persisted.Script.Actions[0].Body, want)
			}
		})
	}
}

func TestSharedImportCommitProjectsDecodedBase64JSONAcrossLegacyRoutes(t *testing.T) {
	const canary = "commit-base64-json-canary-73cc"
	const wireBody = "{\n  \"count\": 1.00e+2,\n  \"password\": \"" + canary + "\",\n  \"ok\": true\n}"
	encodedBody := base64.StdEncoding.EncodeToString([]byte(wireBody))
	routes := []struct {
		name string
		path string
	}{
		{name: "openapi", path: "/api/v1/scripts/import/openapi/commit"},
		{name: "postman", path: "/api/v1/scripts/import/postman/save"},
		{name: "asyncapi", path: "/api/v1/scripts/import/asyncapi/commit"},
	}
	for _, route := range routes {
		route := route
		t.Run(route.name, func(t *testing.T) {
			server := NewServer(ServerConfig{})
			scriptID := "base64-wire-" + route.name
			script := &dsl.Script{
				ID:        scriptID,
				Name:      "Base64 wire " + route.name,
				Version:   "1.0.0",
				Protocol:  "http",
				ProjectID: "proj-base64-wire",
				Config:    dsl.ScriptConfig{BaseURL: "https://api.example.test"},
				Actions: []dsl.Action{{
					Name: "Create record", Type: "request", Method: http.MethodPost, Target: "/records",
					Headers:      map[string]string{"Content-Type": "application/json; charset=utf-8"},
					Body:         encodedBody,
					BodyEncoding: "base64",
				}},
			}
			payload, err := json.Marshal(map[string]any{"script": script})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, route.path, bytes.NewReader(payload))
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			server.mux.ServeHTTP(resp, req)
			if resp.Code != http.StatusCreated {
				t.Fatalf("commit status = %d body=%s", resp.Code, resp.Body.String())
			}
			var result struct {
				StoredScript StoredScript `json:"stored_script"`
			}
			if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			action := result.StoredScript.Script.Actions[0]
			if action.BodyEncoding != "base64" {
				t.Fatalf("response body_encoding = %q, want base64", action.BodyEncoding)
			}
			projectedWire, err := base64.StdEncoding.DecodeString(strings.TrimSpace(action.Body))
			if err != nil {
				t.Fatalf("decode projected response body: %v", err)
			}
			if strings.Contains(string(projectedWire), canary) {
				t.Fatalf("decoded response body retained credential: %s", projectedWire)
			}
			start := strings.Index(string(projectedWire), "{{secret.")
			if start < 0 {
				t.Fatalf("decoded response body contains no secret reference: %s", projectedWire)
			}
			end := strings.Index(string(projectedWire[start:]), "}}")
			if end < 0 {
				t.Fatalf("decoded response body contains an incomplete secret reference: %s", projectedWire)
			}
			reference := string(projectedWire[start : start+end+2])
			if _, ok := credential.ParseReference(reference); !ok {
				t.Fatalf("body reference = %q, want governed secret", reference)
			}
			wantWire := strings.Replace(wireBody, canary, reference, 1)
			if string(projectedWire) != wantWire {
				t.Fatalf("wire bytes changed outside credential occurrence:\n got: %q\nwant: %q", projectedWire, wantWire)
			}
			persisted, ok := server.runner.GetScript(scriptID)
			if !ok || persisted == nil || persisted.Script == nil {
				t.Fatal("committed script was not persisted")
			}
			persistedAction := persisted.Script.Actions[0]
			if persistedAction.Body != action.Body || persistedAction.BodyEncoding != "base64" {
				t.Fatalf("persisted base64 representation differs from response: %#v != %#v", persistedAction, action)
			}
		})
	}
}

func TestSharedImportCommitPreservesDuplicateJSONKeysAsBlockedDraft(t *testing.T) {
	const body = "{\n  \"id\": 1,\n  \"id\": 2,\n  \"ordinary\": true\n}"
	server := NewServer(ServerConfig{})
	script := &dsl.Script{
		ID:        "duplicate-json-body",
		Name:      "Duplicate JSON body",
		Version:   "1.0.0",
		Protocol:  "http",
		ProjectID: "proj-duplicate-json",
		Config:    dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		Actions: []dsl.Action{{
			Name: "Create duplicate", Type: "request", Method: http.MethodPost, Target: "/records",
			Headers: map[string]string{"Content-Type": "application/json"},
			Body:    body,
		}},
	}
	payload, err := json.Marshal(map[string]any{"script": script})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/openapi/commit", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("commit status = %d body=%s", resp.Code, resp.Body.String())
	}
	var result struct {
		StoredScript StoredScript    `json:"stored_script"`
		Preflight    ReplayPreflight `json:"preflight"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	action := result.StoredScript.Script.Actions[0]
	if action.Body != body {
		t.Fatalf("duplicate-key body bytes changed:\n got: %q\nwant: %q", action.Body, body)
	}
	if action.Metadata["execution_blocker"] != "import_review_required" {
		t.Fatalf("duplicate-key body execution gate = %#v", action.Metadata)
	}
	review, _ := action.Metadata["import_review"].(map[string]any)
	reasons, _ := review["reason_codes"].([]any)
	if len(reasons) != 1 || reasons[0] != "credential_json_duplicate_key" {
		t.Fatalf("duplicate-key body review = %#v", review)
	}
	if result.Preflight.Valid {
		t.Fatalf("blocked duplicate-key draft reported executable preflight: %+v", result.Preflight)
	}
}

func TestSharedImportCommitRejectsSensitiveTypedJSONAcrossLegacyRoutes(t *testing.T) {
	const typedValue = "867530912345"
	const body = `{"password":` + typedValue + `}`
	routes := []struct {
		name string
		path string
	}{
		{name: "openapi", path: "/api/v1/scripts/import/openapi/commit"},
		{name: "postman", path: "/api/v1/scripts/import/postman/save"},
		{name: "asyncapi", path: "/api/v1/scripts/import/asyncapi/commit"},
	}
	for _, route := range routes {
		route := route
		t.Run(route.name, func(t *testing.T) {
			server := NewServer(ServerConfig{})
			scriptID := "typed-json-" + route.name
			script := &dsl.Script{
				ID:        scriptID,
				Name:      "Typed JSON " + route.name,
				Version:   "1.0.0",
				Protocol:  "http",
				ProjectID: "proj-typed-json",
				Config:    dsl.ScriptConfig{BaseURL: "https://api.example.test"},
				Actions: []dsl.Action{{
					Name: "Typed credential", Type: "request", Method: http.MethodPost, Target: "/login",
					Headers: map[string]string{"Content-Type": "application/json"},
					Body:    body,
				}},
			}
			payload, err := json.Marshal(map[string]any{"script": script})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, route.path, bytes.NewReader(payload))
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			server.mux.ServeHTTP(resp, req)
			if resp.Code != http.StatusBadRequest {
				t.Fatalf("commit status = %d, want 400 body=%s", resp.Code, resp.Body.String())
			}
			if !strings.Contains(resp.Body.String(), "credential_json_sensitive_non_string_unsupported") {
				t.Fatalf("typed credential rejection omitted stable code: %s", resp.Body.String())
			}
			if strings.Contains(resp.Body.String(), typedValue) {
				t.Fatalf("typed credential leaked through rejection: %s", resp.Body.String())
			}
			if _, ok := server.runner.GetScript(scriptID); ok {
				t.Fatal("rejected typed credential script was persisted")
			}
		})
	}
}

func TestProtectedImportCommitPreservesJSONBodyBytes(t *testing.T) {
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{
		Key:   bytes.Repeat([]byte{0x4b}, 32),
		KeyID: "protected-json-wire-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{
		BrunoArtifactDir:     t.TempDir(),
		BrunoArtifactWrapper: wrapper,
	})
	const body = "{\n  \"id\": 1,\n  \"id\": 2,\n  \"password\": \"{{secret.PROTECTED_PASSWORD}}\"\n}"
	routes := []struct {
		profile string
		path    string
	}{
		{profile: "openapi", path: "/api/v1/scripts/import/openapi/commit"},
		{profile: "postman", path: "/api/v1/scripts/import/postman/save"},
	}
	for _, route := range routes {
		route := route
		t.Run(route.profile, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/", nil)
			candidate := &dsl.Script{
				ID:        "protected-wire-" + route.profile,
				Name:      "Protected wire " + route.profile,
				Version:   "1.0.0",
				Protocol:  "http",
				ProjectID: "proj-protected-wire",
				Config:    dsl.ScriptConfig{BaseURL: "https://api.example.test"},
				Actions: []dsl.Action{{
					Name: "Protected request", Type: "request", Method: http.MethodPost, Target: "/records?user_id=12345",
					Headers: map[string]string{"Content-Type": "application/json"},
					Body:    body,
				}},
			}
			var parameterizationID string
			for _, suggestion := range buildTrustedImportParameterization(candidate).Suggestions {
				if suggestion.VarName == "user_id" {
					parameterizationID = suggestion.ID
					break
				}
			}
			if parameterizationID == "" {
				t.Fatal("protected candidate produced no trusted user_id parameterization")
			}
			sessionID, _, err := server.registerImportCandidateSession(request, route.profile, candidate)
			if err != nil {
				t.Fatal(err)
			}
			payload, err := json.Marshal(map[string]any{
				"import_session_id": sessionID,
				"project_id":        candidate.ProjectID,
				"parameterization_selected_ids": []string{
					parameterizationID,
				},
				"script": dsl.Script{
					Name:      "Ignored client script",
					ProjectID: candidate.ProjectID,
					Actions: []dsl.Action{{
						Name: "Tampered", Type: "request", Method: http.MethodPost, Target: "/tampered",
						Body: `{"password":8675309}`,
					}},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, route.path, bytes.NewReader(payload))
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			server.mux.ServeHTTP(resp, req)
			if resp.Code != http.StatusCreated {
				t.Fatalf("commit status = %d body=%s", resp.Code, resp.Body.String())
			}
			var result struct {
				StoredScript StoredScript `json:"stored_script"`
			}
			if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			action := result.StoredScript.Script.Actions[0]
			if action.Body != body {
				t.Fatalf("protected body bytes changed:\n got: %q\nwant: %q", action.Body, body)
			}
			if action.Target != "/records?user_id={{var.user_id}}" ||
				action.Metadata["execution_blocker"] != "import_review_required" {
				t.Fatalf("protected candidate or duplicate-key gate was not retained: %+v", action)
			}
		})
	}
}

func TestProtectedImportCommitPreservesBase64RepresentationAndWireBytes(t *testing.T) {
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{
		Key:   bytes.Repeat([]byte{0x6d}, 32),
		KeyID: "protected-base64-json-wire-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{
		BrunoArtifactDir:     t.TempDir(),
		BrunoArtifactWrapper: wrapper,
	})
	const wireBody = "{\n  \"id\": 1,\n  \"id\": 2,\n  \"password\": \"{{secret.PROTECTED_PASSWORD}}\"\n}"
	// Deliberately retain harmless authored whitespace around the encoded body.
	// A no-op protected projection must not canonicalize this representation.
	encodedBody := "\r\n" + base64.StdEncoding.EncodeToString([]byte(wireBody)) + "\n"
	routes := []struct {
		profile string
		path    string
	}{
		{profile: "openapi", path: "/api/v1/scripts/import/openapi/commit"},
		{profile: "postman", path: "/api/v1/scripts/import/postman/save"},
	}
	for _, route := range routes {
		route := route
		t.Run(route.profile, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/", nil)
			candidate := &dsl.Script{
				ID:        "protected-base64-wire-" + route.profile,
				Name:      "Protected base64 wire " + route.profile,
				Version:   "1.0.0",
				Protocol:  "http",
				ProjectID: "proj-protected-base64-wire",
				Config:    dsl.ScriptConfig{BaseURL: "https://api.example.test"},
				Actions: []dsl.Action{{
					Name: "Protected request", Type: "request", Method: http.MethodPost, Target: "/records",
					Headers:      map[string]string{"Content-Type": "application/json"},
					Body:         encodedBody,
					BodyEncoding: "base64",
				}},
			}
			sessionID, _, err := server.registerImportCandidateSession(request, route.profile, candidate)
			if err != nil {
				t.Fatal(err)
			}
			payload, err := json.Marshal(map[string]any{
				"import_session_id": sessionID,
				"project_id":        candidate.ProjectID,
				"script": dsl.Script{
					Name:      "Ignored client script",
					ProjectID: candidate.ProjectID,
					Actions: []dsl.Action{{
						Name: "Tampered", Type: "request", Method: http.MethodPost, Target: "/tampered",
						Body: `{"password":8675309}`,
					}},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, route.path, bytes.NewReader(payload))
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			server.mux.ServeHTTP(resp, req)
			if resp.Code != http.StatusCreated {
				t.Fatalf("commit status = %d body=%s", resp.Code, resp.Body.String())
			}
			var result struct {
				StoredScript StoredScript `json:"stored_script"`
			}
			if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			action := result.StoredScript.Script.Actions[0]
			if action.Body != encodedBody || action.BodyEncoding != "base64" {
				t.Fatalf("protected base64 representation changed:\n got: %q (%q)\nwant: %q (base64)", action.Body, action.BodyEncoding, encodedBody)
			}
			decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(action.Body))
			if err != nil {
				t.Fatal(err)
			}
			if string(decoded) != wireBody {
				t.Fatalf("protected wire bytes changed:\n got: %q\nwant: %q", decoded, wireBody)
			}
			if action.Metadata["execution_blocker"] != "import_review_required" {
				t.Fatalf("duplicate-key review gate was not retained: %+v", action.Metadata)
			}
		})
	}
}

func TestMaskScriptSecretsFailsClosedForSensitiveTypedJSONBody(t *testing.T) {
	script := &dsl.Script{
		Name: "Typed secret", Version: "1.0.0", Protocol: "http",
		Actions: []dsl.Action{{
			Name: "Login", Type: "request", Method: http.MethodPost, Target: "/login",
			Headers: map[string]string{"Content-Type": "application/json"},
			Body:    `{"password":7}`,
		}},
	}
	if projected := maskScriptSecrets(script); projected != nil {
		t.Fatalf("typed sensitive JSON became a runnable string projection: %#v", projected.Actions[0])
	}
}

func TestPostmanProtectedImportSessionCommitsServerCandidate(t *testing.T) {
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{
		Key:   bytes.Repeat([]byte{0x63}, 32),
		KeyID: "postman-import-session-test",
	})
	if err != nil {
		t.Fatalf("NewLocalAESKeyWrapper: %v", err)
	}
	server := NewServer(ServerConfig{
		BrunoArtifactDir:     t.TempDir(),
		BrunoArtifactWrapper: wrapper,
	})

	inspectionBody := []byte(`{
	  "content": "{\"info\":{\"name\":\"Protected\"},\"item\":[{\"name\":\"List\",\"request\":{\"method\":\"GET\",\"url\":\"https://api.example.test/orders\"}}]}",
	  "project_id": "proj-protected"
	}`)
	inspectionReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/postman", bytes.NewReader(inspectionBody))
	inspectionReq.Header.Set("Content-Type", "application/json")
	inspectionResp := httptest.NewRecorder()
	server.mux.ServeHTTP(inspectionResp, inspectionReq)
	if inspectionResp.Code != http.StatusOK {
		t.Fatalf("inspection status = %d body=%s", inspectionResp.Code, inspectionResp.Body.String())
	}
	var inspection struct {
		ImportSessionID string     `json:"import_session_id"`
		ExpiresAt       string     `json:"expires_at"`
		Script          dsl.Script `json:"script"`
	}
	if err := json.Unmarshal(inspectionResp.Body.Bytes(), &inspection); err != nil {
		t.Fatalf("decode inspection: %v", err)
	}
	if inspection.ImportSessionID == "" || inspection.ExpiresAt == "" {
		t.Fatalf("protected session fields missing: %s", inspectionResp.Body.String())
	}

	commitPayload := map[string]any{
		"import_session_id": inspection.ImportSessionID,
		"project_id":        "proj-protected",
		"name":              "Renamed Protected Import",
		"script": dsl.Script{
			Name:      "Client Tampering",
			Version:   "1.0.0",
			Protocol:  "http",
			ProjectID: "proj-protected",
			Config:    dsl.ScriptConfig{BaseURL: "https://evil.example.test"},
			Actions: []dsl.Action{{
				Name: "Tampered", Type: "request", Method: "DELETE", Target: "/everything",
			}},
		},
	}
	commitBody, err := json.Marshal(commitPayload)
	if err != nil {
		t.Fatal(err)
	}
	commitReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/postman/save", bytes.NewReader(commitBody))
	commitReq.Header.Set("Content-Type", "application/json")
	commitResp := httptest.NewRecorder()
	server.mux.ServeHTTP(commitResp, commitReq)
	if commitResp.Code != http.StatusCreated {
		t.Fatalf("commit status = %d body=%s", commitResp.Code, commitResp.Body.String())
	}
	var committed struct {
		StoredScript StoredScript `json:"stored_script"`
	}
	if err := json.Unmarshal(commitResp.Body.Bytes(), &committed); err != nil {
		t.Fatalf("decode commit: %v", err)
	}
	if committed.StoredScript.Name != "Renamed Protected Import" ||
		committed.StoredScript.Script == nil ||
		len(committed.StoredScript.Script.Actions) != 1 ||
		committed.StoredScript.Script.Actions[0].Method != "GET" ||
		committed.StoredScript.Script.Config.BaseURL != "https://api.example.test" {
		t.Fatalf("stored candidate accepted client script mutation: %#v", committed.StoredScript)
	}

	retryReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/postman/save", bytes.NewReader(commitBody))
	retryReq.Header.Set("Content-Type", "application/json")
	retryResp := httptest.NewRecorder()
	server.mux.ServeHTTP(retryResp, retryReq)
	if retryResp.Code != http.StatusNotFound {
		t.Fatalf("consumed-session retry status = %d, want 404 body=%s", retryResp.Code, retryResp.Body.String())
	}
}

func TestProtectedImportCreateReconcilesInterruptedClaimCompletion(t *testing.T) {
	artifactDir := t.TempDir()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "protected-import.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{
		Key:   bytes.Repeat([]byte{0x5d}, 32),
		KeyID: "protected-create-reconciliation-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{
		Store:                store,
		BrunoArtifactDir:     artifactDir,
		BrunoArtifactWrapper: wrapper,
	})
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	candidate := &dsl.Script{
		Name:      "Interrupted protected create",
		Version:   "1.0.0",
		Protocol:  "http",
		ProjectID: normalizeProjectID(""),
		Config:    dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		Actions: []dsl.Action{{
			Name: "Get", Type: "request", Method: http.MethodGet, Target: "/orders",
		}},
	}
	sessionID, _, err := server.registerImportCandidateSession(request, "postman", candidate)
	if err != nil || sessionID == "" {
		t.Fatalf("register candidate: id=%q err=%v", sessionID, err)
	}
	protected, firstClaim, err := server.claimProtectedImportCandidateSession(
		request, "postman", sessionID, candidate.ProjectID,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer clearProtectedImportCandidate(protected)

	// Model a controller that commits the script and then exits before it can
	// complete its protected-session claim.
	first, _, err := applySuggestedImportCommitSecretBindings(protected.Script, 0, "postman")
	if err != nil {
		t.Fatal(err)
	}
	stripProtectedImportCredentialMetadata(first)
	first.ID = protectedImportCreateScriptID("postman", sessionID)
	first, _, err = finalizeImportCommitScript(first, "postman")
	if err != nil {
		t.Fatal(err)
	}
	initial, err := server.runner.CreateScriptDurable(first)
	if err != nil {
		t.Fatal(err)
	}

	claimPath := filepath.Join(artifactDir, ".sessions", sessionID+".claim.json")
	stale := time.Now().UTC().Add(-time.Minute)
	if err := os.Chtimes(claimPath, stale, stale); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{
		"import_session_id": sessionID,
		"project_id":        candidate.ProjectID,
	})
	if err != nil {
		t.Fatal(err)
	}
	retry := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts/import/postman/save",
		bytes.NewReader(body),
	)
	retry.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, retry)
	if response.Code != http.StatusCreated {
		t.Fatalf("reconciled retry status = %d body=%s", response.Code, response.Body.String())
	}
	var committed struct {
		StoredScript StoredScript `json:"stored_script"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &committed); err != nil {
		t.Fatal(err)
	}
	if committed.StoredScript.ID != initial.ID {
		t.Fatalf("reconciled script id = %q, want %q", committed.StoredScript.ID, initial.ID)
	}
	// Pin the client-visible flags, not just the durable script id. This retry
	// reconciles rather than replays: the interrupted first attempt never
	// persisted a row, so the retry genuinely creates one and must say so.
	//
	// Note what this does NOT cover. The idempotent_replay=true branch needs a
	// retry against a commit that already persisted, and after a successful
	// commit the session claim is completed, so a retry is rejected at claim time
	// rather than reaching the replay branch. That branch is effectively
	// unreachable for this path until a durable receipt exists (Phase 5.1) --
	// which is exactly why completeImportCandidateClaim stays epoch-fenced.
	var replayFlags struct {
		Created          bool `json:"created"`
		IdempotentReplay bool `json:"idempotent_replay"`
		Replaced         bool `json:"replaced"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &replayFlags); err != nil {
		t.Fatal(err)
	}
	if replayFlags.Replaced || !replayFlags.Created || replayFlags.IdempotentReplay {
		t.Fatalf("reconciled create flags = created:%v idempotent_replay:%v replaced:%v, want true/false/false",
			replayFlags.Created, replayFlags.IdempotentReplay, replayFlags.Replaced)
	}
	scripts, err := store.ListScripts(candidate.ProjectID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(scripts) != 1 || scripts[0].ID != initial.ID {
		t.Fatalf("durable scripts after retry = %#v, want one reconciled script %q", scripts, initial.ID)
	}
	if err := server.completeImportCandidateClaim(context.Background(), firstClaim); err == nil {
		t.Fatal("stale first claim unexpectedly completed after reconciliation")
	}
}

func TestProtectedImportCompletionNeverFinalizesNewerClaimEpoch(t *testing.T) {
	artifactDir := t.TempDir()
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{
		Key:   bytes.Repeat([]byte{0x6e}, 32),
		KeyID: "protected-claim-fencing-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{
		BrunoArtifactDir:     artifactDir,
		BrunoArtifactWrapper: wrapper,
	})
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	candidate := &dsl.Script{
		Name:      "Claim fencing",
		Version:   "1.0.0",
		Protocol:  "http",
		ProjectID: "proj-claim-fencing",
		Config:    dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		Actions: []dsl.Action{{
			Name: "Get", Type: "request", Method: http.MethodGet, Target: "/",
		}},
	}
	sessionID, _, err := server.registerImportCandidateSession(request, "postman", candidate)
	if err != nil || sessionID == "" {
		t.Fatalf("register candidate: id=%q err=%v", sessionID, err)
	}
	_, oldClaim, err := server.claimImportCandidateSession(
		request, "postman", sessionID, candidate.ProjectID,
	)
	if err != nil {
		t.Fatal(err)
	}
	claimPath := filepath.Join(artifactDir, ".sessions", sessionID+".claim.json")
	stale := time.Now().UTC().Add(-time.Minute)
	if err := os.Chtimes(claimPath, stale, stale); err != nil {
		t.Fatal(err)
	}
	_, newClaim, err := server.claimImportCandidateSession(
		request, "postman", sessionID, candidate.ProjectID,
	)
	if err != nil {
		t.Fatalf("recover stale claim: %v", err)
	}
	if oldClaim.Token == newClaim.Token {
		t.Fatal("stale-claim recovery reused the prior fencing token")
	}

	if err := server.completeImportCandidateClaim(context.Background(), oldClaim); err == nil {
		t.Fatal("stale claim completion unexpectedly succeeded")
	}
	manager, err := server.protectedImportSessionManager()
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Renew(*newClaim, time.Now().UTC()); err != nil {
		t.Fatalf("stale completion removed or invalidated newer claim: %v", err)
	}
	if _, _, err := server.claimImportCandidateSession(
		request, "postman", sessionID, candidate.ProjectID,
	); !errors.Is(err, importsession.ErrSessionClaimed) {
		t.Fatalf("newer claim was not retained: err=%v", err)
	}
	server.releaseImportCandidateClaim(newClaim)
}

func TestLegacyImportCreateNeverOverwritesExistingScriptID(t *testing.T) {
	server := NewServer(ServerConfig{})
	existing := &dsl.Script{
		ID:        "legacy-create-collision",
		Name:      "Original",
		Version:   "1.0.0",
		Protocol:  "http",
		ProjectID: "proj-create-only",
		Config:    dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		Actions: []dsl.Action{{
			Name: "Original request", Type: "request", Method: http.MethodGet, Target: "/original",
		}},
	}
	if _, err := server.runner.CreateScriptDurable(existing); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{
		"mode": "create",
		"script": dsl.Script{
			ID:        existing.ID,
			Name:      "Replacement",
			Version:   "1.0.0",
			Protocol:  "http",
			ProjectID: existing.ProjectID,
			Config:    dsl.ScriptConfig{BaseURL: "https://api.example.test"},
			Actions: []dsl.Action{{
				Name: "Replacement request", Type: "request", Method: http.MethodDelete, Target: "/replacement",
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts/import/postman/save",
		bytes.NewReader(body),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("create collision status = %d, want 409 body=%s", response.Code, response.Body.String())
	}
	stored, ok := server.runner.GetScript(existing.ID)
	if !ok || stored == nil || stored.Script == nil {
		t.Fatal("original script disappeared after create collision")
	}
	if stored.Name != existing.Name ||
		len(stored.Script.Actions) != 1 ||
		stored.Script.Actions[0].Target != "/original" {
		t.Fatalf("mode=create overwrote existing script: %+v", stored)
	}
}

func TestImportReplaceRejectsConcurrentTargetMutation(t *testing.T) {
	server := NewServer(ServerConfig{})
	existing := &dsl.Script{
		ID:        "concurrent-import-replace",
		Name:      "Existing",
		Version:   "1.0.0",
		Protocol:  "http",
		ProjectID: "proj-concurrent-import",
		Config:    dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		Actions: []dsl.Action{{
			Name: "Existing request", Type: "request", Method: http.MethodGet, Target: "/existing",
		}},
	}
	stored, err := server.runner.CreateScriptDurable(existing)
	if err != nil {
		t.Fatal(err)
	}
	var concurrentErr error
	spec := importCommitSpec{
		sourceProfile:       "test",
		auditProfile:        "test",
		displayName:         "Test",
		allowedReplaceModes: []string{"full"},
		actionKey:           func(action dsl.Action) string { return action.Name },
		diff: func(*dsl.Script, *dsl.Script, string, func(dsl.Action) string) openAPIImportDiff {
			current, ok := server.runner.GetScript(stored.ID)
			if !ok || current == nil || current.Script == nil {
				concurrentErr = errors.New("target disappeared before concurrent update")
				return openAPIImportDiff{}
			}
			concurrent := cloneScriptDefinition(current.Script)
			concurrent.Actions[0].Target = "/concurrent-editor"
			_, _, concurrentErr = server.runner.UpdateScriptDurableInProject(
				stored.ID,
				concurrent,
				existing.ProjectID,
			)
			return openAPIImportDiff{}
		},
		merge: func(_ *dsl.Script, imported *dsl.Script, _ func(dsl.Action) string) *dsl.Script {
			return imported
		},
		mapCompatibleResponse: identityImportCommitResponse,
	}
	body, err := json.Marshal(map[string]any{
		"mode":             "replace",
		"replace_mode":     "full",
		"target_script_id": stored.ID,
		"script": dsl.Script{
			Name:      "Imported",
			Version:   "1.0.0",
			Protocol:  "http",
			ProjectID: existing.ProjectID,
			Config:    dsl.ScriptConfig{BaseURL: "https://api.example.test"},
			Actions: []dsl.Action{{
				Name: "Imported request", Type: "request", Method: http.MethodGet, Target: "/imported",
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/test/import/commit", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.handleImportCommit(response, request, spec)
	if concurrentErr != nil {
		t.Fatalf("inject concurrent mutation: %v", concurrentErr)
	}
	if response.Code != http.StatusConflict {
		t.Fatalf("concurrent replace status = %d, want 409 body=%s", response.Code, response.Body.String())
	}
	current, ok := server.runner.GetScript(stored.ID)
	if !ok || current == nil || current.Script == nil ||
		len(current.Script.Actions) != 1 ||
		current.Script.Actions[0].Target != "/concurrent-editor" {
		t.Fatalf("stale import overwrote concurrent editor: %+v", current)
	}
}

func TestProtectedImportCorruptSessionIsInternalFailure(t *testing.T) {
	artifactDir := t.TempDir()
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{
		Key:   bytes.Repeat([]byte{0x3a}, 32),
		KeyID: "protected-corrupt-session-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{
		BrunoArtifactDir:     artifactDir,
		BrunoArtifactWrapper: wrapper,
	})
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	candidate := &dsl.Script{
		Name:      "Corrupt session",
		Version:   "1.0.0",
		Protocol:  "http",
		ProjectID: "proj-corrupt-session",
		Config:    dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		Actions: []dsl.Action{{
			Name: "Get", Type: "request", Method: http.MethodGet, Target: "/",
		}},
	}
	sessionID, _, err := server.registerImportCandidateSession(request, "postman", candidate)
	if err != nil || sessionID == "" {
		t.Fatalf("register candidate: id=%q err=%v", sessionID, err)
	}
	descriptorPath := filepath.Join(artifactDir, ".sessions", sessionID+".json")
	if err := os.WriteFile(descriptorPath, []byte("{invalid"), 0o600); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{
		"import_session_id": sessionID,
		"project_id":        candidate.ProjectID,
	})
	if err != nil {
		t.Fatal(err)
	}
	commit := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts/import/postman/save",
		bytes.NewReader(body),
	)
	commit.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, commit)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("corrupt session status = %d, want 500 body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "invalid character") {
		t.Fatalf("corruption details crossed API boundary: %s", response.Body.String())
	}
}

func TestProtectedImportFolderOverrideIsProjectScopedAndNonEnumerating(t *testing.T) {
	artifactDir := t.TempDir()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "protected-folder.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	const (
		projectID       = "protected-folder-project"
		foreignProject  = "protected-folder-foreign-project"
		localFolderID   = "protected-folder-local"
		foreignFolderID = "protected-folder-foreign"
	)
	now := time.Now().UTC()
	for _, project := range []*storage.Project{
		{ID: projectID, Name: "Protected folder project", CreatedAt: now},
		{ID: foreignProject, Name: "Foreign project", CreatedAt: now},
	} {
		if err := store.SaveProject(project); err != nil {
			t.Fatal(err)
		}
	}
	for _, folder := range []*storage.Folder{
		{ID: localFolderID, ProjectID: projectID, Name: "Local", Category: "scripts"},
		{ID: foreignFolderID, ProjectID: foreignProject, Name: "Foreign", Category: "scripts"},
	} {
		if err := store.SaveFolder(folder); err != nil {
			t.Fatal(err)
		}
	}
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{
		Key:   bytes.Repeat([]byte{0x4f}, 32),
		KeyID: "protected-folder-override-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{
		Store:                store,
		BrunoArtifactDir:     artifactDir,
		BrunoArtifactWrapper: wrapper,
	})
	inspection := httptest.NewRequest(http.MethodPost, "/", nil)
	candidate := &dsl.Script{
		Name:      "Folder candidate",
		Version:   "1.0.0",
		Protocol:  "http",
		ProjectID: projectID,
		Config:    dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		Actions: []dsl.Action{{
			Name: "Get", Type: "request", Method: http.MethodGet, Target: "/",
		}},
	}
	sessionID, _, err := server.registerImportCandidateSession(inspection, "postman", candidate)
	if err != nil || sessionID == "" {
		t.Fatalf("register candidate: id=%q err=%v", sessionID, err)
	}
	commitToFolder := func(folderID string) *httptest.ResponseRecorder {
		t.Helper()
		body, marshalErr := json.Marshal(map[string]any{
			"import_session_id": sessionID,
			"project_id":        projectID,
			"folder_id":         folderID,
		})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		request := httptest.NewRequest(
			http.MethodPost,
			"/api/v1/scripts/import/postman/save",
			bytes.NewReader(body),
		)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		server.mux.ServeHTTP(response, request)
		return response
	}
	foreign := commitToFolder(foreignFolderID)
	missing := commitToFolder("protected-folder-missing")
	if foreign.Code != http.StatusNotFound || missing.Code != http.StatusNotFound {
		t.Fatalf(
			"foreign/missing folder statuses = %d/%d, want 404/404 bodies=%s / %s",
			foreign.Code,
			missing.Code,
			foreign.Body.String(),
			missing.Body.String(),
		)
	}
	if foreign.Body.String() != missing.Body.String() {
		t.Fatalf("folder existence leaked through response: foreign=%s missing=%s", foreign.Body.String(), missing.Body.String())
	}
	local := commitToFolder(localFolderID)
	if local.Code != http.StatusCreated {
		t.Fatalf("local folder commit status = %d body=%s", local.Code, local.Body.String())
	}
	var result struct {
		StoredScript StoredScript `json:"stored_script"`
	}
	if err := json.Unmarshal(local.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.StoredScript.FolderID == nil || *result.StoredScript.FolderID != localFolderID {
		t.Fatalf("validated local folder was not applied: %+v", result.StoredScript)
	}
}

func TestProtectedImportSessionProfileIsolationAndDryRunRelease(t *testing.T) {
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{
		Key:   bytes.Repeat([]byte{0x42}, 32),
		KeyID: "profile-isolation-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{
		BrunoArtifactDir:     t.TempDir(),
		BrunoArtifactWrapper: wrapper,
	})
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	candidate := &dsl.Script{
		Name:      "Candidate",
		Version:   "1.0.0",
		Protocol:  "http",
		ProjectID: "proj-profile",
		Config:    dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		Actions:   []dsl.Action{{Name: "Get", Type: "request", Method: "GET", Target: "/"}},
	}
	sessionID, _, err := server.registerImportCandidateSession(request, "postman", candidate)
	if err != nil || sessionID == "" {
		t.Fatalf("register candidate: id=%q err=%v", sessionID, err)
	}

	wrongProfileBody, _ := json.Marshal(map[string]any{
		"import_session_id": sessionID,
		"project_id":        "proj-profile",
	})
	wrongProfileReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/openapi/commit", bytes.NewReader(wrongProfileBody))
	wrongProfileReq.Header.Set("Content-Type", "application/json")
	wrongProfileResp := httptest.NewRecorder()
	server.mux.ServeHTTP(wrongProfileResp, wrongProfileReq)
	if wrongProfileResp.Code != http.StatusNotFound {
		t.Fatalf("cross-profile status = %d body=%s", wrongProfileResp.Code, wrongProfileResp.Body.String())
	}

	dryRunBody, _ := json.Marshal(map[string]any{
		"import_session_id": sessionID,
		"project_id":        "proj-profile",
		"dry_run":           true,
	})
	dryRunReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/postman/save", bytes.NewReader(dryRunBody))
	dryRunReq.Header.Set("Content-Type", "application/json")
	dryRunResp := httptest.NewRecorder()
	server.mux.ServeHTTP(dryRunResp, dryRunReq)
	if dryRunResp.Code != http.StatusOK {
		t.Fatalf("dry run status = %d body=%s", dryRunResp.Code, dryRunResp.Body.String())
	}

	commitBody, _ := json.Marshal(map[string]any{
		"import_session_id": sessionID,
		"project_id":        "proj-profile",
	})
	commitReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/postman/save", bytes.NewReader(commitBody))
	commitReq.Header.Set("Content-Type", "application/json")
	commitResp := httptest.NewRecorder()
	server.mux.ServeHTTP(commitResp, commitReq)
	if commitResp.Code != http.StatusCreated {
		t.Fatalf("commit after dry-run status = %d body=%s", commitResp.Code, commitResp.Body.String())
	}
}

func TestProtectedImportReplaceCannotCrossCandidateProjectScope(t *testing.T) {
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{
		Key:   bytes.Repeat([]byte{0x32}, 32),
		KeyID: "protected-replace-project-scope-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{
		BrunoArtifactDir:     t.TempDir(),
		BrunoArtifactWrapper: wrapper,
	})
	const (
		candidateProject = "protected-candidate-project"
		targetProject    = "protected-target-project"
	)
	target := &dsl.Script{
		ID:        "protected-cross-project-target",
		Name:      "Foreign target",
		Version:   "1.0.0",
		Protocol:  "http",
		ProjectID: targetProject,
		Config:    dsl.ScriptConfig{BaseURL: "https://target.example.test"},
		Actions: []dsl.Action{{
			Name: "Original", Type: "request", Method: http.MethodGet, Target: "/original",
		}},
	}
	storedTarget := server.runner.StoreScript(target)
	if storedTarget == nil {
		t.Fatal("failed to seed replace target")
	}
	candidate := &dsl.Script{
		Name:      "Protected candidate",
		Version:   "1.0.0",
		Protocol:  "http",
		ProjectID: candidateProject,
		Config:    dsl.ScriptConfig{BaseURL: "https://candidate.example.test"},
		Actions: []dsl.Action{{
			Name: "Candidate", Type: "request", Method: http.MethodPost, Target: "/candidate",
		}},
	}
	inspection := httptest.NewRequest(http.MethodPost, "/", nil)
	sessionID, _, err := server.registerImportCandidateSession(inspection, "postman", candidate)
	if err != nil {
		t.Fatalf("register protected candidate: %v", err)
	}

	replaceBody, err := json.Marshal(map[string]any{
		"import_session_id": sessionID,
		"project_id":        candidateProject,
		"mode":              "replace",
		"target_script_id":  storedTarget.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	replaceReq := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts/import/postman/save",
		bytes.NewReader(replaceBody),
	)
	replaceReq.Header.Set("Content-Type", "application/json")
	replaceResp := httptest.NewRecorder()
	server.mux.ServeHTTP(replaceResp, replaceReq)
	if replaceResp.Code != http.StatusNotFound {
		t.Fatalf("cross-project replace status = %d, want 404 body=%s", replaceResp.Code, replaceResp.Body.String())
	}
	if !strings.Contains(replaceResp.Body.String(), "target script not found") {
		t.Fatalf("cross-project response disclosed scope details: %s", replaceResp.Body.String())
	}
	unchanged, ok := server.runner.GetScript(storedTarget.ID)
	if !ok || unchanged == nil || unchanged.Script == nil {
		t.Fatal("replace target disappeared")
	}
	if unchanged.ProjectID != targetProject ||
		unchanged.Script.Config.BaseURL != target.Config.BaseURL ||
		unchanged.Script.Actions[0].Target != target.Actions[0].Target {
		t.Fatalf("cross-project protected candidate mutated target: %+v", unchanged)
	}

	// A scope rejection must release, not consume, the fenced claim.
	createBody, err := json.Marshal(map[string]any{
		"import_session_id": sessionID,
		"project_id":        candidateProject,
	})
	if err != nil {
		t.Fatal(err)
	}
	createReq := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts/import/postman/save",
		bytes.NewReader(createBody),
	)
	createReq.Header.Set("Content-Type", "application/json")
	createResp := httptest.NewRecorder()
	server.mux.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("same-project retry status = %d, want 201 body=%s", createResp.Code, createResp.Body.String())
	}
}

func TestProtectedImportSessionAcceptsOnlyExplicitCandidateOverrides(t *testing.T) {
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{
		Key:   bytes.Repeat([]byte{0x71}, 32),
		KeyID: "explicit-overrides-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{
		BrunoArtifactDir:     t.TempDir(),
		BrunoArtifactWrapper: wrapper,
	})
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	candidate := &dsl.Script{
		Name:      "Server Candidate",
		Version:   "1.0.0",
		Protocol:  "http",
		ProjectID: "proj-explicit",
		Config:    dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		Actions: []dsl.Action{{
			Name: "Get", Type: "request", Method: "GET", Target: "/users?user_id=12345",
			Headers:    map[string]string{"Authorization": maskedScriptSecret},
			HeaderList: dsl.HeaderList{{Name: "Authorization", Value: maskedScriptSecret}},
		}},
	}
	var selectedParameterizationID string
	for _, suggestion := range buildTrustedImportParameterization(candidate).Suggestions {
		if suggestion.VarName == "user_id" {
			selectedParameterizationID = suggestion.ID
			break
		}
	}
	if selectedParameterizationID == "" {
		t.Fatal("protected candidate produced no trusted user_id parameterization")
	}
	sessionID, _, err := server.registerImportCandidateSession(request, "postman", candidate)
	if err != nil {
		t.Fatalf("register candidate: %v", err)
	}
	requirements := discoverSecretBindingRequirements(candidate, 0)
	if len(requirements) != 2 {
		// HeaderList is authoritative but the compatibility map remains another
		// exact serialized occurrence and therefore has its own binding.
		t.Fatalf("binding requirements = %#v, want header list and map occurrences", requirements)
	}
	bindings := make(map[string]string, len(requirements))
	for _, requirement := range requirements {
		bindings[requirement.ID] = "CUSTOM_AUTH"
	}
	foreignFolder := "client-controlled-folder"
	body, _ := json.Marshal(map[string]any{
		"import_session_id": sessionID,
		"project_id":        candidate.ProjectID,
		"secret_bindings":   bindings,
		"parameterization_selected_ids": []string{
			selectedParameterizationID,
		},
		"script": dsl.Script{
			Name:      "Implicit Client Rename",
			ProjectID: candidate.ProjectID,
			FolderID:  &foreignFolder,
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/postman/save", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("commit status = %d body=%s", resp.Code, resp.Body.String())
	}
	var result struct {
		StoredScript StoredScript `json:"stored_script"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.StoredScript.Name != candidate.Name || result.StoredScript.FolderID != nil {
		t.Fatalf("implicit client name/folder changed candidate: %+v", result.StoredScript)
	}
	action := result.StoredScript.Script.Actions[0]
	if action.Headers["Authorization"] != "{{secret.CUSTOM_AUTH}}" ||
		action.HeaderList[0].Value != "{{secret.CUSTOM_AUTH}}" {
		t.Fatalf("explicit protected binding was not applied: %+v", action)
	}
	if !strings.Contains(action.Target, "user_id={{var.user_id}}") ||
		len(result.StoredScript.Script.DataSources) != 1 ||
		len(result.StoredScript.Script.Parameters) != 1 {
		t.Fatalf("explicit protected parameterization was not applied: %+v", result.StoredScript.Script)
	}
}

func TestLegacyImportCommitRunsSharedReadinessFinalizer(t *testing.T) {
	server := NewServer(ServerConfig{})
	body, _ := json.Marshal(map[string]any{
		"script": dsl.Script{
			ID:        "legacy-readiness-script",
			Name:      "Legacy readiness",
			Version:   "1.0.0",
			Protocol:  "http",
			ProjectID: "proj-readiness",
			Config:    dsl.ScriptConfig{BaseURL: "https://api.example.test"},
			Actions: []dsl.Action{{
				Name: "Missing variable", Type: "request", Method: "GET",
				Target: "/accounts/{{var.account_id}}",
			}},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/postman/save", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("commit status = %d body=%s", resp.Code, resp.Body.String())
	}
	var result struct {
		StoredScript StoredScript    `json:"stored_script"`
		Preflight    ReplayPreflight `json:"preflight"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	action := result.StoredScript.Script.Actions[0]
	if action.Metadata["execution_blocker"] != "import_missing_variables" {
		t.Fatalf("missing-variable gate = %#v", action.Metadata)
	}
	if result.Preflight.Valid {
		t.Fatalf("blocked draft reported valid preflight: %+v", result.Preflight)
	}
}

func TestImportCommitReplaceFinalizesMergedScriptReadiness(t *testing.T) {
	for _, dryRun := range []bool{true, false} {
		name := "commit"
		if dryRun {
			name = "dry_run"
		}
		t.Run(name, func(t *testing.T) {
			server := NewServer(ServerConfig{})
			existing := openAPICommitTestScript("Existing target", "proj-merged-readiness", []dsl.Action{{
				Name:   "Preserved manual action",
				Type:   "request",
				Method: http.MethodGet,
				Target: "/accounts/{{var.account_id}}",
			}})
			stored := server.runner.StoreScript(&existing)
			if stored == nil {
				t.Fatal("failed to seed existing script")
			}

			payload, err := json.Marshal(map[string]any{
				"mode":             "replace",
				"replace_mode":     "merge_by_operation_id",
				"target_script_id": stored.ID,
				"dry_run":          dryRun,
				"script": openAPICommitTestScript(
					"Fresh import",
					existing.ProjectID,
					[]dsl.Action{openAPICommitTestAction("ListOrders", http.MethodGet, "/orders", "GET /orders")},
				),
			})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/openapi/commit", bytes.NewReader(payload))
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			server.mux.ServeHTTP(resp, req)
			if resp.Code != http.StatusOK {
				t.Fatalf("replace status = %d body=%s", resp.Code, resp.Body.String())
			}

			var result struct {
				StoredScript *StoredScript   `json:"stored_script"`
				Preflight    ReplayPreflight `json:"preflight"`
			}
			if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.Preflight.Valid {
				t.Fatalf("merged script reported valid preflight: %+v", result.Preflight)
			}
			if len(result.Preflight.MissingVariables) != 1 || result.Preflight.MissingVariables[0] != "account_id" {
				t.Fatalf("merged preflight missing variables = %#v, want account_id", result.Preflight.MissingVariables)
			}

			persisted, ok := server.runner.GetScript(stored.ID)
			if !ok || persisted == nil || persisted.Script == nil {
				t.Fatal("target script disappeared")
			}
			if dryRun {
				if persisted.Name != existing.Name || len(persisted.Script.Actions) != 1 {
					t.Fatalf("dry run mutated target: %+v", persisted)
				}
				return
			}
			var manual *dsl.Action
			for index := range persisted.Script.Actions {
				if persisted.Script.Actions[index].Name == "Preserved manual action" {
					manual = &persisted.Script.Actions[index]
					break
				}
			}
			if manual == nil {
				t.Fatalf("preserved manual action missing from stored merge: %+v", persisted.Script.Actions)
			}
			if manual.Metadata["execution_blocker"] != "import_missing_variables" {
				t.Fatalf("stored manual action gate = %#v, want import_missing_variables", manual.Metadata)
			}
			if result.StoredScript == nil || result.StoredScript.Script == nil {
				t.Fatal("commit response omitted stored merged script")
			}
		})
	}
}

func TestImportCommitReplaceRejectsInvalidMergedScriptBeforeResponseOrPersistence(t *testing.T) {
	spec := importCommitSpec{
		sourceProfile:       "test",
		auditProfile:        "test",
		displayName:         "Test",
		allowedReplaceModes: []string{"full", "merge"},
		actionKey: func(action dsl.Action) string {
			return action.Name
		},
		diff: func(*dsl.Script, *dsl.Script, string, func(dsl.Action) string) openAPIImportDiff {
			return openAPIImportDiff{}
		},
		merge: func(_ *dsl.Script, imported *dsl.Script, _ func(dsl.Action) string) *dsl.Script {
			merged := *imported
			merged.Actions = append(append([]dsl.Action(nil), imported.Actions...), dsl.Action{
				Name:   "Invalid preserved action",
				Type:   "request",
				Method: http.MethodGet,
			})
			return &merged
		},
		mapCompatibleResponse: identityImportCommitResponse,
	}

	for _, dryRun := range []bool{true, false} {
		name := "commit"
		if dryRun {
			name = "dry_run"
		}
		t.Run(name, func(t *testing.T) {
			server := NewServer(ServerConfig{})
			existing := &dsl.Script{
				ID:        "invalid-merge-target-" + name,
				Name:      "Existing target",
				Version:   "1.0.0",
				Protocol:  "http",
				ProjectID: "proj-invalid-merge",
				Config:    dsl.ScriptConfig{BaseURL: "https://api.example.test"},
				Actions: []dsl.Action{{
					Name: "Existing request", Type: "request", Method: http.MethodGet, Target: "/existing",
				}},
			}
			stored := server.runner.StoreScript(existing)
			if stored == nil {
				t.Fatal("failed to seed existing script")
			}
			imported := &dsl.Script{
				Name:      "Incoming replacement",
				Version:   "1.0.0",
				Protocol:  "http",
				ProjectID: existing.ProjectID,
				Config:    dsl.ScriptConfig{BaseURL: "https://api.example.test"},
				Actions: []dsl.Action{{
					Name: "Imported request", Type: "request", Method: http.MethodGet, Target: "/imported",
				}},
			}
			payload, err := json.Marshal(map[string]any{
				"mode":             "replace",
				"replace_mode":     "merge",
				"target_script_id": stored.ID,
				"dry_run":          dryRun,
				"script":           imported,
			})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/test/import/commit", bytes.NewReader(payload))
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			server.handleImportCommit(resp, req, spec)
			if resp.Code != http.StatusBadRequest {
				t.Fatalf("invalid merged status = %d, want 400 body=%s", resp.Code, resp.Body.String())
			}
			if !strings.Contains(resp.Body.String(), "failed to finalize imported script") {
				t.Fatalf("invalid merged error did not come from shared finalizer: %s", resp.Body.String())
			}
			unchanged, ok := server.runner.GetScript(stored.ID)
			if !ok || unchanged == nil || unchanged.Script == nil {
				t.Fatal("target script disappeared")
			}
			if unchanged.Name != existing.Name ||
				len(unchanged.Script.Actions) != 1 ||
				unchanged.Script.Actions[0].Target != "/existing" {
				t.Fatalf("invalid merged result persisted: %+v", unchanged)
			}
		})
	}
}

func TestProtectedImportCandidateClaimHeartbeatPreventsStaleRecovery(t *testing.T) {
	artifactDir := t.TempDir()
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{
		Key:   bytes.Repeat([]byte{0x29}, 32),
		KeyID: "candidate-heartbeat-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{
		BrunoArtifactDir:     artifactDir,
		BrunoArtifactWrapper: wrapper,
	})
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	candidate := &dsl.Script{
		Name: "Heartbeat", Version: "1", Protocol: "http", ProjectID: "proj-heartbeat",
		Config:  dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		Actions: []dsl.Action{{Name: "Get", Type: "request", Method: "GET", Target: "/"}},
	}
	sessionID, _, err := server.registerImportCandidateSession(request, "postman", candidate)
	if err != nil {
		t.Fatal(err)
	}
	_, claim, err := server.claimImportCandidateSession(request, "postman", sessionID, candidate.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	claimPath := filepath.Join(artifactDir, ".sessions", sessionID+".claim.json")
	stale := time.Now().UTC().Add(-time.Minute)
	if err := os.Chtimes(claimPath, stale, stale); err != nil {
		t.Fatal(err)
	}
	lease := server.startImportCandidateClaimLease(claim)
	if err := lease.assertOwned(); err != nil {
		t.Fatalf("assertOwned: %v", err)
	}
	if _, _, err := server.claimImportCandidateSession(
		request, "postman", sessionID, candidate.ProjectID,
	); !errors.Is(err, importsession.ErrSessionClaimed) {
		t.Fatalf("concurrent claim error = %v, want ErrSessionClaimed", err)
	}
	lease.stop()
	server.releaseImportCandidateClaim(claim)
}
