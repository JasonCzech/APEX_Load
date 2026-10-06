package api

import (
	"go/ast"
	"net/http"
	"strconv"
)

// applyScenarioAuthoringContract documents the scenario authoring view the
// same way the script contract documents view=authoring: the enriched legacy
// representation and the exact authored document are alternatives, and only
// the authored document carries a strong validator.
func applyScenarioAuthoringContract(route discoveredRoute, contract *inferredRouteContract, definitions map[string]ast.Expr) {
	if contract == nil || route.Path != "/api/v1/scenarios/{id}" || route.Method != http.MethodGet {
		return
	}
	legacy := openAPISchemaForASTType(ast.NewIdent("StoredScenario"), definitions, map[string]bool{}, 0)
	authoring := openAPISchemaForASTType(ast.NewIdent("ScenarioAuthoringProjection"), definitions, map[string]bool{}, 0)
	version := authoring.Properties["projection_version"]
	version.Enum = []string{scenarioAuthoringProjectionVersion}
	authoring.Properties["projection_version"] = version
	if contract.Responses == nil {
		contract.Responses = map[string]oasResponse{}
	}
	response := contract.Responses["200"]
	response.Content = map[string]oasMediaType{"application/json": {Schema: oasSchema{OneOf: []oasSchema{legacy, authoring}, Description: "Default: enriched StoredScenario. view=authoring: one coherent versioned authored scenario with its groups, data-pool bindings, library placement, monitor selection and durable nonce. Only the authoring representation carries a strong ETag."}}}
	if response.Headers == nil {
		response.Headers = map[string]oasHeader{}
	}
	response.Headers["ETag"] = oasHeader{Description: "Present only for view=authoring: one quoted scenario-authoring-v1:<content_revision> strong validator. This is not an ETag for the enriched StoredScenario response.", Schema: oasParamSchema{Type: "string"}}
	contract.Responses["200"] = response
	found := false
	for i := range contract.QueryParameters {
		if contract.QueryParameters[i].Name == "view" {
			contract.QueryParameters[i].Schema = oasParamSchema{Type: "string", Enum: []string{"authoring"}}
			contract.QueryParameters[i].Description = "Optional exact authoring projection. Omit for the enriched legacy view. Repeated or unsupported values fail."
			found = true
		}
	}
	if !found {
		contract.QueryParameters = append(contract.QueryParameters, oasParameter{Name: "view", In: "query", Description: "Optional exact authoring projection. Omit for the enriched legacy view. Repeated or unsupported values fail.", Schema: oasParamSchema{Type: "string", Enum: []string{"authoring"}}})
	}
	for _, status := range []int{400, 404, 503} {
		key := strconv.Itoa(status)
		if _, exists := contract.Responses[key]; !exists {
			contract.Responses[key] = oasResponse{Description: http.StatusText(status), Content: map[string]oasMediaType{"application/json": {Schema: oasSchema{Type: "object", Properties: map[string]oasSchema{"error": {Type: "string"}}, Required: []string{"error"}}}}}
		}
	}
}
