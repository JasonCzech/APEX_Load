package api

import (
	"net/http"
	"strings"
	"testing"
)

func isAuthoringContentRoute(path string) bool {
	return path == "/api/v1/object-repositories" ||
		strings.HasPrefix(path, "/api/v1/object-repositories/") ||
		path == "/api/v1/correlation-rulesets" ||
		strings.HasPrefix(path, "/api/v1/correlation-rulesets/")
}

func TestAuthoringContentAPIRoutesUseModularCatalogs(t *testing.T) {
	modular := make(map[string]apiRouteSpec)
	counts := map[string]int{}
	for _, catalog := range modularAPIRouteCatalogs {
		for _, route := range catalog.Routes {
			if !isAuthoringContentRoute(route.Path) {
				continue
			}
			modular[route.Method+" "+route.Path] = route
			if strings.HasPrefix(route.Path, "/api/v1/object-repositories") {
				counts["object-repositories"]++
			} else {
				counts["correlation-rulesets"]++
			}
		}
	}
	if got, want := len(modular), 13; got != want {
		t.Fatalf("modular authoring-content route count = %d, want %d", got, want)
	}
	if counts["object-repositories"] != 6 || counts["correlation-rulesets"] != 7 {
		t.Fatalf("domain route counts = %#v, want object-repositories:6 correlation-rulesets:7", counts)
	}

	discovered, err := discoverControllerRoutes("server.go")
	if err != nil {
		t.Fatalf("discoverControllerRoutes: %v", err)
	}
	discoveredAuthoringContent := make(map[string]discoveredRoute)
	for _, route := range discovered {
		if !isAuthoringContentRoute(route.Path) {
			continue
		}
		key := route.Method + " " + route.Path
		discoveredAuthoringContent[key] = route
		spec, ok := modular[key]
		if !ok {
			t.Errorf("authoring-content route %s is registered outside a modular route catalog", key)
			continue
		}
		if route.Handler != spec.HandlerIdentity {
			t.Errorf("authoring-content route %s handler = %q, want %q", key, route.Handler, spec.HandlerIdentity)
		}
		if !route.GenericAuth || route.Unauthenticated {
			t.Errorf("authoring-content route %s middleware discovery = genericAuth:%t unauthenticated:%t, want standard authenticated middleware", key, route.GenericAuth, route.Unauthenticated)
		}
	}
	for key := range modular {
		if _, ok := discoveredAuthoringContent[key]; !ok {
			t.Errorf("modular authoring-content route %s was not discovered", key)
		}
	}

	for _, rule := range legacyRouteCatalogAuthRules {
		if isAuthoringContentRoute(rule.Path) {
			t.Errorf("authoring-content route %s %s still has a legacy authorization rule", rule.Method, rule.Path)
		}
	}
}

func TestAuthoringContentRouteCatalogPreservesAccessContracts(t *testing.T) {
	type routeAccessCase struct {
		method      string
		path        string
		requirement accessRequirement
		scope       string
	}
	cases := []routeAccessCase{
		{http.MethodPost, "/api/v1/object-repositories", editorImportAccess(resolveObjectRepositoryCreateProjectID, 262144), "scripts:write"},
		{http.MethodGet, "/api/v1/object-repositories", viewerProjectAccess(resolveProjectIDFromQuery), "read"},
		{http.MethodPost, "/api/v1/object-repositories/from-script", editorProjectAccess(resolveObjectRepositoryFromScriptProjectID), "scripts:write"},
		{http.MethodGet, "/api/v1/object-repositories/{id}", viewerProjectAccess(resolveObjectRepositoryProjectID), "read"},
		{http.MethodPut, "/api/v1/object-repositories/{id}", editorImportAccess(resolveObjectRepositoryProjectID, 262144), "scripts:write"},
		{http.MethodDelete, "/api/v1/object-repositories/{id}", editorProjectAccess(resolveObjectRepositoryProjectID), "scripts:write"},
		{http.MethodPost, "/api/v1/correlation-rulesets", editorImportAccess(resolveCorrelationRulesetCreateProjectID, 262144), "scripts:write"},
		{http.MethodGet, "/api/v1/correlation-rulesets", viewerProjectAccess(resolveProjectIDFromQuery), "read"},
		{http.MethodPost, "/api/v1/correlation-rulesets/discover-from-script", editorProjectAccess(resolveCorrelationRulesetFromScriptProjectID), "scripts:write"},
		{http.MethodGet, "/api/v1/correlation-rulesets/{id}", viewerProjectAccess(resolveCorrelationRulesetProjectID), "read"},
		{http.MethodPut, "/api/v1/correlation-rulesets/{id}", editorImportAccess(resolveCorrelationRulesetProjectID, 262144), "scripts:write"},
		{http.MethodDelete, "/api/v1/correlation-rulesets/{id}", editorProjectAccess(resolveCorrelationRulesetProjectID), "scripts:write"},
		{http.MethodPost, "/api/v1/correlation-rulesets/{id}/apply-to-script", editorProjectAccess(resolveCorrelationRulesetProjectID), "scripts:write"},
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
