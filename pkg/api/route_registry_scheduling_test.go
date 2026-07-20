package api

import (
	"net/http"
	"strings"
	"testing"
)

func isScenarioSchedulingRoute(path string) bool {
	return path == "/api/v1/schedules/health" ||
		path == "/api/v1/run-calendar" ||
		(strings.HasPrefix(path, "/api/v1/scenarios/") && strings.Contains(path, "/schedules"))
}

func TestScenarioSchedulingAPIRoutesUseModularCatalog(t *testing.T) {
	modular := make(map[string]apiRouteSpec)
	for _, catalog := range modularAPIRouteCatalogs {
		for _, route := range catalog.Routes {
			if isScenarioSchedulingRoute(route.Path) {
				modular[route.Method+" "+route.Path] = route
			}
		}
	}
	if got, want := len(modular), 13; got != want {
		t.Fatalf("modular scenario scheduling route count = %d, want %d", got, want)
	}

	discovered, err := discoverControllerRoutes("server.go")
	if err != nil {
		t.Fatalf("discoverControllerRoutes: %v", err)
	}
	discoveredScheduling := make(map[string]discoveredRoute)
	for _, route := range discovered {
		if !isScenarioSchedulingRoute(route.Path) {
			continue
		}
		key := route.Method + " " + route.Path
		discoveredScheduling[key] = route
		spec, ok := modular[key]
		if !ok {
			t.Errorf("scenario scheduling route %s is registered outside a modular route catalog", key)
			continue
		}
		if route.Handler != spec.HandlerIdentity {
			t.Errorf("scenario scheduling route %s handler = %q, want %q", key, route.Handler, spec.HandlerIdentity)
		}
		if !route.GenericAuth || route.Unauthenticated {
			t.Errorf("scenario scheduling route %s middleware discovery = genericAuth:%t unauthenticated:%t, want standard authenticated middleware", key, route.GenericAuth, route.Unauthenticated)
		}
	}
	for key := range modular {
		if _, ok := discoveredScheduling[key]; !ok {
			t.Errorf("modular scenario scheduling route %s was not discovered", key)
		}
	}

	for _, rule := range legacyRouteCatalogAuthRules {
		if isScenarioSchedulingRoute(rule.Path) {
			t.Errorf("scenario scheduling route %s %s still has a legacy authorization rule", rule.Method, rule.Path)
		}
	}
}

func TestScenarioSchedulingRouteCatalogPreservesAccessContracts(t *testing.T) {
	type routeAccessCase struct {
		method              string
		path                string
		requirement         accessRequirement
		scope               string
		documentedWithScope bool
	}
	cases := []routeAccessCase{
		{http.MethodPost, "/api/v1/scenarios/{id}/schedules/preview", editorProjectAccess(resolveScenarioProjectID), "tests:run", false},
		{http.MethodPost, "/api/v1/scenarios/{id}/schedules", editorProjectAccess(resolveScenarioProjectID), "tests:run", false},
		{http.MethodGet, "/api/v1/scenarios/{id}/schedules", viewerProjectAccess(resolveScenarioProjectID), "results:read", false},
		{http.MethodPut, "/api/v1/scenarios/{id}/schedules/{schedule_id}", editorProjectAccess(resolveScenarioProjectID), "tests:run", false},
		{http.MethodDelete, "/api/v1/scenarios/{id}/schedules/{schedule_id}", editorProjectAccess(resolveScenarioProjectID), "tests:run", false},
		{http.MethodGet, "/api/v1/scenarios/{id}/schedules/{schedule_id}/occurrences", viewerProjectAccess(resolveScenarioProjectID), "results:read", false},
		{http.MethodPost, "/api/v1/scenarios/{id}/schedules/{schedule_id}/occurrences/{occurrence_id}/resolve", projectAdminAccess(resolveScenarioProjectID), "tests:run", true},
		{http.MethodDelete, "/api/v1/scenarios/{id}/schedules/{schedule_id}/occurrences/{occurrence_id}", editorProjectAccess(resolveScenarioProjectID), "tests:run", false},
		{http.MethodGet, "/api/v1/scenarios/{id}/schedules/{schedule_id}/approvals", viewerProjectAccess(resolveScenarioProjectID), "results:read", false},
		{http.MethodPost, "/api/v1/scenarios/{id}/schedules/{schedule_id}/approvals/{approval_id}/approve", projectAdminAccess(resolveScenarioProjectID), "tests:run", false},
		{http.MethodPost, "/api/v1/scenarios/{id}/schedules/{schedule_id}/approvals/{approval_id}/reject", projectAdminAccess(resolveScenarioProjectID), "tests:run", false},
		{http.MethodGet, "/api/v1/schedules/health", viewerGlobalAccess(), "read", true},
		{http.MethodGet, "/api/v1/run-calendar", viewerGlobalAccess(), "read", true},
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
			wantDocumented := test.requirement
			if test.documentedWithScope {
				wantDocumented.ServiceAccountScope = test.scope
			}
			assertAccessRequirementEqual(t, documented, wantDocumented)
		})
	}
}
