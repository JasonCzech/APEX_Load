package api

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
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

	archiveImport := spec.Paths["/api/v1/scripts/import/enterprise-script-archive"]["post"]
	if archiveImport.RequestBody == nil {
		t.Fatal("enterprise archive import is missing its multipart request body")
	}
	multipart := archiveImport.RequestBody.Content["multipart/form-data"].Schema
	if file := multipart.Properties["file"]; file.Type != "string" || file.Format != "binary" {
		t.Errorf("enterprise archive file schema = %#v, want binary string", file)
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
