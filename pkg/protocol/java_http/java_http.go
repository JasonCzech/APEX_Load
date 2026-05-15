package java_http

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/jasonczech/stormrunner/pkg/protocol"
	httpproto "github.com/jasonczech/stormrunner/pkg/protocol/http"
)

const supportedCodec = "java_serialized_object_base64"

func init() {
	protocol.Register("java_http", func() protocol.Protocol {
		return &JavaHTTP{}
	})
}

type JavaHTTP struct {
	http                 *httpproto.HTTP
	codec                string
	helperMode           string
	contentType          string
	accept               string
	bodyEncoding         string
	preserveBinaryResult bool
}

func (j *JavaHTTP) Name() string { return "java_http" }

func (j *JavaHTTP) Init(ctx context.Context, config map[string]any) error {
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
	if len(action.Body) > 0 {
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(action.Body)))
		if err != nil {
			return &protocol.Result{
				ActionName:   action.Name,
				ErrorMessage: fmt.Sprintf("invalid base64 java_http body: %v", err),
			}, nil
		}
		prepared.Body = decoded
	}
	result, err := j.http.Execute(ctx, &prepared)
	if result != nil && j.preserveBinaryResult {
		result.ResponseBody = base64.StdEncoding.EncodeToString([]byte(result.ResponseBody))
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