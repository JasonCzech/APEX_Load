package raml

// SupportMatrixEntry describes coverage of one RAML 1.0 construct.
type SupportMatrixEntry struct {
	ID             string   `json:"id"`
	Area           string   `json:"area"`
	Construct      string   `json:"construct"`
	Status         string   `json:"status"`
	ReviewRequired bool     `json:"review_required,omitempty"`
	Fields         []string `json:"fields,omitempty"`
	Notes          string   `json:"notes,omitempty"`
}

// GetSupportMatrix returns the RAML import support matrix.
func GetSupportMatrix() []SupportMatrixEntry {
	return []SupportMatrixEntry{
		{ID: "raml-root", Area: "Document", Construct: "title / version / baseUri", Status: "supported", Fields: []string{"title", "version", "baseUri", "baseUriParameters"}, Notes: "baseUri seeds config.base_url; the exact {version} parameter binds the document version, declared parameters bind an example/default/enum, and unresolved parameters become explicit environment references."},
		{ID: "raml-resources", Area: "Resources", Construct: "nested resources + URI params", Status: "supported", Fields: []string{"/resource", "/{id}", "uriParameters"}, Notes: "Resources and nested resources convert to HTTP actions; declared URI parameters become DSL parameters, while unresolved parameters remain review-blocked."},
		{ID: "raml-methods", Area: "Methods", Construct: "get/post/put/patch/delete/head/options", Status: "supported", Fields: []string{"method", "displayName", "description"}, Notes: "Each method becomes one HTTP action with a status_code 200 assertion."},
		{ID: "raml-bodies", Area: "Bodies", Construct: "request body examples", Status: "supported", Fields: []string{"body.<media>.example", "body.<media>.examples"}, Notes: "JSON example bodies are inlined; application/json with no example yields an empty object."},
		{ID: "raml-security-oauth2", Area: "Security", Construct: "OAuth 2.0 securityScheme", Status: "supported", Fields: []string{"securitySchemes", "settings.accessTokenUri"}, Notes: "Creates an http_oauth2_token init action (client_credentials) and a Bearer header on actions."},
		{ID: "raml-policy-clientid", Area: "Policy", Construct: "Client ID Enforcement", Status: "supported", ReviewRequired: true, Fields: []string{"securitySchemes.describedBy.headers", "queryParameters"}, Notes: "client_id/client_secret credentials map to env/secret placeholders with a review blocker; never embed live credentials."},
		{ID: "raml-includes", Area: "Includes", Construct: "!include (zip upload)", Status: "supported", Fields: []string{"!include"}, Notes: "!include directives resolve against files in a zip upload; unresolved includes are reported as errors."},
		{ID: "raml-traits", Area: "Traits", Construct: "traits / resourceTypes", Status: "partial", ReviewRequired: true, Notes: "Traits and resourceTypes are not expanded; declared headers/params on methods are honored but inheritance is review-only."},
		{ID: "raml-types", Area: "Types", Construct: "RAML data types / schemas", Status: "review_only", ReviewRequired: true, Notes: "Type definitions are not turned into request bodies; provide examples for body generation."},
		{ID: "raml-overlays", Area: "Overlays", Construct: "overlays / extensions / libraries", Status: "review_only", ReviewRequired: true, Notes: "Overlays, extensions, and shared libraries are not merged; flatten before import."},
	}
}

// GetSupportMatrixNotes returns top-level caveats for the RAML importer.
func GetSupportMatrixNotes() []string {
	return []string{
		"RAML import targets common HTTP/JSON API specifications; advanced type systems, overlays, and custom policy transforms are review-only.",
		"OAuth 2.0 security schemes generate a client-credentials http_oauth2_token init action; supply client_id/scope via env and client_secret via secret.",
		"Anypoint Client ID Enforcement is detected from client_id/client_secret credentials and mapped to env/secret placeholders with a review blocker.",
		"Multi-file specs are supported via zip upload so !include directives resolve against the referenced files.",
	}
}
