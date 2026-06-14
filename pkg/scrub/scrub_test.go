package scrub

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSensitiveHeader(t *testing.T) {
	tests := []struct {
		name   string
		header string
		policy Policy
		want   bool
	}{
		{name: "authorization", header: "Authorization", want: true},
		{name: "case insensitive", header: "X-API-KEY", want: true},
		{name: "cookie scrubbed by default", header: "Cookie", want: true},
		{name: "set-cookie scrubbed by default", header: "Set-Cookie", want: true},
		{name: "cookie kept when policy allows", header: "Cookie", policy: Policy{KeepCookies: true}, want: false},
		{name: "regular header passes", header: "Content-Type", want: false},
		{name: "extra header honored", header: "X-Tenant-Secret", policy: Policy{ExtraHeaders: []string{"x-tenant-secret"}}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.policy.SensitiveHeader(tt.header); got != tt.want {
				t.Errorf("SensitiveHeader(%q) = %v, want %v", tt.header, got, tt.want)
			}
		})
	}
}

func TestHeaders(t *testing.T) {
	in := map[string]string{
		"Authorization": "Bearer abc123",
		"Cookie":        "session=xyz",
		"Content-Type":  "application/json",
	}
	out := Headers(in, Policy{})
	if out["Authorization"] != Redacted || out["Cookie"] != Redacted {
		t.Errorf("sensitive headers not redacted: %v", out)
	}
	if out["Content-Type"] != "application/json" {
		t.Errorf("regular header changed: %v", out)
	}
	if in["Authorization"] != "Bearer abc123" {
		t.Error("input map must not be mutated")
	}
}

func TestValue(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		policy Policy
		want   string
	}{
		{
			name:  "json token field",
			input: `{"access_token":"eyJhbGc.secret","user":"jason"}`,
			want:  `{"access_token":"` + Redacted + `","user":"jason"}`,
		},
		{
			name:  "form password field",
			input: "username=jason&password=hunter2&kind=login",
			want:  "username=jason&password=" + Redacted + "&kind=login",
		},
		{
			name:  "query token",
			input: "https://x.example/cb?code=ok&token=abc123",
			want:  "https://x.example/cb?code=ok&token=" + Redacted,
		},
		{
			name:   "custom pattern",
			input:  "ssn=123-45-6789 name=j",
			policy: Policy{ExtraPatterns: []Rule{{Pattern: `\d{3}-\d{2}-\d{4}`}}},
			want:   "ssn=" + Redacted + " name=j",
		},
		{
			name:   "custom replacement",
			input:  "card 4111111111111111",
			policy: Policy{ExtraPatterns: []Rule{{Pattern: `4\d{15}`, Replacement: "****"}}},
			want:   "card ****",
		},
		{
			name:   "invalid custom pattern skipped",
			input:  "plain text",
			policy: Policy{ExtraPatterns: []Rule{{Pattern: `([`}}},
			want:   "plain text",
		},
		{name: "empty value", input: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Value(tt.input, tt.policy); got != tt.want {
				t.Errorf("Value(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

const sampleHAR = `{
  "log": {
    "version": "1.2",
    "creator": {"name": "test", "version": "1"},
    "entries": [{
      "startedDateTime": "2026-06-11T10:00:00Z",
      "request": {
        "method": "POST",
        "url": "https://shop.example/login?session=abc123",
        "headers": [
          {"name": "Authorization", "value": "Bearer tok123"},
          {"name": "Content-Type", "value": "application/json"}
        ],
        "cookies": [{"name": "sid", "value": "cookie-secret"}],
        "queryString": [
          {"name": "session", "value": "abc123"},
          {"name": "next", "value": "/cart"}
        ],
        "postData": {
          "mimeType": "application/json",
          "text": "{\"password\":\"hunter2\",\"user\":\"jason\"}"
        }
      },
      "response": {
        "status": 200,
        "headers": [{"name": "Set-Cookie", "value": "sid=new-secret"}],
        "cookies": [{"name": "sid", "value": "new-secret"}],
        "content": {
          "mimeType": "application/json",
          "text": "{\"access_token\":\"jwt-secret\",\"ok\":true}"
        }
      },
      "customField": {"preserved": true}
    }]
  }
}`

func TestHAR(t *testing.T) {
	scrubbed, err := HAR([]byte(sampleHAR), Policy{})
	if err != nil {
		t.Fatalf("HAR: %v", err)
	}
	text := string(scrubbed)

	for _, secret := range []string{"Bearer tok123", "cookie-secret", "hunter2", "jwt-secret", "new-secret", "session=abc123"} {
		if strings.Contains(text, secret) {
			t.Errorf("scrubbed HAR still contains %q", secret)
		}
	}
	for _, kept := range []string{"application/json", `"user\":\"jason`, "/cart", "customField", "preserved"} {
		if !strings.Contains(text, strings.ReplaceAll(kept, `\`, ``)) && !strings.Contains(text, kept) {
			t.Errorf("scrubbed HAR lost expected content %q", kept)
		}
	}

	var doc map[string]any
	if err := json.Unmarshal(scrubbed, &doc); err != nil {
		t.Fatalf("scrubbed HAR is not valid JSON: %v", err)
	}
	if HARSensitivity(scrubbed) != SensitivityScrubbed {
		t.Error("scrubbed HAR must carry the scrubbed sensitivity label")
	}
	if HARSensitivity([]byte(sampleHAR)) != SensitivityRaw {
		t.Error("unscrubbed HAR must classify as raw")
	}
}

func TestHARErrors(t *testing.T) {
	if _, err := HAR([]byte("not json"), Policy{}); err == nil {
		t.Error("expected error for invalid JSON")
	}
	if _, err := HAR([]byte(`{"notlog": {}}`), Policy{}); err == nil {
		t.Error("expected error for missing log section")
	}
}

// TestPolicyCompile asserts custom patterns compile once and that invalid
// patterns are surfaced as errors rather than silently swallowed.
func TestPolicyCompile(t *testing.T) {
	t.Run("valid patterns compile", func(t *testing.T) {
		valid, errs := Policy{ExtraPatterns: []Rule{
			{Pattern: `\d{3}-\d{2}-\d{4}`},
			{Pattern: `4\d{15}`, Replacement: "****"},
		}}.Compile()
		if len(errs) != 0 {
			t.Fatalf("unexpected compile errors: %v", errs)
		}
		if len(valid) != 2 {
			t.Fatalf("expected 2 compiled patterns, got %d", len(valid))
		}
	})

	t.Run("invalid pattern surfaced", func(t *testing.T) {
		valid, errs := Policy{ExtraPatterns: []Rule{
			{Pattern: `\d{3}-\d{2}-\d{4}`},
			{Pattern: `([`}, // unbalanced group → invalid
		}}.Compile()
		if len(errs) != 1 {
			t.Fatalf("expected 1 compile error, got %d: %v", len(errs), errs)
		}
		if !strings.Contains(errs[0].Error(), "([") {
			t.Errorf("error should name the offending pattern, got %q", errs[0].Error())
		}
		if len(valid) != 1 {
			t.Errorf("valid patterns should still be returned, got %d", len(valid))
		}
	})

	t.Run("empty policy compiles clean", func(t *testing.T) {
		valid, errs := Policy{}.Compile()
		if len(valid) != 0 || len(errs) != 0 {
			t.Errorf("zero-value policy should yield no patterns/errors, got %d/%d", len(valid), len(errs))
		}
	})
}

// TestHARWithErrorsSurfacesInvalidPattern proves a typo'd custom pattern is
// reported once through the HAR entry point while built-in redaction still runs.
func TestHARWithErrorsSurfacesInvalidPattern(t *testing.T) {
	policy := Policy{ExtraPatterns: []Rule{{Pattern: `([`}}}
	scrubbed, patternErrs, err := HARWithErrors([]byte(sampleHAR), policy)
	if err != nil {
		t.Fatalf("HARWithErrors: %v", err)
	}
	if len(patternErrs) != 1 {
		t.Fatalf("expected the invalid pattern to be surfaced, got %d errors", len(patternErrs))
	}
	// Built-in protections must still apply despite the bad custom rule.
	if strings.Contains(string(scrubbed), "Bearer tok123") {
		t.Error("built-in redaction must still run when a custom pattern is invalid")
	}

	// HAR (the legacy signature) keeps working and just drops the errors.
	if _, err := HAR([]byte(sampleHAR), policy); err != nil {
		t.Fatalf("HAR with invalid custom pattern should not error: %v", err)
	}
}

// TestCompileOnceManyValues feeds a single policy through many values and HAR
// entries, asserting the custom pattern redacts every match. Compiling per call
// would still be correct, so this guards behavior; the benchmark below guards
// against the per-call recompile regression.
func TestCompileOnceManyValues(t *testing.T) {
	policy := Policy{ExtraPatterns: []Rule{{Pattern: `SSN-\d{4}`, Replacement: "[PII]"}}}

	for i := 0; i < 500; i++ {
		got := Value("user SSN-1234 record", policy)
		if got != "user [PII] record" {
			t.Fatalf("iteration %d: Value = %q, want %q", i, got, "user [PII] record")
		}
	}

	hdrs := map[string]string{"X-Note": "SSN-9999 here", "Content-Type": "text/plain"}
	out := Headers(hdrs, policy)
	if out["X-Note"] != "[PII] here" {
		t.Errorf("Headers custom pattern not applied: %q", out["X-Note"])
	}
	if out["Content-Type"] != "text/plain" {
		t.Errorf("Headers altered a non-matching value: %q", out["Content-Type"])
	}
}

// BenchmarkValueCustomPattern guards the compile-once optimization: Value
// applies the same custom policy to many values without recompiling the regexp
// on every call. A regression that recompiled per call would inflate this.
func BenchmarkValueCustomPattern(b *testing.B) {
	policy := Policy{ExtraPatterns: []Rule{
		{Pattern: `SSN-\d{4}`, Replacement: "[PII]"},
		{Pattern: `\b\d{16}\b`, Replacement: "[CARD]"},
	}}
	const input = "account SSN-1234 paid with 4111111111111111 today"
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Value(input, policy)
	}
}

// BenchmarkHARCustomPattern exercises the hot path where one compiled policy is
// reused across every header/cookie/param/body value in a HAR document.
func BenchmarkHARCustomPattern(b *testing.B) {
	policy := Policy{ExtraPatterns: []Rule{{Pattern: `SSN-\d{4}`, Replacement: "[PII]"}}}
	data := []byte(sampleHAR)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := HAR(data, policy); err != nil {
			b.Fatal(err)
		}
	}
}
