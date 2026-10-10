// Package h3transport owns one VUser's HTTP/3 connections. It is the only
// package outside tests that imports quic-go, so a library upgrade or swap
// touches nothing else.
//
// The transport follows decision records D1-D4 in docs/HTTP3_IMPLEMENTATION.md:
// one UDP socket per local address and IP family, opened lazily and closed at
// teardown (D3); a dial detached from the request that triggered it and bounded
// by the connect timeout and the VUser's lifetime (D2); DNS and the handshake
// timed inside APEX's own dial (D1); and full handshakes only, never 0-RTT (D4).
package h3transport

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/netip"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

// Certainty says whether a failed request may have reached the target. It
// decides what a caller may safely do next: only a request that was certainly
// not sent, or that the server rejected unprocessed, may run again.
type Certainty string

const (
	// NotSent: the request never left this process (resolution, dial,
	// handshake or stream-open failure).
	NotSent Certainty = "not_sent"
	// PossiblySent: the request headers were written; the target may have
	// processed it.
	PossiblySent Certainty = "possibly_sent"
	// RejectedUnprocessed: the server reset the stream with
	// H3_REQUEST_REJECTED, which guarantees no processing (RFC 9114 §4.1.1).
	RejectedUnprocessed Certainty = "rejected_unprocessed"
)

// ErrorClass categorizes a failure for the receipt. It describes what the
// transport observed, never a cause it cannot see: a timeout is not reported
// as "UDP blocked".
type ErrorClass string

const (
	ClassTimeout            ErrorClass = "timeout"
	ClassHandshake          ErrorClass = "handshake"
	ClassVersionNegotiation ErrorClass = "version_negotiation"
	ClassConnectionClosed   ErrorClass = "connection_closed"
	ClassRequestRejected    ErrorClass = "request_rejected"
	ClassPossiblySent       ErrorClass = "possibly_sent"
)

// Error is a failed HTTP/3 request with its transmission certainty.
type Error struct {
	Certainty Certainty
	Class     ErrorClass
	Err       error
}

func (e *Error) Error() string { return string(e.Certainty) + ": " + e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

// Limits are installation-wide QUIC controls (APEXLOAD_QUIC_*). Zero values
// keep quic-go's defaults.
type Limits struct {
	InitialStreamReceiveWindow     uint64
	MaxStreamReceiveWindow         uint64
	InitialConnectionReceiveWindow uint64
	MaxConnectionReceiveWindow     uint64
	MaxResponseHeaderBytes         int64
	InitialPacketSize              uint16
	DisablePathMTUDiscovery        bool
}

// Options configure one VUser's transport.
type Options struct {
	// TLS carries the session's certificate settings (client certificate,
	// CA pool, verification). It is cloned and pinned to TLS 1.3; the server
	// name comes from each request's origin host.
	TLS *tls.Config
	// HandshakeTimeout, MaxIdleTimeout and KeepAlive come from
	// config.http.quic.
	HandshakeTimeout time.Duration
	MaxIdleTimeout   time.Duration
	KeepAlive        time.Duration
	Limits           Limits
	// ConnectTimeout bounds DNS plus the handshake of one dial.
	ConnectTimeout time.Duration
	// Lifetime ends every pending dial when the VUser stops.
	Lifetime context.Context
	// Resolve returns a host's addresses in preference order, applying the
	// session's DNS cache. Nil uses Go's resolver.
	Resolve func(ctx context.Context, host string) ([]netip.Addr, error)
	// AuthorizeHost applies the target allow-list at dial time, so a host
	// that reached the dial by any route is still checked.
	AuthorizeHost func(host string) error
	// SourceIP binds the UDP sockets to the VUser's assigned client address.
	SourceIP net.IP
	// IPVersion restricts addresses to "ipv4" or "ipv6"; empty allows both.
	IPVersion string
	// CheckAddress vets every candidate address, literal or resolved, before
	// it is dialed; a refused address is skipped. Nil allows all.
	CheckAddress func(netip.Addr) error
	// Acquire takes a slot in the VUser's connection budget for address
	// ("host:port") before a dial, waiting while the budget is full. The
	// returned release runs when the connection closes or the dial fails.
	Acquire func(ctx context.Context, address string) (release func(), err error)
}

// Transport is an http.RoundTripper for exact HTTP/3.
type Transport struct {
	opts    Options
	h3      *http3.Transport
	session tls.ClientSessionCache
	tokens  quic.TokenStore

	mu      sync.Mutex
	closed  bool
	sockets map[socketKey]*quic.Transport
	conns   map[*quic.Conn]struct{}
	// active counts requests whose response body is still open. quic-go
	// treats a connection as unused once headers arrive, so closing idle
	// connections waits until no body is in flight (SSE included).
	active atomic.Int64
}

type socketKey struct {
	local string
	v6    bool
}

// New returns a transport. No socket is opened until the first request.
func New(opts Options) (*Transport, error) {
	if opts.Lifetime == nil {
		opts.Lifetime = context.Background()
	}
	tlsConfig := &tls.Config{}
	if opts.TLS != nil {
		tlsConfig = opts.TLS.Clone()
	}
	if tlsConfig.MaxVersion != 0 && tlsConfig.MaxVersion < tls.VersionTLS13 {
		return nil, errors.New("HTTP/3 requires TLS 1.3, but the TLS maximum is lower")
	}
	tlsConfig.MinVersion = tls.VersionTLS13
	// Session tickets stay inside this VUser: its cache is never shared.
	session := tls.NewLRUClientSessionCache(64)
	tlsConfig.ClientSessionCache = session
	t := &Transport{
		opts:    opts,
		session: session,
		tokens:  quic.NewLRUTokenStore(16, 4),
		sockets: map[socketKey]*quic.Transport{},
		conns:   map[*quic.Conn]struct{}{},
	}
	quicConfig := &quic.Config{
		HandshakeIdleTimeout:           opts.HandshakeTimeout,
		MaxIdleTimeout:                 opts.MaxIdleTimeout,
		KeepAlivePeriod:                opts.KeepAlive,
		InitialStreamReceiveWindow:     opts.Limits.InitialStreamReceiveWindow,
		MaxStreamReceiveWindow:         opts.Limits.MaxStreamReceiveWindow,
		InitialConnectionReceiveWindow: opts.Limits.InitialConnectionReceiveWindow,
		MaxConnectionReceiveWindow:     opts.Limits.MaxConnectionReceiveWindow,
		InitialPacketSize:              opts.Limits.InitialPacketSize,
		DisablePathMTUDiscovery:        opts.Limits.DisablePathMTUDiscovery,
		TokenStore:                     t.tokens,
	}
	t.h3 = &http3.Transport{
		TLSClientConfig:        tlsConfig,
		QUICConfig:             quicConfig,
		Dial:                   t.dial,
		MaxResponseHeaderBytes: int(opts.Limits.MaxResponseHeaderBytes),
		// Authored encodings are sent exactly and decoded by the bounded
		// response pipeline, as on TCP.
		DisableCompression: true,
	}
	return t, nil
}

// RoundTrip sends one request over HTTP/3. A failure is an *Error carrying
// the request's transmission certainty, except for the caller's own
// cancellation or deadline, which is returned unchanged.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.active.Add(1)
	// quic-go may report the write from another goroutine.
	var written atomic.Bool
	ctx := httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{
		WroteHeaders: func() { written.Store(true) },
	})
	resp, err := t.h3.RoundTrip(req.WithContext(ctx))
	if err == nil {
		resp.Body = &activeBody{ReadCloser: resp.Body, done: func() { t.active.Add(-1) }}
		return resp, nil
	}
	t.active.Add(-1)
	if ctxErr := req.Context().Err(); ctxErr != nil {
		return nil, err
	}
	var h3Err *http3.Error
	switch {
	case errors.As(err, &h3Err) && h3Err.ErrorCode == http3.ErrCodeRequestRejected:
		return nil, &Error{Certainty: RejectedUnprocessed, Class: ClassRequestRejected, Err: err}
	case !written.Load():
		return nil, &Error{Certainty: NotSent, Class: classify(err), Err: err}
	default:
		return nil, &Error{Certainty: PossiblySent, Class: ClassPossiblySent, Err: err}
	}
}

// classify maps a failure before the request was written to a class.
func classify(err error) ErrorClass {
	var idle *quic.IdleTimeoutError
	var handshakeTimeout *quic.HandshakeTimeoutError
	var versions *quic.VersionNegotiationError
	var transportErr *quic.TransportError
	switch {
	case errors.As(err, &idle), errors.As(err, &handshakeTimeout), errors.Is(err, context.DeadlineExceeded):
		return ClassTimeout
	case errors.As(err, &versions):
		return ClassVersionNegotiation
	case errors.As(err, &transportErr) && transportErr.ErrorCode.IsCryptoError():
		return ClassHandshake
	default:
		return ClassConnectionClosed
	}
}

// DialInfo describes a connection this transport opened.
type DialInfo struct {
	// Resumed is set when the TLS handshake resumed an earlier session.
	Resumed bool
}

type dialObserverKey struct{}

// WithDialObserver returns a request context whose request, if it is the
// one that opens a new connection, reports that connection to observe.
func WithDialObserver(ctx context.Context, observe func(DialInfo)) context.Context {
	return context.WithValue(ctx, dialObserverKey{}, observe)
}

// CloseIdleConnections closes connections no request is using. While any
// response body is still open it closes nothing: quic-go would count that
// connection as idle.
func (t *Transport) CloseIdleConnections() {
	if t.active.Load() > 0 {
		return
	}
	t.h3.CloseIdleConnections()
}

// ActiveRequests reports requests whose response body is still open.
func (t *Transport) ActiveRequests() int64 { return t.active.Load() }

// activeBody ends a request's activity when its body reaches EOF or closes.
type activeBody struct {
	io.ReadCloser
	once sync.Once
	done func()
}

func (b *activeBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		b.once.Do(b.done)
	}
	return n, err
}

func (b *activeBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(b.done)
	return err
}

// Close ends every connection with H3_NO_ERROR and releases the sockets.
// It is safe to call more than once.
func (t *Transport) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	conns := make([]*quic.Conn, 0, len(t.conns))
	for conn := range t.conns {
		conns = append(conns, conn)
	}
	sockets := t.sockets
	t.sockets = map[socketKey]*quic.Transport{}
	t.mu.Unlock()
	// quic-go's own close uses application error 0; close first with the
	// HTTP/3 code a server expects from a clean shutdown.
	for _, conn := range conns {
		_ = conn.CloseWithError(quic.ApplicationErrorCode(http3.ErrCodeNoError), "")
	}
	errs := []error{t.h3.Close()}
	for _, socket := range sockets {
		errs = append(errs, socket.Close(), socket.Conn.Close())
	}
	return errors.Join(errs...)
}

// OpenConnections reports how many QUIC connections are open.
func (t *Transport) OpenConnections() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.conns)
}

// OpenSockets reports how many UDP sockets are open.
func (t *Transport) OpenSockets() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.sockets)
}

// dial is http3.Transport's dial hook. quic-go runs it on a context derived
// from the first request that needed the connection; it is detached from
// that request's cancellation so waiting requests are not failed by it (D2).
func (t *Transport) dial(ctx context.Context, addr string, tlsConfig *tls.Config, quicConfig *quic.Config) (*quic.Conn, error) {
	trace := httptrace.ContextClientTrace(ctx)
	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("invalid port in %q", addr)
	}
	if t.opts.AuthorizeHost != nil {
		if err := t.opts.AuthorizeHost(host); err != nil {
			return nil, err
		}
	}
	dialCtx, cancel := t.dialContext(ctx)
	defer cancel()
	release := func() {}
	if t.opts.Acquire != nil {
		if release, err = t.opts.Acquire(dialCtx, addr); err != nil {
			return nil, err
		}
	}
	conn, err := t.dialAddress(ctx, dialCtx, trace, host, port, tlsConfig, quicConfig)
	if err != nil {
		release()
		return nil, err
	}
	context.AfterFunc(conn.Context(), release)
	return conn, nil
}

func (t *Transport) dialAddress(ctx, dialCtx context.Context, trace *httptrace.ClientTrace, host string, port int, tlsConfig *tls.Config, quicConfig *quic.Config) (*quic.Conn, error) {
	addresses, err := t.resolve(dialCtx, trace, host)
	if err != nil {
		return nil, err
	}
	addresses = interleaveFamilies(addresses)
	remote := func(address netip.Addr) string {
		return net.JoinHostPort(address.String(), strconv.Itoa(port))
	}
	// One connect phase covers the whole dial, whichever address wins.
	if trace != nil && trace.ConnectStart != nil {
		trace.ConnectStart("udp", remote(addresses[0]))
	}
	conn, winner, err := t.raceAddresses(dialCtx, addresses, port, tlsConfig, quicConfig)
	if trace != nil && trace.ConnectDone != nil {
		trace.ConnectDone("udp", remote(winner), err)
	}
	if err != nil {
		return nil, err
	}
	if err := t.track(conn); err != nil {
		return nil, err
	}
	if observe, _ := ctx.Value(dialObserverKey{}).(func(DialInfo)); observe != nil {
		observe(DialInfo{Resumed: conn.ConnectionState().TLS.DidResume})
	}
	return conn, nil
}

// fallbackDelay is how long an address gets before the next one starts, as
// in the TCP dialer's dual-stack fallback (RFC 8305 recommends 250 ms).
const fallbackDelay = 300 * time.Millisecond

// raceAddresses dials the addresses in order, starting the next one when the
// previous fails or after fallbackDelay, so an address family that resolves
// but does not route costs a fraction of a second rather than the handshake
// timeout. The first completed handshake wins; later winners are closed.
func (t *Transport) raceAddresses(ctx context.Context, addresses []netip.Addr, port int, tlsConfig *tls.Config, quicConfig *quic.Config) (*quic.Conn, netip.Addr, error) {
	type outcome struct {
		conn    *quic.Conn
		address netip.Addr
		err     error
	}
	raceCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan outcome, len(addresses))
	start := func(address netip.Addr) {
		go func() {
			socket, err := t.socket(address.Is6())
			if err != nil {
				results <- outcome{address: address, err: err}
				return
			}
			remote := &net.UDPAddr{IP: address.AsSlice(), Port: port, Zone: address.Zone()}
			conn, err := socket.Dial(raceCtx, remote, tlsConfig, quicConfig)
			results <- outcome{conn: conn, address: address, err: err}
		}()
	}
	next, pending := 0, 0
	launch := func() {
		start(addresses[next])
		next++
		pending++
	}
	launch()
	var errs []error
	timer := time.NewTimer(fallbackDelay)
	defer timer.Stop()
	for pending > 0 {
		select {
		case result := <-results:
			pending--
			if result.err == nil {
				cancel()
				// Close any later winner once the others finish.
				go func(remaining int) {
					for range remaining {
						if late := <-results; late.conn != nil {
							_ = late.conn.CloseWithError(quic.ApplicationErrorCode(http3.ErrCodeNoError), "")
						}
					}
				}(pending)
				return result.conn, result.address, nil
			}
			errs = append(errs, result.err)
			if next < len(addresses) && ctx.Err() == nil {
				launch()
				timer.Reset(fallbackDelay)
			}
		case <-timer.C:
			if next < len(addresses) {
				launch()
				timer.Reset(fallbackDelay)
			}
		}
	}
	return nil, addresses[0], errors.Join(errs...)
}

// interleaveFamilies alternates address families, keeping the resolver's
// order within each, so the fallback reaches the other family second.
func interleaveFamilies(addresses []netip.Addr) []netip.Addr {
	var first, second []netip.Addr
	for _, address := range addresses {
		if address.Is6() == addresses[0].Is6() {
			first = append(first, address)
		} else {
			second = append(second, address)
		}
	}
	out := make([]netip.Addr, 0, len(addresses))
	for i := 0; i < len(first) || i < len(second); i++ {
		if i < len(first) {
			out = append(out, first[i])
		}
		if i < len(second) {
			out = append(out, second[i])
		}
	}
	return out
}

// dialContext detaches the dial from the triggering request and bounds it by
// the connect timeout and the VUser's lifetime instead.
func (t *Transport) dialContext(parent context.Context) (context.Context, context.CancelFunc) {
	ctx := context.WithoutCancel(parent)
	cancelTimeout := func() {}
	if t.opts.ConnectTimeout > 0 {
		ctx, cancelTimeout = context.WithTimeout(ctx, t.opts.ConnectTimeout)
	}
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(t.opts.Lifetime, cancel)
	return ctx, func() {
		stop()
		cancel()
		cancelTimeout()
	}
}

func (t *Transport) resolve(ctx context.Context, trace *httptrace.ClientTrace, host string) ([]netip.Addr, error) {
	if literal, err := netip.ParseAddr(host); err == nil {
		return t.filterFamily([]netip.Addr{literal.Unmap()}, host)
	}
	if trace != nil && trace.DNSStart != nil {
		trace.DNSStart(httptrace.DNSStartInfo{Host: host})
	}
	var addresses []netip.Addr
	var err error
	if t.opts.Resolve != nil {
		addresses, err = t.opts.Resolve(ctx, host)
	} else {
		var ips []net.IPAddr
		ips, err = net.DefaultResolver.LookupIPAddr(ctx, host)
		for _, ip := range ips {
			if address, ok := netip.AddrFromSlice(ip.IP); ok {
				addresses = append(addresses, address.Unmap().WithZone(ip.Zone))
			}
		}
	}
	if trace != nil && trace.DNSDone != nil {
		info := httptrace.DNSDoneInfo{Err: err}
		for _, address := range addresses {
			info.Addrs = append(info.Addrs, net.IPAddr{IP: address.AsSlice(), Zone: address.Zone()})
		}
		trace.DNSDone(info)
	}
	if err != nil {
		return nil, err
	}
	return t.filterFamily(addresses, host)
}

func (t *Transport) filterFamily(addresses []netip.Addr, host string) ([]netip.Addr, error) {
	var kept []netip.Addr
	var refused error
	for _, address := range addresses {
		address = address.Unmap()
		if t.opts.CheckAddress != nil {
			if err := t.opts.CheckAddress(address); err != nil {
				refused = err
				continue
			}
		}
		switch t.opts.IPVersion {
		case "ipv4":
			if !address.Is4() {
				continue
			}
		case "ipv6":
			if !address.Is6() {
				continue
			}
		}
		if t.opts.SourceIP != nil && (t.opts.SourceIP.To4() != nil) != address.Is4() {
			continue // a bound source address fixes the family
		}
		kept = append(kept, address)
	}
	if len(kept) == 0 {
		if refused != nil {
			return nil, refused
		}
		return nil, fmt.Errorf("no usable address for %s", host)
	}
	return kept, nil
}

// socket returns this VUser's UDP transport for one IP family, opening it on
// first use (D3).
func (t *Transport) socket(v6 bool) (*quic.Transport, error) {
	local := ""
	if t.opts.SourceIP != nil {
		local = t.opts.SourceIP.String()
	}
	key := socketKey{local: local, v6: v6}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil, http3.ErrTransportClosed
	}
	if socket := t.sockets[key]; socket != nil {
		return socket, nil
	}
	network := "udp4"
	if v6 {
		network = "udp6"
	}
	conn, err := net.ListenUDP(network, &net.UDPAddr{IP: t.opts.SourceIP})
	if err != nil {
		return nil, err
	}
	socket := &quic.Transport{Conn: conn}
	t.sockets[key] = socket
	return socket, nil
}

// track records an open connection until it closes, so Close can end it
// cleanly and the owner can count connections.
func (t *Transport) track(conn *quic.Conn) error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		_ = conn.CloseWithError(quic.ApplicationErrorCode(http3.ErrCodeNoError), "")
		return http3.ErrTransportClosed
	}
	t.conns[conn] = struct{}{}
	t.mu.Unlock()
	context.AfterFunc(conn.Context(), func() {
		t.mu.Lock()
		delete(t.conns, conn)
		t.mu.Unlock()
	})
	return nil
}
