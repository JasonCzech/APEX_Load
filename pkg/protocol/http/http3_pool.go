package http

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	stdhttp "net/http"
	"net/netip"
	"net/url"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/netpolicy"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/protocol/http/internal/h3transport"
)

var (
	// ErrHTTP3Required prefixes failures of an exact-3 request. There is no
	// TCP fallback in that mode.
	ErrHTTP3Required = errors.New("http version 3 is required")
	// ErrHTTP3ProxyRoute refuses an exact-3 request whose effective route is
	// a proxy: proxies carry TCP only.
	ErrHTTP3ProxyRoute = errors.New("http3_proxy_unsupported: the request's route is a proxy, which carries TCP only")
	// ErrHTTP3IntegratedAuth refuses an exact-3 request that would use NTLM or
	// Negotiate, whose handshakes are bound to one TCP connection.
	ErrHTTP3IntegratedAuth = errors.New("integrated authentication (NTLM or Negotiate) runs on TCP only; use auto+h3 or another auth type for this request")
	// ErrHTTP3AttemptBudget ends an exact-3 action that used up its attempts:
	// its redirect hops plus one library retry.
	ErrHTTP3AttemptBudget = errors.New("attempt budget exhausted: the action used every HTTP attempt it allows")
)

// HTTP3FailureCertainty returns the transmission certainty of a failed
// exact-3 request: not_sent, possibly_sent or rejected_unprocessed, or "".
func HTTP3FailureCertainty(err error) string {
	var h3Err *h3transport.Error
	if errors.As(err, &h3Err) {
		return string(h3Err.Certainty)
	}
	return ""
}

// http3Owner holds one VUser's HTTP/3 transport. It creates the transport on
// the first request, so a VUser that never uses HTTP/3 opens no UDP socket,
// and recreates it after Close if the session runs again.
type http3Owner struct {
	options h3transport.Options
	stop    context.CancelFunc

	mu        sync.Mutex
	transport *h3transport.Transport
}

func newHTTP3Owner(options h3transport.Options) *http3Owner {
	lifetime, stop := context.WithCancel(context.Background())
	options.Lifetime = lifetime
	return &http3Owner{options: options, stop: stop}
}

func (o *http3Owner) current() (*h3transport.Transport, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.transport == nil {
		if o.options.Lifetime.Err() != nil {
			lifetime, stop := context.WithCancel(context.Background())
			o.options.Lifetime, o.stop = lifetime, stop
		}
		transport, err := h3transport.New(o.options)
		if err != nil {
			return nil, err
		}
		o.transport = transport
	}
	return o.transport, nil
}

func (o *http3Owner) RoundTrip(req *stdhttp.Request) (*stdhttp.Response, error) {
	transport, err := o.current()
	if err != nil {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, err
	}
	return transport.RoundTrip(req)
}

// openConnections reports open QUIC connections, without creating the
// transport.
func (o *http3Owner) openConnections() int64 {
	o.mu.Lock()
	transport := o.transport
	o.mu.Unlock()
	if transport == nil {
		return 0
	}
	return int64(transport.OpenConnections())
}

func (o *http3Owner) CloseIdleConnections() {
	o.mu.Lock()
	transport := o.transport
	o.mu.Unlock()
	if transport != nil {
		transport.CloseIdleConnections()
	}
}

// Close ends pending dials, closes every connection with H3_NO_ERROR and
// releases the sockets.
func (o *http3Owner) Close() error {
	o.mu.Lock()
	transport := o.transport
	o.transport = nil
	stop := o.stop
	o.mu.Unlock()
	stop()
	if transport == nil {
		return nil
	}
	return transport.Close()
}

// http3RouteRoundTripper keeps exact-3 requests off proxied routes. The
// route is decided per request, after PAC or proxy rules run, so a PAC or
// no_proxy DIRECT route keeps HTTP/3.
type http3RouteRoundTripper struct {
	base    stdhttp.RoundTripper
	proxy   func(*stdhttp.Request) (*url.URL, error)
	pac     string
	allowed []string
}

func (rt http3RouteRoundTripper) RoundTrip(req *stdhttp.Request) (*stdhttp.Response, error) {
	if err := rt.directRoute(req); err != nil {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, err
	}
	return rt.base.RoundTrip(req)
}

func (rt http3RouteRoundTripper) directRoute(req *stdhttp.Request) error {
	if rt.pac != "" {
		value, err := evaluatePAC(req.Context(), rt.pac, req.URL, rt.allowed)
		if err != nil {
			return err
		}
		routes, err := parsePACRoutes(value, rt.allowed)
		if err != nil {
			return err
		}
		if len(routes) > 0 && routes[0] != nil {
			return ErrHTTP3ProxyRoute
		}
		return nil
	}
	if rt.proxy == nil {
		return nil
	}
	route, err := rt.proxy(req)
	if err != nil {
		return err
	}
	if route != nil {
		return ErrHTTP3ProxyRoute
	}
	return nil
}

// newHTTP3Pool builds the exact-3 pool. It shares the session's cookie jar,
// redirect policy, target policy, run correlation and user-agent handling
// with the TCP pools; only the transport differs. OAuth token calls stay on
// the hardened TCP client of the automatic pool.
func (h *HTTP) newHTTP3Pool(settings transportConfig, tlsConfig *tls.Config, config map[string]any, baseline *stdhttp.Transport, pacSource string, auto *httpPolicyPool, jar stdhttp.CookieJar) (*httpPolicyPool, error) {
	limits, err := quicOperatorLimits()
	if err != nil {
		return nil, err
	}
	options := h3transport.Options{
		TLS:              tlsConfig,
		HandshakeTimeout: h.quicSettings.HandshakeTimeout,
		MaxIdleTimeout:   h.quicSettings.MaxIdleTimeout,
		KeepAlive:        h.quicSettings.KeepAlive,
		Limits: h3transport.Limits{
			InitialStreamReceiveWindow:     limits.InitialStreamReceiveWindow,
			MaxStreamReceiveWindow:         limits.MaxStreamReceiveWindow,
			InitialConnectionReceiveWindow: limits.InitialConnectionReceiveWindow,
			MaxConnectionReceiveWindow:     limits.MaxConnectionReceiveWindow,
			MaxResponseHeaderBytes:         limits.MaxResponseHeaderBytes,
			InitialPacketSize:              limits.InitialPacketSize,
			DisablePathMTUDiscovery:        limits.DisablePathMTUDiscovery,
		},
		ConnectTimeout: settings.connectTimeout,
		SourceIP:       settings.sourceIP,
		IPVersion:      settings.ipVersion,
	}
	if tlsMaximumBelow13(tlsConfig) {
		return nil, fmt.Errorf("%w: HTTP/3 requires TLS 1.3, but the TLS maximum is lower", ErrHTTP3Required)
	}
	if len(h.allowedTargetHosts) > 0 {
		allowed := h.allowedTargetHosts
		options.AuthorizeHost = func(host string) error { return protocol.ValidateTargetHost(host, allowed) }
	}
	if enabled, _ := config["dns_cache"].(bool); enabled {
		options.Resolve = newCachedResolver(net.DefaultResolver.LookupIPAddr, time.Now)
	}
	if pacSource != "" && len(h.allowedTargetHosts) == 0 {
		// As on TCP, a PAC DIRECT route reaches only public addresses unless
		// an explicit target policy authorizes private ones.
		options.CheckAddress = func(address netip.Addr) error {
			if !netpolicy.IsPublicIP(net.IP(address.AsSlice())) {
				return fmt.Errorf("PAC access to non-public address requires an explicit target policy")
			}
			return nil
		}
	}
	if h.connections != nil {
		// QUIC connections share the VUser's per-host connection budget with
		// TCP sockets (max_conns_per_host, max_idle_conns).
		options.Acquire = h.connections.acquireQUIC
	}
	h.http3 = newHTTP3Owner(options)
	if h.connections != nil {
		owner := h.http3
		h.connections.trackQUIC(&quicBudgetMember{closeIdle: owner.CloseIdleConnections, open: owner.openConnections})
	}
	h.http3Route = http3RouteRoundTripper{proxy: baseline.Proxy, pac: pacSource, allowed: h.allowedTargetHosts}
	route := h.http3Route
	route.base = h.http3
	var transport stdhttp.RoundTripper = route
	client := &stdhttp.Client{Transport: transport, Jar: jar, Timeout: h.requestTimeout}
	applyRedirectPolicy(client, h.maxRedirects)
	if len(h.allowedTargetHosts) > 0 {
		client.Transport = protocol.NewTargetPolicyRoundTripper(client.Transport, h.allowedTargetHosts)
	}
	client.Transport = withoutImplicitUserAgent(client.Transport)
	if h.runCorrelation.enabled() {
		client.Transport = runCorrelationRoundTripper{base: client.Transport, policy: h.runCorrelation}
	}
	// The automatic pool's TCP transport serves its TLS settings (WebSocket
	// steps); requests on this pool never use it.
	return &httpPolicyPool{policy: VersionHTTP3, client: client, base: auto}, nil
}

// newCachedResolver mirrors the TCP DNS cache for QUIC dials: per VUser,
// 60-second entries, at most 256 hosts and 16 addresses each, and failures
// never cached.
func newCachedResolver(lookup func(context.Context, string) ([]net.IPAddr, error), now func() time.Time) func(context.Context, string) ([]netip.Addr, error) {
	type entry struct {
		addresses []netip.Addr
		expires   time.Time
	}
	var mu sync.Mutex
	cache := map[string]entry{}
	return func(ctx context.Context, host string) ([]netip.Addr, error) {
		mu.Lock()
		hit, ok := cache[host]
		mu.Unlock()
		if ok && now().Before(hit.expires) {
			return append([]netip.Addr(nil), hit.addresses...), nil
		}
		ips, err := lookup(ctx, host)
		if err != nil {
			return nil, err
		}
		var addresses []netip.Addr
		for _, ip := range ips {
			if address, ok := netip.AddrFromSlice(ip.IP); ok {
				addresses = append(addresses, address.Unmap().WithZone(ip.Zone))
			}
			if len(addresses) == 16 {
				break
			}
		}
		if len(addresses) == 0 {
			return nil, fmt.Errorf("DNS returned no addresses")
		}
		mu.Lock()
		if len(cache) >= 256 {
			for key, value := range cache {
				if !now().Before(value.expires) {
					delete(cache, key)
				}
			}
			for key := range cache {
				if len(cache) < 256 {
					break
				}
				delete(cache, key)
			}
		}
		cache[host] = entry{addresses: addresses, expires: now().Add(60 * time.Second)}
		mu.Unlock()
		return append([]netip.Addr(nil), addresses...), nil
	}
}

var (
	quicLimitsOnce   sync.Once
	quicLimitsCached QUICOperatorLimits
	quicLimitsErr    error
)

// quicOperatorLimits reads the APEXLOAD_QUIC_* environment once per process.
func quicOperatorLimits() (QUICOperatorLimits, error) {
	quicLimitsOnce.Do(func() { quicLimitsCached, quicLimitsErr = LoadQUICOperatorLimits(nil) })
	return quicLimitsCached, quicLimitsErr
}

// ValidateHTTP3OperatorEnvironment checks APEXLOAD_HTTP3 and the
// APEXLOAD_QUIC_* variables at process start, so a misconfigured controller
// or generator fails before it takes work. Without the flag the QUIC
// variables are unused and not checked.
func ValidateHTTP3OperatorEnvironment() error {
	if !HTTP3EnabledByOperator() {
		return nil
	}
	_, err := quicOperatorLimits()
	return err
}

func tlsMaximumBelow13(tlsConfig *tls.Config) bool {
	return tlsConfig != nil && tlsConfig.MaxVersion != 0 && tlsConfig.MaxVersion < tls.VersionTLS13
}

// newUpgradePool builds the auto+h3 pool: the router over the automatic and
// HTTP/1.1 TCP pools and the VUser's QUIC transport, with the same wrappers
// as the TCP pools (target policy, user agent, integrated auth, run
// correlation). Integrated-auth legs are routed to TCP by the router. quic is
// nil only when tlsBelow keeps every request on TCP.
func (h *HTTP) newUpgradePool(quic stdhttp.RoundTripper, tlsBelow bool, baseline *stdhttp.Transport, pacSource string, auto, http11 *httpPolicyPool, jar stdhttp.CookieJar) *httpPolicyPool {
	h.altSvc = newAltSvcCache(time.Now)
	router := &upgradeRoundTripper{
		tcp:       tcpPoolRoundTripper{pool: auto},
		http11:    tcpPoolRoundTripper{pool: http11},
		h3:        quic,
		cache:     h.altSvc,
		route:     http3RouteRoundTripper{proxy: baseline.Proxy, pac: pacSource, allowed: h.allowedTargetHosts},
		tlsBelow:  tlsBelow || quic == nil,
		brokenTTL: h.quicSettings.BrokenTTL,
	}
	client := &stdhttp.Client{Transport: router, Jar: jar, Timeout: h.requestTimeout}
	applyRedirectPolicy(client, h.maxRedirects)
	if len(h.allowedTargetHosts) > 0 {
		client.Transport = protocol.NewTargetPolicyRoundTripper(client.Transport, h.allowedTargetHosts)
	}
	client.Transport = withoutImplicitUserAgent(client.Transport)
	client.Transport = integratedAuthRoundTripper{base: client.Transport}
	if h.runCorrelation.enabled() {
		client.Transport = runCorrelationRoundTripper{base: client.Transport, policy: h.runCorrelation}
	}
	return &httpPolicyPool{policy: VersionAutoH3, client: client, base: auto}
}
