package http

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
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
	streaming                streamingProfile
	defaultHeaders           map[string]string
	authType                 string
	authHeader               string // pre-computed Authorization header value
	maxRedirects             int    // -1 = no follow, 0 = default (10)
	requestTimeout           time.Duration
	networkProfile           string // assigned network-profile label (population mix)
}

var (
	absoluteHTTPURLBoundaryWhitespace = regexp.MustCompile(`^(https?://[^/?#\s]+)\s+([/?#].*)$`)
	leadingTemplateBaseURLPattern     = regexp.MustCompile(`^\{\{\s*(?:var|param)\.[^{}]+\s*\}\}\s*`)
)

func (h *HTTP) Name() string { return "http" }

func (h *HTTP) Init(ctx context.Context, config map[string]any) error {
	h.baseURL, _ = config["base_url"].(string)
	h.networkProfile, _ = config["web_network_profile_label"].(string)
	transportSettings := parseTransportConfig(config)
	h.requestTimeout = transportSettings.requestTimeout

	tlsCfg, err := buildTLSConfig(config)
	if err != nil {
		return err
	}

	h.transport = newTransport(transportSettings, tlsCfg)
	applyHTTPVersion(h.transport, config)
	applyNetworkSimulation(h.transport, config)
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
	h.streaming = parseStreamingProfile(config)
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
		NetworkProfile: h.networkProfile,
	}

	targetURL := h.resolveURL(action.Target)
	requestBody := action.Body
	redactedRequestBody := action.RedactedBody
	if strings.EqualFold(strings.TrimSpace(action.BodyEncoding), "base64") && len(action.Body) > 0 {
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(action.Body)))
		if err != nil {
			result.Duration = time.Since(result.StartTime)
			result.ErrorMessage = fmt.Sprintf("invalid base64 http body: %v", err)
			result.Snapshot = buildHTTPSnapshot(action.Method, targetURL, nil, action.Body, action.RedactedTarget, action.RedactedHeaders, action.RedactedBody, 0, nil, nil)
			return result, nil
		}
		requestBody = decoded
		if len(redactedRequestBody) > 0 {
			if decodedRedacted, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(redactedRequestBody))); err == nil {
				redactedRequestBody = decodedRedacted
			}
		}
	}
	requestBytes := int64(len(requestBody))
	connectionReused := false
	redirectCount := 0
	redirectDuration := time.Duration(0)
	redirectLegStart := result.StartTime

	var body io.Reader
	if len(requestBody) > 0 {
		body = bytes.NewReader(requestBody)
	}

	method := strings.ToUpper(action.Method)
	if method == "" {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(ctx, method, targetURL, body)
	if err != nil {
		result.Duration = time.Since(result.StartTime)
		result.ErrorMessage = err.Error()
		result.Snapshot = buildHTTPSnapshot(method, targetURL, nil, requestBody, action.RedactedTarget, action.RedactedHeaders, redactedRequestBody, 0, nil, nil)
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
	explicitCorrelationID := correlationIDFromHTTPHeader(req.Header)
	generatedTraceparent := ensureTraceparentHeader(req.Header)

	var dnsStart, connStart, tlsStart time.Time
	trace := &httptrace.ClientTrace{
		DNSStart:             func(_ httptrace.DNSStartInfo) { dnsStart = time.Now() },
		DNSDone:              func(_ httptrace.DNSDoneInfo) { result.DNSTime = time.Since(dnsStart) },
		ConnectStart:         func(_, _ string) { connStart = time.Now() },
		ConnectDone:          func(_, _ string, _ error) { result.ConnectTime = time.Since(connStart) },
		TLSHandshakeStart:    func() { tlsStart = time.Now() },
		TLSHandshakeDone:     func(_ tls.ConnectionState, _ error) { result.TLSTime = time.Since(tlsStart) },
		GotConn:              func(info httptrace.GotConnInfo) { connectionReused = info.Reused },
		GotFirstResponseByte: func() { result.TTFB = time.Since(result.StartTime) },
	}
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
	baseCheckRedirect := h.client.CheckRedirect
	client := *h.client
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		redirectCount = len(via)
		if !redirectLegStart.IsZero() {
			redirectDuration += time.Since(redirectLegStart)
		}
		redirectLegStart = time.Now()
		if baseCheckRedirect != nil {
			return baseCheckRedirect(req, via)
		}
		return nil
	}

	if requestBytes > 0 {
		result.BytesSent = requestBytes
	}

	resp, err := client.Do(req)
	if err != nil {
		result.Duration = time.Since(result.StartTime)
		result.ServerTelemetry = finalizeHTTPServerTelemetry(result, requestBytes, 0, connectionReused, redirectCount, redirectDuration)
		result.ErrorMessage = err.Error()
		result.Snapshot = buildHTTPSnapshot(method, targetURL, req.Header, requestBody, action.RedactedTarget, action.RedactedHeaders, redactedRequestBody, 0, nil, nil)
		return result, nil
	}

	buf := h.bufPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer h.bufPool.Put(buf)

	bodyReadStart := time.Now()
	n, _ := io.Copy(buf, resp.Body)
	bodyReadDuration := time.Since(bodyReadStart)
	resp.Body.Close()

	if delay := h.streaming.responseDelay(resp, action, targetURL, n, bodyReadDuration); delay > 0 {
		if err := h.streaming.wait(ctx, delay); err != nil {
			result.Duration = time.Since(result.StartTime)
			result.StatusCode = resp.StatusCode
			result.BytesReceived = n
			result.ServerTelemetry = finalizeHTTPServerTelemetry(result, requestBytes, n, connectionReused, redirectCount, redirectDuration)
			result.ErrorMessage = err.Error()
			result.Snapshot = buildHTTPSnapshot(method, targetURL, req.Header, requestBody, action.RedactedTarget, action.RedactedHeaders, redactedRequestBody, resp.StatusCode, cloneHTTPHeaders(resp.Header), buf.Bytes())
			return result, nil
		}
	}

	result.Duration = time.Since(result.StartTime)
	result.StatusCode = resp.StatusCode
	result.BytesReceived = n
	result.ServerTelemetry = finalizeHTTPServerTelemetry(result, requestBytes, n, connectionReused, redirectCount, redirectDuration)
	result.Success = resp.StatusCode >= 200 && resp.StatusCode < 400
	result.CorrelationID = explicitCorrelationID
	if result.CorrelationID == "" {
		result.CorrelationID = correlationIDFromHTTPHeader(resp.Header)
	}
	if result.CorrelationID == "" {
		result.CorrelationID = generatedTraceparent
	}
	responseHeaders := cloneHTTPHeaders(resp.Header)

	respBody := buf.Bytes()
	formattedBody, err := applyDataFormattingChain(respBody, h.dataFormattingAutoDetect, h.dataFormattingChain)
	if err != nil {
		result.Duration = time.Since(result.StartTime)
		result.ErrorMessage = err.Error()
		result.Snapshot = buildHTTPSnapshot(method, targetURL, req.Header, requestBody, action.RedactedTarget, action.RedactedHeaders, redactedRequestBody, resp.StatusCode, responseHeaders, respBody)
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
		result.Snapshot = buildHTTPSnapshot(method, targetURL, req.Header, action.Body, action.RedactedTarget, action.RedactedHeaders, action.RedactedBody, resp.StatusCode, responseHeaders, respBody)
	}

	return result, nil
}

func ensureTraceparentHeader(headers http.Header) string {
	if headers == nil {
		return ""
	}
	if existing := strings.TrimSpace(headers.Get("Traceparent")); existing != "" {
		return existing
	}
	traceparent := newTraceparent()
	headers.Set("Traceparent", traceparent)
	return traceparent
}

func newTraceparent() string {
	traceID := make([]byte, 16)
	if _, err := rand.Read(traceID); err != nil || bytes.Equal(traceID, make([]byte, len(traceID))) {
		traceID[len(traceID)-1] = 1
	}
	spanID := make([]byte, 8)
	if _, err := rand.Read(spanID); err != nil || bytes.Equal(spanID, make([]byte, len(spanID))) {
		spanID[len(spanID)-1] = 1
	}
	return "00-" + hex.EncodeToString(traceID) + "-" + hex.EncodeToString(spanID) + "-01"
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
	return ResolveURL(h.baseURL, target)
}

func ResolveURL(baseURL string, target string) string {
	target = NormalizeURLTarget(target)
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		return target
	}
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return target
	}
	target = TrimLeadingTemplateBaseURL(target)
	return strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(target, "/")
}

func NormalizeURLTarget(target string) string {
	target = strings.TrimSpace(target)
	return absoluteHTTPURLBoundaryWhitespace.ReplaceAllString(target, "$1$2")
}

func TrimLeadingTemplateBaseURL(target string) string {
	return leadingTemplateBaseURLPattern.ReplaceAllString(strings.TrimSpace(target), "")
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

func buildHTTPSnapshot(method string, targetURL string, requestHeaders http.Header, requestBody []byte, redactedTarget string, redactedHeaders map[string]string, redactedBody []byte, responseStatus int, responseHeaders map[string]string, responseBody []byte) *protocol.ErrorSnapshot {
	requestURL := snapshotRequestURL(targetURL, redactedTarget)
	headers := cloneHTTPHeaders(requestHeaders)
	if len(redactedHeaders) > 0 {
		headers = cloneStringMap(redactedHeaders)
	}
	body := string(requestBody)
	if len(redactedBody) > 0 {
		body = string(redactedBody)
	}
	return &protocol.ErrorSnapshot{
		RequestMethod:   method,
		RequestURL:      requestURL,
		RequestHeaders:  headers,
		RequestBody:     body,
		ResponseStatus:  responseStatus,
		ResponseHeaders: cloneStringMap(responseHeaders),
		ResponseBody:    string(responseBody),
	}
}

func snapshotRequestURL(targetURL, redactedTarget string) string {
	redacted := strings.TrimSpace(redactedTarget)
	if redacted == "" {
		return targetURL
	}
	if strings.Contains(redacted, "{{") && strings.Contains(redacted, "}}") {
		return targetURL
	}
	return NormalizeURLTarget(redacted)
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

func finalizeHTTPServerTelemetry(result *protocol.Result, requestBytes int64, responseBytes int64, connectionReused bool, redirectCount int, redirectDuration time.Duration) *protocol.ServerTelemetry {
	if result == nil {
		return nil
	}
	telemetry := &protocol.ServerTelemetry{
		ConnectionReused: connectionReused,
		RedirectCount:    redirectCount,
		RedirectMs:       durationToNonNegativeMilliseconds(redirectDuration),
	}
	if requestBytes > 0 {
		telemetry.RequestPayloadKB = bytesToKilobytes(requestBytes)
		telemetry.RequestSizeClass = protocol.ClassifyPayloadSize(requestBytes)
	}
	if responseBytes > 0 {
		telemetry.ResponsePayloadKB = bytesToKilobytes(responseBytes)
		telemetry.ResponseSizeClass = protocol.ClassifyPayloadSize(responseBytes)
	}
	if result.TTFB > 0 {
		telemetry.ServerProcessingMs = durationToNonNegativeMilliseconds(result.TTFB - result.DNSTime - result.ConnectTime - result.TLSTime)
	}
	if result.Duration > result.TTFB {
		telemetry.TransferMs = durationToNonNegativeMilliseconds(result.Duration - result.TTFB)
	}
	return telemetry
}

func durationToNonNegativeMilliseconds(value time.Duration) float64 {
	if value <= 0 {
		return 0
	}
	return float64(value) / float64(time.Millisecond)
}

func bytesToKilobytes(value int64) float64 {
	if value <= 0 {
		return 0
	}
	return float64(value) / 1024.0
}
