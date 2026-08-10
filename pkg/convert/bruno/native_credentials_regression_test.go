package bruno

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestInspectNativePreservesAuthorizationSchemeWhileGoverningVariable(t *testing.T) {
	const sourceCredential = "composed-header-source-value"
	result, err := Inspect(Input{
		Files: map[string][]byte{
			"demo/opencollection.yml": []byte(`opencollection: 1.0.0
info: {name: Composed header}
`),
			"demo/request.yml": []byte(`info: {name: Composed header, uid: composed-header, type: http, seq: 1}
variables:
  - {name: token, value: ` + sourceCredential + `}
http:
  method: GET
  url: https://example.test
  headers:
    - {name: Authorization, value: "Bearer {{token}}"}
`),
		},
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}

	action := result.Scripts[0].Script.Actions[0]
	header := action.Headers["Authorization"]
	if !strings.HasPrefix(header, "Bearer {{secret.") || !strings.HasSuffix(header, "}}") {
		t.Fatalf("Authorization header = %q, want preserved Bearer scheme with governed variable", header)
	}
	foundMigratableValue := false
	for _, requirement := range result.SecretRequirements {
		if requirement.Value == "Bearer {{token}}" {
			t.Fatalf("composed unresolved header became a migratable secret: %#v", requirement)
		}
		if requirement.Value == sourceCredential {
			foundMigratableValue = true
		}
	}
	if !foundMigratableValue {
		t.Fatalf("resolved variable credential requirement not found: %#v", result.SecretRequirements)
	}
	sanitized := tarFile(t, result.SanitizedArchive, "demo/request.yml")
	if bytes.Contains(sanitized, []byte(sourceCredential)) {
		t.Fatalf("sanitized request retained source credential:\n%s", sanitized)
	}
}

func TestInspectNativeResolvesComposedSensitiveHeaderBeforeMigration(t *testing.T) {
	const sourceCredential = "composed-header-token"
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Composed header, uid: composed-header-prefix, type: http}
variables:
  - {name: token, value: ` + sourceCredential + `}
http:
  method: GET
  url: https://example.test
  headers:
    - {name: Authorization, value: "Bearer prefix-{{token}}"}
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := result.Scripts[0]
	header := candidate.Script.Actions[0].Headers["Authorization"]
	if !strings.HasPrefix(header, "{{secret.") || strings.Contains(header, "Bearer prefix-") {
		t.Fatalf("composed Authorization header = %q", header)
	}
	if len(candidate.SecretRequirements) != 1 ||
		candidate.SecretRequirements[0].Value != "Bearer prefix-"+sourceCredential {
		t.Fatalf("composed header requirements = %#v", candidate.SecretRequirements)
	}
	sanitized := tarFile(t, result.SanitizedArchive, "request.yml")
	for _, leaked := range []string{sourceCredential, "Bearer prefix-{{token}}"} {
		if bytes.Contains(sanitized, []byte(leaked)) {
			t.Fatalf("sanitized header source retained %q:\n%s", leaked, sanitized)
		}
	}
}

func TestInspectNativeGatesUnresolvedComposedSensitiveHeader(t *testing.T) {
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Unresolved header, uid: unresolved-header, type: http}
http:
  method: GET
  url: https://example.test
  headers:
    - {name: Authorization, value: "Bearer prefix-{{undefinedToken}}"}
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := result.Scripts[0]
	if len(candidate.MissingVariables) != 0 ||
		!hasNativeDiagnostic(candidate.Diagnostics, "bruno_sensitive_header_resolution_review_required") ||
		candidate.Script.Actions[0].Metadata["promotion_blocker"] != true {
		t.Fatalf("unresolved composed header candidate = %#v", candidate)
	}
	if len(candidate.SecretRequirements) != 1 ||
		candidate.SecretRequirements[0].Value != "" {
		t.Fatalf("unresolved composed header requirements = %#v", candidate.SecretRequirements)
	}
}

func TestInspectNativeSeparatesComposedSensitiveHeaderRequirements(t *testing.T) {
	const sourcePart = "shared-sensitive-part"
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Reused header part, uid: reused-header-part, type: http}
variables:
  - {name: part, value: ` + sourcePart + `}
http:
  method: GET
  url: https://example.test
  headers:
    - {name: Authorization, value: "Bearer prefix-{{part}}"}
    - {name: X-API-Key, value: "key-{{part}}"}
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := result.Scripts[0]
	authorization := candidate.Script.Actions[0].Headers["Authorization"]
	apiKey := candidate.Script.Actions[0].Headers["X-API-Key"]
	if authorization == apiKey ||
		!strings.HasPrefix(authorization, "{{secret.") ||
		!strings.HasPrefix(apiKey, "{{secret.") {
		t.Fatalf("composed header references = Authorization %q, X-API-Key %q", authorization, apiKey)
	}
	values := map[string]bool{}
	for _, requirement := range candidate.SecretRequirements {
		values[requirement.Value] = true
	}
	if len(candidate.SecretRequirements) != 2 ||
		!values["Bearer prefix-"+sourcePart] ||
		!values["key-"+sourcePart] {
		t.Fatalf("composed header requirements = %#v", candidate.SecretRequirements)
	}
	sanitized := tarFile(t, result.SanitizedArchive, "request.yml")
	for _, leaked := range []string{
		sourcePart,
		"Bearer prefix-{{part}}",
		"key-{{part}}",
	} {
		if bytes.Contains(sanitized, []byte(leaked)) {
			t.Fatalf("sanitized shared header dependency retained %q:\n%s", leaked, sanitized)
		}
	}
}

func TestInspectNativePreservesGovernedNamespacedSensitiveHeaders(t *testing.T) {
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Governed headers, uid: governed-headers, type: http}
http:
  method: GET
  url: https://example.test
  headers:
    - {name: Authorization, value: "Bearer {{secret.API_TOKEN}}"}
    - {name: Cookie, value: "session={{secret.SESSION_ID}}"}
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := result.Scripts[0]
	action := candidate.Script.Actions[0]
	if action.Headers["Authorization"] != "Bearer {{secret.API_TOKEN}}" ||
		action.Headers["Cookie"] != "session={{secret.SESSION_ID}}" {
		t.Fatalf("governed headers changed: %#v", action.Headers)
	}
	if action.Metadata["review_required"] != nil ||
		hasNativeDiagnostic(candidate.Diagnostics, "bruno_sensitive_header_resolution_review_required") {
		t.Fatalf("governed headers were review-gated: %#v", candidate)
	}
}

func TestInspectNativeGovernsPolicySensitiveHeaders(t *testing.T) {
	const (
		literalSignature = "literal-signature-value"
		templateValue    = "templated-authentication-value"
	)
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Policy headers, uid: policy-headers, type: http}
variables:
  - {name: neutralValue, value: ` + templateValue + `}
http:
  method: GET
  url: https://example.test
  headers:
    - {name: X-Signature, value: ` + literalSignature + `}
    - {name: Authentication, value: "{{neutralValue}}"}
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := result.Scripts[0]
	action := candidate.Script.Actions[0]
	for _, name := range []string{"X-Signature", "Authentication"} {
		if !strings.HasPrefix(action.Headers[name], "{{secret.") {
			t.Fatalf("%s header = %q", name, action.Headers[name])
		}
	}
	values := map[string]bool{}
	for _, requirement := range candidate.SecretRequirements {
		values[requirement.Value] = true
	}
	if !values[literalSignature] || !values[templateValue] {
		t.Fatalf("policy header requirements = %#v", candidate.SecretRequirements)
	}
	sanitized := tarFile(t, result.SanitizedArchive, "request.yml")
	for _, leaked := range []string{literalSignature, templateValue} {
		if bytes.Contains(sanitized, []byte(leaked)) {
			t.Fatalf("sanitized policy headers retained %q:\n%s", leaked, sanitized)
		}
	}
}

func TestInspectNativeResolvesComposedAuthCredentialBeforeMigration(t *testing.T) {
	const sourcePart = "auth-neutral-part"
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Composed auth, uid: composed-auth, type: http}
variables:
  - {name: neutralValue, value: ` + sourcePart + `}
http:
  method: GET
  url: https://example.test
  auth:
    type: basic
    username: fixed-user
    password: "prefix-{{neutralValue}}"
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := result.Scripts[0]
	auth := candidate.Script.Actions[0].Auth
	if auth == nil ||
		!strings.HasPrefix(auth.Password, "{{secret.") ||
		strings.Contains(auth.Password, "prefix-") ||
		len(candidate.SecretRequirements) != 2 {
		t.Fatalf("composed auth candidate = %#v", candidate)
	}
	values := map[string]bool{}
	for _, requirement := range candidate.SecretRequirements {
		values[requirement.Value] = true
	}
	if !values["fixed-user"] || !values["prefix-"+sourcePart] {
		t.Fatalf("composed auth requirements = %#v", candidate.SecretRequirements)
	}
	sanitized := tarFile(t, result.SanitizedArchive, "request.yml")
	for _, leaked := range []string{sourcePart, "prefix-{{neutralValue}}", "fixed-user"} {
		if bytes.Contains(sanitized, []byte(leaked)) {
			t.Fatalf("sanitized auth source retained %q:\n%s", leaked, sanitized)
		}
	}
}

func TestInspectNativeResolvesComposedSensitiveVariableDependencies(t *testing.T) {
	const sourcePart = "sensitive-variable-neutral-part"
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Composed sensitive variable, uid: composed-sensitive-variable, type: http}
variables:
  - {name: neutralValue, value: ` + sourcePart + `}
  - {name: password, value: "prefix-{{neutralValue}}"}
http:
  method: POST
  url: https://example.test
  body:
    type: text
    data: "credential={{password}}"
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := result.Scripts[0]
	if strings.Contains(candidate.Script.Actions[0].Body, sourcePart) ||
		strings.Contains(candidate.Script.Actions[0].Body, "{{password}}") ||
		!strings.Contains(candidate.Script.Actions[0].Body, "{{secret.") ||
		len(candidate.SecretRequirements) != 1 ||
		candidate.SecretRequirements[0].Value != "prefix-"+sourcePart {
		t.Fatalf("composed sensitive variable candidate = %#v", candidate)
	}
	sanitized := tarFile(t, result.SanitizedArchive, "request.yml")
	for _, leaked := range []string{sourcePart, "prefix-{{neutralValue}}"} {
		if bytes.Contains(sanitized, []byte(leaked)) {
			t.Fatalf("sanitized sensitive variable retained %q:\n%s", leaked, sanitized)
		}
	}
}

func TestInspectNativeRejectsUnusedComposedSensitiveVariable(t *testing.T) {
	_, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Unused sensitive variable, uid: unused-sensitive-variable, type: http}
variables:
  - {name: neutralValue, value: unused-source-secret}
  - {name: password, value: "prefix-{{neutralValue}}"}
http:
  method: GET
  url: https://example.test
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err == nil || !strings.Contains(err.Error(), "unprojected template") {
		t.Fatalf("unused sensitive variable error = %v", err)
	}

	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Disabled sensitive variable, uid: disabled-sensitive-variable, type: http}
variables:
  - {name: neutralValue, value: review-only}
  - {name: password, value: "prefix-{{neutralValue}}", disabled: true}
http:
  method: GET
  url: https://example.test
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatalf("disabled sensitive variable: %v", err)
	}
	if len(result.Scripts[0].SecretRequirements) != 0 {
		t.Fatalf("disabled sensitive variable requirements = %#v", result.Scripts[0].SecretRequirements)
	}
}

func TestInspectNativeGovernsLiteralURLQueryCredentials(t *testing.T) {
	const sourceCredential = "literal-query-credential"
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Query credential, uid: query-credential, type: http}
http:
  method: GET
  url: https://example.test/search?apiKey=` + sourceCredential + `&safe=retained
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := result.Scripts[0]
	target := candidate.Script.Actions[0].Target
	if strings.Contains(target, sourceCredential) ||
		!strings.Contains(target, "apiKey={{secret.") ||
		!strings.Contains(target, "safe=retained") {
		t.Fatalf("governed query target = %q", target)
	}
	if len(candidate.SecretRequirements) != 1 ||
		candidate.SecretRequirements[0].Value != sourceCredential ||
		!hasNativeDiagnostic(candidate.Diagnostics, "bruno_url_secret_encoding_review_required") ||
		candidate.Script.Actions[0].Metadata["promotion_blocker"] != true {
		t.Fatalf("query credential candidate = %#v", candidate)
	}
	if bytes.Contains(tarFile(t, result.SanitizedArchive, "request.yml"), []byte(sourceCredential)) {
		t.Fatal("sanitized source retained a literal URL query credential")
	}
}

func TestInspectNativeGovernsNeutralVariablesInSensitiveURLParameters(t *testing.T) {
	const sourceCredential = "neutral-query-variable-value"
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Query variable credential, uid: query-variable-credential, type: http}
variables:
  - {name: neutralValue, value: ` + sourceCredential + `}
http:
  method: GET
  url: https://example.test/search
  params:
    - {name: token, value: "{{neutralValue}}", type: query}
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := result.Scripts[0]
	if strings.Contains(candidate.Script.Actions[0].Target, sourceCredential) ||
		!strings.Contains(candidate.Script.Actions[0].Target, "token={{secret.") ||
		len(candidate.SecretRequirements) != 1 ||
		candidate.SecretRequirements[0].Value != sourceCredential ||
		!hasNativeDiagnostic(candidate.Diagnostics, "bruno_url_secret_encoding_review_required") {
		t.Fatalf("query variable candidate = %#v", candidate)
	}
	if bytes.Contains(tarFile(t, result.SanitizedArchive, "request.yml"), []byte(sourceCredential)) {
		t.Fatal("sanitized source retained a URL query credential dependency")
	}
}

func TestInspectNativeGovernsComposedURLCredentialAsWholeValue(t *testing.T) {
	const sourcePart = "query-composed-part"
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Composed query credential, uid: composed-query-credential, type: http}
variables:
  - {name: neutralValue, value: ` + sourcePart + `}
http:
  method: GET
  url: "https://example.test/search?token=Bearer-{{neutralValue}}"
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := result.Scripts[0]
	target := candidate.Script.Actions[0].Target
	if strings.Contains(target, "Bearer-") ||
		!strings.Contains(target, "token={{secret.") ||
		len(candidate.SecretRequirements) != 1 ||
		candidate.SecretRequirements[0].Value != "Bearer-"+sourcePart {
		t.Fatalf("composed query candidate = %#v", candidate)
	}
	sanitized := tarFile(t, result.SanitizedArchive, "request.yml")
	for _, leaked := range []string{sourcePart, "Bearer-{{neutralValue}}"} {
		if bytes.Contains(sanitized, []byte(leaked)) {
			t.Fatalf("sanitized composed query retained %q:\n%s", leaked, sanitized)
		}
	}
}

func TestInspectNativeComposedURLCredentialCountsValuelessDuplicate(t *testing.T) {
	const sourcePart = "query-composed-after-flag"
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Composed query after flag, uid: composed-query-after-flag, type: http}
variables:
  - {name: neutralValue, value: ` + sourcePart + `}
http:
  method: GET
  url: "https://example.test/search?token&token=Bearer-{{neutralValue}}"
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := result.Scripts[0]
	target := candidate.Script.Actions[0].Target
	if strings.Contains(target, "Bearer-") ||
		!strings.Contains(target, "?token&token={{secret.") ||
		len(candidate.SecretRequirements) != 1 ||
		candidate.SecretRequirements[0].Value != "Bearer-"+sourcePart {
		t.Fatalf("composed query candidate = %#v", candidate)
	}
	sanitized := tarFile(t, result.SanitizedArchive, "request.yml")
	for _, leaked := range []string{sourcePart, "Bearer-{{neutralValue}}"} {
		if bytes.Contains(sanitized, []byte(leaked)) {
			t.Fatalf("sanitized composed query retained %q:\n%s", leaked, sanitized)
		}
	}
}

func TestInspectNativeSeparatesComposedQueryAndPathParameterCredentials(t *testing.T) {
	const sourcePart = "shared-url-component"
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Composed URL params, uid: composed-url-params, type: http}
variables:
  - {name: neutralValue, value: ` + sourcePart + `}
http:
  method: GET
  url: https://example.test/items/:token
  params:
    - {name: token, value: "path-{{neutralValue}}", type: path}
    - {name: signature, value: "query-{{neutralValue}}", type: query}
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := result.Scripts[0]
	target := candidate.Script.Actions[0].Target
	if strings.Contains(target, "path-") ||
		strings.Contains(target, "query-") ||
		strings.Count(target, "{{secret.") != 2 {
		t.Fatalf("composed URL parameter target = %q", target)
	}
	values := map[string]bool{}
	for _, requirement := range candidate.SecretRequirements {
		values[requirement.Value] = true
	}
	if len(candidate.SecretRequirements) != 2 ||
		!values["path-"+sourcePart] ||
		!values["query-"+sourcePart] {
		t.Fatalf("composed URL parameter requirements = %#v", candidate.SecretRequirements)
	}
	sanitized := tarFile(t, result.SanitizedArchive, "request.yml")
	for _, leaked := range []string{
		sourcePart,
		"path-{{neutralValue}}",
		"query-{{neutralValue}}",
	} {
		if bytes.Contains(sanitized, []byte(leaked)) {
			t.Fatalf("sanitized URL parameters retained %q:\n%s", leaked, sanitized)
		}
	}
}

func TestInspectNativeGovernsSignedURLValues(t *testing.T) {
	const signature = "AbCdEf0123456789GhIjKlMn0Z"
	for _, source := range []string{
		`url: https://example.test/resource?sig=` + signature,
		`url: https://example.test/resource
  params:
    - {name: opaque, value: ` + signature + `, type: query}`,
	} {
		t.Run(source, func(t *testing.T) {
			result, err := Inspect(Input{
				Filename: "request.yml",
				Content: []byte(`info: {name: Signed URL, uid: signed-url, type: http}
http:
  method: GET
  ` + source + `
`),
				Options: InspectOptions{OutputMode: OutputModeNativeRequests},
			})
			if err != nil {
				t.Fatal(err)
			}
			candidate := result.Scripts[0]
			if strings.Contains(candidate.Script.Actions[0].Target, signature) ||
				len(candidate.SecretRequirements) != 1 ||
				candidate.SecretRequirements[0].Value != signature {
				t.Fatalf("signed URL candidate = %#v", candidate)
			}
			if bytes.Contains(tarFile(t, result.SanitizedArchive, "request.yml"), []byte(signature)) {
				t.Fatal("sanitized source retained a signed URL value")
			}
		})
	}
}

func TestInspectNativeRejectsAuthoredURLUserinfo(t *testing.T) {
	_, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: URL userinfo, uid: url-userinfo, type: http}
http:
  method: GET
  url: https://username:password@example.test/private
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err == nil || !strings.Contains(err.Error(), "authored URL userinfo is not supported") {
		t.Fatalf("URL userinfo error = %v", err)
	}
}

func TestInspectNativeRejectsSensitiveURLFragments(t *testing.T) {
	_, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Sensitive fragment, uid: sensitive-fragment, type: http}
http:
  method: GET
  url: https://example.test/callback#access_token=literal-fragment-secret
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err == nil || !strings.Contains(err.Error(), "fragment contains credential material") {
		t.Fatalf("sensitive URL fragment error = %v", err)
	}
}

func TestInspectNativeRejectsCredentialsHiddenInResolvedBaseURLVariables(t *testing.T) {
	for _, sourceURL := range []string{
		"https://username:password@example.test/private",
		"https://example.test/private?token=literal-secret",
	} {
		t.Run(sourceURL, func(t *testing.T) {
			_, err := Inspect(Input{
				Filename: "request.yml",
				Content: []byte(`info: {name: Variable URL credential, uid: variable-url-credential, type: http}
variables:
  - {name: baseURL, value: "` + sourceURL + `"}
http:
  method: GET
  url: "{{baseURL}}"
`),
				Options: InspectOptions{OutputMode: OutputModeNativeRequests},
			})
			if err == nil ||
				(!strings.Contains(err.Error(), "unsupported authored userinfo") &&
					!strings.Contains(err.Error(), "sensitive query value")) {
				t.Fatalf("resolved URL credential error = %v", err)
			}
		})
	}
}

func TestInspectNativeGovernsExactTemplatesInSensitiveXMLContexts(t *testing.T) {
	const sourceCredential = "xml-template-source-value"
	result, err := Inspect(Input{
		Files: map[string][]byte{
			"demo/opencollection.yml": []byte(`opencollection: 1.0.0
info: {name: XML template}
`),
			"demo/request.yml": []byte(`info: {name: XML template, uid: xml-template, type: http, seq: 1}
variables:
  - {name: bodyValue, value: ` + sourceCredential + `}
http:
  method: POST
  url: https://example.test
  body:
    type: xml
    data: '<Envelope><login apiKey="{{bodyValue}}"/><password>{{bodyValue}}</password></Envelope>'
`),
		},
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}

	body := result.Scripts[0].Script.Actions[0].Body
	if strings.Contains(body, "{{param.bodyValue}}") || strings.Contains(body, sourceCredential) {
		t.Fatalf("sensitive XML template was projected as an ordinary value: %q", body)
	}
	if strings.Count(body, "{{secret.") != 2 {
		t.Fatalf("sensitive XML contexts were not governed: %q", body)
	}
	foundMigratableValue := false
	for _, requirement := range result.SecretRequirements {
		if requirement.Value == sourceCredential &&
			strings.HasPrefix(requirement.Key, nativeVariableRequirementKeyPrefix) {
			foundMigratableValue = true
		}
	}
	if !foundMigratableValue {
		t.Fatalf("XML credential variable requirement not found: %#v", result.SecretRequirements)
	}
	sanitized := tarFile(t, result.SanitizedArchive, "demo/request.yml")
	for _, leaked := range []string{sourceCredential, `apiKey="{{bodyValue}}"`, `<password>{{bodyValue}}</password>`} {
		if bytes.Contains(sanitized, []byte(leaked)) {
			t.Fatalf("sanitized XML source retained %q:\n%s", leaked, sanitized)
		}
	}
}

func TestInspectNativeRejectsSensitiveXMLMixedContent(t *testing.T) {
	_, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Mixed XML, uid: mixed-xml, type: http}
http:
  method: POST
  url: https://example.test
  body:
    type: xml
    data: '<Envelope><password>cleartext<suffix/></password></Envelope>'
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err == nil || !strings.Contains(err.Error(), `sensitive XML element "password" contains unsupported child markup`) {
		t.Fatalf("mixed-content XML error = %v", err)
	}
}

func TestInspectNativePreservesCredentialMarkupInRunnableDraft(t *testing.T) {
	const sourceCredential = "xml-markup-credential-canary-5f72"
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Markup XML, uid: markup-xml, type: http}
http:
  method: POST
  url: https://example.test
  body:
    type: xml
    data: '<Envelope><![CDATA[password=` + sourceCredential + `]]></Envelope>'
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := result.Scripts[0]
	action := candidate.Script.Actions[0]
	if strings.Contains(action.Body, sourceCredential) ||
		len(candidate.SecretRequirements) != 1 ||
		candidate.SecretRequirements[0].Value != `<Envelope><![CDATA[password=`+sourceCredential+`]]></Envelope>` ||
		action.Body != candidate.SecretRequirements[0].Replacement ||
		strings.Contains(action.Body, nativeXMLReviewMarker) ||
		hasNativeDiagnostic(candidate.Diagnostics, "bruno_xml_unsupported_markup_review_required") ||
		action.Metadata["review_gate"] != nil {
		t.Fatalf("credential-bearing XML markup was not reversibly protected for commit: %#v", candidate)
	}
	if bytes.Contains(tarFile(t, result.SanitizedArchive, "request.yml"), []byte(sourceCredential)) {
		t.Fatal("sanitized XML source retained credential-bearing markup")
	}
}

func TestInspectNativeResolvesAndScrubsComposedXMLCredentialDependencies(t *testing.T) {
	const sourceCredential = "nested-source-password"
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Composed XML, uid: composed-xml, type: http}
variables:
  - {name: inner, value: ` + sourceCredential + `}
  - {name: outer, value: "prefix-{{inner}}"}
http:
  method: POST
  url: https://example.test
  body:
    type: xml
    data: '<Envelope><userPassword>{{outer}}</userPassword></Envelope>'
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := result.Scripts[0]
	body := candidate.Script.Actions[0].Body
	if strings.Contains(body, "prefix-") || strings.Contains(body, "{{outer}}") ||
		strings.Count(body, "{{secret.") != 1 {
		t.Fatalf("composed XML credential body = %q", body)
	}
	if len(candidate.SecretRequirements) != 1 ||
		candidate.SecretRequirements[0].Value != "prefix-"+sourceCredential {
		t.Fatalf("composed XML requirements = %#v", candidate.SecretRequirements)
	}
	sanitized := tarFile(t, result.SanitizedArchive, "request.yml")
	for _, leaked := range []string{
		sourceCredential,
		"prefix-{{inner}}",
		"<userPassword>{{outer}}</userPassword>",
	} {
		if bytes.Contains(sanitized, []byte(leaked)) {
			t.Fatalf("sanitized source retained %q:\n%s", leaked, sanitized)
		}
	}
}

func TestInspectNativeSeparatesComposedXMLCredentialRequirements(t *testing.T) {
	const sourcePart = "shared-xml-part"
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Reused XML part, uid: reused-xml-part, type: http}
variables:
  - {name: part, value: ` + sourcePart + `}
http:
  method: POST
  url: https://example.test
  body:
    type: xml
    data: '<Envelope><password>A-{{part}}</password><token>B-{{part}}</token></Envelope>'
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := result.Scripts[0]
	body := candidate.Script.Actions[0].Body
	if strings.Count(body, "{{secret.") != 2 {
		t.Fatalf("composed XML body = %s", body)
	}
	values := map[string]bool{}
	replacements := map[string]bool{}
	for _, requirement := range candidate.SecretRequirements {
		values[requirement.Value] = true
		replacements[requirement.Replacement] = true
	}
	if len(candidate.SecretRequirements) != 2 ||
		len(replacements) != 2 ||
		!values["A-"+sourcePart] ||
		!values["B-"+sourcePart] {
		t.Fatalf("composed XML requirements = %#v", candidate.SecretRequirements)
	}
	sanitized := tarFile(t, result.SanitizedArchive, "request.yml")
	for _, leaked := range []string{sourcePart, "A-{{part}}", "B-{{part}}"} {
		if bytes.Contains(sanitized, []byte(leaked)) {
			t.Fatalf("sanitized shared XML dependency retained %q:\n%s", leaked, sanitized)
		}
	}
}

func TestInspectNativeConvertsNamespacedSensitiveXMLTemplateToRequirement(t *testing.T) {
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Namespaced XML, uid: namespaced-xml, type: http}
http:
  method: POST
  url: https://example.test
  body:
    type: xml
    data: '<Envelope><password>{{var.password}}</password></Envelope>'
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := result.Scripts[0]
	if strings.Contains(candidate.Script.Actions[0].Body, "{{var.password}}") ||
		strings.Count(candidate.Script.Actions[0].Body, "{{secret.") != 1 ||
		len(candidate.SecretRequirements) != 1 ||
		candidate.SecretRequirements[0].SourceValueAvailable ||
		len(candidate.MissingVariables) != 0 {
		t.Fatalf("namespaced XML candidate = %#v", candidate)
	}
}

func TestInspectNativeAllowsSensitiveXMLCredentialContainer(t *testing.T) {
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Credential container, uid: credential-container, type: http}
http:
  method: POST
  url: https://example.test
  body:
    type: xml
    data: '<Envelope><auth><password>container-secret</password></auth></Envelope>'
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	// A literal the operator authored into the XML body is preserved. It used to
	// be rewritten to {{secret.NAME}}, which left a stored bundle and an exported
	// script that could not replay until the operator bound a value they never
	// asked to externalize.
	body := result.Scripts[0].Script.Actions[0].Body
	if body != `<Envelope><auth><password>container-secret</password></auth></Envelope>` {
		t.Fatalf("credential container body = %q, want the exact authored XML", body)
	}
	if strings.Contains(body, "{{secret.") {
		t.Fatalf("credential container body = %q, want no import-time secret reference", body)
	}
}

func TestInspectNativeGovernsUnknownFieldsInsideXMLCredentialContainer(t *testing.T) {
	const sourceCredential = "container-value-secret"
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Credential container value, uid: credential-container-value, type: http}
http:
  method: POST
  url: https://example.test
  body:
    type: xml
    data: '<Envelope><auth><value>` + sourceCredential + `</value></auth></Envelope>'
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := result.Scripts[0]
	// The descendant of a credential container is still recognized, but a
	// literal one is now preserved rather than externalized: the operator has
	// the source, and an unbound {{secret.NAME}} would make the draft unrunnable.
	if !strings.Contains(candidate.Script.Actions[0].Body, sourceCredential) ||
		strings.Contains(candidate.Script.Actions[0].Body, "{{secret.") {
		t.Fatalf("credential container body = %q, want the exact authored value",
			candidate.Script.Actions[0].Body)
	}
	if !bytes.Contains(tarFile(t, result.SanitizedArchive, "request.yml"), []byte(sourceCredential)) {
		t.Fatal("sanitized XML lost the authored credential-container descendant")
	}
}

func TestInspectNativeRecognizesCredentialSuffixedXMLName(t *testing.T) {
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Suffixed credential, uid: suffixed-credential, type: http}
http:
  method: POST
  url: https://example.test
  body:
    type: xml
    data: '<Envelope><userPassword>literal-user-password</userPassword></Envelope>'
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	// The credential-suffixed element name is still recognized by the scanner —
	// which is what keeps its value out of ordinary parameterization — but the
	// literal itself is preserved so the imported request stays runnable.
	if body := result.Scripts[0].Script.Actions[0].Body; !strings.Contains(body, "literal-user-password") ||
		strings.Contains(body, "{{secret.") {
		t.Fatalf("credential-suffixed XML lost its authored value: %q", body)
	}
	if len(result.Scripts[0].SecretRequirements) != 0 {
		t.Fatalf("secret requirements = %#v, want no re-entry binding for a preserved literal",
			result.Scripts[0].SecretRequirements)
	}
}

func TestInspectNativeSanitizesCredentialsInsideRawJSONStringBody(t *testing.T) {
	const (
		username = "literal-json-user"
		password = "literal-json-password"
	)
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: JSON credentials, uid: json-credentials, type: http}
http:
  method: POST
  url: https://example.test
  body:
    type: json
    data: '{"username":"` + username + `","nested":{"password":"` + password + `"},"safe":"retained"}'
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	body := result.Scripts[0].Script.Actions[0].Body
	for _, leaked := range []string{username, password} {
		if strings.Contains(body, leaked) {
			t.Fatalf("native JSON body retained %q: %s", leaked, body)
		}
	}
	if strings.Count(body, "{{secret.") != 2 || !strings.Contains(body, `"safe":"retained"`) {
		t.Fatalf("native JSON body = %s", body)
	}
	values := map[string]bool{}
	for _, requirement := range result.Scripts[0].SecretRequirements {
		values[requirement.Value] = true
	}
	if !values[username] || !values[password] {
		t.Fatalf("JSON credential requirements = %#v", result.Scripts[0].SecretRequirements)
	}
	sanitized := tarFile(t, result.SanitizedArchive, "request.yml")
	for _, leaked := range []string{username, password} {
		if bytes.Contains(sanitized, []byte(leaked)) {
			t.Fatalf("sanitized JSON source retained %q:\n%s", leaked, sanitized)
		}
	}
}

func TestInspectNativeGovernsTemplatedJSONCredential(t *testing.T) {
	const sourceCredential = "templated-json-password"
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: JSON template, uid: json-template, type: http}
variables:
  - {name: passwordValue, value: ` + sourceCredential + `}
http:
  method: POST
  url: https://example.test
  body:
    type: json
    data: '{"credentials":{"password":"prefix-{{passwordValue}}"}}'
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := result.Scripts[0]
	body := candidate.Script.Actions[0].Body
	if strings.Contains(body, sourceCredential) || strings.Contains(body, "prefix-") ||
		strings.Count(body, "{{secret.") != 1 {
		t.Fatalf("templated JSON body = %s", body)
	}
	if len(candidate.MissingVariables) != 0 ||
		len(candidate.SecretRequirements) != 1 ||
		candidate.SecretRequirements[0].Value != "prefix-"+sourceCredential {
		t.Fatalf("templated JSON candidate = %#v", candidate)
	}
	sanitized := tarFile(t, result.SanitizedArchive, "request.yml")
	for _, leaked := range []string{sourceCredential, "prefix-{{passwordValue}}"} {
		if bytes.Contains(sanitized, []byte(leaked)) {
			t.Fatalf("sanitized JSON source retained %q:\n%s", leaked, sanitized)
		}
	}
	if !hasNativeDiagnostic(candidate.Diagnostics, "bruno_json_secret_escaping_review_required") ||
		candidate.Script.Actions[0].Metadata["promotion_blocker"] != true {
		t.Fatalf("governed JSON credential was not escaping-gated: %#v", candidate)
	}
}

func TestInspectNativeSeparatesComposedJSONCredentialRequirements(t *testing.T) {
	const sourcePart = "shared-json-part"
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Reused JSON part, uid: reused-json-part, type: http}
variables:
  - {name: part, value: ` + sourcePart + `}
http:
  method: POST
  url: https://example.test
  body:
    type: json
    data: '{"apiKey":"A-{{part}}","clientSecret":"B-{{part}}"}'
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := result.Scripts[0]
	body := candidate.Script.Actions[0].Body
	if strings.Count(body, "{{secret.") != 2 {
		t.Fatalf("composed JSON body = %s", body)
	}
	values := map[string]bool{}
	replacements := map[string]bool{}
	for _, requirement := range candidate.SecretRequirements {
		values[requirement.Value] = true
		replacements[requirement.Replacement] = true
	}
	if len(candidate.SecretRequirements) != 2 ||
		len(replacements) != 2 ||
		!values["A-"+sourcePart] ||
		!values["B-"+sourcePart] {
		t.Fatalf("composed JSON requirements = %#v", candidate.SecretRequirements)
	}
	sanitized := tarFile(t, result.SanitizedArchive, "request.yml")
	for _, leaked := range []string{sourcePart, "A-{{part}}", "B-{{part}}"} {
		if bytes.Contains(sanitized, []byte(leaked)) {
			t.Fatalf("sanitized shared JSON dependency retained %q:\n%s", leaked, sanitized)
		}
	}
}

func TestInspectNativeGatesJSONSecretsThatRequireRuntimeEscaping(t *testing.T) {
	const encodedCredential = `quote\"-slash\\-line\nend`
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Escaped JSON secret, uid: escaped-json-secret, type: http}
http:
  method: POST
  url: https://example.test
  body:
    type: json
    data: '{"password":"` + encodedCredential + `"}'
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := result.Scripts[0]
	if !hasNativeDiagnostic(candidate.Diagnostics, "bruno_json_secret_escaping_review_required") ||
		candidate.Script.Actions[0].Metadata["review_gate"] !=
			"bruno_json_secret_escaping_review_required" {
		t.Fatalf("escaped JSON secret was not review-gated: %#v", candidate)
	}
	if len(candidate.SecretRequirements) != 1 ||
		candidate.SecretRequirements[0].Value != "quote\"-slash\\-line\nend" {
		t.Fatalf("escaped JSON requirement = %#v", candidate.SecretRequirements)
	}
}

func TestInspectNativeGatesSecretsIntroducedByGeneralJSONTemplateRewrite(t *testing.T) {
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: JSON named secret, uid: json-named-secret, type: http}
variables:
  - {name: password, value: 'p"ass'}
http:
  method: POST
  url: https://example.test
  body:
    type: json
    data: '{"value":"{{password}}"}'
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := result.Scripts[0]
	if !strings.Contains(candidate.Script.Actions[0].Body, "{{secret.") ||
		!hasNativeDiagnostic(candidate.Diagnostics, "bruno_json_secret_escaping_review_required") ||
		candidate.Script.Actions[0].Metadata["promotion_blocker"] != true {
		t.Fatalf("general JSON secret rewrite was not escaping-gated: %#v", candidate)
	}
}

func TestInspectNativeGovernsFormAndMultipartCredentialTemplates(t *testing.T) {
	const sourcePart = "body-field-secret-part"
	tests := []struct {
		name       string
		bodyType   string
		value      string
		wantValue  string
		diagnostic string
	}{
		{
			name:       "form exact",
			bodyType:   "form-urlencoded",
			value:      "{{neutralValue}}",
			wantValue:  sourcePart,
			diagnostic: "bruno_form_template_review_required",
		},
		{
			name:       "form composed",
			bodyType:   "form-urlencoded",
			value:      "prefix-{{neutralValue}}",
			wantValue:  "prefix-" + sourcePart,
			diagnostic: "bruno_form_template_review_required",
		},
		{
			name:       "multipart exact",
			bodyType:   "multipart-form",
			value:      "{{neutralValue}}",
			wantValue:  sourcePart,
			diagnostic: "bruno_multipart_template_review_required",
		},
		{
			name:       "multipart composed",
			bodyType:   "multipart-form",
			value:      "prefix-{{neutralValue}}",
			wantValue:  "prefix-" + sourcePart,
			diagnostic: "bruno_multipart_template_review_required",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := Inspect(Input{
				Filename: "request.yml",
				Content: []byte(`info: {name: Body field secret, uid: body-field-secret, type: http}
variables:
  - {name: neutralValue, value: ` + sourcePart + `}
http:
  method: POST
  url: https://example.test
  body:
    type: ` + test.bodyType + `
    data:
      - {name: password, value: "` + test.value + `"}
`),
				Options: InspectOptions{OutputMode: OutputModeNativeRequests},
			})
			if err != nil {
				t.Fatal(err)
			}
			candidate := result.Scripts[0]
			body := candidate.Script.Actions[0].Body
			if strings.Contains(body, sourcePart) ||
				strings.Contains(body, "prefix-{{neutralValue}}") ||
				!strings.Contains(body, "{{secret.") ||
				len(candidate.SecretRequirements) != 1 ||
				candidate.SecretRequirements[0].Value != test.wantValue ||
				!hasNativeDiagnostic(candidate.Diagnostics, test.diagnostic) {
				t.Fatalf("%s candidate = %#v", test.name, candidate)
			}
			sanitized := tarFile(t, result.SanitizedArchive, "request.yml")
			for _, leaked := range []string{sourcePart, test.value} {
				if bytes.Contains(sanitized, []byte(leaked)) {
					t.Fatalf("sanitized %s retained %q:\n%s", test.name, leaked, sanitized)
				}
			}
		})
	}
}

func TestInspectNativeGovernsGraphQLCredentialTemplates(t *testing.T) {
	const sourceCredential = "graphql-neutral-secret"
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: GraphQL credential, uid: graphql-credential, type: http}
variables:
  - {name: neutralValue, value: ` + sourceCredential + `}
http:
  method: POST
  url: https://example.test/graphql
  body:
    type: graphql
    data:
      query: "mutation Login { login }"
      variables:
        password: "prefix-{{neutralValue}}"
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := result.Scripts[0]
	if strings.Contains(candidate.Script.Actions[0].Body, sourceCredential) ||
		strings.Contains(candidate.Script.Actions[0].Body, "prefix-{{neutralValue}}") ||
		!strings.Contains(candidate.Script.Actions[0].Body, "{{secret.") ||
		len(candidate.SecretRequirements) != 1 ||
		candidate.SecretRequirements[0].Value != "prefix-"+sourceCredential ||
		!hasNativeDiagnostic(candidate.Diagnostics, "bruno_graphql_body_review_required") {
		t.Fatalf("GraphQL credential candidate = %#v", candidate)
	}
	if bytes.Contains(tarFile(t, result.SanitizedArchive, "request.yml"), []byte(sourceCredential)) {
		t.Fatal("sanitized GraphQL source retained a credential dependency")
	}
}

func TestInspectNativeContentSniffsMislabeledJSONCredentials(t *testing.T) {
	const sourceCredential = "mislabeled-json-secret"
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Mislabeled JSON, uid: mislabeled-json, type: http}
http:
  method: POST
  url: https://example.test
  body:
    type: text
    data: '{"password":"` + sourceCredential + `"}'
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := result.Scripts[0]
	if strings.Contains(candidate.Script.Actions[0].Body, sourceCredential) ||
		!strings.Contains(candidate.Script.Actions[0].Body, "{{secret.") ||
		len(candidate.SecretRequirements) != 1 ||
		candidate.SecretRequirements[0].Value != sourceCredential ||
		!hasNativeDiagnostic(candidate.Diagnostics, "bruno_json_secret_escaping_review_required") {
		t.Fatalf("mislabeled JSON candidate = %#v", candidate)
	}
	if bytes.Contains(tarFile(t, result.SanitizedArchive, "request.yml"), []byte(sourceCredential)) {
		t.Fatal("sanitized mislabeled JSON retained a credential")
	}
}

func TestInspectNativeContentSniffsScalarJSONBodies(t *testing.T) {
	const sourceCredential = "scalar-json-source-secret"
	for _, test := range []struct {
		name      string
		variables string
		body      string
	}{
		{
			name: "literal",
			body: `{"password":"` + sourceCredential + `"}`,
		},
		{
			name:      "template",
			variables: "variables:\n  - {name: neutralValue, value: " + sourceCredential + "}\n",
			body:      `{"password":"prefix-{{neutralValue}}"}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := Inspect(Input{
				Filename: "request.yml",
				Content: []byte(`info: {name: Scalar JSON, uid: scalar-json, type: http}
` + test.variables + `
http:
  method: POST
  url: https://example.test
  body: '` + test.body + `'
`),
				Options: InspectOptions{OutputMode: OutputModeNativeRequests},
			})
			if err != nil {
				t.Fatal(err)
			}
			candidate := result.Scripts[0]
			if strings.Contains(candidate.Script.Actions[0].Body, sourceCredential) ||
				!strings.Contains(candidate.Script.Actions[0].Body, "{{secret.") ||
				len(candidate.SecretRequirements) != 1 {
				t.Fatalf("scalar JSON candidate = %#v", candidate)
			}
			if bytes.Contains(tarFile(t, result.SanitizedArchive, "request.yml"), []byte(sourceCredential)) {
				t.Fatal("sanitized scalar JSON retained a credential")
			}
		})
	}
}

func TestInspectNativeRejectsCompositeRawJSONCredentialValues(t *testing.T) {
	for _, body := range []string{
		`{"token":["secret-one"]}`,
		`{"apiKey":{"value":"secret-two"}}`,
	} {
		t.Run(body, func(t *testing.T) {
			_, err := Inspect(Input{
				Filename: "request.yml",
				Content: []byte(`info: {name: Composite JSON, uid: composite-json, type: http}
http:
  method: POST
  url: https://example.test
  body:
    type: json
    data: '` + body + `'
`),
				Options: InspectOptions{OutputMode: OutputModeNativeRequests},
			})
			if err == nil || !strings.Contains(err.Error(), "composite JSON credential field") {
				t.Fatalf("composite JSON error = %v", err)
			}
		})
	}
}

func TestInspectNativeRejectsMalformedRawJSONStringBody(t *testing.T) {
	_, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Bad JSON, uid: bad-json, type: http}
http:
  method: POST
  url: https://example.test
  body:
    type: json
    data: '{"password":"unterminated}'
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err == nil || !strings.Contains(err.Error(), "JSON could not be safely parsed") {
		t.Fatalf("malformed JSON error = %v", err)
	}
}

func TestInspectNativeRejectsDuplicateRawJSONKeys(t *testing.T) {
	_, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Duplicate JSON, uid: duplicate-json, type: http}
http:
  method: POST
  url: https://example.test
  body:
    type: json
    data: '{"password":"literal-secret","password":"{{secret.EXISTING}}"}'
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err == nil || !strings.Contains(err.Error(), `duplicate key "password"`) {
		t.Fatalf("duplicate JSON error = %v", err)
	}
}

func TestInspectNativeRejectsNumericRawJSONCredentialWithoutChangingWireType(t *testing.T) {
	_, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Numeric JSON, uid: numeric-json, type: http}
http:
  method: POST
  url: https://example.test
  body:
    type: json
    data: '{"apiKey":123456789,"count":42}'
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err == nil ||
		!strings.Contains(err.Error(), "cannot be governed without changing its wire type") {
		t.Fatalf("numeric JSON credential error = %v", err)
	}
}

func TestInspectNativeRejectsNonStringStructuredJSONCredentialWithoutChangingWireType(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{name: "number", value: "123456789"},
		{name: "boolean", value: "true"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Inspect(Input{
				Filename: "request.yml",
				Content: []byte(`info: {name: Structured JSON, uid: structured-json, type: http}
http:
  method: POST
  url: https://example.test
  body:
    type: json
    data:
      apiKey: ` + test.value + `
      count: 42
`),
				Options: InspectOptions{OutputMode: OutputModeNativeRequests},
			})
			if err == nil ||
				!strings.Contains(err.Error(), "cannot be governed without changing its wire type") {
				t.Fatalf("structured JSON credential error = %v", err)
			}
		})
	}
}

func TestInspectNativeKeepsInheritedComposedAuthCandidateScoped(t *testing.T) {
	result, err := Inspect(Input{
		Files: map[string][]byte{
			"demo/opencollection.yml": []byte(`opencollection: 1.0.0
info: {name: Scoped inherited auth}
`),
			"demo/shared/folder.yml": []byte(`info: {name: Shared, type: folder, seq: 1}
request:
  variables:
    - {name: passwordValue, value: "prefix-{{suffix}}"}
  auth:
    type: basic
    username: ""
    password: "{{passwordValue}}"
`),
			"demo/shared/a.yml": []byte(`info: {name: A, uid: scoped-auth-a, type: http, seq: 1}
variables:
  - {name: suffix, value: alpha}
http:
  method: GET
  url: https://example.test/a
`),
			"demo/shared/b.yml": []byte(`info: {name: B, uid: scoped-auth-b, type: http, seq: 2}
variables:
  - {name: suffix, value: beta}
http:
  method: GET
  url: https://example.test/b
`),
		},
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Scripts) != 2 {
		t.Fatalf("candidate count = %d", len(result.Scripts))
	}
	passwords := make(map[string]string)
	for _, candidate := range result.Scripts {
		auth := candidate.Script.Actions[0].Auth
		if auth == nil || !strings.HasPrefix(auth.Password, "{{secret.") {
			t.Fatalf("%s auth = %#v", candidate.SourcePath, auth)
		}
		if _, duplicate := passwords[auth.Password]; duplicate {
			t.Fatalf("candidate-scoped inherited auth reused %q", auth.Password)
		}
		if len(candidate.SecretRequirements) != 1 {
			t.Fatalf("%s requirements = %#v", candidate.SourcePath, candidate.SecretRequirements)
		}
		passwords[auth.Password] = candidate.SecretRequirements[0].Value
	}
	values := make(map[string]bool)
	for _, value := range passwords {
		values[value] = true
	}
	if !values["prefix-alpha"] || !values["prefix-beta"] {
		t.Fatalf("candidate-scoped auth values = %#v", passwords)
	}
	sanitizedFolder := tarFile(t, result.SanitizedArchive, "demo/shared/folder.yml")
	if bytes.Contains(sanitizedFolder, []byte("prefix-{{suffix}}")) ||
		bytes.Contains(sanitizedFolder, []byte(`password: "{{passwordValue}}"`)) {
		t.Fatalf("sanitized inherited auth retained credential templates:\n%s", sanitizedFolder)
	}
}

func TestInspectNativeSeparatesResolvedAndUnresolvedInheritedCredentials(t *testing.T) {
	result, err := Inspect(Input{
		Files: map[string][]byte{
			"demo/opencollection.yml": []byte(`opencollection: 1.0.0
info: {name: Partially resolved inherited auth}
`),
			"demo/shared/folder.yml": []byte(`info: {name: Shared, type: folder, seq: 1}
request:
  variables:
    - {name: passwordValue, value: "prefix-{{suffix}}"}
  auth:
    type: basic
    username: ""
    password: "{{passwordValue}}"
`),
			"demo/shared/a.yml": []byte(`info: {name: Resolved, uid: partial-auth-a, type: http, seq: 1}
variables:
  - {name: suffix, value: resolved}
http: {method: GET, url: https://example.test/a}
`),
			"demo/shared/b.yml": []byte(`info: {name: Unresolved, uid: partial-auth-b, type: http, seq: 2}
http: {method: GET, url: https://example.test/b}
`),
		},
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Scripts) != 2 {
		t.Fatalf("candidate count = %d", len(result.Scripts))
	}
	first := result.Scripts[0]
	second := result.Scripts[1]
	if first.Script.Actions[0].Auth == nil || second.Script.Actions[0].Auth == nil ||
		first.Script.Actions[0].Auth.Password == second.Script.Actions[0].Auth.Password {
		t.Fatalf("resolved and unresolved auth requirements collided: %#v %#v",
			first.Script.Actions[0].Auth,
			second.Script.Actions[0].Auth,
		)
	}
	values := []string{
		first.SecretRequirements[0].Value,
		second.SecretRequirements[0].Value,
	}
	if !((values[0] == "prefix-resolved" && values[1] == "") ||
		(values[1] == "prefix-resolved" && values[0] == "")) {
		t.Fatalf("resolved/unresolved requirements = %#v", result.SecretRequirements)
	}
}

func TestInspectNativeMergesSharedCredentialUsageScrubTargets(t *testing.T) {
	result, err := Inspect(Input{
		Files: map[string][]byte{
			"demo/opencollection.yml": []byte(`opencollection: 1.0.0
info: {name: Shared XML credential}
variables:
  - {name: bodyValue, value: shared-xml-secret}
`),
			"demo/a.yml": []byte(`info: {name: A, uid: shared-xml-a, type: http, seq: 1}
http:
  method: POST
  url: https://example.test/a
  body: {type: xml, data: '<Envelope><password>{{bodyValue}}</password></Envelope>'}
`),
			"demo/b.yml": []byte(`info: {name: B, uid: shared-xml-b, type: http, seq: 2}
http:
  method: POST
  url: https://example.test/b
  body: {type: xml, data: '<Envelope><password>{{bodyValue}}</password></Envelope>'}
`),
		},
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.SecretRequirements) != 1 ||
		result.SecretRequirements[0].Value != "shared-xml-secret" {
		t.Fatalf("shared requirement = %#v", result.SecretRequirements)
	}
	var replacement string
	for _, candidate := range result.Scripts {
		body := candidate.Script.Actions[0].Body
		if strings.Contains(body, "{{bodyValue}}") || strings.Count(body, "{{secret.") != 1 {
			t.Fatalf("%s body = %q", candidate.SourcePath, body)
		}
		current := candidate.SecretRequirements[0].Replacement
		if replacement != "" && replacement != current {
			t.Fatalf("shared definition produced %q and %q", replacement, current)
		}
		replacement = current
		sanitized := tarFile(t, result.SanitizedArchive, "demo/"+candidate.SourcePath)
		if bytes.Contains(sanitized, []byte("{{bodyValue}}")) {
			t.Fatalf("sanitized %s retained usage:\n%s", candidate.SourcePath, sanitized)
		}
	}
}

func TestInspectNativeRejectsNonStringCredentialScalars(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name: "policy header",
			content: `info: {name: Numeric signature, uid: numeric-signature, type: http}
http:
  method: GET
  url: https://example.test
  headers:
    - {name: X-Signature, value: 123456789}
`,
			want: "sensitive header",
		},
		{
			name: "query parameter",
			content: `info: {name: Numeric token, uid: numeric-token, type: http}
http:
  method: GET
  url: https://example.test
  params:
    - {name: token, type: query, value: 123456789}
`,
			want: "sensitive URL parameter",
		},
		{
			name: "path parameter",
			content: `info: {name: Numeric password, uid: numeric-password, type: http}
http:
  method: GET
  url: https://example.test/:password
  params:
    - {name: password, type: path, value: 123456789}
`,
			want: "sensitive URL parameter",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Inspect(Input{
				Filename: "request.yml",
				Content:  []byte(test.content),
				Options:  InspectOptions{OutputMode: OutputModeNativeRequests},
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestInspectNativeRejectsMalformedLayerShapes(t *testing.T) {
	tests := []struct {
		name  string
		field string
		want  string
	}{
		{"timeout", "settings: {timeout: forever}", "settings.timeout must be an integer"},
		{"redirect bool", "settings: {followRedirects: sometimes}", "settings.followRedirects must be a boolean"},
		{"headers container", "http:\n  method: GET\n  url: https://example.test\n  headers: nope", "http.headers must be an array or object"},
		{"header entry", "http:\n  method: GET\n  url: https://example.test\n  headers: [nope]", "http.headers[0] must be an object"},
		{"variables container", "variables: nope", "variables must be an array or object"},
		{"variable entry", "variables: [nope]", "variables[0] must be an object"},
		{"hook list", "runtime: {scripts: {type: after-response, code: nope}}", "runtime.scripts must be an array"},
		{"http container", "http: nope", "http must be an object"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			content := "info: {name: Malformed, uid: malformed, type: http}\n" + test.field + "\n"
			if !strings.Contains(test.field, "http:") {
				content += "http: {method: GET, url: https://example.test}\n"
			}
			_, err := Inspect(Input{
				Filename: "request.yml",
				Content:  []byte(content),
				Options:  InspectOptions{OutputMode: OutputModeNativeRequests},
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestInspectNativeRejectsAmbiguousMappingAuth(t *testing.T) {
	tests := []struct {
		name string
		auth string
	}{
		{
			name: "missing type",
			auth: `auth:
    username: authored-user
    password: authored-password`,
		},
		{
			name: "misspelled type",
			auth: `auth:
    typ: basic
    username: authored-user
    password: authored-password`,
		},
		{
			name: "empty type",
			auth: `auth:
    type: ""
    username: authored-user
    password: authored-password`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Inspect(Input{
				Filename: "request.yml",
				Content: []byte(`info: {name: Ambiguous auth, uid: ambiguous-auth, type: http}
http:
  method: GET
  url: https://example.test
  ` + test.auth + "\n"),
				Options: InspectOptions{OutputMode: OutputModeNativeRequests},
			})
			if err == nil ||
				(!strings.Contains(err.Error(), "auth.type is required") &&
					!strings.Contains(err.Error(), "auth.type must be a non-empty string")) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestInspectNativeRejectsTemplatedHeaderNames(t *testing.T) {
	tests := []struct {
		name      string
		variables string
		auth      string
	}{
		{
			name:      "known name",
			variables: "variables:\n  - {name: headerName, value: X-Known}\n",
		},
		{
			name: "unresolved name",
		},
		{
			name:      "basic authorization collision",
			variables: "variables:\n  - {name: headerName, value: Authorization}\n",
			auth:      "  auth: {type: basic, username: user, password: pass}\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Inspect(Input{
				Filename: "request.yml",
				Content: []byte(`info: {name: Dynamic header, uid: dynamic-header, type: http}
` + test.variables + `http:
  method: GET
  url: https://example.test
  headers:
    - {name: "{{headerName}}", value: authored}
` + test.auth),
				Options: InspectOptions{OutputMode: OutputModeNativeRequests},
			})
			if err == nil || !strings.Contains(err.Error(), "name cannot contain templates") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestInspectNativeSecretReplacementDropsIrrelevantMissingDependencies(t *testing.T) {
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Missing credential dependency, uid: missing-credential, type: http}
variables:
  - {name: opaque, value: "prefix-{{undefinedPart}}"}
http:
  method: GET
  url: https://example.test
  headers:
    - {name: X-API-Key, value: "{{opaque}}"}
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := result.Scripts[0]
	if len(candidate.MissingVariables) != 0 {
		t.Fatalf("irrelevant missing variables = %#v", candidate.MissingVariables)
	}
	action := candidate.Script.Actions[0]
	if action.Metadata["execution_blocker"] != nil ||
		!strings.HasPrefix(action.Headers["X-API-Key"], "{{secret.") {
		t.Fatalf("governed action = %#v", action)
	}
	if len(candidate.SecretRequirements) != 1 ||
		candidate.SecretRequirements[0].Value != "" {
		t.Fatalf("governed requirement = %#v", candidate.SecretRequirements)
	}
}

func TestInspectNativeScrubsUnresolvedComposedCredentialWithoutMigrationValue(t *testing.T) {
	const literalFragment = "literal-credential-prefix"
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Unresolved composed credential, uid: unresolved-composed, type: http}
http:
  method: GET
  url: https://example.test
  headers:
    - {name: X-API-Key, value: "` + literalFragment + `-{{missingPart}}"}
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := result.Scripts[0]
	if len(candidate.SecretRequirements) != 1 ||
		candidate.SecretRequirements[0].Value != "" ||
		!strings.HasPrefix(candidate.Script.Actions[0].Headers["X-API-Key"], "{{secret.") {
		t.Fatalf("unresolved composed credential = %#v", candidate)
	}
	sanitized := tarFile(t, result.SanitizedArchive, "request.yml")
	if bytes.Contains(sanitized, []byte(literalFragment)) ||
		bytes.Contains(sanitized, []byte("{{missingPart}}")) {
		t.Fatalf("sanitized source retained an incomplete credential:\n%s", sanitized)
	}
}

func TestInspectNativeGovernsCredentialContainerNameValueBodies(t *testing.T) {
	const sourceCredential = "container-name-value-secret"
	tests := []struct {
		name string
		body string
	}{
		{
			name: "raw JSON string",
			body: `    data: '{"credentials":[{"name":"password","value":"` + sourceCredential + `"}],"safe":"retained"}'`,
		},
		{
			name: "structured JSON",
			body: `    data:
      credentials:
        - name: password
          value: ` + sourceCredential + `
      safe: retained`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := Inspect(Input{
				Filename: "request.yml",
				Content: []byte(`info: {name: Credential container, uid: credential-container, type: http}
http:
  method: POST
  url: https://example.test
  body:
    type: json
` + test.body + "\n"),
				Options: InspectOptions{OutputMode: OutputModeNativeRequests},
			})
			if err != nil {
				t.Fatal(err)
			}
			candidate := result.Scripts[0]
			if strings.Contains(candidate.Script.Actions[0].Body, sourceCredential) ||
				!strings.Contains(candidate.Script.Actions[0].Body, "{{secret.") ||
				!strings.Contains(candidate.Script.Actions[0].Body, `"safe":"retained"`) ||
				len(candidate.SecretRequirements) != 1 ||
				candidate.SecretRequirements[0].Value != sourceCredential {
				t.Fatalf("credential-container candidate = %#v", candidate)
			}
			if bytes.Contains(tarFile(t, result.SanitizedArchive, "request.yml"), []byte(sourceCredential)) {
				t.Fatal("sanitized source retained a credential-container descendant")
			}
		})
	}
}

func TestInspectNativeTreatsSentinelWordsAsLiteralCredentials(t *testing.T) {
	tests := []struct {
		name       string
		source     string
		wantValue  string
		assertWire func(*testing.T, NativeScriptCandidate)
	}{
		{
			name: "basic password none",
			source: `info: {name: Basic sentinel, uid: basic-sentinel, type: http}
http:
  method: GET
  url: https://example.test
  auth: {type: basic, username: "", password: "none"}
`,
			wantValue: "none",
			assertWire: func(t *testing.T, candidate NativeScriptCandidate) {
				t.Helper()
				auth := candidate.Script.Actions[0].Auth
				if auth == nil || !strings.HasPrefix(auth.Password, "{{secret.") {
					t.Fatalf("basic auth = %#v", auth)
				}
			},
		},
		{
			name: "JSON password null",
			source: `info: {name: JSON sentinel, uid: json-sentinel, type: http}
http:
  method: POST
  url: https://example.test
  body:
    type: json
    data: '{"password":"null"}'
`,
			wantValue: "null",
			assertWire: func(t *testing.T, candidate NativeScriptCandidate) {
				t.Helper()
				if !strings.Contains(candidate.Script.Actions[0].Body, "{{secret.") {
					t.Fatalf("JSON body = %q", candidate.Script.Actions[0].Body)
				}
			},
		},
		{
			name: "header inherit",
			source: `info: {name: Header sentinel, uid: header-sentinel, type: http}
http:
  method: GET
  url: https://example.test
  headers:
    - {name: X-API-Key, value: "inherit"}
`,
			wantValue: "inherit",
			assertWire: func(t *testing.T, candidate NativeScriptCandidate) {
				t.Helper()
				if !strings.HasPrefix(candidate.Script.Actions[0].Headers["X-API-Key"], "{{secret.") {
					t.Fatalf("headers = %#v", candidate.Script.Actions[0].Headers)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := Inspect(Input{
				Filename: "request.yml",
				Content:  []byte(test.source),
				Options:  InspectOptions{OutputMode: OutputModeNativeRequests},
			})
			if err != nil {
				t.Fatal(err)
			}
			candidate := result.Scripts[0]
			test.assertWire(t, candidate)
			found := false
			for _, requirement := range candidate.SecretRequirements {
				if requirement.Value == test.wantValue {
					found = true
				}
			}
			if !found {
				t.Fatalf("credential value %q was not governed: %#v", test.wantValue, candidate.SecretRequirements)
			}
		})
	}
}

func TestInspectNativePreservesNamespacedTemplatesThroughVariableChains(t *testing.T) {
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Runtime namespace, uid: runtime-namespace, type: http}
variables:
  - {name: base, value: "{{env.BRUNO_BASE_URL}}"}
  - {name: endpoint, value: "{{base}}/v1"}
http:
  method: GET
  url: "{{endpoint}}/health"
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := result.Scripts[0]
	if candidate.Script.Actions[0].Target != "{{env.BRUNO_BASE_URL}}/v1/health" ||
		len(candidate.Script.Parameters) != 0 ||
		len(candidate.MissingVariables) != 0 {
		t.Fatalf("runtime-bound variable chain = %#v", candidate)
	}
}

func TestInspectNativeScrubsDeferredCredentialYAMLAliasGroups(t *testing.T) {
	tests := []struct {
		name          string
		source        string
		forbiddenText string
	}{
		{
			name: "unresolved composed header alias",
			source: `info: {name: Header alias, uid: header-alias, type: http}
headerSource: &headerSource "Bearer alias-header-literal-{{missingToken}}"
headerMirror: *headerSource
http:
  method: GET
  url: https://example.test
  headers:
    - name: Authorization
      value: *headerSource
`,
			forbiddenText: "alias-header-literal",
		},
		{
			name: "neutral variable dependency alias",
			source: `info: {name: Variable alias, uid: variable-alias, type: http}
partSource: &partSource alias-variable-literal
partMirror: *partSource
variables:
  - name: neutralPart
    value: *partSource
http:
  method: GET
  url: https://example.test
  headers:
    - {name: Authorization, value: "Bearer {{neutralPart}}"}
`,
			forbiddenText: "alias-variable-literal",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := Inspect(Input{
				Filename: "request.yml",
				Content:  []byte(test.source),
				Options:  InspectOptions{OutputMode: OutputModeNativeRequests},
			})
			if err != nil {
				t.Fatal(err)
			}
			candidate := result.Scripts[0]
			if !strings.Contains(candidate.Script.Actions[0].Headers["Authorization"], "{{secret.") ||
				len(candidate.SecretRequirements) != 1 {
				t.Fatalf("alias-backed candidate = %#v", candidate)
			}
			sanitized := tarFile(t, result.SanitizedArchive, "request.yml")
			if bytes.Contains(sanitized, []byte(test.forbiddenText)) {
				t.Fatalf("sanitized alias group retained %q:\n%s", test.forbiddenText, sanitized)
			}
		})
	}
}

func TestInspectNativeLeavesOrdinaryYAMLAliasesUnchanged(t *testing.T) {
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Ordinary alias, uid: ordinary-alias, type: http}
ordinarySource: &ordinarySource ordinary-public-value
ordinaryMirror: *ordinarySource
http:
  method: GET
  url: https://example.test
  headers:
    - name: X-Public
      value: *ordinarySource
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	sanitized := tarFile(t, result.SanitizedArchive, "request.yml")
	for _, expected := range []string{"&ordinarySource", "*ordinarySource", "ordinary-public-value"} {
		if !bytes.Contains(sanitized, []byte(expected)) {
			t.Fatalf("ordinary alias lost %q:\n%s", expected, sanitized)
		}
	}
	if got := result.Scripts[0].Script.Actions[0].Headers["X-Public"]; got != "ordinary-public-value" {
		t.Fatalf("ordinary alias candidate value = %q", got)
	}
}

func TestInspectNativeScrubsSpecializedAndCompositeYAMLAliasPeers(t *testing.T) {
	tests := []struct {
		name      string
		source    string
		forbidden []string
		// preserved lists values the import must retain verbatim in both the
		// sanitized alias peers and the candidate script. Literal XML scalars
		// belong here: nothing an imported script already contains is a project
		// secret, so externalizing one is an explicit operator action rather
		// than the import default.
		preserved []string
	}{
		{
			name: "literal raw JSON scalar alias",
			source: `info: {name: Literal JSON alias, uid: literal-json-alias, type: http}
payloadSource: &payload '{"password":"literal-json-alias-secret"}'
payloadMirror: *payload
http:
  method: POST
  url: https://example.test
  body:
    type: json
    data: *payload
`,
			forbidden: []string{"literal-json-alias-secret"},
		},
		{
			name: "templated raw JSON scalar alias",
			source: `info: {name: Templated JSON alias, uid: templated-json-alias, type: http}
payloadSource: &payload '{"password":"templated-json-alias-secret-{{missingJSONAlias}}"}'
payloadMirror: *payload
http:
  method: POST
  url: https://example.test
  body:
    type: json
    data: *payload
`,
			forbidden: []string{"templated-json-alias-secret", "{{missingJSONAlias}}"},
		},
		{
			name: "literal XML scalar alias",
			source: `info: {name: Literal XML alias, uid: literal-xml-alias, type: http}
payloadSource: &payload '<Envelope><password>literal-xml-alias-secret</password></Envelope>'
payloadMirror: *payload
http:
  method: POST
  url: https://example.test
  body:
    type: xml
    data: *payload
`,
			preserved: []string{"literal-xml-alias-secret"},
		},
		{
			name: "templated XML scalar alias",
			source: `info: {name: Templated XML alias, uid: templated-xml-alias, type: http}
payloadSource: &payload '<Envelope><password>templated-xml-alias-secret-{{missingXMLAlias}}</password></Envelope>'
payloadMirror: *payload
http:
  method: POST
  url: https://example.test
  body:
    type: xml
    data: *payload
`,
			forbidden: []string{"templated-xml-alias-secret", "{{missingXMLAlias}}"},
		},
		{
			name: "literal URL scalar alias",
			source: `info: {name: Literal URL alias, uid: literal-url-alias, type: http}
urlSource: &target 'https://example.test/search?apiKey=literal-url-alias-secret'
urlMirror: *target
http:
  method: GET
  url: *target
`,
			forbidden: []string{"literal-url-alias-secret"},
		},
		{
			name: "templated URL scalar alias",
			source: `info: {name: Templated URL alias, uid: templated-url-alias, type: http}
urlSource: &target 'https://example.test/search?apiKey=templated-url-alias-secret-{{missingURLAlias}}'
urlMirror: *target
http:
  method: GET
  url: *target
`,
			forbidden: []string{"templated-url-alias-secret", "{{missingURLAlias}}"},
		},
		{
			name: "mapping alias descendant",
			source: `info: {name: Mapping alias, uid: mapping-alias, type: http}
payloadSource: &payload
  password: "mapping-alias-secret-{{missingMappingAlias}}"
payloadMirror: *payload
http:
  method: POST
  url: https://example.test
  body:
    type: json
    data: *payload
`,
			forbidden: []string{"mapping-alias-secret", "{{missingMappingAlias}}"},
		},
		{
			name: "sequence alias descendant",
			source: `info: {name: Sequence alias, uid: sequence-alias, type: http}
credentialSource: &credentials
  - {name: password, value: "sequence-alias-secret-{{missingSequenceAlias}}"}
credentialMirror: *credentials
http:
  method: POST
  url: https://example.test
  body:
    type: json
    data:
      credentials: *credentials
`,
			forbidden: []string{"sequence-alias-secret", "{{missingSequenceAlias}}"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := Inspect(Input{
				Filename: "request.yml",
				Content:  []byte(test.source),
				Options:  InspectOptions{OutputMode: OutputModeNativeRequests},
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Scripts) != 1 ||
				(len(test.preserved) == 0 && len(result.Scripts[0].SecretRequirements) == 0) {
				t.Fatalf("alias-backed result = %#v", result.Scripts)
			}
			sanitized := tarFile(t, result.SanitizedArchive, "request.yml")
			candidateJSON, err := json.Marshal(result.Scripts[0].Script)
			if err != nil {
				t.Fatal(err)
			}
			for _, forbidden := range test.forbidden {
				if bytes.Contains(sanitized, []byte(forbidden)) {
					t.Fatalf("sanitized alias peers retained %q:\n%s", forbidden, sanitized)
				}
				if bytes.Contains(candidateJSON, []byte(forbidden)) {
					t.Fatalf("candidate retained alias credential %q:\n%s", forbidden, candidateJSON)
				}
			}
			for _, preserved := range test.preserved {
				if !bytes.Contains(sanitized, []byte(preserved)) {
					t.Fatalf("sanitized alias peers lost preserved value %q:\n%s", preserved, sanitized)
				}
				if !bytes.Contains(candidateJSON, []byte(preserved)) {
					t.Fatalf("candidate lost preserved value %q:\n%s", preserved, candidateJSON)
				}
			}
		})
	}
}

func TestInspectNativeScrubsMixedCaseYAMLAliasPeers(t *testing.T) {
	tests := []struct {
		name          string
		source        string
		forbiddenText string
		wantValue     string
	}{
		{
			name: "literal specialized header",
			source: `info: {name: Mixed header, uid: mixed-header, type: http}
"Credential/Source~A": &credential MiXeD-Literal-Header-Secret
CredentialMirror: *credential
HTTP:
  Method: GET
  URL: https://example.test
  Headers:
    - Name: X-API-Key
      Value: *credential
`,
			forbiddenText: "MiXeD-Literal-Header-Secret",
			wantValue:     "MiXeD-Literal-Header-Secret",
		},
		{
			name: "literal specialized JSON",
			source: `info: {name: Mixed JSON, uid: mixed-json, type: http}
PayloadSource: &payload '{"password":"MiXeD-Literal-JSON-Secret"}'
PayloadMirror: *payload
HTTP:
  Method: POST
  URL: https://example.test
  Body:
    Type: JSON
    Data: *payload
`,
			forbiddenText: "MiXeD-Literal-JSON-Secret",
			wantValue:     "MiXeD-Literal-JSON-Secret",
		},
		{
			name: "templated projected JSON",
			source: `info: {name: Mixed projected JSON, uid: mixed-projected-json, type: http}
PayloadSource: &payload '{"password":"MiXeD-Projected-JSON-{{missingJSONPart}}"}'
PayloadMirror: *payload
HTTP:
  Method: POST
  URL: https://example.test
  Body:
    Type: JSON
    Data: *payload
`,
			forbiddenText: "MiXeD-Projected-JSON-",
		},
		{
			name: "projected variable dependency",
			source: `info: {name: Mixed projected variable, uid: mixed-variable, type: http}
PartSource: &part MiXeD-Projected-Variable-Secret
PartMirror: *part
Variables:
  - Name: neutralPart
    Value: *part
HTTP:
  Method: GET
  URL: https://example.test
  Headers:
    - Name: Authorization
      Value: "Bearer {{neutralPart}}"
`,
			forbiddenText: "MiXeD-Projected-Variable-Secret",
			wantValue:     "MiXeD-Projected-Variable-Secret",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := Inspect(Input{
				Filename: "request.yml",
				Content:  []byte(test.source),
				Options:  InspectOptions{OutputMode: OutputModeNativeRequests},
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Scripts) != 1 ||
				len(result.Scripts[0].SecretRequirements) == 0 {
				t.Fatalf("mixed-case alias result = %#v", result.Scripts)
			}
			sanitized := tarFile(t, result.SanitizedArchive, "request.yml")
			candidateJSON, err := json.Marshal(result.Scripts[0].Script)
			if err != nil {
				t.Fatal(err)
			}
			for artifactName, artifact := range map[string][]byte{
				"sanitized source": sanitized,
				"candidate":        candidateJSON,
			} {
				if bytes.Contains(artifact, []byte(test.forbiddenText)) {
					t.Fatalf("%s retained %q:\n%s", artifactName, test.forbiddenText, artifact)
				}
			}
			if test.wantValue != "" {
				found := false
				for _, requirement := range result.Scripts[0].SecretRequirements {
					if requirement.Value == test.wantValue {
						found = true
					}
				}
				if !found {
					t.Fatalf("case-sensitive source value %q was not preserved: %#v", test.wantValue, result.Scripts[0].SecretRequirements)
				}
			}
		})
	}
}

func TestNativeAliasPointerIdentityFoldsDecodedSegmentsOnly(t *testing.T) {
	tests := map[string]string{
		"/HTTP/Headers/0/Value":     "/http/headers/0/value",
		"/Credential~1Source~0A":    "/credential~1source~0a",
		"/  Variables  /0/  Value ": "/variables/0/value",
	}
	for pointer, want := range tests {
		got, err := nativeAliasPointerIdentity(pointer)
		if err != nil {
			t.Fatalf("identity %q: %v", pointer, err)
		}
		if got != want {
			t.Fatalf("identity %q = %q, want %q", pointer, got, want)
		}
	}
}

func TestNativeYAMLAliasGroupsBoundsExpansion(t *testing.T) {
	var source strings.Builder
	source.WriteString("source: &source bounded-value\naliases:\n")
	for range nativeYAMLAliasMaxExpands + 1 {
		source.WriteString("  - *source\n")
	}
	_, err := nativeYAMLAliasGroups([]byte(source.String()))
	if err == nil || !strings.Contains(err.Error(), "alias expansion exceeds limit") {
		t.Fatalf("alias expansion bound error = %v", err)
	}
}

func TestInspectNativeValidatesStructuredJSONCredentialContainerScalarTypes(t *testing.T) {
	for _, test := range []struct {
		name  string
		field string
		value string
	}{
		{name: "auth number", field: "auth", value: "42"},
		{name: "credentials boolean", field: "credentials", value: "true"},
		{name: "secret number", field: "secret", value: "7"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := Inspect(Input{
				Filename: "request.yml",
				Content: []byte(`info: {name: Scalar container, uid: scalar-container, type: http}
http:
  method: POST
  url: https://example.test
  body:
    type: json
    data:
      ` + test.field + `: ` + test.value + `
`),
				Options: InspectOptions{OutputMode: OutputModeNativeRequests},
			})
			if err == nil ||
				!strings.Contains(err.Error(), "cannot be governed without changing its wire type") {
				t.Fatalf("scalar container error = %v", err)
			}
		})
	}

	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: String container, uid: string-container, type: http}
http:
  method: POST
  url: https://example.test
  body:
    type: json
    data:
      credentials: "literal-container-credential"
      auth: null
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := result.Scripts[0]
	if strings.Contains(candidate.Script.Actions[0].Body, "literal-container-credential") ||
		!strings.Contains(candidate.Script.Actions[0].Body, "{{secret.") ||
		!strings.Contains(candidate.Script.Actions[0].Body, `"auth":null`) {
		t.Fatalf("string/null container body = %q", candidate.Script.Actions[0].Body)
	}
}

func TestInspectNativeSanitizesStringValuedYAMLTags(t *testing.T) {
	tests := []struct {
		name      string
		source    string
		plaintext string
	}{
		{
			name: "custom tagged Basic password",
			source: `info: {name: Custom tag, uid: custom-tag, type: http}
http:
  method: GET
  url: https://example.test
  auth:
    type: basic
    username: ""
    password: !credential tagged-custom-secret
`,
			plaintext: "tagged-custom-secret",
		},
		{
			name: "binary tagged Basic password",
			source: `info: {name: Binary tag, uid: binary-tag, type: http}
http:
  method: GET
  url: https://example.test
  auth:
    type: basic
    username: ""
    password: !!binary dGFnZ2VkLWJpbmFyeS1zZWNyZXQ=
`,
			plaintext: "tagged-binary-secret",
		},
		{
			name: "custom tagged structured JSON password",
			source: `info: {name: JSON custom tag, uid: json-custom-tag, type: http}
http:
  method: POST
  url: https://example.test
  body:
    type: json
    data:
      password: !credential tagged-json-secret
`,
			plaintext: "tagged-json-secret",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := Inspect(Input{
				Filename: "request.yml",
				Content:  []byte(test.source),
				Options:  InspectOptions{OutputMode: OutputModeNativeRequests},
			})
			if err != nil {
				t.Fatal(err)
			}
			candidate := result.Scripts[0]
			foundValue := false
			for _, requirement := range candidate.SecretRequirements {
				if requirement.Value == test.plaintext {
					foundValue = true
				}
			}
			if !foundValue {
				t.Fatalf("decoded tagged credential was not governed: %#v", candidate.SecretRequirements)
			}
			if bytes.Contains(
				tarFile(t, result.SanitizedArchive, "request.yml"),
				[]byte(test.plaintext),
			) {
				t.Fatal("sanitized source retained a tagged credential")
			}
		})
	}
}

func TestInspectNativeCachedResolutionRestoresOrdinaryMissingVariables(t *testing.T) {
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Cached missing, uid: cached-missing, type: http}
variables:
  - {name: shared, value: "prefix-{{missingPart}}"}
http:
  method: POST
  url: https://example.test
  headers:
    - {name: Authorization, value: "Bearer {{shared}}"}
  body:
    type: text
    data: "ordinary={{shared}}"
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := result.Scripts[0]
	action := candidate.Script.Actions[0]
	if strings.Join(candidate.MissingVariables, ",") != "missingPart" ||
		action.Metadata["execution_blocker"] != "bruno_missing_variables" ||
		!strings.Contains(action.Body, "{{var.missingPart}}") {
		t.Fatalf("cached missing-variable candidate = %#v", candidate)
	}
}
