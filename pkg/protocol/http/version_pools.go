package http

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	stdhttp "net/http"
	"strings"
	"sync"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// A policy's clients are immutable after Init; their transports are independent
// lazy owners. In particular, selecting a policy does not create its OAuth pool.
type httpPolicyPool struct {
	builder            *httpPolicyPoolBuilder
	policy             string
	transportOnce      sync.Once
	oauthOnce          sync.Once
	baseTransport      stdhttp.RoundTripper
	baseOAuthTransport stdhttp.RoundTripper
	client             *stdhttp.Client
	transport          *stdhttp.Transport
	oauthClient        *stdhttp.Client
	oauthTransport     *stdhttp.Transport
}

// httpPolicyPoolBuilder is shared by this VUser's TCP policies. Every value is
// validated and fixed during Init; only the connection budget is mutable.
type httpPolicyPoolBuilder struct {
	settings    transportConfig
	baseline    *stdhttp.Transport
	tlsConfig   *tls.Config
	budget      *httpConnectionBudget
	pacSource   string
	allowed     []string
	correlation runCorrelationPolicy
}

type lazyHTTPPolicyTransport struct {
	pool  *httpPolicyPool
	oauth bool
}

func (transport lazyHTTPPolicyTransport) RoundTrip(request *stdhttp.Request) (*stdhttp.Response, error) {
	return transport.pool.roundTripper(transport.oauth).RoundTrip(request)
}

func (pool *httpPolicyPool) roundTripper(oauth bool) stdhttp.RoundTripper {
	if oauth {
		pool.oauthOnce.Do(func() {
			pool.oauthTransport, pool.baseOAuthTransport = pool.builder.build(pool.policy, true)
		})
		return pool.baseOAuthTransport
	}
	pool.transportOnce.Do(func() {
		pool.transport, pool.baseTransport = pool.builder.build(pool.policy, false)
	})
	return pool.baseTransport
}

func (builder *httpPolicyPoolBuilder) build(policy string, oauth bool) (*stdhttp.Transport, stdhttp.RoundTripper) {
	tlsConfig := builder.tlsConfig.Clone()
	if oauth {
		// Same credential-endpoint policy as hardenedOAuthHTTPClient, without
		// first constructing and cloning an unused application transport.
		tlsConfig.InsecureSkipVerify = false
		if tlsConfig.MinVersion == 0 {
			tlsConfig.MinVersion = tls.VersionTLS12
		}
	}
	transport := newTransport(builder.settings, tlsConfig)
	transport.Proxy = builder.baseline.Proxy
	transport.DialContext = builder.baseline.DialContext
	selected := policy
	if selected == "auto" {
		selected = ""
	}
	// The only callers use the three validated policies installed by Init.
	_, _ = applyHTTPVersion(transport, map[string]any{"http_version": selected})
	if oauth {
		transport.MaxResponseHeaderBytes = maxOAuthResponseHeaderBytes
	}
	freezePolicyALPN(transport, policy)
	baseDial := builder.baseline.DialContext
	var base stdhttp.RoundTripper = builder.budget.track(transport, baseDial)
	if builder.pacSource != "" {
		base = &pacRoundTripper{source: builder.pacSource, allowed: builder.allowed, base: transport, budget: builder.budget, baseDial: baseDial}
	}
	return transport, base
}

func (builder *httpPolicyPoolBuilder) wrap(base stdhttp.RoundTripper, oauth bool) stdhttp.RoundTripper {
	if len(builder.allowed) > 0 {
		base = protocol.NewTargetPolicyRoundTripper(base, builder.allowed)
	}
	base = withoutImplicitUserAgent(base)
	if !oauth {
		base = integratedAuthRoundTripper{base: base}
		if builder.correlation.enabled() {
			base = runCorrelationRoundTripper{base: base, policy: builder.correlation}
		}
	}
	return base
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
	// Retain validated session settings, not the caller's mutable config map.
	// The baseline also supplies WebSocket TLS settings without opening a pool.
	builder := &httpPolicyPoolBuilder{
		settings: settings, baseline: baseline, tlsConfig: tlsConfig,
		budget: h.connections, pacSource: pacSource,
		allowed: h.allowedTargetHosts, correlation: h.runCorrelation,
	}
	// The upgrade router uses these unwrapped lazy TCP bases; its own wrappers
	// preserve the same request policy without initializing unused versions.
	tcpBase := make(map[string]stdhttp.RoundTripper, 3)
	for _, policy := range []string{"auto", VersionHTTP11, VersionHTTP2} {
		pool := &httpPolicyPool{builder: builder, policy: policy}
		base := lazyHTTPPolicyTransport{pool: pool}
		pool.client = &stdhttp.Client{Transport: builder.wrap(base, false), Jar: jar, Timeout: h.requestTimeout}
		applyRedirectPolicy(pool.client, h.maxRedirects)
		pool.oauthClient = oauthRequestClient(pool.client)
		pool.oauthClient.Transport = builder.wrap(lazyHTTPPolicyTransport{pool: pool, oauth: true}, true)
		tcpBase[policy] = base
		h.policyPools[policy] = pool
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
			h.policyPools[VersionAutoH3] = h.newUpgradePool(quic, tlsBelow, baseline, pacSource, h.policyPools["auto"], tcpBase, jar)
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
	h.client, h.transport, h.oauthClient, h.oauthTransport = pool.client, baseline, pool.oauthClient, nil
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
	child.client, child.oauthClient = pool.client, pool.oauthClient
	child.httpVersion = policy
	if policy == "auto" {
		child.httpVersion = ""
	}
	return child
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
	if h.transport != nil && h.transport.TLSClientConfig != nil {
		maximum := h.transport.TLSClientConfig.MaxVersion
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
