package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestDeploymentTargetRoutesAreSystemAdminOnly asserts against the RESOLVED
// requirement for a real request, not against the catalog entry.
//
// That distinction is the whole point. accessRequirementForRequest consults the
// declarative catalog first and then falls through to prefix catch-alls, so a
// catalog-only assertion would still pass if a future reordering let a broader
// rule win. These routes name the cluster a tenant's generators run in, which no
// viewer, editor, or project admin has any reason to read.
func TestDeploymentTargetRoutesAreSystemAdminOnly(t *testing.T) {
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/system/deployment-targets"},
		{http.MethodGet, "/api/v1/system/deployment-targets/domain-1"},
		{http.MethodPut, "/api/v1/system/deployment-targets/domain-1"},
		{http.MethodDelete, "/api/v1/system/deployment-targets/domain-1"},
	} {
		label := tc.method + " " + tc.path
		requirement, ok := accessRequirementForRequest(httptest.NewRequest(tc.method, tc.path, nil))
		if !ok {
			t.Errorf("%s resolved to NO access requirement; it would be unauthenticated", label)
			continue
		}
		roles := requirement.GlobalRoles
		if len(roles) != 1 || roles[0] != RoleSystemAdmin {
			t.Errorf("%s GlobalRoles = %#v, want exactly [%s]", label, roles, RoleSystemAdmin)
		}
		for _, role := range roles {
			if role == RoleViewer || role == RoleEditor || role == RoleProjectAdmin {
				t.Errorf("%s grants %s; a deployment target must not be readable below system admin", label, role)
			}
		}
		// A domain-scoped role must not substitute for the global one: these are
		// infrastructure bindings, not tenant configuration.
		if len(requirement.DomainRoles) != 0 || requirement.DomainLookup != nil {
			t.Errorf("%s declares domain-scoped access %#v; it must require the global system-admin role",
				label, requirement.DomainRoles)
		}
		if len(requirement.ProjectRoles) != 0 || requirement.ProjectLookup != nil {
			t.Errorf("%s declares project-scoped access; deployment targets are system-scoped", label)
		}
	}
}

// TestDeploymentTargetRoutesEscapeTheDomainsCatchAll pins why these live under
// /api/v1/system/ rather than /api/v1/domains/{id}/deployment-target.
//
// access_policy.go carries a prefix catch-all for /api/v1/domains/ whose GET
// branch grants viewer, editor, and project admin. Registering a deployment
// target under that prefix would put it one rule-ordering change away from being
// world-readable, so this test documents the hazard by proving it is real.
func TestDeploymentTargetRoutesEscapeTheDomainsCatchAll(t *testing.T) {
	underDomains, ok := accessRequirementForRequest(
		httptest.NewRequest(http.MethodGet, "/api/v1/domains/domain-1", nil))
	if !ok {
		t.Skip("the domains prefix rule no longer resolves; the hazard this documents is gone")
	}
	broad := false
	for _, role := range underDomains.GlobalRoles {
		if role == RoleViewer || role == RoleEditor || role == RoleProjectAdmin {
			broad = true
		}
	}
	if !broad {
		t.Skip("the domains prefix no longer grants sub-admin roles; the hazard this documents is gone")
	}

	// The hazard is real, so confirm the deployment-target routes are not under
	// that prefix and do not inherit it.
	requirement, ok := accessRequirementForRequest(
		httptest.NewRequest(http.MethodGet, "/api/v1/system/deployment-targets/domain-1", nil))
	if !ok {
		t.Fatal("deployment-target route resolved to no access requirement")
	}
	for _, role := range requirement.GlobalRoles {
		if role != RoleSystemAdmin {
			t.Fatalf("the deployment-target route inherited %s from a broader rule; it must stay system-admin only", role)
		}
	}
}

// TestDeploymentTargetMutationsRequireSettingsManageScope guards the same trap
// the cluster routes guard: the generic "/api/v1/system/" service-account rule
// maps every method to system:read, so without explicit rules a read-scoped
// token could repoint an entire domain's fleet at a different cloud.
func TestDeploymentTargetMutationsRequireSettingsManageScope(t *testing.T) {
	for _, tc := range []struct{ method, path string }{
		{http.MethodPut, "/api/v1/system/deployment-targets/domain-1"},
		{http.MethodPatch, "/api/v1/system/deployment-targets/domain-1"},
		{http.MethodPost, "/api/v1/system/deployment-targets/domain-1"},
		{http.MethodDelete, "/api/v1/system/deployment-targets/domain-1"},
	} {
		if scope := staticServiceAccountScopeForRoute(tc.method, tc.path); scope != "settings:manage" {
			t.Errorf("%s %s scope = %q, want settings:manage", tc.method, tc.path, scope)
		}
	}
	// Reads stay on the system read scope, matching GET /system/clusters.
	for _, path := range []string{
		"/api/v1/system/deployment-targets",
		"/api/v1/system/deployment-targets/domain-1",
	} {
		if scope := staticServiceAccountScopeForRoute(http.MethodGet, path); scope != "system:read" {
			t.Errorf("GET %s scope = %q, want system:read", path, scope)
		}
	}
}

// TestDeploymentTargetCatalogIsComplete keeps the four routes registered as a
// set: a handler added to the file but omitted from the catalog would be
// unreachable, and one removed from the catalog but left in server.go would be
// served without an authorization rule.
func TestDeploymentTargetCatalogIsComplete(t *testing.T) {
	want := map[string]bool{
		http.MethodGet + " /api/v1/system/deployment-targets":         false,
		http.MethodGet + " /api/v1/system/deployment-targets/{id}":    false,
		http.MethodPut + " /api/v1/system/deployment-targets/{id}":    false,
		http.MethodDelete + " /api/v1/system/deployment-targets/{id}": false,
	}
	for _, route := range domainDeploymentTargetAPIRouteCatalog.Routes {
		key := route.Method + " " + route.Path
		if _, expected := want[key]; !expected {
			t.Errorf("unexpected route %s in the deployment-target catalog", key)
			continue
		}
		want[key] = true
	}
	for key, found := range want {
		if !found {
			t.Errorf("route %s is missing from the deployment-target catalog", key)
		}
	}

	// The write must carry a body cap; an uncapped admin write is a trivial
	// memory-exhaustion vector.
	for _, route := range domainDeploymentTargetAPIRouteCatalog.Routes {
		if route.Method == http.MethodPut && route.Access.MaxBodyBytes <= 0 {
			t.Errorf("%s %s declares no MaxBodyBytes", route.Method, route.Path)
		}
	}
}
