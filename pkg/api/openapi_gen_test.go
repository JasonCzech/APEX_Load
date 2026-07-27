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
	// 429 is emitted when protected import session capacity is exhausted. It is
	// a transient, retryable condition with a Retry-After hint, distinct from the
	// 500 it used to be reported as.
	wantStatuses := []string{"200", "201", "400", "401", "403", "404", "409", "429", "500", "default"}

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
	if catalog.Source != "server.go" {
		t.Fatalf("catalog source = %q, want server.go", catalog.Source)
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
