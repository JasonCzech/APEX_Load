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
