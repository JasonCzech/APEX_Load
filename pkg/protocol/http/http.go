package http

import (
	"bytes"
	"compress/flate"
	"compress/zlib"
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

	"github.com/andybalholm/brotli"
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
	streaming                *streamingProfile
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
	if err := validateAuthType(protocol.ConfigString(config, "auth_type")); err != nil {
		return err
	}
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
	method := actionHTTPMethod(action)
	requestBody := action.Body
	redactedRequestBody := action.RedactedBody
	if strings.EqualFold(strings.TrimSpace(action.BodyEncoding), "base64") && len(action.Body) > 0 {
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(action.Body)))
		if err != nil {
			result.Duration = time.Since(result.StartTime)
			result.ErrorMessage = fmt.Sprintf("invalid base64 http body: %v", err)
			result.Snapshot = buildHTTPSnapshot(method, targetURL, nil, action.Body, action.RedactedTarget, action.RedactedHeaders, action.RedactedBody, 0, nil, nil)
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
		setRequestHeader(req, k, v)
	}

	authHeader := h.authHeader
	actionAuthHeader, actionAuthOverride, actionAuthErr := buildActionAuthHeader(action.Auth)
	if actionAuthErr != nil {
		result.Duration = time.Since(result.StartTime)
		result.ErrorMessage = actionAuthErr.Error()
		result.Snapshot = buildHTTPSnapshot(method, targetURL, req.Header, requestBody, action.RedactedTarget, action.RedactedHeaders, redactedRequestBody, 0, nil, nil)
		return result, nil
	}
	if actionAuthOverride {
		authHeader = actionAuthHeader
	}
	typedAuthApplied := false
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
		typedAuthApplied = actionAuthOverride
	}

	headerListNames := headerListNameSet(action.HeaderList)
	// Apply per-action headers (override defaults)
	for k, v := range action.Headers {
		if _, exists := headerListNames[strings.ToLower(strings.TrimSpace(k))]; exists {
			continue
		}
		setRequestHeader(req, k, v)
	}
	deleteRequestHeaders(req, headerListNames)
	for _, header := range action.HeaderList {
		addRequestHeader(req, header.Name, header.Value)
	}
	typedAuthApplied = typedAuthApplied && req.Header.Get("Authorization") == actionAuthHeader
	redactedAuthHeader := buildRedactedActionAuthHeader(action.RedactedAuth)
	if redactedAuthHeader == "" {
		redactedAuthHeader = buildRedactedActionAuthHeader(action.Auth)
	}
	redactedRequestHeaders := redactedRequestHeadersForSnapshot(req.Header, action.RedactedHeaders, typedAuthApplied, redactedAuthHeader)
	explicitCorrelationID := correlationIDFromHTTPHeader(req.Header)
	generatedTraceparent := ensureTraceparentHeader(req.Header)
	redactedRequestHeaders = requestHeadersWithRedactedHeaderList(redactedRequestHeaders, action.RedactedHeaderList)
	redactedRequestHeaders = requestHeadersWithHostForSnapshot(req.Header, redactedRequestHeaders, req.Host, action.RedactedHeaders)

	// httptrace callbacks run on the transport's connection goroutines, so every
	// field they touch must be guarded. They write into traceMu-protected locals;
	// applyTraceTelemetry copies them into result on this goroutine before the
	// timings are read (previously the callbacks wrote result.* directly, racing
	// finalizeHTTPServerTelemetry on the error path under -race).
	var traceMu sync.Mutex
	var dnsStart, connStart, tlsStart time.Time
	var traceDNSTime, traceConnectTime, traceTLSTime, traceTTFB time.Duration
	startTime := result.StartTime
	trace := &httptrace.ClientTrace{
		DNSStart:          func(_ httptrace.DNSStartInfo) { traceMu.Lock(); dnsStart = time.Now(); traceMu.Unlock() },
		DNSDone:           func(_ httptrace.DNSDoneInfo) { traceMu.Lock(); traceDNSTime = time.Since(dnsStart); traceMu.Unlock() },
		ConnectStart:      func(_, _ string) { traceMu.Lock(); connStart = time.Now(); traceMu.Unlock() },
		ConnectDone:       func(_, _ string, _ error) { traceMu.Lock(); traceConnectTime = time.Since(connStart); traceMu.Unlock() },
		TLSHandshakeStart: func() { traceMu.Lock(); tlsStart = time.Now(); traceMu.Unlock() },
		TLSHandshakeDone: func(_ tls.ConnectionState, _ error) {
			traceMu.Lock()
			traceTLSTime = time.Since(tlsStart)
			traceMu.Unlock()
		},
		GotConn:              func(info httptrace.GotConnInfo) { traceMu.Lock(); connectionReused = info.Reused; traceMu.Unlock() },
		GotFirstResponseByte: func() { traceMu.Lock(); traceTTFB = time.Since(startTime); traceMu.Unlock() },
	}
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
	client := *h.client
	if action.TimeoutMs > 0 {
		client.Timeout = time.Duration(action.TimeoutMs) * time.Millisecond
	}
	if action.MaxRedirects != nil {
		client.CheckRedirect = nil
		applyRedirectPolicy(&client, *action.MaxRedirects)
	}
	baseCheckRedirect := client.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		traceMu.Lock()
		redirectCount = len(via)
		if !redirectLegStart.IsZero() {
			redirectDuration += time.Since(redirectLegStart)
		}
		redirectLegStart = time.Now()
		traceMu.Unlock()
		if baseCheckRedirect != nil {
			return baseCheckRedirect(req, via)
		}
		return nil
	}
	// applyTraceTelemetry publishes the trace timings into result and returns the
	// connection/redirect counters, all under traceMu. Call it on this goroutine
	// before reading the timings.
	applyTraceTelemetry := func() (bool, int, time.Duration) {
		traceMu.Lock()
		defer traceMu.Unlock()
		result.DNSTime = traceDNSTime
		result.ConnectTime = traceConnectTime
		result.TLSTime = traceTLSTime
		result.TTFB = traceTTFB
		return connectionReused, redirectCount, redirectDuration
	}

	if requestBytes > 0 {
		result.BytesSent = requestBytes
	}

	resp, err := client.Do(req)
	if err != nil {
		result.Duration = time.Since(result.StartTime)
		reused, rCount, rDur := applyTraceTelemetry()
		result.ServerTelemetry = finalizeHTTPServerTelemetry(result, requestBytes, 0, reused, rCount, rDur)
		result.ErrorMessage = err.Error()
		result.Snapshot = buildHTTPSnapshot(method, targetURL, req.Header, requestBody, action.RedactedTarget, redactedRequestHeaders, redactedRequestBody, 0, nil, nil)
		return result, nil
	}

	buf := h.bufPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer h.bufPool.Put(buf)

	bodyReadStart := time.Now()
	var (
		n             int64
		bodyReadErr   error
		sseFirstEvent time.Duration
		sseEventCount int
	)
	maxBody := httpMaxBodyBytes(action.Params)
	isEventStream := shouldReadSSE(resp.Header.Get("Content-Type"), action.Params)
	if isEventStream {
		sseResult, err := readSSEResponse(ctx, resp.Body, action.Params, result.StartTime)
		_, _ = buf.Write(sseResult.Body)
		n = sseResult.BytesRead
		sseFirstEvent = sseResult.FirstEvent
		sseEventCount = sseResult.EventCount
		bodyReadErr = err
	} else if maxBody > 0 {
		// Bound the buffered body so a giant (or pathologically compressible)
		// response becomes a failed sample instead of OOM-ing the generator.
		// max_body_bytes <= 0 opts out (legitimate streaming/download tests).
		n, bodyReadErr = io.Copy(buf, io.LimitReader(resp.Body, maxBody+1))
		if bodyReadErr == nil && n > maxBody {
			buf.Truncate(int(maxBody))
			n = maxBody
			bodyReadErr = fmt.Errorf("response body exceeded %d-byte limit", maxBody)
		}
	} else {
		n, bodyReadErr = io.Copy(buf, resp.Body)
	}
	bodyReadDuration := time.Since(bodyReadStart)
	resp.Body.Close()

	if delay := h.streaming.responseDelay(resp, action, targetURL, n, bodyReadDuration); !isEventStream && delay > 0 {
		if err := h.streaming.wait(ctx, delay); err != nil {
			result.Duration = time.Since(result.StartTime)
			result.StatusCode = resp.StatusCode
			result.BytesReceived = n
			reused, rCount, rDur := applyTraceTelemetry()
			result.ServerTelemetry = finalizeHTTPServerTelemetry(result, requestBytes, n, reused, rCount, rDur)
			result.ErrorMessage = err.Error()
			result.Snapshot = buildHTTPSnapshot(method, targetURL, req.Header, requestBody, action.RedactedTarget, redactedRequestHeaders, redactedRequestBody, resp.StatusCode, cloneHTTPHeaders(resp.Header), buf.Bytes())
			return result, nil
		}
	}

	result.Duration = time.Since(result.StartTime)
	result.StatusCode = resp.StatusCode
	result.BytesReceived = n
	reused, rCount, rDur := applyTraceTelemetry()
	result.ServerTelemetry = finalizeHTTPServerTelemetry(result, requestBytes, n, reused, rCount, rDur)
	if result.ServerTelemetry != nil {
		result.ServerTelemetry.FirstEventMs = durationToNonNegativeMilliseconds(sseFirstEvent)
		result.ServerTelemetry.EventCount = sseEventCount
	}
	result.Success = resp.StatusCode >= 200 && resp.StatusCode < 400
	if bodyReadErr != nil {
		result.Success = false
		result.ErrorMessage = bodyReadErr.Error()
	}
	result.CorrelationID = explicitCorrelationID
	if result.CorrelationID == "" {
		result.CorrelationID = correlationIDFromHTTPHeader(resp.Header)
	}
	if result.CorrelationID == "" {
		result.CorrelationID = generatedTraceparent
	}
	responseHeaders := cloneHTTPHeaders(resp.Header)

	respBody := append([]byte(nil), buf.Bytes()...)
	respBody, responseHeaders, err = decodeHTTPResponseContent(respBody, resp.Header, responseHeaders, maxBody)
	if err != nil {
		result.Duration = time.Since(result.StartTime)
		result.Success = false
		result.ErrorMessage = err.Error()
		result.Snapshot = buildHTTPSnapshot(method, targetURL, req.Header, requestBody, action.RedactedTarget, redactedRequestHeaders, redactedRequestBody, resp.StatusCode, responseHeaders, buf.Bytes())
		return result, nil
	}
	formattedBody, err := applyDataFormattingChain(respBody, h.dataFormattingAutoDetect, h.dataFormattingChain)
	if err != nil {
		result.Duration = time.Since(result.StartTime)
		result.ErrorMessage = err.Error()
		result.Snapshot = buildHTTPSnapshot(method, targetURL, req.Header, requestBody, action.RedactedTarget, redactedRequestHeaders, redactedRequestBody, resp.StatusCode, responseHeaders, respBody)
		return result, nil
	}
	respBody = formattedBody
	result.ResponseBody = string(respBody)
	for _, ext := range action.Extractors {
		if ext.Mode == "all" {
			values, err := extractAll(ext, resp.Header, respBody)
			if err == nil && len(values) > 0 {
				for i, value := range values {
					result.Extractions[fmt.Sprintf("%s_%d", ext.Name, i+1)] = value
				}
				result.Extractions[ext.Name+"_count"] = fmt.Sprintf("%d", len(values))
				continue
			}
			if ext.OnMiss == "fail" {
				if result.Success {
					result.Success = false
					if err == nil {
						err = fmt.Errorf("no matches")
					}
					result.ErrorMessage = fmt.Sprintf("extractor %q found no match: %v", ext.Name, err)
				}
				continue
			}
			// A lenient miss still publishes the count so loops over
			// {{var.name_count}} behave.
			result.Extractions[ext.Name+"_count"] = "0"
			if ext.OnMiss == "empty" {
				// Mirror single-value "empty" semantics: assign the base
				// variable an empty string so {{var.name}} expands to "" rather
				// than leaking the literal template into later requests.
				result.Extractions[ext.Name] = ""
			}
			continue
		}
		val, err := extract(ext, resp.Header, respBody)
		if err == nil {
			result.Extractions[ext.Name] = val
			continue
		}
		switch ext.OnMiss {
		case "fail":
			if result.Success {
				result.Success = false
				result.ErrorMessage = fmt.Sprintf("extractor %q found no match: %v", ext.Name, err)
			}
		case "empty":
			// Source-tool NotFound=warning/empty semantics: the variable is
			// assigned an empty string so later templates expand to "".
			result.Extractions[ext.Name] = ""
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
		result.Snapshot = buildHTTPSnapshot(method, targetURL, req.Header, action.Body, action.RedactedTarget, redactedRequestHeaders, action.RedactedBody, resp.StatusCode, responseHeaders, respBody)
	}

	return result, nil
}

func actionHTTPMethod(action *protocol.Action) string {
	if action == nil {
		return http.MethodGet
	}
	if method := strings.ToUpper(strings.TrimSpace(action.Method)); method != "" {
		return method
	}
	method := strings.ToUpper(strings.TrimSpace(action.Type))
	if isKnownHTTPMethod(method) {
		return method
	}
	return http.MethodGet
}

func isKnownHTTPMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodConnect, http.MethodOptions, http.MethodTrace:
		return true
	default:
		return false
	}
}

// defaultHTTPMaxBodyBytes bounds the buffered+decoded response body when the
// caller does not specify max_body_bytes. A value <= 0 (via the action param)
// disables the cap for legitimate streaming/large-download tests.
const defaultHTTPMaxBodyBytes = 64 << 20 // 64 MiB

func httpMaxBodyBytes(params map[string]any) int64 {
	if params != nil {
		if _, ok := params["max_body_bytes"]; ok {
			return int64(sseParamInt(params, "max_body_bytes", 0))
		}
	}
	return defaultHTTPMaxBodyBytes
}

// readAllLimited reads all of r, rejecting payloads larger than maxBytes when
// maxBytes > 0 (bounds decompression bombs). maxBytes <= 0 reads unbounded.
func readAllLimited(r io.Reader, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		return io.ReadAll(r)
	}
	data, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("decoded response body exceeded %d-byte limit", maxBytes)
	}
	return data, nil
}

func decodeHTTPResponseContent(body []byte, headers http.Header, snapshotHeaders map[string]string, maxBytes int64) ([]byte, map[string]string, error) {
	encodings := responseContentEncodings(headers)
	if len(encodings) == 0 {
		return body, snapshotHeaders, nil
	}

	decoded := append([]byte(nil), body...)
	decodedAny := false
	for i := len(encodings) - 1; i >= 0; i-- {
		encoding := encodings[i]
		if encoding == "identity" {
			continue
		}
		var err error
		switch encoding {
		case "gzip", "x-gzip":
			decoded, err = decodeGzipBody(decoded, maxBytes)
		case "deflate":
			decoded, err = decodeDeflateBody(decoded, maxBytes)
		case "br":
			decoded, err = decodeBrotliBody(decoded, maxBytes)
		default:
			return body, snapshotHeaders, fmt.Errorf("unsupported response content encoding %q", encoding)
		}
		if err != nil {
			return body, snapshotHeaders, fmt.Errorf("decode response content encoding %q: %w", encoding, err)
		}
		decodedAny = true
	}
	if decodedAny {
		snapshotHeaders = decodedResponseHeaders(snapshotHeaders)
	}
	return decoded, snapshotHeaders, nil
}

func responseContentEncodings(headers http.Header) []string {
	if len(headers) == 0 {
		return nil
	}
	values := headers.Values("Content-Encoding")
	encodings := make([]string, 0, len(values))
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			encoding := strings.ToLower(strings.TrimSpace(part))
			if encoding != "" {
				encodings = append(encodings, encoding)
			}
		}
	}
	return encodings
}

func decodeDeflateBody(body []byte, maxBytes int64) ([]byte, error) {
	zlibReader, err := zlib.NewReader(bytes.NewReader(body))
	if err == nil {
		defer zlibReader.Close()
		return readAllLimited(zlibReader, maxBytes)
	}

	flateReader := flate.NewReader(bytes.NewReader(body))
	defer flateReader.Close()
	return readAllLimited(flateReader, maxBytes)
}

func decodeBrotliBody(body []byte, maxBytes int64) ([]byte, error) {
	return readAllLimited(brotli.NewReader(bytes.NewReader(body)), maxBytes)
}

func decodedResponseHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	decoded := cloneStringMap(headers)
	for key := range decoded {
		if strings.EqualFold(key, "Content-Encoding") || strings.EqualFold(key, "Content-Length") {
			delete(decoded, key)
		}
	}
	if len(decoded) == 0 {
		return nil
	}
	return decoded
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

func setRequestHeader(req *http.Request, key string, value string) {
	if strings.EqualFold(strings.TrimSpace(key), "Host") {
		req.Host = strings.TrimSpace(value)
		return
	}
	req.Header.Set(key, value)
}

func addRequestHeader(req *http.Request, key string, value string) {
	if strings.EqualFold(strings.TrimSpace(key), "Host") {
		req.Host = strings.TrimSpace(value)
		return
	}
	req.Header.Add(key, value)
}

func headerListNameSet(headers protocol.HeaderList) map[string]struct{} {
	if len(headers) == 0 {
		return nil
	}
	names := make(map[string]struct{}, len(headers))
	for _, header := range headers {
		name := strings.ToLower(strings.TrimSpace(header.Name))
		if name != "" {
			names[name] = struct{}{}
		}
	}
	return names
}

func deleteRequestHeaders(req *http.Request, names map[string]struct{}) {
	for name := range names {
		if name == "host" {
			req.Host = ""
			continue
		}
		req.Header.Del(name)
	}
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

func redactedRequestHeadersForSnapshot(headers http.Header, redactedHeaders map[string]string, redactAuthorization bool, redactedAuthorization string) map[string]string {
	if !redactAuthorization {
		return redactedHeaders
	}
	out := cloneHTTPHeaders(headers)
	if len(redactedHeaders) > 0 {
		out = cloneStringMap(redactedHeaders)
	}
	if out == nil {
		out = map[string]string{}
	}
	if strings.TrimSpace(redactedAuthorization) == "" {
		redactedAuthorization = "[REDACTED]"
	}
	out["Authorization"] = redactedAuthorization
	return out
}

func requestHeadersWithHostForSnapshot(headers http.Header, snapshotHeaders map[string]string, host string, redactedHeaders map[string]string) map[string]string {
	host = strings.TrimSpace(host)
	if host == "" {
		return snapshotHeaders
	}
	out := cloneStringMap(snapshotHeaders)
	if out == nil {
		out = cloneHTTPHeaders(headers)
	}
	if out == nil {
		out = map[string]string{}
	}
	if redactedHost := redactedHeaderValue(redactedHeaders, "Host"); redactedHost != "" {
		out["Host"] = redactedHost
	} else {
		out["Host"] = host
	}
	return out
}

func requestHeadersWithRedactedHeaderList(snapshotHeaders map[string]string, redactedHeaderList protocol.HeaderList) map[string]string {
	if len(redactedHeaderList) == 0 {
		return snapshotHeaders
	}
	out := cloneStringMap(snapshotHeaders)
	if out == nil {
		out = map[string]string{}
	}
	for _, header := range redactedHeaderList {
		name := strings.TrimSpace(header.Name)
		if name == "" {
			continue
		}
		if existing := out[name]; existing != "" {
			out[name] = existing + ", " + header.Value
		} else {
			out[name] = header.Value
		}
	}
	return out
}

func redactedHeaderValue(headers map[string]string, key string) string {
	for headerKey, value := range headers {
		if strings.EqualFold(headerKey, key) {
			return value
		}
	}
	return ""
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
