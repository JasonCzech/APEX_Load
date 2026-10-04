package api

import (
	"go/ast"
	"sort"
	"strings"
	"testing"

	yaml "gopkg.in/yaml.v3"
)

func TestOpenAPICrossPackageModelSchemasAreResolved(t *testing.T) {
	typeDefinitions := map[string]ast.Expr{}
	if err := addOpenAPICrossPackageTypeDefinitions(".", typeDefinitions); err != nil {
		t.Fatalf("addOpenAPICrossPackageTypeDefinitions: %v", err)
	}

	tests := []struct {
		name        string
		packageName string
		typeName    string
		assert      func(*testing.T, oasSchema)
	}{
		{
			name:        "gRPC evidence preserves optional observations",
			packageName: "protocol",
			typeName:    "GRPCDiagnostics",
			assert: func(t *testing.T, schema oasSchema) {
				for _, field := range []string{"first_message_ms", "deadline_budget_ms", "status_code"} {
					property := schema.Properties[field]
					if (property.Type != "number" && property.Type != "integer") || !property.Nullable || containsOpenAPIField(schema.Required, field) {
						t.Fatalf("%s must be an optional nullable measurement: %#v", field, property)
					}
				}
				if schema.Properties["method"].Type != "string" || schema.Properties["messages_received"].Type != "integer" {
					t.Fatal("gRPC identity or counters are opaque")
				}
			},
		},
		{
			name:        "gRPC summaries expose mergeable distributions",
			packageName: "protocol",
			typeName:    "GRPCSummary",
			assert:      assertGRPCSummaryOpenAPISchema,
		},
		{
			name:        "storage user includes session epoch and nested bindings",
			packageName: "storage",
			typeName:    "User",
			assert: func(t *testing.T, schema oasSchema) {
				if got := schema.Properties["session_epoch"].Type; got != "integer" {
					t.Fatalf("session_epoch type = %q, want integer", got)
				}
				bindings := schema.Properties["bindings"]
				if bindings.Type != "array" || bindings.Items == nil || bindings.Items.Properties["role"].Type != "string" {
					t.Fatalf("bindings schema = %#v, want typed storage.UserRoleBinding items", bindings)
				}
			},
		},
		{
			name:        "execution plan request exposes revision and entry shape",
			packageName: "storage",
			typeName:    "ExecutionPlan",
			assert: func(t *testing.T, schema oasSchema) {
				if schema.Properties["revision"].Type != "integer" || !containsOpenAPIField(schema.Required, "revision") {
					t.Fatalf("execution plan revision = %#v required=%v, want required integer", schema.Properties["revision"], schema.Required)
				}
				entries := schema.Properties["entries"]
				if entries.Items == nil || entries.Items.Properties["scenario_id"].Type != "string" {
					t.Fatalf("execution plan entries schema = %#v, want typed entry items", entries)
				}
			},
		},
		{
			name:        "DSL script exposes core payload",
			packageName: "dsl",
			typeName:    "Script",
			assert: func(t *testing.T, schema oasSchema) {
				if schema.Properties["protocol"].Type != "string" {
					t.Fatalf("script protocol schema = %#v, want string", schema.Properties["protocol"])
				}
				actions := schema.Properties["actions"]
				if actions.Items == nil || actions.Items.Properties["type"].Type != "string" {
					t.Fatalf("script actions schema = %#v, want typed action items", actions)
				}
			},
		},
		{
			name:        "k6 export request exposes the script payload",
			packageName: "k6",
			typeName:    "ExportRequest",
			assert: func(t *testing.T, schema oasSchema) {
				script := schema.Properties["script"]
				if script.Properties["protocol"].Type != "string" {
					t.Fatalf("k6 export script schema = %#v, want typed DSL script", script)
				}
				if schema.Properties["options"].Properties["include_thresholds"].Type != "boolean" {
					t.Fatalf("k6 export options schema = %#v, want typed options", schema.Properties["options"])
				}
			},
		},
		{
			name:        "sample import request exposes format content and options",
			packageName: "sample",
			typeName:    "ImportRequest",
			assert: func(t *testing.T, schema oasSchema) {
				if schema.Properties["format"].Type != "string" || schema.Properties["content"].Type != "string" {
					t.Fatalf("sample import schema = %#v, want typed format and content", schema)
				}
				includeAssertions := schema.Properties["options"].Properties["include_assertions"]
				if includeAssertions.Type != "boolean" || !includeAssertions.Nullable {
					t.Fatalf("sample import include_assertions = %#v, want nullable boolean", includeAssertions)
				}
			},
		},
		{
			name:        "pointer-valued storage response fields admit JSON null",
			packageName: "storage",
			typeName:    "BrunoImportCommitResult",
			assert: func(t *testing.T, schema oasSchema) {
				for _, field := range []string{"script", "revision"} {
					property := schema.Properties[field]
					if !property.Nullable {
						t.Fatalf("%s schema = %#v, want nullable pointer representation", field, property)
					}
					if !containsOpenAPIField(schema.Required, field) {
						t.Fatalf("%s is not required even though encoding/json emits a null field", field)
					}
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			schema := openAPISchemaForASTType(
				&ast.SelectorExpr{X: ast.NewIdent(test.packageName), Sel: ast.NewIdent(test.typeName)},
				typeDefinitions,
				map[string]bool{},
				0,
			)
			if len(schema.Properties) == 0 {
				t.Fatalf("%s.%s resolved to a free-form object: %#v", test.packageName, test.typeName, schema)
			}
			test.assert(t, schema)
		})
	}
}

func TestGeneratedOpenAPIRoutesUseCrossPackageSchemas(t *testing.T) {
	data, err := GenerateOpenAPIYAML("server.go")
	if err != nil {
		t.Fatalf("GenerateOpenAPIYAML: %v", err)
	}
	var document oasDoc
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatalf("unmarshal generated OpenAPI: %v", err)
	}

	testResponse := document.Paths["/api/v1/tests/{id}"]["get"].Responses["200"].Content["application/json"].Schema
	transactions := testResponse.Properties["live_metrics"].Properties["transactions"]
	encoded, err := yaml.Marshal(transactions.AdditionalProperties)
	if err != nil {
		t.Fatal(err)
	}
	var transaction oasSchema
	if err := yaml.Unmarshal(encoded, &transaction); err != nil {
		t.Fatal(err)
	}
	assertGRPCSummaryOpenAPISchema(t, transaction.Properties["server_telemetry"].Properties["grpc"])

	userResponse := document.Paths["/api/v1/auth/users/{id}/revoke-sessions"]["post"].Responses["200"]
	userSchema := userResponse.Content["application/json"].Schema
	if userSchema.Properties["session_epoch"].Type != "integer" {
		t.Fatalf("revoke-sessions storage.User schema = %#v, want typed session_epoch", userSchema)
	}

	updatePlan := document.Paths["/api/v1/execution-plans/{id}"]["put"]
	if updatePlan.RequestBody == nil {
		t.Fatal("update execution plan is missing request body")
	}
	planSchema := updatePlan.RequestBody.Content["application/json"].Schema
	if planSchema.Properties["revision"].Type != "integer" || !containsOpenAPIField(planSchema.Required, "revision") {
		t.Fatalf("update execution plan request schema = %#v, want required integer revision", planSchema)
	}

	for _, route := range []struct {
		path           string
		requiredFields []string
	}{
		{path: "/api/v1/scripts/export/k6", requiredFields: []string{"script"}},
		{path: "/api/v1/scripts/import/sample", requiredFields: []string{"content", "format"}},
	} {
		operation := document.Paths[route.path]["post"]
		if operation.RequestBody == nil {
			t.Fatalf("POST %s is missing its request body", route.path)
		}
		schema := operation.RequestBody.Content["application/json"].Schema
		if len(schema.Properties) == 0 {
			t.Fatalf("POST %s request schema is opaque: %#v", route.path, schema)
		}
		for _, field := range route.requiredFields {
			if !containsOpenAPIField(schema.Required, field) {
				t.Errorf("POST %s required fields = %v, want %s", route.path, schema.Required, field)
			}
		}
	}
}

func assertGRPCSummaryOpenAPISchema(t *testing.T, schema oasSchema) {
	t.Helper()
	if schema.Properties["samples"].Type != "integer" {
		t.Fatalf("gRPC summary is opaque: %#v", schema)
	}
	for _, field := range []string{"call_ms", "first_message_ms", "deadline_budget_ms"} {
		distribution := schema.Properties[field]
		buckets := distribution.Properties["buckets"]
		if distribution.Properties["count"].Type != "integer" || distribution.Properties["sum"].Type != "number" ||
			buckets.Type != "array" || buckets.Items == nil || buckets.Items.Type != "integer" {
			t.Fatalf("%s has an opaque diagnostic distribution: %#v", field, distribution)
		}
	}
}

func TestGeneratedOpenAPIHasNoOpaqueJSONRequestBodies(t *testing.T) {
	data, err := GenerateOpenAPIYAML("server.go")
	if err != nil {
		t.Fatalf("GenerateOpenAPIYAML: %v", err)
	}
	var document oasDoc
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatalf("unmarshal generated OpenAPI: %v", err)
	}

	for path, operations := range document.Paths {
		for method, operation := range operations {
			if operation.RequestBody == nil {
				continue
			}
			media, ok := operation.RequestBody.Content["application/json"]
			if !ok {
				continue
			}
			additional, allowsArbitraryFields := media.Schema.AdditionalProperties.(bool)
			if media.Schema.Type == "object" && len(media.Schema.Properties) == 0 && allowsArbitraryFields && additional {
				t.Errorf("%s %s publishes an opaque JSON request body: %s", method, path, media.Schema.Description)
			}
		}
	}
}

func TestExplicitJSONRequestContractsRemainTypedAndPreserveBodyOptionality(t *testing.T) {
	data, err := GenerateOpenAPIYAML("server.go")
	if err != nil {
		t.Fatalf("GenerateOpenAPIYAML: %v", err)
	}
	var document oasDoc
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatalf("unmarshal generated OpenAPI: %v", err)
	}

	routes := make([]string, 0, len(explicitRouteContractTypes))
	for route, override := range explicitRouteContractTypes {
		if override.requestType != "" {
			routes = append(routes, route)
		}
	}
	sort.Strings(routes)
	for _, route := range routes {
		method, path, ok := strings.Cut(route, " ")
		if !ok {
			t.Fatalf("invalid explicit route key %q", route)
		}
		override := explicitRouteContractTypes[route]
		operation, ok := document.Paths[path][strings.ToLower(method)]
		if !ok {
			t.Errorf("explicit request route %s is absent from generated OpenAPI", route)
			continue
		}
		if operation.RequestBody == nil {
			t.Errorf("explicit request route %s has no request body", route)
			continue
		}
		schema := operation.RequestBody.Content["application/json"].Schema
		// A discriminated union carries its fields on the oneOf branches rather
		// than at the root, so it is typed even though the root has no
		// properties. Every branch must still be typed, or the union would be a
		// new way to smuggle an opaque body past this guard.
		typed := len(schema.Properties) > 0
		if len(schema.OneOf) > 0 {
			typed = true
			for index, variant := range schema.OneOf {
				if len(variant.Properties) == 0 {
					t.Errorf("explicit request route %s (%s) oneOf branch %d is opaque", route, override.requestType, index)
				}
			}
		}
		if !typed {
			t.Errorf("explicit request route %s (%s) is opaque: %#v", route, override.requestType, schema)
		}
		if got, want := operation.RequestBody.Required, !override.requestOptional; got != want {
			t.Errorf("explicit request route %s required=%t, want %t", route, got, want)
		}
	}
}

func TestGeneratedOpenAPIAppDynamicsRequestContracts(t *testing.T) {
	data, err := GenerateOpenAPIYAML("server.go")
	if err != nil {
		t.Fatal(err)
	}
	var document oasDoc
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	for _, route := range []struct {
		method, path string
		update       bool
	}{
		{"post", "/api/v1/projects/{id}/monitoring/appdynamics-connections", false},
		{"put", "/api/v1/projects/{id}/monitoring/appdynamics-connections/{connection_id}", true},
	} {
		t.Run(route.method+" connection", func(t *testing.T) {
			body := document.Paths[route.path][route.method].RequestBody
			if body == nil || !body.Required {
				t.Fatal("connection mutation must require a JSON body")
			}
			schema := body.Content["application/json"].Schema
			if schema.Properties["name"].Type != "string" || schema.Properties["revision"].Type != "integer" || !containsOpenAPIField(schema.Required, "name") || containsOpenAPIField(schema.Required, "revision") != route.update {
				t.Fatalf("connection identity/revision contract is inaccurate: %#v", schema)
			}
			config := schema.Properties["config"]
			if config.Properties["endpoint"].Type != "string" || config.Properties["auth_type"].Type != "string" || config.Properties["events_endpoint"].Type != "string" {
				t.Fatalf("Controller and independent Analytics settings are opaque: %#v", config)
			}
			cidrs := config.Properties["approved_cidrs"]
			if cidrs.Type != "array" || cidrs.Items == nil || cidrs.Items.Type != "string" {
				t.Fatalf("private network policy is opaque: %#v", cidrs)
			}
			for _, field := range []string{"client_id_secret", "client_secret_secret", "username_secret", "password_secret", "events_api_key_secret"} {
				property := schema.Properties[field]
				if property.Type != "string" || !property.Nullable || containsOpenAPIField(schema.Required, field) {
					t.Fatalf("optional credential reference %s lost its typed nullable contract: %#v", field, property)
				}
			}
			for _, field := range []string{"config", "enabled", "collection_mode", "collector_agent_id"} {
				if containsOpenAPIField(schema.Required, field) {
					t.Fatalf("disabled name-only drafts cannot omit %s", field)
				}
			}
		})
	}
	body := document.Paths["/api/v1/monitoring/collectors/{id}/inspections/{operation_id}/result"]["post"].RequestBody
	if body == nil || !body.Required {
		t.Fatal("collector inspection result must require a JSON body")
	}
	schema := body.Content["application/json"].Schema
	job := schema.Properties["job"]
	if !containsOpenAPIField(schema.Required, "job") || job.Properties["payload"].Type != "string" || job.Properties["signature"].Type != "string" {
		t.Fatalf("signed inspection authorization is opaque: %#v", job)
	}
	capabilities := schema.Properties["capabilities"]
	for _, field := range []string{"metrics", "events", "violations", "snapshots", "analytics"} {
		if capabilities.Properties[field].Type != "boolean" {
			t.Fatalf("inspection capability %s is opaque: %#v", field, capabilities)
		}
	}
	entities := schema.Properties["entities"]
	if entities.Type != "array" || entities.Items == nil || entities.Items.Properties["name"].Type != "string" || entities.Items.Properties["parent_id"].Type != "string" {
		t.Fatalf("discovery entities are opaque: %#v", entities)
	}
	for _, field := range []string{"entities", "message", "truncated"} {
		if containsOpenAPIField(schema.Required, field) {
			t.Fatalf("empty successful inspection cannot omit %s", field)
		}
	}
}

func containsOpenAPIField(fields []string, want string) bool {
	for _, field := range fields {
		if field == want {
			return true
		}
	}
	return false
}
