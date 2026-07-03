package browser

import "testing"

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

func TestRequestPolicyEnabled(t *testing.T) {
	tests := []struct {
		name string
		b    *Browser
		want bool
	}{
		{
			name: "default no policy",
			b:    &Browser{cacheEnabled: true, downloadResources: "all"},
			want: false,
		},
		{
			name: "cache disabled",
			b:    &Browser{cacheEnabled: false, downloadResources: "all"},
			want: true,
		},
		{
			name: "check newer versions",
			b:    &Browser{cacheEnabled: true, checkNewerVersions: true, downloadResources: "all"},
			want: true,
		},
		{
			name: "download resources none",
			b:    &Browser{cacheEnabled: true, downloadResources: "none"},
			want: true,
		},
		{
			name: "download filter",
			b: &Browser{
				cacheEnabled:        true,
				downloadResources:   "all",
				downloadFilterMode:  "allow_list",
				downloadFilterHosts: []string{"static.example.com"},
			},
			want: true,
		},
		{
			name: "network latency route fallback for non-CDP engine",
			b:    &Browser{engine: "webkit", cacheEnabled: true, downloadResources: "all", networkLatencyMS: 50},
			want: true,
		},
		{
			name: "network latency uses CDP for chromium",
			b:    &Browser{engine: "chromium", cacheEnabled: true, downloadResources: "all", networkLatencyMS: 50},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.b.requestPolicyEnabled(); got != tt.want {
				t.Fatalf("requestPolicyEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRequestPolicyBlocksStaticResourcesOnlyWhenConfigured(t *testing.T) {
	b := Browser{downloadResources: "none"}
	if !b.shouldBlockRequestInfo("https://example.com/logo.png", "image", false) {
		t.Fatal("image request was not blocked when download_resources=none")
	}
	if b.shouldBlockRequestInfo("https://example.com/", "document", true) {
		t.Fatal("navigation document was blocked; main documents must always continue")
	}
	if b.shouldBlockRequestInfo("https://example.com/api", "xhr", false) {
		t.Fatal("xhr request was blocked; non-static resources should continue")
	}
}

func TestRequestPolicyBlocksUnrecordedResources(t *testing.T) {
	b := Browser{
		downloadResources:          "recorded",
		recordedResourcesAvailable: true,
		recordedResources: normalizeRecordedResources([]string{
			"https://cdn.example.com/app.js?v=1",
		}),
	}

	if b.shouldBlockRequestInfo("https://cdn.example.com/app.js?v=1", "script", false) {
		t.Fatal("recorded script was blocked")
	}
	if !b.shouldBlockRequestInfo("https://cdn.example.com/other.js", "script", false) {
		t.Fatal("unrecorded script was not blocked")
	}
	if b.shouldBlockRequestInfo("https://cdn.example.com/api/orders", "xhr", false) {
		t.Fatal("unrecorded xhr was blocked; recorded resource mode should only constrain static resources")
	}
}

func TestRequestPolicyDownloadFiltersApplyToStaticResources(t *testing.T) {
	b := Browser{
		downloadResources:   "all",
		downloadFilterMode:  "allow_list",
		downloadFilterHosts: []string{"cdn.example.com"},
	}

	if b.shouldBlockRequestInfo("https://cdn.example.com/app.js", "script", false) {
		t.Fatal("allow-listed static host was blocked")
	}
	if !b.shouldBlockRequestInfo("https://assets.example.com/app.js", "script", false) {
		t.Fatal("non-allow-listed static host was not blocked")
	}
	if b.shouldBlockRequestInfo("https://assets.example.com/api", "xhr", false) {
		t.Fatal("non-static request was blocked by static download filter")
	}
}

func TestRequestPolicyHeadersDisableCacheAndRevalidate(t *testing.T) {
	b := Browser{
		cacheEnabled:       true,
		checkNewerVersions: true,
		resourceValidators: map[string]string{
			"https://cdn.example.com/app.js": `"app-v1"`,
		},
	}
	original := map[string]string{"Accept": "*/*"}

	headers := b.requestPolicyHeadersFor("https://cdn.example.com/app.js", "script", false, original)
	if headers["Cache-Control"] != "no-cache" {
		t.Fatalf("Cache-Control = %q, want no-cache", headers["Cache-Control"])
	}
	if headers["Pragma"] != "no-cache" {
		t.Fatalf("Pragma = %q, want no-cache", headers["Pragma"])
	}
	if headers["If-None-Match"] != `"app-v1"` {
		t.Fatalf("If-None-Match = %q, want app-v1 validator", headers["If-None-Match"])
	}
	if _, ok := original["Cache-Control"]; ok {
		t.Fatal("requestPolicyHeadersFor mutated the original header map")
	}
}

func TestShouldRevalidateBrowserRequestInfo(t *testing.T) {
	if !shouldRevalidateBrowserRequestInfo("stylesheet", false) {
		t.Fatal("stylesheet should be revalidated")
	}
	if shouldRevalidateBrowserRequestInfo("document", true) {
		t.Fatal("navigation document should not be revalidated through static-resource logic")
	}
	if shouldRevalidateBrowserRequestInfo("xhr", false) {
		t.Fatal("xhr should not be treated as a static browser resource")
	}
}
