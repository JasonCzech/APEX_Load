package http

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptrace"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/stormrunner/pkg/protocol"
)

func init() {
	protocol.Register("http", func() protocol.Protocol {
		return &HTTP{}
	})
}

type HTTP struct {
	client         *http.Client
	transport      *http.Transport
	baseURL        string
	bufPool        sync.Pool
	defaultHeaders map[string]string
	authType       string
	authHeader     string // pre-computed Authorization header value
	maxRedirects   int    // -1 = no follow, 0 = default (10)
	requestTimeout time.Duration
}

func (h *HTTP) Name() string { return "http" }

func (h *HTTP) Init(ctx context.Context, config map[string]any) error {
	h.baseURL, _ = config["base_url"].(string)

	// --- Connection pool ---
	maxConnsPerHost := 100
	if v, ok := config["max_conns_per_host"].(float64); ok {
		maxConnsPerHost = int(v)
	}
	maxIdleConns := maxConnsPerHost
	if v, ok := config["max_idle_conns"].(float64); ok {
		maxIdleConns = int(v)
	}

	// --- Timeouts ---
	connectTimeout := 30 * time.Second
	if v, ok := config["connect_timeout_ms"].(float64); ok {
		connectTimeout = time.Duration(v) * time.Millisecond
	}
	tlsTimeout := 10 * time.Second
	if v, ok := config["tls_timeout_ms"].(float64); ok {
		tlsTimeout = time.Duration(v) * time.Millisecond
	}
	idleTimeout := 90 * time.Second
	if v, ok := config["idle_timeout_ms"].(float64); ok {
		idleTimeout = time.Duration(v) * time.Millisecond
	}
	h.requestTimeout = 60 * time.Second
	if v, ok := config["request_timeout_ms"].(float64); ok {
		h.requestTimeout = time.Duration(v) * time.Millisecond
	}

	keepAlive := 30 * time.Second
	disableKeepAlive := false
	if v, ok := config["keep_alive"].(bool); ok && !v {
		disableKeepAlive = true
		keepAlive = 0
	}

	// --- TLS ---
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}

	if v, ok := config["tls_min_version"].(string); ok {
		if ver, err := parseTLSVersion(v); err == nil {
			tlsCfg.MinVersion = ver
		}
	}
	if v, ok := config["tls_max_version"].(string); ok {
		if ver, err := parseTLSVersion(v); err == nil {
			tlsCfg.MaxVersion = ver
		}
	}
	if v, ok := config["tls_insecure_skip_verify"].(bool); ok {
		tlsCfg.InsecureSkipVerify = v
	}

	// Client certificates
	certFile, _ := config["tls_client_cert_file"].(string)
	keyFile, _ := config["tls_client_key_file"].(string)
	if certFile != "" && keyFile != "" {
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return fmt.Errorf("load client certificate: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}

	// Custom CA
	if caFile, ok := config["tls_ca_cert_file"].(string); ok && caFile != "" {
		caCert, err := os.ReadFile(caFile)
		if err != nil {
			return fmt.Errorf("read CA certificate: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caCert) {
			return fmt.Errorf("failed to parse CA certificate from %s", caFile)
		}
		tlsCfg.RootCAs = pool
	}

	// --- Transport ---
	h.transport = &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   connectTimeout,
			KeepAlive: keepAlive,
		}).DialContext,
		MaxIdleConns:        maxIdleConns,
		MaxIdleConnsPerHost: maxConnsPerHost,
		MaxConnsPerHost:     maxConnsPerHost,
		IdleConnTimeout:     idleTimeout,
		TLSHandshakeTimeout: tlsTimeout,
		TLSClientConfig:     tlsCfg,
		DisableKeepAlives:   disableKeepAlive,
	}

	// HTTP version
	if v, ok := config["http_version"].(string); ok {
		switch v {
		case "2":
			h.transport.ForceAttemptHTTP2 = true
		case "1.1", "1.0":
			// Disable HTTP/2 for explicit 1.x
			h.transport.ForceAttemptHTTP2 = false
			h.transport.TLSClientConfig.NextProtos = []string{"http/1.1"}
		default:
			h.transport.ForceAttemptHTTP2 = true
		}
	} else {
		h.transport.ForceAttemptHTTP2 = true
	}

	// --- Proxy ---
	if proxyURL, ok := config["proxy_url"].(string); ok && proxyURL != "" {
		parsedProxy, err := url.Parse(proxyURL)
		if err != nil {
			return fmt.Errorf("parse proxy URL: %w", err)
		}

		// Proxy authentication
		proxyUser, _ := config["proxy_username"].(string)
		proxyPass, _ := config["proxy_password"].(string)
		if proxyUser != "" {
			parsedProxy.User = url.UserPassword(proxyUser, proxyPass)
		}

		h.transport.Proxy = http.ProxyURL(parsedProxy)
	}

	// --- Cookie jar ---
	cookiesEnabled := true
	if v, ok := config["cookies_enabled"].(bool); ok {
		cookiesEnabled = v
	}
	var jar http.CookieJar
	if cookiesEnabled {
		jar, _ = cookiejar.New(nil)
	}

	// --- Redirect policy ---
	h.maxRedirects = 10
	if v, ok := config["max_redirects"].(float64); ok {
		h.maxRedirects = int(v)
	}

	h.client = &http.Client{
		Transport: h.transport,
		Jar:       jar,
		Timeout:   h.requestTimeout,
	}

	if h.maxRedirects == -1 {
		h.client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		}
	} else if h.maxRedirects != 10 {
		maxR := h.maxRedirects
		h.client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxR {
				return fmt.Errorf("stopped after %d redirects", maxR)
			}
			return nil
		}
	}

	// --- Default headers ---
	h.defaultHeaders = make(map[string]string)
	if v, ok := config["user_agent"].(string); ok && v != "" {
		h.defaultHeaders["User-Agent"] = v
	}
	if v, ok := config["accept_encoding"].(string); ok && v != "" {
		h.defaultHeaders["Accept-Encoding"] = v
	}
	if v, ok := config["accept_language"].(string); ok && v != "" {
		h.defaultHeaders["Accept-Language"] = v
	}
	if hdrs, ok := config["default_headers"].(map[string]string); ok {
		for k, v := range hdrs {
			h.defaultHeaders[k] = v
		}
	}

	// --- Authentication ---
	h.authType, _ = config["auth_type"].(string)
	switch strings.ToLower(h.authType) {
	case "basic":
		user, _ := config["auth_username"].(string)
		pass, _ := config["auth_password"].(string)
		h.authHeader = "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
	case "bearer":
		token, _ := config["auth_token"].(string)
		h.authHeader = "Bearer " + token
	case "ntlm":
		// NTLM requires a specialized transport; log a warning for now
		// Full NTLM support requires go-ntlmssp package
		fmt.Println("[WARN] NTLM authentication is not yet fully supported; using basic auth fallback")
		user, _ := config["auth_username"].(string)
		pass, _ := config["auth_password"].(string)
		h.authHeader = "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
	}

	h.bufPool = sync.Pool{
		New: func() any { return new(bytes.Buffer) },
	}

	return nil
}

func (h *HTTP) Execute(ctx context.Context, action *protocol.Action) (*protocol.Result, error) {
	result := &protocol.Result{
		ActionName:  action.Name,
		StartTime:   time.Now(),
		Extractions: make(map[string]string),
	}

	targetURL := h.resolveURL(action.Target)

	var body io.Reader
	if len(action.Body) > 0 {
		body = bytes.NewReader(action.Body)
	}

	method := strings.ToUpper(action.Method)
	if method == "" {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(ctx, method, targetURL, body)
	if err != nil {
		result.Duration = time.Since(result.StartTime)
		result.ErrorMessage = err.Error()
		return result, nil
	}

	// Apply default headers first
	for k, v := range h.defaultHeaders {
		req.Header.Set(k, v)
	}

	// Apply global auth header
	if h.authHeader != "" {
		req.Header.Set("Authorization", h.authHeader)
	}

	// Apply per-action headers (override defaults)
	for k, v := range action.Headers {
		req.Header.Set(k, v)
	}

	var dnsStart, connStart, tlsStart time.Time
	trace := &httptrace.ClientTrace{
		DNSStart:             func(_ httptrace.DNSStartInfo) { dnsStart = time.Now() },
		DNSDone:              func(_ httptrace.DNSDoneInfo) { result.DNSTime = time.Since(dnsStart) },
		ConnectStart:         func(_, _ string) { connStart = time.Now() },
		ConnectDone:          func(_, _ string, _ error) { result.ConnectTime = time.Since(connStart) },
		TLSHandshakeStart:    func() { tlsStart = time.Now() },
		TLSHandshakeDone:     func(_ tls.ConnectionState, _ error) { result.TLSTime = time.Since(tlsStart) },
		GotFirstResponseByte: func() { result.TTFB = time.Since(result.StartTime) },
	}
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))

	if len(action.Body) > 0 {
		result.BytesSent = int64(len(action.Body))
	}

	resp, err := h.client.Do(req)
	if err != nil {
		result.Duration = time.Since(result.StartTime)
		result.ErrorMessage = err.Error()
		return result, nil
	}

	buf := h.bufPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer h.bufPool.Put(buf)

	n, _ := io.Copy(buf, resp.Body)
	resp.Body.Close()

	result.Duration = time.Since(result.StartTime)
	result.StatusCode = resp.StatusCode
	result.BytesReceived = n
	result.Success = resp.StatusCode >= 200 && resp.StatusCode < 400

	respBody := buf.Bytes()
	for _, ext := range action.Extractors {
		val, err := extract(ext, resp.Header, respBody)
		if err == nil {
			result.Extractions[ext.Name] = val
		}
	}

	for _, assertion := range action.Assertions {
		if err := evaluate(assertion, result, respBody); err != nil {
			result.Success = false
			result.ErrorMessage = err.Error()
			break
		}
	}

	return result, nil
}

func (h *HTTP) Teardown(_ context.Context) error {
	h.transport.CloseIdleConnections()
	return nil
}

func (h *HTTP) HealthCheck(ctx context.Context) error {
	if h.baseURL == "" {
		return fmt.Errorf("no base_url configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, h.baseURL, nil)
	if err != nil {
		return err
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (h *HTTP) resolveURL(target string) string {
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		return target
	}
	return strings.TrimRight(h.baseURL, "/") + "/" + strings.TrimLeft(target, "/")
}

// parseTLSVersion converts a human-readable TLS version string to the crypto/tls constant.
func parseTLSVersion(v string) (uint16, error) {
	switch v {
	case "1.0":
		return tls.VersionTLS10, nil
	case "1.1":
		return tls.VersionTLS11, nil
	case "1.2":
		return tls.VersionTLS12, nil
	case "1.3":
		return tls.VersionTLS13, nil
	default:
		return 0, fmt.Errorf("unknown TLS version: %s", v)
	}
}
