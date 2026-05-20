package java_http

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	httpproto "github.com/jasonczech/apexload/pkg/protocol/http"
)

const supportedCodec = "java_serialized_object_base64"

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
}

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
	j.http = &httpproto.HTTP{}
	return j.http.Init(ctx, config)
}

func (j *JavaHTTP) Execute(ctx context.Context, action *protocol.Action) (*protocol.Result, error) {
	if action == nil {
		return &protocol.Result{ErrorMessage: "java_http action is required"}, nil
	}
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
	serializationDuration := time.Duration(0)
	if len(action.Body) > 0 {
		startedAt := time.Now()
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(action.Body)))
		serializationDuration = time.Since(startedAt)
		if err != nil {
			return &protocol.Result{
				ActionName:      action.Name,
				StartTime:       startedAt,
				Duration:        serializationDuration,
				DependencyType:  "java_http",
				BytesSent:       int64(len(action.Body)),
				ErrorMessage:    fmt.Sprintf("invalid base64 java_http body: %v", err),
				ServerTelemetry: &protocol.ServerTelemetry{SerializationMs: durationToMilliseconds(serializationDuration)},
				Snapshot:        buildJavaHTTPPreflightSnapshot(j.baseURL, &prepared),
			}, nil
		}
		prepared.Body = decoded
	}
	result, err := j.http.Execute(ctx, &prepared)
	deserializationDuration := time.Duration(0)
	if result != nil && j.preserveBinaryResult {
		startedAt := time.Now()
		result.ResponseBody = base64.StdEncoding.EncodeToString([]byte(result.ResponseBody))
		deserializationDuration = time.Since(startedAt)
	}
	if result != nil {
		result.DependencyType = "java_http"
		if result.ServerTelemetry == nil {
			result.ServerTelemetry = &protocol.ServerTelemetry{}
		}
		result.ServerTelemetry.SerializationMs = durationToMilliseconds(serializationDuration)
		result.ServerTelemetry.DeserializationMs = durationToMilliseconds(deserializationDuration)
		if result.Extractions == nil {
			result.Extractions = make(map[string]string)
		}
		result.Extractions["java_http_codec"] = j.codec
		result.Extractions["java_http_helper_mode"] = j.helperMode
	}
	return result, err
}

func (j *JavaHTTP) Teardown(ctx context.Context) error {
	if j.http == nil {
		return nil
	}
	return j.http.Teardown(ctx)
}

func (j *JavaHTTP) HealthCheck(ctx context.Context) error {
	if j.http == nil {
		return nil
	}
	return j.http.HealthCheck(ctx)
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
	method := strings.ToUpper(strings.TrimSpace(action.Method))
	if method == "" {
		method = "GET"
	}
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

func resolveJavaHTTPURL(baseURL string, target string) string {
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		return target
	}
	if strings.TrimSpace(baseURL) == "" {
		return target
	}
	return strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(target, "/")
}

func durationToMilliseconds(duration time.Duration) float64 {
	if duration <= 0 {
		return 0
	}
	return float64(duration) / float64(time.Millisecond)
}
