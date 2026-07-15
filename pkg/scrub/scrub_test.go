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

func TestSensitiveNameNormalizedVocabulary(t *testing.T) {
	for _, name := range []string{
		"private_key", "client-secret", "client%5Fsecret", "sessionId",
		"csrf_token", "XSRF-TOKEN", "credentials[connection_string]",
		"AWS_ACCESS_KEY_ID", "SharedAccessKey", "shared_access_signature",
		"jms_settlement_token", "lock-token", "receipt_handle", "sid",
	} {
		if !SensitiveName(name) {
			t.Errorf("SensitiveName(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"author", "authority", "client_id", "correlation_id", "soap_operation", "status"} {
		if SensitiveName(name) {
			t.Errorf("SensitiveName(%q) = true, want false", name)
		}
	}
}

func TestVocabularyAppliesAcrossStructuredEncodings(t *testing.T) {
	secret := "short"
	values := []string{
		`{"client%5Fsecret":"short","safe":"ok"}`,
		"client%255Fsecret=short&safe=ok",
		`<root private_key="short"><csrf_token>short</csrf_token></root>`,
		"--b\r\nContent-Disposition: form-data; name=\"session%5Fid\"\r\n\r\nshort\r\n--b--",
	}
	for _, input := range values {
		if got := Value(input, Policy{}); strings.Contains(got, secret) || !strings.Contains(got, Redacted) {
			t.Errorf("Value(%q) = %q, want short secret redacted", input, got)
		}
	}
	if got := ValueURL("https://user:short@example.test/cb?client%5Fsecret=short", Policy{}); strings.Contains(got, "user:short") || strings.Contains(got, "=short") {
		t.Fatalf("ValueURL leaked userinfo/query secret: %q", got)
	}
	mapValue := StringMap(map[string]string{"jms_settlement_token": "x", "soap_operation": "Create"}, Policy{})
	if mapValue["jms_settlement_token"] != Redacted || mapValue["soap_operation"] != "Create" {
		t.Fatalf("StringMap = %#v", mapValue)
	}
	structured, _ := Structured(map[string]any{
		"credentials": map[string]any{"private_key": "tiny"},
		"events":      []any{map[string]string{"xsrf": "x", "status": "ok"}},
	}, Policy{}).(map[string]any)
	encoded, err := json.Marshal(structured)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "tiny") || strings.Contains(string(encoded), `"xsrf":"x"`) {
		t.Fatalf("Structured leaked named secret: %s", encoded)
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
			name:  "json array and object secret fields",
			input: `{"clientSecret":["alpha","beta"],"nested":{"apiKey":{"raw":"key-123"}},"csrf":"csrf-secret","largeId":9007199254740993}`,
			want:  `{"clientSecret":["` + Redacted + `","` + Redacted + `"],"csrf":"` + Redacted + `","largeId":9007199254740993,"nested":{"apiKey":{"raw":"` + Redacted + `"}}}`,
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
			name:  "xml token element and attribute",
			input: `<soap><auth token="abc123" data-secret="tracking"><password>hunter2</password></auth></soap>`,
			want:  `<soap><auth token="` + Redacted + `" data-secret="tracking"><password>` + Redacted + `</password></auth></soap>`,
		},
		{
			name:  "multipart password part",
			input: "--b\r\nContent-Disposition: form-data; name=\"password\"\r\n\r\nhunter2\r\n--b--",
			want:  "--b\r\nContent-Disposition: form-data; name=\"password\"\r\n\r\n" + Redacted + "\r\n--b--",
		},
		{
			name:  "bare jwt",
			input: "Authorization failed for eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.signature",
			want:  "Authorization failed for " + Redacted,
		},
		{
			name:  "high entropy bare token",
			input: "token AbCdEfGh1234567890ijklMN should not persist",
			want:  "token " + Redacted + " should not persist",
		},
		{
			// A plain multi-label hostname structurally matches the JWT pattern
			// (three 8+-char dotted labels) but is not a secret: it must survive.
			name:  "multi-label hostname preserved",
			input: "connecting to dashboard.internal.megacorp now",
			want:  "connecting to dashboard.internal.megacorp now",
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

func TestValueScrubsCredentialsInsideEmbeddedURLs(t *testing.T) {
	secret := "short-password"
	input := `request failed for https://user:` + secret + `@example.test/cb?api_key=` + secret + `; retrying`
	got := Value(input, Policy{})
	if strings.Contains(got, secret) || strings.Contains(got, "user:"+secret) {
		t.Fatalf("Value leaked embedded URL credentials: %q", got)
	}
	if !strings.Contains(got, "request failed for") || !strings.Contains(got, "retrying") {
		t.Fatalf("Value discarded surrounding diagnostic text: %q", got)
	}
}

// TestHARPreservesURLHostAndHashedPaths locks in that scrubbing a HAR does not
// corrupt request URLs: multi-label hosts and content-hashed asset paths (both
// JWT/high-entropy look-alikes) must survive, while sensitive query params are
// still redacted. Regression guard for the blanket-redaction-over-URLs bug.
func TestHARPreservesURLHostAndHashedPaths(t *testing.T) {
	input := []byte(`{
	  "log": {
	    "version": "1.2",
	    "creator": {"name": "test", "version": "1"},
	    "entries": [{
	      "request": {"method": "GET", "url": "https://dashboard.internal.megacorp/assets/app.a1b2c3d4e5f6a7b8c9d0.js?token=secret-token-value-1234", "headers": []},
	      "response": {"status": 200, "headers": []}
	    }]
	  }
	}`)
	scrubbed, err := HAR(input, Policy{})
	if err != nil {
		t.Fatalf("HAR: %v", err)
	}
	text := string(scrubbed)
	for _, keep := range []string{"dashboard.internal.megacorp", "app.a1b2c3d4e5f6a7b8c9d0.js"} {
		if !strings.Contains(text, keep) {
			t.Fatalf("scrubbed HAR dropped non-secret URL part %q: %s", keep, text)
		}
	}
	if strings.Contains(text, "secret-token-value-1234") {
		t.Fatalf("scrubbed HAR leaked sensitive query token: %s", text)
	}
}

// TestValueURLRedactsOpaqueQueryAndFragmentSecrets locks in that opaque
// credentials in a URL's query and fragment — signed-URL signatures, OAuth
// codes/tokens under param names outside the fixed sensitive list — are redacted,
// while the scheme/host/path (including content-hashed asset filenames) survive.
func TestValueURLRedactsOpaqueQueryAndFragmentSecrets(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		keep   []string
		redact []string
	}{
		{
			name: "azure sas signature keeps param name",
			in:   "https://storage.example/blob/app.a1b2c3d4e5f6a7b8.js?sig=AbCdEf0123456789GhIjKlMn0Z&se=2026",
			// The param NAME must survive (not be swallowed into the redaction).
			keep:   []string{"storage.example", "app.a1b2c3d4e5f6a7b8.js", "se=2026", "sig="},
			redact: []string{"AbCdEf0123456789GhIjKlMn0Z"},
		},
		{
			name:   "oauth implicit fragment token",
			in:     "https://app.example/callback#access_token=AbCdEf0123456789GhIjKlMn0Z&state=xyz",
			keep:   []string{"app.example", "/callback", "state=xyz"},
			redact: []string{"AbCdEf0123456789GhIjKlMn0Z"},
		},
		{
			name: "percent-encoded signature is decoded and caught",
			// A base64 signature whose +,/ are percent-encoded (%2B/%2F) must still
			// be detected via the decoded form, and the raw encoded run redacted.
			in:     "https://x.example/dl?Signature=YWJjZGVm%2Bghijkl%2Fmnop0123456789QRS",
			keep:   []string{"x.example", "Signature="},
			redact: []string{"YWJjZGVm", "mnop0123456789QRS"},
		},
		{
			name:   "low-entropy values preserved",
			in:     "https://x.example/v2/orders?page=2&size=100&code=US&sort=name",
			keep:   []string{"page=2", "size=100", "code=US", "sort=name", "/v2/orders"},
			redact: []string{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ValueURL(tc.in, Policy{})
			for _, keep := range tc.keep {
				if !strings.Contains(got, keep) {
					t.Errorf("ValueURL dropped non-secret part %q: %s", keep, got)
				}
			}
			for _, secret := range tc.redact {
				if strings.Contains(got, secret) {
					t.Errorf("ValueURL leaked opaque secret %q: %s", secret, got)
				}
			}
		})
	}
}

func TestHARScrubsWebSocketMessages(t *testing.T) {
	input := []byte(`{
	  "log": {
	    "version": "1.2",
	    "creator": {"name": "test", "version": "1"},
	    "entries": [{
	      "request": {"method": "GET", "url": "wss://example.test/ws", "headers": []},
	      "response": {"status": 101, "headers": []},
	      "_webSocketMessages": [
	        {"type": "send", "data": "{\"access_token\":\"ws-secret-token\"}"},
	        {"type": "receive", "data": "Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.signature"}
	      ]
	    }]
	  }
	}`)
	scrubbed, err := HAR(input, Policy{})
	if err != nil {
		t.Fatalf("HAR: %v", err)
	}
	text := string(scrubbed)
	for _, secret := range []string{"ws-secret-token", "eyJhbGciOiJIUzI1NiJ9"} {
		if strings.Contains(text, secret) {
			t.Fatalf("websocket HAR payload leaked %q in %s", secret, text)
		}
	}
	if !strings.Contains(text, Redacted) {
		t.Fatalf("scrubbed HAR = %s, want redaction marker", text)
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

func TestHARSingleEntryRootObject(t *testing.T) {
	input := []byte(`{
	  "request": {
	    "method": "POST",
	    "url": "https://shop.example/login?token=abc123",
	    "headers": [{"name": "Authorization", "value": "Bearer secret-token"}],
	    "postData": {"text": "{\"password\":\"hunter2\"}"}
	  },
	  "response": {
	    "status": 200,
	    "headers": [{"name": "Set-Cookie", "value": "sid=cookie-secret"}],
	    "content": {"text": "{\"access_token\":\"jwt-secret\"}"}
	  }
	}`)
	scrubbed, err := HAR(input, Policy{})
	if err != nil {
		t.Fatalf("HAR single entry: %v", err)
	}
	text := string(scrubbed)
	for _, secret := range []string{"secret-token", "hunter2", "cookie-secret", "jwt-secret", "token=abc123"} {
		if strings.Contains(text, secret) {
			t.Fatalf("single-entry scrub leaked %q in %s", secret, text)
		}
	}
	if HARSensitivity(scrubbed) != SensitivityScrubbed {
		t.Fatalf("single-entry sensitivity = %q, want scrubbed", HARSensitivity(scrubbed))
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

// TestValueRedactsAuthorizationJSONField locks in that an "authorization" JSON
// body field (a Bearer token, never a correlation key) is redacted.
func TestValueRedactsAuthorizationJSONField(t *testing.T) {
	got := Value(`{"authorization":"Bearer eyJ.abc.def"}`, Policy{})
	if !strings.Contains(got, Redacted) || strings.Contains(got, "eyJ.abc.def") {
		t.Fatalf("expected authorization value redacted, got %s", got)
	}
}

// TestValuePreservesCorrelationTargets locks in that JSON body fields the
// correlation engine captures-and-replays (session/sessionid/csrf/xsrf) are NOT
// redacted — the HAR/recorder pipelines scrub before correlation, so redacting
// these would break replay parameterization (see pkg/convert/har TestConvertHAR).
// TestValueRedactsSessionAndCSRFTokens locks in that live session/CSRF tokens in
// JSON bodies are redacted. These were previously preserved for correlation, but
// correlation-time replay reconstruction now runs against the raw server-side
// recording sidecar and raw import content (not this scrubbed view), so leaving
// them un-redacted only leaked live credentials into scrubbed artifacts.
func TestValueRedactsSessionAndCSRFTokens(t *testing.T) {
	cases := []struct {
		in     string
		secret string
	}{
		{`{"sessionId":"abc12345"}`, "abc12345"},
		{`{"session":"sid-123"}`, "sid-123"},
		{`{"csrf":"csrf-tok"}`, "csrf-tok"},
		{`{"csrf_token":"csrf-tok2"}`, "csrf-tok2"},
		{`{"xsrf":"xsrf-tok"}`, "xsrf-tok"},
	}
	for _, tc := range cases {
		got := Value(tc.in, Policy{})
		if strings.Contains(got, tc.secret) {
			t.Fatalf("session/csrf token %q leaked into scrubbed body: %s", tc.secret, got)
		}
		if !strings.Contains(got, Redacted) {
			t.Fatalf("expected redaction marker for %q, got: %s", tc.in, got)
		}
	}
}
