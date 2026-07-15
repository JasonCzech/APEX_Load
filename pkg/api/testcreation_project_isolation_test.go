package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestCreateTestAuthorizationIncludesStoredScenarioProject(t *testing.T) {
	store, server := newCreateTestProjectIsolationServer(t)
	now := time.Now().UTC()
	if err := store.SaveScenario(
		&storage.Scenario{ID: "scenario-project-b", Name: "Project B scenario", Duration: "30s", CreatedAt: now, UpdatedAt: now},
		[]storage.ScenarioScript{{ScenarioID: "scenario-project-b", ScriptID: "script-project-b", VUsers: 1}},
	); err != nil {
		t.Fatalf("SaveScenario returned error: %v", err)
	}
	if err := server.runner.reloadStoredScenarios(); err != nil {
		t.Fatalf("reloadStoredScenarios returned error: %v", err)
	}

	enableTrustedHeaderAuth(t, server)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tests", strings.NewReader(`{"scenario_id":"scenario-project-b"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Auth-Request-User", "project-a-editor")
	req.Header.Set("X-APEXLoad-Project-Roles", "project-a="+RoleEditor+","+storage.DefaultProjectID+"="+RoleEditor)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusForbidden, resp.Body.String())
	}
	if tests := server.runner.ListTests(); len(tests) != 0 {
		t.Fatalf("unauthorized scenario created %d test(s)", len(tests))
	}
}

func TestResolveTestCreateProjectReconcilesAllReferencedResources(t *testing.T) {
	store, _ := newCreateTestProjectIsolationServer(t)
	now := time.Now().UTC()
	if err := store.SaveScenario(
		&storage.Scenario{ID: "scenario-project-b", Name: "Project B scenario", Duration: "30s", CreatedAt: now, UpdatedAt: now},
		[]storage.ScenarioScript{{ScenarioID: "scenario-project-b", ScriptID: "script-project-b", VUsers: 1}},
	); err != nil {
		t.Fatalf("SaveScenario returned error: %v", err)
	}

	tests := []struct {
		name        string
		body        string
		wantStatus  int
		wantProject string
	}{
		{name: "scenario determines scope", body: `{"scenario_id":"scenario-project-b"}`, wantProject: "project-b"},
		{name: "declared project conflicts with scenario", body: `{"project_id":"project-a","scenario_id":"scenario-project-b"}`, wantStatus: http.StatusBadRequest},
		{name: "declared project conflicts with release", body: `{"project_id":"project-a","script_id":"script-project-a","release_id":"release-project-b"}`, wantStatus: http.StatusBadRequest},
		{name: "declared project conflicts with application", body: `{"project_id":"project-a","script_id":"script-project-a","application_id":"application-project-b"}`, wantStatus: http.StatusBadRequest},
		{name: "release and application disagree", body: `{"script_id":"script-project-a","release_id":"release-project-a","application_id":"application-project-b"}`, wantStatus: http.StatusBadRequest},
		{name: "matching references", body: `{"project_id":"project-a","script_id":"script-project-a","release_id":"release-project-a","application_id":"application-project-a"}`, wantProject: "project-a"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/tests", bytes.NewBufferString(tc.body))
			projectID, status, _ := resolveTestCreateProjectID(req, store)
			if status != tc.wantStatus {
				t.Fatalf("status = %d, want %d", status, tc.wantStatus)
			}
			if status == 0 && projectID != tc.wantProject {
				t.Fatalf("project = %q, want %q", projectID, tc.wantProject)
			}
		})
	}
}

func TestCreateTestRunnerRejectsCrossProjectReferencesWithoutMiddleware(t *testing.T) {
	_, server := newCreateTestProjectIsolationServer(t)
	scenario, err := server.runner.StoreScenario(CreateScenarioRequest{
		Name: "project B scenario", Duration: "30s",
		Groups: []CreateGroupEntry{{ScriptID: "script-project-b", VUsers: 1}},
	})
	if err != nil {
		t.Fatalf("StoreScenario returned error: %v", err)
	}

	tests := []struct {
		name string
		req  CreateTestRequest
	}{
		{
			name: "script",
			req:  CreateTestRequest{Name: "cross-project script", ProjectID: "project-a", Duration: "30s", ScriptID: "script-project-b", VUsers: 1},
		},
		{
			name: "release",
			req:  CreateTestRequest{Name: "cross-project release", ProjectID: "project-a", Duration: "30s", ScriptID: "script-project-a", VUsers: 1, ReleaseID: "release-project-b"},
		},
		{
			name: "application",
			req:  CreateTestRequest{Name: "cross-project application", ProjectID: "project-a", Duration: "30s", ScriptID: "script-project-a", VUsers: 1, ApplicationID: "application-project-b"},
		},
		{
			name: "scenario with explicit groups",
			req:  CreateTestRequest{Name: "cross-project scenario", ProjectID: "project-a", Duration: "30s", ScenarioID: scenario.ID, ScriptID: "script-project-a", VUsers: 1},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := server.runner.CreateTest(tc.req); err == nil {
				t.Fatal("CreateTest unexpectedly accepted cross-project references")
			}
		})
	}

	created, err := server.runner.CreateTest(CreateTestRequest{
		Name: "same-project references", ProjectID: "project-a", Duration: "30s",
		ScriptID: "script-project-a", VUsers: 1, ReleaseID: "release-project-a", ApplicationID: "application-project-a",
	})
	if err != nil {
		t.Fatalf("CreateTest returned error for matching references: %v", err)
	}
	if created.ProjectID != "project-a" || created.ReleaseID != "release-project-a" || created.ApplicationID != "application-project-a" {
		t.Fatalf("created context = project %q release %q application %q", created.ProjectID, created.ReleaseID, created.ApplicationID)
	}
}

func newCreateTestProjectIsolationServer(t *testing.T) (*storage.SQLiteStore, *Server) {
	t.Helper()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	for _, projectID := range []string{"project-a", "project-b"} {
		if err := store.SaveProject(&storage.Project{ID: projectID, Name: projectID, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatalf("SaveProject(%s) returned error: %v", projectID, err)
		}
		applicationID := "application-" + projectID
		if err := store.SaveApplication(&storage.Application{ID: applicationID, ProjectID: projectID, Name: applicationID, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatalf("SaveApplication(%s) returned error: %v", applicationID, err)
		}
		if err := store.SaveRelease(&storage.Release{ID: "release-" + projectID, ProjectID: projectID, ApplicationID: applicationID, Name: "release", CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatalf("SaveRelease(%s) returned error: %v", projectID, err)
		}
		if err := store.SaveScript(&dsl.Script{
			ID: "script-" + projectID, Name: "script", ProjectID: projectID, Protocol: "http", Version: "1.0.0",
			Config: dsl.ScriptConfig{BaseURL: "https://example.com"}, Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/"}},
		}); err != nil {
			t.Fatalf("SaveScript(%s) returned error: %v", projectID, err)
		}
	}
	return store, NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
}
