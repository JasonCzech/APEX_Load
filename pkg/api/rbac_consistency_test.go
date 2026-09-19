package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// B1 — the access matrix (authAccessMatrix, which gates every dashboard admin control via
// /auth/effective-access) and the enforced route catalog are authored independently. Nothing
// tied them together, so the UI could silently diverge from enforcement. This test pins the
// two together for the security-load-bearing operations: each matrix operation must grant
// exactly the global (and, where the route is project-scoped, project) roles that the route
// it maps to actually enforces. A drift in either representation fails here.
//
// Operations whose routes use the AuthenticatedOnly + in-handler scoping pattern
// (view_user_activity, manage_user_bindings, manage_users, view_auth_diagnostics) are
// intentionally excluded: their catalog entry declares no concrete roles, so there is nothing
// to compare against — the matrix encodes the in-handler logic and the route_catalog
// characterization test guards the enforcement side.
func TestRBACMatrixAgreesWithRouteCatalog(t *testing.T) {
	checks := []struct {
		operation    string
		method       string
		path         string
		pathValues   map[string]string
		checkProject bool
	}{
		{operation: "view_system", method: http.MethodGet, path: "/api/v1/system/settings"},
		{operation: "manage_system", method: http.MethodPut, path: "/api/v1/system/settings"},
		{operation: "view_audit", method: http.MethodGet, path: "/api/v1/auth/audit-events"},
		{operation: "manage_service_accounts", method: http.MethodGet, path: "/api/v1/auth/service-accounts"},
		{operation: "view_domains", method: http.MethodGet, path: "/api/v1/domains"},
		{operation: "manage_domains", method: http.MethodPost, path: "/api/v1/domains"},
		{operation: "view_tests", method: http.MethodGet, path: "/api/v1/tests/t1/notes", pathValues: map[string]string{"id": "t1"}, checkProject: true},
		{operation: "manage_tests", method: http.MethodPost, path: "/api/v1/tests/t1/notes", pathValues: map[string]string{"id": "t1"}, checkProject: true},
		{operation: "manage_scripts", method: http.MethodPut, path: "/api/v1/scripts/s1/library-assignment", pathValues: map[string]string{"id": "s1"}, checkProject: true},
		{operation: "manage_tests", method: http.MethodPut, path: "/api/v1/scenarios/s1/library-assignment", pathValues: map[string]string{"id": "s1"}, checkProject: true},
		{operation: "view_scripts", method: http.MethodPost, path: "/api/v1/scripts/validate", checkProject: true},
	}

	for _, c := range checks {
		op, ok := matrixOperationByID(c.operation)
		if !ok {
			t.Errorf("operation %q not found in authAccessMatrix", c.operation)
			continue
		}
		req := httptest.NewRequest(c.method, c.path, nil)
		for name, value := range c.pathValues {
			req.SetPathValue(name, value)
		}
		requirement, found := accessRequirementForRequest(req)
		if !found {
			t.Errorf("operation %q: route %s %s not found in catalog", c.operation, c.method, c.path)
			continue
		}
		if !roleSetEqual(requirement.GlobalRoles, op.GlobalRoles) {
			t.Errorf("operation %q (%s %s): global roles route=%v != matrix=%v", c.operation, c.method, c.path, requirement.GlobalRoles, op.GlobalRoles)
		}
		if c.checkProject && !roleSetEqual(requirement.ProjectRoles, op.ProjectRoles) {
			t.Errorf("operation %q (%s %s): project roles route=%v != matrix=%v", c.operation, c.method, c.path, requirement.ProjectRoles, op.ProjectRoles)
		}
	}
}

// TestRBACMatrixRolesAreCanonical guards the cheaper invariant that every role named anywhere
// in the matrix is one of the four canonical roles — catching typos like "superuser" that
// would silently grant nothing while appearing in the effective-access UI.
func TestRBACMatrixRolesAreCanonical(t *testing.T) {
	canonical := map[string]bool{RoleViewer: true, RoleEditor: true, RoleProjectAdmin: true, RoleSystemAdmin: true}
	for _, resource := range authAccessMatrix {
		for _, op := range resource.Operations {
			for _, group := range [][]string{op.GlobalRoles, op.ProjectRoles, op.DomainRoles, op.RepositoryRoles} {
				for _, role := range group {
					if !canonical[role] {
						t.Errorf("operation %q references non-canonical role %q", op.ID, role)
					}
				}
			}
		}
	}
}

func matrixOperationByID(id string) (AuthAccessOperation, bool) {
	for _, resource := range authAccessMatrix {
		for _, op := range resource.Operations {
			if op.ID == id {
				return op, true
			}
		}
	}
	return AuthAccessOperation{}, false
}

func roleSetEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	counts := make(map[string]int, len(a))
	for _, r := range a {
		counts[r]++
	}
	for _, r := range b {
		counts[r]--
	}
	for _, v := range counts {
		if v != 0 {
			return false
		}
	}
	return true
}
