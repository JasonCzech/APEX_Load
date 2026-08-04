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
