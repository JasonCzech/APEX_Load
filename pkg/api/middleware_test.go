package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/stormrunner/pkg/dsl"
	"github.com/jasonczech/stormrunner/pkg/recorder"
	"github.com/jasonczech/stormrunner/pkg/storage"
)

func TestTrustedHeaderAuthRequiresIdentityForSystemSettings(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/settings", nil)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusUnauthorized, resp.Body.String())
	}
}

func TestTrustedHeaderAuthSystemSettingsRequireSystemAdmin(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	settings := enableTrustedHeaderAuth(t, server)
	body, err := json.Marshal(settings)
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}

	req := httptest.NewRequest(http.MethodPut, "/api/v1/system/settings", bytes.NewReader(body))
	req.Header.Set("X-Auth-Request-User", "alice")
	req.Header.Set("X-Stormrunner-Roles", RoleViewer)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusForbidden {
		t.Fatalf("viewer PUT status = %d, want %d body=%s", resp.Code, http.StatusForbidden, resp.Body.String())
	}

	req2 := httptest.NewRequest(http.MethodPut, "/api/v1/system/settings", bytes.NewReader(body))
	req2.Header.Set("X-Auth-Request-User", "root")
	req2.Header.Set("X-Stormrunner-Roles", RoleSystemAdmin)
	resp2 := httptest.NewRecorder()

	server.mux.ServeHTTP(resp2, req2)

	if resp2.Code != http.StatusOK {
		t.Fatalf("system admin PUT status = %d, want %d body=%s", resp2.Code, http.StatusOK, resp2.Body.String())
	}
}

func TestTrustedHeaderAuthRequiresIdentityForSystemInventory(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/inventory", nil)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusUnauthorized, resp.Body.String())
	}
}

func TestTrustedHeaderAuthAllowsViewerToReadSystemInventory(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/inventory", nil)
	req.Header.Set("X-Auth-Request-User", "viewer-user")
	req.Header.Set("X-Stormrunner-Roles", RoleViewer)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("viewer GET status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
}

func TestTrustedHeaderAuthProjectRoleGuardsProjectEndpoints(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "stormrunner.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	project := &storage.Project{ID: uuid.New().String(), Name: "Project One", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+project.ID, nil)
	getReq.Header.Set("X-Auth-Request-User", "viewer-user")
	getReq.Header.Set("X-Stormrunner-Project-Roles", project.ID+"="+RoleViewer)
	getResp := httptest.NewRecorder()

	server.mux.ServeHTTP(getResp, getReq)

	if getResp.Code != http.StatusOK {
		t.Fatalf("project viewer GET status = %d, want %d body=%s", getResp.Code, http.StatusOK, getResp.Body.String())
	}

	updateBody := []byte(`{"name":"Renamed"}`)
	putReq := httptest.NewRequest(http.MethodPut, "/api/v1/projects/"+project.ID, bytes.NewReader(updateBody))
	putReq.Header.Set("X-Auth-Request-User", "viewer-user")
	putReq.Header.Set("X-Stormrunner-Project-Roles", project.ID+"="+RoleViewer)
	putResp := httptest.NewRecorder()

	server.mux.ServeHTTP(putResp, putReq)

	if putResp.Code != http.StatusForbidden {
		t.Fatalf("project viewer PUT status = %d, want %d body=%s", putResp.Code, http.StatusForbidden, putResp.Body.String())
	}

	adminReq := httptest.NewRequest(http.MethodPut, "/api/v1/projects/"+project.ID, bytes.NewReader(updateBody))
	adminReq.Header.Set("X-Auth-Request-User", "project-admin")
	adminReq.Header.Set("X-Stormrunner-Project-Roles", project.ID+"="+RoleProjectAdmin)
	adminResp := httptest.NewRecorder()

	server.mux.ServeHTTP(adminResp, adminReq)

	if adminResp.Code != http.StatusOK {
		t.Fatalf("project admin PUT status = %d, want %d body=%s", adminResp.Code, http.StatusOK, adminResp.Body.String())
	}
	if got, err := store.GetProject(project.ID); err != nil || got.Name != "Renamed" {
		t.Fatalf("project rename failed, got=%v err=%v", got, err)
	}
}

func TestTrustedHeaderAuthProjectRoleGuardsTestStreamEndpoint(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "stormrunner.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	project := &storage.Project{ID: uuid.New().String(), Name: "Checkout", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}
	testRecord := &storage.TestRecord{ID: uuid.New().String(), ProjectID: project.ID, Name: "Checkout Smoke", Status: "RUNNING", StartTime: time.Now()}
	if err := store.SaveTest(testRecord); err != nil {
		t.Fatalf("SaveTest returned error: %v", err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	unauthorizedReq := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testRecord.ID+"/stream", nil)
	unauthorizedResp := httptest.NewRecorder()
	server.mux.ServeHTTP(unauthorizedResp, unauthorizedReq)
	if unauthorizedResp.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized test stream status = %d, want %d body=%s", unauthorizedResp.Code, http.StatusUnauthorized, unauthorizedResp.Body.String())
	}

	forbiddenReq := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testRecord.ID+"/stream", nil)
	forbiddenReq.Header.Set("X-Auth-Request-User", "viewer-user")
	forbiddenReq.Header.Set("X-Stormrunner-Project-Roles", uuid.New().String()+"="+RoleViewer)
	forbiddenResp := httptest.NewRecorder()
	server.mux.ServeHTTP(forbiddenResp, forbiddenReq)
	if forbiddenResp.Code != http.StatusForbidden {
		t.Fatalf("foreign project test stream status = %d, want %d body=%s", forbiddenResp.Code, http.StatusForbidden, forbiddenResp.Body.String())
	}

	viewerReq := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testRecord.ID+"/stream", nil)
	viewerReq.Header.Set("X-Auth-Request-User", "viewer-user")
	viewerReq.Header.Set("X-Stormrunner-Project-Roles", project.ID+"="+RoleViewer)
	viewerResp := httptest.NewRecorder()
	server.mux.ServeHTTP(viewerResp, viewerReq)
	if viewerResp.Code == http.StatusUnauthorized || viewerResp.Code == http.StatusForbidden {
		t.Fatalf("project viewer test stream status = %d, want websocket upgrade attempt body=%s", viewerResp.Code, viewerResp.Body.String())
	}
	if viewerResp.Code != http.StatusBadRequest {
		t.Fatalf("project viewer test stream status = %d, want %d body=%s", viewerResp.Code, http.StatusBadRequest, viewerResp.Body.String())
	}
}

func TestTrustedHeaderAuthScopesGlobalWebSocketToProjectRoles(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "stormrunner.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	project := &storage.Project{ID: uuid.New().String(), Name: "Checkout", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	unauthorizedReq := httptest.NewRequest(http.MethodGet, "/api/v1/ws", nil)
	unauthorizedResp := httptest.NewRecorder()
	server.mux.ServeHTTP(unauthorizedResp, unauthorizedReq)
	if unauthorizedResp.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized global websocket status = %d, want %d body=%s", unauthorizedResp.Code, http.StatusUnauthorized, unauthorizedResp.Body.String())
	}

	viewerReq := httptest.NewRequest(http.MethodGet, "/api/v1/ws", nil)
	viewerReq.Header.Set("X-Auth-Request-User", "viewer-user")
	viewerReq.Header.Set("X-Stormrunner-Project-Roles", project.ID+"="+RoleViewer)
	viewerResp := httptest.NewRecorder()
	server.mux.ServeHTTP(viewerResp, viewerReq)
	if viewerResp.Code != http.StatusForbidden {
		t.Fatalf("project viewer unfiltered global websocket status = %d, want %d body=%s", viewerResp.Code, http.StatusForbidden, viewerResp.Body.String())
	}

	filteredReq := httptest.NewRequest(http.MethodGet, "/api/v1/ws?project_id="+project.ID, nil)
	filteredReq.Header.Set("X-Auth-Request-User", "viewer-user")
	filteredReq.Header.Set("X-Stormrunner-Project-Roles", project.ID+"="+RoleViewer)
	filteredResp := httptest.NewRecorder()
	server.mux.ServeHTTP(filteredResp, filteredReq)
	if filteredResp.Code != http.StatusBadRequest {
		t.Fatalf("project viewer filtered global websocket status = %d, want %d body=%s", filteredResp.Code, http.StatusBadRequest, filteredResp.Body.String())
	}
}

func TestTrustedHeaderAuthScopesApplicationListAndCreateToProjectRoles(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "stormrunner.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	project := &storage.Project{ID: uuid.New().String(), Name: "Project One", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}
	if err := store.SaveApplication(&storage.Application{ID: uuid.New().String(), ProjectID: project.ID, Name: "Payments", CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("SaveApplication returned error: %v", err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/applications", nil)
	listReq.Header.Set("X-Auth-Request-User", "viewer-user")
	listReq.Header.Set("X-Stormrunner-Project-Roles", project.ID+"="+RoleViewer)
	listResp := httptest.NewRecorder()
	server.mux.ServeHTTP(listResp, listReq)
	if listResp.Code != http.StatusForbidden {
		t.Fatalf("unfiltered application list status = %d, want %d body=%s", listResp.Code, http.StatusForbidden, listResp.Body.String())
	}

	filteredReq := httptest.NewRequest(http.MethodGet, "/api/v1/applications?project_id="+project.ID, nil)
	filteredReq.Header.Set("X-Auth-Request-User", "viewer-user")
	filteredReq.Header.Set("X-Stormrunner-Project-Roles", project.ID+"="+RoleViewer)
	filteredResp := httptest.NewRecorder()
	server.mux.ServeHTTP(filteredResp, filteredReq)
	if filteredResp.Code != http.StatusOK {
		t.Fatalf("filtered application list status = %d, want %d body=%s", filteredResp.Code, http.StatusOK, filteredResp.Body.String())
	}

	createBody := []byte(`{"name":"Checkout","project_id":"` + project.ID + `"}`)
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/applications", bytes.NewReader(createBody))
	createReq.Header.Set("Content-Type", "application/json")
	createReq.Header.Set("X-Auth-Request-User", "editor-user")
	createReq.Header.Set("X-Stormrunner-Project-Roles", project.ID+"="+RoleEditor)
	createResp := httptest.NewRecorder()
	server.mux.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("project editor application create status = %d, want %d body=%s", createResp.Code, http.StatusCreated, createResp.Body.String())
	}
	var created storage.Application
	if err := json.Unmarshal(createResp.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal application: %v", err)
	}
	if created.ProjectID != project.ID {
		t.Fatalf("created application project id = %q, want %q", created.ProjectID, project.ID)
	}
}

func TestTrustedHeaderAuthProjectRoleGuardsApplicationAndReleaseEndpoints(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "stormrunner.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	project := &storage.Project{ID: uuid.New().String(), Name: "Project One", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}
	application := &storage.Application{ID: uuid.New().String(), ProjectID: project.ID, Name: "Payments", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveApplication(application); err != nil {
		t.Fatalf("SaveApplication returned error: %v", err)
	}
	release := &storage.Release{ID: uuid.New().String(), ApplicationID: application.ID, Name: "2026.05", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveRelease(release); err != nil {
		t.Fatalf("SaveRelease returned error: %v", err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/applications/"+application.ID, nil)
	getReq.Header.Set("X-Auth-Request-User", "viewer-user")
	getReq.Header.Set("X-Stormrunner-Project-Roles", project.ID+"="+RoleViewer)
	getResp := httptest.NewRecorder()
	server.mux.ServeHTTP(getResp, getReq)
	if getResp.Code != http.StatusOK {
		t.Fatalf("application viewer GET status = %d, want %d body=%s", getResp.Code, http.StatusOK, getResp.Body.String())
	}

	updateReq := httptest.NewRequest(http.MethodPut, "/api/v1/applications/"+application.ID, bytes.NewReader([]byte(`{"name":"Renamed"}`)))
	updateReq.Header.Set("Content-Type", "application/json")
	updateReq.Header.Set("X-Auth-Request-User", "viewer-user")
	updateReq.Header.Set("X-Stormrunner-Project-Roles", project.ID+"="+RoleViewer)
	updateResp := httptest.NewRecorder()
	server.mux.ServeHTTP(updateResp, updateReq)
	if updateResp.Code != http.StatusForbidden {
		t.Fatalf("application viewer PUT status = %d, want %d body=%s", updateResp.Code, http.StatusForbidden, updateResp.Body.String())
	}

	releasesReq := httptest.NewRequest(http.MethodGet, "/api/v1/applications/"+application.ID+"/releases", nil)
	releasesReq.Header.Set("X-Auth-Request-User", "viewer-user")
	releasesReq.Header.Set("X-Stormrunner-Project-Roles", project.ID+"="+RoleViewer)
	releasesResp := httptest.NewRecorder()
	server.mux.ServeHTTP(releasesResp, releasesReq)
	if releasesResp.Code != http.StatusOK {
		t.Fatalf("release list viewer GET status = %d, want %d body=%s", releasesResp.Code, http.StatusOK, releasesResp.Body.String())
	}

	archiveReq := httptest.NewRequest(http.MethodPost, "/api/v1/releases/"+release.ID+"/archive", nil)
	archiveReq.Header.Set("X-Auth-Request-User", "viewer-user")
	archiveReq.Header.Set("X-Stormrunner-Project-Roles", project.ID+"="+RoleViewer)
	archiveResp := httptest.NewRecorder()
	server.mux.ServeHTTP(archiveResp, archiveReq)
	if archiveResp.Code != http.StatusForbidden {
		t.Fatalf("release viewer archive status = %d, want %d body=%s", archiveResp.Code, http.StatusForbidden, archiveResp.Body.String())
	}

	createReleaseReq := httptest.NewRequest(http.MethodPost, "/api/v1/applications/"+application.ID+"/releases", bytes.NewReader([]byte(`{"name":"2026.06"}`)))
	createReleaseReq.Header.Set("Content-Type", "application/json")
	createReleaseReq.Header.Set("X-Auth-Request-User", "editor-user")
	createReleaseReq.Header.Set("X-Stormrunner-Project-Roles", project.ID+"="+RoleEditor)
	createReleaseResp := httptest.NewRecorder()
	server.mux.ServeHTTP(createReleaseResp, createReleaseReq)
	if createReleaseResp.Code != http.StatusCreated {
		t.Fatalf("release editor create status = %d, want %d body=%s", createReleaseResp.Code, http.StatusCreated, createReleaseResp.Body.String())
	}
}

func TestTrustedHeaderAuthScopesTestListByContextToProjectRoles(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "stormrunner.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	project := &storage.Project{ID: uuid.New().String(), Name: "Project One", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}
	application := &storage.Application{ID: uuid.New().String(), ProjectID: project.ID, Name: "Payments", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveApplication(application); err != nil {
		t.Fatalf("SaveApplication returned error: %v", err)
	}
	release := &storage.Release{ID: uuid.New().String(), ApplicationID: application.ID, Name: "2026.05", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveRelease(release); err != nil {
		t.Fatalf("SaveRelease returned error: %v", err)
	}
	if err := store.SaveTest(&storage.TestRecord{ID: uuid.New().String(), ProjectID: project.ID, Name: "Checkout", Status: "COMPLETE", ApplicationID: application.ID, ReleaseID: release.ID, StartTime: time.Now()}); err != nil {
		t.Fatalf("SaveTest returned error: %v", err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/tests?application_id="+application.ID+"&release_id="+release.ID, nil)
	listReq.Header.Set("X-Auth-Request-User", "viewer-user")
	listReq.Header.Set("X-Stormrunner-Project-Roles", project.ID+"="+RoleViewer)
	listResp := httptest.NewRecorder()
	server.mux.ServeHTTP(listResp, listReq)
	if listResp.Code != http.StatusOK {
		t.Fatalf("project viewer test context list status = %d, want %d body=%s", listResp.Code, http.StatusOK, listResp.Body.String())
	}

	otherProject := &storage.Project{ID: uuid.New().String(), Name: "Project Two", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveProject(otherProject); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}
	foreignApplication := &storage.Application{ID: uuid.New().String(), ProjectID: otherProject.ID, Name: "Billing", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveApplication(foreignApplication); err != nil {
		t.Fatalf("SaveApplication returned error: %v", err)
	}
	foreignRelease := &storage.Release{ID: uuid.New().String(), ApplicationID: foreignApplication.ID, Name: "2026.07", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveRelease(foreignRelease); err != nil {
		t.Fatalf("SaveRelease returned error: %v", err)
	}

	inconsistentReq := httptest.NewRequest(http.MethodGet, "/api/v1/tests?application_id="+application.ID+"&release_id="+foreignRelease.ID, nil)
	inconsistentReq.Header.Set("X-Auth-Request-User", "viewer-user")
	inconsistentReq.Header.Set("X-Stormrunner-Project-Roles", project.ID+"="+RoleViewer)
	inconsistentResp := httptest.NewRecorder()
	server.mux.ServeHTTP(inconsistentResp, inconsistentReq)
	if inconsistentResp.Code != http.StatusBadRequest {
		t.Fatalf("inconsistent test context list status = %d, want %d body=%s", inconsistentResp.Code, http.StatusBadRequest, inconsistentResp.Body.String())
	}
}

func TestTrustedHeaderAuthResolvesFolderProjectForMutations(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "stormrunner.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	project := &storage.Project{ID: uuid.New().String(), Name: "Project One", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}
	folder := &storage.Folder{ID: uuid.New().String(), ProjectID: project.ID, Name: "Results", Category: "results", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveFolder(folder); err != nil {
		t.Fatalf("SaveFolder returned error: %v", err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	body := []byte(`{"name":"Archived Results"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/folders/"+folder.ID, bytes.NewReader(body))
	req.Header.Set("X-Auth-Request-User", "project-admin")
	req.Header.Set("X-Stormrunner-Project-Roles", project.ID+"="+RoleProjectAdmin)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("folder update status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	updated, err := store.GetFolder(folder.ID)
	if err != nil {
		t.Fatalf("GetFolder returned error: %v", err)
	}
	if updated.Name != "Archived Results" {
		t.Fatalf("folder name = %q, want Archived Results", updated.Name)
	}
}

func TestTrustedHeaderAuthProjectRoleGuardsStoredScriptEndpoints(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "stormrunner.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	project := &storage.Project{ID: uuid.New().String(), Name: "Project One", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}
	script := &dsl.Script{
		ID:        uuid.New().String(),
		Name:      "Checkout",
		Version:   "1.0.0",
		Protocol:  "http",
		ProjectID: project.ID,
		Config:    dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions:   []dsl.Action{{Name: "Open", Type: "http", Method: "GET", Target: "/"}},
	}
	if err := store.SaveScript(script); err != nil {
		t.Fatalf("SaveScript returned error: %v", err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/"+script.ID, nil)
	getReq.Header.Set("X-Auth-Request-User", "viewer-user")
	getReq.Header.Set("X-Stormrunner-Project-Roles", project.ID+"="+RoleViewer)
	getResp := httptest.NewRecorder()
	server.mux.ServeHTTP(getResp, getReq)
	if getResp.Code != http.StatusOK {
		t.Fatalf("script viewer GET status = %d, want %d body=%s", getResp.Code, http.StatusOK, getResp.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/scripts/"+script.ID, nil)
	deleteReq.Header.Set("X-Auth-Request-User", "viewer-user")
	deleteReq.Header.Set("X-Stormrunner-Project-Roles", project.ID+"="+RoleViewer)
	deleteResp := httptest.NewRecorder()
	server.mux.ServeHTTP(deleteResp, deleteReq)
	if deleteResp.Code != http.StatusForbidden {
		t.Fatalf("script viewer DELETE status = %d, want %d body=%s", deleteResp.Code, http.StatusForbidden, deleteResp.Body.String())
	}

	editorDeleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/scripts/"+script.ID, nil)
	editorDeleteReq.Header.Set("X-Auth-Request-User", "editor-user")
	editorDeleteReq.Header.Set("X-Stormrunner-Project-Roles", project.ID+"="+RoleEditor)
	editorDeleteResp := httptest.NewRecorder()
	server.mux.ServeHTTP(editorDeleteResp, editorDeleteReq)
	if editorDeleteResp.Code != http.StatusOK {
		t.Fatalf("script editor DELETE status = %d, want %d body=%s", editorDeleteResp.Code, http.StatusOK, editorDeleteResp.Body.String())
	}
	if _, err := store.GetScript(script.ID); err == nil {
		t.Fatal("expected deleted script to be removed from storage")
	}
}

func TestTrustedHeaderAuthScopesScriptListAndCreateToProjectRoles(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "stormrunner.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	project := &storage.Project{ID: uuid.New().String(), Name: "Project One", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}
	existing := &dsl.Script{
		ID:        uuid.New().String(),
		Name:      "Existing",
		Version:   "1.0.0",
		Protocol:  "http",
		ProjectID: project.ID,
		Config:    dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions:   []dsl.Action{{Name: "Open", Type: "http", Method: "GET", Target: "/"}},
	}
	if err := store.SaveScript(existing); err != nil {
		t.Fatalf("SaveScript returned error: %v", err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/scripts", nil)
	listReq.Header.Set("X-Auth-Request-User", "viewer-user")
	listReq.Header.Set("X-Stormrunner-Project-Roles", project.ID+"="+RoleViewer)
	listResp := httptest.NewRecorder()
	server.mux.ServeHTTP(listResp, listReq)
	if listResp.Code != http.StatusForbidden {
		t.Fatalf("unfiltered script list status = %d, want %d body=%s", listResp.Code, http.StatusForbidden, listResp.Body.String())
	}

	filteredReq := httptest.NewRequest(http.MethodGet, "/api/v1/scripts?project_id="+project.ID, nil)
	filteredReq.Header.Set("X-Auth-Request-User", "viewer-user")
	filteredReq.Header.Set("X-Stormrunner-Project-Roles", project.ID+"="+RoleViewer)
	filteredResp := httptest.NewRecorder()
	server.mux.ServeHTTP(filteredResp, filteredReq)
	if filteredResp.Code != http.StatusOK {
		t.Fatalf("filtered script list status = %d, want %d body=%s", filteredResp.Code, http.StatusOK, filteredResp.Body.String())
	}

	body := []byte(`{"project_id":"` + project.ID + `","script":{"name":"Created","version":"1.0.0","protocol":"http","config":{"base_url":"https://example.com"},"actions":[{"name":"Open","type":"http","method":"GET","target":"/"}]}}`)
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts", bytes.NewReader(body))
	createReq.Header.Set("Content-Type", "application/json")
	createReq.Header.Set("X-Auth-Request-User", "editor-user")
	createReq.Header.Set("X-Stormrunner-Project-Roles", project.ID+"="+RoleEditor)
	createResp := httptest.NewRecorder()
	server.mux.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("project editor create status = %d, want %d body=%s", createResp.Code, http.StatusCreated, createResp.Body.String())
	}
}

func TestTrustedHeaderAuthProjectRoleGuardsDraftScriptExecution(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)
	projectID := uuid.New().String()

	replayBody := []byte(`{"script":{"name":"Draft Replay","version":"1.0.0","protocol":"http","project_id":"` + projectID + `","config":{"base_url":"https://example.com"},"actions":[]}}`)
	viewerReplayReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/replay", bytes.NewReader(replayBody))
	viewerReplayReq.Header.Set("Content-Type", "application/json")
	viewerReplayReq.Header.Set("X-Auth-Request-User", "viewer-user")
	viewerReplayReq.Header.Set("X-Stormrunner-Project-Roles", projectID+"="+RoleViewer)
	viewerReplayResp := httptest.NewRecorder()
	server.mux.ServeHTTP(viewerReplayResp, viewerReplayReq)
	if viewerReplayResp.Code != http.StatusForbidden {
		t.Fatalf("draft replay viewer status = %d, want %d body=%s", viewerReplayResp.Code, http.StatusForbidden, viewerReplayResp.Body.String())
	}

	editorReplayReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/replay", bytes.NewReader(replayBody))
	editorReplayReq.Header.Set("Content-Type", "application/json")
	editorReplayReq.Header.Set("X-Auth-Request-User", "editor-user")
	editorReplayReq.Header.Set("X-Stormrunner-Project-Roles", projectID+"="+RoleEditor)
	editorReplayResp := httptest.NewRecorder()
	server.mux.ServeHTTP(editorReplayResp, editorReplayReq)
	if editorReplayResp.Code == http.StatusForbidden || editorReplayResp.Code == http.StatusUnauthorized {
		t.Fatalf("draft replay editor status = %d, want auth to pass body=%s", editorReplayResp.Code, editorReplayResp.Body.String())
	}

	browserBody := []byte(`{"script":{"name":"Draft Browser Replay","version":"1.0.0","protocol":"browser","project_id":"` + projectID + `","config":{"browser":{"engine":"chromium"}},"actions":[]}}`)
	viewerBrowserReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/browser-replay/sessions", bytes.NewReader(browserBody))
	viewerBrowserReq.Header.Set("Content-Type", "application/json")
	viewerBrowserReq.Header.Set("X-Auth-Request-User", "viewer-user")
	viewerBrowserReq.Header.Set("X-Stormrunner-Project-Roles", projectID+"="+RoleViewer)
	viewerBrowserResp := httptest.NewRecorder()
	server.mux.ServeHTTP(viewerBrowserResp, viewerBrowserReq)
	if viewerBrowserResp.Code != http.StatusForbidden {
		t.Fatalf("draft browser replay viewer status = %d, want %d body=%s", viewerBrowserResp.Code, http.StatusForbidden, viewerBrowserResp.Body.String())
	}

	editorBrowserReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/browser-replay/sessions", bytes.NewReader(browserBody))
	editorBrowserReq.Header.Set("Content-Type", "application/json")
	editorBrowserReq.Header.Set("X-Auth-Request-User", "editor-user")
	editorBrowserReq.Header.Set("X-Stormrunner-Project-Roles", projectID+"="+RoleEditor)
	editorBrowserResp := httptest.NewRecorder()
	server.mux.ServeHTTP(editorBrowserResp, editorBrowserReq)
	if editorBrowserResp.Code == http.StatusForbidden || editorBrowserResp.Code == http.StatusUnauthorized {
		t.Fatalf("draft browser replay editor status = %d, want auth to pass body=%s", editorBrowserResp.Code, editorBrowserResp.Body.String())
	}
}

func TestTrustedHeaderAuthProjectRoleGuardsJMeterImportSave(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)
	projectID := uuid.New().String()
	body := []byte(`{"scripts":[{"name":"Imported Flow","version":"1.0.0","protocol":"http","project_id":"` + projectID + `","config":{"base_url":"https://example.com"},"actions":[{"name":"Open","type":"http","method":"GET","target":"/"}]}]}`)

	viewerReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/jmeter/save", bytes.NewReader(body))
	viewerReq.Header.Set("Content-Type", "application/json")
	viewerReq.Header.Set("X-Auth-Request-User", "viewer-user")
	viewerReq.Header.Set("X-Stormrunner-Project-Roles", projectID+"="+RoleViewer)
	viewerResp := httptest.NewRecorder()
	server.mux.ServeHTTP(viewerResp, viewerReq)
	if viewerResp.Code != http.StatusForbidden {
		t.Fatalf("jmeter save viewer status = %d, want %d body=%s", viewerResp.Code, http.StatusForbidden, viewerResp.Body.String())
	}

	editorReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/jmeter/save", bytes.NewReader(body))
	editorReq.Header.Set("Content-Type", "application/json")
	editorReq.Header.Set("X-Auth-Request-User", "editor-user")
	editorReq.Header.Set("X-Stormrunner-Project-Roles", projectID+"="+RoleEditor)
	editorResp := httptest.NewRecorder()
	server.mux.ServeHTTP(editorResp, editorReq)
	if editorResp.Code != http.StatusCreated {
		t.Fatalf("jmeter save editor status = %d, want %d body=%s", editorResp.Code, http.StatusCreated, editorResp.Body.String())
	}
}

func TestTrustedHeaderAuthRequiresViewerForJMeterSupportMatrix(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	unauthorizedReq := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/import/jmeter/support-matrix", nil)
	unauthorizedResp := httptest.NewRecorder()
	server.mux.ServeHTTP(unauthorizedResp, unauthorizedReq)
	if unauthorizedResp.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized support matrix status = %d, want %d body=%s", unauthorizedResp.Code, http.StatusUnauthorized, unauthorizedResp.Body.String())
	}

	viewerReq := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/import/jmeter/support-matrix", nil)
	viewerReq.Header.Set("X-Auth-Request-User", "viewer-user")
	viewerReq.Header.Set("X-Stormrunner-Roles", RoleViewer)
	viewerResp := httptest.NewRecorder()
	server.mux.ServeHTTP(viewerResp, viewerReq)
	if viewerResp.Code != http.StatusOK {
		t.Fatalf("viewer support matrix status = %d, want %d body=%s", viewerResp.Code, http.StatusOK, viewerResp.Body.String())
	}
}

func TestTrustedHeaderAuthProjectRoleGuardsImportPreviewEndpoints(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)
	projectID := uuid.New().String()

	viewerLoadRunnerReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/loadrunner?project_id="+projectID, bytes.NewReader([]byte("zip")))
	viewerLoadRunnerReq.Header.Set("Content-Type", "application/zip")
	viewerLoadRunnerReq.Header.Set("X-Auth-Request-User", "viewer-user")
	viewerLoadRunnerReq.Header.Set("X-Stormrunner-Project-Roles", projectID+"="+RoleViewer)
	viewerLoadRunnerResp := httptest.NewRecorder()
	server.mux.ServeHTTP(viewerLoadRunnerResp, viewerLoadRunnerReq)
	if viewerLoadRunnerResp.Code != http.StatusForbidden {
		t.Fatalf("loadrunner import viewer status = %d, want %d body=%s", viewerLoadRunnerResp.Code, http.StatusForbidden, viewerLoadRunnerResp.Body.String())
	}

	editorLoadRunnerReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/loadrunner?project_id="+projectID, bytes.NewReader([]byte("zip")))
	editorLoadRunnerReq.Header.Set("Content-Type", "application/zip")
	editorLoadRunnerReq.Header.Set("X-Auth-Request-User", "editor-user")
	editorLoadRunnerReq.Header.Set("X-Stormrunner-Project-Roles", projectID+"="+RoleEditor)
	editorLoadRunnerResp := httptest.NewRecorder()
	server.mux.ServeHTTP(editorLoadRunnerResp, editorLoadRunnerReq)
	if editorLoadRunnerResp.Code == http.StatusForbidden || editorLoadRunnerResp.Code == http.StatusUnauthorized {
		t.Fatalf("loadrunner import editor status = %d, want auth to pass body=%s", editorLoadRunnerResp.Code, editorLoadRunnerResp.Body.String())
	}

	viewerSampleReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/sample", bytes.NewReader([]byte(`{"project_id":"`+projectID+`","format":"raw_http","content":"GET / HTTP/1.1\nHost: example.com\n\n"}`)))
	viewerSampleReq.Header.Set("Content-Type", "application/json")
	viewerSampleReq.Header.Set("X-Auth-Request-User", "viewer-user")
	viewerSampleReq.Header.Set("X-Stormrunner-Project-Roles", projectID+"="+RoleViewer)
	viewerSampleResp := httptest.NewRecorder()
	server.mux.ServeHTTP(viewerSampleResp, viewerSampleReq)
	if viewerSampleResp.Code != http.StatusForbidden {
		t.Fatalf("sample import viewer status = %d, want %d body=%s", viewerSampleResp.Code, http.StatusForbidden, viewerSampleResp.Body.String())
	}

	editorSampleReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/sample", bytes.NewReader([]byte(`{"project_id":"`+projectID+`","format":"raw_http","content":"GET / HTTP/1.1\nHost: example.com\n\n"}`)))
	editorSampleReq.Header.Set("Content-Type", "application/json")
	editorSampleReq.Header.Set("X-Auth-Request-User", "editor-user")
	editorSampleReq.Header.Set("X-Stormrunner-Project-Roles", projectID+"="+RoleEditor)
	editorSampleResp := httptest.NewRecorder()
	server.mux.ServeHTTP(editorSampleResp, editorSampleReq)
	if editorSampleResp.Code == http.StatusForbidden || editorSampleResp.Code == http.StatusUnauthorized {
		t.Fatalf("sample import editor status = %d, want auth to pass body=%s", editorSampleResp.Code, editorSampleResp.Body.String())
	}
}

func TestTrustedHeaderAuthScopesRecorderListAndStartToProjectRoles(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)
	projectID := uuid.New().String()
	_, err := server.recorder.StartSessionForProject("rec-1", "Checkout Recording", projectID, recorder.SessionFilters{}, nil)
	if err != nil {
		t.Fatalf("StartSessionForProject returned error: %v", err)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/recorder", nil)
	listReq.Header.Set("X-Auth-Request-User", "viewer-user")
	listReq.Header.Set("X-Stormrunner-Project-Roles", projectID+"="+RoleViewer)
	listResp := httptest.NewRecorder()
	server.mux.ServeHTTP(listResp, listReq)
	if listResp.Code != http.StatusForbidden {
		t.Fatalf("unfiltered recorder list status = %d, want %d body=%s", listResp.Code, http.StatusForbidden, listResp.Body.String())
	}

	filteredReq := httptest.NewRequest(http.MethodGet, "/api/v1/recorder?project_id="+projectID, nil)
	filteredReq.Header.Set("X-Auth-Request-User", "viewer-user")
	filteredReq.Header.Set("X-Stormrunner-Project-Roles", projectID+"="+RoleViewer)
	filteredResp := httptest.NewRecorder()
	server.mux.ServeHTTP(filteredResp, filteredReq)
	if filteredResp.Code != http.StatusOK {
		t.Fatalf("filtered recorder list status = %d, want %d body=%s", filteredResp.Code, http.StatusOK, filteredResp.Body.String())
	}

	statusReq := httptest.NewRequest(http.MethodGet, "/api/v1/recorder/status", nil)
	statusReq.Header.Set("X-Auth-Request-User", "viewer-user")
	statusReq.Header.Set("X-Stormrunner-Project-Roles", projectID+"="+RoleViewer)
	statusResp := httptest.NewRecorder()
	server.mux.ServeHTTP(statusResp, statusReq)
	if statusResp.Code != http.StatusForbidden {
		t.Fatalf("unfiltered recorder status = %d, want %d body=%s", statusResp.Code, http.StatusForbidden, statusResp.Body.String())
	}

	filteredStatusReq := httptest.NewRequest(http.MethodGet, "/api/v1/recorder/status?project_id="+projectID, nil)
	filteredStatusReq.Header.Set("X-Auth-Request-User", "viewer-user")
	filteredStatusReq.Header.Set("X-Stormrunner-Project-Roles", projectID+"="+RoleViewer)
	filteredStatusResp := httptest.NewRecorder()
	server.mux.ServeHTTP(filteredStatusResp, filteredStatusReq)
	if filteredStatusResp.Code != http.StatusOK {
		t.Fatalf("filtered recorder status = %d, want %d body=%s", filteredStatusResp.Code, http.StatusOK, filteredStatusResp.Body.String())
	}

	body := []byte(`{"name":"Checkout Recording","project_id":"` + projectID + `"}`)
	viewerStartReq := httptest.NewRequest(http.MethodPost, "/api/v1/recorder/start", bytes.NewReader(body))
	viewerStartReq.Header.Set("Content-Type", "application/json")
	viewerStartReq.Header.Set("X-Auth-Request-User", "viewer-user")
	viewerStartReq.Header.Set("X-Stormrunner-Project-Roles", projectID+"="+RoleViewer)
	viewerStartResp := httptest.NewRecorder()
	server.mux.ServeHTTP(viewerStartResp, viewerStartReq)
	if viewerStartResp.Code != http.StatusForbidden {
		t.Fatalf("recorder viewer start status = %d, want %d body=%s", viewerStartResp.Code, http.StatusForbidden, viewerStartResp.Body.String())
	}

	editorStartReq := httptest.NewRequest(http.MethodPost, "/api/v1/recorder/start", bytes.NewReader(body))
	editorStartReq.Header.Set("Content-Type", "application/json")
	editorStartReq.Header.Set("X-Auth-Request-User", "editor-user")
	editorStartReq.Header.Set("X-Stormrunner-Project-Roles", projectID+"="+RoleEditor)
	editorStartResp := httptest.NewRecorder()
	server.mux.ServeHTTP(editorStartResp, editorStartReq)
	if editorStartResp.Code != http.StatusConflict {
		t.Fatalf("recorder editor start status = %d, want %d body=%s", editorStartResp.Code, http.StatusConflict, editorStartResp.Body.String())
	}
}

func TestTrustedHeaderAuthProjectRoleGuardsRecorderSessionEndpoints(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)
	projectID := uuid.New().String()
	session, err := server.recorder.StartSessionForProject("rec-1", "Checkout Recording", projectID, recorder.SessionFilters{}, nil)
	if err != nil {
		t.Fatalf("StartSessionForProject returned error: %v", err)
	}
	session.Requests = append(session.Requests, recorder.RecordedRequest{
		Seq:        1,
		Method:     http.MethodGet,
		URL:        "https://shop.example.com/api/cart",
		Path:       "/api/cart",
		StatusCode: http.StatusOK,
	})

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/recorder/rec-1", nil)
	getReq.Header.Set("X-Auth-Request-User", "viewer-user")
	getReq.Header.Set("X-Stormrunner-Project-Roles", projectID+"="+RoleViewer)
	getResp := httptest.NewRecorder()
	server.mux.ServeHTTP(getResp, getReq)
	if getResp.Code != http.StatusOK {
		t.Fatalf("recorder viewer GET status = %d, want %d body=%s", getResp.Code, http.StatusOK, getResp.Body.String())
	}

	previewReq := httptest.NewRequest(http.MethodPost, "/api/v1/recorder/rec-1/convert/preview", bytes.NewReader([]byte(`{"name":"Checkout Preview"}`)))
	previewReq.Header.Set("Content-Type", "application/json")
	previewReq.Header.Set("X-Auth-Request-User", "viewer-user")
	previewReq.Header.Set("X-Stormrunner-Project-Roles", projectID+"="+RoleViewer)
	previewResp := httptest.NewRecorder()
	server.mux.ServeHTTP(previewResp, previewReq)
	if previewResp.Code != http.StatusForbidden {
		t.Fatalf("recorder viewer preview status = %d, want %d body=%s", previewResp.Code, http.StatusForbidden, previewResp.Body.String())
	}

	editorPreviewReq := httptest.NewRequest(http.MethodPost, "/api/v1/recorder/rec-1/convert/preview", bytes.NewReader([]byte(`{"name":"Checkout Preview"}`)))
	editorPreviewReq.Header.Set("Content-Type", "application/json")
	editorPreviewReq.Header.Set("X-Auth-Request-User", "editor-user")
	editorPreviewReq.Header.Set("X-Stormrunner-Project-Roles", projectID+"="+RoleEditor)
	editorPreviewResp := httptest.NewRecorder()
	server.mux.ServeHTTP(editorPreviewResp, editorPreviewReq)
	if editorPreviewResp.Code != http.StatusOK {
		t.Fatalf("recorder editor preview status = %d, want %d body=%s", editorPreviewResp.Code, http.StatusOK, editorPreviewResp.Body.String())
	}

	stopReq := httptest.NewRequest(http.MethodPost, "/api/v1/recorder/rec-1/stop", nil)
	stopReq.Header.Set("X-Auth-Request-User", "viewer-user")
	stopReq.Header.Set("X-Stormrunner-Project-Roles", projectID+"="+RoleViewer)
	stopResp := httptest.NewRecorder()
	server.mux.ServeHTTP(stopResp, stopReq)
	if stopResp.Code != http.StatusForbidden {
		t.Fatalf("recorder viewer stop status = %d, want %d body=%s", stopResp.Code, http.StatusForbidden, stopResp.Body.String())
	}

	editorStopReq := httptest.NewRequest(http.MethodPost, "/api/v1/recorder/rec-1/stop", nil)
	editorStopReq.Header.Set("X-Auth-Request-User", "editor-user")
	editorStopReq.Header.Set("X-Stormrunner-Project-Roles", projectID+"="+RoleEditor)
	editorStopResp := httptest.NewRecorder()
	server.mux.ServeHTTP(editorStopResp, editorStopReq)
	if editorStopResp.Code != http.StatusOK {
		t.Fatalf("recorder editor stop status = %d, want %d body=%s", editorStopResp.Code, http.StatusOK, editorStopResp.Body.String())
	}
}

func TestTrustedHeaderAuthProjectRoleGuardsBrowserReplaySessionEndpoints(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)
	projectID := uuid.New().String()
	pauseBody := []byte(`{"script":{"name":"Draft Browser Replay","version":"1.0.0","protocol":"browser","project_id":"` + projectID + `","config":{"browser":{"engine":"chromium"}},"actions":[{"name":"Open homepage","type":"browser_navigate","target":"/"}]},"start_paused":true}`)
	startReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/browser-replay/sessions", bytes.NewReader(pauseBody))
	startReq.Header.Set("Content-Type", "application/json")
	startReq.Header.Set("X-Auth-Request-User", "editor-user")
	startReq.Header.Set("X-Stormrunner-Project-Roles", projectID+"="+RoleEditor)
	startResp := httptest.NewRecorder()
	server.mux.ServeHTTP(startResp, startReq)
	if startResp.Code != http.StatusOK {
		t.Fatalf("browser replay editor start status = %d, want %d body=%s", startResp.Code, http.StatusOK, startResp.Body.String())
	}

	var startPayload struct {
		ReplaySessionID string `json:"replay_session_id"`
	}
	if err := json.Unmarshal(startResp.Body.Bytes(), &startPayload); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if startPayload.ReplaySessionID == "" {
		t.Fatal("expected replay_session_id in browser replay response")
	}

	viewerGetReq := httptest.NewRequest(http.MethodGet, "/api/v1/browser-replay/sessions/"+startPayload.ReplaySessionID, nil)
	viewerGetReq.Header.Set("X-Auth-Request-User", "viewer-user")
	viewerGetReq.Header.Set("X-Stormrunner-Project-Roles", projectID+"="+RoleViewer)
	viewerGetResp := httptest.NewRecorder()
	server.mux.ServeHTTP(viewerGetResp, viewerGetReq)
	if viewerGetResp.Code != http.StatusOK {
		t.Fatalf("browser replay viewer GET status = %d, want %d body=%s", viewerGetResp.Code, http.StatusOK, viewerGetResp.Body.String())
	}

	viewerResumeReq := httptest.NewRequest(http.MethodPost, "/api/v1/browser-replay/sessions/"+startPayload.ReplaySessionID+"/resume", nil)
	viewerResumeReq.Header.Set("X-Auth-Request-User", "viewer-user")
	viewerResumeReq.Header.Set("X-Stormrunner-Project-Roles", projectID+"="+RoleViewer)
	viewerResumeResp := httptest.NewRecorder()
	server.mux.ServeHTTP(viewerResumeResp, viewerResumeReq)
	if viewerResumeResp.Code != http.StatusForbidden {
		t.Fatalf("browser replay viewer resume status = %d, want %d body=%s", viewerResumeResp.Code, http.StatusForbidden, viewerResumeResp.Body.String())
	}

	editorResumeReq := httptest.NewRequest(http.MethodPost, "/api/v1/browser-replay/sessions/"+startPayload.ReplaySessionID+"/resume", nil)
	editorResumeReq.Header.Set("X-Auth-Request-User", "editor-user")
	editorResumeReq.Header.Set("X-Stormrunner-Project-Roles", projectID+"="+RoleEditor)
	editorResumeResp := httptest.NewRecorder()
	server.mux.ServeHTTP(editorResumeResp, editorResumeReq)
	if editorResumeResp.Code != http.StatusOK {
		t.Fatalf("browser replay editor resume status = %d, want %d body=%s", editorResumeResp.Code, http.StatusOK, editorResumeResp.Body.String())
	}

	editorPauseReq := httptest.NewRequest(http.MethodPost, "/api/v1/browser-replay/sessions/"+startPayload.ReplaySessionID+"/pause", nil)
	editorPauseReq.Header.Set("X-Auth-Request-User", "editor-user")
	editorPauseReq.Header.Set("X-Stormrunner-Project-Roles", projectID+"="+RoleEditor)
	editorPauseResp := httptest.NewRecorder()
	server.mux.ServeHTTP(editorPauseResp, editorPauseReq)
	if editorPauseResp.Code != http.StatusOK {
		t.Fatalf("browser replay editor pause status = %d, want %d body=%s", editorPauseResp.Code, http.StatusOK, editorPauseResp.Body.String())
	}

	editorStopReq := httptest.NewRequest(http.MethodPost, "/api/v1/browser-replay/sessions/"+startPayload.ReplaySessionID+"/stop", nil)
	editorStopReq.Header.Set("X-Auth-Request-User", "editor-user")
	editorStopReq.Header.Set("X-Stormrunner-Project-Roles", projectID+"="+RoleEditor)
	editorStopResp := httptest.NewRecorder()
	server.mux.ServeHTTP(editorStopResp, editorStopReq)
	if editorStopResp.Code != http.StatusOK {
		t.Fatalf("browser replay editor stop status = %d, want %d body=%s", editorStopResp.Code, http.StatusOK, editorStopResp.Body.String())
	}
}

func TestTrustedHeaderAuthScopesDataPoolListAndCreateToProjectRoles(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "stormrunner.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	project := &storage.Project{ID: uuid.New().String(), Name: "Checkout", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}
	if err := store.SaveDataPool(&storage.SharedDataPool{ID: uuid.New().String(), Name: "Customers", ProjectID: project.ID, CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("SaveDataPool returned error: %v", err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/data-pools", nil)
	listReq.Header.Set("X-Auth-Request-User", "viewer-user")
	listReq.Header.Set("X-Stormrunner-Project-Roles", project.ID+"="+RoleViewer)
	listResp := httptest.NewRecorder()
	server.mux.ServeHTTP(listResp, listReq)
	if listResp.Code != http.StatusForbidden {
		t.Fatalf("unfiltered data pool list status = %d, want %d body=%s", listResp.Code, http.StatusForbidden, listResp.Body.String())
	}

	filteredReq := httptest.NewRequest(http.MethodGet, "/api/v1/data-pools?project_id="+project.ID, nil)
	filteredReq.Header.Set("X-Auth-Request-User", "viewer-user")
	filteredReq.Header.Set("X-Stormrunner-Project-Roles", project.ID+"="+RoleViewer)
	filteredResp := httptest.NewRecorder()
	server.mux.ServeHTTP(filteredResp, filteredReq)
	if filteredResp.Code != http.StatusOK {
		t.Fatalf("filtered data pool list status = %d, want %d body=%s", filteredResp.Code, http.StatusOK, filteredResp.Body.String())
	}

	createBody := []byte(`{"name":"New Pool","project_id":"` + project.ID + `"}`)
	viewerCreateReq := httptest.NewRequest(http.MethodPost, "/api/v1/data-pools", bytes.NewReader(createBody))
	viewerCreateReq.Header.Set("Content-Type", "application/json")
	viewerCreateReq.Header.Set("X-Auth-Request-User", "viewer-user")
	viewerCreateReq.Header.Set("X-Stormrunner-Project-Roles", project.ID+"="+RoleViewer)
	viewerCreateResp := httptest.NewRecorder()
	server.mux.ServeHTTP(viewerCreateResp, viewerCreateReq)
	if viewerCreateResp.Code != http.StatusForbidden {
		t.Fatalf("data pool viewer create status = %d, want %d body=%s", viewerCreateResp.Code, http.StatusForbidden, viewerCreateResp.Body.String())
	}

	editorCreateReq := httptest.NewRequest(http.MethodPost, "/api/v1/data-pools", bytes.NewReader(createBody))
	editorCreateReq.Header.Set("Content-Type", "application/json")
	editorCreateReq.Header.Set("X-Auth-Request-User", "editor-user")
	editorCreateReq.Header.Set("X-Stormrunner-Project-Roles", project.ID+"="+RoleEditor)
	editorCreateResp := httptest.NewRecorder()
	server.mux.ServeHTTP(editorCreateResp, editorCreateReq)
	if editorCreateResp.Code != http.StatusCreated {
		t.Fatalf("data pool editor create status = %d, want %d body=%s", editorCreateResp.Code, http.StatusCreated, editorCreateResp.Body.String())
	}
}

func TestTrustedHeaderAuthProjectRoleGuardsDataPoolEndpoints(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "stormrunner.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	project := &storage.Project{ID: uuid.New().String(), Name: "Checkout", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}
	pool := &storage.SharedDataPool{ID: uuid.New().String(), Name: "Customers", ProjectID: project.ID, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveDataPool(pool); err != nil {
		t.Fatalf("SaveDataPool returned error: %v", err)
	}
	if err := store.SaveDataPoolRow(&storage.SharedDataPoolRow{ID: uuid.New().String(), PoolID: pool.ID, Data: map[string]string{"customer_id": "C-100"}, CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("SaveDataPoolRow returned error: %v", err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/data-pools/"+pool.ID, nil)
	getReq.Header.Set("X-Auth-Request-User", "viewer-user")
	getReq.Header.Set("X-Stormrunner-Project-Roles", project.ID+"="+RoleViewer)
	getResp := httptest.NewRecorder()
	server.mux.ServeHTTP(getResp, getReq)
	if getResp.Code != http.StatusOK {
		t.Fatalf("data pool viewer GET status = %d, want %d body=%s", getResp.Code, http.StatusOK, getResp.Body.String())
	}

	rowsReq := httptest.NewRequest(http.MethodGet, "/api/v1/data-pools/"+pool.ID+"/rows", nil)
	rowsReq.Header.Set("X-Auth-Request-User", "viewer-user")
	rowsReq.Header.Set("X-Stormrunner-Project-Roles", project.ID+"="+RoleViewer)
	rowsResp := httptest.NewRecorder()
	server.mux.ServeHTTP(rowsResp, rowsReq)
	if rowsResp.Code != http.StatusOK {
		t.Fatalf("data pool viewer rows GET status = %d, want %d body=%s", rowsResp.Code, http.StatusOK, rowsResp.Body.String())
	}

	viewerRowCreateReq := httptest.NewRequest(http.MethodPost, "/api/v1/data-pools/"+pool.ID+"/rows", bytes.NewReader([]byte(`{"data":{"customer_id":"C-101"}}`)))
	viewerRowCreateReq.Header.Set("Content-Type", "application/json")
	viewerRowCreateReq.Header.Set("X-Auth-Request-User", "viewer-user")
	viewerRowCreateReq.Header.Set("X-Stormrunner-Project-Roles", project.ID+"="+RoleViewer)
	viewerRowCreateResp := httptest.NewRecorder()
	server.mux.ServeHTTP(viewerRowCreateResp, viewerRowCreateReq)
	if viewerRowCreateResp.Code != http.StatusForbidden {
		t.Fatalf("data pool viewer row create status = %d, want %d body=%s", viewerRowCreateResp.Code, http.StatusForbidden, viewerRowCreateResp.Body.String())
	}

	editorUpdateReq := httptest.NewRequest(http.MethodPut, "/api/v1/data-pools/"+pool.ID, bytes.NewReader([]byte(`{"name":"Customers Updated"}`)))
	editorUpdateReq.Header.Set("Content-Type", "application/json")
	editorUpdateReq.Header.Set("X-Auth-Request-User", "editor-user")
	editorUpdateReq.Header.Set("X-Stormrunner-Project-Roles", project.ID+"="+RoleEditor)
	editorUpdateResp := httptest.NewRecorder()
	server.mux.ServeHTTP(editorUpdateResp, editorUpdateReq)
	if editorUpdateResp.Code != http.StatusOK {
		t.Fatalf("data pool editor update status = %d, want %d body=%s", editorUpdateResp.Code, http.StatusOK, editorUpdateResp.Body.String())
	}

	editorRowCreateReq := httptest.NewRequest(http.MethodPost, "/api/v1/data-pools/"+pool.ID+"/rows", bytes.NewReader([]byte(`{"data":{"customer_id":"C-101"}}`)))
	editorRowCreateReq.Header.Set("Content-Type", "application/json")
	editorRowCreateReq.Header.Set("X-Auth-Request-User", "editor-user")
	editorRowCreateReq.Header.Set("X-Stormrunner-Project-Roles", project.ID+"="+RoleEditor)
	editorRowCreateResp := httptest.NewRecorder()
	server.mux.ServeHTTP(editorRowCreateResp, editorRowCreateReq)
	if editorRowCreateResp.Code != http.StatusCreated {
		t.Fatalf("data pool editor row create status = %d, want %d body=%s", editorRowCreateResp.Code, http.StatusCreated, editorRowCreateResp.Body.String())
	}

	editorDeleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/data-pools/"+pool.ID, nil)
	editorDeleteReq.Header.Set("X-Auth-Request-User", "editor-user")
	editorDeleteReq.Header.Set("X-Stormrunner-Project-Roles", project.ID+"="+RoleEditor)
	editorDeleteResp := httptest.NewRecorder()
	server.mux.ServeHTTP(editorDeleteResp, editorDeleteReq)
	if editorDeleteResp.Code != http.StatusOK {
		t.Fatalf("data pool editor delete status = %d, want %d body=%s", editorDeleteResp.Code, http.StatusOK, editorDeleteResp.Body.String())
	}
}

func TestTrustedHeaderAuthProjectRoleGuardsStoredTestEndpoints(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "stormrunner.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	project := &storage.Project{ID: uuid.New().String(), Name: "Project One", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}
	folder := &storage.Folder{ID: uuid.New().String(), ProjectID: project.ID, Name: "Results", Category: "results", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveFolder(folder); err != nil {
		t.Fatalf("SaveFolder returned error: %v", err)
	}
	testRecord := &storage.TestRecord{ID: uuid.New().String(), Name: "Run One", Status: "QUEUED", ProjectID: project.ID, StartTime: time.Now()}
	if err := store.SaveTest(testRecord); err != nil {
		t.Fatalf("SaveTest returned error: %v", err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testRecord.ID, nil)
	getReq.Header.Set("X-Auth-Request-User", "viewer-user")
	getReq.Header.Set("X-Stormrunner-Project-Roles", project.ID+"="+RoleViewer)
	getResp := httptest.NewRecorder()
	server.mux.ServeHTTP(getResp, getReq)
	if getResp.Code != http.StatusOK {
		t.Fatalf("test viewer GET status = %d, want %d body=%s", getResp.Code, http.StatusOK, getResp.Body.String())
	}

	moveBody := []byte(`{"folder_id":"` + folder.ID + `"}`)
	viewerMoveReq := httptest.NewRequest(http.MethodPatch, "/api/v1/tests/"+testRecord.ID+"/move", bytes.NewReader(moveBody))
	viewerMoveReq.Header.Set("Content-Type", "application/json")
	viewerMoveReq.Header.Set("X-Auth-Request-User", "viewer-user")
	viewerMoveReq.Header.Set("X-Stormrunner-Project-Roles", project.ID+"="+RoleViewer)
	viewerMoveResp := httptest.NewRecorder()
	server.mux.ServeHTTP(viewerMoveResp, viewerMoveReq)
	if viewerMoveResp.Code != http.StatusForbidden {
		t.Fatalf("test viewer move status = %d, want %d body=%s", viewerMoveResp.Code, http.StatusForbidden, viewerMoveResp.Body.String())
	}

	editorMoveReq := httptest.NewRequest(http.MethodPatch, "/api/v1/tests/"+testRecord.ID+"/move", bytes.NewReader(moveBody))
	editorMoveReq.Header.Set("Content-Type", "application/json")
	editorMoveReq.Header.Set("X-Auth-Request-User", "editor-user")
	editorMoveReq.Header.Set("X-Stormrunner-Project-Roles", project.ID+"="+RoleEditor)
	editorMoveResp := httptest.NewRecorder()
	server.mux.ServeHTTP(editorMoveResp, editorMoveReq)
	if editorMoveResp.Code != http.StatusOK {
		t.Fatalf("test editor move status = %d, want %d body=%s", editorMoveResp.Code, http.StatusOK, editorMoveResp.Body.String())
	}
}

func TestTrustedHeaderAuthScopesTestListAndCreateToProjectRoles(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "stormrunner.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	project := &storage.Project{ID: uuid.New().String(), Name: "Project One", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}
	existingScript := &dsl.Script{
		ID:        uuid.New().String(),
		Name:      "Checkout",
		Version:   "1.0.0",
		Protocol:  "http",
		ProjectID: project.ID,
		Config:    dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions:   []dsl.Action{{Name: "Open", Type: "http", Method: "GET", Target: "/"}},
	}
	if err := store.SaveScript(existingScript); err != nil {
		t.Fatalf("SaveScript returned error: %v", err)
	}
	testRecord := &storage.TestRecord{ID: uuid.New().String(), Name: "Run One", Status: "QUEUED", ProjectID: project.ID, StartTime: time.Now()}
	if err := store.SaveTest(testRecord); err != nil {
		t.Fatalf("SaveTest returned error: %v", err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/tests", nil)
	listReq.Header.Set("X-Auth-Request-User", "viewer-user")
	listReq.Header.Set("X-Stormrunner-Project-Roles", project.ID+"="+RoleViewer)
	listResp := httptest.NewRecorder()
	server.mux.ServeHTTP(listResp, listReq)
	if listResp.Code != http.StatusForbidden {
		t.Fatalf("unfiltered test list status = %d, want %d body=%s", listResp.Code, http.StatusForbidden, listResp.Body.String())
	}

	filteredReq := httptest.NewRequest(http.MethodGet, "/api/v1/tests?project_id="+project.ID, nil)
	filteredReq.Header.Set("X-Auth-Request-User", "viewer-user")
	filteredReq.Header.Set("X-Stormrunner-Project-Roles", project.ID+"="+RoleViewer)
	filteredResp := httptest.NewRecorder()
	server.mux.ServeHTTP(filteredResp, filteredReq)
	if filteredResp.Code != http.StatusOK {
		t.Fatalf("filtered test list status = %d, want %d body=%s", filteredResp.Code, http.StatusOK, filteredResp.Body.String())
	}

	createBody := []byte(`{"project_id":"` + project.ID + `","name":"Created","duration":"30s","groups":[{"name":"buyers","script_id":"` + existingScript.ID + `","vusers":1}]}`)
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/tests", bytes.NewReader(createBody))
	createReq.Header.Set("Content-Type", "application/json")
	createReq.Header.Set("X-Auth-Request-User", "editor-user")
	createReq.Header.Set("X-Stormrunner-Project-Roles", project.ID+"="+RoleEditor)
	createResp := httptest.NewRecorder()
	server.mux.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("project editor test create status = %d, want %d body=%s", createResp.Code, http.StatusCreated, createResp.Body.String())
	}

	estimateReq := httptest.NewRequest(http.MethodPost, "/api/v1/tests/estimate", bytes.NewReader(createBody))
	estimateReq.Header.Set("Content-Type", "application/json")
	estimateReq.Header.Set("X-Auth-Request-User", "viewer-user")
	estimateReq.Header.Set("X-Stormrunner-Project-Roles", project.ID+"="+RoleViewer)
	estimateResp := httptest.NewRecorder()
	server.mux.ServeHTTP(estimateResp, estimateReq)
	if estimateResp.Code != http.StatusForbidden {
		t.Fatalf("project viewer estimate status = %d, want %d body=%s", estimateResp.Code, http.StatusForbidden, estimateResp.Body.String())
	}
}

func TestTrustedHeaderAuthProjectRoleGuardsScenarioEndpoints(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "stormrunner.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	project := &storage.Project{ID: uuid.New().String(), Name: "Project One", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}
	script := &dsl.Script{
		ID:        uuid.New().String(),
		Name:      "Checkout",
		Version:   "1.0.0",
		Protocol:  "http",
		ProjectID: project.ID,
		Config:    dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions:   []dsl.Action{{Name: "Open", Type: "http", Method: "GET", Target: "/"}},
	}
	if err := store.SaveScript(script); err != nil {
		t.Fatalf("SaveScript returned error: %v", err)
	}
	scenario := &storage.Scenario{ID: uuid.New().String(), Name: "Smoke", Duration: "1m", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveScenario(scenario, []storage.ScenarioScript{{ScenarioID: scenario.ID, Order: 0, ScriptID: script.ID, VUsers: 5}}); err != nil {
		t.Fatalf("SaveScenario returned error: %v", err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/scenarios/"+scenario.ID, nil)
	getReq.Header.Set("X-Auth-Request-User", "viewer-user")
	getReq.Header.Set("X-Stormrunner-Project-Roles", project.ID+"="+RoleViewer)
	getResp := httptest.NewRecorder()
	server.mux.ServeHTTP(getResp, getReq)
	if getResp.Code != http.StatusOK {
		t.Fatalf("scenario viewer GET status = %d, want %d body=%s", getResp.Code, http.StatusOK, getResp.Body.String())
	}

	updateReq := httptest.NewRequest(http.MethodPut, "/api/v1/scenarios/"+scenario.ID, bytes.NewReader([]byte(`{"name":"Smoke","duration":"1m","groups":[{"script_id":"`+script.ID+`","vusers":5}]}`)))
	updateReq.Header.Set("Content-Type", "application/json")
	updateReq.Header.Set("X-Auth-Request-User", "viewer-user")
	updateReq.Header.Set("X-Stormrunner-Project-Roles", project.ID+"="+RoleViewer)
	updateResp := httptest.NewRecorder()
	server.mux.ServeHTTP(updateResp, updateReq)
	if updateResp.Code != http.StatusForbidden {
		t.Fatalf("scenario viewer PUT status = %d, want %d body=%s", updateResp.Code, http.StatusForbidden, updateResp.Body.String())
	}

	dataPoolsReq := httptest.NewRequest(http.MethodGet, "/api/v1/scenarios/"+scenario.ID+"/data-pools", nil)
	dataPoolsReq.Header.Set("X-Auth-Request-User", "viewer-user")
	dataPoolsReq.Header.Set("X-Stormrunner-Project-Roles", project.ID+"="+RoleViewer)
	dataPoolsResp := httptest.NewRecorder()
	server.mux.ServeHTTP(dataPoolsResp, dataPoolsReq)
	if dataPoolsResp.Code != http.StatusOK {
		t.Fatalf("scenario viewer data pools GET status = %d, want %d body=%s", dataPoolsResp.Code, http.StatusOK, dataPoolsResp.Body.String())
	}

	resultsReq := httptest.NewRequest(http.MethodGet, "/api/v1/scenarios/"+scenario.ID+"/results", nil)
	resultsReq.Header.Set("X-Auth-Request-User", "viewer-user")
	resultsReq.Header.Set("X-Stormrunner-Project-Roles", project.ID+"="+RoleViewer)
	resultsResp := httptest.NewRecorder()
	server.mux.ServeHTTP(resultsResp, resultsReq)
	if resultsResp.Code != http.StatusOK {
		t.Fatalf("scenario viewer results GET status = %d, want %d body=%s", resultsResp.Code, http.StatusOK, resultsResp.Body.String())
	}

	adminUpdateReq := httptest.NewRequest(http.MethodPut, "/api/v1/scenarios/"+scenario.ID, bytes.NewReader([]byte(`{"name":"Smoke Renamed","duration":"1m","groups":[{"script_id":"`+script.ID+`","vusers":5}]}`)))
	adminUpdateReq.Header.Set("Content-Type", "application/json")
	adminUpdateReq.Header.Set("X-Auth-Request-User", "editor-user")
	adminUpdateReq.Header.Set("X-Stormrunner-Project-Roles", project.ID+"="+RoleEditor)
	adminUpdateResp := httptest.NewRecorder()
	server.mux.ServeHTTP(adminUpdateResp, adminUpdateReq)
	if adminUpdateResp.Code != http.StatusOK {
		t.Fatalf("scenario editor PUT status = %d, want %d body=%s", adminUpdateResp.Code, http.StatusOK, adminUpdateResp.Body.String())
	}
}

func TestTrustedHeaderAuthScopesScenarioListAndCreateToProjectRoles(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "stormrunner.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	project := &storage.Project{ID: uuid.New().String(), Name: "Project One", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}
	script := &dsl.Script{
		ID:        uuid.New().String(),
		Name:      "Checkout",
		Version:   "1.0.0",
		Protocol:  "http",
		ProjectID: project.ID,
		Config:    dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions:   []dsl.Action{{Name: "Open", Type: "http", Method: "GET", Target: "/"}},
	}
	if err := store.SaveScript(script); err != nil {
		t.Fatalf("SaveScript returned error: %v", err)
	}
	scenario := &storage.Scenario{ID: uuid.New().String(), Name: "Smoke", Duration: "1m", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveScenario(scenario, []storage.ScenarioScript{{ScenarioID: scenario.ID, Order: 0, ScriptID: script.ID, VUsers: 5}}); err != nil {
		t.Fatalf("SaveScenario returned error: %v", err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/scenarios", nil)
	listReq.Header.Set("X-Auth-Request-User", "viewer-user")
	listReq.Header.Set("X-Stormrunner-Project-Roles", project.ID+"="+RoleViewer)
	listResp := httptest.NewRecorder()
	server.mux.ServeHTTP(listResp, listReq)
	if listResp.Code != http.StatusForbidden {
		t.Fatalf("unfiltered scenario list status = %d, want %d body=%s", listResp.Code, http.StatusForbidden, listResp.Body.String())
	}

	filteredReq := httptest.NewRequest(http.MethodGet, "/api/v1/scenarios?project_id="+project.ID, nil)
	filteredReq.Header.Set("X-Auth-Request-User", "viewer-user")
	filteredReq.Header.Set("X-Stormrunner-Project-Roles", project.ID+"="+RoleViewer)
	filteredResp := httptest.NewRecorder()
	server.mux.ServeHTTP(filteredResp, filteredReq)
	if filteredResp.Code != http.StatusOK {
		t.Fatalf("filtered scenario list status = %d, want %d body=%s", filteredResp.Code, http.StatusOK, filteredResp.Body.String())
	}

	createBody := []byte(`{"name":"Created","duration":"30s","groups":[{"name":"buyers","script_id":"` + script.ID + `","vusers":1}]}`)
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/scenarios", bytes.NewReader(createBody))
	createReq.Header.Set("Content-Type", "application/json")
	createReq.Header.Set("X-Auth-Request-User", "editor-user")
	createReq.Header.Set("X-Stormrunner-Project-Roles", project.ID+"="+RoleEditor)
	createResp := httptest.NewRecorder()
	server.mux.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("project editor scenario create status = %d, want %d body=%s", createResp.Code, http.StatusCreated, createResp.Body.String())
	}
}

func TestTrustedHeaderAuthRejectsCrossProjectTestMoves(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "stormrunner.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	projectOne := &storage.Project{ID: uuid.New().String(), Name: "Project One", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	projectTwo := &storage.Project{ID: uuid.New().String(), Name: "Project Two", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveProject(projectOne); err != nil {
		t.Fatalf("SaveProject(projectOne) returned error: %v", err)
	}
	if err := store.SaveProject(projectTwo); err != nil {
		t.Fatalf("SaveProject(projectTwo) returned error: %v", err)
	}
	destinationFolder := &storage.Folder{ID: uuid.New().String(), ProjectID: projectTwo.ID, Name: "Other Results", Category: "results", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveFolder(destinationFolder); err != nil {
		t.Fatalf("SaveFolder returned error: %v", err)
	}
	testRecord := &storage.TestRecord{ID: uuid.New().String(), Name: "Run One", Status: "QUEUED", ProjectID: projectOne.ID, StartTime: time.Now()}
	if err := store.SaveTest(testRecord); err != nil {
		t.Fatalf("SaveTest returned error: %v", err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	body := []byte(`{"folder_id":"` + destinationFolder.ID + `"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/tests/"+testRecord.ID+"/move", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Auth-Request-User", "editor-user")
	req.Header.Set("X-Stormrunner-Project-Roles", projectOne.ID+"="+RoleEditor)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("cross-project test move status = %d, want %d body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
}

func enableTrustedHeaderAuth(t *testing.T, server *Server) SystemSettings {
	t.Helper()
	settings := server.runner.GetSystemSettings()
	settings.Auth.Mode = authModeTrustedHeaders
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}
	return settings
}
