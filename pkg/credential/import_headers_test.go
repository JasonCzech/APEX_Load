package credential

import "testing"

func TestImportCredentialHeaderKeepsRecordedStateAsImported(t *testing.T) {
	for _, test := range []struct {
		name string
		want bool
	}{
		{"Authorization", true},
		{"Proxy-Authorization", true},
		{"X-API-Key", true},
		{"api-key", true},
		{"X-Auth-Token", true},
		{"X-CDF-AUTH-TOKEN", true},
		{"X-Access-Token", true},
		{"Client-Secret", true},
		{"Cookie", false},
		{"Set-Cookie", false},
		{"idpctx-session-id", false},
		{"X-Session-Id", false},
		{"X-Session-Token", false},
		{"X-CSRF-Token", false},
		{"X-XSRF-TOKEN", false},
		{"idp-trace-id", false},
		{"X-Correlation-ID", false},
		{"traceparent", false},
		{"X-Span-Auth-Token", true},
		{"X-Session-Api-Key", true},
		{"X-Trace-Authorization", true},
		{"X-Session-Key", false},
		{"Content-Type", false},
		{"Accept", false},
		{"", false},
	} {
		if got := ImportCredentialHeader(test.name); got != test.want {
			t.Errorf("ImportCredentialHeader(%q) = %t, want %t", test.name, got, test.want)
		}
	}
}
