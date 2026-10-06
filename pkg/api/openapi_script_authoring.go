package api

import (
	"go/ast"
	"net/http"
	"strconv"
	"strings"
)

func applyScriptAuthoringContract(route discoveredRoute, contract *inferredRouteContract, definitions map[string]ast.Expr) {
	if contract == nil {
		return
	}
	if route.Path == "/api/v1/scripts/{id}" && route.Method == http.MethodGet {
		legacy := openAPISchemaForASTType(ast.NewIdent("StoredScript"), definitions, map[string]bool{}, 0)
		authoring := openAPISchemaForASTType(ast.NewIdent("ScriptAuthoringProjection"), definitions, map[string]bool{}, 0)
		version := authoring.Properties["projection_version"]
		version.Enum = []string{scriptAuthoringProjectionVersion}
		authoring.Properties["projection_version"] = version
		response := contract.Responses["200"]
		response.Content = map[string]oasMediaType{"application/json": {Schema: oasSchema{OneOf: []oasSchema{legacy, authoring}, Description: "Default: enriched StoredScript. view=authoring: one coherent versioned authored document and placement with its durable nonce. Only the authoring representation carries a strong ETag."}}}
		if response.Headers == nil {
			response.Headers = map[string]oasHeader{}
		}
		response.Headers["ETag"] = oasHeader{Description: "Present only for view=authoring: one quoted script-authoring-v1:<content_revision> strong validator. This is not an ETag for the enriched StoredScript response.", Schema: oasParamSchema{Type: "string"}}
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
			contract.QueryParameters = append(contract.QueryParameters, oasParameter{Name: "view", In: "query", Schema: oasParamSchema{Type: "string", Enum: []string{"authoring"}}})
		}
	}
	if route.Path == "/api/v1/scripts/secret-references/rebind" && route.Method == http.MethodPost {
		response := contract.Responses["200"]
		legacy := openAPISchemaForASTType(ast.NewIdent("ScriptSecretRebindLegacyResponse"), definitions, map[string]bool{}, 0)
		authoring := openAPISchemaForASTType(ast.NewIdent("ScriptSecretRebindAuthoringResponse"), definitions, map[string]bool{}, 0)
		response.Content = map[string]oasMediaType{"application/json": {Schema: oasSchema{OneOf: []oasSchema{authoring, legacy}, Description: "With an exact observed authoring If-Match: a pure authored draft transformation with no context timestamps, authority or new ETag. Without If-Match: the timestamp-gated legacy transformed script."}}}
		contract.Responses["200"] = response
		description := "Optional exact strong authoring validator from view=authoring. Validates the current authored revision for this pure draft transformation; stale returns 412, malformed 400 and unavailable capability 503. No write or new ETag occurs. Without a validator, the legacy draft UpdatedAt gate applies (409)."
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
		for _, status := range []int{400, 404, 409, 412, 503} {
			key := strconv.Itoa(status)
			if _, exists := contract.Responses[key]; !exists {
				contract.Responses[key] = oasResponse{Description: http.StatusText(status), Content: map[string]oasMediaType{"application/json": {Schema: oasSchema{Type: "object", Properties: map[string]oasSchema{"error": {Type: "string"}}, Required: []string{"error"}}}}}
			}
		}
	}
	if route.Path == "/api/v1/scripts/{id}" && route.Method == http.MethodPut {
		description := "Exactly one quoted strong script-authoring-v1:<content_revision> tag obtained with view=authoring. Weak, list, wildcard, malformed and unsupported-version tags fail. Missing tags remain audited legacy saves unless this project's authoring.require_script_save_if_match policy requires them. Stale authored content or placement returns 412; missing required tags return 428."
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
		response := contract.Responses["200"]
		if response.Headers == nil {
			response.Headers = map[string]oasHeader{}
		}
		response.Headers["X-APEX-Script-Save-Mode"] = oasHeader{Description: "conditioned or legacy_unconditioned. A legacy save cannot protect a client draft created before the server reads its current snapshot.", Schema: oasParamSchema{Type: "string", Enum: []string{"conditioned", "legacy_unconditioned"}}}
		contract.Responses["200"] = response
		// A fixed outcome code lets clients preserve their draft and require
		// explicit reload after a transport/audit uncertainty. It contains no
		// persistence diagnostics or executable data.
		response503 := contract.Responses["503"]
		response503.Description = "Service unavailable. script_save_outcome_unknown and script_save_outcome_audit_unavailable require explicit reload before retry; mutation may already have committed. Generic capability/policy/internal failures retain the normal error/ref body."
		response503.Content = map[string]oasMediaType{"application/json": {Schema: oasSchema{
			Type: "object", Properties: map[string]oasSchema{
				"error": {Type: "string"}, "ref": {Type: "string"},
				"code": {Type: "string", Enum: []string{"script_save_outcome_unknown", "script_save_outcome_audit_unavailable"}},
			}, Required: []string{"error"},
		}}}
		contract.Responses["503"] = response503
		for _, status := range []int{400, 404, 412, 428, 503} {
			key := strconv.Itoa(status)
			if _, exists := contract.Responses[key]; !exists {
				contract.Responses[key] = oasResponse{
					Description: http.StatusText(status),
					Content: map[string]oasMediaType{
						"application/json": {
							Schema: oasSchema{
								Type:       "object",
								Properties: map[string]oasSchema{"error": {Type: "string"}},
								Required:   []string{"error"},
							},
						},
					},
				}
			}
		}
	}
}
