package http

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
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

func TestBoundedCookieJarEnforcesCookieAndProcessBudgets(t *testing.T) {
	target, _ := url.Parse("https://example.test/app/page")
	probe := &http.Cookie{Name: "session", Value: "small"}
	budget := &responseCaptureBudget{limit: boundedCookieSize(probe)}
	jar, err := newBoundedCookieJarWithBudget(budget)
	if err != nil {
		t.Fatal(err)
	}
	jar.SetCookies(target, []*http.Cookie{probe, {Name: "second", Value: "small"}})
	if got := jar.Cookies(target); len(got) != 1 || got[0].Name != "session" {
		t.Fatalf("cookies after process budget = %#v, want only session", got)
	}
	jar.SetCookies(target, []*http.Cookie{{Name: "oversized", Value: strings.Repeat("x", maxHTTPCookieBytes)}})
	if got := jar.Cookies(target); len(got) != 1 {
		t.Fatalf("oversized cookie was retained: %#v", got)
	}
	if err := jar.clear(); err != nil {
		t.Fatal(err)
	}
	if got := budget.used.Load(); got != 0 {
		t.Fatalf("cookie clear leaked %d process bytes", got)
	}
}

func TestBoundedCookieJarLimitsDistinctEntries(t *testing.T) {
	target, _ := url.Parse("https://example.test/")
	budget := &responseCaptureBudget{limit: maxHTTPProcessCookieBytes}
	jar, err := newBoundedCookieJarWithBudget(budget)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i <= maxHTTPCookiesPerVUser; i++ {
		jar.SetCookies(target, []*http.Cookie{{Name: fmt.Sprintf("cookie-%d", i), Value: "v"}})
	}
	if got := len(jar.Cookies(target)); got != maxHTTPCookiesPerVUser {
		t.Fatalf("cookie count = %d, want %d", got, maxHTTPCookiesPerVUser)
	}
	_ = jar.clear()
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
