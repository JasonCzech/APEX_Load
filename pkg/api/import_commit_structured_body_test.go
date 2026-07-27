package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/brunoartifact"
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestSharedImportCommitProjectsStructuredBodyKinds(t *testing.T) {
	const canary = "structured-commit-canary-81d2"
	const boundary = "commit-structured-boundary"
	tests := []struct {
		name        string
		contentType string
		body        string
		stable      []string
	}{
		{
			name:        "xml",
			contentType: "application/xml",
			body:        `<root xmlns:a='urn:a' ordinary="keep"><a:password>` + canary + `</a:password></root>`,
			stable:      []string{`xmlns:a='urn:a'`, `ordinary="keep"`},
		},
		{
			name:        "form",
			contentType: "application/x-www-form-urlencoded",
			body:        "ordinary=a%2Fb%2Bc&password=" + canary + "&duplicate=one&duplicate=two",
			stable:      []string{"ordinary=a%2Fb%2Bc", "duplicate=one&duplicate=two"},
		},
		{
			name:        "multipart nested json",
			contentType: "multipart/form-data; boundary=" + boundary,
			body: "--" + boundary + "\r\n" +
				"Content-Disposition: form-data; name=\"payload\"\r\n" +
				"Content-Type: application/json\r\n" +
				"X-Trace: keep  \r\n\r\n" +
				"{\n  \"password\": \"" + canary + "\",\n  \"count\": 1.00e+2\n}\r\n" +
				"--" + boundary + "--\r\n",
			stable: []string{
				"Content-Disposition: form-data; name=\"payload\"\r\n",
				"X-Trace: keep  \r\n",
				`"count": 1.00e+2`,
				"--" + boundary + "--\r\n",
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			server := NewServer(ServerConfig{})
			scriptID := "structured-" + strings.ReplaceAll(test.name, " ", "-")
			script := &dsl.Script{
				ID:        scriptID,
				Name:      "Structured " + test.name,
				Version:   "1.0.0",
				Protocol:  "http",
				ProjectID: "proj-structured",
				Config:    dsl.ScriptConfig{BaseURL: "https://api.example.test"},
				Actions: []dsl.Action{{
					Name: "Submit payload", Type: "request", Method: http.MethodPost, Target: "/submit",
					Headers: map[string]string{"Content-Type": test.contentType},
					Body:    test.body,
				}},
			}
			result, resp := commitStructuredBodyScript(t, server, "/api/v1/scripts/import/postman/save", script)
			if resp.Code != http.StatusCreated {
				t.Fatalf("commit status = %d body=%s", resp.Code, resp.Body.String())
			}
			got := result.StoredScript.Script.Actions[0].Body
			if strings.Contains(got, canary) {
				t.Fatalf("structured credential remained: %s", got)
			}
			if reference := governedReferenceInText(got); reference == "" {
				t.Fatalf("structured credential received no governed reference: %s", got)
			}
			for _, stable := range test.stable {
				if !strings.Contains(got, stable) {
					t.Fatalf("nonsensitive bytes %q changed: %s", stable, got)
				}
			}
			persisted, ok := server.runner.GetScript(scriptID)
			if !ok || persisted == nil || persisted.Script == nil ||
				persisted.Script.Actions[0].Body != got {
				t.Fatalf("persisted structured bytes differ from response: %#v", persisted)
			}
		})
	}
}

func TestSharedImportCommitPreservesRawAndFileSurfaces(t *testing.T) {
	const rawBody = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="
	server := NewServer(ServerConfig{})
	script := &dsl.Script{
		ID:        "raw-file-surfaces",
		Name:      "Raw and file surfaces",
		Version:   "1.0.0",
		Protocol:  "http",
		ProjectID: "proj-raw",
		Config:    dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		Actions: []dsl.Action{
			{
				Name: "Raw", Type: "request", Method: http.MethodPost, Target: "/raw",
				Headers: map[string]string{"Content-Type": "application/octet-stream"},
				Body:    rawBody,
			},
			{
				Name: "File", Type: "request", Method: http.MethodPost, Target: "/file",
				BodyFile: "{{file.upload}}",
			},
		},
	}
	result, resp := commitStructuredBodyScript(t, server, "/api/v1/scripts/import/openapi/commit", script)
	// body_file is a valid blocked draft at the API boundary; it must not be
	// rewritten or mistaken for inline credential material.
	if resp.Code != http.StatusBadRequest {
		// Existing DSL validation rejects API body_file uploads before a draft
		// can be saved. Verify the projection itself independently below.
		t.Fatalf("body_file commit status = %d, want 400 body=%s", resp.Code, resp.Body.String())
	}
	projected := maskScriptSecrets(script)
	if projected == nil {
		t.Fatal("raw/file script could not be projected")
	}
	if projected.Actions[0].Body != rawBody ||
		projected.Actions[1].BodyFile != "{{file.upload}}" {
		t.Fatalf("raw/file surfaces changed: %#v / response=%#v", projected.Actions, result)
	}
}

func TestSharedImportCommitRejectsUnsafeStructuredBodies(t *testing.T) {
	const canary = "unsafe-structured-canary-019f"
	tests := []struct {
		name        string
		contentType string
		body        string
		code        string
	}{
		{
			name:        "graphql inline literal",
			contentType: "application/graphql",
			body:        `mutation { login(password: "` + canary + `") }`,
			code:        "credential_graphql_inline_literal_unsupported",
		},
		{
			name:        "multipart nested typed json",
			contentType: "multipart/form-data; boundary=unsafe-boundary",
			body: "--unsafe-boundary\r\n" +
				"Content-Disposition: form-data; name=\"payload\"\r\n" +
				"Content-Type: application/json\r\n\r\n" +
				`{"password":7}` + "\r\n" +
				"--unsafe-boundary--\r\n",
			code: "credential_json_sensitive_non_string_unsupported",
		},
		{
			name:        "malformed sensitive form",
			contentType: "application/x-www-form-urlencoded",
			body:        "client_secret=%zz" + canary,
			code:        "credential_form_malformed",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			server := NewServer(ServerConfig{})
			script := &dsl.Script{
				ID:        "unsafe-" + strings.ReplaceAll(test.name, " ", "-"),
				Name:      "Unsafe " + test.name,
				Version:   "1.0.0",
				Protocol:  "http",
				ProjectID: "proj-unsafe",
				Config:    dsl.ScriptConfig{BaseURL: "https://api.example.test"},
				Actions: []dsl.Action{{
					Name: "Unsafe body", Type: "request", Method: http.MethodPost, Target: "/unsafe",
					Headers: map[string]string{"Content-Type": test.contentType},
					Body:    test.body,
				}},
			}
			_, resp := commitStructuredBodyScript(t, server, "/api/v1/scripts/import/postman/save", script)
			if resp.Code != http.StatusBadRequest {
				t.Fatalf("commit status = %d, want 400 body=%s", resp.Code, resp.Body.String())
			}
			if !strings.Contains(resp.Body.String(), test.code) {
				t.Fatalf("stable rejection code %q missing: %s", test.code, resp.Body.String())
			}
			if strings.Contains(resp.Body.String(), canary) {
				t.Fatalf("rejection disclosed source credential: %s", resp.Body.String())
			}
			if _, ok := server.runner.GetScript(script.ID); ok {
				t.Fatal("unsafe structured body was persisted")
			}
		})
	}
}

func TestSharedImportCommitBlocksXMLMixedCredentialContent(t *testing.T) {
	const canary = "xml-mixed-canary-bc73"
	body := `<root><password>prefix<token>` + canary + `</token>suffix</password><ordinary>keep</ordinary></root>`
	server := NewServer(ServerConfig{})
	script := &dsl.Script{
		ID:        "xml-mixed-blocked",
		Name:      "XML mixed blocked",
		Version:   "1.0.0",
		Protocol:  "http",
		ProjectID: "proj-xml-mixed",
		Config:    dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		Actions: []dsl.Action{{
			Name: "Mixed XML", Type: "request", Method: http.MethodPost, Target: "/xml",
			Headers: map[string]string{"Content-Type": "application/xml"},
			Body:    body,
		}},
	}
	result, resp := commitStructuredBodyScript(t, server, "/api/v1/scripts/import/openapi/commit", script)
	if resp.Code != http.StatusCreated {
		t.Fatalf("blocked draft status = %d body=%s", resp.Code, resp.Body.String())
	}
	action := result.StoredScript.Script.Actions[0]
	if strings.Contains(action.Body, canary) ||
		action.Metadata["execution_blocker"] != "import_review_required" {
		t.Fatalf("mixed XML was not disclosure-safe and blocked: %#v", action)
	}
	if !strings.Contains(action.Body, "<ordinary>keep</ordinary>") {
		t.Fatalf("nonsensitive XML bytes changed: %s", action.Body)
	}
}

func TestProtectedImportCommitPreservesNonJSONBodyBytes(t *testing.T) {
	wrapper, err := brunoartifact.NewLocalAESKeyWrapper(brunoartifact.LocalKeyOptions{
		Key:   bytes.Repeat([]byte{0x5d}, 32),
		KeyID: "protected-structured-wire-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{
		BrunoArtifactDir:     t.TempDir(),
		BrunoArtifactWrapper: wrapper,
	})
	tests := []struct {
		name        string
		contentType string
		body        string
	}{
		{
			name:        "xml",
			contentType: "application/xml",
			body:        `<root ordinary='keep'><password>{{secret.PROTECTED_XML_PASSWORD}}</password></root>`,
		},
		{
			name:        "raw",
			contentType: "application/octet-stream",
			body:        "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
		},
	}
	for _, profile := range []string{"openapi", "postman"} {
		path := "/api/v1/scripts/import/openapi/commit"
		if profile == "postman" {
			path = "/api/v1/scripts/import/postman/save"
		}
		for _, test := range tests {
			test := test
			t.Run(profile+"/"+test.name, func(t *testing.T) {
				request := httptest.NewRequest(http.MethodPost, "/", nil)
				candidate := &dsl.Script{
					ID:        "protected-" + profile + "-" + test.name,
					Name:      "Protected " + test.name,
					Version:   "1.0.0",
					Protocol:  "http",
					ProjectID: "proj-protected-structured",
					Config:    dsl.ScriptConfig{BaseURL: "https://api.example.test"},
					Actions: []dsl.Action{{
						Name: "Protected", Type: "request", Method: http.MethodPost, Target: "/body",
						Headers: map[string]string{"Content-Type": test.contentType},
						Body:    test.body,
					}},
				}
				sessionID, _, err := server.registerImportCandidateSession(request, profile, candidate)
				if err != nil {
					t.Fatal(err)
				}
				payload, err := json.Marshal(map[string]any{
					"import_session_id": sessionID,
					"project_id":        candidate.ProjectID,
					"script":            dsl.Script{Name: "ignored"},
				})
				if err != nil {
					t.Fatal(err)
				}
				req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(payload))
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
				if got := result.StoredScript.Script.Actions[0].Body; got != test.body {
					t.Fatalf("protected %s body bytes changed:\n got: %q\nwant: %q", test.name, got, test.body)
				}
			})
		}
	}
}

func TestGenericMaskAndStorePreserveGovernedStructuredBodyBytes(t *testing.T) {
	const boundary = "generic-store-boundary"
	bodies := []struct {
		contentType string
		body        string
	}{
		{
			contentType: "application/xml",
			body:        `<root ordinary='keep'><password>{{secret.XML_PASSWORD}}</password></root>`,
		},
		{
			contentType: "application/x-www-form-urlencoded",
			body:        "ordinary=a%2Fb&password={{secret.FORM_PASSWORD}}&duplicate=one&duplicate=two",
		},
		{
			contentType: "multipart/form-data; boundary=" + boundary,
			body: "--" + boundary + "\r\n" +
				"Content-Disposition: form-data; name=\"payload\"\r\n" +
				"Content-Type: application/json\r\n\r\n" +
				"{\n  \"password\": \"{{secret.MULTIPART_PASSWORD}}\",\n  \"count\": 1.00e+2\n}\r\n" +
				"--" + boundary + "--\r\n",
		},
		{
			contentType: "application/octet-stream",
			body:        "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
		},
	}
	script := &dsl.Script{
		ID:        "generic-structured-store",
		Name:      "Generic structured store",
		Version:   "1.0.0",
		Protocol:  "http",
		ProjectID: "proj-generic-structured",
		Config:    dsl.ScriptConfig{BaseURL: "https://api.example.test"},
	}
	for index, item := range bodies {
		script.Actions = append(script.Actions, dsl.Action{
			Name: "Body " + item.contentType, Type: "request", Method: http.MethodPost,
			Target:  "/body/" + string(rune('a'+index)),
			Headers: map[string]string{"Content-Type": item.contentType},
			Body:    item.body,
		})
	}
	projected := maskScriptSecrets(script)
	if projected == nil {
		t.Fatal("generic structured script could not be masked")
	}
	for index, item := range bodies {
		if projected.Actions[index].Body != item.body {
			t.Fatalf("masked body %d changed:\n got: %q\nwant: %q", index, projected.Actions[index].Body, item.body)
		}
	}
	server := NewServer(ServerConfig{})
	stored, err := server.runner.StoreScriptDurable(script)
	if err != nil {
		t.Fatalf("StoreScriptDurable: %v", err)
	}
	for index, item := range bodies {
		if stored.Script.Actions[index].Body != item.body {
			t.Fatalf("stored body %d changed:\n got: %q\nwant: %q", index, stored.Script.Actions[index].Body, item.body)
		}
	}
}

func TestGenericMaskRejectsUnsafeMultipartNestedCredential(t *testing.T) {
	const boundary = "generic-unsafe-boundary"
	script := &dsl.Script{
		Name: "Unsafe multipart", Version: "1.0.0", Protocol: "http",
		Actions: []dsl.Action{{
			Name: "Unsafe", Type: "request", Method: http.MethodPost, Target: "/unsafe",
			Headers: map[string]string{"Content-Type": "multipart/form-data; boundary=" + boundary},
			Body: "--" + boundary + "\r\n" +
				"Content-Disposition: form-data; name=\"payload\"\r\n" +
				"Content-Type: application/json\r\n\r\n" +
				`{"password":7}` + "\r\n" +
				"--" + boundary + "--\r\n",
		}},
	}
	if projected := maskScriptSecrets(script); projected != nil {
		t.Fatalf("unsafe nested multipart became runnable: %#v", projected.Actions[0])
	}
}

func commitStructuredBodyScript(
	t *testing.T,
	server *Server,
	path string,
	script *dsl.Script,
) (struct {
	StoredScript StoredScript    `json:"stored_script"`
	Preflight    ReplayPreflight `json:"preflight"`
}, *httptest.ResponseRecorder) {
	t.Helper()
	var result struct {
		StoredScript StoredScript    `json:"stored_script"`
		Preflight    ReplayPreflight `json:"preflight"`
	}
	payload, err := json.Marshal(map[string]any{"script": script})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Body.Len() > 0 && resp.Code >= 200 && resp.Code < 300 {
		if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
	}
	return result, resp
}

func governedReferenceInText(value string) string {
	start := strings.Index(value, "{{secret.")
	if start < 0 {
		return ""
	}
	end := strings.Index(value[start:], "}}")
	if end < 0 {
		return ""
	}
	reference := value[start : start+end+2]
	if _, ok := credential.ParseReference(reference); !ok {
		return ""
	}
	return reference
}
