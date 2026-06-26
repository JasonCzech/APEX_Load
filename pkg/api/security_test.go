package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveConfinedExportPath(t *testing.T) {
	base := t.TempDir()
	absBase, err := filepath.Abs(base)
	if err != nil {
		t.Fatalf("abs base: %v", err)
	}

	tests := []struct {
		name     string
		userDir  string
		filename string
		wantErr  bool
		wantBase string // expected parent dir of result (absolute)
	}{
		{name: "simple", userDir: "", filename: "order.json", wantBase: absBase},
		{name: "subdir allowed", userDir: "team-a", filename: "x.json", wantBase: filepath.Join(absBase, "team-a")},
		{name: "parent traversal in dir neutralized", userDir: "../../etc", filename: "x.json", wantBase: filepath.Join(absBase, "etc")},
		{name: "absolute dir neutralized", userDir: "/etc/cron.d", filename: "x.json", wantBase: filepath.Join(absBase, "etc", "cron.d")},
		{name: "traversal in filename neutralized", userDir: "", filename: "../../../etc/passwd", wantBase: absBase},
		{name: "slash in filename sanitized", userDir: "", filename: "a/b/c.json", wantBase: absBase},
		{name: "empty filename falls back", userDir: "", filename: "", wantBase: absBase},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveConfinedExportPath(base, tc.userDir, tc.filename)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got path %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != absBase && !strings.HasPrefix(got, absBase+string(filepath.Separator)) {
				t.Fatalf("resolved path %q escaped base %q", got, absBase)
			}
			if dir := filepath.Dir(got); dir != tc.wantBase {
				t.Fatalf("parent dir = %q, want %q", dir, tc.wantBase)
			}
			if strings.Contains(filepath.Base(got), "/") || strings.Contains(filepath.Base(got), "\\") {
				t.Fatalf("filename component not sanitized: %q", filepath.Base(got))
			}
		})
	}
}

func TestOriginAllowed(t *testing.T) {
	newReq := func(origin, host string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/v1/ws", nil)
		r.Host = host
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		return r
	}

	tests := []struct {
		name    string
		allowed []string
		origin  string
		host    string
		want    bool
	}{
		{name: "empty origin permitted", allowed: nil, origin: "", host: "app.example.com", want: true},
		{name: "same origin permitted when unconfigured", allowed: nil, origin: "http://app.example.com", host: "app.example.com", want: true},
		{name: "cross origin rejected when unconfigured", allowed: nil, origin: "http://evil.example.com", host: "app.example.com", want: false},
		{name: "listed origin permitted", allowed: []string{"https://dash.example.com"}, origin: "https://dash.example.com", host: "api.example.com", want: true},
		{name: "unlisted origin rejected", allowed: []string{"https://dash.example.com"}, origin: "https://evil.example.com", host: "api.example.com", want: false},
		{name: "wildcard permits any", allowed: []string{"*"}, origin: "https://anything.example.com", host: "api.example.com", want: true},
		{name: "case-insensitive match", allowed: []string{"https://Dash.Example.com"}, origin: "https://dash.example.com", host: "api.example.com", want: true},
		// Wails desktop: the webview's cross-origin requests to the loopback API
		// must be permitted only when the wails origin is explicitly allow-listed.
		{name: "wails origin rejected when unconfigured", allowed: nil, origin: "wails://wails.localhost", host: "127.0.0.1:54321", want: false},
		{name: "wails macos origin permitted when listed", allowed: []string{"wails://wails.localhost"}, origin: "wails://wails.localhost", host: "127.0.0.1:54321", want: true},
		{name: "wails windows origin permitted when listed", allowed: []string{"http://wails.localhost"}, origin: "http://wails.localhost", host: "127.0.0.1:54321", want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := originAllowed(tc.allowed, newReq(tc.origin, tc.host)); got != tc.want {
				t.Fatalf("originAllowed = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestCORSPreflightAllowsAllowListedWailsOrigin reproduces the desktop failure:
// the dashboard's GET /api/v1/auth/me sets Content-Type, so the webview issues a
// CORS preflight. Without the wails origin on the allow-list the preflight gets
// no Access-Control-Allow-Origin header and the browser blocks the request,
// surfacing as "Authentication check failed". With it allow-listed the origin is
// reflected and the request proceeds.
func TestCORSPreflightAllowsAllowListedWailsOrigin(t *testing.T) {
	const wailsOrigin = "wails://wails.localhost"

	preflight := func(allowed []string) *httptest.ResponseRecorder {
		server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: "127.0.0.1:0", AllowedOrigins: allowed})
		req := httptest.NewRequest(http.MethodOptions, "/api/v1/auth/me", nil)
		req.Header.Set("Origin", wailsOrigin)
		req.Header.Set("Access-Control-Request-Method", http.MethodGet)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		return resp
	}

	blocked := preflight(nil)
	if got := blocked.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("expected no Access-Control-Allow-Origin without allow-list, got %q", got)
	}

	allowed := preflight([]string{wailsOrigin})
	if allowed.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want %d", allowed.Code, http.StatusNoContent)
	}
	if got := allowed.Header().Get("Access-Control-Allow-Origin"); got != wailsOrigin {
		t.Fatalf("Access-Control-Allow-Origin = %q, want %q", got, wailsOrigin)
	}
	if got := allowed.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(got, "X-APEXLoad-CSRF-Token") {
		t.Fatalf("Access-Control-Allow-Headers = %q, want CSRF token header", got)
	}
}

func TestBoundAddrReady(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: "127.0.0.1:0"})
	if server.BoundAddrReady() {
		t.Fatal("BoundAddrReady = true before the listener bound, want false")
	}
	if got := server.BrowserURL(); !strings.HasSuffix(got, ":0") {
		t.Fatalf("BrowserURL before bind = %q, want the unbound :0 placeholder", got)
	}

	server.setBoundAddr("127.0.0.1:54321")
	if !server.BoundAddrReady() {
		t.Fatal("BoundAddrReady = false after setBoundAddr, want true")
	}
	if got := server.BrowserURL(); got != "http://127.0.0.1:54321" {
		t.Fatalf("BrowserURL after bind = %q, want http://127.0.0.1:54321", got)
	}
}

func TestValidateOutboundURL(t *testing.T) {
	t.Setenv("APEXLOAD_ALLOW_PRIVATE_WEBHOOKS", "")

	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{name: "public host literal ip ok", url: "https://8.8.8.8/notify", wantErr: false},
		{name: "loopback rejected", url: "http://127.0.0.1:9000/x", wantErr: true},
		{name: "loopback name rejected", url: "http://localhost/x", wantErr: true},
		{name: "metadata link-local rejected", url: "http://169.254.169.254/latest/meta-data", wantErr: true},
		{name: "private 10/8 rejected", url: "http://10.1.2.3/x", wantErr: true},
		{name: "private 192.168 rejected", url: "http://192.168.0.5/x", wantErr: true},
		{name: "unspecified rejected", url: "http://0.0.0.0/x", wantErr: true},
		{name: "ipv6 loopback rejected", url: "http://[::1]/x", wantErr: true},
		{name: "bad scheme rejected", url: "ftp://example.com/x", wantErr: true},
		{name: "missing host rejected", url: "http:///x", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateOutboundURL(tc.url)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error for %q", tc.url)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.url, err)
			}
		})
	}
}

func TestValidateOutboundURLAllowPrivateEscapeHatch(t *testing.T) {
	t.Setenv("APEXLOAD_ALLOW_PRIVATE_WEBHOOKS", "true")
	if err := validateOutboundURL("http://127.0.0.1:9000/x"); err != nil {
		t.Fatalf("expected private URL to be allowed with escape hatch, got %v", err)
	}
	// Scheme is still enforced.
	if err := validateOutboundURL("ftp://127.0.0.1/x"); err == nil {
		t.Fatalf("expected bad scheme to be rejected even with escape hatch")
	}
}

func TestProductionMode(t *testing.T) {
	cases := []struct {
		env     string
		require string
		want    bool
	}{
		{env: "production", want: true},
		{env: "prod", want: true},
		{env: "Production", want: true},
		{env: "dev", want: false},
		{env: "", want: false},
		{env: "", require: "1", want: true},
		{env: "staging", require: "true", want: true},
	}
	for _, tc := range cases {
		t.Setenv("APEXLOAD_ENV", tc.env)
		t.Setenv("APEXLOAD_REQUIRE_AUTH", tc.require)
		if got := productionMode(); got != tc.want {
			t.Fatalf("productionMode(env=%q require=%q) = %v, want %v", tc.env, tc.require, got, tc.want)
		}
	}
}

func TestRedactURL(t *testing.T) {
	cases := map[string]string{
		"https://hooks.slack.com/services/T000/B000/SECRETTOKEN": "https://hooks.slack.com/[redacted]",
		"https://example.com/path?token=abc":                     "https://example.com/[redacted]",
		"not a url":                                              "[redacted-url]",
	}
	for in, want := range cases {
		if got := redactURL(in); got != want {
			t.Fatalf("redactURL(%q) = %q, want %q", in, got, want)
		}
		if strings.Contains(redactURL(in), "SECRETTOKEN") || strings.Contains(redactURL(in), "token=abc") {
			t.Fatalf("redactURL leaked secret for %q", in)
		}
	}
}
