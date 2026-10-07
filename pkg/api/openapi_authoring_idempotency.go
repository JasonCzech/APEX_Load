package api

import (
	"net/http"
	"strings"
)

// applyAuthoringIdempotencyContract documents retry-safe ordinary creates.
func applyAuthoringIdempotencyContract(route discoveredRoute, contract *inferredRouteContract) {
	if contract == nil || route.Method != http.MethodPost || (route.Path != "/api/v1/scripts" && route.Path != "/api/v1/scenarios") {
		return
	}
	description := "Optional retry key (16-512 visible ASCII characters), scoped to project, caller and create kind. The same key and request replay the originally created resource for 24 hours with Idempotent-Replayed: true; a changed request returns 409 idempotency_key_reused, an expired key 409 idempotency_key_expired, a deleted original 410 idempotency_resource_deleted (it is never recreated), and an in-flight duplicate 409 idempotency_request_in_progress with Retry-After. Controllers without durable receipts refuse the key with 503 idempotency_unavailable. A key is kept, never reused, while an earlier attempt may have committed; it is released for a corrected request only when the create was refused before running or a durable read proves nothing was created. A credential-preserving script import that claims a single-use import attestation is fenced by that attestation instead; one without an attestation uses the key like any other create. Since this contract, an empty or shorter key is refused with 400 idempotency_key_invalid instead of being ignored."
	found := false
	for i := range contract.HeaderParameters {
		if strings.EqualFold(contract.HeaderParameters[i].Name, "Idempotency-Key") {
			contract.HeaderParameters[i].Description = description
			contract.HeaderParameters[i].Required = false
			contract.HeaderParameters[i].Schema = oasParamSchema{Type: "string"}
			found = true
		}
	}
	if !found {
		contract.HeaderParameters = append(contract.HeaderParameters, oasParameter{Name: "Idempotency-Key", In: "header", Description: description, Schema: oasParamSchema{Type: "string"}})
	}
	if contract.Responses == nil {
		contract.Responses = map[string]oasResponse{}
	}
	created := contract.Responses["201"]
	if created.Headers == nil {
		created.Headers = map[string]oasHeader{}
	}
	created.Headers["Idempotent-Replayed"] = oasHeader{Description: "true when the response returns the resource an earlier request with the same Idempotency-Key created. The body is the resource as it is now.", Schema: oasParamSchema{Type: "string", Enum: []string{"true"}}}
	created.Headers["X-APEX-Original-Content-Revision"] = oasHeader{Description: "On a replay, the authored content revision the original create produced. Absent when the replay adopted a create whose response was lost, since the resource may have been edited since.", Schema: oasParamSchema{Type: "string"}}
	contract.Responses["201"] = created
	body := func(codes ...string) map[string]oasMediaType {
		return map[string]oasMediaType{"application/json": {Schema: oasSchema{Type: "object", Properties: map[string]oasSchema{"error": {Type: "string"}, "code": {Type: "string", Enum: codes}}, Required: []string{"error"}}}}
	}
	for status, codes := range map[string][]string{
		"400": {"idempotency_key_invalid"},
		"409": {"idempotency_key_reused", "idempotency_key_expired", "idempotency_request_in_progress"},
		"410": {"idempotency_resource_deleted"},
		"503": {"idempotency_unavailable"},
	} {
		response := contract.Responses[status]
		if response.Description == "" {
			response.Description = http.StatusText(map[string]int{"400": 400, "409": 409, "410": 410, "503": 503}[status])
		}
		response.Description += " Idempotency outcomes carry a code; other failures keep the generic error body."
		response.Content = body(codes...)
		if status == "409" {
			response.Headers = map[string]oasHeader{"Retry-After": {Description: "Seconds to wait before retrying idempotency_request_in_progress.", Schema: oasParamSchema{Type: "string"}}}
		}
		if status == "503" && route.Path == "/api/v1/scenarios" {
			// A create whose commit acknowledgement was lost may have
			// committed: unlike idempotency_unavailable, it must not be
			// retried without its key.
			response.Description += " scenario_save_outcome_unknown means the create may already have committed: retry only with the same Idempotency-Key, which adopts it."
			response.Content = map[string]oasMediaType{"application/json": {Schema: scenarioSaveErrorBody(append(codes, "scenario_save_outcome_unknown")...)}}
		}
		contract.Responses[status] = response
	}
}
