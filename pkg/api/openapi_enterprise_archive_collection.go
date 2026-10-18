package api

import (
	"net/http"
	"sort"
)

func enterpriseArchiveReviewRoute(method, path string) bool {
	return method == http.MethodPost && (path == "/api/v1/scripts/import/enterprise-script-archive" || path == "/api/v1/scripts/import/enterprise-script-archive/validate")
}

// The protected response builder and direct collection encoder hide their map
// contracts from the bounded AST walker. Declare the two disjoint envelopes
// explicitly: ordinary reviews require scripts, while packages return archives.
func applyEnterpriseArchiveCollectionContract(route discoveredRoute, contract *inferredRouteContract) {
	if contract == nil || !enterpriseArchiveReviewRoute(route.Method, route.Path) {
		return
	}
	const memberDescription = "Optional exact source_name of one ZIP/USZ member in an archive collection. Select a member to retry or revalidate that script independently and before attaching supplemental sources or HAR evidence."
	member := oasParameter{Name: "archive_member", In: "query", Description: memberDescription, Schema: oasParamSchema{Type: "string"}}
	found := false
	for index := range contract.QueryParameters {
		if contract.QueryParameters[index].Name == member.Name && contract.QueryParameters[index].In == member.In {
			contract.QueryParameters[index] = member
			found = true
			break
		}
	}
	if !found {
		contract.QueryParameters = append(contract.QueryParameters, member)
	}
	sort.Slice(contract.QueryParameters, func(i, j int) bool {
		return contract.QueryParameters[i].Name < contract.QueryParameters[j].Name
	})
	if contract.RequestBody != nil {
		if media, ok := contract.RequestBody.Content["multipart/form-data"]; ok {
			if media.Schema.Properties == nil {
				media.Schema.Properties = map[string]oasSchema{}
			}
			media.Schema.Properties["archive_member"] = oasSchema{Type: "string", Description: memberDescription}
			contract.RequestBody.Content["multipart/form-data"] = media
		}
	}
	if contract.Responses == nil {
		contract.Responses = map[string]oasResponse{}
	}
	delete(contract.Responses, "2XX")
	contract.Responses["200"] = oasResponse{
		Description: "Converted script review, or independent per-member collection reviews. A valid collection returns 200 even when individual members fail; inspect successful_count, failed_count, and each member's error. This does not verify remote execution.",
		Content: map[string]oasMediaType{
			"application/json": {Schema: oasSchema{Ref: "#/components/schemas/EnterpriseArchiveReviewResponse"}},
		},
	}
}

func enterpriseArchiveCollectionOpenAPISchemas() map[string]oasSchema {
	object := oasSchema{Type: "object", AdditionalProperties: boolPointer(true)}
	optionalObject := object
	optionalObject.Nullable = true
	strings := oasSchema{Type: "array", Items: &oasSchema{Type: "string"}}
	schemas := map[string]oasSchema{
		"EnterpriseArchiveReviewResponse": {
			Description: "Single-project review or a bounded ZIP package of independent ZIP/USZ script projects.",
			OneOf: []oasSchema{
				{Ref: "#/components/schemas/EnterpriseArchiveScriptReviewResponse"},
				{Ref: "#/components/schemas/EnterpriseArchiveCollectionResponse"},
			},
		},
		"EnterpriseArchiveScriptReviewResponse": {
			Type: "object", AdditionalProperties: boolPointer(true),
			Description: "One converted script project's drafts, source review, settings parity, fidelity, and optional protected credential migration capabilities. Java drafts retain their governed approval requirement.",
			Properties: map[string]oasSchema{
				"script":                           optionalObject,
				"scripts":                          {Type: "array", Items: &object},
				"warnings":                         strings,
				"summary":                          object,
				"stats":                            object,
				"source_review":                    optionalObject,
				"fidelity_report":                  optionalObject,
				"review_artifact":                  optionalObject,
				"detected_protocol":                optionalObject,
				"protocol_compat_report":           optionalObject,
				"parameterization":                 optionalObject,
				"preflight":                        optionalObject,
				"review_sections":                  {Type: "array", Items: &object},
				"repair_suggestions":               {Type: "array", Items: &object},
				"secret_binding_requirements":      {Type: "array", Items: &object, Nullable: true},
				"source_secret_session_id":         {Type: "string", Description: "Optional opaque capability scoped to this project's retained source credentials."},
				"source_secret_session_expires_at": {Type: "string", Format: "date-time"},
				"source_secret_migration_allowed":  {Type: "boolean"},
				"import_draft_expires_at":          {Type: "string", Format: "date-time"},
				"secret_store":                     optionalObject,
				"unsupported_elements":             strings,
			},
			Required: []string{"script", "scripts", "warnings", "summary"},
		},
		"EnterpriseArchiveCollectionMember": {
			Type: "object", AdditionalProperties: boolPointer(false),
			Description: "One independent member. A successful conversion includes response; a failed conversion includes error. Settings, fidelity, and capabilities belong only to this member.",
			Properties: map[string]oasSchema{
				"source_name": {Type: "string", Description: "Exact relative collection member path; reuse as archive_member with the original uploaded package."},
				"source_size": {Type: "integer", Description: "Member archive size in bytes."},
				"response":    {Ref: "#/components/schemas/EnterpriseArchiveScriptReviewResponse"},
				"error":       {Type: "string", Description: "Disclosure-safe per-member import error."},
			},
			Required: []string{"source_name", "source_size"},
		},
		"EnterpriseArchiveCollectionResponse": {
			Type: "object", AdditionalProperties: boolPointer(false),
			Description: "A non-recursive package of at most 100 independent script archives, bounded by combined archive entry and decompression limits. Child reviews are not flattened or attributed to a sibling.",
			Properties: map[string]oasSchema{
				"archive_collection": {Type: "boolean", Description: "Always true for this collection envelope."},
				"archives":           {Type: "array", Items: &oasSchema{Ref: "#/components/schemas/EnterpriseArchiveCollectionMember"}},
				"count":              {Type: "integer"},
				"successful_count":   {Type: "integer"},
				"failed_count":       {Type: "integer"},
				"warnings":           strings,
				"unassigned_files":   {Type: "array", Items: &oasSchema{Type: "string"}, Nullable: true, Description: "Loose collection files were not applied to any script; select the intended member and attach them as supplemental sources."},
			},
			Required: []string{"archive_collection", "archives", "count", "successful_count", "failed_count", "warnings", "unassigned_files"},
		},
	}
	review := schemas["EnterpriseArchiveScriptReviewResponse"]
	applyCanonicalImportResponseSchema(&review)
	schemas["EnterpriseArchiveScriptReviewResponse"] = review
	return schemas
}
