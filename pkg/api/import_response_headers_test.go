package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
	byPath := make(map[string]SecretBindingRequirement, len(requirements))
	for _, requirement := range requirements {
		byPath[requirement.Path] = requirement
	}
	if !byPath["/config/http/auth/password"].DestinationValuePreserved {
		t.Fatalf("auth password state = %#v, want source value preserved", byPath)
	}
	proxy := byPath["/config/http/proxy/password"]
	if proxy.DestinationValuePreserved ||
		proxy.PreservationUnavailableReason != "redacted_source_value" {
		t.Fatalf("proxy password state = %#v, want unavailable redacted source", proxy)
	}
}

func TestImportResponseDescribesOrderedSensitiveHeaderByName(t *testing.T) {
	script := &dsl.Script{
		Name: "descriptive header", Version: "1", Protocol: "http",
		Config: dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{
			Name: "Inquire account profile", Type: "request", Method: http.MethodGet, Target: "/profile",
			HeaderList: dsl.HeaderList{{Name: "X-CDF-Auth-Token", Value: maskedScriptSecret}},
		}},
	}
	response := newScriptImportResponse(scriptImportResponseOptions{
		Script: script, Scripts: []*dsl.Script{script},
	})
	requirements, ok := response["secret_binding_requirements"].([]SecretBindingRequirement)
	if !ok || len(requirements) != 1 {
		t.Fatalf("requirements = %#v, want one ordered-header credential", response["secret_binding_requirements"])
	}
	requirement := requirements[0]
	if requirement.Label != "X-CDF-Auth-Token request header" {
		t.Fatalf("label = %q", requirement.Label)
	}
	for _, want := range []string{"X-CDF-Auth-Token", "Inquire account profile", "when this request runs"} {
		if !strings.Contains(requirement.Context, want) {
			t.Fatalf("context = %q, want %q", requirement.Context, want)
		}
	}
	if requirement.Path != "/actions/0/header_list/0/value" ||
		len(requirement.AffectedPaths) != 1 || requirement.AffectedPaths[0] != requirement.Path {
		t.Fatalf("location metadata = %#v", requirement)
	}
	if strings.Contains(requirement.Context, maskedScriptSecret) || strings.Contains(requirement.Label, maskedScriptSecret) {
		t.Fatalf("presentation disclosed placeholder: %#v", requirement)
	}
}
