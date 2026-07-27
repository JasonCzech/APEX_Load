package bruno

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestInspectNativeScrubbedAttachmentShapeProducesSixteenScripts(t *testing.T) {
	files := map[string][]byte{
		"CTA/opencollection.yml": []byte("opencollection: 1.0.0\ninfo: {name: Scrubbed CTA, uid: scrubbed-cta}\n"),
		"CTA/apis/folder.yaml": []byte(`info: {name: APIs, uid: api-folder, seq: 1}
request:
  headers:
    - {name: X-Disabled, value: "{{disabledCredential}}", disabled: true}
  auth: inherit
`),
	}
	const hook = `runtime:
  scripts:
    - type: after-response
      code: |
        test("status", function () {
          expect(res.getStatus()).to.equal(500);
        });
        test("content type", function () {
          expect(res.getHeader("content-type".toLowerCase())).to.include("text/xml");
        });
        test("fields", function () {
          const body = res.getBody();
          const codeMatch = body.match(/<code>(.*?)<\/code>/);
          const descriptionMatch = body.match(/<description>(.*?)<\/description>/);
          const faultCodeMatch = body.match(/<faultCode>(.*?)<\/faultCode>/);
          const faultDescriptionMatch = body.match(/<faultDescription>(.*?)<\/faultDescription>/);
          const code = codeMatch ? codeMatch[1] : null;
          const description = descriptionMatch ? descriptionMatch[1] : null;
          const faultCode = faultCodeMatch ? faultCodeMatch[1] : null;
          const faultDescription = faultDescriptionMatch ? faultDescriptionMatch[1] : null;
          expect(code).to.equal("500");
          expect(description).to.equal("scrubbed failure");
          expect(faultCode).to.equal("SCRUBBED");
          expect(faultDescription).to.equal("scrubbed failure");
        });
`
	for sequence := 16; sequence >= 1; sequence-- {
		name := fmt.Sprintf("API_%02d", sequence)
		body := fmt.Sprintf(`<SOAP-ENV:Envelope xmlns:SOAP-ENV="http://schemas.xmlsoap.org/soap/envelope/"><SOAP-ENV:Body><%sRequest>fixture-%02d</%sRequest></SOAP-ENV:Body></SOAP-ENV:Envelope>`, name, sequence, name)
		files[fmt.Sprintf("CTA/apis/%s.yml", name)] = []byte(fmt.Sprintf(`info: {name: %s, uid: uid-%02d, type: http, seq: %d}
http:
  method: POST
  url: https://example.test/%02d
  headers:
    - {name: Content-Type, value: text/xml}
  body:
    type: xml
    data: '%s'
settings: {encodeUrl: true, timeout: 0, followRedirects: true, maxRedirects: 5}
%s`, name, sequence, sequence, sequence, body, hook))
	}

	result, err := Inspect(Input{
		ProjectID: "project-a",
		Files:     files,
		Options:   InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(result.Scripts) != 16 {
		t.Fatalf("native scripts = %d, want 16", len(result.Scripts))
	}
	for index, candidate := range result.Scripts {
		sequence := index + 1
		wantName := fmt.Sprintf("API_%02d", sequence)
		wantPath := fmt.Sprintf("apis/%s.yml", wantName)
		if candidate.Sequence != sequence || candidate.SourcePath != wantPath || candidate.Script == nil {
			t.Fatalf("candidate[%d] = %#v", index, candidate)
		}
		if candidate.Script.Protocol != "http" || len(candidate.Script.Actions) != 1 {
			t.Fatalf("script[%d] = %#v", index, candidate.Script)
		}
		action := candidate.Script.Actions[0]
		wantBody := fmt.Sprintf(`<SOAP-ENV:Envelope xmlns:SOAP-ENV="http://schemas.xmlsoap.org/soap/envelope/"><SOAP-ENV:Body><%sRequest>fixture-%02d</%sRequest></SOAP-ENV:Body></SOAP-ENV:Envelope>`, wantName, sequence, wantName)
		if action.Type != "POST" || action.Method != "POST" || action.Body != wantBody {
			t.Fatalf("action[%d] = %#v, want raw body %q", index, action, wantBody)
		}
		if len(action.Assertions) != 6 || action.TimeoutMs != nativeMaxTimeoutMS ||
			action.MaxRedirects == nil || *action.MaxRedirects != 5 {
			t.Fatalf("action[%d] assertions/settings = %#v", index, action)
		}
		for _, header := range action.HeaderList {
			if strings.EqualFold(strings.TrimSpace(header.Name), "SOAPAction") {
				t.Fatalf("action[%d] synthesized SOAPAction header: %#v", index, action.HeaderList)
			}
		}
		if !hasNativeDiagnostic(candidate.Diagnostics, "bruno_unbounded_timeout_clamped") {
			t.Fatalf("candidate[%d] diagnostics = %#v", index, candidate.Diagnostics)
		}
		for _, importedAction := range candidate.Script.Actions {
			if importedAction.Type == "bruno_run" {
				t.Fatalf("script[%d] contains bruno_run: %#v", index, candidate.Script.Actions)
			}
		}
	}
}

func TestInspectNativeAttachedShapeProducesExactHTTPAssertions(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "attached", "ExecuteUnifiedCreditPolicyCheck_v246.yml"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := Inspect(Input{
		Filename:  "ExecuteUnifiedCreditPolicyCheck_v246.yml",
		Content:   source,
		ProjectID: "project-a",
		Options:   InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if result.OutputMode != OutputModeNativeRequests || result.Script != nil || len(result.Scripts) != 1 {
		t.Fatalf("native result = %#v", result)
	}
	candidate := result.Scripts[0]
	if candidate.SourcePath != "ExecuteUnifiedCreditPolicyCheck_v246.yml" ||
		candidate.Sequence != 10 || candidate.Method != "POST" ||
		candidate.Script == nil || candidate.Script.Protocol != "http" {
		t.Fatalf("candidate = %#v", candidate)
	}
	if candidate.Script.GeneratedBy != nativeImporterVersion ||
		candidate.Script.GeneratedFrom != "open_collection_yaml" ||
		len(candidate.Script.Actions) != 1 {
		t.Fatalf("script = %#v", candidate.Script)
	}
	action := candidate.Script.Actions[0]
	if action.Type != "POST" || action.Method != "POST" ||
		!strings.Contains(action.Target, "{{var.env}}") ||
		!strings.Contains(action.Body, "<SOAP-ENV:Envelope") {
		t.Fatalf("native HTTP action = %#v", action)
	}
	if action.TimeoutMs != nativeMaxTimeoutMS || action.MaxRedirects == nil || *action.MaxRedirects != 5 {
		t.Fatalf("timeout/redirects = %d/%v", action.TimeoutMs, action.MaxRedirects)
	}
	if candidate.Script.Config.HTTP.Traceparent == nil || *candidate.Script.Config.HTTP.Traceparent ||
		candidate.Script.Config.HTTP.ClearCookiesPerIter == nil || !*candidate.Script.Config.HTTP.ClearCookiesPerIter {
		t.Fatalf("native HTTP config = %#v", candidate.Script.Config.HTTP)
	}
	if len(action.Assertions) != 6 {
		t.Fatalf("assertions = %#v", action.Assertions)
	}
	assertionTypes := make([]string, 0, len(action.Assertions))
	for _, assertion := range action.Assertions {
		assertionTypes = append(assertionTypes, assertion.Type)
	}
	if got := strings.Join(assertionTypes, ","); got != "status_code,header_contains,body_regex_capture_equals,body_regex_capture_equals,body_regex_capture_equals,body_regex_capture_equals" {
		t.Fatalf("assertion types = %q", got)
	}
	if action.Assertions[2].Value != `<code>([^\r\n\x{2028}\x{2029}]*?)</code>` || action.Assertions[2].Expected != "500" {
		t.Fatalf("first regex assertion = %#v", action.Assertions[2])
	}
	if action.Metadata["review_required"] != nil {
		t.Fatalf("known static hook was review-gated: %#v", action.Metadata)
	}
	if !hasNativeDiagnostic(candidate.Diagnostics, "bruno_unbounded_timeout_clamped") {
		t.Fatalf("diagnostics = %#v", candidate.Diagnostics)
	}
}

func TestInspectNativeAppliesPathAndQueryParameters(t *testing.T) {
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Parameters, uid: parameters, type: http}
variables:
  - {name: filterValue, value: active}
http:
  method: GET
  url: https://example.test/users/:id?keep=yes&filter=stale&disabled=stale#fragment
  params:
    - {name: id, value: "a/b", type: path}
    - {name: filter, value: "{{filterValue}}", type: query}
    - {name: disabled, value: ignored, type: query, disabled: true}
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := result.Scripts[0]
	target := candidate.Script.Actions[0].Target
	if target != "https://example.test/users/a%2Fb?keep=yes&filter={{param.filterValue}}#fragment" {
		t.Fatalf("parameterized target = %q", target)
	}
	if !hasNativeDiagnostic(candidate.Diagnostics, "bruno_url_template_encoding_review_required") ||
		candidate.Script.Actions[0].Metadata["review_gate"] != "bruno_url_template_encoding_review_required" {
		t.Fatalf("templated URL component was not review-gated: %#v", candidate)
	}
}

func TestInspectNativeGatesTemplatesEmbeddedInURLComponents(t *testing.T) {
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: URL template, uid: url-template, type: http}
variables:
  - {name: id, value: "a/b"}
  - {name: query, value: "first&admin=true"}
http:
  method: GET
  url: "https://example.test/users/{{id}}?q={{query}}"
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := result.Scripts[0]
	if !hasNativeDiagnostic(candidate.Diagnostics, "bruno_url_template_encoding_review_required") {
		t.Fatalf("URL diagnostics = %#v", candidate.Diagnostics)
	}
	if candidate.Script.Actions[0].Metadata["promotion_blocker"] != true {
		t.Fatalf("URL action metadata = %#v", candidate.Script.Actions[0].Metadata)
	}
}

func TestInspectNativeGatesAuthoredAPEXVarURLComponents(t *testing.T) {
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: APEX URL variable, uid: apex-url-variable, type: http}
http:
  method: GET
  url: "https://example.test/users/{{var.id}}"
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := result.Scripts[0]
	if len(candidate.MissingVariables) != 0 {
		t.Fatalf("authored APEX variable was marked missing: %#v", candidate.MissingVariables)
	}
	if !hasNativeDiagnostic(candidate.Diagnostics, "bruno_url_template_encoding_review_required") ||
		candidate.Script.Actions[0].Metadata["promotion_blocker"] != true {
		t.Fatalf("runtime-capable URL variable was not encoding-gated: %#v", candidate)
	}
}

func TestInspectNativeGatesTemplatesInQueryParameterNames(t *testing.T) {
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Query name template, uid: query-name-template, type: http}
variables:
  - {name: queryName, value: dynamic}
http:
  method: GET
  url: https://example.test/search
  params:
    - {name: "{{queryName}}", value: fixed, type: query}
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := result.Scripts[0]
	if !hasNativeDiagnostic(candidate.Diagnostics, "bruno_url_template_encoding_review_required") ||
		candidate.Script.Actions[0].Metadata["promotion_blocker"] != true {
		t.Fatalf("templated query name was not review-gated: %#v", candidate)
	}
}

func TestInspectNativeGatesMultipartRuntimeTemplates(t *testing.T) {
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Multipart template, uid: multipart-template, type: http}
variables:
  - {name: fieldValue, value: "line one\r\n--apexload-injected"}
http:
  method: POST
  url: https://example.test/upload
  body:
    type: multipart-form
    data:
      - {name: description, value: "{{fieldValue}}"}
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := result.Scripts[0]
	if !hasNativeDiagnostic(candidate.Diagnostics, "bruno_multipart_template_review_required") ||
		candidate.Script.Actions[0].Metadata["review_gate"] != "bruno_multipart_template_review_required" {
		t.Fatalf("multipart template was not review-gated: %#v", candidate)
	}
}

func TestInspectNativeReportsMultipartTemplatesAndOmittedFilesTogether(t *testing.T) {
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Multipart combined review, uid: multipart-combined, type: http}
variables:
  - {name: fieldValue, value: dynamic}
http:
  method: POST
  url: https://example.test/upload
  body:
    type: multipart-form
    data:
      - {name: description, value: "{{fieldValue}}"}
      - {name: attachment, value: ./payload.bin, type: file}
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := result.Scripts[0]
	if !hasNativeDiagnostic(candidate.Diagnostics, "bruno_multipart_template_and_file_review_required") ||
		candidate.Script.Actions[0].Metadata["review_gate"] !=
			"bruno_multipart_template_and_file_review_required" {
		t.Fatalf("combined multipart loss was not fully reported: %#v", candidate)
	}
}

func TestInspectNativeSerializesStructuredHTTPBodies(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantBody    string
		contentType string
	}{
		{
			name:        "json object",
			body:        "type: json\n    data: {name: Ada, count: 2}",
			wantBody:    `{"count":2,"name":"Ada"}`,
			contentType: "application/json",
		},
		{
			name: "form urlencoded",
			body: `type: form-urlencoded
    data:
      - {name: username, value: "john doe"}
      - {name: ignored, value: nope, disabled: true}
      - {name: empty, value: ""}`,
			wantBody:    "username=john%20doe&empty=",
			contentType: "application/x-www-form-urlencoded",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := Inspect(Input{
				Filename: "request.yml",
				Content: []byte(`info: {name: Body, uid: body, type: http}
http:
  method: POST
  url: https://example.test
  body:
    ` + test.body + "\n"),
				Options: InspectOptions{OutputMode: OutputModeNativeRequests},
			})
			if err != nil {
				t.Fatal(err)
			}
			action := result.Scripts[0].Script.Actions[0]
			if action.Body != test.wantBody || action.Headers["Content-Type"] != test.contentType {
				t.Fatalf("body action = %#v", action)
			}
		})
	}
}

func TestInspectNativeRejectsNonYAMLRequestFiles(t *testing.T) {
	_, err := Inspect(Input{
		Files: map[string][]byte{
			"opencollection.yml": []byte(`opencollection: 1.0.0
info: {name: JSON request}
`),
			"request.json": []byte(`{
  "info": {"name": "JSON request", "uid": "json-request", "type": "http", "seq": 1},
  "http": {"method": "GET", "url": "https://example.test?token=literal-secret"}
}`),
		},
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported extension") {
		t.Fatalf("non-YAML native request error = %v", err)
	}
}

func TestInspectNativeRejectsYAMLMergeKeys(t *testing.T) {
	_, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`defaults: &defaults {timeout: 10}
info: {name: Merge, uid: merge, type: http}
http: {method: GET, url: https://example.test}
settings:
  <<: *defaults
  Timeout: 20
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err == nil || !strings.Contains(err.Error(), "YAML merge key") {
		t.Fatalf("merge-key error = %v", err)
	}
}

func TestTranslateNativeAfterResponseRejectsCrossCategoryRedeclaration(t *testing.T) {
	_, recognized := translateNativeAfterResponse(`test("invalid", function () {
  const body = res.getBody();
  const body = body.match(/<code>(.*?)<\/code>/);
  const code = body ? body[1] : null;
  expect(code).to.equal("500");
});`)
	if recognized {
		t.Fatal("invalid JavaScript redeclaration was translated")
	}
}

func TestReplaceNativeColonPathParameterRequiresACompletePlaceholder(t *testing.T) {
	tests := []struct {
		name        string
		target      string
		parameter   string
		want        string
		wantMatched bool
	}{
		{name: "segment", target: "https://example.test/users/:id", parameter: "id", want: "https://example.test/users/42", wantMatched: true},
		{name: "identifier prefix", target: "https://example.test/users/:identifier", parameter: "id", want: "https://example.test/users/:identifier"},
		{name: "unicode identifier prefix", target: "https://example.test/users/:idé", parameter: "id", want: "https://example.test/users/:idé"},
		{name: "port", target: "https://example.test:8080/users", parameter: "8080", want: "https://example.test:8080/users"},
		{name: "suffix delimiter", target: "https://example.test/users/:id.json", parameter: "id", want: "https://example.test/users/42.json", wantMatched: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, matched := replaceNativeColonPathParameter(test.target, test.parameter, "42")
			if got != test.want || matched != test.wantMatched {
				t.Fatalf("replace = %q, %t; want %q, %t", got, matched, test.want, test.wantMatched)
			}
		})
	}
}

func TestTranslateNativeJSRegexPreservesWhitespaceAndWildcardSemantics(t *testing.T) {
	translated, ok := translateNativeJSRegexToRE2(`(\s+)`)
	if !ok {
		t.Fatal("compatible JavaScript regex was rejected")
	}
	compiled, err := regexp.Compile(translated)
	if err != nil {
		t.Fatal(err)
	}
	if !compiled.MatchString("\u00a0xvalue") {
		t.Fatalf("translated whitespace did not match JavaScript non-breaking space: %q", translated)
	}
	wildcard, ok := translateNativeJSRegexToRE2(`(a.*?b)`)
	if !ok {
		t.Fatal("compatible repeated JavaScript wildcard was rejected")
	}
	wildcardRE := regexp.MustCompile(wildcard)
	if wildcardRE.MatchString("a\rb") || wildcardRE.MatchString("a\u2028b") {
		t.Fatalf("translated wildcard accepted a JavaScript line terminator: %q", wildcard)
	}
	for _, unsupported := range []string{
		`^(value)`,
		`(value)$`,
		`(\p{L}+)`,
		`(\1)`,
		`(\a)`,
		`((?s).*)`,
		`(a.b)`,
	} {
		if _, compatible := translateNativeJSRegexToRE2(unsupported); compatible {
			t.Fatalf("unsupported JavaScript regex %q was accepted", unsupported)
		}
	}
}

func TestInspectNativeExcludesDisabledAndBasicShadowedNonStringCredentials(t *testing.T) {
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Excluded credentials, uid: excluded-credentials, type: http}
variables:
  - {name: password, value: 12345, disabled: true}
http:
  method: POST
  url: https://example.test/users
  headers:
    - {name: Authorization, value: 12345}
    - {name: X-API-Key, value: 67890, disabled: true}
  params:
    - {name: apiKey, type: query, value: 12345, disabled: true}
  body:
    type: form-urlencoded
    data:
      - {name: password, value: 12345, disabled: true}
  auth:
    type: basic
    username: ""
    password: ""
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatalf("excluded credentials were type-rejected: %v", err)
	}
	if len(result.Scripts) != 1 {
		t.Fatalf("scripts = %#v", result.Scripts)
	}
	action := result.Scripts[0].Script.Actions[0]
	for _, header := range action.HeaderList {
		if strings.EqualFold(header.Name, "Authorization") || strings.EqualFold(header.Name, "X-API-Key") {
			t.Fatalf("excluded header executed: %#v", action.Headers)
		}
	}
	if len(result.Scripts[0].SecretRequirements) != 0 || len(result.SecretRequirements) != 0 {
		t.Fatalf("excluded credentials created requirements: candidate=%#v aggregate=%#v", result.Scripts[0].SecretRequirements, result.SecretRequirements)
	}
}

func TestInspectNativeValidatesOnlyEffectiveCredentialTypes(t *testing.T) {
	result, err := Inspect(Input{
		Files: map[string][]byte{
			"demo/opencollection.yml": []byte(`opencollection: 1.0.0
info: {name: Effective credentials, uid: effective-credentials}
request:
  variables:
    password: 12345
  auth:
    type: basic
    username: 12345
    password: 67890
`),
			"demo/request.yml": []byte(`info: {name: Child, uid: child, type: http}
variables:
  password: child-value
http:
  method: GET
  url: https://example.test
  auth: noauth
`),
		},
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatalf("shadowed ancestor credentials were type-rejected: %v", err)
	}
	if len(result.Scripts) != 1 || result.Scripts[0].Script.Actions[0].Auth != nil {
		t.Fatalf("native result = %#v", result.Scripts)
	}

	_, err = Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Active, uid: active, type: http}
variables:
  password: 12345
http:
  method: GET
  url: https://example.test/{{password}}
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err == nil || !strings.Contains(err.Error(), "effective variable") {
		t.Fatalf("active non-string credential error = %v", err)
	}
}

func TestInspectNativeMergesFolderDefaultsAndAuthPrecedence(t *testing.T) {
	result, err := Inspect(Input{
		ProjectID: "project-a",
		Files: map[string][]byte{
			"demo/OpenCollection.YAML": []byte(`opencollection: 1.0.0
info: {name: Demo, uid: demo}
request:
  headers:
    - {name: X-Root, value: root}
  auth:
    type: basic
    username: root-user
    password: root-password
`),
			"demo/apis/FOLDER.YAML": []byte(`info: {name: APIs, uid: folder-apis, seq: 1}
request:
  headers:
    - {name: X-Root, value: folder}
    - {name: X-Disabled, value: do-not-send, disabled: true}
    - {name: X-Case, value: first}
    - {name: x-case, value: second}
  auth: inherit
`),
			"demo/apis/call.yml": []byte(`info: {name: Call, uid: call-uid, type: http, seq: 2}
http:
  method: POST
  url: https://example.test/call
  headers:
    - {name: X-Request, value: request}
    - {name: Authorization, value: "{{auth}}"}
  body: {type: text, data: payload}
  auth:
    type: basic
    username: "{{username}}"
    password: "{{password}}"
settings: {followRedirects: false, timeout: 50}
`),
		},
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if result.RootPath != "demo" || len(result.Scripts) != 1 {
		t.Fatalf("root/scripts = %q/%d", result.RootPath, len(result.Scripts))
	}
	candidate := result.Scripts[0]
	action := candidate.Script.Actions[0]
	if action.Headers["X-Root"] != "folder" || action.Headers["X-Request"] != "request" {
		t.Fatalf("merged headers = %#v", action.HeaderList)
	}
	if _, exists := action.Headers["X-Disabled"]; exists {
		t.Fatalf("disabled inherited header was executable: %#v", action.HeaderList)
	}
	for _, header := range action.HeaderList {
		if strings.EqualFold(header.Name, "Authorization") {
			t.Fatalf("Basic auth did not shadow Authorization: %#v", action.HeaderList)
		}
	}
	if action.Auth == nil || action.Auth.Type != "basic" ||
		action.Auth.Username != "{{secret.USERNAME}}" ||
		action.Auth.Password != "{{secret.PASSWORD}}" {
		t.Fatalf("Basic auth = %#v", action.Auth)
	}
	if action.MaxRedirects == nil || *action.MaxRedirects != 0 || action.TimeoutMs != 50 {
		t.Fatalf("settings = %#v", action)
	}
	if !hasNativeDiagnostic(candidate.Diagnostics, "bruno_authorization_header_shadowed") ||
		!hasNativeDiagnostic(candidate.Diagnostics, "bruno_header_case_collision") {
		t.Fatalf("diagnostics = %#v", candidate.Diagnostics)
	}
	for _, requirement := range candidate.SecretRequirements {
		if requirement.Key == "auth" || strings.Contains(strings.ToLower(requirement.Key), "disabled") {
			t.Fatalf("shadowed/disabled value created a requirement: %#v", candidate.SecretRequirements)
		}
	}
}

func TestInspectNativeOrdersNestedRequestsAndRejectsAmbiguousMetadata(t *testing.T) {
	files := map[string][]byte{
		"demo/opencollection.yaml": []byte("info: {name: Demo}\n"),
		"demo/z/folder.yml":        []byte("info: {name: Z, seq: 2}\n"),
		"demo/z/first.yml":         []byte("info: {name: First, type: http, seq: 1}\nhttp: {method: GET, url: https://z.test}\n"),
		"demo/a/folder.yaml":       []byte("info: {name: A, seq: 1}\n"),
		"demo/a/second.yml":        []byte("info: {name: Second, type: http, seq: 9}\nhttp: {method: GET, url: https://a.test}\n"),
	}
	result, err := Inspect(Input{Files: files, Options: InspectOptions{OutputMode: OutputModeNativeRequests}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Scripts) != 2 ||
		result.Scripts[0].SourcePath != "a/second.yml" ||
		result.Scripts[1].SourcePath != "z/first.yml" {
		t.Fatalf("native order = %#v", result.Scripts)
	}

	files["demo/a/folder.yml"] = []byte("info: {name: duplicate}\n")
	_, err = Inspect(Input{Files: files, Options: InspectOptions{OutputMode: OutputModeNativeRequests}})
	if err == nil || !strings.Contains(err.Error(), "ambiguous Bruno folder metadata") {
		t.Fatalf("ambiguous metadata error = %v", err)
	}
}

func TestInspectNativeUnsupportedJavaScriptCreatesAcknowledgedDraftGate(t *testing.T) {
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Dynamic, type: http}
http: {method: GET, url: https://example.test}
runtime:
  scripts:
    - type: after-response
      code: |
        test("dynamic", function () {
          bru.setVar("token", res.getBody());
        });
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := result.Scripts[0]
	if candidate.Fidelity != "review_required" {
		t.Fatalf("fidelity = %q", candidate.Fidelity)
	}
	action := candidate.Script.Actions[0]
	if action.Metadata["review_gate"] != "bruno_javascript" ||
		action.Metadata["review_required"] != true ||
		action.Metadata["promotion_blocker"] != true {
		t.Fatalf("review metadata = %#v", action.Metadata)
	}
	var diagnostic *NativeDiagnostic
	for index := range candidate.Diagnostics {
		if candidate.Diagnostics[index].Code == "bruno_javascript_review_required" {
			diagnostic = &candidate.Diagnostics[index]
			break
		}
	}
	if diagnostic == nil || diagnostic.ID == "" || diagnostic.FindingID != diagnostic.ID ||
		!diagnostic.AcknowledgementRequired {
		t.Fatalf("review diagnostic = %#v", diagnostic)
	}
	if len(result.SecurityFindings) == 0 {
		t.Fatal("review finding was not promoted to the inspect result")
	}
}

func TestInspectNativeRejectsCanonicalOnlyOptions(t *testing.T) {
	source := []byte("info: {name: Request, type: http}\nhttp: {method: GET, url: https://example.test}\n")
	tests := []struct {
		name   string
		mutate func(*Input)
	}{
		{name: "singular name", mutate: func(input *Input) { input.Name = "Combined" }},
		{name: "sandbox", mutate: func(input *Input) { input.Sandbox = "safe" }},
		{name: "selection", mutate: func(input *Input) { input.Options.Selection = "folder" }},
		{name: "tests only", mutate: func(input *Input) { input.Options.TestsOnly = true }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := Input{
				Filename: "request.yml",
				Content:  source,
				Options:  InspectOptions{OutputMode: OutputModeNativeRequests},
			}
			test.mutate(&input)
			if _, err := Inspect(input); err == nil {
				t.Fatal("Inspect unexpectedly accepted canonical-only native options")
			}
		})
	}
}

func TestTranslateNativeAfterResponseRejectsRegexFlagsAndResidualStatements(t *testing.T) {
	tests := []struct {
		name string
		code string
	}{
		{name: "regex flags", code: `test("flagged", function () {
			const body = res.getBody();
			const codeMatch = body.match(/<code>(.*?)<\/code>/i);
			const code = codeMatch ? codeMatch[1] : null;
			expect(code).to.equal("500");
		});`},
		{name: "mutation", code: `test("mutation", function () {
			expect(res.getStatus()).to.equal(500);
			bru.setVar("sideEffect", "dropped");
		});`},
		{name: "statement outside test", code: `const body = res.getBody();
		test("status", function () {
			expect(res.getStatus()).to.equal(500);
		});`},
		{name: "unused assignment", code: `test("status", function () {
			const body = res.getBody();
			expect(res.getStatus()).to.equal(500);
			});`},
		{name: "match one without capture group", code: `test("capture", function () {
			const body = res.getBody();
			const codeMatch = body.match(/status/);
			const code = codeMatch ? codeMatch[1] : null;
			expect(code).to.equal("status");
			});`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if assertions, ok := translateNativeAfterResponse(test.code); ok {
				t.Fatalf("unsafe JavaScript was translated: %#v", assertions)
			}
		})
	}
}

func TestInspectNativeNoCaptureRegexCreatesJavaScriptReviewGate(t *testing.T) {
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: No Capture, uid: no-capture, type: http}
http: {method: GET, url: https://example.test}
runtime:
  scripts:
    - type: tests
      code: |
        test("capture", function () {
          const body = res.getBody();
          const statusMatch = body.match(/status/);
          const status = statusMatch ? statusMatch[1] : null;
          expect(status).to.equal("status");
        });
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := result.Scripts[0]
	action := candidate.Script.Actions[0]
	if len(action.Assertions) != 0 ||
		action.Metadata["review_gate"] != "bruno_javascript" ||
		action.Metadata["review_required"] != true ||
		action.Metadata["promotion_blocker"] != true ||
		!hasNativeDiagnostic(candidate.Diagnostics, "bruno_javascript_review_required") {
		t.Fatalf("no-capture match[1] did not fail closed: action=%#v diagnostics=%#v", action, candidate.Diagnostics)
	}
}

func TestInspectNativeHeaderInheritanceAndBrunoDefaults(t *testing.T) {
	result, err := Inspect(Input{
		Files: map[string][]byte{
			"demo/opencollection.yml": []byte(`info: {name: Demo}
request:
  headers:
    - {name: X-Shared, value: collection}
    - {name: X-Stable, value: stable}
`),
			"demo/apis/folder.yaml": []byte(`info: {name: Demo Folder}
request:
  headers:
    - {name: x-shared, value: folder-first}
    - {name: X-SHARED, value: folder-second}
`),
			"demo/apis/request.yml": []byte(`info: {name: Request, uid: request-uid, type: http}
http: {method: GET, url: https://example.test}
`),
		},
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	action := result.Scripts[0].Script.Actions[0]
	if action.TimeoutMs != nativeDefaultTimeoutMS ||
		action.MaxRedirects == nil || *action.MaxRedirects != nativeDefaultRedirects {
		t.Fatalf("Bruno defaults = timeout %d, redirects %v", action.TimeoutMs, action.MaxRedirects)
	}
	if len(action.HeaderList) != 3 ||
		action.HeaderList[0].Name != "X-Stable" ||
		action.HeaderList[1].Name != "x-shared" ||
		action.HeaderList[1].Value != "folder-first" ||
		action.HeaderList[2].Name != "X-SHARED" ||
		action.HeaderList[2].Value != "folder-second" {
		t.Fatalf("case-insensitive inheritance/same-layer order = %#v", action.HeaderList)
	}
	preserved := result.Scripts[0].Script.Metadata.Import.Preserved
	if preserved["collection_sha256"] == "" || preserved["source_sha256"] == "" {
		t.Fatalf("compact provenance hashes = %#v", preserved)
	}
	effectiveFields, ok := preserved["effective_fields"].(map[string]any)
	if !ok || effectiveFields["timeout_ms"] != nativeDefaultTimeoutMS ||
		effectiveFields["max_redirects"] != nativeDefaultRedirects {
		t.Fatalf("effective-field provenance = %#v", preserved["effective_fields"])
	}
	ancestors, ok := preserved["ancestor_folders"].([]map[string]any)
	if !ok || len(ancestors) != 1 || ancestors[0]["sha256"] == "" {
		t.Fatalf("ancestor provenance = %#v", preserved["ancestor_folders"])
	}
}

func TestInspectNativeFlattensVariableDependenciesAndBlocksCycles(t *testing.T) {
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Variables, uid: variables-uid, type: http}
variables:
  - {name: scheme, value: https}
  - {name: host, value: example.test}
  - {name: base, value: "{{scheme}}://{{host}}"}
  - {name: endpoint, value: "{{base}}/v1"}
  - {name: cycle_a, value: "{{cycle_b}}"}
  - {name: cycle_b, value: "{{cycle_a}}"}
http:
  method: GET
  url: "{{endpoint}}/{{unresolved}}"
  headers:
    - {name: X-Cycle, value: "{{cycle_a}}"}
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := result.Scripts[0]
	action := candidate.Script.Actions[0]
	if action.Target != "{{param.endpoint}}/{{var.unresolved}}" {
		t.Fatalf("flattened target = %q", action.Target)
	}
	if len(candidate.Script.Parameters) != 1 ||
		candidate.Script.Parameters[0].Name != "endpoint" ||
		len(candidate.Script.Parameters[0].Values) != 1 ||
		candidate.Script.Parameters[0].Values[0] != "https://example.test/v1" {
		t.Fatalf("flattened parameters = %#v", candidate.Script.Parameters)
	}
	if got := action.Headers["X-Cycle"]; got != "{{var.cycle_a}}" {
		t.Fatalf("cycle header = %q", got)
	}
	if got := strings.Join(candidate.MissingVariables, ","); got != "cycle_a,cycle_b,unresolved" {
		t.Fatalf("missing variables = %q", got)
	}
	if action.Metadata["execution_blocker"] != "bruno_missing_variables" {
		t.Fatalf("missing-variable blocker = %#v", action.Metadata)
	}
	metadataMissing, ok := action.Metadata["missing_variables"].([]string)
	if !ok || strings.Join(metadataMissing, ",") != "cycle_a,cycle_b,unresolved" {
		t.Fatalf("metadata missing variables = %#v", action.Metadata["missing_variables"])
	}
	if action.Metadata["review_required"] != nil || action.Metadata["promotion_blocker"] != nil {
		t.Fatalf("missing variables unexpectedly became an acknowledgement gate: %#v", action.Metadata)
	}
}

func TestInspectNativeTreatsNoncanonicalNamespaceAsBrunoVariable(t *testing.T) {
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Namespace casing, uid: namespace-casing, type: http}
variables:
  - {name: ENV.URL, value: https://example.test}
http:
  method: GET
  url: "{{ENV.URL}}/health"
  headers:
    - {name: X-Unresolved, value: "{{PARAM.MISSING}}"}
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := result.Scripts[0]
	action := candidate.Script.Actions[0]
	if action.Target != "{{param.ENV.URL}}/health" ||
		len(candidate.Script.Parameters) != 1 ||
		candidate.Script.Parameters[0].Name != "ENV.URL" ||
		candidate.Script.Parameters[0].Values[0] != "https://example.test" {
		t.Fatalf("noncanonical defined namespace was not resolved as a Bruno variable: target=%q parameters=%#v", action.Target, candidate.Script.Parameters)
	}
	if action.Headers["X-Unresolved"] != "{{var.PARAM.MISSING}}" ||
		strings.Join(candidate.MissingVariables, ",") != "PARAM.MISSING" ||
		action.Metadata["execution_blocker"] != "bruno_missing_variables" {
		t.Fatalf("noncanonical unresolved namespace was not blocked: candidate=%#v", candidate)
	}
}

func TestInspectNativeCredentialVariablesPreserveSecretSourceAndIndirection(t *testing.T) {
	tests := []struct {
		name             string
		variableValue    string
		wantPassword     string
		wantSourceValue  string
		forbiddenPayload string
	}{
		{
			name:             "literal is scrubbed and migratable",
			variableValue:    "source-password-value",
			wantSourceValue:  "source-password-value",
			forbiddenPayload: "source-password-value",
		},
		{
			name:          "existing secret indirection is preserved",
			variableValue: "{{secret.REAL_PASSWORD}}",
			wantPassword:  "{{secret.REAL_PASSWORD}}",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := fmt.Sprintf(`info: {name: Credentials, uid: credential-uid, type: http}
variables:
  - name: password
    value: %q
http:
  method: GET
  url: https://example.test
  auth:
    type: basic
    username: user
    password: "{{password}}"
`, test.variableValue)
			result, err := Inspect(Input{
				Filename: "request.yml",
				Content:  []byte(source),
				Options:  InspectOptions{OutputMode: OutputModeNativeRequests},
			})
			if err != nil {
				t.Fatal(err)
			}
			candidate := result.Scripts[0]
			password := candidate.Script.Actions[0].Auth.Password
			if test.wantPassword != "" && password != test.wantPassword {
				t.Fatalf("password = %q, want %q", password, test.wantPassword)
			}
			if strings.Contains(password, "secret.PASSWORD") {
				t.Fatalf("credential definition was replaced with invented name: %q", password)
			}
			if test.wantSourceValue != "" {
				found := false
				for _, requirement := range candidate.SecretRequirements {
					if requirement.Replacement == password && requirement.Value == test.wantSourceValue {
						found = true
					}
				}
				if !found {
					t.Fatalf("migratable requirement for %q not found: %#v", password, candidate.SecretRequirements)
				}
			}
			if test.forbiddenPayload != "" {
				sanitized := tarFile(t, result.SanitizedArchive, "request.yml")
				if bytes.Contains(sanitized, []byte(test.forbiddenPayload)) {
					t.Fatalf("sanitized source retained credential %q:\n%s", test.forbiddenPayload, sanitized)
				}
			}
		})
	}
}

func TestInspectNativeScrubsGenericCredentialVariableAtItsEffectiveDefinition(t *testing.T) {
	const sourceValue = "generic-credential-source-value"
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Credentials, uid: credential-uid, type: http}
variables:
  - name: cred
    value: ` + sourceValue + `
http:
  method: GET
  url: https://example.test
  auth:
    type: basic
    username: operator
    password: "{{cred}}"
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := result.Scripts[0]
	password := candidate.Script.Actions[0].Auth.Password
	if !strings.HasPrefix(password, "{{secret.") || strings.Contains(password, "secret.CRED}}") {
		t.Fatalf("governed credential replacement = %q", password)
	}
	var requirement *SecretRequirement
	for index := range candidate.SecretRequirements {
		current := &candidate.SecretRequirements[index]
		if current.Replacement == password {
			requirement = current
			break
		}
	}
	if requirement == nil || requirement.Value != sourceValue ||
		requirement.Path != "request.yml" ||
		!strings.HasPrefix(requirement.Key, nativeVariableRequirementKeyPrefix) {
		t.Fatalf("governed source requirement = %#v", requirement)
	}
	sanitized := tarFile(t, result.SanitizedArchive, "request.yml")
	if bytes.Contains(sanitized, []byte(sourceValue)) ||
		!bytes.Contains(sanitized, []byte(requirement.Replacement)) {
		t.Fatalf("generic credential definition was not scrubbed:\n%s", sanitized)
	}
	if raw := tarFile(t, result.RawArchive, "request.yml"); !bytes.Contains(raw, []byte(sourceValue)) {
		t.Fatalf("raw evidence unexpectedly changed:\n%s", raw)
	}
}

func TestInspectNativeCredentialDefinitionIdentityControlsSecretSharing(t *testing.T) {
	t.Run("request-local definitions stay distinct", func(t *testing.T) {
		result, err := Inspect(Input{
			Files: map[string][]byte{
				"demo/opencollection.yml": []byte("info: {name: Demo}\n"),
				"demo/a.yml": []byte(`info: {name: A, uid: a-uid, type: http, seq: 1}
variables: [{name: cred, value: request-a-secret}]
http:
  method: GET
  url: https://example.test/a
  auth: {type: basic, username: user, password: "{{cred}}"}
`),
				"demo/b.yaml": []byte(`info: {name: B, uid: b-uid, type: http, seq: 2}
variables: [{name: cred, value: request-b-secret}]
http:
  method: GET
  url: https://example.test/b
  auth: {type: basic, username: user, password: "{{cred}}"}
`),
			},
			Options: InspectOptions{OutputMode: OutputModeNativeRequests},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Scripts) != 2 {
			t.Fatalf("scripts = %d", len(result.Scripts))
		}
		firstPassword := result.Scripts[0].Script.Actions[0].Auth.Password
		secondPassword := result.Scripts[1].Script.Actions[0].Auth.Password
		if firstPassword == secondPassword {
			t.Fatalf("request-local credentials shared logical secret %q", firstPassword)
		}
		values := map[string]string{}
		for _, requirement := range result.SecretRequirements {
			if strings.HasPrefix(requirement.Key, nativeVariableRequirementKeyPrefix) {
				values[requirement.Replacement] = requirement.Value
			}
		}
		if values[firstPassword] != "request-a-secret" || values[secondPassword] != "request-b-secret" {
			t.Fatalf("request-local requirements = %#v", result.SecretRequirements)
		}
		for path, secret := range map[string]string{
			"demo/a.yml":  "request-a-secret",
			"demo/b.yaml": "request-b-secret",
		} {
			if sanitized := tarFile(t, result.SanitizedArchive, path); bytes.Contains(sanitized, []byte(secret)) {
				t.Fatalf("%s retained %q:\n%s", path, secret, sanitized)
			}
		}
	})

	t.Run("shared ancestor definition stays shared", func(t *testing.T) {
		result, err := Inspect(Input{
			Files: map[string][]byte{
				"demo/opencollection.yml": []byte(`info: {name: Demo}
request:
  variables: [{name: cred, value: shared-ancestor-secret}]
`),
				"demo/a.yml": []byte(`info: {name: A, uid: a-uid, type: http, seq: 1}
http:
  method: GET
  url: https://example.test/a
  auth: {type: basic, username: user, password: "{{cred}}"}
`),
				"demo/b.yml": []byte(`info: {name: B, uid: b-uid, type: http, seq: 2}
http:
  method: GET
  url: https://example.test/b
  auth: {type: basic, username: user, password: "{{cred}}"}
`),
			},
			Options: InspectOptions{OutputMode: OutputModeNativeRequests},
		})
		if err != nil {
			t.Fatal(err)
		}
		firstPassword := result.Scripts[0].Script.Actions[0].Auth.Password
		secondPassword := result.Scripts[1].Script.Actions[0].Auth.Password
		if firstPassword == "" || firstPassword != secondPassword {
			t.Fatalf("shared ancestor replacements = %q / %q", firstPassword, secondPassword)
		}
		projected := 0
		for _, requirement := range result.SecretRequirements {
			if strings.HasPrefix(requirement.Key, nativeVariableRequirementKeyPrefix) {
				projected++
				if requirement.Value != "shared-ancestor-secret" {
					t.Fatalf("shared requirement = %#v", requirement)
				}
			}
		}
		if projected != 1 {
			t.Fatalf("projected shared requirements = %d: %#v", projected, result.SecretRequirements)
		}
	})

	t.Run("shadowed ancestor definition stays excluded", func(t *testing.T) {
		result, err := Inspect(Input{
			Files: map[string][]byte{
				"demo/opencollection.yml": []byte(`info: {name: Demo}
request:
  variables: [{name: cred, value: shadowed-ancestor-value}]
`),
				"demo/request.yml": []byte(`info: {name: Request, uid: request-uid, type: http}
variables: [{name: cred, value: active-request-value}]
http:
  method: GET
  url: https://example.test
  auth: {type: basic, username: user, password: "{{cred}}"}
`),
			},
			Options: InspectOptions{OutputMode: OutputModeNativeRequests},
		})
		if err != nil {
			t.Fatal(err)
		}
		projected := make([]SecretRequirement, 0)
		for _, requirement := range result.SecretRequirements {
			if strings.HasPrefix(requirement.Key, nativeVariableRequirementKeyPrefix) {
				projected = append(projected, requirement)
			}
		}
		if len(projected) != 1 ||
			projected[0].Path != "demo/request.yml" ||
			projected[0].Value != "active-request-value" {
			t.Fatalf("shadowed requirement leaked into executable candidates: %#v", projected)
		}
		if strings.Contains(result.Scripts[0].Script.Actions[0].Auth.Password, "OPENCOLLECTION") {
			t.Fatalf("shadowed ancestor credential executed: %#v", result.Scripts[0].Script.Actions[0].Auth)
		}
	})
}

func TestInspectNativeScrubsLiteralXMLCredentialsWithoutReformattingBody(t *testing.T) {
	const body = `<soap:Envelope xmlns:soap="urn:soap" xmlns:auth="urn:auth" keep="unchanged">
  <soap:Body>
    <login apiKey='attribute-api-key' mode="unchanged">
      <username>alice</username>
      <password>  p&amp;w  </password>
      <auth:token>element-token</auth:token>
      <secret>element-secret</secret>
      <auth>literal-auth</auth>
      <note>non-sensitive-text</note>
    </login>
  </soap:Body>
</soap:Envelope>`
	inspect := func(t *testing.T) *Result {
		t.Helper()
		result, err := Inspect(Input{
			Filename: "request.yml",
			Content: []byte(`info: {name: XML Credentials, uid: xml-credentials, type: http}
http:
  method: POST
  url: https://example.test
  body:
    type: xml
    data: |
` + indentNativeTestBody(body, 6)),
			Options: InspectOptions{OutputMode: OutputModeNativeRequests},
		})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}

	first := inspect(t)
	candidate := first.Scripts[0]
	if len(candidate.SecretRequirements) != 5 {
		t.Fatalf("XML secret requirements = %#v", candidate.SecretRequirements)
	}
	expectedBody := body + "\n"
	identity := make(map[string]string)
	for _, requirement := range candidate.SecretRequirements {
		if !strings.HasPrefix(requirement.Key, nativeXMLRequirementKeyPrefix) ||
			requirement.Value == "" ||
			!strings.Contains(requirement.Context, "Bruno XML body") {
			t.Fatalf("XML secret requirement = %#v", requirement)
		}
		expectedBody = strings.Replace(expectedBody, requirement.Value, requirement.Replacement, 1)
		identity[requirement.ID] = requirement.Replacement
	}
	actionBody := candidate.Script.Actions[0].Body
	if actionBody != expectedBody {
		t.Fatalf("XML body changed outside credential spans:\n got: %q\nwant: %q", actionBody, expectedBody)
	}
	for _, preserved := range []string{
		`xmlns:auth="urn:auth"`,
		`keep="unchanged"`,
		`mode="unchanged"`,
		`<username>alice</username>`,
		`<note>non-sensitive-text</note>`,
	} {
		if !strings.Contains(actionBody, preserved) {
			t.Fatalf("non-sensitive XML fragment %q was changed:\n%s", preserved, actionBody)
		}
	}
	sanitized := tarFile(t, first.SanitizedArchive, "request.yml")
	for _, credential := range []string{
		"attribute-api-key",
		"p&amp;w",
		"element-token",
		"element-secret",
		"literal-auth",
	} {
		if bytes.Contains(sanitized, []byte(credential)) {
			t.Fatalf("sanitized XML retained %q:\n%s", credential, sanitized)
		}
	}
	if !bytes.Contains(sanitized, []byte("non-sensitive-text")) {
		t.Fatalf("sanitized XML lost non-sensitive content:\n%s", sanitized)
	}

	second := inspect(t)
	secondIdentity := make(map[string]string)
	for _, requirement := range second.Scripts[0].SecretRequirements {
		secondIdentity[requirement.ID] = requirement.Replacement
	}
	if len(identity) != len(secondIdentity) {
		t.Fatalf("XML requirement identity count changed: first=%#v second=%#v", identity, secondIdentity)
	}
	for id, replacement := range identity {
		if secondIdentity[id] != replacement {
			t.Fatalf("XML requirement identity is not deterministic: first=%#v second=%#v", identity, secondIdentity)
		}
	}
}

func indentNativeTestBody(body string, spaces int) string {
	prefix := strings.Repeat(" ", spaces)
	lines := strings.Split(body, "\n")
	for index := range lines {
		lines[index] = prefix + lines[index]
	}
	return strings.Join(lines, "\n") + "\n"
}

func TestInspectNativeTestsHooksAssertionsAndRedirectBounds(t *testing.T) {
	tests := []struct {
		name               string
		runtime            string
		settings           string
		wantAssertions     int
		wantDiagnostic     string
		wantReviewRequired bool
	}{
		{
			name: "standard tests hook translates",
			runtime: `runtime:
  scripts:
    - type: tests
      code: |
        test("status", function () {
          expect(res.getStatus()).to.equal(500);
        });
`,
			wantAssertions: 1,
		},
		{
			name: "declarative assertions fail closed",
			runtime: `runtime:
  assertions:
    - type: status
      expected: 500
`,
			wantDiagnostic:     "bruno_javascript_review_required",
			wantReviewRequired: true,
		},
		{
			name:               "redirect overflow gates clamped draft",
			settings:           "settings: {maxRedirects: 51}\n",
			wantDiagnostic:     "bruno_max_redirects_clamped",
			wantReviewRequired: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := Inspect(Input{
				Filename: "request.yml",
				Content: []byte(`info: {name: Request, uid: request-uid, type: http}
http: {method: GET, url: https://example.test}
` + test.settings + test.runtime),
				Options: InspectOptions{OutputMode: OutputModeNativeRequests},
			})
			if err != nil {
				t.Fatal(err)
			}
			candidate := result.Scripts[0]
			action := candidate.Script.Actions[0]
			if len(action.Assertions) != test.wantAssertions {
				t.Fatalf("assertions = %#v", action.Assertions)
			}
			if test.wantDiagnostic != "" && !hasNativeDiagnostic(candidate.Diagnostics, test.wantDiagnostic) {
				t.Fatalf("diagnostics = %#v", candidate.Diagnostics)
			}
			if got, _ := action.Metadata["review_required"].(bool); got != test.wantReviewRequired {
				t.Fatalf("review metadata = %#v", action.Metadata)
			}
			if test.wantReviewRequired && action.Metadata["promotion_blocker"] != true {
				t.Fatalf("promotion blocker = %#v", action.Metadata)
			}
			if test.wantReviewRequired {
				findingIDs, ok := candidate.Script.Metadata.Import.Preserved["finding_ids"].([]string)
				if !ok || len(findingIDs) == 0 {
					t.Fatalf("finding provenance = %#v", candidate.Script.Metadata.Import.Preserved)
				}
			}
			if test.name == "redirect overflow gates clamped draft" &&
				(action.MaxRedirects == nil || *action.MaxRedirects != nativeMaxRedirects) {
				t.Fatalf("redirect clamp = %v", action.MaxRedirects)
			}
		})
	}
}

func TestNativeJSStringRejectsUnprovenEscapes(t *testing.T) {
	tests := []struct {
		literal string
		want    string
		ok      bool
	}{
		{literal: `'plain'`, want: "plain", ok: true},
		{literal: `'it\'s'`, want: "it's", ok: true},
		{literal: `'slash\\path'`, want: `slash\path`, ok: true},
		{literal: `"quoted\""`, want: `quoted"`, ok: true},
		{literal: `'line\nbreak'`, ok: false},
		{literal: `'hex\x41'`, ok: false},
		{literal: `"line\nbreak"`, ok: false},
	}
	for _, test := range tests {
		t.Run(test.literal, func(t *testing.T) {
			got, ok := nativeJSString(test.literal)
			if ok != test.ok || got != test.want {
				t.Fatalf("nativeJSString(%q) = %q, %t; want %q, %t", test.literal, got, ok, test.want, test.ok)
			}
		})
	}
}

func TestInspectNativeSingleQuotedUnsupportedEscapeCreatesReviewGate(t *testing.T) {
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Escaped Assertion, uid: escaped-assertion, type: http}
http: {method: GET, url: https://example.test}
runtime:
  scripts:
    - type: tests
      code: |
        test('header', function () {
          expect(res.getHeader('X-Message')).to.include('line\nbreak');
        });
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := result.Scripts[0]
	action := candidate.Script.Actions[0]
	if len(action.Assertions) != 0 ||
		action.Metadata["review_gate"] != "bruno_javascript" ||
		action.Metadata["review_required"] != true ||
		!hasNativeDiagnostic(candidate.Diagnostics, "bruno_javascript_review_required") {
		t.Fatalf("unsupported JS escape did not fail closed: action=%#v diagnostics=%#v", action, candidate.Diagnostics)
	}
}

func TestInspectNativeEmptyBasicAuthPreservesBasicOgEquals(t *testing.T) {
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Empty Basic, uid: empty-basic, type: http}
http:
  method: GET
  url: https://example.test
  auth:
    type: basic
    username: ""
    password: ""
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := result.Scripts[0]
	action := candidate.Script.Actions[0]
	if action.Auth == nil || action.Auth.Type != "basic" ||
		action.Auth.Username != "" || action.Auth.Password != "" {
		t.Fatalf("empty Basic auth = %#v", action.Auth)
	}
	header := "Basic " + base64.StdEncoding.EncodeToString([]byte(action.Auth.Username+":"+action.Auth.Password))
	if header != "Basic Og==" {
		t.Fatalf("empty Basic header = %q, want Basic Og==", header)
	}
	if !hasNativeDiagnostic(candidate.Diagnostics, "bruno_empty_basic_auth") {
		t.Fatalf("empty Basic warning missing: %#v", candidate.Diagnostics)
	}
}

func TestInspectNativeNearestInheritedAuthAndRequestNoAuth(t *testing.T) {
	result, err := Inspect(Input{
		Files: map[string][]byte{
			"demo/opencollection.yml": []byte(`info: {name: Demo}
request:
  auth:
    type: bearer
    token: root-token
`),
			"demo/outer/folder.yml": []byte(`info: {name: Outer, seq: 1}
request:
  auth:
    type: basic
    username: outer-user
    password: outer-password
`),
			"demo/outer/inner/folder.yaml": []byte(`info: {name: Inner, seq: 1}
request:
  auth:
    type: basic
    username: inner-user
    password: inner-password
`),
			"demo/outer/inner/inherit.yml": []byte(`info: {name: Inherit, uid: inherit-uid, type: http, seq: 1}
http:
  method: GET
  url: https://example.test/inherit
  auth: inherit
`),
			"demo/outer/inner/noauth.yaml": []byte(`info: {name: No Auth, uid: noauth-uid, type: http, seq: 2}
http:
  method: GET
  url: https://example.test/noauth
  auth: noauth
`),
		},
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Scripts) != 2 {
		t.Fatalf("scripts = %d, want 2", len(result.Scripts))
	}
	inherited := result.Scripts[0]
	inheritedAuth := inherited.Script.Actions[0].Auth
	if inherited.SourcePath != "outer/inner/inherit.yml" ||
		inheritedAuth == nil || inheritedAuth.Type != "basic" ||
		inheritedAuth.Username == "" || inheritedAuth.Password == "" {
		t.Fatalf("nearest inherited auth = %#v in %#v", inheritedAuth, inherited)
	}
	for _, requirement := range inherited.SecretRequirements {
		if requirement.Path != "demo/outer/inner/folder.yaml" {
			t.Fatalf("shadowed ancestor auth created executable requirement: %#v", inherited.SecretRequirements)
		}
	}
	noAuth := result.Scripts[1]
	if noAuth.SourcePath != "outer/inner/noauth.yaml" ||
		noAuth.Script.Actions[0].Auth != nil ||
		len(noAuth.SecretRequirements) != 0 {
		t.Fatalf("request noauth = %#v", noAuth)
	}
}

func TestInspectNativeDisabledHeadersAndVariablesRemainNonExecutable(t *testing.T) {
	result, err := Inspect(Input{
		Files: map[string][]byte{
			"demo/opencollection.yml": []byte(`info: {name: Demo}
request:
  headers:
    - {name: X-Stable, value: enabled-parent}
  variables:
    - {name: base, value: https://enabled.example.test}
`),
			"demo/apis/folder.yml": []byte(`info: {name: APIs}
request:
  headers:
    - {name: X-Stable, value: disabled-child, disabled: true}
    - {name: X-Disabled, value: "{{disabled_missing}}", disabled: true}
    - {name: Authorization, value: Bearer disabled-header-secret, disabled: true}
  variables:
    - {name: base, value: https://disabled.example.test, disabled: true}
    - {name: password, value: disabled-password-secret, disabled: true}
    - {name: disabled_variable, value: "{{another_missing}}", disabled: true}
`),
			"demo/apis/request.yml": []byte(`info: {name: Request, uid: request-uid, type: http}
http:
  method: GET
  url: "{{base}}/resource"
`),
		},
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := result.Scripts[0]
	action := candidate.Script.Actions[0]
	if action.Headers["X-Stable"] != "enabled-parent" || len(action.HeaderList) != 1 {
		t.Fatalf("disabled headers affected execution: %#v", action.HeaderList)
	}
	if action.Target != "{{param.base}}/resource" ||
		len(candidate.Script.Parameters) != 1 ||
		candidate.Script.Parameters[0].Values[0] != "https://enabled.example.test" {
		t.Fatalf("disabled variables affected execution: target=%q parameters=%#v", action.Target, candidate.Script.Parameters)
	}
	if len(candidate.MissingVariables) != 0 || len(candidate.SecretRequirements) != 0 ||
		len(result.MissingVariables) != 0 || len(result.SecretRequirements) != 0 {
		t.Fatalf("disabled values created requirements: candidate missing=%#v secrets=%#v; result missing=%#v secrets=%#v",
			candidate.MissingVariables,
			candidate.SecretRequirements,
			result.MissingVariables,
			result.SecretRequirements,
		)
	}
}

func TestInspectNativeInlineBlockCommentCodeCreatesJavaScriptReviewGate(t *testing.T) {
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Hidden JavaScript, uid: hidden-javascript, type: http}
http: {method: GET, url: https://example.test}
runtime:
  scripts:
    - type: tests
      code: |
        /* explanatory comment */ bru.setVar("sideEffect", "yes"); /* trailing comment */
        test("status", function () {
          expect(res.getStatus()).to.equal(200);
        });
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := result.Scripts[0]
	action := candidate.Script.Actions[0]
	if len(action.Assertions) != 0 ||
		action.Metadata["review_gate"] != "bruno_javascript" ||
		action.Metadata["review_required"] != true ||
		action.Metadata["promotion_blocker"] != true ||
		!hasNativeDiagnostic(candidate.Diagnostics, "bruno_javascript_review_required") {
		t.Fatalf("inline block-comment JavaScript did not fail closed: action=%#v diagnostics=%#v", action, candidate.Diagnostics)
	}
}

func TestInspectNativeUnsupportedAuthCreatesReviewBlockedDraft(t *testing.T) {
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: API Key, uid: api-key-auth, type: http}
variables:
  - {name: apiKey, value: source-api-key}
http:
  method: GET
  url: https://example.test
  auth:
    type: apikey
    key: x-api-key
    value: "{{apiKey}}"
    placement: header
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := result.Scripts[0]
	action := candidate.Script.Actions[0]
	if candidate.Fidelity != "review_required" ||
		action.Auth == nil ||
		action.Auth.Type != "apikey" ||
		action.Metadata["review_gate"] != "bruno_auth" ||
		action.Metadata["review_required"] != true ||
		action.Metadata["promotion_blocker"] != true ||
		!hasNativeDiagnostic(candidate.Diagnostics, "bruno_auth_review_required") {
		t.Fatalf("unsupported auth was not review-blocked: candidate=%#v action=%#v", candidate, action)
	}
	var findingFound bool
	for _, finding := range result.SecurityFindings {
		if finding.Code == "bruno_auth_review_required" && finding.ID != "" {
			findingFound = true
			break
		}
	}
	if !findingFound {
		t.Fatalf("unsupported auth finding missing: %#v", result.SecurityFindings)
	}
}

func TestInspectNativeVariableResolvedEmptyBasicAuthPreservesBasicOgEquals(t *testing.T) {
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Empty Variable Basic, uid: empty-variable-basic, type: http}
variables:
  - {name: emptyUser, value: ""}
  - {name: emptyPassword, value: ""}
http:
  method: GET
  url: https://example.test
  auth:
    type: basic
    username: "{{emptyUser}}"
    password: "{{emptyPassword}}"
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := result.Scripts[0]
	action := candidate.Script.Actions[0]
	if action.Auth == nil ||
		action.Auth.Type != "basic" ||
		action.Auth.Username != "" ||
		action.Auth.Password != "" {
		t.Fatalf("variable-resolved empty Basic auth = %#v", action.Auth)
	}
	header := "Basic " + base64.StdEncoding.EncodeToString([]byte(action.Auth.Username+":"+action.Auth.Password))
	if header != "Basic Og==" {
		t.Fatalf("variable-resolved empty Basic header = %q, want Basic Og==", header)
	}
	if len(candidate.SecretRequirements) != 0 ||
		len(candidate.MissingVariables) != 0 ||
		!hasNativeDiagnostic(candidate.Diagnostics, "bruno_empty_basic_auth") {
		t.Fatalf("variable-resolved empty Basic metadata: missing=%#v secrets=%#v diagnostics=%#v",
			candidate.MissingVariables,
			candidate.SecretRequirements,
			candidate.Diagnostics,
		)
	}
}

func TestInspectNativeRejectsMultipleOpenCollectionRoots(t *testing.T) {
	_, err := Inspect(Input{
		Files: map[string][]byte{
			"bundle/opencollection.yml":      []byte("info: {name: Outer}\n"),
			"bundle/sub/OpenCollection.YAML": []byte("info: {name: Nested}\n"),
			"bundle/sub/request.yml": []byte(`info: {name: Request, uid: nested-request, type: http}
http: {method: GET, url: https://example.test}
`),
		},
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err == nil ||
		!strings.Contains(err.Error(), "multiple Bruno OpenCollection roots are not supported") ||
		!strings.Contains(err.Error(), "bundle/opencollection.yml") ||
		!strings.Contains(err.Error(), "bundle/sub/OpenCollection.YAML") {
		t.Fatalf("multiple OpenCollection root error = %v", err)
	}
}

func TestInspectNativeRejectsCaseFoldDuplicateYAMLKeysDeterministically(t *testing.T) {
	input := Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Duplicate Settings, uid: duplicate-settings, type: http}
http: {method: GET, url: https://example.test}
settings: {timeout: 10}
Settings: {timeout: 20}
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	}
	var first string
	for iteration := 0; iteration < 20; iteration++ {
		_, err := Inspect(input)
		if err == nil {
			t.Fatal("case-fold duplicate YAML keys were accepted")
		}
		if !strings.Contains(err.Error(), `mapping $ contains case-fold duplicate keys "settings" and "Settings"`) {
			t.Fatalf("case-fold duplicate error = %v", err)
		}
		if iteration == 0 {
			first = err.Error()
		} else if err.Error() != first {
			t.Fatalf("case-fold duplicate error changed: first=%q current=%q", first, err)
		}
	}
}

func hasNativeDiagnostic(diagnostics []NativeDiagnostic, code string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code {
			return true
		}
	}
	return false
}
