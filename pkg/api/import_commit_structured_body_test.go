package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/importartifact"
)

func TestSharedImportCommitPreservesStructuredBodyCredentialsDurablyAndMasksResponse(t *testing.T) {
	const canary = "structured-commit-canary-81d2"
	const boundary = "commit-structured-boundary"
	tests := []struct {
		name         string
		contentType  string
		body         string
		bodyEncoding string
		stable       []string
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
		{
			name:         "base64 json",
			contentType:  "application/json",
			body:         "eyJwYXNzd29yZCI6InN0cnVjdHVyZWQtY29tbWl0LWNhbmFyeS04MWQyIiwib3JkaW5hcnkiOiJrZWVwIn0=",
			bodyEncoding: "base64",
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
					Headers:      map[string]string{"Content-Type": test.contentType},
					Body:         test.body,
					BodyEncoding: test.bodyEncoding,
				}},
			}
			result, resp := commitStructuredBodyScript(t, server, "/api/v1/scripts/import/postman/save", script)
			if resp.Code != http.StatusCreated {
				t.Fatalf("commit status = %d body=%s", resp.Code, resp.Body.String())
			}
			got := result.StoredScript.Script.Actions[0].Body
			if strings.Contains(got, canary) {
				t.Fatalf("stored-script response exposed structured credential: %s", got)
			}
			disclosureBody := got
			if test.bodyEncoding == "base64" {
				decoded, err := base64.StdEncoding.DecodeString(got)
				if err != nil {
					t.Fatalf("decode stored-script response body: %v", err)
				}
				disclosureBody = string(decoded)
			}
			if !strings.Contains(disclosureBody, maskedScriptSecret) {
				t.Fatalf("stored-script response did not mask structured credential: %s", got)
			}
			for _, stable := range test.stable {
				if !strings.Contains(got, stable) {
					t.Fatalf("nonsensitive bytes %q changed: %s", stable, got)
				}
			}
			persisted, ok := server.runner.GetScript(scriptID)
			if !ok || persisted == nil || persisted.Script == nil ||
				persisted.Script.Actions[0].Body != test.body {
				t.Fatalf("durable structured body did not retain exact imported bytes: %#v", persisted)
			}
			if !isCredentialPreservingImport(persisted.Script) {
				t.Fatal("durable imported script lost its server-owned preservation policy")
			}
			values, err := engine.ImportedCredentialValues(persisted.Script)
			if err != nil {
				t.Fatalf("runtime credential values: %v", err)
			}
			if !stringSliceContains(values, canary) {
				t.Fatalf("runtime redaction values = %#v, want canary", values)
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

func TestSharedImportCommitRetainsUnsafeStructuredCredentialsWithoutDisclosure(t *testing.T) {
	const canary = "unsafe-structured-canary-019f"
	tests := []struct {
		name        string
		contentType string
		body        string
		code        string
		blocked     bool
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
			blocked:     true,
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
			result, resp := commitStructuredBodyScript(t, server, "/api/v1/scripts/import/postman/save", script)
			if resp.Code != http.StatusCreated {
				t.Fatalf("commit status = %d, want 201 body=%s", resp.Code, resp.Body.String())
			}
			if strings.Contains(resp.Body.String(), canary) {
				t.Fatalf("commit response disclosed source credential: %s", resp.Body.String())
			}
			if got := result.StoredScript.Script.Actions[0].Body; got != maskedScriptSecret {
				t.Fatalf("disclosure body = %q, want whole-body mask", got)
			}
			persisted, ok := server.runner.GetScript(script.ID)
			if !ok || persisted == nil || persisted.Script == nil {
				t.Fatal("credential-bearing structured import was not persisted")
			}
			action := persisted.Script.Actions[0]
			if action.Body != test.body {
				t.Fatalf("durable body changed: %q", action.Body)
			}
			if !isCredentialPreservingImport(persisted.Script) {
				t.Fatal("durable import lost its credential-preservation policy")
			}
			blocker, _ := action.Metadata["execution_blocker"].(string)
			if test.blocked {
				if blocker != "import_review_required" {
					t.Fatalf("malformed structured body blocker = %q", blocker)
				}
				encoded, err := json.Marshal(action.Metadata)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Contains(encoded, []byte(test.code)) {
					t.Fatalf("stable review code %q missing: %s", test.code, encoded)
				}
			} else if blocker != "" {
				t.Fatalf("exact runnable import gained disclosure-only blocker %q", blocker)
			}
			if !test.blocked && strings.Contains(test.body, canary) {
				values, err := engine.ImportedCredentialValues(persisted.Script)
				if err != nil {
					t.Fatalf("runtime credential values: %v", err)
				}
				if !stringSliceContains(values, canary) {
					t.Fatalf("runtime redaction values = %#v, want canary", values)
				}
			}
		})
	}
}

func TestSharedImportCommitPreservesXMLMixedCredentialContentWithRuntimeLocator(t *testing.T) {
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
	if strings.Contains(action.Body, canary) {
		t.Fatalf("mixed XML response was not disclosure-safe: %#v", action)
	}
	if !strings.Contains(action.Body, "<ordinary>keep</ordinary>") {
		t.Fatalf("nonsensitive XML bytes changed: %s", action.Body)
	}
	persisted, ok := server.runner.GetScript(script.ID)
	if !ok || persisted == nil || persisted.Script == nil {
		t.Fatal("mixed XML import was not persisted")
	}
	if got := persisted.Script.Actions[0].Body; got != body {
		t.Fatalf("durable mixed XML changed: %s", got)
	}
	if blocker, _ := persisted.Script.Actions[0].Metadata["execution_blocker"].(string); blocker != "" {
		t.Fatalf("credential retention alone blocked execution: %q", blocker)
	}
	values, err := engine.ImportedCredentialValues(persisted.Script)
	if err != nil {
		t.Fatalf("runtime credential values: %v", err)
	}
	if !stringSliceContains(values, canary) {
		t.Fatalf("runtime redaction values = %#v, want mixed XML canary", values)
	}
}

func TestProtectedImportCommitPreservesNonJSONBodyBytes(t *testing.T) {
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{
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

func TestGenericMaskWholeBodyRedactsUnsafeMultipartNestedCredential(t *testing.T) {
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
	projected := maskScriptSecrets(script)
	if projected == nil {
		t.Fatal("unsafe nested multipart could not be disclosure-projected")
	}
	if got := projected.Actions[0].Body; got != maskedScriptSecret {
		t.Fatalf("unsafe nested multipart disclosure body = %q", got)
	}
	if blocker, _ := projected.Actions[0].Metadata["execution_blocker"].(string); blocker != "import_review_required" {
		t.Fatalf("disclosure projection blocker = %q, want fail-closed response gate", blocker)
	}
	if projected.Actions[0].Metadata[scriptProjectionResponseGateMetadataKey] == nil {
		t.Fatal("disclosure projection omitted its response-only gate marker")
	}
	if !strings.Contains(script.Actions[0].Body, `{"password":7}`) {
		t.Fatal("disclosure projection mutated the executable source script")
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
