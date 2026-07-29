package http

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func executeResourceAction(t *testing.T, h *HTTP, target string) *protocol.Result {
	t.Helper()
	result, err := h.Execute(context.Background(), &protocol.Action{
		Name:   "page",
		Type:   "http_request",
		Method: http.MethodGet,
		Target: target,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	return result
}

func evidenceInt(t *testing.T, result *protocol.Result, key string) int {
	t.Helper()
	raw, ok := result.ProtocolEvidence[key]
	if !ok {
		t.Fatalf("evidence key %q missing: %#v", key, result.ProtocolEvidence)
	}
	switch v := raw.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		t.Fatalf("evidence key %q has type %T", key, raw)
		return 0
	}
}

func TestExtractResourceRefsMatrix(t *testing.T) {
	base, _ := neturl.Parse("https://site.test/dir/page.html")
	doc := `<html><head>
		<link rel="stylesheet" href="/css/app.css">
		<link rel="icon" href="favicon.ico">
		<link rel="shortcut icon" href="/shortcut.ico">
		<link rel="prefetch" href="/next.html">
		<link rel="prerender" href="https://site.test/render.html">
		<link rel="preload" href="/font.woff2">
		<script src="app.js"></script>
	</head><body background="bg.png">
		<img src="a.png" srcset="b.png 1x, c.png 2x">
		<input type="image" src="submit.png">
		<input type="text" name="q">
		<video src="movie.mp4" poster="poster.jpg"></video>
		<audio src="sound.mp3"></audio>
		<source src="alt.mp4" srcset="alt2.mp4 1x">
		<embed src="widget.swf">
		<object data="applet.jar"></object>
		<iframe src="frame.html"></iframe>
		<img src="data:image/png;base64,xyz">
		<img src="javascript:alert(1)">
		<img src="#fragment">
	</body></html>`

	resources, prefetch := extractResourceRefs([]byte(doc), base)

	wantResources := map[string]string{
		"https://site.test/css/app.css":     "stylesheet",
		"https://site.test/dir/favicon.ico": "icon",
		"https://site.test/shortcut.ico":    "icon",
		"https://site.test/dir/app.js":      "script",
		"https://site.test/dir/bg.png":      "image",
		"https://site.test/dir/a.png":       "image",
		"https://site.test/dir/b.png":       "image",
		"https://site.test/dir/c.png":       "image",
		"https://site.test/dir/submit.png":  "image",
		"https://site.test/dir/movie.mp4":   "media",
		"https://site.test/dir/poster.jpg":  "image",
		"https://site.test/dir/sound.mp3":   "media",
		"https://site.test/dir/alt.mp4":     "media",
		"https://site.test/dir/alt2.mp4":    "media",
		"https://site.test/dir/widget.swf":  "other",
		"https://site.test/dir/applet.jar":  "other",
		"https://site.test/dir/frame.html":  "document",
	}
	got := map[string]string{}
	for _, ref := range resources {
		got[ref.url] = ref.resourceType
	}
	for url, resourceType := range wantResources {
		if got[url] != resourceType {
			t.Errorf("resource %s = %q, want %q", url, got[url], resourceType)
		}
	}
	if len(got) != len(wantResources) {
		t.Errorf("resource count = %d, want %d (%#v)", len(got), len(wantResources), got)
	}

	wantPrefetch := map[string]string{
		"https://site.test/next.html":   "prefetch",
		"https://site.test/render.html": "prerender",
		"https://site.test/font.woff2":  "preload",
	}
	gotPrefetch := map[string]string{}
	for _, ref := range prefetch {
		gotPrefetch[ref.url] = ref.resourceType
	}
	for url, resourceType := range wantPrefetch {
		if gotPrefetch[url] != resourceType {
			t.Errorf("prefetch %s = %q, want %q", url, gotPrefetch[url], resourceType)
		}
	}
	if len(gotPrefetch) != len(wantPrefetch) {
		t.Errorf("prefetch count = %d, want %d (%#v)", len(gotPrefetch), len(wantPrefetch), gotPrefetch)
	}
}

func TestExtractCSSResourceRefs(t *testing.T) {
	base, _ := neturl.Parse("https://site.test/css/app.css")
	css := `body { background: url("../img/bg.png"); }
	.a { background-image: url('sprite.png'); }
	.b { src: url(data:font/woff;base64,xx); }
	.c { background: url( /abs.png ); }`

	refs := extractCSSResourceRefs([]byte(css), base)
	got := map[string]bool{}
	for _, ref := range refs {
		got[ref.url] = true
	}
	for _, want := range []string{"https://site.test/img/bg.png", "https://site.test/css/sprite.png", "https://site.test/abs.png"} {
		if !got[want] {
			t.Errorf("missing css ref %s in %#v", want, got)
		}
	}
	if len(refs) != 3 {
		t.Errorf("css ref count = %d, want 3", len(refs))
	}
}

func TestHTTPExecuteDownloadsResourcesAndReports(t *testing.T) {
	var pageHits, imgHits, cssHits, spriteHits int32
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&pageHits, 1)
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><link rel="stylesheet" href="/app.css"></head><body><img src="/img.png"></body></html>`))
	})
	mux.HandleFunc("/img.png", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&imgHits, 1)
		if r.Header.Get("Referer") == "" {
			t.Error("resource request missing Referer header")
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("0123456789"))
	})
	mux.HandleFunc("/app.css", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&cssHits, 1)
		w.Header().Set("Content-Type", "text/css")
		_, _ = w.Write([]byte(`.x{background:url("/sprite.png")}`))
	})
	mux.HandleFunc("/sprite.png", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&spriteHits, 1)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("sprite"))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	h := initHTTP(t, map[string]any{"base_url": ts.URL, "http_download_non_html_resources": true})
	result := executeResourceAction(t, h, "/")

	if !result.Success {
		t.Fatalf("page failed: %s", result.ErrorMessage)
	}
	if atomic.LoadInt32(&imgHits) != 1 || atomic.LoadInt32(&cssHits) != 1 || atomic.LoadInt32(&spriteHits) != 1 {
		t.Fatalf("resource hits img=%d css=%d sprite=%d, want 1 each", imgHits, cssHits, spriteHits)
	}
	if evidenceInt(t, result, "resource_requests") != 3 {
		t.Fatalf("resource_requests = %v, want 3", result.ProtocolEvidence["resource_requests"])
	}
	if evidenceInt(t, result, "resource_failures") != 0 {
		t.Fatalf("resource_failures = %v, want 0", result.ProtocolEvidence["resource_failures"])
	}
	if len(result.NetworkEntries) != 3 {
		t.Fatalf("network entries = %d, want 3", len(result.NetworkEntries))
	}
	var resourceBytes int64
	for _, entry := range result.NetworkEntries {
		resourceBytes += entry.BytesReceived
	}
	if int64(evidenceInt(t, result, "resource_bytes")) != resourceBytes {
		t.Fatalf("resource_bytes = %v, want %d", result.ProtocolEvidence["resource_bytes"], resourceBytes)
	}
}

func TestHTTPExecuteResourcesUseResolvedRequestIdentityHeaders(t *testing.T) {
	var resourceUserAgent, resourceLanguage string
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><img src="/img.png"></body></html>`))
	})
	mux.HandleFunc("/img.png", func(w http.ResponseWriter, r *http.Request) {
		resourceUserAgent = r.Header.Get("User-Agent")
		resourceLanguage = r.Header.Get("Accept-Language")
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("image"))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	h := initHTTP(t, map[string]any{
		"base_url":                         ts.URL,
		"http_download_non_html_resources": true,
		"user_agent":                       "startup-agent",
	})
	result, err := h.Execute(context.Background(), &protocol.Action{
		Name:                   "resolved-resource-identity",
		Method:                 "GET",
		Target:                 "/",
		ResolvedDefaultHeaders: true,
		DefaultHeaderList: protocol.HeaderList{
			{Name: "User-Agent", Value: "resolved-agent"},
			{Name: "Accept-Language", Value: "fr-CA"},
		},
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !result.Success {
		t.Fatalf("result = %#v, want success", result)
	}
	if resourceUserAgent != "resolved-agent" || resourceLanguage != "fr-CA" {
		t.Fatalf("resource identity = %q / %q, want resolved-agent / fr-CA", resourceUserAgent, resourceLanguage)
	}
}

func TestHTTPExecuteCSSResolvesAgainstStylesheetURL(t *testing.T) {
	var bgHit, wrongHit int32
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><link rel="stylesheet" href="/css/app.css"></head><body></body></html>`))
	})
	mux.HandleFunc("/css/app.css", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css")
		// Relative url() must resolve against /css/, not the page root.
		_, _ = w.Write([]byte(`.x{background:url("img/bg.png")}`))
	})
	mux.HandleFunc("/css/img/bg.png", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&bgHit, 1)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("bg"))
	})
	mux.HandleFunc("/img/bg.png", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&wrongHit, 1)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	h := initHTTP(t, map[string]any{"base_url": ts.URL, "http_download_non_html_resources": true})
	executeResourceAction(t, h, "/")

	if atomic.LoadInt32(&bgHit) != 1 {
		t.Fatalf("stylesheet-relative bg hits = %d, want 1", bgHit)
	}
	if atomic.LoadInt32(&wrongHit) != 0 {
		t.Fatalf("page-relative bg hits = %d, want 0 (url() must resolve against the stylesheet)", wrongHit)
	}
}

func TestHTTPExecuteResourceHeadersOmitAuthAndDecodeGzipCSS(t *testing.T) {
	var leakedAuth int32
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><link rel="stylesheet" href="/g.css"></head><body><img src="/img.png"></body></html>`))
	})
	assertNoSecret := func(r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("X-Api-Key") != "" {
			atomic.AddInt32(&leakedAuth, 1)
		}
	}
	mux.HandleFunc("/img.png", func(w http.ResponseWriter, r *http.Request) {
		assertNoSecret(r)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("img"))
	})
	// Serve gzip-encoded CSS containing a url() ref; the fetcher must still parse it.
	mux.HandleFunc("/g.css", func(w http.ResponseWriter, r *http.Request) {
		assertNoSecret(r)
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		_, _ = gz.Write([]byte(`.x{background:url("/sprite.png")}`))
		_ = gz.Close()
		w.Header().Set("Content-Type", "text/css")
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(buf.Bytes())
	})
	var spriteHit int32
	mux.HandleFunc("/sprite.png", func(w http.ResponseWriter, r *http.Request) {
		assertNoSecret(r)
		atomic.AddInt32(&spriteHit, 1)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	h := initHTTP(t, map[string]any{
		"base_url":                         ts.URL,
		"http_download_non_html_resources": true,
		// Author headers that must NOT leak to resource hosts.
		"default_headers": map[string]string{"Authorization": "Bearer secret", "X-Api-Key": "k"},
		// Explicit accept_encoding must not blind the CSS parser.
		"accept_encoding": "gzip",
	})
	executeResourceAction(t, h, "/")

	if atomic.LoadInt32(&leakedAuth) != 0 {
		t.Fatalf("author auth/API-key headers leaked to %d resource request(s)", leakedAuth)
	}
	if atomic.LoadInt32(&spriteHit) != 1 {
		t.Fatalf("sprite hits = %d, want 1 (gzipped CSS must still be parsed)", spriteHit)
	}
}

func TestHTTPExecuteResourcesDisabledFetchesNothing(t *testing.T) {
	var resourceHits int32
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><img src="/img.png"></body></html>`))
	})
	mux.HandleFunc("/img.png", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&resourceHits, 1)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	h := initHTTP(t, map[string]any{"base_url": ts.URL})
	result := executeResourceAction(t, h, "/")

	if atomic.LoadInt32(&resourceHits) != 0 {
		t.Fatalf("resource hits = %d, want 0 without download flag", resourceHits)
	}
	if len(result.NetworkEntries) != 0 {
		t.Fatalf("network entries = %d, want 0", len(result.NetworkEntries))
	}
}

func TestHTTPExecuteResourceFailureDoesNotFailStep(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><img src="/missing.png"></body></html>`))
	})
	mux.HandleFunc("/missing.png", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	h := initHTTP(t, map[string]any{"base_url": ts.URL, "http_download_non_html_resources": true})
	result := executeResourceAction(t, h, "/")

	if !result.Success {
		t.Fatalf("step failed on resource error: %s", result.ErrorMessage)
	}
	if evidenceInt(t, result, "resource_failures") != 1 {
		t.Fatalf("resource_failures = %v, want 1", result.ProtocolEvidence["resource_failures"])
	}
}

func TestHTTPExecuteResourceConcurrencyBounded(t *testing.T) {
	var inFlight, maxInFlight int32
	var mu sync.Mutex
	mux := http.NewServeMux()
	var page strings.Builder
	page.WriteString("<html><body>")
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&page, `<img src="/img%d.png">`, i)
	}
	page.WriteString("</body></html>")
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(page.String()))
	})
	mux.HandleFunc("/img", func(w http.ResponseWriter, r *http.Request) {})
	for i := 0; i < 20; i++ {
		mux.HandleFunc("/img"+strconv.Itoa(i)+".png", func(w http.ResponseWriter, r *http.Request) {
			current := atomic.AddInt32(&inFlight, 1)
			mu.Lock()
			if current > maxInFlight {
				maxInFlight = current
			}
			mu.Unlock()
			defer atomic.AddInt32(&inFlight, -1)
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("x"))
		})
	}
	ts := httptest.NewServer(mux)
	defer ts.Close()

	h := initHTTP(t, map[string]any{"base_url": ts.URL, "http_download_non_html_resources": true})
	result := executeResourceAction(t, h, "/")

	if got := evidenceInt(t, result, "resource_requests"); got != 20 {
		t.Fatalf("resource_requests = %d, want 20", got)
	}
	mu.Lock()
	peak := maxInFlight
	mu.Unlock()
	if peak > maxResourcePerHostConcurrency {
		t.Fatalf("peak in-flight = %d, want <= %d (per-host bound)", peak, maxResourcePerHostConcurrency)
	}
}

func TestHTTPExecuteResourceCapPerPage(t *testing.T) {
	var resourceHits int32
	mux := http.NewServeMux()
	var page strings.Builder
	page.WriteString("<html><body>")
	for i := 0; i < maxPageResources+20; i++ {
		fmt.Fprintf(&page, `<img src="/r%d.png">`, i)
	}
	page.WriteString("</body></html>")
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(page.String()))
	})
	for i := 0; i < maxPageResources+20; i++ {
		mux.HandleFunc("/r"+strconv.Itoa(i)+".png", func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&resourceHits, 1)
		})
	}
	ts := httptest.NewServer(mux)
	defer ts.Close()

	h := initHTTP(t, map[string]any{"base_url": ts.URL, "http_download_non_html_resources": true})
	result := executeResourceAction(t, h, "/")

	if got := evidenceInt(t, result, "resource_requests"); got != maxPageResources {
		t.Fatalf("resource_requests = %d, want cap %d", got, maxPageResources)
	}
	if hits := atomic.LoadInt32(&resourceHits); int(hits) != maxPageResources {
		t.Fatalf("server hits = %d, want %d", hits, maxPageResources)
	}
}

func TestHTTPExecuteResourcesFeedCacheAcrossPages(t *testing.T) {
	var imgHits int32
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><img src="/img.png"></body></html>`))
	})
	mux.HandleFunc("/img.png", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&imgHits, 1)
		w.Header().Set("Cache-Control", "max-age=300")
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("img-bytes"))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	h := initHTTP(t, map[string]any{
		"base_url":                         ts.URL,
		"http_cache_enabled":               true,
		"http_download_non_html_resources": true,
	})
	first := executeResourceAction(t, h, "/")
	second := executeResourceAction(t, h, "/")

	if atomic.LoadInt32(&imgHits) != 1 {
		t.Fatalf("img hits = %d, want 1 (second page served from cache)", imgHits)
	}
	if evidenceInt(t, first, "resource_cache_hits") != 0 {
		t.Fatalf("first page cache hits = %v, want 0", first.ProtocolEvidence["resource_cache_hits"])
	}
	if evidenceInt(t, second, "resource_cache_hits") != 1 {
		t.Fatalf("second page cache hits = %v, want 1", second.ProtocolEvidence["resource_cache_hits"])
	}
	foundCacheMarker := false
	for _, entry := range second.NetworkEntries {
		if strings.HasSuffix(entry.ResourceType, "(cache)") {
			foundCacheMarker = true
		}
	}
	if !foundCacheMarker {
		t.Fatalf("no (cache) resource marker in %#v", second.NetworkEntries)
	}
}

func TestHTTPExecuteResourceDownloadFilters(t *testing.T) {
	var allowedHits, blockedHits int32
	blocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&blockedHits, 1)
	}))
	defer blocked.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><img src="/ok.png"><img src="` + blocked.URL + `/other.png"></body></html>`))
	})
	mux.HandleFunc("/ok.png", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&allowedHits, 1)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	tsHost := strings.TrimPrefix(ts.URL, "http://")
	h := initHTTP(t, map[string]any{
		"base_url":                         ts.URL,
		"http_download_non_html_resources": true,
		"web_download_filters_mode":        "allow_list",
		"web_download_filters_hosts":       []string{tsHost},
	})
	result := executeResourceAction(t, h, "/")

	if atomic.LoadInt32(&allowedHits) != 1 {
		t.Fatalf("allowed hits = %d, want 1", allowedHits)
	}
	if atomic.LoadInt32(&blockedHits) != 0 {
		t.Fatalf("blocked host hits = %d, want 0", blockedHits)
	}
	if got := evidenceInt(t, result, "resource_requests"); got != 1 {
		t.Fatalf("resource_requests = %d, want 1 (filtered)", got)
	}
}

func TestHTTPExecuteResourceRedirectToFilteredHostBlocked(t *testing.T) {
	var blockedHit int32
	blocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&blockedHit, 1)
	}))
	defer blocked.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><img src="/redir.png"></body></html>`))
	})
	mux.HandleFunc("/redir.png", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, blocked.URL+"/asset.png", http.StatusFound)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	tsHost := strings.TrimPrefix(ts.URL, "http://")
	h := initHTTP(t, map[string]any{
		"base_url":                         ts.URL,
		"http_download_non_html_resources": true,
		"web_download_filters_mode":        "allow_list",
		"web_download_filters_hosts":       []string{tsHost},
	})
	result := executeResourceAction(t, h, "/")

	if atomic.LoadInt32(&blockedHit) != 0 {
		t.Fatalf("redirect target on filtered host was reached %d time(s), want 0", blockedHit)
	}
	// The redirect chain terminates as a failed resource, not a step failure.
	if !result.Success {
		t.Fatalf("step failed on blocked resource redirect: %s", result.ErrorMessage)
	}
	if evidenceInt(t, result, "resource_failures") != 1 {
		t.Fatalf("resource_failures = %v, want 1", result.ProtocolEvidence["resource_failures"])
	}
}

func TestHTTPExecutePrefetchWave(t *testing.T) {
	var prefetchHits, resourceHits int32
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><link rel="prefetch" href="/next.html"><link rel="prerender" href="/render.html"></head><body><img src="/img.png"></body></html>`))
	})
	mux.HandleFunc("/next.html", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&prefetchHits, 1)
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><img src="/never.png"></body></html>`))
	})
	mux.HandleFunc("/render.html", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&prefetchHits, 1)
	})
	mux.HandleFunc("/img.png", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&resourceHits, 1)
	})
	mux.HandleFunc("/never.png", func(w http.ResponseWriter, r *http.Request) {
		t.Error("prerendered document must not be parsed for resources (depth 1)")
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Prefetch alone (download disabled): only the link-rel hints are fetched.
	h := initHTTP(t, map[string]any{"base_url": ts.URL, "http_simulate_prefetch": true})
	result := executeResourceAction(t, h, "/")

	if atomic.LoadInt32(&prefetchHits) != 2 {
		t.Fatalf("prefetch hits = %d, want 2", prefetchHits)
	}
	if atomic.LoadInt32(&resourceHits) != 0 {
		t.Fatalf("resource hits = %d, want 0 with download disabled", resourceHits)
	}
	if got := evidenceInt(t, result, "resource_requests"); got != 2 {
		t.Fatalf("resource_requests = %d, want 2", got)
	}
}

func TestHTTPExecuteCacheContentURLsOnly(t *testing.T) {
	var htmlHits, imgHits int32
	mux := http.NewServeMux()
	mux.HandleFunc("/page.html", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&htmlHits, 1)
		w.Header().Set("Cache-Control", "max-age=300")
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>page</html>"))
	})
	mux.HandleFunc("/img.png", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&imgHits, 1)
		w.Header().Set("Cache-Control", "max-age=300")
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("img"))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	h := initHTTP(t, map[string]any{
		"base_url":                     ts.URL,
		"http_cache_enabled":           true,
		"http_cache_content_urls_only": true,
	})
	for i := 0; i < 2; i++ {
		executeResourceAction(t, h, "/page.html")
		executeResourceAction(t, h, "/img.png")
	}

	if atomic.LoadInt32(&htmlHits) != 1 {
		t.Fatalf("html hits = %d, want 1 (cached)", htmlHits)
	}
	if atomic.LoadInt32(&imgHits) != 2 {
		t.Fatalf("img hits = %d, want 2 (content-only mode must not cache images)", imgHits)
	}
}

func TestHTTPExecuteCheckNewerVersionsForcesRevalidation(t *testing.T) {
	var hits int32
	var conditionalSeen atomic.Bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit := atomic.AddInt32(&hits, 1)
		if r.Header.Get("If-None-Match") == `"v1"` {
			conditionalSeen.Store(true)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Cache-Control", "max-age=300")
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("body-" + strconv.Itoa(int(hit))))
	}))
	defer ts.Close()

	h := initHTTP(t, map[string]any{
		"base_url":                        ts.URL,
		"http_cache_enabled":              true,
		"http_cache_check_newer_versions": true,
	})
	first := executeResourceAction(t, h, "/")
	second := executeResourceAction(t, h, "/")

	if atomic.LoadInt32(&hits) != 2 {
		t.Fatalf("server hits = %d, want 2 (fresh entry must revalidate)", hits)
	}
	if !conditionalSeen.Load() {
		t.Fatal("second request did not carry If-None-Match despite fresh cache entry")
	}
	if first.ResponseBody != "body-1" || second.ResponseBody != "body-1" {
		t.Fatalf("responses = %q / %q, want cached body-1 after 304", first.ResponseBody, second.ResponseBody)
	}
}

func TestHTTPClearCacheEmptiesCache(t *testing.T) {
	var hits int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit := atomic.AddInt32(&hits, 1)
		w.Header().Set("Cache-Control", "max-age=300")
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("v" + strconv.Itoa(int(hit))))
	}))
	defer ts.Close()

	h := initHTTP(t, map[string]any{"base_url": ts.URL, "http_cache_enabled": true})
	first := executeResourceAction(t, h, "/")
	if err := h.ClearCache(context.Background()); err != nil {
		t.Fatalf("ClearCache: %v", err)
	}
	second := executeResourceAction(t, h, "/")

	if atomic.LoadInt32(&hits) != 2 {
		t.Fatalf("server hits = %d, want 2 after ClearCache", hits)
	}
	if first.ResponseBody != "v1" || second.ResponseBody != "v2" {
		t.Fatalf("responses = %q / %q, want v1 then v2", first.ResponseBody, second.ResponseBody)
	}
}

func TestHTTPClearCacheNilCacheIsSafe(t *testing.T) {
	h := initHTTP(t, map[string]any{"base_url": "https://example.test"})
	if err := h.ClearCache(context.Background()); err != nil {
		t.Fatalf("ClearCache on nil cache: %v", err)
	}
}

func TestRefererForResourceFollowsStrictOriginWhenCrossOrigin(t *testing.T) {
	const page = "https://site.test/dir/page.html?session=secret#frag"
	tests := []struct {
		name     string
		pageURL  string
		resource string
		want     string
	}{
		{"same origin keeps full path and query", page, "https://site.test/css/app.css", "https://site.test/dir/page.html?session=secret"},
		{"cross origin is reduced to origin", page, "https://cdn.other/app.js", "https://site.test/"},
		{"cross port is cross origin", page, "https://site.test:8443/app.js", "https://site.test/"},
		{"https to http sends nothing", page, "http://cdn.other/app.js", ""},
		{"http page to http cdn is origin only", "http://site.test/a/b?x=1", "http://cdn.other/app.js", "http://site.test/"},
		{"userinfo never leaks", "https://user:pw@site.test/a?x=1", "https://site.test/app.js", "https://site.test/a?x=1"},
		{"blank page url", "", "https://cdn.other/app.js", ""},
		{"unparseable resource", page, "::not a url::", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := refererForResource(tt.pageURL, tt.resource); got != tt.want {
				t.Errorf("refererForResource(%q, %q) = %q, want %q", tt.pageURL, tt.resource, got, tt.want)
			}
		})
	}
}

func TestProcessResourceSlotsBoundsConcurrency(t *testing.T) {
	slots := processResourceSlots()
	if cap(slots) <= 0 {
		t.Fatalf("process resource slots cap = %d, want > 0", cap(slots))
	}
	// Sized once per process, so repeat calls must hand back the same channel
	// rather than silently widening the generator-wide budget.
	if again := processResourceSlots(); again != slots {
		t.Error("processResourceSlots returned a different channel on second call")
	}
}
