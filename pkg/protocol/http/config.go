package http

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/fileasset"
	"github.com/jasonczech/apexload/pkg/protocol"
	"golang.org/x/net/http/httpproxy"
)

type transportConfig struct {
	ipVersion        string
	maxConnsPerHost  int
	maxIdleConns     int
	connectTimeout   time.Duration
	tlsTimeout       time.Duration
	idleTimeout      time.Duration
	requestTimeout   time.Duration
	keepAlive        time.Duration
	disableKeepAlive bool
	// sourceIP binds outgoing connections to a specific local address (LR-style
	// "IP spoofing" / multiple client IPs). Nil = OS default.
	sourceIP net.IP
}

const (
	defaultHTTPMaxConnsPerHost = 100
	defaultHTTPMaxIdleConns    = 4
	// Match net/http.DefaultTransport so Expect: 100-continue requests remain
	// bounded while still giving a cooperating HTTP/1.1 server time to reply.
	defaultHTTPExpectContinueTimeout = time.Second
	// MaxConfigured* are shared with DSL validation so authored scripts fail
	// explicitly instead of being silently clamped by the runtime.
	MaxConfiguredConnsPerHost = 256
	MaxConfiguredIdleConns    = 32
	MaxConfiguredTimeout      = time.Hour
	MaxConfiguredRedirects    = 50
	MaxConfiguredNetworkKbps  = 100_000_000
	MaxConfiguredStreamKbps   = 100_000_000
	MaxConfiguredStreamBuffer = time.Hour
	// Protocol instances are per VUser. Keep this deliberately small so the
	// TLS session cache cannot multiply into hundreds of thousands of retained
	// sessions at generator scale while still covering ordinary multi-host flows.
	maxTLSClientSessionsPerVUser = 8
)

func validateHTTPRuntimeConfigBounds(config map[string]any) error {
	if _, err := protocol.BoundedMilliseconds(config["challenge_delay_ms"], 0, MaxConfiguredTimeout); err != nil {
		return fmt.Errorf("challenge_delay_ms %w", err)
	}
	for _, key := range []string{"http_errors_as_warnings", "fail_noncritical_resources", "resource_timeout_as_warning", "legacy_html_checks", "dns_cache"} {
		if v := config[key]; v != nil {
			if _, ok := v.(bool); !ok {
				return fmt.Errorf("%s must be a boolean", key)
			}
		}
	}
	if err := ValidateReplayCompatibilityConfig(config); err != nil {
		return err
	}
	for _, field := range []string{"web_network_simulation_latency_ms", "web_network_simulation_jitter_ms"} {
		if _, err := protocol.BoundedMilliseconds(config[field], 0, MaxConfiguredTimeout); err != nil {
			return fmt.Errorf("%s %w", field, err)
		}
	}
	for _, field := range []struct {
		name string
		max  int64
	}{
		{name: "web_network_simulation_bandwidth_kbps", max: MaxConfiguredNetworkKbps},
		{name: "web_streaming_buffer_size_seconds", max: int64(MaxConfiguredStreamBuffer / time.Second)},
		{name: "web_streaming_fixed_bitrate_kbps", max: MaxConfiguredStreamKbps},
	} {
		if config[field.name] == nil {
			continue
		}
		value, err := exactHTTPConfigInteger(config[field.name])
		if err != nil || value < 0 || value > field.max {
			return fmt.Errorf("%s must be an integer between 0 and %d", field.name, field.max)
		}
	}
	if raw := config["web_network_simulation_packet_loss_pct"]; raw != nil {
		value, err := exactHTTPConfigFloat(raw)
		if err != nil || value < 0 || value > 100 {
			return fmt.Errorf("web_network_simulation_packet_loss_pct must be between 0 and 100")
		}
	}
	return nil
}

func exactHTTPConfigInteger(value any) (int64, error) {
	switch typed := value.(type) {
	case int:
		return int64(typed), nil
	case int64:
		return typed, nil
	case int32:
		return int64(typed), nil
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) || math.Trunc(typed) != typed || typed < math.MinInt64 || typed > math.MaxInt64 {
			return 0, fmt.Errorf("not an integer")
		}
		return int64(typed), nil
	case float32:
		converted := float64(typed)
		if math.IsNaN(converted) || math.IsInf(converted, 0) || math.Trunc(converted) != converted {
			return 0, fmt.Errorf("not an integer")
		}
		return int64(typed), nil
	case json.Number:
		return typed.Int64()
	case string:
		return strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
	default:
		return 0, fmt.Errorf("not an integer")
	}
}

func exactHTTPConfigFloat(value any) (float64, error) {
	var converted float64
	switch typed := value.(type) {
	case float64:
		converted = typed
	case float32:
		converted = float64(typed)
	case int:
		converted = float64(typed)
	case int64:
		converted = float64(typed)
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return 0, err
		}
		converted = parsed
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err != nil {
			return 0, err
		}
		converted = parsed
	default:
		return 0, fmt.Errorf("not a number")
	}
	if math.IsNaN(converted) || math.IsInf(converted, 0) {
		return 0, fmt.Errorf("not a finite number")
	}
	return converted, nil
}

func parseTransportConfig(config map[string]any) transportConfig {
	settings := transportConfig{
		ipVersion:       strings.ToLower(strings.TrimSpace(protocol.ConfigString(config, "ip_version"))),
		maxConnsPerHost: defaultHTTPMaxConnsPerHost,
		maxIdleConns:    defaultHTTPMaxIdleConns,
		connectTimeout:  30 * time.Second,
		tlsTimeout:      10 * time.Second,
		idleTimeout:     90 * time.Second,
		requestTimeout:  60 * time.Second,
		keepAlive:       30 * time.Second,
	}
	if value := protocol.ConfigInt(config, "max_conns_per_host"); value > 0 {
		settings.maxConnsPerHost = min(value, MaxConfiguredConnsPerHost)
	}
	if value := protocol.ConfigInt(config, "max_idle_conns"); value > 0 {
		settings.maxIdleConns = min(value, MaxConfiguredIdleConns)
	}
	if value := boundedHTTPTimeout(config, "connect_timeout_ms"); value > 0 {
		settings.connectTimeout = value
	}
	if value := boundedHTTPTimeout(config, "tls_timeout_ms"); value > 0 {
		settings.tlsTimeout = value
	}
	if value := boundedHTTPTimeout(config, "idle_timeout_ms"); value > 0 {
		settings.idleTimeout = value
	}
	if value := boundedHTTPTimeout(config, "request_timeout_ms"); value > 0 {
		settings.requestTimeout = value
	}
	if value, ok := config["keep_alive"].(bool); ok && !value {
		settings.disableKeepAlive = true
		settings.keepAlive = 0
	}
	if raw, ok := config["web_source_ip"].(string); ok {
		if ip := net.ParseIP(strings.TrimSpace(raw)); ip != nil {
			settings.sourceIP = ip
		}
	}

	return settings
}

func boundedHTTPTimeout(config map[string]any, key string) time.Duration {
	milliseconds := protocol.ConfigInt(config, key)
	if milliseconds <= 0 {
		return 0
	}
	if milliseconds > int(MaxConfiguredTimeout/time.Millisecond) {
		return MaxConfiguredTimeout
	}
	duration := time.Duration(milliseconds) * time.Millisecond
	return duration
}

func buildTLSConfig(config map[string]any) (*tls.Config, error) {
	tlsCfg := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		ClientSessionCache: tls.NewLRUClientSessionCache(maxTLSClientSessionsPerVUser),
	}

	if value, ok := config["tls_min_version"].(string); ok && strings.TrimSpace(value) != "" {
		version, err := parseTLSVersion(strings.TrimSpace(value))
		if err != nil {
			return nil, err
		}
		tlsCfg.MinVersion = version
	}
	if value, ok := config["tls_max_version"].(string); ok && strings.TrimSpace(value) != "" {
		version, err := parseTLSVersion(strings.TrimSpace(value))
		if err != nil {
			return nil, err
		}
		tlsCfg.MaxVersion = version
	}
	if tlsCfg.MaxVersion != 0 && tlsCfg.MaxVersion < tlsCfg.MinVersion {
		return nil, fmt.Errorf("TLS maximum version must not be lower than minimum version")
	}
	if value, ok := config["tls_insecure_skip_verify"].(bool); ok {
		tlsCfg.InsecureSkipVerify = value
	}

	certFile, _ := config["tls_client_cert_file"].(string)
	keyFile, _ := config["tls_client_key_file"].(string)
	certFile = strings.TrimSpace(certFile)
	keyFile = strings.TrimSpace(keyFile)
	if (certFile == "") != (keyFile == "") {
		return nil, fmt.Errorf("TLS client certificate and key files must be configured together")
	}
	if certFile != "" {
		certPEM, err := fileasset.ReadCredential(certFile)
		if err != nil {
			return nil, fmt.Errorf("read client certificate: %w", err)
		}
		keyPEM, err := fileasset.ReadCredential(keyFile)
		if err != nil {
			return nil, fmt.Errorf("read client key: %w", err)
		}
		cert, err := tls.X509KeyPair(certPEM, keyPEM)
		if err != nil {
			return nil, fmt.Errorf("load client certificate: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}

	if caFile, ok := config["tls_ca_cert_file"].(string); ok && strings.TrimSpace(caFile) != "" {
		caFile = strings.TrimSpace(caFile)
		caCert, err := fileasset.ReadCredential(caFile)
		if err != nil {
			return nil, fmt.Errorf("read CA certificate: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA certificate from %s", caFile)
		}
		tlsCfg.RootCAs = pool
	}

	return tlsCfg, nil
}

func newTransport(settings transportConfig, tlsCfg *tls.Config) *http.Transport {
	dialer := &net.Dialer{
		Timeout:   settings.connectTimeout,
		KeepAlive: settings.keepAlive,
	}
	if settings.sourceIP != nil {
		// Bind outgoing connections to the assigned client IP so the test
		// presents many distinct source addresses to the target.
		dialer.LocalAddr = &net.TCPAddr{IP: settings.sourceIP}
	}
	maxIdlePerHost := min(settings.maxConnsPerHost, settings.maxIdleConns)
	return &http.Transport{
		DialContext:           familyDialContext(dialer.DialContext, settings.ipVersion),
		MaxIdleConns:          settings.maxIdleConns,
		MaxIdleConnsPerHost:   maxIdlePerHost,
		MaxConnsPerHost:       settings.maxConnsPerHost,
		IdleConnTimeout:       settings.idleTimeout,
		TLSHandshakeTimeout:   settings.tlsTimeout,
		ExpectContinueTimeout: defaultHTTPExpectContinueTimeout,
		TLSClientConfig:       tlsCfg,
		DisableKeepAlives:     settings.disableKeepAlive,
		// Imported requests must not acquire an implicit Accept-Encoding:
		// gzip field or transport-owned transparent decoding. Authored
		// encodings are sent exactly and decoded by the bounded response
		// pipeline in http.go.
		DisableCompression:     true,
		MaxResponseHeaderBytes: maxHTTPResponseHeaderBytes,
	}
}

type noImplicitUserAgentRoundTripper struct {
	base http.RoundTripper
}

func (transport noImplicitUserAgentRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	if request == nil || requestHeaderKeyPresent(request.Header, "User-Agent") {
		return transport.base.RoundTrip(request)
	}
	// net/http emits Go-http-client/{1.1,2.0} when User-Agent is absent.
	// Clone only at the transport boundary and add the documented empty
	// sentinel so raw wire behavior is deterministic without mutating the
	// authored request used for snapshots, caching, or correlation evidence.
	wireRequest := request.Clone(request.Context())
	wireRequest.Header = request.Header.Clone()
	wireRequest.Header["User-Agent"] = []string{""}
	return transport.base.RoundTrip(wireRequest)
}

func withoutImplicitUserAgent(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return noImplicitUserAgentRoundTripper{base: base}
}

func requestHeaderKeyPresent(headers http.Header, name string) bool {
	for key := range headers {
		if strings.EqualFold(key, name) {
			return true
		}
	}
	return false
}

func applyNetworkSimulation(transport *http.Transport, config map[string]any) {
	profile := parseNetworkSimulationProfile(config)
	if !profile.active() {
		return
	}

	baseDialContext := transport.DialContext
	if baseDialContext == nil {
		baseDialContext = (&net.Dialer{}).DialContext
	}
	transport.DialContext = newShapedDialContext(baseDialContext, profile)
}

func parseNetworkSimulationProfile(config map[string]any) networkSimulationProfile {
	mode, _ := config["web_network_simulation_mode"].(string)
	preset, _ := config["web_network_simulation_preset"].(string)
	latencyMS, bandwidthKbps := resolveNetworkProfile(mode, preset, protocol.ConfigInt(config, "web_network_simulation_latency_ms"), protocol.ConfigInt(config, "web_network_simulation_bandwidth_kbps"))
	return networkSimulationProfile{
		latency:          time.Duration(latencyMS) * time.Millisecond,
		jitter:           time.Duration(protocol.ConfigInt(config, "web_network_simulation_jitter_ms")) * time.Millisecond,
		bandwidthBytesPS: bandwidthBytesPerSecond(bandwidthKbps),
		packetLossPct:    protocol.ConfigFloat(config, "web_network_simulation_packet_loss_pct"),
		randFloat64:      defaultRandFloat64,
	}
}

func resolveNetworkProfile(mode, preset string, latencyMS, bandwidthKbps int) (int, int) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "preset":
		switch strings.ToLower(strings.TrimSpace(preset)) {
		case "56k":
			return 120, 56
		case "dsl":
			return 50, 1536
		case "t1":
			return 20, 1544
		case "10mbps":
			return 5, 10240
		}
	case "custom":
		return latencyMS, bandwidthKbps
	}
	return 0, 0
}

func bandwidthBytesPerSecond(kbps int) int {
	if kbps <= 0 {
		return 0
	}
	return int(math.Round(float64(kbps*1024) / 8))
}

func applyHTTPVersion(transport *http.Transport, config map[string]any) (string, error) {
	raw, configured := config["http_version"]
	if !configured || raw == nil {
		transport.ForceAttemptHTTP2 = true
		return "", nil
	}
	value, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("http_version must be a string")
	}
	version := NormalizeVersion(value)
	if err := ValidateVersion(version); err != nil {
		return "", err
	}

	switch version {
	case VersionHTTP2:
		// Requiring HTTP/2 means exactly that for both URL schemes. TLS
		// requests negotiate h2 without advertising HTTP/1.1; cleartext
		// requests use prior-knowledge h2c. There is no HTTP/1 fallback.
		protocols := new(http.Protocols)
		protocols.SetHTTP2(true)
		protocols.SetUnencryptedHTTP2(true)
		transport.Protocols = protocols
		transport.ForceAttemptHTTP2 = true
	case VersionHTTP11:
		protocols := new(http.Protocols)
		protocols.SetHTTP1(true)
		transport.Protocols = protocols
		transport.ForceAttemptHTTP2 = false
	default:
		// Empty remains the compatibility default: HTTP/1.1 with
		// opportunistic HTTP/2 for TLS targets.
		transport.ForceAttemptHTTP2 = true
	}
	return version, nil
}

func applyProxy(transport *http.Transport, config map[string]any) error {
	proxyMode, _ := config["proxy_mode"].(string)
	switch strings.TrimSpace(proxyMode) {
	case "none":
		transport.Proxy = nil
		return nil
	case "system":
		transport.Proxy = proxyWithNoProxy(systemProxyFromEnvironment(), parseNoProxyPatterns(config))
		return nil
	case "custom", "":
		// Custom proxy stays URL-driven; empty mode preserves legacy behavior.
	default:
		transport.Proxy = nil
		return nil
	}

	proxyURL, ok := config["proxy_url"].(string)
	if !ok || proxyURL == "" {
		if proxyMode == "custom" {
			return fmt.Errorf("proxy_mode custom requires proxy_url")
		}
		return nil
	}

	parsedProxy, err := url.Parse(proxyURL)
	if err != nil {
		return fmt.Errorf("parse proxy URL: %w", err)
	}
	if parsedProxy.Host == "" {
		return fmt.Errorf("proxy URL must be absolute and include a host")
	}
	switch strings.ToLower(parsedProxy.Scheme) {
	case "http", "https", "socks5", "socks5h":
	default:
		return fmt.Errorf("unsupported proxy URL scheme %q", parsedProxy.Scheme)
	}

	proxyUser, _ := config["proxy_username"].(string)
	proxyPass, _ := config["proxy_password"].(string)
	if proxyUser != "" {
		parsedProxy.User = url.UserPassword(proxyUser, proxyPass)
	}

	transport.Proxy = proxyWithNoProxy(http.ProxyURL(parsedProxy), parseNoProxyPatterns(config))
	return nil
}

// systemProxyFromEnvironment snapshots the process proxy configuration when a
// protocol instance is initialized. net/http.ProxyFromEnvironment caches the
// first environment it observes for the lifetime of the process, which makes
// later protocol instances depend on initialization order if an embedding host
// updates its environment between runs.
func systemProxyFromEnvironment() func(*http.Request) (*url.URL, error) {
	proxyForURL := httpproxy.FromEnvironment().ProxyFunc()
	return func(req *http.Request) (*url.URL, error) {
		if req == nil || req.URL == nil {
			return nil, nil
		}
		return proxyForURL(req.URL)
	}
}

func proxyWithNoProxy(proxy func(*http.Request) (*url.URL, error), patterns []string) func(*http.Request) (*url.URL, error) {
	if len(patterns) == 0 {
		return proxy
	}
	return func(req *http.Request) (*url.URL, error) {
		if req != nil && req.URL != nil && noProxyMatches(req.URL.Host, patterns) {
			return nil, nil
		}
		return proxy(req)
	}
}

func parseNoProxyPatterns(config map[string]any) []string {
	keys := []string{"no_proxy", "proxy_no_proxy", "proxy_bypass", "proxy_bypass_hosts"}
	seen := make(map[string]struct{})
	var patterns []string
	add := func(value string) {
		for _, part := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ';' }) {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			key := strings.ToLower(part)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			patterns = append(patterns, part)
		}
	}
	for _, key := range keys {
		if raw := protocol.ConfigString(config, key); raw != "" {
			add(raw)
		}
		for _, value := range protocol.ConfigStringSlice(config, key) {
			add(value)
		}
	}
	return patterns
}

func noProxyMatches(reqHost string, patterns []string) bool {
	host, port := splitNoProxyHostPort(reqHost)
	if host == "" {
		return false
	}
	for _, pattern := range patterns {
		patternHost, patternPort := splitNoProxyHostPort(pattern)
		if patternHost == "" {
			continue
		}
		if patternHost == "*" {
			return true
		}
		if patternPort != "" && patternPort != port {
			continue
		}
		if _, network, err := net.ParseCIDR(patternHost); err == nil {
			if ip := net.ParseIP(host); ip != nil && network.Contains(ip) {
				return true
			}
			continue
		}
		if ip := net.ParseIP(patternHost); ip != nil {
			if ip.Equal(net.ParseIP(host)) {
				return true
			}
			continue
		}
		if strings.HasPrefix(patternHost, "*.") {
			suffix := strings.TrimPrefix(patternHost, "*")
			if strings.HasSuffix(host, suffix) && host != strings.TrimPrefix(suffix, ".") {
				return true
			}
			continue
		}
		if strings.HasPrefix(patternHost, ".") {
			root := strings.TrimPrefix(patternHost, ".")
			if host == root || strings.HasSuffix(host, patternHost) {
				return true
			}
			continue
		}
		if host == patternHost || strings.HasSuffix(host, "."+patternHost) {
			return true
		}
	}
	return false
}

func splitNoProxyHostPort(value string) (string, string) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "", ""
	}
	if strings.HasPrefix(value, "[") {
		if host, port, err := net.SplitHostPort(value); err == nil {
			return strings.Trim(host, "[]"), port
		}
		return strings.Trim(value, "[]"), ""
	}
	if host, port, err := net.SplitHostPort(value); err == nil {
		return strings.Trim(host, "."), port
	}
	if idx := strings.LastIndex(value, ":"); idx > 0 && strings.Count(value, ":") == 1 {
		maybePort := value[idx+1:]
		if _, err := strconv.Atoi(maybePort); err == nil {
			return strings.Trim(value[:idx], "."), maybePort
		}
	}
	return strings.Trim(value, "."), ""
}

func buildCookieJar(config map[string]any) http.CookieJar {
	cookiesEnabled := true
	if value, ok := config["cookies_enabled"].(bool); ok {
		cookiesEnabled = value
	}
	if !cookiesEnabled {
		return nil
	}

	jar, _ := newBoundedCookieJar()
	return jar
}

func parseTraceparentEnabled(config map[string]any) bool {
	enabled := true
	for _, key := range []string{"traceparent_enabled", "web_traceparent_enabled", "generate_traceparent", "web_generate_traceparent"} {
		if value, ok := optionalConfigBool(config, key); ok {
			enabled = value
		}
	}
	for _, key := range []string{"disable_traceparent", "web_disable_traceparent"} {
		if value, ok := optionalConfigBool(config, key); ok && value {
			enabled = false
		}
	}
	return enabled
}

func parseTraceparentSampled(config map[string]any) bool {
	sampled := true
	for _, key := range []string{"traceparent_sampled", "web_traceparent_sampled", "traceparent_sampling", "web_traceparent_sampling"} {
		if value, ok := optionalConfigBool(config, key); ok {
			sampled = value
		}
	}
	return sampled
}

func optionalConfigBool(config map[string]any, key string) (bool, bool) {
	if config == nil {
		return false, false
	}
	switch value := config[key].(type) {
	case bool:
		return value, true
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(value))
		if err != nil {
			return false, false
		}
		return parsed, true
	default:
		return false, false
	}
}

// ClearCookies satisfies protocol.CookieController. It replaces the client's
// cookie jar with a fresh, empty one, discarding every cookie this VUser has
// accumulated — the runtime equivalent of an imported web_cleanup_cookies call.
// Go's cookiejar has no in-place clear, so swapping in a new jar is the
// idiomatic reset. When cookies are disabled the client has no jar and this is
// a no-op.
func (h *HTTP) ClearCookies(ctx context.Context) error {
	if h.client == nil || h.client.Jar == nil {
		return nil
	}
	if jar, ok := h.client.Jar.(*boundedCookieJar); ok {
		return jar.clear()
	}
	jar, err := newBoundedCookieJar()
	if err != nil {
		return err
	}
	h.client.Jar = jar
	return nil
}

func parseMaxRedirects(config map[string]any) int {
	maxRedirects := 10
	if _, configured := config["max_redirects"]; configured {
		maxRedirects = protocol.ConfigInt(config, "max_redirects")
		if maxRedirects > MaxConfiguredRedirects {
			maxRedirects = MaxConfiguredRedirects
		}
	}
	return maxRedirects
}

func applyRedirectPolicy(client *http.Client, maxRedirects int) {
	if maxRedirects <= 0 {
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		}
		return
	}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > maxRedirects {
			return fmt.Errorf("stopped after %d redirects", maxRedirects)
		}
		if len(via) > 0 {
			previous := via[len(via)-1]
			if strings.EqualFold(previous.URL.Scheme, "https") && strings.EqualFold(req.URL.Scheme, "http") {
				return fmt.Errorf("refused HTTPS to HTTP redirect")
			}
		}
		return nil
	}
}

func buildDefaultHeaders(config map[string]any) map[string]string {
	headers := make(map[string]string)
	if values, ok := config["default_headers"].(map[string]string); ok {
		for key, value := range values {
			headers[key] = value
		}
	}
	addTypedHeaderUnlessOverridden(headers, "User-Agent", config["user_agent"])
	addTypedHeaderUnlessOverridden(headers, "Accept-Encoding", config["accept_encoding"])
	addTypedHeaderUnlessOverridden(headers, "Accept-Language", config["accept_language"])
	return headers
}

func buildTypedDefaultHeaders(config map[string]any) map[string]string {
	headers := make(map[string]string)
	addTypedHeaderUnlessOverridden(headers, "User-Agent", config["user_agent"])
	addTypedHeaderUnlessOverridden(headers, "Accept-Encoding", config["accept_encoding"])
	addTypedHeaderUnlessOverridden(headers, "Accept-Language", config["accept_language"])
	return headers
}

func mergeDefaultHeaders(typed, authored map[string]string) map[string]string {
	headers := make(map[string]string, len(typed)+len(authored))
	for name, value := range authored {
		headers[name] = value
	}
	for name, value := range typed {
		addTypedHeaderUnlessOverridden(headers, name, value)
	}
	return headers
}

func addTypedHeaderUnlessOverridden(headers map[string]string, name string, raw any) {
	value, ok := raw.(string)
	if !ok || value == "" {
		return
	}
	for existing := range headers {
		if strings.EqualFold(strings.TrimSpace(existing), name) {
			return
		}
	}
	headers[name] = value
}

func buildDefaultHeaderList(config map[string]any) protocol.HeaderList {
	switch values := config["default_header_list"].(type) {
	case protocol.HeaderList:
		return append(protocol.HeaderList(nil), values...)
	case []protocol.HeaderEntry:
		return append(protocol.HeaderList(nil), values...)
	case []map[string]string:
		headers := make(protocol.HeaderList, 0, len(values))
		for _, value := range values {
			headers = append(headers, protocol.HeaderEntry{Name: value["name"], Value: value["value"]})
		}
		return headers
	default:
		return nil
	}
}

func buildAuthHeader(config map[string]any) (string, string) {
	authType, _ := config["auth_type"].(string)
	return authType, authHeaderFromParts(authType, authStringConfig(config, "auth_username"), authStringConfig(config, "auth_password"), authStringConfig(config, "auth_token"))
}

func buildAuthConfig(config map[string]any) *protocol.AuthConfig {
	authType := strings.ToLower(strings.TrimSpace(protocol.ConfigString(config, "auth_type")))
	switch authType {
	case "", "none", "noauth", "inherit":
		return nil
	default:
		return &protocol.AuthConfig{
			Type:     authType,
			Username: authStringConfig(config, "auth_username"),
			Password: authStringConfig(config, "auth_password"),
			Token:    authStringConfig(config, "auth_token"),
			Domain:   authStringConfig(config, "auth_domain"),
		}
	}
}

func validateAuthType(authType string) error {
	switch strings.ToLower(strings.TrimSpace(authType)) {
	case "", "none", "noauth", "inherit", "basic", "bearer", "digest":
		return nil
	case "ntlm", "kerberos", "negotiate":
		return fmt.Errorf("unsupported HTTP auth type %q: configure an external auth flow or remove the auth setting", authType)
	default:
		return fmt.Errorf("unsupported HTTP auth type %q", authType)
	}
}

func buildActionAuthHeader(auth *protocol.AuthConfig) (string, bool, error) {
	if auth == nil {
		return "", false, nil
	}
	if err := ValidateAuthWireValues(auth); err != nil {
		return "", true, err
	}
	switch strings.ToLower(strings.TrimSpace(auth.Type)) {
	case "", "none", "noauth", "inherit":
		return "", true, nil
	case "digest":
		if err := validateAuthType(auth.Type); err != nil {
			return "", true, err
		}
		return "", true, nil
	default:
		if err := validateAuthType(auth.Type); err != nil {
			return "", true, err
		}
		return authHeaderFromParts(auth.Type, auth.Username, auth.Password, auth.Token), true, nil
	}
}

func buildRedactedActionAuthHeader(auth *protocol.AuthConfig) string {
	if auth == nil {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(auth.Type)) {
	case "basic", "ntlm":
		return "Basic [REDACTED]"
	case "bearer":
		return "Bearer [REDACTED]"
	case "digest":
		return "Digest [REDACTED]"
	default:
		return "[REDACTED]"
	}
}

func authHeaderFromParts(authType, username, password, token string) string {
	switch strings.ToLower(strings.TrimSpace(authType)) {
	case "basic":
		return "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
	case "bearer":
		return "Bearer " + token
	default:
		return ""
	}
}

func authStringConfig(config map[string]any, key string) string {
	value, _ := config[key].(string)
	return value
}
