package http

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
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
	client                   *http.Client
	transport                *http.Transport
	baseURL                  string
	bufPool                  sync.Pool
	contentChecks            []contentCheck
	dataFormattingAutoDetect bool
	dataFormattingChain      []dataFormattingTransform
	defaultHeaders           map[string]string
	authType                 string
	authHeader               string // pre-computed Authorization header value
	maxRedirects             int    // -1 = no follow, 0 = default (10)
	requestTimeout           time.Duration
}

func (h *HTTP) Name() string { return "http" }

func (h *HTTP) Init(ctx context.Context, config map[string]any) error {
	h.baseURL, _ = config["base_url"].(string)
	transportSettings := parseTransportConfig(config)
	h.requestTimeout = transportSettings.requestTimeout

	tlsCfg, err := buildTLSConfig(config)
	if err != nil {
		return err
	}

	h.transport = newTransport(transportSettings, tlsCfg)
	applyHTTPVersion(h.transport, config)
	if err := applyProxy(h.transport, config); err != nil {
		return err
	}

	jar := buildCookieJar(config)
	h.maxRedirects = parseMaxRedirects(config)

	h.client = &http.Client{
		Transport: h.transport,
		Jar:       jar,
		Timeout:   h.requestTimeout,
	}
	applyRedirectPolicy(h.client, h.maxRedirects)

	h.defaultHeaders = buildDefaultHeaders(config)
	h.contentChecks = parseContentChecks(config)
	h.dataFormattingAutoDetect = parseDataFormattingAutoDetect(config)
	h.dataFormattingChain = parseDataFormattingChain(config)
	h.authType, h.authHeader = buildAuthHeader(config)

	h.bufPool = sync.Pool{
		New: func() any { return new(bytes.Buffer) },
	}

	return nil
}

func (h *HTTP) Execute(ctx context.Context, action *protocol.Action) (*protocol.Result, error) {
	result := &protocol.Result{
		ActionName:     action.Name,
		StartTime:      time.Now(),
		Extractions:    make(map[string]string),
		DependencyType: "http",
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
		result.Snapshot = buildHTTPSnapshot(method, targetURL, nil, action.Body, 0, nil, nil)
		return result, nil
	}
	result.DependencyTarget = classifyHTTPDependencyTarget(targetURL)
	result.CorrelationID = correlationIDFromHeaders(action.Headers)

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
		result.Snapshot = buildHTTPSnapshot(method, targetURL, req.Header, action.Body, 0, nil, nil)
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
	if result.CorrelationID == "" {
		result.CorrelationID = correlationIDFromHTTPHeader(resp.Header)
	}
	responseHeaders := cloneHTTPHeaders(resp.Header)

	respBody := buf.Bytes()
	formattedBody, err := applyDataFormattingChain(respBody, h.dataFormattingAutoDetect, h.dataFormattingChain)
	if err != nil {
		result.Duration = time.Since(result.StartTime)
		result.ErrorMessage = err.Error()
		result.Snapshot = buildHTTPSnapshot(method, targetURL, req.Header, action.Body, resp.StatusCode, responseHeaders, respBody)
		return result, nil
	}
	respBody = formattedBody
	result.ResponseBody = string(respBody)
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
	if result.Success {
		if err := evaluateContentChecks(h.contentChecks, result, respBody); err != nil {
			result.Success = false
			result.ErrorMessage = err.Error()
		}
	}
	if !result.Success {
		result.Snapshot = buildHTTPSnapshot(method, targetURL, req.Header, action.Body, resp.StatusCode, responseHeaders, respBody)
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

func classifyHTTPDependencyTarget(targetURL string) string {
	parsed, err := url.Parse(targetURL)
	if err != nil {
		return ""
	}
	return parsed.Host
}

func correlationIDFromHeaders(headers map[string]string) string {
	if len(headers) == 0 {
		return ""
	}
	canonical := make(http.Header, len(headers))
	for key, value := range headers {
		canonical.Set(key, value)
	}
	return correlationIDFromHTTPHeader(canonical)
}

func correlationIDFromHTTPHeader(headers http.Header) string {
	for _, key := range []string{"X-Correlation-Id", "X-Request-Id", "Traceparent", "X-Amzn-Trace-Id", "Correlation-Id", "Request-Id"} {
		if value := strings.TrimSpace(headers.Get(key)); value != "" {
			return value
		}
	}
	return ""
}

func buildHTTPSnapshot(method string, targetURL string, requestHeaders http.Header, requestBody []byte, responseStatus int, responseHeaders map[string]string, responseBody []byte) *protocol.ErrorSnapshot {
	return &protocol.ErrorSnapshot{
		RequestMethod:   method,
		RequestURL:      targetURL,
		RequestHeaders:  cloneHTTPHeaders(requestHeaders),
		RequestBody:     string(requestBody),
		ResponseStatus:  responseStatus,
		ResponseHeaders: cloneStringMap(responseHeaders),
		ResponseBody:    string(responseBody),
	}
}

func cloneHTTPHeaders(headers http.Header) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(headers))
	for key, values := range headers {
		if len(values) == 0 {
			continue
		}
		cloned[key] = strings.Join(values, ", ")
	}
	if len(cloned) == 0 {
		return nil
	}
	return cloned
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}
