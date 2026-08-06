package api

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"net/http"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	yaml "gopkg.in/yaml.v3"
)

const (
	swaggerOutputPath      = "../../docs/swagger.yaml"
	swaggerEmbedOutputPath = "apidocs/swagger.yaml"
	catalogOutputPath      = "../../docs/api-endpoint-catalog.json"
	catalogEmbedOutputPath = "apidocs/api-endpoint-catalog.json"
)

// TestSwaggerYAMLIsFresh enforces that docs/swagger.yaml is the exact output of the
// generator over the current routing layer. If routes change, run:
//
//	go generate ./pkg/api
func TestSwaggerYAMLIsFresh(t *testing.T) {
	got, err := GenerateOpenAPIYAML("server.go")
	if err != nil {
		t.Fatalf("GenerateOpenAPIYAML: %v", err)
	}
	for _, path := range []string{swaggerOutputPath, swaggerEmbedOutputPath} {
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("%s is stale; run `go generate ./pkg/api` to regenerate it", path)
		}
	}
}

func TestGenerateOpenAPIYAMLIsDeterministicAcrossRepeatedAnalysis(t *testing.T) {
	want, err := GenerateOpenAPIYAML("server.go")
	if err != nil {
		t.Fatalf("GenerateOpenAPIYAML: %v", err)
	}
	for iteration := 0; iteration < 20; iteration++ {
		got, err := GenerateOpenAPIYAML("server.go")
		if err != nil {
			t.Fatalf("GenerateOpenAPIYAML iteration %d: %v", iteration, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("GenerateOpenAPIYAML iteration %d was not byte-for-byte deterministic", iteration)
		}
	}
}

func TestGenerateOpenAPIYAMLAcceptsRoutingPackageDirectory(t *testing.T) {
	fromFile, err := GenerateOpenAPIYAML("server.go")
	if err != nil {
		t.Fatalf("GenerateOpenAPIYAML file anchor: %v", err)
	}
	fromDirectory, err := GenerateOpenAPIYAML(".")
	if err != nil {
		t.Fatalf("GenerateOpenAPIYAML directory anchor: %v", err)
	}
	if !bytes.Equal(fromDirectory, fromFile) {
		t.Fatal("directory-anchored route discovery differs from server.go-anchored discovery")
	}
}

func TestEndpointCatalogSourceIsInvocationIndependent(t *testing.T) {
	fromPackage, err := GenerateEndpointCatalogJSON("server.go")
	if err != nil {
		t.Fatal(err)
	}
	fromRepositoryRoot, err := GenerateEndpointCatalogJSON("../../pkg/api/server.go")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(fromPackage, fromRepositoryRoot) {
		t.Fatal("endpoint catalog depends on the invocation's server path")
	}
}

func TestBrunoInspectOpenAPIRequestBodyDocumentsAllSupportedUploads(t *testing.T) {
	body := requestBodyForRoute(http.MethodPost, "/api/v1/scripts/import/bruno/inspect")
	if body == nil || !body.Required {
		t.Fatalf("Bruno inspect request body = %#v", body)
	}
	for _, mediaType := range []string{"application/json", "multipart/form-data", "application/octet-stream"} {
		if _, ok := body.Content[mediaType]; !ok {
			t.Errorf("Bruno inspect request body is missing %s", mediaType)
		}
	}
	if schema := body.Content["application/octet-stream"].Schema; schema.Type != "string" || schema.Format != "binary" {
		t.Fatalf("octet-stream schema = %#v", schema)
	}
	if schema := body.Content["multipart/form-data"].Schema; schema.Type != "object" {
		t.Fatalf("multipart schema = %#v", schema)
	}
}

func TestBrunoOpenAPIContractsDescribeNativeModesAndRecovery(t *testing.T) {
	inspectRoute := discoveredRoute{
		Method: http.MethodPost,
		Path:   "/api/v1/scripts/import/bruno/inspect",
	}
	outputMode := queryParameterSchema(inspectRoute, "output_mode", false)
	if !reflect.DeepEqual(outputMode.Enum, []string{"canonical", "native_requests"}) {
		t.Fatalf("Bruno output_mode enum = %v", outputMode.Enum)
	}

	schemas := brunoOpenAPIComponentSchemas()
	preflight := schemas["BrunoExecutionPreflight"]
	if len(preflight.OneOf) != 2 {
		t.Fatalf("Bruno execution preflight variants = %#v", preflight.OneOf)
	}
	nativePreflight := schemas["BrunoNativeExecutionPreflight"]
	if blockers := nativePreflight.Properties["blockers"]; blockers.Type != "integer" {
		t.Fatalf("native preflight blockers schema = %#v, want integer count", blockers)
	}
	if candidates := nativePreflight.Properties["candidates"]; candidates.Items == nil ||
		candidates.Items.Ref != "#/components/schemas/BrunoNativeCandidateExecutionPreflight" {
		t.Fatalf("native preflight candidates schema = %#v", candidates)
	}

	recovery := schemas["BrunoCommitErrorResponse"]
	for _, field := range []string{
		"requirements", "findings", "session_retained", "manual_retry_safe",
		"idempotency_session_id", "committed_names", "remaining_names",
	} {
		if _, ok := recovery.Properties[field]; !ok {
			t.Errorf("Bruno commit recovery schema is missing %q", field)
		}
	}

	responses := map[string]oasResponse{
		"201": {Description: "created"},
		"409": {Description: "conflict"},
		"503": {Description: "unavailable"},
	}
	responses = responseSchemasForRoute(
		http.MethodPost,
		"/api/v1/scripts/import/bruno/commit",
		responses,
	)
	for _, status := range []string{"409", "503"} {
		got := responses[status].Content["application/json"].Schema.Ref
		if got != "#/components/schemas/BrunoCommitErrorResponse" {
			t.Errorf("Bruno commit %s response schema = %q", status, got)
		}
	}
}

// TestEndpointCatalogJSONIsFresh enforces that docs/api-endpoint-catalog.json is
// regenerated with the OpenAPI spec after route changes.
func TestEndpointCatalogJSONIsFresh(t *testing.T) {
	got, err := GenerateEndpointCatalogJSON("server.go")
	if err != nil {
		t.Fatalf("GenerateEndpointCatalogJSON: %v", err)
	}
	for _, path := range []string{catalogOutputPath, catalogEmbedOutputPath} {
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("%s is stale; run `go generate ./pkg/api` to regenerate it", path)
		}
	}
}

// TestGeneratedSwaggerIsValid sanity-checks the generated spec: it parses as YAML,
// declares OpenAPI 3.0.x, has a non-empty paths object, and every operation carries
// an operationId and at least one response.
func TestGeneratedSwaggerIsValid(t *testing.T) {
	data, err := GenerateOpenAPIYAML("server.go")
	if err != nil {
		t.Fatalf("GenerateOpenAPIYAML: %v", err)
	}

	var spec struct {
		OpenAPI string `yaml:"openapi"`
		Paths   map[string]map[string]struct {
			OperationID string                 `yaml:"operationId"`
			Responses   map[string]interface{} `yaml:"responses"`
		} `yaml:"paths"`
	}
	if err := yaml.Unmarshal(data, &spec); err != nil {
		t.Fatalf("generated spec is not valid YAML: %v", err)
	}
	if spec.OpenAPI != "3.0.3" {
		t.Fatalf("openapi version = %q, want 3.0.3", spec.OpenAPI)
	}
	if len(spec.Paths) == 0 {
		t.Fatal("generated spec has no paths")
	}

	seenOpIDs := map[string]string{}
	for path, methods := range spec.Paths {
		for method, op := range methods {
			if op.OperationID == "" {
				t.Errorf("%s %s: missing operationId", method, path)
			}
			if len(op.Responses) == 0 {
				t.Errorf("%s %s: missing responses", method, path)
			}
			if prev, dup := seenOpIDs[op.OperationID]; dup {
				t.Errorf("duplicate operationId %q on %s %s and %s", op.OperationID, method, path, prev)
			}
			seenOpIDs[op.OperationID] = method + " " + path
		}
	}
}

func TestDataPoolCursorPaginationIsDocumented(t *testing.T) {
	data, err := GenerateOpenAPIYAML("server.go")
	if err != nil {
		t.Fatalf("GenerateOpenAPIYAML: %v", err)
	}
	var spec struct {
		Paths map[string]map[string]oasOperation `yaml:"paths"`
	}
	if err := yaml.Unmarshal(data, &spec); err != nil {
		t.Fatalf("decode generated OpenAPI: %v", err)
	}
	for _, path := range []string{"/api/v1/data-pools/{id}", "/api/v1/data-pools/{id}/rows"} {
		operation, ok := spec.Paths[path]["get"]
		if !ok {
			t.Fatalf("generated OpenAPI is missing GET %s", path)
		}
		foundCursor := false
		for _, parameter := range operation.Parameters {
			if parameter.Name == "cursor" && parameter.In == "query" {
				foundCursor = true
				break
			}
		}
		if !foundCursor {
			t.Errorf("GET %s does not document the cursor query parameter", path)
		}
		for _, status := range []string{"400", "409", "501"} {
			if _, ok := operation.Responses[status]; !ok {
				t.Errorf("GET %s does not document response %s", path, status)
			}
		}
		responseSchema := operation.Responses["200"].Content["application/json"].Schema
		for _, field := range []string{"rows", "count", "total", "limit", "offset", "revision", "has_more", "next_cursor"} {
			if _, ok := responseSchema.Properties[field]; !ok {
				t.Errorf("GET %s response does not type cursor field %q: %#v", path, field, responseSchema)
			}
		}
		rowItems := responseSchema.Properties["rows"].Items
		if rowItems == nil {
			t.Errorf("GET %s rows has no item schema", path)
		} else {
			for _, field := range []string{"id", "pool_id", "data", "status", "version", "created_at", "updated_at"} {
				if _, ok := rowItems.Properties[field]; !ok {
					t.Errorf("GET %s row item is missing %q: %#v", path, field, rowItems)
				}
			}
			if !openAPIStringSliceContains(rowItems.Required, "id") ||
				!openAPIStringSliceContains(rowItems.Required, "data") ||
				openAPIStringSliceContains(rowItems.Required, "status") {
				t.Errorf("GET %s row item required fields = %v, want id/data required and status optional", path, rowItems.Required)
			}
			additional, ok := rowItems.Properties["data"].AdditionalProperties.(map[string]any)
			if !ok || additional["type"] != "string" {
				t.Errorf("GET %s row data additionalProperties = %#v, want string values", path, rowItems.Properties["data"].AdditionalProperties)
			}
		}
		if path == "/api/v1/data-pools/{id}" {
			dataPool, ok := responseSchema.Properties["data_pool"]
			if !ok {
				t.Errorf("GET %s response does not type data_pool: %#v", path, responseSchema)
			} else {
				for _, field := range []string{"id", "name", "description", "schema", "project_id", "created_at", "updated_at", "archived_at"} {
					if _, ok := dataPool.Properties[field]; !ok {
						t.Errorf("GET %s data_pool is missing %q: %#v", path, field, dataPool)
					}
				}
				archivedAt := dataPool.Properties["archived_at"]
				if archivedAt.Type != "string" || archivedAt.Format != "date-time" || !archivedAt.Nullable ||
					openAPIStringSliceContains(dataPool.Required, "archived_at") {
					t.Errorf("GET %s data_pool archived_at = %#v required=%v, want optional nullable date-time", path, archivedAt, dataPool.Required)
				}
			}
		}
	}
}

func TestDataPoolImportExportOpenAPIContractIsExact(t *testing.T) {
	data, err := GenerateOpenAPIYAML("server.go")
	if err != nil {
		t.Fatalf("GenerateOpenAPIYAML: %v", err)
	}
	var spec struct {
		Paths map[string]map[string]oasOperation `yaml:"paths"`
	}
	if err := yaml.Unmarshal(data, &spec); err != nil {
		t.Fatalf("decode generated OpenAPI: %v", err)
	}

	importOperation := spec.Paths["/api/v1/data-pools/{id}/rows/import"]["post"]
	if importOperation.RequestBody == nil || !importOperation.RequestBody.Required {
		t.Fatal("data-pool import must require a multipart request body")
	}
	importSchema := importOperation.RequestBody.Content["multipart/form-data"].Schema
	if !openAPIStringSliceContains(importSchema.Required, "file") {
		t.Errorf("data-pool import required fields = %v, want file", importSchema.Required)
	}
	if _, ok := importOperation.Responses["404"]; !ok {
		t.Error("data-pool import does not document its 404 response")
	}

	exportOperation := spec.Paths["/api/v1/data-pools/{id}/rows/export"]["get"]
	if _, ok := exportOperation.Responses["2XX"]; ok {
		t.Error("data-pool export still advertises a generic 2XX response")
	}
	success, ok := exportOperation.Responses["200"]
	if !ok {
		t.Fatal("data-pool export does not document exact 200 response")
	}
	if len(success.Content) != 1 || success.Content["text/csv"].Schema.Type != "string" {
		t.Errorf("data-pool export 200 content = %#v, want text/csv only", success.Content)
	}
	for _, header := range []string{"Cache-Control", "Content-Disposition", "X-APEXLoad-Sensitivity"} {
		if _, ok := success.Headers[header]; !ok {
			t.Errorf("data-pool export 200 response is missing %s", header)
		}
	}
	if _, ok := exportOperation.Responses["404"]; !ok {
		t.Error("data-pool export does not document its 404 response")
	}
}

func TestInferredJSONRequestRequiredFieldsFollowRouteSemantics(t *testing.T) {
	data, err := GenerateOpenAPIYAML("server.go")
	if err != nil {
		t.Fatalf("GenerateOpenAPIYAML: %v", err)
	}
	var spec struct {
		Paths map[string]map[string]oasOperation `yaml:"paths"`
	}
	if err := yaml.Unmarshal(data, &spec); err != nil {
		t.Fatalf("decode generated OpenAPI: %v", err)
	}

	applicationUpdate := spec.Paths["/api/v1/applications/{id}"]["put"].
		RequestBody.Content["application/json"].Schema
	if !openAPIStringSliceContains(applicationUpdate.Required, "updated_at") {
		t.Errorf("application update required fields = %v, want updated_at for optimistic concurrency", applicationUpdate.Required)
	}

	for _, route := range []struct {
		path   string
		method string
	}{
		{path: "/api/v1/scripts/{id}/move", method: "patch"},
		{path: "/api/v1/tests/{id}/move", method: "patch"},
	} {
		schema := spec.Paths[route.path][route.method].RequestBody.Content["application/json"].Schema
		if openAPIStringSliceContains(schema.Required, "folder_id") {
			t.Errorf("%s %s requires folder_id even though omission is accepted", route.method, route.path)
		}
		folderID, ok := schema.Properties["folder_id"]
		if !ok {
			t.Errorf("%s %s is missing folder_id", route.method, route.path)
		} else if folderID.Type != "string" || !folderID.Nullable {
			t.Errorf("%s %s folder_id = %#v, want nullable string", route.method, route.path, folderID)
		}
	}

	poolCreate := spec.Paths["/api/v1/data-pools"]["post"].
		RequestBody.Content["application/json"].Schema
	if !reflect.DeepEqual(poolCreate.Required, []string{"name"}) {
		t.Errorf("data-pool create required fields = %v, want [name]", poolCreate.Required)
	}
	for _, serverOwned := range []string{"id", "created_at", "updated_at", "archived_at"} {
		if _, ok := poolCreate.Properties[serverOwned]; ok {
			t.Errorf("data-pool create exposes server-owned field %q", serverOwned)
		}
	}
	for _, accepted := range []string{"name", "description", "schema", "project_id"} {
		if _, ok := poolCreate.Properties[accepted]; !ok {
			t.Errorf("data-pool create is missing accepted field %q: %#v", accepted, poolCreate)
		}
	}
	for _, operation := range []struct {
		name   string
		schema oasSchema
	}{
		{
			name: "create",
			schema: spec.Paths["/api/v1/data-pools"]["post"].
				Responses["201"].Content["application/json"].Schema,
		},
		{
			name: "update",
			schema: spec.Paths["/api/v1/data-pools/{id}"]["put"].
				Responses["200"].Content["application/json"].Schema,
		},
	} {
		for _, field := range []string{"id", "name", "description", "schema", "project_id", "created_at", "updated_at", "archived_at"} {
			if _, ok := operation.schema.Properties[field]; !ok {
				t.Errorf("data-pool %s response is missing typed field %q: %#v", operation.name, field, operation.schema)
			}
		}
		archivedAt := operation.schema.Properties["archived_at"]
		if archivedAt.Type != "string" || archivedAt.Format != "date-time" || !archivedAt.Nullable {
			t.Errorf("data-pool %s response archived_at = %#v, want nullable date-time", operation.name, archivedAt)
		}
	}

	rowCreate := spec.Paths["/api/v1/data-pools/{id}/rows"]["post"].
		RequestBody.Content["application/json"].Schema
	if !reflect.DeepEqual(rowCreate.Required, []string{"data"}) {
		t.Errorf("data-pool row create required fields = %v, want [data]", rowCreate.Required)
	}
	for _, serverOwned := range []string{"id", "pool_id", "version", "created_at", "updated_at"} {
		if _, ok := rowCreate.Properties[serverOwned]; ok {
			t.Errorf("data-pool row create exposes server-owned field %q", serverOwned)
		}
	}
	for _, operation := range []struct {
		name   string
		schema oasSchema
	}{
		{
			name: "create",
			schema: spec.Paths["/api/v1/data-pools/{id}/rows"]["post"].
				Responses["201"].Content["application/json"].Schema,
		},
		{
			name: "update",
			schema: spec.Paths["/api/v1/data-pools/{id}/rows/{row_id}"]["put"].
				Responses["200"].Content["application/json"].Schema,
		},
	} {
		for _, field := range []string{"id", "pool_id", "data", "status", "version", "created_at", "updated_at"} {
			if _, ok := operation.schema.Properties[field]; !ok {
				t.Errorf("data-pool row %s response is missing typed field %q: %#v", operation.name, field, operation.schema)
			}
		}
	}

	for _, contract := range []struct {
		path     string
		required []string
	}{
		{path: "/api/v1/auth/users", required: []string{"subject"}},
		{path: "/api/v1/auth/service-accounts", required: []string{"name"}},
	} {
		schema := spec.Paths[contract.path]["post"].RequestBody.Content["application/json"].Schema
		if !reflect.DeepEqual(schema.Required, contract.required) {
			t.Errorf("POST %s required fields = %v, want %v", contract.path, schema.Required, contract.required)
		}
	}
}

func TestClearOpenAPISchemaRequiredRecursesThroughRequestShapes(t *testing.T) {
	additional := &oasSchema{
		Type:     "object",
		Required: []string{"map_value"},
		Properties: map[string]oasSchema{
			"map_value": {Type: "string"},
		},
	}
	schema := oasSchema{
		Type:     "object",
		Required: []string{"root"},
		Properties: map[string]oasSchema{
			"root": {
				Type:     "object",
				Required: []string{"nested"},
				Properties: map[string]oasSchema{
					"nested": {Type: "string"},
				},
			},
			"list": {
				Type: "array",
				Items: &oasSchema{
					Type:     "object",
					Required: []string{"item"},
					Properties: map[string]oasSchema{
						"item": {Type: "string"},
					},
				},
			},
		},
		AdditionalProperties: additional,
		OneOf: []oasSchema{{
			Type:     "object",
			Required: []string{"variant"},
			Properties: map[string]oasSchema{
				"variant": {Type: "string"},
			},
		}},
	}

	clearOpenAPISchemaRequired(&schema)
	if len(schema.Required) != 0 ||
		len(schema.Properties["root"].Required) != 0 ||
		len(schema.Properties["list"].Items.Required) != 0 ||
		len(schema.OneOf[0].Required) != 0 ||
		len(additional.Required) != 0 {
		t.Fatalf("required fields remain after recursive request projection: %#v", schema)
	}
}

func TestGeneratedSwaggerDocumentsSupportedAuthenticationAlternatives(t *testing.T) {
	data, err := GenerateOpenAPIYAML("server.go")
	if err != nil {
		t.Fatalf("GenerateOpenAPIYAML: %v", err)
	}

	var spec struct {
		Security   []map[string][]string `yaml:"security"`
		Components struct {
			SecuritySchemes map[string]oasSecurityScheme `yaml:"securitySchemes"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(data, &spec); err != nil {
		t.Fatalf("generated spec is not valid YAML: %v", err)
	}

	for _, name := range []string{"bearerAuth", "apiKeyAuth", "oidcSession"} {
		if _, ok := spec.Components.SecuritySchemes[name]; !ok {
			t.Errorf("generated spec is missing %s security scheme", name)
		}
		found := false
		for _, alternative := range spec.Security {
			if _, ok := alternative[name]; ok {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("generated spec does not offer %s as a security alternative", name)
		}
	}
	if got := spec.Components.SecuritySchemes["bridgeSession"]; got.Type != "apiKey" || got.Name != bridgeSessionHeader || got.In != "header" {
		t.Errorf("bridgeSession = %#v, want %s header scheme", got, bridgeSessionHeader)
	}
	if got := spec.Components.SecuritySchemes["bridgeToken"]; got.Type != "apiKey" || got.Name != bridgeTokenHeader || got.In != "header" {
		t.Errorf("bridgeToken = %#v, want %s header scheme", got, bridgeTokenHeader)
	}
	if got := spec.Components.SecuritySchemes["apiKeyAuth"]; got.Type != "apiKey" || got.Name != "X-APEXLoad-API-Key" || got.In != "header" {
		t.Errorf("apiKeyAuth = %#v, want X-APEXLoad-API-Key header scheme", got)
	}
	if got := spec.Components.SecuritySchemes["oidcSession"]; got.Type != "apiKey" || got.Name != "apexload_auth_session" || got.In != "cookie" {
		t.Errorf("oidcSession = %#v, want apexload_auth_session cookie scheme", got)
	}
}

func TestGeneratedSwaggerDocumentsConditionalAndInHandlerAuthentication(t *testing.T) {
	data, err := GenerateOpenAPIYAML("server.go")
	if err != nil {
		t.Fatalf("GenerateOpenAPIYAML: %v", err)
	}
	var spec struct {
		Paths map[string]map[string]oasOperation `yaml:"paths"`
	}
	if err := yaml.Unmarshal(data, &spec); err != nil {
		t.Fatalf("generated spec is not valid YAML: %v", err)
	}

	metrics := spec.Paths["/metrics"]["get"]
	if metrics.Security == nil || len(*metrics.Security) != 2 {
		t.Fatalf("metrics security = %#v, want anonymous-or-bearer alternatives", metrics.Security)
	}
	if !strings.Contains(metrics.XRoles, "metrics Bearer token") {
		t.Errorf("metrics access text = %q, want conditional metrics-token requirement", metrics.XRoles)
	}
	if !strings.Contains(metrics.XConditional, "APEXLOAD_METRICS_ADDR") {
		t.Errorf("metrics conditional availability = %q", metrics.XConditional)
	}
	if got := spec.Paths["/api/v1/desktop/browser-downloads"]["post"].XConditional; !strings.Contains(got, "desktop") {
		t.Errorf("desktop download conditional availability = %q", got)
	}

	authoringCapabilities := spec.Paths["/api/v1/scripts/authoring/capabilities"]["get"]
	for _, contract := range []string{
		"global roles: Viewer, Editor, ProjectAdmin, SystemAdmin",
		"when project_id is supplied, project roles: Viewer, Editor, ProjectAdmin",
		"service-account scope: read",
	} {
		if !strings.Contains(authoringCapabilities.XRoles, contract) || !strings.Contains(authoringCapabilities.Description, contract) {
			t.Errorf("authoring capabilities access text = %q / %q, want %q", authoringCapabilities.XRoles, authoringCapabilities.Description, contract)
		}
	}

	for _, endpoint := range []struct {
		method string
		path   string
	}{
		{method: "get", path: "/api/v1/auth/diagnostics"},
		{method: "post", path: "/api/v1/monitoring/collectors/{id}/heartbeat"},
	} {
		operation := spec.Paths[endpoint.path][endpoint.method]
		if operation.Security != nil {
			t.Errorf("%s %s unexpectedly overrides inherited authentication with %#v", endpoint.method, endpoint.path, *operation.Security)
		}
	}
}

func TestGeneratedSwaggerDocumentsBridgeDiscoveryAndDocsMedia(t *testing.T) {
	data, err := GenerateOpenAPIYAML("server.go")
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths map[string]map[string]oasOperation `yaml:"paths"`
	}
	if err := yaml.Unmarshal(data, &spec); err != nil {
		t.Fatal(err)
	}
	for _, route := range []struct{ method, path string }{
		{method: "post", path: bridgePairPath},
		{method: "get", path: bridgeStatusPath},
		{method: "get", path: "/.well-known/apexload-ci"},
		{method: "get", path: "/desktop/bootstrap"},
		{method: "get", path: "/desktop/download"},
	} {
		if _, ok := spec.Paths[route.path][route.method]; !ok {
			t.Errorf("generated spec is missing %s %s", route.method, route.path)
		}
	}
	status := spec.Paths[bridgeStatusPath]["get"]
	if !strings.Contains(status.XServer, "recording-agent bridge") {
		t.Errorf("bridge status x-server = %q", status.XServer)
	}
	if status.Security == nil || len(*status.Security) != 1 {
		t.Fatalf("bridge status security = %#v", status.Security)
	}
	for _, scheme := range []string{"bridgeToken", "bridgeSession"} {
		if _, ok := (*status.Security)[0][scheme]; !ok {
			t.Errorf("bridge status does not require %s: %#v", scheme, *status.Security)
		}
	}
	for _, route := range []struct{ method, path string }{
		{method: "post", path: bridgePairPath},
		{method: "get", path: bridgeStatusPath},
	} {
		originRequired := false
		for _, parameter := range spec.Paths[route.path][route.method].Parameters {
			if parameter.In == "header" && strings.EqualFold(parameter.Name, "Origin") {
				originRequired = parameter.Required
			}
		}
		if !originRequired {
			t.Errorf("%s %s does not require the Origin header", route.method, route.path)
		}
	}

	for path, mediaType := range map[string]string{
		"/api/v1/docs/swagger.yaml":     "application/yaml",
		"/api/v1/docs/endpoint-catalog": "application/json",
	} {
		operation := spec.Paths[path]["get"]
		response := operation.Responses["2XX"]
		if _, ok := response.Content[mediaType]; !ok {
			t.Errorf("%s response is missing %s content", path, mediaType)
		}
	}
}

func TestOpenAPIContractSpecialCasesMatchRuntimeWireTypes(t *testing.T) {
	duration := openAPISchemaForASTType(&ast.SelectorExpr{X: ast.NewIdent("time"), Sel: ast.NewIdent("Duration")}, nil, map[string]bool{}, 0)
	if duration.Type != "integer" || duration.Format != "int64" {
		t.Fatalf("time.Duration schema = %#v", duration)
	}
	for _, path := range []string{"/api/v1/integrations/github/branches", "/api/v1/integrations/github/contents"} {
		for _, name := range []string{"owner", "repo"} {
			if !requiredQueryParameter(http.MethodGet, path, name) {
				t.Errorf("GET %s query parameter %s is not required", path, name)
			}
		}
	}
}

func TestHumanizeHandlerPreservesInitialismsAndProductNames(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "handleGetAPIDocsSwagger", want: "Get API Docs Swagger"},
		{input: "handleGetTestSLAStatus", want: "Get Test SLA Status"},
		{input: "handleAIDraftGeneration", want: "AI Draft Generation"},
		{input: "handleGitHubCIExchange", want: "GitHub CI Exchange"},
		{input: "handleImportAsyncAPI", want: "Import AsyncAPI"},
		{input: "handlePreviewBrowserToHTTP", want: "Preview Browser To HTTP"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := humanizeHandler(tt.input); got != tt.want {
				t.Fatalf("humanizeHandler(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestDocsCategoryUsesStableNamesForCIAndDocumentation(t *testing.T) {
	if got := docsCategoryForPath("/api/v1/ci/runs"); got != "CI/CD" {
		t.Fatalf("CI category = %q, want CI/CD", got)
	}
	if got := docsCategoryForPath("/api/v1/docs/swagger.yaml"); got != "Documentation" {
		t.Fatalf("docs category = %q, want Documentation", got)
	}
}

func TestOpenAPITagsUseStableNamesForCIAndDocumentation(t *testing.T) {
	if got := tagForPath("/api/v1/ci/runs"); got != "CI/CD" {
		t.Fatalf("tagForPath(ci) = %q, want CI/CD", got)
	}
	if got := tagForPath("/api/v1/docs/swagger.yaml"); got != "Documentation" {
		t.Fatalf("tagForPath(docs) = %q, want Documentation", got)
	}
}

func TestGeneratedSwaggerIncludesInferredHandlerContracts(t *testing.T) {
	data, err := GenerateOpenAPIYAML("server.go")
	if err != nil {
		t.Fatalf("GenerateOpenAPIYAML: %v", err)
	}
	var spec struct {
		Paths map[string]map[string]oasOperation `yaml:"paths"`
	}
	if err := yaml.Unmarshal(data, &spec); err != nil {
		t.Fatalf("generated spec is not valid YAML: %v", err)
	}

	transactions := spec.Paths["/api/v1/tests/{id}/archive/transactions"]["get"]
	queryParameters := map[string]oasParameter{}
	for _, parameter := range transactions.Parameters {
		if parameter.In == "query" {
			queryParameters[parameter.Name] = parameter
		}
	}
	for _, name := range []string{"action", "format", "limit", "offset", "status", "trace_id"} {
		if _, ok := queryParameters[name]; !ok {
			t.Errorf("archive transactions is missing inferred %q query parameter", name)
		}
	}
	if got := queryParameters["limit"].Schema.Type; got != "integer" {
		t.Errorf("archive transactions limit type = %q, want integer", got)
	}

	ciRun := spec.Paths["/api/v1/ci/runs"]["post"]
	if !operationHasParameter(ciRun, "header", "Idempotency-Key") {
		t.Error("CI run create is missing its handler-read Idempotency-Key header")
	}
	if ciRun.RequestBody == nil || !ciRun.RequestBody.Required {
		t.Fatal("CI run create is missing its required request body")
	}
	ciSchema := ciRun.RequestBody.Content["application/json"].Schema
	for _, name := range []string{"project_id", "source", "execution", "goals"} {
		if _, ok := ciSchema.Properties[name]; !ok {
			t.Errorf("CI run request schema is missing %q", name)
		}
	}
	if _, ok := ciRun.Responses["202"]; !ok {
		t.Error("CI run create is missing its handler-declared 202 response")
	}

	jmeterSave := spec.Paths["/api/v1/scripts/import/jmeter/save"]["post"]
	if !operationHasParameter(jmeterSave, "header", "Idempotency-Key") {
		t.Error("JMeter import save is missing its handler-read Idempotency-Key header")
	}
	if _, ok := jmeterSave.Responses["409"]; !ok {
		t.Error("JMeter import save is missing its handler-declared 409 response")
	}
	for _, contract := range []struct {
		path     string
		required []string
	}{
		{path: "/api/v1/data-pools/{id}", required: []string{"updated_at"}},
		{path: "/api/v1/data-pools/{id}/rows/{row_id}", required: []string{"data", "version"}},
	} {
		operation := spec.Paths[contract.path]["put"]
		if operation.RequestBody == nil {
			t.Errorf("data-pool update %s is missing its request body", contract.path)
			continue
		}
		schema := operation.RequestBody.Content["application/json"].Schema
		for _, required := range contract.required {
			if !openAPIStringSliceContains(schema.Required, required) {
				t.Errorf("data-pool update %s required fields = %v, want %q", contract.path, schema.Required, required)
			}
		}
	}
	poolUpdate := spec.Paths["/api/v1/data-pools/{id}"]["put"]
	poolUpdateSchema := poolUpdate.RequestBody.Content["application/json"].Schema
	if _, ok := poolUpdateSchema.Properties["project_id"]; ok {
		t.Error("data-pool update schema exposes immutable project_id")
	}
	archivedAt := poolUpdateSchema.Properties["archived_at"]
	if archivedAt.Type != "string" || archivedAt.Format != "date-time" || !archivedAt.Nullable {
		t.Errorf("data-pool archived_at schema = %#v, want nullable date-time string", archivedAt)
	}
	rowUpdate := spec.Paths["/api/v1/data-pools/{id}/rows/{row_id}"]["put"]
	rowData := rowUpdate.RequestBody.Content["application/json"].Schema.Properties["data"]
	additional, ok := rowData.AdditionalProperties.(map[string]any)
	if !ok || additional["type"] != "string" {
		t.Errorf("data-pool row data additionalProperties = %#v, want string values", rowData.AdditionalProperties)
	}

	for _, contract := range []struct {
		path     string
		required string
	}{
		{path: "/api/v1/auth/users", required: "subject"},
		{path: "/api/v1/auth/service-accounts", required: "name"},
	} {
		operation := spec.Paths[contract.path]["post"]
		if operation.RequestBody == nil {
			t.Errorf("auth create %s is missing its request body", contract.path)
			continue
		}
		schema := operation.RequestBody.Content["application/json"].Schema
		if !openAPIStringSliceContains(schema.Required, contract.required) {
			t.Errorf("auth create %s required fields = %v, want %q", contract.path, schema.Required, contract.required)
		}
	}

	authoringCreate := spec.Paths["/api/v1/scripts/authoring/sessions"]["post"]
	if authoringCreate.RequestBody == nil || !authoringCreate.RequestBody.Required {
		t.Fatal("authoring-session create is missing its required request body")
	}
	authoringSchema := authoringCreate.RequestBody.Content["application/json"].Schema
	if got := authoringSchema.Ref; got != "#/components/schemas/AuthoringSessionCreateRequest" {
		t.Fatalf("authoring-session request schema = %q, want typed component", got)
	}
	for _, status := range []string{"200", "201", "409"} {
		if _, ok := authoringCreate.Responses[status]; !ok {
			t.Errorf("authoring-session create is missing its handler-declared %s response", status)
		}
	}

	scenarioCreate := spec.Paths["/api/v1/scenarios"]["post"]
	if scenarioCreate.RequestBody == nil || !scenarioCreate.RequestBody.Required {
		t.Fatal("scenario create is missing its required request body")
	}
	scenarioSchema := scenarioCreate.RequestBody.Content["application/json"].Schema
	for _, privateName := range []string{"creationActor", "creation_actor", "runtimeSecretScope", "runtime_secret_scope"} {
		if _, ok := scenarioSchema.Properties[privateName]; ok {
			t.Errorf("scenario request exposes private field %q", privateName)
		}
	}
	for _, required := range []string{"name", "duration", "groups"} {
		if !openAPIStringSliceContains(scenarioSchema.Required, required) {
			t.Errorf("scenario request required fields %v are missing %q", scenarioSchema.Required, required)
		}
	}
	preview := spec.Paths["/api/v1/scenarios/{id}/schedules/preview"]["post"]
	if preview.RequestBody == nil {
		t.Fatal("scenario schedule preview is missing its request body")
	}
	previewSchema := preview.RequestBody.Content["application/json"].Schema
	if !openAPIStringSliceContains(previewSchema.Required, "cron_expr") {
		t.Errorf("scenario schedule preview required fields = %v, want cron_expr", previewSchema.Required)
	}
	if openAPIStringSliceContains(previewSchema.Required, "name") {
		t.Errorf("scenario schedule preview incorrectly requires name: %v", previewSchema.Required)
	}

	rulesetUpdate := spec.Paths["/api/v1/correlation-rulesets/{id}"]["put"]
	if rulesetUpdate.RequestBody == nil || !rulesetUpdate.RequestBody.Required {
		t.Fatal("correlation ruleset update is missing its required request body")
	}
	rulesetUpdateSchema := rulesetUpdate.RequestBody.Content["application/json"].Schema
	for _, name := range []string{"name", "description", "rules", "expected_updated_at"} {
		if _, ok := rulesetUpdateSchema.Properties[name]; !ok {
			t.Errorf("correlation ruleset update schema is missing editable/CAS field %q", name)
		}
	}
	for _, immutable := range []string{"id", "project_id", "source", "created_at", "updated_at"} {
		if _, ok := rulesetUpdateSchema.Properties[immutable]; ok {
			t.Errorf("correlation ruleset update schema exposes immutable field %q", immutable)
		}
	}
	for _, required := range []string{"name", "expected_updated_at"} {
		if !openAPIStringSliceContains(rulesetUpdateSchema.Required, required) {
			t.Errorf("correlation ruleset update required fields %v are missing %q", rulesetUpdateSchema.Required, required)
		}
	}
	rules := rulesetUpdateSchema.Properties["rules"]
	if rules.Type != "array" || rules.Items == nil {
		t.Fatalf("correlation ruleset update rules schema = %#v, want array items", rules)
	}
	for _, name := range []string{"name", "rule_id", "source", "extract_type", "expression", "confidence"} {
		if _, ok := rules.Items.Properties[name]; !ok {
			t.Errorf("correlation ruleset update rule schema is missing %q", name)
		}
	}

	correlationPreview := spec.Paths["/api/v1/scripts/authoring/sessions/{id}/correlations/preview"]["post"]
	if correlationPreview.RequestBody == nil || !correlationPreview.RequestBody.Required {
		t.Fatal("authoring correlation preview is missing its required request body")
	}
	correlationPreviewSchema := correlationPreview.RequestBody.Content["application/json"].Schema
	expectedUpdatedAt, ok := correlationPreviewSchema.Properties["expected_updated_at"]
	if !ok || expectedUpdatedAt.Type != "string" || expectedUpdatedAt.Format != "date-time" {
		t.Errorf("authoring correlation expected_updated_at schema = %#v, want required date-time", expectedUpdatedAt)
	}
	if !openAPIStringSliceContains(correlationPreviewSchema.Required, "expected_updated_at") {
		t.Errorf("authoring correlation required fields = %v, want expected_updated_at", correlationPreviewSchema.Required)
	}
	if _, ok := rules.Items.Properties["sample_value"]; ok {
		t.Error("correlation ruleset update rule schema exposes non-editable sample_value")
	}
	scheduleContracts := []struct {
		name         string
		path         string
		method       string
		requireName  bool
		precondition bool
	}{
		{name: "create scenario schedule", path: "/api/v1/scenarios/{id}/schedules", method: "post", requireName: true},
		{name: "update scenario schedule", path: "/api/v1/scenarios/{id}/schedules/{schedule_id}", method: "put", requireName: true, precondition: true},
		{name: "create execution-plan schedule", path: "/api/v1/execution-plans/{id}/schedules", method: "post"},
		{name: "update execution-plan schedule", path: "/api/v1/execution-plans/{id}/schedules/{schedule_id}", method: "put", precondition: true},
	}
	for _, contract := range scheduleContracts {
		t.Run(contract.name, func(t *testing.T) {
			operation := spec.Paths[contract.path][contract.method]
			if operation.RequestBody == nil {
				t.Fatal("schedule operation is missing its request body")
			}
			schema := operation.RequestBody.Content["application/json"].Schema
			if !openAPIStringSliceContains(schema.Required, "cron_expr") {
				t.Errorf("required fields = %v, want cron_expr", schema.Required)
			}
			if got := openAPIStringSliceContains(schema.Required, "name"); got != contract.requireName {
				t.Errorf("name required = %t, want %t (required fields %v)", got, contract.requireName, schema.Required)
			}
			if !contract.precondition {
				return
			}
			expectedRevision := schema.Properties["expected_revision"]
			if !strings.Contains(expectedRevision.Description, "If-Match") {
				t.Errorf("expected_revision description = %q, want If-Match alternative", expectedRevision.Description)
			}
			ifMatch, ok := operationParameter(operation, "header", "If-Match")
			if !ok {
				t.Fatal("schedule update is missing the If-Match header")
			}
			if ifMatch.Required {
				t.Error("If-Match must remain optional because expected_revision is an alternative")
			}
			if !strings.Contains(ifMatch.Description, "expected_revision") {
				t.Errorf("If-Match description = %q, want expected_revision alternative", ifMatch.Description)
			}
			for _, status := range []string{"409", "428"} {
				if _, ok := operation.Responses[status]; !ok {
					t.Errorf("schedule update is missing documented %s response", status)
				}
			}
		})
	}
	groups := scenarioSchema.Properties["groups"]
	if groups.Type != "array" || groups.Items == nil {
		t.Fatalf("scenario groups schema = %#v, want array items", groups)
	}
	if _, ok := groups.Items.Properties["runtimeSecretScope"]; ok {
		t.Error("scenario group schema exposes private runtimeSecretScope")
	}
	if got := groups.Items.Properties["iterations"].Type; got != "integer" {
		t.Errorf("scenario group iterations type = %q, want integer", got)
	}
	assignment := groups.Items.Properties["assignment"]
	selectedTargets := assignment.Properties["selected_targets"]
	if selectedTargets.Type != "array" || selectedTargets.Items == nil || selectedTargets.Items.Type != "string" {
		t.Errorf("scenario selected_targets schema = %#v, want array of strings", selectedTargets)
	}
	rootRuntime := scenarioSchema.Properties["runtime_settings"]
	browser := rootRuntime.Properties["browser"]
	if got := browser.Properties["headless"].Type; got != "boolean" {
		t.Errorf("scenario browser headless type = %q, want boolean", got)
	}
	createdScenarioResponse := scenarioCreate.Responses["201"].Content["application/json"].Schema
	if createdScenarioResponse.Type != "object" || createdScenarioResponse.Properties["id"].Type != "string" {
		t.Errorf("scenario create response schema = %#v, want typed StoredScenario object", createdScenarioResponse)
	}

	inventory := spec.Paths["/api/v1/system/inventory"]["get"]
	inventorySchema := inventory.Responses["200"].Content["application/json"].Schema
	generators := inventorySchema.Properties["generators"]
	if generators.Type != "array" || generators.Items == nil {
		t.Fatalf("system inventory generators schema = %#v, want array items", generators)
	}
	runtimeCapacities := generators.Items.Properties["runtime_capacities"]
	if runtimeCapacities.Type != "array" || runtimeCapacities.Items == nil {
		t.Fatalf("runtime_capacities schema = %#v, want typed array items", runtimeCapacities)
	}
	runtimeCapacity := runtimeCapacities.Items
	for name, wantType := range map[string]string{
		"protocol":         "string",
		"available":        "boolean",
		"max_vusers":       "integer",
		"available_vusers": "integer",
		"features":         "array",
		"occupancy_mode":   "string",
	} {
		if got := runtimeCapacity.Properties[name].Type; got != wantType {
			t.Errorf("runtime capacity property %q type = %q, want %q", name, got, wantType)
		}
	}
	if features := runtimeCapacity.Properties["features"]; features.Items == nil || features.Items.Type != "string" {
		t.Errorf("runtime capacity features schema = %#v, want array of strings", features)
	}
	if occupancy := runtimeCapacity.Properties["occupancy_mode"]; !reflect.DeepEqual(occupancy.Enum, []string{"idle", "persistent", "legacy"}) {
		t.Errorf("runtime capacity occupancy enum = %v, want idle/persistent/legacy", occupancy.Enum)
	}
	for _, required := range []string{"protocol", "available", "max_vusers", "available_vusers"} {
		if !openAPIStringSliceContains(runtimeCapacity.Required, required) {
			t.Errorf("runtime capacity required fields %v are missing %q", runtimeCapacity.Required, required)
		}
	}

	archiveImport := spec.Paths["/api/v1/scripts/import/enterprise-script-archive"]["post"]
	if archiveImport.RequestBody == nil {
		t.Fatal("enterprise archive import is missing its multipart request body")
	}
	multipart := archiveImport.RequestBody.Content["multipart/form-data"].Schema
	if file := multipart.Properties["file"]; file.Type != "string" || file.Format != "binary" {
		t.Errorf("enterprise archive file schema = %#v, want binary string", file)
	}
	for _, path := range []string{
		"/api/v1/scripts/import/enterprise-script-archive",
		"/api/v1/scripts/import/enterprise-script-archive/validate",
	} {
		operation := spec.Paths[path]["post"]
		var compatibilityParameter *oasParameter
		for index := range operation.Parameters {
			if operation.Parameters[index].In == "query" &&
				operation.Parameters[index].Name == "allow_java_vuser_runner" {
				compatibilityParameter = &operation.Parameters[index]
				break
			}
		}
		if compatibilityParameter == nil {
			t.Errorf("POST %s is missing allow_java_vuser_runner compatibility query parameter", path)
		} else if !compatibilityParameter.Deprecated ||
			!strings.Contains(compatibilityParameter.Description, "accepted but ignored") ||
			!strings.Contains(compatibilityParameter.Description, "one combined java_vuser draft") {
			t.Errorf("POST %s compatibility query parameter = %#v, want deprecated no-op contract", path, compatibilityParameter)
		}
		if operation.RequestBody == nil {
			t.Errorf("POST %s is missing multipart request body", path)
			continue
		}
		property := operation.RequestBody.Content["multipart/form-data"].Schema.Properties["allow_java_vuser_runner"]
		if !property.Deprecated ||
			!strings.Contains(property.Description, "accepted but ignored") ||
			!strings.Contains(property.Description, "one combined java_vuser draft") {
			t.Errorf("POST %s compatibility multipart property = %#v, want deprecated no-op contract", path, property)
		}
	}

	for path, methods := range spec.Paths {
		for method, operation := range methods {
			if (method == "put" || method == "patch") && operation.RequestBody == nil {
				t.Errorf("%s %s is a body-capable route without a generated request body", method, path)
			}
			if (method == "get" || method == "head") && operation.RequestBody != nil {
				t.Errorf("%s %s unexpectedly declares a request body", method, path)
			}
		}
	}
	websocket := spec.Paths["/api/v1/ws"]["get"]
	if _, ok := websocket.Responses["101"]; !ok {
		t.Error("global WebSocket route is missing the 101 protocol-switch response")
	}
}

func TestSharedImportCommitSchemasIncludeOptionalProtectedCandidateFields(t *testing.T) {
	data, err := GenerateOpenAPIYAML("server.go")
	if err != nil {
		t.Fatalf("GenerateOpenAPIYAML: %v", err)
	}
	var spec struct {
		Paths map[string]map[string]oasOperation `yaml:"paths"`
	}
	if err := yaml.Unmarshal(data, &spec); err != nil {
		t.Fatalf("generated spec is not valid YAML: %v", err)
	}

	paths := []string{
		"/api/v1/scripts/import/openapi/commit",
		"/api/v1/scripts/import/postman/save",
		"/api/v1/scripts/import/asyncapi/commit",
	}
	fieldTypes := map[string]string{
		"script":                        "object",
		"import_session_id":             "string",
		"project_id":                    "string",
		"name":                          "string",
		"folder_id":                     "string",
		"mode":                          "string",
		"target_script_id":              "string",
		"replace_mode":                  "string",
		"dry_run":                       "boolean",
		"secret_bindings":               "object",
		"parameterization_selected_ids": "array",
	}
	// 413 is emitted when the bounded request decoder rejects an oversized
	// document. 429 is emitted when protected import session capacity is
	// exhausted; it is a transient, retryable condition with Retry-After.
	wantStatuses := []string{"200", "201", "400", "401", "403", "404", "409", "413", "429", "500", "default"}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			operation, ok := spec.Paths[path]["post"]
			if !ok {
				t.Fatalf("generated spec is missing POST %s", path)
			}
			if operation.RequestBody == nil || !operation.RequestBody.Required {
				t.Fatalf("request body = %#v, want required JSON document", operation.RequestBody)
			}
			schema := operation.RequestBody.Content["application/json"].Schema
			for name, wantType := range fieldTypes {
				field, exists := schema.Properties[name]
				if !exists {
					t.Errorf("request schema is missing %q", name)
					continue
				}
				if field.Type != wantType {
					t.Errorf("%s type = %q, want %q", name, field.Type, wantType)
				}
			}
			if got := schema.Properties["parameterization_selected_ids"]; got.Items == nil || got.Items.Type != "string" {
				t.Errorf("parameterization_selected_ids = %#v, want string array", got)
			}
			if len(schema.Required) != 0 {
				t.Errorf("request fields unexpectedly became required: %v", schema.Required)
			}

			gotStatuses := make([]string, 0, len(operation.Responses))
			for status := range operation.Responses {
				gotStatuses = append(gotStatuses, status)
			}
			sort.Strings(gotStatuses)
			if !reflect.DeepEqual(gotStatuses, wantStatuses) {
				t.Errorf("response statuses = %v, want %v", gotStatuses, wantStatuses)
			}
		})
	}
}

func TestGeneratedBrowserReplayStartContractsIncludeMappedErrors(t *testing.T) {
	data, err := GenerateOpenAPIYAML("server.go")
	if err != nil {
		t.Fatalf("GenerateOpenAPIYAML: %v", err)
	}
	var spec struct {
		Paths map[string]map[string]oasOperation `yaml:"paths"`
	}
	if err := yaml.Unmarshal(data, &spec); err != nil {
		t.Fatalf("generated spec is not valid YAML: %v", err)
	}

	for _, path := range []string{
		"/api/v1/scripts/browser-replay/sessions",
		"/api/v1/scripts/{id}/browser-replay/sessions",
	} {
		operation := spec.Paths[path]["post"]
		for _, status := range []string{"409", "500", "503"} {
			response, ok := operation.Responses[status]
			if !ok {
				t.Errorf("POST %s is missing documented %s response", path, status)
				continue
			}
			schema := response.Content["application/json"].Schema
			if schema.Ref != "#/components/schemas/ErrorResponse" {
				t.Errorf("POST %s response %s schema = %q, want ErrorResponse", path, status, schema.Ref)
			}
		}
		responseSchema := operation.Responses["200"].Content["application/json"].Schema
		for _, field := range []string{"script_id", "replay_session_id", "session", "preflight", "warnings"} {
			if _, ok := responseSchema.Properties[field]; !ok {
				t.Errorf("POST %s success response is missing typed field %q: %#v", path, field, responseSchema)
			}
		}
	}

	draftStart := spec.Paths["/api/v1/scripts/browser-replay/sessions"]["post"]
	if draftStart.RequestBody == nil || !draftStart.RequestBody.Required {
		t.Fatal("draft browser replay start must require a request body")
	}
	draftSchema := draftStart.RequestBody.Content["application/json"].Schema
	if !openAPIStringSliceContains(draftSchema.Required, "script") {
		t.Errorf("draft browser replay required fields = %v, want script", draftSchema.Required)
	}

	storedStart := spec.Paths["/api/v1/scripts/{id}/browser-replay/sessions"]["post"]
	if storedStart.RequestBody == nil || storedStart.RequestBody.Required {
		t.Fatal("stored-script browser replay overrides must be an optional request body")
	}
}

func TestGeneratedReplaySessionResponsesAreTyped(t *testing.T) {
	data, err := GenerateOpenAPIYAML("server.go")
	if err != nil {
		t.Fatalf("GenerateOpenAPIYAML: %v", err)
	}
	var spec struct {
		Paths map[string]map[string]oasOperation `yaml:"paths"`
	}
	if err := yaml.Unmarshal(data, &spec); err != nil {
		t.Fatalf("generated spec is not valid YAML: %v", err)
	}

	sessionContracts := []struct {
		path   string
		method string
		fields []string
	}{
		{"/api/v1/browser-replay/sessions/{session_id}", "get", []string{"id", "project_id", "status", "events", "report"}},
		{"/api/v1/browser-replay/sessions/{session_id}/pause", "post", []string{"id", "project_id", "status", "events", "report"}},
		{"/api/v1/browser-replay/sessions/{session_id}/resume", "post", []string{"id", "project_id", "status", "events", "report"}},
		{"/api/v1/browser-replay/sessions/{session_id}/stop", "post", []string{"id", "project_id", "status", "events", "report"}},
		{"/api/v1/replay/sessions/{session_id}", "get", []string{"id", "project_id", "status", "events", "report", "breakpoints"}},
		{"/api/v1/replay/sessions/{session_id}/pause", "post", []string{"id", "project_id", "status", "events", "report", "breakpoints"}},
		{"/api/v1/replay/sessions/{session_id}/resume", "post", []string{"id", "project_id", "status", "events", "report", "breakpoints"}},
		{"/api/v1/replay/sessions/{session_id}/step", "post", []string{"id", "project_id", "status", "events", "report", "breakpoints"}},
		{"/api/v1/replay/sessions/{session_id}/stop", "post", []string{"id", "project_id", "status", "events", "report", "breakpoints"}},
		{"/api/v1/replay/sessions/{session_id}/breakpoints", "patch", []string{"id", "project_id", "status", "events", "report", "breakpoints"}},
		{"/api/v1/replay/sessions/{session_id}/options", "patch", []string{"id", "project_id", "status", "events", "report", "breakpoints"}},
	}
	for _, contract := range sessionContracts {
		responseSchema := spec.Paths[contract.path][contract.method].Responses["200"].Content["application/json"].Schema
		session, ok := responseSchema.Properties["session"]
		if !ok {
			t.Errorf("%s %s response is missing typed session field: %#v", contract.method, contract.path, responseSchema)
			continue
		}
		for _, field := range contract.fields {
			if _, ok := session.Properties[field]; !ok {
				t.Errorf("%s %s session response is missing %q: %#v", contract.method, contract.path, field, session)
			}
		}
	}

	for _, path := range []string{
		"/api/v1/scripts/replay/sessions",
		"/api/v1/scripts/debug/sessions",
		"/api/v1/scripts/{id}/replay/sessions",
		"/api/v1/scripts/{id}/debug/sessions",
	} {
		responseSchema := spec.Paths[path]["post"].Responses["200"].Content["application/json"].Schema
		for _, field := range []string{"script_id", "replay_session_id", "debug_session_id", "session", "preflight", "warnings"} {
			if _, ok := responseSchema.Properties[field]; !ok {
				t.Errorf("POST %s success response is missing typed field %q: %#v", path, field, responseSchema)
			}
		}
		if _, ok := responseSchema.Properties["session"].Properties["status"]; !ok {
			t.Errorf("POST %s session response is opaque: %#v", path, responseSchema.Properties["session"])
		}
	}
}

func TestGeneratedCorrelationDiagnosisRequestIsTyped(t *testing.T) {
	data, err := GenerateOpenAPIYAML("server.go")
	if err != nil {
		t.Fatalf("GenerateOpenAPIYAML: %v", err)
	}
	var spec struct {
		Paths map[string]map[string]oasOperation `yaml:"paths"`
	}
	if err := yaml.Unmarshal(data, &spec); err != nil {
		t.Fatalf("generated spec is not valid YAML: %v", err)
	}

	operation := spec.Paths["/api/v1/correlations/diagnose"]["post"]
	if operation.RequestBody == nil || !operation.RequestBody.Required {
		t.Fatal("correlation diagnosis must require a request body")
	}
	requestSchema := operation.RequestBody.Content["application/json"].Schema
	for _, field := range []string{"recorded", "replay"} {
		steps, ok := requestSchema.Properties[field]
		if !ok || steps.Type != "array" || steps.Items == nil {
			t.Fatalf("correlation diagnosis %s schema = %#v, want typed step array", field, steps)
		}
		for _, stepField := range []string{"id", "request_url", "request_body", "request_headers", "response_body", "response_headers", "response_status"} {
			if _, ok := steps.Items.Properties[stepField]; !ok {
				t.Errorf("correlation diagnosis %s step schema is missing %q: %#v", field, stepField, steps.Items)
			}
		}
	}
}

func TestGeneratedBrowserRecordingVerifyContract(t *testing.T) {
	data, err := GenerateOpenAPIYAML("server.go")
	if err != nil {
		t.Fatalf("GenerateOpenAPIYAML: %v", err)
	}
	var spec struct {
		Paths map[string]map[string]oasOperation `yaml:"paths"`
	}
	if err := yaml.Unmarshal(data, &spec); err != nil {
		t.Fatalf("generated spec is not valid YAML: %v", err)
	}

	operation := spec.Paths["/api/v1/browser-recorder/sessions/{id}/verify"]["post"]
	if operation.RequestBody == nil || operation.RequestBody.Required {
		t.Fatal("browser recording verification must accept an optional request body")
	}
	requestSchema := operation.RequestBody.Content["application/json"].Schema
	if requestSchema.Properties["replay_session_id"].Type != "string" {
		t.Errorf("verify replay_session_id schema = %#v, want string", requestSchema.Properties["replay_session_id"])
	}
	responseSchema := operation.Responses["201"].Content["application/json"].Schema
	for _, field := range []string{"replay_session_id", "replay_session", "warnings"} {
		if _, ok := responseSchema.Properties[field]; !ok {
			t.Errorf("verify success response is missing typed field %q: %#v", field, responseSchema)
		}
	}
	for _, status := range []string{"409", "500", "503"} {
		response, ok := operation.Responses[status]
		if !ok {
			t.Errorf("verify operation is missing documented %s response", status)
			continue
		}
		if got := response.Content["application/json"].Schema.Ref; got != "#/components/schemas/ErrorResponse" {
			t.Errorf("verify response %s schema = %q, want ErrorResponse", status, got)
		}
	}
}

func TestGeneratedCorrelationRulesetActionContractsRequireScriptID(t *testing.T) {
	data, err := GenerateOpenAPIYAML("server.go")
	if err != nil {
		t.Fatalf("GenerateOpenAPIYAML: %v", err)
	}
	var spec struct {
		Paths map[string]map[string]oasOperation `yaml:"paths"`
	}
	if err := yaml.Unmarshal(data, &spec); err != nil {
		t.Fatalf("generated spec is not valid YAML: %v", err)
	}
	for _, path := range []string{
		"/api/v1/correlation-rulesets/discover-from-script",
		"/api/v1/correlation-rulesets/{id}/apply-to-script",
	} {
		operation := spec.Paths[path]["post"]
		if operation.RequestBody == nil || !operation.RequestBody.Required {
			t.Errorf("POST %s must require a request body", path)
			continue
		}
		schema := operation.RequestBody.Content["application/json"].Schema
		if !openAPIStringSliceContains(schema.Required, "script_id") {
			t.Errorf("POST %s required fields = %v, want script_id", path, schema.Required)
		}
	}
}

func TestGeneratedCorrelationRulesetResponsesAreTypedAndDisclosureSafe(t *testing.T) {
	data, err := GenerateOpenAPIYAML("server.go")
	if err != nil {
		t.Fatalf("GenerateOpenAPIYAML: %v", err)
	}
	var spec struct {
		Paths map[string]map[string]oasOperation `yaml:"paths"`
	}
	if err := yaml.Unmarshal(data, &spec); err != nil {
		t.Fatalf("generated spec is not valid YAML: %v", err)
	}

	rulesetResponses := []struct {
		path   string
		method string
		status string
	}{
		{"/api/v1/correlation-rulesets", "post", "201"},
		{"/api/v1/correlation-rulesets/{id}", "get", "200"},
		{"/api/v1/correlation-rulesets/{id}", "put", "200"},
		{"/api/v1/correlation-rulesets/discover-from-script", "post", "201"},
	}
	for _, contract := range rulesetResponses {
		schema := spec.Paths[contract.path][contract.method].Responses[contract.status].Content["application/json"].Schema
		for _, field := range []string{"id", "project_id", "name", "description", "source", "rules", "created_at", "updated_at"} {
			if _, ok := schema.Properties[field]; !ok {
				t.Errorf("%s %s response is missing typed field %q: %#v", contract.method, contract.path, field, schema)
			}
		}
		rules := schema.Properties["rules"]
		if rules.Type != "array" || rules.Items == nil {
			t.Errorf("%s %s rules schema = %#v, want typed array", contract.method, contract.path, rules)
			continue
		}
		for _, field := range []string{"name", "rule_id", "source", "extract_type", "expression", "confidence"} {
			if _, ok := rules.Items.Properties[field]; !ok {
				t.Errorf("%s %s rule response is missing %q", contract.method, contract.path, field)
			}
		}
		if _, ok := rules.Items.Properties["sample_value"]; ok {
			t.Errorf("%s %s rule response exposes sample_value", contract.method, contract.path)
		}
	}

	listSchema := spec.Paths["/api/v1/correlation-rulesets"]["get"].Responses["200"].Content["application/json"].Schema
	for _, field := range []string{"correlation_rulesets", "count", "limit", "offset"} {
		if _, ok := listSchema.Properties[field]; !ok {
			t.Errorf("ruleset list response is missing typed field %q: %#v", field, listSchema)
		}
	}
	listItems := listSchema.Properties["correlation_rulesets"].Items
	if listItems == nil || listItems.Properties["id"].Type != "string" {
		t.Errorf("ruleset list item schema = %#v, want typed ruleset", listItems)
	}

	deleteSchema := spec.Paths["/api/v1/correlation-rulesets/{id}"]["delete"].Responses["200"].Content["application/json"].Schema
	if deleteSchema.Properties["deleted"].Type != "string" {
		t.Errorf("ruleset delete response schema = %#v, want typed deleted id", deleteSchema)
	}

	applySchema := spec.Paths["/api/v1/correlation-rulesets/{id}/apply-to-script"]["post"].Responses["200"].Content["application/json"].Schema
	for _, field := range []string{"ruleset_id", "script_id", "matched_rules", "applied", "diffs", "preview_token"} {
		if _, ok := applySchema.Properties[field]; !ok {
			t.Errorf("ruleset apply response is missing typed field %q: %#v", field, applySchema)
		}
	}
	diffs := applySchema.Properties["diffs"]
	if diffs.Type != "array" || diffs.Items == nil || diffs.Items.Properties["kind"].Type != "string" || diffs.Items.Properties["path"].Type != "string" {
		t.Errorf("ruleset apply diffs schema = %#v, want typed kind/path array", diffs)
	}
}

func TestAnnotatedOpenAPIResponseStatusesRejectsMalformedStatus(t *testing.T) {
	doc := &ast.CommentGroup{List: []*ast.Comment{{Text: "// OpenAPI-Responses: 409, five-hundred, 503"}}}
	if _, err := annotatedOpenAPIResponseStatuses(doc); err == nil {
		t.Fatal("malformed explicit response status was accepted")
	}
}

func openAPIStringSliceContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func TestMergeOpenAPIRequiredFieldsPreservesInferredAndExplicitContracts(t *testing.T) {
	got := mergeOpenAPIRequiredFields([]string{"existing", "shared"}, []string{"explicit", "shared"})
	want := []string{"existing", "explicit", "shared"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mergeOpenAPIRequiredFields() = %v, want %v", got, want)
	}
}

func operationHasParameter(operation oasOperation, location, name string) bool {
	_, ok := operationParameter(operation, location, name)
	return ok
}

func operationParameter(operation oasOperation, location, name string) (oasParameter, bool) {
	for _, parameter := range operation.Parameters {
		if parameter.In == location && parameter.Name == name {
			return parameter, true
		}
	}
	return oasParameter{}, false
}

func TestGeneratedSwaggerMarksScriptDebugAliasesDeprecated(t *testing.T) {
	data, err := GenerateOpenAPIYAML("server.go")
	if err != nil {
		t.Fatalf("GenerateOpenAPIYAML: %v", err)
	}

	var spec struct {
		Paths map[string]map[string]struct {
			Deprecated bool `yaml:"deprecated"`
		} `yaml:"paths"`
	}
	if err := yaml.Unmarshal(data, &spec); err != nil {
		t.Fatalf("generated spec is not valid YAML: %v", err)
	}

	if !spec.Paths["/api/v1/script-debug/sessions/{session_id}"]["get"].Deprecated {
		t.Fatal("script-debug session alias is not marked deprecated")
	}
	if !spec.Paths["/api/v1/script-debug/sessions/{session_id}/step"]["post"].Deprecated {
		t.Fatal("script-debug step alias is not marked deprecated")
	}
	if spec.Paths["/api/v1/replay/sessions/{session_id}"]["get"].Deprecated {
		t.Fatal("canonical replay session route should not be deprecated")
	}
}

func TestGeneratedEndpointCatalogIsValid(t *testing.T) {
	data, err := GenerateEndpointCatalogJSON("server.go")
	if err != nil {
		t.Fatalf("GenerateEndpointCatalogJSON: %v", err)
	}

	var catalog endpointCatalog
	if err := json.Unmarshal(data, &catalog); err != nil {
		t.Fatalf("generated endpoint catalog is not valid JSON: %v", err)
	}
	if catalog.Version != 1 {
		t.Fatalf("catalog version = %d, want 1", catalog.Version)
	}
	if catalog.Source != "pkg/api/server.go" {
		t.Fatalf("catalog source = %q, want pkg/api/server.go", catalog.Source)
	}
	if len(catalog.Routes) == 0 {
		t.Fatal("generated endpoint catalog has no routes")
	}

	seen := map[string]bool{}
	for _, route := range catalog.Routes {
		key := route.Method + " " + route.Path
		if route.Method == "" {
			t.Errorf("%s: missing method", key)
		}
		if route.Path == "" {
			t.Errorf("%s: missing path", key)
		}
		if route.OperationID == "" {
			t.Errorf("%s: missing operationId", key)
		}
		if route.Summary == "" {
			t.Errorf("%s: missing summary", key)
		}
		if route.Category == "" {
			t.Errorf("%s: missing category", key)
		}
		if route.Access == "" {
			t.Errorf("%s: missing access text", key)
		}
		if route.Public == route.Authenticated {
			t.Errorf("%s: public/authenticated flags should be inverse, got public=%t authenticated=%t", key, route.Public, route.Authenticated)
		}
		if seen[key] {
			t.Errorf("duplicate catalog route %s", key)
		}
		seen[key] = true
	}

	for _, required := range []string{
		"GET /api/v1/execution-plans",
		"GET /api/v1/capacity/timeslots",
		"POST /api/v1/auth/users/onboard",
		"POST /api/v1/auth/users/{id}/revoke-sessions",
		"GET /api/v1/downloads/recording-agent/latest",
	} {
		if !seen[required] {
			t.Errorf("catalog missing required route %s", required)
		}
	}
}

func TestSystemSettingsOpenAPIContractIsTyped(t *testing.T) {
	data, err := GenerateOpenAPIYAML("server.go")
	if err != nil {
		t.Fatalf("GenerateOpenAPIYAML: %v", err)
	}
	var spec struct {
		Paths map[string]map[string]oasOperation `yaml:"paths"`
	}
	if err := yaml.Unmarshal(data, &spec); err != nil {
		t.Fatalf("generated spec is not valid YAML: %v", err)
	}

	get := spec.Paths["/api/v1/system/settings"]["get"]
	getContent := get.Responses["200"].Content
	getSchema, ok := getContent["application/json"]
	if !ok {
		t.Fatalf("settings GET 200 content = %#v, want application/json", getContent)
	}
	for _, name := range []string{"general", "thresholds", "capacity_planning", "archive_retention", "notifications", "auth"} {
		if _, ok := getSchema.Schema.Properties[name]; !ok {
			t.Errorf("settings GET response schema is missing %q", name)
		}
	}
	general := getSchema.Schema.Properties["general"]
	if !openAPIStringSliceContains(general.Required, "max_concurrent_tests") {
		t.Errorf("settings GET general required fields = %v, want max_concurrent_tests", general.Required)
	}
	if _, ok := getSchema.Schema.Properties["thresholds"].Properties["fleet_saturated_available_slots"]; !ok {
		t.Error("settings GET thresholds schema is missing fleet_saturated_available_slots")
	}
	notifications := getSchema.Schema.Properties["notifications"]
	webhooks := notifications.Properties["webhooks"]
	if webhooks.Items == nil {
		t.Fatal("settings GET notifications.webhooks schema has no item type")
	}
	if hasToken, ok := webhooks.Items.Properties[redactedSecretConfiguredField]; !ok || hasToken.Type != "boolean" {
		t.Errorf("settings GET webhook schema has_api_token = %#v, want response-only boolean", hasToken)
	}
	if !strings.Contains(getSchema.Schema.Description, "redacted") {
		t.Errorf("settings GET schema description does not document redaction: %q", getSchema.Schema.Description)
	}
	if revision, ok := getSchema.Schema.Properties["revision"]; !ok || revision.Type != "string" {
		t.Errorf("settings GET revision projection = %#v, want string", revision)
	}
	if _, ok := get.Responses["200"].Headers["ETag"]; !ok {
		t.Error("settings GET 200 response does not document its ETag header")
	}

	put := spec.Paths["/api/v1/system/settings"]["put"]
	if put.RequestBody == nil || !put.RequestBody.Required {
		t.Fatalf("settings PUT request body = %#v", put.RequestBody)
	}
	if _, ok := put.RequestBody.Content["application/octet-stream"]; ok {
		t.Error("settings PUT still documents the octet-stream fallback")
	}
	putSchema, ok := put.RequestBody.Content["application/json"]
	if !ok {
		t.Fatalf("settings PUT content = %#v, want application/json", put.RequestBody.Content)
	}
	if closed, ok := putSchema.Schema.AdditionalProperties.(bool); !ok || closed {
		t.Errorf("settings PUT root additionalProperties = %#v, want false so top-level typos are rejected", putSchema.Schema.AdditionalProperties)
	}
	if len(putSchema.Schema.Required) != 0 {
		t.Errorf("settings PUT root required fields = %v, want none because top-level sections are partial", putSchema.Schema.Required)
	}
	putGeneral, ok := putSchema.Schema.Properties["general"]
	if !ok {
		t.Fatal("settings PUT request schema is missing general")
	}
	if len(putGeneral.Required) != 0 {
		t.Errorf("settings PUT general required fields = %v, want none because {\"general\": {}} resets the section", putGeneral.Required)
	}
	if expectedRevision, ok := putSchema.Schema.Properties["expected_revision"]; !ok || expectedRevision.Type != "string" {
		t.Errorf("settings PUT expected_revision = %#v, want optional string body precondition", expectedRevision)
	}
	foundIfMatch := false
	for _, parameter := range put.Parameters {
		if parameter.In == "header" && strings.EqualFold(parameter.Name, "If-Match") {
			foundIfMatch = true
			if !strings.Contains(parameter.Description, "expected_revision") {
				t.Errorf("settings PUT If-Match description omits body alternative: %q", parameter.Description)
			}
		}
	}
	if !foundIfMatch {
		t.Error("settings PUT does not document If-Match")
	}
	if _, ok := put.Responses["200"].Headers["ETag"]; !ok {
		t.Error("settings PUT 200 response does not document its replacement ETag")
	}
	for _, status := range []string{"412", "428"} {
		if _, ok := put.Responses[status]; !ok {
			t.Errorf("settings PUT does not document %s precondition response", status)
		}
	}
	thresholds, ok := putSchema.Schema.Properties["thresholds"]
	if !ok {
		t.Fatal("settings PUT request schema is missing thresholds")
	}
	if closed, ok := thresholds.AdditionalProperties.(bool); !ok || closed {
		t.Errorf("settings PUT thresholds additionalProperties = %#v, want false so nested typos are rejected", thresholds.AdditionalProperties)
	}
	if _, ok := thresholds.Properties["p99_latency_stop_consecutive_samples"]; !ok {
		t.Error("settings PUT thresholds schema is missing p99_latency_stop_consecutive_samples")
	}
	putNotifications := putSchema.Schema.Properties["notifications"]
	putWebhooks := putNotifications.Properties["webhooks"]
	if putWebhooks.Items == nil {
		t.Fatal("settings PUT notifications.webhooks schema has no item type")
	}
	if closed, ok := putWebhooks.Items.AdditionalProperties.(bool); !ok || closed {
		t.Errorf("settings PUT webhook additionalProperties = %#v, want false", putWebhooks.Items.AdditionalProperties)
	}
	hasToken, ok := putWebhooks.Items.Properties[redactedSecretConfiguredField]
	if !ok || hasToken.Type != "boolean" || !strings.Contains(hasToken.Description, "ignored") {
		t.Errorf("settings PUT webhook has_api_token = %#v, want tolerated-and-ignored boolean echo field", hasToken)
	}
	capacity, ok := putSchema.Schema.Properties["capacity_planning"]
	if !ok {
		t.Fatal("settings PUT request schema is missing capacity_planning")
	}
	if _, ok := capacity.Properties["reservations"]; !ok {
		t.Error("settings PUT capacity_planning schema is missing reservations")
	}
}

func TestGroupRuntimeSettingsOpenAPIRequestOmitsRejectedFields(t *testing.T) {
	data, err := GenerateOpenAPIYAML("server.go")
	if err != nil {
		t.Fatalf("GenerateOpenAPIYAML: %v", err)
	}
	var spec struct {
		Paths map[string]map[string]oasOperation `yaml:"paths"`
	}
	if err := yaml.Unmarshal(data, &spec); err != nil {
		t.Fatalf("generated spec is not valid YAML: %v", err)
	}

	operation := spec.Paths["/api/v1/tests/{id}/groups/{group}/runtime-settings"]["patch"]
	if operation.RequestBody == nil {
		t.Fatal("group runtime-settings PATCH has no request body")
	}
	mediaType, ok := operation.RequestBody.Content["application/json"]
	if !ok {
		t.Fatalf("group runtime-settings PATCH content = %#v, want application/json", operation.RequestBody.Content)
	}
	runtimeSettings, ok := mediaType.Schema.Properties["runtime_settings"]
	if !ok {
		t.Fatal("group runtime-settings PATCH request is missing runtime_settings")
	}
	for _, rejected := range []string{"env", "redaction_rules"} {
		if _, ok := runtimeSettings.Properties[rejected]; ok {
			t.Errorf("group runtime-settings PATCH advertises rejected field %q", rejected)
		}
	}
	for _, editable := range []string{"http", "java_vuser", "think_time"} {
		if _, ok := runtimeSettings.Properties[editable]; !ok {
			t.Errorf("group runtime-settings PATCH is missing editable field %q", editable)
		}
	}
	for _, required := range []string{"expected_revision", "fields"} {
		if !openAPIStringSliceContains(mediaType.Schema.Required, required) {
			t.Errorf("group runtime-settings PATCH required fields = %v, want %q", mediaType.Schema.Required, required)
		}
	}
	getOperation := spec.Paths["/api/v1/tests/{id}/groups/{group}/runtime-settings"]["get"]
	getSchema := getOperation.Responses["200"].Content["application/json"].Schema
	if revision, ok := getSchema.Properties["revision"]; !ok || revision.Type != "string" {
		t.Errorf("group runtime-settings GET revision = %#v, want string", revision)
	}
	patchResponse := operation.Responses["200"].Content["application/json"].Schema
	if revision, ok := patchResponse.Properties["revision"]; !ok || revision.Type != "string" {
		t.Errorf("group runtime-settings PATCH response revision = %#v, want string", revision)
	}
	if _, ok := operation.Responses["409"]; !ok {
		t.Error("group runtime-settings PATCH is missing its optimistic-concurrency 409 response")
	}

	parametersPath := spec.Paths["/api/v1/tests/{id}/groups/{group}/parameters"]
	parametersPut := parametersPath["put"]
	if parametersPut.RequestBody == nil {
		t.Fatal("group parameters PUT has no request body")
	}
	parametersRequest := parametersPut.RequestBody.Content["application/json"].Schema
	for _, required := range []string{"expected_revision", "parameters"} {
		if !openAPIStringSliceContains(parametersRequest.Required, required) {
			t.Errorf("group parameters PUT required fields = %v, want %q", parametersRequest.Required, required)
		}
	}
	parametersGetSchema := parametersPath["get"].Responses["200"].Content["application/json"].Schema
	if revision, ok := parametersGetSchema.Properties["revision"]; !ok || revision.Type != "string" {
		t.Errorf("group parameters GET revision = %#v, want string", revision)
	}
	parametersPutSchema := parametersPut.Responses["200"].Content["application/json"].Schema
	if revision, ok := parametersPutSchema.Properties["revision"]; !ok || revision.Type != "string" {
		t.Errorf("group parameters PUT response revision = %#v, want string", revision)
	}
	if _, ok := parametersPut.Responses["409"]; !ok {
		t.Error("group parameters PUT is missing its optimistic-concurrency 409 response")
	}
	for _, route := range []string{"runtime-settings", "parameters"} {
		getOperation := spec.Paths["/api/v1/tests/{id}/groups/{group}/"+route]["get"]
		for _, status := range []string{"404", "500"} {
			if _, ok := getOperation.Responses[status]; !ok {
				t.Errorf("group %s GET does not document %s", route, status)
			}
		}
	}
}
