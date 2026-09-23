package http

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	"golang.org/x/net/dns/dnsmessage"
)

func TestMetaRefreshDoesNotReattachRedirectedCredentials(t *testing.T) {
	headers := make(chan http.Header, 1)
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/landing" {
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, `<meta http-equiv="refresh" content="0; url=/final">`)
			return
		}
		headers <- r.Header.Clone()
	}))
	defer foreign.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, foreign.URL+"/landing", http.StatusFound)
	}))
	defer origin.Close()
	h := initCompatibilityHTTP(t, map[string]any{"max_meta_refresh": 2})
	result, err := h.Execute(context.Background(), &protocol.Action{Type: "request", Method: "GET", Target: origin.URL, Headers: map[string]string{"Authorization": "Bearer private", "X-Private": "private"}, Auth: &protocol.AuthConfig{Type: "basic", Username: "private", Password: "private"}})
	if err != nil || !result.Success {
		t.Fatalf("refresh failed: %v %+v", err, result)
	}
	var got http.Header
	select {
	case got = <-headers:
	case <-time.After(time.Second):
		t.Fatal("refresh destination was not reached")
	}
	if got.Get("Authorization") != "" || got.Get("X-Private") != "" {
		t.Fatalf("credentials reattached: header names %v", got)
	}
}

func TestMetaRefreshLatencyAssertionIncludesNavigation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, `<meta http-equiv="refresh" content="0.05; url=/final">`)
		}
	}))
	defer server.Close()
	h := initCompatibilityHTTP(t, map[string]any{"max_meta_refresh": 2})
	r, e := h.Execute(context.Background(), &protocol.Action{Type: "request", Method: "GET", Target: server.URL + "/start", Assertions: []protocol.Assertion{{Type: "response_time_ms", Max: 20}}})
	if e != nil || r.Success || !strings.Contains(r.ErrorMessage, "response time") || r.Duration < 50*time.Millisecond {
		t.Fatalf("full navigation latency was not asserted: %+v %v", r, e)
	}
}

func TestMetaRefreshClearsDefaultEntityHeadersAndRetainsRedaction(t *testing.T) {
	seen := make(chan http.Header, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, `<meta http-equiv="refresh" content="0; url=/final">`)
			return
		}
		seen <- r.Header.Clone()
		w.WriteHeader(500)
	}))
	defer server.Close()
	h := initCompatibilityHTTP(t, map[string]any{"max_meta_refresh": 2, "default_headers": map[string]any{"Content-Type": "application/custom"}})
	r, e := h.Execute(context.Background(), &protocol.Action{Type: "request", Method: "GET", Target: server.URL + "/start", ResolvedDefaultHeaders: true, DefaultHeaders: map[string]string{"Content-Type": "application/custom"}, Headers: map[string]string{"X-Custom-Handle": "sensitive"}, RedactedHeaders: map[string]string{"X-Custom-Handle": "[REDACTED]"}})
	if e != nil || r.Success {
		t.Fatalf("unexpected result: %+v %v", r, e)
	}
	var got http.Header
	select {
	case got = <-seen:
	case <-time.After(time.Second):
		t.Fatal("refresh destination was not reached")
	}
	if got.Get("Content-Type") != "" {
		t.Fatal("GET inherited entity headers")
	}
	if r.Snapshot == nil {
		t.Fatal("expected failed request snapshot")
	}
	// Snapshot must use the caller-provided redaction for custom secret headers.
	if r.Snapshot.RequestHeaders["X-Custom-Handle"] != "[REDACTED]" {
		t.Fatal("custom header redaction lost")
	}
}

type reviewTrackedBody struct {
	io.Reader
	closed bool
}

func (b *reviewTrackedBody) Close() error { b.closed = true; return nil }
func TestPACClosesBodyOnPreflightFailure(t *testing.T) {
	b := &reviewTrackedBody{Reader: strings.NewReader("body")}
	req, _ := http.NewRequest("POST", "http://blocked.invalid", b)
	p := &pacRoundTripper{allowed: []string{"allowed.invalid"}, base: &http.Transport{}}
	if _, err := p.RoundTrip(req); err == nil {
		t.Fatal("expected policy rejection")
	}
	if !b.closed {
		t.Fatal("request body leaked")
	}
}
func TestPACMissingDNSNameCanSelectExplicitDirect(t *testing.T) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		for {
			n, addr, e := conn.ReadFrom(buf)
			if e != nil {
				return
			}
			var q dnsmessage.Message
			if q.Unpack(buf[:n]) != nil {
				continue
			}
			a := dnsmessage.Message{Header: dnsmessage.Header{ID: q.ID, Response: true, RCode: dnsmessage.RCodeNameError}, Questions: q.Questions}
			b, _ := a.Pack()
			conn.WriteTo(b, addr)
		}
	}()
	old := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "udp", conn.LocalAddr().String())
	}}
	defer func() { net.DefaultResolver = old; conn.Close(); <-done }()
	target, _ := url.Parse("http://missing.pac.test")
	result, err := evaluatePAC(context.Background(), `function FindProxyForURL(url,host){return isResolvable(host)?"PROXY proxy.pac.test:8080":"DIRECT"}`, target, []string{"missing.pac.test"})
	if err != nil || result != "DIRECT" {
		t.Fatalf("missing DNS name was not a false helper result: %q %v", result, err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer server.Close()
	transport := &pacRoundTripper{source: `function FindProxyForURL(url,host){return "PROXY missing.pac.test:8080; DIRECT"}`, allowed: []string{"missing.pac.test", "127.0.0.1"}, base: &http.Transport{}}
	req, _ := http.NewRequest("GET", server.URL, nil)
	response, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("missing proxy DNS prevented ordered failover: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("unexpected direct response: %d", response.StatusCode)
	}
}

func TestPACDialRetainsPinnedAddressFallbackAndCancellation(t *testing.T) {
	ips := []net.IPAddr{{IP: net.ParseIP("2001:db8::1")}, {IP: net.ParseIP("192.0.2.1")}}
	calls := []string{}
	local, remote := net.Pipe()
	defer local.Close()
	defer remote.Close()
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		calls = append(calls, address)
		if len(calls) == 1 {
			return nil, &net.OpError{Op: "dial", Err: io.EOF}
		}
		return local, nil
	}
	got, err := dialPACIPs(context.Background(), "tcp", "8080", ips, dial)
	if err != nil || got != local || len(calls) != 2 || calls[0] != "[2001:db8::1]:8080" || calls[1] != "192.0.2.1:8080" {
		t.Fatalf("pinned address fallback lost: %v %v", calls, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = dialPACIPs(ctx, "tcp", "8080", ips, dial); err == nil || len(calls) != 2 {
		t.Fatal("cancelled dial attempted network access")
	}
}

func TestMetaRefreshOriginUsesEffectivePort(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		same bool
	}{
		{"http://example.test/start", "http://EXAMPLE.test:80/end", true},
		{"https://example.test/start", "https://example.test:443/end", true},
		{"http://example.test/start", "http://example.test:8080/end", false},
		{"http://example.test/start", "https://example.test/end", false},
		{"http://example.test/start", "http://other.test/end", false},
	} {
		a, _ := url.Parse(tc.a)
		b, _ := url.Parse(tc.b)
		if got := sameRefreshOrigin(a, b); got != tc.same {
			t.Errorf("origin(%s,%s)=%v want %v", tc.a, tc.b, got, tc.same)
		}
	}
}
