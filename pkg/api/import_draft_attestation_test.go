package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/importartifact"
	"github.com/jasonczech/apexload/pkg/internal/importreview"
)

func TestLegacyImportSaveAttestationPreservesCredentialWithoutReentry(t *testing.T) {
	const canary = "legacy-import-save-password-7f31"
	server := NewServer(ServerConfig{})
	script := attestedImportTestScript("attested-import", canary)
	if fingerprint, err := fingerprintImportDraftCredentials(script); err != nil ||
		!fingerprint.HasRawCredentials || fingerprint.StructureDigest == "" {
		t.Fatalf("import draft fingerprint = %#v, %v", fingerprint, err)
	}
	inspectionRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts/import/sample",
		nil,
	)
	response := server.newScriptImportResponseWithSourceSecrets(
		inspectionRequest,
		scriptImportResponseOptions{
			Script: script,
			Result: map[string]any{"script": script},
		},
	)
	if importDraftAttestationToken(script) == "" {
		t.Fatalf("import response did not attest its runnable draft: %#v", response)
	}
	if expiresAt, ok := response["import_draft_expires_at"].(time.Time); !ok ||
		!expiresAt.After(time.Now()) {
		t.Fatalf("import draft expiry = %#v, want a future timestamp", response["import_draft_expires_at"])
	}
	inspectionJSON, err := json.Marshal(response["script"])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(inspectionJSON, []byte(canary)) || bytes.Contains(inspectionJSON, []byte(maskedScriptSecret)) {
		t.Fatalf("inspection draft did not preserve imported credential: %s", inspectionJSON)
	}
	reviewJSON, err := json.Marshal(response["result"])
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(reviewJSON, []byte(importDraftAttestationToken(script))) {
		t.Fatalf("review projection duplicated the import-save attestation: %s", reviewJSON)
	}

	create := createAttestedImportScript(t, server, script)
	if create.Code != http.StatusCreated {
		t.Fatalf("attested import save status = %d body=%s", create.Code, create.Body.String())
	}
	if !strings.Contains(create.Body.String(), canary) {
		t.Fatalf("stored-script response did not preserve credential: %s", create.Body.String())
	}
	durable, ok := server.runner.GetScript(script.ID)
	if !ok || durable == nil || durable.Script == nil {
		t.Fatal("attested import was not saved")
	}
	if got := durable.Script.Actions[0].HeaderList[0].Value; got != "Bearer "+canary {
		t.Fatalf("durable imported Authorization = %q", got)
	}
	if importDraftAttestationToken(durable.Script) != "" {
		t.Fatal("short-lived import save attestation entered durable storage")
	}
	if !isCredentialPreservingImport(durable.Script) {
		t.Fatal("durable import lost its server-owned preservation policy")
	}

	read := httptest.NewRecorder()
	server.mux.ServeHTTP(
		read,
		httptest.NewRequest(http.MethodGet, "/api/v1/scripts/"+script.ID, nil),
	)
	if read.Code != http.StatusOK || !strings.Contains(read.Body.String(), canary) {
		t.Fatalf("stored-script read = %d %s", read.Code, read.Body.String())
	}
}

func TestImportDraftExpirationCanBeDisabledWithoutWeakeningOneShotClaim(t *testing.T) {
	server := NewServer(ServerConfig{})
	now := time.Now().UTC()
	server.importDraftOwner = importreview.NewMemoryDraftOwner(importreview.MemoryDraftOptions{Now: func() time.Time { return now }})
	script := attestedImportTestScript("non-expiring-import", "non-expiring-password-31af")
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/enterprise-script-archive", nil)
	response := server.newScriptImportResponseWithSourceSecrets(request, scriptImportResponseOptions{
		Script:                 script,
		DisableDraftExpiration: true,
	})
	if _, present := response["import_draft_expires_at"]; present {
		t.Fatalf("non-expiring response published an expiry: %#v", response["import_draft_expires_at"])
	}
	now = now.Add(100 * 365 * 24 * time.Hour)
	server.memoryImportDraftOwner().PruneExpired()
	if server.memoryImportDraftOwner().Len() != 1 {
		t.Fatal("non-expiring attestation was pruned")
	}

	claim, err := server.claimImportDraftAttestation(request, script)
	if err != nil || claim == nil || !claim.ExpirationDisabled {
		t.Fatalf("claim non-expiring attestation = %#v, err=%v", claim, err)
	}
	mutated := cloneScriptDefinition(script)
	mutated.Name = "Non-expiring import after review"
	rotatedExpiry, err := server.completeImportDraftMutation(request, claim, mutated)
	if err != nil || !rotatedExpiry.IsZero() {
		t.Fatalf("rotate non-expiring attestation expiry = %v, err=%v", rotatedExpiry, err)
	}
	rotated, err := server.claimImportDraftAttestation(request, mutated)
	if err != nil || rotated == nil || !rotated.ExpirationDisabled {
		t.Fatalf("rotated expiration policy: %#v %v", rotated, err)
	}
	server.releaseImportDraftAttestation(rotated)

	script = mutated

	created := createAttestedImportScript(t, server, script)
	if created.Code != http.StatusCreated {
		t.Fatalf("save non-expiring draft = %d body=%s", created.Code, created.Body.String())
	}
	if reused := createAttestedImportScript(t, server, script); reused.Code != http.StatusConflict {
		t.Fatalf("reuse non-expiring one-shot capability = %d body=%s", reused.Code, reused.Body.String())
	}
}

func TestProtectedImportDraftExpirationPolicySurvivesStoreRoundTrip(t *testing.T) {
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{
		Key:   bytes.Repeat([]byte{0x35}, 32),
		KeyID: "non-expiring-import-attestation",
	})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{
		BrunoArtifactDir:     t.TempDir(),
		BrunoArtifactWrapper: wrapper,
	})
	script := attestedImportTestScript("protected-non-expiring-import", "protected-password-81ce")
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/enterprise-script-archive", nil)
	response := server.newScriptImportResponseWithSourceSecrets(request, scriptImportResponseOptions{
		Script:                 script,
		DisableDraftExpiration: true,
	})
	if _, present := response["import_draft_expires_at"]; present {
		t.Fatalf("protected non-expiring response published an expiry: %#v", response["import_draft_expires_at"])
	}
	token := importDraftAttestationToken(script)
	sessionID := strings.TrimPrefix(token, importDraftProtectedTokenPrefix)
	if sessionID == token || sessionID == "" {
		t.Fatalf("protected token = %q", token)
	}
	candidate, claim, err := server.claimProtectedImportCandidateSession(request, "sample", sessionID, script.ProjectID)
	if err != nil {
		t.Fatalf("claim protected non-expiring draft: %v", err)
	}
	if !candidate.ExpirationDisabled || !candidate.ExpiresAt.Equal(importCandidateNoExpiration) {
		t.Fatalf("protected expiration policy = disabled:%v expires:%v", candidate.ExpirationDisabled, candidate.ExpiresAt)
	}
	clearProtectedImportCandidate(candidate)
	server.releaseImportCandidateClaim(claim)
}

func TestConfiguredProtectedImportStoreFailureNeverFallsBackToMemory(t *testing.T) {
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{
		Key:   bytes.Repeat([]byte{0x6d}, 32),
		KeyID: "configured-store-failure",
	})
	if err != nil {
		t.Fatal(err)
	}
	invalidRoot := filepath.Join(t.TempDir(), "artifact-root-is-a-file")
	if err := os.WriteFile(invalidRoot, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{
		BrunoArtifactDir:     invalidRoot,
		BrunoArtifactWrapper: wrapper,
	})
	script := attestedImportTestScript("configured-store-failure", "configured-store-canary")
	err = server.attachImportDraftAttestation(
		httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/sample", nil),
		script,
	)
	if !errors.Is(err, errProtectedImportStoreUnavailable) {
		t.Fatalf("attach error = %v, want protected store unavailable", err)
	}
	if token := importDraftAttestationToken(script); token != "" {
		t.Fatalf("failed protected store silently issued fallback token %q", token)
	}
}

func TestImportSaveAttestationIncludesProtectedRequirementDescriptors(t *testing.T) {
	const (
		inlineCanary  = "mixed-import-inline-password-8a41"
		protectedOnly = "mixed-import-protected-password-6c29"
	)
	server := NewServer(ServerConfig{})
	script := attestedImportTestScript("attested-mixed-import", inlineCanary)
	script.Actions[0].Body = `{"password":"{{secret.PASSWORD}}"}`
	requirements := []credentialpolicy.Requirement{{
		ID:            "mixed-import-password",
		Value:         protectedOnly,
		SuggestedName: "PASSWORD",
		Replacement:   "{{secret.PASSWORD}}",
	}}

	response := server.newScriptImportResponseWithSourceSecrets(
		httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/sample", nil),
		scriptImportResponseOptions{
			Script:                script,
			ProtectedRequirements: requirements,
		},
	)
	if failed, _ := response[importDraftResponseFailureKey].(bool); failed {
		t.Fatalf("mixed import response failed to secure its draft: %#v", response)
	}
	if importDraftAttestationToken(script) == "" {
		t.Fatal("mixed import response did not attest the final editable draft")
	}
	if script.Metadata == nil || script.Metadata.Import == nil ||
		script.Metadata.Import.Preserved[protectedImportCredentialMetadataKey] == nil {
		t.Fatal("protected credential descriptor was not attached before save")
	}

	created := createAttestedImportScript(t, server, script)
	if created.Code != http.StatusCreated {
		t.Fatalf("mixed import save status = %d body=%s", created.Code, created.Body.String())
	}
	durable, ok := server.runner.GetScript(script.ID)
	if !ok || durable == nil || durable.Script == nil {
		t.Fatal("mixed import was not saved")
	}
	if got := durable.Script.Actions[0].HeaderList[0].Value; got != "Bearer "+inlineCanary {
		t.Fatalf("durable inline credential = %q", got)
	}
	if got := durable.Script.Actions[0].Body; got != `{"password":"{{secret.PASSWORD}}"}` {
		t.Fatalf("durable protected reference body = %q", got)
	}
	if !strings.Contains(created.Body.String(), inlineCanary) ||
		strings.Contains(created.Body.String(), protectedOnly) {
		t.Fatalf("create response did not preserve only the credential present in the draft: %s", created.Body.String())
	}
}

func TestImportResponseFailsClosedWhenDraftAttestationCapacityIsUnavailable(t *testing.T) {
	const canary = "capacity-failure-import-credential-6d3e"
	server := NewServer(ServerConfig{})
	server.importDraftOwner = importreview.NewMemoryDraftOwner(importreview.MemoryDraftOptions{Capacity: 1})
	occupied := attestedImportTestScript("occupied", "existing-capability")
	if err := server.attachImportDraftAttestation(httptest.NewRequest(http.MethodPost, "/", nil), occupied); err != nil {
		t.Fatal(err)
	}

	script := attestedImportTestScript("capacity-failure", canary)
	response := server.newScriptImportResponseWithSourceSecrets(
		httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/sample", nil),
		scriptImportResponseOptions{Script: script},
	)
	recorder := httptest.NewRecorder()
	writeScriptImportResponse(recorder, http.StatusOK, response)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("capacity failure status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), canary) ||
		strings.Contains(recorder.Body.String(), `"script"`) {
		t.Fatalf("capacity failure published an unsaveable raw draft: %s", recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "import_draft_attestation_unavailable") ||
		recorder.Header().Get("Retry-After") == "" {
		t.Fatalf("capacity failure response = %s headers=%v", recorder.Body.String(), recorder.Header())
	}
	if importDraftAttestationToken(script) != "" {
		t.Fatal("capacity failure left a misleading attestation token on the draft")
	}
}

func TestOpaqueImportSourcePayloadsAreAttestedAndRetainedDurably(t *testing.T) {
	cases := []struct {
		key     string
		payload func(string) any
	}{
		{key: "jsr223_sources"},
		{key: "k6_gated_source"},
		{key: "postman_event_sources"},
		{key: "credential_traversal_source_snapshot_base64"},
		{key: dsl.ImportPreservedJMeterJDBCSourcesKey},
		{key: dsl.ImportPreservedEnterpriseUnsupportedProxySourceKey},
		{
			key: dsl.ImportPreservedEnterpriseJavaVMSourceKey,
			payload: func(canary string) any {
				return map[string]any{
					"version":     1,
					"rows_base64": []string{base64.StdEncoding.EncodeToString([]byte("-Dpassword=" + canary))},
				}
			},
		},
		{
			key: dsl.ImportPreservedEnterpriseJavaJDBCSourceKey,
			payload: func(canary string) any {
				return map[string]any{
					"version": 1,
					"connections": []map[string]any{{
						"driver_base64": base64.StdEncoding.EncodeToString([]byte("postgres")),
						"dsn_base64": base64.StdEncoding.EncodeToString(
							[]byte("postgres://operator:" + canary + "@db.example.test/app"),
						),
						"reason":     "unsupported",
						"source_ref": "Fixture.java line 1",
					}},
				}
			},
		},
	}
	for _, test := range cases {
		t.Run(test.key, func(t *testing.T) {
			canary := "opaque-source-credential-" + test.key + "-8f2c"
			payload := any("password=" + canary)
			if test.payload != nil {
				payload = test.payload(canary)
			}
			server := NewServer(ServerConfig{})
			script := &dsl.Script{
				ID:        "opaque-" + strings.ReplaceAll(test.key, "_", "-"),
				Name:      "Opaque import source",
				Version:   "1",
				Protocol:  "http",
				ProjectID: "project-a",
				Config:    dsl.ScriptConfig{BaseURL: "https://example.test"},
				Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
					Source: "fixture",
					Preserved: map[string]any{
						test.key: payload,
					},
				}},
				Actions: []dsl.Action{{
					Name: "Review-gated request", Type: "request",
					Method: http.MethodGet, Target: "/",
					Metadata: map[string]any{
						"execution_blocker": "import_review_required",
					},
				}},
			}
			ensureCredentialPreservingImportMetadata(script, "fixture")
			fingerprint, err := fingerprintImportDraftCredentials(script)
			if err != nil || !fingerprint.HasRawCredentials ||
				fingerprint.FullDigest == "" {
				t.Fatalf("opaque fingerprint = %#v err=%v", fingerprint, err)
			}
			response := server.newScriptImportResponseWithSourceSecrets(
				httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/fixture", nil),
				scriptImportResponseOptions{Script: script},
			)
			if failed, _ := response[importDraftResponseFailureKey].(bool); failed ||
				importDraftAttestationToken(script) == "" {
				t.Fatalf("opaque response was not attested: %#v", response)
			}
			created := createAttestedImportScript(t, server, script)
			if created.Code != http.StatusCreated {
				t.Fatalf("opaque save status = %d body=%s", created.Code, created.Body.String())
			}
			durable, ok := server.runner.GetScript(script.ID)
			if !ok || durable == nil || durable.Script == nil {
				t.Fatal("opaque source script was not persisted")
			}
			got := durable.Script.Metadata.Import.Preserved[test.key]
			if !jsonValuesEqual(got, payload) {
				t.Fatalf("durable opaque payload = %#v, want %#v", got, payload)
			}
			read := httptest.NewRecorder()
			server.mux.ServeHTTP(
				read,
				httptest.NewRequest(http.MethodGet, "/api/v1/scripts/"+script.ID, nil),
			)
			var readStored StoredScript
			if read.Code != http.StatusOK || json.Unmarshal(read.Body.Bytes(), &readStored) != nil ||
				readStored.Script == nil ||
				!jsonValuesEqual(readStored.Script.Metadata.Import.Preserved[test.key], payload) {
				t.Fatalf("ordinary read did not preserve opaque payload: %d %s", read.Code, read.Body.String())
			}
			maskedEdit := maskScriptSecrets(durable.Script)
			if maskedEdit == nil {
				t.Fatal("failed to build masked opaque-source edit")
			}
			maskedEdit.Name = "Renamed opaque import"
			roundTrip, err := preparePersistentLibraryScript(maskedEdit, durable.Script)
			if err != nil {
				t.Fatalf("round-trip masked opaque source: %v", err)
			}
			got = roundTrip.Metadata.Import.Preserved[test.key]
			if !jsonValuesEqual(got, payload) {
				t.Fatalf("round-trip opaque payload = %#v, want %#v", got, payload)
			}
		})
	}
}

func jsonValuesEqual(left, right any) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func TestDiscardProtectedImportDraftAttestationUsesInspectionProjectScope(t *testing.T) {
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{
		Key:   bytes.Repeat([]byte{0x4b}, 32),
		KeyID: "legacy-import-attestation-discard",
	})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{
		BrunoArtifactDir:     t.TempDir(),
		BrunoArtifactWrapper: wrapper,
	})
	script := attestedImportTestScript(
		"attestation-discard-project",
		"discard-project-password-93af",
	)
	script.ProjectID = ""
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts/import/sample?project_id=inspection-project",
		nil,
	)
	if err := server.attachImportDraftAttestation(request, script); err != nil {
		t.Fatalf("attach protected import draft attestation: %v", err)
	}
	token := importDraftAttestationToken(script)
	sessionID := strings.TrimPrefix(token, importDraftProtectedTokenPrefix)
	if sessionID == token || strings.TrimSpace(sessionID) == "" {
		t.Fatalf("protected attestation token = %q", token)
	}

	server.discardImportDraftAttestation(request, script)
	if importDraftAttestationToken(script) != "" {
		t.Fatal("discard left the attestation token on the editable draft")
	}
	candidate, claim, err := server.claimProtectedImportCandidateSession(
		request,
		"sample",
		sessionID,
		"inspection-project",
	)
	if err == nil {
		clearProtectedImportCandidate(candidate)
		server.releaseImportCandidateClaim(claim)
		t.Fatal("discard left the protected import session claimable")
	}
}

func TestImportSaveAttestationRejectsCredentialSubstitutionAndRemainsRetryable(t *testing.T) {
	const original = "attested-original-password-1d62"
	server := NewServer(ServerConfig{})
	script := attestedImportTestScript("attestation-substitution", original)
	server.newScriptImportResponseWithSourceSecrets(
		httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/sample", nil),
		scriptImportResponseOptions{Script: script},
	)
	if importDraftAttestationToken(script) == "" {
		t.Fatal("test import received no attestation")
	}

	substituted := cloneScriptDefinition(script)
	substituted.Actions[0].HeaderList[0].Value = "Bearer attacker-selected-password"
	rejected := createAttestedImportScript(t, server, substituted)
	if rejected.Code != http.StatusConflict ||
		strings.Contains(rejected.Body.String(), original) ||
		strings.Contains(rejected.Body.String(), "attacker-selected-password") {
		t.Fatalf("substituted attestation response = %d %s", rejected.Code, rejected.Body.String())
	}
	if _, ok := server.runner.GetScript(script.ID); ok {
		t.Fatal("credential substitution reached durable storage")
	}

	accepted := createAttestedImportScript(t, server, script)
	if accepted.Code != http.StatusCreated {
		t.Fatalf("original attested retry status = %d body=%s", accepted.Code, accepted.Body.String())
	}
	durable, ok := server.runner.GetScript(script.ID)
	if !ok || durable.Script.Actions[0].HeaderList[0].Value != "Bearer "+original {
		t.Fatalf("original credential was not retained: %#v", durable)
	}
}

func TestImportSaveAttestationAllowsNonCredentialImportOptions(t *testing.T) {
	const credential = "attested-structure-bound-password-39af"
	server := NewServer(ServerConfig{})
	script := attestedImportTestScript("attestation-structure", credential)
	server.newScriptImportResponseWithSourceSecrets(
		httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/sample", nil),
		scriptImportResponseOptions{Script: script},
	)
	if importDraftAttestationToken(script) == "" {
		t.Fatal("test import received no attestation")
	}

	configured := cloneScriptDefinition(script)
	configured.Name = "Configured imported request"
	configured.Config.BaseURL = "https://configured.example"
	configured.Config.Pacing = dsl.PacingConfig{Type: "fixed_delay", DelayMs: 2500}
	configured.Config.HTTP.Auth = dsl.AuthConfig{
		Type:     "basic",
		Username: "operator",
		Password: "{{secret.SAMPLE_LOGIN_PASSWORD}}",
	}
	configured.Actions[0].Target = "/configured"
	configured.Metadata.Import.Preserved["correlation_suggestions"] = []map[string]any{{
		"name":         "session_id",
		"source":       "json",
		"expression":   "$.session_id",
		"sample_value": "correlated-session-id",
		"found_at":     "Step 1",
		"used_at":      []string{"Step 2"},
	}}
	created := createAttestedImportScript(t, server, configured)
	if created.Code != http.StatusCreated ||
		!strings.Contains(created.Body.String(), credential) {
		t.Fatalf("configured attestation response = %d %s", created.Code, created.Body.String())
	}
	durable, ok := server.runner.GetScript(script.ID)
	if !ok || durable == nil || durable.Script == nil {
		t.Fatal("configured import was not saved")
	}
	if durable.Script.Name != configured.Name ||
		durable.Script.Config.BaseURL != configured.Config.BaseURL ||
		durable.Script.Config.Pacing.DelayMs != configured.Config.Pacing.DelayMs ||
		durable.Script.Actions[0].Target != configured.Actions[0].Target ||
		durable.Script.Actions[0].HeaderList[0].Value != "Bearer "+credential {
		t.Fatalf("configured import was not retained exactly: %#v", durable.Script)
	}
}

func TestPartialSecretBindingPreservesUnselectedImportedCredential(t *testing.T) {
	const (
		headerCredential = "partial-binding-header-credential-51a2"
		authCredential   = "partial-binding-auth-password-94bf"
	)
	server := NewServer(ServerConfig{})
	script := attestedImportTestScript("partial-binding-preserve", headerCredential)
	script.Config.HTTP.Auth = dsl.AuthConfig{
		Type:     "basic",
		Username: "operator",
		Password: authCredential,
	}
	inspection := server.newScriptImportResponseWithSourceSecrets(
		httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/sample", nil),
		scriptImportResponseOptions{Script: script},
	)
	requirements, ok := inspection["secret_binding_requirements"].([]SecretBindingRequirement)
	if !ok || len(requirements) != 2 {
		t.Fatalf("inspection requirements = %#v", inspection["secret_binding_requirements"])
	}
	header := findSecretBindingRequirement(
		t,
		requirements,
		"/actions/0/header_list/0/value",
		0,
	)
	projectedJSON, err := json.Marshal(inspection["script"])
	if err != nil {
		t.Fatal(err)
	}
	var submitted dsl.Script
	if err := json.Unmarshal(projectedJSON, &submitted); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(projectedJSON), headerCredential) ||
		!strings.Contains(string(projectedJSON), authCredential) ||
		submitted.Config.HTTP.Auth.Password != authCredential {
		t.Fatalf("partial-binding preview did not preserve imported credentials: %s", projectedJSON)
	}
	originalToken := importDraftAttestationToken(&submitted)
	if originalToken == "" {
		t.Fatal("inspection did not issue an import-draft attestation")
	}

	applied := postSecretBindingJSON(
		t,
		server,
		"/api/v1/scripts/secret-bindings/apply",
		map[string]any{
			"script": &submitted,
			"bindings": []map[string]string{{
				"requirement_id": header.ID,
				"secret_name":    "IMPORTED_HEADER",
			}},
		},
	)
	if applied.Code != http.StatusOK {
		t.Fatalf("partial binding status = %d body=%s", applied.Code, applied.Body.String())
	}
	if applied.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("partial binding cache control = %q", applied.Header().Get("Cache-Control"))
	}
	var payload struct {
		Script       *dsl.Script                `json:"script"`
		Requirements []SecretBindingRequirement `json:"secret_binding_requirements"`
		ExpiresAt    string                     `json:"import_draft_expires_at"`
	}
	if err := json.Unmarshal(applied.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Script == nil {
		t.Fatal("partial binding returned no script")
	}
	if got := payload.Script.Actions[0].HeaderList[0].Value; got != "Bearer {{secret.IMPORTED_HEADER}}" {
		t.Fatalf("selected header = %q", got)
	}
	if got := payload.Script.Config.HTTP.Auth.Password; got != authCredential {
		t.Fatalf("unselected auth password = %q, want preserved value", got)
	}
	if !strings.Contains(applied.Body.String(), authCredential) {
		t.Fatalf("partial binding response did not preserve unselected credential: %s", applied.Body.String())
	}
	if len(payload.Requirements) != 1 ||
		!payload.Requirements[0].DestinationValuePreserved {
		t.Fatalf("remaining preserved requirements = %#v", payload.Requirements)
	}
	if payload.ExpiresAt == "" {
		t.Fatal("rotated draft expiry was not returned")
	}
	rotatedToken := importDraftAttestationToken(payload.Script)
	if rotatedToken == "" || rotatedToken == originalToken {
		t.Fatalf("rotated attestation token = %q, original = %q", rotatedToken, originalToken)
	}

	created := createAttestedImportScript(t, server, payload.Script)
	if created.Code != http.StatusCreated {
		t.Fatalf("save partially rebound import = %d body=%s", created.Code, created.Body.String())
	}
	durable, ok := server.runner.GetScript(payload.Script.ID)
	if !ok || durable == nil || durable.Script == nil {
		t.Fatal("partially rebound import was not persisted")
	}
	if got := durable.Script.Config.HTTP.Auth.Password; got != authCredential {
		t.Fatalf("durable unselected auth password = %q", got)
	}
	if got := durable.Script.Actions[0].HeaderList[0].Value; got != "Bearer {{secret.IMPORTED_HEADER}}" {
		t.Fatalf("durable selected header = %q", got)
	}
	if !strings.Contains(created.Body.String(), authCredential) {
		t.Fatalf("ordinary create response did not preserve retained credential: %s", created.Body.String())
	}
}

func TestForgedImportSaveAttestationCannotAuthorizePlaintext(t *testing.T) {
	server := NewServer(ServerConfig{})
	script := attestedImportTestScript("forged-attestation", "forged-password")
	ensureCredentialPreservingImportMetadata(script, "sample")
	script.Metadata.Import.Preserved[importDraftAttestationMetadataKey] = "forged-token"

	response := createAttestedImportScript(t, server, script)
	if response.Code != http.StatusConflict {
		t.Fatalf("forged attestation status = %d body=%s", response.Code, response.Body.String())
	}
	if _, ok := server.runner.GetScript(script.ID); ok {
		t.Fatal("forged attestation reached durable storage")
	}
}

func TestRetainedCommittedImportAttestationCannotBeClaimedAgain(t *testing.T) {
	server := NewServer(ServerConfig{})
	script := attestedImportTestScript("retained-one-shot", "one-shot-password")
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/sample", nil)
	server.newScriptImportResponseWithSourceSecrets(
		request,
		scriptImportResponseOptions{Script: script},
	)

	claim, err := server.claimImportDraftAttestation(request, script)
	if err != nil || claim == nil {
		t.Fatalf("initial claim = %#v, err=%v", claim, err)
	}
	server.retainCommittedImportDraftAttestation(claim)
	if reused, reuseErr := server.claimImportDraftAttestation(request, script); reuseErr == nil || reused != nil {
		t.Fatalf("retained capability was claimable again: %#v, err=%v", reused, reuseErr)
	}
}

func TestImportSaveAttestationSurvivesReplicaHandoff(t *testing.T) {
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{
		Key:   bytes.Repeat([]byte{0x7c}, 32),
		KeyID: "legacy-import-attestation-ha",
	})
	if err != nil {
		t.Fatal(err)
	}
	artifactDir := t.TempDir()
	sourceReplica := NewServer(ServerConfig{
		BrunoArtifactDir:     artifactDir,
		BrunoArtifactWrapper: wrapper,
	})
	destinationReplica := NewServer(ServerConfig{
		BrunoArtifactDir:     artifactDir,
		BrunoArtifactWrapper: wrapper,
	})
	script := attestedImportTestScript(
		"attestation-replica-handoff",
		"replica-handoff-password-51ab",
	)
	sourceReplica.newScriptImportResponseWithSourceSecrets(
		httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/sample", nil),
		scriptImportResponseOptions{Script: script},
	)
	token := importDraftAttestationToken(script)
	if !strings.HasPrefix(token, importDraftProtectedTokenPrefix) {
		t.Fatalf("shared-store import attestation token = %q", token)
	}

	created := createAttestedImportScript(t, destinationReplica, script)
	if created.Code != http.StatusCreated {
		t.Fatalf("replica-handoff save status = %d body=%s", created.Code, created.Body.String())
	}
	durable, ok := destinationReplica.runner.GetScript(script.ID)
	if !ok || durable == nil || durable.Script == nil ||
		durable.Script.Actions[0].HeaderList[0].Value != "Bearer replica-handoff-password-51ab" {
		t.Fatalf("replica-handoff durable script = %#v", durable)
	}

	reused := createAttestedImportScript(t, sourceReplica, script)
	if reused.Code != http.StatusConflict {
		t.Fatalf("completed shared attestation reuse status = %d body=%s", reused.Code, reused.Body.String())
	}
}

func attestedImportTestScript(id, credentialValue string) *dsl.Script {
	return &dsl.Script{
		ID: id, Name: "Attested import", Version: "1.0.0", Protocol: "http", ProjectID: "project-a",
		Config: dsl.ScriptConfig{BaseURL: "https://example.test"},
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source:    "sample",
			Preserved: map[string]any{},
		}},
		Actions: []dsl.Action{{
			Name: "Request", Type: "request", Method: http.MethodGet, Target: "/",
			HeaderList: dsl.HeaderList{{
				Name:  "Authorization",
				Value: "Bearer " + credentialValue,
			}},
		}},
	}
}

func createAttestedImportScript(
	t *testing.T,
	server *Server,
	script *dsl.Script,
) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"script": script})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	return response
}
