package templatedjson

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNullExpressionsReadsUnquotedExpressionsAsNull(t *testing.T) {
	body := `{"session_id":"AUTO","note":"{{var.kept}} {{x","events":[{"t":{{fn.timestamp.unix_ms}},"u":"a\"{{b"},{{data.n}}]}`
	out, ok := NullExpressions([]byte(body), nil)
	if !ok {
		t.Fatal("expressions refused")
	}
	want := `{"session_id":"AUTO","note":"{{var.kept}} {{x","events":[{"t":null,"u":"a\"{{b"},null]}`
	if string(out) != want {
		t.Fatalf("out = %s\nwant  %s", out, want)
	}
	if !json.Valid(out) {
		t.Fatalf("out is not JSON: %s", out)
	}
}

func TestNullExpressionsRefusesWhatItCannotVouchFor(t *testing.T) {
	allowTimestamps := func(expression string) bool { return expression == "{{fn.timestamp.unix_ms}}" }
	if _, ok := NullExpressions([]byte(`{"t":{{fn.timestamp.unix_ms}}}`), allowTimestamps); !ok {
		t.Fatal("allowed expression refused")
	}
	if _, ok := NullExpressions([]byte(`{"p":{{fn.base64("hunter2")}}}`), allowTimestamps); ok {
		t.Fatal("expression with a literal argument was read")
	}
	if _, ok := NullExpressions([]byte(`{"t":{{fn.timestamp`), nil); ok {
		t.Fatal("unclosed expression was read")
	}
	plain := []byte(`{"a":1}`)
	if out, ok := NullExpressions(plain, allowTimestamps); !ok || &out[0] != &plain[0] {
		t.Fatal("a body without expressions should come back as it is")
	}
	if out, _ := NullExpressions([]byte(`"{{a}}"`), nil); !strings.Contains(string(out), "{{a}}") {
		t.Fatal("expression inside a string was changed")
	}
}

func TestClassifyOccurrencesAndEscapeStringContent(t *testing.T) {
	payload := `{"prompt":"Say \"hello\" to {{var.user}}","max_tokens":{{var.max}}}`
	uIdx := strings.Index(payload, "{{var.user}}")
	mIdx := strings.Index(payload, "{{var.max}}")
	matches := [][]int{
		{uIdx, uIdx + len("{{var.user}}")},
		{mIdx, mIdx + len("{{var.max}}")},
	}
	ctxs, err := ClassifyOccurrences(payload, matches)
	if err != nil {
		t.Fatalf("ClassifyOccurrences: %v", err)
	}
	if !ctxs[0].InString || ctxs[1].InString {
		t.Fatalf("unexpected contexts: %+v", ctxs)
	}

	escaped := EscapeStringContent("line1\n\"quoted\"\t\\slash\x01")
	if escaped != `line1\n\"quoted\"\t\\slash\u0001` {
		t.Fatalf("EscapeStringContent = %q", escaped)
	}
	if err := ValidateUnquotedValue("128"); err != nil {
		t.Fatalf("ValidateUnquotedValue(128): %v", err)
	}
	if err := ValidateUnquotedValue("not json"); err == nil {
		t.Fatal("expected ValidateUnquotedValue to reject unquoted plain text")
	}
}
