package h3fixture

// These tests pin the quic-go behaviors APEX Load's HTTP/3 design depends on
// (docs/HTTP3_IMPLEMENTATION.md, decisions D1 and D2). A quic-go upgrade that
// changes any of them must fail here first.

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"net/http/httptrace"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

type hookLog struct {
	mu     sync.Mutex
	events []string
	reused []bool
}

func (h *hookLog) add(name string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = append(h.events, name)
}

func (h *hookLog) count(name string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, e := range h.events {
		if e == name {
			n++
		}
	}
	return n
}

func (h *hookLog) trace() *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		GetConn: func(string) { h.add("GetConn") },
		GotConn: func(info httptrace.GotConnInfo) {
			h.add("GotConn")
			h.mu.Lock()
			h.reused = append(h.reused, info.Reused)
			h.mu.Unlock()
		},
		DNSStart:             func(httptrace.DNSStartInfo) { h.add("DNSStart") },
		DNSDone:              func(httptrace.DNSDoneInfo) { h.add("DNSDone") },
		ConnectStart:         func(string, string) { h.add("ConnectStart") },
		ConnectDone:          func(string, string, error) { h.add("ConnectDone") },
		TLSHandshakeStart:    func() { h.add("TLSHandshakeStart") },
		TLSHandshakeDone:     func(tls.ConnectionState, error) { h.add("TLSHandshakeDone") },
		WroteHeaders:         func() { h.add("WroteHeaders") },
		WroteRequest:         func(httptrace.WroteRequestInfo) { h.add("WroteRequest") },
		GotFirstResponseByte: func() { h.add("GotFirstResponseByte") },
	}
}

func tracedGet(t *testing.T, rt http.RoundTripper, url string, log *hookLog) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(httptrace.WithClientTrace(context.Background(), log.trace()), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp
}

// D1: with the default dialer quic-go reports connect, TLS, header-write,
// request-write and first-byte events; DNS events come from net's resolver
// only when a name is resolved.
func TestQuicGoTraceHooksDefaultDialer(t *testing.T) {
	s := Start(t, Options{})
	tr := h3Transport(s, 0)
	t.Cleanup(func() { _ = tr.Close() })

	first := &hookLog{}
	tracedGet(t, tr, s.LocalhostURL("/"), first)
	for _, hook := range []string{"GetConn", "GotConn", "DNSStart", "DNSDone", "ConnectStart", "ConnectDone", "TLSHandshakeStart", "TLSHandshakeDone", "WroteHeaders", "WroteRequest", "GotFirstResponseByte"} {
		if first.count(hook) != 1 {
			t.Errorf("first request: %s fired %d times; events=%v", hook, first.count(hook), first.events)
		}
	}
	if len(first.reused) != 1 || first.reused[0] {
		t.Errorf("first request reused = %v", first.reused)
	}

	second := &hookLog{}
	tracedGet(t, tr, s.LocalhostURL("/"), second)
	if second.count("ConnectStart") != 0 || second.count("DNSStart") != 0 || len(second.reused) != 1 || !second.reused[0] {
		t.Errorf("reused request events=%v reused=%v", second.events, second.reused)
	}
}

// D1: a custom Dial bypasses the connect and TLS hooks, so APEX must time its
// own dial. Header, request and first-byte hooks still fire.
func TestQuicGoTraceHooksCustomDial(t *testing.T) {
	s := Start(t, Options{})
	dials := 0
	tr := &http3.Transport{
		TLSClientConfig: s.ClientTLSConfig(),
		Dial: func(ctx context.Context, addr string, tlsCfg *tls.Config, cfg *quic.Config) (*quic.Conn, error) {
			dials++
			return quic.DialAddrEarly(ctx, addr, tlsCfg, cfg)
		},
	}
	t.Cleanup(func() { _ = tr.Close() })
	log := &hookLog{}
	tracedGet(t, tr, s.URL("/"), log)
	if dials != 1 {
		t.Fatalf("dials = %d", dials)
	}
	for _, absent := range []string{"ConnectStart", "ConnectDone", "TLSHandshakeStart", "TLSHandshakeDone"} {
		if log.count(absent) != 0 {
			t.Errorf("%s fired with a custom Dial; events=%v", absent, log.events)
		}
	}
	for _, present := range []string{"GetConn", "GotConn", "WroteHeaders", "WroteRequest", "GotFirstResponseByte"} {
		if log.count(present) != 1 {
			t.Errorf("%s fired %d times with a custom Dial; events=%v", present, log.count(present), log.events)
		}
	}
}

// D1: a request that waits on another request's in-progress dial reports
// Reused=false although it did not dial, so "did not dial" and "GotConn
// reused" are both unreliable reuse evidence. APEX tracks connection identity.
func TestQuicGoSharedPendingDialReportsNotReused(t *testing.T) {
	s := Start(t, Options{HandshakeDelay: 40 * time.Millisecond})
	dials := 0
	var mu sync.Mutex
	tr := &http3.Transport{
		TLSClientConfig: s.ClientTLSConfig(),
		Dial: func(ctx context.Context, addr string, tlsCfg *tls.Config, cfg *quic.Config) (*quic.Conn, error) {
			mu.Lock()
			dials++
			mu.Unlock()
			return quic.DialAddrEarly(ctx, addr, tlsCfg, cfg)
		},
	}
	t.Cleanup(func() { _ = tr.Close() })
	logs := []*hookLog{{}, {}}
	var wg sync.WaitGroup
	for i := range logs {
		wg.Go(func() { tracedGet(t, tr, s.URL("/?i="+strconv.Itoa(i)), logs[i]) })
	}
	wg.Wait()
	if dials != 1 {
		t.Fatalf("dials = %d, want 1 shared dial", dials)
	}
	for i, log := range logs {
		if len(log.reused) != 1 || log.reused[0] {
			t.Errorf("request %d reused = %v; quic-go reports a shared pending dial as not reused", i, log.reused)
		}
	}
}

// D2: quic-go retries H3_REQUEST_REJECTED once, invisibly except that GetConn
// fires once per attempt. APEX counts attempts from GetConn.
func TestQuicGoRetriesRejectedRequestsOnceAndTraceShowsIt(t *testing.T) {
	s := Start(t, Options{})
	tr := h3Transport(s, 0)
	t.Cleanup(func() { _ = tr.Close() })
	tracedGet(t, tr, s.URL("/warm"), &hookLog{})
	s.Ledger.Reset()

	s.RejectNextQUICRequests(1)
	log := &hookLog{}
	tracedGet(t, tr, s.URL("/effect?key=retried"), log)
	if log.count("GetConn") != 2 {
		t.Fatalf("GetConn fired %d times, want 2 (one per attempt); events=%v", log.count("GetConn"), log.events)
	}
	if s.Ledger.Effects("retried") != 1 {
		t.Fatalf("effects = %d", s.Ledger.Effects("retried"))
	}

	// Rejected twice: the single built-in retry is exhausted.
	s.RejectNextQUICRequests(2)
	_, err := tr.RoundTrip(mustRequest(t, http.MethodGet, s.URL("/effect?key=twice"), nil))
	var h3Err *http3.Error
	if !errors.As(err, &h3Err) || h3Err.ErrorCode != http3.ErrCodeRequestRejected {
		t.Fatalf("err = %v, want H3_REQUEST_REJECTED after one retry", err)
	}
}

// D2: a rejected request with a body is retried only when GetBody can
// rebuild it; otherwise quic-go returns an error rather than replaying.
func TestQuicGoRejectedBodyNeedsGetBody(t *testing.T) {
	s := Start(t, Options{})
	tr := h3Transport(s, 0)
	t.Cleanup(func() { _ = tr.Close() })
	tracedGet(t, tr, s.URL("/warm"), &hookLog{})

	s.RejectNextQUICRequests(1)
	withGetBody := mustRequest(t, http.MethodPost, s.URL("/effect?key=getbody"), strings.NewReader("payload"))
	resp, err := tr.RoundTrip(withGetBody)
	if err != nil {
		t.Fatalf("retry with GetBody failed: %v", err)
	}
	_ = resp.Body.Close()
	if s.Ledger.Effects("getbody") != 1 {
		t.Fatalf("effects = %d", s.Ledger.Effects("getbody"))
	}

	s.RejectNextQUICRequests(1)
	noGetBody := mustRequest(t, http.MethodPost, s.URL("/effect?key=nogetbody"), io.NopCloser(bytes.NewReader([]byte("payload"))))
	noGetBody.GetBody = nil
	if _, err := tr.RoundTrip(noGetBody); err == nil || !strings.Contains(err.Error(), "GetBody") {
		t.Fatalf("err = %v, want a cannot-retry error naming GetBody", err)
	}
	if s.Ledger.Effects("nogetbody") != 0 {
		t.Fatal("request without GetBody was replayed")
	}
}

// D2: the dial for a new connection runs on a context derived from the
// first request. Cancelling that request also fails a second request that
// was waiting on the same dial, even though its own context is alive.
func TestQuicGoDialIsCoupledToFirstRequestContext(t *testing.T) {
	s := Start(t, Options{HandshakeDelay: 80 * time.Millisecond})
	tr := h3Transport(s, 0)
	t.Cleanup(func() { _ = tr.Close() })

	firstCtx, cancelFirst := context.WithCancel(context.Background())
	firstErr := make(chan error, 1)
	go func() {
		_, err := tr.RoundTrip(mustRequestCtx(t, firstCtx, s.URL("/first")))
		firstErr <- err
	}()
	time.Sleep(20 * time.Millisecond)
	secondErr := make(chan error, 1)
	go func() {
		_, err := tr.RoundTrip(mustRequestCtx(t, context.Background(), s.URL("/second")))
		secondErr <- err
	}()
	time.Sleep(20 * time.Millisecond)
	cancelFirst()
	if err := <-firstErr; err == nil {
		t.Fatal("cancelled request succeeded")
	}
	if err := <-secondErr; err == nil {
		t.Fatal("second request survived the first request's cancellation; if quic-go decoupled dials, revisit decision D2")
	}
}

// D3: when the server allows one stream, a second request blocks in
// OpenStreamSync on the same connection rather than opening another.
func TestQuicGoQueuesOnPeerStreamLimit(t *testing.T) {
	s := Start(t, Options{MaxIncomingStreams: 1})
	dials := 0
	var mu sync.Mutex
	tr := &http3.Transport{
		TLSClientConfig: s.ClientTLSConfig(),
		Dial: func(ctx context.Context, addr string, tlsCfg *tls.Config, cfg *quic.Config) (*quic.Conn, error) {
			mu.Lock()
			dials++
			mu.Unlock()
			return quic.DialAddrEarly(ctx, addr, tlsCfg, cfg)
		},
	}
	t.Cleanup(func() { _ = tr.Close() })
	tracedGet(t, tr, s.URL("/warm"), &hookLog{})
	var wg sync.WaitGroup
	for i := range 3 {
		wg.Go(func() { tracedGet(t, tr, s.URL("/slow?ms=50&i="+strconv.Itoa(i)), &hookLog{}) })
	}
	wg.Wait()
	if dials != 1 {
		t.Fatalf("dials = %d, want 1: requests must queue for stream credit, not fan out", dials)
	}
}

func mustRequest(t *testing.T, method, url string, body io.Reader) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func mustRequestCtx(t *testing.T, ctx context.Context, url string) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}
