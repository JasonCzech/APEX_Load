package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func forgedCredentialPreservingImportScript(
	id string,
	projectID string,
	credential string,
) *dsl.Script {
	script := &dsl.Script{
		ID:        id,
		Name:      "Forged import trust",
		Version:   "1",
		Protocol:  "http",
		ProjectID: projectID,
		Config:    dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		Actions: []dsl.Action{{
			Name:   "Request",
			Type:   "request",
			Method: http.MethodGet,
			Target: "/forged",
			HeaderList: dsl.HeaderList{{
				Name:  "Authorization",
				Value: "Bearer " + credential,
			}},
			Headers: map[string]string{
				"Authorization": "Bearer " + credential,
			},
		}},
	}
	ensureCredentialPreservingImportMetadata(script, "postman")
	ensureScriptImportPreserved(script, "postman")[importDraftAttestationMetadataKey] =
		importDraftProtectedTokenPrefix + "forged-session"
	return script
}

func forgedCredentialPreservingImportMarkerOnlyScript(
	id string,
	projectID string,
) *dsl.Script {
	script := forgedCredentialPreservingImportScript(
		id,
		projectID,
		"placeholder-removed-before-submit",
	)
	script.Actions[0].HeaderList = nil
	script.Actions[0].Headers = nil
	return script
}

func postPostmanCommitRequest(
	t *testing.T,
	server *Server,
	payload map[string]any,
) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
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
	return response
}

func assertNoClientEstablishedImportTrust(
	t *testing.T,
	server *Server,
	scriptID string,
	credential string,
) {
	t.Helper()
	stored, ok := server.runner.GetScript(scriptID)
	if !ok || stored == nil || stored.Script == nil {
		return
	}
	encoded, err := json.Marshal(stored.Script)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(credential)) {
		t.Fatalf("unattested client JSON persisted executable credential: %s", encoded)
	}
	if isCredentialPreservingImport(stored.Script) {
		t.Fatalf("unattested client JSON established trusted import metadata: %s", encoded)
	}
}

func TestNoSessionImportCommitRetainsOperatorCredentialOnCreate(t *testing.T) {
	server := NewServer(ServerConfig{})
	const credential = "legacy-import-create-password-72d1"
	script := forgedCredentialPreservingImportScript(
		"legacy-import-create",
		storage.DefaultProjectID,
		credential,
	)
	response := postPostmanCommitRequest(t, server, map[string]any{
		"script":     script,
		"project_id": storage.DefaultProjectID,
	})
	if response.Code != http.StatusCreated {
		t.Fatalf("legacy import create status = %d body=%s, want 201", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), credential) {
		t.Fatalf("legacy import create response disclosed credential: %s", response.Body.String())
	}
	stored, ok := server.runner.GetScript(script.ID)
	if !ok || stored == nil || stored.Script == nil {
		t.Fatal("legacy import create was not stored")
	}
	if got := stored.Script.Actions[0].HeaderList[0].Value; got != "Bearer "+credential {
		t.Fatalf("durable imported credential = %q", got)
	}
	if !isCredentialPreservingImport(stored.Script) ||
		importDraftAttestationToken(stored.Script) != "" {
		t.Fatalf("server import provenance = %#v", stored.Script.Metadata)
	}

	markerOnly := forgedCredentialPreservingImportMarkerOnlyScript(
		"legacy-import-marker-only-create",
		storage.DefaultProjectID,
	)
	markerOnlyResponse := postPostmanCommitRequest(t, server, map[string]any{
		"script":     markerOnly,
		"project_id": storage.DefaultProjectID,
	})
	if markerOnlyResponse.Code != http.StatusCreated {
		t.Fatalf(
			"marker-only forged create status = %d body=%s",
			markerOnlyResponse.Code,
			markerOnlyResponse.Body.String(),
		)
	}
	stored, ok = server.runner.GetScript(markerOnly.ID)
	if !ok || stored == nil || stored.Script == nil {
		t.Fatalf("marker-only forged create was not stored: %s", markerOnlyResponse.Body.String())
	}
	if !isCredentialPreservingImport(stored.Script) ||
		importDraftAttestationToken(stored.Script) != "" {
		t.Fatalf("import route did not establish clean server provenance: %#v", stored.Script.Metadata)
	}
}

func TestNoSessionImportCommitRetainsOperatorCredentialOnReplace(t *testing.T) {
	server := NewServer(ServerConfig{})
	existing := &dsl.Script{
		ID:        "forged-import-replace-target",
		Name:      "Existing",
		Version:   "1",
		Protocol:  "http",
		ProjectID: storage.DefaultProjectID,
		Config:    dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		Actions: []dsl.Action{{
			Name: "Existing", Type: "request", Method: http.MethodGet, Target: "/existing",
		}},
	}
	if _, _, err := server.runner.CreateScriptDurableReportingReplay(existing); err != nil {
		t.Fatal(err)
	}
	const credential = "legacy-import-replace-password-918a"
	replacement := forgedCredentialPreservingImportScript(
		"caller-controlled-replacement-id",
		storage.DefaultProjectID,
		credential,
	)
	response := postPostmanCommitRequest(t, server, map[string]any{
		"mode":             "replace",
		"target_script_id": existing.ID,
		"replace_mode":     "full",
		"project_id":       storage.DefaultProjectID,
		"script":           replacement,
	})
	if response.Code != http.StatusOK {
		t.Fatalf("legacy import replace status = %d body=%s, want 200", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), credential) {
		t.Fatalf("legacy import replace response disclosed credential: %s", response.Body.String())
	}
	stored, ok := server.runner.GetScript(existing.ID)
	if !ok || stored == nil || stored.Script == nil {
		t.Fatalf("legacy import replace lost target: %s", response.Body.String())
	}
	if got := stored.Script.Actions[0].HeaderList[0].Value; got != "Bearer "+credential {
		t.Fatalf("durable replacement credential = %q", got)
	}
	if stored.ID != existing.ID ||
		!isCredentialPreservingImport(stored.Script) ||
		importDraftAttestationToken(stored.Script) != "" {
		t.Fatalf("durable replacement provenance/identity = %#v", stored.Script)
	}
}

func TestNoSessionImportCommitAcceptsValidLegacyAttestationAndRetainsSource(t *testing.T) {
	server, store, artifactDir := newImportSourceLifecycleServer(t)
	const credential = "legacy-attested-create-password-5c3e"
	inspection := inspectProtectedPostmanSource(
		t,
		server,
		storage.DefaultProjectID,
		credential,
		"/legacy-create",
	)
	response := postPostmanCommitRequest(t, server, map[string]any{
		"script":     inspection.Script,
		"project_id": storage.DefaultProjectID,
	})
	if response.Code != http.StatusCreated {
		t.Fatalf("legacy attested create status = %d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), credential) {
		t.Fatalf("legacy attested create response disclosed credential: %s", response.Body.String())
	}
	var result struct {
		StoredScript *StoredScript `json:"stored_script"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.StoredScript == nil {
		t.Fatalf("legacy attested create returned no script: %s", response.Body.String())
	}
	durable, ok := server.runner.GetScript(result.StoredScript.ID)
	if !ok || durable == nil || durable.Script == nil ||
		!strings.Contains(durable.Script.Actions[0].HeaderList[0].Value, credential) {
		t.Fatalf("legacy attested create lost executable credential: %#v", durable)
	}
	if importDraftAttestationToken(durable.Script) != "" {
		t.Fatal("legacy draft attestation entered durable storage")
	}
	revisions, err := store.ListBrunoSourceRevisions(
		result.StoredScript.ID,
		storage.DefaultProjectID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(revisions) != 1 || revisions[0].SourceFormat != "postman" {
		t.Fatalf("legacy attested source revisions = %#v, want one Postman revision", revisions)
	}
	descriptors, err := filepath.Glob(filepath.Join(artifactDir, ".sessions", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(descriptors) != 0 {
		t.Fatalf("legacy attested create left protected session descriptors: %v", descriptors)
	}
}

func TestNoSessionImportCommitAcceptsValidLegacyAttestationOnReplace(t *testing.T) {
	server, store, _ := newImportSourceLifecycleServer(t)
	existing := &dsl.Script{
		ID:        "legacy-attested-replace-target",
		Name:      "Existing",
		Version:   "1",
		Protocol:  "http",
		ProjectID: storage.DefaultProjectID,
		Config:    dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		Actions: []dsl.Action{{
			Name: "Existing", Type: "request", Method: http.MethodGet, Target: "/existing",
		}},
	}
	if _, _, err := server.runner.CreateScriptDurableReportingReplay(existing); err != nil {
		t.Fatal(err)
	}
	const credential = "legacy-attested-replace-password-146b"
	inspection := inspectProtectedPostmanSource(
		t,
		server,
		storage.DefaultProjectID,
		credential,
		"/legacy-replace",
	)
	response := postPostmanCommitRequest(t, server, map[string]any{
		"mode":             "replace",
		"target_script_id": existing.ID,
		"replace_mode":     "full",
		"project_id":       storage.DefaultProjectID,
		"script":           inspection.Script,
	})
	if response.Code != http.StatusOK {
		t.Fatalf("legacy attested replace status = %d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), credential) {
		t.Fatalf("legacy attested replace response disclosed credential: %s", response.Body.String())
	}
	durable, ok := server.runner.GetScript(existing.ID)
	if !ok || durable == nil || durable.Script == nil ||
		!strings.Contains(durable.Script.Actions[0].HeaderList[0].Value, credential) {
		t.Fatalf("legacy attested replace lost executable credential: %#v", durable)
	}
	revisions, err := store.ListBrunoSourceRevisions(
		existing.ID,
		storage.DefaultProjectID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(revisions) != 1 || !revisions[0].Active {
		t.Fatalf("legacy attested replace source revisions = %#v, want one active revision", revisions)
	}
}

func TestNoSessionImportCommitLegacyAttestationRetentionRetryIsIdempotent(t *testing.T) {
	server, baseStore, artifactDir := newImportSourceLifecycleServer(t)
	retentionStore := &rejectedImportSourceRetentionStore{
		SQLiteStore: baseStore,
		failures:    1,
		err:         errors.New("injected legacy-attestation retention failure"),
	}
	server.store = retentionStore

	const firstCredential = "legacy-attested-retry-password-81aa"
	inspection := inspectProtectedPostmanSource(
		t,
		server,
		storage.DefaultProjectID,
		firstCredential,
		"/legacy-retry",
	)
	createPayload := map[string]any{
		"script":     inspection.Script,
		"project_id": storage.DefaultProjectID,
	}
	firstCreate := postPostmanCommitRequest(t, server, createPayload)
	if firstCreate.Code != http.StatusServiceUnavailable ||
		!strings.Contains(firstCreate.Body.String(), importSourceRetentionPendingCode) {
		t.Fatalf(
			"legacy attested create retention failure status = %d body=%s",
			firstCreate.Code,
			firstCreate.Body.String(),
		)
	}
	var pendingCreate struct {
		ScriptID string `json:"script_id"`
	}
	if err := json.Unmarshal(firstCreate.Body.Bytes(), &pendingCreate); err != nil {
		t.Fatal(err)
	}
	if pendingCreate.ScriptID == "" {
		t.Fatalf("legacy attested retention failure omitted committed script id: %s", firstCreate.Body.String())
	}
	retriedCreate := postPostmanCommitRequest(t, server, createPayload)
	if retriedCreate.Code != http.StatusOK {
		t.Fatalf(
			"legacy attested create retention retry status = %d body=%s",
			retriedCreate.Code,
			retriedCreate.Body.String(),
		)
	}
	var createResult struct {
		Created          bool `json:"created"`
		IdempotentReplay bool `json:"idempotent_replay"`
	}
	if err := json.Unmarshal(retriedCreate.Body.Bytes(), &createResult); err != nil {
		t.Fatal(err)
	}
	if createResult.Created || !createResult.IdempotentReplay {
		t.Fatalf("legacy attested create retry flags = %#v", createResult)
	}
	revisions, err := baseStore.ListBrunoSourceRevisions(
		pendingCreate.ScriptID,
		storage.DefaultProjectID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(revisions) != 1 {
		t.Fatalf("legacy attested create retry revisions = %#v, want one", revisions)
	}

	retentionStore.rejectNextRetentions(1)
	const replacementCredential = "legacy-attested-retry-replace-password-59bd"
	replacement := inspectProtectedPostmanSource(
		t,
		server,
		storage.DefaultProjectID,
		replacementCredential,
		"/legacy-retry-replace",
	)
	replacePayload := map[string]any{
		"mode":             "replace",
		"target_script_id": pendingCreate.ScriptID,
		"replace_mode":     "full",
		"project_id":       storage.DefaultProjectID,
		"script":           replacement.Script,
	}
	firstReplace := postPostmanCommitRequest(t, server, replacePayload)
	if firstReplace.Code != http.StatusServiceUnavailable {
		t.Fatalf(
			"legacy attested replace retention failure status = %d body=%s",
			firstReplace.Code,
			firstReplace.Body.String(),
		)
	}
	afterFirstReplace, err := baseStore.GetScript(pendingCreate.ScriptID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(
		afterFirstReplace.Actions[0].HeaderList[0].Value,
		replacementCredential,
	) {
		t.Fatalf("legacy attested partial replacement lost credential: %#v", afterFirstReplace.Actions)
	}
	refKeyAfterFirstReplace := afterFirstReplace.RefKey
	updatedAtAfterFirstReplace := afterFirstReplace.UpdatedAt

	retriedReplace := postPostmanCommitRequest(t, server, replacePayload)
	if retriedReplace.Code != http.StatusOK {
		t.Fatalf(
			"legacy attested replace retention retry status = %d body=%s",
			retriedReplace.Code,
			retriedReplace.Body.String(),
		)
	}
	afterRetry, err := baseStore.GetScript(pendingCreate.ScriptID)
	if err != nil {
		t.Fatal(err)
	}
	if afterRetry.RefKey != refKeyAfterFirstReplace ||
		!afterRetry.UpdatedAt.Equal(updatedAtAfterFirstReplace) {
		t.Fatalf(
			"legacy attested replace retry rewrote durable script: ref=%q/%q updated=%v/%v",
			refKeyAfterFirstReplace,
			afterRetry.RefKey,
			updatedAtAfterFirstReplace,
			afterRetry.UpdatedAt,
		)
	}
	revisions, err = baseStore.ListBrunoSourceRevisions(
		pendingCreate.ScriptID,
		storage.DefaultProjectID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(revisions) != 2 {
		t.Fatalf(
			"legacy attested replace retry revisions = %#v, want create plus replacement",
			revisions,
		)
	}
	descriptors, err := filepath.Glob(filepath.Join(artifactDir, ".sessions", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(descriptors) != 0 {
		t.Fatalf("legacy attested retention retries left session descriptors: %v", descriptors)
	}
}
