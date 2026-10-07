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
		{name: "same host with a different scheme is cross origin", allowed: nil, origin: "https://app.example.com", host: "app.example.com", want: false},
		{name: "same origin permitted with a separate allow-list", allowed: []string{"wails://wails"}, origin: "http://127.0.0.1:54321", host: "127.0.0.1:54321", want: true},
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
	if got := allowed.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("Access-Control-Allow-Credentials = %q, want true", got)
	}
	if got := allowed.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(got, "X-APEXLoad-CSRF-Token") {
		t.Fatalf("Access-Control-Allow-Headers = %q, want CSRF token header", got)
	}
	if got := allowed.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(got, "Idempotency-Key") {
		t.Fatalf("Access-Control-Allow-Headers = %q, want the idempotency key header", got)
	}
	if got := allowed.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(got, desktopCapabilityHeader) {
		t.Fatalf("Access-Control-Allow-Headers = %q, want desktop capability header", got)
	}
	if got := allowed.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(got, "If-Match") {
		t.Fatalf("Access-Control-Allow-Headers = %q, want settings concurrency header", got)
	}
}

func TestDesktopCapabilityMiddleware(t *testing.T) {
	const (
		capability = "desktop-secret"
		origin     = "wails://wails"
	)
	tests := []struct {
		name       string
		method     string
		header     string
		query      string
		upgrade    bool
		wantStatus int
		wantCalled bool
	}{
		{name: "missing rejected", method: http.MethodGet, wantStatus: http.StatusUnauthorized},
		{name: "wrong rejected", method: http.MethodPost, header: "wrong", wantStatus: http.StatusUnauthorized},
		{name: "header accepted", method: http.MethodPost, header: capability, wantStatus: http.StatusNoContent, wantCalled: true},
		{name: "websocket query accepted", method: http.MethodGet, query: capability, upgrade: true, wantStatus: http.StatusNoContent, wantCalled: true},
		{name: "query rejected outside websocket", method: http.MethodGet, query: capability, wantStatus: http.StatusUnauthorized},
		{name: "preflight bypasses capability", method: http.MethodOptions, wantStatus: http.StatusNoContent, wantCalled: true},
		{name: "non api route bypasses capability", method: http.MethodGet, wantStatus: http.StatusNoContent, wantCalled: true},
		{name: "metrics requires capability", method: http.MethodGet, wantStatus: http.StatusUnauthorized},
		{name: "metrics accepts capability", method: http.MethodGet, header: capability, wantStatus: http.StatusNoContent, wantCalled: true},
		{name: "oidc callback landing is nonmutating", method: http.MethodGet, wantStatus: http.StatusNoContent, wantCalled: true},
		{name: "oidc callback finalization requires capability", method: http.MethodPost, wantStatus: http.StatusUnauthorized},
		{name: "github callback landing is nonmutating", method: http.MethodGet, wantStatus: http.StatusNoContent, wantCalled: true},
		{name: "github callback finalization requires capability", method: http.MethodPost, wantStatus: http.StatusUnauthorized},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			handler := desktopCapabilityMiddleware(capability, []string{origin})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusNoContent)
			}))
			path := "/api/v1/tests"
			if tc.name == "non api route bypasses capability" {
				path = "/"
			} else if strings.HasPrefix(tc.name, "metrics ") {
				path = "/metrics"
			} else if strings.HasPrefix(tc.name, "oidc callback") {
				path = "/api/v1/auth/callback"
			} else if strings.HasPrefix(tc.name, "github callback") {
				path = "/api/v1/integrations/github/callback"
			}
			if tc.query != "" {
				path += "?" + desktopCapabilityQuery + "=" + tc.query
			}
			req := httptest.NewRequest(tc.method, path, nil)
			req.Header.Set("Origin", origin)
			if tc.header != "" {
				req.Header.Set(desktopCapabilityHeader, tc.header)
			}
			if tc.upgrade {
				req.Header.Set("Upgrade", "websocket")
			}
			resp := httptest.NewRecorder()
			handler.ServeHTTP(resp, req)
			if resp.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", resp.Code, tc.wantStatus)
			}
			if called != tc.wantCalled {
				t.Fatalf("handler called = %v, want %v", called, tc.wantCalled)
			}
			if !tc.wantCalled && resp.Header().Get("Access-Control-Allow-Credentials") != "true" {
				t.Fatal("rejection did not retain credentialed CORS headers")
			}
		})
	}
}

func TestDesktopServerProtectsSamePortMetrics(t *testing.T) {
	server := NewServer(ServerConfig{DesktopMode: true, DesktopCapability: "desktop-secret", Addr: "127.0.0.1:0"})
	request := func(token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		if token != "" {
			req.Header.Set(DesktopCapabilityHeader, token)
		}
		resp := httptest.NewRecorder()
		server.httpServer.Handler.ServeHTTP(resp, req)
		return resp
	}
	if got := request("").Code; got != http.StatusUnauthorized {
		t.Fatalf("unauthenticated metrics status = %d, want %d", got, http.StatusUnauthorized)
	}
	if got := request("wrong").Code; got != http.StatusUnauthorized {
		t.Fatalf("wrong-capability metrics status = %d, want %d", got, http.StatusUnauthorized)
	}
	if got := request("desktop-secret").Code; got != http.StatusOK {
		t.Fatalf("authenticated metrics status = %d, want %d", got, http.StatusOK)
	}
}

func TestDesktopCapabilityMiddlewareRejectsLegacyLoopbackCookie(t *testing.T) {
	handler := desktopCapabilityMiddleware("desktop-secret", []string{"http://wails.localhost"})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tests", nil)
	req.AddCookie(&http.Cookie{Name: "apexload_desktop_capability", Value: "desktop-secret"})
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("legacy cross-port cookie status = %d, want %d", resp.Code, http.StatusUnauthorized)
	}
}

func TestDesktopAuthMaterialMiddlewareInjectsOnlyBoundInProcessSession(t *testing.T) {
	const encodedSession = "signed-session-value"
	handler := desktopAuthMaterialMiddleware(func(capability string) (string, bool) {
		if capability == "browser-secret" {
			return encodedSession, true
		}
		return "", false
	}, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := r.Cookie(oidcStateCookieName); err == nil {
			http.Error(w, "state cookie was not stripped", http.StatusBadRequest)
			return
		}
		session, err := r.Cookie(oidcSessionCookieName)
		if err != nil || session.Value != encodedSession {
			http.Error(w, "bound session was not injected", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	tests := []struct {
		name      string
		header    bool
		websocket bool
	}{
		{name: "header", header: true},
		{name: "websocket query", websocket: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := "/api/v1/system/settings"
			if test.websocket {
				path = "/api/v1/ws?" + desktopCapabilityQuery + "=browser-secret"
			}
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.AddCookie(&http.Cookie{Name: oidcStateCookieName, Value: "attacker-state"})
			req.AddCookie(&http.Cookie{Name: oidcSessionCookieName, Value: "attacker-session"})
			if test.header {
				req.Header.Set(DesktopCapabilityHeader, "browser-secret")
			}
			if test.websocket {
				req.Header.Set("Upgrade", "websocket")
			}
			resp := httptest.NewRecorder()
			handler.ServeHTTP(resp, req)
			if resp.Code != http.StatusNoContent {
				t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
			}
		})
	}
}

func TestDesktopBrowserBootstrapIsSingleUse(t *testing.T) {
	server := NewServer(ServerConfig{DesktopMode: true, Addr: "127.0.0.1:0"})
	server.setBoundAddr("127.0.0.1:54321")
	bootstrapURL, err := server.NewDesktopBrowserURL("admin", "project-a")
	if err != nil {
		t.Fatalf("NewDesktopBrowserURL: %v", err)
	}
	if strings.Contains(bootstrapURL, server.DesktopCapability()) {
		t.Fatal("bootstrap URL exposes the long-lived desktop capability")
	}
	if strings.Contains(bootstrapURL, "project-a") || strings.Contains(bootstrapURL, "admin") {
		t.Fatalf("bootstrap URL exposes development preview context: %s", bootstrapURL)
	}

	request := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, bootstrapURL, nil)
		resp := httptest.NewRecorder()
		server.httpServer.Handler.ServeHTTP(resp, req)
		return resp
	}
	first := request()
	if first.Code != http.StatusOK {
		t.Fatalf("first bootstrap status = %d, want %d", first.Code, http.StatusOK)
	}
	for _, cookie := range first.Result().Cookies() {
		if cookie.Value != "" || cookie.MaxAge >= 0 {
			t.Fatalf("bootstrap emitted auth material instead of only expiring legacy cookies: %#v", cookie)
		}
	}
	body := first.Body.String()
	if strings.Contains(body, server.DesktopCapability()) {
		t.Fatal("bootstrap page exposed the Wails renderer capability")
	}
	if !strings.Contains(body, `sessionStorage.setItem("apexload.desktopCapability"`) ||
		!strings.Contains(body, `localStorage.setItem("apexload.devViewMode","admin")`) ||
		!strings.Contains(body, `const project="project-a"`) ||
		!strings.Contains(body, `localStorage.setItem("apexload.activeProjectId",project)`) ||
		!strings.Contains(body, `location.hash.startsWith("#/")`) ||
		!strings.Contains(body, `location.replace("/"+route)`) {
		t.Fatalf("bootstrap page does not seed port-scoped session storage and preserve a safe dashboard route: %s", body)
	}
	server.desktopBootstrapMu.Lock()
	capabilityCount := len(server.desktopBrowserCapabilities)
	if capabilityCount != 1 {
		server.desktopBootstrapMu.Unlock()
		t.Fatalf("browser capability count = %d, want 1", capabilityCount)
	}
	var browserCapability string
	for candidate := range server.desktopBrowserCapabilities {
		browserCapability = candidate
	}
	grant := server.desktopBrowserCapabilities[browserCapability]
	server.desktopBootstrapMu.Unlock()
	if browserCapability == "" || !strings.Contains(body, browserCapability) || !server.validDesktopBrowserCapability(browserCapability) {
		t.Fatal("bootstrap did not issue an accepted in-memory browser capability")
	}
	if grant.devViewMode != "admin" || grant.projectID != "project-a" {
		t.Fatalf("browser capability preview = (%q, %q), want (admin, project-a)", grant.devViewMode, grant.projectID)
	}

	second := request()
	if second.Code != http.StatusForbidden {
		t.Fatalf("reused bootstrap status = %d, want %d", second.Code, http.StatusForbidden)
	}
}

func TestDesktopBrowserBootstrapPreviewValidationAndMissingProject(t *testing.T) {
	tests := []struct {
		name      string
		mode      string
		projectID string
		wantMode  string
		wantBody  string
	}{
		{name: "user project", mode: "user", projectID: "project-b", wantMode: "user", wantBody: `const project="project-b"`},
		{name: "user missing project", mode: "user", wantMode: "user", wantBody: `const project="";if(project)`},
		{name: "missing mode defaults admin", wantMode: "admin", wantBody: `localStorage.setItem("apexload.devViewMode","admin")`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := NewServer(ServerConfig{DesktopMode: true, Addr: "127.0.0.1:0"})
			server.setBoundAddr("127.0.0.1:54321")
			bootstrapURL, err := server.NewDesktopBrowserURL(test.mode, test.projectID)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(bootstrapURL, test.projectID) && test.projectID != "" {
				t.Fatalf("bootstrap URL exposed project: %s", bootstrapURL)
			}
			resp := httptest.NewRecorder()
			server.httpServer.Handler.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, bootstrapURL, nil))
			if resp.Code != http.StatusOK || !strings.Contains(resp.Body.String(), test.wantBody) {
				t.Fatalf("bootstrap response status=%d body=%s, want %q", resp.Code, resp.Body.String(), test.wantBody)
			}
			if !strings.Contains(resp.Body.String(), `localStorage.setItem("apexload.devViewMode","`+test.wantMode+`")`) {
				t.Fatalf("bootstrap did not preserve %s mode: %s", test.wantMode, resp.Body.String())
			}
			if test.projectID == "" && !strings.Contains(resp.Body.String(), `localStorage.removeItem("apexload.activeProjectId")`) {
				t.Fatalf("missing project did not clear stale browser selection: %s", resp.Body.String())
			}
		})
	}

	server := NewServer(ServerConfig{DesktopMode: true, Addr: "127.0.0.1:0"})
	server.setBoundAddr("127.0.0.1:54321")
	if _, err := server.NewDesktopBrowserURL("owner", "project-a"); err == nil {
		t.Fatal("invalid preview mode produced a bootstrap URL")
	}
	if _, err := server.NewDesktopBrowserURL("user", "project/a"); err == nil {
		t.Fatal("invalid preview project produced a bootstrap URL")
	}
	server.desktopBootstrapMu.Lock()
	defer server.desktopBootstrapMu.Unlock()
	if len(server.desktopBootstrapNonces) != 0 {
		t.Fatalf("invalid preview context retained %d bootstrap grants", len(server.desktopBootstrapNonces))
	}
}

func TestCORSMiddlewareWildcardNeverEnablesCredentials(t *testing.T) {
	handler := corsMiddleware([]string{"*"})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	request := func(cookie string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "https://api.example.test/api/v1/tests", nil)
		req.Host = "api.example.test"
		req.Header.Set("Origin", "https://other.example.test")
		if cookie != "" {
			req.Header.Set("Cookie", cookie)
		}
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, req)
		return resp
	}

	plain := request("")
	if plain.Code != http.StatusNoContent || plain.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("wildcard response = %d ACAO=%q", plain.Code, plain.Header().Get("Access-Control-Allow-Origin"))
	}
	if got := plain.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Fatalf("wildcard response enabled credentials: %q", got)
	}
	credentialed := request("session=ambient")
	if credentialed.Code != http.StatusForbidden {
		t.Fatalf("credentialed wildcard request status = %d, want %d", credentialed.Code, http.StatusForbidden)
	}
}

func TestWebSocketWildcardRejectsAmbientCredentials(t *testing.T) {
	hub := NewHub()
	hub.SetAllowedOrigins([]string{"*"})
	check := hub.upgrader().CheckOrigin
	req := httptest.NewRequest(http.MethodGet, "https://api.example.test/api/v1/ws", nil)
	req.Host = "api.example.test"
	req.Header.Set("Origin", "https://other.example.test")
	if !check(req) {
		t.Fatal("non-credentialed wildcard WebSocket was rejected")
	}
	req.Header.Set("Cookie", "session=ambient")
	if check(req) {
		t.Fatal("credentialed wildcard WebSocket was accepted")
	}

	hub.SetAllowedOrigins([]string{"https://other.example.test"})
	if !hub.upgrader().CheckOrigin(req) {
		t.Fatal("explicitly allow-listed credentialed WebSocket was rejected")
	}
}

func TestWebSocketSameOriginHonorsForwardedHTTPSOnlyFromTrustedProxy(t *testing.T) {
	hub := NewHub()
	hub.SetOriginAuthProvider(func() AuthSettings {
		return AuthSettings{TrustedProxyCIDRs: []string{"10.0.0.0/8"}}
	})
	request := func(remote string) *http.Request {
		req := httptest.NewRequest(http.MethodGet, "http://api.example.test/api/v1/ws", nil)
		req.Host = "api.example.test"
		req.RemoteAddr = remote
		req.Header.Set("Origin", "https://api.example.test")
		req.Header.Set("X-Forwarded-Proto", "https")
		return req
	}
	if !hub.upgrader().CheckOrigin(request("10.1.2.3:443")) {
		t.Fatal("trusted proxy HTTPS origin was rejected")
	}
	if hub.upgrader().CheckOrigin(request("203.0.113.7:443")) {
		t.Fatal("untrusted forwarded HTTPS origin was accepted")
	}
}

func TestCORSMiddlewareRejectsDisallowedMutationOrigins(t *testing.T) {
	const (
		apiHost       = "api.example.test"
		trustedOrigin = "https://dashboard.example.test"
		hostileOrigin = "https://attacker.example.test"
	)

	tests := []struct {
		name           string
		method         string
		origin         string
		allowedOrigins []string
		wantStatus     int
		wantCalled     bool
	}{
		{
			name:       "same-site sibling post rejected",
			method:     http.MethodPost,
			origin:     hostileOrigin,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "same-origin post permitted",
			method:     http.MethodPost,
			origin:     "https://" + apiHost,
			wantStatus: http.StatusNoContent,
			wantCalled: true,
		},
		{
			name:           "allow-listed desktop post permitted",
			method:         http.MethodPost,
			origin:         trustedOrigin,
			allowedOrigins: []string{trustedOrigin},
			wantStatus:     http.StatusNoContent,
			wantCalled:     true,
		},
		{
			name:       "non-browser post without origin permitted",
			method:     http.MethodPost,
			wantStatus: http.StatusNoContent,
			wantCalled: true,
		},
		{
			name:       "disallowed read remains available",
			method:     http.MethodGet,
			origin:     hostileOrigin,
			wantStatus: http.StatusNoContent,
			wantCalled: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			handler := corsMiddleware(tc.allowedOrigins)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusNoContent)
			}))
			req := httptest.NewRequest(tc.method, "https://"+apiHost+"/api/v1/tests/test-a/start", strings.NewReader(`{"name":"changed"}`))
			req.Host = apiHost
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			// text/plain is a CORS-safelisted content type and therefore reaches
			// the server without a preflight even when the body contains JSON.
			req.Header.Set("Content-Type", "text/plain")
			resp := httptest.NewRecorder()

			handler.ServeHTTP(resp, req)

			if resp.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", resp.Code, tc.wantStatus)
			}
			if called != tc.wantCalled {
				t.Fatalf("handler called = %v, want %v", called, tc.wantCalled)
			}
		})
	}
}

func TestCORSMiddlewareHonorsForwardedProtoOnlyFromTrustedProxy(t *testing.T) {
	settings := AuthSettings{TrustedProxyCIDRs: []string{"10.0.0.0/8"}}
	handler := corsMiddleware(nil, func() AuthSettings { return settings })(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	request := func(remoteAddr string) int {
		req := httptest.NewRequest(http.MethodPost, "http://api.example.test/api/v1/tests/test-a/start", nil)
		req.Host = "api.example.test"
		req.RemoteAddr = remoteAddr
		req.Header.Set("Origin", "https://api.example.test")
		req.Header.Set("X-Forwarded-Proto", "https")
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, req)
		return resp.Code
	}
	if got := request("10.2.3.4:443"); got != http.StatusNoContent {
		t.Fatalf("trusted proxy status = %d, want 204", got)
	}
	if got := request("203.0.113.9:443"); got != http.StatusForbidden {
		t.Fatalf("untrusted peer status = %d, want 403", got)
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
