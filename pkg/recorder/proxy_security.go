package recorder

import (
	"crypto/subtle"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/jasonczech/apexload/pkg/netpolicy"
)

const (
	defaultProxyAddr             = "127.0.0.1:8081"
	minStrictProxyAuthTokenBytes = 32
	recorderControlHeaderPrefix  = "x-apexload-recorder-"

	envProxyAllowProduction      = "APEXLOAD_RECORDER_PROXY_ALLOW_PRODUCTION"
	envProxyAllowUnauthenticated = "APEXLOAD_RECORDER_PROXY_ALLOW_UNAUTHENTICATED"
	envProxyAllowPrivateTargets  = "APEXLOAD_RECORDER_ALLOW_PRIVATE_TARGETS"
	envProxyAuthToken            = "APEXLOAD_RECORDER_PROXY_TOKEN"
	envRecorderTLSInsecure       = "APEXLOAD_RECORDER_TLS_INSECURE_SKIP_VERIFY"
)

// Why the recording proxy is not running, as ProxyState reports it.
const (
	ProxyNotStarted         = "not_started"
	ProxyProductionDisabled = "production_disabled"
	ProxyConfiguration      = "configuration"
	ProxyBindFailed         = "bind_failed"
	ProxyStopped            = "stopped"
)

// proxyStartupError is a refusal to start the proxy, with the reason code
// ProxyState reports for it. Its message is the operator-facing cause.
type proxyStartupError struct {
	code string
	err  error
}

func (e *proxyStartupError) Error() string { return e.err.Error() }
func (e *proxyStartupError) Unwrap() error { return e.err }

func proxyStartupFailure(code string, err error) error {
	return &proxyStartupError{code: code, err: err}
}

// proxyTargetRejectedError is a target the proxy will not forward to: a
// private, loopback or link-local address unless private targets are allowed.
// Sessions count these, so the studio can say why traffic is missing.
type proxyTargetRejectedError struct{ message string }

func (e *proxyTargetRejectedError) Error() string { return e.message }

func rejectedProxyTarget(format string, args ...any) error {
	return &proxyTargetRejectedError{message: fmt.Sprintf(format, args...)}
}

func isRejectedProxyTarget(err error) bool {
	var rejected *proxyTargetRejectedError
	return errors.As(err, &rejected)
}

func proxyProductionMode() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("APEXLOAD_ENV"))) {
	case "production", "prod":
		return true
	default:
		return false
	}
}

func proxyEnvBool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on", "enabled":
		return true
	default:
		return false
	}
}

func proxyBindIsLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		host = strings.TrimSpace(addr)
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

func (r *Recorder) validateProxyStartup() error {
	if r.requireProxyAuth && strings.TrimSpace(r.proxyAuthToken) == "" {
		return proxyStartupFailure(ProxyConfiguration, fmt.Errorf("recording proxy authentication token is required"))
	}
	if r.requireProxyAuth && len(r.proxyAuthToken) < minStrictProxyAuthTokenBytes {
		return proxyStartupFailure(ProxyConfiguration, fmt.Errorf("recording proxy authentication token must be at least %d bytes", minStrictProxyAuthTokenBytes))
	}
	if r.requireLoopback && !proxyBindIsLoopback(r.addr) {
		return proxyStartupFailure(ProxyConfiguration, fmt.Errorf("recording proxy strict mode requires a loopback bind, got %q", r.addr))
	}
	strictProtectedProxy := r.requireProxyAuth && r.requireLoopback
	if proxyProductionMode() && !strictProtectedProxy && !proxyEnvBool(envProxyAllowProduction) {
		return proxyStartupFailure(ProxyProductionDisabled, fmt.Errorf("recording proxy is disabled in production mode; set %s=true to opt in explicitly", envProxyAllowProduction))
	}
	if !proxyBindIsLoopback(r.addr) && strings.TrimSpace(r.proxyAuthToken) == "" && !proxyEnvBool(envProxyAllowUnauthenticated) {
		return proxyStartupFailure(ProxyConfiguration, fmt.Errorf("recording proxy wide bind %q requires %s or explicit %s=true", r.addr, envProxyAuthToken, envProxyAllowUnauthenticated))
	}
	if proxyProductionMode() && proxyEnvBool(envRecorderTLSInsecure) {
		return proxyStartupFailure(ProxyConfiguration, fmt.Errorf("%s is a development-only override and cannot be enabled in production", envRecorderTLSInsecure))
	}
	return nil
}

func recorderUpstreamTLSConfig(serverName string) *tls.Config {
	return &tls.Config{
		MinVersion:         tls.VersionTLS12,
		ServerName:         serverName,
		InsecureSkipVerify: !proxyProductionMode() && proxyEnvBool(envRecorderTLSInsecure), //nolint:gosec // explicit development-only operator override
	}
}

// isRecorderControlHeader identifies private browser-to-proxy control headers.
// These headers may contain the proxy authentication token and must never reach
// an origin, a recording, or any artifact derived from a recording.
func isRecorderControlHeader(name string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(name)), recorderControlHeaderPrefix)
}

// stripProxyRequestHeaders removes proxy credentials, recorder-control
// headers, and hop-by-hop fields before a browser request reaches the origin.
// WebSocket handshakes retain only the Connection/Upgrade pair they require.
func stripProxyRequestHeaders(header http.Header, websocket bool) {
	if header == nil {
		return
	}
	for _, nominated := range strings.Split(header.Get("Connection"), ",") {
		name := strings.TrimSpace(nominated)
		if websocket && strings.EqualFold(name, "upgrade") {
			continue
		}
		header.Del(name)
	}
	for _, name := range []string{
		"Proxy-Connection", "Proxy-Authorization", "Proxy-Authenticate",
		"Keep-Alive", "TE", "Trailer", "Transfer-Encoding",
	} {
		header.Del(name)
	}
	if !websocket {
		header.Del("Connection")
		header.Del("Upgrade")
	} else {
		header.Set("Connection", "Upgrade")
	}
	for name := range header {
		if isRecorderControlHeader(name) {
			header.Del(name)
		}
	}
}

func (r *Recorder) authorizeProxyRequest(req *http.Request) bool {
	token := r.ProxyAuthToken()
	if token == "" {
		return !r.requireProxyAuth
	}
	for _, candidate := range proxyAuthCandidates(req) {
		if subtle.ConstantTimeCompare([]byte(candidate), []byte(token)) == 1 {
			return true
		}
	}
	return false
}

func proxyAuthCandidates(req *http.Request) []string {
	values := make([]string, 0, 3)
	if req == nil {
		return values
	}
	if token := strings.TrimSpace(req.Header.Get("X-APEXLoad-Recorder-Token")); token != "" {
		values = append(values, token)
	}
	auth := strings.TrimSpace(req.Header.Get("Proxy-Authorization"))
	if auth == "" {
		return values
	}
	if len(auth) > len("bearer ") && strings.EqualFold(auth[:len("bearer ")], "bearer ") {
		values = append(values, strings.TrimSpace(auth[len("bearer "):]))
		return values
	}
	if strings.HasPrefix(strings.ToLower(auth), "basic ") {
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(auth[len("basic "):]))
		if err == nil {
			_, password, ok := strings.Cut(string(decoded), ":")
			if ok {
				values = append(values, password)
			}
		}
	}
	return values
}

func validateProxyTargetURL(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("invalid proxy target url: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" && parsed.Scheme != "ws" && parsed.Scheme != "wss" {
		return fmt.Errorf("proxy target scheme %q is not allowed", parsed.Scheme)
	}
	return validateProxyTargetHost(parsed.Hostname())
}

// proxyTargetHostname is a target URL's host, for counting a rejection.
func proxyTargetHostname(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

func validateProxyTargetHost(host string) error {
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if host == "" {
		return fmt.Errorf("proxy target is missing a host")
	}
	if proxyEnvBool(envProxyAllowPrivateTargets) {
		return nil
	}
	lower := strings.ToLower(host)
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") {
		return rejectedProxyTarget("proxy target host %q is not allowed", host)
	}
	if ip := net.ParseIP(host); ip != nil && disallowedProxyTargetIP(ip) {
		return rejectedProxyTarget("proxy target host %q is not allowed", host)
	}
	return nil
}

func disallowedProxyTargetIP(ip net.IP) bool {
	return !netpolicy.IsPublicIP(ip)
}

func proxyDialControl(network, address string, _ syscall.RawConn) error {
	if proxyEnvBool(envProxyAllowPrivateTargets) {
		return nil
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil {
		return fmt.Errorf("unresolved proxy target host %q", host)
	}
	if disallowedProxyTargetIP(ip) {
		return rejectedProxyTarget("proxy target address %s is not allowed", ip)
	}
	return nil
}

func proxySafeDialer(timeout time.Duration) *net.Dialer {
	return &net.Dialer{Timeout: timeout, Control: proxyDialControl}
}
