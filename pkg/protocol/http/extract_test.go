package http

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

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

func TestExtractXPath_HTMLFallback(t *testing.T) {
	val, err := extractFromBody(protocol.Extractor{Type: "xpath", Expression: "//a[@class='login']/@href"}, []byte(`<html><body><a class="login" href="/signin">Sign in`))
	if err != nil {
		t.Fatalf("extract XPath HTML fallback: %v", err)
	}
	if val != "/signin" {
		t.Errorf("got %q, want %q", val, "/signin")
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

func TestExtractRegex_NoMatch(t *testing.T) {
	_, err := extractRegex(`xyz=([^&]+)`, []byte(`token=abc`))
	if err == nil {
		t.Fatal("expected error for no match")
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
