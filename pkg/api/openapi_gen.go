package api

// OpenAPI/Swagger generation. docs/swagger.yaml is generated from the controller
// routing layer (pkg/api/server.go) by cmd/swaggergen — do NOT hand-edit it.
// Method/path/handler are extracted from the s.mux.Handle(...) registrations via
// go/ast; the required roles are resolved by reusing the real runtime authorization
// logic (accessRequirementForRequest), so the spec cannot drift from enforcement.
//
//go:generate go run ../../cmd/swaggergen -server server.go -out ../../docs/swagger.yaml -catalog-out ../../docs/api-endpoint-catalog.json -embed-out apidocs/swagger.yaml -catalog-embed-out apidocs/api-endpoint-catalog.json

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	yaml "gopkg.in/yaml.v3"
)

const openapiInfoDescription = "Kubernetes-native performance testing platform REST API.\n\n" +
	"The route inventory, path parameters, deprecation state, and access requirements are " +
	"generated from the controller routing layer (`pkg/api/server.go`) by `cmd/swaggergen`. " +
	"Handler analysis adds query parameter names and types, JSON/multipart/raw request media " +
	"with serializable top-level shapes, declared response statuses, and response media. " +
	"Semantic validation rules and some dynamic representations remain handler-defined. Use the " +
	"API User Guide for workflow examples and the generated Endpoint Map for a compact route " +
	"inventory. Do not hand-edit this file — run `go generate ./pkg/api` after changing routes."

var pathParamRe = regexp.MustCompile(`\{([^}]+)\}`)

var httpRouteMethods = map[string]bool{
	http.MethodGet: true, http.MethodPost: true, http.MethodPut: true,
	http.MethodPatch: true, http.MethodDelete: true, http.MethodHead: true,
	http.MethodOptions: true,
}

// --- OpenAPI document model (typed structs give stable, ordered field emission) ---

type oasDoc struct {
	OpenAPI    string                             `yaml:"openapi"`
	Info       oasInfo                            `yaml:"info"`
	Servers    []oasServer                        `yaml:"servers"`
	Components oasComponents                      `yaml:"components"`
	Security   []map[string][]string              `yaml:"security"`
	Paths      map[string]map[string]oasOperation `yaml:"paths"`
}

type oasInfo struct {
	Title       string `yaml:"title"`
	Description string `yaml:"description"`
	Version     string `yaml:"version"`
}

type oasServer struct {
	URL         string `yaml:"url"`
	Description string `yaml:"description"`
}

type oasComponents struct {
	SecuritySchemes map[string]oasSecurityScheme `yaml:"securitySchemes"`
	Schemas         map[string]oasSchema         `yaml:"schemas,omitempty"`
}

type oasSecurityScheme struct {
	Type         string `yaml:"type"`
	Scheme       string `yaml:"scheme,omitempty"`
	BearerFormat string `yaml:"bearerFormat,omitempty"`
	Name         string `yaml:"name,omitempty"`
	In           string `yaml:"in,omitempty"`
}

type oasOperation struct {
	Summary     string                 `yaml:"summary"`
	Description string                 `yaml:"description"`
	OperationID string                 `yaml:"operationId"`
	Deprecated  bool                   `yaml:"deprecated,omitempty"`
	RequestBody *oasRequestBody        `yaml:"requestBody,omitempty"`
	Responses   map[string]oasResponse `yaml:"responses"`
	XRoles      string                 `yaml:"x-roles"`
	Security    *[]map[string][]string `yaml:"security,omitempty"`
	Parameters  []oasParameter         `yaml:"parameters,omitempty"`
	Tags        []string               `yaml:"tags"`
	XHandler    string                 `yaml:"x-handler,omitempty"`
}

// noSecurity is the per-operation override emitted for public (unauthenticated)
// routes; it renders as `security: []`, overriding the document-level bearerAuth.
var noSecurity = []map[string][]string{}
var optionalMetricsSecurity = []map[string][]string{{}, {"bearerAuth": {}}}

const publicRoleText = "Public (no authentication required)"

type oasResponse struct {
	Description string                  `yaml:"description"`
	Content     map[string]oasMediaType `yaml:"content,omitempty"`
}

type oasParameter struct {
	Name        string         `yaml:"name"`
	In          string         `yaml:"in"`
	Description string         `yaml:"description,omitempty"`
	Required    bool           `yaml:"required"`
	Schema      oasParamSchema `yaml:"schema"`
}

type oasParamSchema struct {
	Type   string          `yaml:"type"`
	Format string          `yaml:"format,omitempty"`
	Items  *oasParamSchema `yaml:"items,omitempty"`
}

type oasRequestBody struct {
	Required bool                    `yaml:"required,omitempty"`
	Content  map[string]oasMediaType `yaml:"content"`
}

type oasMediaType struct {
	Schema oasSchema `yaml:"schema"`
}

type oasSchema struct {
	Ref                  string               `yaml:"$ref,omitempty"`
	Type                 string               `yaml:"type,omitempty"`
	Format               string               `yaml:"format,omitempty"`
	Description          string               `yaml:"description,omitempty"`
	Properties           map[string]oasSchema `yaml:"properties,omitempty"`
	Items                *oasSchema           `yaml:"items,omitempty"`
	AdditionalProperties *bool                `yaml:"additionalProperties,omitempty"`
	Enum                 []string             `yaml:"enum,omitempty"`
	Required             []string             `yaml:"required,omitempty"`
}

type inferredRouteContract struct {
	QueryParameters  []oasParameter
	HeaderParameters []oasParameter
	RequestBody      *oasRequestBody
	Responses        map[string]oasResponse
}

// discoveredRoute is one controller route extracted from the routing source.
type discoveredRoute struct {
	Method        string
	Path          string
	Handler       string // e.g. "handleGetTest" ("" if not a plain s.handleXxx handler)
	ActionArg     string // first string literal arg when the handler is a factory call
	Authenticated bool   // the registration chain includes an auth middleware
}

type routeDoc struct {
	Method        string
	Path          string
	HandlerLabel  string
	OperationID   string
	Summary       string
	Access        string
	Category      string
	Authenticated bool
	Parameters    []oasParameter
}

type endpointCatalog struct {
	Version int                    `json:"version"`
	Source  string                 `json:"source"`
	Routes  []endpointCatalogRoute `json:"routes"`
}

type endpointCatalogRoute struct {
	Method        string `json:"method"`
	Path          string `json:"path"`
	OperationID   string `json:"operationId"`
	Summary       string `json:"summary"`
	Category      string `json:"category"`
	Public        bool   `json:"public"`
	Authenticated bool   `json:"authenticated"`
	Access        string `json:"access"`
}

// GenerateOpenAPIYAML parses the controller routing source (serverGoPath, typically
// pkg/api/server.go) and returns the rendered OpenAPI 3.0 document as YAML bytes.
func GenerateOpenAPIYAML(serverGoPath string) ([]byte, error) {
	routes, err := discoverControllerRoutes(serverGoPath)
	if err != nil {
		return nil, err
	}
	if len(routes) == 0 {
		return nil, fmt.Errorf("no routes discovered in %s", serverGoPath)
	}
	contracts, err := inferRouteContracts(serverGoPath, routes)
	if err != nil {
		return nil, err
	}

	doc := oasDoc{
		OpenAPI: "3.0.3",
		Info: oasInfo{
			Title:       "APEX Load API",
			Description: openapiInfoDescription,
			Version:     "1.0.0",
		},
		Servers: []oasServer{{URL: "http://localhost:8080", Description: "Local development server"}},
		Components: oasComponents{
			SecuritySchemes: map[string]oasSecurityScheme{
				"bearerAuth":  {Type: "http", Scheme: "bearer", BearerFormat: "JWT or service-account key"},
				"apiKeyAuth":  {Type: "apiKey", Name: "X-APEXLoad-API-Key", In: "header"},
				"oidcSession": {Type: "apiKey", Name: "apexload_auth_session", In: "cookie"},
			},
			Schemas: openAPIComponentSchemas(),
		},
		// Each map entry is an alternative (logical OR). Trusted-header identity is
		// deployment-specific and is therefore described in the API User Guide.
		Security: []map[string][]string{{"bearerAuth": {}}, {"apiKeyAuth": {}}, {"oidcSession": {}}},
		Paths:    buildPaths(routes, contracts),
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// GenerateEndpointCatalogJSON parses the controller routing source and returns the
// stable dashboard documentation catalog consumed by the API Guide. It is generated
// from the same route metadata as the OpenAPI spec, but intentionally carries only
// documentation/UI metadata; it must not be used as an authorization source.
func GenerateEndpointCatalogJSON(serverGoPath string) ([]byte, error) {
	routes, err := discoverControllerRoutes(serverGoPath)
	if err != nil {
		return nil, err
	}
	if len(routes) == 0 {
		return nil, fmt.Errorf("no routes discovered in %s", serverGoPath)
	}

	docs := buildRouteDocs(routes)
	catalog := endpointCatalog{
		Version: 1,
		Source:  serverGoPath,
		Routes:  make([]endpointCatalogRoute, 0, len(docs)),
	}
	for _, doc := range docs {
		catalog.Routes = append(catalog.Routes, endpointCatalogRoute{
			Method:        doc.Method,
			Path:          doc.Path,
			OperationID:   doc.OperationID,
			Summary:       doc.Summary,
			Category:      doc.Category,
			Public:        !doc.Authenticated,
			Authenticated: doc.Authenticated,
			Access:        doc.Access,
		})
	}

	data, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// discoverControllerRoutes walks every `<x>.Handle("METHOD /path", handler)`
// registration in the given source file and returns the resolved routes.
func discoverControllerRoutes(serverGoPath string) ([]discoveredRoute, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, serverGoPath, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", serverGoPath, err)
	}

	var routes []discoveredRoute
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || (sel.Sel.Name != "Handle" && sel.Sel.Name != "HandleFunc") || len(call.Args) < 2 {
			return true
		}
		pattern, ok := stringLiteral(call.Args[0])
		if !ok {
			return true
		}
		method, path, ok := splitMethodPath(pattern)
		if !ok || !documentedRoute(method, path) {
			return true
		}
		handler, actionArg := findHandlerFunc(call.Args[1])
		authenticated := chainRequiresAuth(call.Args[1]) || inHandlerAuthenticatedRoute(method, path, handler)
		routes = append(routes, discoveredRoute{
			Method: method, Path: path, Handler: handler, ActionArg: actionArg,
			Authenticated: authenticated,
		})
		return true
	})
	return routes, nil
}

// buildPaths renders the discovered routes into the OpenAPI paths object, keeping
// operationIds unique and deterministic.
func buildPaths(routes []discoveredRoute, contracts map[string]inferredRouteContract) map[string]map[string]oasOperation {
	docs := buildRouteDocs(routes)
	paths := map[string]map[string]oasOperation{}
	for _, doc := range docs {
		contract := contracts[doc.Method+" "+doc.Path]
		responses := contract.Responses
		if len(responses) == 0 {
			responses = broadOperationResponses()
		}
		op := oasOperation{
			Summary:     doc.Summary,
			Description: fmt.Sprintf("**Access Control:** %s", doc.Access),
			OperationID: doc.OperationID,
			Deprecated:  deprecatedRoute(doc.Path),
			Responses:   responses,
			XRoles:      doc.Access,
			Tags:        []string{tagForPath(doc.Path)},
			Parameters:  append(append(doc.Parameters, contract.QueryParameters...), contract.HeaderParameters...),
			XHandler:    doc.HandlerLabel,
		}
		if requestBody := requestBodyForRoute(doc.Method, doc.Path); requestBody != nil {
			op.RequestBody = requestBody
		} else if contract.RequestBody != nil {
			op.RequestBody = contract.RequestBody
		}
		if !doc.Authenticated {
			op.Security = &noSecurity
		}
		if doc.Path == "/metrics" {
			op.Security = &optionalMetricsSecurity
		}
		if paths[doc.Path] == nil {
			paths[doc.Path] = map[string]oasOperation{}
		}
		paths[doc.Path][strings.ToLower(doc.Method)] = op
	}
	return paths
}

func broadOperationResponses() map[string]oasResponse {
	return map[string]oasResponse{
		"2XX": {Description: "Successful response; the exact status and representation are operation-specific."},
		"4XX": {Description: "Request, authentication, authorization, resource, or state error."},
		"5XX": {Description: "Controller-side failure."},
	}
}

func deprecatedRoute(path string) bool {
	return strings.HasPrefix(path, "/api/v1/script-debug/sessions/")
}

func openAPIComponentSchemas() map[string]oasSchema {
	return map[string]oasSchema{
		"ErrorResponse": {
			Type: "object",
			Properties: map[string]oasSchema{
				"error": {Type: "string", Description: "Human-readable error message."},
				"code":  {Type: "string", Description: "Optional stable machine-readable error code."},
			},
			Required: []string{"error"},
		},
		"ScriptDebugBreakpoint": {
			Type: "object",
			Properties: map[string]oasSchema{
				"phase":         {Type: "string", Enum: []string{"init", "action", "teardown"}},
				"action_index":  {Type: "integer"},
				"request_index": {Type: "integer"},
				"step_path":     {Type: "array", Items: &oasSchema{Type: "string"}},
			},
			Required: []string{"phase", "action_index"},
		},
		"ScriptDebugStepRequest": {
			Type: "object",
			Properties: map[string]oasSchema{
				"mode": {Type: "string", Enum: []string{"into", "over", "loop_end"}},
			},
		},
		"ScriptDebugBreakpointsRequest": {
			Type: "object",
			Properties: map[string]oasSchema{
				"breakpoints": {
					Type:  "array",
					Items: &oasSchema{Ref: "#/components/schemas/ScriptDebugBreakpoint"},
				},
			},
			Required: []string{"breakpoints"},
		},
		"ScriptDebugOptionsRequest": {
			Type: "object",
			Properties: map[string]oasSchema{
				"break_on_failure": {Type: "boolean"},
			},
			Required: []string{"break_on_failure"},
		},
	}
}

func requestBodyForRoute(method, path string) *oasRequestBody {
	if method == http.MethodPost && strings.HasSuffix(path, "/step") && replaySessionControlPath(path) {
		return jsonRequestBody("ScriptDebugStepRequest", false)
	}
	if method == http.MethodPatch && strings.HasSuffix(path, "/breakpoints") && replaySessionControlPath(path) {
		return jsonRequestBody("ScriptDebugBreakpointsRequest", true)
	}
	if method == http.MethodPatch && strings.HasSuffix(path, "/options") && replaySessionControlPath(path) {
		return jsonRequestBody("ScriptDebugOptionsRequest", true)
	}
	return nil
}

func replaySessionControlPath(path string) bool {
	return strings.HasPrefix(path, "/api/v1/replay/sessions/") || strings.HasPrefix(path, "/api/v1/script-debug/sessions/")
}

func jsonRequestBody(schemaName string, required bool) *oasRequestBody {
	return &oasRequestBody{
		Required: required,
		Content: map[string]oasMediaType{
			"application/json": {Schema: oasSchema{Ref: "#/components/schemas/" + schemaName}},
		},
	}
}

func buildRouteDocs(routes []discoveredRoute) []routeDoc {
	sort.Slice(routes, func(i, j int) bool {
		if routes[i].Path != routes[j].Path {
			return routes[i].Path < routes[j].Path
		}
		return routes[i].Method < routes[j].Method
	})

	usedOpID := map[string]int{}
	seenRoutes := map[string]bool{}
	docs := make([]routeDoc, 0, len(routes))
	for _, r := range routes {
		routeKey := r.Method + " " + r.Path
		if seenRoutes[routeKey] {
			continue
		}
		seenRoutes[routeKey] = true

		roles := publicRoleText
		if r.Authenticated || r.Path == "/metrics" {
			roles = routeRolesText(r.Method, r.Path)
		}

		handlerLabel := r.Handler
		opIDBase := r.Handler
		summaryBase := r.Handler
		if handlerLabel == "" {
			handlerLabel = "(inline handler)"
			opIDBase = synthOperationID(r.Method, r.Path)
			summaryBase = opIDBase
		}
		if r.ActionArg != "" {
			handlerLabel = fmt.Sprintf("%s(%q)", r.Handler, r.ActionArg)
			opIDBase += titleWord(r.ActionArg)
		}

		opID := opIDBase
		if usedOpID[opID] > 0 {
			usedOpID[opID]++
			opID = fmt.Sprintf("%s_%d", opIDBase, usedOpID[opID])
		} else {
			usedOpID[opID] = 1
		}

		summary := humanizeHandler(summaryBase)
		if r.ActionArg != "" {
			summary = fmt.Sprintf("%s (%s)", summary, r.ActionArg)
		}

		parameters := []oasParameter{}
		for _, m := range pathParamRe.FindAllStringSubmatch(r.Path, -1) {
			parameters = append(parameters, oasParameter{
				Name: m[1], In: "path", Required: true, Schema: oasParamSchema{Type: "string"},
			})
		}

		docs = append(docs, routeDoc{
			Method:        r.Method,
			Path:          r.Path,
			HandlerLabel:  handlerLabel,
			OperationID:   opID,
			Summary:       summary,
			Access:        roles,
			Category:      docsCategoryForPath(r.Path),
			Authenticated: r.Authenticated,
			Parameters:    parameters,
		})
	}
	return docs
}

// routeRolesText resolves the access requirement using the real runtime authorization
// logic so the documented roles cannot drift from enforcement.
func routeRolesText(method, path string) string {
	if method == http.MethodPost && strings.HasSuffix(path, "/monitoring/collectors/{id}/heartbeat") {
		return "Collector token via Bearer or X-APEXLoad-API-Key"
	}
	if method == http.MethodGet && path == "/metrics" {
		return "Public when no metrics token is configured; otherwise requires the metrics Bearer token"
	}
	concrete := pathParamRe.ReplaceAllString(path, "x")
	req, err := http.NewRequest(method, "http://localhost"+concrete, nil)
	if err != nil {
		return "Requires Authentication"
	}
	// Populate path values the way the real ServeMux would: several access
	// resolvers gate on r.PathValue("id"|"pid"|...) being non-empty, so without
	// these the requirement would resolve as if the route had no policy.
	for _, m := range pathParamRe.FindAllStringSubmatch(path, -1) {
		req.SetPathValue(m[1], "x")
	}
	requirement, ok := accessRequirementForRequest(req)
	if !ok {
		return "Requires Authentication"
	}
	return renderAccessRoles(requirement)
}

func inHandlerAuthenticatedRoute(method, path, handler string) bool {
	if method == http.MethodGet && path == "/api/v1/auth/diagnostics" {
		return true
	}
	return method == http.MethodPost && path == "/api/v1/monitoring/collectors/{id}/heartbeat" && handler == "handleCollectorHeartbeat"
}

func renderAccessRoles(req accessRequirement) string {
	parts := []string{"Requires authentication"}
	appendRoles := func(scope string, roles []string) {
		if len(roles) == 0 {
			return
		}
		names := make([]string, 0, len(roles))
		for _, role := range roles {
			names = append(names, roleDisplayName(role))
		}
		parts = append(parts, scope+" roles: "+strings.Join(names, ", "))
	}
	appendRoles("global", req.GlobalRoles)
	appendRoles("project", req.ProjectRoles)
	appendRoles("domain", req.DomainRoles)
	appendRoles("repository", req.RepositoryRoles)
	if req.ServiceAccountScope != "" {
		parts = append(parts, "service-account scope: "+req.ServiceAccountScope)
	} else if req.ServiceAccountScopeLookup != nil {
		parts = append(parts, "service-account scope: determined from the request")
	}
	return strings.Join(parts, "; ")
}

func roleDisplayName(role string) string {
	switch role {
	case RoleSystemAdmin:
		return "SystemAdmin"
	case RoleProjectAdmin:
		return "ProjectAdmin"
	case RoleEditor:
		return "Editor"
	case RoleViewer:
		return "Viewer"
	default:
		return role
	}
}

// findHandlerFunc descends through middleware wrappers to find the innermost
// s.handleXxx handler reference, returning its name and (for handler-factory calls
// like s.handleRemoteRunAction("start")) the first string-literal argument.
func findHandlerFunc(expr ast.Expr) (handler string, actionArg string) {
	ast.Inspect(expr, func(n ast.Node) bool {
		if handler != "" {
			return false
		}
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		ident, ok := sel.X.(*ast.Ident)
		if !ok || ident.Name != "s" || !strings.HasPrefix(sel.Sel.Name, "handle") {
			return true
		}
		handler = sel.Sel.Name
		return false
	})
	if handler == "" {
		return "", ""
	}
	ast.Inspect(expr, func(n ast.Node) bool {
		if actionArg != "" {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		ident, ok := sel.X.(*ast.Ident)
		if !ok || ident.Name != "s" || sel.Sel.Name != handler || len(call.Args) == 0 {
			return true
		}
		if lit, ok := stringLiteral(call.Args[0]); ok {
			actionArg = lit
		}
		return true
	})
	return handler, actionArg
}

// chainRequiresAuth reports whether the middleware chain wrapping a handler
// includes an authentication middleware — the local `auth` middleware or any of
// the specialized `*Auth` session middlewares (e.g. browserRecordingSessionEditAuth).
// Handler references are SelectorExprs (s.handleXxx), so they are never mistaken
// for a middleware Ident even when the handler name contains "Auth".
func chainRequiresAuth(expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(n ast.Node) bool {
		if found {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		ident, ok := call.Fun.(*ast.Ident)
		if !ok {
			return true
		}
		if ident.Name == "auth" || strings.HasSuffix(ident.Name, "Auth") {
			found = true
			return false
		}
		return true
	})
	return found
}

func stringLiteral(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return value, true
}

// documentedRoute reports whether a (method, path) belongs in the public API spec.
// The versioned API surface and the Prometheus scrape endpoint are documented; the
// SPA catch-all ("/"), CORS preflight (OPTIONS), and other infra routes are not.
func documentedRoute(method, path string) bool {
	if method == http.MethodOptions {
		return false
	}
	return strings.HasPrefix(path, "/api/v1/") || path == "/metrics"
}

func splitMethodPath(pattern string) (method, path string, ok bool) {
	parts := strings.SplitN(pattern, " ", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	if !httpRouteMethods[parts[0]] || !strings.HasPrefix(parts[1], "/") {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// humanizeHandler turns handler identifiers into readable API summaries while
// preserving initialisms (for example, "handleGetAPIDocs" -> "Get API Docs").
func humanizeHandler(handler string) string {
	name := strings.TrimPrefix(handler, "handle")
	runes := []rune(name)
	var b strings.Builder
	for i, r := range runes {
		if i > 0 && unicode.IsUpper(r) &&
			(unicode.IsLower(runes[i-1]) || (i+1 < len(runes) && unicode.IsLower(runes[i+1]) && unicode.IsUpper(runes[i-1]))) {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
	}
	readable := strings.TrimSpace(b.String())
	replacer := strings.NewReplacer(
		"Git Hub", "GitHub",
		"Open API", "OpenAPI",
		"Async API", "AsyncAPI",
		"Web Socket", "WebSocket",
	)
	return replacer.Replace(readable)
}

func titleWord(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func tagForPath(path string) string {
	trimmed := strings.TrimPrefix(strings.TrimPrefix(path, "/"), "api/v1/")
	segment := trimmed
	if i := strings.IndexByte(trimmed, '/'); i >= 0 {
		segment = trimmed[:i]
	}
	if segment == "" {
		return "General"
	}
	switch segment {
	case "ci":
		return "CI/CD"
	case "docs":
		return "Documentation"
	}
	words := strings.Split(segment, "-")
	for i, w := range words {
		words[i] = titleWord(w)
	}
	return strings.Join(words, " ")
}

func docsCategoryForPath(path string) string {
	if path == "/metrics" {
		return "Operations & Observability"
	}
	trimmed := strings.TrimPrefix(strings.TrimPrefix(path, "/"), "api/v1/")
	segment := trimmed
	if i := strings.IndexByte(trimmed, '/'); i >= 0 {
		segment = trimmed[:i]
	}
	switch segment {
	case "":
		return "General"
	case "ai":
		return "AI Assistance"
	case "ci":
		return "CI/CD"
	case "application-repository", "applications", "domains", "folders", "projects":
		return "Workspace & Assets"
	case "archives", "releases", "reports", "results":
		return "Results & Reporting"
	case "auth":
		return "Identity & Access"
	case "browser-recorder", "browser-replay", "recorder", "replay", "script-debug":
		return "Recording & Replay"
	case "capacity", "run-calendar", "schedules":
		return "Scheduling"
	case "correlation-rulesets", "correlations", "dsl", "object-repositories", "scripts", "selectors":
		return "Script Authoring"
	case "data-pools":
		return "Data & Parameters"
	case "docs":
		return "Documentation"
	case "desktop", "downloads", "system":
		return "System & Distribution"
	case "fleet", "metrics", "monitoring", "ws":
		return "Operations & Observability"
	case "integrations", "protocol-bundles", "protocols":
		return "Integrations & Protocols"
	case "library":
		return "Script Library"
	case "remotes":
		return "Remote Execution"
	case "runtime-settings":
		return "Execution Configuration"
	case "execution-plans", "scenarios", "tests":
		return "Test Execution"
	default:
		return tagForPath(path)
	}
}

func synthOperationID(method, path string) string {
	cleaned := pathParamRe.ReplaceAllString(path, "")
	cleaned = strings.NewReplacer("/", " ", "-", " ", "_", " ", ".", " ").Replace(cleaned)
	var b strings.Builder
	b.WriteString(strings.ToLower(method))
	for _, word := range strings.Fields(cleaned) {
		b.WriteString(titleWord(word))
	}
	return b.String()
}
