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

func enableTrustedHeaderAuth(t *testing.T, server *Server) SystemSettings {
	t.Helper()
	settings := server.runner.GetSystemSettings()
	settings.Auth.Mode = authModeTrustedHeaders
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}
	return settings
}
