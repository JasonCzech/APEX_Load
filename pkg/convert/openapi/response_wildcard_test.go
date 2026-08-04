package openapi

import "testing"

func TestConvertOpenAPIUsesRangeAndWildcardSchemaFor2XX(t *testing.T) {
	const spec = `{
  "openapi":"3.0.3",
  "info":{"title":"wildcard","version":"1"},
  "paths":{"/orders":{"get":{"operationId":"listOrders","responses":{"2XX":{"description":"success","content":{"application/json":{"schema":{"type":"object","required":["items"],"properties":{"items":{"type":"array"}}}}}}}}}}
}`
	result, err := Convert(ImportRequest{Content: spec, Name: "wildcard.json"})
	if err != nil {
		t.Fatal(err)
	}
	action := result.Script.Actions[0]
	if len(action.Assertions) != 2 || action.Assertions[0].Type != "status_code_range" || action.Assertions[0].Min != 200 || action.Assertions[0].Max != 299 {
		t.Fatalf("assertions = %#v", action.Assertions)
	}
	if action.Assertions[1].Type != "body_contains" || action.Assertions[1].Value != `"items"` {
		t.Fatalf("wildcard response schema was not selected: %#v", action.Assertions)
	}
}

func TestConvertOpenAPIMultipleSuccessStatusesUseRange(t *testing.T) {
	const spec = `{
  "openapi":"3.0.3",
  "info":{"title":"multiple","version":"1"},
  "paths":{"/jobs":{"post":{"operationId":"createJob","responses":{
    "201":{"description":"created"},
    "202":{"description":"accepted"}
  }}}}
}`
	result, err := Convert(ImportRequest{Content: spec, Name: "multiple.json"})
	if err != nil {
		t.Fatal(err)
	}
	assertions := result.Script.Actions[0].Assertions
	if len(assertions) != 1 || assertions[0].Type != "status_code_range" || assertions[0].Min != 200 || assertions[0].Max != 299 {
		t.Fatalf("assertions = %#v, want 2xx range", assertions)
	}
}

func TestConvertOpenAPIGatesConflictingSuccessSchemas(t *testing.T) {
	const spec = `{
  "openapi":"3.0.3",
  "info":{"title":"conflict","version":"1"},
  "paths":{"/jobs":{"post":{"operationId":"createJob","responses":{
    "201":{"description":"created","content":{"application/json":{"schema":{"type":"object","properties":{"id":{"type":"string"}}}}}},
    "202":{"description":"accepted","content":{"application/json":{"schema":{"type":"object","properties":{"ticket":{"type":"string"}}}}}}
  }}}}
}`
	result, err := Convert(ImportRequest{Content: spec, Name: "conflict.json"})
	if err != nil {
		t.Fatal(err)
	}
	action := result.Script.Actions[0]
	if action.Metadata["execution_blocker"] != "import_review_required" {
		t.Fatalf("conflicting response schemas were not review-gated: %#v", action.Metadata)
	}
}

func TestConvertOpenAPIDoesNotGateSuccessSchemasForDifferentExamplesOnly(t *testing.T) {
	const spec = `{
  "openapi":"3.0.3",
  "info":{"title":"examples","version":"1"},
  "servers":[{"url":"https://api.example.test"}],
  "paths":{"/jobs":{"post":{"operationId":"createJob","responses":{
    "201":{"description":"created","content":{"application/json":{"schema":{"type":"object","example":{"id":"one"},"xml":{"name":"created-job"},"required":["id"],"properties":{"id":{"type":"string","example":"one","xml":{"name":"created-id"}}}}}}},
    "202":{"description":"accepted","content":{"application/json":{"schema":{"type":"object","example":{"id":"two"},"xml":{"name":"accepted-job"},"required":["id"],"properties":{"id":{"type":"string","example":"two","xml":{"name":"accepted-id"}}}}}}}
  }}}}
}`
	result, err := Convert(ImportRequest{Content: spec, Name: "examples.json"})
	if err != nil {
		t.Fatal(err)
	}
	if metadata := result.Script.Actions[0].Metadata; metadata["execution_blocker"] == "import_review_required" {
		t.Fatalf("compatible response schemas were gated because examples differed: %#v", metadata)
	}
}

func TestConvertOpenAPIDoesNotGateSuccessSchemasForReorderedSets(t *testing.T) {
	const spec = `{
  "openapi":"3.0.3",
  "info":{"title":"sets","version":"1"},
  "servers":[{"url":"https://api.example.test"}],
  "paths":{"/jobs":{"post":{"operationId":"createJob","responses":{
    "201":{"description":"created","content":{"application/json":{"schema":{"type":"object","properties":{"state":{"type":"string","enum":["queued","running","done"]}}}}}},
    "202":{"description":"accepted","content":{"application/json":{"schema":{"type":"object","properties":{"state":{"type":"string","enum":["done","queued","running"]}}}}}}
  }}}}
}`
	result, err := Convert(ImportRequest{Content: spec, Name: "sets.json"})
	if err != nil {
		t.Fatal(err)
	}
	if metadata := result.Script.Actions[0].Metadata; metadata["execution_blocker"] == "import_review_required" {
		t.Fatalf("compatible response schemas were gated because set order differed: %#v", metadata)
	}
}

func TestResolveSchemaUnionsAllOfRequiredAndScalarFacets(t *testing.T) {
	minimum := 1.0
	minLength := 3
	resolved := (resolver{}).resolveSchema(schema{AllOf: []schema{
		{Type: "object", Required: []string{"id"}, Properties: map[string]schema{"id": {Type: "string", MinLength: &minLength}}},
		{Required: []string{"count"}, Properties: map[string]schema{"count": {Type: "integer", Minimum: &minimum}}},
	}}, "test", nil)
	if len(resolved.Required) != 2 || resolved.Required[0] != "count" || resolved.Required[1] != "id" {
		t.Fatalf("required = %#v", resolved.Required)
	}
	if resolved.Properties["id"].MinLength == nil || resolved.Properties["count"].Minimum == nil {
		t.Fatalf("scalar facets were lost: %#v", resolved.Properties)
	}
}
