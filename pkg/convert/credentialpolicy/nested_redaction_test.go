package credentialpolicy

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/url"
	"runtime"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/credential"
)

func redactionJSONWrapper(t *testing.T, payload string) string {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return `{"payload":` + string(encoded) + `,"ordinary":1.00e+2}`
}

func TestNestedCredentialRedactionPreservesBusinessAndUnselectedSpans(t *testing.T) {
	t.Parallel()
	inner := "{\n  \"recordId\": \"business-fixture\", \"apiToken\": \"" + projectionCanary + "\", \"count\": 1.00e+2\n}"
	for _, test := range []struct{ name, body, contentType string }{
		{"form JSON", "ordinary=a%2fb%2Bc&message=" + url.QueryEscape(inner) + "&duplicate=one&duplicate=two&flag", "application/x-www-form-urlencoded"},
		{"JSON string", redactionJSONWrapper(t, inner), "application/json"},
		{"form JSON string", "message=" + url.QueryEscape(redactionJSONWrapper(t, inner)) + "&ordinary=a%2fb%2Bc", "application/x-www-form-urlencoded"},
	} {
		t.Run(test.name, func(t *testing.T) {
			options := Options{Mode: Redact, KnownCredentialMatcher: func(value string) bool { return strings.Contains(value, projectionCanary) }}
			output, result, err := ProjectRequestBody([]byte(test.body), test.contentType, options)
			if err != nil || result.Disposition == Rejected || len(output) == 0 {
				t.Fatalf("projection failed: %v / %s", err, result.Disposition)
			}
			assertDisclosureSafe(t, output, result)
			semantic := string(output)
			if test.contentType == "application/x-www-form-urlencoded" {
				values, parseErr := url.ParseQuery(semantic)
				if parseErr != nil {
					t.Fatal(parseErr)
				}
				semantic = values.Get("message")
				if !strings.Contains(string(output), "ordinary=a%2fb%2Bc") {
					t.Fatal("unselected form bytes changed")
				}
			}
			if test.name != "form JSON" {
				var wrapper struct {
					Payload string `json:"payload"`
				}
				if json.Unmarshal([]byte(semantic), &wrapper) != nil {
					t.Fatal("invalid projected JSON wrapper")
				}
				semantic = wrapper.Payload
			}
			want := strings.Replace(inner, projectionCanary, credential.Redacted, 1)
			if semantic != want {
				t.Fatal("nested nonsensitive JSON spans changed or whole structure collapsed")
			}
		})
	}
}

func TestNestedCredentialRedactionUnchangedBytesAndSentinel(t *testing.T) {
	t.Parallel()
	for _, body := range []string{
		`ordinary=a%2fb%2Bc&message=%7b%22recordId%22%3a%22business%22%7d&message=%5b1%2c2%5d`,
		`{"payload":"{\"recordId\":\"business\",\"count\":1.00e+2}","placeholder":"[REDACTED]"}`,
		`{"payload":"[1, 2, 3]","ordinary":"\ud83d\ude00"}`,
	} {
		output, result, err := ProjectRequestBody([]byte(body), "", Options{Mode: Redact})
		if err != nil || result.Disposition == Rejected || string(output) != body {
			t.Fatalf("unchanged body was rewritten: %v / %s", err, result.Disposition)
		}
	}
}

func TestNestedCredentialRedactionMasksEveryDuplicateOccurrence(t *testing.T) {
	t.Parallel()
	body := `{"apiToken":"` + projectionCanary + `","apiToken":"` + projectionCanary + `","recordId":"business"}`
	output, _, result, err := ProjectFormURLEncoded("message="+url.QueryEscape(body)+"&message="+url.QueryEscape(body), Options{Mode: Redact})
	if err != nil || result.Disposition == Rejected {
		t.Fatalf("duplicate projection failed: %v / %s", err, result.Disposition)
	}
	assertDisclosureSafe(t, []byte(output), result)
	if strings.Count(output, credential.Redacted) != 4 {
		t.Fatal("not every duplicate credential occurrence was masked")
	}
}

// A credential inside a JSON form value is masked where it sits: every other
// byte keeps its recorded encoding, so the projection lines up with the
// recording, and the placeholder stays literal, where review finds it.
func TestNestedJSONFormValueKeepsItsRecordedEncoding(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ name, body, want string }{
		{
			name: "raw JSON",
			body: `password=` + projectionCanary + `&data={"api_key":"` + projectionCanary + `"}`,
			want: `password=[REDACTED]&data={"api_key":"[REDACTED]"}`,
		},
		{
			name: "uppercase escapes",
			body: `data=%7B%22api_key%22%3A%22` + projectionCanary + `%22%7D&x=1`,
			want: `data=%7B%22api_key%22%3A%22[REDACTED]%22%7D&x=1`,
		},
		{
			name: "lowercase escapes",
			body: `data=%7b%22api_key%22%3a%22` + projectionCanary + `%22%7d`,
			want: `data=%7b%22api_key%22%3a%22[REDACTED]%22%7d`,
		},
		{
			name: "spaces and non-ASCII around it",
			body: `data=%7B%22note%22%3A%22caf%C3%A9+au+lait%22%2C%22token%22%3A%22` + projectionCanary + `%22%7D`,
			want: `data=%7B%22note%22%3A%22caf%C3%A9+au+lait%22%2C%22token%22%3A%22[REDACTED]%22%7D`,
		},
		{
			name: "credential sharing the placeholder's brackets",
			body: `data=%7B%22api_key%22%3A%22%5B` + projectionCanary + `%5D%22%7D`,
			want: `data=%7B%22api_key%22%3A%22[REDACTED]%22%7D`,
		},
		{
			name: "two credentials",
			body: `message={"apiToken":"` + projectionCanary + `","recordId":"business","secret":"` + projectionCanary + `"}&ordinary=a%2fb`,
			want: `message={"apiToken":"[REDACTED]","recordId":"business","secret":"[REDACTED]"}&ordinary=a%2fb`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			output, _, result, err := ProjectFormURLEncoded(test.body, Options{Mode: Redact})
			if err != nil || result.Disposition == Rejected {
				t.Fatalf("projection failed: %v / %s", err, result.Disposition)
			}
			assertDisclosureSafe(t, []byte(output), result)
			if output != test.want {
				t.Fatalf("projected = %s\nwant      %s", output, test.want)
			}
		})
	}
}

// The splice keeps a replacement's unchanged ends as recorded, writes the
// rest escaped only as a form requires, and refuses what does not round-trip.
func TestSpliceFormValue(t *testing.T) {
	t.Parallel()
	raw := `%7B%22k%22%3A%22old%22%7D`
	decoded := `{"k":"old"}`
	spans := []replacement{{start: 5, end: 10, value: []byte(`"a&b c%"`)}}
	projected := `{"k":"a&b c%"}`
	got, ok := spliceFormValue(raw, decoded, projected, spans)
	if !ok || got != `%7B%22k%22%3A%22a%26b+c%25%22%7D` {
		t.Fatalf("splice = %q %t", got, ok)
	}
	if _, ok := spliceFormValue(raw, `{"k":"other"}`, projected, spans); ok {
		t.Fatal("a raw value that does not decode to the decoded text was spliced")
	}
	if _, ok := spliceFormValue(raw, decoded, `{"k":"different"}`, spans); ok {
		t.Fatal("a splice that does not decode to the projection was accepted")
	}
	if _, ok := spliceFormValue(raw, decoded, projected, nil); ok {
		t.Fatal("a splice without spans was accepted")
	}
	if _, ok := spliceFormValue(raw, decoded, projected, []replacement{{start: 5, end: 10}, {start: 6, end: 8}}); ok {
		t.Fatal("overlapping spans were accepted")
	}
}

func TestNestedCredentialRedactionRejectsMalformedAndLimitedDataAtomically(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ name, nested string }{
		{"malformed object", `{"ordinary":`},
		{"malformed array", `[unclosed`},
		{"trailing JSON", `{"ordinary":1} {"password":"` + projectionCanary + `"}`},
		{"invalid UTF8", "{\"ordinary\":\"\xff\"}"},
		{"high surrogate", `{"ordinary":"\ud800"}`},
		{"low surrogate", `{"ordinary":"\udc00"}`},
		{"invalid surrogate pair", `{"ordinary":"\ud800\u0020"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			bodies := []string{"message=" + url.QueryEscape(test.nested), redactionJSONWrapper(t, test.nested)}
			if test.name == "invalid UTF8" {
				bodies[1] = test.nested // json.Marshal would repair invalid bytes before testing the source.
			}
			for _, body := range bodies {
				output, result, err := ProjectRequestBody([]byte(body), "", Options{Mode: Redact})
				if err != nil || result.Disposition != Rejected || output != nil {
					t.Fatalf("unsafe nested body returned a partial projection: %v / %s", err, result.Disposition)
				}
				assertDisclosureSafe(t, output, result)
			}
		})
	}
	base := `{"apiToken":"` + projectionCanary + `","recordId":"business"}`
	for layers := 1; layers <= 4; layers++ {
		body := base
		for count := 0; count < layers; count++ {
			body = redactionJSONWrapper(t, body)
		}
		output, result, err := ProjectRequestBody([]byte(body), "application/json", Options{Mode: Redact})
		if err != nil {
			t.Fatal(err)
		}
		if layers <= 3 {
			if result.Disposition == Rejected {
				t.Fatalf("supported layer %d rejected", layers)
			}
			assertDisclosureSafe(t, output, result)
		} else if result.Disposition != Rejected || output != nil {
			t.Fatal("over-depth body returned a partial projection")
		}
	}
}

func TestNestedCredentialRedactionSharesWorkValuesAndDecodedDepth(t *testing.T) {
	t.Parallel()
	body := redactionJSONWrapper(t, `{"apiToken":"`+projectionCanary+`","recordId":"business"}`)
	for _, test := range []struct {
		name    string
		options Options
	}{
		{"shared work", Options{Mode: Redact, structuredRedactionBudget: &structuredRedactionBudget{remainingBytes: len(body), remainingValues: 100}}},
		{"shared values", Options{Mode: Redact, Limits: Limits{MaxValues: 4}}},
		{"combined decoded depth", Options{Mode: Redact, Limits: Limits{MaxDepth: 1}}},
		{"expanded output", Options{Mode: Redact, Limits: Limits{MaxBytes: len(`{"apiToken":"a"}`)}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := body
			if test.name == "expanded output" {
				input = `{"apiToken":"a"}`
			}
			output, result, err := ProjectRequestBody([]byte(input), "application/json", test.options)
			if err != nil || result.Disposition != Rejected || output != nil {
				t.Fatalf("limit did not reject atomically: %v / %s", err, result.Disposition)
			}
		})
	}
}

func TestNestedCredentialRedactionLeavesExecutableModesUnchanged(t *testing.T) {
	t.Parallel()
	body := redactionJSONWrapper(t, `{"api_token":"`+projectionCanary+`","recordId":"business"}`)
	for _, mode := range []ProjectionMode{Preserve, SecretReference, Audit} {
		output, result, err := ProjectRequestBody([]byte(body), "application/json", Options{Mode: mode})
		if err != nil || result.Disposition == Rejected || !bytes.Equal(output, []byte(body)) {
			t.Fatalf("nested executable projection changed for %s: %v / %s", mode, err, result.Disposition)
		}
	}
	// apiToken is intentionally new shared vocabulary. Preserve recognizes it
	// and records the existing requirement/target while retaining source bytes.
	body = `{"apiToken":"` + projectionCanary + `","recordId":"business"}`
	output, result, err := ProjectRequestBody([]byte(body), "application/json", Options{Mode: Preserve})
	if err != nil || string(output) != body || len(result.Requirements) != 0 || len(result.PreservedTargets) != 1 {
		t.Fatalf("apiToken preserve accounting failed: %v / %d / %d", err, len(result.Requirements), len(result.PreservedTargets))
	}
	form := "message=" + url.QueryEscape(body)
	output, result, err = ProjectRequestBody([]byte(form), "application/x-www-form-urlencoded", Options{Mode: Preserve})
	if err != nil || string(output) != form || len(result.Requirements) != 0 || len(result.PreservedTargets) != 1 {
		t.Fatalf("apiToken form preserve accounting failed: %v / %d / %d", err, len(result.Requirements), len(result.PreservedTargets))
	}
}

func TestNestedRedactionEscapingPreflightMatchesWireEncoding(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"ordinary / + & < >", "quote\"\\\n\x00", "unicode界\u2028\u2029"} {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		for _, size := range []int{len(encoded) - 1, len(encoded)} {
			options, normalizeErr := normalizeOptions(Options{Mode: Redact, Limits: Limits{MaxBytes: size}})
			if normalizeErr != nil {
				t.Fatal(normalizeErr)
			}
			if got := reserveJSONEncodedRedaction(options, []byte(value)); got != (size == len(encoded)) {
				t.Fatal("JSON output preflight diverged from encoder")
			}
		}
		form := url.QueryEscape(value)
		for _, size := range []int{len(form) - 1, len(form)} {
			options, normalizeErr := normalizeOptions(Options{Mode: Redact, Limits: Limits{MaxBytes: size}})
			if normalizeErr != nil {
				t.Fatal(normalizeErr)
			}
			if got := reserveFormEncodedRedaction(options, []byte(value)); got != (size == len(form)) {
				t.Fatal("form output preflight diverged from encoder")
			}
		}
	}
}

func TestFormRedactionRejectsCardinalityBeforeAllocation(t *testing.T) {
	// A tiny caller allowance must reject before Split can allocate one slot
	// for every repeated delimiter. Keep the large input outside measurement.
	raw := strings.Repeat("ordinary=&", 150_000)
	payload := []byte(raw)
	options := Options{Mode: Redact, Limits: Limits{MaxValues: 2}}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	fields, parseErr := ParseFormURLEncoded(raw, options)
	output, projectedFields, result, projectErr := ProjectFormURLEncoded(raw, options)
	inferred := looksLikeFormWire(payload)
	runtime.ReadMemStats(&after)
	if parseErr == nil || fields != nil || projectErr != nil || output != "" || projectedFields != nil || result.Disposition != Rejected || inferred {
		t.Fatal("form cardinality admission did not reject before materialization")
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 512<<10 {
		t.Fatalf("form cardinality rejection allocated %d bytes", allocated)
	}
}

func TestDirectFormRedactionRejectsSizeBeforeSourceScanning(t *testing.T) {
	for _, raw := range []string{"ordinary=" + strings.Repeat("x", 256), "message=" + strings.Repeat("%7B", 128)} {
		output, fields, result, err := ProjectFormURLEncoded(raw, Options{Mode: Redact, Limits: Limits{MaxBytes: 64}})
		if err != nil || output != "" || fields != nil || result.Disposition != Rejected ||
			len(result.Findings) != 1 || result.Findings[0].Code != "credential_form_limit_exceeded" {
			t.Fatal("oversized direct form was scanned or returned as a partial disclosure")
		}
	}
}

func TestNestedQueryJSONPrecedesGraphQLWithoutChangingAnonymousQueries(t *testing.T) {
	for _, inner := range []string{`{"apiToken":"` + projectionCanary + `","recordId":"business"}`, `[{"apiToken":"` + projectionCanary + `","recordId":"business"}]`} {
		quoted, err := json.Marshal(inner)
		if err != nil {
			t.Fatal(err)
		}
		body := `{"query":` + string(quoted) + `,"ordinary":1.00e+2}`
		output, result, err := ProjectRequestBody([]byte(body), "application/json", Options{Mode: Redact})
		if err != nil || result.Disposition == Rejected {
			t.Fatalf("nested query JSON projection failed: %v / %s", err, result.Disposition)
		}
		assertDisclosureSafe(t, output, result)
		var decoded struct {
			Query string `json:"query"`
		}
		if json.Unmarshal(output, &decoded) != nil || decoded.Query != strings.Replace(inner, projectionCanary, credential.Redacted, 1) {
			t.Fatal("query JSON semantic sibling changed")
		}
	}
	query := `{ login(password:"` + projectionCanary + `") { ok } }`
	quoted, _ := json.Marshal(query)
	body := `{"query":` + string(quoted) + `}`
	output, result, err := ProjectRequestBody([]byte(body), "application/json", Options{Mode: Redact, KnownCredentialMatcher: func(value string) bool { return value == projectionCanary }})
	if err != nil {
		t.Fatal(err)
	}
	for _, finding := range result.Findings {
		if finding.Code == "credential_nested_json_rejected" || finding.Code == "credential_nested_json_depth_limit_exceeded" {
			t.Fatal("anonymous GraphQL query was parsed as encoded JSON")
		}
	}
	assertDisclosureSafe(t, output, result)
}

func TestAppliedNestedBusinessTemplatesRemainDisclosureReadable(t *testing.T) {
	for _, template := range []string{"{{var.selected}}", "{{param.selected}}", "{{data.source.column}}", "{{env.VALUE}}", "{{secret.VALUE}}", "{{run.id}}", "{{file.upload}}", "{{fn.uuid()}}", "{{fake.email()}}"} {
		inner := `{"recordId":"` + template + `","ordinary":"business"}`
		for _, body := range []string{redactionJSONWrapper(t, inner), "message=" + url.QueryEscape(inner) + "&ordinary=one%20two"} {
			output, result, err := ProjectRequestBody([]byte(body), "", Options{Mode: Redact})
			if err != nil || result.Disposition == Rejected || string(output) != body {
				t.Fatalf("authored business template was treated as JSON: %v / %s", err, result.Disposition)
			}
		}
	}
	output, result, err := ProjectRequestBody([]byte(`{"payload":"{{bad object}}"}`), "application/json", Options{Mode: Redact})
	if err != nil || output != nil || result.Disposition != Rejected {
		t.Fatal("unsupported malformed double-brace value escaped structured validation")
	}
}

func TestMalformedQueryMemberSyntaxFailsClosedAcrossEncodedLayers(t *testing.T) {
	for _, inner := range []string{
		`{{"apiToken":"` + projectionCanary + `"}}`,
		`{ordinary:invalid,"apiToken":"` + projectionCanary + `"}`,
		`{ ordinary: safe "apiToken" : "` + projectionCanary + `" }`,
		`{!"api\u0054oken":"` + projectionCanary + `"}`,
		"{#ignored\n\"apiToken\":\"" + projectionCanary + "\"}",
		`{'apiToken':'` + projectionCanary + `'}`,
		`{ apiToken: '` + projectionCanary + `' }`,
		`query { "apiToken":"` + projectionCanary + `" }`,
		`mutation Invalid {ordinary:invalid,"apiToken":"` + projectionCanary + `"}`,
		"#comment\n{\"apiToken\":\"" + projectionCanary + "\"}",
		`{"apiToken" "` + projectionCanary + `"}`,
		`{"apiToken"`,
		`{ ordinary: safe "apiToken" "` + projectionCanary + `" }`,
		`query { lookup(input:{"apiToken" "` + projectionCanary + `"}) { id } }`,
		`prefix ' {\"apiToken\":\"` + projectionCanary + `\"}`,
	} {
		quoted, _ := json.Marshal(inner)
		queryBody := `{"query":` + string(quoted) + `}`
		for _, body := range []string{queryBody, redactionJSONWrapper(t, queryBody), "message=" + url.QueryEscape(queryBody)} {
			output, result, err := ProjectRequestBody([]byte(body), "", Options{Mode: Redact})
			if err != nil || result.Disposition != Rejected || output != nil {
				t.Fatal("malformed query member syntax produced partial direct disclosure")
			}
		}
	}
}

func TestJSONMemberPunctuationCannotUseRuntimeNamespaceException(t *testing.T) {
	for _, prefix := range []string{"fn.unknown(", "var.unknown", "file.unknown"} {
		inner := "{{" + prefix + `"apiToken":"` + projectionCanary + `"}}`
		for _, body := range []string{redactionJSONWrapper(t, inner), "message=" + url.QueryEscape(inner)} {
			output, result, err := ProjectRequestBody([]byte(body), "", Options{Mode: Redact})
			if err != nil || result.Disposition != Rejected || output != nil {
				t.Fatal("runtime namespace hid JSON member credential syntax")
			}
		}
	}
}

func TestQueryStructureClassificationPreservesGraphQLScalarBoundaries(t *testing.T) {
	for _, query := range []string{
		`{ renamed: lookup(recordId:"safe {\"apiToken\":\"quoted scalar\"}") { ok } }`,
		"{ lookup(recordId:\"\"\"safe {\"apiToken\":\"block scalar\"}\"\"\") { ...Fields } } # \"apiToken\":\"comment\"",
		`query Lookup($recordId: String = "safe") { lookup(id:$recordId) @include(if:true) { id } }`,
		`{ lookup(ids:["safe", "apiToken"], input:{ordinary:"safe", fields:[{ordinary:"safe"}, "apiToken"]}) { id } }`,
		`#comment
query Lookup { lookup(note:"apiToken", block:"""single 'quote' and escaped \""" quotes""") { id } }`,
	} {
		jsonLike, work, err := InspectQueryValueStructure(query, 2*len(query))
		if err != nil || jsonLike || work != 2*len(query) {
			t.Fatal("valid GraphQL scalar/alias/default/comment syntax became JSON structure")
		}
		quoted, _ := json.Marshal(query)
		body := `{"query":` + string(quoted) + `}`
		output, result, err := ProjectRequestBody([]byte(body), "", Options{Mode: Redact})
		if err != nil || result.Disposition == Rejected || string(output) != body {
			t.Fatal("valid GraphQL syntax changed during direct projection")
		}
	}
}

func TestQueryStructureWorkBudgetRejectsWithoutFallback(t *testing.T) {
	query := `{ ordinary: "safe" }`
	if _, work, err := InspectQueryValueStructure(query, 2*len(query)-1); err != ErrQueryStructureWorkLimit || work != 0 {
		t.Fatal("query classifier exceeded declared work allowance")
	}
	quoted, _ := json.Marshal(query)
	body := `{"query":` + string(quoted) + `}`
	options := Options{Mode: Redact, structuredRedactionBudget: &structuredRedactionBudget{remainingBytes: len(body) + 2*len(query) - 1, remainingValues: 100}}
	output, result, err := ProjectRequestBody([]byte(body), "", options)
	if err != nil || result.Disposition != Rejected || output != nil || len(result.Findings) != 1 || result.Findings[0].Code != "credential_structured_work_limit_exceeded" {
		t.Fatal("query classifier limit silently fell back to GraphQL")
	}
}

func TestDeclaredGraphQLMalformedMembersRejectWithoutPartialOutput(t *testing.T) {
	for _, query := range []string{`query { "apiToken":"` + projectionCanary + `" }`, `{"apiToken" "` + projectionCanary + `"}`, `{'apiToken':'` + projectionCanary + `'}`} {
		output, result, err := ProjectRequestBody([]byte(query), "application/graphql", Options{Mode: Redact})
		if err != nil || result.Disposition != Rejected || output != nil {
			t.Fatal("declared GraphQL malformed member syntax returned direct credential body")
		}
	}
}

func TestQueryStructureAdmissionDoesNotAllocateBeforeLimit(t *testing.T) {
	query := strings.Repeat(" ", 1<<20) + `{"apiToken":"synthetic"}`
	allocations := testing.AllocsPerRun(10, func() {
		_, work, err := InspectQueryValueStructure(query, 8)
		if err != ErrQueryStructureWorkLimit || work != 0 {
			t.Fatal("query work admission did not reject before scanning")
		}
	})
	if allocations != 0 {
		t.Fatalf("query work admission allocated: count=%f", allocations)
	}
}

func TestNeutralQueryTextAndMaskedSentinelRetainSafeStructure(t *testing.T) {
	for _, query := range []string{`SELECT "recordId" FROM "Account"`, `ordinary search "nearby phrase"`, "select accountId from records where name = 'safe'", "ordinary search + nearby?", "Customer O'Brian", credential.Redacted} {
		quoted, _ := json.Marshal(query)
		jsonBody := `{"query":` + string(quoted) + `,"recordId":"safe"}`
		for _, body := range []string{jsonBody, redactionJSONWrapper(t, jsonBody), "ordinary=one%20two&query=" + url.QueryEscape(query) + "&ordinary=three+four&flag"} {
			output, result, err := ProjectRequestBody([]byte(body), "", Options{Mode: Redact})
			if err != nil || result.Disposition == Rejected || string(output) != body {
				t.Fatal("neutral query/sentinel changed its safe sibling or authored bytes")
			}
			second, repeated, err := ProjectRequestBody(output, "", Options{Mode: Redact})
			if err != nil || repeated.Disposition == Rejected || string(second) != body {
				t.Fatal("repeated neutral query/sentinel projection invented a JSON layer")
			}
		}
	}
	output, result, err := ProjectRequestBody([]byte(credential.Redacted), "application/graphql", Options{Mode: Redact})
	if err != nil || result.Disposition == Rejected || string(output) != credential.Redacted {
		t.Fatal("declared GraphQL sentinel invented a structured layer")
	}
}

func TestDirectFormQueryRejectsMalformedStructureAndPreservesGraphQL(t *testing.T) {
	for _, query := range []string{`query {"apiToken" "` + projectionCanary + `"}`, `mutation Invalid {ordinary:invalid,"apiToken":"` + projectionCanary + `"}`} {
		body := "ordinary=one%20two&query=" + url.QueryEscape(query) + "&flag"
		output, result, err := ProjectRequestBody([]byte(body), "application/x-www-form-urlencoded", Options{Mode: Redact})
		if err != nil || result.Disposition != Rejected || output != nil {
			t.Fatal("form query malformed structure returned direct credential output")
		}
	}
	for _, query := range []string{`{ lookup(recordId:"safe") { id } }`, `query Lookup($id:String="safe") { lookup(id:$id) { id } }`} {
		body := "ordinary=one%20two&query=" + url.QueryEscape(query) + "&ordinary=three+four&flag"
		output, result, err := ProjectRequestBody([]byte(body), "application/x-www-form-urlencoded", Options{Mode: Redact})
		if err != nil || result.Disposition == Rejected || string(output) != body {
			t.Fatal("valid form GraphQL changed unselected spelling/order")
		}
	}
}

func TestFormQueryKeepsKnownWholeScalarCredentialMasking(t *testing.T) {
	const secret = "synthetic-whole-query-credential"
	body := "ordinary=one%20two&password=" + secret + "&query=" + url.QueryEscape(secret) + "&flag"
	output, result, err := ProjectRequestBody([]byte(body), "application/x-www-form-urlencoded", Options{Mode: Redact, KnownCredentialMatcher: func(value string) bool { return value == secret }})
	if err != nil || result.Disposition == Rejected || strings.Contains(string(output), secret) || !strings.HasPrefix(string(output), "ordinary=one%20two&") || !strings.HasSuffix(string(output), "&flag") {
		t.Fatal("known scalar query credential lost precise form projection")
	}
	// The placeholder stays literal, as for the password, where review finds it.
	if want := "ordinary=one%20two&password=" + credential.Redacted + "&query=" + credential.Redacted + "&flag"; string(output) != want {
		t.Fatalf("projected = %s, want %s", output, want)
	}
}

func TestQueryInputObjectInsideArrayKeepsFieldAndValueContextsDistinct(t *testing.T) {
	query := `query { lookup(input:[{ordinary:"safe", "apiToken" "synthetic"}]) { id } }`
	jsonLike, _, err := InspectQueryValueStructure(query, 2*len(query))
	if err != nil || !jsonLike {
		t.Fatal("quoted input-object field was treated as an array value")
	}
	deep := "query { lookup(input:" + strings.Repeat("[", 129) + "\"safe\"" + strings.Repeat("]", 129) + ") { id } }"
	if _, work, err := InspectQueryValueStructure(deep, 2*len(deep)); err != ErrQueryStructureWorkLimit || work != 2*len(deep) {
		t.Fatal("query structural context exceeded its fixed admission depth")
	}
}

func TestUnmatchedNeutralQuoteInspectionIsBoundedAndRetainsMemberEvidence(t *testing.T) {
	query := "'" + strings.Repeat(`\'`, 100_000)
	jsonLike, work, err := InspectQueryValueStructure(query, 2*len(query))
	if err != nil || jsonLike || work != 2*len(query) {
		t.Fatal("unmatched neutral quotes were rescanned or misclassified")
	}
	for _, query := range []string{`prefix {'apiToken':'synthetic'}`, `prefix { apiToken:'synthetic' }`, `prefix '{"apiToken":"synthetic"}`, `prefix ' {\"apiToken\":\"synthetic\"}`} {
		jsonLike, _, err := InspectQueryValueStructure(query, 2*len(query))
		if err != nil || !jsonLike {
			t.Fatal("unknown prefix hid malformed member structure")
		}
	}
}

func TestGraphQLNumericExponentSignsRemainDisclosureReadable(t *testing.T) {
	query := `query Lookup { lookup(scale:1.00e+2, offset:-2.5E-1, ordinary:"safe") { id } }`
	jsonLike, work, err := InspectQueryValueStructure(query, 2*len(query))
	if err != nil || jsonLike || work != 2*len(query) {
		t.Fatal("valid numeric exponent was classified as malformed member syntax")
	}
	quoted, _ := json.Marshal(query)
	for _, body := range []struct{ raw, contentType string }{
		{`{"query":` + string(quoted) + `,"ordinary":"safe"}`, "application/json"},
		{"ordinary=one%20two&query=" + url.QueryEscape(query) + "&flag", "application/x-www-form-urlencoded"},
		{query, "application/graphql"},
	} {
		output, result, err := ProjectRequestBody([]byte(body.raw), body.contentType, Options{Mode: Redact})
		if err != nil || result.Disposition == Rejected || string(output) != body.raw {
			t.Fatal("valid numeric exponent changed safe query disclosure")
		}
	}
}

func TestGraphQLRedactionSharesAggregateTokenAndWorkBudgets(t *testing.T) {
	query := `{ lookup(a:"one",b:"two",c:"three",d:"four",e:"five") { id } }`
	quoted, _ := json.Marshal(query)
	limits := Limits{MaxBytes: 4096, MaxDepth: 128, MaxValues: 32, MaxParts: 16}
	for _, count := range []int{1, 4} {
		fields, form := make([]string, count), make([]string, count)
		for index := range fields {
			fields[index] = `"query":` + string(quoted)
			form[index] = "query=" + url.QueryEscape(query)
		}
		for _, body := range []string{"{" + strings.Join(fields, ",") + "}", strings.Join(form, "&")} {
			output, result, err := ProjectRequestBody([]byte(body), "", Options{Mode: Redact, Limits: limits})
			if err != nil {
				t.Fatal(err)
			}
			if count == 1 {
				if result.Disposition == Rejected || string(output) != body {
					t.Fatal("single query did not fit its shared token allowance")
				}
			} else if output != nil || result.Disposition != Rejected {
				t.Fatal("repeated query fields reset the shared token allowance")
			}
			for _, mode := range []ProjectionMode{Preserve, SecretReference, Audit} {
				output, result, err := ProjectRequestBody([]byte(body), "", Options{Mode: mode, Limits: limits})
				if err != nil || result.Disposition == Rejected || string(output) != body {
					t.Fatal("disclosure token accounting changed executable projection semantics")
				}
			}
		}
	}

	options, err := normalizeOptions(Options{Mode: Redact})
	if err != nil {
		t.Fatal(err)
	}
	options.structuredRedactionBudget.remainingValues = 0
	if tokens, err := lexGraphQLWithOptions([]byte(`"`+strings.Repeat("safe", 100_000)+`"`), options); !errors.Is(err, errGraphQLTokenLimit) || tokens != nil {
		t.Fatal("exhausted token budget reached scalar decoding")
	}
	options.structuredRedactionBudget.remainingValues = 32
	options.structuredRedactionBudget.remainingBytes = len(query) - 1
	if tokens, err := lexGraphQLWithOptions([]byte(query), options); !errors.Is(err, errGraphQLWorkLimit) || tokens != nil || options.structuredRedactionBudget.remainingValues != 32 {
		t.Fatal("exhausted lexer work budget allocated or consumed tokens")
	}
	options.structuredRedactionBudget.remainingBytes = DefaultLimits().MaxBytes * 4
	options.structuredRedactionBudget.remainingValues = 1
	options.KnownCredentialMatcher = func(string) bool { return false }
	body := `{"query":` + string(quoted) + `}`
	output, result, err := ProjectJSON([]byte(body), options)
	if err != nil || output != nil || result.Disposition != Rejected {
		t.Fatal("executable-query admission silently fell back after budget exhaustion")
	}
}

// A JSON string holding JSON keeps its recorded escapes: only the credential
// changes, where re-encoding the whole string escaped HTML characters and
// rewrote \u and \/ escapes around it, so the projection no longer lined up
// with the recording. The same holds inside a form value.
func TestNestedJSONStringKeepsItsRecordedEscapes(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ name, body, contentType, want string }{
		{
			name:        "HTML characters",
			body:        `{"payload":"{\"note\":\"a&b <c>\",\"apiToken\":\"` + projectionCanary + `\"}"}`,
			contentType: "application/json",
			want:        `{"payload":"{\"note\":\"a&b <c>\",\"apiToken\":\"[REDACTED]\"}"}`,
		},
		{
			name:        "unicode and solidus escapes",
			body:        jsonEscapes(`{"payload":"{U+0022urlU+0022:U+0022https:\/\/x.test\/cafU+00e9U+0022,U+0022secretU+0022:U+0022` + projectionCanary + `U+0022}"}`),
			contentType: "application/json",
			want:        jsonEscapes(`{"payload":"{U+0022urlU+0022:U+0022https:\/\/x.test\/cafU+00e9U+0022,U+0022secretU+0022:U+0022[REDACTED]U+0022}"}`),
		},
		{
			name:        "credential sharing the placeholder's brackets",
			body:        jsonEscapes(`{"payload":"{\"apiToken\":\"U+005b` + projectionCanary + `U+005d\"}"}`),
			contentType: "application/json",
			want:        `{"payload":"{\"apiToken\":\"[REDACTED]\"}"}`,
		},
		{
			name:        "twice nested",
			body:        `{"outer":"{\"inner\":\"{\\\"apiToken\\\":\\\"` + projectionCanary + `\\\",\\\"q\\\":\\\"a<b\\\"}\"}"}`,
			contentType: "application/json",
			want:        `{"outer":"{\"inner\":\"{\\\"apiToken\\\":\\\"[REDACTED]\\\",\\\"q\\\":\\\"a<b\\\"}\"}"}`,
		},
		{
			name:        "inside a form value",
			body:        `message=` + url.QueryEscape(`{"payload":"{\"note\":\"a&b\",\"apiToken\":\"`+projectionCanary+`\"}"}`),
			contentType: "application/x-www-form-urlencoded",
			want:        `message=` + strings.Replace(url.QueryEscape(`{"payload":"{\"note\":\"a&b\",\"apiToken\":\"`+projectionCanary+`\"}"}`), projectionCanary, "[REDACTED]", 1),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			output, result, err := ProjectRequestBody([]byte(test.body), test.contentType, Options{Mode: Redact})
			if err != nil || result.Disposition == Rejected {
				t.Fatalf("projection failed: %v / %s", err, result.Disposition)
			}
			assertDisclosureSafe(t, output, result)
			if string(output) != test.want {
				t.Fatalf("projected = %s\nwant      %s", output, test.want)
			}
		})
	}
}

// The JSON string splice maps spans through every escape form, refuses a
// boundary inside a character one \u escape wrote, and refuses what does not
// round-trip.
func TestSpliceJSONString(t *testing.T) {
	t.Parallel()
	token := jsonEscapes(`"{U+0022kU+0022:\"old\",\"n\":\"U+d83dU+de00\"}"`)
	decoded := "{\"k\":\"old\",\"n\":\"\U0001F600\"}"
	text, _, ok := jsonStringByteStarts(token)
	if !ok || text != decoded {
		t.Fatalf("decoded = %q %t", text, ok)
	}
	spans := []replacement{{start: 5, end: 10, value: []byte(`"a<b\"c"`)}}
	projected := "{\"k\":\"a<b\\\"c\",\"n\":\"\U0001F600\"}"
	replaced, ok := spliceJSONString(token, decoded, projected, spans)
	if !ok || string(applyReplacements([]byte(token), replaced)) != jsonEscapes(`"{U+0022kU+0022:\"a<b\\\"c\",\"n\":\"U+d83dU+de00\"}"`) {
		t.Fatalf("splice = %q %t", applyReplacements([]byte(token), replaced), ok)
	}
	if _, ok := spliceJSONString(token, `{"k":"other"}`, projected, spans); ok {
		t.Fatal("a token that does not decode to the decoded text was spliced")
	}
	if _, ok := spliceJSONString(token, decoded, `{"k":"different"}`, spans); ok {
		t.Fatal("a splice that does not decode to the projection was accepted")
	}
	emoji := strings.Index(decoded, "\U0001F600")
	if _, ok := spliceJSONString(token, decoded, projected, []replacement{{start: emoji + 1, end: emoji + 2, value: []byte("x")}}); ok {
		t.Fatal("a boundary inside an escaped character was accepted")
	}
	// A replacement that only repeats the placeholder recorded there writes
	// nothing beyond it: the recorded quotes keep their escapes.
	recorded := jsonEscapes(`"{U+0022kU+0022:U+0022[REDACTED]U+0022}"`)
	placeholder := `{"k":"[REDACTED]"}`
	replaced, ok = spliceJSONString(recorded, placeholder, placeholder, []replacement{{start: 5, end: 17, value: []byte(`"[REDACTED]"`)}})
	if !ok || string(applyReplacements([]byte(recorded), replaced)) != recorded {
		t.Fatalf("splice = %q %t, want the recorded token", applyReplacements([]byte(recorded), replaced), ok)
	}
	for _, malformed := range []string{jsonEscapes(`"U+d83d"`), jsonEscapes(`"U+de00"`), `"\q"`, jsonEscapes(`"U+12"`), `"abc`, `"abc\"`} {
		if _, _, ok := jsonStringByteStarts(malformed); ok {
			t.Fatalf("malformed token %s decoded", malformed)
		}
	}
}

// jsonEscapes spells U+XXXX as a JSON \u escape, so these fixtures keep their
// escapes literal in source.
func jsonEscapes(text string) string {
	return strings.ReplaceAll(text, "U+", `\`+"u")
}
