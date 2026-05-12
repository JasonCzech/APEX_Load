package http

import (
	"net/http"
	"testing"

	"github.com/jasonczech/stormrunner/pkg/protocol"
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
