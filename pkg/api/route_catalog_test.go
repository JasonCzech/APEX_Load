package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestAccessRequirementForRequestUsesRouteCatalogExactRules(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/system/usage/snapshots", nil)

	requirement, ok := accessRequirementForRequest(req)
	if !ok {
		t.Fatal("accessRequirementForRequest() did not match catalog rule")
	}
	if len(requirement.GlobalRoles) != 1 || requirement.GlobalRoles[0] != RoleSystemAdmin {
		t.Fatalf("GlobalRoles = %#v, want system admin only", requirement.GlobalRoles)
	}
}

func TestAccessRequirementFromRouteCatalogDistinguishesMethod(t *testing.T) {
	getRequirement, ok := accessRequirementFromRouteCatalog(http.MethodGet, "/api/v1/system/settings")
	if !ok {
		t.Fatal("GET /api/v1/system/settings did not match catalog")
	}
	if len(getRequirement.GlobalRoles) != 4 {
		t.Fatalf("GET GlobalRoles = %#v, want viewer/editor/project/system roles", getRequirement.GlobalRoles)
	}

	putRequirement, ok := accessRequirementFromRouteCatalog(http.MethodPut, "/api/v1/system/settings")
	if !ok {
		t.Fatal("PUT /api/v1/system/settings did not match catalog")
	}
	if len(putRequirement.GlobalRoles) != 1 || putRequirement.GlobalRoles[0] != RoleSystemAdmin {
		t.Fatalf("PUT GlobalRoles = %#v, want system admin only", putRequirement.GlobalRoles)
	}
}

func TestPrometheusGraphsRouteUsesViewerAccess(t *testing.T) {
	requirement, ok := accessRequirementFromRouteCatalog(http.MethodGet, "/api/v1/monitoring/prometheus/graphs")
	if !ok {
		t.Fatal("GET /api/v1/monitoring/prometheus/graphs did not match catalog")
	}
	if len(requirement.GlobalRoles) != 4 || requirement.GlobalRoles[0] != RoleViewer {
		t.Fatalf("GlobalRoles = %#v, want viewer/editor/project/system roles", requirement.GlobalRoles)
	}
}

func TestRoutePathTemplateMatches(t *testing.T) {
	tests := []struct {
		name     string
		template string
		path     string
		want     bool
	}{
		{"single placeholder matches one segment", "/api/v1/tests/{id}", "/api/v1/tests/t1", true},
		{"placeholder does not match deeper path", "/api/v1/tests/{id}", "/api/v1/tests/t1/start", false},
		{"placeholder does not match collection", "/api/v1/tests/{id}", "/api/v1/tests", false},
		{"middle placeholder with literal suffix", "/api/v1/scripts/{id}/github/push", "/api/v1/scripts/s1/github/push", true},
		{"literal suffix must match", "/api/v1/scripts/{id}/github/push", "/api/v1/scripts/s1/github/status", false},
		{"two placeholders", "/api/v1/auth/users/{id}/bindings/{bid}", "/api/v1/auth/users/u1/bindings/b1", true},
		{"empty segment rejected", "/api/v1/tests/{id}", "/api/v1/tests/", false},
		{"exact-only template is skipped here", "/api/v1/tests", "/api/v1/tests", false},
		{"literal prefix mismatch", "/api/v1/tests/{id}", "/api/v1/scripts/s1", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := routePathTemplateMatches(tc.template, tc.path); got != tc.want {
				t.Errorf("routePathTemplateMatches(%q, %q) = %v, want %v", tc.template, tc.path, got, tc.want)
			}
		})
	}
}

// TestRouteCatalogHasNoDuplicateRules guards against two rules claiming the same
// (method, path), which would make access resolution order-dependent.
func TestRouteCatalogHasNoDuplicateRules(t *testing.T) {
	seen := map[string]bool{}
	for _, rule := range routeCatalogAuthRules {
		key := rule.Method + " " + rule.Path
		if seen[key] {
			t.Errorf("duplicate route catalog rule for %s", key)
		}
		seen[key] = true
	}
}

func TestGenericAuthAPIRoutesDeclareAccessRequirement(t *testing.T) {
	routes := registeredServerRoutes(t)
	for _, route := range routes {
		if !strings.HasPrefix(route.Path, "/api/v1/") || !route.GenericAuth {
			continue
		}

		req := httptest.NewRequest(route.Method, route.SamplePath, nil)
		for name, value := range route.PathValues {
			req.SetPathValue(name, value)
		}
		if _, ok := accessRequirementForRequest(req); !ok {
			t.Errorf("%s %s uses auth middleware but has no access requirement", route.Method, route.Path)
		}
	}
}

func TestRouteCatalogDeclaresServiceAccountScopeGate(t *testing.T) {
	for _, rule := range routeCatalogAuthRules {
		req := rule.Requirement
		if req.ServiceAccountScope == "" && req.ServiceAccountScopeLookup == nil {
			t.Errorf("%s %s has no service-account scope gate", rule.Method, rule.Path)
			continue
		}
		if req.ServiceAccountScope != "" {
			if _, err := normalizeServiceAccountScopes([]string{req.ServiceAccountScope}); err != nil {
				t.Errorf("%s %s declares unsupported service-account scope %q: %v", rule.Method, rule.Path, req.ServiceAccountScope, err)
			}
		}
	}
}

func TestGenericAuthAPIRoutesResolveServiceAccountScope(t *testing.T) {
	routes := registeredServerRoutes(t)
	for _, route := range routes {
		if !strings.HasPrefix(route.Path, "/api/v1/") || !route.GenericAuth {
			continue
		}
		req := httptest.NewRequest(route.Method, route.SamplePath, nil)
		for name, value := range route.PathValues {
			req.SetPathValue(name, value)
		}
		scope := requiredServiceAccountScope(req)
		if scope == "" {
			t.Errorf("%s %s resolved empty service-account scope", route.Method, route.Path)
			continue
		}
		if _, err := normalizeServiceAccountScopes([]string{scope}); err != nil {
			t.Errorf("%s %s resolved unsupported service-account scope %q: %v", route.Method, route.Path, scope, err)
		}
	}
}

func TestRequiredServiceAccountScopeComesFromRouteCatalog(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		want   string
	}{
		{name: "system read", method: http.MethodGet, path: "/api/v1/system/settings", want: "system:read"},
		{name: "system manage", method: http.MethodPut, path: "/api/v1/system/settings", want: "settings:manage"},
		{name: "auth user management", method: http.MethodPost, path: "/api/v1/auth/users/onboard", want: "service_accounts:manage"},
		{name: "audit read", method: http.MethodGet, path: "/api/v1/auth/audit-events", want: "audit:read"},
		{name: "access review", method: http.MethodGet, path: "/api/v1/auth/access-review", want: "audit:read"},
		{name: "test read", method: http.MethodGet, path: "/api/v1/tests/t1", want: "results:read"},
		{name: "test control", method: http.MethodPost, path: "/api/v1/tests/t1/start", want: "tests:run"},
		{name: "script write", method: http.MethodPost, path: "/api/v1/scripts/import/sample", want: "scripts:write"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			if got := requiredServiceAccountScope(req); got != tc.want {
				t.Fatalf("requiredServiceAccountScope(%s %s) = %q, want %q", tc.method, tc.path, got, tc.want)
			}
		})
	}
}

type registeredRoute struct {
	Method      string
	Path        string
	SamplePath  string
	PathValues  map[string]string
	GenericAuth bool
	// Unauthenticated is true when the route is registered with no auth middleware of
	// any kind (cors(log(http.HandlerFunc(...)))) — neither generic auth() nor a custom
	// per-resource auth wrapper.
	Unauthenticated bool
}

func registeredServerRoutes(t *testing.T) []registeredRoute {
	t.Helper()

	data, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("read server.go: %v", err)
	}

	routePattern := regexp.MustCompile(`s\.mux\.Handle\("([A-Z]+) ([^"]+)"`)
	var routes []registeredRoute
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.Contains(line, "s.mux.Handle(") {
			continue
		}
		matches := routePattern.FindStringSubmatch(line)
		if len(matches) != 3 {
			continue
		}
		samplePath, pathValues := sampleRegisteredRoutePath(matches[2])
		routes = append(routes, registeredRoute{
			Method:          matches[1],
			Path:            matches[2],
			SamplePath:      samplePath,
			PathValues:      pathValues,
			GenericAuth:     routeUsesGenericAuthMiddleware(line),
			Unauthenticated: routeHasNoAuthMiddleware(line),
		})
	}
	if len(routes) == 0 {
		t.Fatal("no server routes found")
	}
	return routes
}

func sampleRegisteredRoutePath(path string) (string, map[string]string) {
	pathValues := map[string]string{}
	paramPattern := regexp.MustCompile(`\{([^}]+)\}`)
	samplePath := paramPattern.ReplaceAllStringFunc(path, func(token string) string {
		name := strings.Trim(token, "{}")
		value := "sample-" + strings.ReplaceAll(name, "_", "-")
		pathValues[name] = value
		return value
	})
	return samplePath, pathValues
}

func routeUsesGenericAuthMiddleware(line string) bool {
	return strings.Contains(line, "log(auth(")
}

// routeHasNoAuthMiddleware reports whether a mux.Handle line wires the handler with
// no auth middleware at all — the handler sits directly inside log(...) with no auth
// wrapper (cors(log(http.HandlerFunc(...)))). Routes guarded by a custom per-resource
// auth wrapper read log(<wrapper>(http.HandlerFunc(...))) and are not matched.
func routeHasNoAuthMiddleware(line string) bool {
	return strings.Contains(line, "log(http.HandlerFunc(")
}
