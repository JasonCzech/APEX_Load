package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/importartifact"
	"github.com/jasonczech/apexload/pkg/storage"
)

type rejectedImportSourceRetentionStore struct {
	SQLiteStore *storage.SQLiteStore
	mu          sync.Mutex
	failures    int
	err         error
}

type rejectedImportRevisionListStore struct {
	SQLiteStore *storage.SQLiteStore
	mu          sync.Mutex
	reject      bool
}

func (s *rejectedImportRevisionListStore) ListImportSourceRevisions(
	scriptID string,
	projectID string,
) ([]*storage.ImportSourceRevision, error) {
	s.mu.Lock()
	reject := s.reject
	s.mu.Unlock()
	if reject {
		return nil, errors.New("injected revision-list failure")
	}
	return s.SQLiteStore.ListImportSourceRevisions(scriptID, projectID)
}

func (s *rejectedImportRevisionListStore) rejectRevisionLists() {
	s.mu.Lock()
	s.reject = true
	s.mu.Unlock()
}

func (s *rejectedImportSourceRetentionStore) RetainImportSource(
	retention *storage.ImportSourceRetention,
) (*storage.ImportSourceRevision, error) {
	s.mu.Lock()
	if s.failures > 0 {
		s.failures--
		err := s.err
		s.mu.Unlock()
		return nil, err
	}
	s.mu.Unlock()
	return s.SQLiteStore.RetainImportSource(retention)
}

func (s *rejectedImportSourceRetentionStore) rejectNextRetentions(count int) {
	s.mu.Lock()
	s.failures = count
	s.mu.Unlock()
}

type protectedPostmanInspection struct {
	ImportSessionID string     `json:"import_session_id"`
	Script          dsl.Script `json:"script"`
}

func newImportSourceLifecycleServer(
	t *testing.T,
) (*Server, *storage.SQLiteStore, string) {
	t.Helper()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "imports.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{
		Key:   bytes.Repeat([]byte{0x74}, 32),
		KeyID: "import-source-lifecycle-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	artifactDir := t.TempDir()
	server := NewServer(ServerConfig{
		Store:                store,
		BrunoArtifactDir:     artifactDir,
		BrunoArtifactWrapper: wrapper,
	})
	return server, store, artifactDir
}

func inspectProtectedPostmanSource(
	t *testing.T,
	server *Server,
	projectID string,
	credential string,
	path string,
) protectedPostmanInspection {
	t.Helper()
	collection := map[string]any{
		"info": map[string]any{"name": "Protected lifecycle"},
		"item": []any{map[string]any{
			"name": "Request",
			"request": map[string]any{
				"method": "GET",
				"url":    "https://api.example.test" + path,
				"header": []any{map[string]any{
					"key":   "Authorization",
					"value": "Bearer " + credential,
				}},
			},
		}},
	}
	collectionJSON, err := json.Marshal(collection)
	if err != nil {
		t.Fatal(err)
	}
	requestJSON, err := json.Marshal(map[string]any{
		"content":    string(collectionJSON),
		"project_id": projectID,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts/import/postman",
		bytes.NewReader(requestJSON),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("inspect Postman status = %d body=%s", response.Code, response.Body.String())
	}
	var inspection protectedPostmanInspection
	if err := json.Unmarshal(response.Body.Bytes(), &inspection); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(inspection.ImportSessionID) == "" {
		t.Fatalf("inspection has no protected session: %s", response.Body.String())
	}
	if got := importDraftAttestationToken(&inspection.Script); got !=
		importDraftProtectedTokenPrefix+inspection.ImportSessionID {
		t.Fatalf("draft attestation token = %q, want candidate session %q", got, inspection.ImportSessionID)
	}
	if !strings.Contains(response.Body.String(), credential) || strings.Contains(response.Body.String(), maskedScriptSecret) {
		t.Fatalf("import draft did not preserve imported credential: %s", response.Body.String())
	}
	return inspection
}

func commitProtectedPostmanSource(
	t *testing.T,
	server *Server,
	inspection protectedPostmanInspection,
	projectID string,
	targetID string,
) (*httptest.ResponseRecorder, *StoredScript) {
	t.Helper()
	payload := map[string]any{
		"import_session_id": inspection.ImportSessionID,
		"project_id":        projectID,
	}
	wantStatus := http.StatusCreated
	if targetID != "" {
		payload["mode"] = "replace"
		payload["target_script_id"] = targetID
		wantStatus = http.StatusOK
	}
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
	if response.Code != wantStatus {
		t.Fatalf("commit Postman status = %d, want %d body=%s", response.Code, wantStatus, response.Body.String())
	}
	var result struct {
		StoredScript *StoredScript `json:"stored_script"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.StoredScript == nil || result.StoredScript.Script == nil {
		t.Fatalf("commit response has no stored script: %s", response.Body.String())
	}
	return response, result.StoredScript
}

func TestProtectedPostmanInspectionReusesCandidateForLegacyDraftAttestation(t *testing.T) {
	server, _, artifactDir := newImportSourceLifecycleServer(t)
	const credential = "single-session-password-36f1"
	inspection := inspectProtectedPostmanSource(
		t,
		server,
		storage.DefaultProjectID,
		credential,
		"/single-session",
	)
	descriptors, err := filepath.Glob(filepath.Join(artifactDir, ".sessions", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(descriptors) != 1 {
		t.Fatalf("protected inspection descriptors = %v, want one shared candidate/attestation", descriptors)
	}

	response, stored := commitProtectedPostmanSource(
		t,
		server,
		inspection,
		storage.DefaultProjectID,
		"",
	)
	if !strings.Contains(response.Body.String(), credential) {
		t.Fatalf("commit response did not preserve credential: %s", response.Body.String())
	}
	durable, ok := server.runner.GetScript(stored.ID)
	if !ok || durable == nil || durable.Script == nil ||
		!strings.Contains(durable.Script.Actions[0].HeaderList[0].Value, credential) {
		t.Fatalf("durable import lost executable credential: %#v", durable)
	}
	descriptors, err = filepath.Glob(filepath.Join(artifactDir, ".sessions", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(descriptors) != 0 {
		t.Fatalf("successful commit left protected session descriptors: %v", descriptors)
	}
}

func TestProtectedImportReplaceRetainsRevisionAndDeleteCleansHTTPSource(t *testing.T) {
	server, store, artifactDir := newImportSourceLifecycleServer(t)
	const (
		firstCredential  = "retained-first-password-a31c"
		secondCredential = "retained-second-password-b82d"
	)
	firstInspection := inspectProtectedPostmanSource(
		t,
		server,
		storage.DefaultProjectID,
		firstCredential,
		"/first",
	)
	_, created := commitProtectedPostmanSource(
		t,
		server,
		firstInspection,
		storage.DefaultProjectID,
		"",
	)
	if created.Protocol != "http" {
		t.Fatalf("created protocol = %q, want non-Bruno HTTP", created.Protocol)
	}

	secondInspection := inspectProtectedPostmanSource(
		t,
		server,
		storage.DefaultProjectID,
		secondCredential,
		"/second",
	)
	_, replaced := commitProtectedPostmanSource(
		t,
		server,
		secondInspection,
		storage.DefaultProjectID,
		created.ID,
	)
	durable, ok := server.runner.GetScript(replaced.ID)
	if !ok || durable == nil || durable.Script == nil ||
		!strings.Contains(durable.Script.Actions[0].HeaderList[0].Value, secondCredential) {
		t.Fatalf("replacement lost executable credential: %#v", durable)
	}

	revisions, err := store.ListImportSourceRevisions(created.ID, storage.DefaultProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if len(revisions) != 2 || !revisions[0].Active ||
		revisions[0].SourceFormat != "postman" ||
		revisions[1].Active {
		t.Fatalf("retained source revisions = %#v, want active replacement plus superseded create", revisions)
	}
	rollbackBody, err := json.Marshal(map[string]string{
		"revision_id":                 revisions[1].ID,
		"expected_active_revision_id": revisions[0].ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	rollback := httptest.NewRecorder()
	server.mux.ServeHTTP(rollback, httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts/"+url.PathEscape(created.ID)+"/imports/rollback",
		bytes.NewReader(rollbackBody),
	))
	if rollback.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(rollback.Body.String(), "import_rollback_unsupported_source") {
		t.Fatalf("generic rollback status = %d body=%s", rollback.Code, rollback.Body.String())
	}
	activeAfterRollback, err := store.GetActiveImportSourceRevision(
		created.ID,
		storage.DefaultProjectID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if activeAfterRollback.ID != revisions[0].ID {
		t.Fatalf("rejected generic rollback changed active revision to %q", activeAfterRollback.ID)
	}
	durableAfterRollback, ok := server.runner.GetScript(created.ID)
	if !ok || durableAfterRollback == nil || durableAfterRollback.Script == nil ||
		!strings.Contains(
			durableAfterRollback.Script.Actions[0].HeaderList[0].Value,
			secondCredential,
		) {
		t.Fatalf("rejected generic rollback changed executable script: %#v", durableAfterRollback)
	}
	artifactPaths := make([]string, 0, len(revisions))
	for _, revision := range revisions {
		artifact, err := store.GetImportSourceArtifact(
			revision.RawArtifactID,
			storage.DefaultProjectID,
		)
		if err != nil {
			t.Fatal(err)
		}
		artifactPath := filepath.Join(
			artifactDir,
			filepath.FromSlash(artifact.BackendKey),
		)
		if _, err := os.Stat(artifactPath); err != nil {
			t.Fatalf("retained source artifact missing before delete: %v", err)
		}
		artifactPaths = append(artifactPaths, artifactPath)
	}

	request := httptest.NewRequest(
		http.MethodDelete,
		"/api/v1/scripts/"+created.ID,
		nil,
	)
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("delete status = %d body=%s", response.Code, response.Body.String())
	}
	for _, artifactPath := range artifactPaths {
		if _, err := os.Stat(artifactPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("deleted HTTP import left physical source artifact %q: %v", artifactPath, err)
		}
	}
}

func TestDeleteImportedScriptRetainsRowWhenSourceCleanupCannotBePrepared(t *testing.T) {
	baseStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "delete-fence.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = baseStore.Close() })
	store := &rejectedImportRevisionListStore{SQLiteStore: baseStore}
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{
		Key:   bytes.Repeat([]byte{0x39}, 32),
		KeyID: "import-delete-fence-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	artifactDir := t.TempDir()
	server := NewServer(ServerConfig{
		Store:                sqliteOverlay(store),
		BrunoArtifactDir:     artifactDir,
		BrunoArtifactWrapper: wrapper,
	})
	script := &dsl.Script{
		ID:        "delete-source-fence",
		Name:      "Delete source fence",
		Version:   "1",
		Protocol:  "http",
		ProjectID: storage.DefaultProjectID,
		Config:    dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		Actions: []dsl.Action{{
			Name: "Request", Type: "request", Method: http.MethodGet, Target: "/",
		}},
	}
	stored, _, err := server.runner.CreateImportedScriptDurableReportingReplay(
		script,
		"postman",
	)
	if err != nil {
		t.Fatal(err)
	}
	source := []byte(`{"password":"delete-fence-password-81b4"}`)
	if !server.retainProtectedImportSource(
		context.Background(),
		stored.Script,
		"postman",
		source,
		"tester",
		false,
	) {
		t.Fatal("source retention failed")
	}
	active, err := baseStore.GetActiveImportSourceRevision(
		stored.ID,
		storage.DefaultProjectID,
	)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := baseStore.GetImportSourceArtifact(
		active.RawArtifactID,
		storage.DefaultProjectID,
	)
	if err != nil {
		t.Fatal(err)
	}
	artifactPath := filepath.Join(
		artifactDir,
		filepath.FromSlash(artifact.BackendKey),
	)
	store.rejectRevisionLists()

	request := httptest.NewRequest(
		http.MethodDelete,
		"/api/v1/scripts/"+stored.ID,
		nil,
	)
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("delete status = %d body=%s, want fail-closed 500", response.Code, response.Body.String())
	}
	if _, err := baseStore.GetScript(stored.ID); err != nil {
		t.Fatalf("failed cleanup preparation still deleted script: %v", err)
	}
	if _, err := os.Stat(artifactPath); err != nil {
		t.Fatalf("failed cleanup preparation removed source artifact: %v", err)
	}
}

func TestRetainedImportSourceFailureCleansOrJournalsUntrackedObject(t *testing.T) {
	baseStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "retention-failure.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = baseStore.Close() })
	store := &rejectedImportSourceRetentionStore{
		SQLiteStore: baseStore,
		failures:    1,
		err:         errors.New("injected retention metadata failure"),
	}
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{
		Key:   bytes.Repeat([]byte{0x52}, 32),
		KeyID: "import-retention-failure-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{
		Store:                sqliteOverlay(store),
		BrunoArtifactDir:     t.TempDir(),
		BrunoArtifactWrapper: wrapper,
	})
	script := &dsl.Script{
		ID:        "retention-failure-script",
		Name:      "Retention failure",
		Version:   "1",
		Protocol:  "http",
		ProjectID: storage.DefaultProjectID,
		Config:    dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		Actions: []dsl.Action{{
			Name: "Request", Type: "request", Method: http.MethodGet, Target: "/",
		}},
	}
	stored, _, err := server.runner.CreateImportedScriptDurableReportingReplay(
		script,
		"postman",
	)
	if err != nil {
		t.Fatal(err)
	}
	source := []byte(`{"password":"untracked-source-password-4e9b"}`)
	objects, err := server.brunoArtifactStore()
	if err != nil {
		t.Fatal(err)
	}
	plan, err := objects.PlanBytes(
		storage.DefaultProjectID,
		"raw",
		source,
		int64(len(source)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if retained := server.retainProtectedImportSource(
		context.Background(),
		stored.Script,
		"postman",
		source,
		"tester",
		false,
	); retained {
		t.Fatal("failed retention reported success")
	}
	reader, _, err := objects.Open(context.Background(), plan)
	if err == nil {
		_ = reader.Close()
		t.Fatal("retention metadata failure left an untracked encrypted object")
	}
	publications, err := objects.ListExpiredPublications(
		stored.Script.UpdatedAt.AddDate(1, 0, 0),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(publications) != 0 {
		t.Fatalf("successful immediate cleanup left publication journals: %#v", publications)
	}
}

func TestProtectedImportRetentionFailureLeavesSessionRetryable(t *testing.T) {
	baseStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "retention-retry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = baseStore.Close() })
	store := &rejectedImportSourceRetentionStore{
		SQLiteStore: baseStore,
		failures:    1,
		err:         errors.New("injected one-shot retention failure"),
	}
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{
		Key:   bytes.Repeat([]byte{0x41}, 32),
		KeyID: "import-retention-retry-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	artifactDir := t.TempDir()
	server := NewServer(ServerConfig{
		Store:                sqliteOverlay(store),
		BrunoArtifactDir:     artifactDir,
		BrunoArtifactWrapper: wrapper,
	})
	const credential = "retention-retry-password-776e"
	inspection := inspectProtectedPostmanSource(
		t,
		server,
		storage.DefaultProjectID,
		credential,
		"/retry",
	)
	body, err := json.Marshal(map[string]any{
		"import_session_id": inspection.ImportSessionID,
		"project_id":        storage.DefaultProjectID,
	})
	if err != nil {
		t.Fatal(err)
	}
	commit := func(payload []byte) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(
			http.MethodPost,
			"/api/v1/scripts/import/postman/save",
			bytes.NewReader(payload),
		)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		server.mux.ServeHTTP(response, request)
		return response
	}

	first := commit(body)
	if first.Code != http.StatusServiceUnavailable ||
		!strings.Contains(first.Body.String(), "source retention is pending") {
		t.Fatalf("first commit status = %d body=%s, want retryable retention failure", first.Code, first.Body.String())
	}
	if strings.Contains(first.Body.String(), credential) {
		t.Fatalf("retention failure disclosed credential: %s", first.Body.String())
	}
	scriptID := protectedImportCreateScriptID("postman", inspection.ImportSessionID)
	durable, err := baseStore.GetScript(scriptID)
	if err != nil || durable == nil ||
		!strings.Contains(durable.Actions[0].HeaderList[0].Value, credential) {
		t.Fatalf("partially acknowledged commit lost durable credential: script=%#v err=%v", durable, err)
	}
	descriptors, err := filepath.Glob(filepath.Join(artifactDir, ".sessions", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(descriptors) != 1 {
		t.Fatalf("retention failure consumed its retry session: %v", descriptors)
	}

	retry := commit(body)
	if retry.Code != http.StatusOK {
		t.Fatalf("retention retry status = %d body=%s", retry.Code, retry.Body.String())
	}
	var result struct {
		Created          bool `json:"created"`
		IdempotentReplay bool `json:"idempotent_replay"`
	}
	if err := json.Unmarshal(retry.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Created || !result.IdempotentReplay {
		t.Fatalf("retention retry flags = %#v, want idempotent replay", result)
	}
	revisions, err := baseStore.ListImportSourceRevisions(
		scriptID,
		storage.DefaultProjectID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(revisions) != 1 {
		t.Fatalf("retention retry revisions = %#v, want one", revisions)
	}
	descriptors, err = filepath.Glob(filepath.Join(artifactDir, ".sessions", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(descriptors) != 0 {
		t.Fatalf("successful retention retry left session descriptors: %v", descriptors)
	}

	store.rejectNextRetentions(1)
	replaceInspection := inspectProtectedPostmanSource(
		t,
		server,
		storage.DefaultProjectID,
		"retention-replace-password-b668",
		"/retry-replace",
	)
	replaceBody, err := json.Marshal(map[string]any{
		"import_session_id": replaceInspection.ImportSessionID,
		"project_id":        storage.DefaultProjectID,
		"mode":              "replace",
		"target_script_id":  scriptID,
	})
	if err != nil {
		t.Fatal(err)
	}
	firstReplace := commit(replaceBody)
	if firstReplace.Code != http.StatusServiceUnavailable {
		t.Fatalf("first replacement status = %d body=%s", firstReplace.Code, firstReplace.Body.String())
	}
	partiallyReplaced, err := baseStore.GetScript(scriptID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(
		partiallyReplaced.Actions[0].HeaderList[0].Value,
		"retention-replace-password-b668",
	) {
		t.Fatalf("partially acknowledged replacement lost credential: %#v", partiallyReplaced.Actions)
	}
	refKeyAfterFirstReplace := partiallyReplaced.RefKey
	updatedAtAfterFirstReplace := partiallyReplaced.UpdatedAt

	retriedReplace := commit(replaceBody)
	if retriedReplace.Code != http.StatusOK {
		t.Fatalf("replacement retention retry status = %d body=%s", retriedReplace.Code, retriedReplace.Body.String())
	}
	reconciledReplace, err := baseStore.GetScript(scriptID)
	if err != nil {
		t.Fatal(err)
	}
	if reconciledReplace.RefKey != refKeyAfterFirstReplace ||
		!reconciledReplace.UpdatedAt.Equal(updatedAtAfterFirstReplace) {
		t.Fatalf(
			"replacement retry rewrote an already-committed script: ref=%q/%q updated=%v/%v",
			refKeyAfterFirstReplace,
			reconciledReplace.RefKey,
			updatedAtAfterFirstReplace,
			reconciledReplace.UpdatedAt,
		)
	}
	revisions, err = baseStore.ListImportSourceRevisions(
		scriptID,
		storage.DefaultProjectID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(revisions) != 2 {
		t.Fatalf("replacement retention retry revisions = %#v, want create plus replacement", revisions)
	}
}

func TestProtectedImportRetentionRetryDoesNotOverwriteInterveningEdit(t *testing.T) {
	baseStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "retention-edit-fence.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = baseStore.Close() })
	store := &rejectedImportSourceRetentionStore{
		SQLiteStore: baseStore,
		err:         errors.New("injected replacement retention failure"),
	}
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{
		Key:   bytes.Repeat([]byte{0x63}, 32),
		KeyID: "import-retention-edit-fence-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{
		Store:                sqliteOverlay(store),
		BrunoArtifactDir:     t.TempDir(),
		BrunoArtifactWrapper: wrapper,
	})

	initial := inspectProtectedPostmanSource(
		t,
		server,
		storage.DefaultProjectID,
		"initial-retention-password-7f2a",
		"/initial",
	)
	_, created := commitProtectedPostmanSource(
		t,
		server,
		initial,
		storage.DefaultProjectID,
		"",
	)

	store.rejectNextRetentions(1)
	replacementCredential := "replacement-retention-password-92c4"
	replacement := inspectProtectedPostmanSource(
		t,
		server,
		storage.DefaultProjectID,
		replacementCredential,
		"/replacement",
	)
	body, err := json.Marshal(map[string]any{
		"import_session_id": replacement.ImportSessionID,
		"project_id":        storage.DefaultProjectID,
		"mode":              "replace",
		"target_script_id":  created.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	commit := func() *httptest.ResponseRecorder {
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
	first := commit()
	if first.Code != http.StatusServiceUnavailable {
		t.Fatalf("replacement status = %d body=%s, want retention-pending 503", first.Code, first.Body.String())
	}

	committedReplacement, ok := server.runner.GetScript(created.ID)
	if !ok || committedReplacement == nil || committedReplacement.Script == nil {
		t.Fatal("replacement script was not committed")
	}
	if !strings.Contains(
		committedReplacement.Script.Actions[0].HeaderList[0].Value,
		replacementCredential,
	) {
		t.Fatal("replacement commit lost the operator's imported credential")
	}
	if _, exists, err := readProtectedImportSourceCommitFence(committedReplacement.Script); err != nil || !exists {
		t.Fatalf("committed replacement fence = exists:%t err:%v", exists, err)
	}
	masked := maskScriptSecrets(committedReplacement.Script)
	if masked == nil || (masked.Metadata != nil && masked.Metadata.Import != nil &&
		masked.Metadata.Import.Preserved != nil &&
		masked.Metadata.Import.Preserved[storage.ImportSourceCommitFenceMetadataKey] != nil) {
		t.Fatal("private source commit fence reached the disclosure projection")
	}

	edited := cloneScriptDefinition(committedReplacement.Script)
	edited.Name = "Operator edit after replacement"
	actionDigest, err := scriptCorrelationActionGraphDigest(committedReplacement.Script)
	if err != nil {
		t.Fatal(err)
	}
	editedStored, updated, err := server.runner.UpdateScriptDurableInProjectIfUnchanged(
		created.ID,
		edited,
		storage.DefaultProjectID,
		committedReplacement.Script.UpdatedAt,
		actionDigest,
	)
	if err != nil || !updated {
		t.Fatalf("intervening edit = updated:%t err:%v", updated, err)
	}
	editedRefKey := editedStored.RefKey
	editedUpdatedAt := editedStored.Script.UpdatedAt

	retry := commit()
	if retry.Code != http.StatusConflict {
		t.Fatalf("stale retention retry status = %d body=%s, want 409", retry.Code, retry.Body.String())
	}
	if strings.Contains(retry.Body.String(), replacementCredential) {
		t.Fatalf("stale retention retry disclosed credential: %s", retry.Body.String())
	}
	durable, err := baseStore.GetScript(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if durable.Name != "Operator edit after replacement" ||
		durable.RefKey != editedRefKey ||
		!durable.UpdatedAt.Equal(editedUpdatedAt) {
		t.Fatalf(
			"stale retry rewrote intervening edit: name=%q ref=%q/%q updated=%v/%v",
			durable.Name,
			durable.RefKey,
			editedRefKey,
			durable.UpdatedAt,
			editedUpdatedAt,
		)
	}
	revisions, err := baseStore.ListImportSourceRevisions(
		created.ID,
		storage.DefaultProjectID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(revisions) != 1 || !revisions[0].Active {
		t.Fatalf("stale retry changed active retained source: %#v", revisions)
	}

	// A newer protected import may supersede the now-stale pending marker, but
	// it must carry enough private predecessor evidence that retrying the older
	// still-live session cannot overwrite the newer commit.
	newerCredential := "newer-retention-password-5d19"
	newer := inspectProtectedPostmanSource(
		t,
		server,
		storage.DefaultProjectID,
		newerCredential,
		"/newer",
	)
	_, newerStored := commitProtectedPostmanSource(
		t,
		server,
		newer,
		storage.DefaultProjectID,
		created.ID,
	)
	afterNewer := commit()
	if afterNewer.Code != http.StatusConflict {
		t.Fatalf("older retry after newer import status = %d body=%s, want 409", afterNewer.Code, afterNewer.Body.String())
	}
	durable, err = baseStore.GetScript(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if durable.UpdatedAt.Equal(editedUpdatedAt) ||
		!strings.Contains(durable.Actions[0].HeaderList[0].Value, newerCredential) ||
		durable.ID != newerStored.ID {
		t.Fatalf("older retry displaced newer protected import: %#v", durable)
	}
	revisions, err = baseStore.ListImportSourceRevisions(
		created.ID,
		storage.DefaultProjectID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(revisions) != 2 || !revisions[0].Active {
		t.Fatalf("newer retained source revision state = %#v", revisions)
	}
}

func TestProtectedImportCreateRetentionRetryDoesNotAttachSourceAfterEdit(t *testing.T) {
	baseStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "create-retention-edit-fence.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = baseStore.Close() })
	store := &rejectedImportSourceRetentionStore{
		SQLiteStore: baseStore,
		failures:    1,
		err:         errors.New("injected create retention failure"),
	}
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{
		Key:   bytes.Repeat([]byte{0x64}, 32),
		KeyID: "import-create-retention-edit-fence-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{
		Store:                sqliteOverlay(store),
		BrunoArtifactDir:     t.TempDir(),
		BrunoArtifactWrapper: wrapper,
	})
	credential := "create-retention-password-663a"
	inspection := inspectProtectedPostmanSource(
		t,
		server,
		storage.DefaultProjectID,
		credential,
		"/create-pending",
	)
	body, err := json.Marshal(map[string]any{
		"import_session_id": inspection.ImportSessionID,
		"project_id":        storage.DefaultProjectID,
	})
	if err != nil {
		t.Fatal(err)
	}
	commit := func() *httptest.ResponseRecorder {
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
	first := commit()
	if first.Code != http.StatusServiceUnavailable {
		t.Fatalf("create status = %d body=%s, want retention-pending 503", first.Code, first.Body.String())
	}
	scriptID := protectedImportCreateScriptID("postman", inspection.ImportSessionID)
	committed, ok := server.runner.GetScript(scriptID)
	if !ok || committed == nil || committed.Script == nil {
		t.Fatal("create script was not committed")
	}
	if _, exists, err := readProtectedImportSourceCommitFence(committed.Script); err != nil || !exists {
		t.Fatalf("committed create fence = exists:%t err:%v", exists, err)
	}

	edited := cloneScriptDefinition(committed.Script)
	edited.Name = "Operator edit after create"
	actionDigest, err := scriptCorrelationActionGraphDigest(committed.Script)
	if err != nil {
		t.Fatal(err)
	}
	editedStored, updated, err := server.runner.UpdateScriptDurableInProjectIfUnchanged(
		scriptID,
		edited,
		storage.DefaultProjectID,
		committed.Script.UpdatedAt,
		actionDigest,
	)
	if err != nil || !updated {
		t.Fatalf("intervening create edit = updated:%t err:%v", updated, err)
	}
	editedRefKey := editedStored.RefKey
	editedUpdatedAt := editedStored.Script.UpdatedAt

	retry := commit()
	if retry.Code != http.StatusConflict {
		t.Fatalf("stale create retry status = %d body=%s, want 409", retry.Code, retry.Body.String())
	}
	if strings.Contains(retry.Body.String(), credential) {
		t.Fatalf("stale create retry disclosed credential: %s", retry.Body.String())
	}
	durable, err := baseStore.GetScript(scriptID)
	if err != nil {
		t.Fatal(err)
	}
	if durable.Name != "Operator edit after create" ||
		durable.RefKey != editedRefKey ||
		!durable.UpdatedAt.Equal(editedUpdatedAt) {
		t.Fatalf("stale create retry rewrote intervening edit: %#v", durable)
	}
	revisions, err := baseStore.ListImportSourceRevisions(
		scriptID,
		storage.DefaultProjectID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(revisions) != 0 {
		t.Fatalf("stale create retry attached source A to edited script B: %#v", revisions)
	}
}

func TestRetainedImportSourceIdempotentRetryDoesNotDuplicateRevision(t *testing.T) {
	server, store, _ := newImportSourceLifecycleServer(t)
	script := &dsl.Script{
		ID:        "retention-idempotent-script",
		Name:      "Retention idempotency",
		Version:   "1",
		Protocol:  "http",
		ProjectID: storage.DefaultProjectID,
		Config:    dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		Actions: []dsl.Action{{
			Name: "Request", Type: "request", Method: http.MethodGet, Target: "/",
		}},
	}
	stored, _, err := server.runner.CreateImportedScriptDurableReportingReplay(
		script,
		"postman",
	)
	if err != nil {
		t.Fatal(err)
	}
	source := []byte(`{"password":"idempotent-source-password-05da"}`)
	if !server.retainProtectedImportSource(
		context.Background(),
		stored.Script,
		"postman",
		source,
		"tester",
		false,
	) {
		t.Fatal("initial source retention failed")
	}
	if !server.retainProtectedImportSource(
		context.Background(),
		stored.Script,
		"postman",
		source,
		"tester",
		true,
	) {
		t.Fatal("idempotent source retention retry failed")
	}
	revisions, err := store.ListImportSourceRevisions(
		stored.ID,
		storage.DefaultProjectID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(revisions) != 1 {
		t.Fatalf("idempotent retention revisions = %#v, want one", revisions)
	}

	// Identical raw bytes are not sufficient for idempotency: the active
	// revision must also describe the exact current script content.
	current, ok := server.runner.GetScript(stored.ID)
	if !ok || current == nil || current.Script == nil {
		t.Fatal("retained script is unavailable")
	}
	edited := cloneScriptDefinition(current.Script)
	edited.Name = "Retention idempotency edited"
	actionDigest, err := scriptCorrelationActionGraphDigest(current.Script)
	if err != nil {
		t.Fatal(err)
	}
	editedStored, updated, err := server.runner.UpdateScriptDurableInProjectIfUnchanged(
		stored.ID,
		edited,
		storage.DefaultProjectID,
		current.Script.UpdatedAt,
		actionDigest,
	)
	if err != nil || !updated {
		t.Fatalf("edit retained script = updated:%t err:%v", updated, err)
	}
	if !server.retainProtectedImportSource(
		context.Background(),
		editedStored.Script,
		"postman",
		source,
		"tester",
		true,
	) {
		t.Fatal("same-source retention for changed script failed")
	}
	revisions, err = store.ListImportSourceRevisions(
		stored.ID,
		storage.DefaultProjectID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(revisions) != 2 || !revisions[0].Active ||
		revisions[0].CanonicalScript == nil ||
		revisions[0].CanonicalScript.Name != edited.Name {
		t.Fatalf("changed-script retention revisions = %#v, want new active canonical revision", revisions)
	}
}

func TestProtectedImportCreateReplaySurvivesStaleClaimRecovery(t *testing.T) {
	server, store, artifactDir := newImportSourceLifecycleServer(t)
	candidate := &dsl.Script{
		Name:      "Stale claim create",
		Version:   "1",
		Protocol:  "http",
		ProjectID: storage.DefaultProjectID,
		Config:    dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		Actions: []dsl.Action{{
			Name: "Request", Type: "request", Method: http.MethodGet, Target: "/",
		}},
	}
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	sessionID, _, err := server.registerImportCandidateSession(
		request,
		"postman",
		candidate,
	)
	if err != nil {
		t.Fatal(err)
	}
	firstCandidate, firstClaim, err := server.claimProtectedImportCandidateSession(
		request,
		"postman",
		sessionID,
		candidate.ProjectID,
	)
	if err != nil {
		t.Fatal(err)
	}
	first := firstCandidate.Script
	first.ID = protectedImportCreateScriptID("postman", sessionID)
	first, _, err = finalizeImportCommitScript(first, "postman", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, created, err := server.runner.CreateImportedScriptDurableReportingReplay(
		first,
		"postman",
	); err != nil || !created {
		t.Fatalf("initial create = created:%t err:%v", created, err)
	}
	clearProtectedImportCandidate(firstCandidate)

	claimPath := filepath.Join(artifactDir, ".sessions", sessionID+".claim.json")
	stale := time.Now().UTC().Add(-time.Minute)
	if err := os.Chtimes(claimPath, stale, stale); err != nil {
		t.Fatal(err)
	}
	secondCandidate, secondClaim, err := server.claimProtectedImportCandidateSession(
		request,
		"postman",
		sessionID,
		candidate.ProjectID,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		clearProtectedImportCandidate(secondCandidate)
		server.releaseImportCandidateClaim(secondClaim)
	}()
	if _, err := store.GetScript(first.ID); err != nil {
		t.Fatalf("stale claim recovery removed durable script: %v", err)
	}
	second := secondCandidate.Script
	second.ID = protectedImportCreateScriptID("postman", sessionID)
	second, _, err = finalizeImportCommitScript(second, "postman", true)
	if err != nil {
		t.Fatal(err)
	}
	if created, err := store.CreateScriptIfAbsent(cloneScriptDefinition(second)); err != nil || created {
		t.Fatalf("direct create-only replay = created:%t err:%v, want conflict/no-op", created, err)
	}
	if _, created, err := server.runner.CreateImportedScriptDurableReportingReplay(
		second,
		"postman",
	); err != nil || created {
		t.Fatalf("reconciled create = created:%t err:%v, want idempotent replay", created, err)
	}
	if err := server.completeImportCandidateClaim(context.Background(), firstClaim); err == nil {
		t.Fatal("stale first claim unexpectedly completed")
	}
}
