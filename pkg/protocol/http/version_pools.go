package http

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	stdhttp "net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// httpPolicyPool is the client of one HTTP version policy. A TCP pool
// builds its transport, and its hardened OAuth client, on first use: each is
// a net/http Transport with its own HTTP/2 state, and most VUsers never leave
// their session's policy or fetch a token. HTTP/3 pools have a client of
// their own and take their TCP transport and OAuth client from base, the
// automatic pool.
type httpPolicyPool struct {
	policy  string
	builder *httpPoolBuilder
	base    *httpPolicyPool

	once      sync.Once
	client    *stdhttp.Client
	transport *stdhttp.Transport
	// tcpBase is the TCP transport below the shared wrappers, which the
	// auto+h3 router uses with its own copy of those wrappers.
	tcpBase stdhttp.RoundTripper

	oauthOnce sync.Once
	oauth     *httpOAuthClient
}

type httpOAuthClient struct {
	client    *stdhttp.Client
	transport *stdhttp.Transport
}

// built returns the pool, building a TCP pool on first use.
func (p *httpPolicyPool) built() *httpPolicyPool {
	if p.builder != nil {
		p.once.Do(func() { p.client, p.transport, p.tcpBase = p.builder.buildTCP(p.policy) })
	}
	return p
}

func (p *httpPolicyPool) httpClient() *stdhttp.Client {
	return p.built().client
}

func (p *httpPolicyPool) httpTransport() *stdhttp.Transport {
	if p.base != nil {
		return p.base.httpTransport()
	}
	return p.built().transport
}

// oauthClient returns the pool's hardened OAuth client, building it on first
// use.
func (p *httpPolicyPool) oauthClient() *httpOAuthClient {
	if p.base != nil {
		return p.base.oauthClient()
	}
	p.oauthOnce.Do(func() { p.oauth = p.builder.buildOAuth(p.policy) })
	return p.oauth
}

// tcpPoolRoundTripper sends through a TCP pool's tcpBase, building the pool
// on first use.
type tcpPoolRoundTripper struct{ pool *httpPolicyPool }

func (rt tcpPoolRoundTripper) RoundTrip(request *stdhttp.Request) (*stdhttp.Response, error) {
	return rt.pool.built().tcpBase.RoundTrip(request)
}

// httpPoolBuilder holds what a VUser's TCP pools are built from. Every value
// is fixed when Init returns; the session TLS configuration is cloned for
// each transport and never modified.
type httpPoolBuilder struct {
	settings       transportConfig
	tlsConfig      *tls.Config
	proxy          func(*stdhttp.Request) (*url.URL, error)
	dial           func(context.Context, string, string) (net.Conn, error)
	jar            stdhttp.CookieJar
	requestTimeout time.Duration
	maxRedirects   int
	pacSource      string
	allowed        []string
	connections    *httpConnectionBudget
	runCorrelation runCorrelationPolicy
}

// newClient returns policy's private transport, not yet tracked by the
// connection budget, in a client with the session's jar, timeout and
// redirect policy.
func (b *httpPoolBuilder) newClient(policy string) (*stdhttp.Client, *stdhttp.Transport) {
	transport := newTransport(b.settings, b.tlsConfig.Clone())
	transport.Proxy = b.proxy
	transport.DialContext = b.dial
	selected := policy
	if selected == "auto" {
		selected = ""
	}
	// The three TCP policies are valid versions, so this cannot fail.
	_, _ = applyHTTPVersion(transport, map[string]any{"http_version": selected})
	client := &stdhttp.Client{Transport: transport, Jar: b.jar, Timeout: b.requestTimeout}
	applyRedirectPolicy(client, b.maxRedirects)
	return client, transport
}

// route puts policy's frozen transport under the connection budget and the
// session's PAC rule.
func (b *httpPoolBuilder) route(transport *stdhttp.Transport, policy string) stdhttp.RoundTripper {
	freezePolicyALPN(transport, policy)
	var routed stdhttp.RoundTripper = b.connections.track(transport, b.dial)
	if b.pacSource != "" {
		routed = &pacRoundTripper{source: b.pacSource, allowed: b.allowed, base: transport, budget: b.connections, baseDial: b.dial}
	}
	return routed
}

// guard applies the session's target policy and user-agent handling.
func (b *httpPoolBuilder) guard(next stdhttp.RoundTripper) stdhttp.RoundTripper {
	if len(b.allowed) > 0 {
		next = protocol.NewTargetPolicyRoundTripper(next, b.allowed)
	}
	return withoutImplicitUserAgent(next)
}

func (b *httpPoolBuilder) buildTCP(policy string) (*stdhttp.Client, *stdhttp.Transport, stdhttp.RoundTripper) {
	client, transport := b.newClient(policy)
	tcpBase := b.route(transport, policy)
	client.Transport = integratedAuthRoundTripper{base: b.guard(tcpBase)}
	if b.runCorrelation.enabled() {
		client.Transport = runCorrelationRoundTripper{base: client.Transport, policy: b.runCorrelation}
	}
	return client, transport, tcpBase
}

// buildOAuth builds policy's hardened OAuth client on a transport of its own:
// a copy of the pool's live transport would carry its connection-budget
// dialer.
func (b *httpPoolBuilder) buildOAuth(policy string) *httpOAuthClient {
	base, transport := b.newClient(policy)
	client := oauthRequestClient(base)
	hardenOAuthTransport(transport)
	client.Transport = b.guard(b.route(transport, policy))
	return &httpOAuthClient{client: client, transport: transport}
}

type httpVersionCacheKey struct{}

func (h *HTTP) initializeHTTPPolicyPools(ctx context.Context, config map[string]any, settings transportConfig, tlsConfig *tls.Config) error {
	baseline := newTransport(settings, tlsConfig)
	if enabled, _ := config["dns_cache"].(bool); enabled {
		baseline.DialContext = familyDialContext(cachedDNSDialer(baseline.DialContext, settings.connectTimeout), settings.ipVersion)
	}
	version, err := applyHTTPVersion(baseline, config)
	if err != nil {
		return err
	}
	h.httpVersion = version
	applyNetworkSimulation(baseline, config)
	if err := applyProxy(baseline, config); err != nil {
		return err
	}
	if len(h.allowedTargetHosts) > 0 {
		baseline.Proxy = protocol.NewTargetPolicyProxy(baseline.Proxy, h.allowedTargetHosts)
	}
	jar := buildCookieJar(config)
	h.maxRedirects = parseMaxRedirects(config)
	h.connections = newHTTPConnectionBudget(settings.maxConnsPerHost, settings.maxIdleConns)
	h.policyPools = make(map[string]*httpPolicyPool, 3)
	pacSource := ""
	if protocol.ConfigString(config, "proxy_mode") == "pac" {
		pacSource, err = fetchPAC(ctx, protocol.ConfigString(config, "proxy_pac_url"), h.allowedTargetHosts)
		if err != nil {
			return err
		}
	}
	builder := &httpPoolBuilder{
		settings:       settings,
		tlsConfig:      tlsConfig,
		proxy:          baseline.Proxy,
		dial:           baseline.DialContext,
		jar:            jar,
		requestTimeout: h.requestTimeout,
		maxRedirects:   h.maxRedirects,
		pacSource:      pacSource,
		allowed:        h.allowedTargetHosts,
		connections:    h.connections,
		runCorrelation: h.runCorrelation,
	}
	for _, policy := range []string{"auto", VersionHTTP11, VersionHTTP2} {
		h.policyPools[policy] = &httpPolicyPool{policy: policy, builder: builder}
	}
	h.http3, h.http3Err = nil, nil
	switch {
	case HTTP3ExecutionError() != nil:
		h.http3Err = HTTP3ExecutionError()
	case parseNetworkSimulationProfile(config).active():
		h.http3Err = fmt.Errorf("%w: network shaping applies to TCP only", ErrHTTP3Required)
	default:
		pool, err := h.newHTTP3Pool(settings, tlsConfig, config, baseline, pacSource, h.policyPools["auto"], jar)
		if err != nil {
			// A script that never resolves a request to 3 must still run, so
			// the reason is kept for dispatch rather than failing the session.
			h.http3Err = err
		} else {
			h.policyPools[VersionHTTP3] = pool
		}
		// auto+h3 also runs where QUIC is ruled out by the TLS maximum: every
		// request then stays on TCP with selection reason tls_policy.
		if tlsBelow := tlsMaximumBelow13(tlsConfig); pool != nil || (tlsBelow && errors.Is(err, ErrHTTP3Required)) {
			var quic stdhttp.RoundTripper
			if pool != nil {
				quic = h.http3
			}
			h.policyPools[VersionAutoH3] = h.newUpgradePool(quic, tlsBelow, baseline, pacSource, h.policyPools["auto"], h.policyPools[VersionHTTP11], jar)
		}
	}
	defaultPolicy := h.httpVersion
	if defaultPolicy == "" || h.policyPools[defaultPolicy] == nil {
		// Requests that inherit an unavailable HTTP/3 policy are refused
		// before dispatch, so the automatic pool only serves session traffic
		// outside request leaves.
		defaultPolicy = "auto"
	}
	pool := h.policyPools[defaultPolicy]
	h.client, h.transport, h.oauthPool = pool.httpClient(), pool.httpTransport(), pool
	return nil
}

func (h *HTTP) effectiveHTTPVersion(action *protocol.Action) (string, error) {
	if action == nil {
		return "", fmt.Errorf("http action is required")
	}
	policy, err := action.HTTPVersion.Resolve(h.httpVersion)
	if err != nil {
		return "", err
	}
	// Refuse an HTTP/3 policy without a pool here, before forHTTPVersion
	// could fall back to the session's TCP pool.
	switch policy {
	case VersionAutoH3:
		if err := HTTP3UpgradeExecutionError(); err != nil {
			return "", err
		}
		if h.policyPools[VersionAutoH3] == nil {
			if h.http3Err != nil {
				return "", h.http3Err
			}
			return "", ErrHTTP3NotEnabled
		}
	case VersionHTTP3:
		if h.policyPools[VersionHTTP3] == nil {
			if h.http3Err != nil {
				return "", h.http3Err
			}
			return "", ErrHTTP3NotEnabled
		}
	}
	return policy, nil
}

func (h *HTTP) forHTTPVersion(policy string) *HTTP {
	pool := h.policyPools[policy]
	if pool == nil {
		return h
	}
	child := h.metaRefreshClient()
	child.client, child.oauthPool = pool.httpClient(), pool
	if pool.base == nil {
		// The HTTP/3 pools' children read no transport (an auto+h3 router
		// reaches the TCP pools itself), so they keep the session's rather
		// than building the automatic pool's.
		child.transport = pool.httpTransport()
	}
	child.httpVersion = h.versionForPolicy(policy)
	return child
}

// versionForPolicy is the session version forHTTPVersion gives a request
// under policy, without building its pool.
func (h *HTTP) versionForPolicy(policy string) string {
	switch {
	case h.policyPools[policy] == nil:
		return h.httpVersion
	case policy == "auto":
		return ""
	}
	return policy
}

func (h *HTTP) validateHTTPVersionTLS(policy, target string) error {
	if policy == VersionHTTP3 && !strings.HasPrefix(strings.ToLower(target), "https://") {
		// The save-time rule again, after templates and runtime settings
		// resolved the URL: HTTP/3 serves only https origins.
		return fmt.Errorf("%w: HTTP/3 requires an https:// URL", ErrHTTP3Required)
	}
	if policy != VersionHTTP2 || !strings.HasPrefix(strings.ToLower(target), "https://") {
		return nil
	}
	// The pool's transport clones the session TLS configuration, so its
	// maximum is read there without building the pool.
	pool := h.policyPools[policy]
	if pool != nil && pool.builder != nil && pool.builder.tlsConfig != nil {
		maximum := pool.builder.tlsConfig.MaxVersion
		if maximum != 0 && maximum < tls.VersionTLS12 {
			return fmt.Errorf("HTTP version 2 requires TLS 1.2 or later for HTTPS requests")
		}
	}
	return nil
}

// net/http initializes HTTP/2 callbacks during Clone/first use and appends both
// ALPN spellings. Finish that initialization before freezing each private
// transport's ALPN, so exact H1 never advertises h2 and exact H2 never advertises
// HTTP/1.1. Session TLS policy and its shared session cache remain unchanged.
func freezePolicyALPN(transport *stdhttp.Transport, policy string) {
	transport.CloseIdleConnections()
	switch policy {
	case VersionHTTP11:
		transport.TLSClientConfig.NextProtos = []string{"http/1.1"}
	case VersionHTTP2:
		transport.TLSClientConfig.NextProtos = []string{"h2"}
	default:
		transport.TLSClientConfig.NextProtos = []string{"h2", "http/1.1"}
	}
}
