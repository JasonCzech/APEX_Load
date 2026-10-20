package java_http

import (
	"context"
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/boundedcache"
	"github.com/jasonczech/apexload/pkg/protocol"
	httpproto "github.com/jasonczech/apexload/pkg/protocol/http"
)

const supportedCodec = "java_serialized_object_base64"

var responseMappingRegexCache = boundedcache.NewStringCache[*regexp.Regexp](0, 0, 0)

func compileResponseMappingRegex(expr string) *regexp.Regexp {
	if cached, ok := responseMappingRegexCache.Load(expr); ok {
		return cached
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		re = regexp.MustCompile(regexp.QuoteMeta(expr))
	}
	actual, _ := responseMappingRegexCache.LoadOrStore(expr, re)
	return actual
}

func init() {
	protocol.Register("java_http", func() protocol.Protocol {
		return &JavaHTTP{}
	})
}

type JavaHTTP struct {
	http                 *httpproto.HTTP
	baseURL              string
	codec                string
	helperMode           string
	contentType          string
	accept               string
	bodyEncoding         string
	preserveBinaryResult bool
	objectSchema         string
	requestMapping       map[string]string
	responseMapping      map[string]string
}

var (
	_ protocol.CookieController = (*JavaHTTP)(nil)
	_ protocol.CacheController  = (*JavaHTTP)(nil)
)

func (j *JavaHTTP) Name() string { return "java_http" }

func (j *JavaHTTP) Init(ctx context.Context, config map[string]any) error {
	if baseURL, ok := config["base_url"].(string); ok {
		j.baseURL = strings.TrimSpace(baseURL)
	}
	j.codec = supportedCodec
	if value, ok := config["java_http_codec"].(string); ok && strings.TrimSpace(value) != "" {
		j.codec = strings.TrimSpace(value)
	}
	if j.codec != supportedCodec {
		return fmt.Errorf("unsupported java_http codec: %s", j.codec)
	}
	j.helperMode = "embedded"
	if value, ok := config["java_http_helper_mode"].(string); ok && strings.TrimSpace(value) != "" {
		j.helperMode = strings.TrimSpace(value)
	}
	if j.helperMode != "embedded" && j.helperMode != "sidecar" {
		return fmt.Errorf("unsupported java_http helper mode: %s", j.helperMode)
	}
	if j.helperMode == "sidecar" {
		return fmt.Errorf("java_http helper mode sidecar is not wired; use java_vuser for governed sidecar execution or embedded byte-preserving java_http")
	}
	j.contentType = "application/x-java-serialized-object"
	if value, ok := config["java_http_content_type"].(string); ok && strings.TrimSpace(value) != "" {
		j.contentType = strings.TrimSpace(value)
	}
	j.accept = j.contentType
	if value, ok := config["java_http_accept"].(string); ok && strings.TrimSpace(value) != "" {
		j.accept = strings.TrimSpace(value)
	}
	j.bodyEncoding = "base64"
	if value, ok := config["java_http_body_encoding"].(string); ok && strings.TrimSpace(value) != "" {
		j.bodyEncoding = strings.TrimSpace(value)
	}
	if j.bodyEncoding != "base64" {
		return fmt.Errorf("unsupported java_http body encoding: %s", j.bodyEncoding)
	}
	j.preserveBinaryResult, _ = config["java_http_preserve_binary_result"].(bool)
	if value, ok := config["java_http_object_schema"].(string); ok {
		j.objectSchema = strings.TrimSpace(value)
	}
	j.requestMapping = coerceStringMap(config["java_http_request_mapping"])
	j.responseMapping = coerceStringMap(config["java_http_response_mapping"])
	if err := validateResponseMappingBounds(j.responseMapping); err != nil {
		return err
	}
	if len(j.requestMapping) > 0 || j.objectSchema != "" {
		return fmt.Errorf("java_http request_mapping/object_schema are not applied by embedded byte-preserving java_http; use java_vuser sidecar import or remove these fields")
	}
	j.http = &httpproto.HTTP{}
	return j.http.Init(ctx, config)
}

func (j *JavaHTTP) Execute(ctx context.Context, action *protocol.Action) (result *protocol.Result, err error) {
	if action == nil {
		return &protocol.Result{ErrorMessage: "java_http action is required"}, nil
	}
	outerStart := time.Now()
	diagnostic := &protocol.JavaHTTPDiagnostics{Version: 1, Mode: "embedded_opaque_bytes", InputState: "unsupported_encoding", RequestEncodedBytes: int64(len(action.Body)), ResponseState: "unavailable", ObjectFootprint: "unavailable_embedded_opaque_bytes"}
	defer func() {
		if result == nil {
			return
		}
		diagnostic.EnclosingMs = float64(time.Since(outerStart)) / float64(time.Millisecond)
		if diagnostic.Valid() {
			if result.ServerTelemetry == nil {
				result.ServerTelemetry = &protocol.ServerTelemetry{}
			}
			result.ServerTelemetry.JavaHTTP = diagnostic
		}
	}()
	prepared := *action
	prepared.Headers = cloneHeaders(action.Headers)
	if prepared.Headers == nil {
		prepared.Headers = map[string]string{}
	}
	if firstHeaderValue(prepared.Headers, "Content-Type") == "" {
		prepared.Headers["Content-Type"] = j.contentType
	}
	if firstHeaderValue(prepared.Headers, "Accept") == "" {
		prepared.Headers["Accept"] = j.accept
	}
	bodyEncoding := strings.TrimSpace(action.BodyEncoding)
	if bodyEncoding == "" {
		bodyEncoding = j.bodyEncoding
	}
	base64DecodeDuration := time.Duration(0)
	// An explicitly unsupported encoding is invalid even when no bytes need
	// decoding. Check it before calling HTTP so empty bodies cannot bypass
	// the codec's admission contract.
	if bodyEncoding != "base64" {
		startedAt := time.Now()
		base64DecodeDuration = time.Since(startedAt)
		return &protocol.Result{
			ActionName:      action.Name,
			StartTime:       startedAt,
			Duration:        base64DecodeDuration,
			DependencyType:  "java_http",
			ErrorMessage:    fmt.Sprintf("unsupported java_http action body encoding: %s", bodyEncoding),
			ServerTelemetry: &protocol.ServerTelemetry{SerializationMs: durationToMilliseconds(base64DecodeDuration)},
			Snapshot:        buildJavaHTTPPreflightSnapshot(j.baseURL, &prepared),
		}, nil
	}
	zeroBytes := int64(0)
	diagnostic.InputState, diagnostic.RequestDecodedBytes = "empty", &zeroBytes
	if len(action.Body) > 0 {
		diagnostic.InputState, diagnostic.RequestDecodedBytes = "decode_failed", nil
		startedAt := time.Now()
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(action.Body)))
		observedDecode := time.Since(startedAt)
		var decodeMs float64
		base64DecodeDuration, decodeMs = base64OperationMeasurement(observedDecode)
		diagnostic.Base64DecodeMs = &decodeMs
		if err != nil {
			return &protocol.Result{
				ActionName:      action.Name,
				StartTime:       startedAt,
				Duration:        base64DecodeDuration,
				DependencyType:  "java_http",
				ErrorMessage:    fmt.Sprintf("invalid base64 java_http body: %v", err),
				ServerTelemetry: &protocol.ServerTelemetry{SerializationMs: durationToMilliseconds(base64DecodeDuration)},
				Snapshot:        buildJavaHTTPPreflightSnapshot(j.baseURL, &prepared),
			}, nil
		}
		decodedBytes := int64(len(decoded))
		diagnostic.InputState, diagnostic.RequestDecodedBytes = "decoded", &decodedBytes
		prepared.Body = decoded
	}
	// Java HTTP owns the one base64 conversion. The shared HTTP runtime must
	// receive opaque bytes, even when the action explicitly selected base64.
	prepared.BodyEncoding = ""
	httpContext := protocol.WithHTTPBodyObserver(ctx, func(observation protocol.HTTPBodyObservation) {
		value := observation.Bytes
		diagnostic.PreparedRequestBodyBytes, diagnostic.AppliedRequestCompression = &value, observation.AppliedCompression
	})
	result, err = j.http.Execute(httpContext, &prepared)
	if result != nil {
		state, _ := result.ProtocolEvidence["response_content_state"].(string)
		// The HTTP adapter discards a successful body before later checks can
		// fail the action, so a failed action with discard requested and an
		// empty body may have had either; it stays unavailable, not a 0.
		lateFailureAfterDiscard := !result.Success && prepared.DiscardResponseBodyOnSuccess && result.ResponseBody == ""
		if (state == "complete" || state == "collection_window") && !lateFailureAfterDiscard {
			diagnostic.ResponseState = state
			if result.Success && prepared.DiscardResponseBodyOnSuccess {
				diagnostic.ResponseState = "discarded"
			}
			retainedBytes := int64(len(result.ResponseBody))
			diagnostic.ResponseRetainedBytes = &retainedBytes
		}
	}
	if result != nil && result.CorrelationEvidence != nil && len(action.Body) > 0 {
		// Replay repairs cannot address the decoded representation as if it
		// were the original authored body. Preserve that boundary separately
		// from the HTTP adapter's now-cleared encoding instruction.
		result.CorrelationEvidence.RequestBodyTransformed = true
	}
	// Apply response mappings against the raw (pre-base64) response bytes. Java
	// serialized streams embed their string fields as ASCII, so regex mappings can
	// pull named values out of an otherwise-opaque payload and feed correlation —
	// before the body is re-encoded for preservation below.
	mappedExtractions := map[string]string{}
	if result != nil && len(j.responseMapping) > 0 {
		mappedExtractions = applyResponseMapping(j.responseMapping, result.ResponseBody)
	}
	deserializationDuration := time.Duration(0)
	if result != nil && j.preserveBinaryResult {
		startedAt := time.Now()
		result.ResponseBody = base64.StdEncoding.EncodeToString([]byte(result.ResponseBody))
		observedEncode := time.Since(startedAt)
		var encodeMs float64
		deserializationDuration, encodeMs = base64OperationMeasurement(observedEncode)
		diagnostic.Base64EncodeMs = &encodeMs
		if diagnostic.ResponseRetainedBytes != nil {
			encodedBytes := int64(len(result.ResponseBody))
			diagnostic.ResponseBase64Bytes = &encodedBytes
		}
	}
	if result != nil {
		result.DependencyType = "java_http"
		if result.ServerTelemetry == nil {
			result.ServerTelemetry = &protocol.ServerTelemetry{}
		}
		result.ServerTelemetry.SerializationMs = durationToMilliseconds(base64DecodeDuration)
		result.ServerTelemetry.DeserializationMs = durationToMilliseconds(deserializationDuration)
		if result.Extractions == nil {
			result.Extractions = make(map[string]string)
		}
		if storeErr := protocol.StoreExtraction(result, "java_http_codec", j.codec); storeErr != nil {
			result.Success = false
			result.Extractions = nil
			result.ErrorMessage = "java_http extraction retention failed: " + storeErr.Error()
			return result, err
		}
		if storeErr := protocol.StoreExtraction(result, "java_http_helper_mode", j.helperMode); storeErr != nil {
			result.Success = false
			result.Extractions = nil
			result.ErrorMessage = "java_http extraction retention failed: " + storeErr.Error()
			return result, err
		}
		for name, value := range mappedExtractions {
			if storeErr := protocol.StoreExtraction(result, name, value); storeErr != nil {
				result.Success = false
				result.Extractions = nil
				result.ErrorMessage = fmt.Sprintf("java_http response mapping %q rejected: %v", name, storeErr)
				break
			}
		}
	}
	return result, err
}

func validateResponseMappingBounds(mapping map[string]string) error {
	if len(mapping) > protocol.MaxExtractionEntries {
		return fmt.Errorf("java_http response_mapping exceeds %d entries", protocol.MaxExtractionEntries)
	}
	total := 0
	for name, expression := range mapping {
		if name == "" || len(name) > protocol.MaxExtractionKeyBytes {
			return fmt.Errorf("java_http response_mapping name must be 1..%d bytes", protocol.MaxExtractionKeyBytes)
		}
		if len(expression) > protocol.MaxExtractionKeyBytes {
			return fmt.Errorf("java_http response_mapping expression %q exceeds %d bytes", name, protocol.MaxExtractionKeyBytes)
		}
		total += len(name) + len(expression)
		if total > protocol.MaxExtractionBytes {
			return fmt.Errorf("java_http response_mapping exceeds %d bytes", protocol.MaxExtractionBytes)
		}
	}
	return nil
}

func (j *JavaHTTP) Teardown(ctx context.Context) error {
	if j.http == nil {
		return nil
	}
	return j.http.Teardown(ctx)
}

// ClearCookies / ClearCache delegate to the wrapped HTTP protocol so that
// browser-emulation's "simulate a new user on each iteration" (and the
// cookie_clear action) actually reset Java-over-HTTP session state.
func (j *JavaHTTP) ClearCookies(ctx context.Context) error {
	if j.http == nil {
		return nil
	}
	return j.http.ClearCookies(ctx)
}

func (j *JavaHTTP) ClearCache(ctx context.Context) error {
	if j.http == nil {
		return nil
	}
	return j.http.ClearCache(ctx)
}

func (j *JavaHTTP) HealthCheck(ctx context.Context) error {
	if j.http == nil {
		return nil
	}
	return j.http.HealthCheck(ctx)
}

// coerceStringMap accepts either a map[string]string (set by the engine config
// flattener) or a map[string]any (when init config is decoded from JSON).
func coerceStringMap(value any) map[string]string {
	switch typed := value.(type) {
	case map[string]string:
		if len(typed) == 0 {
			return nil
		}
		out := make(map[string]string, len(typed))
		for k, v := range typed {
			out[k] = v
		}
		return out
	case map[string]any:
		if len(typed) == 0 {
			return nil
		}
		out := make(map[string]string, len(typed))
		for k, v := range typed {
			if s, ok := v.(string); ok {
				out[k] = s
			}
		}
		return out
	default:
		return nil
	}
}

// applyResponseMapping evaluates each name→expression mapping against the raw
// response body. An expression is treated as a regex with an optional capture
// group; a substring fallback is used for plain-literal expressions.
func applyResponseMapping(mapping map[string]string, body string) map[string]string {
	out := make(map[string]string, len(mapping))
	for name, expr := range mapping {
		if strings.TrimSpace(expr) == "" {
			continue
		}
		re := compileResponseMappingRegex(expr)
		m := re.FindStringSubmatch(body)
		if m == nil {
			continue
		}
		if len(m) >= 2 {
			out[name] = m[1]
		} else {
			out[name] = m[0]
		}
	}
	return out
}

func cloneHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return map[string]string{}
	}
	cloned := make(map[string]string, len(headers))
	for key, value := range headers {
		cloned[key] = value
	}
	return cloned
}

func firstHeaderValue(headers map[string]string, key string) string {
	for headerKey, value := range headers {
		if strings.EqualFold(headerKey, key) {
			return value
		}
	}
	return ""
}

func buildJavaHTTPPreflightSnapshot(baseURL string, action *protocol.Action) *protocol.ErrorSnapshot {
	if action == nil {
		return nil
	}
	method := javaHTTPActionMethod(action)
	requestURL := resolveJavaHTTPURL(baseURL, action.Target)
	if strings.TrimSpace(action.RedactedTarget) != "" {
		requestURL = resolveJavaHTTPURL(baseURL, action.RedactedTarget)
	}
	requestBody := string(action.Body)
	if len(action.RedactedBody) > 0 {
		requestBody = string(action.RedactedBody)
	}
	requestHeaders := cloneHeaders(action.Headers)
	for key, value := range action.RedactedHeaders {
		requestHeaders[key] = value
	}
	return &protocol.ErrorSnapshot{
		RequestMethod:  method,
		RequestURL:     requestURL,
		RequestHeaders: requestHeaders,
		RequestBody:    requestBody,
	}
}

func javaHTTPActionMethod(action *protocol.Action) string {
	if action == nil {
		return "GET"
	}
	if method := strings.ToUpper(strings.TrimSpace(action.Method)); method != "" {
		return method
	}
	switch method := strings.ToUpper(strings.TrimSpace(action.Type)); method {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "CONNECT", "OPTIONS", "TRACE":
		return method
	default:
		return "GET"
	}
}

func resolveJavaHTTPURL(baseURL string, target string) string {
	return httpproto.ResolveURL(baseURL, target)
}

func durationToMilliseconds(duration time.Duration) float64 {
	if duration <= 0 {
		return 0
	}
	return float64(duration) / float64(time.Millisecond)
}

// A completed operation may take less than the clock's resolution. Keep its
// telemetry present while leaving the zero duration for skipped work intact.
func measuredOperationDuration(duration time.Duration) time.Duration {
	if duration <= 0 {
		return time.Nanosecond
	}
	return duration
}

// BeginIteration delegates cache aging to the shared HTTP runtime.
func (j *JavaHTTP) BeginIteration(ctx context.Context) error {
	if j.http == nil {
		return nil
	}
	return j.http.BeginIteration(ctx)
}

// Keep the old nonzero completed-operation convention while the versioned
// observation preserves the timer reading, including a zero-resolution sample.
func base64OperationMeasurement(elapsed time.Duration) (time.Duration, float64) {
	return measuredOperationDuration(elapsed), durationToMilliseconds(elapsed)
}
