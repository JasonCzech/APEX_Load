package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestWriteScriptImportResponseDisablesCaching(t *testing.T) {
	recorder := httptest.NewRecorder()

	writeScriptImportResponse(recorder, http.StatusAccepted, map[string]any{
		"script": map[string]any{"name": "credential-bearing draft"},
	})

	response := recorder.Result()
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusAccepted)
	}
	if got := response.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	if got := response.Header.Get("Pragma"); got != "no-cache" {
		t.Fatalf("Pragma = %q, want no-cache", got)
	}
	var payload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload["script"] == nil {
		t.Fatalf("response payload = %#v, want script", payload)
	}
}

func TestWriteScriptImportResponseWithSourceSecretsDisablesCaching(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/sample", nil)

	(*Server)(nil).writeScriptImportResponseWithSourceSecrets(
		recorder,
		request,
		http.StatusOK,
		scriptImportResponseOptions{
			Warnings:             []string{"review imported credentials"},
			SkipDraftAttestation: true,
		},
	)

	response := recorder.Result()
	defer response.Body.Close()
	if got := response.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	if got := response.Header.Get("Pragma"); got != "no-cache" {
		t.Fatalf("Pragma = %q, want no-cache", got)
	}
}

func TestImportResponseMarksPreservationPerCredentialOccurrence(t *testing.T) {
	const importedPassword = "operator-imported-password"
	script := &dsl.Script{
		Name: "mixed credentials", Version: "1", Protocol: "http",
		Config: dsl.ScriptConfig{HTTP: dsl.HTTPConfig{
			Auth:  dsl.AuthConfig{Type: "basic", Username: "operator", Password: importedPassword},
			Proxy: dsl.ProxyConfig{URL: "http://proxy.example", Password: maskedScriptSecret},
		}},
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source:    "postman",
			Preserved: map[string]any{},
		}},
		Actions: []dsl.Action{{
			Name: "request", Type: "request", Method: "GET", Target: "/",
		}},
	}
	response := newScriptImportResponse(scriptImportResponseOptions{
		Script:  script,
		Scripts: []*dsl.Script{script},
	})
	requirements, ok := response["secret_binding_requirements"].([]SecretBindingRequirement)
	if !ok || len(requirements) != 2 {
		t.Fatalf("requirements = %#v, want auth and proxy occurrences", response["secret_binding_requirements"])
	}
	states := make(map[string]bool, len(requirements))
	for _, requirement := range requirements {
		states[requirement.Path] = requirement.DestinationValuePreserved
	}
	if !states["/config/http/auth/password"] {
		t.Fatalf("auth password state = %#v, want source value preserved", states)
	}
	if !states["/config/http/proxy/password"] {
		t.Fatalf("proxy password state = %#v, exact authored value must remain preserved", states)
	}
}
