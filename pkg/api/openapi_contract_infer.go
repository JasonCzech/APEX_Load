package api

// This file derives the portable parts of each HTTP contract from handler ASTs.
// Route registration remains the source of truth for method/path/access metadata;
// handler inference adds query names, request media, and response statuses without
// requiring a second 486-route catalog to be maintained by hand. Explicit schemas
// in openapi_gen.go take precedence when a payload needs a stronger contract.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const maxOpenAPIHandlerCallDepth = 4

const openAPIResponsesAnnotation = "OpenAPI-Responses:"

type handlerContractAnalysis struct {
	queryNames       map[string]bool
	repeatedQueries  map[string]bool
	headerNames      map[string]bool
	repeatedHeaders  map[string]bool
	formValues       map[string]bool
	formFiles        map[string]bool
	statuses         map[int]bool
	responseMedia    map[string]bool
	jsonRequest      bool
	optionalJSONBody bool
	multipartRequest bool
	rawRequest       bool
	jsonResponse     bool
	requestSchema    *oasSchema
	responseSchemas  map[int]oasSchema
}

func newHandlerContractAnalysis() *handlerContractAnalysis {
	return &handlerContractAnalysis{
		queryNames:      map[string]bool{},
		repeatedQueries: map[string]bool{},
		headerNames:     map[string]bool{},
		repeatedHeaders: map[string]bool{},
		formValues:      map[string]bool{},
		formFiles:       map[string]bool{},
		statuses:        map[int]bool{},
		responseMedia:   map[string]bool{},
		responseSchemas: map[int]oasSchema{},
	}
}

func inferRouteContracts(serverGoPath string, routes []discoveredRoute) (map[string]inferredRouteContract, error) {
	dir, err := controllerRouteSourceDirectory(serverGoPath)
	if err != nil {
		return nil, err
	}
	packages, err := parser.ParseDir(token.NewFileSet(), dir, func(info fs.FileInfo) bool {
		name := info.Name()
		return strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
	}, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse API handlers in %s: %w", dir, err)
	}

	functionCandidates := map[string][]*ast.FuncDecl{}
	typeDefinitions := map[string]ast.Expr{}
	explicitResponseStatuses := map[*ast.FuncDecl]map[int]bool{}
	for _, pkg := range packages {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if ok && fn.Body != nil {
					statuses, err := annotatedOpenAPIResponseStatuses(fn.Doc)
					if err != nil {
						return nil, fmt.Errorf("%s: %w", fn.Name.Name, err)
					}
					functionCandidates[fn.Name.Name] = append(functionCandidates[fn.Name.Name], fn)
					explicitResponseStatuses[fn] = statuses
				}
				gen, ok := decl.(*ast.GenDecl)
				if !ok || gen.Tok != token.TYPE {
					continue
				}
				for _, spec := range gen.Specs {
					typeSpec, ok := spec.(*ast.TypeSpec)
					if ok {
						typeDefinitions[typeSpec.Name.Name] = typeSpec.Type
					}
				}
			}
		}
	}
	if err := addOpenAPICrossPackageTypeDefinitions(dir, typeDefinitions); err != nil {
		return nil, err
	}
	functions := map[string]*ast.FuncDecl{}
	functionResults := map[string][]ast.Expr{}
	for name, candidates := range functionCandidates {
		if len(candidates) == 1 {
			functions[name] = candidates[0]
			functionResults[name] = functionResultTypes(candidates[0])
			continue
		}
		// Bare selector names are not type-safe: many receiver types expose Run,
		// Get, Start, and similar methods. Never choose one based on randomized
		// package/file map iteration. Route handlers are known to be Server methods,
		// so retain that single unambiguous root while declining result inference for
		// every ambiguous method name.
		var serverHandler *ast.FuncDecl
		for _, candidate := range candidates {
			if receiverTypeName(candidate) == "Server" && strings.HasPrefix(name, "handle") {
				if serverHandler != nil {
					serverHandler = nil
					break
				}
				serverHandler = candidate
			}
		}
		if serverHandler != nil {
			functions[name] = serverHandler
		}
	}

	contracts := make(map[string]inferredRouteContract, len(routes))
	for _, route := range routes {
		analysis := newHandlerContractAnalysis()
		if route.Handler != "" {
			inspectHandlerContract(route.Handler, functions, functionResults, typeDefinitions, explicitResponseStatuses, analysis, map[string]bool{}, 0)
		}
		applyExplicitRouteContractTypes(route, analysis, typeDefinitions)
		applyRouteRequestSchemaProjection(route, analysis)
		contract := inferredContractForAnalysis(route, analysis)
		applySystemSettingsConcurrencyContract(route, &contract)
		applyDataPoolImportExportContract(route, &contract)
		applyGenericImportBatchContract(route, &contract)
		contracts[route.Method+" "+route.Path] = contract
	}
	return contracts, nil
}

func applyGenericImportBatchContract(route discoveredRoute, contract *inferredRouteContract) {
	if contract == nil || route.Method != http.MethodPost || route.Path != "/api/v1/scripts/import/commit-batch" || contract.RequestBody == nil {
		return
	}
	mediaType, ok := contract.RequestBody.Content["application/json"]
	if !ok {
		return
	}
	profile, ok := mediaType.Schema.Properties["source_profile"]
	if !ok {
		return
	}
	profile.Enum = genericImportSourceProfileWireValues()
	profile.Description = "Importer workflow that produced the drafts. Values are strictly fenced to compatible server-attested source profiles."
	mediaType.Schema.Properties["source_profile"] = profile
	contract.RequestBody.Content["application/json"] = mediaType
}

// openAPICrossPackageDirectories is deliberately bounded to the model packages
// used most heavily by REST request and response contracts. Loading the entire
// module would make generation sensitive to unrelated packages and build-tag
// variants; these packages cover the public storage, DSL, execution, recorder,
// and converter models that handlers decode or return directly.
var openAPICrossPackageDirectories = map[string]string{
	"dsl":      "dsl",
	"engine":   "engine",
	"k6":       "convert/k6",
	"recorder": "recorder",
	"sample":   "convert/sample",
	"storage":  "storage",
}

func addOpenAPICrossPackageTypeDefinitions(apiDir string, typeDefinitions map[string]ast.Expr) error {
	packageNames := make([]string, 0, len(openAPICrossPackageDirectories))
	for packageName := range openAPICrossPackageDirectories {
		packageNames = append(packageNames, packageName)
	}
	sort.Strings(packageNames)
	for _, packageName := range packageNames {
		dir := filepath.Clean(filepath.Join(apiDir, "..", openAPICrossPackageDirectories[packageName]))
		packages, err := parser.ParseDir(token.NewFileSet(), dir, func(info fs.FileInfo) bool {
			name := info.Name()
			return strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
		}, 0)
		if err != nil {
			return fmt.Errorf("parse OpenAPI model package %s in %s: %w", packageName, dir, err)
		}
		parsedPackage, ok := packages[packageName]
		if !ok {
			return fmt.Errorf("parse OpenAPI model package %s in %s: package not found", packageName, dir)
		}
		fileNames := make([]string, 0, len(parsedPackage.Files))
		for fileName := range parsedPackage.Files {
			fileNames = append(fileNames, fileName)
		}
		sort.Strings(fileNames)
		for _, fileName := range fileNames {
			for _, decl := range parsedPackage.Files[fileName].Decls {
				gen, ok := decl.(*ast.GenDecl)
				if !ok || gen.Tok != token.TYPE {
					continue
				}
				for _, spec := range gen.Specs {
					typeSpec, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					typeDefinitions[packageName+"."+typeSpec.Name.Name] = typeSpec.Type
				}
			}
		}
	}
	return nil
}

// explicitRouteContractTypes types operations whose handlers hide their JSON
// contract from AST inference. Some handlers decode through chained decoder or
// helper calls that the bounded walker deliberately does not interpret. The
// system-settings handlers read the raw request body (io.ReadAll feeds the
// omitted-section merge probing, which the walker classifies as a binary
// rawRequest) and write a redacted map[string]any response. Data-pool updates
// write values returned from a storage-interface method, whose cross-package
// result type is likewise not visible to the local function-result walker.
var explicitRouteContractTypes = map[string]struct {
	requestType         string
	requestOptional     bool
	responseTypes       map[int]string
	responseDescription string
}{
	"POST /api/v1/auth/ci/github/exchange": {
		requestType: "githubCIExchangeRequest",
	},
	// Cluster registry and host allocation. The request types are named so the
	// published contract documents the credential fields as write-only inputs
	// rather than an opaque object.
	"POST /api/v1/system/clusters": {
		requestType:   "ClusterConnectionRequest",
		responseTypes: map[int]string{201: "ClusterConnectionResponse"},
	},
	"PUT /api/v1/system/clusters/{id}": {
		requestType:   "ClusterConnectionRequest",
		responseTypes: map[int]string{200: "ClusterConnectionResponse"},
	},
	"POST /api/v1/system/cloud-connections": {
		requestType:   "CloudConnectionRequest",
		responseTypes: map[int]string{201: "CloudConnectionResponse"},
	},
	"PUT /api/v1/system/cloud-connections/{id}": {
		requestType:   "CloudConnectionRequest",
		responseTypes: map[int]string{200: "CloudConnectionResponse"},
	},
	"POST /api/v1/system/cloud-connections/{id}/test": {
		responseTypes:       map[int]string{200: "CloudConnectionTestResponse"},
		responseDescription: "Validates a cloud credential against the live provider without requiring an execution target.",
	},
	"GET /api/v1/system/cloud-connections/{id}/resources": {
		responseTypes:       map[int]string{200: "CloudResourceInventory"},
		responseDescription: "Enumerates the provider resources an execution target can bind to.",
	},
	"POST /api/v1/system/execution-targets": {
		requestType:   "ExecutionTargetRequest",
		responseTypes: map[int]string{201: "storage.ExecutionTarget"},
	},
	"GET /api/v1/system/execution-targets/{id}/capacity": {
		responseTypes:       map[int]string{200: "ExecutionTargetCapacityResponse"},
		responseDescription: "Reports the target's configured capacity envelope and its live provider instance inventory.",
	},
	"PUT /api/v1/system/execution-targets/{id}": {
		requestType:   "ExecutionTargetRequest",
		responseTypes: map[int]string{200: "storage.ExecutionTarget"},
	},
	"GET /api/v1/generator-gateway/connect": {
		responseTypes:       map[int]string{http.StatusSwitchingProtocols: "GeneratorGatewayConnectResponse"},
		responseDescription: "Upgrades an authenticated generator-originated WebSocket to the reverse gRPC transport.",
	},
	"PUT /api/v1/fleet/pools/{id}/allocation": {
		requestType:   "FleetPoolAllocationRequest",
		responseTypes: map[int]string{200: "FleetPoolAllocationResponse"},
	},
	"GET /api/v1/fleet/provision-requests/{id}/instances": {
		responseTypes: map[int]string{200: "CapacityInstanceListResponse"},
	},
	"POST /api/v1/ci/runs/{id}/abort": {
		requestType:     "ciAbortRequest",
		requestOptional: true,
	},
	"POST /api/v1/correlations/diff": {
		requestType: "correlationDiffRequest",
	},
	"POST /api/v1/integrations/github/sync-jobs": {
		requestType: "githubSyncJobRequest",
	},
	"PUT /api/v1/projects/{id}/github/policy": {
		requestType: "storage.GitHubProjectPolicy",
	},
	"POST /api/v1/scripts/{id}/diagnose-replay": {
		requestType: "scriptReplayDiagnosisRequest",
	},
	"POST /api/v1/scripts/{id}/export": {
		requestType:     "scriptExportRequest",
		requestOptional: true,
	},
	"POST /api/v1/scripts/{id}/github/push": {
		requestType: "githubPushRequest",
	},
	"PUT /api/v1/scripts/authoring/sessions/{id}": {
		requestType: "authoringSessionRequest",
	},
	"POST /api/v1/scripts/import/github": {
		requestType: "githubImportRequest",
	},
	"POST /api/v1/scripts/import/repairs/preview": {requestType: "ImportRepairRequest", responseTypes: map[int]string{http.StatusOK: "ImportRepairResponse"}},
	"POST /api/v1/scripts/import/repairs/apply":   {requestType: "ImportRepairRequest", responseTypes: map[int]string{http.StatusOK: "ImportRepairResponse"}},
	"POST /api/v1/scripts/secret-bindings/apply": {
		requestType: "applySecretBindingsRequest",
	},
	"POST /api/v1/selectors/diagnose": {
		requestType: "selectorDiagnoseRequest",
	},
	"POST /api/v1/system/deployment-lease/acquire": {
		requestType: "deploymentLeaseRequest",
	},
	"POST /api/v1/system/deployment-lease/release": {
		requestType: "deploymentLeaseRequest",
	},
	"POST /api/v1/system/deployment-lease/renew": {
		requestType: "deploymentLeaseRequest",
	},
	"POST /api/v1/tests/{id}/github/check-run": {
		requestType: "githubCheckPublishRequest",
	},
	"POST /api/v1/tests/{id}/publish/powerbi": {
		requestType:     "powerBIPublishRequest",
		requestOptional: true,
	},
	"GET /api/v1/system/settings": {
		responseTypes:       map[int]string{http.StatusOK: "SystemSettings"},
		responseDescription: "System settings document. notifications.webhooks[].api_token is always redacted to an empty string; a has_api_token boolean reports whether a token is stored.",
	},
	"PUT /api/v1/system/settings": {
		requestType:         "SystemSettings",
		responseTypes:       map[int]string{http.StatusOK: "SystemSettings"},
		responseDescription: "Updated system settings document. Top-level sections omitted from the request body are preserved from the stored settings; send a section explicitly (e.g. \"thresholds\": {}) to reset it. notifications.webhooks[].api_token is always redacted in the response.",
	},
	"PUT /api/v1/data-pools/{id}": {
		responseTypes: map[int]string{http.StatusOK: "storage.SharedDataPool"},
	},
	"PUT /api/v1/data-pools/{id}/rows/{row_id}": {
		responseTypes: map[int]string{http.StatusOK: "storage.SharedDataPoolRow"},
	},
}

func applyExplicitRouteContractTypes(route discoveredRoute, analysis *handlerContractAnalysis, typeDefinitions map[string]ast.Expr) {
	override, ok := explicitRouteContractTypes[route.Method+" "+route.Path]
	if !ok {
		return
	}
	if override.requestType != "" {
		schema := openAPISchemaForASTType(explicitOpenAPITypeExpression(override.requestType), typeDefinitions, map[string]bool{}, 0)
		if route.Method == http.MethodPut && route.Path == "/api/v1/system/settings" {
			removeSystemSettingsInternalRevision(&schema)
			addSystemSettingsUpdateCompatibilityFields(&schema)
			closeStructuredOpenAPISchema(&schema)
			// PUT replaces only the top-level sections the caller supplies.
			// No individual root section is mandatory; inferred request
			// requirements are cleared recursively later because an explicitly
			// empty section is the supported reset form.
			schema.Required = nil
		}
		if override.requestType == "ExecutionTargetRequest" {
			schema = executionTargetRequestUnion(schema)
		}
		// jsonRequest takes precedence over rawRequest in inferredRequestBody,
		// so the octet-stream fallback disappears.
		analysis.jsonRequest = true
		analysis.rawRequest = false
		analysis.requestSchema = &schema
		analysis.optionalJSONBody = override.requestOptional
	}
	for status, typeName := range override.responseTypes {
		schema := openAPISchemaForASTType(explicitOpenAPITypeExpression(typeName), typeDefinitions, map[string]bool{}, 0)
		if typeName == "SystemSettings" {
			removeSystemSettingsInternalRevision(&schema)
			addSystemSettingsResponseProjectionFields(&schema)
		}
		schema.Description = override.responseDescription
		analysis.jsonResponse = true
		analysis.responseSchemas[status] = schema
	}
}

// executionTargetRequestUnion republishes the inferred ExecutionTargetRequest
// as a tagged union keyed on `kind`.
//
// The Go struct carries every provider spec as a sibling optional pointer, so
// the inferred schema says a caller may send `kind: azure_container_apps`
// alongside an `aws_ecs_fargate` block — which Validate() rejects at runtime but
// the published contract permitted. Each variant here pins `kind` to a single
// enum value and requires that kind's own spec object, so the constraint the
// server actually enforces is the one clients generate against.
func executionTargetRequestUnion(base oasSchema) oasSchema {
	specProperty := map[string]string{
		"kubernetes":                "",
		"azure_container_apps":      "azure_container_apps",
		"azure_container_instances": "azure_container_instances",
		"aws_ecs_fargate":           "aws_ecs_fargate",
		"static":                    "static",
	}
	kinds := make([]string, 0, len(specProperty))
	for kind := range specProperty {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)

	variants := make([]oasSchema, 0, len(kinds))
	for _, kind := range kinds {
		variant := oasSchema{Type: base.Type, Properties: map[string]oasSchema{}}
		for name, property := range base.Properties {
			// Drop the other kinds' spec blocks so a variant describes exactly
			// one provider shape.
			if isExecutionTargetSpecProperty(specProperty, name) && name != specProperty[kind] {
				continue
			}
			variant.Properties[name] = property
		}
		kindSchema := base.Properties["kind"]
		kindSchema.Enum = []string{kind}
		variant.Properties["kind"] = kindSchema
		// No `required` is set: clearOpenAPISchemaRequired strips it from every
		// request schema, oneOf branches included, because partial updates are
		// the supported form. The enum-pinned `kind` is what selects the branch.
		variant.Description = "Execution target of kind " + kind + "."
		variants = append(variants, variant)
	}
	return oasSchema{
		Description:   base.Description,
		OneOf:         variants,
		Discriminator: &oasDiscriminator{PropertyName: "kind"},
	}
}

// isExecutionTargetSpecProperty reports whether a property name is one of the
// provider spec blocks, i.e. a value in the kind->spec map rather than a key.
func isExecutionTargetSpecProperty(specProperty map[string]string, name string) bool {
	for _, spec := range specProperty {
		if spec != "" && spec == name {
			return true
		}
	}
	return false
}

func explicitOpenAPITypeExpression(typeName string) ast.Expr {
	packageName, selectedType, qualified := strings.Cut(typeName, ".")
	if qualified {
		return &ast.SelectorExpr{
			X:   &ast.Ident{Name: packageName},
			Sel: &ast.Ident{Name: selectedType},
		}
	}
	return &ast.Ident{Name: typeName}
}

func removeSystemSettingsInternalRevision(schema *oasSchema) {
	if schema == nil {
		return
	}
	delete(schema.Properties, "_revision")
	required := schema.Required[:0]
	for _, field := range schema.Required {
		if field != "_revision" {
			required = append(required, field)
		}
	}
	schema.Required = required
}

// applyRouteRequestSchemaProjection removes response-only or policy-rejected
// fields from inferred request documents and strengthens types that cannot be
// represented faithfully by the handler's decoding helper types.
func applyRouteRequestSchemaProjection(route discoveredRoute, analysis *handlerContractAnalysis) {
	if analysis == nil || analysis.requestSchema == nil {
		return
	}

	switch {
	case route.Method == http.MethodPost && route.Path == "/api/v1/data-pools":
		// Identity and timestamps are assigned after decoding; keep only the
		// caller-owned creation fields from the shared storage/response model.
		for _, field := range []string{"id", "created_at", "updated_at", "archived_at"} {
			delete(analysis.requestSchema.Properties, field)
		}
	case route.Method == http.MethodPut && route.Path == "/api/v1/data-pools/{id}":
		// json.RawMessage lets the handler distinguish omission from explicit
		// null, but its inferred `{}` schema would advertise arbitrary JSON.
		// The accepted wire values are exactly a timestamp or null.
		analysis.requestSchema.Properties["archived_at"] = oasSchema{
			Type:        "string",
			Format:      "date-time",
			Nullable:    true,
			Description: "Archive timestamp, or null to restore the data pool. Omit to preserve the current archive state.",
		}
		delete(analysis.requestSchema.Properties, "project_id")
	case route.Method == http.MethodPost && route.Path == "/api/v1/data-pools/{id}/rows":
		// The create handler owns row identity, parent scope, optimistic
		// version, and timestamps. Reusing the storage/response type for JSON
		// decoding must not expose those server-owned fields as create inputs.
		stringValue := oasSchema{Type: "string"}
		additional := true
		analysis.requestSchema = &oasSchema{
			Type: "object",
			Properties: map[string]oasSchema{
				"data":   {Type: "object", AdditionalProperties: &stringValue},
				"status": {Type: "string"},
			},
			AdditionalProperties: &additional,
		}
	case route.Method == http.MethodPatch &&
		(route.Path == "/api/v1/scripts/{id}/move" || route.Path == "/api/v1/tests/{id}/move"):
		// A null folder_id moves the resource to the root. Pointer inference
		// preserves the scalar type but needs an explicit wire-level null
		// projection.
		folderID, ok := analysis.requestSchema.Properties["folder_id"]
		if !ok {
			return
		}
		folderID.Nullable = true
		analysis.requestSchema.Properties["folder_id"] = folderID
	case route.Method == http.MethodPatch && route.Path == "/api/v1/tests/{id}/groups/{group}/runtime-settings":
		// The handler accepts the shared RuntimeSettingsOverride Go type for
		// decoding, but deliberately rejects environment and redaction-rule
		// edits at this scope.
		runtimeSettings, ok := analysis.requestSchema.Properties["runtime_settings"]
		if !ok {
			return
		}
		delete(runtimeSettings.Properties, "env")
		delete(runtimeSettings.Properties, "redaction_rules")
		analysis.requestSchema.Properties["runtime_settings"] = runtimeSettings
	}
}

func applyDataPoolImportExportContract(route discoveredRoute, contract *inferredRouteContract) {
	if contract == nil {
		return
	}
	switch {
	case route.Method == http.MethodPost && route.Path == "/api/v1/data-pools/{id}/rows/import":
		if contract.RequestBody == nil {
			return
		}
		mediaType, ok := contract.RequestBody.Content["multipart/form-data"]
		if !ok {
			return
		}
		mediaType.Schema.Required = mergeOpenAPIRequiredFields(mediaType.Schema.Required, []string{"file"})
		contract.RequestBody.Content["multipart/form-data"] = mediaType
	case route.Method == http.MethodGet && route.Path == "/api/v1/data-pools/{id}/rows/export":
		if contract.Responses == nil {
			contract.Responses = make(map[string]oasResponse)
		}
		delete(contract.Responses, "2XX")
		contract.Responses[strconv.Itoa(http.StatusOK)] = oasResponse{
			Description: "Data pool rows in the APEX CSV round-trip format.",
			Content: map[string]oasMediaType{
				"text/csv": {Schema: oasSchema{Type: "string"}},
			},
			Headers: map[string]oasHeader{
				"Cache-Control": {
					Description: "Prevents caching of exported raw data.",
					Schema:      oasParamSchema{Type: "string"},
				},
				"Content-Disposition": {
					Description: "Attachment filename ending in .apexload.csv.",
					Schema:      oasParamSchema{Type: "string"},
				},
				"X-APEXLoad-Sensitivity": {
					Description: "Sensitivity classification for the raw export.",
					Schema:      oasParamSchema{Type: "string"},
				},
			},
		}
	}
}

// redactedSystemSettingsResponse adds has_api_token dynamically after encoding
// SystemSettings. Keep that response projection in the generated schema.
func addSystemSettingsResponseProjectionFields(schema *oasSchema) {
	addSystemSettingsWebhookTokenProjectionField(
		schema,
		"True when a write-only Dynatrace API token is stored for this webhook.",
	)
	if schema.Properties == nil {
		schema.Properties = map[string]oasSchema{}
	}
	schema.Properties["revision"] = oasSchema{
		Type:        "string",
		Description: "Opaque disclosure-safe revision corresponding to the ETag response header.",
	}
	schema.Required = mergeOpenAPIRequiredFields(schema.Required, []string{"revision"})
}

// addSystemSettingsUpdateCompatibilityFields documents the one response
// projection accepted by the strict PUT decoder. The value is ignored; it is
// admitted only so clients can safely echo a GET response.
func addSystemSettingsUpdateCompatibilityFields(schema *oasSchema) {
	addSystemSettingsWebhookTokenProjectionField(
		schema,
		"Response projection accepted and ignored when echoing system settings.",
	)
	if schema.Properties == nil {
		schema.Properties = map[string]oasSchema{}
	}
	schema.Properties["revision"] = oasSchema{
		Type:        "string",
		Description: "Response projection accepted and ignored when echoing system settings.",
	}
	schema.Properties["expected_revision"] = oasSchema{
		Type:        "string",
		Description: "Body-form optimistic concurrency precondition. Required only when If-Match is omitted.",
	}
}

func applySystemSettingsConcurrencyContract(route discoveredRoute, contract *inferredRouteContract) {
	if contract == nil || route.Path != "/api/v1/system/settings" {
		return
	}
	if response, ok := contract.Responses[strconv.Itoa(http.StatusOK)]; ok {
		if response.Headers == nil {
			response.Headers = map[string]oasHeader{}
		}
		response.Headers["ETag"] = oasHeader{
			Description: "Strong validator for this redacted system-settings representation. Send it in If-Match when updating.",
			Schema:      oasParamSchema{Type: "string"},
		}
		contract.Responses[strconv.Itoa(http.StatusOK)] = response
	}
	if route.Method != http.MethodPut {
		return
	}
	found := false
	for i := range contract.HeaderParameters {
		if strings.EqualFold(contract.HeaderParameters[i].Name, "If-Match") {
			contract.HeaderParameters[i].Description = "ETag returned by GET /api/v1/system/settings. Required unless expected_revision is supplied in the request body."
			found = true
			break
		}
	}
	if !found {
		contract.HeaderParameters = append(contract.HeaderParameters, oasParameter{
			Name:        "If-Match",
			In:          "header",
			Description: "ETag returned by GET /api/v1/system/settings. Required unless expected_revision is supplied in the request body.",
			Required:    false,
			Schema:      oasParamSchema{Type: "string"},
		})
	}
}

func addSystemSettingsWebhookTokenProjectionField(schema *oasSchema, description string) {
	if schema == nil {
		return
	}
	notifications, ok := schema.Properties["notifications"]
	if !ok {
		return
	}
	webhooks, ok := notifications.Properties["webhooks"]
	if !ok || webhooks.Items == nil {
		return
	}
	if webhooks.Items.Properties == nil {
		webhooks.Items.Properties = map[string]oasSchema{}
	}
	webhooks.Items.Properties[redactedSecretConfiguredField] = oasSchema{
		Type:        "boolean",
		Description: description,
	}
}

// closeStructuredOpenAPISchema mirrors json.Decoder.DisallowUnknownFields for
// an explicitly inferred request schema. Struct-shaped objects are closed
// recursively, while map-shaped objects retain their declared additional-value
// schema.
func closeStructuredOpenAPISchema(schema *oasSchema) {
	if schema == nil {
		return
	}
	if schema.Type == "object" && schema.Properties != nil {
		schema.AdditionalProperties = boolPointer(false)
		for name, property := range schema.Properties {
			closeStructuredOpenAPISchema(&property)
			schema.Properties[name] = property
		}
	}
	closeStructuredOpenAPISchema(schema.Items)
}

func receiverTypeName(fn *ast.FuncDecl) string {
	if fn == nil || fn.Recv == nil || len(fn.Recv.List) != 1 {
		return ""
	}
	expr := fn.Recv.List[0].Type
	if pointer, ok := expr.(*ast.StarExpr); ok {
		expr = pointer.X
	}
	if ident, ok := expr.(*ast.Ident); ok {
		return ident.Name
	}
	return ""
}

func inspectHandlerContract(name string, functions map[string]*ast.FuncDecl, functionResults map[string][]ast.Expr, typeDefinitions map[string]ast.Expr, explicitResponseStatuses map[*ast.FuncDecl]map[int]bool, analysis *handlerContractAnalysis, visiting map[string]bool, depth int) {
	if depth > maxOpenAPIHandlerCallDepth || visiting[name] {
		return
	}
	fn := functions[name]
	if fn == nil || fn.Body == nil {
		return
	}
	visiting[name] = true
	defer delete(visiting, name)

	for status := range explicitResponseStatuses[fn] {
		analysis.statuses[status] = true
	}

	queryVariables := findQueryVariables(fn.Body)
	uploadVariables := findImportUploadVariables(fn.Body)
	variableTypes := findLocalVariableTypes(fn.Body, functionResults)
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		if index, ok := node.(*ast.IndexExpr); ok && receiverIsQuery(index.X, queryVariables) {
			if value, ok := stringLiteral(index.Index); ok && plausibleQueryName(value) {
				analysis.queryNames[value] = true
				analysis.repeatedQueries[value] = true
			}
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		callName := calledFunctionName(call.Fun)

		if selector, ok := call.Fun.(*ast.SelectorExpr); ok && len(call.Args) > 0 {
			if (selector.Sel.Name == "Get" || selector.Sel.Name == "Has" || selector.Sel.Name == "Values") && receiverIsQuery(selector.X, queryVariables) {
				if value, ok := stringLiteral(call.Args[0]); ok && plausibleQueryName(value) {
					analysis.queryNames[value] = true
					if selector.Sel.Name == "Values" {
						analysis.repeatedQueries[value] = true
					}
				}
			}
			if (selector.Sel.Name == "Get" || selector.Sel.Name == "Values") && receiverIsRequestHeader(selector.X) {
				if value, ok := stringLiteral(call.Args[0]); ok && documentableRequestHeader(value) {
					analysis.headerNames[value] = true
					if selector.Sel.Name == "Values" {
						analysis.repeatedHeaders[value] = true
					}
				}
			}
			if selector.Sel.Name == "FormValue" {
				if value, ok := stringLiteral(call.Args[0]); ok {
					analysis.multipartRequest = true
					analysis.formValues[value] = true
				}
			}
			// An importUpload option resolves against the multipart form on a
			// file upload and against the query string on a JSON or raw body,
			// so one Value call documents both surfaces.
			if selector.Sel.Name == "Value" && receiverIsImportUpload(selector.X, uploadVariables) {
				if value, ok := stringLiteral(call.Args[0]); ok {
					analysis.formValues[value] = true
					if plausibleQueryName(value) {
						analysis.queryNames[value] = true
					}
				}
			}
			if selector.Sel.Name == "FormFile" {
				if value, ok := stringLiteral(call.Args[0]); ok {
					analysis.multipartRequest = true
					analysis.formFiles[value] = true
				}
			}
		}

		if strings.Contains(strings.ToLower(callName), "query") {
			for _, arg := range call.Args {
				if value, ok := stringLiteral(arg); ok && plausibleQueryName(value) {
					analysis.queryNames[value] = true
				}
			}
		}
		if callName == "parseLimitOffsetQuery" {
			analysis.queryNames["limit"] = true
			analysis.queryNames["offset"] = true
		}
		// decodeImportUpload resolves these itself for every import route, off
		// fields the AST walk cannot attribute back to r.URL.Query().
		if callName == "decodeImportUpload" {
			analysis.queryNames["filename"] = true
			analysis.queryNames["project_id"] = true
		}
		// bareArtifactName falls back to ?name= when the body is the artifact.
		if callName == "bareArtifactName" {
			analysis.queryNames["name"] = true
		}

		switch callName {
		case "decodeJSONBody", "decodeRequestBody":
			analysis.jsonRequest = true
			setInferredRequestSchema(call, variableTypes, typeDefinitions, analysis)
		case "decodeOptionalJSONBody", "decodeOptionalJSONBodyAllowingUnknownFields":
			analysis.jsonRequest = true
			analysis.optionalJSONBody = true
			setInferredRequestSchema(call, variableTypes, typeDefinitions, analysis)
		case "ParseMultipartForm", "MultipartReader":
			analysis.multipartRequest = true
		case "NewDecoder":
			if expressionContainsRequestBody(call) {
				analysis.jsonRequest = true
			}
		case "ReadAll":
			if expressionContainsRequestBody(call) {
				analysis.rawRequest = true
			}
		case "writeJSON":
			analysis.jsonResponse = true
			if len(call.Args) > 1 {
				if status, ok := openAPIHTTPStatus(call.Args[1]); ok {
					analysis.statuses[status] = true
					setInferredResponseSchema(call, status, variableTypes, functionResults, typeDefinitions, analysis)
				}
			}
		case "writeError":
			analysis.jsonResponse = true
			if len(call.Args) > 1 {
				if status, ok := openAPIHTTPStatus(call.Args[1]); ok {
					analysis.statuses[status] = true
				}
			}
		case "Error":
			if len(call.Args) > 2 {
				if status, ok := openAPIHTTPStatus(call.Args[2]); ok {
					analysis.statuses[status] = true
				}
			}
		case "WriteHeader":
			if len(call.Args) > 0 {
				if status, ok := openAPIHTTPStatus(call.Args[0]); ok {
					analysis.statuses[status] = true
				}
			}
		case "Set", "Add":
			if len(call.Args) > 1 {
				key, keyOK := stringLiteral(call.Args[0])
				value, valueOK := stringLiteral(call.Args[1])
				if keyOK && valueOK && strings.EqualFold(key, "Content-Type") {
					analysis.responseMedia[normalizeMediaType(value)] = true
				}
			}
		}

		if depth < maxOpenAPIHandlerCallDepth && functions[callName] != nil && callPassesRequest(call) && shouldInspectCalledContract(callName) {
			inspectHandlerContract(callName, functions, functionResults, typeDefinitions, explicitResponseStatuses, analysis, visiting, depth+1)
		}
		return true
	})
}

// annotatedOpenAPIResponseStatuses returns response codes explicitly declared
// on a handler or a request-carrying helper. It is reserved for response paths
// hidden behind helpers that the bounded request call-graph analysis cannot
// safely inspect. For example:
//
//	OpenAPI-Responses: 409, 500, 503
func annotatedOpenAPIResponseStatuses(doc *ast.CommentGroup) (map[int]bool, error) {
	statuses := map[int]bool{}
	if doc == nil {
		return statuses, nil
	}
	for _, line := range strings.Split(doc.Text(), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, openAPIResponsesAnnotation) {
			continue
		}
		fields := strings.Split(strings.TrimSpace(strings.TrimPrefix(line, openAPIResponsesAnnotation)), ",")
		for _, field := range fields {
			rawStatus := strings.TrimSpace(field)
			status, err := strconv.Atoi(rawStatus)
			if err != nil || status < 100 || status > 599 {
				return nil, fmt.Errorf("invalid %s status %q", openAPIResponsesAnnotation, rawStatus)
			}
			statuses[status] = true
		}
	}
	return statuses, nil
}

func shouldInspectCalledContract(name string) bool {
	// Follow handler delegation and focused query parsers. Broad request/auth
	// resolution helpers deliberately dispatch across many unrelated route
	// lookups and would contaminate one operation with another operation's body.
	return strings.HasPrefix(name, "handle") ||
		strings.Contains(strings.ToLower(name), "query") ||
		strings.HasPrefix(name, "parse") ||
		strings.HasPrefix(name, "decode") ||
		strings.HasPrefix(name, "read") ||
		strings.HasPrefix(name, "serve") ||
		strings.HasPrefix(name, "write") ||
		strings.HasPrefix(name, "wants")
}

func inferredContractForAnalysis(route discoveredRoute, analysis *handlerContractAnalysis) inferredRouteContract {
	headerParameters := inferredHeaderParameters(analysis)
	if route.Path == bridgePairPath || route.Path == bridgeStatusPath {
		headerParameters = appendOrReplaceHeaderParameter(headerParameters, oasParameter{
			Name:        "Origin",
			In:          "header",
			Description: "Browser origin bound to the recording-agent pairing session.",
			Required:    true,
			Schema:      oasParamSchema{Type: "string"},
		})
	}
	responses := inferredResponses(route, analysis)
	if isScheduleUpdateRoute(route.Method, route.Path) {
		headerParameters = appendOrReplaceHeaderParameter(headerParameters, oasParameter{
			Name:        "If-Match",
			In:          "header",
			Description: "Required unless expected_revision is supplied in the JSON body. Must identify the positive current schedule revision; weak ETag syntax is accepted.",
			Required:    false,
			Schema:      oasParamSchema{Type: "string"},
		})
		if responses == nil {
			responses = map[string]oasResponse{}
		}
		responses["409"] = jsonErrorResponse("The schedule changed after the supplied revision was read, or an active occurrence prevents the update.")
		responses["428"] = jsonErrorResponse("Supply either expected_revision in the JSON body or the If-Match header.")
	}
	return inferredRouteContract{
		QueryParameters:  inferredQueryParameters(route, analysis),
		HeaderParameters: headerParameters,
		RequestBody:      inferredRequestBody(route, analysis),
		Responses:        responses,
	}
}

func isScheduleUpdateRoute(method, path string) bool {
	if method != http.MethodPut {
		return false
	}
	return path == "/api/v1/scenarios/{id}/schedules/{schedule_id}" ||
		path == "/api/v1/execution-plans/{id}/schedules/{schedule_id}"
}

func appendOrReplaceHeaderParameter(parameters []oasParameter, replacement oasParameter) []oasParameter {
	for index := range parameters {
		if strings.EqualFold(parameters[index].Name, replacement.Name) {
			parameters[index] = replacement
			sort.Slice(parameters, func(i, j int) bool {
				return strings.ToLower(parameters[i].Name) < strings.ToLower(parameters[j].Name)
			})
			return parameters
		}
	}
	parameters = append(parameters, replacement)
	sort.Slice(parameters, func(i, j int) bool {
		return strings.ToLower(parameters[i].Name) < strings.ToLower(parameters[j].Name)
	})
	return parameters
}

func inferredHeaderParameters(analysis *handlerContractAnalysis) []oasParameter {
	parameters := make([]oasParameter, 0, len(analysis.headerNames))
	for name := range analysis.headerNames {
		schema := oasParamSchema{Type: "string"}
		if analysis.repeatedHeaders[name] {
			item := schema
			schema = oasParamSchema{Type: "array", Items: &item}
		}
		parameters = append(parameters, oasParameter{
			Name:        name,
			In:          "header",
			Description: "Handler-read request header; whether it is required can depend on authentication mode or operation state.",
			Required:    false,
			Schema:      schema,
		})
	}
	sort.Slice(parameters, func(i, j int) bool {
		return strings.ToLower(parameters[i].Name) < strings.ToLower(parameters[j].Name)
	})
	return parameters
}

func inferredQueryParameters(route discoveredRoute, analysis *handlerContractAnalysis) []oasParameter {
	parameters := make([]oasParameter, 0, len(analysis.queryNames))
	for name := range analysis.queryNames {
		schema := queryParameterSchema(route, name, analysis.repeatedQueries[name])
		description := "Handler-defined query parameter."
		deprecated := false
		if compatibilityDescription, isDeprecated := deprecatedCompatibilityInput(route, name); isDeprecated {
			description = compatibilityDescription
			deprecated = true
		}
		parameters = append(parameters, oasParameter{
			Name:        name,
			In:          "query",
			Description: description,
			Deprecated:  deprecated,
			Required:    requiredQueryParameter(route.Method, route.Path, name),
			Schema:      schema,
		})
	}
	sort.Slice(parameters, func(i, j int) bool { return parameters[i].Name < parameters[j].Name })
	return parameters
}

func queryParameterSchema(route discoveredRoute, name string, repeated bool) oasParamSchema {
	integerNames := map[string]bool{
		"limit": true, "offset": true, "page": true, "page_size": true, "sample_limit": true,
		"top": true, "window_seconds": true, "bucket_seconds": true, "max_points": true,
		"step_seconds": true, "retention_days": true, "max_events": true, "max_destinations": true,
		"min_bytes": true, "max_bytes": true, "min_duration_ms": true, "max_duration_ms": true,
	}
	booleanNames := map[string]bool{
		"include_archived": true, "include_deleted": true, "include_inactive": true,
		"include_steps": true, "include_evidence": true, "force": true, "download": true,
		"success": true, "raw": true, "scrub": true, "requests": true, "include_requests": true,
		"include_excluded": true, "redact_sensitive": true, "include_static_assets": true,
		"salesforce_native": true, "agentforce_transform": true, "sandbox": true, "capacity": true,
	}
	dateTimeNames := map[string]bool{
		"created_after": true, "created_before": true, "time_from": true, "time_to": true,
		"from": true, "to": true, "start": true, "end": true,
	}
	item := oasParamSchema{Type: "string"}
	if route.Method == http.MethodPost && route.Path == "/api/v1/scripts/import/bruno/inspect" && name == "output_mode" {
		item.Enum = []string{"canonical", "native_requests"}
	} else if route.Method == http.MethodPost && route.Path == "/api/v1/scripts/import/bruno/inspect" && name == "sandbox" {
		item.Type = "string"
	} else if integerNames[name] {
		item.Type = "integer"
		item.Format = "int64"
	} else if booleanNames[name] {
		item.Type = "boolean"
	} else if dateTimeNames[name] {
		item.Format = "date-time"
	}
	if repeated {
		return oasParamSchema{Type: "array", Items: &item}
	}
	return item
}

func requiredQueryParameter(method, path, name string) bool {
	required := map[string]map[string]bool{
		"GET /api/v1/tests/{id}/errors/evidence":                   {"signature": true},
		"GET /api/v1/applications/{id}/load-comparison":            {"scope": true},
		"GET /api/v1/library/catalog":                              {"kind": true},
		"GET /api/v1/library/remotes/{id}/catalog":                 {"kind": true},
		"GET /api/v1/auth/effective-access/principal":              {"principal_type": true, "principal_id": true},
		"GET /api/v1/integrations/github/branches":                 {"owner": true, "repo": true},
		"GET /api/v1/integrations/github/contents":                 {"owner": true, "repo": true},
		"GET /api/v1/browser-recorder/sessions/{id}/storage-state": {},
	}
	return required[method+" "+path][name]
}

func inferredRequestBody(route discoveredRoute, analysis *handlerContractAnalysis) *oasRequestBody {
	if analysis.multipartRequest {
		properties := map[string]oasSchema{}
		for name := range analysis.formValues {
			property := oasSchema{Type: "string"}
			if description, deprecated := deprecatedCompatibilityInput(route, name); deprecated {
				property.Description = description
				property.Deprecated = true
			}
			properties[name] = property
		}
		for name := range analysis.formFiles {
			properties[name] = oasSchema{Type: "string", Format: "binary"}
		}
		additional := true
		return &oasRequestBody{
			Required: true,
			Content: map[string]oasMediaType{
				"multipart/form-data": {Schema: oasSchema{Type: "object", Properties: properties, AdditionalProperties: &additional}},
			},
		}
	}
	if analysis.jsonRequest {
		additional := true
		schema := oasSchema{Type: "object", Description: "Handler-defined JSON request object.", AdditionalProperties: &additional}
		if analysis.requestSchema != nil {
			schema = *analysis.requestSchema
		}
		// `omitempty` is an encoding rule, not a decode-time validation rule.
		// Structural inference still marks response fields accurately, but an
		// inferred request starts with no required fields at any depth. Route
		// semantics below then opt top-level fields back in. This also keeps
		// explicit empty-object reset forms such as {"general": {}} valid.
		clearOpenAPISchemaRequired(&schema)
		if required, declared := explicitRequestRequiredFields(route.Method, route.Path, schema.Properties); declared {
			schema.Required = required
		}
		if isScheduleUpdateRoute(route.Method, route.Path) {
			if expectedRevision, ok := schema.Properties["expected_revision"]; ok {
				expectedRevision.Description = "Required unless the If-Match header is supplied. Must be the positive current schedule revision."
				schema.Properties["expected_revision"] = expectedRevision
			}
		}
		return &oasRequestBody{
			Required: !analysis.optionalJSONBody,
			Content: map[string]oasMediaType{
				"application/json": {Schema: schema},
			},
		}
	}
	if analysis.rawRequest {
		return &oasRequestBody{
			Required: true,
			Content: map[string]oasMediaType{
				"application/octet-stream": {Schema: oasSchema{Type: "string", Format: "binary"}},
			},
		}
	}
	return nil
}

func clearOpenAPISchemaRequired(schema *oasSchema) {
	if schema == nil {
		return
	}
	schema.Required = nil
	for name, property := range schema.Properties {
		clearOpenAPISchemaRequired(&property)
		schema.Properties[name] = property
	}
	clearOpenAPISchemaRequired(schema.Items)
	for index := range schema.OneOf {
		clearOpenAPISchemaRequired(&schema.OneOf[index])
	}
	switch additional := schema.AdditionalProperties.(type) {
	case oasSchema:
		clearOpenAPISchemaRequired(&additional)
		schema.AdditionalProperties = additional
	case *oasSchema:
		clearOpenAPISchemaRequired(additional)
	}
}

func deprecatedCompatibilityInput(route discoveredRoute, name string) (string, bool) {
	if route.Method != http.MethodPost ||
		(name != "allow_java_vuser_runner") {
		return "", false
	}
	switch route.Path {
	case "/api/v1/scripts/import/enterprise-script-archive",
		"/api/v1/scripts/import/enterprise-script-archive/validate":
		return "Deprecated compatibility input; accepted but ignored. Every LoadRunner Java archive produces one combined java_vuser draft regardless of this value.", true
	default:
		return "", false
	}
}

func explicitRequestRequiredFields(method, path string, properties map[string]oasSchema) ([]string, bool) {
	declared := map[string][]string{
		"POST /api/v1/auth/login":                            {"password", "username"},
		"POST /api/v1/auth/ci/github/exchange":               {"id_token", "project_id"},
		"POST /api/v1/correlations/diff":                     {"recording_a", "recording_b"},
		"POST /api/v1/scenarios":                             {"name", "duration", "groups"},
		"PUT /api/v1/scenarios/{id}":                         {"name", "duration", "groups"},
		"POST /api/v1/scenarios/{id}/schedules":              {"name", "cron_expr"},
		"PUT /api/v1/scenarios/{id}/schedules/{schedule_id}": {"name", "cron_expr"},
		"POST /api/v1/scenarios/{id}/schedules/preview":      {"cron_expr"},
		"POST /api/v1/scenarios/{id}/schedules/{schedule_id}/occurrences/{occurrence_id}/resolve":       {"action"},
		"POST /api/v1/execution-plans/{id}/schedules":                                                   {"cron_expr"},
		"PUT /api/v1/execution-plans/{id}/schedules/{schedule_id}":                                      {"cron_expr"},
		"POST /api/v1/execution-plans/{id}/schedules/{schedule_id}/occurrences/{occurrence_id}/resolve": {"action"},
		"POST /api/v1/execution-plans":                                                                  {"name"},
		"PUT /api/v1/execution-plans/{id}":                                                              {"name", "revision"},
		"POST /api/v1/auth/users":                                                                       {"subject"},
		"POST /api/v1/auth/service-accounts":                                                            {"name"},
		"PUT /api/v1/applications/{id}":                                                                 {"updated_at"},
		"PUT /api/v1/releases/{id}":                                                                     {"updated_at"},
		"POST /api/v1/data-pools":                                                                       {"name"},
		"PUT /api/v1/data-pools/{id}":                                                                   {"updated_at"},
		"POST /api/v1/data-pools/{id}/rows":                                                             {"data"},
		"PUT /api/v1/data-pools/{id}/rows/{row_id}":                                                     {"data", "version"},
		"PATCH /api/v1/tests/{id}/groups/{group}/runtime-settings":                                      {"expected_revision", "fields"},
		"PUT /api/v1/tests/{id}/groups/{group}/parameters":                                              {"expected_revision", "parameters"},
		"PUT /api/v1/correlation-rulesets/{id}":                                                         {"expected_updated_at", "name"},
		"POST /api/v1/scripts/authoring/sessions/{id}/correlations/preview":                             {"expected_updated_at"},
		"PUT /api/v1/scripts/authoring/sessions/{id}":                                                   {"draft", "source"},
		"POST /api/v1/scripts/export/k6":                                                                {"script"},
		"POST /api/v1/scripts/import/github":                                                            {"owner", "path", "repo"},
		"POST /api/v1/scripts/import/sample":                                                            {"content", "format"},
		"POST /api/v1/scripts/import/asyncapi/commit-batch":                                             {"operation_id", "scripts"},
		"POST /api/v1/scripts/import/commit-batch":                                                      {"operation_id", "scripts", "source_profile"},
		"POST /api/v1/scripts/import/enterprise-script-archive/save":                                    {"operation_id", "scripts"},
		"POST /api/v1/scripts/secret-bindings/apply":                                                    {"script"},
		"POST /api/v1/selectors/diagnose":                                                               {"steps"},
		"POST /api/v1/system/deployment-lease/acquire":                                                  {"owner_id"},
		"POST /api/v1/system/deployment-lease/release":                                                  {"owner_id"},
		"POST /api/v1/system/deployment-lease/renew":                                                    {"owner_id"},
		"POST /api/v1/correlation-rulesets/discover-from-script":                                        {"script_id"},
		"POST /api/v1/correlation-rulesets/{id}/apply-to-script":                                        {"script_id"},
		"POST /api/v1/scripts/browser-replay/sessions":                                                  {"script"},
	}
	fields, ok := declared[method+" "+path]
	if !ok {
		return nil, false
	}
	required := make([]string, 0, len(fields))
	for _, field := range fields {
		if _, ok := properties[field]; ok {
			required = append(required, field)
		}
	}
	sort.Strings(required)
	return required, true
}

func mergeOpenAPIRequiredFields(existing, explicit []string) []string {
	if len(existing) == 0 && len(explicit) == 0 {
		return nil
	}
	unique := make(map[string]struct{}, len(existing)+len(explicit))
	for _, field := range append(append([]string(nil), existing...), explicit...) {
		if strings.TrimSpace(field) != "" {
			unique[field] = struct{}{}
		}
	}
	merged := make([]string, 0, len(unique))
	for field := range unique {
		merged = append(merged, field)
	}
	sort.Strings(merged)
	return merged
}

func functionResultTypes(fn *ast.FuncDecl) []ast.Expr {
	if fn == nil || fn.Type == nil || fn.Type.Results == nil {
		return nil
	}
	results := make([]ast.Expr, 0, len(fn.Type.Results.List))
	for _, field := range fn.Type.Results.List {
		count := len(field.Names)
		if count == 0 {
			count = 1
		}
		for index := 0; index < count; index++ {
			results = append(results, field.Type)
		}
	}
	return results
}

func findLocalVariableTypes(body *ast.BlockStmt, functionResults map[string][]ast.Expr) map[string]ast.Expr {
	variables := map[string]ast.Expr{}
	ast.Inspect(body, func(node ast.Node) bool {
		switch statement := node.(type) {
		case *ast.DeclStmt:
			declaration, ok := statement.Decl.(*ast.GenDecl)
			if !ok || declaration.Tok != token.VAR {
				return true
			}
			for _, spec := range declaration.Specs {
				valueSpec, ok := spec.(*ast.ValueSpec)
				if !ok || valueSpec.Type == nil {
					continue
				}
				for _, name := range valueSpec.Names {
					variables[name.Name] = valueSpec.Type
				}
			}
		case *ast.AssignStmt:
			if len(statement.Rhs) == 1 {
				if call, ok := statement.Rhs[0].(*ast.CallExpr); ok {
					results := functionResults[calledFunctionName(call.Fun)]
					for index, lhs := range statement.Lhs {
						ident, identOK := lhs.(*ast.Ident)
						if identOK && ident.Name != "_" && index < len(results) {
							variables[ident.Name] = results[index]
						}
					}
				}
			}
			for index, rhs := range statement.Rhs {
				if index >= len(statement.Lhs) {
					continue
				}
				literal, ok := rhs.(*ast.CompositeLit)
				ident, identOK := statement.Lhs[index].(*ast.Ident)
				if ok && identOK && literal.Type != nil {
					variables[ident.Name] = literal.Type
				}
			}
		}
		return true
	})
	return variables
}

func setInferredResponseSchema(call *ast.CallExpr, status int, variableTypes map[string]ast.Expr, functionResults map[string][]ast.Expr, typeDefinitions map[string]ast.Expr, analysis *handlerContractAnalysis) {
	if analysis == nil || status < 200 || status >= 400 || len(call.Args) < 3 {
		return
	}
	expr := inferredExpressionType(call.Args[len(call.Args)-1], variableTypes, functionResults)
	if expr == nil {
		return
	}
	schema := openAPISchemaForASTType(expr, typeDefinitions, map[string]bool{}, 0)
	analysis.responseSchemas[status] = schema
}

func inferredExpressionType(expr ast.Expr, variableTypes map[string]ast.Expr, functionResults map[string][]ast.Expr) ast.Expr {
	switch value := expr.(type) {
	case *ast.Ident:
		return variableTypes[value.Name]
	case *ast.UnaryExpr:
		return inferredExpressionType(value.X, variableTypes, functionResults)
	case *ast.CompositeLit:
		return value.Type
	case *ast.CallExpr:
		results := functionResults[calledFunctionName(value.Fun)]
		if len(results) > 0 {
			return results[0]
		}
	}
	return nil
}

func setInferredRequestSchema(call *ast.CallExpr, variableTypes map[string]ast.Expr, typeDefinitions map[string]ast.Expr, analysis *handlerContractAnalysis) {
	if analysis.requestSchema != nil || len(call.Args) == 0 {
		return
	}
	target := call.Args[len(call.Args)-1]
	if unary, ok := target.(*ast.UnaryExpr); ok && unary.Op == token.AND {
		target = unary.X
	}
	ident, ok := target.(*ast.Ident)
	if !ok {
		return
	}
	expr := variableTypes[ident.Name]
	if expr == nil {
		return
	}
	schema := openAPISchemaForASTType(expr, typeDefinitions, map[string]bool{}, 0)
	analysis.requestSchema = &schema
}

func openAPISchemaForASTType(expr ast.Expr, typeDefinitions map[string]ast.Expr, visiting map[string]bool, depth int) oasSchema {
	return openAPISchemaForASTTypeInPackage(expr, typeDefinitions, visiting, depth, "")
}

func openAPISchemaForASTTypeInPackage(expr ast.Expr, typeDefinitions map[string]ast.Expr, visiting map[string]bool, depth int, currentPackage string) oasSchema {
	if expr == nil {
		additional := true
		return oasSchema{Type: "object", AdditionalProperties: &additional}
	}
	switch value := expr.(type) {
	case *ast.Ident:
		switch value.Name {
		case "string":
			return oasSchema{Type: "string"}
		case "bool":
			return oasSchema{Type: "boolean"}
		case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64":
			return oasSchema{Type: "integer", Format: "int64"}
		case "float32", "float64":
			return oasSchema{Type: "number", Format: "double"}
		case "any", "interface{}":
			return oasSchema{}
		}
		if depth > 8 {
			additional := true
			return oasSchema{Type: "object", Description: "Go type: " + value.Name, AdditionalProperties: &additional}
		}
		definitionKey := value.Name
		if currentPackage != "" {
			qualified := currentPackage + "." + value.Name
			if typeDefinitions[qualified] != nil {
				definitionKey = qualified
			}
		}
		definition := typeDefinitions[definitionKey]
		if definition == nil || visiting[definitionKey] {
			additional := true
			description := value.Name
			if currentPackage != "" {
				description = currentPackage + "." + value.Name
			}
			return oasSchema{Type: "object", Description: "Go type: " + description, AdditionalProperties: &additional}
		}
		visiting[definitionKey] = true
		schema := openAPISchemaForASTTypeInPackage(definition, typeDefinitions, visiting, depth+1, currentPackage)
		delete(visiting, definitionKey)
		return schema
	case *ast.StarExpr:
		// Pointer indirection does not make a JSON document structurally deeper.
		// Keeping the same depth also lets scalar pointers retain their real type
		// when nested request models approach the recursion guard. A nil pointer
		// encodes as JSON null whenever its field is not omitted, so retain that
		// wire shape instead of advertising the pointed-to value as the only
		// possible representation.
		schema := openAPISchemaForASTTypeInPackage(value.X, typeDefinitions, visiting, depth, currentPackage)
		schema.Nullable = true
		return schema
	case *ast.ArrayType:
		item := openAPISchemaForASTTypeInPackage(value.Elt, typeDefinitions, visiting, depth+1, currentPackage)
		return oasSchema{Type: "array", Items: &item}
	case *ast.MapType:
		additional := openAPISchemaForASTTypeInPackage(value.Value, typeDefinitions, visiting, depth+1, currentPackage)
		return oasSchema{Type: "object", AdditionalProperties: &additional}
	case *ast.InterfaceType:
		return oasSchema{}
	case *ast.SelectorExpr:
		if pkg, ok := value.X.(*ast.Ident); ok {
			if pkg.Name == "time" && value.Sel.Name == "Time" {
				return oasSchema{Type: "string", Format: "date-time"}
			}
			if pkg.Name == "time" && value.Sel.Name == "Duration" {
				return oasSchema{Type: "integer", Format: "int64", Description: "Duration in nanoseconds."}
			}
			if pkg.Name == "json" && value.Sel.Name == "RawMessage" {
				return oasSchema{}
			}
			if pkg.Name == "storage" {
				switch value.Sel.Name {
				case "SharedDataPool":
					return sharedDataPoolOpenAPISchema()
				case "SharedDataPoolRow":
					return sharedDataPoolRowOpenAPISchema()
				}
			}
			// Cross-package named scalars cannot be resolved from this
			// package's AST alone; known string enums are mapped explicitly so
			// they are not advertised as opaque objects.
			if pkg.Name == "correlate" && value.Sel.Name == "DiffClass" {
				return oasSchema{Type: "string", Description: "Go type: correlate.DiffClass", Enum: []string{"dynamic", "static", "unknown"}}
			}
			if pkg.Name == "generatorrpc" && value.Sel.Name == "ProtocolRuntimeCapacity" {
				return protocolRuntimeCapacityOpenAPISchema()
			}
			definitionKey := pkg.Name + "." + value.Sel.Name
			if definition := typeDefinitions[definitionKey]; definition != nil && !visiting[definitionKey] {
				visiting[definitionKey] = true
				schema := openAPISchemaForASTTypeInPackage(definition, typeDefinitions, visiting, depth+1, pkg.Name)
				delete(visiting, definitionKey)
				return schema
			}
			additional := true
			return oasSchema{Type: "object", Description: "Go type: " + pkg.Name + "." + value.Sel.Name, AdditionalProperties: &additional}
		}
	case *ast.StructType:
		if depth > 8 {
			additional := true
			return oasSchema{Type: "object", AdditionalProperties: &additional}
		}
		properties := map[string]oasSchema{}
		var required []string
		for _, field := range value.Fields.List {
			name, include, omitEmpty := jsonFieldName(field)
			if len(field.Names) == 0 && (!include || name == "") && anonymousJSONFieldPromotesMembers(field) {
				embedded := openAPISchemaForASTTypeInPackage(field.Type, typeDefinitions, visiting, depth+1, currentPackage)
				for embeddedName, embeddedProperty := range embedded.Properties {
					// A directly declared field wins over a promoted field. This
					// matches the only conflict rule that matters for request
					// schema inference while keeping anonymous compatibility
					// request structs visible to the generated contract.
					if _, exists := properties[embeddedName]; !exists {
						properties[embeddedName] = embeddedProperty
					}
				}
				required = mergeOpenAPIRequiredFields(required, embedded.Required)
				continue
			}
			if !include || name == "" {
				continue
			}
			properties[name] = openAPISchemaForASTTypeInPackage(field.Type, typeDefinitions, visiting, depth+1, currentPackage)
			if !omitEmpty {
				required = append(required, name)
			}
		}
		sort.Strings(required)
		return oasSchema{Type: "object", Properties: properties, Required: required}
	}
	additional := true
	return oasSchema{Type: "object", AdditionalProperties: &additional}
}

func sharedDataPoolOpenAPISchema() oasSchema {
	stringValue := oasSchema{Type: "string"}
	return oasSchema{
		Type: "object",
		Properties: map[string]oasSchema{
			"id":          {Type: "string"},
			"name":        {Type: "string"},
			"description": {Type: "string"},
			"schema":      {Type: "object", AdditionalProperties: &stringValue},
			"project_id":  {Type: "string"},
			"created_at":  {Type: "string", Format: "date-time"},
			"updated_at":  {Type: "string", Format: "date-time"},
			"archived_at": {Type: "string", Format: "date-time", Nullable: true},
		},
		Required: []string{"created_at", "id", "name", "updated_at"},
	}
}

func sharedDataPoolRowOpenAPISchema() oasSchema {
	stringValue := oasSchema{Type: "string"}
	return oasSchema{
		Type: "object",
		Properties: map[string]oasSchema{
			"id":         {Type: "string"},
			"pool_id":    {Type: "string"},
			"data":       {Type: "object", AdditionalProperties: &stringValue},
			"status":     {Type: "string"},
			"version":    {Type: "integer", Format: "int64"},
			"created_at": {Type: "string", Format: "date-time"},
			"updated_at": {Type: "string", Format: "date-time"},
		},
		Required: []string{"created_at", "data", "id", "pool_id", "updated_at", "version"},
	}
}

func anonymousJSONFieldPromotesMembers(field *ast.Field) bool {
	if field == nil || len(field.Names) != 0 {
		return false
	}
	if field.Tag == nil {
		return true
	}
	tagValue, err := strconv.Unquote(field.Tag.Value)
	if err != nil {
		return true
	}
	for _, tag := range strings.Fields(tagValue) {
		if !strings.HasPrefix(tag, "json:") {
			continue
		}
		jsonTag, err := strconv.Unquote(strings.TrimPrefix(tag, "json:"))
		if err != nil {
			return true
		}
		name := strings.SplitN(jsonTag, ",", 2)[0]
		return name == ""
	}
	return true
}

func protocolRuntimeCapacityOpenAPISchema() oasSchema {
	stringItem := oasSchema{Type: "string"}
	return oasSchema{
		Type:        "object",
		Description: "Heartbeat snapshot of a protocol-specific external runtime and its independently governed VUser capacity.",
		Properties: map[string]oasSchema{
			"protocol":         {Type: "string"},
			"available":        {Type: "boolean"},
			"max_vusers":       {Type: "integer", Format: "int64"},
			"available_vusers": {Type: "integer", Format: "int64"},
			"features":         {Type: "array", Items: &stringItem},
			"occupancy_mode": {
				Type: "string",
				Enum: []string{"idle", "persistent", "legacy"},
			},
		},
		Required: []string{"protocol", "available", "max_vusers", "available_vusers"},
	}
}

func jsonFieldName(field *ast.Field) (string, bool, bool) {
	// encoding/json never serializes unexported fields, even when they carry an
	// explicit tag. Exposing them here advertised process-only implementation
	// details such as creationActor and runtimeSecretScope as public API fields.
	if len(field.Names) > 0 && !ast.IsExported(field.Names[0].Name) {
		return "", false, false
	}
	if field.Tag != nil {
		tagValue, err := strconv.Unquote(field.Tag.Value)
		if err == nil {
			for _, tag := range strings.Fields(tagValue) {
				if !strings.HasPrefix(tag, "json:") {
					continue
				}
				jsonTag, err := strconv.Unquote(strings.TrimPrefix(tag, "json:"))
				if err != nil {
					break
				}
				name := strings.SplitN(jsonTag, ",", 2)[0]
				if name == "-" {
					return "", false, false
				}
				if name != "" {
					omitEmpty := false
					for _, option := range strings.Split(jsonTag, ",")[1:] {
						if option == "omitempty" {
							omitEmpty = true
							break
						}
					}
					return name, true, omitEmpty
				}
			}
		}
	}
	if len(field.Names) == 0 {
		return "", false, false
	}
	return field.Names[0].Name, true, false
}

func inferredResponses(route discoveredRoute, analysis *handlerContractAnalysis) map[string]oasResponse {
	if route.Path == "/api/v1/ws" || strings.HasSuffix(route.Path, "/stream") {
		responses := map[string]oasResponse{
			"101": {Description: "WebSocket protocol switch."},
			"4XX": {Description: "Authentication, authorization, origin, resource, or upgrade error."},
		}
		if route.Authenticated {
			responses["401"] = jsonErrorResponse("Authentication required or invalid.")
			responses["403"] = jsonErrorResponse("Authenticated principal lacks the required role or service-account scope.")
		}
		return responses
	}
	if len(analysis.statuses) == 0 && !analysis.jsonResponse && len(analysis.responseMedia) == 0 {
		return nil
	}
	responses := map[string]oasResponse{}
	statuses := make([]int, 0, len(analysis.statuses))
	for status := range analysis.statuses {
		statuses = append(statuses, status)
	}
	sort.Ints(statuses)
	for _, status := range statuses {
		description := http.StatusText(status)
		if description == "" {
			description = "Handler-defined response"
		}
		response := oasResponse{Description: description}
		if status >= 400 && analysis.jsonResponse {
			response.Content = map[string]oasMediaType{
				"application/json": {Schema: oasSchema{Ref: "#/components/schemas/ErrorResponse"}},
			}
		} else if status < 400 && status != http.StatusNoContent && status != http.StatusNotModified && route.Method != http.MethodHead {
			response.Content = inferredSuccessResponseContent(route, analysis, status)
		}
		responses[strconv.Itoa(status)] = response
	}
	if !hasSuccessStatus(statuses) {
		responses["2XX"] = oasResponse{Description: "Successful response; exact status is selected by the handler.", Content: inferredSuccessResponseContent(route, analysis, 0)}
	}
	if route.Authenticated {
		responses["401"] = jsonErrorResponse("Authentication required or invalid.")
		responses["403"] = jsonErrorResponse("Authenticated principal lacks the required role or service-account scope.")
	}
	responses["default"] = oasResponse{Description: "Additional handler-defined response."}
	return responses
}

func inferredSuccessResponseContent(route discoveredRoute, analysis *handlerContractAnalysis, status int) map[string]oasMediaType {
	content := map[string]oasMediaType{}
	if analysis.jsonResponse {
		schema, ok := analysis.responseSchemas[status]
		if !ok && status == 0 && len(analysis.responseSchemas) == 1 {
			for _, candidate := range analysis.responseSchemas {
				schema = candidate
				ok = true
			}
		}
		if !ok {
			schema = oasSchema{Description: "Operation-specific JSON response."}
		}
		content["application/json"] = oasMediaType{Schema: schema}
	}
	for mediaType := range analysis.responseMedia {
		if mediaType == "" {
			continue
		}
		schema := oasSchema{}
		if strings.HasPrefix(mediaType, "text/") || strings.Contains(mediaType, "yaml") || strings.Contains(mediaType, "json") || strings.Contains(mediaType, "csv") {
			schema.Type = "string"
		} else {
			schema.Type = "string"
			schema.Format = "binary"
		}
		content[mediaType] = oasMediaType{Schema: schema}
	}
	if len(content) == 0 {
		if route.Path == "/metrics" {
			content["text/plain"] = oasMediaType{Schema: oasSchema{Type: "string"}}
		} else if strings.HasSuffix(route.Path, "/swagger.yaml") {
			content["application/yaml"] = oasMediaType{Schema: oasSchema{Type: "string"}}
		} else if strings.HasSuffix(route.Path, "/endpoint-catalog") {
			content["application/json"] = oasMediaType{Schema: oasSchema{Type: "object"}}
		}
	}
	return content
}

func jsonErrorResponse(description string) oasResponse {
	return oasResponse{
		Description: description,
		Content: map[string]oasMediaType{
			"application/json": {Schema: oasSchema{Ref: "#/components/schemas/ErrorResponse"}},
		},
	}
}

// findImportUploadVariables reports the local variables bound to the result of
// decodeImportUpload. Import readers resolve their options through that value
// rather than touching r.URL.Query()/r.FormValue directly, so without this the
// generator would document every import route as taking no options at all.
func findImportUploadVariables(body *ast.BlockStmt) map[string]bool {
	variables := map[string]bool{}
	ast.Inspect(body, func(node ast.Node) bool {
		assign, ok := node.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for _, rhs := range assign.Rhs {
			call, ok := rhs.(*ast.CallExpr)
			if !ok || calledFunctionName(call.Fun) != "decodeImportUpload" {
				continue
			}
			if len(assign.Lhs) > 0 {
				if ident, ok := assign.Lhs[0].(*ast.Ident); ok && ident.Name != "_" {
					variables[ident.Name] = true
				}
			}
		}
		return true
	})
	return variables
}

func findQueryVariables(body *ast.BlockStmt) map[string]bool {
	variables := map[string]bool{"query": true, "queries": true, "values": true, "params": true, "q": true}
	ast.Inspect(body, func(node ast.Node) bool {
		switch statement := node.(type) {
		case *ast.AssignStmt:
			for index, rhs := range statement.Rhs {
				if !expressionContainsURLQuery(rhs) || index >= len(statement.Lhs) {
					continue
				}
				if ident, ok := statement.Lhs[index].(*ast.Ident); ok {
					variables[ident.Name] = true
				}
			}
		case *ast.ValueSpec:
			for index, value := range statement.Values {
				if !expressionContainsURLQuery(value) || index >= len(statement.Names) {
					continue
				}
				variables[statement.Names[index].Name] = true
			}
		}
		return true
	})
	return variables
}

func receiverIsQuery(expr ast.Expr, variables map[string]bool) bool {
	if expressionContainsURLQuery(expr) {
		return true
	}
	ident, ok := expr.(*ast.Ident)
	return ok && variables[ident.Name]
}

func receiverIsRequestHeader(expr ast.Expr) bool {
	header, ok := expr.(*ast.SelectorExpr)
	if !ok || header.Sel.Name != "Header" {
		return false
	}
	receiver, ok := header.X.(*ast.Ident)
	return ok && receiver.Name == "r"
}

func documentableRequestHeader(value string) bool {
	// OpenAPI ignores Accept, Content-Type, and Authorization header parameters.
	// Authorization and the session cookie are represented by security schemes;
	// Content-Type is represented by requestBody content.
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "accept", "authorization", "content-type", "cookie":
		return false
	default:
		return strings.TrimSpace(value) != ""
	}
}

func expressionContainsURLQuery(node ast.Node) bool {
	found := false
	ast.Inspect(node, func(next ast.Node) bool {
		if found {
			return false
		}
		selector, ok := next.(*ast.SelectorExpr)
		if ok && selector.Sel.Name == "Query" {
			found = true
			return false
		}
		return true
	})
	return found
}

func expressionContainsRequestBody(node ast.Node) bool {
	found := false
	ast.Inspect(node, func(next ast.Node) bool {
		selector, ok := next.(*ast.SelectorExpr)
		if ok && selector.Sel.Name == "Body" {
			found = true
			return false
		}
		return !found
	})
	return found
}

func callPassesRequest(call *ast.CallExpr) bool {
	for _, arg := range call.Args {
		if ident, ok := arg.(*ast.Ident); ok && ident.Name == "r" {
			return true
		}
	}
	return false
}

func calledFunctionName(expr ast.Expr) string {
	switch fn := expr.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		return fn.Sel.Name
	default:
		return ""
	}
}

func plausibleQueryName(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, "/ ?=:,|\n\t") {
		return false
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' && r != '-' {
			return false
		}
	}
	return true
}

func normalizeMediaType(value string) string {
	return strings.TrimSpace(strings.SplitN(value, ";", 2)[0])
}

func hasSuccessStatus(statuses []int) bool {
	for _, status := range statuses {
		if status >= 200 && status < 400 {
			return true
		}
	}
	return false
}

func openAPIHTTPStatus(expr ast.Expr) (int, bool) {
	if literal, ok := expr.(*ast.BasicLit); ok && literal.Kind == token.INT {
		value, err := strconv.Atoi(literal.Value)
		return value, err == nil
	}
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return 0, false
	}
	statuses := map[string]int{
		"StatusSwitchingProtocols": http.StatusSwitchingProtocols,
		"StatusOK":                 http.StatusOK, "StatusCreated": http.StatusCreated, "StatusAccepted": http.StatusAccepted,
		"StatusNoContent": http.StatusNoContent, "StatusPartialContent": http.StatusPartialContent,
		"StatusFound": http.StatusFound, "StatusSeeOther": http.StatusSeeOther,
		"StatusTemporaryRedirect": http.StatusTemporaryRedirect, "StatusPermanentRedirect": http.StatusPermanentRedirect,
		"StatusBadRequest": http.StatusBadRequest, "StatusUnauthorized": http.StatusUnauthorized,
		"StatusForbidden": http.StatusForbidden, "StatusNotFound": http.StatusNotFound,
		"StatusMethodNotAllowed": http.StatusMethodNotAllowed, "StatusConflict": http.StatusConflict,
		"StatusGone": http.StatusGone, "StatusRequestEntityTooLarge": http.StatusRequestEntityTooLarge,
		"StatusPreconditionRequired": http.StatusPreconditionRequired,
		"StatusUnsupportedMediaType": http.StatusUnsupportedMediaType, "StatusUnprocessableEntity": http.StatusUnprocessableEntity,
		"StatusTooManyRequests": http.StatusTooManyRequests, "StatusInternalServerError": http.StatusInternalServerError,
		"StatusNotImplemented": http.StatusNotImplemented, "StatusBadGateway": http.StatusBadGateway,
		"StatusServiceUnavailable": http.StatusServiceUnavailable, "StatusGatewayTimeout": http.StatusGatewayTimeout,
	}
	status, ok := statuses[selector.Sel.Name]
	return status, ok
}

// receiverIsImportUpload reports whether expr names a local bound to the result
// of decodeImportUpload, so its option lookups can be attributed to the route.
func receiverIsImportUpload(expr ast.Expr, variables map[string]bool) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && variables[ident.Name]
}
