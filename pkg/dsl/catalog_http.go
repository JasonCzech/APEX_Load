package dsl

// httpActionSpecs declares the request-shaped action types used by the http,
// soap, and java_http protocols. The action type itself is the HTTP method;
// validation stays in validate.go's generic path (target required), so these
// specs carry metadata only — except http_sequence, whose child-request
// rules live here.
func httpActionSpecs() []ActionSpec {
	requestFields := []FieldSpec{
		{Name: "target", Required: true, Kind: FieldKindString, Doc: "Request path or absolute URL; relative paths resolve against config.base_url."},
		{Name: "method", Kind: FieldKindString, Doc: "Explicit HTTP method override; defaults to the action type."},
		{Name: "headers", Kind: FieldKindObject, Doc: "Request headers merged over config.http.default_headers."},
		{Name: "body", Kind: FieldKindString, Doc: "Request body; template variables are expanded."},
		{Name: "body_file", Kind: FieldKindString, Doc: "Local file containing the request body. Resolved by ParseFile relative to the script directory, confined to that directory, then stored as inline body. Not accepted by API uploads; use body there."},
		{Name: "body_encoding", Kind: FieldKindString, Doc: "Body encoding hint, e.g. base64 for binary payloads."},
		{Name: "params", Kind: FieldKindObject, Doc: "Protocol-specific request options."},
		{Name: "soap", Kind: FieldKindObject, Doc: "SOAP-only: {action, operation, body_mode (raw_xml|generated)}."},
	}
	methods := []struct {
		method  string
		summary string
	}{
		{"GET", "HTTP GET request."},
		{"POST", "HTTP POST request."},
		{"PUT", "HTTP PUT request."},
		{"PATCH", "HTTP PATCH request."},
		{"DELETE", "HTTP DELETE request."},
		{"HEAD", "HTTP HEAD request."},
		{"OPTIONS", "HTTP OPTIONS request."},
	}
	specs := make([]ActionSpec, 0, len(methods)+2)
	for _, m := range methods {
		specs = append(specs, ActionSpec{
			Type:      m.method,
			Protocols: []string{"http", "soap", "java_http"},
			Summary:   m.summary,
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
		Summary:   "Composite step that issues an ordered list of child HTTP requests as one reported unit.",
		Fields: []FieldSpec{
			{Name: "params.requests", Required: true, Kind: FieldKindArray, Doc: "Child requests: {name, method (or type), target, headers, body}. At least one required."},
		},
		Validate: validateHTTPSequenceActionIssues,
	})
	return specs
}
