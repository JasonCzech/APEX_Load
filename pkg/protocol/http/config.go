package http

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

type transportConfig struct {
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

func parseTransportConfig(config map[string]any) transportConfig {
	settings := transportConfig{
		maxConnsPerHost: 100,
		connectTimeout:  30 * time.Second,
		tlsTimeout:      10 * time.Second,
		idleTimeout:     90 * time.Second,
		requestTimeout:  60 * time.Second,
		keepAlive:       30 * time.Second,
	}
	settings.maxIdleConns = settings.maxConnsPerHost

	if value, ok := config["max_conns_per_host"].(float64); ok {
		settings.maxConnsPerHost = int(value)
		settings.maxIdleConns = int(value)
	}
	if value, ok := config["max_idle_conns"].(float64); ok {
		settings.maxIdleConns = int(value)
	}
	if value, ok := config["connect_timeout_ms"].(float64); ok {
		settings.connectTimeout = time.Duration(value) * time.Millisecond
	}
	if value, ok := config["tls_timeout_ms"].(float64); ok {
		settings.tlsTimeout = time.Duration(value) * time.Millisecond
	}
	if value, ok := config["idle_timeout_ms"].(float64); ok {
		settings.idleTimeout = time.Duration(value) * time.Millisecond
	}
	if value, ok := config["request_timeout_ms"].(float64); ok {
		settings.requestTimeout = time.Duration(value) * time.Millisecond
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

func buildTLSConfig(config map[string]any) (*tls.Config, error) {
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}

	if value, ok := config["tls_min_version"].(string); ok {
		if version, err := parseTLSVersion(value); err == nil {
			tlsCfg.MinVersion = version
		}
	}
	if value, ok := config["tls_max_version"].(string); ok {
		if version, err := parseTLSVersion(value); err == nil {
			tlsCfg.MaxVersion = version
		}
	}
	if value, ok := config["tls_insecure_skip_verify"].(bool); ok {
		tlsCfg.InsecureSkipVerify = value
	}

	certFile, _ := config["tls_client_cert_file"].(string)
	keyFile, _ := config["tls_client_key_file"].(string)
	if certFile != "" && keyFile != "" {
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("load client certificate: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}

	if caFile, ok := config["tls_ca_cert_file"].(string); ok && caFile != "" {
		caCert, err := os.ReadFile(caFile)
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
	return &http.Transport{
		DialContext:         dialer.DialContext,
		MaxIdleConns:        settings.maxIdleConns,
		MaxIdleConnsPerHost: settings.maxConnsPerHost,
		MaxConnsPerHost:     settings.maxConnsPerHost,
		IdleConnTimeout:     settings.idleTimeout,
		TLSHandshakeTimeout: settings.tlsTimeout,
		TLSClientConfig:     tlsCfg,
		DisableKeepAlives:   settings.disableKeepAlive,
	}
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
		sleep:            time.Sleep,
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

func applyHTTPVersion(transport *http.Transport, config map[string]any) {
	if value, ok := config["http_version"].(string); ok {
		switch value {
		case "2":
			transport.ForceAttemptHTTP2 = true
		case "1.1", "1.0":
			transport.ForceAttemptHTTP2 = false
			transport.TLSClientConfig.NextProtos = []string{"http/1.1"}
		default:
			transport.ForceAttemptHTTP2 = true
		}
		return
	}

	transport.ForceAttemptHTTP2 = true
}

func applyProxy(transport *http.Transport, config map[string]any) error {
	proxyMode, _ := config["proxy_mode"].(string)
	switch strings.TrimSpace(proxyMode) {
	case "none":
		transport.Proxy = nil
		return nil
	case "system":
		transport.Proxy = proxyWithNoProxy(http.ProxyFromEnvironment, parseNoProxyPatterns(config))
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

	proxyUser, _ := config["proxy_username"].(string)
	proxyPass, _ := config["proxy_password"].(string)
	if proxyUser != "" {
		parsedProxy.User = url.UserPassword(proxyUser, proxyPass)
	}

	transport.Proxy = proxyWithNoProxy(http.ProxyURL(parsedProxy), parseNoProxyPatterns(config))
	return nil
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

	jar, _ := cookiejar.New(nil)
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
	jar, err := cookiejar.New(nil)
	if err != nil {
		return err
	}
	h.client.Jar = jar
	return nil
}

func parseMaxRedirects(config map[string]any) int {
	maxRedirects := 10
	if value, ok := config["max_redirects"].(float64); ok {
		maxRedirects = int(value)
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

	if maxRedirects != 10 {
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("stopped after %d redirects", maxRedirects)
			}
			return nil
		}
	}
}

func buildDefaultHeaders(config map[string]any) map[string]string {
	headers := make(map[string]string)
	if value, ok := config["user_agent"].(string); ok && value != "" {
		headers["User-Agent"] = value
	}
	if value, ok := config["accept_encoding"].(string); ok && value != "" {
		headers["Accept-Encoding"] = value
	}
	if value, ok := config["accept_language"].(string); ok && value != "" {
		headers["Accept-Language"] = value
	}
	if values, ok := config["default_headers"].(map[string]string); ok {
		for key, value := range values {
			headers[key] = value
		}
	}
	return headers
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
