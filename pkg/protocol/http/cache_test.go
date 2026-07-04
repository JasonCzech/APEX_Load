package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestParseCacheControl(t *testing.T) {
	got := parseCacheControl(`max-age=60, public, s-maxage="120", stale-while-revalidate=30`)

	if got["max-age"] != "60" {
		t.Fatalf("max-age = %q, want 60", got["max-age"])
	}
	if _, ok := got["public"]; !ok {
		t.Fatalf("public directive missing: %#v", got)
	}
	if got["s-maxage"] != "120" {
		t.Fatalf("s-maxage = %q, want 120", got["s-maxage"])
	}
	if got["stale-while-revalidate"] != "30" {
		t.Fatalf("stale-while-revalidate = %q, want 30", got["stale-while-revalidate"])
	}
}

func TestCacheEntryFromResponse(t *testing.T) {
	now := time.Now()
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Cache-Control": []string{"max-age=60"},
			"Etag":          []string{`"v1"`},
		},
	}

	entry, ok := cacheEntryFromResponse(resp, []byte("payload"), now)
	if !ok {
		t.Fatal("cacheEntryFromResponse returned ok=false, want true")
	}
	if entry.statusCode != http.StatusOK || entry.etag != `"v1"` || string(entry.body) != "payload" {
		t.Fatalf("entry = %#v, want status 200, etag v1, payload body", entry)
	}
	if !entry.expiresAt.Equal(now.Add(60 * time.Second)) {
		t.Fatalf("expiresAt = %v, want %v", entry.expiresAt, now.Add(60*time.Second))
	}
}

func TestCacheEntryFromResponseRejectsNoStore(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Cache-Control": []string{"no-store, max-age=60"}},
	}

	if _, ok := cacheEntryFromResponse(resp, []byte("payload"), time.Now()); ok {
		t.Fatal("cacheEntryFromResponse returned ok=true for no-store response")
	}
}

func TestHTTPExecuteCacheFreshHitSkipsNetwork(t *testing.T) {
	var hits int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit := atomic.AddInt32(&hits, 1)
		w.Header().Set("Cache-Control", "max-age=60")
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("version-" + strconv.Itoa(int(hit))))
	}))
	defer ts.Close()

	h := initHTTP(t, map[string]any{"base_url": ts.URL, "http_cache_enabled": true})
	first := executeCacheAction(t, h)
	second := executeCacheAction(t, h)

	if atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("server hits = %d, want 1", hits)
	}
	if first.ResponseBody != "version-1" || second.ResponseBody != "version-1" {
		t.Fatalf("responses = %q then %q, want cached version-1 twice", first.ResponseBody, second.ResponseBody)
	}
}

func TestHTTPExecuteCacheDisabledByDefault(t *testing.T) {
	var hits int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit := atomic.AddInt32(&hits, 1)
		w.Header().Set("Cache-Control", "max-age=60")
		_, _ = w.Write([]byte("version-" + strconv.Itoa(int(hit))))
	}))
	defer ts.Close()

	h := initHTTP(t, map[string]any{"base_url": ts.URL})
	first := executeCacheAction(t, h)
	second := executeCacheAction(t, h)

	if atomic.LoadInt32(&hits) != 2 {
		t.Fatalf("server hits = %d, want 2", hits)
	}
	if first.ResponseBody != "version-1" || second.ResponseBody != "version-2" {
		t.Fatalf("responses = %q then %q, want uncached versions", first.ResponseBody, second.ResponseBody)
	}
}

func TestHTTPExecuteCacheRevalidatesWithETag(t *testing.T) {
	var hits int32
	var sawIfNoneMatch atomic.Bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit := atomic.AddInt32(&hits, 1)
		if hit == 1 {
			w.Header().Set("Cache-Control", "max-age=0")
			w.Header().Set("ETag", `"v1"`)
			_, _ = w.Write([]byte("cached"))
			return
		}
		if r.Header.Get("If-None-Match") == `"v1"` {
			sawIfNoneMatch.Store(true)
		}
		w.WriteHeader(http.StatusNotModified)
	}))
	defer ts.Close()

	h := initHTTP(t, map[string]any{"base_url": ts.URL, "http_cache_enabled": true})
	first := executeCacheAction(t, h)
	second := executeCacheAction(t, h)

	if atomic.LoadInt32(&hits) != 2 {
		t.Fatalf("server hits = %d, want initial request plus revalidation", hits)
	}
	if !sawIfNoneMatch.Load() {
		t.Fatal("server did not receive If-None-Match revalidation header")
	}
	if first.ResponseBody != "cached" || second.ResponseBody != "cached" {
		t.Fatalf("responses = %q then %q, want cached body after 304", first.ResponseBody, second.ResponseBody)
	}
	if second.StatusCode != http.StatusOK {
		t.Fatalf("second status = %d, want synthetic 200 from cache", second.StatusCode)
	}
}

func TestHTTPExecuteCacheHonorsNoStore(t *testing.T) {
	var hits int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit := atomic.AddInt32(&hits, 1)
		w.Header().Set("Cache-Control", "no-store, max-age=60")
		_, _ = w.Write([]byte("version-" + strconv.Itoa(int(hit))))
	}))
	defer ts.Close()

	h := initHTTP(t, map[string]any{"base_url": ts.URL, "http_cache_enabled": true})
	first := executeCacheAction(t, h)
	second := executeCacheAction(t, h)

	if atomic.LoadInt32(&hits) != 2 {
		t.Fatalf("server hits = %d, want no-store response to bypass cache", hits)
	}
	if first.ResponseBody != "version-1" || second.ResponseBody != "version-2" {
		t.Fatalf("responses = %q then %q, want uncached no-store versions", first.ResponseBody, second.ResponseBody)
	}
}

func executeCacheAction(t *testing.T, h *HTTP) *protocol.Result {
	t.Helper()
	result, err := h.Execute(context.Background(), &protocol.Action{
		Name:   "cache-get",
		Method: "GET",
		Target: "/resource",
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute failed: status=%d error=%s", result.StatusCode, result.ErrorMessage)
	}
	return result
}
