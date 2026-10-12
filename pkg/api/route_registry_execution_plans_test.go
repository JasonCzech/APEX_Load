package api

import (
	"net/http"
	"strings"
	"testing"
)

func isExecutionPlanRoute(path string) bool {
	return path == "/api/v1/execution-plans" || strings.HasPrefix(path, "/api/v1/execution-plans/")
}

func TestExecutionPlanAPIRoutesUseModularCatalog(t *testing.T) {
	modular := make(map[string]apiRouteSpec)
	for _, catalog := range modularAPIRouteCatalogs {
		for _, route := range catalog.Routes {
			if isExecutionPlanRoute(route.Path) {
				modular[route.Method+" "+route.Path] = route
			}
		}
	}
	if got, want := len(modular), 19; got != want {
		t.Fatalf("modular execution-plan route count = %d, want %d", got, want)
	}

	discovered, err := discoverControllerRoutes("server.go")
	if err != nil {
		t.Fatalf("discoverControllerRoutes: %v", err)
	}
	discoveredExecutionPlans := make(map[string]discoveredRoute)
	for _, route := range discovered {
		if !isExecutionPlanRoute(route.Path) {
			continue
		}
		key := route.Method + " " + route.Path
		discoveredExecutionPlans[key] = route
		spec, ok := modular[key]
		if !ok {
			t.Errorf("execution-plan route %s is registered outside a modular route catalog", key)
			continue
		}
		if route.Handler != spec.HandlerIdentity {
			t.Errorf("execution-plan route %s handler = %q, want %q", key, route.Handler, spec.HandlerIdentity)
		}
		if !route.GenericAuth || route.Unauthenticated {
			t.Errorf("execution-plan route %s middleware discovery = genericAuth:%t unauthenticated:%t, want standard authenticated middleware", key, route.GenericAuth, route.Unauthenticated)
		}
	}
	for key := range modular {
		if _, ok := discoveredExecutionPlans[key]; !ok {
			t.Errorf("modular execution-plan route %s was not discovered", key)
		}
	}

	for _, rule := range legacyRouteCatalogAuthRules {
		if isExecutionPlanRoute(rule.Path) {
			t.Errorf("execution-plan route %s %s still has a legacy authorization rule", rule.Method, rule.Path)
		}
	}
}

func TestExecutionPlanRouteCatalogPreservesAccessContracts(t *testing.T) {
	type routeAccessCase struct {
		method      string
		path        string
		requirement accessRequirement
		scope       string
	}
	cases := []routeAccessCase{
		{http.MethodPost, "/api/v1/execution-plans", editorImportAccess(resolveExecutionPlanCreateProjectID, 262144), "tests:run"},
		{http.MethodGet, "/api/v1/execution-plans", viewerProjectAccess(resolveProjectIDFromQuery), "results:read"},
		{http.MethodGet, "/api/v1/execution-plans/{id}", viewerProjectAccess(resolveExecutionPlanProjectID), "results:read"},
		{http.MethodPut, "/api/v1/execution-plans/{id}", editorImportAccess(resolveExecutionPlanProjectID, 262144), "tests:run"},
		{http.MethodDelete, "/api/v1/execution-plans/{id}", editorProjectAccess(resolveExecutionPlanProjectID), "tests:run"},
		{http.MethodPost, "/api/v1/execution-plans/{id}/runs", editorProjectAccess(resolveExecutionPlanProjectID), "tests:run"},
		{http.MethodGet, "/api/v1/execution-plans/{id}/runs", viewerProjectAccess(resolveExecutionPlanProjectID), "results:read"},
		{http.MethodGet, "/api/v1/execution-plans/{id}/runs/{runId}", viewerProjectAccess(resolveExecutionPlanProjectID), "results:read"},
		{http.MethodPost, "/api/v1/execution-plans/{id}/schedules", editorProjectAccess(resolveExecutionPlanProjectID), "tests:run"},
		{http.MethodGet, "/api/v1/execution-plans/{id}/schedules", viewerProjectAccess(resolveExecutionPlanProjectID), "results:read"},
		{http.MethodPut, "/api/v1/execution-plans/{id}/schedules/{schedule_id}", editorProjectAccess(resolveExecutionPlanProjectID), "tests:run"},
		{http.MethodDelete, "/api/v1/execution-plans/{id}/schedules/{schedule_id}", editorProjectAccess(resolveExecutionPlanProjectID), "tests:run"},
		{http.MethodGet, "/api/v1/execution-plans/{id}/schedules/{schedule_id}/occurrences", viewerProjectAccess(resolveExecutionPlanProjectID), "results:read"},
		{http.MethodPost, "/api/v1/execution-plans/{id}/schedules/{schedule_id}/occurrences/{occurrence_id}/resolve", projectAdminAccess(resolveExecutionPlanProjectID), "tests:run"},
		{http.MethodGet, "/api/v1/execution-plans/{id}/approvals", viewerProjectAccess(resolveExecutionPlanProjectID), "results:read"},
		{http.MethodPost, "/api/v1/execution-plans/{id}/approvals/{approval_id}/approve", projectAdminAccess(resolveExecutionPlanProjectID), "tests:run"},
		{http.MethodPost, "/api/v1/execution-plans/{id}/approvals/{approval_id}/reject", projectAdminAccess(resolveExecutionPlanProjectID), "tests:run"},
	}

	for _, test := range cases {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			got, ok := accessRequirementFromRouteCatalog(test.method, test.path)
			if !ok {
				t.Fatal("route is absent from the authorization catalog")
			}
			want := test.requirement
			want.ServiceAccountScope = test.scope
			assertAccessRequirementEqual(t, got, want)

			documented, ok := documentedAccessRequirementFromAPIRouteCatalogs(test.method, test.path)
			if !ok {
				t.Fatal("route is absent from the documentation access catalog")
			}
			assertAccessRequirementEqual(t, documented, want)
		})
	}
}
