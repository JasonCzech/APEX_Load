package http

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	stdhttp "net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func contentGzip(t *testing.T, body []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	writer := gzip.NewWriter(&out)
	_, err := writer.Write(body)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
func TestHTTPContentDecodeOrderedStackWireHeadersDecodedCacheAndPolicy(t *testing.T) {
	payload := []byte(`{"value":"synthetic"}`)
	gzipBody := contentGzip(t, payload)
	var encoded bytes.Buffer
	writer := brotli.NewWriter(&encoded)
	_, _ = writer.Write(gzipBody)
	_ = writer.Close()
	var calls atomic.Int64
	server := versionTLS(t, stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		calls.Add(1)
		w.Header().Add("Content-Encoding", "gzip")
		w.Header().Add("Content-Encoding", "br")
		w.Header().Set("Content-Length", strconv.Itoa(encoded.Len()))
		w.Header().Set("Cache-Control", "max-age=3600")
		_, _ = w.Write(encoded.Bytes())
	}))
	h := versionHTTP(t, map[string]any{"http_version": "2", "max_conns_per_host": 1, "http_cache_enabled": true, protocol.CorrelationEvidenceCaptureConfigKey: true})
	for _, policy := range []string{"1.1", "2"} {
		for pass := range 2 {
			result, err := h.Execute(context.Background(), &protocol.Action{Type: "GET", Target: server.URL, HTTPVersion: versionOverride(t, policy), CaptureMessageDetail: true, Extractors: []protocol.Extractor{{Name: "observed", Source: "body", Type: "jsonpath", Expression: "$.value"}}})
			if err != nil || !result.Success || result.ResponseBody != string(payload) || result.Extractions["observed"] != "synthetic" {
				t.Fatalf("stack decode failed: %v %s", err, result.ErrorMessage)
			}
			if result.Snapshot == nil || result.Snapshot.ResponseHeaders["Content-Encoding"] != "" || result.Snapshot.ResponseHeaders["Content-Length"] != "" || result.Snapshot.ResponseBodyState != "captured" {
				t.Fatal("decoded snapshot retained wire representation headers")
			}
			if result.ProtocolEvidence["response_content_state"] != "complete" || result.ProtocolEvidence["http_version_policy"] != policy {
				t.Fatal("content receipt overwrote version policy")
			}
			if pass == 0 {
				if result.BytesReceived != int64(encoded.Len()) || result.CorrelationEvidence == nil || result.CorrelationEvidence.ResponseHeaders["Content-Encoding"] != "gzip\nbr" || result.ProtocolEvidence["response_content_basis"] != "decoded" {
					t.Fatal("wire counters/headers or decoded basis were lost")
				}
			} else if result.HTTPVersionSource != "cache" || result.HTTPVersionNegotiated != "" {
				t.Fatal("decoded cache invented a negotiated wire version")
			}
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("decoded cache/policy partition calls=%d", calls.Load())
	}
}
func TestHTTPContentDecodeFailureOmitsBodyExtractionsCacheAndCorrelation(t *testing.T) {
	complete := contentGzip(t, []byte(`{"access_token":"synthetic"}`))
	for _, tc := range []struct {
		name, encoding string
		body           []byte
	}{{"unknown", "private-unsupported", []byte("private-body")}, {"malformed", "gzip,", complete}, {"truncated", "gzip", complete[:len(complete)-2]}, {"invalid", "gzip", []byte("private-body")}, {"empty", "zstd", nil}} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int64
			server := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
				calls.Add(1)
				w.Header().Set("Content-Encoding", tc.encoding)
				w.Header().Set("Cache-Control", "max-age=3600")
				_, _ = w.Write(tc.body)
			}))
			defer server.Close()
			h := versionHTTP(t, map[string]any{"http_cache_enabled": true, protocol.CorrelationEvidenceCaptureConfigKey: true})
			for range 2 {
				result, err := h.Execute(context.Background(), &protocol.Action{Type: "GET", Target: server.URL, Extractors: []protocol.Extractor{{Name: "access_token", Source: "body", Type: "regex", Expression: `(.+)`}}})
				if err != nil || result.Success || result.ResponseBody != "" || len(result.Extractions) != 0 || result.Snapshot == nil || result.Snapshot.ResponseBody != "" || result.Snapshot.ResponseBodyState != "not_captured" {
					t.Fatal("failed finite decode published partial body")
				}
				if result.CorrelationEvidence == nil || result.CorrelationEvidence.UnavailableReason == "" || result.CorrelationEvidence.ResponseBody != "" || result.CorrelationEvidence.RequestURL != "" {
					t.Fatal("failed finite decode retained a raw partial exchange")
				}
				if result.ProtocolEvidence["response_content_state"] != "unavailable" || result.ProtocolEvidence["response_content_basis"] != nil || strings.Contains(result.ErrorMessage, "private") {
					t.Fatal("failed decoder exposed source encoding or claimed completion")
				}
			}
			if calls.Load() != 2 {
				t.Fatal("failed decode was cached")
			}
		})
	}
}
func TestHTTPContentDecodeCompressedSSECollectionWindowAndCorruptTrailer(t *testing.T) {
	for _, mode := range []string{"stop", "window", "truncated"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("Content-Encoding", "gzip")
				if mode == "truncated" {
					body := contentGzip(t, []byte("event: token\ndata: synthetic\n\n"))
					_, _ = w.Write(body[:len(body)-2])
					return
				}
				writer := gzip.NewWriter(w)
				_, _ = io.WriteString(writer, "event: token\ndata: synthetic\n\n")
				_ = writer.Flush()
				w.(stdhttp.Flusher).Flush()
				<-r.Context().Done()
			}))
			defer server.Close()
			h := versionHTTP(t, map[string]any{protocol.CorrelationEvidenceCaptureConfigKey: true})
			params := map[string]any{"stream_timeout_ms": 50}
			if mode == "stop" {
				params["stop_on_event"] = "token"
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			result, err := h.Execute(ctx, &protocol.Action{Type: "GET", Target: server.URL, CaptureMessageDetail: true, Params: params, Extractors: []protocol.Extractor{{Name: "sample", Source: "body", Type: "regex", Expression: `data: ([^\n]+)`}}})
			if err != nil {
				t.Fatal(err)
			}
			if mode == "truncated" {
				if result.Success || result.ResponseBody != "" || len(result.Extractions) != 0 || result.Snapshot.ResponseBodyState != "not_captured" {
					t.Fatal("corrupt compressed SSE trailer became successful partial evidence")
				}
				return
			}
			if !result.Success || result.Extractions["sample"] != "synthetic" || result.ProtocolEvidence["response_content_state"] != "collection_window" || result.Snapshot.ResponseBodyState != "partial" {
				t.Fatalf("compressed collection failed or claimed finite completion: %s", result.ErrorMessage)
			}
			if result.CorrelationEvidence == nil || result.CorrelationEvidence.UnavailableReason == "" || result.CorrelationEvidence.ResponseBody != "" {
				t.Fatal("sampled SSE events became complete correlation input")
			}
			if result.ServerTelemetry == nil || result.ServerTelemetry.EventCount != 1 || result.BytesReceived <= 0 {
				t.Fatal("compressed SSE lost event or wire telemetry")
			}
		})
	}
}
func TestHTTPContentDecodeNoBodySemanticsAndNoRequestDecompression(t *testing.T) {
	server := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		if r.Method == "HEAD" || r.URL.Path == "/no-content" {
			w.Header().Set("Content-Encoding", "gzip")
			if r.Method != "HEAD" {
				w.WriteHeader(204)
			}
			return
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != "literal request" {
			w.WriteHeader(400)
			return
		}
		fmt.Fprint(w, "accepted")
	}))
	defer server.Close()
	h := versionHTTP(t, nil)
	for _, action := range []*protocol.Action{{Type: "HEAD", Target: server.URL}, {Type: "GET", Target: server.URL + "/no-content"}, {Type: "POST", Target: server.URL, Headers: map[string]string{"Content-Encoding": "gzip"}, Body: []byte("literal request")}} {
		result, err := h.Execute(context.Background(), action)
		if err != nil || !result.Success || result.ProtocolEvidence["response_content_state"] != "complete" {
			t.Fatalf("bodyless or authored request semantics changed: %s", result.ErrorMessage)
		}
	}
}
func TestHTTPContentDecodeCorruptResourceOmitsSnapshotCSSAndCache(t *testing.T) {
	var css, hidden atomic.Int64
	server := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<link rel="stylesheet" href="/style">`)
		case "/style":
			css.Add(1)
			w.Header().Set("Content-Type", "text/css")
			w.Header().Set("Content-Encoding", "gzip")
			w.Header().Set("Cache-Control", "max-age=3600")
			fmt.Fprint(w, `body { background: url('/hidden'); }`)
		case "/hidden":
			hidden.Add(1)
		}
	}))
	defer server.Close()
	h := versionHTTP(t, map[string]any{"http_download_non_html_resources": true, "http_cache_enabled": true})
	for range 2 {
		result, err := h.Execute(context.Background(), &protocol.Action{Type: "GET", Target: server.URL, CaptureResourceDetails: true, CaptureMessageDetail: true})
		if err != nil {
			t.Fatal(err)
		}
		if result.Snapshot == nil || len(result.Snapshot.Resources) != 1 || result.Snapshot.Resources[0].ResponseBody != "" || result.Snapshot.Resources[0].ResponseBodyState != "not_captured" {
			t.Fatal("corrupt resource published a partial decoded body")
		}
	}
	if css.Load() != 2 || hidden.Load() != 0 {
		t.Fatalf("corrupt resource cached or parsed CSS: css=%d hidden=%d", css.Load(), hidden.Load())
	}
}

func TestHTTPContentDecodeBodylessResourceAndRevalidationMetadata(t *testing.T) {
	for _, status := range []int{stdhttp.StatusNoContent, stdhttp.StatusNotModified} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			server := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
				w.Header().Set("Content-Encoding", "gzip")
				w.WriteHeader(status)
			}))
			defer server.Close()
			h := versionHTTP(t, nil)
			entry, _, _, failed, _ := h.fetchResource(context.Background(), resourceRef{url: server.URL, resourceType: "image"}, server.URL, nil)
			if failed || entry.StatusCode != status || entry.FailureKind != "" {
				t.Fatal("bodyless resource was decoded as truncated compressed content")
			}
		})
	}
	const payload = "body {color: green}"
	encoded := contentGzip(t, []byte(payload))
	var calls atomic.Int64
	server := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/css")
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("ETag", `"fixture"`)
		w.Header().Set("Cache-Control", "max-age=0")
		if r.Header.Get("If-None-Match") != "" {
			w.Header().Set("Cache-Control", "max-age=3600")
			w.WriteHeader(stdhttp.StatusNotModified)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(encoded)))
		_, _ = w.Write(encoded)
	}))
	defer server.Close()
	h := versionHTTP(t, map[string]any{"http_cache_enabled": true})
	ctx := context.WithValue(context.Background(), httpVersionCacheKey{}, "auto")
	ref := resourceRef{url: server.URL, resourceType: "stylesheet"}
	entry, _, css, failed, hit := h.fetchResource(ctx, ref, server.URL, nil)
	if failed || hit || entry.StatusCode != 200 || css == nil || string(css.body) != payload {
		t.Fatal("initial compressed CSS resource failed")
	}
	entry, _, _, failed, hit = h.fetchResource(ctx, ref, server.URL, nil)
	if failed || !hit || entry.StatusCode != 304 {
		t.Fatal("compressed resource revalidation failed")
	}
	req, _ := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, server.URL, nil)
	cached, fresh := h.cache.lookup(req, time.Now())
	if cached == nil || !fresh || string(cached.body) != payload || cached.header.Get("Content-Encoding") != "" || cached.header.Get("Content-Length") != "" {
		t.Fatal("304 wire metadata corrupted decoded cached representation")
	}
	result, err := h.Execute(context.Background(), &protocol.Action{Type: "GET", Target: server.URL, CaptureMessageDetail: true})
	if err != nil || !result.Success || result.ResponseBody != payload || result.HTTPVersionSource != "cache" || calls.Load() != 2 {
		t.Fatal("decoded revalidated cache was re-decoded or fetched")
	}
}

func TestHTTPContentDecodeLargeResourceValidatesWholeStreamWithoutRetention(t *testing.T) {
	payload := bytes.Repeat([]byte("synthetic resource bytes "), (httpCacheMaxBodyBytes/24)+1024)
	complete := contentGzip(t, payload)
	for _, tc := range []struct {
		name, encoding string
		body           []byte
		failure        bool
	}{
		{"large_identity", "identity", payload, false},
		{"large_gzip", "gzip", complete, false},
		{"large_unknown", "private-encoding", payload, true},
		{"large_gzip_bad_trailer", "gzip", complete[:len(complete)-2], true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
				w.Header().Set("Content-Type", "text/css")
				w.Header().Set("Content-Encoding", tc.encoding)
				w.Header().Set("Cache-Control", "max-age=3600")
				_, _ = w.Write(tc.body)
			}))
			defer server.Close()
			h := versionHTTP(t, map[string]any{"http_cache_enabled": true})
			capture := newResourceCapture(32 << 10)
			defer capture.lease.release()
			ctx := context.WithValue(context.Background(), resourceCaptureKey{}, capture)
			ctx = context.WithValue(ctx, httpVersionCacheKey{}, "auto")
			entry, wire, css, failed, hit := h.fetchResource(ctx, resourceRef{url: server.URL, resourceType: "stylesheet"}, server.URL, nil)
			if failed != tc.failure || hit || css != nil {
				t.Fatalf("large resource admission failed=%v, want %v", failed, tc.failure)
			}
			if !failed && (wire != int64(len(tc.body)) || entry.FailureKind != "") {
				t.Fatal("large successful resource lost actual wire counter")
			}
			req, _ := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, server.URL, nil)
			if cached, _ := h.cache.lookup(req, time.Now()); cached != nil {
				t.Fatal("large/failed resource was cached from incomplete retained bytes")
			}
			if len(capture.entries) != 1 || capture.entries[0].ResponseBody != "" || capture.entries[0].ResponseBodyState != "not_captured" {
				t.Fatal("large/failed resource exposed partial decoded content")
			}
		})
	}
}

func TestHTTPContentDecodeMainRevalidationQualifiesCacheWithoutLosingWireVersion(t *testing.T) {
	const payload = `{"value":"synthetic"}`
	encoded := contentGzip(t, []byte(payload))
	var calls atomic.Int64
	server := versionTLS(t, stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		calls.Add(1)
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Cache-Control", "max-age=0")
		w.Header().Set("ETag", `"fixture"`)
		if r.Header.Get("If-None-Match") != "" {
			w.WriteHeader(stdhttp.StatusNotModified)
			return
		}
		_, _ = w.Write(encoded)
	}))
	h := versionHTTP(t, map[string]any{"http_version": "2", "http_cache_enabled": true})
	for pass := range 2 {
		result, err := h.Execute(context.Background(), &protocol.Action{Type: "GET", Target: server.URL, CaptureMessageDetail: true})
		if err != nil || !result.Success || result.ResponseBody != payload {
			t.Fatal("main decoded revalidation failed")
		}
		if result.HTTPVersionNegotiated != "2" || result.HTTPVersionSource != "wire" {
			t.Fatal("actual wire version of revalidation response was lost")
		}
		if pass == 1 && (result.ProtocolEvidence["response_content_source"] != "cache" || result.ProtocolEvidence["response_content_basis"] != "identity" || result.ProtocolEvidence["response_content_state"] != "complete") {
			t.Fatal("cached logical revalidation body claimed fresh wire representation")
		}
	}
	if calls.Load() != 2 {
		t.Fatal("revalidation fixture failed to exercise actual304")
	}
}
func TestHTTPContentDecodeSSEFormattingFailureCannotReclaimPartialBody(t *testing.T) {
	server := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Content-Encoding", "gzip")
		writer := gzip.NewWriter(w)
		_, _ = io.WriteString(writer, "event: token\ndata: synthetic\n\n")
		_ = writer.Flush()
		w.(stdhttp.Flusher).Flush()
		<-r.Context().Done() // Keep the encoded trailer/full response incomplete.
	}))
	defer server.Close()
	h := versionHTTP(t, map[string]any{"web_data_formatting_chain": []map[string]any{{"type": "xml_to_json"}}, protocol.CorrelationEvidenceCaptureConfigKey: true})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := h.Execute(ctx, &protocol.Action{Type: "GET", Target: server.URL, Params: map[string]any{"stop_on_event": "token"}, CaptureMessageDetail: true, Extractors: []protocol.Extractor{{Name: "sample", Source: "body", Type: "regex", Expression: "synthetic"}}})
	if err != nil || result.Success || result.ResponseBody != "" || len(result.Extractions) != 0 || result.Snapshot == nil || result.Snapshot.ResponseBody != "" || result.Snapshot.ResponseBodyState != "not_captured" {
		t.Fatal("failed authored formatting re-established partial sample body")
	}
	if result.ProtocolEvidence["response_content_state"] != "unavailable" || result.ProtocolEvidence["response_content_basis"] != nil || result.CorrelationEvidence == nil || result.CorrelationEvidence.UnavailableReason == "" {
		t.Fatal("failed sample formatting established content/correlation admission")
	}
}
