package http

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestPageDeadlineIncludesResources(t *testing.T) {
	var resourceStarted atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/asset" {
			resourceStarted.Store(true)
			select {
			case <-r.Context().Done():
			case <-time.After(time.Second):
			}
			return
		}
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, `<html><img src="/asset"></html>`)
	}))
	defer server.Close()
	h := initCompatibilityHTTP(t, map[string]any{"page_timeout_ms": 100, "request_timeout_ms": 2000, "http_download_non_html_resources": true})
	result, err := h.Execute(context.Background(), &protocol.Action{Type: "http_request", Method: "GET", Target: server.URL})
	if err != nil || result.Success || !strings.Contains(result.ErrorMessage, "page download deadline") || !resourceStarted.Load() || result.Duration > 800*time.Millisecond {
		t.Fatalf("result=%+v err=%v resource=%v", result, err, resourceStarted.Load())
	}
	// A caller's earlier cancellation must remain its own cause.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err = h.Execute(ctx, &protocol.Action{Type: "http_request", Method: "GET", Target: server.URL})
	if err != nil || result.Success || strings.Contains(result.ErrorMessage, "page download deadline") {
		t.Fatalf("caller cancellation=%+v %v", result, err)
	}
}

func TestMIMEPoliciesControlDiscoveryAndCache(t *testing.T) {
	for _, tc := range []struct {
		name, mode, content string
		excluded            []string
		want                int32
	}{
		{"native skips plain", "", "text/plain", nil, 0},
		{"text includes plain", "text", "text/plain; charset=utf-8", nil, 1},
		{"text skips xml", "text", "text/xml", nil, 0},
		{"any includes xml", "any", "text/xml", nil, 1},
		{"exclusion wins", "any", "text/plain", []string{"text/plain"}, 0},
		{"html skips xhtml", "html", "application/xhtml+xml", nil, 0},
		{"native includes xhtml", "", "application/xhtml+xml", nil, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var hits atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/asset" {
					hits.Add(1)
					return
				}
				w.Header().Set("Content-Type", tc.content)
				io.WriteString(w, `<img src="/asset">`)
			}))
			defer server.Close()
			h := initCompatibilityHTTP(t, map[string]any{"html_parse_mode": tc.mode, "html_excluded_mime_types": tc.excluded, "http_download_non_html_resources": true})
			result, err := h.Execute(context.Background(), &protocol.Action{Type: "http_request", Method: "GET", Target: server.URL})
			if err != nil || !result.Success || hits.Load() != tc.want {
				t.Fatalf("result=%+v err=%v hits=%d", result, err, hits.Load())
			}
		})
	}
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "max-age=3600")
		io.WriteString(w, "content")
	}))
	defer server.Close()
	h := initCompatibilityHTTP(t, map[string]any{"http_cache_enabled": true, "cache_excluded_mime_types": []any{"TEXT/PLAIN"}})
	for range 2 {
		r, e := h.Execute(context.Background(), &protocol.Action{Type: "http_request", Method: "GET", Target: server.URL})
		if e != nil || !r.Success {
			t.Fatalf("%+v %v", r, e)
		}
	}
	if hits.Load() != 2 {
		t.Fatal("excluded MIME was cached")
	}
}

func TestCacheIterationAgingAndRevalidationExclusion(t *testing.T) {
	for _, maxUnused := range []int{0, 1, 2} {
		t.Run(string(rune('0'+maxUnused)), func(t *testing.T) {
			h := initCompatibilityHTTP(t, map[string]any{"http_cache_enabled": true, "cache_max_unused_iterations": maxUnused})
			c := h.cache
			req, _ := http.NewRequest("GET", "http://example.test/a", nil)
			response := &http.Response{StatusCode: 200, Header: http.Header{"Cache-Control": {"max-age=3600"}, "Content-Type": {"text/plain"}}}
			h.BeginIteration(context.Background())
			c.store(req, response, []byte("a"), time.Now())
			for range 2 {
				h.BeginIteration(context.Background())
			}
			_, exists := c.entries[httpCacheKey(req)]
			if exists != (maxUnused != 1) {
				t.Fatalf("max=%d entries=%v", maxUnused, c.entries)
			}
			if !exists && c.totalBytes != 0 {
				t.Fatal("eviction did not release size")
			}
			if exists {
				c.lookup(req, time.Now())
				h.BeginIteration(context.Background())
				if len(c.entries) != 1 {
					t.Fatal("referenced entry evicted")
				}
			}
			canceled, cancel := context.WithCancel(context.Background())
			cancel()
			before := c.iteration
			if !errors.Is(h.BeginIteration(canceled), context.Canceled) || before != c.iteration {
				t.Fatal("canceled boundary aged cache")
			}
		})
	}
	h := initCompatibilityHTTP(t, map[string]any{"http_cache_enabled": true, "cache_excluded_mime_types": []string{"text/xml"}})
	req, _ := http.NewRequest("GET", "http://example.test/b", nil)
	h.cache.store(req, &http.Response{StatusCode: 200, Header: http.Header{"Cache-Control": {"max-age=3600"}, "Content-Type": {"text/plain"}}}, []byte("b"), time.Now())
	stale, _ := h.cache.lookup(req, time.Now())
	h.cache.refresh(req, stale, &http.Response{StatusCode: 304, Header: http.Header{"Content-Type": {"text/xml"}}}, time.Now())
	if len(h.cache.entries) != 0 || h.cache.totalBytes != 0 {
		t.Fatal("304 retained excluded MIME")
	}
	h.cache.store(req, &http.Response{StatusCode: 200, Header: http.Header{"Cache-Control": {"max-age=3600"}, "Content-Type": {"text/plain"}}}, []byte("old"), time.Now())
	h.cache.store(req, &http.Response{StatusCode: 200, Header: http.Header{"Cache-Control": {"max-age=3600"}, "Content-Type": {"text/xml"}}}, []byte("new"), time.Now())
	if len(h.cache.entries) != 0 || h.cache.totalBytes != 0 {
		t.Fatal("200 retained obsolete cached MIME")
	}

}

func TestIPFamilyDialing(t *testing.T) {
	for _, family := range []string{"auto", "ipv4", "ipv6"} {
		captured := ""
		base := func(ctx context.Context, network, address string) (net.Conn, error) {
			captured = network
			return nil, context.Canceled
		}
		_, err := familyDialContext(base, family)(context.Background(), "tcp", "localhost:80")
		want := map[string]string{"auto": "tcp", "ipv4": "tcp4", "ipv6": "tcp6"}[family]
		if !errors.Is(err, context.Canceled) || captured != want {
			t.Fatalf("%s: %s %v", family, captured, err)
		}
	}
	for _, family := range []string{"ipv4", "ipv6"} {
		t.Run(family, func(t *testing.T) {
			network, address := "tcp4", "127.0.0.1:0"
			if family == "ipv6" {
				network, address = "tcp6", "[::1]:0"
			}
			listener, err := net.Listen(network, address)
			if err != nil {
				t.Skipf("family unavailable: %v", err)
			}
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") }))
			server.Listener.Close()
			server.Listener = listener
			server.Start()
			defer server.Close()
			h := initCompatibilityHTTP(t, map[string]any{"ip_version": family, "proxy_mode": "direct"})
			result, err := h.Execute(context.Background(), &protocol.Action{Type: "http_request", Method: "GET", Target: server.URL})
			if err != nil || !result.Success {
				t.Fatalf("%+v %v", result, err)
			}
		})
	}
}

func TestReplayCompatibilityConfigValidation(t *testing.T) {
	for _, config := range []map[string]any{
		{"ip_version": "2"}, {"ip_version": 2}, {"html_parse_mode": "unknown"}, {"html_parse_mode": true},
		{"page_timeout_ms": -1}, {"page_timeout_ms": 3600001}, {"cache_max_unused_iterations": 1.5}, {"cache_max_unused_iterations": 1000001},
		{"cache_excluded_mime_types": []any{7}}, {"html_excluded_mime_types": []string{"text/*"}}, {"html_excluded_mime_types": []string{"text/html; charset=utf-8"}},
	} {
		if err := new(HTTP).Init(context.Background(), config); err == nil {
			t.Fatalf("accepted %+v", config)
		}
	}
	for _, value := range []any{int32(2), json.Number("2"), "2"} {
		parsed := initCompatibilityHTTP(t, map[string]any{"cache_max_unused_iterations": value})
		if parsed.cacheMaxUnusedIterations != 2 {
			t.Fatalf("validated count not applied: %#v", value)
		}
	}
	h := initCompatibilityHTTP(t, map[string]any{"page_timeout_ms": 3600000, "cache_max_unused_iterations": 1000000, "ip_version": "auto", "html_parse_mode": "any", "cache_excluded_mime_types": []string{"text/xml"}})
	if h.pageTimeout != time.Hour {
		t.Fatal(h.pageTimeout)
	}
}

func initCompatibilityHTTP(t *testing.T, config map[string]any) *HTTP {
	t.Helper()
	h := initHTTP(t, config)
	t.Cleanup(func() { h.Teardown(context.Background()) })
	return h
}

func TestPageDeadlineIsIndependentOfRequestTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(100 * time.Millisecond):
			}
		}
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, "ok")
	}))
	defer server.Close()
	// The ordinary request timeout still wins when shorter than the page deadline.
	h := initCompatibilityHTTP(t, map[string]any{"page_timeout_ms": 1000, "request_timeout_ms": 20})
	result, err := h.Execute(context.Background(), &protocol.Action{Type: "http_request", Method: "GET", Target: server.URL + "/slow"})
	if err != nil || result.Success || strings.Contains(result.ErrorMessage, "page download deadline") {
		t.Fatalf("%+v %v", result, err)
	}
	// Each action gets a fresh page deadline; a prior failure does not poison the instance.
	result, err = h.Execute(context.Background(), &protocol.Action{Type: "http_request", Method: "GET", Target: server.URL})
	if err != nil || !result.Success {
		t.Fatalf("%+v %v", result, err)
	}
}

func TestRevalidatedMIMEControlsCurrentDiscovery(t *testing.T) {
	var assets atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/asset" {
			assets.Add(1)
			return
		}
		w.Header().Set("Cache-Control", "max-age=3600")
		w.Header().Set("ETag", `"v1"`)
		if r.Header.Get("If-None-Match") != "" {
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, `<img src="/asset">`)
	}))
	defer server.Close()
	h := initCompatibilityHTTP(t, map[string]any{"http_cache_enabled": true, "http_cache_check_newer_versions": true, "http_download_non_html_resources": true, "html_parse_mode": "any", "html_excluded_mime_types": []string{"text/plain"}})
	// Go's test server suppresses Content-Type on 304. Supply the changed
	// representation metadata at the transport boundary to exercise its merge.
	baseTransport := h.client.Transport
	h.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		resp, err := baseTransport.RoundTrip(req)
		if err == nil && resp.StatusCode == http.StatusNotModified {
			resp.Header.Set("Content-Type", "text/plain")
		}
		return resp, err
	})

	for range 2 {
		result, err := h.Execute(context.Background(), &protocol.Action{Type: "http_request", Method: "GET", Target: server.URL})
		if err != nil || !result.Success {
			t.Fatalf("%+v %v", result, err)
		}
	}
	if assets.Load() != 1 {
		t.Fatalf("304 MIME exclusion was ignored: %d resource requests", assets.Load())
	}
}
