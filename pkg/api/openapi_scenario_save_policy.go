package api

import (
	"net/http"
	"strconv"
	"strings"
)

// scenarioGuardedWriterRoutes are the secondary writers of scenario-authored
// state. Each compares an optional scenario-authoring-v1 validator inside its
// own transaction and obeys the project's strict save policy.
var scenarioGuardedWriterRoutes = map[string]bool{
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
// saves, the project strict-save policy and the guarded secondary writers.
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
				policy.Description = "When true, every scenario-authored write (PUT /api/v1/scenarios/{id}, DELETE, data-pools, library-assignment, monitor-bindings and runtime/apply-to-groups) must present a strong scenario-authoring-v1 If-Match; a missing validator returns 428. The policy is re-read inside each write's transaction. Controllers without durable scenario revisions refuse those writes under this policy with 503 scenario_save_strict_policy_unavailable. The dashboard sends a tag on every scenario write it makes: editor saves present the baseline their draft was loaded from, library deletes re-read the scenario, refuse one whose authored fields changed (its update time) since the list loaded and present its current tag, and library-assignment and monitor-binding saves present a current tag alongside their own revision (which is what guards them). On PUT an omitted authoring object keeps the current policies; send authoring: {} to clear them."
				authoring.Properties["require_scenario_save_if_match"] = policy
				settings.Properties["authoring"] = authoring
				media.Schema.Properties["settings"] = settings
				contract.RequestBody.Content["application/json"] = media
			}
		}
	}
	if scenarioGuardedWriterRoutes[route.Method+" "+route.Path] {
		setOptionalIfMatchHeader(contract, "Optional. Exactly one quoted strong scenario-authoring-v1:<content_revision> tag (GET view=authoring or a previous ETag); it is compared inside this write's transaction and a stale tag returns 412. Malformed tags return 400. Without a validator the write is unconditioned unless the project's authoring.require_scenario_save_if_match policy requires one (428). Controllers without durable scenario revisions refuse a presented tag with 503 scenario_writer_if_match_unavailable and refuse every write under the strict policy with 503 scenario_save_strict_policy_unavailable.")
		for _, status := range []string{"200", "201"} {
			if route.Method == http.MethodDelete {
				// A deleted scenario has no revision to return.
				break
			}
			if response, exists := contract.Responses[status]; exists {
				if response.Headers == nil {
					response.Headers = map[string]oasHeader{}
				}
				response.Headers["ETag"] = oasHeader{Description: "The scenario-authoring-v1 validator committed by this write's own transaction, when the controller has durable scenario revisions.", Schema: oasParamSchema{Type: "string"}}
				contract.Responses[status] = response
			}
		}
		response := contract.Responses["503"]
		response.Description = "Service unavailable. scenario_save_outcome_unknown means the write may already have committed: reload before retrying and never retry automatically. scenario_writer_if_match_unavailable and scenario_save_strict_policy_unavailable are returned only by controllers without durable scenario revisions; no mutation occurred. Other failures retain the generic error/ref body."
		response.Content = map[string]oasMediaType{"application/json": {Schema: scenarioSaveErrorBody("scenario_save_outcome_unknown", "scenario_writer_if_match_unavailable", "scenario_save_strict_policy_unavailable")}}
		contract.Responses["503"] = response
		for _, status := range []int{400, 404, 412, 428} {
			key := strconv.Itoa(status)
			if _, exists := contract.Responses[key]; !exists {
				contract.Responses[key] = oasResponse{Description: http.StatusText(status), Content: map[string]oasMediaType{"application/json": {Schema: oasSchema{Type: "object", Properties: map[string]oasSchema{"error": {Type: "string"}}, Required: []string{"error"}}}}}
			}
		}
		return
	}
	if route.Method == http.MethodPost && route.Path == "/api/v1/scenarios" {
		if response, exists := contract.Responses["201"]; exists {
			if response.Headers == nil {
				response.Headers = map[string]oasHeader{}
			}
			response.Headers["ETag"] = oasHeader{Description: "The created scenario's scenario-authoring-v1 validator, read inside the create transaction, when the controller has durable scenario revisions; it matches authoring_projection.content_revision in the body. An Idempotency-Key replay carries the current validator only while the returned scenario is the durable state; otherwise read view=authoring before a conditioned save.", Schema: oasParamSchema{Type: "string"}}
			contract.Responses["201"] = response
		}
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
