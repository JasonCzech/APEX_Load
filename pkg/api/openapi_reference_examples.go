package api

import (
	"go/ast"
	"net/http"
)

// Examples are deliberately authored, not inferred from zero values: a Go type
// cannot express the handler's semantic validation or credential boundaries.
func applyAPIReferenceExamples(route discoveredRoute, contract *inferredRouteContract, definitions map[string]ast.Expr) {
	if contract == nil {
		return
	}
	for status, response := range contract.Responses {
		if status != "401" && status != "403" {
			continue
		}
		media, ok := response.Content["application/json"]
		if !ok || media.Schema.Ref != "#/components/schemas/ErrorResponse" {
			continue
		}
		message := "Authentication required or invalid."
		if status == "403" {
			message = "Insufficient permissions for this operation."
		}
		media.Example = map[string]any{"error": message}
		response.Content["application/json"] = media
		contract.Responses[status] = response
	}
	if route.Method == http.MethodPost && route.Path == "/api/v1/scripts/validate" && contract.RequestBody != nil {
		media, ok := contract.RequestBody.Content["application/json"]
		if ok {
			media.Example = map[string]any{"script": map[string]any{
				"name": "Validate an HTTP flow", "version": "1.0.0", "protocol": "http",
				"config":  map[string]any{"base_url": "https://example.com"},
				"actions": []any{map[string]any{"name": "Home page", "type": "request", "method": "GET", "target": "/"}},
			}}
			contract.RequestBody.Content["application/json"] = media
		}
	}
	if route.Method != http.MethodGet || route.Path != "/api/v1/scripts" {
		return
	}
	if contract.Responses == nil {
		contract.Responses = map[string]oasResponse{}
	}
	scriptSchema := openAPISchemaForASTType(ast.NewIdent("StoredScript"), definitions, map[string]bool{}, 0)
	response := contract.Responses["200"]
	response.Description = "Scripts visible in the selected scope. count is the total after filtering, before pagination. limit and offset are included when either is supplied or q is nonempty. scripts may be null when the source list is nil."
	response.Content = map[string]oasMediaType{"application/json": {
		Schema: oasSchema{Type: "object", Required: []string{"count", "scripts"}, Properties: map[string]oasSchema{
			"count":   {Type: "integer", Description: "Total matching scripts before pagination."},
			"scripts": {Type: "array", Nullable: true, Items: &scriptSchema},
			"limit":   {Type: "integer", Description: "Present for paginated or searched results; defaults to 100 and is capped at 1000."},
			"offset":  {Type: "integer", Description: "Present for paginated or searched results; defaults to 0."},
		}},
		Example: map[string]any{"count": 0, "scripts": []any{}},
	}}
	contract.Responses["200"] = response
	for index := range contract.QueryParameters {
		parameter := &contract.QueryParameters[index]
		switch parameter.Name {
		case "q":
			parameter.Description = "Case-insensitive substring search across script name, ID, and reference key. Nonempty search enables pagination."
		case "limit":
			parameter.Description = "Page size. Defaults to 100; values above 1000 are capped. Empty, zero, negative, and invalid values use the default."
		case "offset":
			parameter.Description = "Zero-based starting offset. Empty, negative, and invalid values use 0; offsets beyond the total return no scripts."
		case "project_id":
			parameter.Description = "Filter to scripts in this project; access is checked against the selected scope."
		case "folder_id":
			parameter.Description = "Filter to a folder. An explicitly empty value selects unfiled scripts."
		}
	}
}
