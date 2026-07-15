package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestGitHubOperationalRoutesAndHistoriesEnforceScope(t *testing.T) {
	for _, path := range []string{
		"/api/v1/integrations/github/diagnostics",
		"/api/v1/integrations/github/installations",
		"/api/v1/integrations/github/webhooks",
	} {
		requirement, ok := accessRequirementFromRouteCatalog(http.MethodGet, path)
		if !ok || len(requirement.GlobalRoles) != 1 || requirement.GlobalRoles[0] != RoleSystemAdmin || requirement.AuthenticatedOnly {
			t.Fatalf("%s requirement = %#v, want system-admin only", path, requirement)
		}
	}

	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "github-scope.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	for _, projectID := range []string{"project-a", "project-b"} {
		if err := store.SaveProject(&storage.Project{ID: projectID, Name: projectID, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SaveGitHubSyncJob(&storage.GitHubSyncJob{ID: "job-b", ProjectID: "project-b", Kind: "push", Status: "complete"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTest(&storage.TestRecord{ID: "test-a", Name: "A", ProjectID: "project-a", Status: "COMPLETE"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveScript(&dsl.Script{ID: "script-b", Name: "B", ProjectID: "project-b", Protocol: "http"}); err != nil {
		t.Fatal(err)
	}

	viewer := authResolution{User: &AuthenticatedUser{ProjectRoles: map[string]map[string]struct{}{
		"project-a": {RoleViewer: {}},
	}}}

	listRequirement, _ := accessRequirementFromRouteCatalog(http.MethodGet, "/api/v1/integrations/github/sync-jobs")
	allowedReq := httptest.NewRequest(http.MethodGet, "/api/v1/integrations/github/sync-jobs?project_id=project-a", nil)
	if !authorizeAccessRequirement(httptest.NewRecorder(), allowedReq, store, viewer, listRequirement) {
		t.Fatal("project-a viewer was denied its own sync jobs")
	}
	deniedResp := httptest.NewRecorder()
	deniedReq := httptest.NewRequest(http.MethodGet, "/api/v1/integrations/github/sync-jobs?project_id=project-b", nil)
	if authorizeAccessRequirement(deniedResp, deniedReq, store, viewer, listRequirement) || deniedResp.Code != http.StatusForbidden {
		t.Fatalf("foreign sync-job list authorization = %d, want 403", deniedResp.Code)
	}

	jobRequirement, _ := accessRequirementFromRouteCatalog(http.MethodGet, "/api/v1/integrations/github/sync-jobs/job-b")
	jobReq := httptest.NewRequest(http.MethodGet, "/api/v1/integrations/github/sync-jobs/job-b", nil)
	jobReq.SetPathValue("id", "job-b")
	jobResp := httptest.NewRecorder()
	if authorizeAccessRequirement(jobResp, jobReq, store, viewer, jobRequirement) || jobResp.Code != http.StatusForbidden {
		t.Fatalf("foreign sync-job read authorization = %d, want 403", jobResp.Code)
	}

	checkRequirement, _ := accessRequirementFromRouteCatalog(http.MethodGet, "/api/v1/integrations/github/check-links")
	checkReq := httptest.NewRequest(http.MethodGet, "/api/v1/integrations/github/check-links?test_id=test-a&script_id=script-b", nil)
	checkResp := httptest.NewRecorder()
	if authorizeAccessRequirement(checkResp, checkReq, store, viewer, checkRequirement) || checkResp.Code != http.StatusForbidden {
		t.Fatalf("cross-project check-link query authorization = %d, want 403", checkResp.Code)
	}
}

func TestGitHubProjectPolicyMutationRouteRequiresSystemAdmin(t *testing.T) {
	requirement, ok := accessRequirementFromRouteCatalog(http.MethodPut, "/api/v1/projects/project-a/github/policy")
	if !ok {
		t.Fatal("GitHub project-policy mutation route has no access requirement")
	}
	if len(requirement.GlobalRoles) != 1 || requirement.GlobalRoles[0] != RoleSystemAdmin || len(requirement.ProjectRoles) != 0 || requirement.ProjectLookup != nil {
		t.Fatalf("GitHub project-policy mutation requirement = %#v, want system-admin only", requirement)
	}
}
