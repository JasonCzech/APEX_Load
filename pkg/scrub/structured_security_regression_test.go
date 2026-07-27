package scrub

import (
	"strings"
	"testing"
)

func TestMalformedXMLSensitiveAttributeBeforeLaterTagFailsClosed(t *testing.T) {
	const input = `<root><x password="short-secret></root>`
	for name, scrubber := range map[string]func(string) string{
		"general":    func(value string) string { return Value(value, Policy{}) },
		"credential": RedactCredentialBody,
	} {
		t.Run(name, func(t *testing.T) {
			got := scrubber(input)
			if strings.Contains(got, "short-secret") || !strings.Contains(got, Redacted) {
				t.Fatalf("malformed XML credential leaked: %q", got)
			}
			if !strings.Contains(got, "</root>") {
				t.Fatalf("later recognized closing tag was corrupted: %q", got)
			}
		})
	}

	const safe = `<root><x note="short-safe></root>`
	if got := Value(safe, Policy{}); got != safe {
		t.Fatalf("nonsensitive malformed XML changed:\n got: %q\nwant: %q", got, safe)
	}
}

func TestMalformedXMLQuotedSensitiveAttributeSuffixFailsClosed(t *testing.T) {
	const input = `<root password="first"second-secret></root>`
	for name, scrubber := range map[string]func(string) string{
		"general":    func(value string) string { return Value(value, Policy{}) },
		"credential": RedactCredentialBody,
	} {
		t.Run(name, func(t *testing.T) {
			got := scrubber(input)
			for _, leaked := range []string{"first", "second-secret"} {
				if strings.Contains(got, leaked) {
					t.Fatalf("malformed quoted XML attribute leaked %q: %q", leaked, got)
				}
			}
			if !strings.Contains(got, Redacted) || !strings.Contains(got, "</root>") {
				t.Fatalf("malformed quoted XML attribute was not bounded and redacted: %q", got)
			}
		})
	}

	const safe = `<root note="first"second-safe></root>`
	for name, scrubber := range map[string]func(string) string{
		"general":    func(value string) string { return Value(value, Policy{}) },
		"credential": RedactCredentialBody,
	} {
		t.Run("safe_"+name, func(t *testing.T) {
			if got := scrubber(safe); got != safe {
				t.Fatalf("nonsensitive malformed XML changed:\n got: %q\nwant: %q", got, safe)
			}
		})
	}
}

func TestMalformedJSONQuotedSensitiveSuffixFailsClosed(t *testing.T) {
	const input = `{"password":"short"secret","safe":"ok"}`
	const want = `{"password":"` + Redacted + `","safe":"ok"}`
	for name, scrubber := range map[string]func(string) string{
		"general":    func(value string) string { return Value(value, Policy{}) },
		"credential": RedactCredentialBody,
	} {
		t.Run(name, func(t *testing.T) {
			if got := scrubber(input); got != want {
				t.Fatalf("scrubbed malformed JSON = %q, want %q", got, want)
			}
		})
	}

	const safe = `{"note":"short"suffix","safe":"ok"}`
	if got := Value(safe, Policy{}); got != safe {
		t.Fatalf("nonsensitive malformed JSON changed:\n got: %q\nwant: %q", got, safe)
	}
}

func TestMultipartDuplicateDispositionDeclarationsFailClosed(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name: "duplicate headers",
			input: "--b\r\n" +
				"Content-Disposition: form-data; name=\"note\"\r\n" +
				"Content-Disposition: form-data; name=\"password\"\r\n" +
				"X-Part-Trace: keep\r\n\r\n" +
				"short-secret\r\n--b--\r\n",
		},
		{
			name: "duplicate name parameters",
			input: "--b\r\n" +
				"Content-Disposition: form-data; name=\"note\"; name=\"password\"\r\n" +
				"X-Part-Trace: keep\r\n\r\n" +
				"short-secret\r\n--b--\r\n",
		},
	}
	for _, test := range tests {
		for scrubberName, scrubber := range map[string]func(string) string{
			"general":    func(value string) string { return Value(value, Policy{}) },
			"credential": RedactCredentialBody,
		} {
			t.Run(test.name+"_"+scrubberName, func(t *testing.T) {
				got := scrubber(test.input)
				if strings.Contains(got, "short-secret") || !strings.Contains(got, Redacted) {
					t.Fatalf("ambiguous multipart credential leaked: %q", got)
				}
				for _, evidence := range []string{
					"Content-Disposition: form-data; name=\"note\"",
					"name=\"password\"",
					"X-Part-Trace: keep",
					"\r\n--b--",
				} {
					if !strings.Contains(got, evidence) {
						t.Fatalf("multipart scrub lost bounded evidence %q: %q", evidence, got)
					}
				}
			})
		}
	}

	const safe = "--b\r\n" +
		"Content-Disposition: form-data; name=\"note\"\r\n" +
		"Content-Disposition: form-data; name=\"description\"\r\n\r\n" +
		"short-safe\r\n--b--\r\n"
	for name, scrubber := range map[string]func(string) string{
		"general":    func(value string) string { return Value(value, Policy{}) },
		"credential": RedactCredentialBody,
	} {
		t.Run("safe_"+name, func(t *testing.T) {
			if got := scrubber(safe); got != safe {
				t.Fatalf("safe duplicate multipart declarations changed:\n got: %q\nwant: %q", got, safe)
			}
		})
	}
}

func TestFoldedMultipartContentDispositionIsUnfoldedBeforeClassification(t *testing.T) {
	const input = "--b\r\n" +
		"Content-Disposition: form-data;\r\n" +
		" name=\"password\"\r\n" +
		"X-Part-Trace: keep\r\n\r\n" +
		"short-secret\r\n--b--\r\n"
	for name, scrubber := range map[string]func(string) string{
		"general":    func(value string) string { return Value(value, Policy{}) },
		"credential": RedactCredentialBody,
	} {
		t.Run(name, func(t *testing.T) {
			got := scrubber(input)
			if strings.Contains(got, "short-secret") || !strings.Contains(got, Redacted) {
				t.Fatalf("folded multipart credential leaked: %q", got)
			}
			for _, evidence := range []string{
				"Content-Disposition: form-data;\r\n name=\"password\"",
				"X-Part-Trace: keep",
				"\r\n--b--",
			} {
				if !strings.Contains(got, evidence) {
					t.Fatalf("folded multipart scrub lost %q: %q", evidence, got)
				}
			}
		})
	}
}
