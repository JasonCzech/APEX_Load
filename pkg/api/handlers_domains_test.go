package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestDomainRepositoryProjectReleaseHandlers(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})

	createDomain := httptest.NewRequest(http.MethodPost, "/api/v1/domains", bytes.NewBufferString(`{"name":"Retail","description":"Retail apps"}`))
	createDomain.Header.Set("Content-Type", "application/json")
	domainResp := httptest.NewRecorder()
	server.mux.ServeHTTP(domainResp, createDomain)
	if domainResp.Code != http.StatusCreated {
		t.Fatalf("create domain status = %d body=%s", domainResp.Code, domainResp.Body.String())
	}
	var createdDomain struct {
		Domain          storage.Domain                `json:"domain"`
		ApplicationRepo storage.ApplicationRepository `json:"application_repository"`
		DefaultCategory storage.RepositoryCategory    `json:"default_category"`
	}
	if err := json.Unmarshal(domainResp.Body.Bytes(), &createdDomain); err != nil {
		t.Fatalf("unmarshal created domain: %v", err)
	}
	if createdDomain.Domain.ID == "" || createdDomain.ApplicationRepo.DomainID != createdDomain.Domain.ID || createdDomain.DefaultCategory.RepositoryID != createdDomain.ApplicationRepo.ID {
		t.Fatalf("created hierarchy = %#v", createdDomain)
	}

	createCategory := httptest.NewRequest(http.MethodPost, "/api/v1/domains/"+createdDomain.Domain.ID+"/application-repository/categories", bytes.NewBufferString(`{"name":"Checkout","sort_order":2}`))
	createCategory.Header.Set("Content-Type", "application/json")
	categoryResp := httptest.NewRecorder()
	server.mux.ServeHTTP(categoryResp, createCategory)
	if categoryResp.Code != http.StatusCreated {
		t.Fatalf("create category status = %d body=%s", categoryResp.Code, categoryResp.Body.String())
	}
	var category storage.RepositoryCategory
	if err := json.Unmarshal(categoryResp.Body.Bytes(), &category); err != nil {
		t.Fatalf("unmarshal category: %v", err)
	}

	projectBody := `{"name":"Checkout API","domain_id":"` + createdDomain.Domain.ID + `","category_id":"` + category.ID + `"}`
	createProject := httptest.NewRequest(http.MethodPost, "/api/v1/projects", bytes.NewBufferString(projectBody))
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
	if project.DomainID != createdDomain.Domain.ID || project.CategoryID != category.ID {
		t.Fatalf("project placement = %#v", project)
	}

	listProjects := httptest.NewRequest(http.MethodGet, "/api/v1/projects?domain_id="+createdDomain.Domain.ID+"&category_id="+category.ID, nil)
	listProjectResp := httptest.NewRecorder()
	server.mux.ServeHTTP(listProjectResp, listProjects)
	if listProjectResp.Code != http.StatusOK {
		t.Fatalf("list projects status = %d body=%s", listProjectResp.Code, listProjectResp.Body.String())
	}
	var listedProjects struct {
		Projects []storage.Project `json:"projects"`
		Count    int               `json:"count"`
	}
	if err := json.Unmarshal(listProjectResp.Body.Bytes(), &listedProjects); err != nil {
		t.Fatalf("unmarshal projects: %v", err)
	}
	if listedProjects.Count != 1 || listedProjects.Projects[0].ID != project.ID {
		t.Fatalf("listed projects = %#v", listedProjects)
	}

	createRelease := httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+project.ID+"/releases", bytes.NewBufferString(`{"name":"2026.05"}`))
	createRelease.Header.Set("Content-Type", "application/json")
	releaseResp := httptest.NewRecorder()
	server.mux.ServeHTTP(releaseResp, createRelease)
	if releaseResp.Code != http.StatusCreated {
		t.Fatalf("create project release status = %d body=%s", releaseResp.Code, releaseResp.Body.String())
	}
	var release storage.Release
	if err := json.Unmarshal(releaseResp.Body.Bytes(), &release); err != nil {
		t.Fatalf("unmarshal release: %v", err)
	}
	if release.ProjectID != project.ID || release.ApplicationID == "" {
		t.Fatalf("release = %#v, want project scoped release with compatibility application", release)
	}

	listReleases := httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+project.ID+"/releases", nil)
	listReleaseResp := httptest.NewRecorder()
	server.mux.ServeHTTP(listReleaseResp, listReleases)
	if listReleaseResp.Code != http.StatusOK {
		t.Fatalf("list releases status = %d body=%s", listReleaseResp.Code, listReleaseResp.Body.String())
	}
	var listedReleases struct {
		Releases []storage.Release `json:"releases"`
		Count    int               `json:"count"`
	}
	if err := json.Unmarshal(listReleaseResp.Body.Bytes(), &listedReleases); err != nil {
		t.Fatalf("unmarshal releases: %v", err)
	}
	createdReleaseFound := false
	legacyReleaseFound := false
	for _, listed := range listedReleases.Releases {
		if listed.ProjectID != project.ID {
			t.Fatalf("listed release escaped project scope: %#v", listed)
		}
		createdReleaseFound = createdReleaseFound || listed.ID == release.ID
		legacyReleaseFound = legacyReleaseFound || listed.Name == "Imported Legacy"
	}
	if listedReleases.Count != 2 || !createdReleaseFound || !legacyReleaseFound {
		t.Fatalf("listed releases = %#v", listedReleases)
	}
}
