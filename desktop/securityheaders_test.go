package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDesktopAssetSecurityHeaders(t *testing.T) {
	called := false
	handler := desktopAssetSecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "http://wails.localhost/", nil)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if !called || resp.Code != http.StatusNoContent {
		t.Fatalf("next called=%v status=%d", called, resp.Code)
	}
	if got := resp.Header().Get("Content-Security-Policy"); !strings.Contains(got, "object-src 'none'") || !strings.Contains(got, "http://127.0.0.1:*") {
		t.Fatalf("unexpected CSP %q", got)
	}
	for _, name := range []string{"X-Content-Type-Options", "X-Frame-Options", "Referrer-Policy", "Permissions-Policy"} {
		if resp.Header().Get(name) == "" {
			t.Errorf("missing %s", name)
		}
	}
}
