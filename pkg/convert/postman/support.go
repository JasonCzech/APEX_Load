package postman

// SupportMatrixEntry documents how a Postman collection construct is handled by
// the importer, mirroring the shape used by the OpenAPI importer so the
// dashboard can render every import support matrix the same way.
type SupportMatrixEntry struct {
	ID             string   `json:"id"`
	Area           string   `json:"area"`
	Construct      string   `json:"construct"`
	Status         string   `json:"status"`
	ReviewRequired bool     `json:"review_required,omitempty"`
	Fields         []string `json:"fields,omitempty"`
	Notes          string   `json:"notes,omitempty"`
}

// GetSupportMatrix returns the Postman import coverage matrix.
func GetSupportMatrix() []SupportMatrixEntry {
	return []SupportMatrixEntry{
		{ID: "postman-collection", Area: "Document", Construct: "Collection v2.1 (+ environment export)", Status: "supported", Fields: []string{"info", "item", "variable", "auth", "event"}, Notes: "Folders are flattened; a separate environment export can be supplied to seed variable values."},
		{ID: "postman-requests", Area: "Operations", Construct: "HTTP requests", Status: "supported", Fields: []string{"method", "header", "url", "url.variable", "url.query"}, Notes: "Raw and structured URLs are supported; disabled headers/query entries are dropped."},
		{ID: "postman-bodies", Area: "Bodies", Construct: "raw / urlencoded / graphql", Status: "supported", Fields: []string{"body.raw", "body.urlencoded", "body.graphql"}},
		{ID: "postman-bodies-form", Area: "Bodies", Construct: "form-data / file uploads", Status: "partial", ReviewRequired: true, Fields: []string{"body.formdata"}, Notes: "Form-data is flattened to JSON for review; multipart replay and file parts require manual validation."},
		{ID: "postman-auth-basic", Area: "Security", Construct: "basic / bearer / apikey", Status: "supported", Fields: []string{"auth.basic", "auth.bearer", "auth.apikey"}, Notes: "Credentials become secret/env placeholders; API key is injected as a header or query parameter."},
		{ID: "postman-auth-noauth", Area: "Security", Construct: "noauth override", Status: "supported", Notes: "An explicit per-request noauth overrides inherited collection auth at replay."},
		{ID: "postman-auth-oauth2", Area: "Security", Construct: "oauth2", Status: "partial", ReviewRequired: true, Fields: []string{"auth.oauth2"}, Notes: "Represented as a bearer token placeholder; token acquisition must be configured before replay."},
		{ID: "postman-auth-signed", Area: "Security", Construct: "digest / ntlm / awsv4 / hawk", Status: "review_only", ReviewRequired: true, Notes: "Cannot be executed by the HTTP engine (fail closed); dropped with a warning and left for manual configuration."},
		{ID: "postman-variables", Area: "Variables", Construct: "collection / environment / dynamic", Status: "supported", Fields: []string{"{{var}}", "{{$guid}}", "{{$timestamp}}", "{{$randomInt}}"}, Notes: "Known Postman dynamic variables map to runtime functions; unknown ones are left intact with a warning."},
		{ID: "postman-data", Area: "Replay", Construct: "parameter data source", Status: "supported", ReviewRequired: true, Notes: "Optionally binds detected parameters to a generated CSV data source for per-VUser variation; seed it before replay."},
		{ID: "postman-chaining", Area: "Replay", Construct: "response→request chaining", Status: "supported", ReviewRequired: true, Fields: []string{"event.test pm.*.set(pm.response...)"}, Notes: "Test-script captures of pm.response.json()/headers into variables become response extractors so later {{var}} references resolve at replay."},
		{ID: "postman-scripts", Area: "Scripts", Construct: "pre-request / test scripts", Status: "review_only", ReviewRequired: true, Fields: []string{"event.prerequest", "event.test"}, Notes: "Literal pm.*.set assignments seed variables and response captures become correlation extractors; other JS (signing, conditionals, assertions) is preserved for manual review."},
	}
}

// GetSupportMatrixNotes returns importer-wide caveats for the review panel.
func GetSupportMatrixNotes() []string {
	return []string{
		"Postman import targets HTTP API replay; pre-request and test scripts are not executed.",
		"Signed auth schemes (digest/ntlm/awsv4/hawk) fail closed at HTTP replay and are dropped with a warning rather than producing an unrunnable script.",
		"Supply a Postman environment export and seed the generated data source to make imported scripts replay with realistic, varying data.",
	}
}
