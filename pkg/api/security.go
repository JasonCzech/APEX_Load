package api

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// productionMode reports whether the server is configured for a hardened
// production posture. When true, auth bypass modes are refused at boot,
// dev-preview headers are ignored, and session cookies are forced Secure.
//
// It is enabled by APEXLOAD_ENV=production (or "prod") or by the explicit
// APEXLOAD_REQUIRE_AUTH flag.
func productionMode() bool {
	switch strings.ToLower(strings.TrimSpace(envValue("APEXLOAD_ENV"))) {
	case "production", "prod":
		return true
	}
	return envBool("APEXLOAD_REQUIRE_AUTH")
}

// envValue is a thin wrapper used so security helpers read configuration the
// same way as the rest of the package.
func envValue(name string) string {
	return strings.TrimSpace(os.Getenv(name))
}

// originAllowed reports whether a request's Origin header is permitted under
// the configured allow-list. It is shared by the CORS middleware and the
// WebSocket upgrade check so the two can never drift apart.
//
//   - An empty Origin (non-browser / same-origin navigation) is permitted.
//   - When an explicit allow-list is configured, only listed origins (or "*")
//     are permitted.
//   - When no allow-list is configured, only same-origin requests (Origin host
//     matches the request Host) are permitted — never an arbitrary reflected
//     origin.
func originAllowed(allowedOrigins []string, r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	for _, o := range allowedOrigins {
		o = strings.TrimSpace(o)
		if o == "*" {
			return true
		}
		if strings.EqualFold(o, origin) {
			return true
		}
	}
	if len(allowedOrigins) > 0 {
		return false
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}
	return strings.EqualFold(parsed.Host, r.Host)
}

// exportBaseDir returns the directory that script exports are confined to.
func exportBaseDir() string {
	if dir := envValue("APEXLOAD_EXPORT_DIR"); dir != "" {
		return dir
	}
	return "exports"
}

// resolveConfinedExportPath resolves a user-supplied output directory and
// filename to an absolute path guaranteed to live within base. The filename is
// always sanitized (its directory component is discarded) and the output
// directory is rooted so that "../" sequences cannot escape the base.
func resolveConfinedExportPath(base, userDir, filename string) (string, error) {
	base = strings.TrimSpace(base)
	if base == "" {
		base = "exports"
	}
	absBase, err := filepath.Abs(base)
	if err != nil {
		return "", fmt.Errorf("resolve export base directory: %w", err)
	}

	safeName := sanitizeFilename(filepath.Base(strings.TrimSpace(filename)))
	if safeName == "" || safeName == "." || safeName == ".." {
		safeName = "script.json"
	}

	// Force-root the user directory then strip the leading separator so that
	// any leading or embedded ".." can only collapse back to the root, never
	// above it. Absolute paths are likewise neutralized.
	rooted := filepath.Clean("/" + filepath.ToSlash(strings.TrimSpace(userDir)))
	sub := strings.TrimPrefix(rooted, "/")

	target := filepath.Clean(filepath.Join(absBase, filepath.FromSlash(sub), safeName))
	if target != absBase && !strings.HasPrefix(target, absBase+string(filepath.Separator)) {
		return "", fmt.Errorf("resolved export path escapes the export directory")
	}
	return target, nil
}

// redactURL returns a log-safe representation of a URL: scheme + host only.
// Webhook URLs (e.g. Slack/Teams incoming webhooks) embed secret tokens in
// their path or query, so the full URL must never be logged.
func redactURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" {
		return "[redacted-url]"
	}
	return parsed.Scheme + "://" + parsed.Host + "/[redacted]"
}

// allowPrivateOutbound reports whether SSRF protection is disabled for
// outbound webhook/metric requests (trusted on-prem deployments).
func allowPrivateOutbound() bool {
	return envBool("APEXLOAD_ALLOW_PRIVATE_WEBHOOKS")
}

// validateOutboundURL performs a syntactic SSRF check suitable for both
// config-save time and a fast request-time pre-check: it rejects non-http(s)
// schemes, missing hosts, literal IPs in the loopback/link-local/private/
// unspecified ranges, and localhost names. It deliberately does NOT perform DNS
// resolution — that is unreliable for not-yet-provisioned endpoints and is
// rebindable. The authoritative, DNS-rebind-proof check happens at connect time
// via ssrfSafeDialControl, which inspects the actually-resolved IP.
func validateOutboundURL(raw string) error {
	return validateOutboundURLAllowingHosts(raw, nil)
}

// validateOutboundURLAllowingHosts is validateOutboundURL with a narrow,
// caller-supplied allow-list of hostnames that may be private (e.g. a self-hosted
// GitHub Enterprise Server on an internal address). A host in `allowed` passes
// even if it resolves to a private range; every other host is still subject to
// the standard SSRF rejection. This keeps the control-plane locked down while
// permitting exactly one operator-configured internal endpoint.
func validateOutboundURLAllowingHosts(raw string, allowed map[string]struct{}) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("url scheme must be http or https")
	}
	if parsed.User != nil {
		return fmt.Errorf("url must not contain userinfo credentials")
	}
	host := parsed.Hostname()
	if host == "" {
		return fmt.Errorf("url is missing a host")
	}
	if _, ok := allowed[strings.ToLower(host)]; ok {
		return nil
	}
	if allowPrivateOutbound() {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil {
		if isDisallowedIP(ip) {
			return fmt.Errorf("url host %s is not an allowed destination", host)
		}
		return nil
	}
	lower := strings.ToLower(host)
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") {
		return fmt.Errorf("url host %q is not an allowed destination", host)
	}
	return nil
}

// validateCredentialTransportURL rejects cleartext credential transport by
// default. The opt-in is intentionally caller-specific so enabling an isolated
// development integration cannot weaken unrelated outbound clients.
func validateCredentialTransportURL(label, raw, insecureHTTPEnv string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("%s must be a valid http(s) URL", label)
	}
	if parsed.User != nil {
		return fmt.Errorf("%s must not contain userinfo credentials", label)
	}
	if parsed.Scheme != "https" && !envBool(insecureHTTPEnv) {
		return fmt.Errorf("%s must use https; set %s only for isolated development", label, insecureHTTPEnv)
	}
	return nil
}

// sameOriginRedirectPolicy prevents credential-bearing clients from forwarding
// a request body or headers to a different origin, and also blocks HTTPS to HTTP
// downgrade redirects. Same-origin path redirects remain supported.
func sameOriginRedirectPolicy(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return fmt.Errorf("stopped after %d redirects", len(via))
	}
	if req == nil || req.URL == nil || len(via) == 0 || via[len(via)-1] == nil || via[len(via)-1].URL == nil {
		return fmt.Errorf("refusing redirect with an invalid origin")
	}
	if req.URL.User != nil {
		return fmt.Errorf("refusing redirect target containing userinfo credentials")
	}
	previous := via[len(via)-1].URL
	if !strings.EqualFold(req.URL.Scheme, previous.Scheme) || !strings.EqualFold(req.URL.Host, previous.Host) {
		return fmt.Errorf("refusing cross-origin or scheme-changing redirect from %s://%s to %s://%s", previous.Scheme, previous.Host, req.URL.Scheme, req.URL.Host)
	}
	return nil
}

// isDisallowedIP reports whether an IP is in a range we never allow outbound
// (control-plane) requests to reach.
func isDisallowedIP(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsPrivate()
}

// ssrfSafeDialControl returns a net.Dialer Control function that re-validates
// the resolved IP at connect time, defeating DNS-rebinding attacks that pass
// the up-front validateOutboundURL check but then resolve to a private address.
func ssrfSafeDialControl(network, address string, _ syscall.RawConn) error {
	if allowPrivateOutbound() {
		return nil
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("unresolved outbound host %q", host)
	}
	if isDisallowedIP(ip) {
		return fmt.Errorf("outbound connection to %s blocked", ip)
	}
	return nil
}

// newSSRFSafeHTTPClient builds an HTTP client whose dialer rejects connections
// to disallowed IP ranges at connect time.
func newSSRFSafeHTTPClient(timeout time.Duration) *http.Client {
	return newHostAllowlistedHTTPClient(nil, timeout)
}

// newHostAllowlistedHTTPClient builds an SSRF-safe HTTP client that additionally
// permits connections to a small set of operator-configured hostnames even if
// they resolve to private ranges (e.g. an internal GitHub Enterprise Server).
// The allow check is by the pre-resolution request host, so only the configured
// host bypasses the private-IP block; all other hosts keep ssrfSafeDialControl.
// Pass nil/empty allowedHosts for the standard locked-down client.
func newHostAllowlistedHTTPClient(allowedHosts []string, timeout time.Duration) *http.Client {
	return newHostAllowlistedHTTPClientWithTLS(allowedHosts, nil, timeout)
}

// newHostAllowlistedHTTPClientWithTLS is newHostAllowlistedHTTPClient with an
// optional custom TLS config (e.g. an internal CA bundle for a self-hosted GHES).
func newHostAllowlistedHTTPClientWithTLS(allowedHosts []string, tlsConfig *tls.Config, timeout time.Duration) *http.Client {
	allow := make(map[string]struct{}, len(allowedHosts))
	for _, h := range allowedHosts {
		if trimmed := strings.ToLower(strings.TrimSpace(h)); trimmed != "" {
			allow[trimmed] = struct{}{}
		}
	}
	safeDialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second, Control: ssrfSafeDialControl}
	plainDialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				host = addr
			}
			if _, ok := allow[strings.ToLower(host)]; ok {
				return plainDialer.DialContext(ctx, network, addr)
			}
			return safeDialer.DialContext(ctx, network, addr)
		},
		TLSClientConfig:       tlsConfig,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          50,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	return &http.Client{Timeout: timeout, Transport: transport, CheckRedirect: sameOriginRedirectPolicy}
}
