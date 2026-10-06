package api

import (
	"net/http"
	"strconv"
	"strings"
)

// scenarioUnconditionedWriterRoutes cannot compare an authored-content
// validator inside their mutation, so they refuse a presented If-Match.
var scenarioUnconditionedWriterRoutes = map[string]bool{
	http.MethodDelete + " /api/v1/scenarios/{id}":                       true,
	http.MethodPut + " /api/v1/scenarios/{id}/data-pools":               true,
	http.MethodPut + " /api/v1/scenarios/{id}/library-assignment":       true,
	http.MethodPost + " /api/v1/scenarios/{id}/runtime/apply-to-groups": true,
	http.MethodPut + " /api/v1/scenarios/{id}/monitor-bindings":         true,
}

func setOptionalIfMatchHeader(contract *inferredRouteContract, description string) {
	for i := range contract.HeaderParameters {
		if strings.EqualFold(contract.HeaderParameters[i].Name, "If-Match") {
			contract.HeaderParameters[i].Description = description
			contract.HeaderParameters[i].Required = false
			contract.HeaderParameters[i].Schema = oasParamSchema{Type: "string"}
			return
		}
	}
	contract.HeaderParameters = append(contract.HeaderParameters, oasParameter{Name: "If-Match", In: "header", Description: description, Schema: oasParamSchema{Type: "string"}})
}

func scenarioSaveErrorBody(codes ...string) oasSchema {
	properties := map[string]oasSchema{"error": {Type: "string"}, "ref": {Type: "string"}}
	if len(codes) > 0 {
		properties["code"] = oasSchema{Type: "string", Enum: codes}
	}
	return oasSchema{Type: "object", Properties: properties, Required: []string{"error"}}
}

// applyScenarioSavePolicyAvailabilityContract documents conditioned scenario
// saves, the project strict-save policy and the writers that refuse If-Match.
func applyScenarioSavePolicyAvailabilityContract(route discoveredRoute, contract *inferredRouteContract) {
	if contract == nil {
		return
	}
	if contract.Responses == nil {
		contract.Responses = map[string]oasResponse{}
	}
	projectWrite := (route.Method == http.MethodPost && route.Path == "/api/v1/projects") || (route.Method == http.MethodPut && route.Path == "/api/v1/projects/{id}")
	if projectWrite && contract.RequestBody != nil {
		if media, exists := contract.RequestBody.Content["application/json"]; exists {
			settings, hasSettings := media.Schema.Properties["settings"]
			authoring, hasAuthoring := settings.Properties["authoring"]
			policy, hasPolicy := authoring.Properties["require_scenario_save_if_match"]
			if hasSettings && hasAuthoring && hasPolicy {
				policy.Description = "When true, ordinary scenario saves (PUT /api/v1/scenarios/{id}) must present a strong scenario-authoring-v1 If-Match; a missing validator returns 428. The policy is re-read inside the save transaction. Controllers without durable scenario revisions refuse saves under this policy with 503 scenario_save_strict_policy_unavailable."
				authoring.Properties["require_scenario_save_if_match"] = policy
				settings.Properties["authoring"] = authoring
				media.Schema.Properties["settings"] = settings
				contract.RequestBody.Content["application/json"] = media
			}
		}
	}
	if scenarioUnconditionedWriterRoutes[route.Method+" "+route.Path] {
		setOptionalIfMatchHeader(contract, "Not supported on this endpoint. Any presented If-Match is refused with 503 scenario_writer_if_match_unavailable instead of being ignored. For stale-edit protection, save the whole scenario with PUT /api/v1/scenarios/{id} and a scenario-authoring-v1 validator.")
		response := contract.Responses["503"]
		response.Description = "Service unavailable. scenario_writer_if_match_unavailable refuses a presented If-Match this endpoint cannot honor; no mutation occurred. Other failures retain the generic error/ref body."
		response.Content = map[string]oasMediaType{"application/json": {Schema: scenarioSaveErrorBody("scenario_writer_if_match_unavailable")}}
		contract.Responses["503"] = response
		return
	}
	if route.Method != http.MethodPut || route.Path != "/api/v1/scenarios/{id}" {
		return
	}
	setOptionalIfMatchHeader(contract, "Exactly one quoted strong scenario-authoring-v1:<content_revision> tag obtained with GET view=authoring (or the ETag of a previous save). Weak, list, wildcard, malformed and unsupported-version tags fail with 400 after resource ownership is resolved. A stale validator returns 412. Without a validator the save is an audited legacy compatibility save, unless the project's authoring.require_scenario_save_if_match policy requires one (428).")
	response := contract.Responses["200"]
	if response.Headers == nil {
		response.Headers = map[string]oasHeader{}
	}
	response.Headers["X-APEX-Scenario-Save-Mode"] = oasHeader{Description: "conditioned or legacy_unconditioned. A legacy save cannot protect a client draft loaded before the server read its current snapshot.", Schema: oasParamSchema{Type: "string", Enum: []string{scenarioSaveModeConditioned, scenarioSaveModeLegacy}}}
	response.Headers["ETag"] = oasHeader{Description: "The new scenario-authoring-v1 validator when the controller has durable scenario revisions; it matches authoring_projection.content_revision in the body, which was read inside the save transaction.", Schema: oasParamSchema{Type: "string"}}
	contract.Responses["200"] = response
	response503 := contract.Responses["503"]
	response503.Description = "Service unavailable. scenario_save_outcome_unknown and scenario_save_outcome_audit_unavailable mean the save may already have committed: reload before retrying and never retry automatically. scenario_save_if_match_unavailable and scenario_save_strict_policy_unavailable are returned only by controllers without durable scenario revisions. Other failures retain the generic error/ref body."
	response503.Content = map[string]oasMediaType{"application/json": {Schema: scenarioSaveErrorBody("scenario_save_outcome_unknown", "scenario_save_outcome_audit_unavailable", "scenario_save_if_match_unavailable", "scenario_save_strict_policy_unavailable")}}
	contract.Responses["503"] = response503
	for _, status := range []int{400, 404, 409, 412, 428} {
		key := strconv.Itoa(status)
		if _, exists := contract.Responses[key]; !exists {
			contract.Responses[key] = oasResponse{Description: http.StatusText(status), Content: map[string]oasMediaType{"application/json": {Schema: oasSchema{Type: "object", Properties: map[string]oasSchema{"error": {Type: "string"}}, Required: []string{"error"}}}}}
		}
	}
}
