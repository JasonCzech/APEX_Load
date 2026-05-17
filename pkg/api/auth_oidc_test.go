package api

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v3"
	"github.com/go-jose/go-jose/v3/jwt"
	"github.com/jasonczech/stormrunner/pkg/storage"
)

func TestHandleGetAuthStatusReturnsOIDCLoginAndLogoutURLs(t *testing.T) {
	provider := newFakeOIDCProvider(t)
	defer provider.Close()
	t.Setenv("STORMRUNNER_OIDC_CLIENT_SECRET", "test-client-secret")
	t.Setenv("STORMRUNNER_AUTH_SESSION_SECRET", "test-session-secret")

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
}

func TestHandleGetAuthDiagnosticsReturnsHealthyOIDCReadiness(t *testing.T) {
	provider := newFakeOIDCProvider(t)
	defer provider.Close()
	t.Setenv("STORMRUNNER_OIDC_CLIENT_SECRET", "test-client-secret")
	t.Setenv("STORMRUNNER_AUTH_SESSION_SECRET", "0123456789abcdef0123456789abcdef")

	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	enableOIDCAuth(t, server, provider.Issuer)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/diagnostics", nil)
	req.Header.Set("X-Auth-Request-User", "alice")
	req.Header.Set("X-Stormrunner-Roles", RoleSystemAdmin)
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
	t.Setenv("STORMRUNNER_OIDC_CLIENT_SECRET", "test-client-secret")
	t.Setenv("STORMRUNNER_AUTH_SESSION_SECRET", "test-session-secret")

	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "stormrunner.db"))
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

	protectedReq := httptest.NewRequest(http.MethodGet, "/api/v1/system/settings", nil)
	protectedReq.AddCookie(sessionCookie)
	protectedResp := httptest.NewRecorder()
	server.mux.ServeHTTP(protectedResp, protectedReq)
	if protectedResp.Code != http.StatusOK {
		t.Fatalf("protected route status = %d, want %d body=%s", protectedResp.Code, http.StatusOK, protectedResp.Body.String())
	}

	logoutReq := httptest.NewRequest(http.MethodGet, oidcLogoutPath, nil)
	logoutReq.AddCookie(sessionCookie)
	logoutResp := httptest.NewRecorder()
	server.mux.ServeHTTP(logoutResp, logoutReq)
	if logoutResp.Code != http.StatusSeeOther {
		t.Fatalf("logout status = %d, want %d body=%s", logoutResp.Code, http.StatusSeeOther, logoutResp.Body.String())
	}
	cleared := findResponseCookie(t, logoutResp.Result().Cookies(), oidcSessionCookieName)
	if cleared.MaxAge != -1 {
		t.Fatalf("cleared session max age = %d, want -1", cleared.MaxAge)
	}

	events, err := store.ListAuditEvents(storage.AuditEventFilter{Action: "auth.oidc.logout", Limit: 10})
	if err != nil {
		t.Fatalf("ListAuditEvents returned error: %v", err)
	}
	if len(events) == 0 || events[0].ActorSubject != "alice" {
		t.Fatalf("logout audit events = %#v, want oidc logout for alice", events)
	}
}

func TestHandleGetAuthAccessMatrixReturnsRBACVersionWhenOIDCEnabled(t *testing.T) {
	provider := newFakeOIDCProvider(t)
	defer provider.Close()
	t.Setenv("STORMRUNNER_OIDC_CLIENT_SECRET", "test-client-secret")
	t.Setenv("STORMRUNNER_AUTH_SESSION_SECRET", "test-session-secret")

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
	settings.Auth.ClientID = "stormrunner-dashboard"
	settings.Auth.ClientSecretEnvVar = "STORMRUNNER_OIDC_CLIENT_SECRET"
	settings.Auth.RedirectURL = "http://stormrunner.example.test/api/v1/auth/callback"
	settings.Auth.SessionSecretEnvVar = "STORMRUNNER_AUTH_SESSION_SECRET"
	settings.Auth.Scopes = []string{"openid", "profile", "email"}
	settings.Auth.RolesClaim = "roles"
	settings.Auth.ProjectRolesClaim = "project_roles"
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}
}

type fakeOIDCProvider struct {
	Issuer string
	server *httptest.Server
	key    *rsa.PrivateKey
	keyID  string
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
		Audience: jwt.Audience{"stormrunner-dashboard"},
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
