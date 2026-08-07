package api

import (
	"net/http"
	"strings"
	"testing"
)

// TestClusterRoutesAreSystemAdminOnly is the authorization guard for the whole
// cluster-administration surface. Even the redacted read shape carries API
// server endpoints and Azure directory identifiers, so no viewer, editor, or
// project-admin tier is acceptable on any route here.
func TestClusterRoutesAreSystemAdminOnly(t *testing.T) {
	if len(clusterAdministrationAPIRouteCatalog.Routes) == 0 {
		t.Fatal("the cluster administration catalog is empty")
	}
	for _, route := range clusterAdministrationAPIRouteCatalog.Routes {
		label := route.Method + " " + route.Path
		roles := route.Access.GlobalRoles
		if len(roles) != 1 || roles[0] != RoleSystemAdmin {
			t.Errorf("%s GlobalRoles = %#v, want exactly [%s]", label, roles, RoleSystemAdmin)
		}
		for _, role := range roles {
			if role == RoleViewer || role == RoleEditor || role == RoleProjectAdmin {
				t.Errorf("%s grants %s; cluster administration must not be reachable below system admin", label, role)
			}
		}
		if len(route.Access.ProjectRoles) != 0 {
			t.Errorf("%s declares project roles %#v; cluster connections are system-scoped", label, route.Access.ProjectRoles)
		}
	}
}

// TestClusterMutationsRequireSettingsManageScope guards a real trap: the
// generic "/api/v1/system/" service-account scope rule maps every method to
// system:read, so without explicit rules a read-scoped token could register or
// delete a cluster connection.
func TestClusterMutationsRequireSettingsManageScope(t *testing.T) {
	mutations := []struct{ method, path string }{
		{http.MethodPost, "/api/v1/system/clusters"},
		{http.MethodPut, "/api/v1/system/clusters/cluster-1"},
		{http.MethodDelete, "/api/v1/system/clusters/cluster-1"},
		{http.MethodPost, "/api/v1/system/clusters/cluster-1/test"},
	}
	for _, tc := range mutations {
		if scope := staticServiceAccountScopeForRoute(tc.method, tc.path); scope != "settings:manage" {
			t.Errorf("%s %s scope = %q, want settings:manage", tc.method, tc.path, scope)
		}
	}
	// Reads stay on the system read scope.
	if scope := staticServiceAccountScopeForRoute(http.MethodGet, "/api/v1/system/clusters"); scope != "system:read" {
		t.Errorf("GET clusters scope = %q, want system:read", scope)
	}
}

// TestAllocationRouteIsRegisteredUnderFleetPools keeps the allocation surface
// discoverable next to the pool it belongs to.
func TestAllocationRouteIsRegisteredUnderFleetPools(t *testing.T) {
	var foundPut, foundPreflight bool
	for _, route := range clusterAdministrationAPIRouteCatalog.Routes {
		if route.Method == http.MethodPut && route.Path == "/api/v1/fleet/pools/{id}/allocation" {
			foundPut = true
			if route.Access.MaxBodyBytes <= 0 {
				t.Error("the allocation PUT has no request body bound")
			}
		}
		if route.Method == http.MethodGet && strings.HasSuffix(route.Path, "/allocation/preflight") {
			foundPreflight = true
		}
	}
	if !foundPut || !foundPreflight {
		t.Fatalf("allocation routes registered: put=%v preflight=%v, want both", foundPut, foundPreflight)
	}
}
