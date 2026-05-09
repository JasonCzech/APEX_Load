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

func TestExtract_UnsupportedBodyType(t *testing.T) {
	_, err := extractFromBody(protocol.Extractor{Type: "xpath"}, []byte(`<xml/>`))
	if err == nil {
		t.Fatal("expected error for unsupported extraction type")
	}
}
