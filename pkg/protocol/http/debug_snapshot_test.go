package http

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestDebugSnapshotsPreserveHTTPExchangeWithoutChangingDefaultDisclosure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Set-Cookie", "session=response-secret")
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "response-secret")
	}))
	defer server.Close()
	for _, debug := range []bool{false, true} {
		t.Run(map[bool]string{false: "default", true: "debug"}[debug], func(t *testing.T) {
			h := initHTTP(t, map[string]any{protocol.ReplayUnredactedDebugConfigKey: debug})
			result, err := h.Execute(context.Background(), &protocol.Action{
				Type: "POST", Method: "POST", Target: server.URL + "/?token=request-secret",
				Headers: map[string]string{"Authorization": "Bearer request-secret", "Host": "request-secret.example"}, Body: []byte("token=request-secret"),
				RedactedTarget: server.URL + "/?token=[REDACTED]", RedactedHeaders: map[string]string{"Authorization": "Bearer [REDACTED]", "Host": "[REDACTED].example"},
				RedactedBody: []byte("token=[REDACTED]"), CaptureMessageDetail: true,
			})
			if err != nil || result.Snapshot == nil {
				t.Fatalf("execute: result=%+v error=%v", result, err)
			}
			s := result.Snapshot
			for _, value := range []string{s.RequestURL, s.RequestHeaders["Authorization"], s.RequestHeaders["Host"], s.RequestBody} {
				if strings.Contains(value, "request-secret") != debug {
					t.Fatalf("request disclosure mismatch: %+v", s)
				}
			}
			if strings.Contains(s.ResponseHeaders["Set-Cookie"], "response-secret") != debug {
				t.Fatalf("response header disclosure mismatch: %+v", s)
			}
		})
	}
}

func TestDebugHTTPFailuresPreserveDetails(t *testing.T) {
	const secret = "error-detail-canary"
	transportErr := &url.Error{Op: "Get", URL: "https://example.test/?token=" + secret, Err: errors.New("dial failed")}
	for _, debug := range []bool{false, true} {
		for _, version := range []string{"", VersionHTTP2, VersionHTTP3} {
			h := &HTTP{unredactedDebugSnapshots: debug, httpVersion: version}
			if got := h.transportErrorMessage(transportErr); strings.Contains(got, secret) != debug {
				t.Fatalf("debug=%v version=%q transport error=%q", debug, version, got)
			}
		}
		h := initHTTP(t, map[string]any{protocol.ReplayUnredactedDebugConfigKey: debug})
		result, err := h.Execute(context.Background(), &protocol.Action{Method: "GET", Target: "https://example.test/?token=" + secret + "\x7f", RedactedTarget: "https://example.test/[REDACTED]"})
		if err != nil || result == nil {
			t.Fatalf("debug=%v execute=%+v err=%v", debug, result, err)
		}
		if strings.Contains(result.ErrorMessage, secret) != debug {
			t.Fatalf("debug=%v invalid-target error=%q", debug, result.ErrorMessage)
		}
	}
}

func TestDebugResourceCapturePreservesValuesWithinBounds(t *testing.T) {
	for _, debug := range []bool{false, true} {
		c := newResourceCapture(1 << 20)
		c.unredacted = debug
		headers := http.Header{"Authorization": []string{"Bearer request-secret"}, "Content-Type": []string{"application/json"}}
		c.record(protocol.NetworkEntry{URL: "https://example.test/?token=request-secret", StatusCode: 200}, headers, headers, []byte(`{"token":"request-secret"}`), false, true)
		s := &protocol.ErrorSnapshot{}
		c.attach(s)
		c.lease.release()
		if len(s.Resources) != 1 {
			t.Fatalf("resources = %+v", s.Resources)
		}
		r := s.Resources[0]
		for _, value := range []string{r.RequestURL, r.RequestHeaders["Authorization"], r.ResponseBody} {
			if strings.Contains(value, "request-secret") != debug {
				t.Fatalf("debug=%v, resource=%+v", debug, r)
			}
		}
	}
}
