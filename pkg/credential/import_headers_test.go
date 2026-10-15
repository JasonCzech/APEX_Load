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
		{"Ocp-Apim-Subscription-Key", true},
		{"x-functions-key", true},
		{"X-Apigee-Key", true},
		{"X-Auth", true},
		{"PRIVATE-TOKEN", true},
		{"X-SFDC-Session", false},
		{"Content-Type", false},
		{"Accept", false},
		{"", false},
	} {
		if got := ImportCredentialHeader(test.name); got != test.want {
			t.Errorf("ImportCredentialHeader(%q) = %t, want %t", test.name, got, test.want)
		}
	}
}

func TestRecordedStateHeaderIsTheStateImportsKeep(t *testing.T) {
	for _, test := range []struct {
		name string
		want bool
	}{
		{"Cookie", true},
		{" set-cookie ", true},
		{"idpctx-session-id", true},
		{"X-Session-Token", true},
		{"X-Session-Key", true},
		{"X-CSRF-Token", true},
		{"X-XSRF-TOKEN", true},
		{"traceparent", true},
		{"X-Amzn-Trace-Id", true},
		{"X-B3-SpanId", true},
		{"X-Correlation-ID", true},
		{"Idempotency-Key", true},
		{"X-Idempotency-Key", true},
		{"Authorization", false},
		{"X-Auth-Token", false},
		{"X-Span-Auth-Token", false},
		{"X-Session-Api-Key", false},
		{"X-Trace-Authorization", false},
		{"X-API-Key", false},
		{"Content-Type", false},
		{"", false},
	} {
		if got := RecordedStateHeader(test.name); got != test.want {
			t.Errorf("RecordedStateHeader(%q) = %t, want %t", test.name, got, test.want)
		}
		if RecordedStateHeader(test.name) && ImportCredentialHeader(test.name) {
			t.Errorf("%q is both recorded state and an import credential", test.name)
		}
	}
}
