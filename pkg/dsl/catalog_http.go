package dsl

import httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"

// httpActionSpecs declares the request-shaped action types used by the http,
// soap, and java_http protocols. The action type itself is the HTTP method;
// validation stays in validate.go's generic path (target required), so these
// specs carry metadata only — except http_sequence, whose child-request
// rules live here.
func httpActionSpecs() []ActionSpec {
	requestFields := []FieldSpec{
		{Name: "target", Required: true, Kind: FieldKindString, Doc: "Request path or absolute URL; relative paths resolve against config.base_url. Not used by an HTML-mode step (params.html_step)."},
		{Name: "method", Kind: FieldKindString, Doc: "Explicit HTTP method override; defaults to the action type."},
		{Name: "http_version", Kind: FieldKindEnum, Enum: []string{"auto", "1.1", "2"}, SchemaEnum: true, Doc: "Request transport policy. Omission inherits config.http.version; auto negotiates, 1.1 requires HTTP/1.1, and 2 requires HTTP/2 (TLS or prior-knowledge h2c) without fallback. Null and empty strings are invalid."},
		{Name: "headers", Kind: FieldKindObject, Doc: "Legacy single-value compatibility map merged over config.http.default_headers. Names must be unique case-insensitively; when header_list contains the same name, header_list is authoritative."},
		{Name: "header_list", Kind: FieldKindArray, Doc: "Ordered logical header occurrences. The DSL retains casing, order, and ordinary duplicates; the HTTP transport may normalize casing/order or combine protocol-compatible values. Transport-owned, connection-specific, or duplicate singleton headers that cannot preserve semantics must be omitted behind a durable import review gate."},
		{Name: "body", Kind: FieldKindString, Doc: "Request body; template variables are expanded."},
		{Name: "body_file", Kind: FieldKindString, Doc: "Local file containing the request body. Resolved by ParseFile relative to the script directory, confined to that directory, then stored as inline body. Not accepted by API uploads; use body there."},
		{Name: "body_encoding", Kind: FieldKindString, Doc: "Body wire encoding: base64 for binary payloads; windows-1252 or utf-16le (no byte-order mark) for strict text encoding after interpolation, where unrepresentable characters fail; or c_escaped for text whose C escapes (`\\xNN`, octal, `\\\\`, `\\n`) name raw bytes, decoded after interpolation with template values sent verbatim (LoadRunner BodyBinary)."},
		{Name: "params.html_step", Kind: FieldKindObject, Doc: "HTML-mode step (LoadRunner web_link, web_image, web_submit_form, web_image_check) resolved against the page this virtual user last loaded, instead of target: {kind: link (text), image (alt and/or src; follows the enclosing link, the single area of its image map, or submits the form of an input image), image_check (passes when the page has the image; no request), or form (fields and hidden [{name, value}], optional action and submit [{name, value}]); ordinal (1-based), case_insensitive}. Forms are chosen by VuGen's order: every scripted field present, then action, then ordinal; the submission is the page's successful controls in document order with scripted values overriding by name, hidden values filling names the page lacks, and the form's method and encoding. The page URL becomes the Referer. A step that matches nothing fails with html_link_not_found, html_image_not_found or html_form_not_found. Steps inside frames are not supported yet."},
		{Name: "params.html_mode", Kind: FieldKindBool, Doc: "LoadRunner Mode=: false (Mode=HTTP) replays the step without parsing its page, so no resources are discovered from it and it does not become the page context for HTML-mode steps; absent or true (Mode=HTML) does both."},
		{Name: "params.resource_step", Kind: FieldKindBool, Doc: "LoadRunner Resource=1: the step is a resource. An HTTP error or transport failure becomes the warning resource_step_failed unless config.http.fail_noncritical_resources is on (a timeout also fails when resource_timeout_as_warning is explicitly off), and the step never becomes the page context."},
		{Name: "params.resources", Kind: FieldKindArray, Doc: "Explicit resource GETs belonging to this request: {url, referer?}. Maximum 256; relative URLs use the final response URL. Included in parent timing, cache, and verification."},
		{Name: "params", Kind: FieldKindObject, Doc: "Protocol-specific request options."},
		{Name: "soap", Kind: FieldKindObject, Doc: "SOAP-only: {action, operation, body_mode (raw_xml|generated)}."},
	}
	methods := httpprotocol.SupportedMethods()
	specs := make([]ActionSpec, 0, len(methods)+2)
	for _, method := range methods {
		specs = append(specs, ActionSpec{
			Type:      method,
			Protocols: []string{"http", "soap", "java_http"},
			Summary:   "HTTP " + method + " request.",
			Fields:    requestFields,
		})
	}
	specs = append(specs, ActionSpec{
		Type:      "http_oauth2_token",
		Protocols: []string{"http"},
		Summary:   "Fetch and cache an OAuth2 access token for later HTTP requests.",
		Fields: []FieldSpec{
			{Name: "params.token_url", Required: true, Kind: FieldKindString, Doc: "OAuth2 token endpoint URL."},
			{Name: "params.grant_type", Required: true, Kind: FieldKindString, Doc: "Grant type: client_credentials, jwt_bearer, refresh_token, or password."},
			{Name: "params.client_id", Required: true, Kind: FieldKindString, Doc: "OAuth2 client ID or Salesforce connected-app consumer key."},
			{Name: "params.client_secret", Kind: FieldKindString, Doc: "OAuth2 client secret when required by the grant; prefer a {{secret.NAME}} template."},
			{Name: "params.scope", Kind: FieldKindString, Doc: "Space-delimited OAuth2 scopes."},
			{Name: "params.audience", Kind: FieldKindString, Doc: "JWT-bearer audience; defaults to the token endpoint origin."},
			{Name: "params.issuer", Kind: FieldKindString, Doc: "JWT-bearer issuer; defaults to client_id."},
			{Name: "params.subject", Kind: FieldKindString, Doc: "JWT-bearer subject, usually the Salesforce integration username."},
			{Name: "params.private_key", Kind: FieldKindString, Doc: "PEM-encoded RSA private key for JWT-bearer assertions; prefer a {{secret.NAME}} template."},
			{Name: "params.refresh_token", Kind: FieldKindString, Doc: "Refresh token for refresh_token grant; prefer a {{secret.NAME}} template."},
			{Name: "params.username", Kind: FieldKindString, Doc: "Resource owner username for password grant; prefer an {{env.NAME}} template."},
			{Name: "params.password", Kind: FieldKindString, Doc: "Resource owner password for password grant; prefer a {{secret.NAME}} template."},
			{Name: "params.save_as", Kind: FieldKindString, Doc: "VUser variable name for the access token; defaults to access_token."},
		},
	})
	specs = append(specs, ActionSpec{
		Type:      "http_sequence",
		Protocols: []string{"http"},
		Summary:   "Composite HTTP step with sequential execution by default or bounded concurrent requests sharing one VUser session.",
		Fields: []FieldSpec{
			{Name: "params.requests", Required: true, Kind: FieldKindArray, Doc: "Child HTTP requests with full action fields. At least one required; concurrent mode permits at most 256. Concurrent children are prepared in source order, joined, then publish extractions in source order. A concurrent group whose every child sets metadata.source_request_transaction records one transaction per child under that name, timed by the child's request and passing when it succeeds, instead of the group's aggregate transaction; each counts toward a transaction goal. Naming only some children is rejected."},
			{Name: "params.execution_mode", Kind: FieldKindEnum, Enum: []string{HTTPSequenceSequential, HTTPSequenceConcurrent}, SchemaEnum: true, Doc: "Sequential when absent. Concurrent requests share the parent HTTP session; debugger pause and step occur at the group boundary."},
			{Name: "params.max_concurrency", Kind: FieldKindInt, Maximum: MaxHTTPConcurrency, Doc: "Integer 1..16. Concurrent default is min(8, child count). Concurrent preparation is bounded to 16 MiB of source and prepared surfaces and 64 MiB of work. Duplicate extractor names and nonzero child think time are rejected before traffic."},
		},
		Validate: validateHTTPSequenceActionIssues,
	})
	return specs
}
