package api

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestStaticRoutesServeIndexForClientPath(t *testing.T) {
	server := NewServer(ServerConfig{})

	req := httptest.NewRequest(http.MethodGet, "/scripts", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	if contentType := resp.Header().Get("Content-Type"); !strings.Contains(contentType, "text/html") {
		t.Fatalf("content-type = %q, want text/html", contentType)
	}
	if got := resp.Header().Get("Permissions-Policy"); got != "clipboard-write=(self)" {
		t.Fatalf("permissions-policy = %q, want clipboard-write=(self)", got)
	}
	staticSub, err := fs.Sub(StaticFS, "static")
	if err == nil {
		if ok, _ := dashboardStaticReady(staticSub); ok {
			if !strings.Contains(resp.Body.String(), "<div id=\"root\"></div>") {
				t.Fatalf("body = %q, want dashboard shell", resp.Body.String())
			}
			return
		}
	}
	if !strings.Contains(resp.Body.String(), "Dashboard not built") {
		t.Fatalf("body = %q, want dashboard shell or build instructions", resp.Body.String())
	}
}

func TestStaticRoutesReturnNotFoundForMissingAssetPath(t *testing.T) {
	server := NewServer(ServerConfig{})

	req := httptest.NewRequest(http.MethodGet, "/assets/does-not-exist.js", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusNotFound, resp.Body.String())
	}
	if strings.Contains(resp.Body.String(), "<div id=\"root\"></div>") {
		t.Fatalf("body = %q, did not want dashboard shell for missing asset", resp.Body.String())
	}
}

func TestStaticRoutesReturnJSONNotFoundForUnknownAPIPath(t *testing.T) {
	server := NewServer(ServerConfig{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/downloads/not-a-real-download", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusNotFound, resp.Body.String())
	}
	if contentType := resp.Header().Get("Content-Type"); !strings.Contains(contentType, "application/json") {
		t.Fatalf("content-type = %q, want application/json", contentType)
	}
	if strings.Contains(resp.Body.String(), "<div id=\"root\"></div>") {
		t.Fatalf("body = %q, did not want dashboard shell for unknown API path", resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "API endpoint not found") {
		t.Fatalf("body = %q, want API not found error", resp.Body.String())
	}
}

func TestDashboardStaticReadyAcceptsMatchingAssets(t *testing.T) {
	staticFS := fstest.MapFS{
		"index.html": {
			Data: []byte(`<!doctype html><script type="module" src="/assets/index.js"></script><link rel="stylesheet" href="/assets/index.css">`),
		},
		"assets/index.js": {
			Data: []byte(`console.log("ok")`),
		},
		"assets/index.css": {
			Data: []byte(`body { color: #111; }`),
		},
	}

	ok, reason := dashboardStaticReady(staticFS)
	if !ok {
		t.Fatalf("dashboardStaticReady() = false, reason %q", reason)
	}
}

func TestDashboardStaticReadyRejectsMissingBundleAsset(t *testing.T) {
	staticFS := fstest.MapFS{
		"index.html": {
			Data: []byte(`<!doctype html><script type="module" src="/assets/index.js"></script>`),
		},
	}

	ok, reason := dashboardStaticReady(staticFS)
	if ok {
		t.Fatalf("dashboardStaticReady() = true, want false")
	}
	if !strings.Contains(reason, "assets/index.js") {
		t.Fatalf("reason = %q, want missing asset path", reason)
	}
}

func TestDashboardStaticReadyRejectsUnbuiltIndex(t *testing.T) {
	staticFS := fstest.MapFS{
		"index.html": {
			Data: []byte(`<!doctype html><div id="root"></div>`),
		},
	}

	ok, reason := dashboardStaticReady(staticFS)
	if ok {
		t.Fatalf("dashboardStaticReady() = true, want false")
	}
	if !strings.Contains(reason, "does not reference") {
		t.Fatalf("reason = %q, want unbuilt index explanation", reason)
	}
}
