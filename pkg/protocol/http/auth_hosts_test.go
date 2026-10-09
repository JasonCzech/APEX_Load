package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestParseAuthHostScopes(t *testing.T) {
	valid := []any{"Shop.Test", "api.shop.test:8443", "[::1]:9000", "10.0.0.5"}
	scopes, err := parseAuthHostScopes(valid)
	if err != nil {
		t.Fatalf("valid hosts rejected: %v", err)
	}
	if len(scopes) != 4 || scopes[0].host != "shop.test" || scopes[1].port != "8443" || scopes[2].host != "::1" {
		t.Fatalf("scopes = %+v", scopes)
	}
	for _, bad := range []string{"", "https://shop.test", "shop.test/path", "*.shop.test", "user@shop.test", "shop.test:"} {
		if err := ValidateAuthHosts([]string{bad}); err == nil {
			t.Errorf("host %q must be rejected", bad)
		}
	}
	if _, err := parseAuthHostScopes(42); err == nil {
		t.Fatal("a non-list value must be rejected")
	}
}

func TestAuthHostScopesAllow(t *testing.T) {
	scopes, _ := parseAuthHostScopes([]string{"shop.test", "api.shop.test:8443", "plain.test:80"})
	tests := []struct {
		target string
		want   bool
	}{
		{"https://shop.test/a", true},
		{"http://SHOP.test:9999/a", true},
		{"https://api.shop.test:8443/x", true},
		{"https://api.shop.test/x", false},
		{"http://plain.test/x", true},
		{"https://plain.test/x", false},
		{"https://cdn.test/x", false},
		{"not a url", false},
	}
	for _, tt := range tests {
		if got := authHostScopesAllow(scopes, tt.target); got != tt.want {
			t.Errorf("authHostScopesAllow(%q) = %v, want %v", tt.target, got, tt.want)
		}
	}
	if !authHostScopesAllow(nil, "https://any.test/") {
		t.Fatal("no scopes must allow every host")
	}
}

func TestScriptAuthIsSentOnlyToScopedHosts(t *testing.T) {
	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Host+" "+r.Header.Get("Authorization"))
	}))
	defer server.Close()
	scoped := strings.TrimPrefix(server.URL, "http://")
	runtime := &HTTP{}
	if err := runtime.Init(context.Background(), map[string]any{
		"base_url": server.URL, "auth_type": "basic", "auth_username": "alice", "auth_password": "s3cret",
		"auth_hosts": []string{scoped},
	}); err != nil {
		t.Fatal(err)
	}
	defer runtime.Teardown(context.Background())
	for _, target := range []string{"/secure", strings.Replace(server.URL, "127.0.0.1", "localhost", 1) + "/public"} {
		if _, err := runtime.Execute(context.Background(), &protocol.Action{Name: "a", Method: "GET", Target: target}); err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 2 || !strings.HasSuffix(seen[0], "Basic YWxpY2U6czNjcmV0") || strings.Contains(seen[1], "Basic") {
		t.Fatalf("requests = %q, want credentials only on the scoped host", seen)
	}
}
