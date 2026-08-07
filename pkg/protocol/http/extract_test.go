package http

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestExtractJSONPath_Simple(t *testing.T) {
	body := []byte(`{"name":"test","value":42}`)

	val, err := extractJSONPath("$.name", body)
	if err != nil {
		t.Fatalf("extractJSONPath: %v", err)
	}
	if val != "test" {
		t.Errorf("got %q, want %q", val, "test")
	}
}

func TestExtractJSONPath_Nested(t *testing.T) {
	body := []byte(`{"data":{"token":"abc123"}}`)

	val, err := extractJSONPath("$.data.token", body)
	if err != nil {
		t.Fatalf("extractJSONPath: %v", err)
	}
	if val != "abc123" {
		t.Errorf("got %q, want %q", val, "abc123")
	}
}

func TestExtractJSONPath_ArrayIndex(t *testing.T) {
	body := []byte(`{"items":[{"id":"first"},{"id":"second"}]}`)

	val, err := extractJSONPath("$.items[1].id", body)
	if err != nil {
		t.Fatalf("extractJSONPath: %v", err)
	}
	if val != "second" {
		t.Errorf("got %q, want %q", val, "second")
	}
}

func TestExtractJSONPath_PreservesJSONNumbers(t *testing.T) {
	body := []byte(`{"id":1000000,"order":9223372036854775807,"price":12.5}`)
	tests := []struct {
		expr string
		want string
	}{
		{"$.id", "1000000"},
		{"$.order", "9223372036854775807"},
		{"$.price", "12.5"},
	}
	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			got, err := extractJSONPath(tt.expr, body)
			if err != nil {
				t.Fatalf("extractJSONPath: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractJSONPath_RootArrayAndChainedIndexes(t *testing.T) {
	body := []byte(`[{"id":"first","tokens":[["nested-token"]]}]`)

	val, err := extractJSONPath("$[0].id", body)
	if err != nil {
		t.Fatalf("extractJSONPath root array: %v", err)
	}
	if val != "first" {
		t.Errorf("root array got %q, want first", val)
	}

	val, err = extractJSONPath("$[0].tokens[0][0]", body)
	if err != nil {
		t.Fatalf("extractJSONPath chained indexes: %v", err)
	}
	if val != "nested-token" {
		t.Errorf("chained indexes got %q, want nested-token", val)
	}
}

func TestExtractJSONPath_MissingField(t *testing.T) {
	body := []byte(`{"name":"test"}`)

	_, err := extractJSONPath("$.missing", body)
	if err == nil {
		t.Fatal("expected error for missing field")
	}
}

func TestExtractJSONPath_InvalidJSON(t *testing.T) {
	_, err := extractJSONPath("$.field", []byte(`not json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestJSONExtractorsRejectTrailingDataAndMultipleDocuments(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "trailing garbage", body: `{"field":"ok"} trailing`},
		{name: "multiple documents", body: `{"field":"ok"} {"field":"second"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := extractJSONPath("$.field", []byte(test.body)); err == nil {
				t.Fatal("JSONPath accepted a body that is not exactly one JSON document")
			}
			if _, err := extractJMESPath("field", []byte(test.body)); err == nil {
				t.Fatal("JMESPath accepted a body that is not exactly one JSON document")
			}
		})
	}
}

func TestJSONExtractorsAllowTrailingWhitespace(t *testing.T) {
	body := []byte("{\"field\":\"ok\"}\n\t ")
	if got, err := extractJSONPath("$.field", body); err != nil || got != "ok" {
		t.Fatalf("JSONPath trailing whitespace = %q, %v", got, err)
	}
	if got, err := extractJMESPath("field", body); err != nil || got != "ok" {
		t.Fatalf("JMESPath trailing whitespace = %q, %v", got, err)
	}
}

func TestJSONExtractorsRejectInvalidUTF8(t *testing.T) {
	body := []byte{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'}
	if _, err := extractJSONPath("$.x", body); err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("JSONPath invalid UTF-8 error = %v", err)
	}
	if _, err := extractJMESPath("x", body); err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("JMESPath invalid UTF-8 error = %v", err)
	}
}

func TestJSONTreeExtractorsBoundNodeAndDepthAmplification(t *testing.T) {
	overNodeLimit := []byte("[" + strings.Repeat("0,", maxExtractorStructuredNodes) + "0]")
	for name, extractValue := range map[string]func([]byte) error{
		"jsonpath": func(body []byte) error {
			_, err := extractJSONPath("$[0]", body)
			return err
		},
		"jmespath": func(body []byte) error {
			_, err := extractJMESPath("[0]", body)
			return err
		},
		"jsonpath all": func(body []byte) error {
			_, err := extractAllJSONPath("$", body)
			return err
		},
	} {
		t.Run(name+" nodes", func(t *testing.T) {
			err := extractValue(overNodeLimit)
			if !errors.Is(err, errExtractorResourceLimit) || !strings.Contains(err.Error(), "nodes") {
				t.Fatalf("node-amplification error = %v, want extractor resource node limit", err)
			}
		})
	}

	atNodeLimit := []byte("[" + strings.Repeat("0,", maxExtractorStructuredNodes-2) + "7]")
	if err := validateExtractorJSONStructure(atNodeLimit); err != nil {
		t.Fatalf("JSON node boundary preflight: %v", err)
	}

	atDepthLimit := []byte(strings.Repeat("[", maxExtractorStructuredDepth) + `"ok"` + strings.Repeat("]", maxExtractorStructuredDepth))
	deepPath := "$" + strings.Repeat("[0]", maxExtractorStructuredDepth)
	if got, err := extractJSONPath(deepPath, atDepthLimit); err != nil || got != "ok" {
		t.Fatalf("JSON depth boundary extraction = %q, %v; want ok", got, err)
	}
	overDepthLimit := []byte(strings.Repeat("[", maxExtractorStructuredDepth+1) + "0" + strings.Repeat("]", maxExtractorStructuredDepth+1))
	if _, err := extractJMESPath("[0]", overDepthLimit); !errors.Is(err, errExtractorResourceLimit) || !strings.Contains(err.Error(), "depth") {
		t.Fatalf("JSON depth-amplification error = %v, want extractor resource depth limit", err)
	}
}

func TestExtractJMESPath(t *testing.T) {
	body := []byte(`{"people":[{"name":"James"},{"name":"Jacob"}],"meta":{"count":2}}`)

	val, err := extractJMESPath("people[1].name", body)
	if err != nil {
		t.Fatalf("extractJMESPath: %v", err)
	}
	if val != "Jacob" {
		t.Errorf("got %q, want %q", val, "Jacob")
	}
}

func TestExtractJMESPathLargeNumberPreservesDecimalString(t *testing.T) {
	body := []byte(`{"id":12345678901234567890,"items":[{"id":98765432109876543210}]}`)

	val, err := extractJMESPath("id", body)
	if err != nil {
		t.Fatalf("extractJMESPath: %v", err)
	}
	if val != "12345678901234567890" {
		t.Errorf("got %q, want decimal string", val)
	}

	nested, err := extractJMESPath("items[0].id", body)
	if err != nil {
		t.Fatalf("extractJMESPath nested: %v", err)
	}
	if nested != "98765432109876543210" {
		t.Errorf("nested got %q, want decimal string", nested)
	}
}

func TestExtractJMESPath_ArrayResult(t *testing.T) {
	body := []byte(`{"people":[{"name":"James"},{"name":"Jacob"}]}`)

	val, err := extractJMESPath("people[*].name", body)
	if err != nil {
		t.Fatalf("extractJMESPath: %v", err)
	}
	if val != `["James","Jacob"]` {
		t.Errorf("got %q, want %q", val, `["James","Jacob"]`)
	}
}

func TestExtractJMESPath_NoMatch(t *testing.T) {
	_, err := extractJMESPath("missing.value", []byte(`{"name":"test"}`))
	if err == nil {
		t.Fatal("expected error for missing JMESPath match")
	}
}

func TestExtractJMESPath_InvalidExpression(t *testing.T) {
	_, err := extractJMESPath("[", []byte(`{"name":"test"}`))
	if err == nil {
		t.Fatal("expected error for invalid JMESPath expression")
	}
}

func TestExtractJMESPath_NumericComparisons(t *testing.T) {
	tests := []struct {
		name string
		expr string
		want string
	}{
		{name: "equality filter", expr: "items[?count == `2`].name | [0]", want: "second"},
		{name: "ordering filter", expr: "items[?count > `1`].name | [0]", want: "second"},
		{name: "mixed predicate keeps numeric-only match", expr: "items[?name == 'first' || count > `1`].name", want: `["first","second"]`},
		{name: "numeric scalar", expr: "items[1].count", want: "2"},
	}
	body := []byte(`{"items":[{"name":"first","count":1},{"name":"second","count":2}]}`)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := extractJMESPath(test.expr, body)
			if err != nil {
				t.Fatalf("extractJMESPath(%q): %v", test.expr, err)
			}
			if got != test.want {
				t.Fatalf("extractJMESPath(%q) = %q, want %q", test.expr, got, test.want)
			}
		})
	}
}

func TestExtractJMESPath_NumericPredicateKeepsPlainNumberFormatting(t *testing.T) {
	tests := []struct {
		name string
		body string
		expr string
		want string
	}{
		{
			name: "large integer id",
			body: `{"items":[{"count":1,"id":1000000},{"count":2,"id":2000000}]}`,
			expr: "items[?count > `1`].id | [0]",
			want: "2000000",
		},
		{
			name: "small fractional id",
			body: `{"items":[{"count":1,"id":0.001},{"count":2,"id":0.000034}]}`,
			expr: "items[?count > `1`].id | [0]",
			want: "0.000034",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := extractJMESPath(test.expr, []byte(test.body))
			if err != nil {
				t.Fatalf("extractJMESPath(%q): %v", test.expr, err)
			}
			if got != test.want {
				t.Fatalf("extractJMESPath(%q) = %q, want %q", test.expr, got, test.want)
			}
		})
	}
}

func TestExtractJMESPathStructuralSelectionSkipsNormalizedReevaluation(t *testing.T) {
	var builder strings.Builder
	builder.WriteString(`{"items":[`)
	const items = 400
	for index := 0; index < items; index++ {
		if index > 0 {
			builder.WriteByte(',')
		}
		fmt.Fprintf(&builder, `{"count":%d,"id":%d,"name":"row-%d"}`, index, 1000000+index, index)
	}
	builder.WriteString(`]}`)
	body := []byte(builder.String())

	got, err := extractJMESPath("items[200].id", body)
	if err != nil {
		t.Fatalf("extractJMESPath(): %v", err)
	}
	if want := "1000200"; got != want {
		t.Fatalf("extractJMESPath() = %q, want %q", got, want)
	}

	decodeAllocs := testing.AllocsPerRun(20, func() {
		if _, err := decodeExtractorJSON(body); err != nil {
			t.Fatalf("decodeExtractorJSON(): %v", err)
		}
	})
	extractAllocs := testing.AllocsPerRun(20, func() {
		if _, err := extractJMESPath("items[200].id", body); err != nil {
			t.Fatalf("extractJMESPath(): %v", err)
		}
	})
	// A path selection cannot observe number representation, so it must not pay
	// for the normalized tree copy on top of the decode every response already
	// performs. The copy is proportional to the document, so a small relative
	// margin over the decode is enough to detect its return.
	if limit := 1.02*decodeAllocs + 32; extractAllocs > limit {
		t.Fatalf("structural extraction allocated %.0f, want <= %.0f (decode alone allocates %.0f)",
			extractAllocs, limit, decodeAllocs)
	}
}

func TestJMESPathNumberRepresentationSensitiveClassification(t *testing.T) {
	for expr, want := range map[string]bool{
		"data.orderId":                          false,
		"items[200].id":                         false,
		"items[*].name | [0]":                   false,
		"items[?enabled].name":                  false,
		"items[?count > `1`].id | [0]":          true,
		"items[?id == `9007199254740993`].name": true,
		"length(items)":                         true,
		"sum(items[*].count)":                   true,
		"!items":                                true,
	} {
		if got := jmesPathNumberRepresentationSensitive(expr); got != want {
			t.Fatalf("jmesPathNumberRepresentationSensitive(%q) = %t, want %t", expr, got, want)
		}
	}
}

func TestExtractJMESPath_PreservesUnrepresentableScalarNumber(t *testing.T) {
	got, err := extractJMESPath("value", []byte(`{"value":1e400}`))
	if err != nil {
		t.Fatalf("extractJMESPath: %v", err)
	}
	if got != "1e400" {
		t.Fatalf("scalar = %q, want exact JSON number", got)
	}
}

func TestExtractJMESPathRejectsUnsafeLargeIntegerComparison(t *testing.T) {
	body := []byte(`{"items":[{"name":"rounded-down","id":9007199254740992},{"name":"exact-target","id":9007199254740993}]}`)
	if _, err := extractJMESPath("items[?id == `9007199254740993`].name", body); err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("unsafe integer predicate error = %v", err)
	}
	for _, expression := range []string{
		"items[?id > `9007199254740992`].name",
		"items[?id < `9007199254740994`].name",
		"items[?id > threshold].name",
	} {
		if _, err := extractJMESPath(expression, []byte(`{"items":[{"name":"boundary","id":9007199254740993,"threshold":9007199254740992}]}`)); err == nil || !strings.Contains(err.Error(), "unsafe") {
			t.Fatalf("unsafe integer predicate %q error = %v", expression, err)
		}
	}
	got, err := extractJMESPath("items[?name == 'exact-target'].id | [0]", body)
	if err != nil {
		t.Fatalf("exact nonnumeric predicate: %v", err)
	}
	if got != "9007199254740993" {
		t.Fatalf("exact selected integer = %q, want 9007199254740993", got)
	}

	got, err = extractJMESPath("items[?count > `1`].id | [0]", []byte(`{"items":[{"count":1,"id":9007199254740992},{"count":2,"id":9007199254740993}]}`))
	if err != nil {
		t.Fatalf("safe predicate with unrelated large identifier: %v", err)
	}
	if got != "9007199254740993" {
		t.Fatalf("large identifier selected by safe predicate = %q, want 9007199254740993", got)
	}
	got, err = extractJMESPath("items[?!enabled].id | [0]", []byte(`{"items":[{"enabled":false,"id":9007199254740993}]}`))
	if err != nil {
		t.Fatalf("unary not with unrelated large identifier: %v", err)
	}
	if got != "9007199254740993" {
		t.Fatalf("large identifier selected by unary predicate = %q, want 9007199254740993", got)
	}
}

func TestExtractJMESPathAllowsQualifiedSafeFieldWithDuplicateUnsafeLeaf(t *testing.T) {
	body := []byte(`{"archive":{"id":9007199254740993},"current":{"id":2}}`)
	got, err := extractJMESPath("current.id == `2`", body)
	if err != nil {
		t.Fatalf("safe qualified comparison: %v", err)
	}
	if got != "true" {
		t.Fatalf("safe qualified comparison = %q, want true", got)
	}
	if _, err := extractJMESPath("archive.id > `9007199254740992`", body); err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("unsafe qualified comparison error = %v", err)
	}
}

func TestExtractJMESPathRejectsUnsafeQualifiedFieldInsideFilter(t *testing.T) {
	body := []byte(`{"items":[{"name":"unsafe","foo":{"id":9007199254740993}}]}`)
	expr := "length(items[?foo.id > `9007199254740992`])"
	if _, err := extractJMESPath(expr, body); err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("unsafe nested filter comparison error = %v", err)
	}
}

func TestExtractJMESPathAllowsScopedSafeLeafWithNestedUnsafeDuplicate(t *testing.T) {
	body := []byte(`{"archive":{"id":9007199254740993},"id":2,"items":[{"id":2,"name":"safe"}]}`)
	for expr, want := range map[string]string{
		"id == `2`":                    "true",
		"items[?id == `2`].name | [0]": "safe",
	} {
		got, err := extractJMESPath(expr, body)
		if err != nil || got != want {
			t.Fatalf("safe scoped comparison %q = %q, %v; want %q", expr, got, err, want)
		}
	}
}

func TestExtractJMESPathRejectsUnsafeQualifiedFieldAfterIndexProjection(t *testing.T) {
	body := []byte(`{"items":[{"foo":{"id":9007199254740993}}]}`)
	for _, expr := range []string{
		"items[0].foo.id > `9007199254740992`",
		"items[].foo.id > `9007199254740992`",
	} {
		if _, err := extractJMESPath(expr, body); err == nil || !strings.Contains(err.Error(), "unsafe") {
			t.Fatalf("unsafe projected comparison %q error = %v", expr, err)
		}
	}

	safeBody := []byte(`{"archive":[{"foo":{"id":9007199254740993}}],"current":[{"foo":{"id":2}}]}`)
	for _, expr := range []string{
		"current[0].foo.id == `2`",
		"current[].foo.id == `[2]`",
	} {
		got, err := extractJMESPath(expr, safeBody)
		if err != nil || got != "true" {
			t.Fatalf("safe same-suffix comparison %q = %q, %v", expr, got, err)
		}
	}
}

func TestExtractJMESPathQualifiesPipeRelativeNumericComparisons(t *testing.T) {
	unsafeBody := []byte(`{"items":[{"id":9007199254740993}]}`)
	for _, expr := range []string{
		"items[0] | (id > `9007199254740992` || `false`)",
		"items[0] | (`false` || id > `9007199254740992`)",
		"root | child.id > `9007199254740992`",
	} {
		body := unsafeBody
		if strings.HasPrefix(expr, "root") {
			body = []byte(`{"root":{"child":{"id":9007199254740993}}}`)
		}
		if _, err := extractJMESPath(expr, body); err == nil || !strings.Contains(err.Error(), "unsafe") {
			t.Fatalf("unsafe pipe-relative comparison %q error = %v", expr, err)
		}
	}

	safeBody := []byte(`{"archive":[{"id":9007199254740993}],"current":[{"id":2}]}`)
	got, err := extractJMESPath("current[0] | (id == `2` || `false`)", safeBody)
	if err != nil || got != "true" {
		t.Fatalf("safe qualified pipe comparison = %q, %v; want true", got, err)
	}
}

// TestExtractCompositeReferenceId guards the A5 composite-chaining contract: a
// referenceId-scoped lookup needs a filter predicate, which only the jmespath
// extractor supports — the hand-rolled jsonpath extractor errors on it. The
// Salesforce importer must therefore emit jmespath, not jsonpath, for these.
func TestExtractCompositeReferenceId(t *testing.T) {
	body := []byte(`{"compositeResponse":[{"referenceId":"NewAccount","httpStatusCode":201,"body":{"id":"001ABC"}},{"referenceId":"NewContact","httpStatusCode":201,"body":{"id":"003XYZ"}}]}`)

	val, err := extractJMESPath("compositeResponse[?referenceId=='NewContact'].body.id | [0]", body)
	if err != nil {
		t.Fatalf("extractJMESPath filter: %v", err)
	}
	if val != "003XYZ" {
		t.Errorf("jmespath filter got %q, want %q", val, "003XYZ")
	}

	if _, err := extractJSONPath("$.compositeResponse[?referenceId=='NewContact'].body.id", body); err == nil {
		t.Fatal("hand-rolled jsonpath unexpectedly handled a filter predicate; the importer relies on this failing")
	}
}

func TestExtractCompositeTreeReferenceId(t *testing.T) {
	body := []byte(`{"hasErrors":false,"results":[{"referenceId":"RootAccount","id":"001TREE","errors":[]},{"referenceId":"ChildContact","id":"003TREE","errors":[]}]}`)

	val, err := extractJMESPath("results[?referenceId=='ChildContact'].id | [0]", body)
	if err != nil {
		t.Fatalf("extractJMESPath tree filter: %v", err)
	}
	if val != "003TREE" {
		t.Errorf("jmespath tree filter got %q, want %q", val, "003TREE")
	}
}

func TestExtractCompositeGraphReferenceId(t *testing.T) {
	body := []byte(`{"graphs":[{"graphId":"G1","graphResponse":{"compositeResponse":[{"referenceId":"RootAccount","httpStatusCode":201,"body":{"id":"001G1"}}]}},{"graphId":"G2","graphResponse":{"compositeResponse":[{"referenceId":"RootAccount","httpStatusCode":201,"body":{"id":"001G2"}}]}}]}`)

	expr := "graphs[?graphId=='G2'].graphResponse.compositeResponse[] | [?referenceId=='RootAccount'].body.id | [0]"
	val, err := extractJMESPath(expr, body)
	if err != nil {
		t.Fatalf("extractJMESPath graph filter: %v", err)
	}
	if val != "001G2" {
		t.Errorf("jmespath graph filter got %q, want %q", val, "001G2")
	}
}

// TestExtractCometdClientIdArrayRoot guards the A5 CometD contract: Salesforce
// still needs JMESPath for filter predicates, while the lighter JSONPath helper
// now supports direct numeric array-root indexing used by generic correlations.
func TestExtractCometdClientIdArrayRoot(t *testing.T) {
	body := []byte(`[{"id":"1","channel":"/meta/handshake","successful":true,"clientId":"3f8k2j9xqz"}]`)

	val, err := extractJMESPath("[?channel=='/meta/handshake'].clientId | [0]", body)
	if err != nil {
		t.Fatalf("extractJMESPath array-root: %v", err)
	}
	if val != "3f8k2j9xqz" {
		t.Errorf("got %q, want %q", val, "3f8k2j9xqz")
	}

	val, err = extractJSONPath("$[0].clientId", body)
	if err != nil {
		t.Fatalf("extractJSONPath array-root index: %v", err)
	}
	if val != "3f8k2j9xqz" {
		t.Errorf("jsonpath array-root got %q, want %q", val, "3f8k2j9xqz")
	}
}

func TestCorrelationSuggestedExtractorsRoundTripThroughHTTPExtractor(t *testing.T) {
	formToken := "tok+/==abcdef"
	fwuid := "FWUID-abcdef1234567890"
	app := "markup://one:one"
	viewState := "/wEPDwABC123456789=="
	formBody := "aura.token=" + url.QueryEscape(formToken) +
		"&aura.context=" + url.QueryEscape(`{"fwuid":"`+fwuid+`","app":"`+app+`"}`) +
		"&com.salesforce.visualforce.ViewState=" + url.QueryEscape(viewState)
	observations := []correlate.Observation{
		{
			ID:           1,
			ResponseBody: `{"auth":{"access_token":"tok-json-abcdef123456"},"items":[{"id":"1234567890123"}]}`,
		},
		{
			ID:             2,
			RequestURL:     "https://api.example.test/orders/1234567890123",
			RequestHeaders: map[string]string{"Authorization": "Bearer tok-json-abcdef123456"},
		},
		{
			ID:           3,
			ResponseBody: `<Envelope><Body><CreateOrderResponse correlationId="soap-corr-abcdef123456">ok</CreateOrderResponse></Body></Envelope>`,
			ResponseHeaders: map[string]string{
				"Location":   "https://idp.example.test/callback?code=code-abcdef123456&state=state-abcdef123456",
				"Set-Cookie": "session=sid-abcdef123456; Path=/; HttpOnly",
			},
		},
		{
			ID:             4,
			RequestURL:     "https://client.example.test/callback?code=code-abcdef123456&state=state-abcdef123456",
			RequestBody:    "correlationId=soap-corr-abcdef123456",
			RequestHeaders: map[string]string{"Cookie": "session=sid-abcdef123456"},
		},
		{
			ID:           5,
			ResponseBody: formBody,
		},
		{
			ID:          6,
			RequestBody: "aura.token=" + url.QueryEscape(formToken) + "&fwuid=" + url.QueryEscape(fwuid) + "&app=" + url.QueryEscape(app) + "&viewstate=" + url.QueryEscape(viewState),
		},
	}

	correlations := correlate.Detect(observations)
	if len(correlations) < 8 {
		t.Fatalf("correlations = %#v, want broad round-trip coverage", correlations)
	}
	byID := make(map[int]correlate.Observation, len(observations))
	for _, observation := range observations {
		byID[observation.ID] = observation
	}
	for _, corr := range correlations {
		source := byID[corr.FoundInID]
		got, err := extract(protocol.Extractor{
			Name:       corr.Name,
			Source:     corr.Source,
			Type:       corr.ExtractType,
			Expression: corr.Expression,
			Convert:    corr.Convert,
		}, http.Header(firstValuesToHeader(source.ResponseHeaders)), []byte(source.ResponseBody))
		if err != nil {
			t.Fatalf("%s (%s %q convert=%q) did not round-trip: %v", corr.Name, corr.ExtractType, corr.Expression, corr.Convert, err)
		}
		if got != corr.Value {
			t.Fatalf("%s extracted %q, want %q (type=%s expr=%q convert=%q)", corr.Name, got, corr.Value, corr.ExtractType, corr.Expression, corr.Convert)
		}
	}
}

func firstValuesToHeader(values map[string]string) map[string][]string {
	if len(values) == 0 {
		return nil
	}
	headers := make(map[string][]string, len(values))
	for key, value := range values {
		headers[key] = []string{value}
	}
	return headers
}

func TestExtractCSS_Text(t *testing.T) {
	body := []byte(`<html><body><p class="primary">Alpha</p><p>Beta</p></body></html>`)

	val, err := extractCSS("p.primary", body)
	if err != nil {
		t.Fatalf("extractCSS: %v", err)
	}
	if val != "Alpha" {
		t.Errorf("got %q, want %q", val, "Alpha")
	}
}

func TestExtractCSS_Attribute(t *testing.T) {
	body := []byte(`<html><body><a class="login" href="/auth">Login</a></body></html>`)

	val, err := extractCSS("a.login|||href", body)
	if err != nil {
		t.Fatalf("extractCSS: %v", err)
	}
	if val != "/auth" {
		t.Errorf("got %q, want %q", val, "/auth")
	}
}

func TestExtractCSS_NoMatch(t *testing.T) {
	_, err := extractCSS("p.missing", []byte(`<html><body><p>Alpha</p></body></html>`))
	if err == nil {
		t.Fatal("expected error for missing CSS selector match")
	}
}

func TestExtractCSS_MissingAttribute(t *testing.T) {
	_, err := extractCSS("a.login|||href", []byte(`<html><body><a class="login">Login</a></body></html>`))
	if err == nil {
		t.Fatal("expected error for missing CSS attribute")
	}
}

func TestExtractXPath_XMLText(t *testing.T) {
	val, err := extractFromBody(protocol.Extractor{Type: "xpath", Expression: "/response/user/name/text()"}, []byte(`<response><user><name>Ada</name></user></response>`))
	if err != nil {
		t.Fatalf("extract XPath XML text: %v", err)
	}
	if val != "Ada" {
		t.Errorf("got %q, want %q", val, "Ada")
	}
}

func TestExtractXPath_XMLAttribute(t *testing.T) {
	val, err := extractFromBody(protocol.Extractor{Type: "xpath", Expression: "/response/link/@href"}, []byte(`<response><link href="/signin">Sign in</link></response>`))
	if err != nil {
		t.Fatalf("extract XPath XML attribute: %v", err)
	}
	if val != "/signin" {
		t.Errorf("got %q, want %q", val, "/signin")
	}
}

func TestExtractXPath_XMLAttributeLocalName(t *testing.T) {
	val, err := extractFromBody(protocol.Extractor{Type: "xpath", Expression: "//@*[local-name()='correlationId']"}, []byte(`<Envelope><Body><CreateOrderResponse correlationId="soap-corr-abcdef123456">ok</CreateOrderResponse></Body></Envelope>`))
	if err != nil {
		t.Fatalf("extract XPath XML local-name attribute: %v", err)
	}
	if val != "soap-corr-abcdef123456" {
		t.Errorf("got %q, want soap-corr-abcdef123456", val)
	}
}

func TestExtractXPath_HTMLFallback(t *testing.T) {
	val, err := extractFromBody(protocol.Extractor{Type: "xpath", Expression: "//a[@class='login']/@href"}, []byte(`<html><body><a class="login" href="/signin">Sign in`))
	if err != nil {
		t.Fatalf("extract XPath HTML fallback: %v", err)
	}
	if val != "/signin" {
		t.Errorf("got %q, want %q", val, "/signin")
	}
}

func TestExtractXPathDeclaredXMLDoesNotFallBackToHTML(t *testing.T) {
	body := []byte(`<?xml version="1.0"?><response><token>abc</token>`)
	if _, err := extractXPath("//token/text()", body); err == nil || !strings.Contains(err.Error(), "invalid XML body") {
		t.Fatalf("declared malformed XML error = %v", err)
	}
	if _, err := extractAllXPath("//token/text()", body); err == nil || !strings.Contains(err.Error(), "invalid XML body") {
		t.Fatalf("declared malformed XML save-all error = %v", err)
	}
}

func TestXPathXMLTreePreflightBoundsNodesAndDepth(t *testing.T) {
	atNodeLimit := []byte("<root>" +
		strings.Repeat("<n/>", maxExtractorStructuredNodes-3) +
		`<n id="last"/></root>`)
	if err := validateExtractorXMLStructure(atNodeLimit); err != nil {
		t.Fatalf("XML node boundary preflight: %v", err)
	}
	overNodeLimitByAttribute := []byte("<root>" +
		strings.Repeat("<n/>", maxExtractorStructuredNodes-3) +
		`<n id="last" extra="x"/></root>`)
	if err := validateExtractorXMLStructure(overNodeLimitByAttribute); !errors.Is(err, errExtractorResourceLimit) || !strings.Contains(err.Error(), "nodes") {
		t.Fatalf("XML retained-attribute node-amplification error = %v, want extractor resource node limit", err)
	}

	overNodeLimit := []byte("<root>" +
		strings.Repeat("<n/>", maxExtractorStructuredNodes) +
		"</root>")
	for name, extractValues := range map[string]func() error{
		"single": func() error {
			_, err := extractXPath("/root/n[1]", overNodeLimit)
			return err
		},
		"all": func() error {
			_, err := extractAllXPath("/root/n", overNodeLimit)
			return err
		},
	} {
		t.Run(name+" nodes", func(t *testing.T) {
			err := extractValues()
			if !errors.Is(err, errExtractorResourceLimit) || !strings.Contains(err.Error(), "nodes") {
				t.Fatalf("XML node-amplification error = %v, want extractor resource node limit", err)
			}
		})
	}

	atDepthLimit := []byte(strings.Repeat("<n>", maxExtractorStructuredDepth) +
		"ok" + strings.Repeat("</n>", maxExtractorStructuredDepth))
	deepPath := strings.Repeat("/n", maxExtractorStructuredDepth) + "/text()"
	if got, err := extractXPath(deepPath, atDepthLimit); err != nil || got != "ok" {
		t.Fatalf("XML depth boundary extraction = %q, %v; want ok", got, err)
	}
	overDepthLimit := []byte(strings.Repeat("<n>", maxExtractorStructuredDepth+1) +
		strings.Repeat("</n>", maxExtractorStructuredDepth+1))
	if _, err := extractXPath("//n", overDepthLimit); !errors.Is(err, errExtractorResourceLimit) || !strings.Contains(err.Error(), "depth") {
		t.Fatalf("XML depth-amplification error = %v, want extractor resource depth limit", err)
	}
}

func TestHTMLTreePreflightBoundsNodesAndDepthAcrossCSSAndXPath(t *testing.T) {
	atNodeLimit := []byte("<!doctype html><html><body><input id=\"target\" disabled>" +
		strings.Repeat("<div></div>", maxExtractorStructuredNodes-6))
	if err := validateExtractorHTMLStructure(atNodeLimit); err != nil {
		t.Fatalf("HTML node boundary preflight: %v", err)
	}
	overNodeLimitByAttribute := []byte("<!doctype html><html><body><input id=\"target\" disabled title=\"x\">" +
		strings.Repeat("<div></div>", maxExtractorStructuredNodes-6))
	if err := validateExtractorHTMLStructure(overNodeLimitByAttribute); !errors.Is(err, errExtractorResourceLimit) || !strings.Contains(err.Error(), "nodes") {
		t.Fatalf("HTML retained-attribute node-amplification error = %v, want extractor resource node limit", err)
	}

	overNodeLimit := []byte("<!doctype html><html><body><input disabled>" +
		strings.Repeat("<div></div>", maxExtractorStructuredNodes-3))
	for name, extractValues := range map[string]func() error{
		"css": func() error {
			_, err := extractCSS("input", overNodeLimit)
			return err
		},
		"xpath single": func() error {
			_, err := extractXPath("//input", overNodeLimit)
			return err
		},
		"xpath all": func() error {
			_, err := extractAllXPath("//input", overNodeLimit)
			return err
		},
	} {
		t.Run(name+" nodes", func(t *testing.T) {
			err := extractValues()
			if !errors.Is(err, errExtractorResourceLimit) || !strings.Contains(err.Error(), "nodes") {
				t.Fatalf("HTML node-amplification error = %v, want extractor resource node limit", err)
			}
		})
	}

	atDepthLimit := []byte("<!doctype html><input id=\"target\" disabled>" +
		strings.Repeat("<div>", maxExtractorStructuredDepth) +
		strings.Repeat("</div>", maxExtractorStructuredDepth))
	if got, err := extractCSS("#target|||id", atDepthLimit); err != nil || got != "target" {
		t.Fatalf("HTML depth boundary CSS extraction = %q, %v; want target", got, err)
	}
	overDepthLimit := []byte("<!doctype html><input disabled>" +
		strings.Repeat("<div>", maxExtractorStructuredDepth+1) +
		strings.Repeat("</div>", maxExtractorStructuredDepth+1))
	if _, err := extractXPath("//input", overDepthLimit); !errors.Is(err, errExtractorResourceLimit) || !strings.Contains(err.Error(), "depth") {
		t.Fatalf("HTML depth-amplification error = %v, want extractor resource depth limit", err)
	}
	slashSuffixedDepth := []byte("<!doctype html><input disabled>" +
		strings.Repeat("<div/>", maxExtractorStructuredDepth+1))
	if _, err := extractCSS("input", slashSuffixedDepth); !errors.Is(err, errExtractorResourceLimit) || !strings.Contains(err.Error(), "depth") {
		t.Fatalf("HTML slash-suffixed depth-amplification error = %v, want extractor resource depth limit", err)
	}
}

func TestStructuredTreePreflightBoundsElementAttributes(t *testing.T) {
	buildAttributes := func(count int) string {
		var attributes strings.Builder
		for index := 0; index < count; index++ {
			fmt.Fprintf(&attributes, ` a%d="x"`, index)
		}
		return attributes.String()
	}

	xmlAtLimit := []byte("<root" + buildAttributes(maxExtractorAttributesPerElement) + "/>")
	if err := validateExtractorXMLStructure(xmlAtLimit); err != nil {
		t.Fatalf("XML attribute boundary preflight: %v", err)
	}
	xmlOverLimit := []byte("<root" + buildAttributes(maxExtractorAttributesPerElement+1) + "/>")
	if _, err := extractXPath("/root", xmlOverLimit); !errors.Is(err, errExtractorResourceLimit) || !strings.Contains(err.Error(), "attributes") {
		t.Fatalf("XML attribute-amplification error = %v, want extractor resource attribute limit", err)
	}

	htmlAtLimit := []byte("<!doctype html><input disabled" + buildAttributes(maxExtractorAttributesPerElement-1) + ">")
	if err := validateExtractorHTMLStructure(htmlAtLimit); err != nil {
		t.Fatalf("HTML attribute boundary preflight: %v", err)
	}
	htmlOverLimit := []byte("<!doctype html><input disabled" + buildAttributes(maxExtractorAttributesPerElement) + ">")
	if _, err := extractCSS("input", htmlOverLimit); !errors.Is(err, errExtractorResourceLimit) || !strings.Contains(err.Error(), "attributes") {
		t.Fatalf("HTML attribute-amplification error = %v, want extractor resource attribute limit", err)
	}
}

func TestExtractXPath_XMLFragment(t *testing.T) {
	val, err := extractFromBody(protocol.Extractor{Type: "xpath", Expression: xpathFragmentExpressionPrefix + "/response/page"}, []byte(`<response><page>two</page></response>`))
	if err != nil {
		t.Fatalf("extract XPath XML fragment: %v", err)
	}
	if val != "<page>two</page>" {
		t.Errorf("got %q, want %q", val, "<page>two</page>")
	}
}

func TestExtractXPath_HTMLFragment(t *testing.T) {
	val, err := extractFromBody(protocol.Extractor{Type: "xpath", Expression: xpathFragmentExpressionPrefix + "//a[@class='login']"}, []byte(`<html><body><a class="login" href="/signin">Sign in`))
	if err != nil {
		t.Fatalf("extract XPath HTML fragment: %v", err)
	}
	if val != `<a class="login" href="/signin">Sign in</a>` {
		t.Errorf("got %q, want %q", val, `<a class="login" href="/signin">Sign in</a>`)
	}
}

func TestExtractXPath_XMLNamespaces(t *testing.T) {
	expression := mustEncodeXPathExpressionForTest(t, xpathExpressionConfig{
		Expression: "/age:ag/hd:head/title",
		Namespaces: map[string]string{
			"age": "http://www.w3.org/wgs84_pos#",
			"hd":  "http://www.w3.org/wgs85_pos#",
		},
	})
	val, err := extractFromBody(protocol.Extractor{Type: "xpath", Expression: expression}, []byte(`<age:ag xmlns:age="http://www.w3.org/wgs84_pos#"><hd:head xmlns:hd="http://www.w3.org/wgs85_pos#"><title>test</title></hd:head></age:ag>`))
	if err != nil {
		t.Fatalf("extract XPath XML namespaces: %v", err)
	}
	if val != "test" {
		t.Errorf("got %q, want %q", val, "test")
	}
}

func TestExtractXPath_XMLNamespacedFragment(t *testing.T) {
	expression := mustEncodeXPathExpressionForTest(t, xpathExpressionConfig{
		Expression: "/age:ag/hd:head",
		Fragment:   true,
		Namespaces: map[string]string{
			"age": "http://www.w3.org/wgs84_pos#",
			"hd":  "http://www.w3.org/wgs85_pos#",
		},
	})
	val, err := extractFromBody(protocol.Extractor{Type: "xpath", Expression: expression}, []byte(`<age:ag xmlns:age="http://www.w3.org/wgs84_pos#"><hd:head xmlns:hd="http://www.w3.org/wgs85_pos#"><title>test</title></hd:head></age:ag>`))
	if err != nil {
		t.Fatalf("extract XPath XML namespaced fragment: %v", err)
	}
	if val != `<hd:head xmlns:hd="http://www.w3.org/wgs85_pos#"><title>test</title></hd:head>` {
		t.Errorf("got %q, want %q", val, `<hd:head xmlns:hd="http://www.w3.org/wgs85_pos#"><title>test</title></hd:head>`)
	}
}

func TestExtractXPath_NoMatch(t *testing.T) {
	_, err := extractFromBody(protocol.Extractor{Type: "xpath", Expression: "/response/missing/text()"}, []byte(`<response><value>ok</value></response>`))
	if err == nil {
		t.Fatal("expected XPath no-match error")
	}
}

func TestExtractXPath_InvalidExpression(t *testing.T) {
	_, err := extractFromBody(protocol.Extractor{Type: "xpath", Expression: "//a["}, []byte(`<html><body><a href="/signin">Sign in</a></body></html>`))
	if err == nil {
		t.Fatal("expected invalid XPath expression error")
	}
}

func mustEncodeXPathExpressionForTest(t *testing.T, config xpathExpressionConfig) string {
	t.Helper()
	payload, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("marshal XPath config: %v", err)
	}
	return xpathExpressionConfigPrefix + base64.RawStdEncoding.EncodeToString(payload)
}

func TestExtractRegex(t *testing.T) {
	body := []byte(`token=abc123&expires=3600`)

	val, err := extractRegex(`token=([^&]+)`, body)
	if err != nil {
		t.Fatalf("extractRegex: %v", err)
	}
	if val != "abc123" {
		t.Errorf("got %q, want %q", val, "abc123")
	}
}

func TestExtractRegexWithoutCaptureReturnsFullMatch(t *testing.T) {
	got, err := extractRegex(`token-\d+`, []byte(`before token-42 after`))
	if err != nil {
		t.Fatalf("extractRegex: %v", err)
	}
	if got != "token-42" {
		t.Fatalf("extractRegex = %q, want full match", got)
	}
}

func TestExtractRegexVariantsWithoutCaptureReturnFullMatches(t *testing.T) {
	body := []byte(`token-1 token-2`)
	ordinal, err := extractRegexOrdinal(`token-\d+`, body)
	if err != nil {
		t.Fatalf("extractRegexOrdinal: %v", err)
	}
	if ordinal != "token-1" {
		t.Fatalf("extractRegexOrdinal = %q, want token-1", ordinal)
	}
	all, err := extractAllRegex(`token-\d+`, body)
	if err != nil {
		t.Fatalf("extractAllRegex: %v", err)
	}
	if strings.Join(all, ",") != "token-1,token-2" {
		t.Fatalf("extractAllRegex = %#v", all)
	}
}

func TestExtractRegex_NoMatch(t *testing.T) {
	_, err := extractRegex(`xyz=([^&]+)`, []byte(`token=abc`))
	if err == nil {
		t.Fatal("expected error for no match")
	}
}

func TestExtractorPostProcessingBoundsTransformInputAndOutput(t *testing.T) {
	tooLargeInput := strings.Repeat("a", maxExtractorTransformInputBytes+1)
	if _, err := postProcessExtractedValue(protocol.Extractor{Convert: "url_decode"}, tooLargeInput); !errors.Is(err, errExtractorResourceLimit) || !strings.Contains(err.Error(), "transform input") {
		t.Fatalf("oversized transform input error = %v, want resource limit", err)
	}

	expanded := strings.Repeat("?", maxAllExtractorBytes)
	if _, err := postProcessExtractedValue(protocol.Extractor{Convert: "url_encode"}, expanded); !errors.Is(err, errExtractorResourceLimit) || !strings.Contains(err.Error(), "output") {
		t.Fatalf("expanded transform output error = %v, want resource limit", err)
	}

	trimmed, err := postProcessExtractedValue(protocol.Extractor{SaveLength: 4}, tooLargeInput)
	if err != nil || trimmed != "aaaa" {
		t.Fatalf("bounded save_length result = %q, %v; want aaaa", trimmed, err)
	}

	oversizedCapture := []byte("token=" + strings.Repeat("x", maxExtractorTransformInputBytes+1))
	if _, err := extractRegex(`token=(.+)`, oversizedCapture); !errors.Is(err, errExtractorResourceLimit) || !strings.Contains(err.Error(), "capture") {
		t.Fatalf("oversized regex capture error = %v, want resource limit before string materialization", err)
	}
}

func TestExtractRegex_InvalidPattern(t *testing.T) {
	_, err := extractRegex(`[invalid`, []byte(`data`))
	if err == nil {
		t.Fatal("expected error for invalid regex")
	}
}

func TestExtractRegexCount(t *testing.T) {
	val, err := extractRegexCount(`Show Errors`, []byte(`OK Show Errors also Show Errors`))
	if err != nil {
		t.Fatalf("extractRegexCount: %v", err)
	}
	if val != "2" {
		t.Fatalf("regex count = %q, want 2", val)
	}
}

func TestExtractRegexCountRejectsExcessiveMatches(t *testing.T) {
	body := bytes.Repeat([]byte("x"), maxAllExtractorMatches+1)
	if _, err := extractRegexCount("x", body); err == nil {
		t.Fatal("expected bounded regex count to reject excessive matches")
	}
}

func TestExtractRegexOrdinalIgnoresLaterMatches(t *testing.T) {
	body := bytes.Repeat([]byte("value=ok "), maxAllExtractorMatches+1)
	payload, err := json.Marshal(regexOrdinalExpressionConfig{Expression: `value=(\w+)`, Ordinal: 1})
	if err != nil {
		t.Fatal(err)
	}
	expr := regexOrdinalExpressionConfigPrefix + string(payload)
	got, err := extractRegexOrdinal(expr, body)
	if err != nil || got != "ok" {
		t.Fatalf("extract ordinal = %q, %v; want ok", got, err)
	}
}

func TestExtractRegexOrdinal(t *testing.T) {
	payload, err := json.Marshal(regexOrdinalExpressionConfig{
		Expression: `/TD><TD>(.*?)</TD`,
		Ordinal:    2,
	})
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	val, err := extractRegexOrdinal(regexOrdinalExpressionConfigPrefix+string(payload), []byte(`/TD><TD>first</TD /TD><TD>second</TD`))
	if err != nil {
		t.Fatalf("extractRegexOrdinal: %v", err)
	}
	if val != "second" {
		t.Fatalf("regex ordinal = %q, want second", val)
	}
}

func TestExtractBoundary(t *testing.T) {
	body := []byte(`<input name="csrf" value="tok123" />`)

	val, err := extractBoundary(`value="|||"`, body)
	if err != nil {
		t.Fatalf("extractBoundary: %v", err)
	}
	if val != "tok123" {
		t.Errorf("got %q, want %q", val, "tok123")
	}
}

func TestExtractBoundary_MissingLeft(t *testing.T) {
	_, err := extractBoundary(`notfound|||end`, []byte(`some content`))
	if err == nil {
		t.Fatal("expected error for missing left boundary")
	}
}

func TestExtractBoundary_MissingRight(t *testing.T) {
	_, err := extractBoundary(`some|||notfound`, []byte(`some content`))
	if err == nil {
		t.Fatal("expected error for missing right boundary")
	}
}

func TestExtractBoundary_InvalidFormat(t *testing.T) {
	_, err := extractBoundary(`no-separator-here`, []byte(`data`))
	if err == nil {
		t.Fatal("expected error for missing ||| separator")
	}
}

func TestExtractBoundary_ConfigOrdinal(t *testing.T) {
	body := []byte(`id="first" id="second" id="third"`)

	tests := []struct {
		ordinal int
		want    string
	}{
		{0, "first"},
		{1, "first"},
		{2, "second"},
		{3, "third"},
	}
	for _, tc := range tests {
		expr := boundaryExpressionConfigPrefix + fmt.Sprintf(`{"left":"id=\"","right":"\"","ordinal":%d}`, tc.ordinal)
		val, err := extractBoundary(expr, body)
		if err != nil {
			t.Fatalf("ordinal %d: %v", tc.ordinal, err)
		}
		if val != tc.want {
			t.Errorf("ordinal %d = %q, want %q", tc.ordinal, val, tc.want)
		}
	}
}

func TestExtractBoundary_ConfigOrdinalOutOfRange(t *testing.T) {
	body := []byte(`id="first" id="second"`)
	expr := boundaryExpressionConfigPrefix + `{"left":"id=\"","right":"\"","ordinal":5}`
	_, err := extractBoundary(expr, body)
	if err == nil {
		t.Fatal("expected error for out-of-range ordinal")
	}
	if !strings.Contains(err.Error(), "ordinal 5") {
		t.Errorf("error should mention requested ordinal: %v", err)
	}
}

func TestExtractBoundary_ConfigCaseInsensitive(t *testing.T) {
	body := []byte(`SessionID=ABC123;`)
	expr := boundaryExpressionConfigPrefix + `{"left":"sessionid=","right":";","case_insensitive":true}`
	val, err := extractBoundary(expr, body)
	if err != nil {
		t.Fatalf("extractBoundary: %v", err)
	}
	if val != "ABC123" {
		t.Errorf("got %q, want ABC123 (original casing preserved)", val)
	}
}

func TestExtractBoundary_ConfigSeparatorCollision(t *testing.T) {
	body := []byte(`x=a|||b;`)
	expr := boundaryExpressionConfigPrefix + `{"left":"x=","right":";"}`
	val, err := extractBoundary(expr, body)
	if err != nil {
		t.Fatalf("extractBoundary: %v", err)
	}
	if val != "a|||b" {
		t.Errorf("got %q, want a|||b", val)
	}
}

func TestExtractBoundary_ConfigInvalid(t *testing.T) {
	if _, err := extractBoundary(boundaryExpressionConfigPrefix+`{not-json`, []byte(`x`)); err == nil {
		t.Fatal("expected error for invalid config payload")
	}
	if _, err := extractBoundary(boundaryExpressionConfigPrefix+`{}`, []byte(`x`)); err == nil {
		t.Fatal("expected error for empty boundaries")
	}
}

func TestExtract_FromHeader(t *testing.T) {
	headers := http.Header{}
	headers.Set("X-Request-Id", "req-12345")

	val, err := extract(protocol.Extractor{
		Name:       "request_id",
		Source:     "header",
		Expression: "X-Request-Id",
	}, headers, nil)

	if err != nil {
		t.Fatalf("extract header: %v", err)
	}
	if val != "req-12345" {
		t.Errorf("got %q, want %q", val, "req-12345")
	}
}

func TestExtract_FromHeaderRegex(t *testing.T) {
	headers := http.Header{}
	headers.Set("Set-Cookie", "session_id=abc123; Path=/; HttpOnly")

	val, err := extract(protocol.Extractor{
		Name:       "session_id",
		Source:     "header",
		Type:       "regex",
		Expression: `Set-Cookie: session_id=([^;]+)`,
	}, headers, nil)

	if err != nil {
		t.Fatalf("extract header regex: %v", err)
	}
	if val != "abc123" {
		t.Errorf("got %q, want %q", val, "abc123")
	}
}

func TestExtract_FromHeaderBoundary(t *testing.T) {
	headers := http.Header{}
	headers.Set("Set-Cookie", "session_id=abc123; Path=/; HttpOnly")

	val, err := extract(protocol.Extractor{
		Name:       "session_id",
		Source:     "header",
		Type:       "boundary",
		Expression: "Set-Cookie: session_id=|||;",
	}, headers, nil)

	if err != nil {
		t.Fatalf("extract header boundary: %v", err)
	}
	if val != "abc123" {
		t.Errorf("got %q, want %q", val, "abc123")
	}
}

func TestExtract_HeaderNotFound(t *testing.T) {
	_, err := extract(protocol.Extractor{
		Source:     "header",
		Expression: "X-Missing",
	}, http.Header{}, nil)

	if err == nil {
		t.Fatal("expected error for missing header")
	}
}

func TestExtract_UnsupportedSource(t *testing.T) {
	_, err := extract(protocol.Extractor{Source: "cookies"}, http.Header{}, nil)
	if err == nil {
		t.Fatal("expected error for unsupported source")
	}
}

func TestExtract_UnsupportedHeaderType(t *testing.T) {
	_, err := extract(protocol.Extractor{Source: "header", Type: "jsonpath", Expression: "$.token"}, http.Header{"X-Test": {"value"}}, nil)
	if err == nil {
		t.Fatal("expected error for unsupported header extraction type")
	}
}

func TestExtract_UnsupportedBodyType(t *testing.T) {
	_, err := extractFromBody(protocol.Extractor{Type: "graphql"}, []byte(`<xml/>`))
	if err == nil {
		t.Fatal("expected error for unsupported extraction type")
	}
}

func TestExtractBoundary_EmptyRightTakesRestOfBody(t *testing.T) {
	val, err := extractBoundary(`token=|||`, []byte(`prefix token=abc123`))
	if err != nil {
		t.Fatalf("extractBoundary: %v", err)
	}
	if val != "abc123" {
		t.Errorf("got %q, want abc123 (rest of body)", val)
	}
}

func TestExtractBoundary_EmptyLeftStartsAtBeginning(t *testing.T) {
	val, err := extractBoundary(`|||;`, []byte(`abc;def`))
	if err != nil {
		t.Fatalf("extractBoundary: %v", err)
	}
	if val != "abc" {
		t.Errorf("got %q, want abc", val)
	}
}

func TestExtractAll_Regex(t *testing.T) {
	body := []byte(`id="a" id="b" id="c"`)
	vals, err := extractAll(protocol.Extractor{Source: "body", Type: "regex", Expression: `id="([a-z])"`, Mode: "all"}, nil, body)
	if err != nil {
		t.Fatalf("extractAll: %v", err)
	}
	if len(vals) != 3 || vals[0] != "a" || vals[2] != "c" {
		t.Errorf("vals = %v, want [a b c]", vals)
	}
}

func TestExtractAll_DirectHeaderPreservesOccurrences(t *testing.T) {
	headers := http.Header{}
	headers.Add("Set-Cookie", "first=one")
	headers.Add("Set-Cookie", "second=two")

	vals, err := extractAll(protocol.Extractor{
		Source:     "header",
		Type:       "header",
		Expression: "set-cookie",
		Mode:       "all",
	}, headers, nil)
	if err != nil {
		t.Fatalf("extractAll direct header: %v", err)
	}
	if len(vals) != 2 || vals[0] != "first=one" || vals[1] != "second=two" {
		t.Fatalf("vals = %v, want both Set-Cookie occurrences", vals)
	}

	vals, err = extractAll(protocol.Extractor{
		Source:     "header",
		Expression: "X-Missing",
		Mode:       "all",
	}, headers, nil)
	if err != nil {
		t.Fatalf("extractAll missing direct header: %v", err)
	}
	if len(vals) != 0 {
		t.Fatalf("missing header vals = %v, want empty", vals)
	}
}

func TestExtractAll_BoundaryWithConfig(t *testing.T) {
	body := []byte(`ID=x1; id=x2; Id=x3;`)
	expr := boundaryExpressionConfigPrefix + `{"left":"id=","right":";","case_insensitive":true}`
	vals, err := extractAll(protocol.Extractor{Source: "body", Type: "boundary", Expression: expr, Mode: "all"}, nil, body)
	if err != nil {
		t.Fatalf("extractAll: %v", err)
	}
	if len(vals) != 3 || vals[0] != "x1" || vals[1] != "x2" || vals[2] != "x3" {
		t.Errorf("vals = %v, want [x1 x2 x3] with original casing", vals)
	}
}

func TestExtractAll_BoundaryPlain(t *testing.T) {
	vals, err := extractAll(protocol.Extractor{Source: "body", Type: "boundary", Expression: "k=|||;", Mode: "all"}, nil, []byte(`k=1; k=2;`))
	if err != nil {
		t.Fatalf("extractAll: %v", err)
	}
	if len(vals) != 2 || vals[0] != "1" || vals[1] != "2" {
		t.Errorf("vals = %v, want [1 2]", vals)
	}
}

func TestExtractAll_JSONPathArray(t *testing.T) {
	body := []byte(`{"items":["alpha","beta"]}`)
	vals, err := extractAll(protocol.Extractor{Source: "body", Type: "jsonpath", Expression: "$.items", Mode: "all"}, nil, body)
	if err != nil {
		t.Fatalf("extractAll: %v", err)
	}
	if len(vals) != 2 || vals[0] != "alpha" || vals[1] != "beta" {
		t.Errorf("vals = %v, want [alpha beta]", vals)
	}
}

func TestExtractAll_XPathMultiple(t *testing.T) {
	body := []byte(`<r><v>one</v><v>two</v></r>`)
	vals, err := extractAll(protocol.Extractor{Source: "body", Type: "xpath", Expression: "//v", Mode: "all"}, nil, body)
	if err != nil {
		t.Fatalf("extractAll: %v", err)
	}
	if len(vals) != 2 || vals[0] != "one" || vals[1] != "two" {
		t.Errorf("vals = %v, want [one two]", vals)
	}
}

func TestExtractAll_ZeroMatches(t *testing.T) {
	vals, err := extractAll(protocol.Extractor{Source: "body", Type: "regex", Expression: `nope=(\d+)`, Mode: "all"}, nil, []byte(`body`))
	if err != nil {
		t.Fatalf("extractAll: %v", err)
	}
	if len(vals) != 0 {
		t.Errorf("vals = %v, want empty", vals)
	}
}

func TestPostProcessExtractedValue(t *testing.T) {
	tests := []struct {
		name string
		ext  protocol.Extractor
		in   string
		want string
	}{
		{"offset", protocol.Extractor{SaveOffset: 2}, "abcdef", "cdef"},
		{"offset beyond end", protocol.Extractor{SaveOffset: 10}, "abc", ""},
		{"length", protocol.Extractor{SaveLength: 3}, "abcdef", "abc"},
		{"offset then length", protocol.Extractor{SaveOffset: 1, SaveLength: 2}, "abcdef", "bc"},
		{"length beyond end", protocol.Extractor{SaveLength: 10}, "abc", "abc"},
		{"url encode", protocol.Extractor{Convert: "url_encode"}, "a b&c", "a+b%26c"},
		{"url decode", protocol.Extractor{Convert: "url_decode"}, "a+b%26c", "a b&c"},
		{"html decode", protocol.Extractor{Convert: "html_decode"}, "a &amp; b &lt;ok&gt;", "a & b <ok>"},
		{"combined", protocol.Extractor{SaveOffset: 4, Convert: "url_encode"}, "val=x y", "x+y"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := postProcessExtractedValue(tc.ext, tc.in)
			if err != nil {
				t.Fatalf("postProcess: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPostProcessExtractedValue_UnknownConvert(t *testing.T) {
	if _, err := postProcessExtractedValue(protocol.Extractor{Convert: "rot13"}, "x"); err == nil {
		t.Fatal("expected error for unknown convert mode")
	}
}

func TestExtract_AppliesPostProcessing(t *testing.T) {
	val, err := extract(protocol.Extractor{Source: "body", Type: "boundary", Expression: "t=|||;", SaveOffset: 1, SaveLength: 2}, nil, []byte(`t=abcdef;`))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if val != "bc" {
		t.Errorf("got %q, want bc", val)
	}
}

func TestExtractBoundary_CaseInsensitiveUnicodePreservesIndices(t *testing.T) {
	// U+212A KELVIN SIGN is 3 bytes but lowercases to 1-byte "k"; Unicode-aware
	// folding would shift match offsets and corrupt the extracted value.
	body := []byte("KK token=abc;done")
	expr := boundaryExpressionConfigPrefix + `{"left":"TOKEN=","right":";","case_insensitive":true}`
	val, err := extractBoundary(expr, body)
	if err != nil {
		t.Fatalf("extractBoundary: %v", err)
	}
	if val != "abc" {
		t.Errorf("got %q, want abc", val)
	}
}

func TestExtractBoundary_CaseInsensitiveExpandingRuneNoPanic(t *testing.T) {
	// U+023A grows from 2 to 3 bytes under Unicode lowercasing; with an empty
	// right boundary this used to compute indices past the original body and
	// panic, killing the load-generator process.
	body := []byte("Ⱥ token=abc")
	expr := boundaryExpressionConfigPrefix + `{"left":"TOKEN=","right":"","case_insensitive":true}`
	val, err := extractBoundary(expr, body)
	if err != nil {
		t.Fatalf("extractBoundary: %v", err)
	}
	if val != "abc" {
		t.Errorf("got %q, want abc", val)
	}
}

func TestExtractAllBoundary_CaseInsensitiveUnicode(t *testing.T) {
	body := []byte("K id=a; ID=b;")
	expr := boundaryExpressionConfigPrefix + `{"left":"id=","right":";","case_insensitive":true}`
	vals, err := extractAll(protocol.Extractor{Source: "body", Type: "boundary", Expression: expr, Mode: "all"}, nil, body)
	if err != nil {
		t.Fatalf("extractAll: %v", err)
	}
	if len(vals) != 2 || vals[0] != "a" || vals[1] != "b" {
		t.Errorf("vals = %v, want [a b]", vals)
	}
}

func TestExtractAllBoundary_EmptyRightSingleMatch(t *testing.T) {
	vals, err := extractAll(protocol.Extractor{Source: "body", Type: "boundary", Expression: "k=|||", Mode: "all"}, nil, []byte(`k=rest of body`))
	if err != nil {
		t.Fatalf("extractAll: %v", err)
	}
	if len(vals) != 1 || vals[0] != "rest of body" {
		t.Errorf("vals = %v, want single rest-of-body match (no phantom empties)", vals)
	}
}

func TestPostProcessExtractedValue_HTMLURLEncode(t *testing.T) {
	got, err := postProcessExtractedValue(protocol.Extractor{Convert: "html_url_encode"}, "a &amp; b")
	if err != nil {
		t.Fatalf("postProcess: %v", err)
	}
	if got != "a%20%26%20b" {
		t.Errorf("got %q, want a%%20%%26%%20b (entity-decoded, %%20 spaces)", got)
	}
}

func TestCompileRegexCached_ReusesInstance(t *testing.T) {
	const expr = `compile-cache-reuse-([0-9]+)`

	first, err := compileRegexCached(expr)
	if err != nil {
		t.Fatalf("compileRegexCached: %v", err)
	}
	second, err := compileRegexCached(expr)
	if err != nil {
		t.Fatalf("compileRegexCached (repeat): %v", err)
	}
	// The cache must hand back the exact same *regexp.Regexp, not a fresh
	// recompilation, so repeated VUser requests share one compiled pattern.
	if first != second {
		t.Error("expected the cached compile to return the same *regexp.Regexp instance")
	}
}

func TestCompileRegexCached_InvalidNotCached(t *testing.T) {
	const expr = `[invalid-uncached`

	if _, err := compileRegexCached(expr); err == nil {
		t.Fatal("expected error for invalid regex")
	}
	// A failed compile must not be stored: the second call has to surface the
	// same error rather than a nil cache hit.
	if _, ok := compiledRegexCache.Load(expr); ok {
		t.Error("invalid regex must not be cached")
	}
	if _, err := compileRegexCached(expr); err == nil {
		t.Fatal("expected the invalid regex to keep erroring on repeat calls")
	}
}

func TestCompiledExtractorCacheBoundsRetainedKeyBytes(t *testing.T) {
	cache := compiledExtractorCache[int]{maxKeyBytes: 10}
	cache.put("123456", 1)
	cache.put("abcdef", 2)
	if _, ok := cache.Load("123456"); ok {
		t.Fatal("oldest key was retained after the byte budget was exceeded")
	}
	if value, ok := cache.Load("abcdef"); !ok || value != 2 {
		t.Fatalf("new key = %d, %t; want retained value 2", value, ok)
	}
	if cache.keyBytes != len("abcdef") {
		t.Fatalf("retained key bytes = %d, want %d", cache.keyBytes, len("abcdef"))
	}

	cache.put("this-key-is-too-large", 3)
	if _, ok := cache.Load("this-key-is-too-large"); ok {
		t.Fatal("a key larger than the cache byte budget was retained")
	}
	if cache.keyBytes != len("abcdef") {
		t.Fatalf("oversized uncached key changed retained bytes to %d", cache.keyBytes)
	}
}

func TestCompileRegexCachedRejectsOversizedExpression(t *testing.T) {
	expr := strings.Repeat("x", MaxExtractorExpressionBytes+1)
	if _, err := compileRegexCached(expr); !errors.Is(err, errExtractorResourceLimit) {
		t.Fatalf("compile oversized regex error = %v, want extractor resource limit", err)
	}
	if _, ok := compiledRegexCache.Load(expr); ok {
		t.Fatal("oversized regex expression must not be cached")
	}
}

func TestCompiledExpressionCachesRejectOversizedKeys(t *testing.T) {
	expr := strings.Repeat("x", MaxExtractorExpressionBytes+1)
	tests := []struct {
		name    string
		compile func() error
		cached  func() bool
	}{
		{
			name: "jmespath",
			compile: func() error {
				_, err := compileJMESPathCached(expr)
				return err
			},
			cached: func() bool {
				_, ok := compiledJMESPathCache.Load(expr)
				return ok
			},
		},
		{
			name: "xpath",
			compile: func() error {
				_, err := compileXPathSelector(expr, nil)
				return err
			},
			cached: func() bool {
				_, ok := compiledXPathCache.Load(expr)
				return ok
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.compile(); !errors.Is(err, errExtractorResourceLimit) {
				t.Fatalf("compile oversized expression error = %v, want extractor resource limit", err)
			}
			if test.cached() {
				t.Fatal("oversized expression must not be cached")
			}
		})
	}
}

func TestExtractAllRegex_CachedMatchesStable(t *testing.T) {
	// Caching must not change match semantics: the same extractor reused across
	// many requests must produce identical extractions every time.
	const expr = `id="([a-z]+)"`
	body := []byte(`id="alpha" id="beta" id="gamma"`)
	want := []string{"alpha", "beta", "gamma"}

	for i := 0; i < 5; i++ {
		got, err := extractAllRegex(expr, body)
		if err != nil {
			t.Fatalf("iteration %d: extractAllRegex: %v", i, err)
		}
		if len(got) != len(want) {
			t.Fatalf("iteration %d: got %v, want %v", i, got, want)
		}
		for j := range want {
			if got[j] != want[j] {
				t.Fatalf("iteration %d: got %v, want %v", i, got, want)
			}
		}
	}
}

func TestExtractRegex_CachedAndUncachedAgree(t *testing.T) {
	// The cached path must yield exactly what an independent fresh compile does.
	const expr = `token=([^&]+)`
	body := []byte(`token=abc123&expires=3600`)

	cached, err := extractRegex(expr, body)
	if err != nil {
		t.Fatalf("extractRegex: %v", err)
	}
	fresh := regexp.MustCompile(expr).FindSubmatch(body)
	if len(fresh) < 2 {
		t.Fatal("control compile found no match")
	}
	if cached != string(fresh[1]) {
		t.Errorf("cached extract = %q, fresh compile = %q", cached, string(fresh[1]))
	}
}

func BenchmarkExtractAllRegex(b *testing.B) {
	// Reuses one static extractor expression across every iteration, mirroring a
	// single extractor replayed by a VUser. With per-request regexp.Compile this
	// allocated/compiled on every call; with the cache it compiles once.
	const expr = `id="([a-z]+)"`
	body := []byte(`id="alpha" id="beta" id="gamma" id="delta"`)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := extractAllRegex(expr, body); err != nil {
			b.Fatalf("extractAllRegex: %v", err)
		}
	}
}

func BenchmarkExtractRegex(b *testing.B) {
	const expr = `token=([^&]+)`
	body := []byte(`token=abc123&expires=3600`)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := extractRegex(expr, body); err != nil {
			b.Fatalf("extractRegex: %v", err)
		}
	}
}
