package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/jasonczech/stormrunner/pkg/storage"
)

func TestProjectAndFolderHandlers(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "stormrunner.db"))
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
