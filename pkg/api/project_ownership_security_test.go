package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

func projectSecurityScript(id string, projectID string) *dsl.Script {
	return &dsl.Script{
		ID: id, Name: id, ProjectID: projectID, Version: "1.0.0", Protocol: "http",
		Config:  dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{Name: "Home", Type: "request", Method: http.MethodGet, Target: "/"}},
	}
}

func TestExecutionPlanValidationUsesDurableScenarioAndDataPoolOwnership(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	now := time.Now().UTC()
	for _, projectID := range []string{"plan-project-a", "plan-project-b"} {
		if err := store.SaveProject(&storage.Project{ID: projectID, Name: projectID, CreatedAt: now}); err != nil {
			t.Fatalf("SaveProject(%s): %v", projectID, err)
		}
	}
	if err := store.SaveScript(projectSecurityScript("plan-script-b", "plan-project-b")); err != nil {
		t.Fatalf("SaveScript: %v", err)
	}
	for _, pool := range []*storage.SharedDataPool{
		{ID: "plan-pool-a", Name: "A", ProjectID: "plan-project-a", CreatedAt: now},
		{ID: "plan-pool-b", Name: "B", ProjectID: "plan-project-b", CreatedAt: now},
	} {
		if err := store.SaveDataPool(pool); err != nil {
			t.Fatalf("SaveDataPool(%s): %v", pool.ID, err)
		}
	}

	// Construct the server before inserting the scenario so its local scenario
	// cache is deliberately stale. Durable validation must still reject it.
	server := NewServer(ServerConfig{Store: store})
	scenario := &storage.Scenario{ID: "plan-scenario-b", Name: "B", Duration: "1m", CreatedAt: now}
	if err := store.SaveScenario(scenario, []storage.ScenarioScript{{ScenarioID: scenario.ID, ScriptID: "plan-script-b"}}); err != nil {
		t.Fatalf("SaveScenario: %v", err)
	}

	foreignScenario := &storage.ExecutionPlan{
		ID: "plan-foreign-scenario", ProjectID: "plan-project-a", Name: "Foreign scenario", RunMode: "parallel",
		Entries: []storage.ExecutionPlanEntry{{ScenarioID: scenario.ID}},
	}
	if status, _ := server.validateExecutionPlan(foreignScenario); status != http.StatusBadRequest {
		t.Fatalf("foreign scenario validation status = %d, want %d", status, http.StatusBadRequest)
	}
	foreignPool := &storage.ExecutionPlan{
		ID: "plan-foreign-pool", ProjectID: "plan-project-a", Name: "Foreign pool", RunMode: "parallel",
		Entries: []storage.ExecutionPlanEntry{{ScenarioID: "future-scenario", DataPools: []storage.ScenarioDataPoolBinding{{PoolID: "plan-pool-b", Alias: "users"}}}},
	}
	if status, _ := server.validateExecutionPlan(foreignPool); status != http.StatusBadRequest {
		t.Fatalf("foreign pool validation status = %d, want %d", status, http.StatusBadRequest)
	}
	ownedDraft := &storage.ExecutionPlan{
		ID: "plan-owned-draft", ProjectID: "plan-project-a", Name: "Owned draft", RunMode: "parallel",
		Entries: []storage.ExecutionPlanEntry{{ScenarioID: "future-scenario", DataPools: []storage.ScenarioDataPoolBinding{{PoolID: "plan-pool-a", Alias: "users"}}}},
	}
	if status, message := server.validateExecutionPlan(ownedDraft); status != 0 {
		t.Fatalf("owned draft validation = %d %q, want success", status, message)
	}
}

func TestScriptUpdateCannotChangeProjectOwnership(t *testing.T) {
	server := NewServer(ServerConfig{})
	stored := server.runner.StoreScript(projectSecurityScript("script-a", "project-a"))
	update := projectSecurityScript(stored.ID, "project-b")
	body, _ := json.Marshal(map[string]any{"script": update})

	req := httptest.NewRequest(http.MethodPut, "/api/v1/scripts/"+stored.ID, bytes.NewReader(body))
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	got, ok := server.runner.GetScript(stored.ID)
	if !ok || got.ProjectID != "project-a" || got.Script.ProjectID != "project-a" {
		t.Fatalf("stored script ownership changed: %#v", got)
	}
}

func TestScriptFolderProjectCannotAuthorizeOrPersistAnotherProject(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	now := time.Now().UTC()
	for _, projectID := range []string{"script-folder-project-a", "script-folder-project-b"} {
		if err := store.SaveProject(&storage.Project{ID: projectID, Name: projectID, CreatedAt: now}); err != nil {
			t.Fatalf("SaveProject(%s): %v", projectID, err)
		}
	}
	foreignFolder := &storage.Folder{ID: "script-folder-project-b-folder", ProjectID: "script-folder-project-b", Name: "B", Category: "scripts", CreatedAt: now}
	if err := store.SaveFolder(foreignFolder); err != nil {
		t.Fatalf("SaveFolder: %v", err)
	}

	resolverBody := `{"folder_id":"` + foreignFolder.ID + `","script":{"name":"forged","project_id":"script-folder-project-a"}}`
	resolverRequest := httptest.NewRequest(http.MethodPost, "/api/v1/scripts", strings.NewReader(resolverBody))
	if _, status, _ := resolveScriptCreateProjectID(resolverRequest, store); status != http.StatusBadRequest {
		t.Fatalf("resolveScriptCreateProjectID status = %d, want %d", status, http.StatusBadRequest)
	}

	server := NewServer(ServerConfig{Store: store})
	forged := projectSecurityScript("forged-folder-script", "script-folder-project-a")
	createBody, _ := json.Marshal(map[string]any{"folder_id": foreignFolder.ID, "script": forged})
	createResponse := httptest.NewRecorder()
	server.mux.ServeHTTP(createResponse, httptest.NewRequest(http.MethodPost, "/api/v1/scripts", bytes.NewReader(createBody)))
	if createResponse.Code != http.StatusBadRequest {
		t.Fatalf("cross-project folder create status = %d, want %d body=%s", createResponse.Code, http.StatusBadRequest, createResponse.Body.String())
	}
	if _, err := store.GetScript(forged.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("forged script persisted: %v", err)
	}

	owned, err := server.runner.StoreScriptDurable(projectSecurityScript("owned-folder-script", "script-folder-project-a"))
	if err != nil {
		t.Fatalf("StoreScriptDurable: %v", err)
	}
	update := projectSecurityScript(owned.ID, owned.ProjectID)
	update.FolderID = &foreignFolder.ID
	updateBody, _ := json.Marshal(map[string]any{"script": update})
	updateResponse := httptest.NewRecorder()
	server.mux.ServeHTTP(updateResponse, httptest.NewRequest(http.MethodPut, "/api/v1/scripts/"+owned.ID, bytes.NewReader(updateBody)))
	if updateResponse.Code != http.StatusBadRequest {
		t.Fatalf("cross-project folder update status = %d, want %d body=%s", updateResponse.Code, http.StatusBadRequest, updateResponse.Body.String())
	}
	unchanged, err := store.GetScript(owned.ID)
	if err != nil {
		t.Fatalf("GetScript: %v", err)
	}
	if unchanged.FolderID != nil {
		t.Fatalf("rejected update changed folder to %#v", unchanged.FolderID)
	}
}

func TestFolderOnlyListsUseTheFolderProject(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	now := time.Now().UTC()
	project := &storage.Project{ID: "folder-filter-project", Name: "Folder filter", CreatedAt: now}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	folder := &storage.Folder{ID: "folder-filter", ProjectID: project.ID, Name: "Filter", Category: "general", CreatedAt: now}
	if err := store.SaveFolder(folder); err != nil {
		t.Fatalf("SaveFolder: %v", err)
	}
	script := projectSecurityScript("folder-filter-script", project.ID)
	script.FolderID = &folder.ID
	if err := store.SaveScript(script); err != nil {
		t.Fatalf("SaveScript: %v", err)
	}
	if err := store.SaveTest(&storage.TestRecord{ID: "folder-filter-test", Name: "filtered", Status: "COMPLETE", ProjectID: project.ID, FolderID: &folder.ID, StartTime: now}); err != nil {
		t.Fatalf("SaveTest: %v", err)
	}
	server := NewServer(ServerConfig{Store: store})

	for _, endpoint := range []struct {
		path string
		id   string
	}{
		{path: "/api/v1/scripts?folder_id=" + folder.ID, id: script.ID},
		{path: "/api/v1/tests?folder_id=" + folder.ID, id: "folder-filter-test"},
	} {
		response := httptest.NewRecorder()
		server.mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, endpoint.path, nil))
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), endpoint.id) {
			t.Fatalf("GET %s status=%d body=%s, want %s", endpoint.path, response.Code, response.Body.String(), endpoint.id)
		}
	}
}

func TestAuthoringSessionProjectOwnershipIsImmutable(t *testing.T) {
	server := NewServer(ServerConfig{})

	createBody := `{"project_id":"project-a","source":"manual","draft":{"name":"draft","project_id":"project-b","version":"1.0.0","protocol":"http","config":{"base_url":"https://example.test"},"actions":[{"name":"Home","type":"request","target":"/"}]}}`
	createResp := httptest.NewRecorder()
	server.mux.ServeHTTP(createResp, httptest.NewRequest(http.MethodPost, "/api/v1/scripts/authoring/sessions", strings.NewReader(createBody)))
	if createResp.Code != http.StatusBadRequest {
		t.Fatalf("mismatched create status = %d, want %d body=%s", createResp.Code, http.StatusBadRequest, createResp.Body.String())
	}

	now := time.Now().UTC()
	valid := &storage.AuthoringSession{
		ID: "session-a", ProjectID: "project-a", Source: "manual", Name: "Project A draft", Status: "reviewing",
		Draft: projectSecurityScript("draft-a", "project-a"), CreatedAt: now, UpdatedAt: now, LastAccessedAt: now,
	}
	if err := server.saveAuthoringSession(valid); err != nil {
		t.Fatalf("saveAuthoringSession(valid) returned error: %v", err)
	}
	updateBody := `{"project_id":"project-b","source":"manual","draft":{"name":"moved","project_id":"project-b","version":"1.0.0","protocol":"http","config":{"base_url":"https://example.test"},"actions":[{"name":"Home","type":"request","target":"/"}]}}`
	updateResp := httptest.NewRecorder()
	server.mux.ServeHTTP(updateResp, httptest.NewRequest(http.MethodPut, "/api/v1/scripts/authoring/sessions/"+valid.ID, strings.NewReader(updateBody)))
	if updateResp.Code != http.StatusBadRequest {
		t.Fatalf("mismatched update status = %d, want %d body=%s", updateResp.Code, http.StatusBadRequest, updateResp.Body.String())
	}
	unchanged, err := server.loadAuthoringSession(valid.ID)
	if err != nil || unchanged.ProjectID != "project-a" || unchanged.Draft.ProjectID != "project-a" {
		t.Fatalf("session ownership changed after rejected update: session=%#v err=%v", unchanged, err)
	}

	corrupt := &storage.AuthoringSession{
		ID: "session-cross-project", ProjectID: "project-a", Source: "manual", Name: "Cross-project draft", Status: "reviewing",
		Draft: projectSecurityScript("draft-b", "project-b"), CreatedAt: now, UpdatedAt: now, LastAccessedAt: now,
	}
	if err := server.saveAuthoringSession(corrupt); err != nil {
		t.Fatalf("saveAuthoringSession returned error: %v", err)
	}
	approveResp := httptest.NewRecorder()
	server.mux.ServeHTTP(approveResp, httptest.NewRequest(http.MethodPost, "/api/v1/scripts/authoring/sessions/"+corrupt.ID+"/approve", nil))
	if approveResp.Code != http.StatusBadRequest {
		t.Fatalf("cross-project approval status = %d, want %d body=%s", approveResp.Code, http.StatusBadRequest, approveResp.Body.String())
	}
}

func TestReleaseCreationUsesApplicationProject(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	now := time.Now().UTC()
	if err := store.SaveProject(&storage.Project{ID: "project-a", Name: "A", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}
	if err := store.SaveApplication(&storage.Application{ID: "application-a", ProjectID: "project-a", Name: "App", CreatedAt: now}); err != nil {
		t.Fatalf("SaveApplication returned error: %v", err)
	}
	server := NewServer(ServerConfig{Store: store})
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, httptest.NewRequest(http.MethodPost, "/api/v1/applications/application-a/releases", strings.NewReader(`{"name":"Release","project_id":"project-b"}`)))
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	releases, err := store.ListReleases("application-a", true)
	if err != nil {
		t.Fatalf("ListReleases returned error: %v", err)
	}
	if len(releases) != 0 {
		t.Fatalf("created cross-project releases: %#v", releases)
	}
}

func TestScenarioUpdateCannotMoveAcrossProjects(t *testing.T) {
	server := NewServer(ServerConfig{})
	scriptA := server.runner.StoreScript(projectSecurityScript("scenario-script-a", "project-a"))
	scriptB := server.runner.StoreScript(projectSecurityScript("scenario-script-b", "project-b"))
	scenario, err := server.runner.StoreScenario(CreateScenarioRequest{
		Name: "Scenario A", Duration: "30s", Groups: []CreateGroupEntry{{ScriptID: scriptA.ID, VUsers: 1}},
	})
	if err != nil {
		t.Fatalf("StoreScenario returned error: %v", err)
	}
	body, _ := json.Marshal(CreateScenarioRequest{
		Name: "Moved Scenario", Duration: "30s", Groups: []CreateGroupEntry{{ScriptID: scriptB.ID, VUsers: 1}},
	})
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, httptest.NewRequest(http.MethodPut, "/api/v1/scenarios/"+scenario.ID, bytes.NewReader(body)))
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	got, ok := server.runner.GetScenario(scenario.ID)
	if !ok || got.ProjectID != "project-a" || len(got.Scripts) != 1 || got.Scripts[0].ScriptID != scriptA.ID {
		t.Fatalf("scenario moved or changed after rejected update: %#v", got)
	}
}

func TestAIAssistanceRejectsCrossProjectReferences(t *testing.T) {
	server := NewServer(ServerConfig{})
	stored := server.runner.StoreScript(projectSecurityScript("ai-script-b", "project-b"))
	server.runner.mu.Lock()
	server.runner.scenarios["ai-empty-scenario-b"] = &StoredScenario{ID: "ai-empty-scenario-b", Name: "Empty B", ProjectID: "project-b"}
	server.runner.mu.Unlock()
	now := time.Now().UTC()
	if err := server.saveAuthoringSession(&storage.AuthoringSession{
		ID: "ai-session-b", ProjectID: "project-b", Source: "ai", Name: "B", Status: "reviewing",
		Draft: projectSecurityScript("ai-session-draft-b", "project-b"), CreatedAt: now, UpdatedAt: now, LastAccessedAt: now,
	}); err != nil {
		t.Fatalf("saveAuthoringSession returned error: %v", err)
	}

	tests := []struct {
		name string
		path string
		body string
	}{
		{name: "draft existing script", path: "/api/v1/ai/drafts", body: `{"project_id":"project-a","source_type":"existing_script","existing_script_id":"` + stored.ID + `"}`},
		{name: "repair stored script", path: "/api/v1/ai/repairs", body: `{"project_id":"project-a","script_id":"` + stored.ID + `"}`},
		{name: "repair authoring session", path: "/api/v1/ai/repairs", body: `{"project_id":"project-a","session_id":"ai-session-b"}`},
		{name: "explain stored script", path: "/api/v1/ai/explain", body: `{"project_id":"project-a","target_type":"script","target_id":"` + stored.ID + `"}`},
		{name: "explain empty stored scenario", path: "/api/v1/ai/explain", body: `{"project_id":"project-a","target_type":"scenario","target_id":"ai-empty-scenario-b"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := httptest.NewRecorder()
			server.mux.ServeHTTP(resp, httptest.NewRequest(http.MethodPost, tt.path, strings.NewReader(tt.body)))
			if resp.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
			}
		})
	}
}

func TestProjectAccessResolversReconcileAllReferencedObjects(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	now := time.Now().UTC()
	if err := store.SaveProject(&storage.Project{ID: "project-b", Name: "Project B", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}
	if err := store.SaveScript(projectSecurityScript("resolver-script-b", "project-b")); err != nil {
		t.Fatalf("SaveScript returned error: %v", err)
	}
	if err := store.SaveScenario(
		&storage.Scenario{ID: "resolver-scenario-b", Name: "Scenario B", Duration: "30s", CreatedAt: now, UpdatedAt: now},
		[]storage.ScenarioScript{{ScenarioID: "resolver-scenario-b", ScriptID: "resolver-script-b", VUsers: 1}},
	); err != nil {
		t.Fatalf("SaveScenario returned error: %v", err)
	}
	if err := store.SaveScenario(
		&storage.Scenario{ID: "resolver-empty-scenario", Name: "Legacy Empty Scenario", Duration: "30s", CreatedAt: now, UpdatedAt: now}, nil,
	); err != nil {
		t.Fatalf("SaveScenario(empty) returned error: %v", err)
	}
	if err := store.SaveAuthoringSession(&storage.AuthoringSession{
		ID: "resolver-session-b", ProjectID: "project-b", Source: "ai", Name: "B", Status: "reviewing",
		Draft: projectSecurityScript("resolver-draft-b", "project-b"), CreatedAt: now, UpdatedAt: now, LastAccessedAt: now,
	}); err != nil {
		t.Fatalf("SaveAuthoringSession returned error: %v", err)
	}
	if err := store.SaveTest(&storage.TestRecord{ID: "resolver-run-b", Name: "Run B", ProjectID: "project-b", Status: "COMPLETE", StartTime: now}); err != nil {
		t.Fatalf("SaveTest returned error: %v", err)
	}

	tests := []struct {
		name string
		body string
	}{
		{name: "existing script", body: `{"project_id":"project-a","existing_script_id":"resolver-script-b"}`},
		{name: "repair script", body: `{"project_id":"project-a","script_id":"resolver-script-b"}`},
		{name: "authoring session", body: `{"project_id":"project-a","session_id":"resolver-session-b"}`},
		{name: "target script", body: `{"project_id":"project-a","target_type":"script","target_id":"resolver-script-b"}`},
		{name: "target scenario", body: `{"project_id":"project-a","target_type":"scenario","target_id":"resolver-scenario-b"}`},
		{name: "target report", body: `{"project_id":"project-a","target_type":"report","target_id":"resolver-run-b"}`},
		{name: "inline scenario", body: `{"project_id":"project-a","scenario":{"groups":[{"script_id":"resolver-script-b"}]}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/ai/explain", strings.NewReader(tt.body))
			if _, status, _ := resolveAIAssistanceProjectID(req, store); status != http.StatusNotFound {
				t.Fatalf("status = %d, want %d", status, http.StatusNotFound)
			}
		})
	}
	emptyScenarioReq := httptest.NewRequest(http.MethodPost, "/api/v1/ai/explain", strings.NewReader(`{"target_type":"scenario","target_id":"resolver-empty-scenario"}`))
	if _, status, _ := resolveAIAssistanceProjectID(emptyScenarioReq, store); status != http.StatusBadRequest {
		t.Fatalf("empty scenario resolver status = %d, want %d", status, http.StatusBadRequest)
	}

	browserReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/browser-to-http/preview", strings.NewReader(`{"project_id":"project-a","source_browser_script":{"project_id":"project-b"}}`))
	if _, status, _ := resolveBrowserToHTTPPreviewProjectID(browserReq, store); status != http.StatusBadRequest {
		t.Fatalf("browser resolver status = %d, want %d", status, http.StatusBadRequest)
	}
	unscopedReplayReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/browser-to-http/preview", strings.NewReader(`{"replay_session_id":"project-owned-session"}`))
	if _, status, _ := resolveBrowserToHTTPPreviewProjectID(unscopedReplayReq, store); status != http.StatusBadRequest {
		t.Fatalf("unscoped replay resolver status = %d, want %d", status, http.StatusBadRequest)
	}
	authoringReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/authoring/sessions", strings.NewReader(`{"project_id":"project-a","draft":{"project_id":"project-b"}}`))
	if _, status, _ := resolveAuthoringSessionRequestProjectID(authoringReq, store); status != http.StatusBadRequest {
		t.Fatalf("authoring resolver status = %d, want %d", status, http.StatusBadRequest)
	}
}

func TestBrowserToHTTPRejectsReplaySessionFromAnotherProject(t *testing.T) {
	server := NewServer(ServerConfig{})
	server.browserReplay.mu.Lock()
	server.browserReplay.sessions["replay-b"] = &BrowserReplaySession{
		ID: "replay-b", ProjectID: "project-b", Status: BrowserReplayPaused, Protocol: "browser", stateChanged: make(chan struct{}),
	}
	server.browserReplay.mu.Unlock()
	body, _ := json.Marshal(BrowserToHTTPPreviewRequest{
		ProjectID: "project-a", ReplaySessionID: "replay-b",
		SourceBrowserScript: &dsl.Script{Name: "Browser A", ProjectID: "project-a", Protocol: "browser"},
		NetworkEntries:      []protocol.NetworkEntry{{URL: "https://example.test/api", Method: http.MethodGet}},
	})
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, httptest.NewRequest(http.MethodPost, "/api/v1/scripts/browser-to-http/preview", bytes.NewReader(body)))
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
}

func TestBrowserToHTTPReplayProjectIsAuthorizedBeforeSessionRead(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})
	enableTrustedHeaderAuth(t, server)
	server.browserReplay.mu.Lock()
	server.browserReplay.sessions["foreign-replay"] = &BrowserReplaySession{
		ID: "foreign-replay", ProjectID: "project-b", Status: BrowserReplayPaused, Protocol: "browser", stateChanged: make(chan struct{}),
	}
	server.browserReplay.mu.Unlock()

	unscopedBody, _ := json.Marshal(BrowserToHTTPPreviewRequest{ReplaySessionID: "foreign-replay"})
	unscopedReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/browser-to-http/preview", bytes.NewReader(unscopedBody))
	unscopedReq.Header.Set("X-Auth-Request-User", "project-a-editor")
	unscopedReq.Header.Set("X-APEXLoad-Project-Roles", storage.DefaultProjectID+"="+RoleEditor)
	unscopedResp := httptest.NewRecorder()
	server.mux.ServeHTTP(unscopedResp, unscopedReq)
	if unscopedResp.Code != http.StatusBadRequest {
		t.Fatalf("unscoped replay status = %d, want %d body=%s", unscopedResp.Code, http.StatusBadRequest, unscopedResp.Body.String())
	}

	foreignBody, _ := json.Marshal(BrowserToHTTPPreviewRequest{ProjectID: "project-b", ReplaySessionID: "foreign-replay"})
	foreignReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/browser-to-http/preview", bytes.NewReader(foreignBody))
	foreignReq.Header.Set("X-Auth-Request-User", "project-a-editor")
	foreignReq.Header.Set("X-APEXLoad-Project-Roles", "project-a="+RoleEditor)
	foreignResp := httptest.NewRecorder()
	server.mux.ServeHTTP(foreignResp, foreignReq)
	if foreignResp.Code != http.StatusForbidden {
		t.Fatalf("foreign replay status = %d, want %d body=%s", foreignResp.Code, http.StatusForbidden, foreignResp.Body.String())
	}
}

func TestArchiveComparisonRejectsCrossProjectBaseline(t *testing.T) {
	server := NewServer(ServerConfig{})
	server.runner.mu.Lock()
	server.runner.tests["run-a"] = &ManagedTest{ID: "run-a", Name: "Run A", ProjectID: "project-a", Status: "COMPLETE"}
	server.runner.tests["run-b"] = &ManagedTest{ID: "run-b", Name: "Run B", ProjectID: "project-b", Status: "COMPLETE"}
	server.runner.archives["run-a"] = &storage.RunArchiveRecord{TestID: "run-a", ArchiveBackend: archiveBackendFilesystem}
	server.runner.archives["run-b"] = &storage.RunArchiveRecord{TestID: "run-b", ArchiveBackend: archiveBackendFilesystem}
	server.runner.mu.Unlock()

	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/api/v1/tests/run-a/archive/comparison?baseline_id=run-b", nil))
	if resp.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusNotFound, resp.Body.String())
	}
}

func TestParseArchivePageRejectsOversizedLimit(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/archive?limit=1001", nil)
	resp := httptest.NewRecorder()
	if _, ok := parseArchivePage(resp, req); ok {
		t.Fatal("parseArchivePage accepted an oversized limit")
	}
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}

	validReq := httptest.NewRequest(http.MethodGet, "/archive?limit=1000", nil)
	validResp := httptest.NewRecorder()
	page, ok := parseArchivePage(validResp, validReq)
	if !ok || page.Limit != maxArchivePageLimit {
		t.Fatalf("page = %#v ok=%v, want maximum accepted page", page, ok)
	}
}
