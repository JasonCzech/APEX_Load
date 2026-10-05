package http

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestReplayStatusPolicyPreservesAssertionsAndBodyFailures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500); io.WriteString(w, "error") }))
	defer server.Close()
	for _, tc := range []struct {
		name       string
		warnings   bool
		assertions []protocol.Assertion
		success    bool
	}{
		{"default error", false, nil, false}, {"warning", true, nil, true},
		{"authored check wins", true, []protocol.Assertion{{Type: "body_contains", Value: "missing"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := initCompatibilityHTTP(t, map[string]any{"http_errors_as_warnings": tc.warnings})
			result, err := h.Execute(context.Background(), &protocol.Action{Type: "http_request", Method: "GET", Target: server.URL, Assertions: tc.assertions})
			if err != nil || result.Success != tc.success {
				t.Fatalf("%+v %v", result, err)
			}
			if tc.warnings && result.ProtocolEvidence["http_status_warning"] != true {
				t.Fatal("missing warning evidence")
			}
		})
	}
}
func TestReplayResourceFailurePolicies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/asset" {
			w.WriteHeader(500)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, `<img src="/asset">`)
	}))
	defer server.Close()
	for _, tc := range []struct{ fail, warn, success bool }{{false, false, true}, {true, false, false}, {true, true, true}} {
		h := initCompatibilityHTTP(t, map[string]any{"http_download_non_html_resources": true, "fail_noncritical_resources": tc.fail, "http_errors_as_warnings": tc.warn})
		result, err := h.Execute(context.Background(), &protocol.Action{Type: "http_request", Method: "GET", Target: server.URL})
		if err != nil || result.Success != tc.success || result.ProtocolEvidence["resource_failures"] != 1 {
			t.Fatalf("%+v %v", result, err)
		}
	}
}
func TestReplayResourceTimeoutPolicyDoesNotHideMainFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			<-r.Context().Done()
			return
		}
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, `<img src="/slow">`)
	}))
	defer server.Close()
	for _, tc := range []struct {
		path          string
		warn, success bool
	}{{"/", true, true}, {"/", false, false}, {"/slow", true, false}} {
		h := initCompatibilityHTTP(t, map[string]any{"http_download_non_html_resources": true, "page_timeout_ms": 100, "resource_timeout_as_warning": tc.warn})
		result, err := h.Execute(context.Background(), &protocol.Action{Type: "http_request", Method: "GET", Target: server.URL + tc.path})
		if err != nil || result.Success != tc.success {
			t.Fatalf("path=%s warn=%v result=%+v %v", tc.path, tc.warn, result, err)
		}
	}
	h := initCompatibilityHTTP(t, map[string]any{"http_download_non_html_resources": true, "page_timeout_ms": 1000, "resource_timeout_as_warning": true})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	result, _ := h.Execute(ctx, &protocol.Action{Type: "http_request", Method: "GET", Target: server.URL})
	if result.Success {
		t.Fatal("caller deadline became warning")
	}
}
func TestLegacyChecksOnlySuppressMarkedChecks(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); io.WriteString(w, "body") }))
	defer server.Close()
	h := initCompatibilityHTTP(t, map[string]any{"legacy_html_checks": false})
	action := &protocol.Action{Type: "http_request", Method: "GET", Target: server.URL, Assertions: []protocol.Assertion{{Type: "body_contains", Value: "missing", LegacyHTMLCheck: true}}}
	result, _ := h.Execute(context.Background(), action)
	if !result.Success {
		t.Fatalf("legacy check wasn't suppressed: %+v", result)
	}
	action.Assertions[0].LegacyHTMLCheck = false
	result, _ = h.Execute(context.Background(), action)
	if result.Success {
		t.Fatal("authored check suppressed")
	}
	action.Params = map[string]any{"legacy_html_check": true}
	result, _ = h.Execute(context.Background(), action)
	if !result.Success || hits.Load() != 2 {
		t.Fatal("disabled image check issued a request")
	}
}
func TestCachedDNSDialerExpiryIsolationAndFamily(t *testing.T) {
	now := time.Unix(0, 0)
	calls := 0
	var dialed []string
	lookup := func(context.Context, string) ([]net.IPAddr, error) {
		calls++
		return []net.IPAddr{{IP: net.ParseIP("::1")}, {IP: net.ParseIP("127.0.0.1")}}, nil
	}
	base := func(_ context.Context, network, address string) (net.Conn, error) {
		dialed = append(dialed, address)
		return nil, errors.New("test dial")
	}
	dial := newCachedDNSDialer(base, lookup, func() time.Time { return now })
	for range 2 {
		dial(context.Background(), "tcp4", "example.test:80")
	}
	if calls != 1 || len(dialed) != 2 || dialed[0] != "127.0.0.1:80" {
		t.Fatalf("calls=%d dialed=%v", calls, dialed)
	}
	now = now.Add(61 * time.Second)
	dial(context.Background(), "tcp4", "example.test:80")
	if calls != 2 {
		t.Fatal("expired cache reused")
	}
	other := newCachedDNSDialer(base, lookup, func() time.Time { return now })
	other(context.Background(), "tcp4", "example.test:80")
	if calls != 3 {
		t.Fatal("cache shared across VUsers")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := dial(ctx, "tcp4", "example.test:80")
	if !errors.Is(err, context.Canceled) || calls != 3 {
		t.Fatal("canceled lookup executed")
	}
	dial(context.Background(), "tcp4", "127.0.0.2:80")
	if calls != 3 {
		t.Fatal("literal looked up")
	}
}
func TestDigestDelayIsCancellableAndRequiresChallenge(t *testing.T) {
	req, _ := http.NewRequest("GET", "http://example.test", nil)
	auth := &protocol.AuthConfig{Type: "digest", Username: "user", Password: "secret"}
	response := func() *http.Response {
		return &http.Response{StatusCode: 401, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{"Www-Authenticate": {`Digest realm="test", nonce="nonce", qop="auth"`}}}
	}
	ctx, cancel := context.WithCancel(context.Background())
	req = req.WithContext(ctx)
	cancel()
	_, _, _, err := retryWithDigestAuthDelay(&http.Client{}, req, response(), auth, nil, time.Hour)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("delay not canceled: %v", err)
	}
	resp := response()
	resp.StatusCode = 403
	_, _, retried, err := retryWithDigestAuthDelay(&http.Client{}, req, resp, auth, nil, time.Hour)
	if err != nil || retried {
		t.Fatal("non-challenge invoked delay")
	}
}
func TestOutcomePolicyValidation(t *testing.T) {
	for _, config := range []map[string]any{{"dns_cache": "true"}, {"challenge_delay_ms": -1}, {"challenge_delay_ms": 3600001}, {"resource_timeout_as_warning": 1}} {
		h := &HTTP{}
		if err := h.Init(context.Background(), config); err == nil {
			h.Teardown(context.Background())
			t.Fatalf("accepted %v", config)
		}
	}
}

func TestDigestDelayRunsBeforeAuthenticatedRetry(t *testing.T) {
	req, _ := http.NewRequest("GET", "http://example.test", nil)
	resp := &http.Response{StatusCode: 401, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{"Www-Authenticate": {`Digest realm="test", nonce="nonce", qop="auth"`}}}
	started := time.Now()
	client := &http.Client{Transport: digestRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if time.Since(started) < 20*time.Millisecond || !strings.HasPrefix(r.Header.Get("Authorization"), "Digest ") {
			t.Error("retry happened before configured delay")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok")), Header: http.Header{}}, nil
	})}
	_, result, retried, err := retryWithDigestAuthDelay(client, req, resp, &protocol.AuthConfig{Type: "digest", Username: "user", Password: "secret"}, nil, 20*time.Millisecond)
	if err != nil || !retried || result.StatusCode != 200 {
		t.Fatalf("%+v %v", result, err)
	}
	result.Body.Close()
}

func TestHTTPWarningPolicyDoesNotHideBodyReadFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(500)
		io.WriteString(w, "short")
	}))
	defer server.Close()
	h := initCompatibilityHTTP(t, map[string]any{"http_errors_as_warnings": true})
	result, err := h.Execute(context.Background(), &protocol.Action{Type: "http_request", Method: "GET", Target: server.URL})
	if err != nil || result.Success || result.ErrorMessage == "" {
		t.Fatalf("body failure hidden: %+v %v", result, err)
	}
}

func TestUnresolvedVariablePlaceholderIsSentWithWarning(t *testing.T) {
	var received atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received.Store(string(body))
		io.WriteString(w, "ok")
	}))
	defer server.Close()
	h := initCompatibilityHTTP(t, nil)
	result, err := h.Execute(context.Background(), &protocol.Action{Type: "http_request", Method: "POST", Target: server.URL + "?store={{var.store_ref}}",
		Body: []byte(`{"store":"{{var.store_ref}}","other":"{{ var.missing_two }}"}`)})
	if err != nil || !result.Success {
		t.Fatalf("%+v %v", result, err)
	}
	if got, _ := received.Load().(string); !strings.Contains(got, "{{var.store_ref}}") {
		t.Fatalf("placeholder not sent literally: %q", got)
	}
	names, _ := result.ProtocolEvidence["unresolved_variables"].([]string)
	if result.ProtocolEvidence["unresolved_variable_sent"] != true || len(names) != 2 || names[0] != "store_ref" || names[1] != "missing_two" {
		t.Fatalf("evidence = %+v, warnings = %v", result.ProtocolEvidence, result.WarningCodes)
	}
	clean, err := h.Execute(context.Background(), &protocol.Action{Type: "http_request", Method: "GET", Target: server.URL})
	if err != nil || clean.ProtocolEvidence["unresolved_variable_sent"] != nil {
		t.Fatalf("clean request warned: %+v %v", clean.ProtocolEvidence, err)
	}
}
