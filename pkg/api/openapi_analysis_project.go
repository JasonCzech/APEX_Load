package api

import (
	"net/http"
	"strings"
)

// applyAnalysisProjectContract documents what the analysis policy and
// evaluation handlers require but read through shared helpers: every route
// is scoped by a required project_id (400 without it), the policy list by a
// required kind, and evaluation creates replay by Idempotency-Key.
func applyAnalysisProjectContract(route discoveredRoute, contract *inferredRouteContract) {
	if contract == nil || !strings.HasPrefix(route.Path, "/api/v1/analysis/policies") && !strings.HasPrefix(route.Path, "/api/v1/analysis/evaluations") {
		return
	}
	requireQueryParameter(contract, "project_id", "Project that owns the policy or evaluation; requests without it are refused with 400.")
	if route.Method == http.MethodGet && route.Path == "/api/v1/analysis/policies" {
		requireQueryParameter(contract, "kind", "Policy kind to list; requests without it are refused with 400.")
	}
	if contract.Responses == nil {
		contract.Responses = map[string]oasResponse{}
	}
	if _, documented := contract.Responses["400"]; !documented {
		contract.Responses["400"] = oasResponse{Description: "Bad request: a required parameter is missing or invalid.", Content: map[string]oasMediaType{"application/json": {Schema: scenarioSaveErrorBody()}}}
	}
	if route.Method != http.MethodPost || route.Path != "/api/v1/analysis/evaluations" {
		return
	}
	accepted := contract.Responses["202"]
	if accepted.Headers == nil {
		accepted.Headers = map[string]oasHeader{}
	}
	accepted.Headers["Idempotent-Replayed"] = oasHeader{Description: "true when the response returns the job an earlier request with the same Idempotency-Key created.", Schema: oasParamSchema{Type: "string", Enum: []string{"true"}}}
	contract.Responses["202"] = accepted
	for status, codes := range map[string][]string{
		"400": {"idempotency_key_invalid"},
		"409": {"idempotency_key_reused"},
		"503": {"idempotency_unavailable"},
	} {
		response := contract.Responses[status]
		if response.Description == "" {
			response.Description = http.StatusText(map[string]int{"400": 400, "409": 409, "503": 503}[status])
		}
		response.Description += " Idempotency outcomes carry a code; other failures keep the generic error body."
		response.Content = map[string]oasMediaType{"application/json": {Schema: scenarioSaveErrorBody(codes...)}}
		contract.Responses[status] = response
	}
}

// requireQueryParameter marks a query parameter required, adding it when
// inference did not see the helper that reads it.
func requireQueryParameter(contract *inferredRouteContract, name, description string) {
	for i := range contract.QueryParameters {
		if contract.QueryParameters[i].Name == name {
			contract.QueryParameters[i].Required = true
			contract.QueryParameters[i].Description = description
			return
		}
	}
	contract.QueryParameters = append(contract.QueryParameters, oasParameter{Name: name, In: "query", Required: true, Description: description, Schema: oasParamSchema{Type: "string"}})
}
