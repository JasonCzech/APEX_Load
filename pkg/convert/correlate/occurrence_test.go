package correlate

import (
	"strings"
	"testing"
)

func TestOccurrenceReplaceable(t *testing.T) {
	for _, tc := range []struct {
		text, literal string
		want          bool
	}{
		{"?token=abc123&x", "abc123", true},
		{"xabc123", "abc123", false},
		{"abc123x", "abc123", false},
		{"%22abc123%22", "abc123", true},
		{`"abc123`, "abc123", true},
		{`\nabc123`, "abc123", true},
		{"https://abc123.example.test/x", "abc123", false},
		{"https://user@host.abc123.test:8443/x", "abc123", false},
		{`https:\/\/abc123.example.test\/x`, "abc123", false},
		{"https%3A%2F%2Fabc123.example.test%2Fx", "abc123", false},
		{"https://example.test/abc123", "abc123", true},
		{"https://example.test?u=abc123", "abc123", true},
		{"next=https://example.test&t=abc123", "abc123", true},
		{"abc123://example.test", "abc123", false},
		{"https://example.test/query/01g-2000", "/query/01g-2000", true},
		{"https%3A%2F%2Fexample.test%2Fquery%2F01g", "%2Fquery%2F01g", true},
		{`https:\/\/example.test\/query`, `\/query`, true},
	} {
		start := strings.Index(tc.text, tc.literal)
		if got := OccurrenceReplaceable(tc.text, start, start+len(tc.literal)); got != tc.want {
			t.Errorf("OccurrenceReplaceable(%q, %q) = %v, want %v", tc.text, tc.literal, got, tc.want)
		}
	}
}

// A Host value is the target host: no correlation rewrites it, though the
// same value elsewhere in the request is rewritten.
func TestApplyVarsToHeadersLeavesTargetHostHeaders(t *testing.T) {
	const token = "tk4815162342ab"
	vars := map[string]string{token: "{{var.corr_token}}"}
	headers := ApplyVarsToHeaders(map[string]string{"Host": token + ".tenant.example.test", ":authority": token + ".tenant.example.test", "X-Token": token}, vars)
	if headers["Host"] != token+".tenant.example.test" || headers[":authority"] != token+".tenant.example.test" || headers["X-Token"] != "{{var.corr_token}}" {
		t.Fatalf("headers = %v, want only X-Token rewritten", headers)
	}
}
