package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

var liveTableScopeRoutes = []struct {
	method, path, scope, poolPath string
}{
	{http.MethodGet, "/api/v1/tests/missing-run/live-tables", "read", "/api/v1/data-pools/pool"},
	{http.MethodGet, "/api/v1/tests/missing-run/live-tables/table/rows", "read", "/api/v1/data-pools/pool/rows"},
	{http.MethodGet, "/api/v1/tests/missing-run/live-tables/table/export", "read", "/api/v1/data-pools/pool/rows/export"},
	{http.MethodPost, "/api/v1/tests/missing-run/live-tables/table/finalize", "scripts:write", "/api/v1/data-pools"},
}

func TestLiveTableRouteScopesMatchSharedDataPools(t *testing.T) {
	for _, route := range liveTableScopeRoutes {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			req := httptest.NewRequest(route.method, route.path, nil)
			requirement, ok := accessRequirementForRequest(req)
			if !ok || requirement.ServiceAccountScope != route.scope || requirement.ServiceAccountScopeLookup != nil || requirement.ProjectLookup == nil {
				t.Fatalf("live table route lacks explicit pool scope and project gate: %+v", requirement)
			}
			if got := requiredServiceAccountScope(req); got != route.scope {
				t.Fatalf("effective scope = %q, want %q", got, route.scope)
			}
			if got := requiredServiceAccountScope(httptest.NewRequest(route.method, route.poolPath, nil)); got != route.scope {
				t.Fatalf("shared data pool scope = %q, live table scope = %q", got, route.scope)
			}
		})
	}
}

func TestLiveTableRoutesRejectNarrowRunScopesThroughMiddleware(t *testing.T) {
	server, store := newRuntimeOverrideTestServer(t)
	enableTrustedHeaderAuth(t, server)
	now := time.Now().UTC()
	for routeIndex, route := range liveTableScopeRoutes {
		for scopeIndex, scope := range []string{"results:read", "tests:run", "read", "write", "scripts:write", "*", "automation:*"} {
			t.Run(route.method+" "+route.path+" "+scope, func(t *testing.T) {
				key := fmt.Sprintf("live-scope-%d-%d", routeIndex, scopeIndex)
				// A system-admin role deliberately passes RBAC: service-account
				// scopes must independently deny raw data reads and pool writes.
				if err := store.SaveServiceAccount(&storage.ServiceAccount{
					ID: key, Name: key, KeyHash: hashAPIKey(key), Active: true,
					GlobalRoles: []string{RoleSystemAdmin}, Scopes: []string{scope}, CreatedAt: now, UpdatedAt: now,
				}); err != nil {
					t.Fatal(err)
				}
				req := httptest.NewRequest(route.method, route.path, strings.NewReader(`{"mode":"save_as_new","name":"must not create"}`))
				req.Header.Set("X-APEXLoad-API-Key", key)
				response := httptest.NewRecorder()
				server.mux.ServeHTTP(response, req)
				allowed := scope == route.scope || scope == "*" || scope == "automation:*"
				if allowed {
					// The admitted request reaches the real handler, which reports
					// the absent run without touching live state or reusable pools.
					if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), "run not found") {
						t.Fatalf("explicit pool scope did not reach handler: %d %s", response.Code, response.Body.String())
					}
				} else if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "token scope") {
					t.Fatalf("scope %q bypassed shared-data scope gate: %d %s", scope, response.Code, response.Body.String())
				}
			})
		}
	}
}
