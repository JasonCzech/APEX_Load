package api

import (
	"net/http"
	"testing"
)

func TestOpenAPIClosedAnalyticalRequestsRetainTypedIdentityAndDefinition(t *testing.T) {
	routes := []discoveredRoute{
		{Method: http.MethodPut, Path: "/api/v1/analysis/imports/{id}/mapping", Handler: "handleAnalysisMapping"},
		{Method: http.MethodPost, Path: "/api/v1/analysis/mappings", Handler: "handleAnalysisDefinitions"},
		{Method: http.MethodPut, Path: "/api/v1/analysis/mappings/{id}", Handler: "handleAnalysisDefinitions"},
		{Method: http.MethodPost, Path: "/api/v1/analysis/comparisons", Handler: "handleAnalysisDefinitions"},
		{Method: http.MethodPut, Path: "/api/v1/analysis/comparisons/{id}", Handler: "handleAnalysisDefinitions"},
		{Method: http.MethodPost, Path: "/api/v1/analysis/compare", Handler: "handleAnalysisComparison"},
		{Method: http.MethodPost, Path: "/api/v1/analysis/datasets/{id}/series", Handler: "handleAnalysisDatasetSeries"},
	}
	contracts, err := inferRouteContracts("server.go", routes)
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range routes {
		t.Run(route.Method+" "+route.Path, func(t *testing.T) {
			body := contracts[route.Method+" "+route.Path].RequestBody
			if body == nil || len(body.Content) != 1 {
				t.Fatal("closed request has opaque or extra media", body)
			}
			media, ok := body.Content["application/json"]
			if !ok || !analysisSchemaClosed(media.Schema) {
				t.Fatal("closed request no longer advertises JSON exact shape", media)
			}
			schema := media.Schema
			switch route.Path {
			case "/api/v1/analysis/imports/{id}/mapping":
				assertAnalysisMappingSchema(t, schema.Properties["mapping"])
			case "/api/v1/analysis/mappings", "/api/v1/analysis/mappings/{id}":
				assertAnalysisMappingSchema(t, schema.Properties["definition"])
			case "/api/v1/analysis/datasets/{id}/series":
				if schema.Properties["identity_schema_version"].Type != "integer" || schema.Properties["source_id"].Type != "string" || schema.Properties["service"].Type != "string" {
					t.Fatal("series identity contract was lost", schema)
				}
				assertAnalysisFilterSchema(t, schema.Properties["dimensions"])
			default:
				if route.Path != "/api/v1/analysis/compare" {
					schema = schema.Properties["definition"]
				}
				if !analysisSchemaClosed(schema) || !analysisSchemaClosed(schema.Properties["left"]) || schema.Properties["left"].Properties["service"].Type != "string" || schema.Properties["from"].Format != "date-time" {
					t.Fatal("comparison typed identity/window was lost", schema)
				}
				assertAnalysisFilterSchema(t, schema.Properties["left"].Properties["dimensions"])
			}
		})
	}
}
func analysisSchemaClosed(schema oasSchema) bool {
	closed, ok := schema.AdditionalProperties.(*bool)
	return schema.Type == "object" && schema.Properties != nil && ok && !*closed && !schema.Nullable
}
func assertAnalysisMappingSchema(t *testing.T, schema oasSchema) {
	t.Helper()
	if !analysisSchemaClosed(schema) || schema.Properties["version"].Type != "integer" || schema.Properties["timestamp"].Type != "array" {
		t.Fatal("mapping raw bytes were advertised as opaque", schema)
	}
	identity := schema.Properties["identity"]
	if !analysisSchemaClosed(identity) || identity.Properties["source"].Items == nil || identity.Properties["source"].Items.Type != "string" || identity.Properties["dimensions"].Items == nil || !analysisSchemaClosed(*identity.Properties["dimensions"].Items) {
		t.Fatal("mapping identity shape missing or nullable", identity)
	}
}
func assertAnalysisFilterSchema(t *testing.T, schema oasSchema) {
	t.Helper()
	value, ok := schema.AdditionalProperties.(*oasSchema)
	if schema.Type != "object" || !ok || value.Type != "string" || value.Nullable {
		t.Fatal("exact arbitrary filter keys no longer carry string values", schema)
	}
}
