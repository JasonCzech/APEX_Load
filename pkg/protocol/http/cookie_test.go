package http

import (
	"context"
	"net/http"
	"net/url"
	"testing"
)

func TestHTTPClearCookiesEmptiesJar(t *testing.T) {
	h := &HTTP{}
	if err := h.Init(context.Background(), map[string]any{"base_url": "https://example.test"}); err != nil {
		t.Fatalf("init error: %v", err)
	}
	if h.client.Jar == nil {
		t.Fatal("expected a cookie jar when cookies are enabled by default")
	}

	target, _ := url.Parse("https://example.test/app")
	h.client.Jar.SetCookies(target, []*http.Cookie{{Name: "session", Value: "abc123"}})
	if got := h.client.Jar.Cookies(target); len(got) != 1 {
		t.Fatalf("seeded cookies = %#v, want one session cookie", got)
	}

	if err := h.ClearCookies(context.Background()); err != nil {
		t.Fatalf("ClearCookies error: %v", err)
	}
	if got := h.client.Jar.Cookies(target); len(got) != 0 {
		t.Fatalf("cookies after clear = %#v, want empty jar", got)
	}
}

func TestHTTPClearCookiesNoJarIsNoop(t *testing.T) {
	h := &HTTP{}
	if err := h.Init(context.Background(), map[string]any{"base_url": "https://example.test", "cookies_enabled": false}); err != nil {
		t.Fatalf("init error: %v", err)
	}
	if h.client.Jar != nil {
		t.Fatal("expected no cookie jar when cookies are disabled")
	}
	if err := h.ClearCookies(context.Background()); err != nil {
		t.Fatalf("ClearCookies on jar-less client should be a no-op, got: %v", err)
	}
}
