// Package h3fixture is a loopback test target that serves one handler over
// TCP (HTTP/1.1 and HTTP/2) and QUIC (HTTP/3) on the same numeric port, plus
// an optional alternate QUIC port. Deterministic switches cover Alt-Svc
// advertisements, rejected and blackholed UDP, handshake delay,
// H3_REQUEST_REJECTED, H3_VERSION_FALLBACK, partial responses, redirects,
// stream limits and connection drops. A Ledger records every application
// attempt the target saw, so tests can prove a write happened exactly once.
//
// The package is test infrastructure: only *_test.go files import it.
package h3fixture

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

// QUICState controls what UDP clients experience.
type QUICState int

const (
	// QUICServing answers QUIC normally.
	QUICServing QUICState = iota
	// QUICBlackholed keeps the socket open but drops every inbound packet.
	QUICBlackholed
	// QUICClosed closes the socket, as when nothing listens on UDP.
	QUICClosed
)

// Advertise selects the Alt-Svc header the fixture sends at start.
type Advertise int

const (
	// AdvertiseNone sends no Alt-Svc header.
	AdvertiseNone Advertise = iota
	// AdvertiseSamePort advertises h3 on the TCP port.
	AdvertiseSamePort
	// AdvertiseAltPort advertises h3 on the alternate port only.
	AdvertiseAltPort
)

// Options configure Start. The zero value serves QUIC on the TCP port without
// advertising it.
type Options struct {
	// IPv6 binds [::1] instead of 127.0.0.1.
	IPv6 bool
	// Advertise sets the initial Alt-Svc header.
	Advertise Advertise
	// AltSvcMaxAge adds "; ma=<seconds>" when positive.
	AltSvcMaxAge int
	// AlternatePort also serves QUIC on a second, randomly chosen UDP port.
	AlternatePort bool
	// QUIC is the initial UDP state of both endpoints.
	QUIC QUICState
	// HandshakeDelay delays every inbound QUIC packet.
	HandshakeDelay time.Duration
	// MaxIncomingStreams caps concurrent request streams per connection;
	// zero keeps the quic-go default.
	MaxIncomingStreams int64
}

// Server is a running fixture. Fields are read-only after Start.
type Server struct {
	Host    string
	Port    int
	AltPort int
	Certs   *Certs
	Ledger  *Ledger

	altSvc       atomic.Value // string
	rejectNext   atomic.Int64
	fallbackNext atomic.Int64

	tcp   *http.Server
	main  *quicEndpoint
	alt   *quicEndpoint
	quic  *quic.Config
	delay time.Duration

	mu     sync.Mutex
	conns  map[*quic.Conn]struct{}
	closed bool
	wg     sync.WaitGroup
}

type quicEndpoint struct {
	name string
	port int

	mu    sync.Mutex
	state QUICState
	gated *gatedConn
	tr    *quic.Transport
	ln    *quic.Listener
	h3    *http3.Server
}

// Start launches a fixture and registers its shutdown with tb.Cleanup.
func Start(tb testing.TB, opts Options) *Server {
	tb.Helper()
	s, err := start(opts)
	if err != nil {
		tb.Fatalf("h3fixture: %v", err)
	}
	tb.Cleanup(func() {
		if err := s.Close(); err != nil {
			tb.Errorf("h3fixture: close: %v", err)
		}
	})
	return s
}

func start(opts Options) (*Server, error) {
	certs, err := newCerts()
	if err != nil {
		return nil, err
	}
	host := "127.0.0.1"
	if opts.IPv6 {
		host = "::1"
	}
	s := &Server{
		Host:   host,
		Certs:  certs,
		Ledger: newLedger(),
		conns:  map[*quic.Conn]struct{}{},
		delay:  opts.HandshakeDelay,
		quic: &quic.Config{
			MaxIncomingStreams: opts.MaxIncomingStreams,
			MaxIdleTimeout:     30 * time.Second,
		},
	}
	s.altSvc.Store("")

	tcpLn, udp, err := listenPair(host)
	if err != nil {
		return nil, err
	}
	s.Port = tcpLn.Addr().(*net.TCPAddr).Port
	s.tcp = &http.Server{
		Handler:           s.handler("tcp"),
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          log.New(io.Discard, "", 0),
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{certs.Leaf},
			NextProtos:   []string{"h2", "http/1.1"},
			MinVersion:   tls.VersionTLS12,
		},
	}
	s.wg.Go(func() {
		_ = s.tcp.ServeTLS(tcpLn, "", "")
	})

	s.main = &quicEndpoint{name: "quic", port: s.Port}
	if err := s.openEndpoint(s.main, udp); err != nil {
		_ = s.Close()
		return nil, err
	}
	if opts.AlternatePort {
		altUDP, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP(host)})
		if err != nil {
			_ = s.Close()
			return nil, fmt.Errorf("listen alternate UDP: %w", err)
		}
		s.AltPort = altUDP.LocalAddr().(*net.UDPAddr).Port
		s.alt = &quicEndpoint{name: "quic-alt", port: s.AltPort}
		if err := s.openEndpoint(s.alt, altUDP); err != nil {
			_ = s.Close()
			return nil, err
		}
	}
	switch opts.Advertise {
	case AdvertiseSamePort:
		s.SetAltSvc(s.AltSvcFor(s.Port, opts.AltSvcMaxAge))
	case AdvertiseAltPort:
		if s.AltPort == 0 {
			_ = s.Close()
			return nil, errors.New("AdvertiseAltPort requires AlternatePort")
		}
		s.SetAltSvc(s.AltSvcFor(s.AltPort, opts.AltSvcMaxAge))
	}
	if opts.QUIC != QUICServing {
		if err := s.SetQUICState(opts.QUIC); err != nil {
			_ = s.Close()
			return nil, err
		}
	}
	return s, nil
}

// listenPair binds TCP and UDP to the same numeric port.
func listenPair(host string) (net.Listener, *net.UDPConn, error) {
	var lastErr error
	for range 32 {
		tcpLn, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
		if err != nil {
			return nil, nil, fmt.Errorf("listen TCP: %w", err)
		}
		port := tcpLn.Addr().(*net.TCPAddr).Port
		udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP(host), Port: port})
		if err == nil {
			return tcpLn, udp, nil
		}
		lastErr = err
		_ = tcpLn.Close()
	}
	return nil, nil, fmt.Errorf("bind TCP and UDP to one port: %w", lastErr)
}

func (s *Server) openEndpoint(ep *quicEndpoint, udp *net.UDPConn) error {
	// The gatedConn wrapper hides the *net.UDPConn from quic-go, which then
	// cannot enlarge the socket buffers itself; bursts of handshakes would
	// otherwise drop packets at the fixture and look like target loss.
	_ = udp.SetReadBuffer(4 << 20)
	_ = udp.SetWriteBuffer(4 << 20)
	gated := &gatedConn{PacketConn: udp}
	gated.delay.Store(int64(s.delay))
	tr := &quic.Transport{Conn: gated}
	tlsConf := http3.ConfigureTLSConfig(&tls.Config{
		Certificates: []tls.Certificate{s.Certs.Leaf},
		MinVersion:   tls.VersionTLS13,
	})
	ln, err := tr.Listen(tlsConf, s.quic)
	if err != nil {
		_ = tr.Close()
		_ = udp.Close()
		return fmt.Errorf("listen QUIC on %s: %w", ep.name, err)
	}
	h3 := &http3.Server{
		Handler:    s.handler(ep.name),
		TLSConfig:  tlsConf,
		QUICConfig: s.quic,
	}
	ep.mu.Lock()
	ep.state, ep.gated, ep.tr, ep.ln, ep.h3 = QUICServing, gated, tr, ln, h3
	ep.mu.Unlock()
	s.wg.Go(func() { s.acceptLoop(ln, h3) })
	return nil
}

func (s *Server) acceptLoop(ln *quic.Listener, h3 *http3.Server) {
	for {
		conn, err := ln.Accept(context.Background())
		if err != nil {
			return
		}
		if !s.track(conn) {
			_ = conn.CloseWithError(quic.ApplicationErrorCode(http3.ErrCodeNoError), "fixture closed")
			return
		}
		s.wg.Go(func() {
			defer s.untrack(conn)
			s.serveConn(conn, h3)
		})
	}
}

// serveConn runs the HTTP/3 exchange itself instead of http3.Server.Serve so
// it can refuse a request stream before any response header is written,
// which is what H3_REQUEST_REJECTED and H3_VERSION_FALLBACK require.
func (s *Server) serveConn(conn *quic.Conn, h3 *http3.Server) {
	hconn, err := h3.NewRawServerConn(conn)
	if err != nil {
		_ = conn.CloseWithError(quic.ApplicationErrorCode(http3.ErrCodeInternalError), "")
		return
	}
	var streams sync.WaitGroup
	streams.Go(func() {
		for {
			str, err := conn.AcceptUniStream(context.Background())
			if err != nil {
				return
			}
			streams.Go(func() { hconn.HandleUnidirectionalStream(str) })
		}
	})
	for {
		str, err := conn.AcceptStream(context.Background())
		if err != nil {
			break
		}
		if code, refuse := s.refusal(); refuse {
			str.CancelRead(quic.StreamErrorCode(code))
			str.CancelWrite(quic.StreamErrorCode(code))
			continue
		}
		streams.Go(func() { hconn.HandleRequestStream(str) })
	}
	streams.Wait()
}

func (s *Server) refusal() (http3.ErrCode, bool) {
	if take(&s.rejectNext) {
		return http3.ErrCodeRequestRejected, true
	}
	if take(&s.fallbackNext) {
		return http3.ErrCodeVersionFallback, true
	}
	return 0, false
}

func take(counter *atomic.Int64) bool {
	for {
		n := counter.Load()
		if n <= 0 {
			return false
		}
		if counter.CompareAndSwap(n, n-1) {
			return true
		}
	}
}

func (s *Server) track(conn *quic.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.conns[conn] = struct{}{}
	return true
}

func (s *Server) untrack(conn *quic.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.conns, conn)
}

// URL returns an https URL for path on the fixture's IP literal.
func (s *Server) URL(path string) string {
	return "https://" + net.JoinHostPort(s.Host, strconv.Itoa(s.Port)) + path
}

// LocalhostURL returns an https URL for path using the localhost name.
func (s *Server) LocalhostURL(path string) string {
	return "https://" + net.JoinHostPort("localhost", strconv.Itoa(s.Port)) + path
}

// AltSvcFor renders an h3 Alt-Svc value for port on the same host.
func (s *Server) AltSvcFor(port, maxAge int) string {
	value := fmt.Sprintf(`h3=":%d"`, port)
	if maxAge > 0 {
		value += fmt.Sprintf("; ma=%d", maxAge)
	}
	return value
}

// SetAltSvc replaces the Alt-Svc header on every later response; "" removes it.
func (s *Server) SetAltSvc(value string) { s.altSvc.Store(value) }

// RejectNextQUICRequests refuses the next n HTTP/3 request streams with
// H3_REQUEST_REJECTED before reading them.
func (s *Server) RejectNextQUICRequests(n int) { s.rejectNext.Store(int64(n)) }

// VersionFallbackNextQUICRequests refuses the next n HTTP/3 request streams
// with H3_VERSION_FALLBACK before reading them.
func (s *Server) VersionFallbackNextQUICRequests(n int) { s.fallbackNext.Store(int64(n)) }

// SetHandshakeDelay delays every later inbound QUIC packet by d.
func (s *Server) SetHandshakeDelay(d time.Duration) {
	for _, ep := range s.endpoints() {
		ep.mu.Lock()
		if ep.gated != nil {
			ep.gated.delay.Store(int64(d))
		}
		ep.mu.Unlock()
	}
}

// SetQUICState switches both QUIC endpoints. Moving from QUICClosed back to
// QUICServing rebinds the original ports.
func (s *Server) SetQUICState(state QUICState) error {
	for _, ep := range s.endpoints() {
		if err := s.setEndpointState(ep, state); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) setEndpointState(ep *quicEndpoint, state QUICState) error {
	ep.mu.Lock()
	current := ep.state
	switch {
	case state == QUICClosed && current != QUICClosed:
		ep.state = QUICClosed
		ln, tr := ep.ln, ep.tr
		ep.ln, ep.tr, ep.gated = nil, nil, nil
		ep.mu.Unlock()
		if ln != nil {
			_ = ln.Close()
		}
		s.closeConnsOn(ep.port)
		if tr != nil {
			_ = tr.Close()
			_ = tr.Conn.Close()
		}
		return nil
	case state != QUICClosed && current == QUICClosed:
		ep.mu.Unlock()
		udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP(s.Host), Port: ep.port})
		if err != nil {
			return fmt.Errorf("rebind %s UDP port %d: %w", ep.name, ep.port, err)
		}
		if err := s.openEndpoint(ep, udp); err != nil {
			return err
		}
		ep.mu.Lock()
	}
	ep.state = state
	if ep.gated != nil {
		ep.gated.blackhole.Store(state == QUICBlackholed)
	}
	ep.mu.Unlock()
	return nil
}

// CloseQUICConnections drops every open QUIC connection with H3_NO_ERROR, as
// a server restart or idle reaping would.
func (s *Server) CloseQUICConnections() { s.closeConnsOn(0) }

func (s *Server) closeConnsOn(port int) {
	s.mu.Lock()
	conns := make([]*quic.Conn, 0, len(s.conns))
	for conn := range s.conns {
		if port == 0 || conn.LocalAddr().(*net.UDPAddr).Port == port {
			conns = append(conns, conn)
		}
	}
	s.mu.Unlock()
	for _, conn := range conns {
		_ = conn.CloseWithError(quic.ApplicationErrorCode(http3.ErrCodeNoError), "fixture dropped connection")
	}
}

// QUICConnections reports how many QUIC connections are open.
func (s *Server) QUICConnections() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.conns)
}

// ClientTLSConfig trusts only the fixture CA.
func (s *Server) ClientTLSConfig() *tls.Config {
	return &tls.Config{RootCAs: s.Certs.CAPool, MinVersion: tls.VersionTLS12}
}

func (s *Server) endpoints() []*quicEndpoint {
	eps := []*quicEndpoint{}
	if s.main != nil {
		eps = append(eps, s.main)
	}
	if s.alt != nil {
		eps = append(eps, s.alt)
	}
	return eps
}

// Close stops every listener and connection and waits, bounded, for the
// fixture's goroutines to exit.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()

	if s.tcp != nil {
		_ = s.tcp.Close()
	}
	for _, ep := range s.endpoints() {
		ep.mu.Lock()
		ln, tr := ep.ln, ep.tr
		ep.ln, ep.tr, ep.gated, ep.state = nil, nil, nil, QUICClosed
		ep.mu.Unlock()
		if ln != nil {
			_ = ln.Close()
		}
		if tr != nil {
			_ = tr.Close()
			_ = tr.Conn.Close()
		}
	}
	s.closeConnsOn(0)
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-time.After(5 * time.Second):
		return errors.New("fixture goroutines did not stop within 5s")
	}
}
