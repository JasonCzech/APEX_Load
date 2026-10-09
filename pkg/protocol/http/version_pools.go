package http

import (
	"context"
	"crypto/tls"
	"fmt"
	stdhttp "net/http"
	"strings"

	"github.com/jasonczech/apexload/pkg/protocol"
)

type httpPolicyPool struct {
	client         *stdhttp.Client
	transport      *stdhttp.Transport
	oauthClient    *stdhttp.Client
	oauthTransport *stdhttp.Transport
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
	baseDial := baseline.DialContext
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
	for _, policy := range []string{"auto", VersionHTTP11, VersionHTTP2} {
		transport := newTransport(settings, tlsConfig.Clone())
		transport.Proxy = baseline.Proxy
		transport.DialContext = baseDial
		selected := policy
		if selected == "auto" {
			selected = ""
		}
		if _, err := applyHTTPVersion(transport, map[string]any{"http_version": selected}); err != nil {
			return err
		}
		client := &stdhttp.Client{Transport: transport, Jar: jar, Timeout: h.requestTimeout}
		applyRedirectPolicy(client, h.maxRedirects)
		oauthClient := hardenedOAuthHTTPClient(client)
		oauthTransport, _ := oauthClient.Transport.(*stdhttp.Transport)
		freezePolicyALPN(transport, policy)
		freezePolicyALPN(oauthTransport, policy)
		owner := h.connections.track(transport, baseDial)
		oauthOwner := h.connections.track(oauthTransport, baseDial)
		client.Transport = owner
		oauthClient.Transport = oauthOwner
		if pacSource != "" {
			client.Transport = &pacRoundTripper{source: pacSource, allowed: h.allowedTargetHosts, base: transport, budget: h.connections, baseDial: baseDial}
			oauthClient.Transport = &pacRoundTripper{source: pacSource, allowed: h.allowedTargetHosts, base: oauthTransport, budget: h.connections, baseDial: baseDial}
		}
		if len(h.allowedTargetHosts) > 0 {
			client.Transport = protocol.NewTargetPolicyRoundTripper(client.Transport, h.allowedTargetHosts)
			oauthClient.Transport = protocol.NewTargetPolicyRoundTripper(oauthClient.Transport, h.allowedTargetHosts)
		}
		client.Transport = withoutImplicitUserAgent(client.Transport)
		client.Transport = integratedAuthRoundTripper{base: client.Transport}
		if h.runCorrelation.enabled() {
			client.Transport = runCorrelationRoundTripper{base: client.Transport, policy: h.runCorrelation}
		}
		oauthClient.Transport = withoutImplicitUserAgent(oauthClient.Transport)
		h.policyPools[policy] = &httpPolicyPool{client: client, transport: transport, oauthClient: oauthClient, oauthTransport: oauthTransport}
	}
	defaultPolicy := h.httpVersion
	if defaultPolicy == "" {
		defaultPolicy = "auto"
	}
	pool := h.policyPools[defaultPolicy]
	h.client, h.transport, h.oauthClient, h.oauthTransport = pool.client, pool.transport, pool.oauthClient, pool.oauthTransport
	return nil
}

func (h *HTTP) effectiveHTTPVersion(action *protocol.Action) (string, error) {
	if action == nil {
		return "", fmt.Errorf("http action is required")
	}
	return action.HTTPVersion.Resolve(h.httpVersion)
}

func (h *HTTP) forHTTPVersion(policy string) *HTTP {
	pool := h.policyPools[policy]
	if pool == nil {
		return h
	}
	child := h.metaRefreshClient()
	child.client, child.transport, child.oauthClient, child.oauthTransport = pool.client, pool.transport, pool.oauthClient, pool.oauthTransport
	child.httpVersion = policy
	if policy == "auto" {
		child.httpVersion = ""
	}
	return child
}

func (h *HTTP) validateHTTPVersionTLS(policy, target string) error {
	if policy != VersionHTTP2 || !strings.HasPrefix(strings.ToLower(target), "https://") {
		return nil
	}
	pool := h.policyPools[policy]
	if pool != nil && pool.transport.TLSClientConfig != nil {
		maximum := pool.transport.TLSClientConfig.MaxVersion
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
