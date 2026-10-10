package http

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/h3fixture"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func availability(t *testing.T, result *protocol.Result) map[string]any {
	t.Helper()
	values, _ := result.ProtocolEvidence["timing_availability"].(map[string]any)
	if values == nil {
		t.Fatalf("no timing_availability in %v", result.ProtocolEvidence)
	}
	return values
}

// The handshake delay is applied per inbound packet by the fixture, so the
// measured handshake is at least the delay; the upper bound is generous for
// loaded CI machines.
const handshakeDelay = 200 * time.Millisecond

func TestHTTP3TimingForNewAndReusedConnections(t *testing.T) {
	fixture := h3fixture.Start(t, h3fixture.Options{})
	fixture.SetHandshakeDelay(handshakeDelay)
	h := http3Session(t, map[string]any{quicHandshakeTimeoutKey: float64(5000)})
	first := run(t, h, h3Get(t, fixture.URL("/echo"), "3"))
	if !first.Success {
		t.Fatal(first.ErrorMessage)
	}
	if first.ConnectTime < handshakeDelay || first.ConnectTime > handshakeDelay+3*time.Second {
		t.Fatalf("connect time %s, want the handshake (at least %s)", first.ConnectTime, handshakeDelay)
	}
	if first.TLSTime != 0 || first.DNSTime != 0 || first.TTFB < first.ConnectTime {
		t.Fatalf("tls=%s dns=%s ttfb=%s connect=%s", first.TLSTime, first.DNSTime, first.TTFB, first.ConnectTime)
	}
	// No double subtraction: server processing is TTFB minus the handshake.
	processing := time.Duration(first.ServerTelemetry.ServerProcessingMs * float64(time.Millisecond))
	if want := first.TTFB - first.ConnectTime; processing < want-time.Millisecond || processing > want+time.Millisecond {
		t.Fatalf("server processing %s, want TTFB-connect %s", processing, want)
	}
	evidence := first.ProtocolEvidence
	if evidence["quic_connection"] != "new" || evidence["tls_in_transport_handshake"] != true || evidence["attempt_count"] != 1 ||
		evidence["http_version_selection_reason"] != "exact_policy" || evidence["quic_attempted"] != true || evidence["ttfb_boundary"] != "first_byte" {
		t.Fatalf("new-connection receipt = %v", evidence)
	}
	if got := availability(t, first); got["connect"] != "available" || got["dns"] != "ip_literal" || got["tls"] != "in_transport_handshake" || got["ttfb"] != "available" {
		t.Fatalf("new-connection availability = %v", got)
	}
	second := run(t, h, h3Get(t, fixture.URL("/echo"), "3"))
	if second.ConnectTime != 0 || second.ProtocolEvidence["quic_connection"] != "reused" || second.ServerTelemetry.ConnectionReused != true {
		t.Fatalf("reused: connect=%s receipt=%v", second.ConnectTime, second.ProtocolEvidence)
	}
	if got := availability(t, second); got["connect"] != "reused_connection" || got["dns"] != "reused_connection" {
		t.Fatalf("reused availability = %v", got)
	}
}

func TestHTTP3TimingForSharedPendingAndResolvedConnections(t *testing.T) {
	fixture := h3fixture.Start(t, h3fixture.Options{})
	fixture.SetHandshakeDelay(handshakeDelay)
	h := http3Session(t, map[string]any{quicHandshakeTimeoutKey: float64(5000)})
	results := make([]*protocol.Result, 2)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if i == 1 {
				time.Sleep(50 * time.Millisecond) // join the first request's handshake
			}
			results[i] = run(t, h, h3Get(t, fixture.LocalhostURL("/echo"), "3"))
		}()
	}
	wg.Wait()
	dialer, waiter := results[0], results[1]
	if dialer.ProtocolEvidence["quic_connection"] != "new" || dialer.DNSTime <= 0 || availability(t, dialer)["dns"] != "available" {
		t.Fatalf("dialer: dns=%s receipt=%v", dialer.DNSTime, dialer.ProtocolEvidence)
	}
	if waiter.ProtocolEvidence["quic_connection"] != "shared_pending" || waiter.ConnectTime != 0 || availability(t, waiter)["connect"] != "shared_pending_connection" {
		t.Fatalf("waiter: connect=%s receipt=%v", waiter.ConnectTime, waiter.ProtocolEvidence)
	}
	if fixture.QUICConnections() != 1 {
		t.Fatalf("the waiter opened its own connection: %d connections", fixture.QUICConnections())
	}
}

func TestHTTP3TimingForResumedConnections(t *testing.T) {
	fixture := h3fixture.Start(t, h3fixture.Options{})
	h := http3Session(t, nil)
	if r := run(t, h, h3Get(t, fixture.URL("/echo"), "3")); !r.Success || r.ProtocolEvidence["tls_resumed"] != nil {
		t.Fatalf("first connection: %v", r.ProtocolEvidence)
	}
	h.http3.CloseIdleConnections()
	transport, _ := h.http3.current()
	deadline := time.Now().Add(2 * time.Second)
	for transport.OpenConnections() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	resumed := run(t, h, h3Get(t, fixture.URL("/echo"), "3"))
	if !resumed.Success || resumed.ProtocolEvidence["quic_connection"] != "new" || resumed.ProtocolEvidence["tls_resumed"] != true {
		t.Fatalf("second connection: %v", resumed.ProtocolEvidence)
	}
}

func TestHTTP3ReceiptForFailuresAndRefusals(t *testing.T) {
	fixture := h3fixture.Start(t, h3fixture.Options{})
	h := http3Session(t, nil)
	fixture.RejectNextQUICRequests(2)
	rejected := run(t, h, h3Get(t, fixture.URL("/effect?key=r"), "3"))
	if rejected.Success || rejected.ProtocolEvidence["quic_error_class"] != "request_rejected" || rejected.ProtocolEvidence["attempt_count"] != 2 {
		t.Fatalf("rejected twice: %v", rejected.ProtocolEvidence)
	}
	if err := fixture.SetQUICState(h3fixture.QUICBlackholed); err != nil {
		t.Fatal(err)
	}
	fresh := http3Session(t, nil)
	timedOut := run(t, fresh, h3Get(t, fixture.URL("/echo"), "3"))
	if timedOut.ProtocolEvidence["quic_error_class"] != "timeout" || timedOut.ProtocolEvidence["quic_attempted"] != true || timedOut.ProtocolEvidence["quic_connection"] != "dial_failed" || availability(t, timedOut)["ttfb"] != "no_response" {
		t.Fatalf("blackholed: %v", timedOut.ProtocolEvidence)
	}
	proxy := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(proxy.Close)
	proxied := http3Session(t, map[string]any{"proxy_mode": "custom", "proxy_url": proxy.URL})
	routed := run(t, proxied, h3Get(t, fixture.URL("/echo"), "3"))
	if routed.ProtocolEvidence["http_version_selection_reason"] != "proxy_route" || routed.ProtocolEvidence["quic_attempted"] != false || routed.ProtocolEvidence["quic_connection"] != nil {
		t.Fatalf("proxy route: %v", routed.ProtocolEvidence)
	}
	ntlm := http3Session(t, map[string]any{"auth_type": "ntlm", "auth_username": "u", "auth_password": "p"})
	refused := run(t, ntlm, h3Get(t, fixture.URL("/echo"), "3"))
	if refused.ProtocolEvidence["http_version_selection_reason"] != "integrated_auth" || refused.ProtocolEvidence["quic_attempted"] != false {
		t.Fatalf("integrated auth: %v", refused.ProtocolEvidence)
	}
	// TCP policies carry none of the HTTP/3 receipt fields.
	tcp := run(t, ntlm, h3Get(t, proxy.URL, "2"))
	if _, present := tcp.ProtocolEvidence["quic_attempted"]; present {
		t.Fatalf("a TCP request carried HTTP/3 receipt fields: %v", tcp.ProtocolEvidence)
	}
}
