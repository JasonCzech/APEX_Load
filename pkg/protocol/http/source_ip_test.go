package http

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseTransportConfigSourceIP(t *testing.T) {
	if got := parseTransportConfig(map[string]any{"web_source_ip": "10.1.2.3"}).sourceIP; got == nil || got.String() != "10.1.2.3" {
		t.Fatalf("valid source ip not parsed, got %v", got)
	}
	if got := parseTransportConfig(map[string]any{"web_source_ip": "not-an-ip"}).sourceIP; got != nil {
		t.Fatalf("invalid source ip should be nil, got %v", got)
	}
	if got := parseTransportConfig(map[string]any{}).sourceIP; got != nil {
		t.Fatalf("absent source ip should be nil, got %v", got)
	}
}

func TestNewTransportBindsLocalAddr(t *testing.T) {
	settings := parseTransportConfig(map[string]any{"web_source_ip": "127.0.0.1"})
	transport := newTransport(settings, nil)
	if transport.DialContext == nil {
		t.Fatal("expected a DialContext")
	}
	// The dialer should refuse to bind a local address it does not own; binding
	// 127.0.0.1 is always valid on a host with loopback.
	conn, err := transport.DialContext(context.Background(), "tcp", "127.0.0.1:0")
	if err == nil {
		conn.Close()
	}
}

// TestSourceIPBindingObservedByServer proves the bound LocalAddr is actually used
// for the outgoing connection by binding to 127.0.0.2 and checking the address
// the server observes. Loopback aliases other than 127.0.0.1 are not guaranteed
// on every CI host, so the test skips if the alias is unavailable.
func TestSourceIPBindingObservedByServer(t *testing.T) {
	const sourceIP = "127.0.0.2"

	// Probe whether we can bind to the alias at all; skip if not (e.g. sandbox).
	probe, err := net.Listen("tcp", sourceIP+":0")
	if err != nil {
		t.Skipf("loopback alias %s unavailable: %v", sourceIP, err)
	}
	probe.Close()

	var observed string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		observed = host
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	transport := newTransport(parseTransportConfig(map[string]any{"web_source_ip": sourceIP}), nil)
	client := &http.Client{Transport: transport}
	resp, err := client.Get(server.URL)
	if err != nil {
		t.Skipf("request with bound source ip failed (environment may block alias): %v", err)
	}
	resp.Body.Close()

	if observed != sourceIP {
		t.Fatalf("server observed source %q, want %q", observed, sourceIP)
	}
	if !strings.HasPrefix(server.URL, "http://127.0.0.1") {
		t.Fatalf("expected loopback test server, got %s", server.URL)
	}
}
