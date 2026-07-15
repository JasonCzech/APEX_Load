package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// newGitHubStub returns an httptest server that mimics the GHES OAuth token
// endpoint and the /user identity endpoint.
func newGitHubStub(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/login/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "gho_testtoken", "token_type": "bearer", "scope": "repo",
		})
	})
	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer gho_testtoken" {
			http.Error(w, "bad token", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"login": "octocat", "id": 583231})
	})
	return httptest.NewServer(mux)
}

func newGitHubTestServer(t *testing.T, stubURL string) *Server {
	t.Helper()
	t.Setenv(githubAllowInsecureHTTPEnv, "true")
	t.Setenv("APEXLOAD_GITHUB_TOKEN_KEY", "unit-test-encryption-key")
	t.Setenv("APEXLOAD_TEST_GH_SECRET", "client-secret")

	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.SaveProject(&storage.Project{ID: "team-a", Name: "Team A", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	server := NewServer(ServerConfig{Store: store})

	settings := server.runner.GetSystemSettings()
	settings.GitHub = GitHubSettings{
		Enabled:            true,
		ClientID:           "client-id",
		ClientSecretEnvVar: "APEXLOAD_TEST_GH_SECRET",
		APIBaseURL:         stubURL,
		WebBaseURL:         stubURL,
		Scopes:             []string{"repo"},
		DefaultBranch:      "main",
	}
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings: %v", err)
	}
	return server
}

func TestGitHubOAuthConnectFlow(t *testing.T) {
	stub := newGitHubStub(t)
	defer stub.Close()
	server := newGitHubTestServer(t, stub.URL)

	// --- login: expect redirect to the authorize endpoint + a state cookie ---
	loginReq := httptest.NewRequest(http.MethodGet, "/api/v1/integrations/github/login", nil)
	loginResp := httptest.NewRecorder()
	server.mux.ServeHTTP(loginResp, loginReq)
	if loginResp.Code != http.StatusFound {
		t.Fatalf("login status = %d, want 302; body=%s", loginResp.Code, loginResp.Body.String())
	}
	location := loginResp.Header().Get("Location")
	parsed, err := url.Parse(location)
	if err != nil {
		t.Fatalf("parse login redirect %q: %v", location, err)
	}
	state := parsed.Query().Get("state")
	if state == "" || parsed.Query().Get("client_id") != "client-id" {
		t.Fatalf("unexpected authorize redirect: %s", location)
	}
	var stateCookie *http.Cookie
	for _, c := range loginResp.Result().Cookies() {
		if c.Name == githubStateCookieName {
			stateCookie = c
		}
	}
	if stateCookie == nil {
		t.Fatal("login did not set the github state cookie")
	}

	// --- callback: exchange code, fetch identity, persist connection ---
	cbReq := httptest.NewRequest(http.MethodGet, "/api/v1/integrations/github/callback?code=abc123&state="+state, nil)
	cbReq.AddCookie(stateCookie)
	cbResp := httptest.NewRecorder()
	server.mux.ServeHTTP(cbResp, cbReq)
	if cbResp.Code != http.StatusFound {
		t.Fatalf("callback status = %d, want 302; body=%s", cbResp.Code, cbResp.Body.String())
	}

	// --- connection: should report connected as octocat ---
	connResp := doGitHubJSON(t, server, http.MethodGet, "/api/v1/integrations/github/connection")
	if connResp["connected"] != true || connResp["login"] != "octocat" {
		t.Fatalf("unexpected connection: %#v", connResp)
	}

	// --- the persisted access token must be encrypted, not plaintext ---
	ghStore := server.store.(storage.GitHubConnectionStore)
	stored, err := ghStore.GetGitHubConnection(githubUserKey(cbReq))
	if err != nil || stored == nil {
		t.Fatalf("expected stored connection, got (%v, %v)", stored, err)
	}
	if stored.AccessTokenEnc == "" || stored.AccessTokenEnc == "gho_testtoken" {
		t.Fatalf("access token should be stored encrypted, got %q", stored.AccessTokenEnc)
	}
	dec, err := decryptGitHubToken(stored.AccessTokenEnc)
	if err != nil || dec != "gho_testtoken" {
		t.Fatalf("decrypt stored token = (%q, %v), want gho_testtoken", dec, err)
	}

	// --- disconnect ---
	disc := doGitHubJSON(t, server, http.MethodDelete, "/api/v1/integrations/github/connection")
	if disc["disconnected"] != true {
		t.Fatalf("disconnect response = %#v", disc)
	}
	after := doGitHubJSON(t, server, http.MethodGet, "/api/v1/integrations/github/connection")
	if after["connected"] != false {
		t.Fatalf("expected connected=false after disconnect, got %#v", after)
	}
}

func TestGitHubOAuthCallbackCannotUndoNewerDisconnect(t *testing.T) {
	stub := newGitHubStub(t)
	defer stub.Close()
	server := newGitHubTestServer(t, stub.URL)

	loginReq := httptest.NewRequest(http.MethodGet, "/api/v1/integrations/github/login", nil)
	loginResp := httptest.NewRecorder()
	server.mux.ServeHTTP(loginResp, loginReq)
	location, err := url.Parse(loginResp.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	state := location.Query().Get("state")
	var stateCookie *http.Cookie
	for _, cookie := range loginResp.Result().Cookies() {
		if cookie.Name == githubStateCookieName {
			stateCookie = cookie
			break
		}
	}
	if state == "" || stateCookie == nil {
		t.Fatal("login did not issue github oauth state")
	}
	if err := server.store.(storage.GitHubConnectionStore).DeleteGitHubConnection("local"); err != nil {
		t.Fatal(err)
	}

	callbackReq := httptest.NewRequest(http.MethodGet, "/api/v1/integrations/github/callback?code=abc123&state="+state, nil)
	callbackReq.AddCookie(stateCookie)
	callbackResp := httptest.NewRecorder()
	server.mux.ServeHTTP(callbackResp, callbackReq)
	if callbackResp.Code != http.StatusConflict {
		t.Fatalf("callback after disconnect = %d, want %d; body=%s", callbackResp.Code, http.StatusConflict, callbackResp.Body.String())
	}
	connection, err := server.store.(storage.GitHubConnectionStore).GetGitHubConnection("local")
	if err != nil || connection != nil {
		t.Fatalf("callback recreated disconnected connection: %#v, %v", connection, err)
	}
}

func TestGitHubCallbackURLIgnoresInvalidForwardedProto(t *testing.T) {
	server := NewServer(ServerConfig{})
	req := httptest.NewRequest(http.MethodGet, "http://apexload.example.test/api/v1/integrations/github/login", nil)
	req.Header.Set("X-Forwarded-Proto", "javascript")

	if got, want := server.githubCallbackURL(req), "http://apexload.example.test/api/v1/integrations/github/callback"; got != want {
		t.Fatalf("callback URL = %q, want %q", got, want)
	}
}

func TestGitHubCallbackURLUsesOnlyHTTPSForwardedProtoInProduction(t *testing.T) {
	t.Setenv("APEXLOAD_ENV", "production")
	server := NewServer(ServerConfig{})
	req := httptest.NewRequest(http.MethodGet, "http://apexload.example.test/api/v1/integrations/github/login", nil)
	req.Header.Set("X-Forwarded-Proto", "http")

	if got, want := server.githubCallbackURL(req), "https://apexload.example.test/api/v1/integrations/github/callback"; got != want {
		t.Fatalf("callback URL = %q, want %q", got, want)
	}

	req.Header.Set("X-Forwarded-Proto", "https, http")
	if got, want := server.githubCallbackURL(req), "https://apexload.example.test/api/v1/integrations/github/callback"; got != want {
		t.Fatalf("callback URL with forwarded chain = %q, want %q", got, want)
	}
}

func TestGitHubLoginRequiresConfiguration(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store}) // github disabled by default

	req := httptest.NewRequest(http.MethodGet, "/api/v1/integrations/github/login", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("login with github disabled = %d, want 404", resp.Code)
	}
}

func TestGitHubConfigEndpointHidesSecret(t *testing.T) {
	stub := newGitHubStub(t)
	defer stub.Close()
	server := newGitHubTestServer(t, stub.URL)

	cfg := doGitHubJSON(t, server, http.MethodGet, "/api/v1/integrations/github/config")
	if cfg["oauth_configured"] != true || cfg["enabled"] != true {
		t.Fatalf("config = %#v", cfg)
	}
	if _, leaked := cfg["client_secret"]; leaked {
		t.Fatal("config endpoint must never expose the client secret")
	}
	for _, internalField := range []string{"app_private_key_env_var", "app_private_key_file", "webhook_secret_env_var", "pat_env_var", "tls_insecure_skip_verify"} {
		if _, leaked := cfg[internalField]; leaked {
			t.Fatalf("config endpoint exposed internal field %q", internalField)
		}
	}
	body, _ := json.Marshal(cfg)
	if string(body) == "" {
		t.Fatal("empty config")
	}
}

func TestGitHubOAuthCallbackRejectsChangedLocalIdentity(t *testing.T) {
	stub := newGitHubStub(t)
	defer stub.Close()
	server := newGitHubTestServer(t, stub.URL)

	now := time.Now().UTC()
	encoded, err := encodeSignedCookieValue(githubStateToken{
		State: "state-token", UserID: "victim", ReturnTo: "/", IssuedAt: now, ExpiresAt: now.Add(time.Minute),
	}, githubCookieSecret())
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/integrations/github/callback?code=unused&state=state-token", nil)
	req.AddCookie(&http.Cookie{Name: githubStateCookieName, Value: encoded})
	req = withAuthenticatedContext(req, authResolution{User: &AuthenticatedUser{Subject: "attacker"}})
	resp := httptest.NewRecorder()
	server.handleGitHubCallback(resp, req)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("callback status = %d, want 401; body=%s", resp.Code, resp.Body.String())
	}
	connection, err := server.store.(storage.GitHubConnectionStore).GetGitHubConnection("victim")
	if err != nil || connection != nil {
		t.Fatalf("callback persisted victim connection = %#v, %v", connection, err)
	}
}

func TestDecodeGitHubOAuthTokenResponseEnforcesBounds(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
	}{
		{name: "http error with token-shaped body", statusCode: http.StatusBadGateway, body: `{"access_token":"must-not-be-accepted"}`},
		{name: "oversized token", statusCode: http.StatusOK, body: `{"access_token":"` + strings.Repeat("x", githubOAuthTokenMaxBytes+1) + `"}`},
		{name: "control character metadata", statusCode: http.StatusOK, body: `{"access_token":"token","scope":"repo\nadmin"}`},
		{name: "negative expiry", statusCode: http.StatusOK, body: `{"access_token":"token","expires_in":-1}`},
		{name: "excessive expiry", statusCode: http.StatusOK, body: `{"access_token":"token","expires_in":` + fmt.Sprint(int64(githubOAuthMaxTokenTTL/time.Second)+1) + `}`},
		{name: "oversized response", statusCode: http.StatusOK, body: strings.Repeat("x", githubOAuthResponseMaxBytes+1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resp := &http.Response{
				StatusCode: test.statusCode,
				Body:       io.NopCloser(strings.NewReader(test.body)),
			}
			if _, err := decodeGitHubOAuthTokenResponse(resp, "test exchange"); err == nil {
				t.Fatal("unsafe OAuth token response was accepted")
			}
		})
	}
}

func TestGitHubOAuthExpiryIsBoundedWithoutDurationOverflow(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	want := now.Add(githubOAuthMaxTokenTTL)
	got, err := githubOAuthExpiresAt(now, int64(githubOAuthMaxTokenTTL/time.Second))
	if err != nil || got == nil || !got.Equal(want) {
		t.Fatalf("maximum allowed expiry = (%v, %v), want %v", got, err, want)
	}
	if _, err := githubOAuthExpiresAt(now, int64(githubOAuthMaxTokenTTL/time.Second)+1); err == nil {
		t.Fatal("excessive OAuth token expiry was accepted")
	}
	if got, err := githubOAuthExpiresAt(now, 0); err != nil || got != nil {
		t.Fatalf("non-expiring token expiry = (%v, %v), want nil,nil", got, err)
	}
}

func doGitHubJSON(t *testing.T, server *Server, method, path string) map[string]any {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("%s %s status = %d, body=%s", method, path, resp.Code, resp.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %s %s: %v", method, path, err)
	}
	return out
}
