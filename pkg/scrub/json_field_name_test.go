package scrub

import "testing"

func TestUnquoteJSONFieldNameDecodesTheCapturedQuotedSpan(t *testing.T) {
	value := `{"password": "x", "a\"b": 1}`
	matches := jsonAnyFieldStart.FindAllStringSubmatchIndex(value, -1)
	if len(matches) != 2 {
		t.Fatalf("matches = %v", matches)
	}
	for i, want := range []string{"password", `a"b`} {
		got, err := unquoteJSONFieldName(value, matches[i][4], matches[i][5])
		if err != nil || got != want {
			t.Fatalf("field %d = %q, %v; want %q", i, got, err, want)
		}
	}
	// Bounds that are not a quoted span are a syntax error, not a panic.
	for _, span := range [][2]int{{0, 3}, {2, len(value)}, {3, 5}} {
		if _, err := unquoteJSONFieldName(value, span[0], span[1]); err == nil {
			t.Fatalf("span %v decoded", span)
		}
	}
}
