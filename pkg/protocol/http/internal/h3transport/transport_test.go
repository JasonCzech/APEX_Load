package h3transport

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/h3fixture"
)

func newTransport(t *testing.T, fixture *h3fixture.Server, mutate func(*Options)) *Transport {
	t.Helper()
	opts := Options{TLS: fixture.ClientTLSConfig(), HandshakeTimeout: 2 * time.Second, MaxIdleTimeout: 10 * time.Second, ConnectTimeout: 2 * time.Second}
	if mutate != nil {
		mutate(&opts)
	}
	transport, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	return transport
}

func get(t *testing.T, transport *Transport, ctx context.Context, target string) (*http.Response, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := transport.RoundTrip(req)
	if err == nil {
		t.Cleanup(func() { _ = resp.Body.Close() })
	}
	return resp, err
}

func certainty(err error) Certainty {
	var h3Err *Error
	if errors.As(err, &h3Err) {
		return h3Err.Certainty
	}
	return ""
}

func TestRequestsRunOverHTTP3AndReuseOneConnection(t *testing.T) {
	fixture := h3fixture.Start(t, h3fixture.Options{})
	transport := newTransport(t, fixture, nil)
	for range 3 {
		resp, err := get(t, transport, context.Background(), fixture.URL("/echo"))
		if err != nil {
			t.Fatal(err)
		}
		if resp.ProtoMajor != 3 {
			t.Fatalf("proto = %s", resp.Proto)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
	}
	if n := fixture.Ledger.Count(func(a h3fixture.Attempt) bool { return a.Proto == "HTTP/3.0" }); n != 3 {
		t.Fatalf("fixture saw %d HTTP/3 requests, want 3", n)
	}
	if transport.OpenConnections() != 1 || transport.OpenSockets() != 1 || fixture.QUICConnections() != 1 {
		t.Fatalf("connections=%d sockets=%d server connections=%d, want one each", transport.OpenConnections(), transport.OpenSockets(), fixture.QUICConnections())
	}
}

func TestUnreachableQUICFailsAsNotSent(t *testing.T) {
	for _, state := range []h3fixture.QUICState{h3fixture.QUICBlackholed, h3fixture.QUICClosed} {
		fixture := h3fixture.Start(t, h3fixture.Options{QUIC: state})
		transport := newTransport(t, fixture, func(o *Options) { o.HandshakeTimeout = 300 * time.Millisecond; o.ConnectTimeout = time.Second })
		started := time.Now()
		_, err := get(t, transport, context.Background(), fixture.URL("/effect?key=k"))
		if certainty(err) != NotSent {
			t.Fatalf("state %d: err = %v, want not_sent", state, err)
		}
		if elapsed := time.Since(started); elapsed > 3*time.Second {
			t.Fatalf("state %d: failing took %s", state, elapsed)
		}
		if n := len(fixture.Ledger.Attempts()); n != 0 {
			t.Fatalf("state %d: the target saw %d attempts", state, n)
		}
	}
}

func TestRequestRejectedIsRetriedOnceThenReportedUnprocessed(t *testing.T) {
	fixture := h3fixture.Start(t, h3fixture.Options{})
	transport := newTransport(t, fixture, nil)
	fixture.RejectNextQUICRequests(1)
	resp, err := get(t, transport, context.Background(), fixture.URL("/effect?key=once"))
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("one rejection: %v %v, want quic-go's single retry to succeed", resp, err)
	}
	fixture.RejectNextQUICRequests(2)
	_, err = get(t, transport, context.Background(), fixture.URL("/effect?key=twice"))
	if certainty(err) != RejectedUnprocessed {
		t.Fatalf("two rejections: err = %v, want rejected_unprocessed", err)
	}
	if effects := fixture.Ledger.Effects("twice"); effects != 0 {
		t.Fatalf("a rejected request took effect %d times", effects)
	}
	if effects := fixture.Ledger.Effects("once"); effects != 1 {
		t.Fatalf("the retried request took effect %d times, want once", effects)
	}
}

func TestDroppedConnectionAfterTheWriteIsPossiblySent(t *testing.T) {
	fixture := h3fixture.Start(t, h3fixture.Options{})
	transport := newTransport(t, fixture, nil)
	if _, err := get(t, transport, context.Background(), fixture.URL("/echo")); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(200 * time.Millisecond)
		fixture.CloseQUICConnections()
	}()
	_, err := get(t, transport, context.Background(), fixture.URL("/slow?ms=3000"))
	if certainty(err) != PossiblySent {
		t.Fatalf("err = %v, want possibly_sent", err)
	}
}

func TestCallerCancellationIsReturnedUnchanged(t *testing.T) {
	fixture := h3fixture.Start(t, h3fixture.Options{})
	transport := newTransport(t, fixture, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_, err := get(t, transport, ctx, fixture.URL("/slow?ms=3000"))
	if err == nil || certainty(err) != "" {
		t.Fatalf("err = %v, want the caller's own cancellation", err)
	}
}

// D2: a request cancelled while its connection is still dialing must not fail
// the requests waiting on the same dial.
func TestDialIsDetachedFromTheTriggeringRequest(t *testing.T) {
	fixture := h3fixture.Start(t, h3fixture.Options{})
	fixture.SetHandshakeDelay(300 * time.Millisecond)
	transport := newTransport(t, fixture, nil)
	first, cancelFirst := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = get(t, transport, first, fixture.URL("/echo?first=1"))
	}()
	time.Sleep(50 * time.Millisecond)
	cancelFirst()
	resp, err := get(t, transport, context.Background(), fixture.URL("/echo?second=1"))
	wg.Wait()
	if err != nil || resp.ProtoMajor != 3 {
		t.Fatalf("waiting request: %v %v", resp, err)
	}
}

func TestLifetimeEndsPendingDials(t *testing.T) {
	fixture := h3fixture.Start(t, h3fixture.Options{QUIC: h3fixture.QUICBlackholed})
	lifetime, stop := context.WithCancel(context.Background())
	transport := newTransport(t, fixture, func(o *Options) {
		o.Lifetime = lifetime
		o.HandshakeTimeout = 10 * time.Second
		o.ConnectTimeout = 10 * time.Second
	})
	time.AfterFunc(100*time.Millisecond, stop)
	started := time.Now()
	_, err := get(t, transport, context.Background(), fixture.URL("/echo"))
	if certainty(err) != NotSent || time.Since(started) > 3*time.Second {
		t.Fatalf("err = %v after %s, want a prompt not_sent", err, time.Since(started))
	}
}

func TestDialAppliesHostPolicyFamilyAndSchemeRules(t *testing.T) {
	fixture := h3fixture.Start(t, h3fixture.Options{})
	denied := newTransport(t, fixture, func(o *Options) {
		o.AuthorizeHost = func(host string) error { return errors.New("target host " + host + " is not allowed") }
	})
	if _, err := get(t, denied, context.Background(), fixture.URL("/echo")); certainty(err) != NotSent || !strings.Contains(err.Error(), "is not allowed") {
		t.Fatalf("denied host: %v", err)
	}
	wrongFamily := newTransport(t, fixture, func(o *Options) { o.IPVersion = "ipv6" })
	if _, err := get(t, wrongFamily, context.Background(), fixture.URL("/echo")); !strings.Contains(err.Error(), "no usable address") {
		t.Fatalf("IPv4 target on an IPv6-only VUser: %v", err)
	}
	plain := newTransport(t, fixture, nil)
	if _, err := get(t, plain, context.Background(), strings.Replace(fixture.URL("/echo"), "https://", "http://", 1)); err == nil {
		t.Fatal("an http:// request ran over HTTP/3")
	}
	if len(fixture.Ledger.Attempts()) != 0 {
		t.Fatal("a refused request reached the target")
	}
}

func TestDialTimesResolutionAndHandshakeAndUsesTheResolver(t *testing.T) {
	fixture := h3fixture.Start(t, h3fixture.Options{})
	resolved := []string{}
	transport := newTransport(t, fixture, func(o *Options) {
		o.Resolve = func(_ context.Context, host string) ([]netip.Addr, error) {
			resolved = append(resolved, host)
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		}
	})
	var dnsStart, dnsDone, connectStart, connectDone, tlsStart bool
	ctx := httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{
		DNSStart:          func(httptrace.DNSStartInfo) { dnsStart = true },
		DNSDone:           func(httptrace.DNSDoneInfo) { dnsDone = true },
		ConnectStart:      func(string, string) { connectStart = true },
		ConnectDone:       func(network, _ string, err error) { connectDone = network == "udp" && err == nil },
		TLSHandshakeStart: func() { tlsStart = true },
	})
	resp, err := get(t, transport, ctx, fixture.LocalhostURL("/echo"))
	if err != nil || resp.ProtoMajor != 3 {
		t.Fatalf("%v %v", resp, err)
	}
	if len(resolved) != 1 || resolved[0] != "localhost" {
		t.Fatalf("resolver calls = %v", resolved)
	}
	if !dnsStart || !dnsDone || !connectStart || !connectDone {
		t.Fatalf("dns=%v/%v connect=%v/%v", dnsStart, dnsDone, connectStart, connectDone)
	}
	// D1: the TLS handshake is part of the QUIC handshake, timed as connect.
	if tlsStart {
		t.Fatal("a separate TLS handshake was reported")
	}
}

func TestSourceAddressBindsTheSocket(t *testing.T) {
	fixture := h3fixture.Start(t, h3fixture.Options{})
	transport := newTransport(t, fixture, func(o *Options) { o.SourceIP = net.ParseIP("127.0.0.1") })
	if _, err := get(t, transport, context.Background(), fixture.URL("/echo")); err != nil {
		t.Fatal(err)
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	for _, socket := range transport.sockets {
		if addr := socket.Conn.LocalAddr().(*net.UDPAddr); !addr.IP.Equal(net.ParseIP("127.0.0.1")) {
			t.Fatalf("socket bound to %s", addr)
		}
	}
}

func TestCloseEndsConnectionsAndSockets(t *testing.T) {
	fixture := h3fixture.Start(t, h3fixture.Options{})
	transport := newTransport(t, fixture, nil)
	if _, err := get(t, transport, context.Background(), fixture.URL("/echo")); err != nil {
		t.Fatal(err)
	}
	if err := transport.Close(); err != nil {
		t.Fatal(err)
	}
	if transport.OpenSockets() != 0 {
		t.Fatalf("sockets left open: %d", transport.OpenSockets())
	}
	deadline := time.Now().Add(2 * time.Second)
	for transport.OpenConnections() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if transport.OpenConnections() != 0 {
		t.Fatal("connections left open after Close")
	}
	if _, err := get(t, transport, context.Background(), fixture.URL("/echo")); err == nil {
		t.Fatal("a closed transport sent a request")
	}
	if err := transport.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestTLSBelow13IsRefused(t *testing.T) {
	if _, err := New(Options{TLS: &tls.Config{MaxVersion: tls.VersionTLS12}}); err == nil || !strings.Contains(err.Error(), "TLS 1.3") {
		t.Fatalf("err = %v", err)
	}
}

func TestUnroutedFamilyFallsBackQuickly(t *testing.T) {
	fixture := h3fixture.Start(t, h3fixture.Options{}) // IPv4 only
	transport := newTransport(t, fixture, func(o *Options) {
		o.HandshakeTimeout, o.ConnectTimeout = 10*time.Second, 10*time.Second
		o.Resolve = func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("::1"), netip.MustParseAddr("127.0.0.1")}, nil
		}
	})
	started := time.Now()
	resp, err := get(t, transport, context.Background(), fixture.LocalhostURL("/echo"))
	if err != nil || resp.ProtoMajor != 3 {
		t.Fatalf("%v %v", resp, err)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("falling back from an unrouted IPv6 address took %s", elapsed)
	}
	if transport.OpenConnections() != 1 {
		t.Fatalf("connections = %d, want only the winner", transport.OpenConnections())
	}
}

func TestInterleaveFamilies(t *testing.T) {
	in := []netip.Addr{netip.MustParseAddr("::1"), netip.MustParseAddr("::2"), netip.MustParseAddr("10.0.0.1"), netip.MustParseAddr("10.0.0.2")}
	got := interleaveFamilies(in)
	want := []string{"::1", "10.0.0.1", "::2", "10.0.0.2"}
	for i, address := range got {
		if address.String() != want[i] {
			t.Fatalf("interleaved = %v, want %v", got, want)
		}
	}
}

func TestCheckAddressRefusesAddresses(t *testing.T) {
	fixture := h3fixture.Start(t, h3fixture.Options{})
	transport := newTransport(t, fixture, func(o *Options) {
		o.CheckAddress = func(netip.Addr) error { return errors.New("non-public address refused") }
	})
	if _, err := get(t, transport, context.Background(), fixture.URL("/echo")); certainty(err) != NotSent || !strings.Contains(err.Error(), "non-public address refused") {
		t.Fatalf("err = %v", err)
	}
	if len(fixture.Ledger.Attempts()) != 0 {
		t.Fatal("a refused address was dialed")
	}
}

func TestIdleCloseWaitsForOpenBodies(t *testing.T) {
	fixture := h3fixture.Start(t, h3fixture.Options{})
	transport := newTransport(t, fixture, nil)
	resp, err := get(t, transport, context.Background(), fixture.URL("/sse?n=3&interval_ms=150"))
	if err != nil {
		t.Fatal(err)
	}
	if transport.ActiveRequests() != 1 {
		t.Fatalf("active = %d while the stream is open", transport.ActiveRequests())
	}
	transport.CloseIdleConnections() // quic-go alone would close this stream's connection
	body, err := io.ReadAll(resp.Body)
	if err != nil || !strings.Contains(string(body), "data:") {
		t.Fatalf("stream cut short: %q %v", body, err)
	}
	_ = resp.Body.Close()
	if transport.ActiveRequests() != 0 {
		t.Fatalf("active = %d after the body closed", transport.ActiveRequests())
	}
	transport.CloseIdleConnections()
	deadline := time.Now().Add(2 * time.Second)
	for transport.OpenConnections() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if transport.OpenConnections() != 0 {
		t.Fatal("an idle connection survived CloseIdleConnections")
	}
}

func TestAcquireHoldsASlotForTheConnectionLifetime(t *testing.T) {
	fixture := h3fixture.Start(t, h3fixture.Options{})
	var mu sync.Mutex
	held := 0
	transport := newTransport(t, fixture, func(o *Options) {
		o.Acquire = func(context.Context, string) (func(), error) {
			mu.Lock()
			held++
			mu.Unlock()
			return func() { mu.Lock(); held--; mu.Unlock() }, nil
		}
	})
	if _, err := get(t, transport, context.Background(), fixture.URL("/echo")); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	during := held
	mu.Unlock()
	if during != 1 {
		t.Fatalf("slots held during the connection = %d", during)
	}
	_ = transport.Close()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		after := held
		mu.Unlock()
		if after == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the slot was not released when the connection closed")
}

func TestCloseEndsQueuedAndHandshakingDials(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state h3fixture.QUICState
		queue bool
	}{{"handshaking", h3fixture.QUICBlackholed, false}, {"queued for the budget", h3fixture.QUICServing, true}} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := h3fixture.Start(t, h3fixture.Options{QUIC: tc.state})
			transport := newTransport(t, fixture, func(o *Options) {
				o.HandshakeTimeout, o.ConnectTimeout = 30*time.Second, 30*time.Second
				if tc.queue {
					o.Acquire = func(ctx context.Context, _ string) (func(), error) {
						<-ctx.Done() // the budget never frees a slot
						return nil, ctx.Err()
					}
				}
			})
			done := make(chan error, 1)
			go func() {
				_, err := get(t, transport, context.Background(), fixture.URL("/echo"))
				done <- err
			}()
			time.Sleep(150 * time.Millisecond)
			closed := make(chan struct{})
			go func() { _ = transport.Close(); close(closed) }()
			select {
			case <-closed:
			case <-time.After(3 * time.Second):
				t.Fatal("Close waited on a pending dial")
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("the pending request succeeded after Close")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("the pending request did not end after Close")
			}
		})
	}
}
