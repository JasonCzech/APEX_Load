package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/importartifact"
	"github.com/jasonczech/apexload/pkg/secretstore"
)

func TestSampleAndHARProtectedCredentialEndpointsUseDurableSourceSessions(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		body     func(projectID, canary string) []byte
	}{
		{
			name:     "sample",
			endpoint: "/api/v1/scripts/import/sample",
			body: func(projectID, canary string) []byte {
				payload, _ := json.Marshal(map[string]any{
					"name":       "Protected Sample",
					"project_id": projectID,
					"format":     "rest_json",
					"content": fmt.Sprintf(
						`{"request":{"method":"POST","url":"https://api.example.test/login","body":{"password":%q}}}`,
						canary,
					),
				})
				return payload
			},
		},
		{
			name:     "har",
			endpoint: "/api/v1/scripts/import/har",
			body: func(projectID, canary string) []byte {
				payload, _ := json.Marshal(map[string]any{
					"name":       "Protected HAR",
					"project_id": projectID,
					"content": `{
					  "log":{"entries":[{
					    "request":{
					      "method":"POST",
					      "url":"https://api.example.test/login",
					      "headers":[{"name":"Content-Type","value":"application/json"}],
					      "postData":{"mimeType":"application/json","text":"{\"password\":\"` + canary + `\"}"}
					    },
					    "response":{"status":204,"headers":[],"content":{"text":""}}
					  }]}
					}`,
				})
				return payload
			},
		},
		{
			name:     "har_salesforce_branch",
			endpoint: "/api/v1/scripts/import/har",
			body:     protectedSalesforceHARImportBody,
		},
		{
			name:     "salesforce",
			endpoint: "/api/v1/scripts/import/salesforce",
			body:     protectedSalesforceHARImportBody,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			server, records, _, projectID := newProjectSecretAPIServer(t)
			settings := server.runner.GetSystemSettings()
			settings.ScriptImports.AllowSourceSecretMigration = true
			if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
				t.Fatal(err)
			}
			server.protectedSourceSessions = nil

			unavailableCanary := "protected-" + test.name + "-unavailable-canary-8f91d2"
			unavailable := invokeProtectedCredentialImport(
				t,
				server,
				test.endpoint,
				test.body(projectID, unavailableCanary),
			)
			assertProtectedCredentialImportResponse(t, unavailable, unavailableCanary, true)

			wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{
				Key:   bytes.Repeat([]byte{0x7c}, 32),
				KeyID: "sample-har-protected-source-session",
			})
			if err != nil {
				t.Fatal(err)
			}
			server.protectedSourceSessions = &secretstore.ProtectedSourceSessionStore{
				Records: records,
				Wrapper: wrapper,
			}

			availableCanary := "protected-" + test.name + "-available-canary-6a42c7"
			available := invokeProtectedCredentialImport(
				t,
				server,
				test.endpoint,
				test.body(projectID, availableCanary),
			)
			assertProtectedCredentialImportResponse(t, available, availableCanary, true)
		})
	}
}

func protectedSalesforceHARImportBody(projectID, canary string) []byte {
	payload, _ := json.Marshal(map[string]any{
		"name":       "Protected Salesforce HAR",
		"project_id": projectID,
		"content": `{
		  "log":{"entries":[{
		    "request":{
		      "method":"POST",
		      "url":"https://contract.sandbox.my.salesforce.com/services/data/v61.0/sobjects/Account",
		      "headers":[{"name":"Content-Type","value":"application/json"}],
		      "postData":{"mimeType":"application/json","text":"{\"name\":\"Contract\",\"password\":\"` + canary + `\"}"}
		    },
		    "response":{"status":201,"headers":[],"content":{"mimeType":"application/json","text":"{\"id\":\"001contract\"}"}}
		  }]}
		}`,
	})
	return payload
}

type protectedCredentialImportResponse struct {
	Body             []byte
	Script           dsl.Script
	Requirements     []SecretBindingRequirement
	SessionID        string
	MigrationAllowed bool
}

func invokeProtectedCredentialImport(
	t *testing.T,
	server *Server,
	endpoint string,
	body []byte,
) protectedCredentialImportResponse {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	clear(body)
	if response.Code != http.StatusOK {
		t.Fatalf("%s status = %d, want %d body=%s", endpoint, response.Code, http.StatusOK, response.Body.String())
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("%s Cache-Control = %q, want no-store", endpoint, got)
	}
	if got := response.Header().Get("Pragma"); got != "no-cache" {
		t.Fatalf("%s Pragma = %q, want no-cache", endpoint, got)
	}
	var payload struct {
		Script           dsl.Script                 `json:"script"`
		Requirements     []SecretBindingRequirement `json:"secret_binding_requirements"`
		SessionID        string                     `json:"source_secret_session_id"`
		MigrationAllowed bool                       `json:"source_secret_migration_allowed"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	return protectedCredentialImportResponse{
		Body:             append([]byte(nil), response.Body.Bytes()...),
		Script:           payload.Script,
		Requirements:     payload.Requirements,
		SessionID:        payload.SessionID,
		MigrationAllowed: payload.MigrationAllowed,
	}
}

func assertProtectedCredentialImportResponse(
	t *testing.T,
	response protectedCredentialImportResponse,
	canary string,
	wantAvailable bool,
) {
	t.Helper()
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(response.Body, &envelope); err != nil {
		t.Fatal(err)
	}
	delete(envelope, "script")
	delete(envelope, "scripts")
	reviewEvidence, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(reviewEvidence, []byte(canary)) {
		t.Fatalf("import review evidence disclosed protected canary: %s", strings.ReplaceAll(string(reviewEvidence), canary, "<PROTECTED>"))
	}
	scriptJSON, err := json.Marshal(response.Script)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(scriptJSON, []byte(canary)) || !bytes.Contains(scriptJSON, []byte(maskedScriptSecret)) {
		t.Fatal("opaque import script disclosed or failed to mask the source credential")
	}
	if len(response.Requirements) != 1 {
		t.Fatalf("requirements = %#v, want one protected credential", response.Requirements)
	}
	requirement := response.Requirements[0]
	if requirement.ID == "" || requirement.Path == "" || requirement.SuggestedName == "" ||
		!strings.Contains(requirement.Replacement, "{{secret.") {
		t.Fatalf("protected requirement omitted safe binding metadata: %#v", requirement)
	}
	if requirement.SourceValueAvailable != wantAvailable {
		t.Fatalf("source_value_available = %t, want %t: %#v", requirement.SourceValueAvailable, wantAvailable, requirement)
	}
	if wantAvailable {
		if strings.TrimSpace(response.SessionID) == "" || !response.MigrationAllowed {
			t.Fatalf("protected source session = %q migration_allowed=%t, want an enabled session", response.SessionID, response.MigrationAllowed)
		}
		return
	}
	if response.SessionID != "" || response.MigrationAllowed {
		t.Fatalf("migration was advertised without a durable protected source session: id=%q allowed=%t", response.SessionID, response.MigrationAllowed)
	}
}

func TestScriptImportResponseClearsProtectedRequirementValues(t *testing.T) {
	const canary = "response-zeroing-canary-f2b781"
	script := &dsl.Script{
		Name:     "Protected response",
		Version:  "1.0.0",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		Actions: []dsl.Action{{
			Name:   "Login",
			Type:   "request",
			Method: http.MethodPost,
			Target: "/login",
			Body:   `{"password":"{{secret.PASSWORD}}"}`,
		}},
	}
	requirements := []credentialpolicy.Requirement{{
		ID:            "protected-response-password",
		Value:         canary,
		SuggestedName: "PASSWORD",
		Replacement:   "{{secret.PASSWORD}}",
	}}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/sample", nil)
	response := (*Server)(nil).newScriptImportResponseWithSourceSecrets(request, scriptImportResponseOptions{
		Script:                script,
		ProtectedRequirements: requirements,
	})

	if requirements[0].Value != "" {
		t.Fatal("protected requirement source value was not cleared after response construction")
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(canary)) {
		t.Fatal("response JSON disclosed the cleared protected requirement")
	}
}

func TestScriptImportResponseCombinesLegacyAndProtectedSourceValues(t *testing.T) {
	const (
		legacyCanary    = "legacy-source-canary-19fd82"
		protectedCanary = "protected-source-canary-b5083a"
	)
	server, records, _, projectID := newProjectSecretAPIServer(t)
	settings := server.runner.GetSystemSettings()
	settings.ScriptImports.AllowSourceSecretMigration = true
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatal(err)
	}
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{
		Key:   bytes.Repeat([]byte{0x2d}, 32),
		KeyID: "combined-import-source-session",
	})
	if err != nil {
		t.Fatal(err)
	}
	server.protectedSourceSessions = &secretstore.ProtectedSourceSessionStore{
		Records: records,
		Wrapper: wrapper,
	}

	script := &dsl.Script{
		Name:      "Combined credentials",
		Version:   "1.0.0",
		Protocol:  "http",
		ProjectID: projectID,
		Config:    dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		Actions: []dsl.Action{{
			Name:    "Login",
			Type:    "request",
			Method:  http.MethodPost,
			Target:  "/login",
			Headers: map[string]string{"Authorization": "Bearer " + legacyCanary},
			Body:    `{"password":"{{secret.PASSWORD}}"}`,
		}},
	}
	protected := []credentialpolicy.Requirement{{
		ID:            "combined-protected-password",
		Value:         protectedCanary,
		SuggestedName: "PASSWORD",
		Replacement:   "{{secret.PASSWORD}}",
	}}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/sample", nil)
	response := server.newScriptImportResponseWithSourceSecrets(request, scriptImportResponseOptions{
		Script:                script,
		ProtectedRequirements: protected,
	})

	if protected[0].Value != "" {
		t.Fatal("protected source value was not cleared")
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(legacyCanary)) || !bytes.Contains(encoded, []byte(maskedScriptSecret)) {
		t.Fatal("opaque import response disclosed or failed to mask its already-authored credential")
	}
	if bytes.Contains(encoded, []byte(protectedCanary)) {
		t.Fatalf("protected requirement payload disclosed %q", protectedCanary)
	}
	requirements, _ := response["secret_binding_requirements"].([]SecretBindingRequirement)
	if len(requirements) != 2 {
		t.Fatalf("requirements = %#v, want legacy and protected requirements", requirements)
	}
	for _, requirement := range requirements {
		if !requirement.SourceValueAvailable {
			t.Fatalf("requirement %q was not included in the combined source session: %#v", requirement.ID, requirement)
		}
	}
	if sessionID, _ := response["source_secret_session_id"].(string); strings.TrimSpace(sessionID) == "" {
		t.Fatal("combined credentials did not produce one protected source session")
	}
}
