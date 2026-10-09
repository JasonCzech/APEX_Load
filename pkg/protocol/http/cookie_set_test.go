package http

import (
	"context"
	"net/url"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestSetCookieWritesTheJar(t *testing.T) {
	h := &HTTP{}
	if err := h.Init(context.Background(), map[string]any{"base_url": "https://shop.test/app"}); err != nil {
		t.Fatal(err)
	}
	defer h.Teardown(context.Background())
	ctx := context.Background()
	for _, cookie := range []protocol.ScriptCookie{
		{Name: "hostonly", Value: "1"},
		{Name: "domainwide", Value: "2", Domain: ".shop.test"},
		{Name: "secureonly", Value: "3", Secure: true},
		{Name: "scoped", Value: "4", Path: "/app"},
	} {
		if err := h.SetCookie(ctx, cookie); err != nil {
			t.Fatal(err)
		}
	}
	names := func(raw string) map[string]string {
		target, _ := url.Parse(raw)
		out := map[string]string{}
		for _, c := range h.client.Jar.Cookies(target) {
			out[c.Name] = c.Value
		}
		return out
	}
	root := names("https://shop.test/")
	if root["hostonly"] != "1" || root["domainwide"] != "2" || root["secureonly"] != "3" || root["scoped"] != "" {
		t.Fatalf("cookies at https://shop.test/ = %v", root)
	}
	if app := names("https://shop.test/app/x"); app["scoped"] != "4" {
		t.Fatalf("path cookie missing under /app: %v", app)
	}
	if sub := names("https://api.shop.test/"); sub["domainwide"] != "2" || sub["hostonly"] != "" {
		t.Fatalf("subdomain cookies = %v, want only the Domain cookie", sub)
	}
	if plain := names("http://shop.test/"); plain["secureonly"] != "" {
		t.Fatalf("secure cookie leaked to http: %v", plain)
	}

	if err := h.SetCookie(ctx, protocol.ScriptCookie{Name: "hostonly", Value: "replaced"}); err != nil {
		t.Fatal(err)
	}
	if got := names("https://shop.test/")["hostonly"]; got != "replaced" {
		t.Fatalf("setting the same cookie again must replace it, got %q", got)
	}
	if err := h.SetCookie(ctx, protocol.ScriptCookie{Name: "hostonly", Remove: true}); err != nil {
		t.Fatal(err)
	}
	if _, ok := names("https://shop.test/")["hostonly"]; ok {
		t.Fatal("remove must expire the cookie")
	}
	if err := h.SetCookie(ctx, protocol.ScriptCookie{}); err == nil {
		t.Fatal("a cookie without a name must be rejected")
	}
}

func TestSetCookieNeedsADomainWithoutABaseURL(t *testing.T) {
	h := &HTTP{}
	if err := h.Init(context.Background(), map[string]any{}); err != nil {
		t.Fatal(err)
	}
	defer h.Teardown(context.Background())
	if err := h.SetCookie(context.Background(), protocol.ScriptCookie{Name: "a", Value: "b"}); err == nil {
		t.Fatal("without a base URL a domain is required")
	}
	if err := h.SetCookie(context.Background(), protocol.ScriptCookie{Name: "a", Value: "b", Domain: "shop.test"}); err != nil {
		t.Fatal(err)
	}
}
