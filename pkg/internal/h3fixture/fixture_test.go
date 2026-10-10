package h3fixture

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

func tcpClient(s *Server, h2 bool) *http.Client {
	tr := &http.Transport{TLSClientConfig: s.ClientTLSConfig(), ForceAttemptHTTP2: h2}
	if !h2 {
		tr.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	}
	return &http.Client{Transport: tr, Timeout: 10 * time.Second}
}

func h3Transport(s *Server, handshake time.Duration) *http3.Transport {
	cfg := &quic.Config{}
	if handshake > 0 {
		cfg.HandshakeIdleTimeout = handshake
	}
	return &http3.Transport{TLSClientConfig: s.ClientTLSConfig(), QUICConfig: cfg}
}

func get(t *testing.T, client *http.Client, url string) *http.Response {
	t.Helper()
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp
}

func TestServesHTTP1HTTP2AndHTTP3OnOnePort(t *testing.T) {
	s := Start(t, Options{})
	h3 := h3Transport(s, 0)
	t.Cleanup(func() { _ = h3.Close() })

	cases := []struct {
		name   string
		client *http.Client
		proto  string
		ln     string
	}{
		{"http1", tcpClient(s, false), "HTTP/1.1", "tcp"},
		{"http2", tcpClient(s, true), "HTTP/2.0", "tcp"},
		{"http3", &http.Client{Transport: h3, Timeout: 10 * time.Second}, "HTTP/3.0", "quic"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := get(t, tc.client, s.URL("/?key="+tc.name))
			if resp.StatusCode != http.StatusOK || resp.Header.Get("X-Fixture-Proto") != tc.proto || resp.Header.Get("X-Fixture-Listener") != tc.ln {
				t.Fatalf("status=%d proto=%q listener=%q", resp.StatusCode, resp.Header.Get("X-Fixture-Proto"), resp.Header.Get("X-Fixture-Listener"))
			}
			got := s.Ledger.Count(func(a Attempt) bool { return a.Key == tc.name })
			if got != 1 {
				t.Fatalf("ledger attempts for %s = %d", tc.name, got)
			}
		})
	}
	if s.Ledger.Count(func(a Attempt) bool { return a.Proto == "HTTP/3.0" && a.Listener == "quic" }) != 1 {
		t.Fatalf("ledger = %+v", s.Ledger.Attempts())
	}
}

func TestAltSvcAdvertisementsAndAlternatePort(t *testing.T) {
	s := Start(t, Options{Advertise: AdvertiseAltPort, AlternatePort: true, AltSvcMaxAge: 60})
	resp := get(t, tcpClient(s, true), s.URL("/"))
	want := `h3=":` + strconv.Itoa(s.AltPort) + `"; ma=60`
	if got := resp.Header.Get("Alt-Svc"); got != want {
		t.Fatalf("Alt-Svc = %q, want %q", got, want)
	}

	// Dial the alternate endpoint while authenticating the original origin,
	// which is what an Alt-Svc client must do.
	alt := &http3.Transport{
		TLSClientConfig: s.ClientTLSConfig(),
		Dial: func(ctx context.Context, _ string, tlsCfg *tls.Config, cfg *quic.Config) (*quic.Conn, error) {
			return quic.DialAddrEarly(ctx, net.JoinHostPort(s.Host, strconv.Itoa(s.AltPort)), tlsCfg, cfg)
		},
	}
	t.Cleanup(func() { _ = alt.Close() })
	req, _ := http.NewRequest(http.MethodGet, s.URL("/"), nil)
	req.Header.Set("Alt-Used", net.JoinHostPort(s.Host, strconv.Itoa(s.AltPort)))
	altResp, err := alt.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = altResp.Body.Close()
	if altResp.Header.Get("X-Fixture-Listener") != "quic-alt" {
		t.Fatalf("listener = %q", altResp.Header.Get("X-Fixture-Listener"))
	}
	attempts := s.Ledger.Attempts()
	if last := attempts[len(attempts)-1]; last.AltUsed == "" || last.Listener != "quic-alt" {
		t.Fatalf("last attempt = %+v", last)
	}

	s.SetAltSvc("")
	if got := get(t, tcpClient(s, true), s.URL("/")).Header.Get("Alt-Svc"); got != "" {
		t.Fatalf("Alt-Svc after clear = %q", got)
	}
}

func TestRequestRejectedIsNeverProcessed(t *testing.T) {
	s := Start(t, Options{})
	h3 := h3Transport(s, 0)
	t.Cleanup(func() { _ = h3.Close() })
	// Warm the connection so the rejection hits a request stream.
	get(t, &http.Client{Transport: h3}, s.URL("/warm"))
	s.Ledger.Reset()

	s.RejectNextQUICRequests(1)
	resp := get(t, &http.Client{Transport: h3}, s.URL("/effect?key=rejected"))
	// quic-go retries H3_REQUEST_REJECTED once on a new connection; the
	// fixture refused the first stream before the handler ran.
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if n := s.Ledger.Effects("rejected"); n != 1 {
		t.Fatalf("effects = %d, want 1", n)
	}
	if n := s.Ledger.Count(nil); n != 1 {
		t.Fatalf("attempts = %d, want 1 (the rejected stream must not reach the handler)", n)
	}
}

func TestVersionFallbackReachesTheClient(t *testing.T) {
	s := Start(t, Options{})
	h3 := h3Transport(s, 0)
	t.Cleanup(func() { _ = h3.Close() })
	get(t, &http.Client{Transport: h3}, s.URL("/warm"))
	s.Ledger.Reset()

	s.VersionFallbackNextQUICRequests(1)
	_, err := (&http.Client{Transport: h3}).Get(s.URL("/effect?key=fallback"))
	var h3Err *http3.Error
	if !errors.As(err, &h3Err) || h3Err.ErrorCode != http3.ErrCodeVersionFallback {
		t.Fatalf("err = %v, want H3_VERSION_FALLBACK", err)
	}
	if s.Ledger.Count(nil) != 0 || s.Ledger.Effects("fallback") != 0 {
		t.Fatalf("refused stream reached the handler: %+v", s.Ledger.Attempts())
	}
}

func TestPartialResponseAfterEffect(t *testing.T) {
	s := Start(t, Options{})
	h3 := h3Transport(s, 0)
	t.Cleanup(func() { _ = h3.Close() })
	clients := map[string]*http.Client{
		"http2": tcpClient(s, true),
		"http3": {Transport: h3},
	}
	for name, client := range clients {
		t.Run(name, func(t *testing.T) {
			key := "partial-" + name
			resp, err := client.Post(s.URL("/partial?key="+key), "text/plain", strings.NewReader("write"))
			if err == nil {
				_, err = io.ReadAll(resp.Body)
				_ = resp.Body.Close()
			}
			if err == nil {
				t.Fatal("partial response read without error")
			}
			if n := s.Ledger.Effects(key); n != 1 {
				t.Fatalf("effects = %d, want 1", n)
			}
		})
	}
}

func TestQUICStatesBlackholeCloseAndRestore(t *testing.T) {
	s := Start(t, Options{QUIC: QUICBlackholed})
	h3 := h3Transport(s, 300*time.Millisecond)
	t.Cleanup(func() { _ = h3.Close() })
	client := &http.Client{Transport: h3, Timeout: 5 * time.Second}

	started := time.Now()
	if _, err := client.Get(s.URL("/")); err == nil {
		t.Fatal("blackholed QUIC answered")
	}
	if time.Since(started) < 250*time.Millisecond {
		t.Fatalf("blackhole failed fast (%s); it must look like silence, not a refusal", time.Since(started))
	}
	// TCP is unaffected by the UDP state.
	if get(t, tcpClient(s, true), s.URL("/")).StatusCode != http.StatusOK {
		t.Fatal("TCP failed while QUIC was blackholed")
	}

	if err := s.SetQUICState(QUICClosed); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Get(s.URL("/")); err == nil {
		t.Fatal("closed QUIC answered")
	}

	if err := s.SetQUICState(QUICServing); err != nil {
		t.Fatal(err)
	}
	if resp := get(t, client, s.URL("/")); resp.Header.Get("X-Fixture-Proto") != "HTTP/3.0" {
		t.Fatalf("proto after restore = %q", resp.Header.Get("X-Fixture-Proto"))
	}
}

func TestHandshakeDelay(t *testing.T) {
	s := Start(t, Options{HandshakeDelay: 60 * time.Millisecond})
	h3 := h3Transport(s, 0)
	t.Cleanup(func() { _ = h3.Close() })
	started := time.Now()
	get(t, &http.Client{Transport: h3}, s.URL("/"))
	// A QUIC handshake needs at least two inbound client flights at the server.
	if elapsed := time.Since(started); elapsed < 120*time.Millisecond {
		t.Fatalf("elapsed = %s, want >= 120ms with a 60ms per-packet delay", elapsed)
	}
}

func TestStreamLimitQueuesRequestsOnOneConnection(t *testing.T) {
	s := Start(t, Options{MaxIncomingStreams: 1})
	h3 := h3Transport(s, 0)
	t.Cleanup(func() { _ = h3.Close() })
	client := &http.Client{Transport: h3, Timeout: 10 * time.Second}
	get(t, client, s.URL("/warm"))

	started := time.Now()
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Go(func() {
			resp, err := client.Get(s.URL("/slow?ms=200&i=" + strconv.Itoa(i)))
			if err != nil {
				t.Error(err)
				return
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		})
	}
	wg.Wait()
	if elapsed := time.Since(started); elapsed < 380*time.Millisecond {
		t.Fatalf("elapsed = %s; with one stream allowed the requests must run one after the other", elapsed)
	}
	if n := s.QUICConnections(); n != 1 {
		t.Fatalf("QUIC connections = %d, want 1 (queueing, not fan-out)", n)
	}
}

func TestCloseQUICConnections(t *testing.T) {
	s := Start(t, Options{})
	h3 := h3Transport(s, 0)
	t.Cleanup(func() { _ = h3.Close() })
	client := &http.Client{Transport: h3, Timeout: 5 * time.Second}
	get(t, client, s.URL("/"))
	if s.QUICConnections() != 1 {
		t.Fatalf("connections = %d", s.QUICConnections())
	}
	s.CloseQUICConnections()
	deadline := time.Now().Add(2 * time.Second)
	for s.QUICConnections() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if s.QUICConnections() != 0 {
		t.Fatal("connection still tracked after drop")
	}
}

func TestRedirectStatusBytesAndSSE(t *testing.T) {
	s := Start(t, Options{})
	h3 := h3Transport(s, 0)
	t.Cleanup(func() { _ = h3.Close() })
	client := &http.Client{Transport: h3, Timeout: 5 * time.Second}

	resp, err := client.Get(s.URL("/redirect?to=/status%3Fcode%3D204"))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("redirect target status = %d", resp.StatusCode)
	}

	resp, err = client.Get(s.URL("/bytes?n=100000"))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if len(body) != 100000 {
		t.Fatalf("bytes = %d", len(body))
	}

	resp, err = client.Get(s.URL("/sse?n=3"))
	if err != nil {
		t.Fatal(err)
	}
	events, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if strings.Count(string(events), "data: event-") != 3 || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("sse = %q", events)
	}
}

func TestIPv6Loopback(t *testing.T) {
	if ln, err := net.Listen("tcp", "[::1]:0"); err != nil {
		t.Skip("IPv6 loopback unavailable")
	} else {
		_ = ln.Close()
	}
	s := Start(t, Options{IPv6: true})
	h3 := h3Transport(s, 0)
	t.Cleanup(func() { _ = h3.Close() })
	if resp := get(t, &http.Client{Transport: h3}, s.URL("/")); resp.Header.Get("X-Fixture-Proto") != "HTTP/3.0" {
		t.Fatalf("proto = %q", resp.Header.Get("X-Fixture-Proto"))
	}
}

func TestCloseReleasesGoroutines(t *testing.T) {
	baseline := runtime.NumGoroutine()
	for range 3 {
		s, err := start(Options{AlternatePort: true})
		if err != nil {
			t.Fatal(err)
		}
		h3 := h3Transport(s, 0)
		get(t, &http.Client{Transport: h3}, s.URL("/"))
		get(t, tcpClient(s, true), s.URL("/"))
		_ = h3.Close()
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(3 * time.Second)
	for runtime.NumGoroutine() > baseline+4 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > baseline+4 {
		buf := make([]byte, 1<<20)
		t.Fatalf("goroutines = %d, baseline %d\n%s", n, baseline, buf[:runtime.Stack(buf, true)])
	}
}
