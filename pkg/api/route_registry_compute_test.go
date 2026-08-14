package api

import (
	"net/http"
	"testing"
)

func TestComputeRoutesAreSystemAdminOnly(t *testing.T) {
	if got, want := len(computeAdministrationAPIRouteCatalog.Routes), 12; got != want {
		t.Fatalf("compute route count = %d, want %d", got, want)
	}
	for _, route := range computeAdministrationAPIRouteCatalog.Routes {
		if route.Access == nil {
			t.Errorf("%s %s has no access requirement", route.Method, route.Path)
			continue
		}
		if roles := route.Access.GlobalRoles; len(roles) != 1 || roles[0] != RoleSystemAdmin {
			t.Errorf("%s %s roles = %#v, want system_admin only", route.Method, route.Path, roles)
		}
	}
	want := map[string]bool{
		http.MethodPost + " /api/v1/system/cloud-connections":                         true,
		http.MethodGet + " /api/v1/system/cloud-connections":                          true,
		http.MethodGet + " /api/v1/system/cloud-connections/{id}":                     true,
		http.MethodPut + " /api/v1/system/cloud-connections/{id}":                     true,
		http.MethodDelete + " /api/v1/system/cloud-connections/{id}":                  true,
		http.MethodPost + " /api/v1/system/execution-targets":                         true,
		http.MethodGet + " /api/v1/system/execution-targets":                          true,
		http.MethodGet + " /api/v1/system/execution-targets/{id}":                     true,
		http.MethodPut + " /api/v1/system/execution-targets/{id}":                     true,
		http.MethodDelete + " /api/v1/system/execution-targets/{id}":                  true,
		http.MethodPost + " /api/v1/system/execution-targets/{id}/test":               true,
		http.MethodGet + " /api/v1/system/execution-targets/{id}/capabilities":         true,
	}
	for _, route := range computeAdministrationAPIRouteCatalog.Routes {
		delete(want, route.Method+" "+route.Path)
	}
	if len(want) != 0 {
		t.Fatalf("compute catalog is missing exact routes: %#v", want)
	}
}

func TestComputeMutationsRequireSettingsManageScope(t *testing.T) {
	mutations := []struct{ method, path string }{
		{http.MethodPost, "/api/v1/system/cloud-connections"},
		{http.MethodPut, "/api/v1/system/cloud-connections/resource-1"},
		{http.MethodDelete, "/api/v1/system/cloud-connections/resource-1"},
		{http.MethodPost, "/api/v1/system/execution-targets"},
		{http.MethodPut, "/api/v1/system/execution-targets/resource-1"},
		{http.MethodDelete, "/api/v1/system/execution-targets/resource-1"},
		{http.MethodPost, "/api/v1/system/execution-targets/resource-1/test"},
	}
	for _, mutation := range mutations {
		if scope := staticServiceAccountScopeForRoute(mutation.method, mutation.path); scope != "settings:manage" {
			t.Errorf("%s %s scope = %q, want settings:manage", mutation.method, mutation.path, scope)
		}
	}
	for _, prefix := range []string{"cloud-connections", "execution-targets"} {
		if scope := staticServiceAccountScopeForRoute(http.MethodGet, "/api/v1/system/"+prefix); scope != "system:read" {
			t.Errorf("GET %s scope = %q, want system:read", prefix, scope)
		}
	}
}
