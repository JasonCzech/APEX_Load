package credentialpolicy

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

const projectionCanary = "canary-credential-9f3a7c"

func projectionOptions() Options {
	return Options{
		Mode:       SecretReference,
		Source:     SourceRef{Format: "fixture", Path: "request.json", Pointer: "/request"},
		RequestKey: "request-one",
		NamePrefix: "fixture",
		Registry:   NewNameRegistry(),
	}
}

func assertDisclosureSafe(t *testing.T, output []byte, result Result) {
	t.Helper()
	if bytes.Contains(output, []byte(projectionCanary)) {
		t.Fatalf("projected output disclosed canary: %s", output)
	}
	serialized, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(serialized, []byte(projectionCanary)) {
		t.Fatalf("serialized projection sidecar disclosed canary: %s", serialized)
	}
	for _, finding := range result.Findings {
		if strings.Contains(finding.Code, projectionCanary) ||
			strings.Contains(finding.Source.Path, projectionCanary) ||
			strings.Contains(finding.Source.Pointer, projectionCanary) {
			t.Fatalf("finding disclosed canary: %#v", finding)
		}
	}
}

func TestClassifyWholeTemplate(t *testing.T) {
	t.Parallel()
	proven := map[string]struct{}{"response_token": {}}
	tests := []struct {
		value string
		kind  WholeTemplateKind
		name  string
	}{
		{"{{secret.api-key}}", TemplateSecret, "API_KEY"},
		{"  {{env.API_TOKEN}} ", TemplateEnv, "API_TOKEN"},
		{"{{var.response_token}}", TemplateDynamic, "response_token"},
		{"{{var.static_token}}", TemplateUnproven, "static_token"},
		{"Bearer {{var.response_token}}", TemplateNone, ""},
		{"{{fn.uuid()}}", TemplateOther, ""},
	}
	for _, test := range tests {
		test := test
		t.Run(test.value, func(t *testing.T) {
			t.Parallel()
			kind, name := ClassifyWholeTemplate(test.value, proven)
			if kind != test.kind || (test.name != "" && name != test.name) {
				t.Fatalf("ClassifyWholeTemplate(%q) = %q, %q; want %q, %q", test.value, kind, name, test.kind, test.name)
			}
		})
	}
}

func TestProjectHeadersPreservesOccurrencesAndGovernsComposedValues(t *testing.T) {
	t.Parallel()
	options := projectionOptions()
	input := []HeaderOccurrence{
		{Name: "X-Trace", Value: "one"},
		{Name: "Authorization", Value: "Bearer " + projectionCanary},
		{Name: "authorization", Value: "{{var.response_token}}"},
		{Name: "X-Trace", Value: "two"},
	}
	options.ProvenVars = map[string]struct{}{"response_token": {}}
	output, result, err := ProjectHeaders(input, options)
	if err != nil {
		t.Fatal(err)
	}
	if len(output) != len(input) {
		t.Fatalf("header count = %d, want %d", len(output), len(input))
	}
	for index := range input {
		if output[index].Name != input[index].Name {
			t.Fatalf("header %d casing/order changed: %#v", index, output)
		}
	}
	if output[0].Value != "one" || output[3].Value != "two" {
		t.Fatalf("nonsensitive headers changed: %#v", output)
	}
	if output[1].Value != "{{secret.FIXTURE_AUTHORIZATION}}" {
		t.Fatalf("composed credential was not governed as a whole: %#v", output[1])
	}
	if output[2].Value != "{{var.response_token}}" {
		t.Fatalf("proven response variable changed: %#v", output[2])
	}
	if len(result.Requirements) != 1 || result.Disposition != ExecutableExact {
		t.Fatalf("result = %#v", result)
	}
	assertDisclosureSafe(t, []byte(output[1].Value), result)
}

func TestHeaderAndAuthProjectionBoundsWork(t *testing.T) {
	t.Parallel()
	options := projectionOptions()
	options.Limits = Limits{MaxBytes: 8, MaxDepth: 2, MaxValues: 1, MaxParts: 1}

	if output, result, err := ProjectHeaders([]HeaderOccurrence{
		{Name: "A", Value: "1"},
		{Name: "B", Value: "2"},
	}, options); err != nil || output != nil || result.Disposition != Rejected {
		t.Fatalf("header count limit output=%#v result=%#v err=%v", output, result, err)
	}
	if output, result, err := ProjectHeaders([]HeaderOccurrence{{
		Name: "Authorization", Value: "too-large",
	}}, options); err != nil || output != nil || result.Disposition != Rejected {
		t.Fatalf("header byte limit output=%#v result=%#v err=%v", output, result, err)
	}
	if output, result, err := ProjectAuth(AuthFields{
		Type: "bearer", Token: "too-large",
	}, options); err != nil || output != (AuthFields{}) || result.Disposition != Rejected {
		t.Fatalf("auth byte limit output=%#v result=%#v err=%v", output, result, err)
	}
}

func TestProjectHeadersUnprovenVariableIsNotTrusted(t *testing.T) {
	t.Parallel()
	output, result, err := ProjectHeaders([]HeaderOccurrence{{
		Name:  "X-Auth-Token",
		Value: "{{var.looks_dynamic}}",
	}}, projectionOptions())
	if err != nil {
		t.Fatal(err)
	}
	if output[0].Value == "{{var.looks_dynamic}}" || len(result.Requirements) != 1 {
		t.Fatalf("unproven variable escaped governance: %#v / %#v", output, result)
	}
}

func TestProjectHeadersMaterializesSourceVariableUnlessPriorExtractorProvesDynamic(t *testing.T) {
	t.Parallel()
	options := projectionOptions()
	options.SourceVariables = map[string]SourceVariable{
		"authValue": {
			Value:  projectionCanary,
			Source: SourceRef{Format: "fixture", Path: "collection.json", Pointer: "/variable/0"},
		},
	}
	input := []HeaderOccurrence{{
		Name:  "Authorization",
		Value: "Bearer {{var.authValue}}",
	}}
	output, result, err := ProjectHeaders(input, options)
	if err != nil {
		t.Fatal(err)
	}
	if output[0].Value != "{{secret.FIXTURE_AUTHORIZATION}}" {
		t.Fatalf("static source variable escaped credential projection: %#v", output)
	}
	if len(result.Requirements) != 1 ||
		result.Requirements[0].Value != "Bearer "+projectionCanary ||
		len(result.Requirements[0].SourceVariableNames) != 1 ||
		result.Requirements[0].SourceVariableNames[0] != "authValue" {
		t.Fatalf("source-variable requirement = %#v", result.Requirements)
	}
	assertDisclosureSafe(t, []byte(output[0].Value), result)

	options.ProvenVars = map[string]struct{}{"authValue": {}}
	dynamicInput := []HeaderOccurrence{{
		Name:  "Authorization",
		Value: "{{var.authValue}}",
	}}
	output, result, err = ProjectHeaders(dynamicInput, options)
	if err != nil {
		t.Fatal(err)
	}
	if output[0].Value != dynamicInput[0].Value || len(result.Requirements) != 0 {
		t.Fatalf("prior response-derived variable was not preserved: %#v / %#v", output, result)
	}
}

func TestMaterializeSourceVariablesReturnsProtectedValidationValue(t *testing.T) {
	t.Parallel()
	options := projectionOptions()
	options.SourceVariables = map[string]SourceVariable{
		"token": {Value: projectionCanary + "\r\nInjected: yes"},
	}
	materialized, names := MaterializeSourceVariables("Bearer {{var.token}}", options)
	if materialized != "Bearer "+projectionCanary+"\r\nInjected: yes" ||
		len(names) != 1 || names[0] != "token" {
		t.Fatal("source variable did not resolve to the expected protected validation value")
	}
	options.ProvenVars = map[string]struct{}{"token": {}}
	if dynamic, used := MaterializeSourceVariables("{{var.token}}", options); dynamic != "{{var.token}}" || len(used) != 0 {
		t.Fatalf("proven dynamic variable was materialized = %q / %#v", dynamic, used)
	}
}

func TestMaterializeSourceVariablesSupportsStaticParameters(t *testing.T) {
	t.Parallel()
	options := projectionOptions()
	options.SourceVariables = map[string]SourceVariable{
		"password": {Value: "source-password"},
	}
	// ProvenVars applies only to response-derived {{var.*}} references. A
	// source-backed {{param.*}} value remains static credential material.
	options.ProvenVars = map[string]struct{}{"password": {}}

	materialized, names := MaterializeSourceVariables(
		"Basic user:{{param.password}}",
		options,
	)
	if materialized != "Basic user:source-password" {
		t.Fatalf("materialized = %q", materialized)
	}
	if len(names) != 1 || names[0] != "password" {
		t.Fatalf("used names = %#v", names)
	}
}

func TestMaterializeSourceVariablesMatchesRuntimeParameterNamesCaseInsensitively(t *testing.T) {
	t.Parallel()
	options := projectionOptions()
	options.SourceVariables = map[string]SourceVariable{
		"Token": {Value: projectionCanary},
	}

	materialized, names := MaterializeSourceVariables(
		"Bearer {{var.token}}",
		options,
	)
	if materialized != "Bearer "+projectionCanary {
		t.Fatalf("materialized = %q", materialized)
	}
	if len(names) != 1 || names[0] != "Token" {
		t.Fatalf("used names = %#v", names)
	}

	options.SourceVariables["TOKEN"] = SourceVariable{Value: "ambiguous"}
	ambiguous, names := MaterializeSourceVariables("{{var.token}}", options)
	if ambiguous != "{{var.token}}" || len(names) != 0 {
		t.Fatalf("case-colliding variables must fail closed = %q / %#v", ambiguous, names)
	}
}

func TestProjectHeadersPreservesOnlyPreGovernedOccurrence(t *testing.T) {
	t.Parallel()
	options := projectionOptions()
	options.PreGovernedHeaderOccurrences = map[int]struct{}{0: {}}
	input := []HeaderOccurrence{
		{Name: "Cookie", Value: "locale={{data.fixture.locale}}; session={{secret.EXISTING_SESSION}}"},
		{Name: "X-API-Key", Value: projectionCanary},
	}
	output, result, err := ProjectHeaders(input, options)
	if err != nil {
		t.Fatal(err)
	}
	if output[0] != input[0] {
		t.Fatalf("pre-governed composed header changed: %#v", output[0])
	}
	if output[1].Value != "{{secret.FIXTURE_X_API_KEY}}" {
		t.Fatalf("independent credential header was not projected: %#v", output[1])
	}
	if len(result.Requirements) != 1 {
		t.Fatalf("requirements = %#v, want only independent credential", result.Requirements)
	}
	assertDisclosureSafe(t, []byte(output[0].Value+output[1].Value), result)
}

func TestProjectAuthOnlyProjectsCredentialFields(t *testing.T) {
	t.Parallel()
	output, result, err := ProjectAuth(AuthFields{
		Type:     "digest",
		Username: "operator",
		Password: projectionCanary,
		Token:    "{{secret.existing}}",
		Domain:   "EXAMPLE",
	}, projectionOptions())
	if err != nil {
		t.Fatal(err)
	}
	if output.Username != "operator" || output.Domain != "EXAMPLE" {
		t.Fatalf("auth identifiers changed: %#v", output)
	}
	if output.Password == projectionCanary || output.Token != "{{secret.EXISTING}}" {
		t.Fatalf("auth credentials not projected: %#v", output)
	}
	if len(result.Requirements) != 2 {
		t.Fatalf("requirements = %#v", result.Requirements)
	}
	assertDisclosureSafe(t, []byte(output.Password+output.Token), result)
}

func TestProjectRawURLPreservesQueryOrderAndBlocksUnsafeComponents(t *testing.T) {
	t.Parallel()
	raw := "https://alice:" + projectionCanary + "@example.test/a?z=1&token=" + projectionCanary + "&z=2#token=" + projectionCanary
	output, result, err := ProjectRawURL(raw, projectionOptions())
	if err != nil {
		t.Fatal(err)
	}
	if result.Disposition != BlockedDraft {
		t.Fatalf("disposition = %q, want blocked", result.Disposition)
	}
	if !strings.Contains(output, "?z=1&token={{secret.FIXTURE_TOKEN}}&z=2") {
		t.Fatalf("query ordering/duplicates changed: %s", output)
	}
	if strings.Count(output, "z=") != 2 {
		t.Fatalf("duplicate query fields collapsed: %s", output)
	}
	assertDisclosureSafe(t, []byte(output), result)
}

func TestProjectRawURLScopesRequirementTargetsToRequestedSurface(t *testing.T) {
	t.Parallel()

	options := projectionOptions()
	options.TargetField = "base_url"
	output, result, err := ProjectRawURL(
		"https://example.test/root?token="+projectionCanary,
		options,
	)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output, projectionCanary) || len(result.Requirements) != 1 {
		t.Fatalf("projection = %q / %#v", output, result)
	}
	targets := result.Requirements[0].Targets
	if len(targets) != 1 || targets[0].Field != "base_url.query" {
		t.Fatalf("requirement targets = %#v, want base_url.query", targets)
	}
}

func TestProjectFormURLEncodedPreservesRawNonsensitiveFields(t *testing.T) {
	t.Parallel()
	raw := "note=a+b&client%5Fsecret=" + projectionCanary + "&note=a%20b&blank=&flag"
	output, fields, result, err := ProjectFormURLEncoded(raw, projectionOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) != 5 || output != "note=a+b&client%5Fsecret={{secret.FIXTURE_CLIENT_SECRET}}&note=a%20b&blank=&flag" {
		t.Fatalf("form projection changed wire structure: %q / %#v", output, fields)
	}
	if fields[4].HadEquals {
		t.Fatalf("missing equals sign was not preserved: %#v", fields[4])
	}
	assertDisclosureSafe(t, []byte(output), result)
}

func TestProjectPairsGovernComposedCredentialValues(t *testing.T) {
	t.Parallel()
	formOutput, _, formResult, err := ProjectFormURLEncoded(
		"config=password%3D"+projectionCanary+"&note=%2Fpassword%2Freset",
		projectionOptions(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if formOutput != "config={{secret.FIXTURE_PASSWORD}}&note=%2Fpassword%2Freset" ||
		len(formResult.Requirements) != 1 ||
		formResult.Requirements[0].Value != "password%3D"+projectionCanary {
		t.Fatalf("composed form credential was not governed exactly: %q / %#v", formOutput, formResult)
	}
	assertDisclosureSafe(t, []byte(formOutput), formResult)

	urlOutput, urlResult, err := ProjectRawURL(
		"https://example.test/path?safe=1;password="+projectionCanary+"&ordinary=keep",
		projectionOptions(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(urlOutput, "?safe={{secret.FIXTURE_PASSWORD}}&ordinary=keep") ||
		len(urlResult.Requirements) != 1 ||
		urlResult.Requirements[0].Value != "1;password="+projectionCanary {
		t.Fatalf("composed URL credential was not governed exactly: %q / %#v", urlOutput, urlResult)
	}
	assertDisclosureSafe(t, []byte(urlOutput), urlResult)
}

func TestStructuredRequirementsRetainWireReadyEncoding(t *testing.T) {
	t.Parallel()
	formOutput, _, formResult, err := ProjectFormURLEncoded("password=a%2Fb%2Bc", projectionOptions())
	if err != nil {
		t.Fatal(err)
	}
	if formOutput != "password={{secret.FIXTURE_PASSWORD}}" ||
		formResult.Requirements[0].Value != "a%2Fb%2Bc" {
		t.Fatalf("form requirement did not retain wire encoding: %q / %#v", formOutput, formResult.Requirements[0])
	}

	jsonOutput, jsonResult, err := ProjectJSON([]byte(`{"password":"a\\\"b\\n"}`), projectionOptions())
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(jsonOutput) || jsonResult.Requirements[0].Value != `a\\\"b\\n` {
		t.Fatalf("JSON requirement did not retain wire escaping: %s / %#v", jsonOutput, jsonResult.Requirements[0])
	}

	xmlOutput, xmlResult, err := ProjectXML([]byte(`<root><password>a&amp;b</password></root>`), projectionOptions())
	if err != nil {
		t.Fatal(err)
	}
	if xmlResult.Requirements[0].Value != "a&amp;b" ||
		!bytes.Contains(xmlOutput, []byte(`<password>{{secret.FIXTURE_PASSWORD}}</password>`)) {
		t.Fatalf("XML requirement did not retain entity encoding: %s / %#v", xmlOutput, xmlResult.Requirements[0])
	}
}

func TestProjectRawURLGovernsSchemeRelativeUserinfo(t *testing.T) {
	t.Parallel()
	raw := "//alice:" + projectionCanary + "@example.test/orders?tag=keep"
	output, result, err := ProjectRawURL(raw, projectionOptions())
	if err != nil {
		t.Fatalf("ProjectRawURL: %v", err)
	}
	if strings.Contains(output, projectionCanary) ||
		!strings.HasSuffix(output, "@example.test/orders?tag=keep") {
		t.Fatalf("scheme-relative URL was not projected byte-safely: %q", output)
	}
	if result.Disposition != BlockedDraft || len(result.Requirements) != 1 ||
		result.Requirements[0].Value != "alice:"+projectionCanary {
		t.Fatalf("userinfo projection = %#v", result)
	}
	assertDisclosureSafe(t, []byte(output), result)
}

func TestProjectFormMalformedSensitiveIsRejectedWithoutDisclosure(t *testing.T) {
	t.Parallel()
	output, _, result, err := ProjectFormURLEncoded("client_secret=%zz"+projectionCanary, projectionOptions())
	if err != nil {
		t.Fatal(err)
	}
	if output != "" || result.Disposition != Rejected {
		t.Fatalf("malformed sensitive form = %q / %#v", output, result)
	}
	assertDisclosureSafe(t, []byte(output), result)
}

func TestMalformedStructuredFallbackUsesAuthoritativeCredentialVocabulary(t *testing.T) {
	t.Parallel()
	const opaqueValue = "opaque-canary-7f91"
	names := []string{"token", "secret", "cookie", "session", "signature", "pass"}
	for _, name := range names {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			jsonOutput, jsonResult, err := ProjectJSON(
				[]byte(`{"`+name+`":"`+opaqueValue),
				projectionOptions(),
			)
			if err != nil {
				t.Fatal(err)
			}
			assertMalformedCredentialRejected(t, "JSON", jsonOutput, jsonResult, opaqueValue)

			xmlOutput, xmlResult, err := ProjectXML(
				[]byte(`<root><`+name+`>`+opaqueValue),
				projectionOptions(),
			)
			if err != nil {
				t.Fatal(err)
			}
			assertMalformedCredentialRejected(t, "XML", xmlOutput, xmlResult, opaqueValue)

			formOutput, _, formResult, err := ProjectFormURLEncoded(
				name+"=%zz"+opaqueValue,
				projectionOptions(),
			)
			if err != nil {
				t.Fatal(err)
			}
			assertMalformedCredentialRejected(t, "form", []byte(formOutput), formResult, opaqueValue)
		})
	}
}

func TestMalformedStructuredFallbackRecognizesEncodedSensitiveNames(t *testing.T) {
	t.Parallel()
	const opaqueValue = "opaque-canary-encoded-28a1"

	jsonOutput, jsonResult, err := ProjectJSON(
		[]byte(`{"to\u006ben":"`+opaqueValue),
		projectionOptions(),
	)
	if err != nil {
		t.Fatal(err)
	}
	assertMalformedCredentialRejected(t, "JSON", jsonOutput, jsonResult, opaqueValue)

	formOutput, _, formResult, err := ProjectFormURLEncoded(
		"%74oken=%zz"+opaqueValue,
		projectionOptions(),
	)
	if err != nil {
		t.Fatal(err)
	}
	assertMalformedCredentialRejected(t, "form", []byte(formOutput), formResult, opaqueValue)
}

func assertMalformedCredentialRejected(t *testing.T, format string, output []byte, result Result, sensitiveValue string) {
	t.Helper()
	if len(output) != 0 {
		t.Fatalf("%s malformed credential output = %q, want no retained source bytes", format, output)
	}
	if result.Disposition != Rejected {
		t.Fatalf("%s malformed credential disposition = %q, want %q", format, result.Disposition, Rejected)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(output, []byte(sensitiveValue)) || bytes.Contains(encoded, []byte(sensitiveValue)) {
		t.Fatalf("%s malformed credential disclosed protected value", format)
	}
}

func TestProjectJSONPreservesBytesTypesAndDuplicateOccurrences(t *testing.T) {
	t.Parallel()
	payload := []byte("{\n  \"count\": 1.00e+2, \"password\": \"" + projectionCanary + "\", \"password\":\"" + projectionCanary + "\", \"ok\": true\n}")
	output, result, err := ProjectJSON(payload, projectionOptions())
	if err != nil {
		t.Fatal(err)
	}
	if result.Disposition != BlockedDraft {
		t.Fatalf("duplicate key disposition = %q, want blocked", result.Disposition)
	}
	if !json.Valid(output) {
		t.Fatalf("projected JSON is invalid: %s", output)
	}
	if !bytes.Contains(output, []byte(`"count": 1.00e+2`)) || !bytes.Contains(output, []byte(`"ok": true`)) {
		t.Fatalf("nonsensitive JSON bytes changed: %s", output)
	}
	if len(result.Requirements) != 2 {
		t.Fatalf("duplicate sensitive occurrences were collapsed: %#v", result.Requirements)
	}
	if result.Requirements[0].ID == result.Requirements[1].ID {
		t.Fatal("duplicate occurrences received the same provenance identity")
	}
	assertDisclosureSafe(t, output, result)
}

func TestProjectJSONRejectsSensitiveNonStringWithoutCoercion(t *testing.T) {
	t.Parallel()
	for _, payload := range [][]byte{
		[]byte(`{"password":123}`),
		[]byte(`{"token":true}`),
		[]byte(`{"secret":null}`),
		[]byte(`{"password":{"value":"x"}}`),
		[]byte(`{"token":["x"]}`),
	} {
		output, result, err := ProjectJSON(payload, projectionOptions())
		if err != nil {
			t.Fatal(err)
		}
		if output != nil || result.Disposition != Rejected {
			t.Fatalf("typed sensitive value was coerced or retained: %s / %#v", output, result)
		}
	}
}

func TestProjectJSONMalformedAndTrailingValues(t *testing.T) {
	t.Parallel()
	tests := []struct {
		payload     string
		disposition Disposition
		output      bool
	}{
		{`{"ordinary":`, BlockedDraft, true},
		{`{"password":"` + projectionCanary, Rejected, false},
		{`{"ordinary":1} {"ordinary":2}`, BlockedDraft, true},
		{`{"password":"` + projectionCanary + `"} {}`, Rejected, false},
	}
	for _, test := range tests {
		output, result, err := ProjectJSON([]byte(test.payload), projectionOptions())
		if err != nil {
			t.Fatal(err)
		}
		if result.Disposition != test.disposition || (output != nil) != test.output {
			t.Fatalf("ProjectJSON(%q) = %s / %q", test.payload, output, result.Disposition)
		}
		assertDisclosureSafe(t, output, result)
	}
}

func TestSchemaSensitiveNameOverridesProjectAndFailClosed(t *testing.T) {
	t.Parallel()
	schemaOptions := func() Options {
		options := projectionOptions()
		options.SensitiveNames = map[string]string{"pin": "pin"}
		return options
	}

	jsonOutput, jsonResult, err := ProjectJSON(
		[]byte(`{"pin":"`+projectionCanary+`","note":"unchanged"}`),
		schemaOptions(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if string(jsonOutput) != `{"pin":"{{secret.FIXTURE_PIN}}","note":"unchanged"}` ||
		len(jsonResult.Requirements) != 1 {
		t.Fatalf("schema-sensitive JSON = %s / %#v", jsonOutput, jsonResult)
	}
	assertDisclosureSafe(t, jsonOutput, jsonResult)

	formOutput, _, formResult, err := ProjectFormURLEncoded(
		"pin="+projectionCanary+"&note=unchanged",
		schemaOptions(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if formOutput != "pin={{secret.FIXTURE_PIN}}&note=unchanged" ||
		len(formResult.Requirements) != 1 {
		t.Fatalf("schema-sensitive form = %s / %#v", formOutput, formResult)
	}
	assertDisclosureSafe(t, []byte(formOutput), formResult)

	xmlOutput, xmlResult, err := ProjectXML(
		[]byte(`<root><pin>`+projectionCanary+`</pin><note>unchanged</note></root>`),
		schemaOptions(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if string(xmlOutput) != `<root><pin>{{secret.FIXTURE_PIN}}</pin><note>unchanged</note></root>` ||
		len(xmlResult.Requirements) != 1 {
		t.Fatalf("schema-sensitive XML = %s / %#v", xmlOutput, xmlResult)
	}
	assertDisclosureSafe(t, xmlOutput, xmlResult)

	for _, malformed := range []struct {
		name    string
		project func() ([]byte, Result, error)
	}{
		{
			name: "json",
			project: func() ([]byte, Result, error) {
				return ProjectJSON([]byte(`{"pin":"`+projectionCanary), schemaOptions())
			},
		},
		{
			name: "xml",
			project: func() ([]byte, Result, error) {
				return ProjectXML([]byte(`<root><pin>`+projectionCanary), schemaOptions())
			},
		},
	} {
		malformed := malformed
		t.Run(malformed.name, func(t *testing.T) {
			output, result, err := malformed.project()
			if err != nil {
				t.Fatal(err)
			}
			if output != nil || result.Disposition != Rejected {
				t.Fatalf("malformed schema-sensitive %s retained source: %s / %#v", malformed.name, output, result)
			}
			assertDisclosureSafe(t, output, result)
		})
	}
}

func TestProjectXMLPreservesNamespacesQuotesAndSurroundingBytes(t *testing.T) {
	t.Parallel()
	payload := []byte(`<s:Envelope xmlns:s="urn:s" xmlns:auth="urn:credentials"><s:Body><login ordinary="keep" auth-token='` + projectionCanary + `'><s:password>  ` + projectionCanary + `  </s:password><count>01</count></login></s:Body></s:Envelope>`)
	output, result, err := ProjectXML(payload, projectionOptions())
	if err != nil {
		t.Fatal(err)
	}
	if result.Disposition != ExecutableExact || len(result.Requirements) != 2 {
		t.Fatalf("XML result = %#v", result)
	}
	if !bytes.Contains(output, []byte(`xmlns:s="urn:s"`)) ||
		!bytes.Contains(output, []byte(`xmlns:auth="urn:credentials"`)) ||
		!bytes.Contains(output, []byte(`ordinary="keep"`)) ||
		!bytes.Contains(output, []byte(`<s:password>  {{secret.FIXTURE_PASSWORD}}  </s:password>`)) ||
		!bytes.Contains(output, []byte(`<count>01</count>`)) {
		t.Fatalf("XML surrounding bytes changed: %s", output)
	}
	assertDisclosureSafe(t, output, result)
}

func TestProjectXMLProjectsNamedCredentialFieldsAndValueAttributes(t *testing.T) {
	tests := []struct {
		name    string
		payload string
	}{
		{
			name:    "named value attribute",
			payload: `<root><entry name="pass&#x77;ord" value="` + projectionCanary + `"/></root>`,
		},
		{
			name:    "named element text",
			payload: `<root><property key="client_secret">` + projectionCanary + `</property></root>`,
		},
		{
			name:    "sensitive element value attribute",
			payload: `<root><password fixed="` + projectionCanary + `"/></root>`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output, result, err := ProjectXML([]byte(test.payload), projectionOptions())
			if err != nil {
				t.Fatalf("ProjectXML() error = %v", err)
			}
			if result.Disposition != ExecutableExact || len(result.Requirements) != 1 {
				t.Fatalf("result = %#v, want one executable requirement", result)
			}
			if bytes.Contains(output, []byte(projectionCanary)) ||
				!bytes.Contains(output, []byte(`{{secret.`)) {
				t.Fatalf("ProjectXML() leaked named credential: %s", output)
			}
			if result.Requirements[0].Value != projectionCanary {
				t.Fatalf("protected value = %q, want canary", result.Requirements[0].Value)
			}
		})
	}
}

func TestProjectXMLSanitizesSensitiveMixedContentAsBlockedDraft(t *testing.T) {
	t.Parallel()
	payload := []byte(`<root><password>before<x token="` + projectionCanary + `"/>after</password></root>`)
	output, result, err := ProjectXML(payload, projectionOptions())
	if err != nil {
		t.Fatal(err)
	}
	if result.Disposition != BlockedDraft ||
		!bytes.Contains(output, []byte(`<password>[REDACTED]</password>`)) {
		t.Fatalf("mixed sensitive XML was not converted to a safe blocked draft: %s / %#v", output, result)
	}
	if len(result.Requirements) != 0 {
		t.Fatalf("mixed XML created a wire-unsafe credential requirement: %#v", result.Requirements)
	}
	assertDisclosureSafe(t, output, result)
}

func TestProjectXMLSanitizesCredentialsInUnsupportedMarkup(t *testing.T) {
	t.Parallel()
	tests := []string{
		`<root><![CDATA[password=` + projectionCanary + `]]></root>`,
		`<!-- password=` + projectionCanary + ` --><root/>`,
		`<?auth password="` + projectionCanary + `" ?><root/>`,
		`<!DOCTYPE root SYSTEM "password-` + projectionCanary + `"><root/>`,
		`<!DOCTYPE root [<!ENTITY password "` + projectionCanary + `">]><root/>`,
	}
	for _, payload := range tests {
		output, result, err := ProjectXML([]byte(payload), projectionOptions())
		if err != nil {
			t.Fatal(err)
		}
		if result.Disposition != BlockedDraft ||
			bytes.Contains(output, []byte(projectionCanary)) ||
			bytes.Equal(output, []byte(payload)) {
			t.Fatalf("unsupported XML credential markup was retained: %s / %#v", output, result)
		}
		assertDisclosureSafe(t, output, result)
	}
}

func TestProjectXMLPreservesAdapterProvenWholeRuntimeValue(t *testing.T) {
	t.Parallel()
	const runtimeValue = "{{data.wsdl.password}}"
	options := projectionOptions()
	options.TrustedWholeValues = map[string]struct{}{runtimeValue: {}}
	output, result, err := ProjectXML([]byte(`<root><password>`+runtimeValue+`</password></root>`), options)
	if err != nil {
		t.Fatal(err)
	}
	if string(output) != `<root><password>`+runtimeValue+`</password></root>` ||
		result.Disposition != ExecutableExact ||
		len(result.Requirements) != 0 {
		t.Fatalf("trusted runtime value changed: %s / %#v", output, result)
	}
}

func TestProjectXMLRejectsMalformedSensitive(t *testing.T) {
	t.Parallel()
	output, result, err := ProjectXML([]byte(`<root><password>`+projectionCanary), projectionOptions())
	if err != nil {
		t.Fatal(err)
	}
	if output != nil || result.Disposition != Rejected {
		t.Fatalf("unsafe XML retained: %s / %#v", output, result)
	}
	assertDisclosureSafe(t, output, result)
}

func TestProjectMultipartPreservesBoundaryPartsAndFiles(t *testing.T) {
	t.Parallel()
	input := Multipart{
		Boundary: "fixture-boundary",
		Parts: []MultipartPart{
			{
				Headers: []HeaderOccurrence{{Name: "X-Trace", Value: "one"}},
				Name:    "note",
				Body:    []byte("unchanged"),
			},
			{
				Name: "password",
				Body: []byte(projectionCanary),
			},
			{
				Name:        "document",
				FileName:    "report.txt",
				ContentType: "text/plain",
				Body:        []byte("file bytes"),
			},
		},
	}
	output, result, err := ProjectMultipart(input, projectionOptions())
	if err != nil {
		t.Fatal(err)
	}
	if output.Boundary != input.Boundary || len(output.Parts) != 3 ||
		string(output.Parts[0].Body) != "unchanged" ||
		string(output.Parts[2].Body) != "file bytes" ||
		string(output.Parts[1].Body) == projectionCanary {
		t.Fatalf("multipart structure changed or leaked: %#v", output)
	}
	assertDisclosureSafe(t, output.Parts[1].Body, result)
}

func TestProjectMultipartBoundsHeadersAndMetadata(t *testing.T) {
	t.Parallel()
	options := projectionOptions()
	options.Limits = Limits{MaxBytes: 64, MaxDepth: 2, MaxValues: 2, MaxParts: 2}
	input := Multipart{
		Boundary: "b",
		Parts: []MultipartPart{{
			Name: "one",
			Headers: []HeaderOccurrence{
				{Name: "X-One", Value: "1"},
				{Name: "X-Two", Value: "2"},
			},
		}},
	}
	if output, result, err := ProjectMultipart(input, options); err != nil ||
		len(output.Parts) != 0 || result.Disposition != Rejected {
		t.Fatalf("multipart value limit output=%#v result=%#v err=%v", output, result, err)
	}

	wire := "--b\r\nX-One: 1\r\nX-Two: 2\r\n\r\nbody\r\n--b--\r\n"
	if output, result, err := ProjectMultipartWire(
		[]byte(wire),
		"multipart/form-data",
		"b",
		options,
	); err != nil || output != nil || result.Disposition != Rejected {
		t.Fatalf("multipart wire value limit output=%q result=%#v err=%v", output, result, err)
	}
}

func TestProjectMultipartRejectsSensitiveFileAndBadBoundary(t *testing.T) {
	t.Parallel()
	tests := []Multipart{
		{Boundary: "", Parts: []MultipartPart{{Name: "ordinary", Body: []byte("x")}}},
		{Boundary: "ok", Parts: []MultipartPart{{Name: "password", FileName: "secret.bin", Body: []byte(projectionCanary)}}},
	}
	for _, input := range tests {
		output, result, err := ProjectMultipart(input, projectionOptions())
		if err != nil {
			t.Fatal(err)
		}
		if result.Disposition != Rejected {
			t.Fatalf("multipart disposition = %q, output %#v", result.Disposition, output)
		}
		serialized, _ := json.Marshal(output)
		assertDisclosureSafe(t, serialized, result)
	}
}

func TestProjectMultipartProjectsCredentialsInsideNeutralStructuredParts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		contentType string
		body        string
		wantStable  string
	}{
		{
			name:        "json",
			contentType: "application/json",
			body:        "{\n  \"password\": \"" + projectionCanary + "\",\n  \"count\": 1.00e+2\n}",
			wantStable:  `"count": 1.00e+2`,
		},
		{
			name:        "xml",
			contentType: "application/xml",
			body:        `<root ordinary="keep"><password>` + projectionCanary + `</password></root>`,
			wantStable:  `ordinary="keep"`,
		},
		{
			name:        "form",
			contentType: "application/x-www-form-urlencoded",
			body:        "ordinary=a%2Fb&password=" + projectionCanary,
			wantStable:  "ordinary=a%2Fb",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			input := Multipart{
				Boundary: "nested-boundary",
				Parts: []MultipartPart{{
					Name: "payload",
					Headers: []HeaderOccurrence{{
						Name: "Content-Type", Value: test.contentType,
					}},
					Body: []byte(test.body),
				}},
			}
			output, result, err := ProjectMultipart(input, projectionOptions())
			if err != nil {
				t.Fatal(err)
			}
			if result.Disposition == Rejected || len(output.Parts) != 1 {
				t.Fatalf("nested multipart projection = %#v / %#v", output, result)
			}
			if !bytes.Contains(output.Parts[0].Body, []byte(test.wantStable)) {
				t.Fatalf("nonsensitive nested bytes changed: %s", output.Parts[0].Body)
			}
			assertDisclosureSafe(t, output.Parts[0].Body, result)
		})
	}
}

func TestProjectMultipartDoesNotReuseRequestHeaderExemptionsForPartHeaders(t *testing.T) {
	t.Parallel()
	options := projectionOptions()
	options.PreGovernedHeaderOccurrences = map[int]struct{}{0: {}}
	output, result, err := ProjectMultipart(Multipart{
		Boundary: "part-header-boundary",
		Parts: []MultipartPart{{
			Name: "payload",
			Headers: []HeaderOccurrence{{
				Name:  "Authorization",
				Value: "Bearer " + projectionCanary,
			}},
			Body: []byte("ordinary"),
		}},
	}, options)
	if err != nil {
		t.Fatal(err)
	}
	if result.Disposition == Rejected || len(output.Parts) != 1 ||
		strings.Contains(output.Parts[0].Headers[0].Value, projectionCanary) ||
		!strings.Contains(output.Parts[0].Headers[0].Value, "{{secret.") {
		t.Fatalf("multipart part header exemption leaked from request headers: %#v / %#v", output, result)
	}
	assertDisclosureSafe(t, []byte(output.Parts[0].Headers[0].Value), result)
}

func TestProjectMultipartRecursivelyProjectsNestedMultipart(t *testing.T) {
	t.Parallel()
	const innerBoundary = "inner-credential-boundary"
	inner := "--" + innerBoundary + "\r\n" +
		"Content-Disposition: form-data; name=\"password\"\r\n\r\n" +
		projectionCanary + "\r\n" +
		"--" + innerBoundary + "--\r\n"
	output, result, err := ProjectMultipart(Multipart{
		Boundary: "outer-credential-boundary",
		Parts: []MultipartPart{{
			Name: "payload",
			Headers: []HeaderOccurrence{{
				Name:  "Content-Type",
				Value: "multipart/mixed; boundary=" + innerBoundary,
			}},
			Body: []byte(inner),
		}},
	}, projectionOptions())
	if err != nil {
		t.Fatal(err)
	}
	if result.Disposition == Rejected || len(output.Parts) != 1 ||
		strings.Contains(string(output.Parts[0].Body), projectionCanary) ||
		!strings.Contains(string(output.Parts[0].Body), "{{secret.") {
		t.Fatalf("nested multipart credential was not governed: %#v / %#v", output, result)
	}
	assertDisclosureSafe(t, output.Parts[0].Body, result)
}

func TestProjectMultipartProjectsCredentialHintInsideNeutralPlainPart(t *testing.T) {
	t.Parallel()
	body := []byte("password = " + projectionCanary + "\nordinary prose")
	output, result, err := ProjectMultipart(Multipart{
		Boundary: "plain-part-boundary",
		Parts: []MultipartPart{{
			Name:        "payload",
			ContentType: "text/plain",
			Body:        body,
		}},
	}, projectionOptions())
	if err != nil {
		t.Fatal(err)
	}
	if result.Disposition == Rejected || len(output.Parts) != 1 ||
		string(output.Parts[0].Body) != "{{secret.FIXTURE_RAW_BODY}}" ||
		len(result.Requirements) != 1 ||
		result.Requirements[0].Value != string(body) {
		t.Fatalf("neutral plain multipart credential was not governed: %#v / %#v", output, result)
	}
	assertDisclosureSafe(t, output.Parts[0].Body, result)
}

func TestProjectMultipartRejectsExcessiveNesting(t *testing.T) {
	t.Parallel()
	const innerBoundary = "inner-depth-boundary"
	inner := "--" + innerBoundary + "\r\n" +
		"Content-Disposition: form-data; name=\"ordinary\"\r\n\r\n" +
		"value\r\n" +
		"--" + innerBoundary + "--\r\n"
	options := projectionOptions()
	options.Limits.MaxDepth = 1
	output, result, err := ProjectMultipart(Multipart{
		Boundary: "outer-depth-boundary",
		Parts: []MultipartPart{{
			Name: "payload",
			Headers: []HeaderOccurrence{{
				Name:  "Content-Type",
				Value: "multipart/mixed; boundary=" + innerBoundary,
			}},
			Body: []byte(inner),
		}},
	}, options)
	if err != nil {
		t.Fatal(err)
	}
	if result.Disposition != Rejected || len(output.Parts) != 0 {
		t.Fatalf("excessively nested multipart was retained: %#v / %#v", output, result)
	}
}

func TestProjectMultipartRejectsUnsafeNeutralStructuredPart(t *testing.T) {
	t.Parallel()
	for _, part := range []MultipartPart{
		{
			Name:    "payload",
			Headers: []HeaderOccurrence{{Name: "Content-Type", Value: "application/json"}},
			Body:    []byte(`{"password":7}`),
		},
		{
			Name:    "payload",
			Headers: []HeaderOccurrence{{Name: "Content-Type", Value: "application/graphql"}},
			Body:    []byte(`mutation { login(password: "` + projectionCanary + `") }`),
		},
		{
			Name:    "payload",
			Headers: []HeaderOccurrence{{Name: "Content-Type", Value: "application/json"}},
			Body:    []byte(`{"query":"mutation { login(password: \"` + projectionCanary + `\") }"}`),
		},
	} {
		output, result, err := ProjectMultipart(Multipart{
			Boundary: "nested-boundary",
			Parts:    []MultipartPart{part},
		}, projectionOptions())
		if err != nil {
			t.Fatal(err)
		}
		if result.Disposition != Rejected || len(output.Parts) != 0 {
			t.Fatalf("unsafe nested multipart retained: %#v / %#v", output, result)
		}
		serialized, _ := json.Marshal(output)
		assertDisclosureSafe(t, serialized, result)
	}
}

func TestProjectMultipartHonorsAdapterSensitiveNames(t *testing.T) {
	t.Parallel()
	options := projectionOptions()
	options.SensitiveNames = map[string]string{"pin": "account_pin"}
	output, result, err := ProjectMultipart(Multipart{
		Boundary: "adapter-sensitive-boundary",
		Parts: []MultipartPart{{
			Name: "PIN",
			Body: []byte(projectionCanary),
		}},
	}, options)
	if err != nil {
		t.Fatal(err)
	}
	if result.Disposition == Rejected || len(output.Parts) != 1 {
		t.Fatalf("adapter-sensitive multipart projection = %#v / %#v", output, result)
	}
	if strings.Contains(string(output.Parts[0].Body), projectionCanary) ||
		!strings.Contains(string(output.Parts[0].Body), "{{secret.") {
		t.Fatalf("adapter-sensitive multipart field was not governed: %q", output.Parts[0].Body)
	}
	assertDisclosureSafe(t, output.Parts[0].Body, result)
}

func TestProjectGraphQLProjectsVariablesAndRejectsInlineCredential(t *testing.T) {
	t.Parallel()
	input := GraphQL{
		Query:     []byte(`mutation Login($password: String!) { login(password: $password) { id } }`),
		Variables: []byte(`{"password":"` + projectionCanary + `","count":2}`),
	}
	output, result, err := ProjectGraphQL(input, projectionOptions())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(output.Query, input.Query) || !json.Valid(output.Variables) {
		t.Fatalf("GraphQL structure changed: %#v", output)
	}
	assertDisclosureSafe(t, output.Variables, result)

	output, result, err = ProjectGraphQL(GraphQL{
		Query: []byte(`mutation { login(password: "` + projectionCanary + `") }`),
	}, projectionOptions())
	if err != nil {
		t.Fatal(err)
	}
	if output.Query != nil || result.Disposition != Rejected {
		t.Fatalf("inline GraphQL credential was retained: %#v / %#v", output, result)
	}
	assertDisclosureSafe(t, output.Query, result)

	output, result, err = ProjectGraphQL(GraphQL{
		Query: []byte(`mutation Login($clientSecret: String) { login(clientSecret: "` + projectionCanary + `", privateKey: "x") }`),
	}, projectionOptions())
	if err != nil {
		t.Fatal(err)
	}
	if output.Query != nil || result.Disposition != Rejected {
		t.Fatalf("inline GraphQL credential after a variable definition was retained: %#v / %#v", output, result)
	}
	assertDisclosureSafe(t, output.Query, result)
}

func TestProjectGraphQLRejectsCredentialLiteralsAcrossIgnoredTokensAndDefaults(t *testing.T) {
	t.Parallel()
	tests := []string{
		`mutation { login(password                    : "` + projectionCanary + `") }`,
		"mutation { login(password # arbitrary comment\n : \"" + projectionCanary + "\") }",
		`mutation Login($password: String = "` + projectionCanary + `") { login(password: $password) }`,
		`mutation { login(password: """` + projectionCanary + `""") }`,
		`mutation { login(password: 1234) }`,
		`mutation { login(password: true) }`,
		`mutation { login(password: null) }`,
		`mutation { login(password: CREDENTIAL_ENUM) }`,
		`mutation { login(password: [1, 2]) }`,
		`mutation { login(password: { value: 1 }) }`,
		`mutation Login($password: Int = 1234) { login(password: $password) }`,
		`mutation Login($password: Auth = { value: 1 }) { login(password: $password) }`,
	}
	for _, query := range tests {
		output, result, err := ProjectGraphQL(GraphQL{Query: []byte(query)}, projectionOptions())
		if err != nil {
			t.Fatal(err)
		}
		if output.Query != nil || result.Disposition != Rejected {
			t.Fatalf("inline GraphQL credential was retained for %q: %#v / %#v", query, output, result)
		}
		assertDisclosureSafe(t, output.Query, result)
	}
}

func TestProjectGraphQLAllowsSensitiveNamedFieldAlias(t *testing.T) {
	t.Parallel()
	input := GraphQL{Query: []byte(`query { password: credentialStatus }`)}
	output, result, err := ProjectGraphQL(input, projectionOptions())
	if err != nil {
		t.Fatal(err)
	}
	if result.Disposition != ExecutableExact || !bytes.Equal(output.Query, input.Query) {
		t.Fatalf("valid sensitive-named alias was treated as a credential literal: %#v / %#v", output, result)
	}
}

func TestProjectGraphQLScannerIsBounded(t *testing.T) {
	t.Parallel()
	options := projectionOptions()
	options.Limits.MaxValues = 4
	output, result, err := ProjectGraphQL(GraphQL{
		Query: []byte(`query { one two three four five }`),
	}, options)
	if err != nil {
		t.Fatal(err)
	}
	if output.Query != nil || result.Disposition != Rejected {
		t.Fatalf("over-limit GraphQL source was retained: %#v / %#v", output, result)
	}
}

func TestProjectionAuditModeAddsUnmanagedBlocker(t *testing.T) {
	t.Parallel()
	options := projectionOptions()
	options.Mode = Audit
	output, result, err := ProjectHeaders([]HeaderOccurrence{{Name: "Authorization", Value: projectionCanary}}, options)
	if err != nil {
		t.Fatal(err)
	}
	if output[0].Value != projectionCanary || result.Disposition != BlockedDraft {
		t.Fatalf("audit compatibility behavior changed: %#v / %#v", output, result)
	}
	found := false
	for _, finding := range result.Findings {
		found = found || finding.Code == "import_credentials_unmanaged"
	}
	if !found {
		t.Fatalf("audit mode omitted unmanaged blocker: %#v", result)
	}
}

func TestProjectionIsDeterministicAndIDsDoNotDependOnValues(t *testing.T) {
	t.Parallel()
	var firstOutput []HeaderOccurrence
	var firstResult Result
	for iteration := 0; iteration < 25; iteration++ {
		options := projectionOptions()
		output, result, err := ProjectHeaders([]HeaderOccurrence{{
			Name:  "Authorization",
			Value: "Bearer " + projectionCanary,
		}}, options)
		if err != nil {
			t.Fatal(err)
		}
		if iteration == 0 {
			firstOutput, firstResult = output, result
			continue
		}
		left, _ := json.Marshal(struct {
			Output []HeaderOccurrence
			Result Result
		}{firstOutput, firstResult})
		right, _ := json.Marshal(struct {
			Output []HeaderOccurrence
			Result Result
		}{output, result})
		if !bytes.Equal(left, right) {
			t.Fatalf("projection %d was nondeterministic:\n%s\n%s", iteration, left, right)
		}
	}
	rotatedOptions := projectionOptions()
	_, rotated, err := ProjectHeaders([]HeaderOccurrence{{
		Name:  "Authorization",
		Value: "Bearer rotated-value",
	}}, rotatedOptions)
	if err != nil {
		t.Fatal(err)
	}
	if firstResult.Requirements[0].ID != rotated.Requirements[0].ID {
		t.Fatal("requirement ID depends on source value")
	}
}

func FuzzProjectJSONBounded(f *testing.F) {
	f.Add([]byte(`{"password":"secret","n":1}`))
	f.Add([]byte(`{"a":1,"a":2}`))
	f.Add([]byte(`{"password":false}`))
	f.Fuzz(func(t *testing.T, payload []byte) {
		options := projectionOptions()
		options.Limits.MaxBytes = 4096
		if len(payload) > options.Limits.MaxBytes {
			payload = payload[:options.Limits.MaxBytes+1]
		}
		_, _, _ = ProjectJSON(payload, options)
	})
}

func FuzzProjectXMLBounded(f *testing.F) {
	f.Add([]byte(`<root password="secret"><token>x</token></root>`))
	f.Add([]byte(`<root><password><![CDATA[x]]></password></root>`))
	f.Fuzz(func(t *testing.T, payload []byte) {
		options := projectionOptions()
		options.Limits.MaxBytes = 4096
		if len(payload) > options.Limits.MaxBytes {
			payload = payload[:options.Limits.MaxBytes+1]
		}
		_, _, _ = ProjectXML(payload, options)
	})
}

func FuzzProjectFormBounded(f *testing.F) {
	f.Add("password=secret&ordinary=a+b&ordinary=a%20b")
	f.Add("client_secret=%zz")
	f.Add("flag&blank=&token={{var.dynamic}}")
	f.Fuzz(func(t *testing.T, payload string) {
		options := projectionOptions()
		options.Limits.MaxBytes = 4096
		if len(payload) > options.Limits.MaxBytes {
			payload = payload[:options.Limits.MaxBytes+1]
		}
		_, _, _, _ = ProjectFormURLEncoded(payload, options)
	})
}
