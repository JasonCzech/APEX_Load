package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
	if !strings.Contains(resp.Body.String(), "<div id=\"root\"></div>") {
		t.Fatalf("body = %q, want dashboard shell", resp.Body.String())
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
