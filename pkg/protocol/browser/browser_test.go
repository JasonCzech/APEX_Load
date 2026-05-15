package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/jasonczech/stormrunner/pkg/protocol"
)

func TestBrowserProtocolRegistered(t *testing.T) {
	p, err := protocol.Get("browser")
	if err != nil {
		t.Fatalf("Get(browser) returned error: %v", err)
	}
	if p.Name() != "browser" {
		t.Fatalf("Name() = %q, want browser", p.Name())
	}
}

func TestBrowserConfigIntAcceptsEngineNumericTypes(t *testing.T) {
	config := map[string]any{
		"int":     12,
		"int64":   int64(34),
		"float64": float64(56),
		"float32": float32(78),
		"string":  "90",
	}

	tests := map[string]int{
		"int":     12,
		"int64":   34,
		"float64": 56,
		"float32": 78,
		"string":  0,
		"missing": 0,
	}
	for key, want := range tests {
		if got := configInt(config, key); got != want {
			t.Fatalf("configInt(%q) = %d, want %d", key, got, want)
		}
	}
}

func TestBrowserExecuteNilActionReturnsFailureResult(t *testing.T) {
	b := &Browser{}
	result, err := b.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result == nil {
		t.Fatal("result = nil")
	}
	if result.Success {
		t.Fatal("Success = true, want false")
	}
	if result.ErrorMessage != "browser action is required" {
		t.Fatalf("ErrorMessage = %q, want browser action is required", result.ErrorMessage)
	}
}

func TestBrowserProtocolExecuteBrowserActions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html>
<html>
  <head>
    <title>Browser Test</title>
    <script>
      function submitForm() {
        const value = document.querySelector('#name').value;
        document.querySelector('#status').textContent = 'Saved ' + value;
        console.log('saved', value);
      }
      window.addEventListener('load', () => console.log('page loaded'));
    </script>
  </head>
  <body>
    <label for="name">Name</label>
    <input id="name" />
    <button id="submit" onclick="submitForm()">Save</button>
    <div id="status">Idle</div>
  </body>
</html>`))
	}))
	defer server.Close()

	b := &Browser{}
	if err := b.Init(context.Background(), map[string]any{
		"base_url":                    server.URL,
		"browser_headless":            true,
		"browser_capture_screenshots": "per_action",
		"browser_viewport_width":      1280,
		"browser_viewport_height":     720,
	}); err != nil {
		lower := strings.ToLower(err.Error())
		if strings.Contains(lower, "executable") || strings.Contains(lower, "chrome") {
			t.Skipf("browser runtime unavailable: %v", err)
		}
		t.Fatalf("Init returned error: %v", err)
	}
	defer b.Teardown(context.Background())

	navigate, err := b.Execute(context.Background(), &protocol.Action{
		Name:   "Open homepage",
		Type:   "browser_navigate",
		Target: "/",
	})
	if err != nil {
		t.Fatalf("navigate returned error: %v", err)
	}
	if !navigate.Success {
		t.Fatalf("navigate failed: %s", navigate.ErrorMessage)
	}
	if navigate.PageTitle != "Browser Test" {
		t.Fatalf("PageTitle = %q, want Browser Test", navigate.PageTitle)
	}
	if navigate.ScreenshotBase64 == "" {
		t.Fatal("navigate did not capture per-action screenshot")
	}
	if len(navigate.NetworkEntries) == 0 {
		t.Fatal("navigate did not capture network activity")
	}

	fill, err := b.Execute(context.Background(), &protocol.Action{
		Name:     "Fill name",
		Type:     "browser_fill",
		Selector: "#name",
		Value:    "stormrunner",
	})
	if err != nil {
		t.Fatalf("fill returned error: %v", err)
	}
	if !fill.Success {
		t.Fatalf("fill failed: %s", fill.ErrorMessage)
	}

	click, err := b.Execute(context.Background(), &protocol.Action{
		Name:     "Click save",
		Type:     "browser_click",
		Selector: "#submit",
	})
	if err != nil {
		t.Fatalf("click returned error: %v", err)
	}
	if !click.Success {
		t.Fatalf("click failed: %s", click.ErrorMessage)
	}
	if len(click.ConsoleEntries) == 0 {
		t.Fatal("click did not capture console activity")
	}

	wait, err := b.Execute(context.Background(), &protocol.Action{
		Name: "Wait for saved state",
		Type: "browser_wait",
		WaitFor: &protocol.WaitCondition{
			Type:      "text",
			Value:     "Saved stormrunner",
			TimeoutMs: 3000,
		},
	})
	if err != nil {
		t.Fatalf("wait returned error: %v", err)
	}
	if !wait.Success {
		t.Fatalf("wait failed: %s", wait.ErrorMessage)
	}

	screenshot, err := b.Execute(context.Background(), &protocol.Action{
		Name: "Capture page",
		Type: "browser_screenshot",
		Capture: &protocol.BrowserCaptureOptions{
			Screenshot: true,
			HTML:       true,
		},
	})
	if err != nil {
		t.Fatalf("screenshot returned error: %v", err)
	}
	if !screenshot.Success {
		t.Fatalf("screenshot failed: %s", screenshot.ErrorMessage)
	}
	if screenshot.ScreenshotBase64 == "" {
		t.Fatal("ScreenshotBase64 = empty, want captured screenshot")
	}
	if !strings.Contains(screenshot.HTML, "Saved stormrunner") {
		t.Fatalf("HTML did not contain saved marker: %q", screenshot.HTML)
	}
	if screenshot.DOMSummary == nil || screenshot.DOMSummary.Title != "Browser Test" {
		t.Fatalf("DOMSummary = %#v, want title Browser Test", screenshot.DOMSummary)
	}
}

func TestBrowserProtocolCacheDisabledReloadsCacheableAssets(t *testing.T) {
	var assetHits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/asset.js":
			atomic.AddInt32(&assetHits, 1)
			w.Header().Set("Cache-Control", "public, max-age=3600")
			w.Header().Set("Content-Type", "application/javascript")
			_, _ = w.Write([]byte(`window.__stormrunnerAssetLoaded = (window.__stormrunnerAssetLoaded || 0) + 1;`))
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
		"base_url":                  server.URL,
		"browser_headless":          true,
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

func TestNormalizeDownloadFilterHosts(t *testing.T) {
	hosts := normalizeDownloadFilterHosts([]string{
		" https://cdn.example.com/ ",
		"http://cdn.example.com",
		"assets.example.com:8443/",
		"",
		"assets.example.com:8443",
	})

	want := []string{"cdn.example.com", "assets.example.com:8443"}
	if len(hosts) != len(want) {
		t.Fatalf("normalizeDownloadFilterHosts length = %d, want %d (%v)", len(hosts), len(want), hosts)
	}
	for idx, expected := range want {
		if hosts[idx] != expected {
			t.Fatalf("normalizeDownloadFilterHosts[%d] = %q, want %q", idx, hosts[idx], expected)
		}
	}
}

func TestBrowserFetchRequestPatterns(t *testing.T) {
	tests := []struct {
		name  string
		b     Browser
		want  []network.ResourceType
	}{
		{
			name: "download filters only",
			b: Browser{downloadFilterMode: "block_list", downloadFilterHosts: []string{"ads.example.com"}, cacheContentURLs: true},
			want: []network.ResourceType{
				network.ResourceTypeImage,
				network.ResourceTypeStylesheet,
				network.ResourceTypeScript,
				network.ResourceTypeFont,
				network.ResourceTypeMedia,
				network.ResourceTypeOther,
			},
		},
		{
			name: "content cache override only",
			b: Browser{cacheContentURLs: false},
			want: []network.ResourceType{
				network.ResourceTypeDocument,
				network.ResourceTypeFetch,
				network.ResourceTypeXHR,
			},
		},
		{
			name: "combined patterns are unique",
			b: Browser{downloadFilterMode: "allow_list", downloadFilterHosts: []string{"cdn.example.com"}, cacheContentURLs: false},
			want: []network.ResourceType{
				network.ResourceTypeImage,
				network.ResourceTypeStylesheet,
				network.ResourceTypeScript,
				network.ResourceTypeFont,
				network.ResourceTypeMedia,
				network.ResourceTypeOther,
				network.ResourceTypeDocument,
				network.ResourceTypeFetch,
				network.ResourceTypeXHR,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patterns := tt.b.fetchRequestPatterns()
			if len(patterns) != len(tt.want) {
				t.Fatalf("len(patterns) = %d, want %d", len(patterns), len(tt.want))
			}
			for idx, expected := range tt.want {
				if patterns[idx].ResourceType != expected {
					t.Fatalf("patterns[%d] = %v, want %v", idx, patterns[idx].ResourceType, expected)
				}
			}
		})
	}
}

func TestContinuedRequestHeadersAddsNoCache(t *testing.T) {
	headers := continuedRequestHeaders(network.Headers{
		"Accept":        "application/json",
		"Cache-Control": "public, max-age=3600",
	})
	values := make(map[string]string, len(headers))
	for _, header := range headers {
		values[strings.ToLower(header.Name)] = header.Value
	}
	if values["accept"] != "application/json" {
		t.Fatalf("accept header = %q, want application/json", values["accept"])
	}
	if values["cache-control"] != "no-cache" {
		t.Fatalf("cache-control header = %q, want no-cache", values["cache-control"])
	}
	if values["pragma"] != "no-cache" {
		t.Fatalf("pragma header = %q, want no-cache", values["pragma"])
	}
}

func TestShouldRevalidateContentRequest(t *testing.T) {
	if !shouldRevalidateContentRequest(network.ResourceTypeDocument) {
		t.Fatal("document requests should be revalidated")
	}
	if !shouldRevalidateContentRequest(network.ResourceTypeXHR) {
		t.Fatal("xhr requests should be revalidated")
	}
	if shouldRevalidateContentRequest(network.ResourceTypeImage) {
		t.Fatal("image requests should not be revalidated by cache_content_urls")
	}
}

func TestNormalizeRecordedResources(t *testing.T) {
	resources := normalizeRecordedResources([]string{
		"https://example.com/static/logo.png",
		"/static/logo.png",
		"https://cdn.example.com/app/site.css?v=2",
	})
	if _, ok := resources["example.com/static/logo.png"]; !ok {
		t.Fatal("expected host-qualified recorded resource key")
	}
	if _, ok := resources["/static/logo.png"]; !ok {
		t.Fatal("expected path-only recorded resource key")
	}
	if _, ok := resources["cdn.example.com/app/site.css?v=2"]; !ok {
		t.Fatal("expected query-preserving recorded resource key")
	}
}

func TestBrowserShouldBlockUnrecordedRequest(t *testing.T) {
	browser := &Browser{
		downloadResources:          "recorded",
		recordedResourcesAvailable: true,
		recordedResources: normalizeRecordedResources([]string{
			"https://example.com/static/logo.png",
			"https://cdn.example.com/app/site.css?v=2",
		}),
	}
	if browser.shouldBlockUnrecordedRequest("https://example.com/static/logo.png") {
		t.Fatal("recorded asset should not be blocked")
	}
	if browser.shouldBlockUnrecordedRequest("https://cdn.example.com/app/site.css?v=2") {
		t.Fatal("recorded asset with query should not be blocked")
	}
	if !browser.shouldBlockUnrecordedRequest("https://example.com/static/app.js") {
		t.Fatal("unrecorded asset should be blocked")
	}
	empty := &Browser{downloadResources: "recorded", recordedResourcesAvailable: true}
	if !empty.shouldBlockUnrecordedRequest("https://example.com/static/app.js") {
		t.Fatal("recorded mode with empty metadata should block assets")
	}
	unavailable := &Browser{downloadResources: "recorded", recordedResourcesAvailable: false}
	if unavailable.shouldBlockUnrecordedRequest("https://example.com/static/app.js") {
		t.Fatal("missing recorded metadata should not silently classify in helper")
	}
}

func TestBrowserShouldBlockFilteredRequest(t *testing.T) {
	tests := []struct {
		name   string
		mode   string
		hosts  []string
		rawURL string
		want   bool
	}{
		{
			name:   "allow list keeps listed host",
			mode:   "allow_list",
			hosts:  []string{"cdn.example.com"},
			rawURL: "https://cdn.example.com/app.js",
			want:   false,
		},
		{
			name:   "allow list blocks unlisted host",
			mode:   "allow_list",
			hosts:  []string{"cdn.example.com"},
			rawURL: "https://ads.example.com/banner.js",
			want:   true,
		},
		{
			name:   "block list blocks listed host",
			mode:   "block_list",
			hosts:  []string{"ads.example.com"},
			rawURL: "https://ads.example.com/banner.js",
			want:   true,
		},
		{
			name:   "block list allows unlisted host",
			mode:   "block_list",
			hosts:  []string{"ads.example.com"},
			rawURL: "https://cdn.example.com/app.js",
			want:   false,
		},
		{
			name:   "host match accepts explicit port",
			mode:   "block_list",
			hosts:  []string{"assets.example.com:8443"},
			rawURL: "https://assets.example.com:8443/file.css",
			want:   true,
		},
		{
			name:   "invalid url does not block",
			mode:   "allow_list",
			hosts:  []string{"cdn.example.com"},
			rawURL: "://bad-url",
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			browser := &Browser{
				downloadFilterMode:  tt.mode,
				downloadFilterHosts: tt.hosts,
			}
			if got := browser.shouldBlockFilteredRequest(tt.rawURL); got != tt.want {
				t.Fatalf("shouldBlockFilteredRequest(%q) = %v, want %v", tt.rawURL, got, tt.want)
			}
		})
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
			_, _ = w.Write([]byte(`window.__stormrunnerRevalidated = (window.__stormrunnerRevalidated || 0) + 1;`))
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
		"base_url":                      server.URL,
		"browser_headless":              true,
		"browser_cache_enabled":         true,
		"browser_check_newer_versions":  true,
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
		"base_url":                           server.URL,
		"browser_headless":                   true,
		"web_network_simulation_mode":        "custom",
		"web_network_simulation_latency_ms":  250,
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
