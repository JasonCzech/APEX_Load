package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

type failSecondEnterpriseBatchScriptStore struct {
	*libraryAtomicForwardingStore
	mu    sync.Mutex
	count int
}

func (s *failSecondEnterpriseBatchScriptStore) CreateScriptIfAbsent(script *dsl.Script) (bool, error) {
	s.mu.Lock()
	s.count++
	count := s.count
	s.mu.Unlock()
	if count == 2 {
		return false, errors.New("injected second enterprise script failure")
	}
	return s.libraryAtomicForwardingStore.CreateScriptIfAbsent(script)
}

func TestHandleSaveEnterpriseArchiveImportCommitsBatchToSelectedFolder(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "enterprise-batch.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	if err := store.SaveProject(&storage.Project{ID: "project-enterprise", Name: "Enterprise", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveFolder(&storage.Folder{ID: "folder-enterprise", ProjectID: "project-enterprise", Name: "Imported", Category: "scripts", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{Store: store})
	response := postEnterpriseArchiveBatch(t, server, map[string]any{
		"operation_id": "enterprise-batch-success",
		"folder_id":    "folder-enterprise",
		"scripts": []any{
			enterpriseBatchTestScript("First", "project-enterprise"),
			enterpriseBatchTestScript("Second", "project-enterprise"),
		},
	})
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d body=%s", response.Code, http.StatusCreated, response.Body.String())
	}
	var body struct {
		Count   int            `json:"count"`
		Scripts []StoredScript `json:"scripts"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Count != 2 || len(body.Scripts) != 2 {
		t.Fatalf("response = %#v, want two scripts", body)
	}
	for _, script := range body.Scripts {
		if script.FolderID == nil || *script.FolderID != "folder-enterprise" {
			t.Fatalf("stored script folder = %#v, want folder-enterprise", script.FolderID)
		}
	}
}

func TestHandleSaveEnterpriseArchiveImportRequiresOperationID(t *testing.T) {
	server := NewServer(ServerConfig{})
	response := postEnterpriseArchiveBatch(t, server, map[string]any{
		"scripts": []any{
			enterpriseBatchTestScript("Missing operation", storage.DefaultProjectID),
		},
	})
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "operation_id is required") {
		t.Fatalf("body = %s, want operation_id guidance", response.Body.String())
	}
	if scripts := server.runner.ListScripts(); len(scripts) != 0 {
		t.Fatalf("missing operation id persisted scripts: %#v", scripts)
	}
}

func TestHandleSaveEnterpriseArchiveImportRollsBackEveryScriptOnLaterFailure(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "enterprise-batch-rollback.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	store := &failSecondEnterpriseBatchScriptStore{
		libraryAtomicForwardingStore: &libraryAtomicForwardingStore{Store: base},
	}
	server := NewServer(ServerConfig{Store: store})
	response := postEnterpriseArchiveBatch(t, server, map[string]any{
		"operation_id": "enterprise-batch-rollback",
		"scripts": []any{
			enterpriseBatchTestScript("First", storage.DefaultProjectID),
			enterpriseBatchTestScript("Second", storage.DefaultProjectID),
		},
	})
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d body=%s", response.Code, http.StatusInternalServerError, response.Body.String())
	}
	if scripts, err := base.ListScripts(storage.DefaultProjectID, nil); err != nil {
		t.Fatal(err)
	} else if len(scripts) != 0 {
		t.Fatalf("failed batch left scripts behind: %#v", scripts)
	}
}

func TestHandleSaveEnterpriseArchiveImportReplaysExactCommittedBatch(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "enterprise-batch-replay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})
	payload := map[string]any{
		"operation_id": "enterprise-batch-replay",
		"scripts": []any{
			enterpriseBatchTestScript("First", storage.DefaultProjectID),
			enterpriseBatchTestScript("Second", storage.DefaultProjectID),
		},
	}
	first := postEnterpriseArchiveBatch(t, server, payload)
	if first.Code != http.StatusCreated {
		t.Fatalf("first status = %d body=%s", first.Code, first.Body.String())
	}
	second := postEnterpriseArchiveBatch(t, server, payload)
	if second.Code != http.StatusCreated {
		t.Fatalf("replay status = %d body=%s", second.Code, second.Body.String())
	}
	var firstBody, secondBody struct {
		Count   int            `json:"count"`
		Scripts []StoredScript `json:"scripts"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &firstBody); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(second.Body.Bytes(), &secondBody); err != nil {
		t.Fatal(err)
	}
	if firstBody.Count != 2 || secondBody.Count != 2 {
		t.Fatalf("counts = %d/%d, want 2/2", firstBody.Count, secondBody.Count)
	}
	for index := range firstBody.Scripts {
		if firstBody.Scripts[index].ID != secondBody.Scripts[index].ID {
			t.Fatalf("script %d replay id = %q, want %q", index, secondBody.Scripts[index].ID, firstBody.Scripts[index].ID)
		}
	}
	if scripts, err := store.ListScripts(storage.DefaultProjectID, nil); err != nil {
		t.Fatal(err)
	} else if len(scripts) != 2 {
		t.Fatalf("replayed batch stored %d scripts, want exactly 2", len(scripts))
	}

	conflictingPayload := map[string]any{
		"operation_id": "enterprise-batch-replay",
		"scripts": []any{
			enterpriseBatchTestScript("Changed", storage.DefaultProjectID),
			enterpriseBatchTestScript("Second", storage.DefaultProjectID),
		},
	}
	conflict := postEnterpriseArchiveBatch(t, server, conflictingPayload)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("conflict status = %d, want %d body=%s", conflict.Code, http.StatusConflict, conflict.Body.String())
	}
}

func TestHandleSaveEnterpriseArchiveImportRestartRecoversCommittedCredentialedBatch(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "enterprise-restart-recovery.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	store := &failOnceDirectImportReceiptStore{SQLiteStore: base, failComplete: true}
	firstServer := NewServer(ServerConfig{Store: store})
	script := &dsl.Script{
		Name: "Enterprise restart recovery", Version: "1.0.0", Protocol: "http", ProjectID: storage.DefaultProjectID,
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Source: "loadrunner", Preserved: map[string]any{}}},
		Config:   dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		Actions: []dsl.Action{{
			Name: "Request", Type: "request", Method: http.MethodGet, Target: "/",
			HeaderList: dsl.HeaderList{{Name: "Authorization", Value: "Bearer enterprise-restart-secret"}},
		}},
	}
	preview := firstServer.newScriptImportResponseWithSourceSecrets(
		httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/enterprise-script-archive", nil),
		scriptImportResponseOptions{Script: script, Scripts: []*dsl.Script{script}},
	)
	masked, ok := preview["scripts"].([]any)
	if !ok || len(masked) != 1 {
		t.Fatalf("preview scripts = %#v", preview["scripts"])
	}
	payload := map[string]any{
		"operation_id": "enterprise-credential-restart-recovery",
		"scripts":      masked,
	}
	first := postEnterpriseArchiveBatch(t, firstServer, payload)
	if first.Code != http.StatusInternalServerError {
		t.Fatalf("injected completion status = %d body=%s", first.Code, first.Body.String())
	}
	reuse := postEnterpriseArchiveBatch(t, firstServer, map[string]any{
		"operation_id": "enterprise-credential-restart-different-operation",
		"scripts":      masked,
	})
	if reuse.Code != http.StatusConflict {
		t.Fatalf("post-commit capability reuse status = %d body=%s", reuse.Code, reuse.Body.String())
	}

	restarted := NewServer(ServerConfig{Store: store})
	recovered := postEnterpriseArchiveBatch(t, restarted, payload)
	if recovered.Code != http.StatusCreated {
		t.Fatalf("restart recovery status = %d body=%s", recovered.Code, recovered.Body.String())
	}
	scripts, err := base.ListScripts(storage.DefaultProjectID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(scripts) != 1 || scripts[0].Actions[0].HeaderList[0].Value != "Bearer enterprise-restart-secret" {
		t.Fatalf("recovered scripts = %#v, want one exact credentialed script", scripts)
	}
}

func TestHandleSaveEnterpriseArchiveImportRestoresAttestedCredentialInEmbeddedFolder(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "enterprise-attested.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	const projectID = "project-enterprise-attested"
	const folderID = "folder-enterprise-attested"
	if err := store.SaveProject(&storage.Project{ID: projectID, Name: "Enterprise", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveFolder(&storage.Folder{ID: folderID, ProjectID: projectID, Name: "Imported", Category: "scripts", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{Store: store})
	script := &dsl.Script{
		Name: "Attested LoadRunner", Version: "1.0.0", Protocol: "http", ProjectID: projectID,
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Source: "loadrunner", Preserved: map[string]any{}}},
		Config:   dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		Actions: []dsl.Action{{
			Name: "Request", Type: "request", Method: http.MethodGet, Target: "/",
			HeaderList: dsl.HeaderList{{Name: "Authorization", Value: "Bearer enterprise-exact-secret"}},
		}},
	}
	preview := server.newScriptImportResponseWithSourceSecrets(
		httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/enterprise-script-archive", nil),
		scriptImportResponseOptions{Script: script, Scripts: []*dsl.Script{script}},
	)
	masked, ok := preview["script"].(map[string]any)
	if !ok {
		t.Fatalf("masked preview script = %T", preview["script"])
	}
	masked["name"] = "UI renamed LoadRunner"
	// The exact candidate is at the library root; the browser-selected embedded
	// destination must survive restoration of that server-owned candidate.
	masked["folder_id"] = folderID
	response := postEnterpriseArchiveBatch(t, server, map[string]any{
		"operation_id": "enterprise-attested-folder",
		"scripts":      []any{masked},
	})
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Scripts []StoredScript `json:"scripts"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Scripts) != 1 {
		t.Fatalf("scripts = %#v", body.Scripts)
	}
	stored, ok := server.runner.GetScript(body.Scripts[0].ID)
	if !ok || stored == nil || stored.Script == nil {
		t.Fatal("stored attested enterprise script not found")
	}
	if stored.FolderID == nil || *stored.FolderID != folderID {
		t.Fatalf("folder = %#v, want %q", stored.FolderID, folderID)
	}
	if got := stored.Script.Actions[0].HeaderList[0].Value; got != "Bearer enterprise-exact-secret" {
		t.Fatalf("restored credential = %q", got)
	}
	if got := stored.Script.Name; got != "UI renamed LoadRunner" {
		t.Fatalf("restored script name = %q, want requested UI name", got)
	}
	if got := stored.Script.Metadata.Import.Source; got != "loadrunner" {
		t.Fatalf("durable source profile = %q, want loadrunner", got)
	}
	if !strings.Contains(response.Body.String(), "enterprise-exact-secret") {
		t.Fatalf("save response did not preserve credential: %s", response.Body.String())
	}
}

func TestHandleSaveEnterpriseArchiveImportAllocatesAttestedNamesAcrossArchives(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "enterprise-attested-names.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})
	previewScript := func(credential string) map[string]any {
		t.Helper()
		script := &dsl.Script{
			Name: "Shared Human Name", Version: "1.0.0", Protocol: "http", ProjectID: storage.DefaultProjectID,
			Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Source: "loadrunner", Preserved: map[string]any{}}},
			Config:   dsl.ScriptConfig{BaseURL: "https://api.example.test"},
			Actions: []dsl.Action{{
				Name: "Request", Type: "request", Method: http.MethodGet, Target: "/",
				HeaderList: dsl.HeaderList{{Name: "Authorization", Value: "Bearer " + credential}},
			}},
		}
		preview := server.newScriptImportResponseWithSourceSecrets(
			httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/enterprise-script-archive", nil),
			scriptImportResponseOptions{Script: script, Scripts: []*dsl.Script{script}},
		)
		masked, ok := preview["script"].(map[string]any)
		if !ok {
			t.Fatalf("masked preview script = %T", preview["script"])
		}
		return masked
	}
	first := previewScript("first-attested-enterprise-secret")
	second := previewScript("second-attested-enterprise-secret")
	// Simulate independently converted archives whose human names differ only
	// by case. The backend, not just the dashboard, owns the final collision
	// guarantee.
	second["name"] = "shared human name"
	payload := map[string]any{
		"operation_id": "enterprise-attested-name-batch",
		"scripts":      []any{first, second},
	}
	response := postEnterpriseArchiveBatch(t, server, payload)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Scripts []StoredScript `json:"scripts"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Scripts) != 2 || body.Scripts[0].Name != "Shared Human Name" ||
		body.Scripts[1].Name != "shared human name_2" {
		t.Fatalf("allocated scripts = %#v", body.Scripts)
	}
	for index, want := range []string{
		"Bearer first-attested-enterprise-secret",
		"Bearer second-attested-enterprise-secret",
	} {
		stored, ok := server.runner.GetScript(body.Scripts[index].ID)
		if !ok || stored == nil || stored.Script == nil {
			t.Fatalf("stored script %d not found", index)
		}
		if got := stored.Script.Actions[0].HeaderList[0].Value; got != want {
			t.Fatalf("stored credential %d = %q, want %q", index, got, want)
		}
	}

	// A lost-response retry reaches the durable receipt before the one-shot
	// attestations and returns the exact same allocated assets.
	replay := postEnterpriseArchiveBatch(t, server, payload)
	if replay.Code != http.StatusCreated {
		t.Fatalf("replay status = %d body=%s", replay.Code, replay.Body.String())
	}
	var replayBody struct {
		Scripts []StoredScript `json:"scripts"`
	}
	if err := json.Unmarshal(replay.Body.Bytes(), &replayBody); err != nil {
		t.Fatal(err)
	}
	if len(replayBody.Scripts) != 2 || replayBody.Scripts[0].ID != body.Scripts[0].ID ||
		replayBody.Scripts[1].ID != body.Scripts[1].ID ||
		replayBody.Scripts[1].Name != "shared human name_2" {
		t.Fatalf("replayed allocation = %#v, first = %#v", replayBody.Scripts, body.Scripts)
	}
}

func TestHandleSaveEnterpriseArchiveImportRejectsUnavailableCredentialWithoutCapability(t *testing.T) {
	server := NewServer(ServerConfig{})
	script := enterpriseBatchTestScript("Unavailable credential", storage.DefaultProjectID)
	actions := script["actions"].([]any)
	action := actions[0].(map[string]any)
	action["header_list"] = []any{
		map[string]any{"name": "Authorization", "value": "Bearer [REDACTED]"},
	}

	response := postEnterpriseArchiveBatch(t, server, map[string]any{
		"operation_id": "enterprise-missing-capability",
		"scripts":      []any{script},
	})
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d body=%s", response.Code, http.StatusConflict, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "missing its server capability") {
		t.Fatalf("response did not explain the save gate: %s", response.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["code"] != enterpriseArchiveDraftInvalidCode {
		t.Fatalf("response code = %q, want %q", body["code"], enterpriseArchiveDraftInvalidCode)
	}
	if scripts := server.runner.ListScripts(); len(scripts) != 0 {
		t.Fatalf("rejected batch stored scripts: %#v", scripts)
	}
}

func TestHandleSaveEnterpriseArchiveImportCodesInvalidAttestationForFreshConversion(t *testing.T) {
	server := NewServer(ServerConfig{})
	script := enterpriseBatchTestScript("Expired capability", storage.DefaultProjectID)
	metadata := script["metadata"].(map[string]any)
	importMetadata := metadata["import"].(map[string]any)
	importMetadata["preserved"] = map[string]any{
		importDraftAttestationMetadataKey: "expired-enterprise-capability",
	}

	response := postEnterpriseArchiveBatch(t, server, map[string]any{
		"operation_id": "enterprise-expired-capability",
		"scripts":      []any{script},
	})
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d body=%s", response.Code, http.StatusConflict, response.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["code"] != enterpriseArchiveDraftInvalidCode || !strings.Contains(body["error"], "invalid or expired") {
		t.Fatalf("response = %#v, want coded expired-draft guidance", body)
	}
	if scripts := server.runner.ListScripts(); len(scripts) != 0 {
		t.Fatalf("invalid attestation stored scripts: %#v", scripts)
	}
}

func postEnterpriseArchiveBatch(t *testing.T, server *Server, payload map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/enterprise-script-archive/save", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	return response
}

func enterpriseBatchTestScript(name, projectID string) map[string]any {
	return map[string]any{
		"name": name, "version": "1.0.0", "protocol": "http", "project_id": projectID,
		"metadata": map[string]any{"import": map[string]any{"source": enterpriseArchiveSaveProfile}},
		"config":   map[string]any{"base_url": "https://api.example.test"},
		"actions":  []any{map[string]any{"name": "Request", "type": "request", "method": "GET", "target": "/"}},
	}
}
