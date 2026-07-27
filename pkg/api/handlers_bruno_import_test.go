package api

import (
	"archive/tar"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/brunoartifact"
	brunoconvert "github.com/jasonczech/apexload/pkg/convert/bruno"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/secretstore"
	"github.com/jasonczech/apexload/pkg/storage"
)

const brunoImportTestProject = "project-bruno-api"

func TestBrunoImportIgnoresUnusedMigrationOptIn(t *testing.T) {
	server, _, _ := newBrunoImportAPITestServer(t)
	inspection := inspectBrunoJSON(t, server, []byte(`info: {name: No Secrets, type: http}
http: {method: GET, url: https://example.test}
`), "")
	migrate := true
	response := commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID:       brunoImportTestProject,
		ImportSessionID: inspection.ImportSessionID,
		Mode:            "create",
		MigrateSecrets:  &migrate,
	})
	if response.Code != http.StatusCreated {
		t.Fatalf("unused migration opt-in status = %d body=%s", response.Code, response.Body.String())
	}
}

func TestBrunoImportMigrationOptInIgnoresRequirementWithoutSourceValue(t *testing.T) {
	server, _, _ := newBrunoImportAPITestServer(t)
	inspection := inspectBrunoJSON(t, server, []byte(`info: {name: Existing Secret, type: http}
http:
  method: GET
  url: https://example.test
  headers:
    - name: Authorization
      value: 'Bearer {{secret.api-token}}'
`), "")
	if len(inspection.Result.SecretRequirements) != 1 {
		t.Fatalf("secret requirements = %#v", inspection.Result.SecretRequirements)
	}
	requirement := inspection.Result.SecretRequirements[0]
	if requirement.Value != "" || requirement.SourceValueAvailable || !requirement.ExistingReference {
		t.Fatalf("non-migratable secret requirement = %#v", requirement)
	}

	migrate := true
	response := commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID:       brunoImportTestProject,
		ImportSessionID: inspection.ImportSessionID,
		Mode:            "create",
		MigrateSecrets:  &migrate,
	})
	if response.Code != http.StatusCreated {
		t.Fatalf("non-migratable requirement status = %d body=%s", response.Code, response.Body.String())
	}
	if _, _, err := server.projectSecretStore.Get(
		context.Background(), brunoImportTestProject, requirement.SuggestedName,
	); !errors.Is(err, secretstore.ErrNotFound) {
		t.Fatalf("non-migratable requirement unexpectedly created a secret: %v", err)
	}
}

func TestBrunoSecretMatchesRetainedMigration(t *testing.T) {
	started := time.Now().UTC()
	window := brunoSecretMigrationWindow{StartedAt: started, ExpiresAt: started.Add(30 * time.Minute)}
	for _, test := range []struct {
		name     string
		value    string
		metadata secretstore.Metadata
		actor    string
		want     bool
	}{
		{
			name:  "same session",
			value: "source-value",
			metadata: secretstore.Metadata{
				CreatedBy: "actor-a",
				CreatedAt: started.Add(time.Second),
			},
			actor: "actor-a",
			want:  true,
		},
		{
			name:  "different value",
			value: "other-value",
			metadata: secretstore.Metadata{
				CreatedBy: "actor-a",
				CreatedAt: started.Add(time.Second),
			},
			actor: "actor-a",
		},
		{
			name:  "different actor",
			value: "source-value",
			metadata: secretstore.Metadata{
				CreatedBy: "actor-b",
				CreatedAt: started.Add(time.Second),
			},
			actor: "actor-a",
		},
		{
			name:  "outside session window",
			value: "source-value",
			metadata: secretstore.Metadata{
				CreatedBy: "actor-a",
				CreatedAt: started.Add(-2 * time.Minute),
			},
			actor: "actor-a",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := brunoSecretMatchesRetainedMigration(
				test.value, "source-value", test.metadata, test.actor, window,
			)
			if got != test.want {
				t.Fatalf("match = %v, want %v", got, test.want)
			}
		})
	}
}

func rewriteBrunoImportSessionForTest(t *testing.T, server *Server, id string, mutate func(*brunoImportSession)) *brunoImportSession {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/bruno/commit", nil)
	session, err := server.consumeBrunoImportSession(request, id, brunoImportTestProject)
	if err != nil {
		t.Fatalf("consume test Bruno import session: %v", err)
	}
	mutate(session)
	if err := server.persistBrunoImportSession(context.Background(), session); err != nil {
		t.Fatalf("persist rewritten Bruno import session: %v", err)
	}
	server.storeBrunoImportSession(session)
	return session
}

func TestBrunoImportInspectCommitSourceSyncAndRollback(t *testing.T) {
	server, store, artifactDir := newBrunoImportAPITestServer(t)
	enableSourceSecretMigration(t, server)
	source := []byte(`info:
  name: Login
  type: http
  uid: login-request
http:
  method: POST
  url: https://example.test/login
  auth:
    type: basic
    username: literal-user
    password: literal-password
`)

	inspect := inspectBrunoJSON(t, server, source, "")
	if inspect.Result == nil || inspect.Result.Script == nil || inspect.Result.Script.Protocol != "bruno" {
		t.Fatalf("inspect result = %#v", inspect.Result)
	}
	if len(inspect.Result.SecretRequirements) != 2 {
		t.Fatalf("secret requirements = %#v", inspect.Result.SecretRequirements)
	}
	for _, requirement := range inspect.Result.SecretRequirements {
		if requirement.Value != "" {
			t.Fatal("inspect API serialized an imported secret value")
		}
	}
	if inspect.Parameterization != nil {
		for _, suggestion := range inspect.Parameterization.Suggestions {
			if suggestion.Kind != "data_param" {
				t.Fatalf("Bruno inspect advertised non-commit-safe parameterization: %#v", suggestion)
			}
		}
	}
	if len(inspect.Artifacts) != 1 || inspect.Artifacts[0].ArtifactKind != "sanitized" || inspect.Artifacts[0].SHA256 == "" {
		t.Fatalf("artifact summaries = %#v", inspect.Artifacts)
	}
	assertDirectoryDoesNotContain(t, artifactDir, "literal-password")

	bindings := make(map[string]string, len(inspect.Result.SecretRequirements))
	for index, requirement := range inspect.Result.SecretRequirements {
		bindings[requirement.ID] = "BRUNO_SECRET_" + string(rune('A'+index))
	}
	migrateSecrets := true
	server.brunoImportMu.Lock()
	server.brunoImportSessions = make(map[string]*brunoImportSession)
	server.brunoImportMu.Unlock()
	commitResponse := commitBrunoImport(t, server, brunoCommitRequest{
		ProjectID:       brunoImportTestProject,
		ImportSessionID: inspect.ImportSessionID,
		Mode:            "create",
		SecretBindings:  bindings,
		MigrateSecrets:  &migrateSecrets,
	})
	script := decodeCommittedBrunoScript(t, commitResponse.Body.Bytes())
	if script.ID == "" || script.Config.Bruno.SourceRevision == "" {
		t.Fatalf("committed script = %#v", script)
	}
	expectedValues := map[string]string{
		"http.auth.username": "literal-user",
		"http.auth.password": "literal-password",
	}
	for _, requirement := range inspect.Result.SecretRequirements {
		name := bindings[requirement.ID]
		value, metadata, err := server.projectSecretStore.Get(context.Background(), brunoImportTestProject, name)
		if err != nil {
			t.Fatalf("resolve migrated %s: %v", requirement.Key, err)
		}
		if value != expectedValues[requirement.Key] || metadata.Name != name {
			t.Fatalf("migrated %s = value:%q metadata:%#v", requirement.Key, value, metadata)
		}
	}
	migrationAudits, err := store.ListAuditEvents(storage.AuditEventFilter{
		Action:    "script_import.secret.migrate",
		ProjectID: brunoImportTestProject,
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("ListAuditEvents: %v", err)
	}
	if len(migrationAudits) != 2*len(inspect.Result.SecretRequirements) {
		t.Fatalf("migration audit events = %#v, want intent and outcome per logical secret", migrationAudits)
	}
	auditPhases := make(map[string]map[string]bool)
	for _, event := range migrationAudits {
		operationID, _ := event.Details["operation_id"].(string)
		if operationID == "" {
			t.Fatalf("migration audit event has no operation id: %#v", event)
		}
		if auditPhases[operationID] == nil {
			auditPhases[operationID] = make(map[string]bool)
		}
		auditPhases[operationID][event.Status] = true
	}
	if len(auditPhases) != len(inspect.Result.SecretRequirements) {
		t.Fatalf("migration audit operations = %#v, want one per logical secret", auditPhases)
	}
	for operationID, phases := range auditPhases {
		if !phases["pending"] || !phases["success"] {
			t.Fatalf("migration audit operation %s phases = %#v", operationID, phases)
		}
	}
	encodedAudits, err := json.Marshal(migrationAudits)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encodedAudits, []byte("literal-user")) || bytes.Contains(encodedAudits, []byte("literal-password")) {
		t.Fatalf("migration audit events disclosed imported credential material: %s", encodedAudits)
	}
	firstRevision, err := store.GetActiveBrunoSourceRevision(script.ID, brunoImportTestProject)
	if err != nil {
		t.Fatalf("GetActiveBrunoSourceRevision: %v", err)
	}

	sanitized := requestBrunoSource(t, server, script.ID, false)
	if bytes.Contains(sanitized, []byte("literal-user")) || bytes.Contains(sanitized, []byte("literal-password")) {
		t.Fatal("sanitized source contains imported credentials")
	}
	if !tarContains(t, sanitized, "login.yml") {
		t.Fatal("sanitized source does not contain login.yml")
	}
	raw := requestBrunoSource(t, server, script.ID, true)
	if !bytes.Equal(raw, source) {
		t.Fatalf("raw source was not preserved exactly:\n%s", raw)
	}

	updatedSource := bytes.Replace(source, []byte("/login"), []byte("/login-v2"), 1)
	syncInspect := inspectBrunoJSON(t, server, updatedSource, script.ID)
	if syncInspect.SyncPreview == nil || syncInspect.SyncPreview.TargetScriptID != script.ID {
		t.Fatalf("sync preview = %#v", syncInspect.SyncPreview)
	}
	current, err := store.GetScript(script.ID)
	if err != nil {
		t.Fatal(err)
	}
	syncResponse := commitBrunoImport(t, server, brunoCommitRequest{
		ProjectID:              brunoImportTestProject,
		ImportSessionID:        syncInspect.ImportSessionID,
		Mode:                   "sync",
		SyncScriptID:           script.ID,
		ExpectedUpdatedAt:      &current.UpdatedAt,
		ExpectedActiveRevision: firstRevision.ID,
	})
	updatedScript := decodeCommittedBrunoScript(t, syncResponse.Body.Bytes())
	if updatedScript.Config.Bruno.SourceRevision == firstRevision.ID {
		t.Fatal("synchronization did not activate a new source revision")
	}
	secondRevision, err := store.GetActiveBrunoSourceRevision(script.ID, brunoImportTestProject)
	if err != nil {
		t.Fatal(err)
	}

	rollbackBody, _ := json.Marshal(map[string]string{
		"revision_id":                 firstRevision.ID,
		"expected_active_revision_id": secondRevision.ID,
	})
	rollback := httptest.NewRecorder()
	server.mux.ServeHTTP(rollback, httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts/"+url.PathEscape(script.ID)+"/bruno-imports/rollback",
		bytes.NewReader(rollbackBody),
	))
	if rollback.Code != http.StatusOK {
		t.Fatalf("rollback status = %d body=%s", rollback.Code, rollback.Body.String())
	}
	active, err := store.GetActiveBrunoSourceRevision(script.ID, brunoImportTestProject)
	if err != nil {
		t.Fatal(err)
	}
	if active.ID != firstRevision.ID {
		t.Fatalf("active revision = %q, want %q", active.ID, firstRevision.ID)
	}

	revisions := httptest.NewRecorder()
	server.mux.ServeHTTP(revisions, httptest.NewRequest(
		http.MethodGet,
		"/api/v1/scripts/"+url.PathEscape(script.ID)+"/bruno-imports/revisions",
		nil,
	))
	if revisions.Code != http.StatusOK || !strings.Contains(revisions.Body.String(), secondRevision.ID) {
		t.Fatalf("revision list status = %d body=%s", revisions.Code, revisions.Body.String())
	}
	if strings.Contains(revisions.Body.String(), firstRevision.RawArtifactID) ||
		strings.Contains(revisions.Body.String(), secondRevision.RawArtifactID) ||
		strings.Contains(revisions.Body.String(), `"raw_artifact_id"`) {
		t.Fatalf("revision list exposed protected raw artifact identifiers: %s", revisions.Body.String())
	}

	preview := httptest.NewRecorder()
	server.mux.ServeHTTP(preview, httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts/"+url.PathEscape(script.ID)+"/bruno-projection/preview",
		nil,
	))
	if preview.Code != http.StatusOK || !strings.Contains(preview.Body.String(), `"eligible":false`) {
		t.Fatalf("projection preview status = %d body=%s", preview.Code, preview.Body.String())
	}
	projectionCommit := httptest.NewRecorder()
	server.mux.ServeHTTP(projectionCommit, httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts/"+url.PathEscape(script.ID)+"/bruno-projection/commit",
		nil,
	))
	if projectionCommit.Code != http.StatusServiceUnavailable {
		t.Fatalf("projection commit status = %d body=%s", projectionCommit.Code, projectionCommit.Body.String())
	}
}

func TestBrunoImportMigratesRepeatedCredentialFieldsToDistinctBindings(t *testing.T) {
	server, _, _ := newBrunoImportAPITestServer(t)
	enableSourceSecretMigration(t, server)
	inspect := inspectBrunoJSON(t, server, []byte(`info: {name: Repeated credentials, type: http}
http:
  method: POST
  url: https://example.test/login
  body:
    type: json
    data:
      - password: first-source-password
      - password: second-source-password
`), "")
	if len(inspect.Result.SecretRequirements) != 2 {
		t.Fatalf("requirements = %#v, want two independently bindable credentials", inspect.Result.SecretRequirements)
	}
	if inspect.Result.SecretRequirements[0].ID == inspect.Result.SecretRequirements[1].ID ||
		inspect.Result.SecretRequirements[0].SuggestedName == inspect.Result.SecretRequirements[1].SuggestedName {
		t.Fatalf("repeated fields collapsed requirement identity: %#v", inspect.Result.SecretRequirements)
	}

	bindings := make(map[string]string, 2)
	expected := map[string]string{}
	for _, requirement := range inspect.Result.SecretRequirements {
		var logicalName, value string
		switch requirement.Key {
		case "http.body.data.0.password":
			logicalName, value = "BRUNO_PRIMARY_PASSWORD", "first-source-password"
		case "http.body.data.1.password":
			logicalName, value = "BRUNO_PROXY_PASSWORD", "second-source-password"
		default:
			t.Fatalf("unexpected requirement path: %#v", requirement)
		}
		bindings[requirement.ID] = logicalName
		expected[logicalName] = value
	}
	migrate := true
	response := commitBrunoImport(t, server, brunoCommitRequest{
		ProjectID:       brunoImportTestProject,
		ImportSessionID: inspect.ImportSessionID,
		Mode:            "create",
		SecretBindings:  bindings,
		MigrateSecrets:  &migrate,
	})
	script := decodeCommittedBrunoScript(t, response.Body.Bytes())
	if len(script.Config.Bruno.SecretBindings) != 2 {
		t.Fatalf("committed bindings = %#v, want two aliases", script.Config.Bruno.SecretBindings)
	}
	for _, requirement := range inspect.Result.SecretRequirements {
		wantReference := "{{secret." + bindings[requirement.ID] + "}}"
		if got := script.Config.Bruno.SecretBindings[requirement.SuggestedName]; got != wantReference {
			t.Fatalf("binding %q = %q, want %q", requirement.SuggestedName, got, wantReference)
		}
	}
	for logicalName, wantValue := range expected {
		value, _, err := server.projectSecretStore.Get(context.Background(), brunoImportTestProject, logicalName)
		if err != nil || value != wantValue {
			t.Fatalf("migrated %s = %q, %v; want %q", logicalName, value, err, wantValue)
		}
	}
}

func TestBrunoRollbackBlocksPreUpgradeRevisionWithPlaintextSecretBinding(t *testing.T) {
	server, store, _ := newBrunoImportAPITestServer(t)
	const (
		scriptID     = "legacy-bruno-rollback"
		legacySecret = "legacy-bruno-plaintext-never-restore"
	)
	commitFixture := func(revisionID, rawID, sanitizedID string, bindings map[string]string) *storage.BrunoImportCommit {
		return &storage.BrunoImportCommit{
			Script: &dsl.Script{
				ID: scriptID, Name: "Legacy Bruno rollback", Version: "1.0.0", Protocol: "bruno", ProjectID: brunoImportTestProject,
				Config:  dsl.ScriptConfig{Bruno: dsl.BrunoConfig{SecretBindings: bindings}},
				Actions: []dsl.Action{{Name: "Run", Type: "bruno_run", Target: "/"}},
			},
			RawArtifact: storage.BrunoSourceArtifact{
				ID: rawID, ProjectID: brunoImportTestProject, ArtifactKind: "raw", BackendKey: "legacy/" + rawID,
				SHA256: "sha-" + rawID, SizeBytes: 1,
			},
			SanitizedArtifact: storage.BrunoSourceArtifact{
				ID: sanitizedID, ProjectID: brunoImportTestProject, ArtifactKind: "sanitized", BackendKey: "legacy/" + sanitizedID,
				SHA256: "sha-" + sanitizedID, SizeBytes: 1,
			},
			Revision: storage.BrunoSourceRevision{ID: revisionID, SourceFormat: "bruno"},
		}
	}

	legacy := commitFixture("legacy-revision", "legacy-raw", "legacy-sanitized", map[string]string{"TOKEN": legacySecret})
	if _, err := store.CommitBrunoImport(legacy); err != nil {
		t.Fatalf("seed legacy revision: %v", err)
	}
	current, err := store.GetScript(scriptID)
	if err != nil {
		t.Fatal(err)
	}
	safe := commitFixture("safe-revision", "safe-raw", "safe-sanitized", nil)
	safe.ExpectedScriptUpdatedAt = &current.UpdatedAt
	safe.ExpectedActiveRevisionID = "legacy-revision"
	if _, err := store.CommitBrunoImport(safe); err != nil {
		t.Fatalf("seed safe active revision: %v", err)
	}

	payload, err := json.Marshal(map[string]string{
		"revision_id":                 "legacy-revision",
		"expected_active_revision_id": "safe-revision",
	})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts/"+url.PathEscape(scriptID)+"/bruno-imports/rollback",
		bytes.NewReader(payload),
	))
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), secretBindingsRequiredCode) {
		t.Fatalf("rollback status = %d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), legacySecret) {
		t.Fatalf("rollback error disclosed legacy secret: %s", response.Body.String())
	}
	active, err := store.GetActiveBrunoSourceRevision(scriptID, brunoImportTestProject)
	if err != nil {
		t.Fatal(err)
	}
	if active.ID != "safe-revision" {
		t.Fatalf("active revision = %q, want safe-revision", active.ID)
	}
	stored, err := store.GetScript(scriptID)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(legacySecret)) {
		t.Fatalf("blocked rollback restored legacy secret: %s", encoded)
	}
}

func TestBrunoImportSavesSuggestedDeferredBindingWhenMigrationIsDisabled(t *testing.T) {
	server, _, _ := newBrunoImportAPITestServer(t)
	source := []byte(`info: {name: Secret, type: http}
http:
  method: GET
  url: https://example.test
  auth: {type: bearer, token: literal-token}
`)
	inspect := inspectBrunoJSON(t, server, source, "")
	if len(inspect.Result.SecretRequirements) != 1 {
		t.Fatalf("secret requirements = %#v", inspect.Result.SecretRequirements)
	}
	requirement := inspect.Result.SecretRequirements[0]
	if requirement.SourceValueAvailable {
		t.Fatal("source value was advertised while migration policy was disabled")
	}
	migrateSecrets := false
	request := brunoCommitRequest{
		ProjectID:       brunoImportTestProject,
		ImportSessionID: inspect.ImportSessionID,
		Mode:            "create",
		MigrateSecrets:  &migrateSecrets,
	}
	response := commitBrunoImport(t, server, request)
	script := decodeCommittedBrunoScript(t, response.Body.Bytes())
	wantBinding := "{{secret." + requirement.SuggestedName + "}}"
	if got := script.Config.Bruno.SecretBindings[requirement.SuggestedName]; got != wantBinding {
		t.Fatalf("deferred binding = %q, want %q", got, wantBinding)
	}
	if strings.Contains(response.Body.String(), "literal-token") {
		t.Fatal("deferred commit response disclosed the source credential")
	}
	if _, _, err := server.projectSecretStore.Get(context.Background(), brunoImportTestProject, requirement.SuggestedName); !errors.Is(err, secretstore.ErrNotFound) {
		t.Fatalf("deferred commit unexpectedly created a secret: %v", err)
	}
}

func TestBrunoImportReusesExistingSourceSecretReferenceWithoutMigration(t *testing.T) {
	server, _, _ := newBrunoImportAPITestServer(t)
	inspect := inspectBrunoJSON(t, server, []byte(`info: {name: Existing reference, type: http}
http:
  method: GET
  url: https://example.test
  headers:
    - name: Authorization
      value: 'Bearer {{secret.api-token}}'
`), "")
	if len(inspect.Result.SecretRequirements) != 1 {
		t.Fatalf("secret requirements = %#v", inspect.Result.SecretRequirements)
	}
	requirement := inspect.Result.SecretRequirements[0]
	if !requirement.ExistingReference || requirement.SuggestedName != "API_TOKEN" ||
		requirement.SourceValueAvailable || requirement.Value != "" {
		t.Fatalf("existing reference requirement = %#v", requirement)
	}

	response := commitBrunoImport(t, server, brunoCommitRequest{
		ProjectID:       brunoImportTestProject,
		ImportSessionID: inspect.ImportSessionID,
		Mode:            "create",
	})
	script := decodeCommittedBrunoScript(t, response.Body.Bytes())
	if got := script.Config.Bruno.SecretBindings["API_TOKEN"]; got != "{{secret.API_TOKEN}}" {
		t.Fatalf("reused binding = %q", got)
	}
	if _, _, err := server.projectSecretStore.Get(context.Background(), brunoImportTestProject, "API_TOKEN"); !errors.Is(err, secretstore.ErrNotFound) {
		t.Fatalf("existing reference import unexpectedly migrated a value: %v", err)
	}
	statusRequest := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/"+script.ID+"/secret-status", nil)
	statusRequest.SetPathValue("id", script.ID)
	statusResponse := httptest.NewRecorder()
	server.mux.ServeHTTP(statusResponse, statusRequest)
	if statusResponse.Code != http.StatusOK || !strings.Contains(statusResponse.Body.String(), `"missing":["API_TOKEN"]`) {
		t.Fatalf("secret status = %d %s", statusResponse.Code, statusResponse.Body.String())
	}
}

func TestGroupedBrunoRequestedBindingsRejectsConflictingDuplicateReferenceTargets(t *testing.T) {
	requirements := []brunoconvert.SecretRequirement{
		{ID: "first", SuggestedName: "API_TOKEN", ExistingReference: true},
		{ID: "second", SuggestedName: "API_TOKEN", ExistingReference: true},
	}
	grouped, err := groupedBrunoRequestedBindings(requirements, map[string]string{
		"first":  "project-api-token",
		"second": "PROJECT.API.TOKEN",
	})
	if err != nil || grouped["API_TOKEN"] != "PROJECT_API_TOKEN" {
		t.Fatalf("compatible aliases were not grouped: %#v, %v", grouped, err)
	}
	if _, err := groupedBrunoRequestedBindings(requirements, map[string]string{
		"first":  "FIRST_TOKEN",
		"second": "SECOND_TOKEN",
	}); err == nil || !strings.Contains(err.Error(), "cannot target multiple") {
		t.Fatalf("conflicting grouped binding error = %v", err)
	}
}

func TestBrunoImportCommitPersistsSelectedServerDerivedParameterization(t *testing.T) {
	server, store, _ := newBrunoImportAPITestServer(t)
	inspect := inspectBrunoJSON(t, server, []byte(`info: {name: Parameterized, type: http}
http:
  method: GET
  url: https://example.test
`), "")

	var selectedID string
	rewriteBrunoImportSessionForTest(t, server, inspect.ImportSessionID, func(session *brunoImportSession) {
		if session.Result == nil || session.Result.Script == nil || len(session.Result.Script.Actions) == 0 {
			t.Fatalf("inspect session has no canonical action: %#v", session)
		}
		session.Result.Script.Actions[0].Target = "/users?user_id=12345"
		trusted := buildTrustedImportParameterization(session.Result.Script)
		for _, suggestion := range trusted.Suggestions {
			if suggestion.VarName == "user_id" {
				selectedID = suggestion.ID
				break
			}
		}
		if selectedID == "" {
			t.Fatalf("rewritten Bruno session produced no user_id suggestion: %#v", trusted.Suggestions)
		}
	})

	response := commitBrunoImport(t, server, brunoCommitRequest{
		ProjectID:           brunoImportTestProject,
		ImportSessionID:     inspect.ImportSessionID,
		Mode:                "create",
		ParameterizationIDs: []string{selectedID},
	})
	committed := decodeCommittedBrunoScript(t, response.Body.Bytes())
	stored, err := store.GetScript(committed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stored.Actions[0].Target, "user_id={{var.user_id}}") {
		t.Fatalf("stored Bruno action target = %q, want server-applied parameter", stored.Actions[0].Target)
	}
	if len(stored.DataSources) != 1 || len(stored.Parameters) != 1 {
		t.Fatalf("stored Bruno parameter material = data_sources:%#v parameters:%#v", stored.DataSources, stored.Parameters)
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["parameterization_applied"] != float64(1) {
		t.Fatalf("parameterization_applied = %#v, want 1", payload["parameterization_applied"])
	}
}

func TestBrunoImportRejectsStaleParameterizationWithoutConsumingSession(t *testing.T) {
	server, _, _ := newBrunoImportAPITestServer(t)
	inspect := inspectBrunoJSON(t, server, []byte(`info: {name: Retry parameterization, type: http}
http:
  method: GET
  url: https://example.test
`), "")

	var selectedID string
	rewriteBrunoImportSessionForTest(t, server, inspect.ImportSessionID, func(session *brunoImportSession) {
		if session.Result == nil || session.Result.Script == nil || len(session.Result.Script.Actions) == 0 {
			t.Fatalf("inspect session has no canonical action: %#v", session)
		}
		session.Result.Script.Actions[0].Target = "/users?user_id=12345"
		trusted := buildTrustedImportParameterization(session.Result.Script)
		if len(trusted.Suggestions) == 0 {
			t.Fatal("rewritten Bruno session produced no trusted suggestions")
		}
		selectedID = trusted.Suggestions[0].ID
	})

	stale := commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID:           brunoImportTestProject,
		ImportSessionID:     inspect.ImportSessionID,
		Mode:                "create",
		ParameterizationIDs: []string{"pz_stale_or_forged"},
	})
	if stale.Code != http.StatusConflict || !strings.Contains(stale.Body.String(), "stale or unavailable") {
		t.Fatalf("stale parameterization status = %d body=%s", stale.Code, stale.Body.String())
	}

	retry := commitBrunoImport(t, server, brunoCommitRequest{
		ProjectID:           brunoImportTestProject,
		ImportSessionID:     inspect.ImportSessionID,
		Mode:                "create",
		ParameterizationIDs: []string{selectedID},
	})
	if !strings.Contains(retry.Body.String(), `"parameterization_applied":1`) {
		t.Fatalf("retry did not apply retained-session parameterization: %s", retry.Body.String())
	}
}

func TestBrunoImportCommitAppliesDurableSecretBoundary(t *testing.T) {
	server, store, _ := newBrunoImportAPITestServer(t)
	source := []byte(`info: {name: Safe, type: http}
http:
  method: GET
  url: https://example.test
`)
	rewriteSession := func(t *testing.T, id string, mutate func(*brunoImportSession)) {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/bruno/commit", nil)
		session, err := server.consumeBrunoImportSession(request, id, brunoImportTestProject)
		if err != nil {
			t.Fatalf("consume test import session: %v", err)
		}
		mutate(session)
		if err := server.persistBrunoImportSession(context.Background(), session); err != nil {
			t.Fatalf("persist rewritten import session: %v", err)
		}
		server.storeBrunoImportSession(session)
	}

	t.Run("rejects executable plaintext", func(t *testing.T) {
		inspect := inspectBrunoJSON(t, server, source, "")
		rewriteSession(t, inspect.ImportSessionID, func(session *brunoImportSession) {
			if session.Result == nil || session.Result.Script == nil || len(session.Result.Script.Actions) == 0 {
				t.Fatalf("inspect session has no canonical action: %#v", session)
			}
			session.Result.Script.Actions[0].Headers = map[string]string{"Authorization": "Bearer bruno-plaintext-never-store"}
		})

		response := commitBrunoImportWithStatus(t, server, brunoCommitRequest{
			ProjectID:       brunoImportTestProject,
			ImportSessionID: inspect.ImportSessionID,
			Mode:            "create",
		})
		if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), secretBindingsRequiredCode) {
			t.Fatalf("commit status = %d body=%s", response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), "bruno-plaintext-never-store") {
			t.Fatalf("commit error disclosed plaintext credential: %s", response.Body.String())
		}
		scripts, err := store.ListScripts(brunoImportTestProject, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(scripts) != 0 {
			t.Fatalf("blocked Bruno commit persisted scripts: %#v", scripts)
		}
	})

	t.Run("projects source review metadata", func(t *testing.T) {
		inspect := inspectBrunoJSON(t, server, source, "")
		const reviewSecret = "bruno-review-metadata-never-store"
		rewriteSession(t, inspect.ImportSessionID, func(session *brunoImportSession) {
			if session.Result == nil || session.Result.Script == nil {
				t.Fatalf("inspect session has no canonical script: %#v", session)
			}
			session.Result.Script.Metadata = &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
				Source: "bruno",
				Preserved: map[string]any{
					"source_review": map[string]any{"authorization": "Bearer " + reviewSecret},
				},
			}}
		})

		response := commitBrunoImport(t, server, brunoCommitRequest{
			ProjectID:       brunoImportTestProject,
			ImportSessionID: inspect.ImportSessionID,
			Mode:            "create",
		})
		committed := decodeCommittedBrunoScript(t, response.Body.Bytes())
		stored, err := store.GetScript(committed.ID)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(stored)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(encoded, []byte(reviewSecret)) {
			t.Fatalf("Bruno commit persisted source-review plaintext: %s", encoded)
		}
	})
}

func TestBrunoImportSavesExplicitDeferredBindingName(t *testing.T) {
	server, _, _ := newBrunoImportAPITestServer(t)
	source := []byte(`info: {name: Secret, type: http}
http:
  method: GET
  url: https://example.test
  auth: {type: bearer, token: literal-token}
`)
	inspect := inspectBrunoJSON(t, server, source, "")
	if len(inspect.Result.SecretRequirements) != 1 {
		t.Fatalf("secret requirements = %#v", inspect.Result.SecretRequirements)
	}
	requirement := inspect.Result.SecretRequirements[0]
	response := commitBrunoImport(t, server, brunoCommitRequest{
		ProjectID:       brunoImportTestProject,
		ImportSessionID: inspect.ImportSessionID,
		Mode:            "create",
		SecretBindings:  map[string]string{requirement.ID: "CUSTOM_DEFERRED_TOKEN"},
	})
	script := decodeCommittedBrunoScript(t, response.Body.Bytes())
	if got := script.Config.Bruno.SecretBindings[requirement.SuggestedName]; got != "{{secret.CUSTOM_DEFERRED_TOKEN}}" {
		t.Fatalf("explicit deferred binding = %q", got)
	}
	if _, _, err := server.projectSecretStore.Get(context.Background(), brunoImportTestProject, "CUSTOM_DEFERRED_TOKEN"); !errors.Is(err, secretstore.ErrNotFound) {
		t.Fatalf("explicit deferred commit unexpectedly created a secret: %v", err)
	}
	if strings.Contains(response.Body.String(), "literal-token") {
		t.Fatal("explicit deferred commit response disclosed the source credential")
	}
}

func TestBrunoImportDefaultOffPolicyDefersExplicitMigrationWithoutDisclosure(t *testing.T) {
	server, _, _ := newBrunoImportAPITestServer(t)
	source := []byte(`info: {name: Secret, type: http}
http:
  method: GET
  url: https://example.test
  auth: {type: bearer, token: default-off-literal-token}
`)
	inspect := inspectBrunoJSON(t, server, source, "")
	if len(inspect.Result.SecretRequirements) != 1 {
		t.Fatalf("secret requirements = %#v", inspect.Result.SecretRequirements)
	}
	if inspect.Result.SecretRequirements[0].SourceValueAvailable {
		t.Fatal("source value was advertised while migration policy was disabled")
	}
	inspectPayload, err := json.Marshal(inspect)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(inspectPayload, []byte("default-off-literal-token")) {
		t.Fatal("inspect response disclosed the source credential")
	}

	migrateSecrets := true
	response := commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID:       brunoImportTestProject,
		ImportSessionID: inspect.ImportSessionID,
		Mode:            "create",
		MigrateSecrets:  &migrateSecrets,
	})
	if response.Code != http.StatusCreated {
		t.Fatalf("commit status = %d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "default-off-literal-token") {
		t.Fatal("deferred migration response disclosed the source credential")
	}
	requirement := inspect.Result.SecretRequirements[0]
	if _, _, err := server.projectSecretStore.Get(
		context.Background(), brunoImportTestProject, requirement.SuggestedName,
	); !errors.Is(err, secretstore.ErrNotFound) {
		t.Fatalf("deferred migration unexpectedly created a secret: %v", err)
	}
}

func TestBrunoImportPreflightsEverySecretBeforeWritingMigrations(t *testing.T) {
	server, _, _ := newBrunoImportAPITestServer(t)
	enableSourceSecretMigration(t, server)
	source := []byte(`info: {name: Secret, type: http}
http:
  method: GET
  url: https://example.test
  auth:
    type: basic
    username: imported-user
    password: imported-password
`)
	inspect := inspectBrunoJSON(t, server, source, "")
	if len(inspect.Result.SecretRequirements) != 2 {
		t.Fatalf("secret requirements = %#v", inspect.Result.SecretRequirements)
	}
	first, conflicting := inspect.Result.SecretRequirements[0], inspect.Result.SecretRequirements[1]
	racingStore := &brunoMigrationRaceStore{
		firstName:       first.SuggestedName,
		conflictingName: conflicting.SuggestedName,
	}
	server.projectSecretStore = racingStore
	migrateSecrets := true
	response := commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID:       brunoImportTestProject,
		ImportSessionID: inspect.ImportSessionID,
		Mode:            "create",
		MigrateSecrets:  &migrateSecrets,
	})
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "bruno_secret_migration_conflict") {
		t.Fatalf("commit status = %d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "imported-user") || strings.Contains(response.Body.String(), "imported-password") {
		t.Fatal("migration conflict response leaked an imported secret value")
	}
	if racingStore.puts != 0 {
		t.Fatalf("migration writes = %d, want zero before full preflight succeeds", racingStore.puts)
	}
}

func TestBrunoImportClaimsOneShotSessionBeforeSecretMigration(t *testing.T) {
	server, records, _ := newBrunoImportAPITestServer(t)
	enableSourceSecretMigration(t, server)
	inspect := inspectBrunoJSON(t, server, []byte(`info: {name: One shot race, type: http}
http:
  method: GET
  url: https://example.test
  auth: {type: bearer, token: one-shot-race-secret}
`), "")
	if len(inspect.Result.SecretRequirements) != 1 {
		t.Fatalf("secret requirements = %#v", inspect.Result.SecretRequirements)
	}
	requirementID := inspect.Result.SecretRequirements[0].ID
	blocking := &blockingCreateSecretStore{
		delegate: server.projectSecretStore,
		entered:  make(chan string, 2),
		release:  make(chan struct{}),
	}
	server.projectSecretStore = blocking
	migrate := true
	firstRequest := brunoCommitRequest{
		ProjectID: brunoImportTestProject, ImportSessionID: inspect.ImportSessionID,
		Mode: "create", ScriptID: "one-shot-first", MigrateSecrets: &migrate,
		SecretBindings: map[string]string{requirementID: "ONE_SHOT_FIRST"},
	}
	secondRequest := brunoCommitRequest{
		ProjectID: brunoImportTestProject, ImportSessionID: inspect.ImportSessionID,
		Mode: "create", ScriptID: "one-shot-second", MigrateSecrets: &migrate,
		SecretBindings: map[string]string{requirementID: "ONE_SHOT_SECOND"},
	}
	marshalRequest := func(request brunoCommitRequest) []byte {
		payload, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		return payload
	}
	firstBody := marshalRequest(firstRequest)
	secondBody := marshalRequest(secondRequest)
	serveCommit := func(body []byte) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/bruno/commit", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		server.mux.ServeHTTP(response, request)
		return response
	}
	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { firstDone <- serveCommit(firstBody) }()
	select {
	case name := <-blocking.entered:
		if name != "ONE_SHOT_FIRST" {
			t.Fatalf("first migration name = %q", name)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first migration did not reach the blocking create")
	}

	second := serveCommit(secondBody)
	if second.Code != http.StatusNotFound && second.Code != http.StatusConflict {
		t.Fatalf("concurrent commit status = %d body=%s", second.Code, second.Body.String())
	}
	if !strings.Contains(second.Body.String(), "bruno_import_session_consumed") && !strings.Contains(second.Body.String(), "bruno_import_session_claimed") {
		t.Fatalf("concurrent commit did not explain one-shot consumption: %s", second.Body.String())
	}
	close(blocking.release)
	var first *httptest.ResponseRecorder
	select {
	case first = <-firstDone:
	case <-time.After(5 * time.Second):
		t.Fatal("first commit did not complete")
	}
	if first.Code != http.StatusCreated {
		t.Fatalf("first commit status = %d body=%s", first.Code, first.Body.String())
	}
	if calls := blocking.createNames(); strings.Join(calls, ",") != "ONE_SHOT_FIRST" {
		t.Fatalf("secret creates = %#v, want only the winning binding", calls)
	}
	if _, _, err := blocking.delegate.Get(context.Background(), brunoImportTestProject, "ONE_SHOT_SECOND"); !errors.Is(err, secretstore.ErrNotFound) {
		t.Fatalf("losing binding lookup = %v, want not found", err)
	}
	if _, err := records.GetScript("one-shot-first"); err != nil {
		t.Fatalf("winning script: %v", err)
	}
	if _, err := records.GetScript("one-shot-second"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("losing script lookup = %v, want not found", err)
	}
}

func TestBrunoImportAutomaticMigrationConflictsWithEqualExistingSecret(t *testing.T) {
	server, _, _ := newBrunoImportAPITestServer(t)
	enableSourceSecretMigration(t, server)
	const sourceValue = "already-present-token"
	inspect := inspectBrunoJSON(t, server, []byte(`info: {name: Existing, type: http}
http:
  method: GET
  url: https://example.test
  auth: {type: bearer, token: `+sourceValue+`}
`), "")
	if len(inspect.Result.SecretRequirements) != 1 {
		t.Fatalf("secret requirements = %#v", inspect.Result.SecretRequirements)
	}
	name := inspect.Result.SecretRequirements[0].SuggestedName
	if _, err := server.projectSecretStore.Create(context.Background(), brunoImportTestProject, name, sourceValue, "test"); err != nil {
		t.Fatal(err)
	}
	migrateSecrets := true
	response := commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID:       brunoImportTestProject,
		ImportSessionID: inspect.ImportSessionID,
		Mode:            "create",
		MigrateSecrets:  &migrateSecrets,
	})
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "bruno_secret_migration_conflict") {
		t.Fatalf("commit status = %d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), sourceValue) {
		t.Fatal("migration conflict response disclosed the source value")
	}
}

func TestBrunoRevisionPrivateKeySourceRestriction(t *testing.T) {
	if brunoRevisionHasPrivateKeyFiles(nil) {
		t.Fatal("nil revision reported private-key files")
	}
	revision := &storage.BrunoSourceRevision{SecurityProfile: map[string]any{
		"capabilities": map[string]any{"has_private_key_files": true},
	}}
	if !brunoRevisionHasPrivateKeyFiles(revision) {
		t.Fatal("private-key capability was not detected")
	}
}

func TestBrunoImportReportsPartialSecretMigrationWithoutCompensatingCreates(t *testing.T) {
	server, records, _ := newBrunoImportAPITestServer(t)
	enableSourceSecretMigration(t, server)
	const username = "bruno-partial-source-user-never-return"
	const password = "bruno-partial-source-password-never-return"
	inspect := inspectBrunoJSON(t, server, []byte(`info: {name: Partial, type: http}
http:
  method: GET
  url: https://example.test
  auth:
    type: basic
    username: `+username+`
    password: `+password+`
`), "")
	bindings := make(map[string]string, len(inspect.Result.SecretRequirements))
	for _, requirement := range inspect.Result.SecretRequirements {
		switch requirement.Key {
		case "http.auth.username":
			bindings[requirement.ID] = "BRUNO_PARTIAL_A"
		case "http.auth.password":
			bindings[requirement.ID] = "BRUNO_PARTIAL_B"
		default:
			t.Fatalf("unexpected secret requirement: %#v", requirement)
		}
	}
	underlying := server.projectSecretStore
	failing := &failNthCreateSecretStore{delegate: underlying, failOnCall: 2}
	server.projectSecretStore = failing
	migrateSecrets := true
	request := brunoCommitRequest{
		ProjectID:       brunoImportTestProject,
		ImportSessionID: inspect.ImportSessionID,
		Mode:            "create",
		ScriptID:        "bruno-partial-provider-failure",
		SecretBindings:  bindings,
		MigrateSecrets:  &migrateSecrets,
	}
	response := commitBrunoImportWithStatus(t, server, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("partial migration status = %d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Code                        string                 `json:"code"`
		Partial                     bool                   `json:"partial"`
		ScriptCommitted             bool                   `json:"script_committed"`
		Migrated                    []secretstore.Metadata `json:"migrated"`
		MigratedCount               int                    `json:"migrated_count"`
		CommittedNames              []string               `json:"committed_names"`
		RemainingNames              []string               `json:"remaining_names"`
		FailedName                  string                 `json:"failed_name"`
		Reason                      string                 `json:"reason"`
		ImportSessionConsumed       bool                   `json:"import_session_consumed"`
		SourceValuesCleared         bool                   `json:"source_values_cleared"`
		ManualConfigurationRequired bool                   `json:"manual_configuration_required"`
		CommittedSecretsRetained    bool                   `json:"committed_secrets_retained"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Code != "bruno_secret_migration_partial" || !payload.Partial || payload.ScriptCommitted ||
		payload.MigratedCount != 1 || len(payload.Migrated) != 1 || payload.Migrated[0].Name != "BRUNO_PARTIAL_A" ||
		strings.Join(payload.CommittedNames, ",") != "BRUNO_PARTIAL_A" || strings.Join(payload.RemainingNames, ",") != "BRUNO_PARTIAL_B" ||
		payload.FailedName != "BRUNO_PARTIAL_B" || payload.Reason != "provider_error" || payload.ImportSessionConsumed ||
		payload.SourceValuesCleared || !payload.ManualConfigurationRequired || !payload.CommittedSecretsRetained {
		t.Fatalf("partial migration payload = %+v body=%s", payload, response.Body.String())
	}
	if strings.Contains(response.Body.String(), username) || strings.Contains(response.Body.String(), password) {
		t.Fatalf("partial migration response disclosed source values: %s", response.Body.String())
	}
	value, _, err := underlying.Get(context.Background(), brunoImportTestProject, "BRUNO_PARTIAL_A")
	if err != nil || value != username {
		t.Fatalf("committed partial value = %q, %v", value, err)
	}
	if _, _, err := underlying.Get(context.Background(), brunoImportTestProject, "BRUNO_PARTIAL_B"); !errors.Is(err, secretstore.ErrNotFound) {
		t.Fatalf("failed partial value lookup = %v, want not found", err)
	}
	if _, err := records.GetScript(request.ScriptID); err == nil {
		t.Fatal("script was committed after partial secret migration failure")
	}
	retry := commitBrunoImportWithStatus(t, server, request)
	if retry.Code != http.StatusCreated || failing.createCalls < 3 {
		t.Fatalf("retained partial migration retry = %d calls=%d body=%s", retry.Code, failing.createCalls, retry.Body.String())
	}
	value, _, err = underlying.Get(context.Background(), brunoImportTestProject, "BRUNO_PARTIAL_A")
	if err != nil || value != username {
		t.Fatalf("retry changed committed partial value = %q, %v", value, err)
	}
	events, err := records.ListAuditEvents(storage.AuditEventFilter{ProjectID: brunoImportTestProject, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	encodedEvents, _ := json.Marshal(events)
	if strings.Contains(string(encodedEvents), username) || strings.Contains(string(encodedEvents), password) {
		t.Fatalf("partial migration audit disclosed source values: %s", encodedEvents)
	}
}

func TestBrunoImportReportsCommittedSecretsWhenScriptCommitFails(t *testing.T) {
	server, records, _ := newBrunoImportAPITestServer(t)
	enableSourceSecretMigration(t, server)
	const username = "bruno-commit-source-user-never-return"
	const password = "bruno-commit-source-password-never-return"
	inspect := inspectBrunoJSON(t, server, []byte(`info: {name: Commit failure, type: http}
http:
  method: GET
  url: https://example.test
  auth:
    type: basic
    username: `+username+`
    password: `+password+`
`), "")
	bindings := make(map[string]string, len(inspect.Result.SecretRequirements))
	for _, requirement := range inspect.Result.SecretRequirements {
		switch requirement.Key {
		case "http.auth.username":
			bindings[requirement.ID] = "BRUNO_COMMIT_A"
		case "http.auth.password":
			bindings[requirement.ID] = "BRUNO_COMMIT_B"
		default:
			t.Fatalf("unexpected secret requirement: %#v", requirement)
		}
	}
	brunoStore, ok := any(records).(storage.BrunoImportStore)
	if !ok {
		t.Fatal("test store does not implement BrunoImportStore")
	}
	const commitFailure = "simulated commit detail must not reach response"
	server.store = &failingBrunoCommitStore{Store: records, delegate: brunoStore, err: errors.New(commitFailure)}
	migrateSecrets := true
	request := brunoCommitRequest{
		ProjectID:       brunoImportTestProject,
		ImportSessionID: inspect.ImportSessionID,
		Mode:            "create",
		ScriptID:        "bruno-script-commit-failure",
		SecretBindings:  bindings,
		MigrateSecrets:  &migrateSecrets,
	}
	response := commitBrunoImportWithStatus(t, server, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("script commit failure status = %d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Code                        string                 `json:"code"`
		Partial                     bool                   `json:"partial"`
		ScriptCommitted             bool                   `json:"script_committed"`
		Migrated                    []secretstore.Metadata `json:"migrated"`
		MigratedCount               int                    `json:"migrated_count"`
		CommittedNames              []string               `json:"committed_names"`
		RemainingNames              []string               `json:"remaining_names"`
		Reason                      string                 `json:"reason"`
		ImportSessionConsumed       bool                   `json:"import_session_consumed"`
		SourceValuesCleared         bool                   `json:"source_values_cleared"`
		ManualConfigurationRequired bool                   `json:"manual_configuration_required"`
		CommittedSecretsRetained    bool                   `json:"committed_secrets_retained"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Code != "bruno_import_commit_failed_after_secret_migration" || !payload.Partial || payload.ScriptCommitted ||
		payload.MigratedCount != 2 || len(payload.Migrated) != 2 || strings.Join(payload.CommittedNames, ",") != "BRUNO_COMMIT_A,BRUNO_COMMIT_B" ||
		len(payload.RemainingNames) != 0 || payload.Reason != "storage_error" || payload.ImportSessionConsumed ||
		payload.SourceValuesCleared || payload.ManualConfigurationRequired || !payload.CommittedSecretsRetained {
		t.Fatalf("script commit partial payload = %+v body=%s", payload, response.Body.String())
	}
	if strings.Contains(response.Body.String(), username) || strings.Contains(response.Body.String(), password) || strings.Contains(response.Body.String(), commitFailure) {
		t.Fatalf("script commit failure response disclosed protected detail: %s", response.Body.String())
	}
	for name, expected := range map[string]string{"BRUNO_COMMIT_A": username, "BRUNO_COMMIT_B": password} {
		value, _, err := server.projectSecretStore.Get(context.Background(), brunoImportTestProject, name)
		if err != nil || value != expected {
			t.Fatalf("committed secret %s = %q, %v", name, value, err)
		}
	}
	if _, err := records.GetScript(request.ScriptID); err == nil {
		t.Fatal("script row persisted despite forced transactional commit failure")
	}
	retry := commitBrunoImportWithStatus(t, server, request)
	if retry.Code != http.StatusInternalServerError {
		t.Fatalf("retained script-commit retry status = %d body=%s", retry.Code, retry.Body.String())
	}
	for name, expected := range map[string]string{"BRUNO_COMMIT_A": username, "BRUNO_COMMIT_B": password} {
		value, _, err := server.projectSecretStore.Get(context.Background(), brunoImportTestProject, name)
		if err != nil || value != expected {
			t.Fatalf("retry changed committed secret %s = %q, %v", name, value, err)
		}
	}
	events, err := records.ListAuditEvents(storage.AuditEventFilter{ProjectID: brunoImportTestProject, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	encodedEvents, _ := json.Marshal(events)
	if strings.Contains(string(encodedEvents), username) || strings.Contains(string(encodedEvents), password) || strings.Contains(string(encodedEvents), commitFailure) {
		t.Fatalf("script commit failure audit disclosed protected detail: %s", encodedEvents)
	}
}

func TestBrunoImportSessionSurvivesValidationFailure(t *testing.T) {
	server, _, _ := newBrunoImportAPITestServer(t)
	inspect := inspectBrunoJSON(t, server, []byte(`info: {name: Once, type: http}
http: {method: GET, url: https://example.test}
`), "")
	failed := commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID:       brunoImportTestProject,
		ImportSessionID: inspect.ImportSessionID,
		Mode:            "invalid",
	})
	if failed.Code != http.StatusBadRequest {
		t.Fatalf("first commit status = %d body=%s", failed.Code, failed.Body.String())
	}
	retry := commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID:       brunoImportTestProject,
		ImportSessionID: inspect.ImportSessionID,
		Mode:            "create",
	})
	if retry.Code != http.StatusCreated {
		t.Fatalf("retried commit status = %d, want 201 body=%s", retry.Code, retry.Body.String())
	}
	consumed := commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID:       brunoImportTestProject,
		ImportSessionID: inspect.ImportSessionID,
		Mode:            "create",
	})
	if consumed.Code != http.StatusNotFound {
		t.Fatalf("committed session remained reusable: status=%d body=%s", consumed.Code, consumed.Body.String())
	}
}

func TestBrunoImportSessionScopeMismatchDoesNotConsumeOwnerSession(t *testing.T) {
	server, _, _ := newBrunoImportAPITestServer(t)
	source := []byte("info: {name: Scoped, type: http}\nhttp: {method: GET, url: https://example.test}\n")
	ownerRequest := func() *http.Request {
		return httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/bruno/commit", nil)
	}
	consumeAsOwner := func(sessionID string) {
		session, err := server.consumeBrunoImportSession(ownerRequest(), sessionID, brunoImportTestProject)
		if err != nil {
			t.Fatalf("owner consume after rejected scope attempt: %v", err)
		}
		server.releaseBrunoImportSession(session, false, true)
	}

	t.Run("actor", func(t *testing.T) {
		inspect := inspectBrunoJSON(t, server, source, "")
		wrongActor := withAuthenticatedContext(ownerRequest(), authResolution{User: &AuthenticatedUser{Subject: "other-actor"}})
		if _, err := server.consumeBrunoImportSession(wrongActor, inspect.ImportSessionID, brunoImportTestProject); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("wrong-actor consume error = %v, want not found", err)
		}
		consumeAsOwner(inspect.ImportSessionID)
	})

	t.Run("project", func(t *testing.T) {
		inspect := inspectBrunoJSON(t, server, source, "")
		if _, err := server.consumeBrunoImportSession(ownerRequest(), inspect.ImportSessionID, "another-project"); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("wrong-project consume error = %v, want not found", err)
		}
		consumeAsOwner(inspect.ImportSessionID)
	})
}

func TestBoundedBrunoImportSecretValues(t *testing.T) {
	requirements := func(count, valueBytes int) []brunoconvert.SecretRequirement {
		result := make([]brunoconvert.SecretRequirement, count)
		for index := range result {
			result[index] = brunoconvert.SecretRequirement{
				ID:    fmt.Sprintf("requirement-%d", index),
				Value: strings.Repeat("s", valueBytes),
			}
		}
		return result
	}
	tests := []struct {
		name         string
		requirements []brunoconvert.SecretRequirement
		wantError    string
	}{
		{name: "maximum value", requirements: requirements(1, projectSecretMaxValueBytes)},
		{name: "value too large", requirements: requirements(1, projectSecretMaxValueBytes+1), wantError: "byte limit"},
		{name: "maximum aggregate", requirements: requirements(sourceSecretSessionMaxTotalBytes/projectSecretMaxValueBytes, projectSecretMaxValueBytes)},
		{name: "aggregate too large", requirements: append(requirements(sourceSecretSessionMaxTotalBytes/projectSecretMaxValueBytes, projectSecretMaxValueBytes), brunoconvert.SecretRequirement{ID: "overflow", Value: "x"}), wantError: "session limit"},
		{name: "maximum entries", requirements: requirements(projectSecretMaxBatchItems, 1)},
		{name: "too many entries", requirements: requirements(projectSecretMaxBatchItems+1, 1), wantError: "entry session limit"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values, err := boundedBrunoImportSecretValues(test.requirements)
			if test.wantError == "" {
				if err != nil || len(values) != len(test.requirements) {
					t.Fatalf("bounded values count=%d error=%v", len(values), err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error = %v, want substring %q", err, test.wantError)
			}
		})
	}
}

func TestBrunoImportDropsOversizedMigrationValueWithoutBlockingDeferredImport(t *testing.T) {
	server, _, _ := newBrunoImportAPITestServer(t)
	enableSourceSecretMigration(t, server)
	source := []byte("info: {name: Oversized, type: http}\nhttp:\n  method: GET\n  url: https://example.test\n  auth:\n    type: bearer\n    token: " + strings.Repeat("x", projectSecretMaxValueBytes+1) + "\n")
	inspect := inspectBrunoJSON(t, server, source, "")
	t.Cleanup(func() { server.deleteBrunoImportSession(inspect.ImportSessionID) })
	if len(inspect.Result.SecretRequirements) != 1 {
		t.Fatalf("secret requirements = %#v", inspect.Result.SecretRequirements)
	}
	if inspect.Result.SecretRequirements[0].SourceValueAvailable {
		t.Fatal("oversized source credential was retained for migration")
	}
}

func TestProjectDeletionSweepsPersistedBrunoSessionAfterControllerRestart(t *testing.T) {
	server, store, artifactDir := newBrunoImportAPITestServer(t)
	enableSourceSecretMigration(t, server)
	inspect := inspectBrunoJSON(t, server, []byte(`info: {name: Restart, type: http}
http:
  method: GET
  url: https://example.test
  auth: {type: bearer, token: restart-source-secret}
`), "")
	if inspect.ImportSessionID == "" {
		t.Fatal("inspect returned no import session")
	}
	server.brunoImportMu.Lock()
	for _, session := range server.brunoImportSessions {
		if session != nil && session.Timer != nil {
			session.Timer.Stop()
			session.Timer = nil
		}
	}
	server.brunoImportSessions = make(map[string]*brunoImportSession)
	server.brunoImportMu.Unlock()

	wrapper, err := brunoartifact.NewLocalAESKeyWrapper(brunoartifact.LocalKeyOptions{
		Key:   bytes.Repeat([]byte{0x5a}, 32),
		KeyID: "test-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	restarted := NewServer(ServerConfig{
		Store:                store,
		MaxConcurrent:        2,
		ImportAssetDir:       filepath.Dir(artifactDir),
		BrunoArtifactWrapper: wrapper,
	})
	response := httptest.NewRecorder()
	restarted.mux.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/api/v1/projects/"+brunoImportTestProject, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("delete project status = %d body=%s", response.Code, response.Body.String())
	}
	assertDirectoryHasNoRegularFiles(t, artifactDir)
}

func TestExpiredBrunoSessionJanitorDoesNotRaceCrashClaimAfterControllerRestart(t *testing.T) {
	server, store, artifactDir := newBrunoImportAPITestServer(t)
	enableSourceSecretMigration(t, server)
	inspect := inspectBrunoJSON(t, server, []byte(`info: {name: Expired, type: http}
http:
  method: GET
  url: https://example.test
  auth: {type: bearer, token: expired-source-secret}
`), "")
	server.brunoImportMu.Lock()
	for _, session := range server.brunoImportSessions {
		if session != nil && session.Timer != nil {
			session.Timer.Stop()
			session.Timer = nil
		}
	}
	server.brunoImportSessions = make(map[string]*brunoImportSession)
	server.brunoImportMu.Unlock()

	sessionDir := filepath.Join(artifactDir, ".sessions")
	descriptorPath := filepath.Join(sessionDir, inspect.ImportSessionID+".json")
	descriptorData, err := os.ReadFile(descriptorPath)
	if err != nil {
		t.Fatal(err)
	}
	var descriptor brunoartifact.SessionDescriptor
	if err := json.Unmarshal(descriptorData, &descriptor); err != nil {
		t.Fatal(err)
	}
	descriptor.ExpiresAt = time.Now().UTC().Add(-time.Minute)
	descriptorData, err = json.Marshal(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(descriptorPath, descriptorData, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(descriptorPath, filepath.Join(sessionDir, inspect.ImportSessionID+".claim.json")); err != nil {
		t.Fatal(err)
	}

	wrapper, err := brunoartifact.NewLocalAESKeyWrapper(brunoartifact.LocalKeyOptions{
		Key:   bytes.Repeat([]byte{0x5a}, 32),
		KeyID: "test-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	restarted := NewServer(ServerConfig{
		Store:                store,
		MaxConcurrent:        2,
		ImportAssetDir:       filepath.Dir(artifactDir),
		BrunoArtifactWrapper: wrapper,
	})
	if err := restarted.pruneExpiredBrunoImportSessions(time.Now().UTC()); err != nil {
		t.Fatalf("prune must ignore an in-flight claim: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, inspect.ImportSessionID+".claim.json")); err != nil {
		t.Fatalf("in-flight claim descriptor stat error = %v", err)
	}
	objects, err := restarted.brunoArtifactStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := objects.DeleteProject(context.Background(), brunoImportTestProject); err != nil {
		t.Fatalf("project cleanup must sweep abandoned claims: %v", err)
	}
	assertDirectoryHasNoRegularFiles(t, artifactDir)
}

func TestExpiredBrunoSessionJanitorDefersSourceCollectionDuringPublicationGrace(t *testing.T) {
	server, _, artifactDir := newBrunoImportAPITestServer(t)
	inspected := inspectBrunoJSON(t, server, []byte(`info: {name: Grace, type: http}
http: {method: GET, url: https://example.test/grace}
`), "")

	server.brunoImportMu.Lock()
	session := server.brunoImportSessions[inspected.ImportSessionID]
	if session == nil {
		server.brunoImportMu.Unlock()
		t.Fatal("inspect session was not retained")
	}
	if session.Timer != nil {
		session.Timer.Stop()
		session.Timer = nil
	}
	rawArtifact := session.RawArtifact
	server.brunoImportMu.Unlock()

	expiredAt := time.Now().UTC().Add(-time.Minute)
	expireBrunoSessionDescriptor(t, artifactDir, inspected.ImportSessionID, expiredAt)
	if err := server.pruneExpiredBrunoImportSessions(time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	objects, err := server.brunoArtifactStore()
	if err != nil {
		t.Fatal(err)
	}
	request := brunoartifact.OpenRequest{
		BackendKey: rawArtifact.BackendKey,
		ProjectID:  rawArtifact.ProjectID,
		Kind:       rawArtifact.ArtifactKind,
		SHA256:     rawArtifact.SHA256,
		SizeBytes:  rawArtifact.SizeBytes,
	}
	if _, err := objects.ReadAll(context.Background(), request, rawArtifact.SizeBytes); err != nil {
		t.Fatalf("source was collected during publication grace: %v", err)
	}
	descriptorPath := filepath.Join(artifactDir, ".sessions", inspected.ImportSessionID+".json")
	if _, err := os.Stat(descriptorPath); err != nil {
		t.Fatalf("reservation descriptor was removed during publication grace: %v", err)
	}

	afterGrace := expiredAt.Add(brunoArtifactCollectionGrace + time.Second)
	if err := server.pruneExpiredBrunoImportSessions(afterGrace); err != nil {
		t.Fatal(err)
	}
	if _, err := objects.ReadAll(context.Background(), request, rawArtifact.SizeBytes); err == nil {
		t.Fatal("unreferenced source remained after collection grace")
	}
}

func TestExpiredBrunoSessionJanitorFailsClosedWithoutDatabaseReferenceChecker(t *testing.T) {
	server, store, artifactDir := newBrunoImportAPITestServer(t)
	inspected := inspectBrunoJSON(t, server, []byte(`info: {name: Unknown references, type: http}
http: {method: GET, url: https://example.test/unknown}
`), "")

	server.brunoImportMu.Lock()
	session := server.brunoImportSessions[inspected.ImportSessionID]
	if session == nil {
		server.brunoImportMu.Unlock()
		t.Fatal("inspect session was not retained")
	}
	if session.Timer != nil {
		session.Timer.Stop()
		session.Timer = nil
	}
	rawArtifact := session.RawArtifact
	server.brunoImportMu.Unlock()

	expiredAt := time.Now().UTC().Add(-brunoArtifactCollectionGrace - time.Minute)
	expireBrunoSessionDescriptor(t, artifactDir, inspected.ImportSessionID, expiredAt)
	server.store = &brunoStoreWithoutArtifactReferenceChecker{Store: store}
	if err := server.pruneExpiredBrunoImportSessions(time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	objects, err := server.brunoArtifactStore()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := objects.ReadAll(context.Background(), brunoartifact.OpenRequest{
		BackendKey: rawArtifact.BackendKey,
		ProjectID:  rawArtifact.ProjectID,
		Kind:       rawArtifact.ArtifactKind,
		SHA256:     rawArtifact.SHA256,
		SizeBytes:  rawArtifact.SizeBytes,
	}, rawArtifact.SizeBytes); err != nil {
		t.Fatalf("unknown database reference state deleted source evidence: %v", err)
	}
	if _, err := os.Stat(filepath.Join(artifactDir, ".sessions", inspected.ImportSessionID+".json")); err != nil {
		t.Fatalf("cleanup locator descriptor was removed: %v", err)
	}
}

func expireBrunoSessionDescriptor(t *testing.T, artifactDir, sessionID string, expiresAt time.Time) {
	t.Helper()
	descriptorPath := filepath.Join(artifactDir, ".sessions", sessionID+".json")
	descriptorData, err := os.ReadFile(descriptorPath)
	if err != nil {
		t.Fatal(err)
	}
	var descriptor brunoartifact.SessionDescriptor
	if err := json.Unmarshal(descriptorData, &descriptor); err != nil {
		t.Fatal(err)
	}
	descriptor.ExpiresAt = expiresAt.UTC()
	descriptorData, err = json.Marshal(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(descriptorPath, descriptorData, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestBrunoImportMultipartDirectoryAndUnsafePath(t *testing.T) {
	server, _, _ := newBrunoImportAPITestServer(t)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("project_id", brunoImportTestProject); err != nil {
		t.Fatal(err)
	}
	writeBrunoMultipartFile(t, writer, "demo/opencollection.yml", []byte("opencollection: 1.0.0\ninfo: {name: Demo}\n"))
	writeBrunoMultipartFile(t, writer, "demo/request.yml", []byte("info: {name: Request, type: http}\nhttp: {method: GET, url: https://example.test}\n"))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/bruno/inspect?output_mode=native_requests", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("directory inspect status = %d body=%s", response.Code, response.Body.String())
	}
	var inspected brunoInspectResponse
	if err := json.Unmarshal(response.Body.Bytes(), &inspected); err != nil {
		t.Fatal(err)
	}
	if inspected.Result.RootPath != "demo" || inspected.Result.Collection.Name != "Demo" ||
		inspected.Result.OutputMode != brunoconvert.OutputModeNativeRequests {
		t.Fatalf("directory result = %#v", inspected.Result)
	}

	body.Reset()
	writer = multipart.NewWriter(&body)
	if err := writer.WriteField("project_id", brunoImportTestProject); err != nil {
		t.Fatal(err)
	}
	writeBrunoMultipartFile(t, writer, "../escape.yml", []byte("info: {name: Escape, type: http}\nhttp: {url: https://example.test}\n"))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/bruno/inspect", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response = httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(strings.ToLower(response.Body.String()), "escape") {
		t.Fatalf("unsafe-path status = %d body=%s", response.Code, response.Body.String())
	}
}

func TestApplyBrunoNativeSecretBindingsDoesNotCascade(t *testing.T) {
	script := &dsl.Script{
		Name:     "binding cascade",
		Protocol: "http",
		Actions: []dsl.Action{{
			Name: "request",
			Type: "request",
			HeaderList: dsl.HeaderList{
				{Name: "X-Password", Value: "{{secret.PASSWORD}}"},
				{Name: "X-Token", Value: "{{secret.TOKEN}}"},
			},
		}},
	}
	bound, err := applyBrunoNativeSecretBindings(script, map[string]string{
		"PASSWORD": "{{secret.TOKEN}}",
		"TOKEN":    "{{secret.FINAL_TOKEN}}",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := bound.Actions[0].HeaderList[0].Value; got != "{{secret.TOKEN}}" {
		t.Fatalf("PASSWORD binding cascaded to %q", got)
	}
	if got := bound.Actions[0].HeaderList[1].Value; got != "{{secret.FINAL_TOKEN}}" {
		t.Fatalf("TOKEN binding = %q", got)
	}
}

func TestBrunoRawUploadCanSelectNativeOutput(t *testing.T) {
	server, _, _ := newBrunoImportAPITestServer(t)
	source := []byte(`info: {name: Raw native, uid: raw-native, type: http}
http:
  method: GET
  url: https://example.test
`)
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts/import/bruno/inspect?project_id="+url.QueryEscape(brunoImportTestProject)+
			"&filename=request.yml&output_mode=native_requests",
		bytes.NewReader(source),
	)
	request.Header.Set("Content-Type", "application/octet-stream")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("native raw inspect status = %d body=%s", response.Code, response.Body.String())
	}
	var inspected brunoInspectResponse
	if err := json.Unmarshal(response.Body.Bytes(), &inspected); err != nil {
		t.Fatal(err)
	}
	if inspected.Result.OutputMode != brunoconvert.OutputModeNativeRequests ||
		len(inspected.Result.Scripts) != 1 {
		t.Fatalf("native raw result = %#v", inspected.Result)
	}
}

func TestBrunoJSONQueryCanSelectNativeOutputAndRejectsConflict(t *testing.T) {
	server, _, _ := newBrunoImportAPITestServer(t)
	source := "info: {name: JSON native, uid: json-native, type: http}\nhttp: {method: GET, url: https://example.test}\n"
	payload, err := json.Marshal(brunoInspectJSONRequest{
		ProjectID: brunoImportTestProject,
		Filename:  "request.yml",
		Content:   source,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts/import/bruno/inspect?output_mode=native_requests",
		bytes.NewReader(payload),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("JSON query native inspect status = %d body=%s", response.Code, response.Body.String())
	}
	var inspected brunoInspectResponse
	if err := json.Unmarshal(response.Body.Bytes(), &inspected); err != nil {
		t.Fatal(err)
	}
	if inspected.Result == nil || inspected.Result.OutputMode != brunoconvert.OutputModeNativeRequests {
		t.Fatalf("JSON query native result = %#v", inspected.Result)
	}

	conflictingPayload, err := json.Marshal(brunoInspectJSONRequest{
		ProjectID: brunoImportTestProject,
		Filename:  "request.yml",
		Content:   source,
		Options: brunoconvert.InspectOptions{
			OutputMode: brunoconvert.OutputModeCanonical,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts/import/bruno/inspect?output_mode=native_requests",
		bytes.NewReader(conflictingPayload),
	)
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "conflicts") {
		t.Fatalf("JSON output mode conflict status = %d body=%s", response.Code, response.Body.String())
	}
}

func TestCleanupUnreferencedBrunoArtifactsDeletesFailedPublication(t *testing.T) {
	server, _, _ := newBrunoImportAPITestServer(t)
	objects, err := server.brunoArtifactStore()
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("failed-publication-source")
	object, err := objects.PutBytes(context.Background(), brunoImportTestProject, "raw", payload, int64(len(payload)))
	if err != nil {
		t.Fatal(err)
	}
	artifact := storage.BrunoSourceArtifact{
		ProjectID: brunoImportTestProject, ArtifactKind: "raw",
		BackendKey: object.BackendKey, SHA256: object.SHA256, SizeBytes: object.SizeBytes,
	}
	server.cleanupUnreferencedBrunoArtifacts([]storage.BrunoSourceArtifact{artifact})
	if _, err := objects.ReadAll(
		context.Background(),
		brunoArtifactOpenRequest(artifact),
		int64(len(payload)),
	); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed-publication artifact survived cleanup: %v", err)
	}
}

func TestExpiredBrunoPublicationJournalRecoversInterruptedSourceWrite(t *testing.T) {
	server, _, _ := newBrunoImportAPITestServer(t)
	objects, err := server.brunoArtifactStore()
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("interrupted-source-write")
	planned, err := objects.PlanBytes(brunoImportTestProject, "raw", payload, int64(len(payload)))
	if err != nil {
		t.Fatal(err)
	}
	if err := objects.SavePublication(brunoartifact.PublicationDescriptor{
		ID:        "interrupted-source",
		ProjectID: brunoImportTestProject,
		ExpiresAt: time.Now().UTC().Add(-time.Minute),
		Objects:   []brunoartifact.OpenRequest{planned},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := objects.PutBytes(
		context.Background(), brunoImportTestProject, "raw", payload, int64(len(payload)),
	); err != nil {
		t.Fatal(err)
	}
	if err := server.pruneExpiredBrunoImportSessions(time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := objects.ReadAll(context.Background(), planned, int64(len(payload))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("interrupted source object survived publication recovery: %v", err)
	}
	if publications, err := objects.ListExpiredPublications(time.Now().UTC()); err != nil || len(publications) != 0 {
		t.Fatalf("recovered publication journal = %#v, %v", publications, err)
	}
}

func TestExpiredBrunoPublicationJournalFailsClosedWithoutReferenceChecker(t *testing.T) {
	server, store, _ := newBrunoImportAPITestServer(t)
	objects, err := server.brunoArtifactStore()
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("unknown-reference-source")
	planned, err := objects.PlanBytes(brunoImportTestProject, "raw", payload, int64(len(payload)))
	if err != nil {
		t.Fatal(err)
	}
	if err := objects.SavePublication(brunoartifact.PublicationDescriptor{
		ID:        "unknown-reference-publication",
		ProjectID: brunoImportTestProject,
		ExpiresAt: time.Now().UTC().Add(-time.Minute),
		Objects:   []brunoartifact.OpenRequest{planned},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := objects.PutBytes(
		context.Background(), brunoImportTestProject, "raw", payload, int64(len(payload)),
	); err != nil {
		t.Fatal(err)
	}
	server.store = &brunoStoreWithoutArtifactReferenceChecker{Store: store}
	if err := server.pruneExpiredBrunoImportSessions(time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := objects.ReadAll(context.Background(), planned, int64(len(payload))); err != nil {
		t.Fatalf("unknown reference state deleted source: %v", err)
	}
	publications, err := objects.ListExpiredPublications(time.Now().UTC())
	if err != nil || len(publications) != 1 || publications[0].ID != "unknown-reference-publication" {
		t.Fatalf("fail-closed publication journal = %#v, %v", publications, err)
	}
}

func TestBrunoSupportMatrixAdvertisesPinnedCompatibility(t *testing.T) {
	server := NewServer(ServerConfig{})
	response, support := requestBrunoSupportMatrix(t, server)
	if support.SecretStore.SourceSecretMigrationPolicyEnabled {
		t.Fatal("support matrix enabled source migration by default")
	}
	if !strings.Contains(response.Body.String(), brunoconvert.CompatibilityVersion) ||
		!strings.Contains(response.Body.String(), "WebSocket") {
		t.Fatalf("support matrix = %s", response.Body.String())
	}

	enableSourceSecretMigration(t, server)
	_, support = requestBrunoSupportMatrix(t, server)
	if !support.SecretStore.SourceSecretMigrationPolicyEnabled {
		t.Fatal("support matrix did not advertise the enabled source migration policy")
	}
}

func TestSummarizeBrunoIdentityChangesDistinguishesMovesRenamesAndDuplicates(t *testing.T) {
	changes := summarizeBrunoIdentityChanges(
		map[string]string{
			"old/request.bru":   "uid-moved",
			"same/old-name.bru": "uid-renamed",
			"dupe/a.bru":        "uid-duplicate",
			"dupe/b.bru":        "uid-duplicate",
		},
		map[string]string{
			"new/request.bru":   "uid-moved",
			"same/new-name.bru": "uid-renamed",
			"dupe/c.bru":        "uid-duplicate",
			"dupe/d.bru":        "uid-duplicate",
		},
	)
	counts, _ := changes["counts"].(map[string]int)
	if counts["moved"] != 1 || counts["renamed"] != 1 {
		t.Fatalf("change counts = %#v", counts)
	}
	if counts["added"] != 2 || counts["deleted"] != 2 {
		t.Fatalf("duplicate fallback identities were matched ambiguously: %#v", changes)
	}
}

type brunoMigrationRaceStore struct {
	mu              sync.Mutex
	firstName       string
	conflictingName string
	gets            map[string]int
	puts            int
}

type blockingCreateSecretStore struct {
	delegate secretstore.Store
	entered  chan string
	release  chan struct{}

	mu    sync.Mutex
	calls []string
}

func (s *blockingCreateSecretStore) Get(ctx context.Context, projectID, name string) (string, secretstore.Metadata, error) {
	return s.delegate.Get(ctx, projectID, name)
}

func (s *blockingCreateSecretStore) Create(ctx context.Context, projectID, name, value, actor string) (secretstore.Metadata, error) {
	s.mu.Lock()
	s.calls = append(s.calls, name)
	s.mu.Unlock()
	select {
	case s.entered <- name:
	case <-ctx.Done():
		return secretstore.Metadata{}, ctx.Err()
	}
	select {
	case <-s.release:
		return s.delegate.Create(ctx, projectID, name, value, actor)
	case <-ctx.Done():
		return secretstore.Metadata{}, ctx.Err()
	}
}

func (s *blockingCreateSecretStore) Replace(ctx context.Context, projectID, name, value, actor string) (secretstore.Metadata, error) {
	return s.delegate.Replace(ctx, projectID, name, value, actor)
}

func (s *blockingCreateSecretStore) List(ctx context.Context, projectID string) ([]secretstore.Metadata, error) {
	return s.delegate.List(ctx, projectID)
}

func (s *blockingCreateSecretStore) Writable() bool   { return s.delegate.Writable() }
func (s *blockingCreateSecretStore) Provider() string { return "blocking-" + s.delegate.Provider() }

func (s *blockingCreateSecretStore) createNames() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

type failingBrunoCommitStore struct {
	storage.Store
	delegate storage.BrunoImportStore
	err      error
}

func (s *failingBrunoCommitStore) CommitBrunoImport(*storage.BrunoImportCommit) (*storage.BrunoImportCommitResult, error) {
	return nil, s.err
}

func (s *failingBrunoCommitStore) GetBrunoSourceArtifact(id string, projectID string) (*storage.BrunoSourceArtifact, error) {
	return s.delegate.GetBrunoSourceArtifact(id, projectID)
}

func (s *failingBrunoCommitStore) GetBrunoSourceRevision(id string, projectID string) (*storage.BrunoSourceRevision, error) {
	return s.delegate.GetBrunoSourceRevision(id, projectID)
}

func (s *failingBrunoCommitStore) GetActiveBrunoSourceRevision(scriptID string, projectID string) (*storage.BrunoSourceRevision, error) {
	return s.delegate.GetActiveBrunoSourceRevision(scriptID, projectID)
}

func (s *failingBrunoCommitStore) ListBrunoSourceRevisions(scriptID string, projectID string) ([]*storage.BrunoSourceRevision, error) {
	return s.delegate.ListBrunoSourceRevisions(scriptID, projectID)
}

func (s *failingBrunoCommitStore) ActivateBrunoSourceRevision(scriptID string, projectID string, revisionID string, expectedActiveRevisionID string) (*storage.BrunoSourceRevision, error) {
	return s.delegate.ActivateBrunoSourceRevision(scriptID, projectID, revisionID, expectedActiveRevisionID)
}

type flakyBrunoArtifactReferenceStore struct {
	storage.Store
	fail bool
}

type brunoStoreWithoutArtifactReferenceChecker struct {
	storage.Store
}

func (s *flakyBrunoArtifactReferenceStore) IsBrunoArtifactBackendKeyReferenced(backendKey string) (bool, error) {
	if s.fail {
		return false, errors.New("temporary reference check failure")
	}
	checker, ok := s.Store.(storage.BrunoArtifactReferenceChecker)
	if !ok {
		return false, nil
	}
	return checker.IsBrunoArtifactBackendKeyReferenced(backendKey)
}

func (s *brunoMigrationRaceStore) Get(_ context.Context, projectID, name string) (string, secretstore.Metadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gets == nil {
		s.gets = map[string]int{}
	}
	s.gets[name]++
	if name == s.conflictingName && s.gets[name] >= 2 {
		return "existing-different-value", secretstore.Metadata{
			ProjectID: projectID,
			Name:      name,
			Provider:  "race-test",
			Writable:  true,
		}, nil
	}
	return "", secretstore.Metadata{}, secretstore.ErrNotFound
}

func (s *brunoMigrationRaceStore) Create(_ context.Context, projectID, name, _ string, _ string) (secretstore.Metadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.puts++
	return secretstore.Metadata{ProjectID: projectID, Name: name, Provider: "race-test", Writable: true}, nil
}

func (*brunoMigrationRaceStore) Replace(context.Context, string, string, string, string) (secretstore.Metadata, error) {
	return secretstore.Metadata{}, secretstore.ErrNotFound
}

func (*brunoMigrationRaceStore) List(context.Context, string) ([]secretstore.Metadata, error) {
	return nil, nil
}

func (*brunoMigrationRaceStore) Writable() bool   { return true }
func (*brunoMigrationRaceStore) Provider() string { return "race-test" }

func newBrunoImportAPITestServer(t *testing.T) (*Server, *storage.SQLiteStore, string) {
	t.Helper()
	root := t.TempDir()
	store, err := storage.NewSQLiteStore(filepath.Join(root, "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	if err := store.SaveProject(&storage.Project{
		ID: brunoImportTestProject, Name: "Bruno API", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	wrapper, err := brunoartifact.NewLocalAESKeyWrapper(brunoartifact.LocalKeyOptions{
		Key:   bytes.Repeat([]byte{0x5a}, 32),
		KeyID: "test-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	importAssetDir := filepath.Join(root, "import-assets")
	artifactDir := filepath.Join(importAssetDir, ".bruno-artifacts")
	server := NewServer(ServerConfig{
		Store:                store,
		MaxConcurrent:        2,
		ImportAssetDir:       importAssetDir,
		BrunoArtifactWrapper: wrapper,
	})
	return server, store, artifactDir
}

func enableSourceSecretMigration(t *testing.T, server *Server) {
	t.Helper()
	settings := server.runner.GetSystemSettings()
	settings.ScriptImports.AllowSourceSecretMigration = true
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("enable source secret migration: %v", err)
	}
}

func requestBrunoSupportMatrix(t *testing.T, server *Server) (*httptest.ResponseRecorder, struct {
	SecretStore struct {
		SourceSecretMigrationPolicyEnabled bool `json:"source_secret_migration_policy_enabled"`
	} `json:"secret_store"`
}) {
	t.Helper()
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/scripts/import/bruno/support-matrix", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	var support struct {
		SecretStore struct {
			SourceSecretMigrationPolicyEnabled bool `json:"source_secret_migration_policy_enabled"`
		} `json:"secret_store"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &support); err != nil {
		t.Fatal(err)
	}
	return response, support
}

func inspectBrunoJSON(t *testing.T, server *Server, source []byte, syncScriptID string) brunoInspectResponse {
	t.Helper()
	payload, err := json.Marshal(brunoInspectJSONRequest{
		ProjectID:    brunoImportTestProject,
		Filename:     "login.yml",
		Content:      string(source),
		SyncScriptID: syncScriptID,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/bruno/inspect", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("inspect status = %d body=%s", response.Code, response.Body.String())
	}
	var inspected brunoInspectResponse
	if err := json.Unmarshal(response.Body.Bytes(), &inspected); err != nil {
		t.Fatal(err)
	}
	return inspected
}

func TestBrunoInspectRejectsQueryBodyProjectMismatch(t *testing.T) {
	server, _, artifactDir := newBrunoImportAPITestServer(t)
	payload, err := json.Marshal(brunoInspectJSONRequest{
		ProjectID: "project-b",
		Filename:  "request.yml",
		Content:   "info: {name: Request, type: http}\nhttp: {method: GET, url: https://example.test}\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/bruno/inspect?project_id="+url.QueryEscape(brunoImportTestProject), bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "does not match") {
		t.Fatalf("mismatched inspect status=%d body=%s", response.Code, response.Body.String())
	}
	assertDirectoryHasNoRegularFiles(t, artifactDir)
}

func TestBrunoExecutionPreflightUsesCompatibleGeneratorInventory(t *testing.T) {
	server, _, _ := newBrunoImportAPITestServer(t)
	t.Setenv("APEXLOAD_BRUNO_EGRESS_BROKER_URL", "http://egress-broker.internal:8080")
	server.runner.setCachedGeneratorInventory([]GeneratorInventoryEntry{{
		Status: "ready", DrainState: "active", SupportedProtocols: []string{"http", "bruno"},
	}}, time.Now())
	preflight := server.brunoExecutionPreflight(context.Background(), "safe")
	if ready, _ := preflight["ready"].(bool); !ready {
		t.Fatalf("compatible Bruno preflight = %#v", preflight)
	}
}

func TestMergeBrunoSynchronizationKeepsFreshRunOptionsAndExistingTuning(t *testing.T) {
	existing := &dsl.Script{
		ID: "script-sync", Name: "Curated name", ProjectID: brunoImportTestProject,
		Config: dsl.ScriptConfig{
			Pacing: dsl.PacingConfig{Type: "fixed_delay", DelayMs: 900},
			Bruno:  dsl.BrunoConfig{DefaultEnvironment: "old", Sandbox: "safe", SecretBindings: map[string]string{"TOKEN": "{{secret.EXISTING}}"}},
		},
		Actions: []dsl.Action{{Name: "Curated action", Type: "bruno_run", Params: map[string]any{"selection": "collection"}}},
	}
	imported := &dsl.Script{
		Name: "Source name", ProjectID: brunoImportTestProject,
		Config:  dsl.ScriptConfig{Bruno: dsl.BrunoConfig{DefaultEnvironment: "new", Sandbox: "developer", PersistSessionAcrossIterations: true}},
		Actions: []dsl.Action{{Name: "Source action", Type: "bruno_run", Params: map[string]any{"selection": "request", "paths": []any{"orders/get.yml"}}}},
	}
	merged := mergeBrunoSynchronization(existing, imported)
	if merged.Config.Bruno.DefaultEnvironment != "new" || merged.Config.Bruno.Sandbox != "developer" || !merged.Config.Bruno.PersistSessionAcrossIterations {
		t.Fatalf("fresh Bruno config was discarded: %#v", merged.Config.Bruno)
	}
	if merged.Actions[0].Params["selection"] != "request" || merged.Config.Pacing.DelayMs != 900 || merged.Name != "Curated name" || merged.Actions[0].Name != "Curated action" {
		t.Fatalf("sync merge = %#v", merged)
	}
	if merged.Config.Bruno.SecretBindings["TOKEN"] != "{{secret.EXISTING}}" {
		t.Fatalf("secret bindings = %#v", merged.Config.Bruno.SecretBindings)
	}
}

func commitBrunoImport(t *testing.T, server *Server, request brunoCommitRequest) *httptest.ResponseRecorder {
	t.Helper()
	response := commitBrunoImportWithStatus(t, server, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("commit status = %d body=%s", response.Code, response.Body.String())
	}
	return response
}

func commitBrunoImportWithStatus(t *testing.T, server *Server, request brunoCommitRequest) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	httpRequest := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/bruno/commit", bytes.NewReader(payload))
	httpRequest.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, httpRequest)
	return response
}

func decodeCommittedBrunoScript(t *testing.T, payload []byte) *storageScriptAlias {
	t.Helper()
	var response struct {
		Script *storageScriptAlias `json:"script"`
	}
	if err := json.Unmarshal(payload, &response); err != nil {
		t.Fatal(err)
	}
	if response.Script == nil {
		t.Fatalf("commit response has no script: %s", payload)
	}
	return response.Script
}

type storageScriptAlias = struct {
	ID        string `json:"id"`
	UpdatedAt time.Time
	Config    struct {
		Bruno struct {
			SourceRevision string            `json:"source_revision"`
			SecretBindings map[string]string `json:"secret_bindings"`
		} `json:"bruno"`
	} `json:"config"`
}

func requestBrunoSource(t *testing.T, server *Server, scriptID string, raw bool) []byte {
	t.Helper()
	target := "/api/v1/scripts/" + url.PathEscape(scriptID) + "/bruno-imports/source"
	if raw {
		target += "?raw=true"
	}
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("source status = %d body=%s", response.Code, response.Body.String())
	}
	return response.Body.Bytes()
}

func writeBrunoMultipartFile(t *testing.T, writer *multipart.Writer, relative string, content []byte) {
	t.Helper()
	part, err := writer.CreateFormFile("file:"+url.QueryEscape(relative), filepath.Base(relative))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
}

func tarContains(t *testing.T, payload []byte, name string) bool {
	t.Helper()
	reader := tar.NewReader(bytes.NewReader(payload))
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return false
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Name == name {
			return true
		}
	}
}

func assertDirectoryDoesNotContain(t *testing.T, root, value string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		payload, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(payload, []byte(value)) {
			t.Fatalf("encrypted artifact file %q contains plaintext %q", path, value)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func assertDirectoryHasNoRegularFiles(t *testing.T, root string) {
	t.Helper()
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if strings.Contains(filepath.ToSlash(path), "/.project-tombstones/") {
			return nil
		}
		if info.Mode().IsRegular() {
			t.Errorf("unexpected retained file %s", path)
		}
		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}
