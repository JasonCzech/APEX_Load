package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNarrowServiceAccountScopesDoNotImplyGenericAccess(t *testing.T) {
	narrowScopes := []string{
		"results:read", "system:read", "audit:read", "scripts:write", "tests:run",
		"results:write", "settings:manage", "service_accounts:manage",
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
