package openapi

type SupportMatrixEntry struct {
	ID             string   `json:"id"`
	Area           string   `json:"area"`
	Construct      string   `json:"construct"`
	Status         string   `json:"status"`
	ReviewRequired bool     `json:"review_required,omitempty"`
	Fields         []string `json:"fields,omitempty"`
	Notes          string   `json:"notes,omitempty"`
}

func GetSupportMatrix() []SupportMatrixEntry {
	return []SupportMatrixEntry{
		{ID: "openapi-doc", Area: "Document", Construct: "OpenAPI 2.0 / 3.x / 3.1", Status: "supported", Fields: []string{"openapi", "swagger", "paths", "servers", "host/basePath"}},
		{ID: "openapi-methods", Area: "Operations", Construct: "HTTP operations", Status: "supported", Fields: []string{"get", "post", "put", "patch", "delete", "head", "options"}, Notes: "TRACE remains review-only."},
		{ID: "openapi-params", Area: "Parameters", Construct: "path/query/header/cookie parameters", Status: "supported", Fields: []string{"parameters", "style", "explode"}},
		{ID: "openapi-bodies-json", Area: "Bodies", Construct: "JSON request schemas", Status: "supported", Fields: []string{"requestBody.content.application/json", "schema"}, Notes: "Scalars become data placeholders with schema-aware seed values."},
		{ID: "openapi-bodies-form", Area: "Bodies", Construct: "form-urlencoded and multipart", Status: "supported", ReviewRequired: true, Fields: []string{"application/x-www-form-urlencoded", "multipart/form-data"}, Notes: "Multipart binary fields create file placeholders that must be replaced before replay."},
		{ID: "openapi-bodies-xml", Area: "Bodies", Construct: "XML request schemas", Status: "partial", ReviewRequired: true, Fields: []string{"application/xml", "text/xml", "schema.xml"}, Notes: "Schema-derived XML is generated; namespace-heavy payloads require review."},
		{ID: "openapi-security", Area: "Security", Construct: "HTTP, API key, OAuth2/OIDC", Status: "partial", ReviewRequired: true, Fields: []string{"security", "securitySchemes", "securityDefinitions"}, Notes: "The first fully supported OR alternative is selected in source order and every scheme in its AND requirement is applied deterministically. Conflicting or unsupported combinations are saved as blocked drafts. OAuth2 client_credentials can generate token init; auth-code/implicit/OIDC use secret placeholders."},
		{ID: "openapi-extractors", Area: "Replay", Construct: "response extractors", Status: "supported", Fields: []string{"responses.2xx.content.application/json.schema", "headers.Location"}, Notes: "Identity and Location values are extracted into vars for later actions."},
		{ID: "openapi-chaining", Area: "Replay", Construct: "cross-operation chaining", Status: "supported", ReviewRequired: true, Notes: "High-confidence path ID chains auto-apply; lower-confidence matches are surfaced for review."},
		{ID: "openapi-replay-readiness", Area: "Replay", Construct: "replay readiness", Status: "supported", ReviewRequired: true, Fields: []string{"base_url", "env", "secret", "data_sources", "file placeholders"}, Notes: "Preflight diagnostics keep blockers visible before save or execution."},
		{ID: "openapi-composition", Area: "Schemas", Construct: "allOf / oneOf / anyOf / nullable / maps", Status: "partial", ReviewRequired: true, Notes: "allOf and nullable are handled; ambiguous variants remain review-only."},
		{ID: "openapi-refs", Area: "References", Construct: "local $ref", Status: "partial", ReviewRequired: true, Fields: []string{"#/components/*", "#/definitions", "#/parameters"}, Notes: "External refs are reported but not inlined."},
	}
}

func GetSupportMatrixNotes() []string {
	return []string{
		"OpenAPI import targets HTTP API replay; SOAP-specific behavior belongs in the WSDL importer.",
		"Generated file placeholders and unresolved secrets are valid draft artifacts but block replay readiness until supplied.",
		"Correlation is spec-inferred, so high-confidence chains are applied conservatively and shown in the review panel.",
		"Authentication is applied per operation from operation/document-level security; OR alternatives and AND requirements follow OpenAPI semantics and there is no single collection-level default auth.",
	}
}
