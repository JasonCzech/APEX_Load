package java_http

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jasonczech/stormrunner/pkg/protocol"
)

func TestJavaHTTPProtocolRegistered(t *testing.T) {
	p, err := protocol.Get("java_http")
	if err != nil {
		t.Fatalf("Get(java_http) returned error: %v", err)
	}
	if p.Name() != "java_http" {
		t.Fatalf("Name() = %q, want java_http", p.Name())
	}
}

func TestJavaHTTPExecuteDecodesRequestAndEncodesResponse(t *testing.T) {
	requestPayload := []byte{0xac, 0xed, 0x00, 0x05, 'd', 'e', 'm', 'o'}
	responsePayload := []byte{0xac, 0xed, 0x00, 0x05, 'o', 'k'}
	requestBase64 := base64.StdEncoding.EncodeToString(requestPayload)
	responseBase64 := base64.StdEncoding.EncodeToString(responsePayload)

	var seenContentType string
	var seenAccept string
	var seenBody []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenContentType = r.Header.Get("Content-Type")
		seenAccept = r.Header.Get("Accept")
		seenBody, _ = io.ReadAll(r.Body)
		_, _ = w.Write(responsePayload)
	}))
	defer ts.Close()

	p := &JavaHTTP{}
	if err := p.Init(context.Background(), map[string]any{
		"base_url":                         ts.URL,
		"java_http_codec":                  supportedCodec,
		"java_http_helper_mode":            "embedded",
		"java_http_content_type":           "application/x-java-serialized-object",
		"java_http_accept":                 "application/x-java-serialized-object",
		"java_http_body_encoding":          "base64",
		"java_http_preserve_binary_result": true,
	}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}

	result, err := p.Execute(context.Background(), &protocol.Action{
		Name:   "java-call",
		Method: "POST",
		Target: "/invoke",
		Body:   []byte(requestBase64),
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result == nil || !result.Success {
		t.Fatalf("result = %#v, want success", result)
	}
	if string(seenBody) != string(requestPayload) {
		t.Fatalf("server saw body %v, want %v", seenBody, requestPayload)
	}
	if seenContentType != "application/x-java-serialized-object" {
		t.Fatalf("content-type = %q, want application/x-java-serialized-object", seenContentType)
	}
	if seenAccept != "application/x-java-serialized-object" {
		t.Fatalf("accept = %q, want application/x-java-serialized-object", seenAccept)
	}
	if result.ResponseBody != responseBase64 {
		t.Fatalf("response body = %q, want %q", result.ResponseBody, responseBase64)
	}
	if result.BytesSent != int64(len(requestPayload)) {
		t.Fatalf("bytes sent = %d, want %d", result.BytesSent, len(requestPayload))
	}
	if result.DependencyType != "java_http" {
		t.Fatalf("dependency type = %q, want java_http", result.DependencyType)
	}
	if result.ServerTelemetry == nil {
		t.Fatal("ServerTelemetry = nil, want Java HTTP telemetry")
	}
	if result.ServerTelemetry.SerializationMs <= 0 {
		t.Fatalf("SerializationMs = %v, want > 0", result.ServerTelemetry.SerializationMs)
	}
	if result.ServerTelemetry.DeserializationMs <= 0 {
		t.Fatalf("DeserializationMs = %v, want > 0", result.ServerTelemetry.DeserializationMs)
	}
	if result.Extractions["java_http_codec"] != supportedCodec {
		t.Fatalf("java_http_codec = %q, want %q", result.Extractions["java_http_codec"], supportedCodec)
	}
	if result.Extractions["java_http_helper_mode"] != "embedded" {
		t.Fatalf("java_http_helper_mode = %q, want embedded", result.Extractions["java_http_helper_mode"])
	}
}

func TestJavaHTTPExecuteRejectsInvalidBase64(t *testing.T) {
	p := &JavaHTTP{}
	if err := p.Init(context.Background(), map[string]any{
		"base_url":                "https://example.com",
		"java_http_codec":         supportedCodec,
		"java_http_helper_mode":   "embedded",
		"java_http_body_encoding": "base64",
	}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}

	result, err := p.Execute(context.Background(), &protocol.Action{Name: "bad", Target: "/", Body: []byte("not-base64%%")})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result == nil || result.ErrorMessage == "" {
		t.Fatalf("result = %#v, want validation error", result)
	}
}

func TestJavaHTTPInitAllowsSidecarHelperMode(t *testing.T) {
	p := &JavaHTTP{}
	if err := p.Init(context.Background(), map[string]any{
		"base_url":                "https://example.com",
		"java_http_codec":         supportedCodec,
		"java_http_helper_mode":   "sidecar",
		"java_http_body_encoding": "base64",
	}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	if p.helperMode != "sidecar" {
		t.Fatalf("helperMode = %q, want sidecar", p.helperMode)
	}
}
