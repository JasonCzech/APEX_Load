package java_http

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
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

func TestJavaHTTPResponseMappingBoundsFailClosed(t *testing.T) {
	oversizedMapping := make(map[string]string, protocol.MaxExtractionEntries+1)
	for i := 0; i <= protocol.MaxExtractionEntries; i++ {
		oversizedMapping[strings.Repeat("x", i%8+1)+string(rune(i+1))] = "value"
	}
	if err := (&JavaHTTP{}).Init(context.Background(), map[string]any{
		"java_http_response_mapping": oversizedMapping,
	}); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized response mapping error = %v", err)
	}

	body := strings.Repeat("z", protocol.MaxExtractionValueBytes+1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer ts.Close()
	p := &JavaHTTP{}
	if err := p.Init(context.Background(), map[string]any{
		"base_url": ts.URL,
		"java_http_response_mapping": map[string]string{
			"payload": `(z+)`,
		},
	}); err != nil {
		t.Fatal(err)
	}
	result, err := p.Execute(context.Background(), &protocol.Action{Name: "mapped", Method: "GET", Target: "/"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Success || !strings.Contains(result.ErrorMessage, "rejected") || len(result.Extractions) != 0 {
		t.Fatalf("oversized mapped extraction success=%v error=%q extractions=%d", result.Success, result.ErrorMessage, len(result.Extractions))
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

	result, err := p.Execute(context.Background(), &protocol.Action{
		Name:   "bad",
		Method: "POST",
		Target: "/invoke",
		Headers: map[string]string{
			"X-Request-ID": "req-123",
		},
		Body: []byte("not-base64%%"),
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result == nil || result.ErrorMessage == "" {
		t.Fatalf("result = %#v, want validation error", result)
	}
	if result.DependencyType != "java_http" {
		t.Fatalf("DependencyType = %q, want java_http", result.DependencyType)
	}
	if result.ServerTelemetry == nil {
		t.Fatalf("ServerTelemetry = %#v, want serialization timing", result.ServerTelemetry)
	}
	if result.Snapshot == nil {
		t.Fatal("Snapshot = nil, want retained Java HTTP preflight request")
	}
	if result.Snapshot.RequestMethod != "POST" {
		t.Fatalf("Snapshot.RequestMethod = %q, want POST", result.Snapshot.RequestMethod)
	}
	if result.Snapshot.RequestURL != "https://example.com/invoke" {
		t.Fatalf("Snapshot.RequestURL = %q, want https://example.com/invoke", result.Snapshot.RequestURL)
	}
	if result.Snapshot.RequestHeaders["Content-Type"] != "application/x-java-serialized-object" {
		t.Fatalf("Snapshot.RequestHeaders Content-Type = %q, want application/x-java-serialized-object", result.Snapshot.RequestHeaders["Content-Type"])
	}
	if result.Snapshot.RequestHeaders["X-Request-ID"] != "req-123" {
		t.Fatalf("Snapshot.RequestHeaders X-Request-ID = %q, want req-123", result.Snapshot.RequestHeaders["X-Request-ID"])
	}
	if result.Snapshot.RequestBody != "not-base64%%" {
		t.Fatalf("Snapshot.RequestBody = %q, want original encoded body", result.Snapshot.RequestBody)
	}
	if result.BytesSent != 0 {
		t.Fatalf("BytesSent = %d, want 0 because request was not sent", result.BytesSent)
	}
}

func TestJavaHTTPExecuteRejectsUnsupportedActionBodyEncoding(t *testing.T) {
	p := &JavaHTTP{}
	if err := p.Init(context.Background(), map[string]any{
		"base_url":                "https://example.com",
		"java_http_codec":         supportedCodec,
		"java_http_helper_mode":   "embedded",
		"java_http_body_encoding": "base64",
	}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}

	result, err := p.Execute(context.Background(), &protocol.Action{
		Name:         "raw",
		Method:       "POST",
		Target:       "/invoke",
		BodyEncoding: "raw",
		Body:         []byte("not-base64"),
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result == nil || !strings.Contains(result.ErrorMessage, "unsupported java_http action body encoding") {
		t.Fatalf("result = %#v, want body encoding error", result)
	}
	if result.BytesSent != 0 {
		t.Fatalf("BytesSent = %d, want 0 because request was not sent", result.BytesSent)
	}
}

func TestResolveJavaHTTPURLNormalizesAbsoluteURLBoundaryWhitespace(t *testing.T) {
	got := resolveJavaHTTPURL("https://example.com", "https://service.example.com:443 /invoke")
	if got != "https://service.example.com:443/invoke" {
		t.Fatalf("resolveJavaHTTPURL returned %q", got)
	}
}

func TestResolveJavaHTTPURLTrimsUnresolvedLeadingTemplateBase(t *testing.T) {
	got := resolveJavaHTTPURL("https://service.example.com:443", "{{var.url}}/invoke")
	if got != "https://service.example.com:443/invoke" {
		t.Fatalf("resolveJavaHTTPURL returned %q", got)
	}
}

func TestJavaHTTPResponseMappingExtractsFromRawPayload(t *testing.T) {
	// A Java serialized stream carries its string fields as ASCII, so a regex
	// mapping can pull a named value out of the opaque payload for correlation.
	responsePayload := append([]byte{0xac, 0xed, 0x00, 0x05}, []byte(`sessionId=sess-abc-987;ok`)...)
	requestBase64 := base64.StdEncoding.EncodeToString([]byte{0xac, 0xed, 0x00, 0x05})

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(responsePayload)
	}))
	defer ts.Close()

	p := &JavaHTTP{}
	if err := p.Init(context.Background(), map[string]any{
		"base_url":                         ts.URL,
		"java_http_codec":                  supportedCodec,
		"java_http_helper_mode":            "embedded",
		"java_http_body_encoding":          "base64",
		"java_http_preserve_binary_result": true,
		"java_http_response_mapping": map[string]string{
			"session_id": `sessionId=([^;]+)`,
		},
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
	if got := result.Extractions["session_id"]; got != "sess-abc-987" {
		t.Fatalf("session_id extraction = %q, want sess-abc-987", got)
	}
	// Response body is still preserved as opaque base64.
	if result.ResponseBody != base64.StdEncoding.EncodeToString(responsePayload) {
		t.Fatalf("response body not preserved as base64: %q", result.ResponseBody)
	}
}

func TestJavaHTTPResponseMappingInvalidRegexFallsBackToLiteral(t *testing.T) {
	got := applyResponseMapping(map[string]string{"literal": "[session"}, "prefix [session suffix")
	if got["literal"] != "[session" {
		t.Fatalf("literal extraction = %#v, want invalid regex treated literally", got)
	}
}

func TestJavaHTTPResponseMappingEmptyIsNoop(t *testing.T) {
	responsePayload := []byte{0xac, 0xed, 0x00, 0x05, 'o', 'k'}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(responsePayload)
	}))
	defer ts.Close()

	p := &JavaHTTP{}
	if err := p.Init(context.Background(), map[string]any{
		"base_url":                ts.URL,
		"java_http_codec":         supportedCodec,
		"java_http_body_encoding": "base64",
	}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	result, err := p.Execute(context.Background(), &protocol.Action{Name: "c", Method: "POST", Target: "/x", Body: []byte(base64.StdEncoding.EncodeToString([]byte{0xac, 0xed}))})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	// Only the standard codec/helper extractions are present.
	if len(result.Extractions) != 2 {
		t.Fatalf("expected only the 2 standard extractions, got %#v", result.Extractions)
	}
}

func TestJavaHTTPUsesTypeAsMethodWhenMethodMissing(t *testing.T) {
	seenMethod := ""
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenMethod = r.Method
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST from action type", r.Method)
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("ok"))
	}))
	defer ts.Close()

	p := &JavaHTTP{}
	if err := p.Init(context.Background(), map[string]any{
		"base_url":                ts.URL,
		"java_http_codec":         supportedCodec,
		"java_http_body_encoding": "base64",
	}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	result, err := p.Execute(context.Background(), &protocol.Action{Name: "type-only", Type: "POST", Target: "/x"})
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

func TestJavaHTTPInitRejectsSidecarHelperMode(t *testing.T) {
	p := &JavaHTTP{}
	err := p.Init(context.Background(), map[string]any{
		"base_url":                "https://example.com",
		"java_http_codec":         supportedCodec,
		"java_http_helper_mode":   "sidecar",
		"java_http_body_encoding": "base64",
	})
	if err == nil || !strings.Contains(err.Error(), "sidecar is not wired") {
		t.Fatalf("Init error = %v, want sidecar rejection", err)
	}
}

// TestJavaHTTPClearCacheAndCookiesDelegateToWrappedHTTP covers the
// protocol.CacheController / CookieController delegation that browser
// emulation's "simulate a new user on each iteration" relies on. The compile
// -time interface assertions only prove the methods exist; this proves they
// actually reach the wrapped HTTP instance that owns the cache and cookie jar.
func TestJavaHTTPClearCacheAndCookiesDelegateToWrappedHTTP(t *testing.T) {
	var (
		mu         sync.Mutex
		requests   int
		sawCookies []string
	)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		sawCookies = append(sawCookies, r.Header.Get("Cookie"))
		mu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: "sid", Value: "abc", Path: "/"})
		w.Header().Set("Cache-Control", "max-age=60")
		_, _ = w.Write([]byte("ok"))
	}))
	defer ts.Close()

	p := &JavaHTTP{}
	if err := p.Init(context.Background(), map[string]any{
		"base_url":           ts.URL,
		"http_cache_enabled": true,
	}); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Teardown(context.Background()) }()

	get := func() {
		t.Helper()
		if _, err := p.Execute(context.Background(), &protocol.Action{Name: "page", Method: "GET", Target: "/"}); err != nil {
			t.Fatalf("execute: %v", err)
		}
	}
	countRequests := func() int {
		mu.Lock()
		defer mu.Unlock()
		return requests
	}

	get()
	if countRequests() != 1 {
		t.Fatalf("first GET reached server %d times, want 1", countRequests())
	}
	// Second GET is served from the simulated cache, so the server sees nothing.
	get()
	if countRequests() != 1 {
		t.Fatalf("cached GET reached server %d times, want 1", countRequests())
	}

	if err := p.ClearCache(context.Background()); err != nil {
		t.Fatalf("ClearCache: %v", err)
	}
	get()
	if countRequests() != 2 {
		t.Fatalf("after ClearCache the GET reached server %d times, want 2", countRequests())
	}

	mu.Lock()
	lastCookie := sawCookies[len(sawCookies)-1]
	mu.Unlock()
	if !strings.Contains(lastCookie, "sid=abc") {
		t.Fatalf("expected the jar to replay the cookie, got %q", lastCookie)
	}

	if err := p.ClearCookies(context.Background()); err != nil {
		t.Fatalf("ClearCookies: %v", err)
	}
	if err := p.ClearCache(context.Background()); err != nil {
		t.Fatalf("ClearCache: %v", err)
	}
	get()
	mu.Lock()
	afterClear := sawCookies[len(sawCookies)-1]
	mu.Unlock()
	if afterClear != "" {
		t.Fatalf("cookie header after ClearCookies = %q, want empty", afterClear)
	}
}

// Nil inner protocol must not panic: the engine may clear before Init on an
// aborted VUser start.
func TestJavaHTTPClearCacheAndCookiesNilInnerAreSafe(t *testing.T) {
	p := &JavaHTTP{}
	if err := p.ClearCache(context.Background()); err != nil {
		t.Fatalf("ClearCache on uninitialized protocol: %v", err)
	}
	if err := p.ClearCookies(context.Background()); err != nil {
		t.Fatalf("ClearCookies on uninitialized protocol: %v", err)
	}
}
