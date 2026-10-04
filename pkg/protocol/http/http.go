package http

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/internal/contentdecode"
	"github.com/jasonczech/apexload/pkg/protocol"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

func init() {
	protocol.Register("http/1.1", func() protocol.Protocol { return &HTTP{aliasVersion: VersionHTTP11} })
	protocol.Register("http/2", func() protocol.Protocol { return &HTTP{aliasVersion: VersionHTTP2} })
	protocol.Register("http", func() protocol.Protocol {
		return &HTTP{}
	})
}

type HTTP struct {
	receiveBufferBytes     int
	maxMetaRefresh         int
	contentCheckErrorLimit int
	outcome                replayOutcomePolicy
	authDelay              time.Duration

	aliasVersion               string
	policyPools                map[string]*httpPolicyPool
	connections                *httpConnectionBudget
	client                     *http.Client
	oauthClient                *http.Client
	transport                  *http.Transport
	oauthTransport             *http.Transport
	allowedTargetHosts         []string
	baseURL                    string
	bufPool                    *httpBufferPool
	contentChecks              []contentCheck
	verificationMu             sync.RWMutex
	verifications              map[string]registeredVerification
	dataFormattingAutoDetect   bool
	dataFormattingChain        []dataFormattingTransform
	streaming                  *streamingProfile
	defaultHeaders             map[string]string
	typedDefaultHeaders        map[string]string
	defaultHeaderList          protocol.HeaderList
	authType                   string
	authHeader                 string // pre-computed Authorization header value
	httpVersion                string // empty = automatic; "1.1"/"2" = exact
	maxRedirects               int    // <=0 = no follow, unset/default = 10
	requestTimeout             time.Duration
	pageTimeout                time.Duration
	htmlParseMode              string
	htmlExcludedMIMETypes      []string
	cacheMaxUnusedIterations   int
	networkProfile             string // assigned network-profile label (population mix)
	traceparentEnabled         bool
	traceparentSampled         bool
	authConfig                 *protocol.AuthConfig
	cache                      *httpResponseCache
	cacheContentURLsOnly       bool
	cacheCheckNewerVersions    bool
	resources                  *resourceFetcher
	captureCorrelationEvidence bool
}

const (
	maxPooledHTTPBuffers             = 32
	maxPooledHTTPBufferSize          = 256 << 10
	maxHTTPResponseHeaderBytes int64 = 64 << 10
	// Keep direct protocol callers under the same ceiling as authored DSL
	// actions. Input-byte charging alone is insufficient for empty responses,
	// where an attacker-controlled extractor list would otherwise perform an
	// unbounded number of expression parses/lookups at zero charged bytes.
	maxHTTPActionExtractors = protocol.MaxExtractionEntries
)

// httpBufferPool is deliberately count- and capacity-bounded. sync.Pool can
// retain every buffer returned between garbage collections; under thousands of
// VUsers a burst of near-limit responses could therefore retain gigabytes of
// backing arrays after the samples finished.
type httpBufferPool struct {
	buffers chan *bytes.Buffer
}

var processHTTPBufferPool = newHTTPBufferPool()

func newHTTPBufferPool() *httpBufferPool {
	return &httpBufferPool{buffers: make(chan *bytes.Buffer, maxPooledHTTPBuffers)}
}

func (p *httpBufferPool) get() *bytes.Buffer {
	if p != nil {
		select {
		case buffer := <-p.buffers:
			buffer.Reset()
			return buffer
		default:
		}
	}
	return new(bytes.Buffer)
}

func (p *httpBufferPool) put(buffer *bytes.Buffer) {
	if p == nil || buffer == nil || buffer.Cap() > maxPooledHTTPBufferSize {
		return
	}
	buffer.Reset()
	select {
	case p.buffers <- buffer:
	default:
	}
}

var (
	absoluteHTTPURLBoundaryWhitespace = regexp.MustCompile(`^(https?://[^/?#\s]+)\s+([/?#].*)$`)
	leadingTemplateBaseURLPattern     = regexp.MustCompile(`^\{\{\s*(?:var|param)\.[^{}]+\s*\}\}\s*`)
)

func (h *HTTP) Name() string { return "http" }

func (h *HTTP) ParallelSafe() bool { return true }

// Initialized options are immutable during a group. net/http clients and cookie
// jars are concurrent-safe; cache, verification, network shaping, and streaming
// state have their own locks. Resource waves retain request-local observation.
// Teardown and iteration resets occur only after the engine's join barrier.
func (h *HTTP) SharedSessionConcurrentSafe() bool { return h != nil && h.client != nil }

func (h *HTTP) Init(ctx context.Context, config map[string]any) error {
	if h.aliasVersion != "" {
		if raw, exists := config["http_version"]; !exists || raw == nil || raw == "" {
			copyConfig := make(map[string]any, len(config)+1)
			for key, value := range config {
				copyConfig[key] = value
			}
			copyConfig["http_version"] = h.aliasVersion
			config = copyConfig
		}
	}
	if err := validateHTTPRuntimeConfigBounds(config); err != nil {
		return err
	}
	h.receiveBufferBytes = protocol.ConfigInt(config, "receive_buffer_bytes")
	h.maxMetaRefresh = protocol.ConfigInt(config, "max_meta_refresh")
	h.contentCheckErrorLimit = protocol.ConfigInt(config, "content_check_error_limit")
	h.outcome = parseReplayOutcomePolicy(config)
	h.authDelay = boundedHTTPTimeout(config, "challenge_delay_ms")
	h.baseURL, _ = config["base_url"].(string)
	h.captureCorrelationEvidence, _ = config[protocol.CorrelationEvidenceCaptureConfigKey].(bool)
	h.allowedTargetHosts = protocol.TargetPolicyHosts(config)
	h.networkProfile, _ = config["web_network_profile_label"].(string)
	h.traceparentEnabled = parseTraceparentEnabled(config)
	h.traceparentSampled = parseTraceparentSampled(config)
	transportSettings := parseTransportConfig(config)
	h.requestTimeout = transportSettings.requestTimeout
	h.pageTimeout = boundedHTTPTimeout(config, "page_timeout_ms")
	h.htmlParseMode = strings.ToLower(strings.TrimSpace(protocol.ConfigString(config, "html_parse_mode")))
	h.htmlExcludedMIMETypes, _ = compatibilityMIMETypes(config["html_excluded_mime_types"])
	cacheMaxUnusedIterations, _ := exactHTTPConfigInteger(config["cache_max_unused_iterations"])
	h.cacheMaxUnusedIterations = int(cacheMaxUnusedIterations)

	tlsCfg, err := buildTLSConfig(config)
	if err != nil {
		return err
	}

	if err := h.initializeHTTPPolicyPools(ctx, config, transportSettings, tlsCfg); err != nil {
		return err
	}

	h.defaultHeaders = buildDefaultHeaders(config)
	h.typedDefaultHeaders = buildTypedDefaultHeaders(config)
	h.defaultHeaderList = buildDefaultHeaderList(config)
	h.contentChecks = parseContentChecks(config)
	h.dataFormattingAutoDetect = parseDataFormattingAutoDetect(config)
	h.dataFormattingChain = parseDataFormattingChain(config)
	h.streaming = parseStreamingProfile(config)
	if parseHTTPCacheEnabled(config) {
		h.cache = newHTTPResponseCache()
		h.cache.excludedMIMETypes, _ = compatibilityMIMETypes(config["cache_excluded_mime_types"])
	} else {
		h.cache = nil
	}
	h.cacheContentURLsOnly, _ = config["http_cache_content_urls_only"].(bool)
	h.cacheCheckNewerVersions, _ = config["http_cache_check_newer_versions"].(bool)
	h.resources = newResourceFetcher(h, config)
	if err := validateAuthType(protocol.ConfigString(config, "auth_type")); err != nil {
		return err
	}
	h.authType, h.authHeader = buildAuthHeader(config)
	h.authConfig = buildAuthConfig(config)
	if err := ValidateAuthWireValues(h.authConfig); err != nil {
		return err
	}

	// Protocol instances are per-VUser, so the bounded pool must be shared at
	// process scope; a per-instance pool would still retain one backing array per
	// VUser and multiply the apparent bound by the fleet size.
	h.bufPool = processHTTPBufferPool

	return nil
}

func (h *HTTP) Execute(ctx context.Context, action *protocol.Action) (*protocol.Result, error) {
	ctx, verification := h.verificationContext(ctx)
	result := &protocol.Result{
		ActionName:     protocol.ActionName(action),
		StartTime:      time.Now(),
		Extractions:    make(map[string]string),
		DependencyType: "http",
		NetworkProfile: h.networkProfile,
	}
	resourceTimeoutWarned := false
	callerCtx := ctx
	if h.pageTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeoutCause(ctx, h.pageTimeout, errPageDeadline)
		defer cancel()
		defer func() {
			if context.Cause(ctx) == errPageDeadline && !(resourceTimeoutWarned && callerCtx.Err() == nil && result.Success) {
				result.Success = false
				result.ErrorMessage = errPageDeadline.Error()
				result.Duration = time.Since(result.StartTime)
				result.ProtocolEvidence = buildHTTPProtocolEvidence(action, result)
			}
		}()
	}
	if action == nil {
		result.StatusCode = http.StatusBadRequest
		result.ErrorMessage = "http action is required"
		return result, nil
	}
	if err := validateHTTPActionBounds(action); err != nil {
		result.StatusCode = http.StatusBadRequest
		result.ErrorMessage = err.Error()
		result.Duration = time.Since(result.StartTime)
		result.ProtocolEvidence = buildHTTPProtocolEvidence(action, result)
		return result, nil
	}
	if skip, _ := action.Params["legacy_html_check"].(bool); skip && !h.outcome.legacyChecks && !hasAuraSuccessAssertion(action.Assertions) {
		result.Success = true
		result.ProtocolEvidence = map[string]any{"legacy_html_check": "disabled"}
		return result, nil
	}
	policy, policyErr := h.effectiveHTTPVersion(action)
	if policyErr == nil {
		policyErr = h.validateHTTPVersionTLS(policy, h.resolveURL(action.Target))
	}
	if policyErr != nil {
		result.StatusCode = http.StatusBadRequest
		result.ErrorMessage = policyErr.Error()
		return result, nil
	}
	h = h.forHTTPVersion(policy)
	ctx = context.WithValue(ctx, httpVersionCacheKey{}, policy)
	result.HTTPVersionPolicy = policy
	defer func() { result.ProtocolEvidence = buildHTTPProtocolEvidence(action, result) }()
	defaultHeaders := h.defaultHeaders
	defaultHeaderList := h.defaultHeaderList
	if action.ResolvedDefaultHeaders {
		defaultHeaders = mergeDefaultHeaders(h.typedDefaultHeaders, action.DefaultHeaders)
		defaultHeaderList = action.DefaultHeaderList
	}
	effectiveHeaders, headerMapIssues := effectiveAuthoredHeaderList(defaultHeaders, defaultHeaderList, action.Headers, action.HeaderList)
	headerIssues := append(headerMapIssues, HeaderListIssuesForVersion(effectiveHeaders, h.httpVersion)...)
	if len(headerIssues) > 0 {
		err := &HeaderListValidationError{Issues: headerIssues}
		result.StatusCode = http.StatusBadRequest
		result.ErrorMessage = err.Error()
		result.Duration = time.Since(result.StartTime)
		result.ProtocolEvidence = buildHTTPProtocolEvidence(action, result)
		return result, nil
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
	if strings.EqualFold(action.BodyEncoding, "windows-1252") {
		var e error
		requestBody, e = encodeWindows1252Body(action.Body)
		if e != nil {
			result.ErrorMessage = "HTTP body cannot be represented in Windows-1252"
			result.Duration = time.Since(result.StartTime)
			return result, nil
		}
		if len(redactedRequestBody) > 0 {
			redactedRequestBody, _ = encodeWindows1252Body(redactedRequestBody)
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
		result.Success = false
		// net/http parse errors quote the full target, including URL userinfo
		// and query values. The snapshot already has the governed redacted
		// target; keep the public diagnostic value-free.
		result.ErrorMessage = "invalid HTTP request target"
		result.Snapshot = buildHTTPSnapshot(method, targetURL, nil, requestBody, action.RedactedTarget, action.RedactedHeaders, redactedRequestBody, 0, nil, nil)
		return result, nil
	}
	result.DependencyTarget = classifyHTTPDependencyTarget(targetURL)
	result.CorrelationID = correlationIDFromHeaders(action.Headers)

	authHeader := h.authHeader
	digestAuth := digestAuthConfig(h.authConfig)
	actionAuthHeader, actionAuthOverride, actionAuthErr := buildActionAuthHeader(action.Auth)
	if actionAuthErr != nil {
		result.StatusCode = http.StatusBadRequest
		result.Duration = time.Since(result.StartTime)
		result.ErrorMessage = actionAuthErr.Error()
		result.Snapshot = buildHTTPSnapshot(method, targetURL, req.Header, requestBody, action.RedactedTarget, action.RedactedHeaders, redactedRequestBody, 0, nil, nil)
		result.ProtocolEvidence = buildHTTPProtocolEvidence(action, result)
		return result, nil
	}
	if actionAuthOverride {
		authHeader = actionAuthHeader
		digestAuth = digestAuthConfig(action.Auth)
	}
	typedAuthApplied := false
	appliedTypedAuthHeader := authHeader
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
		typedAuthApplied = true
	}

	// Apply the already-projected authored headers after typed auth. This makes
	// the documented precedence explicit: typed settings, script defaults, then
	// action headers. The projection has already removed shadowed lower layers.
	authoredHeaderNames := headerListNameSet(effectiveHeaders)
	authoredHeaderNames = mergeHeaderNameSets(authoredHeaderNames, headerListNameSet(defaultHeaderList))
	authoredHeaderNames = mergeHeaderNameSets(authoredHeaderNames, headerListNameSet(action.HeaderList))
	deleteRequestHeaders(req, authoredHeaderNames)
	for _, header := range effectiveHeaders {
		addRequestHeader(req, header.Name, header.Value)
	}
	mergeRequestCookieHeaders(req)
	// Authored headers are authoritative over typed auth. Digest normally
	// starts without Authorization and retries a 401 challenge, but doing so
	// here would silently replace an authored value on the second request.
	_, authorizationAuthored := authoredHeaderNames["authorization"]
	if digestAuth != nil && (requestHeaderKeyPresent(req.Header, "Authorization") || authorizationAuthored) {
		digestAuth = nil
	}
	typedAuthApplied = typedAuthApplied && req.Header.Get("Authorization") == appliedTypedAuthHeader
	redactedAuthConfig := h.authConfig
	if actionAuthOverride {
		redactedAuthConfig = action.Auth
		if action.RedactedAuth != nil {
			redactedAuthConfig = action.RedactedAuth
		}
	}
	redactedAuthHeader := buildRedactedActionAuthHeader(redactedAuthConfig)
	redactedRequestHeaders := redactedRequestHeadersForSnapshot(req.Header, action.RedactedHeaders, typedAuthApplied, redactedAuthHeader)
	// User-authored traceparent always wins. When traceparent propagation is
	// enabled, continue the active OTel action/test context and fall back to a
	// synthetic header only when no active span exists.
	explicitCorrelationID := correlationIDFromHTTPHeader(req.Header)
	generatedTraceparent := ""
	if h.traceparentEnabled {
		if strings.TrimSpace(req.Header.Get("Traceparent")) == "" {
			injectActiveTraceContext(req.Context(), req.Header)
		}
		generatedTraceparent = ensureTraceparentHeader(req.Header, h.traceparentSampled)
	}
	result.CorrelationID = explicitCorrelationID
	if result.CorrelationID == "" {
		result.CorrelationID = generatedTraceparent
	}
	var staleCacheEntry *httpCacheEntry
	cacheHit := false
	var resp *http.Response
	if h.cache != nil && method == http.MethodGet {
		if entry, fresh := h.cache.lookup(req, time.Now()); entry != nil {
			// "Check for newer versions on every visit" forces revalidation of
			// fresh entries: the stored page is only served after a 304.
			if fresh && !h.cacheCheckNewerVersions {
				resp = entry.response(req)
				cacheHit = true
			} else {
				entry.addConditionalHeaders(req)
				staleCacheEntry = entry
			}
		}
	}
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
		GotConn: func(info httptrace.GotConnInfo) { traceMu.Lock(); connectionReused = info.Reused; traceMu.Unlock() },
		GotFirstResponseByte: func() {
			traceMu.Lock()
			if traceTTFB == 0 {
				traceTTFB = time.Since(startTime)
			}
			traceMu.Unlock()
		},
	}
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
	client := *h.client
	if action.TimeoutMs > 0 {
		client.Timeout = time.Duration(action.TimeoutMs) * time.Millisecond
	} else if sseRequestedByParams(action.Params) {
		streamTimeout, err := protocol.BoundedMilliseconds(action.Params["stream_timeout_ms"], defaultSSEStreamTimeout, MaxConfiguredTimeout)
		if err != nil {
			result.ErrorMessage = fmt.Sprintf("stream_timeout_ms %v", err)
			return result, nil
		}
		if streamTimeout > 0 {
			// readSSEResponse owns the authored collection window. The transport
			// timeout must not preempt that window; retain a small allowance for
			// response headers and orderly body shutdown.
			client.Timeout = streamTimeout + 5*time.Second
		} else {
			client.Timeout = 0
		}
	}
	if action.MaxRedirects != nil {
		client.CheckRedirect = nil
		applyRedirectPolicy(&client, *action.MaxRedirects)
	}
	baseCheckRedirect := client.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		observeVerificationRedirect(ctx, req.Response, false)
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

	if !cacheHit {
		var err error
		resp, err = client.Do(req)
		if err != nil {
			result.Duration = time.Since(result.StartTime)
			reused, rCount, rDur := applyTraceTelemetry()
			result.ServerTelemetry = finalizeHTTPServerTelemetry(result, requestBytes, 0, reused, rCount, rDur)
			result.ErrorMessage = h.transportErrorMessage(err)
			result.Snapshot = buildHTTPSnapshot(method, targetURL, req.Header, requestBody, action.RedactedTarget, redactedRequestHeaders, redactedRequestBody, 0, nil, nil)
			h.attachCorrelationEvidence(result, action, req, targetURL, requestBody, resp, nil, false)
			return result, nil
		}
		if err := h.validateResponseVersion(resp); err != nil {
			_ = resp.Body.Close()
			result.Duration = time.Since(result.StartTime)
			reused, rCount, rDur := applyTraceTelemetry()
			result.ServerTelemetry = finalizeHTTPServerTelemetry(result, requestBytes, 0, reused, rCount, rDur)
			result.ErrorMessage = err.Error()
			result.Snapshot = buildHTTPSnapshot(method, targetURL, req.Header, requestBody, action.RedactedTarget, redactedRequestHeaders, redactedRequestBody, 0, nil, nil)
			h.attachCorrelationEvidence(result, action, req, targetURL, requestBody, resp, nil, false)
			return result, nil
		}
	}
	digestRetried := false
	if !cacheHit && digestAuth != nil {
		var retryErr error
		req, resp, digestRetried, retryErr = retryWithDigestAuthDelay(&client, req, resp, digestAuth, requestBody, h.authDelay)
		// retryWithDigestAuth returns the authenticated request even when its
		// transport fails. Redact that request before every error/version path;
		// waiting until the success-only accounting block below would copy the
		// raw Digest Authorization value into a failure snapshot.
		if digestRetried && req != nil && requestHeaderKeyPresent(req.Header, "Authorization") {
			redactedRequestHeaders = redactedRequestHeadersForSnapshot(req.Header, action.RedactedHeaders, true, "Digest [REDACTED]")
			redactedRequestHeaders = requestHeadersWithRedactedHeaderList(redactedRequestHeaders, action.RedactedHeaderList)
			redactedRequestHeaders = requestHeadersWithHostForSnapshot(req.Header, redactedRequestHeaders, req.Host, action.RedactedHeaders)
		}
		if retryErr != nil {
			result.Duration = time.Since(result.StartTime)
			reused, rCount, rDur := applyTraceTelemetry()
			result.ServerTelemetry = finalizeHTTPServerTelemetry(result, requestBytes, 0, reused, rCount, rDur)
			result.ErrorMessage = h.transportErrorMessage(retryErr)
			result.Snapshot = buildHTTPSnapshot(method, targetURL, req.Header, requestBody, action.RedactedTarget, redactedRequestHeaders, redactedRequestBody, 0, nil, nil)
			h.attachCorrelationEvidence(result, action, req, targetURL, requestBody, resp, nil, false)
			return result, nil
		}
		if err := h.validateResponseVersion(resp); err != nil {
			_ = resp.Body.Close()
			result.Duration = time.Since(result.StartTime)
			reused, rCount, rDur := applyTraceTelemetry()
			result.ServerTelemetry = finalizeHTTPServerTelemetry(result, requestBytes, 0, reused, rCount, rDur)
			result.ErrorMessage = err.Error()
			result.Snapshot = buildHTTPSnapshot(method, targetURL, req.Header, requestBody, action.RedactedTarget, redactedRequestHeaders, redactedRequestBody, 0, nil, nil)
			h.attachCorrelationEvidence(result, action, req, targetURL, requestBody, resp, nil, false)
			return result, nil
		}
		if digestRetried {
			// The challenged request may differ from the original after a
			// redirect (for example POST -> GET). Count the body actually
			// replayed for digest rather than charging the original body twice.
			if req != nil && req.ContentLength > 0 {
				result.BytesSent += req.ContentLength
			}
		}
	}
	if cacheHit {
		result.HTTPVersionSource = "cache"
	} else {
		result.HTTPVersionSource = "wire"
		switch {
		case resp.ProtoMajor == 1 && resp.ProtoMinor == 1:
			result.HTTPVersionNegotiated = VersionHTTP11
		case resp.ProtoMajor == 2 && resp.ProtoMinor == 0:
			result.HTTPVersionNegotiated = VersionHTTP2
		}
	}
	if !cacheHit && staleCacheEntry != nil && resp != nil && resp.StatusCode == http.StatusNotModified {
		h.cache.refresh(req, staleCacheEntry, resp, time.Now())
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		resp = staleCacheEntry.response(req)
		cacheHit = true
	}

	buf := h.bufPool.get()
	buf.Reset()
	defer h.bufPool.put(buf)
	captureLease := processHTTPResponseCaptureBudget.lease()
	defer captureLease.release()

	bodyReadStart := time.Now()
	var (
		n             int64
		bodyReadErr   error
		sseFirstEvent time.Duration
		sseEventCount int
	)
	maxBody := httpMaxBodyBytes(action.Params)
	isEventStream := shouldReadSSE(resp.Header.Get("Content-Type"), action.Params)
	decoderLimit := maxBody
	if isEventStream && sseMaxBodyBytes(action.Params) < decoderLimit {
		decoderLimit = sseMaxBodyBytes(action.Params)
	}
	encodings := resp.Header.Values("Content-Encoding")
	if method == http.MethodHead || resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusNotModified {
		encodings = nil // These wire responses cannot carry representation bytes.
	}
	decoder, decodeErr := contentdecode.NewReader(ctx, resp.Body, encodings, contentdecode.DefaultLimits(decoderLimit))
	if decodeErr != nil {
		bodyReadErr = decodeErr
	} else if isEventStream {
		sseResult, err := readSSEResponse(ctx, decoder, action.Params, result.StartTime, captureLease)
		_, writeErr := (&responseCaptureWriter{buffer: buf, lease: captureLease}).Write(sseResult.Body)
		sseFirstEvent = sseResult.FirstEvent
		sseEventCount = sseResult.EventCount
		bodyReadErr = err
		if bodyReadErr == nil {
			bodyReadErr = writeErr
		}
	} else {
		_, bodyReadErr = copyResponse(&responseCaptureWriter{buffer: buf, lease: captureLease}, decoder, h.receiveBufferBytes)
	}
	bodyReadDuration := time.Since(bodyReadStart)
	contentComplete, contentDecoded := false, false
	if decoder != nil {
		n = decoder.EncodedBytes()
		contentComplete, contentDecoded = decoder.Complete(), decoder.Decoding()
		_ = decoder.Close()
	} else {
		_ = resp.Body.Close()
	}
	if bodyReadErr == nil && !isEventStream && !contentComplete {
		bodyReadErr = &contentdecode.Error{Kind: contentdecode.Truncated}
	}
	defer func() {
		if !contentComplete && isEventStream && bodyReadErr == nil && result.Snapshot != nil && result.Snapshot.ResponseBody != "" && result.Snapshot.ResponseBodyState != "not_captured" && result.ProtocolEvidence["response_content_state"] == "collection_window" {
			result.Snapshot.ResponseBodyState = "partial"
		}
	}()
	contentState := "complete"
	if bodyReadErr != nil {
		contentState = "unavailable"
	} else if !contentComplete {
		contentState = "collection_window"
	}
	contentBasis := "identity"
	if contentDecoded {
		contentBasis = "decoded"
	}
	addResponseContentReceipt(result, contentState, contentBasis)
	if cacheHit {
		result.ProtocolEvidence["response_content_source"] = "cache"
	} else {
		result.ProtocolEvidence["response_content_source"] = "wire"
	}
	if bodyReadErr != nil {
		buf.Reset()
		result.Duration = time.Since(result.StartTime)
		result.StatusCode = resp.StatusCode
		result.BytesReceived = n
		reused, rCount, rDur := applyTraceTelemetry()
		result.ServerTelemetry = finalizeHTTPServerTelemetry(result, requestBytes, n, reused, rCount, rDur)
		result.Success = false
		result.ErrorMessage = fixedResponseReadError(bodyReadErr)
		result.Snapshot = buildHTTPSnapshot(method, targetURL, req.Header, requestBody, action.RedactedTarget, redactedRequestHeaders, redactedRequestBody, resp.StatusCode, cloneHTTPHeaders(resp.Header), nil)
		result.Snapshot.ResponseBodyState = "not_captured"
		h.unavailableResponseContentEvidence(result, false)
		return result, nil
	}
	responseHeaders := cloneHTTPHeaders(resp.Header)
	responseView := *resp
	responseView.Header = resp.Header.Clone()
	if contentDecoded {
		responseHeaders = decodedResponseHeaders(responseHeaders)
		responseView.Header.Del("Content-Encoding")
		responseView.Header.Del("Content-Length")
		responseView.ContentLength = -1
	}

	if delay := h.streaming.responseDelay(resp, action, targetURL, n, bodyReadDuration); !isEventStream && delay > 0 {
		if err := h.streaming.wait(ctx, delay); err != nil {
			result.Duration = time.Since(result.StartTime)
			result.StatusCode = resp.StatusCode
			result.BytesReceived = n
			reused, rCount, rDur := applyTraceTelemetry()
			result.ServerTelemetry = finalizeHTTPServerTelemetry(result, requestBytes, n, reused, rCount, rDur)
			result.ErrorMessage = err.Error()
			result.Snapshot = buildHTTPSnapshot(method, targetURL, req.Header, requestBody, action.RedactedTarget, redactedRequestHeaders, redactedRequestBody, resp.StatusCode, responseHeaders, nil)
			result.Snapshot.ResponseBodyState = "not_captured"
			h.unavailableResponseContentEvidence(result, false)
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
	if resp.StatusCode >= 400 && h.outcome.httpWarnings {
		result.Success = true
		addReplayWarning(result, "http_status_warning")
	}
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
	rawResponseBody := buf.Bytes()
	respBody := rawResponseBody
	formattedBody, err := applyDataFormattingChain(respBody, h.dataFormattingAutoDetect, h.dataFormattingChain, maxBody)
	if err != nil {
		result.Duration = time.Since(result.StartTime)
		result.Success = false
		result.ErrorMessage = err.Error()
		result.Snapshot = buildHTTPSnapshot(method, targetURL, req.Header, requestBody, action.RedactedTarget, redactedRequestHeaders, redactedRequestBody, resp.StatusCode, responseHeaders, nil)
		result.Snapshot.ResponseBodyState = "not_captured"
		addResponseContentReceipt(result, "unavailable", contentBasis)
		h.unavailableResponseContentEvidence(result, false)
		return result, nil
	}
	if h.cache != nil && !cacheHit && contentComplete && !isEventStream {
		// "Cache URLs requiring content" restricts storage to content-bearing
		// (HTML) responses, matching the LR checkbox semantics.
		if !h.cacheContentURLsOnly || isContentBearingResponse(resp.Header) {
			h.cache.store(req, &responseView, rawResponseBody, time.Now())
		}
	}
	respBody = formattedBody
	if contentComplete {
		if refreshed, ok := h.followMetaRefresh(ctx, action, req, resp, respBody, result); ok {
			return refreshed, nil
		}
	}
	if !result.Success && bodyReadErr == nil && result.ErrorMessage == "" && responseStatusExplicitlyAccepted(action.Assertions, result) {
		result.Success = true
	}
	// Snapshot the response/transport outcome before extractors, content checks,
	// and non-status assertions can change the overall action outcome.
	if contentComplete {
		h.attachCorrelationEvidence(result, action, req, targetURL, requestBody, resp, respBody, result.Success)
	} else {
		h.unavailableResponseContentEvidence(result, result.Success)
	}
	extractorWork := newExtractorInputWorkBudget(resp.Header, respBody)
extractors:
	for _, ext := range action.Extractors {
		if err := extractorWork.charge(ext); err != nil {
			result.Success = false
			result.Extractions = nil
			result.ExtractionsIncomplete = true
			result.ErrorMessage = fmt.Sprintf("extractor %q input rejected: %v", ext.Name, err)
			break
		}
		if ext.Mode == "all" {
			values, err := extractAll(ext, resp.Header, respBody)
			if errors.Is(err, errExtractorResourceLimit) {
				result.Success = false
				result.Extractions = nil
				result.ExtractionsIncomplete = true
				result.ErrorMessage = fmt.Sprintf("extractor %q output rejected: %v", ext.Name, err)
				break
			}
			if ext.OnMiss == "default" && (ext.DefaultValue == nil || (err != nil && !errors.Is(err, protocol.ErrExtractionMiss))) {
				result.Success = false
				result.ErrorMessage = fmt.Sprintf("extractor %q default rejected: %v", ext.Name, err)
				continue
			}
			if err == nil && len(values) > 0 {
				for i, value := range values {
					if !storeHTTPExtraction(result, ext, fmt.Sprintf("%s_%d", ext.Name, i+1), value) {
						break extractors
					}
				}
				if !storeHTTPExtraction(result, ext, ext.Name+"_count", fmt.Sprintf("%d", len(values))) {
					break
				}
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
			if !storeHTTPExtraction(result, ext, ext.Name+"_count", "0") {
				break
			}
			if ext.OnMiss == "default" && ext.DefaultValue != nil {
				if !storeHTTPExtraction(result, ext, ext.Name, *ext.DefaultValue) {
					break extractors
				}
			}
			if ext.OnMiss == "empty" {
				// Mirror single-value "empty" semantics: assign the base
				// variable an empty string so {{var.name}} expands to "" rather
				// than leaking the literal template into later requests.
				if !storeHTTPExtraction(result, ext, ext.Name, "") {
					break
				}
			}
			continue
		}
		val, err := extract(ext, resp.Header, respBody)
		if errors.Is(err, errExtractorResourceLimit) {
			result.Success = false
			result.Extractions = nil
			result.ExtractionsIncomplete = true
			result.ErrorMessage = fmt.Sprintf("extractor %q output rejected: %v", ext.Name, err)
			break
		}
		if err == nil {
			if !storeHTTPExtraction(result, ext, ext.Name, val) {
				break
			}
			continue
		}
		switch ext.OnMiss {
		case "default":
			if ext.DefaultValue == nil || !errors.Is(err, protocol.ErrExtractionMiss) {
				result.Success = false
				result.ErrorMessage = fmt.Sprintf("extractor %q failed: %v", ext.Name, err)
				continue
			}
			if ext.DefaultValue != nil {
				if !storeHTTPExtraction(result, ext, ext.Name, *ext.DefaultValue) {
					break extractors
				}
			}
		case "fail":
			if result.Success {
				result.Success = false
				result.ErrorMessage = fmt.Sprintf("extractor %q found no match: %v", ext.Name, err)
			}
		case "empty":
			// Source-tool NotFound=warning/empty semantics: the variable is
			// assigned an empty string so later templates expand to "".
			if !storeHTTPExtraction(result, ext, ext.Name, "") {
				break extractors
			}
		}
	}

	responseBodyText := ""
	responseBodyTextLoaded := false
	loadResponseBodyText := func() string {
		if !responseBodyTextLoaded {
			responseBodyText = string(respBody)
			responseBodyTextLoaded = true
		}
		return responseBodyText
	}
	// The final page owns assertions, but latency covers the complete refresh chain.
	if state, ok := ctx.Value(metaRefreshContextKey{}).(*metaRefreshState); ok {
		result.Duration = time.Since(state.start)
	}
	assertionWork := newAssertionInputWorkBudget(resp.Header, respBody)
	assertionWork.requestBytes = int64(len(requestBody))
	for index, assertion := range action.Assertions {
		if assertion.LegacyHTMLCheck && !h.outcome.legacyChecks {
			continue
		}
		if err := assertionWork.charge(assertion); err != nil {
			result.Success = false
			result.ErrorMessage = fmt.Sprintf("assertion[%d] %q input rejected: %v", index, assertion.Type, err)
			break
		}
		bodyText := ""
		if assertionNeedsResponseBody(assertion) && assertion.Type != "aura_actions_success" {
			bodyText = loadResponseBodyText()
		}
		var assertionErr error
		if assertion.Type == "aura_actions_success" {
			assertionErr = evaluateAuraAssertion(result, requestBody, respBody)
		} else {
			assertionErr = evaluateResponseAssertion(assertion, result, resp.Header, bodyText)
		}
		if err := assertionErr; err != nil {
			result.Success = false
			result.ErrorMessage = err.Error()
			break
		}
	}
	if result.Success && len(h.contentChecks) > 0 {
		if err := evaluateContentChecksLimited(h.contentChecks, result, loadResponseBodyText(), h.contentCheckErrorLimit); err != nil {
			result.Success = false
			result.ErrorMessage = err.Error()
		}
	}
	if !result.Success || action.CaptureMessageDetail {
		result.Snapshot = buildHTTPSnapshot(method, targetURL, req.Header, requestBody, action.RedactedTarget, redactedRequestHeaders, redactedRequestBody, resp.StatusCode, responseHeaders, respBody)
	}
	if !result.Success || !action.DiscardResponseBodyOnSuccess {
		result.ResponseBody = string(respBody)
	}

	// Browser-emulation resource download + prefetch simulation: fetch the
	// page's embedded resources after the main sample. Gated on the transport
	// outcome (not result.Success, which extractors/assertions may have
	// flipped) — a real browser fetches resources for any rendered page.
	observeVerification(ctx, resp.Header, respBody, false, contentComplete)
	var resourceEvidence *resourceCapture
	resourceCtx := ctx
	if action.CaptureResourceDetails {
		resourceEvidence = newResourceCapture(action.MessageDetailMaxItemBytes)
		defer resourceEvidence.lease.release()
		resourceCtx = context.WithValue(ctx, resourceCaptureKey{}, resourceEvidence)
	}
	explicit, _ := ParseExplicitResources(action.Params)
	fetcher := h.resources
	if fetcher == nil && len(explicit) > 0 {
		fetcher = &resourceFetcher{h: h}
	}
	if fetcher != nil && contentComplete && resp.StatusCode >= 200 && resp.StatusCode < 400 {
		finalURL := req.URL
		if resp.Request != nil && resp.Request.URL != nil {
			finalURL = resp.Request.URL
		}
		var pageBody []byte
		if h.shouldParseHTML(resp.Header) {
			pageBody = respBody
		}
		if len(explicit) > 0 || len(pageBody) > 0 {
			fetcher.fetchPageResources(resourceCtx, finalURL, pageBody, result, req.Header, explicit...)
			for _, warning := range result.WarningCodes {
				resourceTimeoutWarned = resourceTimeoutWarned || warning == "resource_timeout_warning"
			}
			if callerCtx.Err() != nil {
				result.Success = false
				result.ErrorMessage = callerCtx.Err().Error()
			}
			result.Duration = time.Since(result.StartTime)
		}
	}

	if err := verification.err(); err != nil {
		result.Success = false
		result.ErrorMessage = err.Error()
		if result.Snapshot == nil {
			result.Snapshot = buildHTTPSnapshot(method, targetURL, req.Header, requestBody, action.RedactedTarget, redactedRequestHeaders, redactedRequestBody, resp.StatusCode, responseHeaders, respBody)
		}
	}

	if resourceEvidence != nil && (action.CaptureMessageDetail || !result.Success) {
		if result.Snapshot == nil {
			result.Snapshot = buildHTTPSnapshot(method, targetURL, req.Header, requestBody, action.RedactedTarget, redactedRequestHeaders, redactedRequestBody, resp.StatusCode, responseHeaders, respBody)
		}
		resourceEvidence.attach(result.Snapshot)
	}

	return result, nil
}

func validateHTTPActionBounds(action *protocol.Action) error {
	if action == nil {
		return fmt.Errorf("http action is required")
	}
	if err := action.HTTPVersion.Validate(); err != nil {
		return err
	}
	if err := ValidateMethod(ResolveMethod(action.Method, action.Type)); err != nil {
		return err
	}
	if err := ValidateTimeoutMS(action.TimeoutMs); err != nil {
		return err
	}
	if err := ValidateMaxRedirects(action.MaxRedirects); err != nil {
		return err
	}
	if len(action.Extractors) > maxHTTPActionExtractors {
		return fmt.Errorf("http extractors must not exceed %d", maxHTTPActionExtractors)
	}
	for index, extractor := range action.Extractors {
		if err := validateExtractorExpressionSize(extractor.Expression); err != nil {
			return fmt.Errorf("http extractors[%d] %q: %w", index, extractor.Name, err)
		}
	}
	if err := ValidateAssertions(action.Assertions); err != nil {
		return err
	}
	if _, err := ParseExplicitResources(action.Params); err != nil {
		return err
	}
	if action.Params != nil {
		if raw, present := action.Params["stream_timeout_ms"]; present {
			if _, err := protocol.BoundedMilliseconds(raw, defaultSSEStreamTimeout, MaxConfiguredTimeout); err != nil {
				return fmt.Errorf("http stream_timeout_ms %w", err)
			}
		}
	}
	return nil
}

func storeHTTPExtraction(result *protocol.Result, extractor protocol.Extractor, key, value string) bool {
	if err := protocol.StoreExtraction(result, key, value); err != nil {
		result.Success = false
		result.Extractions = nil
		result.ExtractionsIncomplete = true
		result.ErrorMessage = fmt.Sprintf("extractor %q output rejected: %v", extractor.Name, err)
		return false
	}
	return true
}

func actionHTTPMethod(action *protocol.Action) string {
	if action == nil {
		return http.MethodGet
	}
	return ResolveMethod(action.Method, action.Type)
}

// defaultHTTPMaxBodyBytes bounds the buffered+decoded response body when the
// caller does not specify max_body_bytes. hardHTTPMaxBodyBytes is deliberately
// not script-controlled: one action must never turn response buffering into an
// unbounded allocation on every VUser. Authors may select a smaller capture
// limit; zero, negative, or oversized values resolve to the hard ceiling.
const (
	defaultHTTPMaxBodyBytes int64 = 64 << 20 // 64 MiB
	hardHTTPMaxBodyBytes    int64 = 64 << 20 // 64 MiB
)

func httpMaxBodyBytes(params map[string]any) int64 {
	if params != nil {
		if _, ok := params["max_body_bytes"]; ok {
			requested := int64(sseParamInt(params, "max_body_bytes", 0))
			if requested > 0 && requested < hardHTTPMaxBodyBytes {
				return requested
			}
			return hardHTTPMaxBodyBytes
		}
	}
	return defaultHTTPMaxBodyBytes
}

// readAllLimited reads all of r, rejecting payloads larger than maxBytes.
// Internal callers must pass the already-normalized, positive operator cap.
func readAllLimited(r io.Reader, maxBytes int64) ([]byte, error) {
	return readAllLimitedWithLease(r, maxBytes, nil)
}

func readAllLimitedWithLease(r io.Reader, maxBytes int64, lease *responseCaptureLease) ([]byte, error) {
	if maxBytes <= 0 {
		maxBytes = hardHTTPMaxBodyBytes
	}
	var buffer bytes.Buffer
	_, err := io.Copy(&responseCaptureWriter{buffer: &buffer, lease: lease}, io.LimitReader(r, maxBytes+1))
	if err != nil {
		return nil, err
	}
	data := buffer.Bytes()
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("decoded response body exceeded %d-byte limit", maxBytes)
	}
	return data, nil
}

func decodeHTTPResponseContent(body []byte, headers http.Header, snapshotHeaders map[string]string, maxBytes int64, leases ...*responseCaptureLease) ([]byte, map[string]string, error) {
	return decodeHTTPResponseContentContext(context.Background(), body, headers, snapshotHeaders, maxBytes, leases...)
}
func decodeHTTPResponseContentContext(ctx context.Context, body []byte, headers http.Header, snapshotHeaders map[string]string, maxBytes int64, leases ...*responseCaptureLease) ([]byte, map[string]string, error) {
	decoded, err := contentdecode.Decode(ctx, body, headers.Values("Content-Encoding"), contentdecode.DefaultLimits(maxBytes))
	if err != nil {
		return nil, snapshotHeaders, err
	}
	if len(leases) > 0 && !leases[0].reserve(int64(len(decoded))) {
		return nil, snapshotHeaders, leases[0].exhaustedError()
	}
	for _, encoding := range responseContentEncodings(headers) {
		if encoding != "identity" {
			return decoded, decodedResponseHeaders(snapshotHeaders), nil
		}
	}
	return decoded, snapshotHeaders, nil
}

func responseContentEncodings(headers http.Header) []string {
	var encodings []string
	for _, value := range headers.Values("Content-Encoding") {
		for _, part := range strings.Split(value, ",") {
			encodings = append(encodings, strings.ToLower(strings.TrimSpace(part)))
		}
	}
	return encodings
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

func ensureTraceparentHeader(headers http.Header, sampled bool) string {
	if headers == nil {
		return ""
	}
	if existing := strings.TrimSpace(headers.Get("Traceparent")); existing != "" {
		return existing
	}
	traceparent := newTraceparent(sampled)
	headers.Set("Traceparent", traceparent)
	return traceparent
}

func injectActiveTraceContext(ctx context.Context, headers http.Header) string {
	if headers == nil {
		return ""
	}
	if existing := strings.TrimSpace(headers.Get("Traceparent")); existing != "" {
		return existing
	}
	if spanContext := trace.SpanContextFromContext(ctx); !spanContext.IsValid() {
		return ""
	}
	carrier := propagation.HeaderCarrier(headers)
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	if injected := strings.TrimSpace(headers.Get("Traceparent")); injected != "" {
		return injected
	}
	// Embedders may install a tracer provider but leave the global propagator at
	// its default no-op. W3C propagation is APEX Load's explicit contract.
	propagation.TraceContext{}.Inject(ctx, carrier)
	return strings.TrimSpace(headers.Get("Traceparent"))
}

func (h *HTTP) OAuthHTTPClient() *http.Client {
	if h == nil || h.oauthClient == nil {
		return nil
	}
	client := *h.oauthClient
	return &client
}

func newTraceparent(sampled bool) string {
	traceID := make([]byte, 16)
	if _, err := rand.Read(traceID); err != nil || bytes.Equal(traceID, make([]byte, len(traceID))) {
		traceID[len(traceID)-1] = 1
	}
	spanID := make([]byte, 8)
	if _, err := rand.Read(spanID); err != nil || bytes.Equal(spanID, make([]byte, len(spanID))) {
		spanID[len(spanID)-1] = 1
	}
	flags := "00"
	if sampled {
		flags = "01"
	}
	return "00-" + hex.EncodeToString(traceID) + "-" + hex.EncodeToString(spanID) + "-" + flags
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

// mergeRequestCookieHeaders preserves every validated authored Cookie
// occurrence while presenting the single RFC 6265 field expected by
// Request.AddCookie. Without this boundary merge, a configured cookie jar
// appends to Header.Get("Cookie") and silently discards later occurrences.
func mergeRequestCookieHeaders(req *http.Request) {
	values := req.Header.Values("Cookie")
	if len(values) <= 1 {
		return
	}
	req.Header.Set("Cookie", strings.Join(values, "; "))
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

func mergeHeaderNameSets(destination, source map[string]struct{}) map[string]struct{} {
	if len(source) == 0 {
		return destination
	}
	if destination == nil {
		destination = make(map[string]struct{}, len(source))
	}
	for name := range source {
		destination[name] = struct{}{}
	}
	return destination
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
	if h.cache != nil {
		h.cache.clear()
	}
	if h.client != nil {
		if jar, ok := h.client.Jar.(*boundedCookieJar); ok {
			_ = jar.clear()
		}
	}
	if h.connections != nil {
		h.connections.closeIdle()
	} else if h.transport != nil {
		h.transport.CloseIdleConnections()
	}
	if h.oauthTransport != nil && h.oauthTransport != h.transport {
		h.oauthTransport.CloseIdleConnections()
	}
	return nil
}

// CloseIdleConnections implements protocol.ConnectionController. It affects
// only currently-idle pooled connections and leaves active requests alone.
func (h *HTTP) CloseIdleConnections(_ context.Context) error {
	if h.connections != nil {
		h.connections.closeIdle()
	} else if h.transport != nil {
		h.transport.CloseIdleConnections()
	}
	if h.oauthTransport != nil && h.oauthTransport != h.transport {
		h.oauthTransport.CloseIdleConnections()
	}
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
		if h.httpVersion == VersionHTTP2 {
			return fmt.Errorf("%w: transport request failed", ErrHTTP2Required)
		}
		return err
	}
	resp.Body.Close()
	return h.validateResponseVersion(resp)
}

func (h *HTTP) transportErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	if h != nil && h.httpVersion == VersionHTTP2 {
		return ErrHTTP2Required.Error() + ": transport request failed"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		// Depending on scheduler timing, net/http may wrap a client timeout as
		// either "Client.Timeout exceeded" or the bare context deadline error.
		// Keep the public diagnostic stable (and URL-free) in both cases.
		return "request timeout: context deadline exceeded"
	}
	var urlError *url.Error
	if errors.As(err, &urlError) && urlError.Err != nil {
		// url.Error.Error quotes the complete request URL. Retain its useful
		// underlying cause (including target-policy host diagnostics) without
		// disclosing userinfo, query values, or fragments.
		return urlError.Err.Error()
	}
	return err.Error()
}

func (h *HTTP) validateResponseVersion(resp *http.Response) error {
	if h == nil || h.httpVersion != VersionHTTP2 || resp == nil || resp.ProtoMajor == 2 {
		return nil
	}
	return fmt.Errorf("%w: transport negotiated %s", ErrHTTP2Required, resp.Proto)
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
	// Redacted* fields are a fidelity aid, not the disclosure boundary. Direct
	// protocol callers and early request-construction failures may not have
	// supplied them, while default/authored credential headers are already
	// present on the request. Protect every credential-classified header here so
	// no failure path can publish the raw value in an ErrorSnapshot.
	headers = redactSnapshotCredentialHeaders(headers)
	body := string(requestBody)
	if len(redactedBody) > 0 {
		body = string(redactedBody)
	}
	responseBodyState := "captured"
	if responseStatus == 0 {
		responseBodyState = "not_captured"
	} else if len(responseBody) == 0 {
		responseBodyState = "empty"
	}
	return &protocol.ErrorSnapshot{
		ResponseBodyState: responseBodyState,
		RequestMethod:     method,
		RequestURL:        requestURL,
		RequestHeaders:    headers,
		RequestBody:       body,
		ResponseStatus:    responseStatus,
		ResponseHeaders:   redactSnapshotCredentialHeaders(responseHeaders),
		ResponseBody:      string(responseBody),
	}
}

func redactSnapshotCredentialHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := cloneStringMap(headers)
	for name, value := range out {
		if !credential.SensitiveHeader(name) {
			continue
		}
		// Preserve an already-safe projection such as "Basic [REDACTED]" so
		// diagnostics retain the auth scheme without weakening the boundary.
		if strings.Contains(value, credential.Redacted) {
			continue
		}
		if _, safeReference := credential.ParseReference(value); safeReference {
			continue
		}
		out[name] = credential.Redacted
	}
	return out
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
		// Preserve occurrence boundaries. Comma folding changes the value of
		// non-list headers (and Set-Cookie in particular) and prevents correlation
		// diagnosis from matching an ordinal extractor against replay evidence.
		cloned[key] = strings.Join(values, "\n")
	}
	if len(cloned) == 0 {
		return nil
	}
	return cloned
}

// attachCorrelationEvidence exposes one raw HTTP exchange only to the replay
// engine's pre-scrub observer. Load runs never allocate this side channel. The
// response body passed here is the decoded/formatted body used by extractors;
// repeated header occurrences remain newline-delimited instead of comma-folded.
func (h *HTTP) attachCorrelationEvidence(result *protocol.Result, action *protocol.Action, request *http.Request, fallbackURL string, fallbackBody []byte, response *http.Response, responseBody []byte, responseSuccess bool) {
	if h == nil || !h.captureCorrelationEvidence || result == nil {
		return
	}
	// response.Request is the final redirect-hop request. Repair locations must
	// instead describe the initial request represented by this editable action;
	// otherwise a POST that follows a redirect appears as the final GET and loses
	// the stale URL/body that the user can actually fix.
	wireRequest := request
	requestURL := fallbackURL
	if wireRequest != nil && wireRequest.URL != nil {
		requestURL = wireRequest.URL.String()
	}
	if !httpCorrelationEvidenceFitsCaptureLimit(requestURL, fallbackBody, wireRequest, responseBody, response) {
		result.CorrelationEvidence = protocol.UnavailableCorrelationEvidence(protocol.CorrelationEvidenceTooLargeReason)
		return
	}
	responseOutcome := responseSuccess
	var responseHeaders map[string]string
	responseStatus := 0
	if response != nil {
		responseHeaders = cloneHTTPHeaders(response.Header)
		responseStatus = response.StatusCode
	}
	result.CorrelationEvidence = &protocol.CorrelationEvidence{
		RequestURL:             requestURL,
		RequestBody:            string(fallbackBody),
		RequestBodyTransformed: action != nil && (strings.EqualFold(strings.TrimSpace(action.BodyEncoding), "base64") || strings.EqualFold(action.BodyEncoding, "windows-1252")),
		RequestHeaders:         correlationEvidenceRequestHeaders(wireRequest),
		ResponseBody:           string(responseBody),
		ResponseHeaders:        responseHeaders,
		ResponseStatus:         responseStatus,
		ResponseSuccess:        &responseOutcome,
	}
}

func httpCorrelationEvidenceFitsCaptureLimit(requestURL string, requestBody []byte, request *http.Request, responseBody []byte, response *http.Response) bool {
	remaining := protocol.MaxCorrelationEvidenceBytes
	consume := func(size int) bool {
		if size < 0 || size > remaining {
			return false
		}
		remaining -= size
		return true
	}
	consumeHeaders := func(headers http.Header, skipHost bool) bool {
		for key, values := range headers {
			if len(values) == 0 || (skipHost && strings.EqualFold(key, "Host")) {
				continue
			}
			if !consume(len(key)) {
				return false
			}
			for index, value := range values {
				if index > 0 && !consume(1) {
					return false
				}
				if !consume(len(value)) {
					return false
				}
			}
		}
		return true
	}
	if !consume(len(requestURL)) || !consume(len(requestBody)) || !consume(len(responseBody)) {
		return false
	}
	if request != nil {
		host := strings.TrimSpace(request.Host)
		if host == "" && request.URL != nil {
			host = strings.TrimSpace(request.URL.Host)
		}
		if !consumeHeaders(request.Header, host != "") {
			return false
		}
		if host != "" && (!consume(len("Host")) || !consume(len(host))) {
			return false
		}
	}
	return response == nil || consumeHeaders(response.Header, false)
}

func correlationEvidenceRequestHeaders(request *http.Request) map[string]string {
	if request == nil {
		return nil
	}
	headers := cloneHTTPHeaders(request.Header)
	host := strings.TrimSpace(request.Host)
	if host == "" && request.URL != nil {
		host = strings.TrimSpace(request.URL.Host)
	}
	if host == "" {
		return headers
	}
	if headers == nil {
		headers = make(map[string]string)
	}
	for key := range headers {
		if strings.EqualFold(key, "Host") {
			delete(headers, key)
		}
	}
	headers["Host"] = host
	return headers
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
	type headerGroup struct {
		name   string
		values []string
	}
	groups := make(map[string]*headerGroup, len(redactedHeaderList))
	order := make([]string, 0, len(redactedHeaderList))
	for _, header := range redactedHeaderList {
		name := strings.TrimSpace(header.Name)
		if name == "" {
			continue
		}
		normalized := strings.ToLower(name)
		group := groups[normalized]
		if group == nil {
			group = &headerGroup{name: name}
			groups[normalized] = group
			order = append(order, normalized)
		}
		group.values = append(group.values, header.Value)
	}
	for _, normalized := range order {
		group := groups[normalized]
		// HeaderList has wire-level precedence over the summary map. Remove an
		// existing case variant instead of appending it, which otherwise duplicates
		// the first occurrence in snapshots.
		for existingName := range out {
			if strings.EqualFold(existingName, group.name) {
				delete(out, existingName)
			}
		}
		out[group.name] = strings.Join(group.values, "\n")
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
