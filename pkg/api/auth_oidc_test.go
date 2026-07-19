package api

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v3"
	"github.com/go-jose/go-jose/v3/jwt"
	"github.com/jasonczech/apexload/pkg/storage"
	"golang.org/x/oauth2"
)

func TestOIDCSecureURLRejectsCleartextRemoteEndpoints(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{name: "https remote", raw: "https://identity.example.test/tenant"},
		{name: "http ipv4 loopback", raw: "http://127.0.0.1:8080/tenant"},
		{name: "http ipv6 loopback", raw: "http://[::1]:8080/tenant"},
		{name: "http localhost", raw: "http://localhost:8080/tenant"},
		{name: "http remote", raw: "http://identity.example.test/tenant", wantErr: true},
		{name: "userinfo", raw: "https://user:pass@identity.example.test/tenant", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateOIDCSecureURL("test endpoint", test.raw)
			if (err != nil) != test.wantErr {
				t.Fatalf("validateOIDCSecureURL(%q) error = %v, wantErr=%v", test.raw, err, test.wantErr)
			}
		})
	}
}

func TestOIDCTokenExchangeRefusesCrossOriginCredentialRedirect(t *testing.T) {
	var leaked atomic.Bool
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked.Store(true)
		_ = r.ParseForm()
		writeJSON(w, http.StatusOK, map[string]any{"access_token": "stolen", "token_type": "Bearer"})
	}))
	defer sink.Close()
	tokenEndpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, sink.URL+"/capture", http.StatusTemporaryRedirect)
	}))
	defer tokenEndpoint.Close()

	runtime := &oidcRuntime{
		httpClient: newOIDCHTTPClient(),
		oauth2Config: oauth2.Config{
			ClientID:     "client-id",
			ClientSecret: "client-secret",
			Endpoint:     oauth2.Endpoint{TokenURL: tokenEndpoint.URL},
		},
	}
	if _, err := runtime.exchange(context.Background(), "authorization-code"); err == nil {
		t.Fatal("OIDC token exchange followed a cross-origin credential redirect")
	}
	if leaked.Load() {
		t.Fatal("cross-origin redirect received OIDC client credentials")
	}
}

func TestOIDCHTTPClientBoundsRemoteResponses(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", oidcRemoteResponseMaxBytes+1)))
	}))
	defer remote.Close()
	resp, err := newOIDCHTTPClient().Get(remote.URL)
	if err != nil {
		t.Fatalf("GET bounded response: %v", err)
	}
	defer resp.Body.Close()
	if _, err := io.ReadAll(resp.Body); err == nil {
		t.Fatalf("response larger than %d bytes was accepted", oidcRemoteResponseMaxBytes)
	}
}

func TestAuthSessionSecretRequiresProductionStrength(t *testing.T) {
	t.Setenv("APEXLOAD_ENV", "production")
	t.Setenv("APEXLOAD_AUTH_SESSION_SECRET", "short")
	if _, err := authSessionSecret(AuthSettings{SessionSecretEnvVar: "APEXLOAD_AUTH_SESSION_SECRET"}); err == nil {
		t.Fatal("weak production OIDC session secret was accepted")
	}
	t.Setenv("APEXLOAD_AUTH_SESSION_SECRET", strings.Repeat("s", productionSecretMinBytes))
	if _, err := authSessionSecret(AuthSettings{SessionSecretEnvVar: "APEXLOAD_AUTH_SESSION_SECRET"}); err != nil {
		t.Fatalf("strong production OIDC session secret rejected: %v", err)
	}
}

func TestSanitizeReturnToRejectsNetworkPathVariants(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "local path", raw: "/results/run-1?tab=summary#latency", want: "/results/run-1?tab=summary#latency"},
		{name: "empty", raw: "", want: "/"},
		{name: "absolute", raw: "https://evil.test/", want: "/"},
		{name: "network path", raw: "//evil.test/", want: "/"},
		{name: "backslash network path", raw: "/\\\\evil.test/", want: "/"},
		{name: "encoded backslash network path", raw: "/%5c%5cevil.test/", want: "/"},
		{name: "encoded slash network path", raw: "/%2f%2fevil.test/", want: "/"},
		{name: "control character", raw: "/safe\nLocation: https://evil.test", want: "/"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := sanitizeReturnTo(test.raw); got != test.want {
				t.Fatalf("sanitizeReturnTo(%q) = %q, want %q", test.raw, got, test.want)
			}
		})
	}
}

func TestHandleGetAuthStatusReturnsOIDCLoginAndLogoutURLs(t *testing.T) {
	provider := newFakeOIDCProvider(t)
	defer provider.Close()
	t.Setenv("APEXLOAD_OIDC_CLIENT_SECRET", "test-client-secret")
	t.Setenv("APEXLOAD_AUTH_SESSION_SECRET", "test-session-secret")

	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	enableOIDCAuth(t, server, provider.Issuer)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var payload AuthStatusResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal auth status: %v", err)
	}
	if payload.Mode != authModeOIDC {
		t.Fatalf("mode = %q, want %q", payload.Mode, authModeOIDC)
	}
	if payload.LoginURL != oidcLoginPath {
		t.Fatalf("login url = %q, want %q", payload.LoginURL, oidcLoginPath)
	}
	if payload.LogoutURL != oidcLogoutPath {
		t.Fatalf("logout url = %q, want %q", payload.LogoutURL, oidcLogoutPath)
	}
	if payload.Authenticated {
		t.Fatal("expected unauthenticated auth status before oidc login")
	}
	if payload.LogoutCSRF != "" {
		t.Fatalf("logout csrf token = %q, want empty for unauthenticated status", payload.LogoutCSRF)
	}
	if payload.SessionStatus != "missing" {
		t.Fatalf("session status = %q, want missing", payload.SessionStatus)
	}
}

func TestOIDCLoginCachesProviderDiscoveryAndRateLimitsAnonymousRequests(t *testing.T) {
	provider := newFakeOIDCProvider(t)
	defer provider.Close()
	t.Setenv("APEXLOAD_OIDC_CLIENT_SECRET", "test-client-secret")
	t.Setenv("APEXLOAD_AUTH_SESSION_SECRET", "test-session-secret")
	t.Setenv("APEXLOAD_OIDC_LOGIN_RATE_LIMIT_PER_MINUTE", "2")

	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	enableOIDCAuth(t, server, provider.Issuer)
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, oidcLoginPath, nil)
		req.RemoteAddr = "203.0.113.7:1234"
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		if resp.Code != http.StatusFound {
			t.Fatalf("login %d status = %d, want %d body=%s", i+1, resp.Code, http.StatusFound, resp.Body.String())
		}
	}
	if calls := provider.discoveryCalls.Load(); calls != 1 {
		t.Fatalf("provider discovery calls = %d, want 1", calls)
	}

	limitedReq := httptest.NewRequest(http.MethodGet, oidcLoginPath, nil)
	limitedReq.RemoteAddr = "203.0.113.7:1234"
	limitedResp := httptest.NewRecorder()
	server.mux.ServeHTTP(limitedResp, limitedReq)
	if limitedResp.Code != http.StatusTooManyRequests {
		t.Fatalf("limited login status = %d, want %d body=%s", limitedResp.Code, http.StatusTooManyRequests, limitedResp.Body.String())
	}
}

func TestOIDCLoginReturnsProviderURLForManagedBrowserNavigation(t *testing.T) {
	provider := newFakeOIDCProvider(t)
	defer provider.Close()
	t.Setenv("APEXLOAD_OIDC_CLIENT_SECRET", "test-client-secret")
	t.Setenv("APEXLOAD_AUTH_SESSION_SECRET", "test-session-secret")

	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	enableOIDCAuth(t, server, provider.Issuer)
	req := httptest.NewRequest(http.MethodGet, oidcLoginPath+"?return_to=/settings", nil)
	req.Header.Set("Accept", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("managed login status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	if resp.Header().Get("Location") != "" {
		t.Fatalf("managed login unexpectedly redirected to %q", resp.Header().Get("Location"))
	}
	if resp.Header().Get("Cache-Control") != "no-store" || resp.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("managed login security headers = %#v", resp.Header())
	}
	var payload struct {
		RedirectURL string `json:"redirect_url"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(payload.RedirectURL)
	if err != nil || !strings.HasPrefix(payload.RedirectURL, provider.Issuer+"/authorize") || parsed.Query().Get("state") == "" || parsed.Query().Get("nonce") == "" {
		t.Fatalf("managed login redirect URL = %q, parse error %v", payload.RedirectURL, err)
	}
	if findResponseCookie(t, resp.Result().Cookies(), oidcStateCookieName).Value == "" {
		t.Fatal("managed login did not set the signed state cookie")
	}
}

func TestDesktopBrowserBootstrapCapabilityStartsOIDCNavigation(t *testing.T) {
	provider := newFakeOIDCProvider(t)
	defer provider.Close()
	t.Setenv("APEXLOAD_OIDC_CLIENT_SECRET", "test-client-secret")
	t.Setenv("APEXLOAD_AUTH_SESSION_SECRET", "test-session-secret")

	server := NewServer(ServerConfig{DesktopMode: true, DesktopCapability: "native-renderer-secret", MaxConcurrent: 2, Addr: "127.0.0.1:0"})
	enableOIDCAuth(t, server, provider.Issuer)
	server.setBoundAddr("127.0.0.1:54321")
	bootstrapURL, err := server.NewDesktopBrowserURL("admin", "")
	if err != nil {
		t.Fatal(err)
	}
	bootstrapReq := httptest.NewRequest(http.MethodGet, bootstrapURL, nil)
	bootstrapResp := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(bootstrapResp, bootstrapReq)
	if bootstrapResp.Code != http.StatusOK {
		t.Fatalf("bootstrap status = %d body=%s", bootstrapResp.Code, bootstrapResp.Body.String())
	}

	server.desktopBootstrapMu.Lock()
	var browserCapability string
	for capability := range server.desktopBrowserCapabilities {
		browserCapability = capability
	}
	server.desktopBootstrapMu.Unlock()
	if browserCapability == "" || !strings.Contains(bootstrapResp.Body.String(), browserCapability) {
		t.Fatal("bootstrap did not seed the browser-only capability")
	}

	loginReq := httptest.NewRequest(http.MethodGet, oidcLoginPath, nil)
	loginReq.Header.Set(DesktopCapabilityHeader, browserCapability)
	loginReq.Header.Set("Accept", "application/json")
	loginResp := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(loginResp, loginReq)
	if loginResp.Code != http.StatusOK {
		t.Fatalf("capability-bearing login status = %d body=%s", loginResp.Code, loginResp.Body.String())
	}
	var payload struct {
		RedirectURL string `json:"redirect_url"`
	}
	if err := json.Unmarshal(loginResp.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	parsedRedirect, err := url.Parse(payload.RedirectURL)
	if err != nil || !strings.HasPrefix(payload.RedirectURL, provider.Issuer+"/authorize") {
		t.Fatalf("login redirect = %q, parse error %v", payload.RedirectURL, err)
	}
	if got, want := parsedRedirect.Query().Get("redirect_uri"), "http://127.0.0.1:54321"+oidcCallbackPath; got != want {
		t.Fatalf("desktop OIDC redirect_uri = %q, want %q", got, want)
	}
	if cookies := loginResp.Result().Cookies(); len(cookies) != 0 {
		t.Fatalf("desktop OIDC login emitted host-scoped cookies: %#v", cookies)
	}
	state := parsedRedirect.Query().Get("state")
	nonce := parsedRedirect.Query().Get("nonce")
	callbackURL := oidcCallbackURL(nonce, state)

	// The provider's top-level GET only renders a no-mutation landing page. The
	// tab must then finalize with its sessionStorage capability.
	landingReq := httptest.NewRequest(http.MethodGet, callbackURL, nil)
	landingResp := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(landingResp, landingReq)
	if landingResp.Code != http.StatusOK || !strings.Contains(landingResp.Body.String(), `method:"POST"`) {
		t.Fatalf("desktop callback landing = %d %s", landingResp.Code, landingResp.Body.String())
	}
	server.desktopBootstrapMu.Lock()
	boundBeforeFinalize := server.desktopBrowserCapabilities[browserCapability].oidcSessionCookie
	server.desktopBootstrapMu.Unlock()
	if boundBeforeFinalize != "" {
		t.Fatal("GET callback mutated the desktop authentication session")
	}

	missingCapabilityReq := httptest.NewRequest(http.MethodPost, callbackURL, nil)
	missingCapabilityResp := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(missingCapabilityResp, missingCapabilityReq)
	if missingCapabilityResp.Code != http.StatusUnauthorized {
		t.Fatalf("callback without capability = %d, want 401", missingCapabilityResp.Code)
	}
	const otherBrowserCapability = "other-browser-tab-secret"
	server.desktopBootstrapMu.Lock()
	server.desktopBrowserCapabilities[otherBrowserCapability] = desktopBrowserCapability{issuedAt: time.Now().UTC()}
	server.desktopBootstrapMu.Unlock()
	otherTabReq := httptest.NewRequest(http.MethodPost, callbackURL, nil)
	otherTabReq.Header.Set(DesktopCapabilityHeader, otherBrowserCapability)
	otherTabResp := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(otherTabResp, otherTabReq)
	if otherTabResp.Code != http.StatusUnauthorized {
		t.Fatalf("callback from another authorized tab = %d, want 401", otherTabResp.Code)
	}

	callbackReq := httptest.NewRequest(http.MethodPost, callbackURL, nil)
	callbackReq.Header.Set(DesktopCapabilityHeader, browserCapability)
	callbackReq.Header.Set("Accept", "application/json")
	callbackResp := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(callbackResp, callbackReq)
	if callbackResp.Code != http.StatusOK {
		t.Fatalf("capability callback status = %d body=%s", callbackResp.Code, callbackResp.Body.String())
	}
	if cookies := callbackResp.Result().Cookies(); len(cookies) != 0 {
		t.Fatalf("desktop OIDC callback emitted host-scoped cookies: %#v", cookies)
	}

	statusReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	statusReq.Header.Set(DesktopCapabilityHeader, browserCapability)
	statusResp := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(statusResp, statusReq)
	var status AuthStatusResponse
	if err := json.Unmarshal(statusResp.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if !status.Authenticated || status.User == nil || status.User.Subject != "alice" {
		t.Fatalf("desktop capability did not resolve the established session: %#v", status)
	}
	replayReq := httptest.NewRequest(http.MethodPost, callbackURL, nil)
	replayReq.Header.Set(DesktopCapabilityHeader, browserCapability)
	replayResp := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(replayResp, replayReq)
	if replayResp.Code != http.StatusUnauthorized {
		t.Fatalf("replayed desktop callback = %d, want 401", replayResp.Code)
	}
	pendingAtLogout := oidcStateToken{
		State: "pending-at-logout", Nonce: "pending-nonce", IssuedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(oidcStateTTL),
	}
	if !server.storeDesktopOIDCState(browserCapability, pendingAtLogout) {
		t.Fatal("failed to install pending desktop OIDC state before logout")
	}
	logoutReq := httptest.NewRequest(http.MethodPost, oidcLogoutPath, nil)
	logoutReq.Header.Set(DesktopCapabilityHeader, browserCapability)
	logoutReq.Header.Set("X-APEXLoad-CSRF-Token", status.LogoutCSRF)
	logoutResp := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(logoutResp, logoutReq)
	if logoutResp.Code != http.StatusOK {
		t.Fatalf("desktop logout = %d body=%s", logoutResp.Code, logoutResp.Body.String())
	}
	if cookies := logoutResp.Result().Cookies(); len(cookies) != 0 {
		t.Fatalf("desktop logout emitted host-scoped cookies: %#v", cookies)
	}
	if server.bindDesktopOIDCSession(browserCapability, pendingAtLogout.State, "stale-session") {
		t.Fatal("an OIDC callback already in flight re-authenticated the capability after logout")
	}
	server.desktopBootstrapMu.Lock()
	remainingFlow := server.desktopBrowserCapabilities[browserCapability].oidcFlowState
	remainingPending := len(server.desktopOIDCStates)
	server.desktopBootstrapMu.Unlock()
	if remainingFlow != "" || remainingPending != 0 {
		t.Fatalf("logout left desktop OIDC flow state=%q pending=%d", remainingFlow, remainingPending)
	}
	loggedOutStatusReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	loggedOutStatusReq.Header.Set(DesktopCapabilityHeader, browserCapability)
	loggedOutStatusResp := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(loggedOutStatusResp, loggedOutStatusReq)
	var loggedOutStatus AuthStatusResponse
	if err := json.Unmarshal(loggedOutStatusResp.Body.Bytes(), &loggedOutStatus); err != nil {
		t.Fatal(err)
	}
	if loggedOutStatus.Authenticated {
		t.Fatalf("desktop capability remained authenticated after logout: %#v", loggedOutStatus)
	}
}

func TestDesktopOIDCNewerFlowFencesInFlightOlderCallback(t *testing.T) {
	server := NewServer(ServerConfig{DesktopMode: true, DesktopCapability: "native-renderer-secret"})
	const capability = "duplicated-oidc-tab"
	server.desktopBootstrapMu.Lock()
	server.desktopBrowserCapabilities[capability] = desktopBrowserCapability{issuedAt: time.Now().UTC()}
	server.desktopBootstrapMu.Unlock()
	now := time.Now().UTC()
	oldState := oidcStateToken{State: "old-state", Nonce: "old-nonce", IssuedAt: now, ExpiresAt: now.Add(oidcStateTTL)}
	newState := oidcStateToken{State: "new-state", Nonce: "new-nonce", IssuedAt: now.Add(time.Second), ExpiresAt: now.Add(oidcStateTTL)}
	if !server.storeDesktopOIDCState(capability, oldState) {
		t.Fatal("failed to start old desktop OIDC flow")
	}
	if taken, ok := server.takeDesktopOIDCState(capability, oldState.State); !ok || taken.State != oldState.State {
		t.Fatalf("take old desktop OIDC state = (%+v, %v)", taken, ok)
	}
	// Simulate the old callback performing its provider exchange while a newer
	// login begins through a duplicated tab carrying the same capability.
	if !server.storeDesktopOIDCState(capability, newState) {
		t.Fatal("failed to start newer desktop OIDC flow")
	}
	if server.bindDesktopOIDCSession(capability, oldState.State, "old-session") {
		t.Fatal("older in-flight OIDC callback overwrote the newer active flow")
	}
	if taken, ok := server.takeDesktopOIDCState(capability, newState.State); !ok || taken.State != newState.State {
		t.Fatalf("take newer desktop OIDC state = (%+v, %v)", taken, ok)
	}
	if !server.bindDesktopOIDCSession(capability, newState.State, "new-session") {
		t.Fatal("newest desktop OIDC callback could not bind its session")
	}
	if session, ok := server.desktopOIDCSessionForCapability(capability); !ok || session != "new-session" {
		t.Fatalf("desktop OIDC session = (%q, %v), want newest session", session, ok)
	}
}

func TestProductionAuthCookiesAlwaysRequireSecureTransport(t *testing.T) {
	t.Setenv("APEXLOAD_ENV", "production")
	server := NewServer(ServerConfig{DesktopMode: true, DesktopCapability: "native-renderer-secret", Addr: "127.0.0.1:0"})
	server.setBoundAddr("127.0.0.1:54321")
	settings := AuthSettings{RedirectURL: "https://hosted.example.test/api/v1/auth/callback"}

	resp := httptest.NewRecorder()
	server.setSignedAuthCookie(resp, settings, &http.Cookie{
		Name:     oidcStateCookieName,
		Value:    "signed-state",
		Path:     "/api/v1/auth",
		HttpOnly: true,
	})
	cookie := findResponseCookie(t, resp.Result().Cookies(), oidcStateCookieName)
	if !cookie.Secure {
		t.Fatal("production auth cookie was permitted on cleartext loopback")
	}

	hosted := NewServer(ServerConfig{})
	hostedResp := httptest.NewRecorder()
	hosted.setSignedAuthCookie(hostedResp, settings, &http.Cookie{Name: oidcStateCookieName, Value: "signed-state", Path: "/"})
	if cookie := findResponseCookie(t, hostedResp.Result().Cookies(), oidcStateCookieName); !cookie.Secure {
		t.Fatal("hosted production auth cookie must remain Secure")
	}
}

func TestDesktopOAuthEmitsNoAuthMaterialToAnotherLoopbackPort(t *testing.T) {
	provider := newFakeOIDCProvider(t)
	defer provider.Close()
	t.Setenv("APEXLOAD_OIDC_CLIENT_SECRET", "test-client-secret")
	t.Setenv("APEXLOAD_AUTH_SESSION_SECRET", "test-session-secret")

	server := NewServer(ServerConfig{DesktopMode: true, DesktopCapability: "native-renderer-secret", MaxConcurrent: 2, Addr: "127.0.0.1:0"})
	enableOIDCAuth(t, server, provider.Issuer)
	appHTTP := httptest.NewServer(server.httpServer.Handler)
	defer appHTTP.Close()
	appURL, err := url.Parse(appHTTP.URL)
	if err != nil {
		t.Fatal(err)
	}
	server.setBoundAddr(appURL.Host)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate cookies left by an older desktop version; the bootstrap removes
	// them before starting a new browser-capability flow.
	jar.SetCookies(appURL, []*http.Cookie{
		{Name: oidcSessionCookieName, Value: "legacy-session", Path: "/"},
		{Name: oidcStateCookieName, Value: "legacy-state", Path: "/api/v1/auth"},
		{Name: githubStateCookieName, Value: "legacy-github", Path: "/api/v1/integrations/github"},
	})
	client := &http.Client{Jar: jar}
	bootstrapURL, err := server.NewDesktopBrowserURL("admin", "")
	if err != nil {
		t.Fatal(err)
	}
	bootstrapResp, err := client.Get(bootstrapURL)
	if err != nil {
		t.Fatal(err)
	}
	_ = bootstrapResp.Body.Close()

	server.desktopBootstrapMu.Lock()
	var browserCapability string
	for capability := range server.desktopBrowserCapabilities {
		browserCapability = capability
	}
	server.desktopBootstrapMu.Unlock()
	loginReq, err := http.NewRequest(http.MethodGet, appHTTP.URL+oidcLoginPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	loginReq.Header.Set(DesktopCapabilityHeader, browserCapability)
	loginReq.Header.Set("Accept", "application/json")
	loginResp, err := client.Do(loginReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = loginResp.Body.Close()
	if cookies := loginResp.Cookies(); len(cookies) != 0 {
		t.Fatalf("desktop login emitted host-scoped authentication cookies: %#v", cookies)
	}

	seenCookies := make(chan string, 3)
	unrelated := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenCookies <- r.Header.Get("Cookie")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer unrelated.Close()
	for _, path := range []string{"/", "/api/v1/auth/callback", "/api/v1/integrations/github/callback"} {
		resp, requestErr := client.Get(unrelated.URL + path)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		_ = resp.Body.Close()
		if got := <-seenCookies; got != "" {
			t.Fatalf("unrelated loopback service received desktop auth material on %s: %q", path, got)
		}
	}
}

func TestHandleGetAuthDiagnosticsReturnsHealthyOIDCReadiness(t *testing.T) {
	provider := newFakeOIDCProvider(t)
	defer provider.Close()
	t.Setenv("APEXLOAD_OIDC_CLIENT_SECRET", "test-client-secret")
	t.Setenv("APEXLOAD_AUTH_SESSION_SECRET", "0123456789abcdef0123456789abcdef")

	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	enableOIDCAuth(t, server, provider.Issuer)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/diagnostics", nil)
	// Diagnostics is admin-gated in OIDC mode; authenticate via an admin session.
	req.AddCookie(oidcAdminSessionCookie(t, server, RoleSystemAdmin))
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var payload AuthDiagnosticsResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal auth diagnostics: %v", err)
	}
	if !payload.Ready {
		t.Fatalf("expected healthy oidc diagnostics, got %#v", payload)
	}
	if payload.ProviderModel != "native_oidc" {
		t.Fatalf("provider model = %q, want native_oidc", payload.ProviderModel)
	}
	if len(payload.Checks) < 6 {
		t.Fatalf("checks = %#v, want oidc diagnostics", payload.Checks)
	}
}

func TestOIDCLoginCallbackCreatesSessionAndAuthorizesProtectedRoutes(t *testing.T) {
	provider := newFakeOIDCProvider(t)
	defer provider.Close()
	t.Setenv("APEXLOAD_OIDC_CLIENT_SECRET", "test-client-secret")
	t.Setenv("APEXLOAD_AUTH_SESSION_SECRET", "test-session-secret")

	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableOIDCAuth(t, server, provider.Issuer)

	loginReq := httptest.NewRequest(http.MethodGet, oidcLoginPath+"?return_to=/settings", nil)
	loginResp := httptest.NewRecorder()
	server.mux.ServeHTTP(loginResp, loginReq)

	if loginResp.Code != http.StatusFound {
		t.Fatalf("login status = %d, want %d body=%s", loginResp.Code, http.StatusFound, loginResp.Body.String())
	}
	loginLocation := loginResp.Header().Get("Location")
	if !strings.HasPrefix(loginLocation, provider.Issuer+"/authorize") {
		t.Fatalf("login redirect = %q, want provider authorize endpoint", loginLocation)
	}
	stateCookie := findResponseCookie(t, loginResp.Result().Cookies(), oidcStateCookieName)
	if stateCookie.Value == "" {
		t.Fatal("expected oidc state cookie to be set")
	}
	authorizeURL, err := url.Parse(loginLocation)
	if err != nil {
		t.Fatalf("parse authorize url: %v", err)
	}
	redirectState := authorizeURL.Query().Get("state")
	nonce := authorizeURL.Query().Get("nonce")
	if redirectState == "" || nonce == "" {
		t.Fatalf("authorize query = %q, want state and nonce", authorizeURL.RawQuery)
	}

	callbackReq := httptest.NewRequest(http.MethodGet, oidcCallbackURL(nonce, redirectState), nil)
	callbackReq.AddCookie(stateCookie)
	callbackResp := httptest.NewRecorder()
	server.mux.ServeHTTP(callbackResp, callbackReq)

	if callbackResp.Code != http.StatusSeeOther {
		t.Fatalf("callback status = %d, want %d body=%s", callbackResp.Code, http.StatusSeeOther, callbackResp.Body.String())
	}
	if callbackResp.Header().Get("Location") != "/settings" {
		t.Fatalf("callback redirect = %q, want /settings", callbackResp.Header().Get("Location"))
	}
	sessionCookie := findResponseCookie(t, callbackResp.Result().Cookies(), oidcSessionCookieName)
	if sessionCookie.Value == "" {
		t.Fatal("expected oidc session cookie to be set")
	}

	statusReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	statusReq.AddCookie(sessionCookie)
	statusResp := httptest.NewRecorder()
	server.mux.ServeHTTP(statusResp, statusReq)

	if statusResp.Code != http.StatusOK {
		t.Fatalf("status request status = %d, want %d body=%s", statusResp.Code, http.StatusOK, statusResp.Body.String())
	}
	var statusPayload AuthStatusResponse
	if err := json.Unmarshal(statusResp.Body.Bytes(), &statusPayload); err != nil {
		t.Fatalf("unmarshal auth status: %v", err)
	}
	if !statusPayload.Authenticated || statusPayload.User == nil {
		t.Fatalf("expected authenticated oidc user, got %#v", statusPayload)
	}
	if statusPayload.User.Subject != "alice" {
		t.Fatalf("subject = %q, want alice", statusPayload.User.Subject)
	}
	if len(statusPayload.User.GlobalRoles) == 0 || statusPayload.User.GlobalRoles[0] != RoleSystemAdmin {
		t.Fatalf("global roles = %#v, want system_admin present", statusPayload.User.GlobalRoles)
	}
	if strings.TrimSpace(statusPayload.LogoutCSRF) == "" {
		t.Fatal("expected authenticated oidc status to include logout csrf token")
	}
	if statusPayload.SessionStatus != "active" {
		t.Fatalf("session status = %q, want active", statusPayload.SessionStatus)
	}
	if statusPayload.SessionExpiresAt == "" || statusPayload.SessionTTLSeconds <= 0 {
		t.Fatalf("session expiry metadata = (%q, %d), want populated", statusPayload.SessionExpiresAt, statusPayload.SessionTTLSeconds)
	}

	protectedReq := httptest.NewRequest(http.MethodGet, "/api/v1/system/settings", nil)
	protectedReq.AddCookie(sessionCookie)
	protectedResp := httptest.NewRecorder()
	server.mux.ServeHTTP(protectedResp, protectedReq)
	if protectedResp.Code != http.StatusOK {
		t.Fatalf("protected route status = %d, want %d body=%s", protectedResp.Code, http.StatusOK, protectedResp.Body.String())
	}

	logoutReq := httptest.NewRequest(http.MethodPost, oidcLogoutPath, nil)
	logoutReq.AddCookie(sessionCookie)
	logoutReq.Header.Set("X-APEXLoad-CSRF-Token", statusPayload.LogoutCSRF)
	logoutResp := httptest.NewRecorder()
	server.mux.ServeHTTP(logoutResp, logoutReq)
	if logoutResp.Code != http.StatusOK {
		t.Fatalf("logout status = %d, want %d body=%s", logoutResp.Code, http.StatusOK, logoutResp.Body.String())
	}
	cleared := findResponseCookie(t, logoutResp.Result().Cookies(), oidcSessionCookieName)
	if cleared.MaxAge != -1 {
		t.Fatalf("cleared session max age = %d, want -1", cleared.MaxAge)
	}
	var logoutPayload AuthLogoutResponse
	if err := json.Unmarshal(logoutResp.Body.Bytes(), &logoutPayload); err != nil {
		t.Fatalf("unmarshal logout response: %v", err)
	}
	if logoutPayload.RedirectURL != "/" {
		t.Fatalf("logout redirect url = %q, want /", logoutPayload.RedirectURL)
	}

	events, err := store.ListAuditEvents(storage.AuditEventFilter{Action: "auth.oidc.logout", Limit: 10})
	if err != nil {
		t.Fatalf("ListAuditEvents returned error: %v", err)
	}
	if len(events) == 0 || events[0].ActorSubject != "alice" {
		t.Fatalf("logout audit events = %#v, want oidc logout for alice", events)
	}
}

func TestOIDCLogoutRejectsMissingCSRFToken(t *testing.T) {
	provider := newFakeOIDCProvider(t)
	defer provider.Close()
	t.Setenv("APEXLOAD_OIDC_CLIENT_SECRET", "test-client-secret")
	t.Setenv("APEXLOAD_AUTH_SESSION_SECRET", "test-session-secret")

	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	enableOIDCAuth(t, server, provider.Issuer)

	sessionCookie := issueOIDCTestSessionCookie(t, server, "alice")

	logoutReq := httptest.NewRequest(http.MethodPost, oidcLogoutPath, nil)
	logoutReq.AddCookie(sessionCookie)
	logoutResp := httptest.NewRecorder()
	server.mux.ServeHTTP(logoutResp, logoutReq)

	if logoutResp.Code != http.StatusForbidden {
		t.Fatalf("logout status = %d, want %d body=%s", logoutResp.Code, http.StatusForbidden, logoutResp.Body.String())
	}
}

func TestHandleGetAuthStatusReportsExpiredOIDCSession(t *testing.T) {
	provider := newFakeOIDCProvider(t)
	defer provider.Close()
	t.Setenv("APEXLOAD_OIDC_CLIENT_SECRET", "test-client-secret")
	t.Setenv("APEXLOAD_AUTH_SESSION_SECRET", "test-session-secret")

	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	enableOIDCAuth(t, server, provider.Issuer)
	expiredCookie := issueOIDCTestSessionCookieWithExpiry(t, server, "alice", time.Now().UTC().Add(-time.Minute))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.AddCookie(expiredCookie)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var payload AuthStatusResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal auth status: %v", err)
	}
	if payload.Authenticated {
		t.Fatalf("expected expired session to be unauthenticated, got %#v", payload)
	}
	if payload.SessionStatus != "expired" {
		t.Fatalf("session status = %q, want expired", payload.SessionStatus)
	}
	if payload.SessionExpiresAt == "" {
		t.Fatalf("session expires at should remain visible for expired sessions")
	}
}

func TestHandleGetAuthStatusAppliesLocalOIDCAccess(t *testing.T) {
	provider := newFakeOIDCProvider(t)
	defer provider.Close()
	t.Setenv("APEXLOAD_OIDC_CLIENT_SECRET", "test-client-secret")
	t.Setenv("APEXLOAD_AUTH_SESSION_SECRET", "test-session-secret")

	cases := []struct {
		name       string
		subject    string
		seed       func(*storage.SQLiteStore, time.Time)
		session    oidcSessionToken
		wantStatus string
	}{
		{
			name:    "not onboarded",
			subject: "alice",
			seed: func(store *storage.SQLiteStore, now time.Time) {
				if err := store.SaveUser(&storage.User{ID: "seed-user", Subject: "seed", Active: true, CreatedAt: now, UpdatedAt: now}); err != nil {
					t.Fatalf("SaveUser seed: %v", err)
				}
			},
			wantStatus: "not_onboarded",
		},
		{
			name:    "disabled",
			subject: "alice",
			seed: func(store *storage.SQLiteStore, now time.Time) {
				disabledAt := now
				if err := store.SaveUser(&storage.User{ID: "user-alice", Subject: "alice", Active: false, DisabledAt: &disabledAt, CreatedAt: now, UpdatedAt: now}); err != nil {
					t.Fatalf("SaveUser disabled alice: %v", err)
				}
			},
			wantStatus: "disabled",
		},
		{
			name:    "revoked",
			subject: "alice",
			seed: func(store *storage.SQLiteStore, now time.Time) {
				if err := store.SaveUser(&storage.User{ID: "user-alice", Subject: "alice", Active: true, SessionEpoch: 2, CreatedAt: now, UpdatedAt: now}); err != nil {
					t.Fatalf("SaveUser revoked alice: %v", err)
				}
				if err := store.SaveUserRoleBinding(&storage.UserRoleBinding{ID: "alice-admin", UserID: "user-alice", ScopeType: "global", Role: RoleSystemAdmin, CreatedAt: now, UpdatedAt: now}); err != nil {
					t.Fatalf("SaveUserRoleBinding alice: %v", err)
				}
			},
			session:    oidcSessionToken{SessionEpoch: 1},
			wantStatus: "revoked",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
			if err != nil {
				t.Fatalf("NewSQLiteStore returned error: %v", err)
			}
			defer store.Close()
			now := time.Now().UTC()
			tc.seed(store, now)

			server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
			enableOIDCAuth(t, server, provider.Issuer)
			session := tc.session
			session.Subject = tc.subject
			session.GlobalRoles = []string{RoleSystemAdmin}
			session.IssuedAt = now
			session.ExpiresAt = now.Add(time.Hour)
			cookie := issueOIDCTestSessionCookieForToken(t, server, session)

			statusReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
			statusReq.AddCookie(cookie)
			statusResp := httptest.NewRecorder()
			server.mux.ServeHTTP(statusResp, statusReq)
			if statusResp.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 body=%s", statusResp.Code, statusResp.Body.String())
			}
			var payload AuthStatusResponse
			if err := json.Unmarshal(statusResp.Body.Bytes(), &payload); err != nil {
				t.Fatalf("unmarshal auth status: %v", err)
			}
			if payload.Authenticated {
				t.Fatalf("authenticated = true, want false payload=%#v", payload)
			}
			if payload.SessionStatus != tc.wantStatus {
				t.Fatalf("session status = %q, want %q", payload.SessionStatus, tc.wantStatus)
			}
			if strings.TrimSpace(payload.LogoutCSRF) == "" {
				t.Fatalf("blocked oidc status should keep logout csrf available: %#v", payload)
			}

			protectedReq := httptest.NewRequest(http.MethodGet, "/api/v1/system/settings", nil)
			protectedReq.AddCookie(cookie)
			protectedResp := httptest.NewRecorder()
			server.mux.ServeHTTP(protectedResp, protectedReq)
			if protectedResp.Code == http.StatusOK {
				t.Fatalf("protected route status = 200, want denied for %s", tc.wantStatus)
			}
		})
	}
}

func TestOIDCSessionCannotTransferToReplacementUserWithReusedSubject(t *testing.T) {
	provider := newFakeOIDCProvider(t)
	defer provider.Close()
	t.Setenv("APEXLOAD_OIDC_CLIENT_SECRET", "test-client-secret")
	t.Setenv("APEXLOAD_AUTH_SESSION_SECRET", "test-session-secret")

	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "subject-reuse.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	original := &storage.User{ID: "original-user", Subject: "reused-subject", Active: true, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveUser(original); err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableOIDCAuth(t, server, provider.Issuer)
	oldCookie := issueOIDCTestSessionCookie(t, server, original.Subject)

	if err := store.DeleteUser(original.ID); err != nil {
		t.Fatal(err)
	}
	replacement := &storage.User{ID: "replacement-user", Subject: original.Subject, Active: true, CreatedAt: now.Add(time.Minute), UpdatedAt: now.Add(time.Minute)}
	if err := store.SaveUser(replacement); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveUserRoleBinding(&storage.UserRoleBinding{ID: "replacement-admin", UserID: replacement.ID, ScopeType: "global", Role: RoleSystemAdmin, CreatedAt: replacement.CreatedAt, UpdatedAt: replacement.UpdatedAt}); err != nil {
		t.Fatal(err)
	}

	protectedReq := httptest.NewRequest(http.MethodGet, "/api/v1/system/settings", nil)
	protectedReq.AddCookie(oldCookie)
	protectedResp := httptest.NewRecorder()
	server.mux.ServeHTTP(protectedResp, protectedReq)
	if protectedResp.Code != http.StatusUnauthorized {
		t.Fatalf("replacement inherited prior user's session: status=%d body=%s", protectedResp.Code, protectedResp.Body.String())
	}
}

func TestOIDCCallbackFailureIsAuditedWithoutSensitiveMaterial(t *testing.T) {
	provider := newFakeOIDCProvider(t)
	defer provider.Close()
	t.Setenv("APEXLOAD_OIDC_CLIENT_SECRET", "test-client-secret")
	t.Setenv("APEXLOAD_AUTH_SESSION_SECRET", "test-session-secret")

	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableOIDCAuth(t, server, provider.Issuer)

	loginReq := httptest.NewRequest(http.MethodGet, oidcLoginPath, nil)
	loginResp := httptest.NewRecorder()
	server.mux.ServeHTTP(loginResp, loginReq)
	stateCookie := findResponseCookie(t, loginResp.Result().Cookies(), oidcStateCookieName)

	callbackReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/callback?state=wrong-state&code=secret-code", nil)
	callbackReq.AddCookie(stateCookie)
	callbackResp := httptest.NewRecorder()
	server.mux.ServeHTTP(callbackResp, callbackReq)

	if callbackResp.Code != http.StatusUnauthorized {
		t.Fatalf("callback status = %d, want %d body=%s", callbackResp.Code, http.StatusUnauthorized, callbackResp.Body.String())
	}
	events, err := store.ListAuditEvents(storage.AuditEventFilter{Action: "auth.oidc.callback", Limit: 10})
	if err != nil {
		t.Fatalf("ListAuditEvents returned error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("audit events = %#v, want one callback failure", events)
	}
	if events[0].Status != "failure" {
		t.Fatalf("audit status = %q, want failure", events[0].Status)
	}
	if events[0].Details["reason"] != "state_mismatch" {
		t.Fatalf("audit details = %#v, want state_mismatch reason", events[0].Details)
	}
	if _, ok := events[0].Details["code"]; ok {
		t.Fatalf("audit details leaked code: %#v", events[0].Details)
	}
}

func TestHandleGetAuthAccessMatrixReturnsRBACVersionWhenOIDCEnabled(t *testing.T) {
	provider := newFakeOIDCProvider(t)
	defer provider.Close()
	t.Setenv("APEXLOAD_OIDC_CLIENT_SECRET", "test-client-secret")
	t.Setenv("APEXLOAD_AUTH_SESSION_SECRET", "test-session-secret")

	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	enableOIDCAuth(t, server, provider.Issuer)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/access-matrix", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var payload AuthAccessMatrixResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal auth access matrix: %v", err)
	}
	if payload.Version != "rbac_v1" {
		t.Fatalf("version = %q, want rbac_v1", payload.Version)
	}
}

func enableOIDCAuth(t *testing.T, server *Server, issuerURL string) {
	t.Helper()
	settings := server.runner.GetSystemSettings()
	settings.Auth.Mode = authModeOIDC
	settings.Auth.ProviderName = "Test OIDC"
	settings.Auth.IssuerURL = issuerURL
	settings.Auth.ClientID = "apexload-dashboard"
	settings.Auth.ClientSecretEnvVar = "APEXLOAD_OIDC_CLIENT_SECRET"
	settings.Auth.RedirectURL = "http://127.0.0.1/api/v1/auth/callback"
	settings.Auth.SessionSecretEnvVar = "APEXLOAD_AUTH_SESSION_SECRET"
	settings.Auth.Scopes = []string{"openid", "profile", "email"}
	settings.Auth.RolesClaim = "roles"
	settings.Auth.ProjectRolesClaim = "project_roles"
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}
}

func issueOIDCTestSessionCookie(t *testing.T, server *Server, subject string) *http.Cookie {
	t.Helper()
	return issueOIDCTestSessionCookieWithExpiry(t, server, subject, time.Now().UTC().Add(time.Hour))
}

func issueOIDCTestSessionCookieWithExpiry(t *testing.T, server *Server, subject string, expiresAt time.Time) *http.Cookie {
	t.Helper()
	return issueOIDCTestSessionCookieForToken(t, server, oidcSessionToken{
		Subject:   subject,
		IssuedAt:  time.Now().UTC(),
		ExpiresAt: expiresAt,
	})
}

func issueOIDCTestSessionCookieForToken(t *testing.T, server *Server, session oidcSessionToken) *http.Cookie {
	t.Helper()
	if strings.TrimSpace(session.UserID) == "" && server.store != nil && strings.TrimSpace(session.Subject) != "" {
		if local, err := server.store.GetUserBySubject(session.Subject); err == nil && local != nil {
			session.UserID = local.ID
		}
	}
	settings := normalizeAuthSettings(server.runner.GetSystemSettings().Auth)
	encodedSession, err := encodeSignedCookieValue(session, strings.TrimSpace(getenvOrFatal(t, settings.SessionSecretEnvVar)))
	if err != nil {
		t.Fatalf("encodeSignedCookieValue returned error: %v", err)
	}
	return &http.Cookie{Name: oidcSessionCookieName, Value: encodedSession, Path: "/"}
}

func getenvOrFatal(t *testing.T, key string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		t.Fatalf("env %s is not set", key)
	}
	return value
}

type fakeOIDCProvider struct {
	Issuer         string
	server         *httptest.Server
	key            *rsa.PrivateKey
	keyID          string
	discoveryCalls atomic.Int64
}

func newFakeOIDCProvider(t *testing.T) *fakeOIDCProvider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	provider := &fakeOIDCProvider{key: key, keyID: "test-key"}
	mux := http.NewServeMux()
	provider.server = httptest.NewServer(mux)
	provider.Issuer = provider.server.URL

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		provider.discoveryCalls.Add(1)
		writeJSON(w, http.StatusOK, map[string]any{
			"issuer":                                provider.Issuer,
			"authorization_endpoint":                provider.Issuer + "/authorize",
			"token_endpoint":                        provider.Issuer + "/token",
			"jwks_uri":                              provider.Issuer + "/jwks",
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		jwk := jose.JSONWebKey{Key: &provider.key.PublicKey, KeyID: provider.keyID, Algorithm: string(jose.RS256), Use: "sig"}
		writeJSON(w, http.StatusOK, map[string]any{"keys": []any{jwk}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			writeError(w, http.StatusBadRequest, "invalid token request")
			return
		}
		nonce := strings.TrimSpace(r.Form.Get("code"))
		if nonce == "" {
			writeError(w, http.StatusBadRequest, "code is required")
			return
		}
		idToken, err := provider.signIDToken(nonce)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"access_token": "access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
			"id_token":     idToken,
		})
	})

	return provider
}

func (p *fakeOIDCProvider) Close() {
	if p != nil && p.server != nil {
		p.server.Close()
	}
}

func (p *fakeOIDCProvider) signIDToken(nonce string) (string, error) {
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: p.key}, (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", p.keyID))
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	return jwt.Signed(signer).Claims(jwt.Claims{
		Issuer:   p.Issuer,
		Subject:  "alice",
		Audience: jwt.Audience{"apexload-dashboard"},
		IssuedAt: jwt.NewNumericDate(now),
		Expiry:   jwt.NewNumericDate(now.Add(time.Hour)),
	}).Claims(map[string]any{
		"nonce": nonce,
		"name":  "Alice Admin",
		"email": "alice@example.com",
		"roles": []string{RoleSystemAdmin, RoleViewer},
		"project_roles": map[string][]string{
			"project-a": {RoleEditor},
		},
	}).CompactSerialize()
}

func oidcCallbackURL(code string, state string) string {
	values := url.Values{}
	values.Set("code", code)
	values.Set("state", state)
	return "/api/v1/auth/callback?" + values.Encode()
}

func findResponseCookie(t *testing.T, cookies []*http.Cookie, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range cookies {
		if cookie != nil && cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("cookie %q not found in response", name)
	return nil
}
