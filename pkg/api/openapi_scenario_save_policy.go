package api

import (
	"net/http"
	"strings"
)

// Describe the foundation's explicit refusals without advertising a conditioned
// scenario representation or silently treating If-Match as a supported guard.
func applyScenarioSavePolicyAvailabilityContract(route discoveredRoute, contract *inferredRouteContract) {
	if contract == nil {
		return
	}
	projectWrite := (route.Method == http.MethodPost && route.Path == "/api/v1/projects") || (route.Method == http.MethodPut && route.Path == "/api/v1/projects/{id}")
	if projectWrite && contract.RequestBody != nil {
		if media, exists := contract.RequestBody.Content["application/json"]; exists {
			settings, hasSettings := media.Schema.Properties["settings"]
			authoring, hasAuthoring := settings.Properties["authoring"]
			policy, hasPolicy := authoring.Properties["require_scenario_save_if_match"]
			if hasSettings && hasAuthoring && hasPolicy {
				policy.Description = "Scenario conditioned HTTP saves are unavailable in this release. Omit or set false. Enabling true is rejected with 400 before project persistence; stored policy does not imply supported scenario If-Match."
				authoring.Properties["require_scenario_save_if_match"] = policy
				settings.Properties["authoring"] = authoring
				media.Schema.Properties["settings"] = settings
				contract.RequestBody.Content["application/json"] = media
			}
		}
	}
	if route.Method != http.MethodPut || route.Path != "/api/v1/scenarios/{id}" {
		return
	}
	description := "Conditioned scenario saves are unavailable. Any presented If-Match, including empty or repeated values, is refused with 503 after resource ownership admission. Omit for the existing unconditioned compatibility save. This endpoint does not accept a strong scenario authoring validator in this release."
	found := false
	for i := range contract.HeaderParameters {
		if strings.EqualFold(contract.HeaderParameters[i].Name, "If-Match") {
			contract.HeaderParameters[i].Description = description
			contract.HeaderParameters[i].Required = false
			contract.HeaderParameters[i].Schema = oasParamSchema{Type: "string"}
			found = true
		}
	}
	if !found {
		contract.HeaderParameters = append(contract.HeaderParameters, oasParameter{Name: "If-Match", In: "header", Description: description, Schema: oasParamSchema{Type: "string"}})
	}
	if contract.Responses == nil {
		contract.Responses = map[string]oasResponse{}
	}
	response := contract.Responses["503"]
	response.Description = "Service unavailable. scenario_save_if_match_unavailable refuses an explicitly requested unsupported condition; scenario_save_strict_policy_unavailable refuses an externally configured strict policy before mutation. Both have fixed safe error/code/ref guidance. Other policy or persistence failures retain the generic error/ref body. No conditioned save or successful mutation is implied."
	response.Content = map[string]oasMediaType{"application/json": {Schema: oasSchema{
		Type: "object", Properties: map[string]oasSchema{
			"error": {Type: "string"}, "ref": {Type: "string"},
			"code": {Type: "string", Enum: []string{"scenario_save_if_match_unavailable", "scenario_save_strict_policy_unavailable"}},
		}, Required: []string{"error"},
	}}}
	contract.Responses["503"] = response
}
