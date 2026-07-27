package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/brunoartifact"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestOpenAPIAndPostmanCredentialValuesRoundTripOnlyInEncryptedCandidate(t *testing.T) {
	tests := []struct {
		name       string
		profile    string
		inspectURL string
		commitURL  string
		content    func(string) string
		protected  func(string) string
	}{
		{
			name:       "OpenAPI",
			profile:    "openapi",
			inspectURL: "/api/v1/scripts/import/openapi",
			commitURL:  "/api/v1/scripts/import/openapi/commit",
			content: func(value string) string {
				return fmt.Sprintf(`{
				  "openapi":"3.0.3",
				  "info":{"title":"Protected OpenAPI","version":"1"},
				  "servers":[{"url":"https://api.example.test"}],
				  "paths":{"/login":{"post":{
				    "operationId":"login",
				    "requestBody":{"content":{"application/json":{"example":{"password":%q}}}},
				    "responses":{"204":{"description":"ok"}}
				  }}}
				}`, value)
			},
		},
		{
			name:       "Postman",
			profile:    "postman",
			inspectURL: "/api/v1/scripts/import/postman",
			commitURL:  "/api/v1/scripts/import/postman/save",
			content: func(value string) string {
				return fmt.Sprintf(`{
				  "info":{"name":"Protected Postman"},
				  "item":[{
				    "name":"Login",
				    "request":{
				      "method":"POST",
				      "url":"https://api.example.test/login",
				      "header":[{"key":"Content-Type","value":"application/xml"}],
				      "body":{"mode":"raw","raw":"<login><password>%s</password></login>"}
				    }
				  }]
				}`, value)
			},
		},
		{
			name:       "Postman static variable",
			profile:    "postman",
			inspectURL: "/api/v1/scripts/import/postman",
			commitURL:  "/api/v1/scripts/import/postman/save",
			content: func(value string) string {
				return fmt.Sprintf(`{
				  "info":{"name":"Protected Postman Variable"},
				  "variable":[
				    {"key":"authValue","value":%q},
				    {"key":"visibleLabel","value":"ordinary"}
				  ],
				  "item":[{
				    "name":"List",
				    "request":{
				      "method":"GET",
				      "url":"https://api.example.test/items?label={{visibleLabel}}",
				      "header":[{"key":"Authorization","value":"Bearer {{authValue}}"}]
				    }
				  }]
				}`, value)
			},
			protected: func(value string) string {
				return "Bearer " + value
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			value := randomProtectedImportValue(t)
			protectedValue := value
			if test.protected != nil {
				protectedValue = test.protected(value)
			}
			artifactDir := t.TempDir()
			wrapper, err := brunoartifact.NewLocalAESKeyWrapper(brunoartifact.LocalKeyOptions{
				Key:   bytes.Repeat([]byte{0x5a}, 32),
				KeyID: "protected-import-credential-test",
			})
			if err != nil {
				t.Fatal(err)
			}
			server := NewServer(ServerConfig{
				BrunoArtifactDir:     artifactDir,
				BrunoArtifactWrapper: wrapper,
			})
			projectID := "project-" + test.profile
			requestBody, err := json.Marshal(map[string]any{
				"content":    test.content(value),
				"project_id": projectID,
			})
			if err != nil {
				t.Fatal(err)
			}
			inspectRequest := httptest.NewRequest(http.MethodPost, test.inspectURL, bytes.NewReader(requestBody))
			inspectRequest.Header.Set("Content-Type", "application/json")
			inspectResponse := httptest.NewRecorder()
			server.mux.ServeHTTP(inspectResponse, inspectRequest)
			clear(requestBody)
			if inspectResponse.Code != http.StatusOK {
				t.Fatalf("inspection status = %d", inspectResponse.Code)
			}
			if bytes.Contains(inspectResponse.Body.Bytes(), []byte(value)) {
				t.Fatalf(
					"inspection JSON disclosed a protected credential value: %s",
					strings.ReplaceAll(inspectResponse.Body.String(), value, "<PROTECTED>"),
				)
			}
			var inspection struct {
				ImportSessionID string                     `json:"import_session_id"`
				Script          dsl.Script                 `json:"script"`
				Requirements    []SecretBindingRequirement `json:"secret_binding_requirements"`
			}
			if err := json.Unmarshal(inspectResponse.Body.Bytes(), &inspection); err != nil {
				t.Fatal(err)
			}
			if strings.TrimSpace(inspection.ImportSessionID) == "" {
				t.Fatal("inspection did not create a protected import session")
			}
			if len(inspection.Requirements) != 1 {
				t.Fatalf("inspection requirements = %#v, want one protected credential", inspection.Requirements)
			}
			publicRequirement := inspection.Requirements[0]
			if publicRequirement.ID == "" || publicRequirement.Path == "" ||
				publicRequirement.SuggestedName == "" || publicRequirement.Replacement == "" {
				t.Fatalf("inspection requirement omitted safe binding metadata: %#v", publicRequirement)
			}
			if publicRequirement.SourceValueAvailable {
				t.Fatal("inspection claimed a source value was available without a durable protected source store")
			}
			assertScriptOmitsProtectedImportValue(t, &inspection.Script, value)
			assertProtectedImportArtifactsEncrypted(t, artifactDir, value)

			customSecretName := strings.ToUpper(test.profile) + "_PROTECTED_CUSTOM"
			applyBody, err := json.Marshal(map[string]any{
				"script": inspection.Script,
				"bindings": []map[string]string{{
					"requirement_id": publicRequirement.ID,
					"secret_name":    customSecretName,
				}},
			})
			if err != nil {
				t.Fatal(err)
			}
			applyRequest := httptest.NewRequest(
				http.MethodPost,
				"/api/v1/scripts/secret-bindings/apply",
				bytes.NewReader(applyBody),
			)
			applyRequest.Header.Set("Content-Type", "application/json")
			applyResponse := httptest.NewRecorder()
			server.mux.ServeHTTP(applyResponse, applyRequest)
			clear(applyBody)
			if applyResponse.Code != http.StatusOK {
				t.Fatalf("binding status = %d body=%s", applyResponse.Code, applyResponse.Body.String())
			}
			if bytes.Contains(applyResponse.Body.Bytes(), []byte(value)) {
				t.Fatal("binding JSON disclosed a protected credential value")
			}
			if !bytes.Contains(applyResponse.Body.Bytes(), []byte(`"secret_binding_requirements":[]`)) {
				t.Fatalf("binding JSON must encode an empty requirement list as []: %s", applyResponse.Body.String())
			}
			var applied struct {
				Script       dsl.Script                 `json:"script"`
				Requirements []SecretBindingRequirement `json:"secret_binding_requirements"`
			}
			if err := json.Unmarshal(applyResponse.Body.Bytes(), &applied); err != nil {
				t.Fatal(err)
			}
			if len(applied.Requirements) != 0 {
				t.Fatalf("binding requirements remain = %#v", applied.Requirements)
			}
			assertScriptReferencesProtectedImportName(t, &applied.Script, customSecretName)
			assertProtectedImportMetadataRemoved(t, &applied.Script)

			candidate, claim, err := server.claimProtectedImportCandidateSession(
				httptest.NewRequest(http.MethodPost, test.commitURL, nil),
				test.profile,
				inspection.ImportSessionID,
				projectID,
			)
			if err != nil {
				t.Fatal(err)
			}
			valueMatched := false
			identityPresent := false
			for _, requirement := range candidate.CredentialRequirements {
				if requirement.ID == publicRequirement.ID {
					identityPresent = true
				}
				if requirement.Value == protectedValue {
					valueMatched = true
				}
			}
			if !identityPresent || !valueMatched {
				t.Fatal("protected credential identity/value did not round-trip through the encrypted envelope")
			}
			assertScriptOmitsProtectedImportValue(t, candidate.Script, value)
			clearProtectedImportCandidate(candidate)
			server.releaseImportCandidateClaim(claim)

			commitBody, err := json.Marshal(map[string]any{
				"import_session_id": inspection.ImportSessionID,
				"project_id":        projectID,
				"script":            dsl.Script{},
				"secret_bindings": map[string]string{
					publicRequirement.ID: customSecretName,
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			commitRequest := httptest.NewRequest(http.MethodPost, test.commitURL, bytes.NewReader(commitBody))
			commitRequest.Header.Set("Content-Type", "application/json")
			commitResponse := httptest.NewRecorder()
			server.mux.ServeHTTP(commitResponse, commitRequest)
			clear(commitBody)
			if commitResponse.Code != http.StatusCreated {
				t.Fatalf("commit status = %d", commitResponse.Code)
			}
			if bytes.Contains(commitResponse.Body.Bytes(), []byte(value)) {
				t.Fatal("commit JSON disclosed a protected credential value")
			}
			var committed struct {
				Script       *dsl.Script  `json:"script"`
				StoredScript StoredScript `json:"stored_script"`
			}
			if err := json.Unmarshal(commitResponse.Body.Bytes(), &committed); err != nil {
				t.Fatal(err)
			}
			if committed.Script == nil || committed.StoredScript.Script == nil || committed.StoredScript.ID == "" {
				t.Fatal("commit response did not contain the stored script")
			}
			assertScriptOmitsProtectedImportValue(t, committed.Script, value)
			assertScriptOmitsProtectedImportValue(t, committed.StoredScript.Script, value)
			assertScriptReferencesProtectedImportName(t, committed.Script, customSecretName)
			assertScriptReferencesProtectedImportName(t, committed.StoredScript.Script, customSecretName)
			assertProtectedImportMetadataRemoved(t, committed.Script)
			assertProtectedImportMetadataRemoved(t, committed.StoredScript.Script)
			durable, ok := server.runner.GetScript(committed.StoredScript.ID)
			if !ok || durable == nil || durable.Script == nil {
				t.Fatal("committed script was not durably available")
			}
			assertScriptOmitsProtectedImportValue(t, durable.Script, value)
			assertScriptReferencesProtectedImportName(t, durable.Script, customSecretName)
			assertProtectedImportMetadataRemoved(t, durable.Script)
			assertProtectedImportArtifactsEncrypted(t, artifactDir, value)
		})
	}
}

func TestProtectedOpenAPICredentialValueMigratesWithoutDisclosure(t *testing.T) {
	_, records, managed, projectID := newProjectSecretAPIServer(t)
	wrapper, err := brunoartifact.NewLocalAESKeyWrapper(brunoartifact.LocalKeyOptions{
		Key:   bytes.Repeat([]byte{0x6b}, 32),
		KeyID: "protected-openapi-migration-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{
		Store:                records,
		ProjectSecretStore:   managed,
		BrunoArtifactDir:     t.TempDir(),
		BrunoArtifactWrapper: wrapper,
	})
	settings := server.runner.GetSystemSettings()
	settings.ScriptImports.AllowSourceSecretMigration = true
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatal(err)
	}

	value := randomProtectedImportValue(t)
	content := fmt.Sprintf(`{
	  "openapi":"3.0.3",
	  "info":{"title":"Migratable OpenAPI","version":"1"},
	  "servers":[{"url":"https://api.example.test"}],
	  "paths":{"/login":{"post":{
	    "operationId":"login",
	    "requestBody":{"content":{"application/json":{"example":{"password":%q}}}},
	    "responses":{"204":{"description":"ok"}}
	  }}}
	}`, value)
	inspection := secretAPIRequest(t, server, http.MethodPost, "/api/v1/scripts/import/openapi", map[string]any{
		"content":    content,
		"project_id": projectID,
	})
	if inspection.Code != http.StatusOK {
		t.Fatalf("inspection status = %d body=%s", inspection.Code, inspection.Body.String())
	}
	if strings.Contains(inspection.Body.String(), value) {
		t.Fatal("inspection disclosed a migratable OpenAPI credential")
	}
	var payload struct {
		SourceSessionID string                     `json:"source_secret_session_id"`
		Requirements    []SecretBindingRequirement `json:"secret_binding_requirements"`
	}
	if err := json.Unmarshal(inspection.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.SourceSessionID == "" || len(payload.Requirements) != 1 ||
		!payload.Requirements[0].SourceValueAvailable {
		t.Fatalf("protected migration metadata = %#v", payload)
	}

	migration := secretAPIRequest(t, server, http.MethodPost, "/api/v1/scripts/import/secrets/migrate", map[string]any{
		"project_id":               projectID,
		"source_secret_session_id": payload.SourceSessionID,
		"secrets": []map[string]string{{
			"requirement_id": payload.Requirements[0].ID,
			"secret_name":    "MIGRATED_OPENAPI_PASSWORD",
		}},
	})
	if migration.Code != http.StatusCreated {
		t.Fatalf("migration status = %d body=%s", migration.Code, migration.Body.String())
	}
	if strings.Contains(migration.Body.String(), value) {
		t.Fatal("migration response disclosed the protected OpenAPI credential")
	}
	storedValue, _, err := managed.Get(context.Background(), projectID, "MIGRATED_OPENAPI_PASSWORD")
	if err != nil {
		t.Fatal(err)
	}
	if storedValue != value {
		t.Fatal("migrated OpenAPI credential did not match the protected source value")
	}
}

func TestImportCandidateEnvelopeDecodesLegacyPayloadWithoutCredentials(t *testing.T) {
	payload, err := json.Marshal(importCandidateEnvelope{
		Version:   importCandidateSessionVersion,
		Profile:   "postman",
		ProjectID: "legacy-project",
		Script: dsl.Script{
			Name:     "Legacy",
			Version:  "1.0.0",
			Protocol: "http",
			Actions:  []dsl.Action{{Name: "request", Type: "request", Method: "GET", Target: "/"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	envelope, requirements, err := unmarshalImportCandidateEnvelope(payload)
	clear(payload)
	if err != nil {
		t.Fatal(err)
	}
	if envelope.Script.Name != "Legacy" || len(requirements) != 0 {
		t.Fatal("legacy protected candidate payload changed during decode")
	}
}

func randomProtectedImportValue(t *testing.T) string {
	t.Helper()
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(raw)
}

func assertScriptOmitsProtectedImportValue(t *testing.T, script *dsl.Script, value string) {
	t.Helper()
	payload, err := json.Marshal(script)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(payload)
	if bytes.Contains(payload, []byte(value)) {
		t.Fatal("script retained a protected credential value")
	}
}

func assertScriptReferencesProtectedImportName(t *testing.T, script *dsl.Script, name string) {
	t.Helper()
	payload, err := json.Marshal(script)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(payload)
	if !bytes.Contains(payload, []byte("{{secret."+name+"}}")) {
		t.Fatalf("script does not reference governed secret name %q", name)
	}
}

func assertProtectedImportMetadataRemoved(t *testing.T, script *dsl.Script) {
	t.Helper()
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil {
		return
	}
	if _, exists := script.Metadata.Import.Preserved[protectedImportCredentialMetadataKey]; exists {
		t.Fatal("protected credential binding metadata remained in the committed script")
	}
}

func assertProtectedImportArtifactsEncrypted(t *testing.T, root, value string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		payload, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		defer clear(payload)
		if bytes.Contains(payload, []byte(value)) {
			return fmt.Errorf("protected credential appeared in a persisted plaintext artifact")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
