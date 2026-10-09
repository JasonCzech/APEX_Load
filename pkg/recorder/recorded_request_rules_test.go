package recorder

import (
	"testing"
)

func TestNormalizeRecordedRequestsCollapsesIntegratedAuthHandshakes(t *testing.T) {
	leg := RecordedRequest{Seq: 1, Method: "GET", URL: "https://intranet.test/app", StatusCode: 401,
		Headers:         map[string]string{"Authorization": "NTLM type1"},
		ResponseHeaders: map[string]string{"Www-Authenticate": "NTLM challenge"}}
	retry := RecordedRequest{Seq: 2, Method: "GET", URL: "https://intranet.test/app", StatusCode: 200,
		Headers:         map[string]string{"Authorization": "NTLM type3", "Accept": "text/html"},
		HeadersMulti:    map[string][]string{"Authorization": {"NTLM type3"}},
		RawHeaders:      map[string]string{"authorization": "NTLM type3"},
		RawHeadersMulti: map[string][]string{"Authorization": {"NTLM type3"}}}
	later := RecordedRequest{Seq: 3, Method: "GET", URL: "https://intranet.test/next", StatusCode: 200,
		Headers: map[string]string{"Authorization": "Negotiate kerb"}}
	static := RecordedRequest{Seq: 4, Method: "GET", URL: "https://intranet.test/logo.png", IsStatic: true, StatusCode: 401,
		ResponseHeaders: map[string]string{"WWW-Authenticate": "NTLM"}}
	input := []RecordedRequest{leg, static, retry, later}

	got := normalizeRecordedRequests(input)
	if !got.requests[0].Excluded {
		t.Fatal("the 401 handshake leg must be excluded")
	}
	if got.requests[1].Excluded {
		t.Fatal("static requests are left alone")
	}
	for _, index := range []int{2, 3} {
		req := got.requests[index]
		if recordedIntegratedAuthScheme(req) != "" {
			t.Fatalf("request %d still carries its single-use token: %+v", req.Seq, req)
		}
		if findings := got.findings[req.Seq]; len(findings) != 1 || findings[0].Code != "integrated_auth_requires_configuration" {
			t.Fatalf("request %d findings = %+v", req.Seq, findings)
		}
	}
	if got.requests[2].Headers["Accept"] != "text/html" {
		t.Fatal("other headers must survive")
	}
	if input[2].Headers["Authorization"] != "NTLM type3" || input[0].Excluded {
		t.Fatal("the caller's session must not be modified")
	}
	if len(got.findings[1]) != 0 {
		t.Fatal("an excluded leg carries no findings")
	}
}

func TestNormalizeRecordedRequestsGatesTruncatedBodies(t *testing.T) {
	got := normalizeRecordedRequests([]RecordedRequest{{Seq: 7, Method: "POST", URL: "https://x.test/up", Body: "AAAA", BodyTruncated: true, BodySizeBytes: 900000}})
	if findings := got.findings[7]; len(findings) != 1 || findings[0].Code != "recorded_body_truncated" {
		t.Fatalf("findings = %+v", findings)
	}
}

func TestIsRecordedRedirectStep(t *testing.T) {
	tests := []struct {
		req  RecordedRequest
		want bool
	}{
		{RecordedRequest{StatusCode: 302, ResponseHeaders: map[string]string{"Location": "/next"}}, true},
		{RecordedRequest{StatusCode: 303, ResponseHeadersMulti: map[string][]string{"location": {"/next"}}}, true},
		{RecordedRequest{StatusCode: 302}, false},
		{RecordedRequest{StatusCode: 304, ResponseHeaders: map[string]string{"Location": "/x"}}, false},
		{RecordedRequest{StatusCode: 200, ResponseHeaders: map[string]string{"Location": "/x"}}, false},
	}
	for i, tt := range tests {
		if got := isRecordedRedirectStep(tt.req); got != tt.want {
			t.Errorf("case %d: isRecordedRedirectStep = %v, want %v", i, got, tt.want)
		}
	}
}
