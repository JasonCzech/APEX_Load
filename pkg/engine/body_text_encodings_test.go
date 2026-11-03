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

func TestJSONBodyTemplateEncoding(t *testing.T) {
	vu := &VUser{
		Variables: map[string]string{
			"prompt":     "Line 1\n\"quoted\" and \\backslash\\\t\x01",
			"max_tokens": "128",
			"messages":   `[{"role":"user","content":"hello\nworld"}]`,
			"bad_num":    "128; drop table",
		},
		SecretResolver: SecretResolverFunc(func(name string) (string, bool) {
			if name == "API_TOKEN" {
				return `"secret-json-string"`, true
			}
			return "", false
		}),
	}
	action := dsl.Action{
		Name:                 "chat",
		Type:                 "http",
		Method:               "POST",
		Target:               "https://example.invalid/v1/chat/completions",
		Body:                 `{"prompt":"{{var.prompt}}","max_tokens":{{var.max_tokens}},"messages":{{var.messages}},"token":{{secret.API_TOKEN}}}`,
		BodyTemplateEncoding: "json",
	}
	resolved := vu.resolveAction(action)
	if vu.bodyResolutionError != nil {
		t.Fatalf("unexpected bodyResolutionError: %v", vu.bodyResolutionError)
	}
	wantBody := `{"prompt":"Line 1\n\"quoted\" and \\backslash\\\t\u0001","max_tokens":128,"messages":[{"role":"user","content":"hello\nworld"}],"token":"secret-json-string"}`
	if string(resolved.Body) != wantBody {
		t.Fatalf("resolved.Body = %s, want %s", resolved.Body, wantBody)
	}
	wantRedacted := `{"prompt":"Line 1\n\"quoted\" and \\backslash\\\t\u0001","max_tokens":128,"messages":[{"role":"user","content":"hello\nworld"}],"token":"[REDACTED]"}`
	if string(resolved.RedactedBody) != wantRedacted {
		t.Fatalf("resolved.RedactedBody = %s, want %s", resolved.RedactedBody, wantRedacted)
	}

	// Unquoted non-JSON replacement must fail resolution.
	bad := action
	bad.Body = `{"max_tokens":{{var.bad_num}}}`
	vu.resolveAction(bad)
	if vu.bodyResolutionError == nil {
		t.Fatalf("expected bodyResolutionError for invalid unquoted JSON value")
	}
}
