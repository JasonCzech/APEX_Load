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
	description := "Optional retry key (16-512 visible ASCII characters), scoped to project, caller and create kind. The same key and request replay the originally created resource for 24 hours with Idempotent-Replayed: true; a changed request returns 409 idempotency_key_reused, an expired key 409 idempotency_key_expired, a deleted original 410 idempotency_resource_deleted (it is never recreated), and an in-flight duplicate 409 idempotency_request_in_progress with Retry-After. Controllers without durable receipts refuse the key with 503 idempotency_unavailable. Credential-preserving script imports are fenced by their import attestation instead."
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
	created.Headers["X-APEX-Original-Content-Revision"] = oasHeader{Description: "On a replay, the authored content revision the original create produced, when recorded.", Schema: oasParamSchema{Type: "string"}}
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
		contract.Responses[status] = response
	}
}
