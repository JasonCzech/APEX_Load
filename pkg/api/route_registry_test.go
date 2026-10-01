package api

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestModularAPIRouteCatalogsAreCompleteAndUnique(t *testing.T) {
	seen := map[string]string{}
	for _, catalog := range modularAPIRouteCatalogs {
		if catalog.Domain == "" {
			t.Error("modular route catalog has an empty domain")
		}
		if len(catalog.Routes) == 0 {
			t.Errorf("route catalog %q has no routes", catalog.Domain)
		}
		for _, route := range catalog.Routes {
			key := route.Method + " " + route.Path
			if previous, duplicate := seen[key]; duplicate {
				t.Errorf("route %s appears in both %q and %q catalogs", key, previous, catalog.Domain)
			}
			seen[key] = catalog.Domain
			if !httpRouteMethods[route.Method] || route.Path == "" || route.HandlerIdentity == "" || route.Handler == nil {
				t.Errorf("route catalog %q has incomplete route %#v", catalog.Domain, route)
			}
			switch route.MiddlewareProfile {
			case apiRouteMiddlewarePublic:
				if route.Access != nil {
					t.Errorf("public route %s unexpectedly declares a generic-auth access requirement", key)
				}
			case apiRouteMiddlewareAuthenticated:
				if route.Access == nil {
					t.Errorf("authenticated route %s has no access requirement", key)
				}
			default:
				t.Errorf("route %s has unsupported middleware profile %d", key, route.MiddlewareProfile)
			}
		}
	}
}

func TestControllerRouteDiscoveryIncludesModularCatalogs(t *testing.T) {
	routes, err := discoverControllerRoutes("server.go")
	if err != nil {
		t.Fatalf("discoverControllerRoutes: %v", err)
	}
	discovered := make(map[string]discoveredRoute, len(routes))
	for _, route := range routes {
		key := route.Method + " " + route.Path
		if _, duplicate := discovered[key]; duplicate {
			t.Errorf("route discovery returned duplicate %s", key)
		}
		discovered[key] = route
	}

	for _, catalog := range modularAPIRouteCatalogs {
		for _, spec := range catalog.Routes {
			key := spec.Method + " " + spec.Path
			route, ok := discovered[key]
			if !ok {
				t.Errorf("modular route %s was not discovered from the routing package", key)
				continue
			}
			if route.Handler != spec.HandlerIdentity {
				t.Errorf("route %s handler = %q, want %q", key, route.Handler, spec.HandlerIdentity)
			}
			wantAuthenticated := spec.MiddlewareProfile == apiRouteMiddlewareAuthenticated
			if route.GenericAuth != wantAuthenticated || route.Unauthenticated == wantAuthenticated {
				t.Errorf("route %s middleware discovery = genericAuth:%t unauthenticated:%t, want authenticated:%t", key, route.GenericAuth, route.Unauthenticated, wantAuthenticated)
			}
		}
	}
}

// boundHandlerMethodName recovers the *Server method a route binder actually
// closed over. Every modular binder has the shape
// `func(s *Server) http.Handler { return http.HandlerFunc(s.handleX) }`, and Go
// compiles the method value `s.handleX` into a uniquely named wrapper
// `<pkgpath>.(*Server).handleX-fm`, so the bound method is recoverable from the
// handler's code pointer.
func boundHandlerMethodName(handler http.Handler) (string, bool) {
	handlerFunc, ok := handler.(http.HandlerFunc)
	if !ok {
		return "", false
	}
	fn := runtime.FuncForPC(reflect.ValueOf(handlerFunc).Pointer())
	if fn == nil {
		return "", false
	}
	name := strings.TrimSuffix(fn.Name(), "-fm")
	if index := strings.LastIndex(name, "."); index >= 0 {
		name = name[index+1:]
	}
	if name == "" {
		return "", false
	}
	return name, true
}

// TestModularRouteHandlerIdentityMatchesBoundHandler closes the gap left by
// TestControllerRouteDiscoveryIncludesModularCatalogs: discoveredRouteSpec reads
// the handler name out of the third string literal passed to
// newAuthenticatedAPIRoute/newPublicAPIRoute, which IS HandlerIdentity, so that
// comparison is satisfied no matter which method the binder closure registers.
// HandlerIdentity is the only input the OpenAPI/endpoint-catalog generators have
// for a route's operation identity, so a binder that serves a different handler
// than its identity claims would ship a silently wrong published contract. This
// test binds the declared identity to the handler the mux actually receives.
func TestModularRouteHandlerIdentityMatchesBoundHandler(t *testing.T) {
	server := &Server{mux: http.NewServeMux()}
	checked := 0
	for _, catalog := range modularAPIRouteCatalogs {
		for _, route := range catalog.Routes {
			key := route.Method + " " + route.Path
			if route.Handler == nil {
				t.Errorf("route %s in catalog %q has no handler binder", key, catalog.Domain)
				continue
			}
			bound, ok := boundHandlerMethodName(route.Handler(server))
			if !ok {
				t.Errorf("route %s in catalog %q binds a handler whose *Server method is not recoverable; keep binders as http.HandlerFunc(s.<method>) so HandlerIdentity stays verifiable", key, catalog.Domain)
				continue
			}
			if bound != route.HandlerIdentity {
				t.Errorf("route %s in catalog %q declares HandlerIdentity %q but registers %q", key, catalog.Domain, route.HandlerIdentity, bound)
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("no modular route handler identities were verified")
	}
}

func TestModularRouteAccessRulesFeedAuthorizationCatalog(t *testing.T) {
	for _, catalog := range modularAPIRouteCatalogs {
		for _, route := range catalog.Routes {
			if route.Access == nil {
				continue
			}
			got, ok := accessRequirementFromRouteCatalog(route.Method, route.Path)
			if !ok {
				t.Errorf("route %s %s is absent from authorization catalog", route.Method, route.Path)
				continue
			}
			if len(got.GlobalRoles) != len(route.Access.GlobalRoles) {
				t.Errorf("route %s %s global role count = %d, want %d", route.Method, route.Path, len(got.GlobalRoles), len(route.Access.GlobalRoles))
			}
		}
	}
}

func TestRegisterAPIRouteCatalogPreservesMiddlewareOrder(t *testing.T) {
	server := &Server{mux: http.NewServeMux()}
	var calls []string
	wrap := func(name string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls = append(calls, name)
				next.ServeHTTP(w, r)
			})
		}
	}
	catalog := apiRouteCatalog{Domain: "test", Routes: []apiRouteSpec{
		newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/test-route", "handleTest", viewerGlobalAccess(), func(*Server) http.Handler {
			return http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls = append(calls, "handler") })
		}),
	}}
	server.registerAPIRouteCatalog(catalog, apiRouteMiddlewareStack{cors: wrap("cors"), log: wrap("log"), auth: wrap("auth")})
	server.mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/test-route", nil))

	want := []string{"cors", "log", "auth", "handler"}
	if len(calls) != len(want) {
		t.Fatalf("middleware calls = %v, want %v", calls, want)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatalf("middleware calls = %v, want %v", calls, want)
		}
	}
}

func TestAllSystemAPIRoutesUseModularCatalogs(t *testing.T) {
	const systemPrefix = "/api/v1/system/"

	modular := make(map[string]apiRouteSpec)
	for _, catalog := range modularAPIRouteCatalogs {
		for _, route := range catalog.Routes {
			if strings.HasPrefix(route.Path, systemPrefix) {
				modular[route.Method+" "+route.Path] = route
			}
		}
	}
	// 28 system routes, the 7 cluster-registry routes, 15 normalized compute
	// routes, and the 4 domain deployment-target routes.
	if got, want := len(modular), 55; got != want {
		t.Fatalf("modular system route count = %d, want %d", got, want)
	}

	discovered, err := discoverControllerRoutes("server.go")
	if err != nil {
		t.Fatalf("discoverControllerRoutes: %v", err)
	}
	discoveredSystem := make(map[string]discoveredRoute)
	for _, route := range discovered {
		if !strings.HasPrefix(route.Path, systemPrefix) {
			continue
		}
		key := route.Method + " " + route.Path
		discoveredSystem[key] = route
		spec, ok := modular[key]
		if !ok {
			t.Errorf("system route %s is registered outside a modular route catalog", key)
			continue
		}
		if route.Handler != spec.HandlerIdentity {
			t.Errorf("system route %s handler = %q, want %q", key, route.Handler, spec.HandlerIdentity)
		}
	}
	for key := range modular {
		if _, ok := discoveredSystem[key]; !ok {
			t.Errorf("modular system route %s was not discovered", key)
		}
	}

	for _, rule := range legacyRouteCatalogAuthRules {
		if strings.HasPrefix(rule.Path, systemPrefix) {
			t.Errorf("system route %s %s still has a legacy authorization rule", rule.Method, rule.Path)
		}
	}
}

func TestSystemRouteCatalogPreservesAccessContracts(t *testing.T) {
	viewerRoles := []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}
	systemAdminRoles := []string{RoleSystemAdmin}
	type accessContract struct {
		roles        []string
		scope        string
		maxBodyBytes int64
	}
	want := map[string]accessContract{
		http.MethodGet + " /api/v1/system/settings":                  {roles: viewerRoles, scope: "system:read"},
		http.MethodPut + " /api/v1/system/settings":                  {roles: systemAdminRoles, scope: "settings:manage"},
		http.MethodGet + " /api/v1/system/entitlements":              {roles: viewerRoles, scope: "system:read"},
		http.MethodGet + " /api/v1/system/entitlements/usage":        {roles: viewerRoles, scope: "system:read"},
		http.MethodGet + " /api/v1/system/inventory":                 {roles: viewerRoles, scope: "system:read"},
		http.MethodGet + " /api/v1/system/services":                  {roles: viewerRoles, scope: "system:read"},
		http.MethodGet + " /api/v1/system/usage":                     {roles: viewerRoles, scope: "system:read"},
		http.MethodGet + " /api/v1/system/usage/export":              {roles: viewerRoles, scope: "system:read"},
		http.MethodGet + " /api/v1/system/usage/snapshots":           {roles: viewerRoles, scope: "system:read"},
		http.MethodPost + " /api/v1/system/usage/snapshots":          {roles: systemAdminRoles, scope: "system:read"},
		http.MethodGet + " /api/v1/system/parity-status":             {roles: viewerRoles, scope: "system:read"},
		http.MethodGet + " /api/v1/system/browser-benchmarks":        {roles: viewerRoles, scope: "system:read"},
		http.MethodGet + " /api/v1/system/deployment-lease":          {roles: systemAdminRoles, scope: "system:read"},
		http.MethodPost + " /api/v1/system/deployment-lease/acquire": {roles: systemAdminRoles, scope: "deployments:manage", maxBodyBytes: deploymentLeaseMaxBody},
		http.MethodPost + " /api/v1/system/deployment-lease/renew":   {roles: systemAdminRoles, scope: "deployments:manage", maxBodyBytes: deploymentLeaseMaxBody},
		http.MethodPost + " /api/v1/system/deployment-lease/release": {roles: systemAdminRoles, scope: "deployments:manage", maxBodyBytes: deploymentLeaseMaxBody},
		http.MethodPatch + " /api/v1/system/generators/drain":        {roles: systemAdminRoles, scope: "settings:manage"},
		http.MethodGet + " /api/v1/system/restarts/capabilities":     {roles: systemAdminRoles, scope: "settings:manage"},
		http.MethodPost + " /api/v1/system/restarts":                 {roles: systemAdminRoles, scope: "settings:manage"},
	}

	for key, contract := range want {
		method, path, ok := strings.Cut(key, " ")
		if !ok {
			t.Fatalf("invalid test route key %q", key)
		}
		got, ok := accessRequirementFromRouteCatalog(method, path)
		if !ok {
			t.Errorf("authenticated system route %s is absent from the authorization catalog", key)
			continue
		}
		if strings.Join(got.GlobalRoles, ",") != strings.Join(contract.roles, ",") {
			t.Errorf("system route %s global roles = %v, want %v", key, got.GlobalRoles, contract.roles)
		}
		if got.ServiceAccountScope != contract.scope {
			t.Errorf("system route %s service-account scope = %q, want %q", key, got.ServiceAccountScope, contract.scope)
		}
		if got.MaxBodyBytes != contract.maxBodyBytes {
			t.Errorf("system route %s max body bytes = %d, want %d", key, got.MaxBodyBytes, contract.maxBodyBytes)
		}
		if got.AuthenticatedOnly || len(got.ProjectRoles) != 0 || got.ProjectLookup != nil || len(got.DomainRoles) != 0 || got.DomainLookup != nil || len(got.RepositoryRoles) != 0 || got.RepositoryLookup != nil || got.ServiceAccountScopeLookup != nil {
			t.Errorf("system route %s unexpectedly declares project, domain, repository, lookup, or authenticated-only access", key)
		}
	}

	publicRoutes := []string{
		"/api/v1/system/health",
		"/api/v1/system/version",
		"/api/v1/system/readiness",
		"/api/v1/system/execution-readiness",
	}
	for _, path := range publicRoutes {
		if _, ok := accessRequirementFromRouteCatalog(http.MethodGet, path); ok {
			t.Errorf("public system route GET %s unexpectedly has an authorization rule", path)
		}
	}
}

func TestCapacityAndFleetAPIRoutesUseModularCatalogs(t *testing.T) {
	prefixCounts := map[string]int{
		"/api/v1/capacity/": 9,
		// 22 fleet routes plus the pool allocation PUT and preflight GET.
		"/api/v1/fleet/": 24,
	}

	modular := make(map[string]apiRouteSpec)
	actualCounts := make(map[string]int)
	for _, catalog := range modularAPIRouteCatalogs {
		for _, route := range catalog.Routes {
			for prefix := range prefixCounts {
				if strings.HasPrefix(route.Path, prefix) {
					modular[route.Method+" "+route.Path] = route
					actualCounts[prefix]++
				}
			}
		}
	}
	for prefix, want := range prefixCounts {
		if got := actualCounts[prefix]; got != want {
			t.Errorf("modular %s route count = %d, want %d", prefix, got, want)
		}
	}

	discovered, err := discoverControllerRoutes("server.go")
	if err != nil {
		t.Fatalf("discoverControllerRoutes: %v", err)
	}
	discoveredOperational := make(map[string]discoveredRoute)
	for _, route := range discovered {
		matched := false
		for prefix := range prefixCounts {
			matched = matched || strings.HasPrefix(route.Path, prefix)
		}
		if !matched {
			continue
		}
		key := route.Method + " " + route.Path
		discoveredOperational[key] = route
		spec, ok := modular[key]
		if !ok {
			t.Errorf("capacity or fleet route %s is registered outside a modular route catalog", key)
			continue
		}
		if route.Handler != spec.HandlerIdentity {
			t.Errorf("capacity or fleet route %s handler = %q, want %q", key, route.Handler, spec.HandlerIdentity)
		}
		if !route.GenericAuth || route.Unauthenticated {
			t.Errorf("capacity or fleet route %s middleware discovery = genericAuth:%t unauthenticated:%t, want standard authenticated middleware", key, route.GenericAuth, route.Unauthenticated)
		}
	}
	for key := range modular {
		if _, ok := discoveredOperational[key]; !ok {
			t.Errorf("modular capacity or fleet route %s was not discovered", key)
		}
	}

	for _, rule := range legacyRouteCatalogAuthRules {
		for prefix := range prefixCounts {
			if strings.HasPrefix(rule.Path, prefix) {
				t.Errorf("capacity or fleet route %s %s still has a legacy authorization rule", rule.Method, rule.Path)
			}
		}
	}
}

func TestCapacityAndFleetRouteCatalogPreservesAccessContracts(t *testing.T) {
	viewerRoles := []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}
	viewerProjectRoles := []string{RoleViewer, RoleEditor, RoleProjectAdmin}
	type routeAccessCase struct {
		method      string
		path        string
		requirement accessRequirement
		scope       string
	}
	cases := []routeAccessCase{
		{http.MethodPost, "/api/v1/capacity/timeslots", editorImportAccess(resolveTimeslotCreateProjectID, 65536), "tests:run"},
		{http.MethodGet, "/api/v1/capacity/timeslots", viewerProjectAccess(resolveProjectIDFromQuery), "results:read"},
		{http.MethodPost, "/api/v1/capacity/timeslots/availability", accessRequirement{GlobalRoles: viewerRoles, ProjectRoles: viewerProjectRoles, ProjectLookup: resolveTimeslotCreateProjectID, MaxBodyBytes: 65536}, "tests:run"},
		{http.MethodGet, "/api/v1/capacity/timeslots/{id}", viewerProjectAccess(resolveTimeslotProjectID), "results:read"},
		{http.MethodPut, "/api/v1/capacity/timeslots/{id}", editorImportAccess(resolveTimeslotProjectID, 65536), "tests:run"},
		{http.MethodDelete, "/api/v1/capacity/timeslots/{id}", projectAdminAccess(resolveTimeslotProjectID), "tests:run"},
		{http.MethodPost, "/api/v1/capacity/timeslots/{id}/approve", projectAdminAccess(resolveTimeslotProjectID), "tests:run"},
		{http.MethodPost, "/api/v1/capacity/timeslots/{id}/reject", projectAdminAccess(resolveTimeslotProjectID), "tests:run"},
		{http.MethodPost, "/api/v1/capacity/timeslots/{id}/bind", editorImportAccess(resolveTimeslotProjectID, 65536), "tests:run"},
		{http.MethodGet, "/api/v1/fleet/alerts", viewerGlobalAccess(), "read"},
		{http.MethodPatch, "/api/v1/fleet/alerts/{id}/ack", accessRequirement{GlobalRoles: []string{RoleSystemAdmin}, MaxBodyBytes: 65536}, "write"},
		{http.MethodGet, "/api/v1/fleet/capacity", unrestrictedViewerGlobalAccess(), "read"},
		{http.MethodGet, "/api/v1/fleet/load-zones", unrestrictedViewerGlobalAccess(), "read"},
		{http.MethodGet, "/api/v1/fleet/provisioner", viewerGlobalAccess(), "read"},
		{http.MethodPost, "/api/v1/fleet/pools", accessRequirement{GlobalRoles: []string{RoleSystemAdmin}, MaxBodyBytes: 1048576}, "write"},
		{http.MethodGet, "/api/v1/fleet/pools", systemAdminAccess(), "read"},
		{http.MethodGet, "/api/v1/fleet/pools/{id}", systemAdminAccess(), "read"},
		{http.MethodPut, "/api/v1/fleet/pools/{id}", accessRequirement{GlobalRoles: []string{RoleSystemAdmin}, MaxBodyBytes: 1048576}, "write"},
		{http.MethodDelete, "/api/v1/fleet/pools/{id}", systemAdminAccess(), "write"},
		{http.MethodGet, "/api/v1/fleet/generators", systemAdminAccess(), "read"},
		{http.MethodPatch, "/api/v1/fleet/generators/{id}", accessRequirement{GlobalRoles: []string{RoleSystemAdmin}, MaxBodyBytes: 262144}, "write"},
		{http.MethodPost, "/api/v1/fleet/generators/{id}/cordon", accessRequirement{GlobalRoles: []string{RoleSystemAdmin}, MaxBodyBytes: 65536}, "write"},
		{http.MethodPost, "/api/v1/fleet/generators/{id}/uncordon", accessRequirement{GlobalRoles: []string{RoleSystemAdmin}, MaxBodyBytes: 65536}, "write"},
		{http.MethodPost, "/api/v1/fleet/generators/{id}/drain", accessRequirement{GlobalRoles: []string{RoleSystemAdmin}, MaxBodyBytes: 65536}, "write"},
		{http.MethodPost, "/api/v1/fleet/generators/{id}/evacuate", accessRequirement{GlobalRoles: []string{RoleSystemAdmin}, MaxBodyBytes: 65536}, "write"},
		{http.MethodPost, "/api/v1/fleet/provision-requests", accessRequirement{GlobalRoles: []string{RoleSystemAdmin}, MaxBodyBytes: 262144}, "write"},
		{http.MethodGet, "/api/v1/fleet/provision-requests", systemAdminAccess(), "read"},
		{http.MethodGet, "/api/v1/fleet/provision-requests/{id}", systemAdminAccess(), "read"},
		{http.MethodPost, "/api/v1/fleet/provision-requests/{id}/cancel", systemAdminAccess(), "write"},
		{http.MethodPost, "/api/v1/fleet/provision-requests/{id}/retry", systemAdminAccess(), "write"},
	}

	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			got, ok := accessRequirementFromRouteCatalog(tc.method, tc.path)
			if !ok {
				t.Fatal("route is absent from the authorization catalog")
			}
			want := tc.requirement
			want.ServiceAccountScope = tc.scope
			assertAccessRequirementEqual(t, got, want)
		})
	}
}

func TestArchiveAPIRoutesUseModularCatalogs(t *testing.T) {
	isArchiveRoute := func(path string) bool {
		return strings.HasPrefix(path, "/api/v1/archives/") ||
			path == "/api/v1/tests/archive/reports" ||
			(strings.HasPrefix(path, "/api/v1/tests/{id}/archive") &&
				(path == "/api/v1/tests/{id}/archive" || strings.HasPrefix(path, "/api/v1/tests/{id}/archive/")))
	}

	modular := make(map[string]apiRouteSpec)
	for _, catalog := range modularAPIRouteCatalogs {
		for _, route := range catalog.Routes {
			if isArchiveRoute(route.Path) {
				modular[route.Method+" "+route.Path] = route
			}
		}
	}
	if got, want := len(modular), 17; got != want {
		t.Fatalf("modular archive route count = %d, want %d", got, want)
	}

	discovered, err := discoverControllerRoutes("server.go")
	if err != nil {
		t.Fatalf("discoverControllerRoutes: %v", err)
	}
	discoveredArchives := make(map[string]discoveredRoute)
	for _, route := range discovered {
		if !isArchiveRoute(route.Path) {
			continue
		}
		key := route.Method + " " + route.Path
		discoveredArchives[key] = route
		spec, ok := modular[key]
		if !ok {
			t.Errorf("archive route %s is registered outside a modular route catalog", key)
			continue
		}
		if route.Handler != spec.HandlerIdentity {
			t.Errorf("archive route %s handler = %q, want %q", key, route.Handler, spec.HandlerIdentity)
		}
		if !route.GenericAuth || route.Unauthenticated {
			t.Errorf("archive route %s middleware discovery = genericAuth:%t unauthenticated:%t, want standard authenticated middleware", key, route.GenericAuth, route.Unauthenticated)
		}
	}
	for key := range modular {
		if _, ok := discoveredArchives[key]; !ok {
			t.Errorf("modular archive route %s was not discovered", key)
		}
	}

	for _, rule := range legacyRouteCatalogAuthRules {
		if isArchiveRoute(rule.Path) {
			t.Errorf("archive route %s %s still has a legacy authorization rule", rule.Method, rule.Path)
		}
	}
}

func TestArchiveRouteCatalogPreservesAccessContracts(t *testing.T) {
	readPaths := []string{
		"/api/v1/tests/{id}/archive",
		"/api/v1/tests/{id}/archive/manifest",
		"/api/v1/tests/{id}/archive/verify",
		"/api/v1/tests/{id}/archive/files/{name}",
		"/api/v1/tests/{id}/archive/browser-diagnostics",
		"/api/v1/tests/{id}/archive/transactions",
		"/api/v1/tests/{id}/archive/resources",
		"/api/v1/tests/{id}/archive/summaries",
		"/api/v1/tests/{id}/archive/report",
		"/api/v1/tests/{id}/archive/analysis",
		"/api/v1/tests/{id}/archive/root-cause",
		"/api/v1/tests/{id}/archive/ai-root-cause",
		"/api/v1/tests/{id}/archive/comparison",
	}
	for _, path := range readPaths {
		t.Run(http.MethodGet+" "+path, func(t *testing.T) {
			got, ok := accessRequirementFromRouteCatalog(http.MethodGet, path)
			if !ok {
				t.Fatal("route is absent from the authorization catalog")
			}
			want := viewerProjectAccess(resolveTestProjectID)
			want.ServiceAccountScope = "results:read"
			assertAccessRequirementEqual(t, got, want)
			documented, ok := documentedAccessRequirementFromAPIRouteCatalogs(http.MethodGet, path)
			if !ok {
				t.Fatal("route is absent from the documentation access catalog")
			}
			assertAccessRequirementEqual(t, documented, viewerProjectAccess(resolveTestProjectID))

			concrete := strings.NewReplacer("{id}", "run-1", "{name}", "artifact.json").Replace(path)
			req := httptest.NewRequest(http.MethodGet, concrete, nil)
			req.SetPathValue("id", "run-1")
			req.SetPathValue("name", "artifact.json")
			if scope := requiredServiceAccountScope(req); scope != "results:read" {
				t.Errorf("effective service-account scope = %q, want %q", scope, "results:read")
			}
		})
	}

	batch, ok := accessRequirementFromRouteCatalog(http.MethodPost, "/api/v1/tests/archive/reports")
	if !ok {
		t.Fatal("batch archive-report route is absent from the authorization catalog")
	}
	wantBatch := accessRequirement{
		GlobalRoles:         []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
		ProjectRoles:        []string{RoleViewer, RoleEditor, RoleProjectAdmin},
		ProjectLookup:       resolveProjectIDFromQuery,
		ServiceAccountScope: "tests:run",
		MaxBodyBytes:        65536,
	}
	assertAccessRequirementEqual(t, batch, wantBatch)

	for _, path := range []string{"/api/v1/archives/retention/plan", "/api/v1/archives/retention/apply"} {
		t.Run(http.MethodPost+" "+path, func(t *testing.T) {
			got, ok := accessRequirementFromRouteCatalog(http.MethodPost, path)
			if !ok {
				t.Fatal("route is absent from the authorization catalog")
			}
			want := systemAdminAccess()
			want.ServiceAccountScope = "write"
			assertAccessRequirementEqual(t, got, want)
		})
	}
}

func assertAccessRequirementEqual(t *testing.T, got, want accessRequirement) {
	t.Helper()
	if got.AuthenticatedOnly != want.AuthenticatedOnly {
		t.Errorf("AuthenticatedOnly = %t, want %t", got.AuthenticatedOnly, want.AuthenticatedOnly)
	}
	if !reflect.DeepEqual(got.GlobalRoles, want.GlobalRoles) {
		t.Errorf("GlobalRoles = %v, want %v", got.GlobalRoles, want.GlobalRoles)
	}
	if !reflect.DeepEqual(got.ProjectRoles, want.ProjectRoles) {
		t.Errorf("ProjectRoles = %v, want %v", got.ProjectRoles, want.ProjectRoles)
	}
	if !reflect.DeepEqual(got.DomainRoles, want.DomainRoles) {
		t.Errorf("DomainRoles = %v, want %v", got.DomainRoles, want.DomainRoles)
	}
	if !reflect.DeepEqual(got.RepositoryRoles, want.RepositoryRoles) {
		t.Errorf("RepositoryRoles = %v, want %v", got.RepositoryRoles, want.RepositoryRoles)
	}
	if functionPointer(got.ProjectLookup) != functionPointer(want.ProjectLookup) {
		t.Error("ProjectLookup changed")
	}
	if functionPointer(got.DomainLookup) != functionPointer(want.DomainLookup) {
		t.Error("DomainLookup changed")
	}
	if functionPointer(got.RepositoryLookup) != functionPointer(want.RepositoryLookup) {
		t.Error("RepositoryLookup changed")
	}
	if got.ServiceAccountScope != want.ServiceAccountScope {
		t.Errorf("ServiceAccountScope = %q, want %q", got.ServiceAccountScope, want.ServiceAccountScope)
	}
	if functionPointer(got.ServiceAccountScopeLookup) != functionPointer(want.ServiceAccountScopeLookup) {
		t.Error("ServiceAccountScopeLookup changed")
	}
	if got.MaxBodyBytes != want.MaxBodyBytes {
		t.Errorf("MaxBodyBytes = %d, want %d", got.MaxBodyBytes, want.MaxBodyBytes)
	}
}

func functionPointer(fn any) uintptr {
	value := reflect.ValueOf(fn)
	if !value.IsValid() || value.IsNil() {
		return 0
	}
	return value.Pointer()
}
