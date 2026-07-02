package http

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func initHTTP(t *testing.T, config map[string]any) *HTTP {
	t.Helper()
	h := &HTTP{}
	if err := h.Init(context.Background(), config); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return h
}

func TestHTTP_Name(t *testing.T) {
	h := &HTTP{}
	if h.Name() != "http" {
		t.Errorf("Name() = %q, want %q", h.Name(), "http")
	}
}

func TestHTTP_Init_Defaults(t *testing.T) {
	h := initHTTP(t, map[string]any{
		"base_url": "https://example.com",
	})

	if h.baseURL != "https://example.com" {
		t.Errorf("baseURL = %q", h.baseURL)
	}
	if h.requestTimeout != 60*time.Second {
		t.Errorf("requestTimeout = %v, want 60s", h.requestTimeout)
	}
	if h.maxRedirects != 10 {
		t.Errorf("maxRedirects = %d, want 10", h.maxRedirects)
	}
	if h.transport.MaxConnsPerHost != 100 {
		t.Errorf("MaxConnsPerHost = %d, want 100", h.transport.MaxConnsPerHost)
	}
}

func TestHTTP_Init_CustomTimeouts(t *testing.T) {
	h := initHTTP(t, map[string]any{
		"connect_timeout_ms": float64(5000),
		"request_timeout_ms": float64(15000),
		"tls_timeout_ms":     float64(3000),
		"idle_timeout_ms":    float64(45000),
	})

	if h.requestTimeout != 15*time.Second {
		t.Errorf("requestTimeout = %v, want 15s", h.requestTimeout)
	}
	if h.transport.TLSHandshakeTimeout != 3*time.Second {
		t.Errorf("TLSHandshakeTimeout = %v, want 3s", h.transport.TLSHandshakeTimeout)
	}
	if h.transport.IdleConnTimeout != 45*time.Second {
		t.Errorf("IdleConnTimeout = %v, want 45s", h.transport.IdleConnTimeout)
	}
}

func TestHTTP_Init_ConnectionPool(t *testing.T) {
	h := initHTTP(t, map[string]any{
		"max_conns_per_host": float64(50),
		"max_idle_conns":     float64(25),
	})

	if h.transport.MaxConnsPerHost != 50 {
		t.Errorf("MaxConnsPerHost = %d, want 50", h.transport.MaxConnsPerHost)
	}
	if h.transport.MaxIdleConns != 25 {
		t.Errorf("MaxIdleConns = %d, want 25", h.transport.MaxIdleConns)
	}
}

func TestHTTP_Init_DisableKeepAlive(t *testing.T) {
	h := initHTTP(t, map[string]any{
		"keep_alive": false,
	})

	if !h.transport.DisableKeepAlives {
		t.Error("DisableKeepAlives should be true")
	}
}

func TestHTTP_Init_TLSVersions(t *testing.T) {
	h := initHTTP(t, map[string]any{
		"tls_min_version": "1.2",
		"tls_max_version": "1.3",
	})

	if h.transport.TLSClientConfig.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion = %d, want %d", h.transport.TLSClientConfig.MinVersion, tls.VersionTLS12)
	}
	if h.transport.TLSClientConfig.MaxVersion != tls.VersionTLS13 {
		t.Errorf("MaxVersion = %d, want %d", h.transport.TLSClientConfig.MaxVersion, tls.VersionTLS13)
	}
}

func TestHTTP_Init_InsecureSkipVerify(t *testing.T) {
	h := initHTTP(t, map[string]any{
		"tls_insecure_skip_verify": true,
	})

	if !h.transport.TLSClientConfig.InsecureSkipVerify {
		t.Error("InsecureSkipVerify should be true")
	}
}

func TestHTTP_Init_DefaultHeaders(t *testing.T) {
	h := initHTTP(t, map[string]any{
		"user_agent":      "APEXLoad/1.0",
		"accept_encoding": "gzip",
		"accept_language": "en-US",
		"default_headers": map[string]string{"X-Custom": "value"},
	})

	if h.defaultHeaders["User-Agent"] != "APEXLoad/1.0" {
		t.Errorf("User-Agent = %q", h.defaultHeaders["User-Agent"])
	}
	if h.defaultHeaders["Accept-Encoding"] != "gzip" {
		t.Errorf("Accept-Encoding = %q", h.defaultHeaders["Accept-Encoding"])
	}
	if h.defaultHeaders["X-Custom"] != "value" {
		t.Errorf("X-Custom = %q", h.defaultHeaders["X-Custom"])
	}
}

func TestHTTP_Init_BasicAuth(t *testing.T) {
	h := initHTTP(t, map[string]any{
		"auth_type":     "basic",
		"auth_username": "admin",
		"auth_password": "secret",
	})

	if h.authType != "basic" {
		t.Errorf("authType = %q", h.authType)
	}
	if h.authHeader == "" {
		t.Error("authHeader should be set for basic auth")
	}
}

func TestHTTP_Init_BearerAuth(t *testing.T) {
	h := initHTTP(t, map[string]any{
		"auth_type":  "bearer",
		"auth_token": "my-jwt",
	})

	if h.authHeader != "Bearer my-jwt" {
		t.Errorf("authHeader = %q, want %q", h.authHeader, "Bearer my-jwt")
	}
}

func TestHTTP_Init_UnsupportedAuthFailsClosed(t *testing.T) {
	for _, authType := range []string{"ntlm", "digest", "kerberos", "negotiate"} {
		t.Run(authType, func(t *testing.T) {
			h := &HTTP{}
			err := h.Init(context.Background(), map[string]any{"auth_type": authType})
			if err == nil || !strings.Contains(err.Error(), "unsupported HTTP auth type") {
				t.Fatalf("Init error = %v, want unsupported auth error", err)
			}
		})
	}
}

func TestHTTP_Init_NoRedirects(t *testing.T) {
	h := initHTTP(t, map[string]any{
		"max_redirects": float64(-1),
	})

	if h.maxRedirects != -1 {
		t.Errorf("maxRedirects = %d, want -1", h.maxRedirects)
	}
	if h.client.CheckRedirect == nil {
		t.Error("CheckRedirect should be set when max_redirects is -1")
	}
}

func TestHTTP_Init_DisableCookies(t *testing.T) {
	h := initHTTP(t, map[string]any{
		"cookies_enabled": false,
	})

	if h.client.Jar != nil {
		t.Error("Jar should be nil when cookies are disabled")
	}
}

func TestHTTP_Init_HTTPVersion2(t *testing.T) {
	h := initHTTP(t, map[string]any{
		"http_version": "2",
	})

	if !h.transport.ForceAttemptHTTP2 {
		t.Error("ForceAttemptHTTP2 should be true for version 2")
	}
}

func TestHTTP_Init_HTTPVersion1(t *testing.T) {
	h := initHTTP(t, map[string]any{
		"http_version": "1.1",
	})

	if h.transport.ForceAttemptHTTP2 {
		t.Error("ForceAttemptHTTP2 should be false for version 1.1")
	}
}

func TestHTTP_Init_CustomProxyMode(t *testing.T) {
	h := initHTTP(t, map[string]any{
		"proxy_mode":     "custom",
		"proxy_url":      "http://proxy.internal:8080",
		"proxy_username": "perf-user",
		"proxy_password": "secret",
	})

	if h.transport.Proxy == nil {
		t.Fatal("Proxy should be configured for custom mode")
	}
	req := httptest.NewRequest(http.MethodGet, "https://service.example.com", nil)
	proxyURL, err := h.transport.Proxy(req)
	if err != nil {
		t.Fatalf("Proxy() returned error: %v", err)
	}
	if proxyURL == nil || proxyURL.String() != "http://perf-user:secret@proxy.internal:8080" {
		t.Fatalf("proxy URL = %v, want authenticated custom proxy", proxyURL)
	}
}

func TestHTTP_Init_SystemProxyMode(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://system-proxy.internal:8443")
	h := initHTTP(t, map[string]any{
		"proxy_mode": "system",
	})

	if h.transport.Proxy == nil {
		t.Fatal("Proxy should be configured for system mode")
	}
	req := httptest.NewRequest(http.MethodGet, "https://service.example.com", nil)
	proxyURL, err := h.transport.Proxy(req)
	if err != nil {
		t.Fatalf("Proxy() returned error: %v", err)
	}
	if proxyURL == nil || proxyURL.String() != "http://system-proxy.internal:8443" {
		t.Fatalf("proxy URL = %v, want system proxy from environment", proxyURL)
	}
}

func TestHTTP_Init_NoProxyModeIgnoresCustomProxyURL(t *testing.T) {
	h := initHTTP(t, map[string]any{
		"proxy_mode": "none",
		"proxy_url":  "http://proxy.internal:8080",
	})

	if h.transport.Proxy != nil {
		t.Fatal("Proxy should be disabled for none mode")
	}
}

func TestHTTP_Init_CustomProxyModeRequiresURL(t *testing.T) {
	h := &HTTP{}
	err := h.Init(context.Background(), map[string]any{
		"proxy_mode": "custom",
	})
	if err == nil {
		t.Fatal("expected error when proxy_mode is custom without proxy_url")
	}
}

func TestHTTP_Execute_GET(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("method = %q, want GET", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer ts.Close()

	h := initHTTP(t, map[string]any{"base_url": ts.URL})

	result, err := h.Execute(context.Background(), &protocol.Action{
		Name:   "test-get",
		Method: "GET",
		Target: "/test",
	})

	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Success {
		t.Errorf("Success = false, error: %s", result.ErrorMessage)
	}
	if result.StatusCode != 200 {
		t.Errorf("StatusCode = %d, want 200", result.StatusCode)
	}
	if result.Duration == 0 {
		t.Error("Duration should be non-zero")
	}
	if result.BytesReceived == 0 {
		t.Error("BytesReceived should be non-zero")
	}
	if result.ResponseBody != `{"status":"ok"}` {
		t.Errorf("ResponseBody = %q, want JSON body", result.ResponseBody)
	}
}

func TestHTTP_ExecuteCapturesDependencyAndCorrelationMetadata(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-Id", "resp-corr-2")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	h := initHTTP(t, map[string]any{"base_url": ts.URL})

	result, err := h.Execute(context.Background(), &protocol.Action{
		Name:   "test-correlation",
		Method: "GET",
		Target: "/metadata",
		Headers: map[string]string{
			"X-Correlation-Id": "req-corr-1",
		},
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.DependencyType != "http" {
		t.Fatalf("DependencyType = %q, want http", result.DependencyType)
	}
	if result.DependencyTarget == "" {
		t.Fatal("expected dependency target to be set")
	}
	if result.CorrelationID != "req-corr-1" {
		t.Fatalf("CorrelationID = %q, want req-corr-1", result.CorrelationID)
	}
	result, err = h.Execute(context.Background(), &protocol.Action{Name: "test-response-correlation", Method: "GET", Target: "/metadata"})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.CorrelationID != "resp-corr-2" {
		t.Fatalf("CorrelationID = %q, want resp-corr-2", result.CorrelationID)
	}
}

func TestHTTP_ExecuteInjectsTraceparentHeaderWhenMissing(t *testing.T) {
	var receivedTraceparent string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedTraceparent = r.Header.Get("Traceparent")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	h := initHTTP(t, map[string]any{"base_url": ts.URL})
	result, err := h.Execute(context.Background(), &protocol.Action{
		Name:   "test-traceparent",
		Method: "GET",
		Target: "/trace",
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	pattern := regexp.MustCompile(`^00-[0-9a-f]{32}-[0-9a-f]{16}-01$`)
	if !pattern.MatchString(receivedTraceparent) {
		t.Fatalf("Traceparent header = %q, want W3C traceparent format", receivedTraceparent)
	}
	if result.CorrelationID != receivedTraceparent {
		t.Fatalf("CorrelationID = %q, want generated traceparent %q", result.CorrelationID, receivedTraceparent)
	}
}

func TestHTTP_ExecutePreservesExplicitTraceparentHeader(t *testing.T) {
	const traceparent = "00-00000000000000000000000000000001-0000000000000001-01"
	var receivedTraceparent string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedTraceparent = r.Header.Get("Traceparent")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	h := initHTTP(t, map[string]any{"base_url": ts.URL})
	result, err := h.Execute(context.Background(), &protocol.Action{
		Name:   "test-explicit-traceparent",
		Method: "GET",
		Target: "/trace",
		Headers: map[string]string{
			"Traceparent": traceparent,
		},
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if receivedTraceparent != traceparent {
		t.Fatalf("Traceparent header = %q, want %q", receivedTraceparent, traceparent)
	}
	if result.CorrelationID != traceparent {
		t.Fatalf("CorrelationID = %q, want %q", result.CorrelationID, traceparent)
	}
}

func TestHTTP_ExecutePopulatesServerTelemetry(t *testing.T) {
	responseBody := strings.Repeat("x", 2048)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(10 * time.Millisecond)
		_, _ = w.Write([]byte(responseBody))
	}))
	defer ts.Close()

	h := initHTTP(t, map[string]any{"base_url": ts.URL})

	_, err := h.Execute(context.Background(), &protocol.Action{
		Name:   "warm-connection",
		Method: "GET",
		Target: "/telemetry",
	})
	if err != nil {
		t.Fatalf("warm Execute returned error: %v", err)
	}

	result, err := h.Execute(context.Background(), &protocol.Action{
		Name:   "server-telemetry",
		Method: "POST",
		Target: "/telemetry",
		Body:   []byte(strings.Repeat("a", 1024)),
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.ServerTelemetry == nil {
		t.Fatal("ServerTelemetry = nil, want populated telemetry")
	}
	if result.ServerTelemetry.RequestPayloadKB < 0.99 || result.ServerTelemetry.RequestPayloadKB > 1.01 {
		t.Fatalf("RequestPayloadKB = %v, want about 1.0", result.ServerTelemetry.RequestPayloadKB)
	}
	if result.ServerTelemetry.RequestSizeClass != "small" {
		t.Fatalf("RequestSizeClass = %q, want small", result.ServerTelemetry.RequestSizeClass)
	}
	if result.ServerTelemetry.ResponsePayloadKB < 1.99 || result.ServerTelemetry.ResponsePayloadKB > 2.01 {
		t.Fatalf("ResponsePayloadKB = %v, want about 2.0", result.ServerTelemetry.ResponsePayloadKB)
	}
	if result.ServerTelemetry.ResponseSizeClass != "small" {
		t.Fatalf("ResponseSizeClass = %q, want small", result.ServerTelemetry.ResponseSizeClass)
	}
	if result.ServerTelemetry.ServerProcessingMs < 0 {
		t.Fatalf("ServerProcessingMs = %v, want non-negative", result.ServerTelemetry.ServerProcessingMs)
	}
	if result.ServerTelemetry.TransferMs < 0 {
		t.Fatalf("TransferMs = %v, want non-negative", result.ServerTelemetry.TransferMs)
	}
	if !result.ServerTelemetry.ConnectionReused {
		t.Fatalf("ConnectionReused = %v, want true on warmed connection", result.ServerTelemetry.ConnectionReused)
	}
	if result.ServerTelemetry.RedirectCount != 0 {
		t.Fatalf("RedirectCount = %d, want 0", result.ServerTelemetry.RedirectCount)
	}
	if result.ServerTelemetry.RedirectMs != 0 {
		t.Fatalf("RedirectMs = %v, want 0", result.ServerTelemetry.RedirectMs)
	}
}

func TestHTTP_ExecuteCapturesRedirectTelemetry(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redirect":
			time.Sleep(10 * time.Millisecond)
			http.Redirect(w, r, "/final", http.StatusFound)
		case "/final":
			time.Sleep(5 * time.Millisecond)
			_, _ = w.Write([]byte("ok"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	h := initHTTP(t, map[string]any{"base_url": ts.URL})

	result, err := h.Execute(context.Background(), &protocol.Action{
		Name:   "redirect-telemetry",
		Method: "GET",
		Target: "/redirect",
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !result.Success {
		t.Fatalf("Success = false, error: %s", result.ErrorMessage)
	}
	if result.ServerTelemetry == nil {
		t.Fatal("ServerTelemetry = nil, want redirect telemetry")
	}
	if result.ServerTelemetry.RedirectCount != 1 {
		t.Fatalf("RedirectCount = %d, want 1", result.ServerTelemetry.RedirectCount)
	}
	if result.ServerTelemetry.RedirectMs <= 0 {
		t.Fatalf("RedirectMs = %v, want > 0", result.ServerTelemetry.RedirectMs)
	}
	if result.StatusCode != http.StatusOK {
		t.Fatalf("StatusCode = %d, want 200", result.StatusCode)
	}
}

func TestHTTP_Execute_ActionRedirectOverride(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redirect":
			http.Redirect(w, r, "/final", http.StatusFound)
		case "/final":
			_, _ = w.Write([]byte("ok"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	h := initHTTP(t, map[string]any{
		"base_url":      ts.URL,
		"max_redirects": float64(-1),
	})
	maxRedirects := 2
	result, err := h.Execute(context.Background(), &protocol.Action{
		Name:         "follow-action",
		Method:       "GET",
		Target:       "/redirect",
		MaxRedirects: &maxRedirects,
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !result.Success || result.StatusCode != http.StatusOK {
		t.Fatalf("result = success:%v status:%d error:%s, want followed redirect", result.Success, result.StatusCode, result.ErrorMessage)
	}
}

func TestHTTP_Execute_ActionTimeoutOverride(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(150 * time.Millisecond)
		_, _ = w.Write([]byte("slow"))
	}))
	defer ts.Close()

	h := initHTTP(t, map[string]any{
		"base_url":           ts.URL,
		"request_timeout_ms": float64(1000),
	})
	result, err := h.Execute(context.Background(), &protocol.Action{
		Name:      "short-timeout",
		Method:    "GET",
		Target:    "/",
		TimeoutMs: 25,
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Success || !strings.Contains(strings.ToLower(result.ErrorMessage), "timeout") {
		t.Fatalf("result = success:%v error:%q, want timeout", result.Success, result.ErrorMessage)
	}
}

func TestHTTP_Execute_POST(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("method = %q, want POST", r.Method)
		}
		w.WriteHeader(201)
		w.Write([]byte(`{"id":"123"}`))
	}))
	defer ts.Close()

	h := initHTTP(t, map[string]any{"base_url": ts.URL})

	result, err := h.Execute(context.Background(), &protocol.Action{
		Name:   "test-post",
		Method: "POST",
		Target: "/create",
		Body:   []byte(`{"name":"test"}`),
	})

	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Success {
		t.Errorf("Success = false, error: %s", result.ErrorMessage)
	}
	if result.StatusCode != 201 {
		t.Errorf("StatusCode = %d, want 201", result.StatusCode)
	}
	if result.BytesSent != 15 {
		t.Errorf("BytesSent = %d, want 15", result.BytesSent)
	}
}

func TestHTTP_Execute_DecodesBase64Body(t *testing.T) {
	binaryBody := []byte{0x00, 0xff, 0x10, 0x80, 0x42}
	var received []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		received, err = io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer ts.Close()

	h := initHTTP(t, map[string]any{"base_url": ts.URL})

	result, err := h.Execute(context.Background(), &protocol.Action{
		Name:         "binary-post",
		Method:       "POST",
		Target:       "/upload",
		Body:         []byte(base64.StdEncoding.EncodeToString(binaryBody)),
		BodyEncoding: "base64",
	})

	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Success {
		t.Fatalf("Success = false, error: %s", result.ErrorMessage)
	}
	if !bytes.Equal(received, binaryBody) {
		t.Fatalf("received body = %v, want %v", received, binaryBody)
	}
	if result.BytesSent != int64(len(binaryBody)) {
		t.Fatalf("BytesSent = %d, want %d", result.BytesSent, len(binaryBody))
	}
}

func TestHTTP_Execute_InvalidBase64BodyFailsBeforeSend(t *testing.T) {
	called := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	h := initHTTP(t, map[string]any{"base_url": ts.URL})

	result, err := h.Execute(context.Background(), &protocol.Action{
		Name:         "bad-binary-post",
		Method:       "POST",
		Target:       "/upload",
		Body:         []byte("not base64!"),
		BodyEncoding: "base64",
	})

	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if called {
		t.Fatal("server should not be called when base64 decoding fails")
	}
	if result.ErrorMessage == "" || !strings.Contains(result.ErrorMessage, "invalid base64 http body") {
		t.Fatalf("ErrorMessage = %q, want invalid base64 error", result.ErrorMessage)
	}
}

func TestHTTP_Execute_HostHeaderOverridesRequestHost(t *testing.T) {
	var seenHost string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenHost = r.Host
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	h := initHTTP(t, map[string]any{"base_url": ts.URL})
	result, err := h.Execute(context.Background(), &protocol.Action{
		Name:    "host-override",
		Method:  "GET",
		Target:  "/",
		Headers: map[string]string{"Host": "tenant.example.test"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Success {
		t.Fatalf("Success = false, error: %s", result.ErrorMessage)
	}
	if seenHost != "tenant.example.test" {
		t.Fatalf("request Host = %q, want tenant.example.test", seenHost)
	}
}

func TestHTTP_Execute_HeaderListPreservesDuplicateHeaders(t *testing.T) {
	var seenDuplicate []string
	var seenMapOnly string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenDuplicate = append([]string(nil), r.Header.Values("X-Duplicate")...)
		seenMapOnly = r.Header.Get("X-Map-Only")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	h := initHTTP(t, map[string]any{"base_url": ts.URL})
	result, err := h.Execute(context.Background(), &protocol.Action{
		Name:    "duplicate-headers",
		Method:  "GET",
		Target:  "/",
		Headers: map[string]string{"X-Duplicate": "summary", "X-Map-Only": "kept"},
		HeaderList: protocol.HeaderList{
			{Name: "X-Duplicate", Value: "one"},
			{Name: "X-Duplicate", Value: "two"},
		},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Success {
		t.Fatalf("Success = false, error: %s", result.ErrorMessage)
	}
	if strings.Join(seenDuplicate, ",") != "one,two" {
		t.Fatalf("X-Duplicate values = %#v, want one/two without map summary", seenDuplicate)
	}
	if seenMapOnly != "kept" {
		t.Fatalf("X-Map-Only = %q, want kept", seenMapOnly)
	}
}

func TestHTTP_Execute_UnsupportedActionAuthFailsBeforeSend(t *testing.T) {
	called := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	h := initHTTP(t, map[string]any{"base_url": ts.URL})
	result, err := h.Execute(context.Background(), &protocol.Action{
		Name:   "bad-auth",
		Method: "GET",
		Target: "/",
		Auth:   &protocol.AuthConfig{Type: "digest", Username: "alice", Password: "secret"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if called {
		t.Fatal("server should not be called for unsupported action auth")
	}
	if result.Success || !strings.Contains(result.ErrorMessage, "unsupported HTTP auth type") {
		t.Fatalf("result = %#v, want unsupported auth failure", result)
	}
}

func TestHTTP_Execute_DefaultMethod(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("method = %q, want GET (default)", r.Method)
		}
		w.Write([]byte("ok"))
	}))
	defer ts.Close()

	h := initHTTP(t, map[string]any{"base_url": ts.URL})

	result, _ := h.Execute(context.Background(), &protocol.Action{
		Name:   "default-method",
		Target: "/",
	})

	if !result.Success {
		t.Errorf("expected success, got error: %s", result.ErrorMessage)
	}
}

func TestHTTP_Execute_UsesTypeAsMethodWhenMethodMissing(t *testing.T) {
	seenMethod := ""
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenMethod = r.Method
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST from action type", r.Method)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("ok"))
	}))
	defer ts.Close()

	h := initHTTP(t, map[string]any{"base_url": ts.URL})
	result, err := h.Execute(context.Background(), &protocol.Action{
		Name:   "type-only-post",
		Type:   "POST",
		Target: "/",
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success, got error: %s", result.ErrorMessage)
	}
	if seenMethod != http.MethodPost {
		t.Fatalf("server saw method %q, want POST", seenMethod)
	}
}

func TestHTTP_Execute_ServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(500)
		w.Write([]byte("internal error"))
	}))
	defer ts.Close()

	h := initHTTP(t, map[string]any{"base_url": ts.URL})

	result, _ := h.Execute(context.Background(), &protocol.Action{
		Name:   "server-error",
		Method: "GET",
		Target: "/fail",
	})

	if result.Success {
		t.Error("5xx should not be marked as success")
	}
	if result.StatusCode != 500 {
		t.Errorf("StatusCode = %d, want 500", result.StatusCode)
	}
	if result.Snapshot == nil {
		t.Fatal("Snapshot = nil, want populated snapshot on HTTP failure")
	}
	if result.Snapshot.RequestMethod != http.MethodGet {
		t.Fatalf("Snapshot.RequestMethod = %q, want GET", result.Snapshot.RequestMethod)
	}
	if result.Snapshot.RequestURL != ts.URL+"/fail" {
		t.Fatalf("Snapshot.RequestURL = %q, want %q", result.Snapshot.RequestURL, ts.URL+"/fail")
	}
	if result.Snapshot.ResponseStatus != 500 {
		t.Fatalf("Snapshot.ResponseStatus = %d, want 500", result.Snapshot.ResponseStatus)
	}
	if result.Snapshot.ResponseBody != "internal error" {
		t.Fatalf("Snapshot.ResponseBody = %q, want internal error", result.Snapshot.ResponseBody)
	}
	if result.Snapshot.ResponseHeaders["Content-Type"] != "text/plain" {
		t.Fatalf("Snapshot.ResponseHeaders[Content-Type] = %q, want text/plain", result.Snapshot.ResponseHeaders["Content-Type"])
	}
}

func TestHTTP_Execute_HeadersApplied(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Custom") != "test-value" {
			t.Errorf("X-Custom = %q, want %q", r.Header.Get("X-Custom"), "test-value")
		}
		if r.Header.Get("User-Agent") != "APEXLoad/1.0" {
			t.Errorf("User-Agent = %q, want %q", r.Header.Get("User-Agent"), "APEXLoad/1.0")
		}
		w.Write([]byte("ok"))
	}))
	defer ts.Close()

	h := initHTTP(t, map[string]any{
		"base_url":   ts.URL,
		"user_agent": "APEXLoad/1.0",
	})

	h.Execute(context.Background(), &protocol.Action{
		Name:    "with-headers",
		Method:  "GET",
		Target:  "/",
		Headers: map[string]string{"X-Custom": "test-value"},
	})
}

func TestHTTP_Execute_AuthHeader(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer my-token" {
			t.Errorf("Authorization = %q, want %q", auth, "Bearer my-token")
		}
		w.Write([]byte("ok"))
	}))
	defer ts.Close()

	h := initHTTP(t, map[string]any{
		"base_url":   ts.URL,
		"auth_type":  "bearer",
		"auth_token": "my-token",
	})

	h.Execute(context.Background(), &protocol.Action{
		Name:   "auth-test",
		Method: "GET",
		Target: "/",
	})
}

func TestHTTP_Execute_ActionAuthOverridesGlobalAuth(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Basic YWN0aW9uOnNlY3JldA==" {
			t.Fatalf("Authorization = %q, want action basic auth", got)
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	defer ts.Close()

	h := initHTTP(t, map[string]any{
		"base_url":   ts.URL,
		"auth_type":  "bearer",
		"auth_token": "global-token",
	})

	result, err := h.Execute(context.Background(), &protocol.Action{
		Name:   "action-auth",
		Method: "GET",
		Target: "/",
		Auth: &protocol.AuthConfig{
			Type:     "basic",
			Username: "action",
			Password: "secret",
		},
		RedactedAuth: &protocol.AuthConfig{Type: "basic"},
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Snapshot == nil {
		t.Fatal("Snapshot = nil, want failed response snapshot")
	}
	if got := result.Snapshot.RequestHeaders["Authorization"]; got != "Basic [REDACTED]" {
		t.Fatalf("snapshot Authorization = %q, want redacted action auth", got)
	}
}

func TestHTTP_Execute_ActionAuthNoneSuppressesGlobalAuth(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("Authorization = %q, want no auth header", got)
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer ts.Close()

	h := initHTTP(t, map[string]any{
		"base_url":   ts.URL,
		"auth_type":  "bearer",
		"auth_token": "global-token",
	})

	result, err := h.Execute(context.Background(), &protocol.Action{
		Name:   "no-auth",
		Method: "GET",
		Target: "/",
		Auth:   &protocol.AuthConfig{Type: "none"},
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !result.Success {
		t.Fatalf("Success = false, error=%s", result.ErrorMessage)
	}
}

func TestHTTP_Execute_WithExtractor(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"access_token":"abc123","expires_in":3600}`))
	}))
	defer ts.Close()

	h := initHTTP(t, map[string]any{"base_url": ts.URL})

	result, _ := h.Execute(context.Background(), &protocol.Action{
		Name:   "extract-test",
		Method: "GET",
		Target: "/token",
		Extractors: []protocol.Extractor{
			{Name: "token", Source: "body", Type: "jsonpath", Expression: "$.access_token"},
		},
	})

	if result.Extractions["token"] != "abc123" {
		t.Errorf("extracted token = %q, want %q", result.Extractions["token"], "abc123")
	}
}

func TestHTTP_Execute_AppliesDataFormattingChainBeforeExtractionAndAssertions(t *testing.T) {
	encodedJSON := base64.StdEncoding.EncodeToString([]byte(`{"access_token":"abc123","expires_in":3600}`))
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(encodedJSON))
	}))
	defer ts.Close()

	h := initHTTP(t, map[string]any{
		"base_url":                        ts.URL,
		"web_data_formatting_auto_detect": true,
		"web_data_formatting_chain": []map[string]any{
			{"type": "base64"},
			{"type": "json_to_xml"},
		},
	})

	result, _ := h.Execute(context.Background(), &protocol.Action{
		Name:   "formatted-response",
		Method: "GET",
		Target: "/formatted",
		Extractors: []protocol.Extractor{
			{Name: "token", Source: "body", Type: "xpath", Expression: "/json/value/access_token/text()"},
		},
		Assertions: []protocol.Assertion{
			{Type: "body_contains", Value: "<access_token>abc123</access_token>"},
		},
	})

	if !result.Success {
		t.Fatalf("formatted response should pass, error: %s", result.ErrorMessage)
	}
	if result.Extractions["token"] != "abc123" {
		t.Fatalf("extracted token = %q, want abc123", result.Extractions["token"])
	}
	if !strings.Contains(result.ResponseBody, "<expires_in>3600</expires_in>") {
		t.Fatalf("response body = %q, want transformed xml", result.ResponseBody)
	}
}

func TestHTTP_Execute_AutoDetectsBase64ResponseBeforeExtractionAndAssertions(t *testing.T) {
	encodedJSON := base64.StdEncoding.EncodeToString([]byte(`{"access_token":"abc123","expires_in":3600}`))
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(encodedJSON))
	}))
	defer ts.Close()

	h := initHTTP(t, map[string]any{
		"base_url":                        ts.URL,
		"web_data_formatting_auto_detect": true,
	})

	result, _ := h.Execute(context.Background(), &protocol.Action{
		Name:   "formatted-response-auto-detect",
		Method: "GET",
		Target: "/formatted",
		Extractors: []protocol.Extractor{
			{Name: "token", Source: "body", Type: "jsonpath", Expression: "$.access_token"},
		},
		Assertions: []protocol.Assertion{
			{Type: "body_contains", Value: `"access_token":"abc123"`},
		},
	})

	if !result.Success {
		t.Fatalf("auto-detected formatted response should pass, error: %s", result.ErrorMessage)
	}
	if result.Extractions["token"] != "abc123" {
		t.Fatalf("extracted token = %q, want abc123", result.Extractions["token"])
	}
	if result.ResponseBody != `{"access_token":"abc123","expires_in":3600}` {
		t.Fatalf("response body = %q, want decoded json", result.ResponseBody)
	}
}

func TestHTTP_Execute_AppliesGzipDataFormattingBeforeExtractionAndAssertions(t *testing.T) {
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write([]byte(`{"access_token":"abc123","expires_in":3600}`)); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(compressed.Bytes())
	}))
	defer ts.Close()

	h := initHTTP(t, map[string]any{
		"base_url": ts.URL,
		"web_data_formatting_chain": []map[string]any{{
			"type": "gzip",
			"options": map[string]any{
				"mode": "decode",
			},
		}},
	})

	result, _ := h.Execute(context.Background(), &protocol.Action{
		Name:   "formatted-response-gzip",
		Method: "GET",
		Target: "/formatted",
		Extractors: []protocol.Extractor{{
			Name:       "token",
			Source:     "body",
			Type:       "jsonpath",
			Expression: "$.access_token",
		}},
		Assertions: []protocol.Assertion{{
			Type:  "body_contains",
			Value: `"access_token":"abc123"`,
		}},
	})

	if !result.Success {
		t.Fatalf("gzip formatted response should pass, error: %s", result.ErrorMessage)
	}
	if result.Extractions["token"] != "abc123" {
		t.Fatalf("extracted token = %q, want abc123", result.Extractions["token"])
	}
	if result.ResponseBody != `{"access_token":"abc123","expires_in":3600}` {
		t.Fatalf("response body = %q, want decoded json", result.ResponseBody)
	}
}

func TestHTTP_Execute_DecodesContentEncodingForAssertionsAndSnapshots(t *testing.T) {
	payload := `<soap:Envelope><soap:Body><Success>true</Success></soap:Body></soap:Envelope>`
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write([]byte(payload)); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	compressedBody := compressed.Bytes()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept-Encoding") != "gzip" {
			http.Error(w, "missing gzip accept encoding", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Length", strconv.Itoa(len(compressedBody)))
		w.Header().Set("Content-Type", "text/xml;charset=UTF-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(compressedBody)
	}))
	defer ts.Close()

	h := initHTTP(t, map[string]any{
		"base_url":        ts.URL,
		"accept_encoding": "gzip",
	})

	passing, _ := h.Execute(context.Background(), &protocol.Action{
		Name:   "gzip-success",
		Method: http.MethodGet,
		Target: "/soap",
		Assertions: []protocol.Assertion{{
			Type:  "body_contains",
			Value: "<Success>true</Success>",
		}},
	})
	if !passing.Success {
		t.Fatalf("decoded gzip assertion should pass, error: %s", passing.ErrorMessage)
	}
	if passing.ResponseBody != payload {
		t.Fatalf("response body = %q, want decoded SOAP payload", passing.ResponseBody)
	}

	failing, _ := h.Execute(context.Background(), &protocol.Action{
		Name:   "gzip-failure-snapshot",
		Method: http.MethodGet,
		Target: "/soap",
		Assertions: []protocol.Assertion{{
			Type:  "status_code",
			Value: "201",
		}},
	})
	if failing.Success {
		t.Fatal("status-code assertion should fail")
	}
	if failing.Snapshot == nil {
		t.Fatal("Snapshot = nil, want populated snapshot on assertion failure")
	}
	if failing.Snapshot.ResponseBody != payload {
		t.Fatalf("snapshot response body = %q, want decoded SOAP payload", failing.Snapshot.ResponseBody)
	}
	if _, ok := failing.Snapshot.ResponseHeaders["Content-Encoding"]; ok {
		t.Fatalf("snapshot response headers retained Content-Encoding: %#v", failing.Snapshot.ResponseHeaders)
	}
	if _, ok := failing.Snapshot.ResponseHeaders["Content-Length"]; ok {
		t.Fatalf("snapshot response headers retained Content-Length: %#v", failing.Snapshot.ResponseHeaders)
	}
}

func TestHTTP_Execute_WithAssertion(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	}))
	defer ts.Close()

	h := initHTTP(t, map[string]any{"base_url": ts.URL})

	result, _ := h.Execute(context.Background(), &protocol.Action{
		Name:   "assert-test",
		Method: "GET",
		Target: "/",
		Assertions: []protocol.Assertion{
			{Type: "status_code", Expected: float64(200)},
		},
	})

	if !result.Success {
		t.Errorf("assertion should pass, error: %s", result.ErrorMessage)
	}
}

func TestHTTP_Execute_FailedAssertion(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	}))
	defer ts.Close()

	h := initHTTP(t, map[string]any{"base_url": ts.URL})

	result, _ := h.Execute(context.Background(), &protocol.Action{
		Name:   "assert-fail",
		Method: "GET",
		Target: "/",
		Assertions: []protocol.Assertion{
			{Type: "status_code", Expected: float64(201)},
		},
	})

	if result.Success {
		t.Error("assertion should fail for status 200 vs expected 201")
	}
	if result.Snapshot == nil {
		t.Fatal("Snapshot = nil, want populated snapshot on assertion failure")
	}
	if result.Snapshot.ResponseStatus != 200 {
		t.Fatalf("Snapshot.ResponseStatus = %d, want 200", result.Snapshot.ResponseStatus)
	}
	if result.Snapshot.ResponseBody != "ok" {
		t.Fatalf("Snapshot.ResponseBody = %q, want ok", result.Snapshot.ResponseBody)
	}
}

func TestBuildHTTPSnapshot_RedactsSecretRequestValues(t *testing.T) {
	headers := http.Header{}
	headers.Set("Authorization", "Bearer super-secret")

	snapshot := buildHTTPSnapshot(
		http.MethodPost,
		"https://api.example.test/login?token=super-secret",
		headers,
		[]byte("token=super-secret"),
		"https://api.example.test/login?token=[REDACTED]",
		map[string]string{"Authorization": "Bearer [REDACTED]"},
		[]byte("token=[REDACTED]"),
		500,
		map[string]string{"Content-Type": "text/plain"},
		[]byte("failure"),
	)

	if snapshot.RequestURL != "https://api.example.test/login?token=[REDACTED]" {
		t.Fatalf("RequestURL = %q", snapshot.RequestURL)
	}
	if snapshot.RequestHeaders["Authorization"] != "Bearer [REDACTED]" {
		t.Fatalf("RequestHeaders[Authorization] = %q", snapshot.RequestHeaders["Authorization"])
	}
	if snapshot.RequestBody != "token=[REDACTED]" {
		t.Fatalf("RequestBody = %q", snapshot.RequestBody)
	}
}

func TestBuildHTTPSnapshot_UsesEffectiveURLWhenRedactedTargetIsUnresolvedTemplate(t *testing.T) {
	effectiveURL := "https://csi-gw-ext.att.com:443/Services/com/cingular/csi/edp/InquireSubscriberBillingAgreement.jws"
	snapshot := buildHTTPSnapshot(
		http.MethodPost,
		effectiveURL,
		nil,
		nil,
		"{{var.url}}/Services/com/cingular/csi/edp/InquireSubscriberBillingAgreement.jws",
		nil,
		nil,
		0,
		nil,
		nil,
	)

	if snapshot.RequestURL != effectiveURL {
		t.Fatalf("RequestURL = %q, want effective URL %q", snapshot.RequestURL, effectiveURL)
	}
}

func TestHTTP_Execute_WithRequiredGlobalContentCheck(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("order completed successfully"))
	}))
	defer ts.Close()

	h := initHTTP(t, map[string]any{
		"base_url": ts.URL,
		"web_content_checks": []map[string]any{{
			"text":          "completed",
			"scope":         "any_response",
			"fail_if_found": false,
		}},
	})

	result, _ := h.Execute(context.Background(), &protocol.Action{
		Name:   "global-content-check-pass",
		Method: "GET",
		Target: "/",
	})

	if !result.Success {
		t.Fatalf("required content check should pass, error: %s", result.ErrorMessage)
	}
}

func TestHTTP_Execute_FailsOnForbiddenGlobalContentCheck(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("Access Denied by upstream gateway"))
	}))
	defer ts.Close()

	h := initHTTP(t, map[string]any{
		"base_url": ts.URL,
		"web_content_checks": []map[string]any{{
			"text":          "Access Denied",
			"scope":         "main_document",
			"fail_if_found": true,
		}},
	})

	result, _ := h.Execute(context.Background(), &protocol.Action{
		Name:   "global-content-check-fail",
		Method: "GET",
		Target: "/",
	})

	if result.Success {
		t.Fatal("forbidden global content check should fail")
	}
	if !strings.Contains(result.ErrorMessage, "forbidden text") {
		t.Fatalf("error = %q, want forbidden text content-check failure", result.ErrorMessage)
	}
}

func TestHTTP_Execute_SkipsResourceScopedGlobalContentCheck(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("Access Denied by upstream gateway"))
	}))
	defer ts.Close()

	h := initHTTP(t, map[string]any{
		"base_url": ts.URL,
		"web_content_checks": []map[string]any{{
			"text":          "Access Denied",
			"scope":         "resource",
			"fail_if_found": true,
		}},
	})

	result, _ := h.Execute(context.Background(), &protocol.Action{
		Name:   "global-content-check-resource-skip",
		Method: "GET",
		Target: "/",
	})

	if !result.Success {
		t.Fatalf("resource-scoped content checks should be skipped, error: %s", result.ErrorMessage)
	}
}

func TestHTTP_ResolveURL(t *testing.T) {
	h := &HTTP{baseURL: "https://api.example.com"}

	tests := []struct {
		target string
		want   string
	}{
		{"/users", "https://api.example.com/users"},
		{"https://other.com/path", "https://other.com/path"},
		{"http://other.com/path", "http://other.com/path"},
		{"/", "https://api.example.com/"},
		{" https://other.com:443/path ", "https://other.com:443/path"},
		{"https://other.com:443 /path", "https://other.com:443/path"},
		{"https://other.com:443\t/path", "https://other.com:443/path"},
		{"https://other.com:443\r\n/path", "https://other.com:443/path"},
		{"{{var.url}}/Services/com/cingular/csi/edp/InquireSubscriberBillingAgreement.jws", "https://api.example.com/Services/com/cingular/csi/edp/InquireSubscriberBillingAgreement.jws"},
		{"{{var.url}} /Services/com/cingular/csi/edp/InquireSubscriberBillingAgreement.jws", "https://api.example.com/Services/com/cingular/csi/edp/InquireSubscriberBillingAgreement.jws"},
		{"{{param.URL}}/Services/com/cingular/csi/edp/InquireSubscriberBillingAgreement.jws", "https://api.example.com/Services/com/cingular/csi/edp/InquireSubscriberBillingAgreement.jws"},
	}

	for _, tt := range tests {
		got := h.resolveURL(tt.target)
		if got != tt.want {
			t.Errorf("resolveURL(%q) = %q, want %q", tt.target, got, tt.want)
		}
	}
}

func TestHTTP_HealthCheck(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "HEAD" {
			t.Errorf("HealthCheck method = %q, want HEAD", r.Method)
		}
		w.WriteHeader(200)
	}))
	defer ts.Close()

	h := initHTTP(t, map[string]any{"base_url": ts.URL})

	if err := h.HealthCheck(context.Background()); err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
}

func TestHTTP_HealthCheck_NoBaseURL(t *testing.T) {
	h := initHTTP(t, map[string]any{})

	if err := h.HealthCheck(context.Background()); err == nil {
		t.Fatal("expected error when no base_url configured")
	}
}

func TestHTTP_Teardown(t *testing.T) {
	h := initHTTP(t, map[string]any{"base_url": "https://example.com"})
	if err := h.Teardown(context.Background()); err != nil {
		t.Fatalf("Teardown: %v", err)
	}
}

func TestParseTLSVersion(t *testing.T) {
	tests := []struct {
		input string
		want  uint16
		err   bool
	}{
		{"1.0", tls.VersionTLS10, false},
		{"1.1", tls.VersionTLS11, false},
		{"1.2", tls.VersionTLS12, false},
		{"1.3", tls.VersionTLS13, false},
		{"invalid", 0, true},
	}

	for _, tt := range tests {
		got, err := parseTLSVersion(tt.input)
		if tt.err && err == nil {
			t.Errorf("parseTLSVersion(%q): expected error", tt.input)
		}
		if !tt.err && got != tt.want {
			t.Errorf("parseTLSVersion(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestHTTP_Execute_ExtractorOnMissFail(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"unrelated":"payload"}`))
	}))
	defer ts.Close()

	h := initHTTP(t, map[string]any{"base_url": ts.URL})

	result, err := h.Execute(context.Background(), &protocol.Action{
		Name:   "miss-fail",
		Method: "GET",
		Target: "/page",
		Extractors: []protocol.Extractor{
			{Name: "token", Source: "body", Type: "jsonpath", Expression: "$.access_token", OnMiss: "fail"},
		},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Success {
		t.Fatal("request should fail when an on_miss=fail extractor finds no match")
	}
	if !strings.Contains(result.ErrorMessage, `"token"`) {
		t.Errorf("error message should name the extractor: %q", result.ErrorMessage)
	}
}

func TestHTTP_Execute_ExtractorOnMissDefaultIgnores(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"unrelated":"payload"}`))
	}))
	defer ts.Close()

	h := initHTTP(t, map[string]any{"base_url": ts.URL})

	result, err := h.Execute(context.Background(), &protocol.Action{
		Name:   "miss-ignore",
		Method: "GET",
		Target: "/page",
		Extractors: []protocol.Extractor{
			{Name: "token", Source: "body", Type: "jsonpath", Expression: "$.access_token"},
		},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Success {
		t.Fatalf("request should stay successful without on_miss=fail: %s", result.ErrorMessage)
	}
	if _, ok := result.Extractions["token"]; ok {
		t.Error("missed extraction should leave the variable unset")
	}
}

func TestHTTP_Execute_ExtractorOnMissFailKeepsLaterExtractions(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"present":"yes"}`))
	}))
	defer ts.Close()

	h := initHTTP(t, map[string]any{"base_url": ts.URL})

	result, err := h.Execute(context.Background(), &protocol.Action{
		Name:   "miss-then-hit",
		Method: "GET",
		Target: "/page",
		Extractors: []protocol.Extractor{
			{Name: "absent", Source: "body", Type: "jsonpath", Expression: "$.absent", OnMiss: "fail"},
			{Name: "present", Source: "body", Type: "jsonpath", Expression: "$.present"},
		},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Success {
		t.Fatal("request should fail on the missed extractor")
	}
	if result.Extractions["present"] != "yes" {
		t.Errorf("later extractors should still run; got %v", result.Extractions)
	}
}

func TestHTTP_Execute_SaveAllExtractor(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`item=a; item=b; item=c;`))
	}))
	defer ts.Close()

	h := initHTTP(t, map[string]any{"base_url": ts.URL})
	result, err := h.Execute(context.Background(), &protocol.Action{
		Name:   "save-all",
		Method: "GET",
		Target: "/page",
		Extractors: []protocol.Extractor{
			{Name: "items", Source: "body", Type: "boundary", Expression: "item=|||;", Mode: "all"},
		},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	want := map[string]string{"items_1": "a", "items_2": "b", "items_3": "c", "items_count": "3"}
	for k, v := range want {
		if result.Extractions[k] != v {
			t.Errorf("Extractions[%q] = %q, want %q", k, result.Extractions[k], v)
		}
	}
	if _, ok := result.Extractions["items"]; ok {
		t.Error("save-all must not define the bare name")
	}
}

func TestHTTP_Execute_SaveAllZeroMatchesLenient(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`nothing here`))
	}))
	defer ts.Close()

	h := initHTTP(t, map[string]any{"base_url": ts.URL})
	result, err := h.Execute(context.Background(), &protocol.Action{
		Name:   "save-all-empty",
		Method: "GET",
		Target: "/page",
		Extractors: []protocol.Extractor{
			{Name: "items", Source: "body", Type: "boundary", Expression: "item=|||;", Mode: "all"},
		},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Success {
		t.Fatalf("lenient zero-match must not fail: %s", result.ErrorMessage)
	}
	if result.Extractions["items_count"] != "0" {
		t.Errorf("items_count = %q, want 0", result.Extractions["items_count"])
	}
}

func TestHTTP_Execute_SaveAllZeroMatchesFail(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`nothing here`))
	}))
	defer ts.Close()

	h := initHTTP(t, map[string]any{"base_url": ts.URL})
	result, err := h.Execute(context.Background(), &protocol.Action{
		Name:   "save-all-miss",
		Method: "GET",
		Target: "/page",
		Extractors: []protocol.Extractor{
			{Name: "items", Source: "body", Type: "boundary", Expression: "item=|||;", Mode: "all", OnMiss: "fail"},
		},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Success {
		t.Fatal("zero matches with on_miss=fail must fail the request")
	}
	if _, ok := result.Extractions["items_count"]; ok {
		t.Error("failed save-all must not publish a count")
	}
}

func TestHTTP_Execute_ExtractorOnMissEmptyAssignsEmptyString(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`no match here`))
	}))
	defer ts.Close()

	h := initHTTP(t, map[string]any{"base_url": ts.URL})
	result, err := h.Execute(context.Background(), &protocol.Action{
		Name:   "miss-empty",
		Method: "GET",
		Target: "/page",
		Extractors: []protocol.Extractor{
			{Name: "token", Source: "body", Type: "boundary", Expression: "token=|||;", OnMiss: "empty"},
		},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Success {
		t.Fatalf("on_miss=empty must not fail: %s", result.ErrorMessage)
	}
	if val, ok := result.Extractions["token"]; !ok || val != "" {
		t.Errorf("Extractions[token] = %q (present=%v), want empty string", val, ok)
	}
}

// TestHTTP_Execute_SaveAllOnMiss covers every on_miss disposition for a
// mode:"all" extractor that finds zero matches. The "empty" case is the bug
// fix: previously a save-all miss never assigned the base variable for "empty",
// so {{var.items}} leaked the literal template into later requests.
func TestHTTP_Execute_SaveAllOnMiss(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`nothing here`))
	}))
	defer ts.Close()

	tests := []struct {
		name string
		// onMiss is the extractor's on_miss disposition.
		onMiss string
		// wantSuccess is whether the request should still succeed.
		wantSuccess bool
		// wantBaseSet is whether the bare variable name must be assigned.
		wantBaseSet bool
		// wantBaseVal is the expected bare-variable value when wantBaseSet.
		wantBaseVal string
		// wantCountSet is whether name_count must be published.
		wantCountSet bool
		// wantCountVal is the expected name_count when wantCountSet.
		wantCountVal string
	}{
		{
			name:         "empty assigns empty string and zero count",
			onMiss:       "empty",
			wantSuccess:  true,
			wantBaseSet:  true,
			wantBaseVal:  "",
			wantCountSet: true,
			wantCountVal: "0",
		},
		{
			name:         "ignore leaves base unset but publishes zero count",
			onMiss:       "ignore",
			wantSuccess:  true,
			wantBaseSet:  false,
			wantCountSet: true,
			wantCountVal: "0",
		},
		{
			name:         "default leaves base unset but publishes zero count",
			onMiss:       "",
			wantSuccess:  true,
			wantBaseSet:  false,
			wantCountSet: true,
			wantCountVal: "0",
		},
		{
			name:         "fail fails the request and publishes nothing",
			onMiss:       "fail",
			wantSuccess:  false,
			wantBaseSet:  false,
			wantCountSet: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := initHTTP(t, map[string]any{"base_url": ts.URL})
			result, err := h.Execute(context.Background(), &protocol.Action{
				Name:   "save-all-onmiss",
				Method: "GET",
				Target: "/page",
				Extractors: []protocol.Extractor{
					{Name: "items", Source: "body", Type: "boundary", Expression: "item=|||;", Mode: "all", OnMiss: tc.onMiss},
				},
			})
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if result.Success != tc.wantSuccess {
				t.Fatalf("Success = %v, want %v (%s)", result.Success, tc.wantSuccess, result.ErrorMessage)
			}

			val, ok := result.Extractions["items"]
			if tc.wantBaseSet {
				if !ok {
					t.Errorf("Extractions[items] missing; want assigned %q so templates expand instead of leaking literally", tc.wantBaseVal)
				} else if val != tc.wantBaseVal {
					t.Errorf("Extractions[items] = %q, want %q", val, tc.wantBaseVal)
				}
			} else if ok {
				t.Errorf("Extractions[items] = %q, want unset for on_miss=%q", val, tc.onMiss)
			}

			count, ok := result.Extractions["items_count"]
			if tc.wantCountSet {
				if !ok {
					t.Errorf("items_count missing; want %q", tc.wantCountVal)
				} else if count != tc.wantCountVal {
					t.Errorf("items_count = %q, want %q", count, tc.wantCountVal)
				}
			} else if ok {
				t.Errorf("items_count = %q, want unpublished for on_miss=%q", count, tc.onMiss)
			}

			// Save-all must never define indexed slots on a miss.
			if v, ok := result.Extractions["items_1"]; ok {
				t.Errorf("items_1 = %q, want no indexed values on a zero-match", v)
			}
		})
	}
}

func TestHTTPExecuteServerSentEvents(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatalf("response writer does not implement http.Flusher")
		}
		_, _ = w.Write([]byte("event: token\n"))
		_, _ = w.Write([]byte("data: hello\n\n"))
		flusher.Flush()
		time.Sleep(10 * time.Millisecond)
		_, _ = w.Write([]byte("event: done\n"))
		_, _ = w.Write([]byte("data: complete\n\n"))
		flusher.Flush()
	}))
	defer ts.Close()

	h := initHTTP(t, map[string]any{"request_timeout_ms": float64(1000)})
	result, err := h.Execute(context.Background(), &protocol.Action{
		Name:   "agent-stream",
		Type:   "GET",
		Target: ts.URL,
		Params: map[string]any{
			"stop_on_event":     "done",
			"stream_timeout_ms": float64(1000),
		},
		Extractors: []protocol.Extractor{
			{Name: "first_token", Type: "sse_event", Expression: "token"},
			{Name: "all_events", Type: "sse_concat"},
		},
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !result.Success {
		t.Fatalf("result was not successful: %s", result.ErrorMessage)
	}
	if result.ServerTelemetry == nil {
		t.Fatalf("server telemetry missing")
	}
	if result.ServerTelemetry.EventCount != 2 {
		t.Fatalf("event count = %d, want 2", result.ServerTelemetry.EventCount)
	}
	if result.ServerTelemetry.FirstEventMs <= 0 {
		t.Fatalf("first event ms = %f, want > 0", result.ServerTelemetry.FirstEventMs)
	}
	if got := result.Extractions["first_token"]; got != "hello" {
		t.Fatalf("first_token = %q, want hello", got)
	}
	if got := result.Extractions["all_events"]; got != "hello\ncomplete" {
		t.Fatalf("all_events = %q, want concatenated data", got)
	}
	if !strings.Contains(result.ResponseBody, "event: done") {
		t.Fatalf("response body did not retain SSE framing: %q", result.ResponseBody)
	}
}

func TestHTTP_Execute_BoundsResponseBodySize(t *testing.T) {
	big := bytes.Repeat([]byte("a"), 256*1024) // 256 KiB
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(big)
	}))
	defer ts.Close()
	h := initHTTP(t, map[string]any{"base_url": ts.URL})

	// Cap below the body size → failed sample, body truncated to the cap.
	capped, _ := h.Execute(context.Background(), &protocol.Action{
		Name:   "capped",
		Method: http.MethodGet,
		Target: "/",
		Params: map[string]any{"max_body_bytes": 4096},
	})
	if capped.Success {
		t.Fatal("expected oversized response to fail the sample")
	}
	if capped.BytesReceived != 4096 {
		t.Fatalf("BytesReceived = %d, want 4096", capped.BytesReceived)
	}
	if !strings.Contains(capped.ErrorMessage, "exceeded") {
		t.Fatalf("ErrorMessage = %q, want limit error", capped.ErrorMessage)
	}

	// Opt out (<= 0) → full body read.
	full, _ := h.Execute(context.Background(), &protocol.Action{
		Name:   "uncapped",
		Method: http.MethodGet,
		Target: "/",
		Params: map[string]any{"max_body_bytes": 0},
	})
	if !full.Success {
		t.Fatalf("uncapped request should succeed: %s", full.ErrorMessage)
	}
	if full.BytesReceived != int64(len(big)) {
		t.Fatalf("uncapped BytesReceived = %d, want %d", full.BytesReceived, len(big))
	}
}

func TestHTTP_Execute_BoundsDecompressedBodySize(t *testing.T) {
	payload := bytes.Repeat([]byte("b"), 512*1024) // inflates well past the cap
	var compressed bytes.Buffer
	gw := gzip.NewWriter(&compressed)
	if _, err := gw.Write(payload); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	body := compressed.Bytes()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	defer ts.Close()
	h := initHTTP(t, map[string]any{"base_url": ts.URL, "accept_encoding": "gzip"})

	res, _ := h.Execute(context.Background(), &protocol.Action{
		Name:   "bomb",
		Method: http.MethodGet,
		Target: "/",
		Params: map[string]any{"max_body_bytes": 8192},
	})
	if res.Success {
		t.Fatal("expected decompression past the cap to fail the sample")
	}
	if !strings.Contains(res.ErrorMessage, "exceeded") {
		t.Fatalf("ErrorMessage = %q, want decoded limit error", res.ErrorMessage)
	}
}
