package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	recbrowser "github.com/jasonczech/apexload/pkg/recorder/browser"
	"github.com/jasonczech/apexload/pkg/storage"
	"github.com/santhosh-tekuri/jsonschema/v6"
	yaml "gopkg.in/yaml.v3"
)

func TestCanonicalImportOpenAPIContract(t *testing.T) {
	data, err := GenerateOpenAPIYAML("server.go")
	if err != nil {
		t.Fatal(err)
	}
	var spec oasDoc
	if err := yaml.Unmarshal(data, &spec); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"/api/v1/scripts/import/sample", "/api/v1/scripts/import/har", "/api/v1/scripts/import/salesforce",
		"/api/v1/scripts/import/enterprise-script-archive", "/api/v1/scripts/import/enterprise-script-archive/validate",
		"/api/v1/scripts/import/devweb", "/api/v1/scripts/import/k6", "/api/v1/scripts/import/github",
		"/api/v1/scripts/import/openapi", "/api/v1/scripts/import/raml", "/api/v1/scripts/import/wsdl",
		"/api/v1/scripts/import/postman", "/api/v1/scripts/import/postman/validate",
		"/api/v1/scripts/import/asyncapi", "/api/v1/scripts/import/eventhubs-capture", "/api/v1/scripts/import/broker-config",
		"/api/v1/scripts/import/jmeter", "/api/v1/scripts/import/jmeter/validate", "/api/v1/scripts/import/cloudtest",
		"/api/v1/scripts/import/recorder", "/api/v1/scripts/import/recorder/validate", "/api/v1/scripts/import/recorder/replay",
		"/api/v1/scripts/import/browser-recorder/attest", "/api/v1/recorder/{id}/convert/preview",
		"/api/v1/browser-recorder/sessions/{id}/convert/preview", "/api/v1/recorder/{id}/convert",
		"/api/v1/browser-recorder/sessions/{id}/convert",
	} {
		operation := spec.Paths[path]["post"]
		parameters := 0
		for _, parameter := range operation.Parameters {
			if parameter.Name != "response_format" || parameter.In != "query" {
				continue
			}
			parameters++
			if parameter.Required || parameter.Schema.Type != "string" || len(parameter.Schema.Enum) != 0 || !strings.Contains(parameter.Description, "unrecognized") || !strings.Contains(parameter.Description, "canonical-v1") {
				t.Errorf("%s negotiation must retain arbitrary-value legacy fallback: %+v", path, parameter)
			}
		}
		if parameters != 1 {
			t.Errorf("%s negotiation parameters=%d, want one", path, parameters)
		}
		expected := "#/components/schemas/ScriptImportReviewResponse"
		if enterpriseArchiveReviewRoute(http.MethodPost, path) {
			expected = "#/components/schemas/EnterpriseArchiveReviewResponse"
		} else if path == "/api/v1/recorder/{id}/convert" || path == "/api/v1/browser-recorder/sessions/{id}/convert" {
			expected = "#/components/schemas/SavedRecordingConversionResponse"
		}
		successes := 0
		for status, response := range operation.Responses {
			if len(status) == 3 && status[0] == '2' {
				successes++
				if got := response.Content["application/json"].Schema.Ref; got != expected {
					t.Errorf("%s %s schema=%q, want %q", path, status, got, expected)
				}
			}
		}
		if successes == 0 {
			t.Errorf("%s has no documented successful review", path)
		}
	}
	for _, name := range []string{"ScriptImportReviewResponse", "EnterpriseArchiveScriptReviewResponse"} {
		schema := spec.Components.Schemas[name]
		if openAPIStringSliceContains(schema.Required, "script") || len(schema.OneOf) != 2 || !reflect.DeepEqual(schema.OneOf[0].Required, []string{"script"}) || !reflect.DeepEqual(schema.OneOf[1].Required, []string{"response_format"}) {
			t.Fatalf("%s does not distinguish the legacy alias from canonical negotiation: %#v", name, schema)
		}
		if !reflect.DeepEqual(schema.Properties["response_format"].Enum, []string{"canonical-v1"}) || schema.Properties["primary_script_index"].Type != "integer" || schema.Properties["salesforce_preview_script_index"].Type != "integer" || schema.Properties["unsupported_from_diagnostics"].Type != "boolean" {
			t.Fatalf("%s lost canonical reference metadata", name)
		}
		aliases, ok := schema.Properties["result_aliases"].AdditionalProperties.(map[string]any)
		if !ok || aliases["type"] != "string" {
			t.Fatalf("%s result aliases are not typed field references: %#v", name, schema.Properties["result_aliases"])
		}
	}
	assertImportOpenAPISchemasAcceptWire(t, spec.Components.Schemas)
	assertSavedRecordingOpenAPISchemaAcceptsWire(t, spec.Components.Schemas)
	assertRecorderReplayBadRequestOpenAPISchemaAcceptsWire(t, spec)
}

func assertRecorderReplayBadRequestOpenAPISchemaAcceptsWire(t *testing.T, spec oasDoc) {
	t.Helper()
	operation := spec.Paths["/api/v1/scripts/import/recorder/replay"]["post"]
	schema := operation.Responses["400"].Content["application/json"].Schema
	if len(schema.OneOf) != 2 || schema.OneOf[0].Ref != "#/components/schemas/ErrorResponse" || schema.OneOf[1].Ref != "#/components/schemas/RecorderImportReplayInvalidResponse" {
		t.Fatalf("recorder replay lost its distinct decoding/validation refusal bodies: %#v", schema)
	}
	spec.Components.Schemas["RecorderReplayBadRequest"] = schema
	validator := compileImportOpenAPISchema(t, spec.Components.Schemas, "RecorderReplayBadRequest")
	server, store := newAuditTestServer(t)
	defer store.Close()
	body, err := json.Marshal(map[string]any{"content": `{"title":"Manual review","steps":[{"type":"customStep"}]}`})
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"", "canonical-v1", "future-version"} {
		for _, malformed := range []bool{false, true} {
			payload := body
			if malformed {
				payload = []byte(`{`)
			}
			request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/recorder/replay?project_id="+storage.DefaultProjectID+"&response_format="+format, bytes.NewReader(payload))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			server.mux.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("replay refusal format=%q malformed=%v status=%d body=%s", format, malformed, response.Code, response.Body.String())
			}
			value, err := jsonschema.UnmarshalJSON(bytes.NewReader(response.Body.Bytes()))
			if err != nil {
				t.Fatal(err)
			}
			if err := validator.Validate(value); err != nil {
				t.Fatalf("replay refusal format=%q malformed=%v violates generated schema: %v", format, malformed, err)
			}
			wire := value.(map[string]any)
			if malformed {
				if wire["error"] == nil || wire["valid"] != nil {
					t.Fatal("decoding error did not retain the ordinary error envelope")
				}
			} else {
				issues, ok := wire["issues"].([]any)
				if wire["valid"] != false || !ok || len(issues) == 0 || wire["error"] != nil {
					t.Fatal("replay validation did not retain its review and issues")
				}
				if (format == "canonical-v1") != (wire["response_format"] == "canonical-v1") {
					t.Fatal("replay validation did not retain response negotiation")
				}
			}
		}
	}
}

func assertSavedRecordingOpenAPISchemaAcceptsWire(t *testing.T, schemas map[string]oasSchema) {
	t.Helper()
	validator := compileImportOpenAPISchema(t, schemas, "SavedRecordingConversionResponse")
	server, store := newAuditTestServer(t)
	defer store.Close()
	script := &dsl.Script{ID: "saved-recording", Name: "Saved recording", ProjectID: storage.DefaultProjectID, Protocol: "browser"}
	stored := &StoredScript{ID: script.ID, Name: script.Name, ProjectID: script.ProjectID, Script: script}
	for _, format := range []string{"", "canonical-v1", "future-version"} {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/browser-recorder/sessions/session/convert?project_id="+storage.DefaultProjectID+"&response_format="+format, nil)
		response := httptest.NewRecorder()
		server.writeSavedBrowserRecordingConversion(response, request, "session", &recbrowser.ConvertResult{Script: script, Scripts: []*dsl.Script{script}}, nil, nil, []*StoredScript{stored}, nil, false)
		if response.Code != http.StatusCreated {
			t.Fatalf("saved recording status=%d body=%s", response.Code, response.Body.String())
		}
		value, err := jsonschema.UnmarshalJSON(bytes.NewReader(response.Body.Bytes()))
		if err != nil {
			t.Fatal(err)
		}
		if err := validator.Validate(value); err != nil {
			t.Fatalf("saved recording format=%q rejected by generated contract: %v", format, err)
		}
		wire := value.(map[string]any)
		if wire["script"] == nil || (format == "canonical-v1" && wire["response_format"] != format) {
			t.Fatal("saved conversion must retain its stored alias with canonical metadata")
		}
	}
}

func assertImportOpenAPISchemasAcceptWire(t *testing.T, schemas map[string]oasSchema) {
	t.Helper()
	validators := map[string]*jsonschema.Schema{}
	for _, name := range []string{"ScriptImportReviewResponse", "EnterpriseArchiveReviewResponse"} {
		validators[name] = compileImportOpenAPISchema(t, schemas, name)
	}
	for _, canonical := range []bool{false, true} {
		for _, primary := range []*dsl.Script{nil, {Name: "Imported", Protocol: "http", Actions: []dsl.Action{{Type: "request", Method: "GET", Target: "https://example.test"}}}} {
			wire := newScriptImportResponse(scriptImportResponseOptions{CanonicalResponse: canonical, Script: primary})
			value := importOpenAPIJSONValue(t, wire)
			for name, validator := range validators {
				if err := validator.Validate(value); err != nil {
					t.Fatalf("%s rejected actual canonical=%v primary=%v response: %v", name, canonical, primary != nil, err)
				}
			}
			collection := map[string]any{"archive_collection": true, "archives": []any{
				map[string]any{"source_name": "project.zip", "source_size": 100, "response": wire},
				map[string]any{"source_name": "bad.zip", "source_size": 3, "error": "invalid archive"},
			}, "count": 2, "successful_count": 1, "failed_count": 1, "warnings": []string{}, "unassigned_files": nil}
			if err := validators["EnterpriseArchiveReviewResponse"].Validate(importOpenAPIJSONValue(t, collection)); err != nil {
				t.Fatalf("collection rejected an independent canonical=%v member: %v", canonical, err)
			}
		}
	}
	for _, mutate := range []func(map[string]any){
		func(wire map[string]any) { delete(wire, "response_format") },
		func(wire map[string]any) { wire["script"] = nil },
		func(wire map[string]any) { delete(wire, "scripts") },
		func(wire map[string]any) { wire["response_format"] = "unknown-canonical-version" },
	} {
		wire := newScriptImportResponse(scriptImportResponseOptions{CanonicalResponse: true})
		mutate(wire)
		for name, validator := range validators {
			if err := validator.Validate(importOpenAPIJSONValue(t, wire)); err == nil {
				t.Fatalf("%s accepted a malformed canonical envelope: %#v", name, wire)
			}
		}
	}
}

func compileImportOpenAPISchema(t *testing.T, schemas map[string]oasSchema, name string) *jsonschema.Schema {
	t.Helper()
	raw, err := yaml.Marshal(map[string]any{"$ref": "#/components/schemas/" + name, "components": map[string]any{"schemas": schemas}})
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	// OpenAPI 3.0 nullable is equivalent to JSON Schema's null type union.
	// Translate only that dialect keyword; validation uses the generated shape.
	var nullableTypes func(any)
	nullableTypes = func(value any) {
		switch value := value.(type) {
		case map[string]any:
			if value["nullable"] == true {
				if kind, ok := value["type"].(string); ok {
					value["type"] = []any{kind, "null"}
				}
				delete(value, "nullable")
			}
			for _, child := range value {
				nullableTypes(child)
			}
		case []any:
			for _, child := range value {
				nullableTypes(child)
			}
		}
	}
	nullableTypes(document)
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("import-response.json", document); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("import-response.json")
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func importOpenAPIJSONValue(t *testing.T, value any) any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

func TestCanonicalImportOpenAPIContractIsScopedAndIdempotent(t *testing.T) {
	for _, route := range []discoveredRoute{
		{Method: http.MethodGet, Path: "/api/v1/scripts/import/har"},
		{Method: http.MethodPost, Path: "/api/v1/scripts/import/enterprise-script-archive/save"},
		{Method: http.MethodPost, Path: "/api/v1/scripts/import/bruno/inspect"},
		{Method: http.MethodPost, Path: "/api/v1/scripts/import/repairs/apply"},
		{Method: http.MethodPost, Path: "/api/v1/scripts/import/settings"},
	} {
		contract := inferredRouteContract{Responses: map[string]oasResponse{"201": {Description: "original"}}}
		before := inferredRouteContract{Responses: map[string]oasResponse{"201": {Description: "original"}}}
		applyCanonicalImportContract(route, &contract)
		if !reflect.DeepEqual(before, contract) {
			t.Fatalf("unrelated contract changed: %s %s", route.Method, route.Path)
		}
	}
	route := discoveredRoute{Method: http.MethodPost, Path: "/api/v1/scripts/import/har"}
	contract := inferredRouteContract{QueryParameters: []oasParameter{{Name: "response_format", In: "query", Required: true}}, Responses: map[string]oasResponse{"2XX": {Description: "success"}, "400": {Description: "invalid input"}}}
	applyCanonicalImportContract(route, &contract)
	first, err := yaml.Marshal(contract)
	if err != nil {
		t.Fatal(err)
	}
	applyCanonicalImportContract(route, &contract)
	second, err := yaml.Marshal(contract)
	if err != nil || !bytes.Equal(first, second) || contract.Responses["400"].Description != "invalid input" {
		t.Fatalf("contract application changed responses or duplicated negotiation: %v", err)
	}
	applyCanonicalImportContract(route, nil)
	custom := inferredRouteContract{Responses: map[string]oasResponse{"200": {Content: map[string]oasMediaType{"application/json": {Schema: oasSchema{Type: "object", Properties: map[string]oasSchema{"script": {Type: "object", Nullable: true}, "converter_receipt": {Type: "string"}}, Required: []string{"script", "converter_receipt"}}}}}}}
	applyCanonicalImportContract(route, &custom)
	schema := custom.Responses["200"].Content["application/json"].Schema
	if schema.Properties["converter_receipt"].Type != "string" || !openAPIStringSliceContains(schema.Required, "converter_receipt") || openAPIStringSliceContains(schema.Required, "script") {
		t.Fatal("route-specific inferred fields or requirements were lost")
	}
	replay := inferredRouteContract{}
	replayRoute := discoveredRoute{Method: http.MethodPost, Path: "/api/v1/scripts/import/recorder/replay"}
	applyCanonicalImportContract(replayRoute, &replay)
	first, err = yaml.Marshal(replay)
	if err != nil {
		t.Fatal(err)
	}
	applyCanonicalImportContract(replayRoute, &replay)
	second, err = yaml.Marshal(replay)
	if err != nil || !bytes.Equal(first, second) {
		t.Fatalf("replay refusal alternatives changed on repeated application: %v", err)
	}
}
