package engine

import (
	"errors"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/bodyencoding"
)

func TestCEscapedBodyDecodesAfterInterpolationAndKeepsValuesVerbatim(t *testing.T) {
	vu := &VUser{Variables: map[string]string{"token": `a\x41`}}
	action := dsl.Action{Type: "http", Method: "POST", Target: "https://example.invalid", Body: `\x02{{var.token}}\x03\\`, BodyEncoding: "c_escaped"}
	resolved := vu.resolveAction(action)
	if vu.bodyResolutionError != nil {
		t.Fatal(vu.bodyResolutionError)
	}
	if got, want := string(resolved.Body), "\x02a\\x41\x03\\"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
	if string(resolved.RedactedBody) != string(resolved.Body) {
		t.Fatalf("redacted body = %q, want the same bytes when no secret is involved", resolved.RedactedBody)
	}

	broken := action
	broken.Body = `\q`
	vu.resolveAction(broken)
	if !errors.Is(vu.bodyResolutionError, bodyencoding.ErrInvalidEscape) {
		t.Fatalf("an invalid escape must refuse the body, got %v", vu.bodyResolutionError)
	}
}

func TestUTF16LEBodyIsInterpolatedAsText(t *testing.T) {
	vu := &VUser{Variables: map[string]string{"name": "é"}}
	resolved := vu.resolveAction(dsl.Action{Type: "http", Method: "POST", Target: "https://example.invalid", Body: "hi {{var.name}}", BodyEncoding: "utf-16le"})
	if string(resolved.Body) != "hi é" || resolved.BodyEncoding != "utf-16le" {
		t.Fatalf("resolved = %q (%s); the protocol encodes UTF-16LE after interpolation", resolved.Body, resolved.BodyEncoding)
	}
}

func TestTextMatchesTreatsParameterValuesLiterally(t *testing.T) {
	vu := &VUser{Variables: map[string]string{"code": "a.b(1)"}}
	resolved := vu.resolveAction(dsl.Action{Type: "http", Method: "GET", Target: "https://example.invalid", Assertions: []dsl.Assertion{
		{Type: "text_matches", Value: `[0-9]{{var.code}}`, Source: "headers"},
		{Type: "text_contains", Value: `{{var.code}}`},
	}})
	if got := resolved.Assertions[0].Value; got != `[0-9]a\.b\(1\)` {
		t.Fatalf("pattern = %q, want the parameter value quoted", got)
	}
	if resolved.Assertions[0].Source != "headers" {
		t.Fatalf("source = %q", resolved.Assertions[0].Source)
	}
	if got := resolved.Assertions[1].Value; got != "a.b(1)" {
		t.Fatalf("literal = %q, want the raw value", got)
	}
}
