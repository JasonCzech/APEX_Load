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
	versionDescription := "Receipt contract for a keyed create; ignored without Idempotency-Key. Absent or 1 is the legacy contract above. 2 completes the receipt in the create's own transaction and stores the disclosure-safe original outcome: a replay then returns an AuthoringReceiptOutcome (receipt_version, resource_type, resource_id, project_id, content_revision, created_at and the original authoring_projection, or authoring_projection_omitted naming why) instead of the resource as it is now; read the current authoring projection before editing. A recorded outcome is replayed only to a request that asks for contract 2; a key first claimed under contract 1, a contract 1 retry of a contract 2 key, and a contract 2 receipt completed without an outcome (by an older controller) all replay the resource as it is now. A 201 echoes the contract that applied. Any other value is refused with 400 idempotency_receipt_version_unsupported, and a controller whose store cannot complete receipts atomically refuses 2 with 503 idempotency_receipt_version_unavailable."
	contract.HeaderParameters = append(contract.HeaderParameters, oasParameter{Name: authoringReceiptVersionHeader, In: "header", Description: versionDescription, Schema: oasParamSchema{Type: "string", Enum: []string{"1", "2"}}})
	if contract.Responses == nil {
		contract.Responses = map[string]oasResponse{}
	}
	created := contract.Responses["201"]
	if created.Headers == nil {
		created.Headers = map[string]oasHeader{}
	}
	created.Headers["Idempotent-Replayed"] = oasHeader{Description: "true when the response returns the resource an earlier request with the same Idempotency-Key created. Under receipt contract 1 the body is the resource as it is now; under contract 2 it is the AuthoringReceiptOutcome the original create recorded.", Schema: oasParamSchema{Type: "string", Enum: []string{"true"}}}
	created.Headers[authoringReceiptVersionHeader] = oasHeader{Description: "On a keyed create, the receipt contract that applied: 2 only when the original outcome was recorded and the request asked for it.", Schema: oasParamSchema{Type: "string", Enum: []string{"1", "2"}}}
	created.Headers["X-APEX-Original-Content-Revision"] = oasHeader{Description: "On a replay, the authored content revision the original create produced. Absent when the replay adopted a create whose response was lost, since the resource may have been edited since.", Schema: oasParamSchema{Type: "string"}}
	if media, ok := created.Content["application/json"]; ok {
		// A version 2 replay returns the recorded original outcome instead.
		media.Schema = oasSchema{OneOf: []oasSchema{media.Schema, {Ref: "#/components/schemas/AuthoringReceiptOutcome"}}}
		created.Content["application/json"] = media
	}
	contract.Responses["201"] = created
	body := func(codes ...string) map[string]oasMediaType {
		return map[string]oasMediaType{"application/json": {Schema: oasSchema{Type: "object", Properties: map[string]oasSchema{"error": {Type: "string"}, "code": {Type: "string", Enum: codes}}, Required: []string{"error"}}}}
	}
	for status, codes := range map[string][]string{
		"400": {"idempotency_key_invalid", "idempotency_receipt_version_unsupported"},
		"409": {"idempotency_key_reused", "idempotency_key_expired", "idempotency_request_in_progress"},
		"410": {"idempotency_resource_deleted"},
		"503": {"idempotency_unavailable", "idempotency_receipt_version_unavailable"},
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

func authoringReceiptOpenAPISchemas() map[string]oasSchema {
	return map[string]oasSchema{
		"AuthoringReceiptOutcome": {
			Type:        "object",
			Description: "The original outcome a receipt contract 2 create recorded in its own transaction, returned unchanged by a replay. It is not the resource's current state.",
			Properties: map[string]oasSchema{
				"receipt_version":              {Type: "integer", Description: "Always 2."},
				"resource_type":                {Type: "string", Enum: []string{"script", "scenario"}},
				"resource_id":                  {Type: "string"},
				"project_id":                   {Type: "string"},
				"content_revision":             {Type: "string"},
				"created_at":                   {Type: "string", Format: "date-time"},
				"authoring_projection":         {Type: "object", AdditionalProperties: boolPointer(true), Description: "The disclosure-safe authoring projection the create committed."},
				"authoring_projection_omitted": {Type: "string", Enum: []string{"exceeds_receipt_outcome_bound"}, Description: "Why authoring_projection is absent."},
			},
			Required: []string{"receipt_version", "resource_type", "resource_id", "project_id", "content_revision", "created_at"},
		},
	}
}
