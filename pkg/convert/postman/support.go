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
		{ID: "postman-collection", Area: "Document", Construct: "Collection v2.1 (+ environment export)", Status: "supported", Fields: []string{"info", "item", "variable", "auth", "event"}, Notes: "Folders are flattened after resolving collection → folder → request auth precedence; a separate environment export can seed variable values."},
		{ID: "postman-requests", Area: "Operations", Construct: "HTTP requests", Status: "supported", Fields: []string{"method", "header", "url", "url.variable", "url.query"}, Notes: "Raw and structured URLs are supported. Active header order, duplicate occurrences, and authored casing are preserved; disabled entries are dropped."},
		{ID: "postman-bodies", Area: "Bodies", Construct: "raw / urlencoded / graphql", Status: "supported", Fields: []string{"body.raw", "body.urlencoded", "body.graphql"}},
		{ID: "postman-bodies-form", Area: "Bodies", Construct: "form-data / file uploads", Status: "partial", ReviewRequired: true, Fields: []string{"body.formdata"}, Notes: "Form-data is serialized as deterministic multipart/form-data with ordered duplicate fields and source boundaries when present. File parts become governed file bindings and remain blocked until an asset is supplied."},
		{ID: "postman-auth-basic", Area: "Security", Construct: "basic / bearer / digest / apikey", Status: "supported", Fields: []string{"auth.basic", "auth.bearer", "auth.digest", "auth.apikey"}, Notes: "Credentials become governed runtime placeholders; API keys are injected as headers or query parameters and digest maps to the native HTTP runtime."},
		{ID: "postman-auth-noauth", Area: "Security", Construct: "noauth override", Status: "supported", Notes: "An explicit per-request noauth overrides inherited collection auth at replay."},
		{ID: "postman-auth-oauth2", Area: "Security", Construct: "oauth2", Status: "partial", ReviewRequired: true, Fields: []string{"auth.oauth2"}, Notes: "Represented as a bearer token placeholder; token acquisition must be configured before replay."},
		{ID: "postman-auth-signed", Area: "Security", Construct: "ntlm / awsv4 / hawk / unknown", Status: "review_only", ReviewRequired: true, Notes: "Unsupported negotiation or signing behavior is preserved as review metadata and durably gated before transport."},
		{ID: "postman-variables", Area: "Variables", Construct: "collection / environment / dynamic", Status: "supported", Fields: []string{"{{var}}", "{{$guid}}", "{{$timestamp}}", "{{$randomInt}}"}, Notes: "Known Postman dynamic variables map to runtime functions; unknown ones are left intact with a warning."},
		{ID: "postman-data", Area: "Replay", Construct: "parameter data source", Status: "supported", ReviewRequired: true, Notes: "Optionally binds detected parameters to a generated CSV data source for per-VUser variation; seed it before replay."},
		{ID: "postman-chaining", Area: "Replay", Construct: "response→request chaining", Status: "supported", ReviewRequired: true, Fields: []string{"event.test pm.*.set(pm.response...)"}, Notes: "Test-script captures of pm.response.json()/headers into variables become response extractors so later {{var}} references resolve at replay."},
		{ID: "postman-scripts", Area: "Scripts", Construct: "pre-request / test scripts", Status: "review_only", ReviewRequired: true, Fields: []string{"event.prerequest", "event.test"}, Notes: "Exact status assertions and deterministic variable/correlation patterns are translated. Every residual script behavior is preserved for review and durably gated before transport."},
	}
}

// GetSupportMatrixNotes returns importer-wide caveats for the review panel.
func GetSupportMatrixNotes() []string {
	return []string{
		"Postman import targets HTTP API replay; only explicitly supported script patterns are translated and residual JavaScript is blocked.",
		"NTLM and source-specific signing schemes fail closed at HTTP replay; digest is executed by the native HTTP runtime.",
		"Supply a Postman environment export and seed the generated data source to make imported scripts replay with realistic, varying data.",
	}
}
