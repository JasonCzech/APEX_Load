package http

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jasonczech/stormrunner/pkg/protocol"
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
		"user_agent":      "StormRunner/1.0",
		"accept_encoding": "gzip",
		"accept_language": "en-US",
		"default_headers": map[string]string{"X-Custom": "value"},
	})

	if h.defaultHeaders["User-Agent"] != "StormRunner/1.0" {
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

func TestHTTP_Execute_ServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
}

func TestHTTP_Execute_HeadersApplied(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Custom") != "test-value" {
			t.Errorf("X-Custom = %q, want %q", r.Header.Get("X-Custom"), "test-value")
		}
		if r.Header.Get("User-Agent") != "StormRunner/1.0" {
			t.Errorf("User-Agent = %q, want %q", r.Header.Get("User-Agent"), "StormRunner/1.0")
		}
		w.Write([]byte("ok"))
	}))
	defer ts.Close()

	h := initHTTP(t, map[string]any{
		"base_url":   ts.URL,
		"user_agent": "StormRunner/1.0",
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
