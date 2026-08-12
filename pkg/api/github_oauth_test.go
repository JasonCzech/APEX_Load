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
	"sync"
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

func TestDesktopGitHubOAuthUsesCapabilityStateWithoutCookies(t *testing.T) {
	stub := newGitHubStub(t)
	defer stub.Close()
	server := newGitHubTestServer(t, stub.URL)
	server.desktopMode = true
	server.desktopCapability = "native-renderer-secret"
	server.setBoundAddr("127.0.0.1:54321")
	const browserCapability = "browser-tab-secret"
	server.desktopBootstrapMu.Lock()
	server.desktopBrowserCapabilities[browserCapability] = desktopBrowserCapability{issuedAt: time.Now().UTC()}
	server.desktopBootstrapMu.Unlock()

	loginReq := httptest.NewRequest(http.MethodGet, "/api/v1/integrations/github/login?return_to=/#/settings", nil)
	loginReq.Header.Set(DesktopCapabilityHeader, browserCapability)
	loginReq.Header.Set("Accept", "application/json")
	loginResp := httptest.NewRecorder()
	server.mux.ServeHTTP(loginResp, loginReq)
	if loginResp.Code != http.StatusOK {
		t.Fatalf("desktop github login = %d body=%s", loginResp.Code, loginResp.Body.String())
	}
	if cookies := loginResp.Result().Cookies(); len(cookies) != 0 {
		t.Fatalf("desktop github login emitted host-scoped cookies: %#v", cookies)
	}
	var navigation struct {
		RedirectURL string `json:"redirect_url"`
	}
	if err := json.Unmarshal(loginResp.Body.Bytes(), &navigation); err != nil {
		t.Fatal(err)
	}
	authorizeURL, err := url.Parse(navigation.RedirectURL)
	if err != nil {
		t.Fatal(err)
	}
	state := authorizeURL.Query().Get("state")
	callbackURL := "/api/v1/integrations/github/callback?code=abc123&state=" + url.QueryEscape(state)

	landingReq := httptest.NewRequest(http.MethodGet, callbackURL, nil)
	landingResp := httptest.NewRecorder()
	server.mux.ServeHTTP(landingResp, landingReq)
	if landingResp.Code != http.StatusOK || !strings.Contains(landingResp.Body.String(), `method:"POST"`) {
		t.Fatalf("desktop github landing = %d body=%s", landingResp.Code, landingResp.Body.String())
	}
	if connection, err := server.store.(storage.GitHubConnectionStore).GetGitHubConnection("local"); err != nil || connection != nil {
		t.Fatalf("GET callback mutated github connection: %#v, %v", connection, err)
	}

	missingReq := httptest.NewRequest(http.MethodPost, callbackURL, nil)
	missingResp := httptest.NewRecorder()
	server.mux.ServeHTTP(missingResp, missingReq)
	if missingResp.Code != http.StatusUnauthorized {
		t.Fatalf("github callback without capability = %d, want 401", missingResp.Code)
	}

	callbackReq := httptest.NewRequest(http.MethodPost, callbackURL, nil)
	callbackReq.Header.Set(DesktopCapabilityHeader, browserCapability)
	callbackResp := httptest.NewRecorder()
	server.mux.ServeHTTP(callbackResp, callbackReq)
	if callbackResp.Code != http.StatusOK {
		t.Fatalf("desktop github callback = %d body=%s", callbackResp.Code, callbackResp.Body.String())
	}
	if cookies := callbackResp.Result().Cookies(); len(cookies) != 0 {
		t.Fatalf("desktop github callback emitted host-scoped cookies: %#v", cookies)
	}
	connection, err := server.store.(storage.GitHubConnectionStore).GetGitHubConnection("local")
	if err != nil || connection == nil || connection.GitHubLogin != "octocat" {
		t.Fatalf("desktop github connection = %#v, %v", connection, err)
	}
}

func TestDesktopGitHubOAuthCallbackRestoresLoginPreviewAfterBootstrapSwitch(t *testing.T) {
	stub := newGitHubStub(t)
	defer stub.Close()
	server := newGitHubTestServer(t, stub.URL)
	server.desktopMode = true
	server.desktopCapability = "native-renderer-secret"
	server.setBoundAddr("127.0.0.1:54321")
	const browserCapability = "dev-user-browser-tab"
	server.desktopBootstrapMu.Lock()
	server.desktopBrowserCapabilities[browserCapability] = desktopBrowserCapability{
		issuedAt: time.Now().UTC(), devViewMode: devViewModeAdmin,
	}
	server.desktopBootstrapMu.Unlock()

	// Recreate the two desktop wrappers because this test server was initially
	// built in hosted mode by the shared GitHub fixture.
	handler := desktopAuthMaterialMiddleware(server.desktopOIDCSessionForCapability, nil, server.desktopDevPreviewForCapability)(server.mux)
	handler = desktopCapabilityMiddleware(server.desktopCapability, nil, server.validDesktopBrowserCapability)(handler)

	loginReq := httptest.NewRequest(http.MethodGet, "/api/v1/integrations/github/login?return_to=/#/settings", nil)
	loginReq.Header.Set(DesktopCapabilityHeader, browserCapability)
	loginReq.Header.Set("Accept", "application/json")
	// The tab switched from its bootstrap Admin preview to a project-scoped User
	// preview before starting OAuth. The initiating request, not stale bootstrap
	// state, defines the local identity to which the GitHub token is attached.
	loginReq.Header.Set(devViewHeader, devViewModeUser)
	loginReq.Header.Set(devProjectIDHeader, "team-a")
	loginResp := httptest.NewRecorder()
	handler.ServeHTTP(loginResp, loginReq)
	if loginResp.Code != http.StatusOK {
		t.Fatalf("dev-user desktop github login = %d body=%s", loginResp.Code, loginResp.Body.String())
	}
	var navigation struct {
		RedirectURL string `json:"redirect_url"`
	}
	if err := json.Unmarshal(loginResp.Body.Bytes(), &navigation); err != nil {
		t.Fatal(err)
	}
	authorizeURL, err := url.Parse(navigation.RedirectURL)
	if err != nil {
		t.Fatal(err)
	}
	state := authorizeURL.Query().Get("state")
	server.desktopBootstrapMu.Lock()
	storedState := server.desktopGitHubStates[state].state
	boundPreview := server.desktopBrowserCapabilities[browserCapability]
	server.desktopBootstrapMu.Unlock()
	if storedState.UserID != "dev-user" {
		t.Fatalf("desktop GitHub state user = %q, want dev-user", storedState.UserID)
	}
	if boundPreview.devViewMode != devViewModeUser || boundPreview.projectID != "team-a" {
		t.Fatalf("desktop browser preview after login = (%q, %q), want (%q, %q)", boundPreview.devViewMode, boundPreview.projectID, devViewModeUser, "team-a")
	}

	// The real callback landing POST contains only this capability. It does not
	// resend localStorage preview headers, so the middleware must restore the
	// context captured when this OAuth flow started before comparing identities.
	callbackReq := httptest.NewRequest(http.MethodPost, "/api/v1/integrations/github/callback?code=abc123&state="+url.QueryEscape(state), nil)
	callbackReq.Header.Set(DesktopCapabilityHeader, browserCapability)
	callbackResp := httptest.NewRecorder()
	handler.ServeHTTP(callbackResp, callbackReq)
	if callbackResp.Code != http.StatusOK {
		t.Fatalf("dev-user desktop github callback = %d body=%s", callbackResp.Code, callbackResp.Body.String())
	}
	connection, err := server.store.(storage.GitHubConnectionStore).GetGitHubConnection("dev-user")
	if err != nil || connection == nil || connection.GitHubLogin != "octocat" {
		t.Fatalf("dev-user desktop github connection = %#v, %v", connection, err)
	}
	if local, err := server.store.(storage.GitHubConnectionStore).GetGitHubConnection("local"); err != nil || local != nil {
		t.Fatalf("desktop callback fell back to admin/local identity: %#v, %v", local, err)
	}
}

func TestDesktopGitHubOAuthNewerFlowSupersedesCapabilityState(t *testing.T) {
	server := NewServer(ServerConfig{DesktopMode: true, DesktopCapability: "native-renderer-secret"})
	const capability = "shared-browser-tab"
	server.desktopBootstrapMu.Lock()
	server.desktopBrowserCapabilities[capability] = desktopBrowserCapability{issuedAt: time.Now().UTC()}
	server.desktopBootstrapMu.Unlock()
	now := time.Now().UTC()
	first := githubStateToken{State: "first-state", UserID: "local", IssuedAt: now, ExpiresAt: now.Add(githubStateTTL)}
	second := githubStateToken{State: "second-state", UserID: "dev-user", IssuedAt: now.Add(time.Second), ExpiresAt: now.Add(githubStateTTL)}
	adminRequest := httptest.NewRequest(http.MethodGet, "/api/v1/integrations/github/login", nil)
	adminRequest.Header.Set(devViewHeader, devViewModeAdmin)
	userRequest := httptest.NewRequest(http.MethodGet, "/api/v1/integrations/github/login", nil)
	userRequest.Header.Set(devViewHeader, devViewModeUser)
	userRequest.Header.Set(devProjectIDHeader, "team-a")
	if stored, err := server.storeDesktopGitHubLoginState(capability, adminRequest, first); err != nil || !stored {
		t.Fatalf("store first desktop GitHub state = (%v, %v)", stored, err)
	}
	if stored, err := server.storeDesktopGitHubLoginState(capability, userRequest, second); err != nil || !stored {
		t.Fatalf("store second desktop GitHub state = (%v, %v)", stored, err)
	}
	if _, ok := server.takeDesktopGitHubState(capability, first.State); ok {
		t.Fatal("older GitHub OAuth state remained valid after a newer flow started for the same capability")
	}
	if got, ok := server.takeDesktopGitHubState(capability, second.State); !ok || got.UserID != second.UserID {
		t.Fatalf("newest GitHub OAuth state = (%+v, %v), want dev-user state", got, ok)
	}
}

func TestDesktopGitHubOAuthConcurrentStartsKeepPreviewAndStateAtomic(t *testing.T) {
	server := NewServer(ServerConfig{DesktopMode: true, DesktopCapability: "native-renderer-secret"})
	const capability = "duplicated-browser-tab"
	now := time.Now().UTC()

	for iteration := 0; iteration < 100; iteration++ {
		server.desktopBootstrapMu.Lock()
		server.desktopBrowserCapabilities[capability] = desktopBrowserCapability{issuedAt: now}
		server.desktopGitHubStates = make(map[string]desktopGitHubStateGrant)
		server.desktopBootstrapMu.Unlock()

		adminRequest := httptest.NewRequest(http.MethodGet, "/api/v1/integrations/github/login", nil)
		adminRequest.Header.Set(devViewHeader, devViewModeAdmin)
		userRequest := httptest.NewRequest(http.MethodGet, "/api/v1/integrations/github/login", nil)
		userRequest.Header.Set(devViewHeader, devViewModeUser)
		userRequest.Header.Set(devProjectIDHeader, "team-a")
		adminState := githubStateToken{State: fmt.Sprintf("admin-%d", iteration), UserID: "local", IssuedAt: now, ExpiresAt: now.Add(githubStateTTL)}
		userState := githubStateToken{State: fmt.Sprintf("user-%d", iteration), UserID: "dev-user", IssuedAt: now, ExpiresAt: now.Add(githubStateTTL)}

		start := make(chan struct{})
		results := make(chan error, 2)
		var wait sync.WaitGroup
		for _, flow := range []struct {
			request *http.Request
			state   githubStateToken
		}{{adminRequest, adminState}, {userRequest, userState}} {
			wait.Add(1)
			go func(request *http.Request, state githubStateToken) {
				defer wait.Done()
				<-start
				stored, err := server.storeDesktopGitHubLoginState(capability, request, state)
				if err != nil {
					results <- err
				} else if !stored {
					results <- fmt.Errorf("state %q was not stored", state.State)
				}
			}(flow.request, flow.state)
		}
		close(start)
		wait.Wait()
		close(results)
		for err := range results {
			t.Fatal(err)
		}

		server.desktopBootstrapMu.Lock()
		grant := server.desktopBrowserCapabilities[capability]
		if len(server.desktopGitHubStates) != 1 {
			server.desktopBootstrapMu.Unlock()
			t.Fatalf("iteration %d retained %d states, want 1", iteration, len(server.desktopGitHubStates))
		}
		var retained githubStateToken
		for _, stateGrant := range server.desktopGitHubStates {
			retained = stateGrant.state
		}
		server.desktopBootstrapMu.Unlock()
		if grant.devViewMode == devViewModeUser {
			if retained.UserID != "dev-user" || grant.projectID != "team-a" {
				t.Fatalf("iteration %d user preview/state mismatch: grant=%+v state=%+v", iteration, grant, retained)
			}
		} else if grant.devViewMode != devViewModeAdmin || retained.UserID != "local" || grant.projectID != "" {
			t.Fatalf("iteration %d admin preview/state mismatch: grant=%+v state=%+v", iteration, grant, retained)
		}
	}
}

func TestGitHubOAuthCallbackRetainsInitiatingOIDCIdentity(t *testing.T) {
	stub := newGitHubStub(t)
	defer stub.Close()
	server := newGitHubTestServer(t, stub.URL)
	t.Setenv("APEXLOAD_OIDC_CLIENT_SECRET", "test-client-secret")
	t.Setenv("APEXLOAD_AUTH_SESSION_SECRET", "test-session-secret")
	enableOIDCAuth(t, server, stub.URL)

	now := time.Now().UTC()
	if err := server.store.SaveUser(&storage.User{
		ID:        "user-alice",
		Subject:   "alice",
		Active:    true,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("save local OIDC user: %v", err)
	}
	sessionCookie := issueOIDCTestSessionCookie(t, server, "alice")

	loginReq := httptest.NewRequest(http.MethodGet, "/api/v1/integrations/github/login?return_to=/%23/settings", nil)
	loginReq.AddCookie(sessionCookie)
	loginResp := httptest.NewRecorder()
	server.mux.ServeHTTP(loginResp, loginReq)
	if loginResp.Code != http.StatusFound {
		t.Fatalf("OIDC-authenticated login status = %d, want 302; body=%s", loginResp.Code, loginResp.Body.String())
	}
	authorizeURL, err := url.Parse(loginResp.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse authorize URL: %v", err)
	}
	state := authorizeURL.Query().Get("state")
	stateCookie := findResponseCookie(t, loginResp.Result().Cookies(), githubStateCookieName)

	callbackReq := httptest.NewRequest(http.MethodGet, "/api/v1/integrations/github/callback?code=abc123&state="+url.QueryEscape(state), nil)
	callbackReq.AddCookie(sessionCookie)
	callbackReq.AddCookie(stateCookie)
	callbackResp := httptest.NewRecorder()
	server.mux.ServeHTTP(callbackResp, callbackReq)
	if callbackResp.Code != http.StatusFound {
		t.Fatalf("OIDC-authenticated callback status = %d, want 302; body=%s", callbackResp.Code, callbackResp.Body.String())
	}
	if got := callbackResp.Header().Get("Location"); got != "/#/settings" {
		t.Fatalf("callback return location = %q, want /#/settings", got)
	}
	connection, err := server.store.(storage.GitHubConnectionStore).GetGitHubConnection("alice")
	if err != nil || connection == nil || connection.GitHubLogin != "octocat" {
		t.Fatalf("OIDC user's GitHub connection = %#v, %v", connection, err)
	}
	if local, err := server.store.(storage.GitHubConnectionStore).GetGitHubConnection("local"); err != nil || local != nil {
		t.Fatalf("callback must not fall back to the synthetic local identity: %#v, %v", local, err)
	}
}

func TestGitHubOAuthLoginReturnsProviderURLForManagedBrowserNavigation(t *testing.T) {
	stub := newGitHubStub(t)
	defer stub.Close()
	server := newGitHubTestServer(t, stub.URL)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/integrations/github/login?return_to=/settings", nil)
	req.Header.Set("Accept", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("managed github login status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	if resp.Header().Get("Location") != "" {
		t.Fatalf("managed github login unexpectedly redirected to %q", resp.Header().Get("Location"))
	}
	var payload struct {
		RedirectURL string `json:"redirect_url"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(payload.RedirectURL)
	if err != nil || !strings.HasPrefix(payload.RedirectURL, stub.URL+"/login/oauth/authorize") || parsed.Query().Get("state") == "" {
		t.Fatalf("managed github redirect URL = %q, parse error %v", payload.RedirectURL, err)
	}
	if resp.Header().Get("Cache-Control") != "no-store" || resp.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("managed github login security headers = %#v", resp.Header())
	}
	var stateCookie *http.Cookie
	for _, cookie := range resp.Result().Cookies() {
		if cookie.Name == githubStateCookieName {
			stateCookie = cookie
			break
		}
	}
	if stateCookie == nil || stateCookie.Value == "" {
		t.Fatal("managed github login did not set the signed state cookie")
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

func TestGitHubCallbackURLUsesBoundDesktopLoopbackPortInProduction(t *testing.T) {
	t.Setenv("APEXLOAD_ENV", "production")
	server := NewServer(ServerConfig{DesktopMode: true, DesktopCapability: "native-renderer-secret", Addr: "127.0.0.1:0"})
	server.setBoundAddr("127.0.0.1:54321")
	req := httptest.NewRequest(http.MethodGet, "http://untrusted.invalid/api/v1/integrations/github/login", nil)

	if got, want := server.githubCallbackURL(req), "http://127.0.0.1:54321/api/v1/integrations/github/callback"; got != want {
		t.Fatalf("desktop callback URL = %q, want %q", got, want)
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
