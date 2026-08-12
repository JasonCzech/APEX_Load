package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func newLocalAuthTestServer(t *testing.T, desktop bool) (*Server, *storage.SQLiteStore) {
	t.Helper()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "local-auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: "127.0.0.1:0", DesktopMode: desktop, DesktopCapability: "desktop-test-capability"})
	settings := server.runner.GetSystemSettings()
	settings.Auth.Mode = authModeLocal
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("enable local auth: %v", err)
	}
	server.captureStartupAuthSettings()
	if err := server.ensureLocalAuthFixtures(); err != nil {
		t.Fatalf("seed local auth: %v", err)
	}
	return server, store
}

func localLogin(t *testing.T, server *Server, username, password string) (*httptest.ResponseRecorder, *http.Cookie) {
	t.Helper()
	body, _ := json.Marshal(LocalLoginRequest{Username: username, Password: password})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	req.RemoteAddr = "192.0.2.10:1234"
	rec := httptest.NewRecorder()
	server.mux.ServeHTTP(rec, req)
	var cookie *http.Cookie
	for _, candidate := range rec.Result().Cookies() {
		if candidate.Name == localSessionCookieName {
			cookie = candidate
		}
	}
	return rec, cookie
}

func TestLocalLoginPersonasAndNormalizedUsername(t *testing.T) {
	server, _ := newLocalAuthTestServer(t, false)
	tests := []struct {
		username, subject, globalRole, scopeType, scopeID, scopeRole string
	}{
		{" ADMIN ", "local:admin", RoleSystemAdmin, "", "", ""},
		{"domainadmin", "local:domainadmin", RoleViewer, "domain", storage.DefaultDomainID, RoleProjectAdmin},
		{"user", "local:user", RoleViewer, "project", storage.DefaultProjectID, RoleViewer},
		{"dev", "local:dev", RoleViewer, "project", storage.DefaultProjectID, RoleEditor},
	}
	for _, test := range tests {
		t.Run(strings.TrimSpace(test.username), func(t *testing.T) {
			password := strings.ToLower(strings.TrimSpace(test.username))
			login, cookie := localLogin(t, server, test.username, password)
			if login.Code != http.StatusOK || cookie == nil {
				t.Fatalf("login = %d cookie=%v body=%s", login.Code, cookie, login.Body.String())
			}
			if !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.MaxAge != int(localSessionTTL.Seconds()) {
				t.Fatalf("unsafe local cookie: %#v", cookie)
			}
			request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
			request.AddCookie(cookie)
			response := httptest.NewRecorder()
			server.mux.ServeHTTP(response, request)
			var status AuthStatusResponse
			if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
				t.Fatal(err)
			}
			if !status.Authenticated || status.User == nil || status.User.Subject != test.subject || status.SessionStatus != "active" || status.LogoutCSRF == "" {
				t.Fatalf("auth status = %#v", status)
			}
			if !containsString(status.User.GlobalRoles, test.globalRole) {
				t.Fatalf("global roles = %#v, want %q", status.User.GlobalRoles, test.globalRole)
			}
			if test.scopeType == "domain" && !containsString(status.User.DomainRoles[test.scopeID], test.scopeRole) {
				t.Fatalf("domain roles = %#v", status.User.DomainRoles)
			}
			if test.scopeType == "project" && !containsString(status.User.ProjectRoles[test.scopeID], test.scopeRole) {
				t.Fatalf("project roles = %#v", status.User.ProjectRoles)
			}
		})
	}
}

func TestLocalAuthPersonaFixtureBindingsAreExact(t *testing.T) {
	_, store := newLocalAuthTestServer(t, false)
	tests := []struct {
		username string
		bindings []string
	}{
		{"admin", []string{"global||" + RoleSystemAdmin}},
		{"domainadmin", []string{"global||" + RoleViewer, "domain|" + storage.DefaultDomainID + "|" + RoleProjectAdmin}},
		{"user", []string{
			"global||" + RoleViewer,
			"domain|" + storage.DefaultDomainID + "|" + RoleViewer,
			"application_repository|" + storage.DefaultApplicationRepositoryID + "|" + RoleViewer,
			"project|" + storage.DefaultProjectID + "|" + RoleViewer,
		}},
		{"dev", []string{
			"global||" + RoleViewer,
			"domain|" + storage.DefaultDomainID + "|" + RoleEditor,
			"application_repository|" + storage.DefaultApplicationRepositoryID + "|" + RoleEditor,
			"project|" + storage.DefaultProjectID + "|" + RoleEditor,
		}},
	}
	for _, test := range tests {
		t.Run(test.username, func(t *testing.T) {
			user, err := store.GetUser("local-auth-" + test.username)
			if err != nil {
				t.Fatal(err)
			}
			actual := make(map[string]struct{}, len(user.Bindings))
			for _, binding := range user.Bindings {
				actual[binding.ScopeType+"|"+binding.ScopeID+"|"+binding.Role] = struct{}{}
			}
			if len(actual) != len(test.bindings) {
				t.Fatalf("bindings = %#v, want exactly %#v", actual, test.bindings)
			}
			for _, expected := range test.bindings {
				if _, ok := actual[expected]; !ok {
					t.Fatalf("bindings = %#v, missing %q", actual, expected)
				}
			}
		})
	}
}

func TestLocalLoginPersonasHonorWorkspaceBindings(t *testing.T) {
	server, store := newLocalAuthTestServer(t, false)
	now := time.Now().UTC()
	const (
		otherDomainID     = "local-persona-other-domain"
		otherRepositoryID = "local-persona-other-repository"
		otherCategoryID   = "local-persona-other-category"
		otherProjectID    = "local-persona-other-project"
	)
	if err := store.SaveDomain(&storage.Domain{ID: otherDomainID, Name: "Other", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveApplicationRepository(&storage.ApplicationRepository{ID: otherRepositoryID, DomainID: otherDomainID, Name: "Other", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRepositoryCategory(&storage.RepositoryCategory{ID: otherCategoryID, DomainID: otherDomainID, RepositoryID: otherRepositoryID, Name: "Other", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveProject(&storage.Project{ID: otherProjectID, DomainID: otherDomainID, CategoryID: otherCategoryID, Name: "Other", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}

	getProject := func(cookie *http.Cookie, projectID string) int {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID, nil)
		request.AddCookie(cookie)
		response := httptest.NewRecorder()
		server.mux.ServeHTTP(response, request)
		return response.Code
	}
	getPath := func(cookie *http.Cookie, path string) int {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.AddCookie(cookie)
		response := httptest.NewRecorder()
		server.mux.ServeHTTP(response, request)
		return response.Code
	}
	postScript := func(cookie *http.Cookie) int {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts", strings.NewReader(`{"project_id":"`+storage.DefaultProjectID+`"}`))
		request.Header.Set("Content-Type", "application/json")
		request.AddCookie(cookie)
		response := httptest.NewRecorder()
		server.mux.ServeHTTP(response, request)
		return response.Code
	}

	for _, test := range []struct {
		username  string
		wantOther int
	}{
		{username: "admin", wantOther: http.StatusOK},
		{username: "domainadmin", wantOther: http.StatusForbidden},
		{username: "user", wantOther: http.StatusForbidden},
		{username: "dev", wantOther: http.StatusForbidden},
	} {
		t.Run(test.username, func(t *testing.T) {
			login, cookie := localLogin(t, server, test.username, test.username)
			if login.Code != http.StatusOK || cookie == nil {
				t.Fatalf("login = %d cookie=%v body=%s", login.Code, cookie, login.Body.String())
			}
			sessionRequest := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
			sessionRequest.AddCookie(cookie)
			session := resolveLocalSession(sessionRequest, store)
			if session.User == nil || !server.websocketUserCanViewProject(session.User, storage.DefaultProjectID) {
				t.Fatal("default-project websocket was not authorized")
			}
			if got := server.websocketUserCanViewProject(session.User, otherProjectID); got != (test.wantOther == http.StatusOK) {
				t.Fatalf("other-project websocket authorization = %v", got)
			}
			if got := server.websocketUserCanViewProject(session.User, ""); got != (test.username == "admin") {
				t.Fatalf("unfiltered websocket authorization = %v", got)
			}
			if got := getProject(cookie, storage.DefaultProjectID); got != http.StatusOK {
				t.Fatalf("default project status = %d, want 200", got)
			}
			if got := getProject(cookie, otherProjectID); got != test.wantOther {
				t.Fatalf("other project status = %d, want %d", got, test.wantOther)
			}
			if got := getPath(cookie, "/api/v1/scripts?project_id="+storage.DefaultProjectID); got != http.StatusOK {
				t.Fatalf("scoped script list status = %d, want 200", got)
			}
			wantUnscoped := http.StatusForbidden
			if test.username == "admin" {
				wantUnscoped = http.StatusOK
			}
			if got := getPath(cookie, "/api/v1/scripts"); got != wantUnscoped {
				t.Fatalf("unscoped script list status = %d, want %d", got, wantUnscoped)
			}
			for _, path := range []string{"/api/v1/ai/review-queue", "/api/v1/recorder/status"} {
				if got := getPath(cookie, path+"?project_id="+storage.DefaultProjectID); got != http.StatusOK {
					t.Fatalf("scoped %s status = %d, want 200", path, got)
				}
				if got := getPath(cookie, path); got != wantUnscoped {
					t.Fatalf("unscoped %s status = %d, want %d", path, got, wantUnscoped)
				}
			}
			writeStatus := postScript(cookie)
			if test.username == "user" && writeStatus != http.StatusForbidden {
				t.Fatalf("read-only user script create status = %d, want 403", writeStatus)
			}
			if test.username != "user" && writeStatus == http.StatusForbidden {
				t.Fatalf("%s unexpectedly lacked write access in the default project", test.username)
			}
		})
	}

	// Access edits are effective without reseeding: a domain-list administrator
	// inherits its new domain binding across every project in that domain.
	if err := store.SaveUserRoleBinding(&storage.UserRoleBinding{
		ID: "local-domainadmin-other-domain", UserID: "local-auth-domainadmin",
		ScopeType: "domain", ScopeID: otherDomainID, Role: RoleProjectAdmin,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	login, cookie := localLogin(t, server, "domainadmin", "domainadmin")
	if login.Code != http.StatusOK || cookie == nil {
		t.Fatalf("domainadmin re-login = %d cookie=%v", login.Code, cookie)
	}
	if got := getProject(cookie, otherProjectID); got != http.StatusOK {
		t.Fatalf("newly bound domain project status = %d, want 200", got)
	}
}

func TestLocalAuthModeTransitionTakesEffectOnlyAfterRestart(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "local-auth-restart.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	server := NewServer(ServerConfig{Store: store})
	settings := server.runner.GetSystemSettings()
	settings.Auth.Mode = authModeLocal
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("save local mode: %v", err)
	}
	if got := server.authSettingsForRequest().Mode; got != authModeDev {
		t.Fatalf("mode before restart = %q, want %q", got, authModeDev)
	}
	beforeRestart := httptest.NewRecorder()
	server.mux.ServeHTTP(beforeRestart, httptest.NewRequest(http.MethodGet, "/api/v1/system/settings", nil))
	if beforeRestart.Code != http.StatusOK {
		t.Fatalf("saving local mode immediately locked the current session: %d %s", beforeRestart.Code, beforeRestart.Body.String())
	}

	restarted := NewServer(ServerConfig{Store: store})
	if got := restarted.authSettingsForRequest().Mode; got != authModeLocal {
		t.Fatalf("mode after restart = %q, want %q", got, authModeLocal)
	}
	localGate := httptest.NewRecorder()
	restarted.mux.ServeHTTP(localGate, httptest.NewRequest(http.MethodGet, "/api/v1/system/settings", nil))
	if localGate.Code != http.StatusUnauthorized {
		t.Fatalf("restarted local gate status = %d, want 401", localGate.Code)
	}

	settings = restarted.runner.GetSystemSettings()
	settings.Auth.Mode = authModeDev
	if _, err := restarted.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("save dev mode: %v", err)
	}
	if got := restarted.authSettingsForRequest().Mode; got != authModeLocal {
		t.Fatalf("mode before leaving-local restart = %q, want %q", got, authModeLocal)
	}

	restartedAgain := NewServer(ServerConfig{Store: store})
	if got := restartedAgain.authSettingsForRequest().Mode; got != authModeDev {
		t.Fatalf("mode after leaving-local restart = %q, want %q", got, authModeDev)
	}
}

func TestServerStartSeedsLocalAuthFixturesBeforeServing(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "local-auth-start.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	server := NewServer(ServerConfig{Store: store, Addr: "127.0.0.1:0", ProxyAddr: "127.0.0.1:0"})
	settings := server.runner.GetSystemSettings()
	settings.Auth.Mode = authModeLocal
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("persist local mode: %v", err)
	}
	started := make(chan error, 1)
	go func() { started <- server.Start() }()
	deadline := time.Now().Add(10 * time.Second)
	for !server.BoundAddrReady() {
		select {
		case err := <-started:
			t.Fatalf("server stopped before binding: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("server did not bind before timeout")
		}
		time.Sleep(10 * time.Millisecond)
	}

	for _, username := range []string{"admin", "domainadmin", "user", "dev"} {
		credential, err := store.GetLocalAuthCredential(username)
		if err != nil || credential == nil || credential.UserID != "local-auth-"+username {
			t.Fatalf("fixture %q after Start = %#v, %v", username, credential, err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if err := <-started; err != nil {
		t.Fatalf("Start returned after shutdown: %v", err)
	}
}

func TestLocalLoginCookieSecureFollowsTrustedRequestTransport(t *testing.T) {
	tests := []struct {
		name              string
		target            string
		remoteAddr        string
		forwardedProto    string
		trustedProxyCIDRs []string
		staleRedirectURL  string
		wantSecure        bool
	}{
		{
			name:             "cleartext ignores stale HTTPS OIDC redirect",
			target:           "http://apexload.example.test/api/v1/auth/login",
			remoteAddr:       "192.0.2.10:1234",
			staleRedirectURL: "https://old.example.test/api/v1/auth/callback",
		},
		{
			name:             "direct TLS ignores stale HTTP OIDC redirect",
			target:           "https://apexload.example.test/api/v1/auth/login",
			remoteAddr:       "192.0.2.10:1234",
			staleRedirectURL: "http://old.example.test/api/v1/auth/callback",
			wantSecure:       true,
		},
		{
			name:              "trusted proxy HTTPS is public secure transport",
			target:            "http://apexload.example.test/api/v1/auth/login",
			remoteAddr:        "10.2.3.4:443",
			forwardedProto:    "https",
			trustedProxyCIDRs: []string{"10.0.0.0/8"},
			wantSecure:        true,
		},
		{
			name:              "untrusted forwarded HTTPS is ignored",
			target:            "http://apexload.example.test/api/v1/auth/login",
			remoteAddr:        "203.0.113.7:443",
			forwardedProto:    "https",
			trustedProxyCIDRs: []string{"10.0.0.0/8"},
		},
		{
			name:              "trusted proxy HTTP describes public cleartext",
			target:            "https://apexload.example.test/api/v1/auth/login",
			remoteAddr:        "10.2.3.4:443",
			forwardedProto:    "http",
			trustedProxyCIDRs: []string{"10.0.0.0/8"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, _ := newLocalAuthTestServer(t, false)
			settings := server.runner.GetSystemSettings()
			settings.Auth.RedirectURL = test.staleRedirectURL
			settings.Auth.TrustedProxyCIDRs = test.trustedProxyCIDRs
			if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
				t.Fatalf("update auth transport settings: %v", err)
			}
			server.captureStartupAuthSettings()

			body, _ := json.Marshal(LocalLoginRequest{Username: "admin", Password: "admin"})
			request := httptest.NewRequest(http.MethodPost, test.target, bytes.NewReader(body))
			request.RemoteAddr = test.remoteAddr
			if test.forwardedProto != "" {
				request.Header.Set("X-Forwarded-Proto", test.forwardedProto)
			}
			response := httptest.NewRecorder()
			server.mux.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("login = %d body=%s", response.Code, response.Body.String())
			}
			cookie := findResponseCookie(t, response.Result().Cookies(), localSessionCookieName)
			if cookie.Secure != test.wantSecure {
				t.Fatalf("cookie Secure = %v, want %v", cookie.Secure, test.wantSecure)
			}

			logoutTarget := strings.TrimSuffix(test.target, "/login") + "/logout"
			logout := httptest.NewRequest(http.MethodPost, logoutTarget, nil)
			logout.RemoteAddr = test.remoteAddr
			logout.AddCookie(cookie)
			logout.Header.Set("X-APEXLoad-CSRF-Token", localLogoutCSRFToken(cookie.Value))
			if test.forwardedProto != "" {
				logout.Header.Set("X-Forwarded-Proto", test.forwardedProto)
			}
			logoutResponse := httptest.NewRecorder()
			server.mux.ServeHTTP(logoutResponse, logout)
			if logoutResponse.Code != http.StatusOK {
				t.Fatalf("logout = %d body=%s", logoutResponse.Code, logoutResponse.Body.String())
			}
			cleared := findResponseCookie(t, logoutResponse.Result().Cookies(), localSessionCookieName)
			if cleared.MaxAge != -1 || cleared.Secure != test.wantSecure {
				t.Fatalf("cleared cookie = %#v, want MaxAge=-1 Secure=%v", cleared, test.wantSecure)
			}
		})
	}
}

func TestLocalLoginUsesGenericFailuresAndRateLimit(t *testing.T) {
	server, store := newLocalAuthTestServer(t, false)
	for _, credentials := range [][2]string{{"admin", "wrong"}, {"missing", "wrong"}} {
		response, _ := localLogin(t, server, credentials[0], credentials[1])
		if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), localInvalidCredentials) {
			t.Fatalf("failure for %q = %d %s", credentials[0], response.Code, response.Body.String())
		}
	}
	for attempt := 0; attempt < localLoginRateLimitPerMinute; attempt++ {
		response, _ := localLogin(t, server, "rate-limited", "wrong")
		if attempt < localLoginRateLimitPerMinute-1 && response.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d = %d", attempt+1, response.Code)
		}
	}
	response, _ := localLogin(t, server, "rate-limited", "wrong")
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("rate-limit response = %d %s", response.Code, response.Body.String())
	}
	events, err := store.ListAuditEvents(storage.AuditEventFilter{Action: "auth.local.login", Status: "failure", Limit: 20})
	if err != nil || len(events) == 0 {
		t.Fatalf("failed-login audit events = %d, err=%v", len(events), err)
	}
	encoded, _ := json.Marshal(events)
	if strings.Contains(string(encoded), "wrong") {
		t.Fatalf("audit events leaked a submitted password: %s", encoded)
	}
}

func TestLocalSessionDisableRevocationExpiryAndLogoutCSRF(t *testing.T) {
	server, store := newLocalAuthTestServer(t, false)
	login, cookie := localLogin(t, server, "dev", "dev")
	if login.Code != http.StatusOK || cookie == nil {
		t.Fatalf("login = %d %s", login.Code, login.Body.String())
	}
	me := func() AuthStatusResponse {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		server.mux.ServeHTTP(rec, req)
		var status AuthStatusResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &status)
		return status
	}
	status := me()

	badLogout := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	badLogout.AddCookie(cookie)
	badResponse := httptest.NewRecorder()
	server.mux.ServeHTTP(badResponse, badLogout)
	if badResponse.Code != http.StatusForbidden {
		t.Fatalf("logout without csrf = %d", badResponse.Code)
	}
	logout := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	logout.AddCookie(cookie)
	logout.Header.Set("X-APEXLoad-CSRF-Token", status.LogoutCSRF)
	logoutResponse := httptest.NewRecorder()
	server.mux.ServeHTTP(logoutResponse, logout)
	if logoutResponse.Code != http.StatusOK || me().Authenticated {
		t.Fatalf("logout = %d, status=%#v", logoutResponse.Code, me())
	}

	_, cookie = localLogin(t, server, "dev", "dev")
	if err := store.UpdateUserActive("local-auth-dev", false, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if got := me(); got.Authenticated || got.SessionStatus != "disabled" {
		t.Fatalf("disabled status = %#v", got)
	}
	if disabledLogin, disabledCookie := localLogin(t, server, "dev", "dev"); disabledLogin.Code != http.StatusUnauthorized || disabledCookie != nil || !strings.Contains(disabledLogin.Body.String(), localInvalidCredentials) {
		t.Fatalf("disabled login = %d cookie=%v body=%s", disabledLogin.Code, disabledCookie, disabledLogin.Body.String())
	}
	if err := store.UpdateUserActive("local-auth-dev", true, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.BumpUserSessionEpoch("local-auth-dev", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if got := me(); got.Authenticated || got.SessionStatus != "revoked" || got.LogoutCSRF == "" {
		t.Fatalf("revoked status = %#v", got)
	} else {
		clearRevoked := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
		clearRevoked.AddCookie(cookie)
		clearRevoked.Header.Set("X-APEXLoad-CSRF-Token", got.LogoutCSRF)
		clearResponse := httptest.NewRecorder()
		server.mux.ServeHTTP(clearResponse, clearRevoked)
		if clearResponse.Code != http.StatusOK {
			t.Fatalf("clear revoked session = %d body=%s", clearResponse.Code, clearResponse.Body.String())
		}
	}

	_, cookie = localLogin(t, server, "user", "user")
	hash := localSessionTokenHash(cookie.Value)
	if err := store.DeleteLocalAuthSessionByTokenHash(hash); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := store.SaveLocalAuthSession(&storage.LocalAuthSession{TokenHash: hash, UserID: "local-auth-user", CreatedAt: now.Add(-time.Hour), ExpiresAt: now.Add(-time.Second)}); err != nil {
		t.Fatal(err)
	}
	if got := me(); got.Authenticated || got.SessionStatus != "expired" || got.LogoutCSRF == "" {
		t.Fatalf("expired status = %#v", got)
	} else {
		clearExpired := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
		clearExpired.AddCookie(cookie)
		clearExpired.Header.Set("X-APEXLoad-CSRF-Token", got.LogoutCSRF)
		clearResponse := httptest.NewRecorder()
		server.mux.ServeHTTP(clearResponse, clearExpired)
		if clearResponse.Code != http.StatusOK {
			t.Fatalf("clear expired session = %d body=%s", clearResponse.Code, clearResponse.Body.String())
		}
	}

	// Expiry cleanup may remove the durable row before the browser next loads.
	// The stale HttpOnly cookie must still receive a derived CSRF token and be
	// clearable through the idempotent logout path.
	_, cookie = localLogin(t, server, "user", "user")
	if err := store.DeleteLocalAuthSessionByTokenHash(localSessionTokenHash(cookie.Value)); err != nil {
		t.Fatal(err)
	}
	if got := me(); got.Authenticated || got.SessionStatus != "invalid" || got.LogoutCSRF == "" {
		t.Fatalf("cleaned session status = %#v", got)
	} else {
		clearMissing := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
		clearMissing.AddCookie(cookie)
		clearMissing.Header.Set("X-APEXLoad-CSRF-Token", got.LogoutCSRF)
		clearResponse := httptest.NewRecorder()
		server.mux.ServeHTTP(clearResponse, clearMissing)
		if clearResponse.Code != http.StatusOK {
			t.Fatalf("clear cleaned session = %d body=%s", clearResponse.Code, clearResponse.Body.String())
		}
		var cleared bool
		for _, responseCookie := range clearResponse.Result().Cookies() {
			if responseCookie.Name == localSessionCookieName && responseCookie.MaxAge < 0 {
				cleared = true
			}
		}
		if !cleared {
			t.Fatal("idempotent logout did not clear the stale browser cookie")
		}
	}
}

func TestDesktopLocalSessionIsCapabilityBoundAndRejectsAmbientCookie(t *testing.T) {
	server, store := newLocalAuthTestServer(t, true)
	handler := server.httpServer.Handler

	ambient := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	ambient.Header.Set(desktopCapabilityHeader, "desktop-test-capability")
	ambient.AddCookie(&http.Cookie{Name: localSessionCookieName, Value: "ambient-loopback-cookie"})
	ambientResponse := httptest.NewRecorder()
	handler.ServeHTTP(ambientResponse, ambient)
	var before AuthStatusResponse
	_ = json.Unmarshal(ambientResponse.Body.Bytes(), &before)
	if before.Authenticated || before.SessionStatus != "missing" {
		t.Fatalf("ambient cookie was accepted: %#v", before)
	}

	body, _ := json.Marshal(LocalLoginRequest{Username: "dev", Password: "dev"})
	login := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	login.Header.Set(desktopCapabilityHeader, "desktop-test-capability")
	loginResponse := httptest.NewRecorder()
	handler.ServeHTTP(loginResponse, login)
	if loginResponse.Code != http.StatusOK || len(loginResponse.Result().Cookies()) != 0 {
		t.Fatalf("desktop login = %d cookies=%v body=%s", loginResponse.Code, loginResponse.Result().Cookies(), loginResponse.Body.String())
	}

	me := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	me.Header.Set(desktopCapabilityHeader, "desktop-test-capability")
	meResponse := httptest.NewRecorder()
	handler.ServeHTTP(meResponse, me)
	var after AuthStatusResponse
	_ = json.Unmarshal(meResponse.Body.Bytes(), &after)
	if !after.Authenticated || after.User == nil || after.User.Subject != "local:dev" {
		t.Fatalf("capability-bound auth status = %#v", after)
	}
	token, ok := server.desktopLocalSessionForCapability("desktop-test-capability")
	if !ok || token == "" {
		t.Fatal("desktop capability did not retain the local session token")
	}

	logout := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	logout.Header.Set(desktopCapabilityHeader, "desktop-test-capability")
	logout.Header.Set("X-APEXLoad-CSRF-Token", after.LogoutCSRF)
	logoutResponse := httptest.NewRecorder()
	handler.ServeHTTP(logoutResponse, logout)
	if logoutResponse.Code != http.StatusOK || len(logoutResponse.Result().Cookies()) != 0 {
		t.Fatalf("desktop logout = %d cookies=%v body=%s", logoutResponse.Code, logoutResponse.Result().Cookies(), logoutResponse.Body.String())
	}
	if _, ok := server.desktopLocalSessionForCapability("desktop-test-capability"); ok {
		t.Fatal("desktop capability retained its local session after logout")
	}
	if _, err := store.GetLocalAuthSessionByTokenHash(localSessionTokenHash(token)); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("persisted session after desktop logout: %v", err)
	}

	afterLogout := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	afterLogout.Header.Set(desktopCapabilityHeader, "desktop-test-capability")
	afterLogoutResponse := httptest.NewRecorder()
	handler.ServeHTTP(afterLogoutResponse, afterLogout)
	var signedOut AuthStatusResponse
	_ = json.Unmarshal(afterLogoutResponse.Body.Bytes(), &signedOut)
	if signedOut.Authenticated || signedOut.SessionStatus != "missing" {
		t.Fatalf("auth status after desktop logout = %#v", signedOut)
	}

	relogin := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	relogin.Header.Set(desktopCapabilityHeader, "desktop-test-capability")
	reloginResponse := httptest.NewRecorder()
	handler.ServeHTTP(reloginResponse, relogin)
	if reloginResponse.Code != http.StatusOK || len(reloginResponse.Result().Cookies()) != 0 {
		t.Fatalf("desktop re-login = %d cookies=%v body=%s", reloginResponse.Code, reloginResponse.Result().Cookies(), reloginResponse.Body.String())
	}
}
