package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
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
	body, _ := json.Marshal(cfg)
	if string(body) == "" {
		t.Fatal("empty config")
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
