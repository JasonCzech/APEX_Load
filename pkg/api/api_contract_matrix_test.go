package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

const qaAPIContractMatrixPath = "../../docs/qa/evidence/M1/api-contract-matrix.json"

type qaAPIContractMatrix struct {
	SchemaVersion int                  `json:"schema_version"`
	Catalog       qaAPIContractCatalog `json:"catalog"`
	Totals        qaAPIContractTotals  `json:"totals"`
	Routes        []qaAPIContractRoute `json:"routes"`
}

type qaAPIContractCatalog struct {
	Version int    `json:"version"`
	Source  string `json:"source"`
}

type qaAPIContractTotals struct {
	Routes               int `json:"routes"`
	Public               int `json:"public"`
	Protected            int `json:"protected"`
	RBAC                 int `json:"rbac"`
	HandlerAuthenticated int `json:"handler_authenticated"`
	RoleMetadataMissing  int `json:"role_metadata_missing"`
	Green                int `json:"green"`
	Partial              int `json:"partial"`
	Missing              int `json:"missing"`
}

type qaAPIContractRoute struct {
	Method              string                `json:"method"`
	Path                string                `json:"path"`
	OperationID         string                `json:"operation_id"`
	Public              bool                  `json:"public"`
	Authenticated       bool                  `json:"authenticated"`
	State               string                `json:"state"`
	Enforcement         string                `json:"enforcement"`
	Access              string                `json:"access"`
	AnonymousAssertion  string                `json:"anonymous_assertion"`
	RoleAssertion       string                `json:"role_assertion"`
	AllowedRoles        qaAPIContractRoleSets `json:"allowed_roles"`
	DeniedRole          string                `json:"denied_role,omitempty"`
	ServiceAccountScope string                `json:"service_account_scope,omitempty"`
	Conditions          []string              `json:"conditions,omitempty"`
}

type qaAPIContractRoleSets struct {
	Global     []string `json:"global,omitempty"`
	Project    []string `json:"project,omitempty"`
	Domain     []string `json:"domain,omitempty"`
	Repository []string `json:"repository,omitempty"`
}

func TestGeneratedAPIContractMatrixIsFresh(t *testing.T) {
	got := buildQAAPIContractMatrix(t)
	want, err := os.ReadFile(qaAPIContractMatrixPath)
	if err != nil {
		t.Fatalf("read %s: %v", qaAPIContractMatrixPath, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s is stale; run `APEXLOAD_UPDATE_QA_API_CONTRACT_MATRIX=1 go test ./pkg/api -run TestGeneratedAPIContractMatrixIsFresh`", qaAPIContractMatrixPath)
	}
}

func TestAPIContractMatrixAuthAssertions(t *testing.T) {
	discovered, err := discoverControllerRoutes("server.go")
	if err != nil {
		t.Fatal(err)
	}

	settings := func() AuthSettings {
		return AuthSettings{Mode: authModeTrustedHeaders}
	}
	contractServer := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	contractSettings := contractServer.runner.GetSystemSettings()
	contractSettings.Auth.Mode = authModeTrustedHeaders
	if _, err := contractServer.runner.UpdateSystemSettings(contractSettings); err != nil {
		t.Fatal(err)
	}
	for _, route := range discovered {
		if !route.Authenticated {
			continue
		}
		route := route
		t.Run(route.Method+" "+route.Path, func(t *testing.T) {
			req := qaContractRequest(t, route)
			if route.GenericAuth {
				recorder := httptest.NewRecorder()
				reached := false
				handler := authMiddleware(settings, nil, nil)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
					reached = true
				}))
				handler.ServeHTTP(recorder, req)
				if recorder.Code != http.StatusUnauthorized || reached {
					t.Fatalf("anonymous auth boundary = (%d, reached=%v), want (401, false); body=%s", recorder.Code, reached, recorder.Body.String())
				}
			} else if route.AuthMiddleware != "" {
				recorder := httptest.NewRecorder()
				contractServer.mux.ServeHTTP(recorder, req)
				if recorder.Code != http.StatusUnauthorized {
					t.Fatalf("anonymous specialized auth boundary = %d, want 401; body=%s", recorder.Code, recorder.Body.String())
				}
			}

			requirement, ok := accessRequirementForRequest(req)
			if !ok {
				requirement, ok = documentedSpecializedAccessRequirement(route.Method, route.Path, route.AuthMiddleware)
			}
			if ok {
				assertQAContractRoleCases(t, req, requirement)
			}
			if conditionalReq, conditionalRequirement, ok := qaContractProjectVariant(req, requirement); ok {
				assertQAContractRoleCases(t, conditionalReq, conditionalRequirement)
			}
		})
	}
}

func TestAPIContractHandlerSpecificAuthAssertions(t *testing.T) {
	t.Run("authentication diagnostics role gate", func(t *testing.T) {
		server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
		settings := server.runner.GetSystemSettings()
		settings.Auth.Mode = authModeTrustedHeaders
		if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
			t.Fatal(err)
		}
		anonymousResponse := httptest.NewRecorder()
		server.mux.ServeHTTP(anonymousResponse, httptest.NewRequest(http.MethodGet, "/api/v1/auth/diagnostics", nil))
		if anonymousResponse.Code != http.StatusUnauthorized {
			t.Fatalf("auth diagnostics without identity = %d, want 401", anonymousResponse.Code)
		}
		viewerRequest := httptest.NewRequest(http.MethodGet, "/api/v1/auth/diagnostics", nil)
		viewerRequest.Header.Set("X-Auth-Request-User", "qa-contract-viewer")
		viewerRequest.Header.Set("X-APEXLoad-Roles", RoleViewer)
		viewerResponse := httptest.NewRecorder()
		server.mux.ServeHTTP(viewerResponse, viewerRequest)
		if viewerResponse.Code != http.StatusForbidden {
			t.Fatalf("auth diagnostics with viewer role = %d, want 403", viewerResponse.Code)
		}
		adminRequest := httptest.NewRequest(http.MethodGet, "/api/v1/auth/diagnostics", nil)
		adminRequest.Header.Set("X-Auth-Request-User", "qa-contract-admin")
		adminRequest.Header.Set("X-APEXLoad-Roles", RoleSystemAdmin)
		adminResponse := httptest.NewRecorder()
		server.mux.ServeHTTP(adminResponse, adminRequest)
		if adminResponse.Code != http.StatusOK {
			t.Fatalf("auth diagnostics with system-admin role = %d, want 200; body=%s", adminResponse.Code, adminResponse.Body.String())
		}
	})

	t.Run("conditional metrics bearer token", func(t *testing.T) {
		publicServer := NewServer(ServerConfig{})
		publicResponse := httptest.NewRecorder()
		publicServer.mux.ServeHTTP(publicResponse, httptest.NewRequest(http.MethodGet, "/metrics", nil))
		if publicResponse.Code != http.StatusOK {
			t.Fatalf("metrics without configured token = %d, want 200", publicResponse.Code)
		}

		protectedServer := NewServer(ServerConfig{MetricsToken: "qa-contract-metrics"})
		anonymousResponse := httptest.NewRecorder()
		protectedServer.mux.ServeHTTP(anonymousResponse, httptest.NewRequest(http.MethodGet, "/metrics", nil))
		if anonymousResponse.Code != http.StatusUnauthorized {
			t.Fatalf("protected metrics without bearer token = %d, want 401", anonymousResponse.Code)
		}
		authorizedRequest := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		authorizedRequest.Header.Set("Authorization", "Bearer qa-contract-metrics")
		authorizedResponse := httptest.NewRecorder()
		protectedServer.mux.ServeHTTP(authorizedResponse, authorizedRequest)
		if authorizedResponse.Code != http.StatusOK {
			t.Fatalf("protected metrics with bearer token = %d, want 200", authorizedResponse.Code)
		}
	})

	t.Run("recording bridge token and session", func(t *testing.T) {
		server := newBrowserRecorderBridgeTestServer(t, newFakeBrowserRecorder(nil))
		sessionID := pairBridgeForTest(t, server, bridgeTestOrigin)

		anonymousRequest := httptest.NewRequest(http.MethodGet, bridgeStatusPath, nil)
		anonymousRequest.Header.Set("Origin", bridgeTestOrigin)
		anonymousResponse := httptest.NewRecorder()
		server.mux.ServeHTTP(anonymousResponse, anonymousRequest)
		if anonymousResponse.Code != http.StatusUnauthorized {
			t.Fatalf("bridge status without capability headers = %d, want 401", anonymousResponse.Code)
		}

		authorizedRequest := httptest.NewRequest(http.MethodGet, bridgeStatusPath, nil)
		authorizedRequest.Header.Set("Origin", bridgeTestOrigin)
		authorizedRequest.Header.Set(bridgeTokenHeader, bridgeTestToken)
		authorizedRequest.Header.Set(bridgeSessionHeader, sessionID)
		authorizedResponse := httptest.NewRecorder()
		server.mux.ServeHTTP(authorizedResponse, authorizedRequest)
		if authorizedResponse.Code != http.StatusOK {
			t.Fatalf("bridge status with capability headers = %d, want 200; body=%s", authorizedResponse.Code, authorizedResponse.Body.String())
		}
	})

	t.Run("monitoring collector bearer token", func(t *testing.T) {
		server, _ := newTimeslotServer(t)
		create := timeslotRequest(t, server, http.MethodPost, "/api/v1/monitoring/collectors",
			map[string]any{"project_id": "proj-a", "name": "qa-contract", "allowed_metric_packs": []string{"linux-hosts"}})
		if create.Code != http.StatusCreated {
			t.Fatalf("create collector = %d, want 201; body=%s", create.Code, create.Body.String())
		}
		var created struct {
			Collector storage.MonitoringCollector `json:"collector"`
			Token     string                      `json:"token"`
		}
		if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
			t.Fatal(err)
		}
		heartbeat := map[string]any{"health": "healthy", "packs": []map[string]any{{"pack": "linux-hosts", "samples": map[string]float64{"cpu_pct": 1}}}}
		path := "/api/v1/monitoring/collectors/" + created.Collector.ID + "/heartbeat"
		if response := collectorRequest(t, server, http.MethodPost, path, "", heartbeat); response.Code != http.StatusUnauthorized {
			t.Fatalf("collector heartbeat without token = %d, want 401", response.Code)
		}
		if response := collectorRequest(t, server, http.MethodPost, path, created.Token, heartbeat); response.Code != http.StatusOK {
			t.Fatalf("collector heartbeat with token = %d, want 200; body=%s", response.Code, response.Body.String())
		}
	})
}

func assertQAContractRoleCases(t *testing.T, req *http.Request, requirement accessRequirement) {
	t.Helper()
	const scopeID = "qa-contract-scope"
	resolved := requirement
	if resolved.ProjectLookup != nil {
		resolved.ProjectLookup = func(*http.Request, storage.Store) (string, int, string) { return scopeID, 0, "" }
	}
	if resolved.DomainLookup != nil {
		resolved.DomainLookup = func(*http.Request, storage.Store) (string, int, string) { return scopeID, 0, "" }
	}
	if resolved.RepositoryLookup != nil {
		resolved.RepositoryLookup = func(*http.Request, storage.Store) (string, int, string) { return scopeID, 0, "" }
	}
	resolved.ServiceAccountScope = ""
	resolved.ServiceAccountScopeLookup = nil

	assertAllowed := func(name string, user *AuthenticatedUser) {
		t.Helper()
		recorder := httptest.NewRecorder()
		if !authorizeAccessRequirement(recorder, req.Clone(req.Context()), nil, authResolution{User: user}, resolved) {
			t.Errorf("%s allowed role was denied: status=%d body=%s", name, recorder.Code, recorder.Body.String())
		}
	}
	for _, role := range requirement.GlobalRoles {
		assertAllowed("global "+role, qaContractUser(map[string]struct{}{role: {}}, nil, nil, nil))
	}
	for _, role := range requirement.ProjectRoles {
		assertAllowed("project "+role, qaContractUser(nil, map[string]map[string]struct{}{scopeID: {role: {}}}, nil, nil))
	}
	for _, role := range requirement.DomainRoles {
		assertAllowed("domain "+role, qaContractUser(nil, nil, map[string]map[string]struct{}{scopeID: {role: {}}}, nil))
	}
	for _, role := range requirement.RepositoryRoles {
		assertAllowed("repository "+role, qaContractUser(nil, nil, nil, map[string]map[string]struct{}{scopeID: {role: {}}}))
	}

	if requirement.AuthenticatedOnly {
		assertAllowed("authenticated identity", qaContractUser(map[string]struct{}{}, nil, nil, nil))
		return
	}
	recorder := httptest.NewRecorder()
	denied := qaContractUser(map[string]struct{}{qaContractDeniedRole: {}}, map[string]map[string]struct{}{scopeID: {qaContractDeniedRole: {}}}, map[string]map[string]struct{}{scopeID: {qaContractDeniedRole: {}}}, map[string]map[string]struct{}{scopeID: {qaContractDeniedRole: {}}})
	if authorizeAccessRequirement(recorder, req.Clone(req.Context()), nil, authResolution{User: denied}, resolved) {
		t.Fatal("unlisted role was authorized")
	}
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("unlisted role status = %d, want 403; body=%s", recorder.Code, recorder.Body.String())
	}
}

const qaContractDeniedRole = "qa_contract_unlisted"

func qaContractUser(global map[string]struct{}, project, domain, repository map[string]map[string]struct{}) *AuthenticatedUser {
	if global == nil {
		global = map[string]struct{}{}
	}
	if project == nil {
		project = map[string]map[string]struct{}{}
	}
	if domain == nil {
		domain = map[string]map[string]struct{}{}
	}
	if repository == nil {
		repository = map[string]map[string]struct{}{}
	}
	return &AuthenticatedUser{Subject: "qa-contract", GlobalRoles: global, ProjectRoles: project, DomainRoles: domain, RepositoryRoles: repository}
}

func buildQAAPIContractMatrix(t *testing.T) []byte {
	t.Helper()
	catalogJSON, err := GenerateEndpointCatalogJSON("server.go")
	if err != nil {
		t.Fatal(err)
	}
	var catalog endpointCatalog
	if err := json.Unmarshal(catalogJSON, &catalog); err != nil {
		t.Fatal(err)
	}
	discovered, err := discoverControllerRoutes("server.go")
	if err != nil {
		t.Fatal(err)
	}
	discoveredByKey := make(map[string]discoveredRoute, len(discovered))
	for _, route := range discovered {
		discoveredByKey[route.Method+" "+route.Path] = route
	}

	matrix := qaAPIContractMatrix{SchemaVersion: 1, Catalog: qaAPIContractCatalog{Version: catalog.Version, Source: catalog.Source}}
	for _, documented := range catalog.Routes {
		route, ok := discoveredByKey[documented.Method+" "+documented.Path]
		if !ok {
			t.Fatalf("catalog route %s %s was not discovered", documented.Method, documented.Path)
		}
		discoveredPublic := route.Unauthenticated && !inHandlerAuthenticatedRoute(route.Method, route.Path, route.Handler)
		if documented.Public != discoveredPublic || documented.Authenticated != route.Authenticated {
			t.Fatalf("catalog auth flags for %s %s = (public=%v, authenticated=%v), discovered (public=%v, authenticated=%v)", documented.Method, documented.Path, documented.Public, documented.Authenticated, discoveredPublic, route.Authenticated)
		}
		row := qaAPIContractRoute{
			Method: documented.Method, Path: documented.Path, OperationID: documented.OperationID,
			Public: documented.Public, Authenticated: documented.Authenticated, Access: documented.Access,
			AllowedRoles: qaAPIContractRoleSets{},
		}
		matrix.Totals.Routes++
		switch {
		case documented.Public:
			matrix.Totals.Public++
			if route.Path == "/metrics" {
				matrix.Totals.Green++
				row.State = "GREEN"
				row.Enforcement = "conditional handler token"
				row.AnonymousAssertion = "HTTP 200 without token configuration; HTTP 401 when configured and missing; HTTP 200 with valid bearer token"
				row.RoleAssertion = "not applicable; route uses a metrics bearer token rather than RBAC"
			} else {
				matrix.Totals.Green++
				row.State = "GREEN"
				row.Enforcement = "public"
				row.AnonymousAssertion = "route is registered without HTTP auth middleware"
				row.RoleAssertion = "not applicable to public route"
			}
		case documented.Authenticated:
			matrix.Totals.Protected++
			req := qaContractRequest(t, route)
			requirement, ok := accessRequirementForRequest(req)
			specializedProfile := false
			if !ok {
				requirement, ok = documentedSpecializedAccessRequirement(route.Method, route.Path, route.AuthMiddleware)
				specializedProfile = ok
			}
			if route.GenericAuth {
				row.AnonymousAssertion = "HTTP 401 before handler"
			} else if route.AuthMiddleware == "" {
				row.AnonymousAssertion = "handler performs authentication and authorization"
			} else {
				row.AnonymousAssertion = "route is registered with handler-specific auth middleware"
			}
			if !ok {
				if inHandlerAuthenticatedRoute(route.Method, route.Path, route.Handler) || route.Path == bridgeStatusPath {
					matrix.Totals.HandlerAuthenticated++
					matrix.Totals.Green++
					row.State = "GREEN"
					row.Enforcement = "handler-specific capability authentication"
					if route.Path == bridgeStatusPath {
						row.AnonymousAssertion = "HTTP 401 without bridge token/session; HTTP 200 with both valid headers"
						row.RoleAssertion = "not applicable; route authenticates bridge token and session capabilities rather than an RBAC role"
					} else {
						row.AnonymousAssertion = "HTTP 401 without collector token; HTTP 200 with valid collector bearer token"
						row.RoleAssertion = "not applicable; route authenticates a collector capability rather than an RBAC role"
					}
				} else {
					matrix.Totals.RoleMetadataMissing++
					matrix.Totals.Missing++
					row.State = "MISSING"
					row.Enforcement = "resource-scoped authentication middleware"
					row.RoleAssertion = "MISSING: endpoint catalog has no allowed or denied role metadata"
				}
				break
			}
			matrix.Totals.RBAC++
			matrix.Totals.Green++
			row.State = "GREEN"
			if specializedProfile {
				row.Enforcement = "specialized resource authentication middleware and shared access profile"
			} else {
				row.Enforcement = "authentication middleware and access catalog"
			}
			row.AllowedRoles = qaAPIContractRoleSets{
				Global: sortedCopy(requirement.GlobalRoles), Project: sortedCopy(requirement.ProjectRoles),
				Domain: sortedCopy(requirement.DomainRoles), Repository: sortedCopy(requirement.RepositoryRoles),
			}
			if _, conditionalRequirement, ok := qaContractProjectVariant(req, requirement); ok {
				row.AllowedRoles.Global = sortedUnion(row.AllowedRoles.Global, conditionalRequirement.GlobalRoles)
				row.AllowedRoles.Project = sortedUnion(row.AllowedRoles.Project, conditionalRequirement.ProjectRoles)
				row.AllowedRoles.Domain = sortedUnion(row.AllowedRoles.Domain, conditionalRequirement.DomainRoles)
				row.AllowedRoles.Repository = sortedUnion(row.AllowedRoles.Repository, conditionalRequirement.RepositoryRoles)
				row.Conditions = append(row.Conditions, "project_id supplied")
			}
			if requirement.AuthenticatedOnly {
				row.RoleAssertion = "any authenticated identity allowed"
			} else {
				row.RoleAssertion = "each listed role allowed; unlisted role denied with HTTP 403"
				row.DeniedRole = qaContractDeniedRole
			}
			row.ServiceAccountScope = requiredServiceAccountScope(req)
		default:
			t.Fatalf("route %s %s is neither public nor authenticated", documented.Method, documented.Path)
		}
		matrix.Routes = append(matrix.Routes, row)
	}
	if len(discoveredByKey) != len(matrix.Routes) {
		t.Fatalf("discovered route count %d differs from catalog route count %d", len(discoveredByKey), len(matrix.Routes))
	}
	if classified := matrix.Totals.Green + matrix.Totals.Partial + matrix.Totals.Missing; classified != matrix.Totals.Routes {
		t.Fatalf("contract state total %d differs from route total %d", classified, matrix.Totals.Routes)
	}

	data, err := json.MarshalIndent(matrix, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if os.Getenv("APEXLOAD_UPDATE_QA_API_CONTRACT_MATRIX") == "1" {
		if err := os.WriteFile(qaAPIContractMatrixPath, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return data
}

func qaContractRequest(t *testing.T, route discoveredRoute) *http.Request {
	t.Helper()
	samplePath, pathValues := sampleRegisteredRoutePath(route.Path)
	req := httptest.NewRequest(route.Method, samplePath, nil)
	for name, value := range pathValues {
		req.SetPathValue(name, value)
	}
	return req
}

func qaContractProjectVariant(req *http.Request, base accessRequirement) (*http.Request, accessRequirement, bool) {
	if req.Method != http.MethodGet || base.ProjectLookup != nil {
		return nil, accessRequirement{}, false
	}
	variant := req.Clone(req.Context())
	query := variant.URL.Query()
	query.Set("project_id", "qa-contract-scope")
	variant.URL.RawQuery = query.Encode()
	requirement, ok := accessRequirementForRequest(variant)
	if !ok || requirement.ProjectLookup == nil || len(requirement.ProjectRoles) == 0 {
		return nil, accessRequirement{}, false
	}
	return variant, requirement, true
}

func sortedCopy(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}

func sortedUnion(left, right []string) []string {
	seen := make(map[string]struct{}, len(left)+len(right))
	for _, value := range left {
		seen[value] = struct{}{}
	}
	for _, value := range right {
		seen[value] = struct{}{}
	}
	result := make([]string, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
