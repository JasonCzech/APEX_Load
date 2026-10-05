package api

import (
	"go/ast"
	"net/http"
)

// Closed analytical decoders retain raw mapping bytes until bounded semantic
// admission. Publish the admitted typed shape rather than opaque RawMessage;
// the generated client must not advertise options that runtime ignores/refuses.
func applyAnalysisRequestContract(route discoveredRoute, analysis *handlerContractAnalysis, types map[string]ast.Expr) {
	if analysis == nil || analysis.requestSchema == nil {
		return
	}
	mapping := route.Method == http.MethodPut && route.Path == "/api/v1/analysis/imports/{id}/mapping"
	mappingDefinition := (route.Method == http.MethodPost && route.Path == "/api/v1/analysis/mappings") || (route.Method == http.MethodPut && route.Path == "/api/v1/analysis/mappings/{id}")
	comparisonDefinition := (route.Method == http.MethodPost && route.Path == "/api/v1/analysis/comparisons") || (route.Method == http.MethodPut && route.Path == "/api/v1/analysis/comparisons/{id}")
	query := route.Method == http.MethodPost && (route.Path == "/api/v1/analysis/compare" || route.Path == "/api/v1/analysis/datasets/{id}/series")
	if !mapping && !mappingDefinition && !comparisonDefinition && !query {
		return
	}
	schema := analysis.requestSchema
	if mapping || mappingDefinition {
		name := "mapping"
		if mappingDefinition {
			name = "definition"
		}
		schema.Properties[name] = openAPISchemaForASTType(explicitOpenAPITypeExpression("analysis.ParquetMapping"), types, map[string]bool{}, 0)
	} else if comparisonDefinition {
		schema.Properties["definition"] = openAPISchemaForASTType(explicitOpenAPITypeExpression("AnalysisComparisonRequest"), types, map[string]bool{}, 0)
	}
	closeAnalysisRequestSchema(schema)
}

func closeAnalysisRequestSchema(schema *oasSchema) {
	if schema == nil {
		return
	}
	// Unlike historical reads, authored closed analytical requests reject null
	// for every present field, including optional identity pointers.
	schema.Nullable = false
	if schema.Type == "object" && schema.Properties != nil {
		schema.AdditionalProperties = boolPointer(false)
		for name, property := range schema.Properties {
			closeAnalysisRequestSchema(&property)
			schema.Properties[name] = property
		}
	}
	closeAnalysisRequestSchema(schema.Items)
	if value, ok := schema.AdditionalProperties.(*oasSchema); ok {
		closeAnalysisRequestSchema(value)
	}
}
