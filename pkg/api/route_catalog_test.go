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

type registeredRoute struct {
	Method      string
	Path        string
	SamplePath  string
	PathValues  map[string]string
	GenericAuth bool
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
			Method:      matches[1],
			Path:        matches[2],
			SamplePath:  samplePath,
			PathValues:  pathValues,
			GenericAuth: routeUsesGenericAuthMiddleware(line),
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
