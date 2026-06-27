package api

// OpenAPI/Swagger generation. docs/swagger.yaml is generated from the controller
// routing layer (pkg/api/server.go) by cmd/swaggergen — do NOT hand-edit it.
// Method/path/handler are extracted from the s.mux.Handle(...) registrations via
// go/ast; the required roles are resolved by reusing the real runtime authorization
// logic (accessRequirementForRequest), so the spec cannot drift from enforcement.
//
//go:generate go run ../../cmd/swaggergen -server server.go -out ../../docs/swagger.yaml -catalog-out ../../docs/api-endpoint-catalog.json

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
	"All endpoints and their required roles are generated from the controller routing " +
	"layer (`pkg/api/server.go`) by `cmd/swaggergen`. Do not hand-edit this file — run " +
	"`go generate ./pkg/api` after changing routes."

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
	Scheme       string `yaml:"scheme"`
	BearerFormat string `yaml:"bearerFormat"`
}

type oasOperation struct {
	Summary     string                 `yaml:"summary"`
	Description string                 `yaml:"description"`
	OperationID string                 `yaml:"operationId"`
	RequestBody *oasRequestBody        `yaml:"requestBody,omitempty"`
	Responses   map[string]oasResponse `yaml:"responses"`
	XRoles      string                 `yaml:"x-roles"`
	Security    *[]map[string][]string `yaml:"security,omitempty"`
	Parameters  []oasParameter         `yaml:"parameters,omitempty"`
	Tags        []string               `yaml:"tags"`
}

// noSecurity is the per-operation override emitted for public (unauthenticated)
// routes; it renders as `security: []`, overriding the document-level bearerAuth.
var noSecurity = []map[string][]string{}

const publicRoleText = "Public (no authentication required)"

type oasResponse struct {
	Description string `yaml:"description"`
}

type oasParameter struct {
	Name     string         `yaml:"name"`
	In       string         `yaml:"in"`
	Required bool           `yaml:"required"`
	Schema   oasParamSchema `yaml:"schema"`
}

type oasParamSchema struct {
	Type string `yaml:"type"`
}

type oasRequestBody struct {
	Required bool                    `yaml:"required,omitempty"`
	Content  map[string]oasMediaType `yaml:"content"`
}

type oasMediaType struct {
	Schema oasSchema `yaml:"schema"`
}

type oasSchema struct {
	Ref        string               `yaml:"$ref,omitempty"`
	Type       string               `yaml:"type,omitempty"`
	Properties map[string]oasSchema `yaml:"properties,omitempty"`
	Items      *oasSchema           `yaml:"items,omitempty"`
	Enum       []string             `yaml:"enum,omitempty"`
	Required   []string             `yaml:"required,omitempty"`
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
				"bearerAuth": {Type: "http", Scheme: "bearer", BearerFormat: "JWT"},
			},
			Schemas: openAPIComponentSchemas(),
		},
		Security: []map[string][]string{{"bearerAuth": {}}},
		Paths:    buildPaths(routes),
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
		routes = append(routes, discoveredRoute{
			Method: method, Path: path, Handler: handler, ActionArg: actionArg,
			Authenticated: chainRequiresAuth(call.Args[1]),
		})
		return true
	})
	return routes, nil
}

// buildPaths renders the discovered routes into the OpenAPI paths object, keeping
// operationIds unique and deterministic.
func buildPaths(routes []discoveredRoute) map[string]map[string]oasOperation {
	docs := buildRouteDocs(routes)
	paths := map[string]map[string]oasOperation{}
	for _, doc := range docs {
		op := oasOperation{
			Summary:     doc.Summary,
			Description: fmt.Sprintf("**Handler:** `%s`\n\n**Access Control:** %s", doc.HandlerLabel, doc.Access),
			OperationID: doc.OperationID,
			Responses:   map[string]oasResponse{"200": {Description: "Successful operation"}},
			XRoles:      doc.Access,
			Tags:        []string{tagForPath(doc.Path)},
			Parameters:  doc.Parameters,
		}
		if requestBody := requestBodyForRoute(doc.Method, doc.Path); requestBody != nil {
			op.RequestBody = requestBody
		}
		if !doc.Authenticated {
			op.Security = &noSecurity
		}
		if paths[doc.Path] == nil {
			paths[doc.Path] = map[string]oasOperation{}
		}
		paths[doc.Path][strings.ToLower(doc.Method)] = op
	}
	return paths
}

func openAPIComponentSchemas() map[string]oasSchema {
	return map[string]oasSchema{
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
		if r.Authenticated {
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

func renderAccessRoles(req accessRequirement) string {
	entityScoped := req.ProjectLookup != nil || req.DomainLookup != nil || req.RepositoryLookup != nil ||
		len(req.ProjectRoles) > 0 || len(req.DomainRoles) > 0 || len(req.RepositoryRoles) > 0
	if entityScoped {
		return "Requires Authentication (Role based on specific entity)"
	}
	if len(req.GlobalRoles) > 0 {
		names := make([]string, 0, len(req.GlobalRoles))
		for _, role := range req.GlobalRoles {
			names = append(names, roleDisplayName(role))
		}
		return "Roles: " + strings.Join(names, ", ")
	}
	return "Requires Authentication"
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

// humanizeHandler turns "handleGetTestSLAStatus" into "Get Test S L A Status" by
// dropping the handle prefix and inserting a space before every uppercase rune.
func humanizeHandler(handler string) string {
	name := strings.TrimPrefix(handler, "handle")
	var b strings.Builder
	for i, r := range name {
		if i > 0 && unicode.IsUpper(r) {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
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
