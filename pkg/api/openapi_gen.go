package api

// OpenAPI/Swagger generation. docs/swagger.yaml is generated from the controller
// routing package, anchored at pkg/api/server.go, by cmd/swaggergen — do NOT
// hand-edit it. Method/path/handler metadata is extracted from both legacy
// s.mux.Handle(...) registrations and modular apiRouteSpec constructors via go/ast;
// the required roles are resolved by reusing the real runtime authorization logic
// (accessRequirementForRequest), so the spec cannot drift from enforcement.
//
//go:generate go run ../../cmd/swaggergen -server server.go -out ../../docs/swagger.yaml -catalog-out ../../docs/api-endpoint-catalog.json -embed-out apidocs/swagger.yaml -catalog-embed-out apidocs/api-endpoint-catalog.json
//go:generate go run ../../cmd/openapits -in ../../docs/swagger.yaml -out ../../dashboard/src/types/generated/openapi.ts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"os"
	"path/filepath"
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
	Description  string `yaml:"description,omitempty"`
	Type         string `yaml:"type"`
	Scheme       string `yaml:"scheme,omitempty"`
	BearerFormat string `yaml:"bearerFormat,omitempty"`
	Name         string `yaml:"name,omitempty"`
	In           string `yaml:"in,omitempty"`
}

type oasOperation struct {
	Summary      string                 `yaml:"summary"`
	Description  string                 `yaml:"description"`
	OperationID  string                 `yaml:"operationId"`
	Deprecated   bool                   `yaml:"deprecated,omitempty"`
	RequestBody  *oasRequestBody        `yaml:"requestBody,omitempty"`
	Responses    map[string]oasResponse `yaml:"responses"`
	XRoles       string                 `yaml:"x-roles"`
	Security     *[]map[string][]string `yaml:"security,omitempty"`
	Parameters   []oasParameter         `yaml:"parameters,omitempty"`
	Tags         []string               `yaml:"tags"`
	XHandler     string                 `yaml:"x-handler,omitempty"`
	XServer      string                 `yaml:"x-server,omitempty"`
	XConditional string                 `yaml:"x-conditional,omitempty"`
}

// noSecurity is the per-operation override emitted for public (unauthenticated)
// routes; it renders as `security: []`, overriding the document-level bearerAuth.
var noSecurity = []map[string][]string{}
var optionalMetricsSecurity = []map[string][]string{{}, {"bearerAuth": {}}}
var collectorTokenSecurity = []map[string][]string{{"collectorBearerAuth": {}}, {"collectorApiKeyAuth": {}}}

const publicRoleText = "Public (no authentication required)"

type oasResponse struct {
	Description string                  `yaml:"description"`
	Content     map[string]oasMediaType `yaml:"content,omitempty"`
	Headers     map[string]oasHeader    `yaml:"headers,omitempty"`
}

type oasHeader struct {
	Description string         `yaml:"description,omitempty"`
	Schema      oasParamSchema `yaml:"schema"`
}

type oasParameter struct {
	Name        string         `yaml:"name"`
	In          string         `yaml:"in"`
	Description string         `yaml:"description,omitempty"`
	Deprecated  bool           `yaml:"deprecated,omitempty"`
	Required    bool           `yaml:"required"`
	Schema      oasParamSchema `yaml:"schema"`
}

type oasParamSchema struct {
	Type   string          `yaml:"type"`
	Format string          `yaml:"format,omitempty"`
	Items  *oasParamSchema `yaml:"items,omitempty"`
	Enum   []string        `yaml:"enum,omitempty"`
}

type oasRequestBody struct {
	Required bool                    `yaml:"required,omitempty"`
	Content  map[string]oasMediaType `yaml:"content"`
}

type oasMediaType struct {
	Schema  oasSchema `yaml:"schema"`
	Example any       `yaml:"example,omitempty"`
}

type oasSchema struct {
	Ref                  string               `yaml:"$ref,omitempty"`
	Type                 string               `yaml:"type,omitempty"`
	Format               string               `yaml:"format,omitempty"`
	Description          string               `yaml:"description,omitempty"`
	Deprecated           bool                 `yaml:"deprecated,omitempty"`
	Properties           map[string]oasSchema `yaml:"properties,omitempty"`
	Items                *oasSchema           `yaml:"items,omitempty"`
	AdditionalProperties any                  `yaml:"additionalProperties,omitempty"`
	Enum                 []string             `yaml:"enum,omitempty"`
	Required             []string             `yaml:"required,omitempty"`
	Nullable             bool                 `yaml:"nullable,omitempty"`
	OneOf                []oasSchema          `yaml:"oneOf,omitempty"`
	Discriminator        *oasDiscriminator    `yaml:"discriminator,omitempty"`
}

// oasDiscriminator names the property that selects a oneOf variant, so a
// generated client can model a tagged union instead of a bag of mutually
// exclusive optional objects.
type oasDiscriminator struct {
	PropertyName string            `yaml:"propertyName"`
	Mapping      map[string]string `yaml:"mapping,omitempty"`
}

type inferredRouteContract struct {
	QueryParameters  []oasParameter
	HeaderParameters []oasParameter
	RequestBody      *oasRequestBody
	Responses        map[string]oasResponse
}

// discoveredRoute is one controller route extracted from the routing source.
type discoveredRoute struct {
	Method          string
	Path            string
	Handler         string // e.g. "handleGetTest" ("" if not a plain s.handleXxx handler)
	ActionArg       string // first string literal arg when the handler is a factory call
	AuthMiddleware  string // registration-chain auth identifier (auth, recorderSessionViewAuth, ...)
	Authenticated   bool   // the registration chain includes an auth middleware
	GenericAuth     bool   // the registration uses the standard auth middleware
	Unauthenticated bool   // no HTTP authentication middleware wraps the handler
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
	Server        string
	Conditional   string
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
	Server        string `json:"server,omitempty"`
	Conditional   string `json:"conditional,omitempty"`
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
				"bearerAuth":          {Type: "http", Scheme: "bearer", BearerFormat: "JWT or service-account key"},
				"apiKeyAuth":          {Type: "apiKey", Name: "X-APEXLoad-API-Key", In: "header"},
				"oidcSession":         {Type: "apiKey", Name: "apexload_auth_session", In: "cookie"},
				"bridgeToken":         {Type: "apiKey", Name: bridgeTokenHeader, In: "header"},
				"bridgeSession":       {Type: "apiKey", Name: bridgeSessionHeader, In: "header"},
				"collectorBearerAuth": {Type: "http", Scheme: "bearer", BearerFormat: "Collector capability token", Description: "Collector-scoped token issued by collector enrollment; browser sessions and user/service-account bearer credentials do not authorize this channel."},
				"collectorApiKeyAuth": {Type: "apiKey", Name: "X-APEXLoad-API-Key", In: "header", Description: "The same collector-scoped capability token in the API-key header; a project API key does not authorize this channel."},
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
		Source:  "pkg/api/server.go",
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
			Server:        doc.Server,
			Conditional:   doc.Conditional,
		})
	}

	data, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// discoverControllerRoutes walks every route registration in the package
// containing serverGoPath. The file remains an anchor for backward-compatible
// generator invocations and catalog metadata, but route declarations may live in
// any non-test Go source file in that package.
func discoverControllerRoutes(serverGoPath string) ([]discoveredRoute, error) {
	anchorInfo, err := os.Stat(serverGoPath)
	if err != nil {
		return nil, fmt.Errorf("stat route source %s: %w", serverGoPath, err)
	}
	legacyRouteFile := serverGoPath
	if anchorInfo.IsDir() {
		legacyRouteFile = filepath.Join(serverGoPath, "server.go")
	}
	legacyRouteFile = filepath.Clean(legacyRouteFile)

	sourceFiles, err := controllerRouteSourceFiles(serverGoPath)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	var routes []discoveredRoute
	hasBridgeRoutes := false
	for _, sourceFile := range sourceFiles {
		if filepath.Base(sourceFile) == "browser_recording_bridge.go" {
			hasBridgeRoutes = true
		}
		isLegacyRouteFile := filepath.Clean(sourceFile) == legacyRouteFile
		source, readErr := os.ReadFile(sourceFile)
		if readErr != nil {
			return nil, fmt.Errorf("read %s: %w", sourceFile, readErr)
		}
		if !isLegacyRouteFile &&
			!bytes.Contains(source, []byte("newAuthenticatedAPIRoute(")) &&
			!bytes.Contains(source, []byte("newPublicAPIRoute(")) {
			continue
		}
		file, parseErr := parser.ParseFile(fset, sourceFile, source, 0)
		if parseErr != nil {
			return nil, fmt.Errorf("parse %s: %w", sourceFile, parseErr)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			// Direct registrations remain temporarily anchored in server.go. New
			// package-level route files use apiRouteSpec, which avoids confusing
			// the controller API with the separate browser-recording bridge mux.
			if isLegacyRouteFile {
				if route, ok := discoveredMuxRoute(call); ok {
					routes = append(routes, route)
					return true
				}
			}
			if route, ok := discoveredRouteSpec(call); ok {
				routes = append(routes, route)
			}
			return true
		})
	}
	if hasBridgeRoutes {
		// The local recording-agent bridge uses a separate mux and address, so it
		// is not discoverable through the controller registrations above. These
		// unique pairing routes are nevertheless a supported public contract;
		// x-server keeps clients from mistaking them for controller endpoints.
		routes = append(routes,
			discoveredRoute{Method: http.MethodPost, Path: bridgePairPath, Handler: "handlePair", Unauthenticated: true},
			discoveredRoute{Method: http.MethodGet, Path: bridgeStatusPath, Handler: "handleStatus", Authenticated: true},
		)
	}
	return routes, nil
}

func controllerRouteSourceFiles(anchorPath string) ([]string, error) {
	dir, err := controllerRouteSourceDirectory(anchorPath)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read route source directory %s: %w", dir, err)
	}
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		files = append(files, filepath.Join(dir, name))
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no Go route sources found in %s", dir)
	}
	sort.Strings(files)
	return files, nil
}

func controllerRouteSourceDirectory(anchorPath string) (string, error) {
	info, err := os.Stat(anchorPath)
	if err != nil {
		return "", fmt.Errorf("stat route source %s: %w", anchorPath, err)
	}
	if info.IsDir() {
		return anchorPath, nil
	}
	return filepath.Dir(anchorPath), nil
}

func discoveredMuxRoute(call *ast.CallExpr) (discoveredRoute, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || (sel.Sel.Name != "Handle" && sel.Sel.Name != "HandleFunc") || len(call.Args) < 2 {
		return discoveredRoute{}, false
	}
	muxSelector, ok := sel.X.(*ast.SelectorExpr)
	if !ok || muxSelector.Sel.Name != "mux" {
		return discoveredRoute{}, false
	}
	receiver, ok := muxSelector.X.(*ast.Ident)
	if !ok || receiver.Name != "s" {
		return discoveredRoute{}, false
	}
	pattern, ok := stringLiteral(call.Args[0])
	if !ok {
		return discoveredRoute{}, false
	}
	method, path, ok := splitMethodPath(pattern)
	if !ok || !documentedRoute(method, path) {
		return discoveredRoute{}, false
	}
	handler, actionArg := findHandlerFunc(call.Args[1])
	authMiddleware := chainAuthMiddlewareName(call.Args[1])
	hasAuthMiddleware := authMiddleware != ""
	return discoveredRoute{
		Method:          method,
		Path:            path,
		Handler:         handler,
		ActionArg:       actionArg,
		AuthMiddleware:  authMiddleware,
		Authenticated:   hasAuthMiddleware || inHandlerAuthenticatedRoute(method, path, handler),
		GenericAuth:     authMiddleware == "auth",
		Unauthenticated: !hasAuthMiddleware,
	}, true
}

func discoveredRouteSpec(call *ast.CallExpr) (discoveredRoute, bool) {
	constructor, ok := call.Fun.(*ast.Ident)
	if !ok || (constructor.Name != "newAuthenticatedAPIRoute" && constructor.Name != "newPublicAPIRoute") || len(call.Args) < 3 {
		return discoveredRoute{}, false
	}
	method, ok := httpMethodExpression(call.Args[0])
	if !ok {
		return discoveredRoute{}, false
	}
	path, ok := stringLiteral(call.Args[1])
	if !ok || !documentedRoute(method, path) {
		return discoveredRoute{}, false
	}
	handler, ok := stringLiteral(call.Args[2])
	if !ok {
		return discoveredRoute{}, false
	}
	hasAuthMiddleware := constructor.Name == "newAuthenticatedAPIRoute"
	authMiddleware := ""
	if hasAuthMiddleware {
		authMiddleware = "auth"
	}
	return discoveredRoute{
		Method:          method,
		Path:            path,
		Handler:         handler,
		AuthMiddleware:  authMiddleware,
		Authenticated:   hasAuthMiddleware || inHandlerAuthenticatedRoute(method, path, handler),
		GenericAuth:     hasAuthMiddleware,
		Unauthenticated: !hasAuthMiddleware,
	}, true
}

func httpMethodExpression(expr ast.Expr) (string, bool) {
	if method, ok := stringLiteral(expr); ok && httpRouteMethods[method] {
		return method, true
	}
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	packageName, ok := selector.X.(*ast.Ident)
	if !ok || packageName.Name != "http" {
		return "", false
	}
	methods := map[string]string{
		"MethodGet": http.MethodGet, "MethodPost": http.MethodPost,
		"MethodPut": http.MethodPut, "MethodPatch": http.MethodPatch,
		"MethodDelete": http.MethodDelete, "MethodHead": http.MethodHead,
		"MethodOptions": http.MethodOptions,
	}
	method, ok := methods[selector.Sel.Name]
	return method, ok
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
		responses = responseSchemasForRoute(doc.Method, doc.Path, responses)
		op := oasOperation{
			Summary:      doc.Summary,
			Description:  fmt.Sprintf("**Access Control:** %s", doc.Access),
			OperationID:  doc.OperationID,
			Deprecated:   deprecatedRoute(doc.Path),
			Responses:    responses,
			XRoles:       doc.Access,
			Tags:         []string{tagForPath(doc.Path)},
			Parameters:   append(append(doc.Parameters, contract.QueryParameters...), contract.HeaderParameters...),
			XHandler:     doc.HandlerLabel,
			XServer:      doc.Server,
			XConditional: doc.Conditional,
		}
		if doc.Server != "" {
			op.Description += "\n\n**Server:** " + doc.Server
		}
		if doc.Conditional != "" {
			op.Description += "\n\n**Availability:** " + doc.Conditional
		}
		if requestBody := requestBodyForRoute(doc.Method, doc.Path); requestBody != nil {
			op.RequestBody = requestBody
		} else if contract.RequestBody != nil {
			op.RequestBody = contract.RequestBody
		}
		if !doc.Authenticated {
			op.Security = &noSecurity
		}
		if expected := collectorTokenRouteHandler(doc.Method, doc.Path); doc.Authenticated && expected != "" && doc.HandlerLabel == expected {
			// Collector credentials are separate from document-level browser and
			// service-account identity. Either token header is sufficient.
			op.Security = &collectorTokenSecurity
		}
		if doc.Path == "/metrics" {
			op.Security = &optionalMetricsSecurity
		}
		if doc.Path == bridgeStatusPath {
			// Both headers are required by authorizeTokenRequest. Putting the two
			// schemes in one requirement object expresses AND in OpenAPI.
			bridgeSecurity := []map[string][]string{{"bridgeToken": {}, "bridgeSession": {}}}
			op.Security = &bridgeSecurity
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
	schemas := map[string]oasSchema{
		"JMeterReviewSummary": {
			Type: "object", Description: "Distinct source behavior counts. Unsupported behaviors are a subset; sampler and action statistics are separate.",
			Properties: map[string]oasSchema{
				"blocking_behaviors": {Type: "integer"}, "review_behaviors": {Type: "integer"}, "unsupported_behaviors": {Type: "integer"}, "warnings": {Type: "integer"}, "informational": {Type: "integer"},
			}, Required: []string{"blocking_behaviors", "review_behaviors", "unsupported_behaviors", "warnings", "informational"},
		},
		"JMeterReviewDiagnostic": {
			Type: "object", Properties: map[string]oasSchema{
				"code": {Type: "string"}, "source_ref": {Type: "string"}, "element": {Type: "string"}, "detail": {Type: "string"}, "location": {Type: "string"}, "severity": {Type: "string", Enum: []string{"blocker", "warning", "info"}},
			}, Required: []string{"element", "detail", "severity"},
		},
		"JMeterImportReviewResponse": {
			Type: "object", AdditionalProperties: boolPointer(true),
			Properties: map[string]oasSchema{
				"review_summary":       {Ref: "#/components/schemas/JMeterReviewSummary"},
				"diagnostics":          {Type: "array", Items: &oasSchema{Ref: "#/components/schemas/JMeterReviewDiagnostic"}},
				"unsupported_elements": {Type: "array", Items: &oasSchema{Type: "string"}, Description: "Only enabled source behaviors without a faithful implementation; excludes configuration repairs and information."},
			}, Required: []string{"review_summary", "diagnostics", "unsupported_elements"},
		},
		"ErrorResponse": {
			Type: "object",
			Properties: map[string]oasSchema{
				"error": {Type: "string", Description: "Human-readable error message."},
				"code":  {Type: "string", Description: "Optional stable machine-readable error code."},
			},
			Required: []string{"error"},
		},
		"SystemReadinessDetails": {
			Type:        "object",
			Description: "Sanitized operational metadata whose fields vary by check. Secret-bearing configuration values are never returned.",
			Properties: map[string]oasSchema{
				"enabled":                  {Type: "boolean"},
				"configured":               {Type: "boolean"},
				"namespace":                {Type: "string"},
				"pod_count":                {Type: "integer"},
				"ready_pods":               {Type: "integer"},
				"terminating_pods":         {Type: "integer"},
				"probe_latency_ms":         {Type: "number", Format: "double"},
				"reason":                   {Type: "string"},
				"store_type":               {Type: "string", Enum: []string{"sqlite", "postgres", "unknown"}},
				"lease_capability":         {Type: "boolean"},
				"timescaledb_enabled":      {Type: "boolean"},
				"cache_backend":            {Type: "string", Enum: []string{"memory", "redis", "valkey", "unknown"}},
				"redis_url_configured":     {Type: "boolean"},
				"tls_enabled":              {Type: "boolean"},
				"endpoint":                 {Type: "string", Description: "Credential-free Redis/Valkey scheme and authority when safely parseable."},
				"timeout":                  {Type: "string"},
				"config_error":             {Type: "string", Description: "Sanitized cache configuration category."},
				"execution_mode":           {Type: "string", Enum: []string{"local", "distributed"}},
				"generators":               {Type: "integer"},
				"available":                {Type: "integer"},
				"unreachable":              {Type: "integer"},
				"grpc_transport":           {Type: "string"},
				"enabled_schedules":        {Type: "integer"},
				"due_schedules":            {Type: "integer"},
				"errored_schedules":        {Type: "integer"},
				"metrics_interval_seconds": {Type: "integer"},
				"ws_clients":               {Type: "integer"},
			},
			AdditionalProperties: boolPointer(true),
		},
		"SystemReadinessCheck": {
			Type: "object",
			Properties: map[string]oasSchema{
				"name": {
					Type: "string",
					Enum: []string{
						"kubernetes_api", "metadata_store", "archive_writer", "cache_backend",
						"generator_rpc", "telemetry_fanout", "auth_mode", "platform_integrations",
						"github_enterprise", "scheduler_state",
					},
				},
				"status":  {Type: "string", Enum: []string{"ready", "degraded", "not_ready"}},
				"message": {Type: "string"},
				"details": {Ref: "#/components/schemas/SystemReadinessDetails"},
			},
			Required: []string{"name", "status", "message"},
		},
		"SystemReadinessResponse": {
			Type: "object",
			Properties: map[string]oasSchema{
				"status":       {Type: "string", Enum: []string{"ready", "degraded", "not_ready"}},
				"generated_at": {Type: "string", Format: "date-time"},
				"checks": {
					Type:  "array",
					Items: &oasSchema{Ref: "#/components/schemas/SystemReadinessCheck"},
				},
			},
			Required: []string{"status", "generated_at", "checks"},
		},
		"LibraryAssignmentRequest": {
			Type: "object", Required: []string{"application_id", "release_ids", "expected_revision"},
			Properties: map[string]oasSchema{
				"application_id":    {Type: "string", Nullable: true},
				"release_ids":       {Type: "array", Items: &oasSchema{Type: "string"}},
				"expected_revision": {Type: "integer", Format: "int64"},
			},
		},
		"LibraryAssignment": {
			Type: "object", Required: []string{"application", "releases", "revision"},
			Properties: map[string]oasSchema{
				"application": {Type: "object", Nullable: true, Required: []string{"id", "name"}, Properties: map[string]oasSchema{"id": {Type: "string"}, "name": {Type: "string"}}},
				"releases":    {Type: "array", Items: &oasSchema{Type: "object", Required: []string{"id", "name", "application_id"}, Properties: map[string]oasSchema{"id": {Type: "string"}, "name": {Type: "string"}, "application_id": {Type: "string"}, "archived_at": {Type: "string", Format: "date-time"}}}},
				"revision":    {Type: "integer", Format: "int64"},
			},
		},
		"ProjectReleaseCreateRequest": {
			Type:        "object",
			Description: "Creates one logical release family joined to every application in the project.",
			Properties: map[string]oasSchema{
				"name":           {Type: "string"},
				"application_id": {Type: "string", Description: "Preferred application member returned for immediate selection."},
			},
			Required: []string{"name"},
		},
		"ReleaseFamily": {
			Type:        "object",
			Description: "Canonical project release family. id and group_id are the stable family id; member_ids maps application ids to physical release ids used for run placement.",
			Properties: map[string]oasSchema{
				"id":                     {Type: "string"},
				"group_id":               {Type: "string"},
				"project_id":             {Type: "string"},
				"application_id":         {Type: "string"},
				"application_release_id": {Type: "string"},
				"member_ids":             openAPIStringMap("Application id to physical release id."),
				"name":                   {Type: "string"},
				"created_at":             {Type: "string", Format: "date-time"},
				"updated_at":             {Type: "string", Format: "date-time"},
				"archived_at":            {Type: "string", Format: "date-time", Nullable: true},
			},
			Required: []string{"id", "group_id", "project_id", "name", "created_at", "updated_at", "member_ids"},
		},
		"ReleaseFamilyListResponse": {
			Type: "object",
			Properties: map[string]oasSchema{
				"releases": {Type: "array", Items: &oasSchema{Ref: "#/components/schemas/ReleaseFamily"}},
				"count":    {Type: "integer"},
			},
			Required: []string{"releases", "count"},
		},
		"ScriptDebugBreakpoint": {
			Type: "object",
			Properties: map[string]oasSchema{
				"disabled":      {Type: "boolean"},
				"once":          {Type: "boolean"},
				"hit_count":     {Type: "integer", Description: "Pause on this eligible visit (1-based); zero means every visit."},
				"iteration":     {Type: "integer", Description: "Innermost loop iteration, zero-based."},
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
		"AuthoringSessionCreateRequest": {
			Type: "object",
			Properties: map[string]oasSchema{
				"project_id": {Type: "string"},
				"folder_id": {
					Type:     "string",
					Nullable: true,
					// Omitting the field and sending null are indistinguishable
					// here (both decode to a nil pointer), so neither clears a
					// folder the draft already carries — same semantics as
					// folder_id on POST /scripts. Clear placement via
					// draft.folder_id instead.
					Description: "Optional script folder for the approved script. Omitting it (or sending null) leaves draft.folder_id in force rather than clearing it; set draft.folder_id to null to file the script at the project root. Must belong to the same project, and is validated on create and again at approval.",
				},
				"source": {Type: "string"},
				"name":   {Type: "string"},
				"status": {
					Type: "string",
					Enum: []string{"reviewing", "editing", "blocked", "ready"},
				},
				"draft": {
					Type:                 "object",
					Description:          "APEX Load script draft retained for review and approval.",
					AdditionalProperties: boolPointer(true),
				},
				"review_state": {
					Type:                 "object",
					Description:          "Client-extensible review evidence and workflow state.",
					AdditionalProperties: boolPointer(true),
				},
				"idempotency_key": {
					Type:        "string",
					Description: "Optional project-scoped retry key (maximum 256 bytes). Reusing it with the same request returns the original session; reusing it with different content returns 409.",
				},
			},
			Required: []string{"source"},
		},
	}
	for name, schema := range brunoOpenAPIComponentSchemas() {
		schemas[name] = schema
	}
	for name, schema := range enterpriseArchiveCollectionOpenAPISchemas() {
		schemas[name] = schema
	}
	for name, schema := range authoringReceiptOpenAPISchemas() {
		schemas[name] = schema
	}
	for name, schema := range scriptImportReviewOpenAPISchemas() {
		schemas[name] = schema
	}
	return schemas
}

func brunoOpenAPIComponentSchemas() map[string]oasSchema {
	return map[string]oasSchema{
		"BrunoInspectOptions": {
			Type:        "object",
			Description: "Native OpenCollection YAML HTTP inspection controls. Omitting output_mode selects native_requests.",
			Properties: map[string]oasSchema{
				"output_mode": {
					Type:        "string",
					Enum:        []string{"native_requests"},
					Description: "Creates one server-owned native HTTP candidate per supported request.",
				},
			},
		},
		"BrunoInspectFile": {
			Type:        "object",
			Description: "One file in a directory-style Bruno inspection upload.",
			Properties: map[string]oasSchema{
				"path":           {Type: "string"},
				"content":        {Type: "string"},
				"content_base64": {Type: "string", Format: "byte"},
			},
			Required: []string{"path"},
		},
		"BrunoInspectRequest": {
			Type: "object",
			Properties: map[string]oasSchema{
				"project_id":     {Type: "string"},
				"name":           {Type: "string"},
				"filename":       {Type: "string"},
				"content":        {Type: "string"},
				"content_base64": {Type: "string", Format: "byte"},
				"files": {
					Type:  "array",
					Items: &oasSchema{Ref: "#/components/schemas/BrunoInspectFile"},
				},
				"root_path": {Type: "string"},
				"options":   {Ref: "#/components/schemas/BrunoInspectOptions"},
			},
		},
		"BrunoArtifactSummary": {
			Type: "object",
			Properties: map[string]oasSchema{
				"id":              {Type: "string"},
				"artifact_kind":   {Type: "string"},
				"sha256":          {Type: "string"},
				"size_bytes":      {Type: "integer", Format: "int64"},
				"manifest_sha256": {Type: "string"},
			},
			Required: []string{"id", "artifact_kind", "sha256", "size_bytes"},
		},
		"BrunoManifestFile": {
			Type: "object",
			Properties: map[string]oasSchema{
				"path":       {Type: "string"},
				"role":       {Type: "string"},
				"media_type": {Type: "string"},
				"size_bytes": {Type: "integer", Format: "int64"},
				"sha256":     {Type: "string"},
				"mode":       {Type: "integer", Format: "int64"},
				"executable": {Type: "boolean"},
			},
			Required: []string{"path", "role", "size_bytes", "sha256", "mode"},
		},
		"BrunoManifest": {
			Type: "object",
			Properties: map[string]oasSchema{
				"version":       {Type: "string"},
				"sha256":        {Type: "string"},
				"bundle_sha256": {Type: "string"},
				"total_bytes":   {Type: "integer", Format: "int64"},
				"files": {
					Type:  "array",
					Items: &oasSchema{Ref: "#/components/schemas/BrunoManifestFile"},
				},
			},
			Required: []string{"version", "sha256", "bundle_sha256", "total_bytes", "files"},
		},
		"BrunoCollectionItem": {
			Type: "object",
			Properties: map[string]oasSchema{
				"uid":         {Type: "string"},
				"name":        {Type: "string"},
				"path":        {Type: "string"},
				"type":        {Type: "string"},
				"sequence":    {Type: "integer"},
				"tags":        {Type: "array", Items: &oasSchema{Type: "string"}},
				"executable":  {Type: "boolean"},
				"has_scripts": {Type: "boolean"},
				"has_tests":   {Type: "boolean"},
				"children": {
					Type:  "array",
					Items: &oasSchema{Ref: "#/components/schemas/BrunoCollectionItem"},
				},
			},
			Required: []string{"uid", "name", "path", "type", "executable"},
		},
		"BrunoCollection": {
			Type: "object",
			Properties: map[string]oasSchema{
				"name": {Type: "string"},
				"name_declared": {
					Type:        "boolean",
					Description: "True when name comes from the collection's own metadata rather than a folder name, file name or default.",
				},
				"uid": {Type: "string"},
				"items": {
					Type:  "array",
					Items: &oasSchema{Ref: "#/components/schemas/BrunoCollectionItem"},
				},
			},
			Required: []string{"name", "items"},
		},
		"BrunoEnvironment": {
			Type: "object",
			Properties: map[string]oasSchema{
				"name":   {Type: "string"},
				"path":   {Type: "string"},
				"type":   {Type: "string"},
				"values": openAPIStringMap("The environment's plain values for the variables the requests leave unset, offered to fill those inputs. Secret, disabled and scrubbed variables are never included, nor are .env values."),
			},
			Required: []string{"name", "path", "type"},
		},
		"BrunoCapabilities": {
			Type: "object",
			Properties: map[string]oasSchema{
				"request_types":         {Type: "array", Items: &oasSchema{Type: "string"}},
				"has_javascript":        {Type: "boolean"},
				"has_npm_dependencies":  {Type: "boolean"},
				"has_local_modules":     {Type: "boolean"},
				"has_file_bodies":       {Type: "boolean"},
				"has_certificates":      {Type: "boolean"},
				"has_private_key_files": {Type: "boolean"},
				"has_bruno_apps":        {Type: "boolean"},
				"required_sandbox":      {Type: "string"},
				"runner_profile":        {Type: "string"},
				"converted_hooks": {
					Type:        "integer",
					Description: "JavaScript hooks converted to native checks, counted once per request that runs them.",
				},
				"unconverted_hooks": {
					Type:        "integer",
					Description: "JavaScript hooks left for review, counted once per request that runs them.",
				},
			},
			Required: []string{
				"request_types", "has_javascript", "has_npm_dependencies", "has_local_modules",
				"has_file_bodies", "has_certificates", "has_private_key_files", "has_bruno_apps",
				"required_sandbox",
			},
		},
		"BrunoSecretRequirement": {
			Type: "object",
			Properties: map[string]oasSchema{
				"id":                          {Type: "string"},
				"path":                        {Type: "string"},
				"key":                         {Type: "string"},
				"suggested_name":              {Type: "string"},
				"replacement":                 {Type: "string"},
				"context":                     {Type: "string"},
				"display_label":               {Type: "string"},
				"source_variable":             {Type: "string"},
				"unresolved_source_variable":  {Type: "boolean"},
				"affected_paths":              {Type: "array", Items: &oasSchema{Type: "string"}},
				"source_value_available":      {Type: "boolean"},
				"destination_value_preserved": {Type: "boolean"},
				"preservation_unavailable_reason": {
					Type: "string",
					Enum: []string{
						"source_value_unavailable",
						"existing_secret_reference",
						"destination_mapping_incomplete",
					},
				},
				"existing_reference": {Type: "boolean"},
			},
			Required: []string{"id", "path", "key", "suggested_name", "replacement", "context"},
		},
		"BrunoSecurityFinding": {
			Type: "object",
			Properties: map[string]oasSchema{
				"id":       {Type: "string"},
				"severity": {Type: "string"},
				"code":     {Type: "string"},
				"path":     {Type: "string"},
				"summary":  {Type: "string"},
			},
			Required: []string{"severity", "code", "summary"},
		},
		"BrunoNativeDiagnostic": {
			Type: "object",
			Properties: map[string]oasSchema{
				"id":                       {Type: "string"},
				"finding_id":               {Type: "string"},
				"severity":                 {Type: "string"},
				"code":                     {Type: "string"},
				"path":                     {Type: "string"},
				"message":                  {Type: "string"},
				"acknowledgement_required": {Type: "boolean"},
				"header_repair":            {Ref: "#/components/schemas/BrunoNativeHeaderRepair"},
			},
			Required: []string{"id", "severity", "code", "message"},
		},
		"BrunoNativeHeaderRepair": {
			Type:        "object",
			Description: "Locates a request header HTTP cannot carry and lists the fixes available at commit. It carries no header value.",
			Properties: map[string]oasSchema{
				"action_index":       {Type: "integer"},
				"header_index":       {Type: "integer"},
				"header_name":        {Type: "string", Description: "Empty when the name itself is invalid."},
				"options":            {Type: "array", Items: &oasSchema{Type: "string", Enum: []string{"trim", "remove"}}},
				"kept_characters":    {Type: "integer", Description: "Characters trim keeps."},
				"removed_characters": {Type: "integer", Description: "Characters trim removes."},
				"invalid_character":  {Type: "string", Description: "Plain description of the first character HTTP does not allow."},
				"omitted":            {Type: "boolean", Description: "True when the header is not a credential and was left out of the script: trim puts the kept text back, remove confirms it stays out."},
				"replaces_body_default": {
					Type:        "boolean",
					Description: "True when the request body's default Content-Type took the left-out header's place: trim overwrites that default and remove keeps it.",
				},
			},
			Required: []string{"action_index", "header_index", "header_name", "options"},
		},
		"BrunoNativeExistingScript": {
			Type:        "object",
			Description: "A script an earlier import created from the same source request.",
			Properties: map[string]oasSchema{
				"script_id":  {Type: "string"},
				"name":       {Type: "string"},
				"ref_key":    {Type: "string"},
				"folder_id":  {Type: "string"},
				"updated_at": {Type: "string", Format: "date-time"},
				"matches":    {Type: "integer", Description: "Active scripts imported from this request. A script from the same collection is offered first, then the newest."},
				"same_collection": {
					Type:        "boolean",
					Description: "True when the collection UIDs match or, with no UIDs, the collection names do. Other matches should default to creating a copy.",
				},
				"edited_since_import": {
					Type:        "boolean",
					Description: "True when the script changed after the import that last saved it, including a rename or move, or that cannot be confirmed. Updating replaces its steps and settings and keeps its name and folder.",
				},
			},
			Required: []string{"script_id", "name", "updated_at", "matches"},
		},
		"BrunoNativeUpdateTarget": {
			Type:                 "object",
			AdditionalProperties: boolPointer(false),
			Properties: map[string]oasSchema{
				"script_id": {Type: "string"},
				"expected_updated_at": {
					Type:        "string",
					Format:      "date-time",
					Description: "The script's updated_at when it was reviewed. The update fails if the script changed since.",
				},
			},
			Required: []string{"script_id", "expected_updated_at"},
		},
		"BrunoSourceMapping": {
			Type: "object",
			Properties: map[string]oasSchema{
				"path":   {Type: "string"},
				"role":   {Type: "string"},
				"sha256": {Type: "string"},
			},
			Required: []string{"path", "role", "sha256"},
		},
		"APEXScript": {
			Type:        "object",
			Description: "APEX script DSL. Native inspection returns its disclosure-safe masked representation; commit returns the durable script.",
			Properties: map[string]oasSchema{
				"id":         {Type: "string"},
				"ref_key":    {Type: "string"},
				"name":       {Type: "string"},
				"version":    {Type: "string"},
				"protocol":   {Type: "string"},
				"project_id": {Type: "string"},
				"folder_id":  {Type: "string", Nullable: true},
				"config":     openAPIFreeformObject("APEX script configuration."),
				"parameters": {Type: "array", Items: &oasSchema{Type: "object", AdditionalProperties: boolPointer(true)}},
				"init":       {Type: "array", Items: &oasSchema{Type: "object", AdditionalProperties: boolPointer(true)}},
				"actions":    {Type: "array", Items: &oasSchema{Type: "object", AdditionalProperties: boolPointer(true)}},
				"teardown":   {Type: "array", Items: &oasSchema{Type: "object", AdditionalProperties: boolPointer(true)}},
				"metadata":   openAPIFreeformObject("Script provenance and runtime metadata."),
				"created_at": {Type: "string", Format: "date-time"},
				"updated_at": {Type: "string", Format: "date-time"},
			},
			AdditionalProperties: boolPointer(true),
			Required:             []string{"id", "name", "version", "protocol", "config", "actions"},
		},
		"BrunoNativeScriptCandidate": {
			Type:        "object",
			Description: "One ordered, server-owned native HTTP script candidate. Clients may select or rename it but never submit replacement DSL or choose its durable ID.",
			Properties: map[string]oasSchema{
				"candidate_id":      {Type: "string"},
				"planned_script_id": {Type: "string"},
				"source_path":       {Type: "string"},
				"source_uid":        {Type: "string"},
				"sequence":          {Type: "integer"},
				"method":            {Type: "string"},
				"target":            {Type: "string"},
				"script": {
					Ref:         "#/components/schemas/APEXScript",
					Description: "Server-authored, masked APEX DSL candidate.",
				},
				"fidelity": {Type: "string"},
				"diagnostics": {
					Type:  "array",
					Items: &oasSchema{Ref: "#/components/schemas/BrunoNativeDiagnostic"},
				},
				"existing_script":   {Ref: "#/components/schemas/BrunoNativeExistingScript"},
				"missing_variables": {Type: "array", Items: &oasSchema{Type: "string"}},
				"secret_requirements": {
					Type:  "array",
					Items: &oasSchema{Ref: "#/components/schemas/BrunoSecretRequirement"},
				},
				"source_mappings": {
					Type:        "array",
					Description: "Scrubbed, sanitized-source mappings that influenced this candidate.",
					Items:       &oasSchema{Ref: "#/components/schemas/BrunoSourceMapping"},
				},
				"parameterization": {
					Type:                 "object",
					Description:          "This request's shared parameter suggestions (generated values and data parameters), as the other importers report them. Select IDs through parameterization_selections on commit.",
					AdditionalProperties: boolPointer(true),
				},
			},
			Required: []string{
				"candidate_id", "planned_script_id", "source_path", "source_uid",
				"method", "target", "script", "fidelity",
			},
		},
		"BrunoFidelityReport": {
			Type: "object",
			Properties: map[string]oasSchema{
				"status":                 {Type: "string"},
				"preserved_files":        {Type: "integer"},
				"executable_items":       {Type: "integer"},
				"preserved_apps":         {Type: "integer"},
				"dropped_items":          {Type: "integer"},
				"compatibility":          {Type: "string"},
				"intentional_extensions": {Type: "array", Items: &oasSchema{Type: "string"}},
			},
			Required: []string{
				"status", "preserved_files", "executable_items", "preserved_apps",
				"dropped_items", "compatibility",
			},
		},
		"BrunoNativeProjection": {
			Type: "object",
			Properties: map[string]oasSchema{
				"eligible": {Type: "boolean"},
				"reasons":  {Type: "array", Items: &oasSchema{Type: "string"}},
			},
			Required: []string{"eligible"},
		},
		"BrunoNativeInspectResult": brunoInspectResultSchema("native_requests"),
		"BrunoInspectResult":       {Ref: "#/components/schemas/BrunoNativeInspectResult"},
		"BrunoImportConflict": {
			Type: "object",
			Properties: map[string]oasSchema{
				"id":                  {Type: "string"},
				"field":               {Type: "string"},
				"summary":             {Type: "string"},
				"required_resolution": {Type: "string"},
			},
			Required: []string{"id", "field", "summary", "required_resolution"},
		},
		"BrunoInspectResponse": {
			Type: "object",
			Properties: map[string]oasSchema{
				"import_session_id": {Type: "string"},
				"expires_at":        {Type: "string", Format: "date-time"},
				"result":            {Ref: "#/components/schemas/BrunoInspectResult"},
				"artifacts": {
					Type:  "array",
					Items: &oasSchema{Ref: "#/components/schemas/BrunoArtifactSummary"},
				},
				"execution_preflight": {Ref: "#/components/schemas/BrunoExecutionPreflight"},
				"parameterization": {
					Type:                 "object",
					Description:          "Retained response field; null for native request inspection.",
					AdditionalProperties: boolPointer(true),
					Nullable:             true,
				},
				"shared_values": {
					Type:                 "object",
					Description:          "Collection-wide values the operator supplies once; present for native request inspection only.",
					AdditionalProperties: boolPointer(true),
				},
				"previous_inputs": {
					Type:                 "object",
					Description:          "Shared-value candidate ID to how the scripts a re-import updates supplied that input: a value for every user, a data pool column or a generated value.",
					AdditionalProperties: &oasSchema{Ref: "#/components/schemas/BrunoPreviousInput"},
				},
			},
			Required: []string{
				"import_session_id", "expires_at", "result", "artifacts",
				"execution_preflight", "parameterization",
			},
		},
		"BrunoNativeCandidateExecutionPreflight": {
			Type:        "object",
			Description: "Replay readiness for one native Bruno candidate.",
			Properties: map[string]oasSchema{
				"candidate_id": {Type: "string"},
				"ready":        {Type: "boolean"},
				"blockers":     {Type: "array", Items: &oasSchema{Type: "string"}},
			},
			Required: []string{"candidate_id", "ready"},
		},
		"BrunoNativeExecutionPreflight": {
			Type:        "object",
			Description: "Aggregate and per-candidate native Bruno replay readiness.",
			Properties: map[string]oasSchema{
				"ready":    {Type: "boolean"},
				"blockers": {Type: "integer"},
				"candidates": {
					Type:  "array",
					Items: &oasSchema{Ref: "#/components/schemas/BrunoNativeCandidateExecutionPreflight"},
				},
			},
			Required: []string{"ready", "blockers", "candidates"},
		},
		"BrunoExecutionPreflight": {Ref: "#/components/schemas/BrunoNativeExecutionPreflight"},
		"BrunoNativeBatchCommitRequest": {
			Type:                 "object",
			AdditionalProperties: boolPointer(false),
			Properties: map[string]oasSchema{
				"project_id":        {Type: "string"},
				"import_session_id": {Type: "string"},
				"mode":              {Type: "string", Enum: []string{"create_native_batch"}},
				"folder_id":         {Type: "string"},
				"secret_bindings":   openAPIStringMap("Secret requirement ID to governed logical secret name."),
				"migrate_secrets":   {Type: "boolean"},
				"candidate_ids": {
					Type:        "array",
					Description: "Native candidate IDs to save. Omission selects every native candidate.",
					Items:       &oasSchema{Type: "string"},
				},
				"name_overrides": openAPIStringMap("Native candidate ID to requested script name."),
				"shared_values":  openAPIStringMap("Shared-value candidate ID to the operator-entered value applied across every selected script."),
				"acknowledged_finding_ids": {
					Type:  "array",
					Items: &oasSchema{Type: "string"},
				},
				"update_targets": {
					Type:                 "object",
					Description:          "Native candidate ID to the script an earlier import created from the same request. That script is updated as a new version instead of creating a duplicate. folder_id may be omitted when every selected candidate has an update target.",
					AdditionalProperties: &oasSchema{Ref: "#/components/schemas/BrunoNativeUpdateTarget"},
				},
				"header_repairs": {
					Type:                 "object",
					Description:          "bruno_invalid_header_value finding ID to the fix to apply. A repaired finding needs no acknowledgement.",
					AdditionalProperties: &oasSchema{Type: "string", Enum: []string{"trim", "remove"}},
				},
				"input_bindings": {
					Type:                 "object",
					Description:          "Shared-value candidate ID of a variable the source left unset to a data pool column ({{data.alias.column}}, read per virtual user) or a value generated each iteration. A candidate takes a binding or a shared_values value, not both.",
					AdditionalProperties: &oasSchema{Ref: "#/components/schemas/BrunoInputBinding"},
				},
				"parameterization_selections": {
					Type:                 "object",
					Description:          "Native candidate ID to the IDs of that candidate's parameterization suggestions to apply, such as a trace or message ID generated each iteration.",
					AdditionalProperties: &oasSchema{Type: "array", Items: &oasSchema{Type: "string"}},
				},
			},
			Required: []string{"project_id", "import_session_id", "mode"},
		},
		"BrunoCommitRequest": {Ref: "#/components/schemas/BrunoNativeBatchCommitRequest"},
		"BrunoInputBinding": {
			Type:                 "object",
			AdditionalProperties: boolPointer(false),
			Properties: map[string]oasSchema{
				"mode":      {Type: "string", Enum: []string{"data", "generated", "secret"}},
				"alias":     {Type: "string", Description: "Data binding alias the scenario binds to a pool."},
				"column":    {Type: "string"},
				"generator": {Type: "string", Enum: []string{"uuid", "timestamp", "digits"}},
				"digits":    {Type: "integer", Description: "Length of a generated digits value, 1 to 64."},
				"secret":    {Type: "string", Description: "Project secret a credential input reads (capital letters, digits, underscores)."},
			},
			Required: []string{"mode"},
		},
		"BrunoPreviousInput": {
			Type: "object",
			Properties: map[string]oasSchema{
				"mode":      {Type: "string", Enum: []string{"value", "data", "generated", "secret"}},
				"value":     {Type: "string"},
				"alias":     {Type: "string"},
				"column":    {Type: "string"},
				"generator": {Type: "string", Enum: []string{"uuid", "timestamp", "digits"}},
				"digits":    {Type: "integer"},
				"reference": {Type: "string", Description: "Project secret a credential input reads, {{secret.NAME}}."},
			},
			Required: []string{"mode"},
		},
		"ImportSourceRevision": {
			Type: "object",
			Properties: map[string]oasSchema{
				"id":                      {Type: "string"},
				"script_id":               {Type: "string"},
				"project_id":              {Type: "string"},
				"parent_revision_id":      {Type: "string"},
				"raw_artifact_id":         {Type: "string"},
				"sanitized_artifact_id":   {Type: "string"},
				"source_format":           {Type: "string"},
				"compatibility_version":   {Type: "string"},
				"identity_map":            openAPIStringMap("Stable source-to-script identities."),
				"import_options":          openAPIFreeformObject("Retained source import options."),
				"dependency_snapshot_ref": {Type: "string"},
				"security_profile":        openAPIFreeformObject("Retained source security metadata."),
				"change_summary":          openAPIFreeformObject("Retained source change summary."),
				"secret_bindings":         openAPIStringMap("Source requirement to governed logical secret."),
				"active":                  {Type: "boolean"},
				"created_by":              {Type: "string"},
				"created_at":              {Type: "string", Format: "date-time"},
			},
			Required: []string{
				"id", "script_id", "project_id", "sanitized_artifact_id", "source_format",
				"compatibility_version", "active", "created_at",
			},
		},
		"BrunoNativeBatchCommitResponse": {
			Type:        "object",
			Description: "Atomic receipt for a create-only native Bruno batch.",
			Properties: map[string]oasSchema{
				"batch_id": {Type: "string"},
				"scripts": {
					Type:  "array",
					Items: &oasSchema{Ref: "#/components/schemas/APEXScript"},
				},
				"count":             {Type: "integer"},
				"created":           {Type: "boolean"},
				"idempotent_replay": {Type: "boolean"},
				"secret_migrations": {Type: "array", Items: &oasSchema{Type: "object", AdditionalProperties: boolPointer(true)}},
				"updated_script_ids": {
					Type:        "array",
					Description: "Scripts this batch updated as a new version rather than created.",
					Items:       &oasSchema{Type: "string"},
				},
				"unconfigured_secret_names": {
					Type:        "array",
					Description: "Project secrets the saved scripts reference that have no value yet. Configure them before a run. Omitted on an idempotent replay.",
					Items:       &oasSchema{Type: "string"},
				},
			},
			Required: []string{"batch_id", "scripts", "count", "created", "idempotent_replay"},
		},
		"BrunoCommitResponse": {Ref: "#/components/schemas/BrunoNativeBatchCommitResponse"},
		"BrunoCommitErrorResponse": {
			Type:        "object",
			Description: "Bruno commit failure, including retry and partial secret-migration recovery state when applicable.",
			Properties: map[string]oasSchema{
				"error":        {Type: "string"},
				"code":         {Type: "string"},
				"reason":       {Type: "string"},
				"project_id":   {Type: "string"},
				"candidate_id": {Type: "string"},
				"commit_state": {Type: "string"},
				"script_committed": {
					OneOf: []oasSchema{{Type: "boolean"}, {Type: "string"}},
				},
				"partial":                       {Type: "boolean"},
				"session_retained":              {Type: "boolean"},
				"manual_retry_safe":             {Type: "boolean"},
				"idempotency_session_id":        {Type: "string"},
				"import_session_consumed":       {Type: "boolean"},
				"manual_configuration_required": {Type: "boolean"},
				"committed_secrets_retained":    {Type: "boolean"},
				"source_values_retained":        {Type: "boolean"},
				"source_values_cleared":         {Type: "boolean"},
				"source_values_disclosed":       {Type: "boolean"},
				"failed_name":                   {Type: "string"},
				"resolution":                    {Type: "string"},
				"migrated_count":                {Type: "integer"},
				"committed_names":               {Type: "array", Items: &oasSchema{Type: "string"}},
				"remaining_names":               {Type: "array", Items: &oasSchema{Type: "string"}},
				"committed_secret_names":        {Type: "array", Items: &oasSchema{Type: "string"}},
				"migrated_secret_names":         {Type: "array", Items: &oasSchema{Type: "string"}},
				"findings":                      {Type: "array", Items: &oasSchema{Type: "string"}},
				"requirements":                  {Type: "array", Items: &oasSchema{Ref: "#/components/schemas/BrunoSecretRequirement"}},
				"conflicts":                     {Type: "array", Items: &oasSchema{Ref: "#/components/schemas/BrunoImportConflict"}},
				"issues":                        {Type: "array", Items: &oasSchema{Type: "string"}},
				"migrated":                      {Type: "array", Items: &oasSchema{Type: "object", AdditionalProperties: boolPointer(true)}},
				"secret_migrations":             {Type: "array", Items: &oasSchema{Type: "object", AdditionalProperties: boolPointer(true)}},
			},
			Required: []string{"error"},
		},
	}
}

func brunoInspectResultSchema(outputMode string) oasSchema {
	properties := map[string]oasSchema{
		"schema_version": {Type: "string"},
		"output_mode":    {Type: "string", Enum: []string{outputMode}},
		"source_format":  {Type: "string"},
		"root_path":      {Type: "string"},
		"manifest":       {Ref: "#/components/schemas/BrunoManifest"},
		"collection":     {Ref: "#/components/schemas/BrunoCollection"},
		"environments": {
			Type:  "array",
			Items: &oasSchema{Ref: "#/components/schemas/BrunoEnvironment"},
		},
		"capabilities": {Ref: "#/components/schemas/BrunoCapabilities"},
		"secret_requirements": {
			Type:  "array",
			Items: &oasSchema{Ref: "#/components/schemas/BrunoSecretRequirement"},
		},
		"security_findings": {
			Type:  "array",
			Items: &oasSchema{Ref: "#/components/schemas/BrunoSecurityFinding"},
		},
		"fidelity":           {Ref: "#/components/schemas/BrunoFidelityReport"},
		"native_projection":  {Ref: "#/components/schemas/BrunoNativeProjection"},
		"missing_variables":  {Type: "array", Items: &oasSchema{Type: "string"}},
		"inspected_at":       {Type: "string", Format: "date-time"},
		"skipped_file_count": {Type: "integer", Description: "All skipped files; skipped_files lists at most the first 100 in path order."},
		"skipped_files": {
			Type:        "array",
			Description: "Uploaded files that are not collection content: operating-system or version-control files, files Bruno's ignore list excludes, and gitignored files Bruno does not load itself.",
			Items: &oasSchema{
				Type: "object",
				Properties: map[string]oasSchema{
					"path":   {Type: "string"},
					"reason": {Type: "string", Enum: []string{"operating_system_file", "version_control_file", "ignore_list"}},
				},
				Required: []string{"path", "reason"},
			},
		},
		"kept_headers": {
			Type:        "array",
			Description: "Sensitive-looking headers, such as cookies and session ids, kept as imported because they carry recorded state rather than credentials.",
			Items: &oasSchema{
				Type: "object",
				Properties: map[string]oasSchema{
					"name":     {Type: "string"},
					"requests": {Type: "integer", Description: "Requests that send this header."},
				},
				Required: []string{"name", "requests"},
			},
		},
	}
	required := []string{
		"schema_version", "output_mode", "source_format", "root_path", "manifest",
		"collection", "capabilities", "fidelity", "native_projection", "inspected_at",
	}
	if outputMode == "native_requests" {
		properties["scripts"] = oasSchema{
			Type:        "array",
			Description: "Native candidates in deterministic Bruno sequence/path order.",
			Items:       &oasSchema{Ref: "#/components/schemas/BrunoNativeScriptCandidate"},
		}
		required = append(required, "scripts")
	}
	return oasSchema{
		Type:       "object",
		Properties: properties,
		Required:   required,
	}
}

func openAPIFreeformObject(description string) oasSchema {
	return oasSchema{
		Type:                 "object",
		Description:          description,
		AdditionalProperties: boolPointer(true),
	}
}

func openAPIStringMap(description string) oasSchema {
	return oasSchema{
		Type:                 "object",
		Description:          description,
		AdditionalProperties: &oasSchema{Type: "string"},
	}
}

func requestBodyForRoute(method, path string) *oasRequestBody {
	if method == http.MethodPut && strings.HasSuffix(path, "/library-assignment") {
		return jsonRequestBody("LibraryAssignmentRequest", true)
	}
	if method == http.MethodPost && path == "/api/v1/projects/{id}/releases" {
		return jsonRequestBody("ProjectReleaseCreateRequest", true)
	}
	if method == http.MethodPost && path == "/api/v1/scripts/authoring/sessions" {
		return jsonRequestBody("AuthoringSessionCreateRequest", true)
	}
	if method == http.MethodPost && path == "/api/v1/scripts/import/bruno/inspect" {
		return brunoInspectRequestBody()
	}
	if method == http.MethodPost && path == "/api/v1/scripts/import/bruno/commit" {
		return jsonRequestBody("BrunoCommitRequest", true)
	}
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

func responseSchemasForRoute(method, path string, responses map[string]oasResponse) map[string]oasResponse {
	if method == http.MethodPut && strings.HasSuffix(path, "/library-assignment") {
		setJSONResponseSchema(responses, "200", "LibraryAssignment")
		return responses
	}
	switch {
	case method == http.MethodPost && (path == "/api/v1/scripts/import/jmeter" || path == "/api/v1/scripts/import/jmeter/validate"):
		setJSONResponseSchema(responses, "200", "JMeterImportReviewResponse")
	case method == http.MethodGet && path == "/api/v1/system/readiness":
		return systemReadinessOpenAPIResponses()
	case method == http.MethodGet && path == "/api/v1/projects/{id}/releases":
		setJSONResponseSchema(responses, "200", "ReleaseFamilyListResponse")
	case method == http.MethodPost && path == "/api/v1/projects/{id}/releases":
		setJSONResponseSchema(responses, "201", "ReleaseFamily")
	case method == http.MethodGet && strings.HasSuffix(path, "/swagger.yaml"):
		setResponseMediaSchema(responses, "2XX", "application/yaml", oasSchema{Type: "string"})
	case method == http.MethodGet && strings.HasSuffix(path, "/endpoint-catalog"):
		setResponseMediaSchema(responses, "2XX", "application/json", oasSchema{Type: "object"})
	case method == http.MethodPost && path == "/api/v1/scripts/import/bruno/inspect":
		setJSONResponseSchema(responses, "200", "BrunoInspectResponse")
	case method == http.MethodPost && path == "/api/v1/scripts/import/bruno/commit":
		setJSONResponseSchema(responses, "200", "BrunoCommitResponse")
		setJSONResponseSchema(responses, "201", "BrunoCommitResponse")
		for status := range responses {
			if strings.HasPrefix(status, "4") || strings.HasPrefix(status, "5") {
				setJSONResponseSchema(responses, status, "BrunoCommitErrorResponse")
			}
		}
	}
	return responses
}

func systemReadinessOpenAPIResponses() map[string]oasResponse {
	media := map[string]oasMediaType{
		"application/json": {Schema: oasSchema{Ref: "#/components/schemas/SystemReadinessResponse"}},
	}
	return map[string]oasResponse{
		"200": {
			Description: "The controller is ready or operational with advisory degradation.",
			Content:     media,
		},
		"503": {
			Description: "At least one required dependency is not ready.",
			Content: map[string]oasMediaType{
				"application/json": {Schema: oasSchema{Ref: "#/components/schemas/SystemReadinessResponse"}},
			},
		},
	}
}

func setResponseMediaSchema(responses map[string]oasResponse, status, mediaType string, schema oasSchema) {
	response, ok := responses[status]
	if !ok {
		return
	}
	if response.Content == nil {
		response.Content = make(map[string]oasMediaType)
	}
	response.Content[mediaType] = oasMediaType{Schema: schema}
	responses[status] = response
}

func setJSONResponseSchema(responses map[string]oasResponse, status, schemaName string) {
	response, ok := responses[status]
	if !ok {
		return
	}
	if response.Content == nil {
		response.Content = make(map[string]oasMediaType)
	}
	response.Content["application/json"] = oasMediaType{
		Schema: oasSchema{Ref: "#/components/schemas/" + schemaName},
	}
	responses[status] = response
}

func boolPointer(value bool) *bool {
	return &value
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

func brunoInspectRequestBody() *oasRequestBody {
	multipartSchema := oasSchema{
		Type:        "object",
		Description: "Multipart Bruno source upload. A directory may use repeated file parts or dynamic file:<URL-encoded-relative-path> part names.",
		Properties: map[string]oasSchema{
			"project_id": {Type: "string"},
			"name":       {Type: "string"},
			"root_path":  {Type: "string"},
			"options": {
				Type:        "string",
				Description: "JSON-encoded Bruno inspection options, including output_mode.",
			},
			"file": {
				Type:        "string",
				Format:      "binary",
				Description: "A single Bruno file or archive.",
			},
			"files": {
				Type:        "array",
				Description: "Repeated Bruno directory files; preserve each part filename or X-APEXLoad-Relative-Path.",
				Items:       &oasSchema{Type: "string", Format: "binary"},
			},
		},
		AdditionalProperties: boolPointer(true),
	}
	return &oasRequestBody{
		Required: true,
		Content: map[string]oasMediaType{
			"application/json": {
				Schema: oasSchema{Ref: "#/components/schemas/BrunoInspectRequest"},
			},
			"multipart/form-data": {
				Schema: multipartSchema,
			},
			"application/octet-stream": {
				Schema: oasSchema{
					Type:        "string",
					Format:      "binary",
					Description: "Raw Bruno file or archive. Supply filename and inspection fields as query parameters.",
				},
			},
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
			roles = routeRolesText(r.Method, r.Path, r.AuthMiddleware)
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
			Server:        documentedRouteServer(r.Method, r.Path),
			Conditional:   documentedRouteCondition(r.Method, r.Path),
		})
	}
	return docs
}

// routeRolesText resolves access from the canonical registry first, preserving
// the pre-migration presentation of derived scopes, then falls back to runtime
// authorization logic for legacy routes.
func routeRolesText(method, path, authMiddleware string) string {
	if method == http.MethodGet && path == bridgeStatusPath {
		return "Recording-agent bridge token and session via " + bridgeTokenHeader + " and " + bridgeSessionHeader
	}
	if collectorTokenRouteHandler(method, path) != "" {
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
	requirement, ok := documentedAccessRequirementFromAPIRouteCatalogs(method, path)
	if !ok {
		requirement, ok = accessRequirementForRequest(req)
	}
	if !ok {
		requirement, ok = documentedSpecializedAccessRequirement(method, path, authMiddleware)
	}
	if !ok {
		return "Requires Authentication"
	}
	accessText := renderAccessRoles(requirement)
	if method != http.MethodGet || len(requirement.ProjectRoles) > 0 {
		return accessText
	}

	// Query-conditional legacy routes are globally readable when no project is
	// selected and enforce project RBAC when project_id is supplied. Probe that
	// canonical runtime branch so generated Swagger and the endpoint catalog
	// describe both contracts instead of only the empty-query variant.
	projectReq, err := http.NewRequest(method, "http://localhost"+concrete+"?project_id=x", nil)
	if err != nil {
		return accessText
	}
	for _, m := range pathParamRe.FindAllStringSubmatch(path, -1) {
		projectReq.SetPathValue(m[1], "x")
	}
	projectRequirement, ok := accessRequirementForRequest(projectReq)
	if !ok || projectRequirement.ProjectLookup == nil || len(projectRequirement.ProjectRoles) == 0 {
		return accessText
	}
	return accessText + "; when project_id is supplied, project roles: " + renderRoleNames(projectRequirement.ProjectRoles)
}

func inHandlerAuthenticatedRoute(method, path, handler string) bool {
	if method == http.MethodGet && path == "/api/v1/auth/diagnostics" {
		return true
	}
	expected := collectorTokenRouteHandler(method, path)
	return expected != "" && handler == expected
}

func renderAccessRoles(req accessRequirement) string {
	parts := []string{"Requires authentication"}
	appendRoles := func(scope string, roles []string) {
		if len(roles) == 0 {
			return
		}
		parts = append(parts, scope+" roles: "+renderRoleNames(roles))
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

func renderRoleNames(roles []string) string {
	names := make([]string, 0, len(roles))
	for _, role := range roles {
		names = append(names, roleDisplayName(role))
	}
	return strings.Join(names, ", ")
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

// chainAuthMiddlewareName returns the authentication middleware identifier from
// the chain wrapping a handler — local `auth` or a specialized `*Auth` session
// middleware (e.g. browserRecordingSessionEditAuth). Handler references are
// SelectorExprs (s.handleXxx), so they are never mistaken for middleware Idents
// even when the handler name contains "Auth".
func chainAuthMiddlewareName(expr ast.Expr) string {
	found := ""
	ast.Inspect(expr, func(n ast.Node) bool {
		if found != "" {
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
			found = ident.Name
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
	if strings.HasPrefix(path, "/api/v1/") || path == "/metrics" {
		return true
	}
	switch method + " " + path {
	case "GET /.well-known/apexload-ci", "GET /desktop/bootstrap", "GET /desktop/download":
		return true
	default:
		return false
	}
}

func documentedRouteServer(_ string, path string) string {
	if path == bridgePairPath || path == bridgeStatusPath {
		return "Local recording-agent bridge (address selected during agent pairing)"
	}
	return ""
}

func documentedRouteCondition(method, path string) string {
	switch method + " " + path {
	case "GET /metrics":
		return "Served on the controller API address only when APEXLOAD_METRICS_ADDR is unset; otherwise use the dedicated metrics listener."
	case "GET /desktop/bootstrap", "GET /desktop/download", "POST /api/v1/desktop/browser-downloads":
		return "Available only when the controller is running with desktop capabilities enabled."
	default:
		return ""
	}
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

// Collector endpoints authenticate inside their exact handlers. Keep the
// documentation allowlist narrow so a similarly named future route cannot
// silently inherit a claim of authentication.
func collectorTokenRouteHandler(method, path string) string {
	switch method + " " + path {
	case "POST /api/v1/monitoring/collectors/{id}/heartbeat":
		return "handleCollectorHeartbeat"
	case "GET /api/v1/monitoring/collectors/{id}/jobs":
		return "handlePollRunMonitorJobs"
	case "POST /api/v1/monitoring/collectors/{id}/jobs/{job_id}/samples":
		return "handleRunMonitorSamples"
	case "POST /api/v1/monitoring/collectors/{id}/jobs/{job_id}/management-polls":
		return "handleManagementPoll"
	}
	return ""
}
