package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestProjectRoleAuthorizationBindsResolvedProjectToRequest(t *testing.T) {
	req := httptest.NewRequest(http.MethodPut, "/api/v1/scripts/script-a", nil)
	rec := httptest.NewRecorder()
	resolution := authResolution{User: &AuthenticatedUser{
		ID: "editor",
		ProjectRoles: map[string]map[string]struct{}{
			"project-a": {RoleEditor: {}},
		},
	}}
	requirement := accessRequirement{
		ProjectRoles: []string{RoleEditor},
		ProjectLookup: func(*http.Request, storage.Store) (string, int, string) {
			return "project-a", 0, ""
		},
	}
	if !authorizeAccessRequirement(rec, req, nil, resolution, requirement) {
		t.Fatalf("authorization failed: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got, ok := authorizedProjectFromRequest(req); !ok || got != "project-a" {
		t.Fatalf("authorized project = %q, %v; want project-a, true", got, ok)
	}
}

func TestGlobalAuthorizationDoesNotImposeProjectBinding(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/script-a", nil)
	rec := httptest.NewRecorder()
	resolution := authResolution{User: &AuthenticatedUser{
		ID:          "admin",
		GlobalRoles: map[string]struct{}{RoleSystemAdmin: {}},
	}}
	requirement := accessRequirement{
		GlobalRoles:  []string{RoleSystemAdmin},
		ProjectRoles: []string{RoleViewer},
		ProjectLookup: func(*http.Request, storage.Store) (string, int, string) {
			return "project-a", 0, ""
		},
	}
	if !authorizeAccessRequirement(rec, req, nil, resolution, requirement) {
		t.Fatalf("authorization failed: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got, ok := authorizedProjectFromRequest(req); ok || got != "" {
		t.Fatalf("global authorization unexpectedly bound project = %q, %v", got, ok)
	}
}
