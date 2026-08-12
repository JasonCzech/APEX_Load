package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestProjectRoleAuthorizationBindsResolvedProjectToRequest(t *testing.T) {
	req := httptest.NewRequest(http.MethodPut, "/api/v1/scripts/script-a", nil)
	req = withProjectMutationAdmissionRequest(req)
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
	if got, ok := mutationProjectFromRequest(req); !ok || got != "project-a" {
		t.Fatalf("mutation project = %q, %v; want project-a, true", got, ok)
	}
}

func TestGlobalAuthorizationDoesNotImposeProjectBinding(t *testing.T) {
	req := httptest.NewRequest(http.MethodPut, "/api/v1/scripts/script-a", nil)
	req = withProjectMutationAdmissionRequest(req)
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
	if !authorizeAccessRequirement(rec, req, &metricsStoreSpy{}, resolution, requirement) {
		t.Fatalf("authorization failed: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got, ok := authorizedProjectFromRequest(req); ok || got != "" {
		t.Fatalf("global authorization unexpectedly bound project = %q, %v", got, ok)
	}
	if got, ok := mutationProjectFromRequest(req); !ok || got != "project-a" {
		t.Fatalf("global authorization mutation project = %q, %v; want project-a, true", got, ok)
	}
}

func TestGlobalReadAuthorizationSkipsMutationProjectLookup(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/script-a", nil)
	rec := httptest.NewRecorder()
	resolution := authResolution{User: &AuthenticatedUser{
		ID:          "admin",
		GlobalRoles: map[string]struct{}{RoleSystemAdmin: {}},
	}}
	requirement := accessRequirement{
		GlobalRoles: []string{RoleSystemAdmin},
		ProjectLookup: func(*http.Request, storage.Store) (string, int, string) {
			t.Fatal("read authorization unexpectedly resolved a mutation project")
			return "", 0, ""
		},
	}
	if !authorizeAccessRequirement(rec, req, &metricsStoreSpy{}, resolution, requirement) {
		t.Fatalf("authorization failed: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got, ok := mutationProjectFromRequest(req); ok || got != "" {
		t.Fatalf("read authorization unexpectedly bound mutation project = %q, %v", got, ok)
	}
}

func TestAuthorizedProjectOrPreservesAbsentUnrestrictedFilter(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/fleet/load-zones", nil)
	if got := authorizedProjectOr(req, ""); got != "" {
		t.Fatalf("authorizedProjectOr without a binding or filter = %q, want empty", got)
	}

	bindAuthorizedProjectToRequest(req, "project-a")
	if got := authorizedProjectOr(req, ""); got != "project-a" {
		t.Fatalf("authorizedProjectOr with a bound project = %q, want project-a", got)
	}
}

func TestDomainAuthorizationBindsOnlyMutationProject(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects/project-a/releases", nil)
	rec := httptest.NewRecorder()
	resolution := authResolution{User: &AuthenticatedUser{
		ID:          "domain-editor",
		DomainRoles: map[string]map[string]struct{}{"domain-a": {RoleEditor: {}}},
	}}
	requirement := accessRequirement{
		ProjectRoles: []string{RoleEditor},
		ProjectLookup: func(*http.Request, storage.Store) (string, int, string) {
			return "project-a", 0, ""
		},
		DomainRoles: []string{RoleEditor},
		DomainLookup: func(*http.Request, storage.Store) (string, int, string) {
			return "domain-a", 0, ""
		},
	}
	if !authorizeAccessRequirement(rec, req, nil, resolution, requirement) {
		t.Fatalf("authorization failed: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got, ok := authorizedProjectFromRequest(req); ok || got != "" {
		t.Fatalf("domain authorization unexpectedly bound RBAC project = %q, %v", got, ok)
	}
	if got, ok := mutationProjectFromRequest(req); !ok || got != "project-a" {
		t.Fatalf("domain authorization mutation project = %q, %v; want project-a, true", got, ok)
	}
}
