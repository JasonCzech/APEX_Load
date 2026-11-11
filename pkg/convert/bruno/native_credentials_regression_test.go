package bruno

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/engine"
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
	if header != "Bearer "+sourceCredential {
		t.Fatalf("Authorization header = %q, want %q", header, "Bearer "+sourceCredential)
	}
	if len(result.SecretRequirements) != 0 {
		t.Fatalf("unexpected secret requirements: %#v", result.SecretRequirements)
	}
	sanitized := tarFile(t, result.SanitizedArchive, "demo/request.yml")
	if !bytes.Contains(sanitized, []byte(sourceCredential)) {
		t.Fatalf("archived request lost source credential:\n%s", sanitized)
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
	if header != "Bearer prefix-"+sourceCredential {
		t.Fatalf("composed Authorization header = %q", header)
	}
	if len(candidate.SecretRequirements) != 0 {
		t.Fatalf("composed header requirements = %#v", candidate.SecretRequirements)
	}
	sanitized := tarFile(t, result.SanitizedArchive, "request.yml")
	for _, preserved := range []string{sourceCredential, "Bearer prefix-{{token}}"} {
		if !bytes.Contains(sanitized, []byte(preserved)) {
			t.Fatalf("archived header source lost %q:\n%s", preserved, sanitized)
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
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Reused header part, uid: reused-header-part, type: http}
http:
  method: GET
  url: https://example.test
  headers:
    - {name: Authorization, value: "Bearer prefix-{{missingPart}}"}
    - {name: X-API-Key, value: "key-{{missingPart}}"}
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
	if len(candidate.SecretRequirements) != 2 {
		t.Fatalf("composed header requirements = %#v", candidate.SecretRequirements)
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
	if action.Headers["X-Signature"] != literalSignature ||
		action.Headers["Authentication"] != templateValue {
		t.Fatalf("headers = %#v", action.Headers)
	}
	if len(candidate.SecretRequirements) != 0 {
		t.Fatalf("policy header requirements = %#v", candidate.SecretRequirements)
	}
	sanitized := tarFile(t, result.SanitizedArchive, "request.yml")
	for _, preserved := range []string{literalSignature, templateValue} {
		if !bytes.Contains(sanitized, []byte(preserved)) {
			t.Fatalf("archived policy headers lost %q:\n%s", preserved, sanitized)
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
		auth.Username != "fixed-user" ||
		auth.Password != "prefix-"+sourcePart ||
		len(candidate.SecretRequirements) != 0 {
		t.Fatalf("composed auth candidate = %#v", candidate)
	}
	sanitized := tarFile(t, result.SanitizedArchive, "request.yml")
	for _, preserved := range []string{sourcePart, "prefix-{{neutralValue}}", "fixed-user"} {
		if !bytes.Contains(sanitized, []byte(preserved)) {
			t.Fatalf("archived auth source lost %q:\n%s", preserved, sanitized)
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
	if candidate.Script.Actions[0].Body != "credential=prefix-"+sourcePart ||
		len(candidate.SecretRequirements) != 0 {
		t.Fatalf("composed sensitive variable candidate = %#v", candidate)
	}
	sanitized := tarFile(t, result.SanitizedArchive, "request.yml")
	for _, preserved := range []string{sourcePart, "prefix-{{neutralValue}}"} {
		if !bytes.Contains(sanitized, []byte(preserved)) {
			t.Fatalf("archived sensitive variable lost %q:\n%s", preserved, sanitized)
		}
	}
}

func TestInspectNativeRejectsUnusedComposedSensitiveVariable(t *testing.T) {
	result, err := Inspect(Input{
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
	if err != nil {
		t.Fatalf("unused sensitive variable error = %v", err)
	}
	if len(result.Scripts[0].SecretRequirements) != 0 {
		t.Fatalf("unused sensitive variable requirements = %#v", result.Scripts[0].SecretRequirements)
	}

	result, err = Inspect(Input{
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
	if target != "https://example.test/search?apiKey="+sourceCredential+"&safe=retained" {
		t.Fatalf("query target = %q", target)
	}
	if len(candidate.SecretRequirements) != 0 ||
		hasNativeDiagnostic(candidate.Diagnostics, "bruno_url_secret_encoding_review_required") ||
		candidate.Script.Actions[0].Metadata["promotion_blocker"] != nil {
		t.Fatalf("query credential candidate = %#v", candidate)
	}
	if !bytes.Contains(tarFile(t, result.SanitizedArchive, "request.yml"), []byte(sourceCredential)) {
		t.Fatal("archived source lost literal URL query credential")
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
	if candidate.Script.Actions[0].Target != "https://example.test/search?token="+sourceCredential ||
		len(candidate.SecretRequirements) != 0 ||
		hasNativeDiagnostic(candidate.Diagnostics, "bruno_url_secret_encoding_review_required") {
		t.Fatalf("query variable candidate = %#v", candidate)
	}
	if !bytes.Contains(tarFile(t, result.SanitizedArchive, "request.yml"), []byte(sourceCredential)) {
		t.Fatal("archived source lost URL query credential dependency")
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
	if target != "https://example.test/search?token=Bearer-"+sourcePart ||
		len(candidate.SecretRequirements) != 0 {
		t.Fatalf("composed query candidate = %#v", candidate)
	}
}

func TestInspectNativeComposedURLCredentialCountsValuelessDuplicate(t *testing.T) {
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Composed query after flag, uid: composed-query-after-flag, type: http}
http:
  method: GET
  url: "https://example.test/search?token&token=Bearer-{{missingValue}}"
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := result.Scripts[0]
	target := candidate.Script.Actions[0].Target
	if !strings.Contains(target, "?token&token={{secret.") ||
		len(candidate.SecretRequirements) != 1 {
		t.Fatalf("composed query candidate = %#v", candidate)
	}
}

func TestInspectNativeSeparatesComposedQueryAndPathParameterCredentials(t *testing.T) {
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Composed URL params, uid: composed-url-params, type: http}
http:
  method: GET
  url: https://example.test/items/:token
  params:
    - {name: token, value: "path-{{missingValue}}", type: path}
    - {name: signature, value: "query-{{missingValue}}", type: query}
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
		strings.Count(target, "{{secret.") != 2 ||
		len(candidate.SecretRequirements) != 2 {
		t.Fatalf("composed URL parameter target = %q, reqs = %#v", target, candidate.SecretRequirements)
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
			if !strings.Contains(candidate.Script.Actions[0].Target, signature) ||
				len(candidate.SecretRequirements) != 0 {
				t.Fatalf("signed URL candidate = %#v", candidate)
			}
			if !bytes.Contains(tarFile(t, result.SanitizedArchive, "request.yml"), []byte(signature)) {
				t.Fatal("archived source lost signed URL value")
			}
		})
	}
}

func TestInspectNativeRejectsAuthoredURLUserinfo(t *testing.T) {
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: URL userinfo, uid: url-userinfo, type: http}
http:
  method: GET
  url: https://username:password@example.test/private
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatalf("URL userinfo error = %v", err)
	}
	if got := result.Scripts[0].Script.Actions[0].Target; got != "https://username:password@example.test/private" {
		t.Fatalf("URL userinfo target = %q", got)
	}
	if len(result.SecretRequirements) != 0 {
		t.Fatalf("URL userinfo secret requirements = %#v", result.SecretRequirements)
	}
}

func TestInspectNativeRejectsSensitiveURLFragments(t *testing.T) {
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Sensitive fragment, uid: sensitive-fragment, type: http}
http:
  method: GET
  url: https://example.test/callback#access_token=literal-fragment-secret
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatalf("sensitive URL fragment error = %v", err)
	}
	if got := result.Scripts[0].Script.Actions[0].Target; got != "https://example.test/callback#access_token=literal-fragment-secret" {
		t.Fatalf("URL fragment target = %q", got)
	}
	if len(result.SecretRequirements) != 0 {
		t.Fatalf("URL fragment secret requirements = %#v", result.SecretRequirements)
	}
}

func TestInspectNativeRejectsCredentialsHiddenInResolvedBaseURLVariables(t *testing.T) {
	for _, sourceURL := range []string{
		"https://username:password@example.test/private",
		"https://example.test/private?token=literal-secret",
	} {
		t.Run(sourceURL, func(t *testing.T) {
			result, err := Inspect(Input{
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
			if err != nil {
				t.Fatalf("resolved URL credential error = %v", err)
			}
			params := result.Scripts[0].Script.Parameters
			if len(params) != 1 || len(params[0].Values) != 1 || params[0].Values[0] != sourceURL {
				t.Fatalf("parameters = %#v, want %q", params, sourceURL)
			}
			if len(result.SecretRequirements) != 0 {
				t.Fatalf("unexpected secret requirements = %#v", result.SecretRequirements)
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
	wantBody := `<Envelope><login apiKey="` + sourceCredential + `"/><password>` + sourceCredential + `</password></Envelope>`
	if body != wantBody {
		t.Fatalf("sensitive XML template body = %q, want %q", body, wantBody)
	}
	if len(result.SecretRequirements) != 0 {
		t.Fatalf("unexpected XML requirements: %#v", result.SecretRequirements)
	}
}

func TestInspectNativeRejectsSensitiveXMLMixedContent(t *testing.T) {
	result, err := Inspect(Input{
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
	if err != nil {
		t.Fatalf("mixed-content XML error = %v", err)
	}
	if got := result.Scripts[0].Script.Actions[0].Body; got != `<Envelope><password>cleartext<suffix/></password></Envelope>` {
		t.Fatalf("mixed-content XML body = %q", got)
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
	wantBody := `<Envelope><![CDATA[password=` + sourceCredential + `]]></Envelope>`
	if action.Body != wantBody ||
		len(candidate.SecretRequirements) != 0 ||
		strings.Contains(action.Body, nativeXMLReviewMarker) ||
		hasNativeDiagnostic(candidate.Diagnostics, "bruno_xml_unsupported_markup_review_required") ||
		action.Metadata["review_gate"] != nil {
		t.Fatalf("credential-bearing XML markup was altered: %#v", candidate)
	}
	if !bytes.Contains(tarFile(t, result.SanitizedArchive, "request.yml"), []byte(sourceCredential)) {
		t.Fatal("archived XML source lost credential-bearing markup")
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
	if body != `<Envelope><userPassword>prefix-`+sourceCredential+`</userPassword></Envelope>` {
		t.Fatalf("composed XML credential body = %q", body)
	}
	if len(candidate.SecretRequirements) != 0 {
		t.Fatalf("composed XML requirements = %#v", candidate.SecretRequirements)
	}
}

func TestInspectNativeSeparatesComposedXMLCredentialRequirements(t *testing.T) {
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Reused XML part, uid: reused-xml-part, type: http}
http:
  method: POST
  url: https://example.test
  body:
    type: xml
    data: '<Envelope><password>A-{{missingPart}}</password><token>B-{{missingPart}}</token></Envelope>'
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
	replacements := map[string]bool{}
	for _, requirement := range candidate.SecretRequirements {
		replacements[requirement.Replacement] = true
	}
	if len(candidate.SecretRequirements) != 2 || len(replacements) != 2 {
		t.Fatalf("composed XML requirements = %#v", candidate.SecretRequirements)
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
	for _, preserved := range []string{username, password, `"safe":"retained"`} {
		if !strings.Contains(body, preserved) {
			t.Fatalf("native JSON body lost %q: %s", preserved, body)
		}
	}
	if strings.Contains(body, "{{secret.") || len(result.Scripts[0].SecretRequirements) != 0 {
		t.Fatalf("native JSON body = %s, requirements = %#v", body, result.Scripts[0].SecretRequirements)
	}
	sanitized := tarFile(t, result.SanitizedArchive, "request.yml")
	for _, preserved := range []string{username, password} {
		if !bytes.Contains(sanitized, []byte(preserved)) {
			t.Fatalf("sanitized JSON source lost %q:\n%s", preserved, sanitized)
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
	if !strings.Contains(body, "prefix-"+sourceCredential) || strings.Contains(body, "{{secret.") {
		t.Fatalf("templated JSON body = %s", body)
	}
	if len(candidate.MissingVariables) != 0 || len(candidate.SecretRequirements) != 0 ||
		hasNativeDiagnostic(candidate.Diagnostics, "bruno_json_secret_escaping_review_required") {
		t.Fatalf("templated JSON candidate = %#v", candidate)
	}
	sanitized := tarFile(t, result.SanitizedArchive, "request.yml")
	for _, preserved := range []string{sourceCredential, "prefix-{{passwordValue}}"} {
		if !bytes.Contains(sanitized, []byte(preserved)) {
			t.Fatalf("sanitized JSON source lost %q:\n%s", preserved, sanitized)
		}
	}
}

func TestInspectNativeSeparatesComposedJSONCredentialRequirements(t *testing.T) {
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Reused JSON part, uid: reused-json-part, type: http}
http:
  method: POST
  url: https://example.test
  body:
    type: json
    data: '{"apiKey":"A-{{missingPart}}","clientSecret":"B-{{missingPart}}"}'
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
	replacements := map[string]bool{}
	for _, requirement := range candidate.SecretRequirements {
		if requirement.Value != "" {
			t.Fatalf("unresolved composed JSON requirement leaked value: %#v", requirement)
		}
		replacements[requirement.Replacement] = true
	}
	if len(candidate.SecretRequirements) != 2 || len(replacements) != 2 {
		t.Fatalf("composed JSON requirements = %#v", candidate.SecretRequirements)
	}
	sanitized := tarFile(t, result.SanitizedArchive, "request.yml")
	for _, preserved := range []string{"A-{{missingPart}}", "B-{{missingPart}}"} {
		if !bytes.Contains(sanitized, []byte(preserved)) {
			t.Fatalf("sanitized JSON source lost authored template %q:\n%s", preserved, sanitized)
		}
	}
}

func TestInspectNativeGatesJSONSecretsThatRequireRuntimeEscaping(t *testing.T) {
	const encodedCredential = `quote\"-slash\\-line\nend`
	literalResult, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Escaped JSON literal, uid: escaped-json-literal, type: http}
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
	literalCandidate := literalResult.Scripts[0]
	if len(literalCandidate.SecretRequirements) != 0 ||
		hasNativeDiagnostic(literalCandidate.Diagnostics, "bruno_json_secret_escaping_review_required") ||
		!strings.Contains(literalCandidate.Script.Actions[0].Body, encodedCredential) {
		t.Fatalf("literal escaped JSON credential was altered or gated: %#v", literalCandidate)
	}

	governedResult, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Escaped JSON secret, uid: escaped-json-secret, type: http}
http:
  method: POST
  url: https://example.test
  body:
    type: json
    data: '{"password":"{{secret.ESCAPED}}"}'
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	governedCandidate := governedResult.Scripts[0]
	if !hasNativeDiagnostic(governedCandidate.Diagnostics, "bruno_json_secret_escaping_review_required") ||
		governedCandidate.Script.Actions[0].Metadata["review_gate"] !=
			"bruno_json_secret_escaping_review_required" ||
		len(governedCandidate.SecretRequirements) != 1 ||
		!governedCandidate.SecretRequirements[0].ExistingReference {
		t.Fatalf("explicit JSON secret was not review-gated: %#v", governedCandidate)
	}
}

func TestInspectNativeGatesSecretsIntroducedByGeneralJSONTemplateRewrite(t *testing.T) {
	resolvedResult, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: JSON resolved named secret, uid: json-resolved-named-secret, type: http}
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
	resolvedCandidate := resolvedResult.Scripts[0]
	if resolvedCandidate.Script.Actions[0].Body != `{"value":"p\"ass"}` ||
		len(resolvedCandidate.SecretRequirements) != 0 ||
		hasNativeDiagnostic(resolvedCandidate.Diagnostics, "bruno_json_secret_escaping_review_required") {
		t.Fatalf("resolved JSON named variable candidate = %#v", resolvedCandidate)
	}

	unresolvedResult, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: JSON named secret, uid: json-named-secret, type: http}
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
	candidate := unresolvedResult.Scripts[0]
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
		resolved   string
		unresolved string
		wantValue  string
		diagnostic string
	}{
		{
			name:       "form exact",
			bodyType:   "form-urlencoded",
			resolved:   "{{neutralValue}}",
			unresolved: "{{missingValue}}",
			wantValue:  sourcePart,
			diagnostic: "bruno_form_template_review_required",
		},
		{
			name:       "form composed",
			bodyType:   "form-urlencoded",
			resolved:   "prefix-{{neutralValue}}",
			unresolved: "prefix-{{missingValue}}",
			wantValue:  "prefix-" + sourcePart,
			diagnostic: "bruno_form_template_review_required",
		},
		{
			name:       "multipart exact",
			bodyType:   "multipart-form",
			resolved:   "{{neutralValue}}",
			unresolved: "{{missingValue}}",
			wantValue:  sourcePart,
			diagnostic: "bruno_multipart_template_review_required",
		},
		{
			name:       "multipart composed",
			bodyType:   "multipart-form",
			resolved:   "prefix-{{neutralValue}}",
			unresolved: "prefix-{{missingValue}}",
			wantValue:  "prefix-" + sourcePart,
			diagnostic: "bruno_multipart_template_review_required",
		},
	}
	for _, test := range tests {
		t.Run(test.name+" resolved", func(t *testing.T) {
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
      - {name: password, value: "` + test.resolved + `"}
`),
				Options: InspectOptions{OutputMode: OutputModeNativeRequests},
			})
			if err != nil {
				t.Fatal(err)
			}
			candidate := result.Scripts[0]
			body := candidate.Script.Actions[0].Body
			if !strings.Contains(body, test.wantValue) ||
				strings.Contains(body, "{{secret.") ||
				len(candidate.SecretRequirements) != 0 ||
				hasNativeDiagnostic(candidate.Diagnostics, test.diagnostic) {
				t.Fatalf("%s resolved candidate = %#v", test.name, candidate)
			}
			sanitized := tarFile(t, result.SanitizedArchive, "request.yml")
			for _, preserved := range []string{sourcePart, test.resolved} {
				if !bytes.Contains(sanitized, []byte(preserved)) {
					t.Fatalf("sanitized %s lost %q:\n%s", test.name, preserved, sanitized)
				}
			}
		})
		t.Run(test.name+" unresolved", func(t *testing.T) {
			result, err := Inspect(Input{
				Filename: "request.yml",
				Content: []byte(`info: {name: Body field secret, uid: body-field-secret-missing, type: http}
http:
  method: POST
  url: https://example.test
  body:
    type: ` + test.bodyType + `
    data:
      - {name: password, value: "` + test.unresolved + `"}
`),
				Options: InspectOptions{OutputMode: OutputModeNativeRequests},
			})
			if err != nil {
				t.Fatal(err)
			}
			candidate := result.Scripts[0]
			body := candidate.Script.Actions[0].Body
			if !strings.Contains(body, "{{secret.") ||
				len(candidate.SecretRequirements) != 1 ||
				candidate.SecretRequirements[0].Value != "" ||
				!hasNativeDiagnostic(candidate.Diagnostics, test.diagnostic) {
				t.Fatalf("%s unresolved candidate = %#v", test.name, candidate)
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
	if !strings.Contains(candidate.Script.Actions[0].Body, "prefix-"+sourceCredential) ||
		strings.Contains(candidate.Script.Actions[0].Body, "{{secret.") ||
		len(candidate.SecretRequirements) != 0 ||
		!hasNativeDiagnostic(candidate.Diagnostics, "bruno_graphql_body_review_required") {
		t.Fatalf("GraphQL credential candidate = %#v", candidate)
	}
	if !bytes.Contains(tarFile(t, result.SanitizedArchive, "request.yml"), []byte(sourceCredential)) {
		t.Fatal("sanitized GraphQL source lost its authored variable value")
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
	if !strings.Contains(candidate.Script.Actions[0].Body, sourceCredential) ||
		strings.Contains(candidate.Script.Actions[0].Body, "{{secret.") ||
		len(candidate.SecretRequirements) != 0 ||
		hasNativeDiagnostic(candidate.Diagnostics, "bruno_json_secret_escaping_review_required") {
		t.Fatalf("mislabeled JSON literal candidate = %#v", candidate)
	}
	if !bytes.Contains(tarFile(t, result.SanitizedArchive, "request.yml"), []byte(sourceCredential)) {
		t.Fatal("sanitized mislabeled JSON lost its authored value")
	}

	unresolvedResult, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Mislabeled JSON unresolved, uid: mislabeled-json-unresolved, type: http}
http:
  method: POST
  url: https://example.test
  body:
    type: text
    data: '{"password":"{{missingPassword}}"}'
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	unresolvedCandidate := unresolvedResult.Scripts[0]
	if !strings.Contains(unresolvedCandidate.Script.Actions[0].Body, "{{secret.") ||
		len(unresolvedCandidate.SecretRequirements) != 1 ||
		!unresolvedCandidate.SecretRequirements[0].UnresolvedSourceVariable ||
		!hasNativeDiagnostic(unresolvedCandidate.Diagnostics, "bruno_json_secret_escaping_review_required") {
		t.Fatalf("mislabeled JSON unresolved candidate = %#v", unresolvedCandidate)
	}
}

func TestInspectNativeContentSniffsScalarJSONBodies(t *testing.T) {
	const sourceCredential = "scalar-json-source-secret"
	for _, test := range []struct {
		name      string
		variables string
		body      string
		want      string
	}{
		{
			name: "literal",
			body: `{"password":"` + sourceCredential + `"}`,
			want: sourceCredential,
		},
		{
			name:      "template",
			variables: "variables:\n  - {name: neutralValue, value: " + sourceCredential + "}\n",
			body:      `{"password":"prefix-{{neutralValue}}"}`,
			want:      "prefix-" + sourceCredential,
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
			if !strings.Contains(candidate.Script.Actions[0].Body, test.want) ||
				strings.Contains(candidate.Script.Actions[0].Body, "{{secret.") ||
				len(candidate.SecretRequirements) != 0 {
				t.Fatalf("scalar JSON candidate = %#v", candidate)
			}
			if !bytes.Contains(tarFile(t, result.SanitizedArchive, "request.yml"), []byte(sourceCredential)) {
				t.Fatal("sanitized scalar JSON lost its authored value")
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
			result, err := Inspect(Input{
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
			if err != nil {
				t.Fatalf("composite JSON error = %v", err)
			}
			if got := result.Scripts[0].Script.Actions[0].Body; got != body || len(result.Scripts[0].SecretRequirements) != 0 {
				t.Fatalf("composite JSON body = %q, requirements = %#v", got, result.Scripts[0].SecretRequirements)
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
	result, err := Inspect(Input{
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
	if err != nil {
		t.Fatalf("numeric JSON credential error = %v", err)
	}
	if got := result.Scripts[0].Script.Actions[0].Body; got != `{"apiKey":123456789,"count":42}` || len(result.Scripts[0].SecretRequirements) != 0 {
		t.Fatalf("numeric JSON body = %q, requirements = %#v", got, result.Scripts[0].SecretRequirements)
	}
}

func TestInspectNativeRejectsNonStringStructuredJSONCredentialWithoutChangingWireType(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "number", value: "123456789", want: `"apiKey":123456789`},
		{name: "boolean", value: "true", want: `"apiKey":true`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := Inspect(Input{
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
			if err != nil {
				t.Fatalf("structured JSON credential error = %v", err)
			}
			if got := result.Scripts[0].Script.Actions[0].Body; !strings.Contains(got, test.want) || len(result.Scripts[0].SecretRequirements) != 0 {
				t.Fatalf("structured JSON body = %q, requirements = %#v", got, result.Scripts[0].SecretRequirements)
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
	if len(result.Scripts) != 2 || len(result.SecretRequirements) != 0 {
		t.Fatalf("candidate count = %d, requirements = %#v", len(result.Scripts), result.SecretRequirements)
	}
	values := make(map[string]bool)
	for _, candidate := range result.Scripts {
		auth := candidate.Script.Actions[0].Auth
		if auth == nil || len(candidate.SecretRequirements) != 0 {
			t.Fatalf("%s auth = %#v requirements = %#v", candidate.SourcePath, auth, candidate.SecretRequirements)
		}
		values[auth.Password] = true
	}
	if !values["prefix-alpha"] || !values["prefix-beta"] {
		t.Fatalf("candidate-scoped auth values = %#v", values)
	}
	sanitizedFolder := tarFile(t, result.SanitizedArchive, "demo/shared/folder.yml")
	if !bytes.Contains(sanitizedFolder, []byte("prefix-{{suffix}}")) ||
		!bytes.Contains(sanitizedFolder, []byte(`password: "{{passwordValue}}"`)) {
		t.Fatalf("sanitized inherited auth lost authored templates:\n%s", sanitizedFolder)
	}
}

func TestInspectNativeReportsInheritedComposedHeaderRequestPaths(t *testing.T) {
	result, err := Inspect(Input{
		Files: map[string][]byte{
			"demo/opencollection.yml": []byte(`opencollection: 1.0.0
info: {name: Scoped inherited header}
`),
			"demo/shared/folder.yml": []byte(`info: {name: Shared, type: folder, seq: 1}
request:
  variables:
    - {name: tokenValue, value: "prefix-{{suffix}}"}
  headers:
    - {name: X-API-Key, value: "{{tokenValue}}"}
`),
			"demo/shared/a.yml": []byte(`info: {name: A, uid: scoped-header-a, type: http, seq: 1}
variables:
  - {name: suffix, value: "{{missingAlpha}}"}
http: {method: GET, url: https://example.test/a}
`),
			"demo/shared/b.yml": []byte(`info: {name: B, uid: scoped-header-b, type: http, seq: 2}
variables:
  - {name: suffix, value: "{{missingBeta}}"}
http: {method: GET, url: https://example.test/b}
`),
		},
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Scripts) != 2 || len(result.SecretRequirements) != 2 {
		t.Fatalf("inherited header result = %#v", result)
	}
	for _, candidate := range result.Scripts {
		if len(candidate.SecretRequirements) != 1 {
			t.Fatalf("%s requirements = %#v", candidate.SourcePath, candidate.SecretRequirements)
		}
		requirement := candidate.SecretRequirements[0]
		if requirement.Path != "demo/shared/folder.yml" ||
			!reflect.DeepEqual(requirement.AffectedPaths, []string{"demo/" + candidate.SourcePath}) {
			t.Fatalf("%s inherited header locations = path %q affected %#v", candidate.SourcePath, requirement.Path, requirement.AffectedPaths)
		}
		if got := candidate.Script.Actions[0].Headers["X-API-Key"]; got != requirement.Replacement {
			t.Fatalf("%s inherited header = %q, want %q", candidate.SourcePath, got, requirement.Replacement)
		}
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
	if len(result.Scripts) != 2 || len(result.SecretRequirements) != 1 {
		t.Fatalf("candidates = %d, aggregate requirements = %#v", len(result.Scripts), result.SecretRequirements)
	}
	first := result.Scripts[0]
	second := result.Scripts[1]
	if first.Script.Actions[0].Auth == nil || second.Script.Actions[0].Auth == nil ||
		first.Script.Actions[0].Auth.Password != "prefix-resolved" ||
		len(first.SecretRequirements) != 0 ||
		!strings.HasPrefix(second.Script.Actions[0].Auth.Password, "{{secret.") ||
		len(second.SecretRequirements) != 1 ||
		second.SecretRequirements[0].Value != "" {
		t.Fatalf("resolved/unresolved inherited auth = first(%#v, %#v) second(%#v, %#v)",
			first.Script.Actions[0].Auth, first.SecretRequirements,
			second.Script.Actions[0].Auth, second.SecretRequirements,
		)
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
	if len(result.SecretRequirements) != 0 {
		t.Fatalf("shared requirement = %#v, want 0 for resolved XML variable", result.SecretRequirements)
	}
	for _, candidate := range result.Scripts {
		body := candidate.Script.Actions[0].Body
		if body != "<Envelope><password>shared-xml-secret</password></Envelope>" || len(candidate.SecretRequirements) != 0 {
			t.Fatalf("%s body = %q, requirements = %#v", candidate.SourcePath, body, candidate.SecretRequirements)
		}
		sanitized := tarFile(t, result.SanitizedArchive, "demo/"+candidate.SourcePath)
		if !bytes.Contains(sanitized, []byte("{{bodyValue}}")) {
			t.Fatalf("sanitized %s lost authored template:\n%s", candidate.SourcePath, sanitized)
		}
	}
}

func TestInspectNativeDescribesSharedUnresolvedXMLCredentials(t *testing.T) {
	result, err := Inspect(Input{
		Files: map[string][]byte{
			"demo/opencollection.yml": []byte(`opencollection: 1.0.0
info: {name: Shared unresolved XML credential}
`),
			"demo/a.yml": []byte(`info: {name: A, uid: unresolved-xml-a, type: http, seq: 1}
http:
  method: POST
  url: https://example.test/a
  body: {type: xml, data: '<Envelope><mh:authentication><mh:userName>{{partnerUserName}}</mh:userName><mh:userPassword>{{partnerPassword}}</mh:userPassword></mh:authentication></Envelope>'}
`),
			"demo/b.yml": []byte(`info: {name: B, uid: unresolved-xml-b, type: http, seq: 2}
http:
  method: POST
  url: https://example.test/b
  body: {type: xml, data: '<Envelope><mh:authentication><mh:userName>{{partnerUserName}}</mh:userName><mh:userPassword>{{partnerPassword}}</mh:userPassword></mh:authentication></Envelope>'}
`),
		},
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.SecretRequirements) != 2 {
		t.Fatalf("shared unresolved requirements = %#v", result.SecretRequirements)
	}
	if len(result.MissingVariables) != 0 {
		t.Fatalf("aggregate missing variables = %#v", result.MissingVariables)
	}
	wantPaths := []string{"demo/a.yml", "demo/b.yml"}
	wantMetadata := map[string]struct {
		namePrefix string
		label      string
		context    string
	}{
		"partnerPassword": {"PARTNER_PASSWORD", "Partner password", "XML password element <mh:userPassword>"},
		"partnerUserName": {"PARTNER_USERNAME", "Partner username", "XML username element <mh:userName>"},
	}
	bindings := make(map[string]string, len(result.SecretRequirements))
	for _, requirement := range result.SecretRequirements {
		want, exists := wantMetadata[requirement.SourceVariable]
		// A readable name: the variable's own, with no source path or hash.
		if !exists || requirement.SuggestedName != want.namePrefix ||
			requirement.DisplayLabel != want.label ||
			!requirement.UnresolvedSourceVariable ||
			requirement.Context != want.context ||
			requirement.Value != "" {
			t.Fatalf("unresolved XML presentation metadata = %#v", requirement)
		}
		if !reflect.DeepEqual(requirement.AffectedPaths, wantPaths) {
			t.Fatalf("affected paths = %#v, want %#v", requirement.AffectedPaths, wantPaths)
		}
		bindings[requirement.SourceVariable] = requirement.Replacement
	}
	for _, candidate := range result.Scripts {
		if len(candidate.MissingVariables) != 0 || len(candidate.SecretRequirements) != 2 {
			t.Fatalf("candidate %q requirements = %#v", candidate.SourcePath, candidate.SecretRequirements)
		}
		action := candidate.Script.Actions[0]
		if blocker := action.Metadata["execution_blocker"]; blocker == "bruno_missing_variables" {
			t.Fatalf("candidate %q retained missing-variable blocker: %#v", candidate.SourcePath, action.Metadata)
		}
		wantBody := "<Envelope><mh:authentication><mh:userName>" + bindings["partnerUserName"] +
			"</mh:userName><mh:userPassword>" + bindings["partnerPassword"] +
			"</mh:userPassword></mh:authentication></Envelope>"
		if body := action.Body; body != wantBody {
			t.Fatalf("candidate %q body = %q", candidate.SourcePath, body)
		}
	}
}

func TestInspectNativeKeepsNormalizationEquivalentUnresolvedVariablesDistinct(t *testing.T) {
	const body = `<authentication><credential>{{clientId}}</credential><credential>{{client_id}}</credential><credential>{{ClientId}}</credential><credential>{{CLIENT-ID}}</credential></authentication>`
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Distinct unresolved credentials, uid: distinct-unresolved-credentials, type: http}
http:
  method: POST
  url: https://example.test
  body:
    type: xml
    data: '` + body + `'
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantVariables := map[string]bool{
		"clientId":  false,
		"client_id": false,
		"ClientId":  false,
		"CLIENT-ID": false,
	}
	if len(result.SecretRequirements) != len(wantVariables) || len(result.Scripts) != 1 {
		t.Fatalf("normalization-equivalent requirements = %#v", result.SecretRequirements)
	}
	ids := make(map[string]struct{}, len(wantVariables))
	names := make(map[string]struct{}, len(wantVariables))
	replacements := make(map[string]struct{}, len(wantVariables))
	for _, requirement := range result.SecretRequirements {
		if _, exists := wantVariables[requirement.SourceVariable]; !exists ||
			!requirement.UnresolvedSourceVariable ||
			!(requirement.SuggestedName == "CLIENT_ID" || strings.HasPrefix(requirement.SuggestedName, "CLIENT_ID_")) {
			t.Fatalf("distinct unresolved metadata = %#v", requirement)
		}
		wantVariables[requirement.SourceVariable] = true
		ids[requirement.ID] = struct{}{}
		names[requirement.SuggestedName] = struct{}{}
		replacements[requirement.Replacement] = struct{}{}
	}
	for variable, found := range wantVariables {
		if !found {
			t.Errorf("requirement for %q was not retained", variable)
		}
	}
	if len(ids) != len(wantVariables) || len(names) != len(wantVariables) ||
		len(replacements) != len(wantVariables) {
		t.Fatalf("distinct variables shared an identity or runtime binding: %#v", result.SecretRequirements)
	}
	candidate := result.Scripts[0]
	if len(candidate.MissingVariables) != 0 ||
		candidate.Script.Actions[0].Metadata["execution_blocker"] == "bruno_missing_variables" {
		t.Fatalf("governed unresolved credentials remained blocked: %#v", candidate)
	}
	for replacement := range replacements {
		if !strings.Contains(candidate.Script.Actions[0].Body, replacement) {
			t.Errorf("candidate body does not use distinct binding %q: %s", replacement, candidate.Script.Actions[0].Body)
		}
	}
}

func TestInspectNativeKeepsNormalizationEquivalentUnresolvedAuthVariablesDistinct(t *testing.T) {
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Distinct unresolved auth, uid: distinct-unresolved-auth, type: http}
http:
  method: GET
  url: https://example.test
  auth:
    type: basic
    username: "{{env.client.id}}"
    password: "{{env.client-id}}"
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Scripts) != 1 || len(result.SecretRequirements) != 2 {
		t.Fatalf("normalization-equivalent auth requirements = %#v", result.SecretRequirements)
	}
	candidate := result.Scripts[0]
	if len(candidate.MissingVariables) != 0 ||
		candidate.Script.Actions[0].Metadata["execution_blocker"] == "bruno_missing_variables" {
		t.Fatalf("governed unresolved auth remained blocked: %#v", candidate)
	}
	wantVariables := map[string]bool{"env.client.id": false, "env.client-id": false}
	ids := make(map[string]struct{}, len(wantVariables))
	names := make(map[string]struct{}, len(wantVariables))
	for _, requirement := range result.SecretRequirements {
		if _, exists := wantVariables[requirement.SourceVariable]; !exists ||
			!requirement.UnresolvedSourceVariable || requirement.Value != "" {
			t.Fatalf("distinct unresolved auth metadata = %#v", requirement)
		}
		wantVariables[requirement.SourceVariable] = true
		ids[requirement.ID] = struct{}{}
		names[requirement.SuggestedName] = struct{}{}
	}
	if !wantVariables["env.client.id"] || !wantVariables["env.client-id"] ||
		len(ids) != len(wantVariables) || len(names) != len(wantVariables) {
		t.Fatalf("distinct auth variables shared an identity or runtime binding: %#v", result.SecretRequirements)
	}
	auth := candidate.Script.Actions[0].Auth
	if auth == nil || auth.Username == auth.Password ||
		!strings.HasPrefix(auth.Username, "{{secret.ENV_CLIENT_ID") ||
		!strings.HasPrefix(auth.Password, "{{secret.ENV_CLIENT_ID") {
		t.Fatalf("distinct auth bindings = %#v", auth)
	}
}

func TestInspectNativeKeepsNormalizationEquivalentUnresolvedHeaderVariablesDistinct(t *testing.T) {
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Distinct unresolved headers, uid: distinct-unresolved-headers, type: http}
http:
  method: GET
  url: https://example.test
  headers:
    - {name: Authorization, value: "Bearer {{Token}}"}
    - {name: Cookie, value: "session={{token}}"}
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Scripts) != 1 || len(result.SecretRequirements) != 2 {
		t.Fatalf("normalization-equivalent header requirements = %#v", result.SecretRequirements)
	}
	candidate := result.Scripts[0]
	if len(candidate.MissingVariables) != 0 ||
		candidate.Script.Actions[0].Metadata["execution_blocker"] == "bruno_missing_variables" {
		t.Fatalf("governed unresolved headers remained blocked: %#v", candidate)
	}
	wantVariables := map[string]bool{"Token": false, "token": false}
	bindings := make(map[string]string, len(wantVariables))
	for _, requirement := range result.SecretRequirements {
		if _, exists := wantVariables[requirement.SourceVariable]; !exists ||
			!requirement.UnresolvedSourceVariable || requirement.Value != "" {
			t.Fatalf("distinct unresolved header metadata = %#v", requirement)
		}
		wantVariables[requirement.SourceVariable] = true
		bindings[requirement.SourceVariable] = requirement.Replacement
	}
	if !wantVariables["Token"] || !wantVariables["token"] ||
		bindings["Token"] == bindings["token"] {
		t.Fatalf("distinct header variables shared a runtime binding: %#v", result.SecretRequirements)
	}
	headers := candidate.Script.Actions[0].Headers
	if headers["Authorization"] != "Bearer "+bindings["Token"] ||
		headers["Cookie"] != "session="+bindings["token"] {
		t.Fatalf("distinct header bindings = %#v, requirements %#v", headers, result.SecretRequirements)
	}
}

func TestInspectNativeKeepsNormalizationEquivalentUnresolvedJSONVariablesDistinct(t *testing.T) {
	result, err := Inspect(Input{
		Files: map[string][]byte{
			"demo/opencollection.yml": []byte(`opencollection: 1.0.0
info: {name: Distinct unresolved JSON credentials}
`),
			"demo/a.yml": []byte(`info: {name: A, uid: distinct-json-a, type: http, seq: 1}
http:
  method: POST
  url: https://example.test/a
  body: {type: json, data: '{"password":"{{env.client.id}}"}'}
`),
			"demo/b.yml": []byte(`info: {name: B, uid: distinct-json-b, type: http, seq: 2}
http:
  method: POST
  url: https://example.test/b
  body: {type: json, data: '{"password":"{{env.client-id}}"}'}
`),
		},
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Scripts) != 2 || len(result.SecretRequirements) != 2 {
		t.Fatalf("normalization-equivalent JSON requirements = %#v", result.SecretRequirements)
	}
	wantVariables := map[string]bool{"env.client.id": false, "env.client-id": false}
	bindings := make(map[string]string, len(wantVariables))
	for _, requirement := range result.SecretRequirements {
		if _, exists := wantVariables[requirement.SourceVariable]; !exists ||
			!requirement.UnresolvedSourceVariable || requirement.Value != "" {
			t.Fatalf("distinct unresolved JSON metadata = %#v", requirement)
		}
		wantVariables[requirement.SourceVariable] = true
		bindings[requirement.SourceVariable] = requirement.Replacement
	}
	if !wantVariables["env.client.id"] || !wantVariables["env.client-id"] ||
		bindings["env.client.id"] == bindings["env.client-id"] {
		t.Fatalf("distinct JSON variables shared a runtime binding: %#v", result.SecretRequirements)
	}
	for _, candidate := range result.Scripts {
		if len(candidate.MissingVariables) != 0 || len(candidate.SecretRequirements) != 1 ||
			candidate.Script.Actions[0].Metadata["execution_blocker"] == "bruno_missing_variables" {
			t.Fatalf("governed unresolved JSON candidate remained blocked: %#v", candidate)
		}
		requirement := candidate.SecretRequirements[0]
		if !strings.Contains(candidate.Script.Actions[0].Body, requirement.Replacement) {
			t.Fatalf("JSON body does not use candidate binding %q: %s", requirement.Replacement, candidate.Script.Actions[0].Body)
		}
	}
}

func TestInspectNativeKeepsNormalizationEquivalentNamespacedXMLVariablesDistinct(t *testing.T) {
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Distinct namespaced XML credentials, uid: distinct-namespaced-xml, type: http}
http:
  method: POST
  url: https://example.test
  body:
    type: xml
    data: '<authentication><password>{{env.client.id}}</password><password>{{env.client-id}}</password></authentication>'
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Scripts) != 1 || len(result.SecretRequirements) != 2 {
		t.Fatalf("normalization-equivalent namespaced XML requirements = %#v", result.SecretRequirements)
	}
	wantVariables := map[string]bool{"env.client.id": false, "env.client-id": false}
	bindings := make(map[string]string, len(wantVariables))
	for _, requirement := range result.SecretRequirements {
		if _, exists := wantVariables[requirement.SourceVariable]; !exists ||
			!requirement.UnresolvedSourceVariable || requirement.Value != "" {
			t.Fatalf("distinct namespaced XML metadata = %#v", requirement)
		}
		wantVariables[requirement.SourceVariable] = true
		bindings[requirement.SourceVariable] = requirement.Replacement
	}
	candidate := result.Scripts[0]
	if !wantVariables["env.client.id"] || !wantVariables["env.client-id"] ||
		bindings["env.client.id"] == bindings["env.client-id"] ||
		len(candidate.MissingVariables) != 0 ||
		candidate.Script.Actions[0].Metadata["execution_blocker"] == "bruno_missing_variables" {
		t.Fatalf("distinct namespaced XML variables shared a runtime binding: %#v", candidate)
	}
	body := candidate.Script.Actions[0].Body
	if !strings.Contains(body, bindings["env.client.id"]) ||
		!strings.Contains(body, bindings["env.client-id"]) {
		t.Fatalf("XML body does not retain both namespaced bindings: %s", body)
	}
}

func TestInspectNativeNamespacedCredentialSourceDoesNotReclassifyExistingSecret(t *testing.T) {
	tests := []struct {
		name     string
		bodyType string
		body     string
	}{
		{name: "JSON", bodyType: "json", body: `{"password":"{{secret.EXISTING_PASSWORD}}"}`},
		{name: "XML", bodyType: "xml", body: `<authentication><password>{{secret.EXISTING_PASSWORD}}</password></authentication>`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := Inspect(Input{
				Filename: "request.yml",
				Content: []byte(`info: {name: Existing governed credential, uid: existing-governed-credential, type: http}
http:
  method: POST
  url: https://example.test
  body:
    type: ` + test.bodyType + `
    data: '` + test.body + `'
`),
				Options: InspectOptions{OutputMode: OutputModeNativeRequests},
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.SecretRequirements) != 1 {
				t.Fatalf("existing secret requirements = %#v", result.SecretRequirements)
			}
			requirement := result.SecretRequirements[0]
			if !requirement.ExistingReference || requirement.UnresolvedSourceVariable ||
				requirement.SourceVariable != "" || requirement.SuggestedName != "EXISTING_PASSWORD" ||
				requirement.Replacement != "{{secret.EXISTING_PASSWORD}}" {
				t.Fatalf("existing secret was reclassified as an unresolved source variable: %#v", requirement)
			}
		})
	}
}

func TestInspectNativeKeepsNormalizationEquivalentUnresolvedURLCredentialsDistinct(t *testing.T) {
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Distinct unresolved URL credentials, uid: distinct-unresolved-url, type: http}
http:
  method: GET
  url: "https://example.test?password={{clientId}}&password={{client_id}}"
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Scripts) != 1 || len(result.SecretRequirements) != 2 {
		t.Fatalf("normalization-equivalent URL requirements = %#v", result.SecretRequirements)
	}
	ids := make(map[string]struct{}, 2)
	names := make(map[string]struct{}, 2)
	wantVariables := map[string]string{
		"clientId":  "Client ID",
		"client_id": "Client ID",
	}
	for _, requirement := range result.SecretRequirements {
		wantLabel, exists := wantVariables[requirement.SourceVariable]
		if !exists || requirement.DisplayLabel != wantLabel ||
			!requirement.UnresolvedSourceVariable || requirement.Value != "" {
			t.Fatalf("unresolved URL source metadata = %#v", requirement)
		}
		ids[requirement.ID] = struct{}{}
		names[requirement.SuggestedName] = struct{}{}
	}
	candidate := result.Scripts[0]
	if len(ids) != 2 || len(names) != 2 || len(candidate.MissingVariables) != 0 ||
		candidate.Script.Actions[0].Metadata["execution_blocker"] == "bruno_missing_variables" ||
		strings.Count(candidate.Script.Actions[0].Target, "{{secret.") != 2 {
		t.Fatalf("URL credential occurrences were not independently governed: %#v", candidate)
	}
}

func TestInspectNativeReusesSharedUnresolvedURLSourceIdentity(t *testing.T) {
	result, err := Inspect(Input{
		Files: map[string][]byte{
			"demo/opencollection.yml": []byte(`opencollection: 1.0.0
info: {name: Shared unresolved URL credential}
`),
			"demo/a.yml": []byte(`info: {name: A, uid: shared-url-a, type: http, seq: 1}
http:
  method: GET
  url: "https://example.test/a?password={{clientId}}"
`),
			"demo/b.yml": []byte(`info: {name: B, uid: shared-url-b, type: http, seq: 2}
http:
  method: GET
  url: "https://example.test/b?password={{clientId}}"
`),
		},
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantPaths := []string{"demo/a.yml", "demo/b.yml"}
	if len(result.Scripts) != 2 || len(result.SecretRequirements) != 1 {
		t.Fatalf("shared URL requirements = %#v", result.SecretRequirements)
	}
	requirement := result.SecretRequirements[0]
	if requirement.SourceVariable != "clientId" ||
		requirement.DisplayLabel != "Client ID" ||
		!requirement.UnresolvedSourceVariable || requirement.Value != "" ||
		!reflect.DeepEqual(requirement.AffectedPaths, wantPaths) {
		t.Fatalf("shared URL source metadata = %#v", requirement)
	}
	for _, candidate := range result.Scripts {
		if len(candidate.SecretRequirements) != 1 ||
			candidate.SecretRequirements[0].ID != requirement.ID ||
			!strings.Contains(candidate.Script.Actions[0].Target, requirement.Replacement) ||
			strings.Contains(candidate.Script.Actions[0].Target, "{{clientId}}") {
			t.Fatalf("candidate did not reuse shared URL binding: %#v", candidate)
		}
	}
}

func TestInspectNativeDescribesExactCredentialSourcesAcrossAuthAndForm(t *testing.T) {
	t.Run("resolved auth variable chain", func(t *testing.T) {
		result, err := Inspect(Input{
			Filename: "request.yml",
			Content: []byte(`info: {name: Auth source metadata, uid: auth-source-metadata, type: http}
variables:
  - {name: sourceCredential, value: imported-auth-value}
  - {name: clientId, value: "{{sourceCredential}}"}
http:
  method: GET
  url: https://example.test
  auth:
    type: bearer
    token: "{{clientId}}"
`),
			Options: InspectOptions{OutputMode: OutputModeNativeRequests},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(result.SecretRequirements) != 0 || len(result.Scripts) != 1 {
			t.Fatalf("resolved credential source requirements = %#v", result.SecretRequirements)
		}
		candidate := result.Scripts[0]
		if candidate.Script.Actions[0].Auth == nil ||
			candidate.Script.Actions[0].Auth.Token != "imported-auth-value" ||
			len(candidate.SecretRequirements) != 0 ||
			len(candidate.MissingVariables) != 0 {
			t.Fatalf("resolved credential source candidate = %#v", candidate)
		}
	})

	t.Run("unresolved form variable", func(t *testing.T) {
		result, err := Inspect(Input{
			Filename: "request.yml",
			Content: []byte(`info: {name: Form source metadata, uid: form-source-metadata, type: http}
http:
  method: POST
  url: https://example.test
  body:
    type: form-urlencoded
    data:
      - {name: password, value: "{{clientId}}"}
`),
			Options: InspectOptions{OutputMode: OutputModeNativeRequests},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(result.SecretRequirements) != 1 || len(result.Scripts) != 1 {
			t.Fatalf("credential source requirements = %#v", result.SecretRequirements)
		}
		requirement := result.SecretRequirements[0]
		if requirement.SourceVariable != "clientId" ||
			requirement.DisplayLabel != "Client ID" ||
			!requirement.UnresolvedSourceVariable ||
			requirement.Value != "" ||
			requirement.Context != "Bruno form body credential password" ||
			!reflect.DeepEqual(requirement.AffectedPaths, []string{"request.yml"}) {
			t.Fatalf("credential source metadata = %#v", requirement)
		}
		candidate := result.Scripts[0]
		if len(candidate.SecretRequirements) != 1 ||
			candidate.SecretRequirements[0].ID != requirement.ID ||
			len(candidate.MissingVariables) != 0 {
			t.Fatalf("credential source candidate = %#v", candidate)
		}
	})
}

func TestNativeXMLCredentialContext(t *testing.T) {
	tests := []struct {
		name string
		kind string
		tag  string
		want string
	}{
		{name: "username element", kind: "element", tag: "mh:userName", want: "XML username element <mh:userName>"},
		{name: "partner password element", kind: "element", tag: "partnerPassword", want: "XML password element <partnerPassword>"},
		{name: "password element with namespace", kind: "element", tag: "mh:userPassword", want: "XML password element <mh:userPassword>"},
		{name: "token element", kind: "element", tag: "access_token", want: "XML token element <access_token>"},
		{name: "API key element", kind: "element", tag: "api-key", want: "XML API key element <api-key>"},
		{name: "attribute", kind: "attribute", tag: "mh:userName", want: "XML username attribute mh:userName"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := nativeXMLCredentialContext(test.kind, test.tag); got != test.want {
				t.Fatalf("nativeXMLCredentialContext(%q, %q) = %q, want %q", test.kind, test.tag, got, test.want)
			}
		})
	}
}

func TestNativeHeaderDisplayLabel(t *testing.T) {
	tests := []struct {
		header string
		want   string
	}{
		{header: "X-CDF-AUTH-TOKEN", want: "X-CDF auth token"},
		{header: "idpctx-ixp-dark-mode-session", want: "IDP context IXP dark mode session"},
		{header: "idpctx-session-id", want: "IDP context session ID"},
		{header: "X-API-Key", want: "X-API key"},
		{header: "Cookie", want: "Cookie"},
	}
	for _, test := range tests {
		t.Run(test.header, func(t *testing.T) {
			if got := nativeHeaderDisplayLabel(test.header); got != test.want {
				t.Fatalf("nativeHeaderDisplayLabel(%q) = %q, want %q", test.header, got, test.want)
			}
		})
	}
}

func TestInspectNativeDescribesSuppliedCollectionHeaderCredentials(t *testing.T) {
	result, err := Inspect(Input{
		Files: map[string][]byte{
			"CTA_Collection 6a/opencollection.yml": []byte(`opencollection: 1.0.0
info: {name: CTA credential presentation}
`),
			"CTA_Collection 6a/CTA/InquireAccountProfile_312.yml": []byte(`info: {name: InquireAccountProfile_312, uid: inquire-account-profile-312, type: http, seq: 1}
http:
  method: POST
  url: https://example.test/account
  headers:
    - {name: Content-Type, value: text/xml}
    - {name: X-CDF-AUTH-TOKEN, value: "{{authToken}}"}
    - {name: idp-trace-id, value: test-trace-id-for-account-profile}
    - {name: idpctx-session-id, value: test-session-id-for-account-profile}
    - {name: attcsitest, value: "{{attcsitest}}"}
    - {name: cookie, value: test-cookie}
`),
			"CTA_Collection 6a/CTA/ActivateSubscriber_v302.yml": []byte(`info: {name: ActivateSubscriber_v302, uid: activate-subscriber-v302, type: http, seq: 2}
http:
  method: POST
  url: https://example.test/activate
  headers:
    - {name: Content-Type, value: text/xml}
    - {name: idpctx-feature-svc-fraud-check, value: "false"}
    - {name: idpctx-feature-svc-relax-credit-check-requirement, value: "true"}
    - {name: idpctx-session-id, value: test-session-id-for-activate-subscriber}
    - {name: randomheadertest, value: harmless-test-value}
    - {name: idpctx-user-type, value: test-user}
    - {name: idpctx-partner-initiated-order, value: "true"}
    - {name: idpctx-ixp-dark-mode-session, value: test-dark-mode-session-value}
`),
			"CTA_Collection 6a/CTA/InquireCPNIContactInformation_224.yml": []byte(`info: {name: InquireCPNIContactInformation_224, uid: inquire-cpni-contact-224, type: http, seq: 3}
http:
  method: POST
  url: https://example.test/cpni
  headers:
    - {name: Content-Type, value: text/xml}
    - {name: idp-trace-id, value: test-trace-id-for-cpni-contact}
    - {name: idpctx-session-id, value: test-session-id-for-cpni-contact}
    - {name: attcsitest, value: "{{attcsitest}}"}
`),
		},
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		id             string
		name           string
		label          string
		path           string
		sourceVariable string
		contextParts   []string
		unresolved     bool
		hasValue       bool
	}{
		{
			id:             "a73758b339b71e1635eb25f9",
			name:           "X_CDF_AUTH_TOKEN",
			label:          "X-CDF auth token",
			path:           "CTA_Collection 6a/CTA/InquireAccountProfile_312.yml",
			sourceVariable: "authToken",
			contextParts:   []string{`header "X-CDF-AUTH-TOKEN"`, "references {{authToken}}", "does not include a value", "configured destination value", "when this request runs"},
			unresolved:     true,
		},
	}
	// Session, trace and cookie headers are recorded state, not credentials:
	// they stay as imported and never reach credential review.
	if len(result.SecretRequirements) != len(tests) {
		t.Fatalf("credential requirements = %#v, want only the auth token", result.SecretRequirements)
	}
	for _, candidate := range result.Scripts {
		for _, header := range candidate.Script.Actions[0].HeaderList {
			switch strings.ToLower(header.Name) {
			case "idpctx-session-id", "idpctx-ixp-dark-mode-session", "idp-trace-id", "cookie":
				if !strings.HasPrefix(header.Value, "test-") {
					t.Errorf("%s header in %s = %q, want the imported value", header.Name, candidate.SourcePath, header.Value)
				}
			}
		}
	}
	requirementsByName := make(map[string]SecretRequirement, len(result.SecretRequirements))
	for _, requirement := range result.SecretRequirements {
		requirementsByName[requirement.SuggestedName] = requirement
	}
	for _, test := range tests {
		t.Run(test.label, func(t *testing.T) {
			requirement, exists := requirementsByName[test.name]
			if !exists {
				t.Fatalf("stable requirement %q not found in %#v", test.name, result.SecretRequirements)
			}
			if requirement.ID != test.id ||
				requirement.DisplayLabel != test.label ||
				requirement.Path != test.path ||
				requirement.SourceVariable != test.sourceVariable ||
				requirement.UnresolvedSourceVariable != test.unresolved ||
				(requirement.Value != "") != test.hasValue ||
				requirement.Replacement != "{{secret."+test.name+"}}" ||
				!reflect.DeepEqual(requirement.AffectedPaths, []string{test.path}) {
				t.Fatalf("descriptive header requirement = %#v", requirement)
			}
			for _, part := range test.contextParts {
				if !strings.Contains(requirement.Context, part) {
					t.Errorf("context %q does not contain %q", requirement.Context, part)
				}
			}
			if strings.Contains(requirement.Context, "Configure the generated project secret") {
				t.Errorf("context contains mutable destination instruction: %q", requirement.Context)
			}
		})
	}
}

func TestInspectNativeTurnsRedactedRecordedHeaderIntoInput(t *testing.T) {
	tests := []struct {
		name       string
		headers    string
		value      string
		wantRegion bool
	}{
		{
			name: "cookie pair",
			headers: `
    - {name: Cookie, value: "sid=[redacted]; theme=dark"}`,
			value: "sid=[redacted]; theme=dark",
		},
		{
			name: "cookie pair beside a template",
			headers: `
    - {name: Cookie, value: "sid=[redacted]; region={{region}}"}`,
			value:      "sid=[redacted]; region={{var.region}}",
			wantRegion: true,
		},
		{
			name: "unnamed part",
			headers: `
    - {name: X-Device, value: "tablet-[redacted]"}`,
			value: "tablet-[redacted]",
		},
		{
			name: "cookie with two redacted pairs",
			headers: `
    - {name: Cookie, value: "sid=[redacted]; csrf=[REDACTED]; region={{region}}"}`,
			value:      "sid=[redacted]; csrf=[REDACTED]; region={{var.region}}",
			wantRegion: true,
		},
		{
			name: "header list",
			headers: `
    - {name: idpctx-ixp-dark-mode-session, value: "[REDACTED]"}`,
			value: "[REDACTED]",
		},
		{
			name: "header map",
			headers: `
    idpctx-ixp-dark-mode-session: "[redacted]"`,
			value: "[redacted]",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := Inspect(Input{
				Filename: "request.yml",
				Content: []byte(`info: {name: Redacted native header, uid: redacted-native-header, type: http}
http:
  method: GET
  url: https://example.test
  headers:` + test.headers + `
`),
				Options: InspectOptions{OutputMode: OutputModeNativeRequests},
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.SecretRequirements) != 0 || len(result.Scripts) != 1 {
				t.Fatalf("redacted recorded header = requirements %#v candidates %#v", result.SecretRequirements, result.Scripts)
			}
			candidate := result.Scripts[0]
			if got := candidate.Script.Actions[0].HeaderList[0].Value; got != test.value {
				t.Fatalf("redacted header value = %q, want %q", got, test.value)
			}
			if slices.Contains(candidate.MissingVariables, "region") != test.wantRegion {
				t.Fatalf("missing variables = %#v, want region=%t", candidate.MissingVariables, test.wantRegion)
			}
			if !test.wantRegion && len(candidate.MissingVariables) != 0 {
				t.Fatalf("unexpected synthetic missing variables = %#v", candidate.MissingVariables)
			}
		})
	}
}

func TestInspectNativeTreatsCredentialRedactionSentinelsAsUnrecoverable(t *testing.T) {
	tests := []struct {
		name             string
		source           string
		preserved        string
		urlQuerySentinel bool
	}{
		{
			name: "literal JSON credential",
			source: `info: {name: JSON sentinel, uid: json-sentinel, type: http}
http:
  method: POST
  url: https://example.test
  body:
    type: json
    data: '{"password":" [rEdAcTeD] "}'
`,
			preserved: "[rEdAcTeD]",
		},
		{
			name: "percent encoded URL credential",
			source: `info: {name: URL sentinel, uid: url-sentinel, type: http}
http:
  method: GET
  url: https://example.test?token=%5bReDaCtEd%5d
`,
			preserved:        "%5bReDaCtEd%5d",
			urlQuerySentinel: true,
		},
		{
			name: "multiply percent encoded URL credential",
			source: `info: {name: Nested URL sentinel, uid: nested-url-sentinel, type: http}
http:
  method: GET
  url: https://example.test?token=%25255BReDaCtEd%25255D
`,
			preserved:        "%25255BReDaCtEd%25255D",
			urlQuerySentinel: true,
		},
		{
			name: "malformed percent suffix URL credential",
			source: `info: {name: Malformed URL sentinel, uid: malformed-url-sentinel, type: http}
http:
  method: GET
  url: https://example.test?token=%5BReDaCtEd%5D%ZZ
`,
			preserved: "%5BReDaCtEd%5D%ZZ",
		},
		{
			name: "over-depth percent encoded URL credential",
			source: `info: {name: Deep URL sentinel, uid: deep-url-sentinel, type: http}
http:
  method: GET
  url: https://example.test?token=` + strings.Repeat("%25", 40) + `5BReDaCtEd%5D
`,
			preserved:        "5BReDaCtEd%5D",
			urlQuerySentinel: true,
		},
		{
			name: "query parameter credential",
			source: `info: {name: Query sentinel, uid: query-sentinel, type: http}
http:
  method: GET
  url: https://example.test
  params:
    - {name: token, type: query, value: " [redacted] "}
`,
			preserved:        "%5Bredacted%5D",
			urlQuerySentinel: true,
		},
		{
			name: "literal XML element credential",
			source: `info: {name: XML sentinel, uid: xml-sentinel, type: http}
http:
  method: POST
  url: https://example.test
  body:
    type: xml
    data: '<root><password> [rEdAcTeD] </password></root>'
`,
			preserved: "[rEdAcTeD]",
		},
		{
			name: "literal XML attribute credential",
			source: `info: {name: XML attribute sentinel, uid: xml-attribute-sentinel, type: http}
http:
  method: POST
  url: https://example.test
  body:
    type: xml
    data: '<root password="&#91;ReDaCtEd&#93;"/>'
`,
			preserved: "&#91;ReDaCtEd&#93;",
		},
		{
			name: "multiply escaped XML attribute credential",
			source: `info: {name: Nested XML sentinel, uid: nested-xml-sentinel, type: http}
http:
  method: POST
  url: https://example.test
  body:
    type: xml
    data: '<root password="&amp;amp;#91;ReDaCtEd&amp;amp;#93;"/>'
`,
			preserved: "&amp;amp;#91;ReDaCtEd&amp;amp;#93;",
		},
		{
			name: "unsupported XML credential markup",
			source: `info: {name: XML markup sentinel, uid: xml-markup-sentinel, type: http}
http:
  method: POST
  url: https://example.test
  body:
    type: xml
    data: '<root><!-- password=[REDACTED] --><value>kept</value></root>'
`,
			preserved: "<!-- password=[REDACTED] -->",
		},
		{
			name: "resolved auth variable",
			source: `info: {name: Auth sentinel, uid: auth-sentinel, type: http}
variables:
  - {name: neutralPart, value: " [redacted] "}
http:
  method: GET
  url: https://example.test
  auth: {type: basic, username: operator, password: "{{neutralPart}}"}
`,
			preserved: "[redacted]",
		},
		{
			name: "composed auth variable",
			source: `info: {name: Composed auth sentinel, uid: composed-auth-sentinel, type: http}
variables:
  - {name: neutralPart, value: "[redacted]"}
http:
  method: GET
  url: https://example.test
  auth: {type: basic, username: operator, password: "prefix-{{neutralPart}}"}
`,
			preserved: "prefix-[redacted]",
		},
		{
			name: "authorization wrapper",
			source: `info: {name: Authorization sentinel, uid: authorization-sentinel, type: http}
http:
  method: GET
  url: https://example.test
  headers:
    - {name: Authorization, value: "Bearer [REDACTED]"}
`,
			preserved: "Bearer [REDACTED]",
		},
		{
			name: "composed header variable",
			source: `info: {name: Header sentinel, uid: header-sentinel, type: http}
variables:
  - {name: neutralPart, value: "[redacted]"}
http:
  method: GET
  url: https://example.test
  headers:
    - {name: X-API-Key, value: "prefix-{{neutralPart}}"}
`,
			preserved: "prefix-[redacted]",
		},
		{
			name: "composed form variable",
			source: `info: {name: Form sentinel, uid: form-sentinel, type: http}
variables:
  - {name: neutralPart, value: "[redacted]"}
http:
  method: POST
  url: https://example.test
  body:
    type: form-urlencoded
    data:
      - {name: password, value: "prefix-{{neutralPart}}"}
`,
			preserved: "prefix-%5Bredacted%5D",
		},
		{
			name: "composed JSON variable",
			source: `info: {name: Composed JSON sentinel, uid: composed-json-sentinel, type: http}
variables:
  - {name: neutralPart, value: "[redacted]"}
http:
  method: POST
  url: https://example.test
  body:
    type: json
    data: '{"password":"prefix-{{neutralPart}}"}'
`,
			preserved: "prefix-[redacted]",
		},
		{
			name: "composed URL variable",
			source: `info: {name: Composed URL sentinel, uid: composed-url-sentinel, type: http}
variables:
  - {name: neutralPart, value: "[redacted]"}
http:
  method: GET
  url: https://example.test?token=prefix-{{neutralPart}}
`,
			preserved:        "prefix-%5Bredacted%5D",
			urlQuerySentinel: true,
		},
		{
			name: "XML escaped variable chain",
			source: `info: {name: Composed XML sentinel, uid: composed-xml-sentinel, type: http}
variables:
  - {name: firstPart, value: "&#91;ReDaCtEd&#93;"}
  - {name: neutralPart, value: "{{firstPart}}"}
http:
  method: POST
  url: https://example.test
  body:
    type: xml
    data: '<root><password>prefix-{{neutralPart}}</password></root>'
`,
			preserved: "prefix-&amp;#91;ReDaCtEd&amp;#93;",
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
			if len(result.SecretRequirements) != 0 || len(result.Scripts) != 1 ||
				len(result.Scripts[0].SecretRequirements) != 0 {
				t.Fatalf("sentinel requirements = aggregate %#v candidate %#v", result.SecretRequirements, result.Scripts[0].SecretRequirements)
			}
			action := result.Scripts[0].Script.Actions[0]
			var rawParts strings.Builder
			rawParts.WriteString(action.Target)
			rawParts.WriteByte('\n')
			rawParts.WriteString(action.Body)
			for k, v := range action.Headers {
				rawParts.WriteByte('\n')
				rawParts.WriteString(k + ":" + v)
			}
			if action.Auth != nil {
				rawParts.WriteByte('\n')
				rawParts.WriteString(action.Auth.Username + ":" + action.Auth.Password + ":" + action.Auth.Token)
			}
			rawAction := rawParts.String()
			if test.urlQuerySentinel {
				if !strings.Contains(action.Target, test.preserved) &&
					!strings.Contains(action.Target, "{{secret.IMPORT_BASE_URL_TOKEN}}") {
					t.Fatalf("unexpected URL sentinel target = %q", action.Target)
				}
			} else {
				if strings.Contains(rawAction, "{{secret.") {
					t.Fatalf("action substituted secret reference for literal sentinel: %s", rawAction)
				}
				if !strings.Contains(rawAction, test.preserved) {
					t.Fatalf("action lost literal sentinel %q: %s", test.preserved, rawAction)
				}
			}
			sanitized := tarFile(t, result.SanitizedArchive, "request.yml")
			if !bytes.Equal(sanitized, []byte(test.source)) {
				t.Fatalf("sanitized archive altered source:\n%s", sanitized)
			}
		})
	}
}

func TestNativeSourceVariableSecretNameIsStableAndBounded(t *testing.T) {
	sourceVariable := "partner" + strings.Repeat("CredentialSegment", 32)
	first := nativeSourceVariableSecretName(sourceVariable)
	second := nativeSourceVariableSecretName(sourceVariable)
	if first != second {
		t.Fatalf("same raw variable produced %q and %q", first, second)
	}
	if len(first) > credential.MaxLogicalNameBytes {
		t.Fatalf("logical secret name is %d bytes, max %d: %q", len(first), credential.MaxLogicalNameBytes, first)
	}
	if _, err := engine.NormalizeLogicalSecretName(first); err != nil {
		t.Fatalf("logical secret name %q is not runtime-valid: %v", first, err)
	}
}

func TestMergeNativeSecretRequirementsScalesHighCardinality(t *testing.T) {
	const requirementCount = 20_000
	merged := SecretRequirement{
		ID:            "shared-requirement",
		Path:          "requests/00000.yml",
		SuggestedName: "SHARED_REQUIREMENT",
		Value:         "shared-source-value",
		AffectedPaths: []string{"requests/00000.yml"},
		scrubTargets: []nativeSecretScrubTarget{{
			Path:    "requests/00000.yml",
			Pointer: "/variables/0/value",
		}},
	}
	for index := requirementCount - 1; index > 0; index-- {
		sourcePath := fmt.Sprintf("requests/%05d.yml", index)
		merged = mergeNativeSecretRequirements(merged, SecretRequirement{
			ID:            merged.ID,
			Path:          sourcePath,
			SuggestedName: merged.SuggestedName,
			Value:         merged.Value,
			AffectedPaths: []string{sourcePath},
			scrubTargets: []nativeSecretScrubTarget{{
				Path:    sourcePath,
				Pointer: "/variables/0/value",
			}},
		})
	}
	if len(merged.affectedPathSet) != requirementCount ||
		len(merged.scrubTargetSet) != requirementCount ||
		len(merged.AffectedPaths) != requirementCount ||
		len(merged.scrubTargets) != requirementCount {
		t.Fatalf(
			"high-cardinality merge sizes = paths %d/%d targets %d/%d",
			len(merged.AffectedPaths), len(merged.affectedPathSet),
			len(merged.scrubTargets), len(merged.scrubTargetSet),
		)
	}
	finalized := finalizeNativeSecretRequirement(merged)
	if finalized.affectedPathSet != nil || finalized.scrubTargetSet != nil {
		t.Fatal("finalized requirement retained accumulator maps")
	}
	if len(finalized.AffectedPaths) != requirementCount ||
		finalized.AffectedPaths[0] != "requests/00000.yml" ||
		finalized.AffectedPaths[requirementCount-1] != "requests/19999.yml" {
		t.Fatalf("finalized affected paths are not complete and sorted: first=%q last=%q count=%d",
			finalized.AffectedPaths[0],
			finalized.AffectedPaths[len(finalized.AffectedPaths)-1],
			len(finalized.AffectedPaths),
		)
	}
	for index := 1; index < len(finalized.scrubTargets); index++ {
		if nativeSecretScrubTargetIdentity(finalized.scrubTargets[index-1]) >=
			nativeSecretScrubTargetIdentity(finalized.scrubTargets[index]) {
			t.Fatalf("finalized scrub targets are not deterministically sorted at %d", index)
		}
	}
}

func TestRegisterNativeGeneratedRequirementRejectsSameNameDifferentIdentity(t *testing.T) {
	state := &nativeTemplateState{generatedReq: make(map[string]SecretRequirement)}
	first := SecretRequirement{
		ID:            "first-identity",
		SuggestedName: "COLLIDING_DESTINATION",
		Replacement:   "{{secret.COLLIDING_DESTINATION}}",
		Path:          "first.yml",
	}
	second := first
	second.ID = "second-identity"
	second.Path = "second.yml"
	registerNativeGeneratedRequirement(state, first)
	registerNativeGeneratedRequirement(state, second)
	if state.generatedErr == nil ||
		!strings.Contains(state.generatedErr.Error(), "distinct credential requirements") {
		t.Fatalf("same-name identity collision error = %v", state.generatedErr)
	}
	stored := state.generatedReq[first.SuggestedName]
	if stored.ID != first.ID || len(stored.AffectedPaths) != 1 ||
		stored.AffectedPaths[0] != first.Path {
		t.Fatalf("collision mutated first registered requirement: %#v", stored)
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
			want: "123456789",
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
			want: "token=123456789",
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
			want: "https://example.test/123456789",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := Inspect(Input{
				Filename: "request.yml",
				Content:  []byte(test.content),
				Options:  InspectOptions{OutputMode: OutputModeNativeRequests},
			})
			if err != nil {
				t.Fatalf("error = %v", err)
			}
			actionJSON, err := json.Marshal(result.Scripts[0].Script.Actions[0])
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(actionJSON), test.want) || len(result.Scripts[0].SecretRequirements) != 0 {
				t.Fatalf("action = %s, requirements = %#v", actionJSON, result.Scripts[0].SecretRequirements)
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
	if !bytes.Contains(sanitized, []byte(literalFragment+"-{{missingPart}}")) {
		t.Fatalf("sanitized source altered authored template:\n%s", sanitized)
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
			if !strings.Contains(candidate.Script.Actions[0].Body, sourceCredential) ||
				strings.Contains(candidate.Script.Actions[0].Body, "{{secret.") ||
				!strings.Contains(candidate.Script.Actions[0].Body, `"safe":"retained"`) ||
				len(candidate.SecretRequirements) != 0 {
				t.Fatalf("credential-container candidate = %#v", candidate)
			}
			if !bytes.Contains(tarFile(t, result.SanitizedArchive, "request.yml"), []byte(sourceCredential)) {
				t.Fatal("sanitized source lost credential-container descendant")
			}
		})
	}
}

func TestInspectNativeTreatsSentinelWordsAsLiteralCredentials(t *testing.T) {
	tests := []struct {
		name       string
		source     string
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
			assertWire: func(t *testing.T, candidate NativeScriptCandidate) {
				t.Helper()
				auth := candidate.Script.Actions[0].Auth
				if auth == nil || auth.Password != "none" {
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
			assertWire: func(t *testing.T, candidate NativeScriptCandidate) {
				t.Helper()
				if candidate.Script.Actions[0].Body != `{"password":"null"}` {
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
			assertWire: func(t *testing.T, candidate NativeScriptCandidate) {
				t.Helper()
				if candidate.Script.Actions[0].Headers["X-API-Key"] != "inherit" {
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
			if len(candidate.SecretRequirements) != 0 {
				t.Fatalf("unexpected requirements for literal sentinel word: %#v", candidate.SecretRequirements)
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
	t.Run("unresolved composed header alias", func(t *testing.T) {
		source := `info: {name: Header alias, uid: header-alias, type: http}
headerSource: &headerSource "Bearer alias-header-literal-{{missingToken}}"
headerMirror: *headerSource
http:
  method: GET
  url: https://example.test
  headers:
    - name: Authorization
      value: *headerSource
`
		result, err := Inspect(Input{
			Filename: "request.yml",
			Content:  []byte(source),
			Options:  InspectOptions{OutputMode: OutputModeNativeRequests},
		})
		if err != nil {
			t.Fatal(err)
		}
		candidate := result.Scripts[0]
		if !strings.Contains(candidate.Script.Actions[0].Headers["Authorization"], "{{secret.") ||
			len(candidate.SecretRequirements) != 1 ||
			candidate.SecretRequirements[0].Value != "" {
			t.Fatalf("unresolved alias-backed candidate = %#v", candidate)
		}
		if !bytes.Equal(tarFile(t, result.SanitizedArchive, "request.yml"), []byte(source)) {
			t.Fatal("sanitized archive altered authored YAML alias source")
		}
	})

	t.Run("neutral variable dependency alias", func(t *testing.T) {
		source := `info: {name: Variable alias, uid: variable-alias, type: http}
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
`
		result, err := Inspect(Input{
			Filename: "request.yml",
			Content:  []byte(source),
			Options:  InspectOptions{OutputMode: OutputModeNativeRequests},
		})
		if err != nil {
			t.Fatal(err)
		}
		candidate := result.Scripts[0]
		if candidate.Script.Actions[0].Headers["Authorization"] != "Bearer alias-variable-literal" ||
			len(candidate.SecretRequirements) != 0 {
			t.Fatalf("resolved alias-backed candidate = %#v", candidate)
		}
		if !bytes.Equal(tarFile(t, result.SanitizedArchive, "request.yml"), []byte(source)) {
			t.Fatal("sanitized archive altered authored YAML alias source")
		}
	})
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
		name               string
		source             string
		candidateForbidden []string
		preserved          []string
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
			preserved: []string{"literal-json-alias-secret"},
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
			candidateForbidden: []string{"templated-json-alias-secret", "{{missingJSONAlias}}"},
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
			candidateForbidden: []string{"templated-xml-alias-secret", "{{missingXMLAlias}}"},
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
			preserved: []string{"literal-url-alias-secret"},
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
			candidateForbidden: []string{"templated-url-alias-secret", "{{missingURLAlias}}"},
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
			candidateForbidden: []string{"mapping-alias-secret", "{{missingMappingAlias}}"},
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
			candidateForbidden: []string{"sequence-alias-secret", "{{missingSequenceAlias}}"},
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
			if len(result.Scripts) != 1 {
				t.Fatalf("alias-backed result = %#v", result.Scripts)
			}
			if len(test.preserved) > 0 && len(result.Scripts[0].SecretRequirements) != 0 {
				t.Fatalf("literal alias generated requirements = %#v", result.Scripts[0].SecretRequirements)
			}
			if len(test.preserved) == 0 && len(result.Scripts[0].SecretRequirements) == 0 {
				t.Fatalf("unresolved alias missing requirements = %#v", result.Scripts[0])
			}
			sanitized := tarFile(t, result.SanitizedArchive, "request.yml")
			if !bytes.Equal(sanitized, []byte(test.source)) {
				t.Fatalf("sanitized archive altered authored YAML:\n%s", sanitized)
			}
			candidateJSON, err := json.Marshal(result.Scripts[0].Script)
			if err != nil {
				t.Fatal(err)
			}
			for _, forbidden := range test.candidateForbidden {
				if bytes.Contains(candidateJSON, []byte(forbidden)) {
					t.Fatalf("candidate retained unresolved alias template %q:\n%s", forbidden, candidateJSON)
				}
			}
			for _, preserved := range test.preserved {
				if !bytes.Contains(candidateJSON, []byte(preserved)) {
					t.Fatalf("candidate lost preserved value %q:\n%s", preserved, candidateJSON)
				}
			}
		})
	}
}

func TestInspectNativeScrubsMixedCaseYAMLAliasPeers(t *testing.T) {
	tests := []struct {
		name               string
		source             string
		candidateForbidden string
		preserved          string
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
			preserved: "MiXeD-Literal-Header-Secret",
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
			preserved: "MiXeD-Literal-JSON-Secret",
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
			candidateForbidden: "MiXeD-Projected-JSON-",
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
			preserved: "Bearer MiXeD-Projected-Variable-Secret",
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
			if len(result.Scripts) != 1 {
				t.Fatalf("mixed-case alias result = %#v", result.Scripts)
			}
			sanitized := tarFile(t, result.SanitizedArchive, "request.yml")
			if !bytes.Equal(sanitized, []byte(test.source)) {
				t.Fatalf("sanitized source altered authored YAML:\n%s", sanitized)
			}
			candidateJSON, err := json.Marshal(result.Scripts[0].Script)
			if err != nil {
				t.Fatal(err)
			}
			if test.preserved != "" {
				if len(result.Scripts[0].SecretRequirements) != 0 ||
					!bytes.Contains(candidateJSON, []byte(test.preserved)) {
					t.Fatalf("case-sensitive preserved value %q missing: %s (requirements=%#v)",
						test.preserved, candidateJSON, result.Scripts[0].SecretRequirements)
				}
			}
			if test.candidateForbidden != "" {
				if len(result.Scripts[0].SecretRequirements) != 1 ||
					bytes.Contains(candidateJSON, []byte(test.candidateForbidden)) {
					t.Fatalf("unresolved mixed-case alias candidate = %s (requirements=%#v)",
						candidateJSON, result.Scripts[0].SecretRequirements)
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
		want  string
	}{
		{name: "auth number", field: "auth", value: "42", want: `"auth":42`},
		{name: "credentials boolean", field: "credentials", value: "true", want: `"credentials":true`},
		{name: "secret number", field: "secret", value: "7", want: `"secret":7`},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := Inspect(Input{
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
			if err != nil {
				t.Fatalf("scalar container error = %v", err)
			}
			if got := result.Scripts[0].Script.Actions[0].Body; !strings.Contains(got, test.want) || len(result.Scripts[0].SecretRequirements) != 0 {
				t.Fatalf("scalar container body = %q, requirements = %#v", got, result.Scripts[0].SecretRequirements)
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
	if !strings.Contains(candidate.Script.Actions[0].Body, "literal-container-credential") ||
		strings.Contains(candidate.Script.Actions[0].Body, "{{secret.") ||
		!strings.Contains(candidate.Script.Actions[0].Body, `"auth":null`) ||
		len(candidate.SecretRequirements) != 0 {
		t.Fatalf("string/null container body = %q, requirements = %#v", candidate.Script.Actions[0].Body, candidate.SecretRequirements)
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
			actionJSON, err := json.Marshal(candidate.Script.Actions[0])
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(actionJSON), test.plaintext) || len(candidate.SecretRequirements) != 0 {
				t.Fatalf("decoded tagged credential missing or externalized: action=%s requirements=%#v",
					actionJSON, candidate.SecretRequirements)
			}
			if !bytes.Equal(tarFile(t, result.SanitizedArchive, "request.yml"), []byte(test.source)) {
				t.Fatal("sanitized source altered authored tagged YAML")
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

func TestInspectNativeResolvesDotEnvAndUnambiguousEnvironmentSecrets(t *testing.T) {
	dotEnv := "API_KEY=dotenv-api-key\nexport BEARER_TOKEN=\"dotenv-bearer-token\"\n"
	devEnv := `name: dev
variables:
  - name: dbPassword
    value: env-secret-password
    secret: true
  - name: baseHost
    value: api.example.test
`
	requestYAML := `info: {name: Env resolved request, uid: env-resolved-request, type: http, seq: 1}
http:
  method: POST
  url: "https://{{baseHost}}/v1/login?api_key={{API_KEY}}"
  headers:
    - {name: Authorization, value: "Bearer {{process.env.BEARER_TOKEN}}"}
  body:
    type: json
    data: '{"password":"{{dbPassword}}"}'
`
	result, err := Inspect(Input{
		Files: map[string][]byte{
			"demo/opencollection.yml":   []byte("opencollection: 1.0.0\ninfo: {name: Demo}\n"),
			"demo/.env":                 []byte(dotEnv),
			"demo/environments/dev.yml": []byte(devEnv),
			"demo/login.yml":            []byte(requestYAML),
		},
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Scripts) != 1 || len(result.SecretRequirements) != 0 {
		t.Fatalf("env resolved result = scripts(%d) secrets(%#v) missing(%#v)",
			len(result.Scripts), result.SecretRequirements, result.MissingVariables)
	}
	if len(result.Environments) != 2 ||
		result.Environments[0].Values["API_KEY"] != "dotenv-api-key" ||
		result.Environments[0].Values["BEARER_TOKEN"] != "dotenv-bearer-token" ||
		result.Environments[1].Values["baseHost"] != "api.example.test" ||
		result.Environments[1].Values["dbPassword"] != "env-secret-password" {
		t.Fatalf("environments = %#v, want .env and dev environment values preserved", result.Environments)
	}
	candidate := result.Scripts[0]
	action := candidate.Script.Actions[0]
	if action.Target != "https://{{var.baseHost}}/v1/login?api_key=dotenv-api-key" {
		t.Fatalf("action.Target = %q", action.Target)
	}
	if action.Headers["Authorization"] != "Bearer dotenv-bearer-token" {
		t.Fatalf("Authorization = %q", action.Headers["Authorization"])
	}
	if action.Body != `{"password":"env-secret-password"}` {
		t.Fatalf("action.Body = %q", action.Body)
	}
	if !bytes.Equal(tarFile(t, result.SanitizedArchive, "demo/.env"), []byte(dotEnv)) {
		t.Fatal("sanitized archive modified .env")
	}
	if !bytes.Equal(tarFile(t, result.SanitizedArchive, "demo/environments/dev.yml"), []byte(devEnv)) {
		t.Fatal("sanitized archive modified environments/dev.yml")
	}

	// Conflicting multi-environment values remain unset so environment selection stays meaningful.
	conflictResult, err := Inspect(Input{
		Files: map[string][]byte{
			"demo/opencollection.yml":    []byte("opencollection: 1.0.0\ninfo: {name: Demo}\n"),
			"demo/environments/dev.yml":  []byte("name: dev\nvariables:\n  - {name: apiToken, value: dev-token, secret: true}\n"),
			"demo/environments/prod.yml": []byte("name: prod\nvariables:\n  - {name: apiToken, value: prod-token, secret: true}\n"),
			"demo/call.yml": []byte(`info: {name: Call, uid: call-multi-env, type: http, seq: 1}
http:
  method: GET
  url: https://example.test/items
  headers:
    - {name: X-API-Key, value: "{{apiToken}}"}
`),
		},
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(conflictResult.SecretRequirements) != 1 || !conflictResult.SecretRequirements[0].UnresolvedSourceVariable {
		t.Fatalf("conflicting multi-environment credential requirements = %#v", conflictResult.SecretRequirements)
	}
}

func TestInspectNativePreservesDisabledCredentialsInArchiveWithoutActiveEmission(t *testing.T) {
	source := `info: {name: Disabled sensitive fields, uid: disabled-sensitive-fields, type: http}
http:
  method: GET
  url: https://example.test/items
  headers:
    - {name: Authorization, value: "Bearer disabled-header-secret", disabled: true}
    - {name: Accept, value: application/json}
  params:
    - {name: apiKey, type: query, value: disabled-query-secret, disabled: true}
`
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content:  []byte(source),
		Options:  InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := result.Scripts[0]
	action := candidate.Script.Actions[0]
	if _, exists := action.Headers["Authorization"]; exists {
		t.Fatalf("disabled Authorization header emitted on wire: %#v", action.Headers)
	}
	if action.Target != "https://example.test/items" || len(candidate.SecretRequirements) != 0 {
		t.Fatalf("disabled query/header candidate = target %q requirements %#v", action.Target, candidate.SecretRequirements)
	}
	sanitized := tarFile(t, result.SanitizedArchive, "request.yml")
	if !bytes.Equal(sanitized, []byte(source)) {
		t.Fatalf("disabled fields were scrubbed in archive:\n%s", sanitized)
	}
}

func TestInspectNativeResolvesCredentialArrayElementsFromEnvironmentAndGovernsMissing(t *testing.T) {
	result, err := Inspect(Input{
		Files: map[string][]byte{
			"demo/opencollection.yml": []byte("opencollection: 1.0.0\ninfo: {name: Demo}\n"),
			"demo/environments/prod.yml": []byte(`name: prod
variables:
  - name: plain_var
    value: resolved-array-secret
`),
			"demo/login.yml": []byte(`info: {name: Array Creds, uid: array-creds, type: http, seq: 1}
http:
  method: POST
  url: https://example.test/login
  body:
    type: json
    data: '{"passwords":["{{plain_var}}"],"apiKeys":["{{missing_var}}"]}'
`),
		},
		Options: InspectOptions{
			OutputMode: OutputModeNativeRequests,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := result.Scripts[0]
	if len(candidate.MissingVariables) != 0 {
		t.Fatalf("expected zero MissingVariables, got %#v", candidate.MissingVariables)
	}
	action := candidate.Script.Actions[0]
	if !strings.Contains(action.Body, `"passwords":["resolved-array-secret"]`) {
		t.Fatalf("expected passwords array element resolved from prod.yml, got %s", action.Body)
	}
	if !strings.Contains(action.Body, `"apiKeys":["{{secret.`) {
		t.Fatalf("expected missing apiKeys array element governed as {{secret.*}}, got %s", action.Body)
	}
	if len(candidate.SecretRequirements) != 1 || !candidate.SecretRequirements[0].UnresolvedSourceVariable {
		t.Fatalf("expected 1 unresolved SecretRequirement for missing_var, got %#v", candidate.SecretRequirements)
	}
}
