package engine

import "testing"

func TestClassifyError(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		message    string
		success    bool
		want       string
	}{
		{name: "success returns empty", statusCode: 200, success: true, want: ""},
		{name: "404 is http_4xx", statusCode: 404, want: ErrorClassHTTP4xx},
		{name: "429 is http_4xx", statusCode: 429, want: ErrorClassHTTP4xx},
		{name: "500 is http_5xx", statusCode: 500, want: ErrorClassHTTP5xx},
		{name: "503 is http_5xx", statusCode: 503, want: ErrorClassHTTP5xx},
		{name: "context deadline exceeded is timeout", statusCode: 0, message: `Get "http://x": context deadline exceeded`, want: ErrorClassTimeout},
		{name: "client timeout phrasing", statusCode: 0, message: "request timed out after 30s", want: ErrorClassTimeout},
		{name: "connection refused is connection", statusCode: 0, message: "dial tcp 10.0.0.1:80: connect: connection refused", want: ErrorClassConnection},
		{name: "EOF is connection", statusCode: 0, message: "unexpected EOF", want: ErrorClassConnection},
		{name: "no such host is connection", statusCode: 0, message: "dial tcp: lookup bad.host: no such host", want: ErrorClassConnection},
		{name: "assertion failure", statusCode: 200, message: "assertion failed: status expected 201 got 200", want: ErrorClassAssertion},
		{name: "validation failure", statusCode: 200, message: "response validation error", want: ErrorClassAssertion},
		{name: "unknown failure is other", statusCode: 0, message: "something weird happened", want: ErrorClassOther},
		{name: "status wins over message", statusCode: 502, message: "context deadline exceeded", want: ErrorClassHTTP5xx},
		{name: "browser resource limit", statusCode: 0, message: "browser resource limit (browser_memory): the browser context was closed to protect the generator", want: ErrorClassResourceLimit},
		{name: "refused browser context", statusCode: 0, message: "browser resource limit: measured browser and generator memory 900 MiB plus a new context would exceed 1000 MiB", want: ErrorClassResourceLimit},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyError(tc.statusCode, tc.message, tc.success); got != tc.want {
				t.Fatalf("ClassifyError(%d, %q, %v) = %q, want %q", tc.statusCode, tc.message, tc.success, got, tc.want)
			}
		})
	}
}
