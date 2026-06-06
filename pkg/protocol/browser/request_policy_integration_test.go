package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestBrowserProtocolCacheDisabledReloadsCacheableAssets(t *testing.T) {
	var assetHits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/asset.js":
			atomic.AddInt32(&assetHits, 1)
			w.Header().Set("Cache-Control", "public, max-age=3600")
			w.Header().Set("Content-Type", "application/javascript")
			_, _ = w.Write([]byte(`window.__apexloadAssetLoaded = (window.__apexloadAssetLoaded || 0) + 1;`))
		default:
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<!doctype html>
<html>
  <head>
    <title>Browser Cache Test</title>
    <script src="/asset.js"></script>
  </head>
  <body>
    <div id="status">cache test</div>
  </body>
</html>`))
		}
	}))
	defer server.Close()

	b := &Browser{}
	if err := b.Init(context.Background(), map[string]any{
		"base_url":              server.URL,
		"browser_headless":      true,
		"browser_cache_enabled": false,
	}); err != nil {
		lower := strings.ToLower(err.Error())
		if strings.Contains(lower, "executable") || strings.Contains(lower, "chrome") {
			t.Skipf("browser runtime unavailable: %v", err)
		}
		t.Fatalf("Init returned error: %v", err)
	}
	defer b.Teardown(context.Background())

	for _, target := range []string{"/?view=one", "/?view=two"} {
		result, err := b.Execute(context.Background(), &protocol.Action{
			Name:   "Navigate " + target,
			Type:   "browser_navigate",
			Target: target,
		})
		if err != nil {
			t.Fatalf("navigate %s returned error: %v", target, err)
		}
		if !result.Success {
			t.Fatalf("navigate %s failed: %s", target, result.ErrorMessage)
		}
	}

	if got := atomic.LoadInt32(&assetHits); got < 2 {
		t.Fatalf("asset hits = %d, want at least 2 when cache is disabled", got)
	}
}

func TestBrowserProtocolDownloadResourcesNoneBlocksStaticAssets(t *testing.T) {
	var imageHits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/pixel.png":
			atomic.AddInt32(&imageHits, 1)
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("not-a-real-png"))
		default:
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<!doctype html>
<html>
  <head><title>Browser Download Filter Test</title></head>
  <body>
    <img src="/pixel.png" alt="pixel" />
    <div id="status">resource gate</div>
  </body>
</html>`))
		}
	}))
	defer server.Close()

	b := &Browser{}
	if err := b.Init(context.Background(), map[string]any{
		"base_url":                   server.URL,
		"browser_headless":           true,
		"browser_download_resources": "none",
	}); err != nil {
		lower := strings.ToLower(err.Error())
		if strings.Contains(lower, "executable") || strings.Contains(lower, "chrome") {
			t.Skipf("browser runtime unavailable: %v", err)
		}
		t.Fatalf("Init returned error: %v", err)
	}
	defer b.Teardown(context.Background())

	result, err := b.Execute(context.Background(), &protocol.Action{
		Name:   "Navigate with blocked assets",
		Type:   "browser_navigate",
		Target: "/",
	})
	if err != nil {
		t.Fatalf("navigate returned error: %v", err)
	}
	if !result.Success {
		t.Fatalf("navigate failed: %s", result.ErrorMessage)
	}
	if got := atomic.LoadInt32(&imageHits); got != 0 {
		t.Fatalf("image hits = %d, want 0 when download_resources is none", got)
	}
}

func TestBrowserProtocolDownloadResourcesRecordedRequiresMetadata(t *testing.T) {
	b := &Browser{}
	err := b.Init(context.Background(), map[string]any{
		"base_url":                   "https://example.test",
		"browser_headless":           true,
		"browser_download_resources": "recorded",
	})
	if err == nil {
		t.Fatal("Init returned nil error, want recorded-resource metadata guard")
	}
	if !strings.Contains(err.Error(), "recorded resource metadata") {
		t.Fatalf("Init error = %q, want recorded resource metadata", err.Error())
	}
}

func TestBrowserProtocolCheckNewerVersionsRevalidatesCachedAssets(t *testing.T) {
	var assetHits int32
	var revalidationHits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/asset.js":
			atomic.AddInt32(&assetHits, 1)
			w.Header().Set("ETag", `"asset-v1"`)
			w.Header().Set("Cache-Control", "public, max-age=3600")
			w.Header().Set("Content-Type", "application/javascript")
			if r.Header.Get("If-None-Match") == `"asset-v1"` {
				atomic.AddInt32(&revalidationHits, 1)
				w.WriteHeader(http.StatusNotModified)
				return
			}
			_, _ = w.Write([]byte(`window.__apexloadRevalidated = (window.__apexloadRevalidated || 0) + 1;`))
		default:
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<!doctype html>
<html>
  <head>
    <title>Browser Revalidation Test</title>
    <script src="/asset.js"></script>
  </head>
  <body>
    <div id="status">revalidation test</div>
  </body>
</html>`))
		}
	}))
	defer server.Close()

	b := &Browser{}
	if err := b.Init(context.Background(), map[string]any{
		"base_url":                     server.URL,
		"browser_headless":             true,
		"browser_cache_enabled":        true,
		"browser_check_newer_versions": true,
	}); err != nil {
		lower := strings.ToLower(err.Error())
		if strings.Contains(lower, "executable") || strings.Contains(lower, "chrome") {
			t.Skipf("browser runtime unavailable: %v", err)
		}
		t.Fatalf("Init returned error: %v", err)
	}
	defer b.Teardown(context.Background())

	for _, target := range []string{"/?view=one", "/?view=two"} {
		result, err := b.Execute(context.Background(), &protocol.Action{
			Name:   "Navigate " + target,
			Type:   "browser_navigate",
			Target: target,
		})
		if err != nil {
			t.Fatalf("navigate %s returned error: %v", target, err)
		}
		if !result.Success {
			t.Fatalf("navigate %s failed: %s", target, result.ErrorMessage)
		}
	}

	if got := atomic.LoadInt32(&assetHits); got < 2 {
		t.Fatalf("asset hits = %d, want at least 2 with revalidation enabled", got)
	}
	if got := atomic.LoadInt32(&revalidationHits); got < 1 {
		t.Fatalf("revalidation hits = %d, want at least 1 conditional asset request", got)
	}
}

func TestBrowserProtocolNetworkSimulationAddsLatency(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html>
<html>
  <head><title>Browser Network Simulation Test</title></head>
  <body>
    <div id="status">network simulation</div>
  </body>
</html>`))
	}))
	defer server.Close()

	b := &Browser{}
	if err := b.Init(context.Background(), map[string]any{
		"base_url":                          server.URL,
		"browser_headless":                  true,
		"web_network_simulation_mode":       "custom",
		"web_network_simulation_latency_ms": 250,
	}); err != nil {
		lower := strings.ToLower(err.Error())
		if strings.Contains(lower, "executable") || strings.Contains(lower, "chrome") {
			t.Skipf("browser runtime unavailable: %v", err)
		}
		t.Fatalf("Init returned error: %v", err)
	}
	defer b.Teardown(context.Background())

	startedAt := time.Now()
	result, err := b.Execute(context.Background(), &protocol.Action{
		Name:   "Navigate with simulated latency",
		Type:   "browser_navigate",
		Target: "/",
	})
	if err != nil {
		t.Fatalf("navigate returned error: %v", err)
	}
	if !result.Success {
		t.Fatalf("navigate failed: %s", result.ErrorMessage)
	}
	if elapsed := time.Since(startedAt); elapsed < 150*time.Millisecond {
		t.Fatalf("navigation elapsed = %v, want at least 150ms with simulated latency", elapsed)
	}
	if len(result.NetworkEntries) == 0 {
		t.Fatal("navigate did not capture network activity")
	}
}
