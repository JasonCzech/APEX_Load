package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestProjectAndFolderHandlers(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})

	createProject := httptest.NewRequest(http.MethodPost, "/api/v1/projects", bytes.NewBufferString(`{"name":"Payments"}`))
	createProject.Header.Set("Content-Type", "application/json")
	projectResp := httptest.NewRecorder()
	server.mux.ServeHTTP(projectResp, createProject)
	if projectResp.Code != http.StatusCreated {
		t.Fatalf("create project status = %d body=%s", projectResp.Code, projectResp.Body.String())
	}
	var project storage.Project
	if err := json.Unmarshal(projectResp.Body.Bytes(), &project); err != nil {
		t.Fatalf("unmarshal project: %v", err)
	}

	createFolder := httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+project.ID+"/folders", bytes.NewBufferString(`{"name":"Checkout","category":"scripts"}`))
	createFolder.Header.Set("Content-Type", "application/json")
	folderResp := httptest.NewRecorder()
	server.mux.ServeHTTP(folderResp, createFolder)
	if folderResp.Code != http.StatusCreated {
		t.Fatalf("create folder status = %d body=%s", folderResp.Code, folderResp.Body.String())
	}

	listFolders := httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+project.ID+"/folders?category=scripts", nil)
	listResp := httptest.NewRecorder()
	server.mux.ServeHTTP(listResp, listFolders)
	if listResp.Code != http.StatusOK {
		t.Fatalf("list folders status = %d body=%s", listResp.Code, listResp.Body.String())
	}
	var listed struct {
		Folders []storage.Folder `json:"folders"`
	}
	if err := json.Unmarshal(listResp.Body.Bytes(), &listed); err != nil {
		t.Fatalf("unmarshal folders: %v", err)
	}
	if len(listed.Folders) != 1 || listed.Folders[0].Name != "Checkout" {
		t.Fatalf("unexpected folders: %#v", listed.Folders)
	}
}

func TestProjectHandlersPersistRedactionSettings(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})

	createProject := httptest.NewRequest(http.MethodPost, "/api/v1/projects", bytes.NewBufferString(`{"name":"Payments","settings":{"redaction_rules":[{"name":"customer id","pattern":"customer-[0-9]+"}],"max_concurrent_tests":2,"max_vusers":250}}`))
	createProject.Header.Set("Content-Type", "application/json")
	projectResp := httptest.NewRecorder()
	server.mux.ServeHTTP(projectResp, createProject)
	if projectResp.Code != http.StatusCreated {
		t.Fatalf("create project status = %d body=%s", projectResp.Code, projectResp.Body.String())
	}
	var created storage.Project
	if err := json.Unmarshal(projectResp.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal created project: %v", err)
	}
	if len(created.Settings.RedactionRules) != 1 || created.Settings.RedactionRules[0].Pattern != "customer-[0-9]+" {
		t.Fatalf("created settings = %#v, want redaction rule", created.Settings)
	}
	if created.Settings.MaxConcurrentTests != 2 {
		t.Fatalf("created max_concurrent_tests = %d, want 2", created.Settings.MaxConcurrentTests)
	}
	if created.Settings.MaxVUsers != 250 {
		t.Fatalf("created max_vusers = %d, want 250", created.Settings.MaxVUsers)
	}

	getProject := httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+created.ID, nil)
	getResp := httptest.NewRecorder()
	server.mux.ServeHTTP(getResp, getProject)
	if getResp.Code != http.StatusOK {
		t.Fatalf("get project status = %d body=%s", getResp.Code, getResp.Body.String())
	}
	var fetched storage.Project
	if err := json.Unmarshal(getResp.Body.Bytes(), &fetched); err != nil {
		t.Fatalf("unmarshal fetched project: %v", err)
	}
	if len(fetched.Settings.RedactionRules) != 1 || fetched.Settings.RedactionRules[0].Name != "customer id" {
		t.Fatalf("fetched settings = %#v, want persisted rule", fetched.Settings)
	}
	if fetched.Settings.MaxConcurrentTests != 2 {
		t.Fatalf("fetched max_concurrent_tests = %d, want 2", fetched.Settings.MaxConcurrentTests)
	}
	if fetched.Settings.MaxVUsers != 250 {
		t.Fatalf("fetched max_vusers = %d, want 250", fetched.Settings.MaxVUsers)
	}
}

func TestProjectHandlersRejectInvalidRedactionSettings(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects", bytes.NewBufferString(`{"name":"Payments","settings":{"redaction_rules":[{"name":"broken","pattern":"["}]}}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
}

func TestProjectHandlersRejectNegativeConcurrentLimit(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects", bytes.NewBufferString(`{"name":"Payments","settings":{"max_concurrent_tests":-1}}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
}

func TestProjectHandlersRejectNegativeVUserLimit(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects", bytes.NewBufferString(`{"name":"Payments","settings":{"max_vusers":-1}}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
}

func TestHandleUpdateProjectReconcilesPendingQueue(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 5})

	project := &storage.Project{ID: "project-1", Name: "Payments", Settings: storage.ProjectSettings{MaxConcurrentTests: 1}, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}
	server.runner.StoreScript(&dsl.Script{
		ID:        "script-project-reconcile",
		Name:      "Project Reconcile Script",
		Protocol:  "http",
		Version:   "1.0.0",
		ProjectID: project.ID,
		Actions:   []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})

	first, err := server.runner.CreateTest(CreateTestRequest{Name: "First", Duration: "30s", ScriptID: "script-project-reconcile", VUsers: 1})
	if err != nil {
		t.Fatalf("CreateTest(first) returned error: %v", err)
	}
	second, err := server.runner.CreateTest(CreateTestRequest{Name: "Second", Duration: "30s", ScriptID: "script-project-reconcile", VUsers: 1})
	if err != nil {
		t.Fatalf("CreateTest(second) returned error: %v", err)
	}

	server.runner.mu.Lock()
	server.runner.tests[first.ID].Status = "RUNNING"
	server.runner.mu.Unlock()

	if err := server.runner.StartTest(second.ID); err != nil {
		t.Fatalf("StartTest(second) returned error: %v", err)
	}
	pending, ok := server.runner.GetTest(second.ID)
	if !ok || pending.Status != "PENDING" {
		t.Fatalf("expected pending test before quota update, got %#v ok=%v", pending, ok)
	}

	body := bytes.NewBufferString(`{"name":"Payments","settings":{"max_concurrent_tests":2}}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/projects/project-1", body)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	promoted := waitForStatusAny(t, server.runner, second.ID, 5*time.Second, "INITIALIZING", "RUNNING", "COMPLETE")
	if promoted.Status == "PENDING" {
		t.Fatalf("expected pending test to be promoted after project update, got %s", promoted.Status)
	}
}
