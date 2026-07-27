package credentialpolicy

import "testing"

func TestMultipartDispositionTokenSafety(t *testing.T) {
	t.Parallel()

	sanitized, changed := SanitizeMultipartDispositionToken("field\r\nX-Evil: yes\x7f")
	if !changed {
		t.Fatal("expected control bytes to be reported")
	}
	if want := "field%0D%0AX-Evil: yes%7F"; sanitized != want {
		t.Fatalf("sanitized = %q, want %q", sanitized, want)
	}
	if want := `a\\b\"c`; EscapeMultipartDispositionToken(`a\b"c`) != want {
		t.Fatalf("quoted token mismatch: got %q, want %q", EscapeMultipartDispositionToken(`a\b"c`), want)
	}
}

func TestAppendSourceCredentialValue(t *testing.T) {
	t.Parallel()

	values := AppendSourceCredentialValue(nil, "first")
	values = AppendSourceCredentialValue(values, "")
	values = AppendSourceCredentialValue(values, "first")
	values = AppendSourceCredentialValue(values, "second")
	if len(values) != 2 || values[0] != "first" || values[1] != "second" {
		t.Fatalf("unexpected protected values: %#v", values)
	}
}
