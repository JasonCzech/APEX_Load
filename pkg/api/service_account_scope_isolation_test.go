package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNarrowServiceAccountScopesDoNotImplyGenericAccess(t *testing.T) {
	narrowScopes := []string{
		"results:read", "system:read", "audit:read", "scripts:write", "tests:run",
		"results:write", "settings:manage", "deployments:manage", deploymentActiveTestsOverrideScope, "service_accounts:manage",
	}
	for _, scope := range narrowScopes {
		t.Run(scope+"_read", func(t *testing.T) {
			account := &AuthenticatedServiceAccount{ID: "svc", Scopes: []string{scope}}
			if serviceAccountAllowsRequest(account, httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil)) {
				t.Fatalf("narrow scope %q implied generic read", scope)
			}
		})
		t.Run(scope+"_write", func(t *testing.T) {
			account := &AuthenticatedServiceAccount{ID: "svc", Scopes: []string{scope}}
			if serviceAccountAllowsRequest(account, httptest.NewRequest(http.MethodPost, "/api/v1/projects", nil)) {
				t.Fatalf("narrow scope %q implied generic write", scope)
			}
		})
	}

	for _, scope := range []string{"read", "write", "*", "automation:*"} {
		method := http.MethodGet
		if scope == "write" {
			method = http.MethodPost
		}
		account := &AuthenticatedServiceAccount{ID: "svc", Scopes: []string{scope}}
		if !serviceAccountAllowsRequest(account, httptest.NewRequest(method, "/api/v1/projects", nil)) {
			t.Fatalf("explicit broad scope %q did not authorize its generic route", scope)
		}
	}
}

func TestActiveTestDeploymentOverrideRequiresBothDeploymentScopes(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/v1/system/deployment-lease/acquire", nil)
	overrideOnly := &AuthenticatedServiceAccount{ID: "override-only", Scopes: []string{deploymentActiveTestsOverrideScope}}
	if serviceAccountAllowsRequest(overrideOnly, request) {
		t.Fatal("active-test override scope implied ordinary deployment lease authority")
	}
	if !serviceAccountHasExplicitScope(overrideOnly, deploymentActiveTestsOverrideScope) {
		t.Fatal("explicit active-test override scope was not recognized")
	}
	ordinary := &AuthenticatedServiceAccount{ID: "ordinary", Scopes: []string{"deployments:manage"}}
	if serviceAccountHasExplicitScope(ordinary, deploymentActiveTestsOverrideScope) {
		t.Fatal("ordinary deployment scope implied active-test override authority")
	}
	elevated := &AuthenticatedServiceAccount{ID: "elevated", Scopes: []string{"deployments:manage", deploymentActiveTestsOverrideScope}}
	if !serviceAccountAllowsRequest(elevated, request) || !serviceAccountHasExplicitScope(elevated, deploymentActiveTestsOverrideScope) {
		t.Fatal("separately scoped active-test override credential was rejected")
	}
	if _, err := normalizeServiceAccountScopes(elevated.Scopes); err != nil {
		t.Fatalf("active-test override service-account scopes were rejected: %v", err)
	}
}

func TestSpecializedRoutesUseExactServiceAccountScopes(t *testing.T) {
	tests := []struct {
		scope  string
		method string
		path   string
	}{
		{scope: "audit:read", method: http.MethodGet, path: "/api/v1/auth/audit-events"},
		{scope: "system:read", method: http.MethodGet, path: "/api/v1/system/settings"},
		{scope: "results:read", method: http.MethodGet, path: "/api/v1/tests/test-1"},
		{scope: "scripts:write", method: http.MethodPost, path: "/api/v1/scripts"},
		{scope: "tests:run", method: http.MethodPost, path: "/api/v1/execution-plans"},
		{scope: "results:write", method: http.MethodPost, path: "/api/v1/results/import"},
		{scope: "settings:manage", method: http.MethodPut, path: "/api/v1/system/settings"},
		{scope: "deployments:manage", method: http.MethodPost, path: "/api/v1/system/deployment-lease/acquire"},
		{scope: "service_accounts:manage", method: http.MethodGet, path: "/api/v1/auth/users"},
	}
	for _, test := range tests {
		t.Run(test.scope, func(t *testing.T) {
			req := httptest.NewRequest(test.method, test.path, nil)
			if got := requiredServiceAccountScope(req); got != test.scope {
				t.Fatalf("required scope = %q, want %q", got, test.scope)
			}
			if !serviceAccountAllowsRequest(&AuthenticatedServiceAccount{ID: "svc", Scopes: []string{test.scope}}, req) {
				t.Fatal("exact specialized scope was rejected")
			}
		})
	}
}
