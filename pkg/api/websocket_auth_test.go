package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jasonczech/apexload/pkg/storage"
)

type failingWebsocketSettingsStore struct{ block bool }

type cancelAwareDebugSettingsStore struct {
	failingWebsocketSettingsStore
	entered chan struct{}
}

func (s *cancelAwareDebugSettingsStore) GetSettingContext(ctx context.Context, _ string) (string, error) {
	close(s.entered)
	<-ctx.Done()
	return "", ctx.Err()
}

func (*failingWebsocketSettingsStore) GetSetting(string) (string, error) {
	return "", errors.New("settings unavailable")
}
func (*failingWebsocketSettingsStore) SaveSetting(string, string) error { return nil }
func (s *failingWebsocketSettingsStore) GetSettingContext(ctx context.Context, _ string) (string, error) {
	if s.block {
		<-ctx.Done()
		return "", ctx.Err()
	}
	return "", errors.New("settings unavailable")
}

type legacyWebsocketSettingsStore struct{}

func (*legacyWebsocketSettingsStore) GetSetting(string) (string, error) {
	panic("unbounded settings read")
}
func (*legacyWebsocketSettingsStore) SaveSetting(string, string) error { return nil }

type blockedWebsocketIdentityStore struct {
	*storage.SQLiteStore
	called chan struct{}
}

type delayedWebsocketIdentityStore struct{ *storage.SQLiteStore }

func (s *delayedWebsocketIdentityStore) GetUserBySubjectContext(ctx context.Context, subject string) (*storage.User, error) {
	timer := time.NewTimer(80 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return s.SQLiteStore.GetUserBySubjectContext(ctx, subject)
	}
}

// Embedding only the public Store interface models older store implementations
// that do not expose the optional contextual authorization methods.
type legacyWebsocketStore struct{ storage.Store }

func (s *blockedWebsocketIdentityStore) GetUserBySubjectContext(ctx context.Context, _ string) (*storage.User, error) {
	close(s.called)
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestWebSocketAuthorizerRevokesOnLiveAuthProviderChange(t *testing.T) {
	t.Setenv("APEXLOAD_ENV", "development")
	for _, tc := range []struct {
		name       string
		startMode  string
		preview    bool
		changeAuth func(*AuthSettings)
	}{
		{name: "dev bypass to trusted headers", startMode: authModeDev, changeAuth: func(a *AuthSettings) { a.Mode = authModeTrustedHeaders }},
		{name: "dev preview to trusted headers", startMode: authModeDev, preview: true, changeAuth: func(a *AuthSettings) { a.Mode = authModeTrustedHeaders }},
		{name: "trusted header provider changes", startMode: authModeTrustedHeaders, changeAuth: func(a *AuthSettings) { a.RolesHeader = "X-New-Trusted-Roles" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := NewServer(ServerConfig{})
			settings := server.runner.GetSystemSettings()
			settings.Auth.Mode = tc.startMode
			if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodGet, "/api/v1/ws?project_id=project-a", nil)
			if tc.preview {
				request.Header.Set(devViewHeader, devViewModeUser)
				request.Header.Set(devProjectIDHeader, "project-a")
			} else if tc.startMode == authModeTrustedHeaders {
				request = withAuthenticatedContext(request, authResolution{User: &AuthenticatedUser{Subject: "editor", GlobalRoles: map[string]struct{}{RoleEditor: {}}}})
			}
			authorize := server.websocketScopedAuthorizer(request)
			if !authorize("project-a", false) || !authorize("project-a", true) {
				t.Fatal("fresh connection did not retain its original access")
			}
			original := settings.Auth
			tc.changeAuth(&settings.Auth)
			if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
				t.Fatal(err)
			}
			if authorize("project-a", true) || authorize("project-a", false) {
				t.Fatal("old connection survived an active authentication-provider change")
			}
			settings.Auth = original
			if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
				t.Fatal(err)
			}
			if authorize("project-a", true) || authorize("project-a", false) {
				t.Fatal("revoked connection regained access after authentication settings reverted")
			}
		})
	}
}

func TestWebSocketUpgradeRejectsProviderChangeAfterMiddlewareAdmission(t *testing.T) {
	t.Setenv("APEXLOAD_ENV", "development")
	server := NewServer(ServerConfig{})
	settings := server.runner.GetSystemSettings()
	settings.Auth.Mode = authModeDev
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatal(err)
	}
	handler := authMiddleware(server.authSettingsForRequest, nil, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		settings.Auth.Mode = authModeTrustedHeaders
		if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
			t.Fatal(err)
		}
		server.handleWebSocket(w, r)
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/ws", nil))
	if response.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403 when the provider changes after admission: %s", response.Code, response.Body.String())
	}
}

func TestDebuggerWebSocketChecksDurableSettingsBeforeDelivery(t *testing.T) {
	t.Setenv("APEXLOAD_ENV", "development")
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "socket-settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	reader := NewServer(ServerConfig{Store: store})
	writer := NewServer(ServerConfig{Store: store})
	authorize := reader.websocketScopedAuthorizer(httptest.NewRequest(http.MethodGet, "/api/v1/ws", nil))
	if !authorize("project-a", true) {
		t.Fatal("fresh bypass socket rejected before settings change")
	}
	settings := writer.runner.GetSystemSettings()
	settings.Auth.Mode = authModeTrustedHeaders
	if _, err := writer.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatal(err)
	}
	// An unrelated stale refresh must not make a security-sensitive delivery
	// use the cached dev settings or wait on that refresh's completion.
	reader.runner.settingsRefreshMu.Lock()
	inFlight := make(chan struct{})
	reader.runner.settingsRefreshInFlight = inFlight
	reader.runner.settingsRefreshMu.Unlock()
	defer func() {
		reader.runner.settingsRefreshMu.Lock()
		reader.runner.settingsRefreshInFlight = nil
		close(inFlight)
		reader.runner.settingsRefreshMu.Unlock()
	}()
	if authorize("project-a", true) {
		t.Fatal("raw event authorized using stale settings after another replica changed provider")
	}
}

func TestDebuggerWebSocketDeniesWhenDurableSettingsCannotBeRead(t *testing.T) {
	t.Setenv("APEXLOAD_ENV", "development")
	for _, tc := range []struct {
		name  string
		store settingsPersistence
	}{
		{"error", &failingWebsocketSettingsStore{}},
		{"timeout", &failingWebsocketSettingsStore{block: true}},
		{"legacy uncancellable", &legacyWebsocketSettingsStore{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := NewServer(ServerConfig{})
			authorize := server.websocketScopedAuthorizer(httptest.NewRequest(http.MethodGet, "/api/v1/ws", nil))
			server.runner.settingsStore = tc.store
			start := time.Now()
			if authorize("project-a", true) {
				t.Fatal("raw event authorized without a successful durable settings read")
			}
			if time.Since(start) > 2*time.Second {
				t.Fatal("settings read did not respect the delivery timeout")
			}
			server.runner.settingsStore = nil
			if !authorize("project-a", true) {
				t.Fatal("temporary settings-read error permanently revoked the connection")
			}
		})
	}
}

func TestDebuggerWebSocketBoundsInMemorySettingsLock(t *testing.T) {
	t.Setenv("APEXLOAD_ENV", "development")
	server := NewServer(ServerConfig{})
	authorize := server.websocketScopedAuthorizer(httptest.NewRequest(http.MethodGet, "/api/v1/ws", nil))
	server.runner.mu.Lock()
	start := time.Now()
	allowed := authorize("project-a", true)
	server.runner.mu.Unlock()
	if allowed || time.Since(start) > 2*time.Second {
		t.Fatalf("blocked in-memory settings lock allowed=%v after %v", allowed, time.Since(start))
	}
}

func TestDebuggerWebSocketBoundsIdentityRevalidation(t *testing.T) {
	t.Setenv("APEXLOAD_ENV", "development")
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "blocked-socket-identity.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	now := time.Now().UTC()
	user := &storage.User{ID: "blocked-editor", Subject: "blocked-editor", Active: true, CreatedAt: now, UpdatedAt: now}
	if err := base.SaveUser(user); err != nil {
		t.Fatal(err)
	}
	store := &blockedWebsocketIdentityStore{SQLiteStore: base, called: make(chan struct{})}
	server := NewServer(ServerConfig{Store: store})
	enableTrustedHeaderAuth(t, server)
	request := withAuthenticatedContext(httptest.NewRequest(http.MethodGet, "/api/v1/ws", nil), authResolution{User: &AuthenticatedUser{ID: user.ID, Subject: user.Subject, GlobalRoles: map[string]struct{}{RoleEditor: {}}}})
	authorize := server.websocketScopedAuthorizer(request)
	start := time.Now()
	if authorize("project-a", true) {
		t.Fatal("blocked identity lookup authorized raw debugger data")
	}
	select {
	case <-store.called:
	default:
		t.Fatal("test did not reach the contextual identity lookup")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("blocked identity lookup exceeded bounded delivery time: %v", elapsed)
	}
}

func TestDebuggerWebSocketRejectsSessionThatExpiresDuringIdentityRead(t *testing.T) {
	t.Setenv("APEXLOAD_ENV", "development")
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "expiring-socket-identity.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	now := time.Now().UTC()
	user := &storage.User{ID: "expiring-editor", Subject: "expiring-editor", Active: true, CreatedAt: now, UpdatedAt: now}
	if err := base.SaveUser(user); err != nil {
		t.Fatal(err)
	}
	if err := base.SaveUserRoleBinding(&storage.UserRoleBinding{ID: "expiring-editor-role", UserID: user.ID, ScopeType: "global", Role: RoleEditor, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{Store: &delayedWebsocketIdentityStore{SQLiteStore: base}})
	enableTrustedHeaderAuth(t, server)
	identity := &AuthenticatedUser{ID: user.ID, Subject: user.Subject, SessionScoped: true, SessionExpiresAt: time.Now().Add(30 * time.Millisecond), GlobalRoles: map[string]struct{}{RoleEditor: {}}}
	request := withAuthenticatedContext(httptest.NewRequest(http.MethodGet, "/api/v1/ws", nil), authResolution{User: identity})
	if server.websocketScopedAuthorizer(request)("project-a", true) {
		t.Fatal("raw debugger event was authorized after the session expired during identity lookup")
	}
}

func TestDevPreviewWebSocketUpgradeHonorsCancellation(t *testing.T) {
	t.Setenv("APEXLOAD_ENV", "development")
	server := NewServer(ServerConfig{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/ws", nil).WithContext(ctx)
	request.Header.Set(devViewHeader, devViewModeUser)
	request.Header.Set(devProjectIDHeader, "project-a")
	response := httptest.NewRecorder()
	server.handleWebSocket(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("canceled preview upgrade = %d, want 503", response.Code)
	}
}

func TestWebSocketUpgradeBoundsViewerRevalidationAndHonorsCancellation(t *testing.T) {
	t.Setenv("APEXLOAD_ENV", "development")
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "blocked-socket-upgrade.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	now := time.Now().UTC()
	user := &storage.User{ID: "blocked-viewer", Subject: "blocked-viewer", Active: true, CreatedAt: now, UpdatedAt: now}
	if err := base.SaveUser(user); err != nil {
		t.Fatal(err)
	}
	store := &blockedWebsocketIdentityStore{SQLiteStore: base, called: make(chan struct{})}
	server := NewServer(ServerConfig{Store: store})
	enableTrustedHeaderAuth(t, server)
	identity := authResolution{User: &AuthenticatedUser{ID: user.ID, Subject: user.Subject, GlobalRoles: map[string]struct{}{RoleViewer: {}}}}
	request := withAuthenticatedContext(httptest.NewRequest(http.MethodGet, "/api/v1/ws", nil), identity)
	response := httptest.NewRecorder()
	start := time.Now()
	server.handleWebSocket(response, request)
	if response.Code != http.StatusServiceUnavailable || time.Since(start) > 2*time.Second {
		t.Fatalf("blocked viewer upgrade = %d after %v", response.Code, time.Since(start))
	}
	select {
	case <-store.called:
	default:
		t.Fatal("test did not reach the contextual viewer identity lookup")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	request = withAuthenticatedContext(httptest.NewRequest(http.MethodGet, "/api/v1/ws", nil).WithContext(canceled), identity)
	response = httptest.NewRecorder()
	start = time.Now()
	server.handleWebSocket(response, request)
	if response.Code != http.StatusServiceUnavailable || time.Since(start) > 250*time.Millisecond {
		t.Fatalf("canceled viewer upgrade = %d after %v", response.Code, time.Since(start))
	}
}

func TestLegacyStoreKeepsOrdinaryWebSocketAndDeniesRawDebuggerEvents(t *testing.T) {
	t.Setenv("APEXLOAD_ENV", "development")
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "legacy-socket.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	now := time.Now().UTC()
	user := &storage.User{ID: "legacy-editor", Subject: "legacy-editor", Active: true, CreatedAt: now, UpdatedAt: now}
	if err := base.SaveUser(user); err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{Store: &legacyWebsocketStore{Store: base}})
	enableTrustedHeaderAuth(t, server)
	go server.hub.Run()
	defer server.hub.Stop()
	identity := authResolution{User: &AuthenticatedUser{ID: user.ID, Subject: user.Subject, GlobalRoles: map[string]struct{}{RoleEditor: {}}}}
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		server.handleWebSocket(w, withAuthenticatedContext(r, identity))
	}))
	defer httpServer.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(httpServer.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	waitForHubClientCount(t, server.hub, 1)
	server.hub.BroadcastJSON(map[string]any{"type": "script_debug_event", "project_id": "project-a", "secret": "private"})
	server.hub.BroadcastJSON(map[string]any{"type": "metrics", "project_id": "project-a"})
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil || strings.Contains(string(data), "private") || !strings.Contains(string(data), "metrics") {
		t.Fatalf("ordinary websocket delivery = %s, %v", data, err)
	}
}

func TestRawScriptDebugHTTPUsesFreshDurableAuthSettings(t *testing.T) {
	t.Setenv("APEXLOAD_ENV", "development")
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "debug-http-settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	reader := NewServer(ServerConfig{Store: store})
	writer := NewServer(ServerConfig{Store: store})
	session := retainedDebugHARSession()
	session.ProjectID = storage.DefaultProjectID
	session.FinalReport.FinalVariables = map[string]string{"token": "private-debug-value"}
	reader.scriptDebug.sessions[session.ID] = session

	settings := writer.runner.GetSystemSettings()
	settings.Auth.Mode = authModeTrustedHeaders
	if _, err := writer.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatal(err)
	}
	reader.runner.settingsRefreshMu.Lock()
	reader.runner.settingsAuthRefreshAt = time.Now()
	reader.runner.settingsRefreshMu.Unlock()
	if reader.authSettingsForRequest().Mode != authModeDev {
		t.Fatal("test did not retain the stale bypass-mode settings cache")
	}
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/script-debug/sessions/" + session.ID},
		{http.MethodGet, "/api/v1/script-debug/sessions/" + session.ID + "/export?format=json"},
		{http.MethodGet, "/api/v1/replay/sessions/" + session.ID},
		{http.MethodGet, "/api/v1/replay/sessions/" + session.ID + "/export?format=json"},
		{http.MethodPost, "/api/v1/script-debug/sessions/" + session.ID + "/pause"},
		{http.MethodPost, "/api/v1/replay/sessions/" + session.ID + "/resume"},
		{http.MethodPost, "/api/v1/scripts/debug/sessions"},
		{http.MethodPost, "/api/v1/scripts/replay/sessions"},
		{http.MethodPost, "/api/v1/scripts/example/debug/sessions"},
		{http.MethodPost, "/api/v1/scripts/example/replay/sessions"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			request := httptest.NewRequest(tc.method, tc.path, nil)
			// A stale dev-preview header must not override a newly active provider.
			request.Header.Set(devViewHeader, devViewModeUser)
			request.Header.Set(devProjectIDHeader, storage.DefaultProjectID)
			response := httptest.NewRecorder()
			reader.mux.ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized || strings.Contains(response.Body.String(), "private-debug-value") {
				t.Fatalf("status %d, want 401 without debug data: %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestRawScriptDebugHTTPFailsClosedOnSettingsError(t *testing.T) {
	t.Setenv("APEXLOAD_ENV", "development")
	server := NewServer(ServerConfig{})
	server.runner.settingsStore = &failingWebsocketSettingsStore{}
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/script-debug/sessions/example"},
		{http.MethodGet, "/api/v1/replay/sessions/example/export"},
		{http.MethodPost, "/api/v1/script-debug/sessions/example/pause"},
		{http.MethodPost, "/api/v1/scripts/debug/sessions"},
		{http.MethodPost, "/api/v1/scripts/example/replay/sessions"},
	} {
		response := httptest.NewRecorder()
		server.mux.ServeHTTP(response, httptest.NewRequest(tc.method, tc.path, nil))
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s %s: status %d, want 503: %s", tc.method, tc.path, response.Code, response.Body.String())
		}
	}
}

func TestRawScriptDebugHTTPSettingsReadStopsOnRequestCancellation(t *testing.T) {
	t.Setenv("APEXLOAD_ENV", "development")
	server := NewServer(ServerConfig{})
	store := &cancelAwareDebugSettingsStore{entered: make(chan struct{})}
	server.runner.settingsStore = store
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/script-debug/sessions/example", nil).WithContext(ctx)
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		server.mux.ServeHTTP(response, request)
		close(done)
	}()
	select {
	case <-store.entered:
	case <-time.After(time.Second):
		t.Fatal("settings read did not start")
	}
	cancel()
	select {
	case <-done:
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", response.Code)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("settings read did not stop promptly when the request was canceled")
	}
}

func TestRawScriptDebugRequestClassification(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		want         bool
	}{
		{http.MethodPost, "/api/v1/scripts/debug/sessions", true},
		{http.MethodPost, "/api/v1/scripts/a/replay/sessions", true},
		{http.MethodGet, "/api/v1/script-debug/sessions/a/export", true},
		{http.MethodPatch, "/api/v1/replay/sessions/a/breakpoints", true},
		{http.MethodPost, "/api/v1/scripts/a/browser-replay/sessions", false},
		{http.MethodGet, "/api/v1/scripts/a", false},
		{http.MethodGet, "/api/v1/ws", false},
	} {
		if got := isRawScriptDebugRequest(httptest.NewRequest(tc.method, tc.path, nil)); got != tc.want {
			t.Errorf("%s %s: got %v, want %v", tc.method, tc.path, got, tc.want)
		}
	}
}

func TestWebSocketAuthorizerRechecksRoleAndSessionEpoch(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	project := &storage.Project{ID: "project-ws", Name: "WebSocket", CreatedAt: now, UpdatedAt: now}
	if err := store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	user := &storage.User{ID: "user-ws", Subject: "alice", Active: true, SessionEpoch: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveUser(user); err != nil {
		t.Fatal(err)
	}
	binding := &storage.UserRoleBinding{ID: "binding-ws", UserID: user.ID, ScopeType: "project", ScopeID: project.ID, Role: RoleViewer, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveUserRoleBinding(binding); err != nil {
		t.Fatal(err)
	}
	stored, err := store.GetUserBySubject(user.Subject)
	if err != nil {
		t.Fatal(err)
	}
	identity := authenticatedUserFromStoredUser(stored)
	identity.SessionScoped = true
	identity.SessionEpoch = 1
	identity.SessionExpiresAt = now.Add(time.Hour)

	server := NewServer(ServerConfig{Store: store})
	enableTrustedHeaderAuth(t, server)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ws?project_id="+project.ID, nil)
	req = withAuthenticatedContext(req, authResolution{User: identity})
	authorized := server.websocketAuthorizer(req, project.ID)
	if !authorized() {
		t.Fatal("fresh project role should authorize websocket")
	}
	if err := store.DeleteUserRoleBinding(binding.ID); err != nil {
		t.Fatal(err)
	}
	if authorized() {
		t.Fatal("removed project role must revoke websocket")
	}
	if err := store.SaveUserRoleBinding(binding); err != nil {
		t.Fatal(err)
	}
	stored.SessionEpoch = 2
	stored.UpdatedAt = now.Add(time.Minute)
	if err := store.SaveUser(stored); err != nil {
		t.Fatal(err)
	}
	if authorized() {
		t.Fatal("bumped session epoch must revoke websocket")
	}
}

func TestWebSocketAuthorizerRechecksExactLocalSessionAfterLogout(t *testing.T) {
	server, store := newLocalAuthTestServer(t, false)
	firstLogin, firstCookie := localLogin(t, server, "dev", "dev")
	secondLogin, secondCookie := localLogin(t, server, "dev", "dev")
	if firstLogin.Code != http.StatusOK || firstCookie == nil || secondLogin.Code != http.StatusOK || secondCookie == nil {
		t.Fatalf("logins = (%d, %v), (%d, %v)", firstLogin.Code, firstCookie, secondLogin.Code, secondCookie)
	}

	authorizerFor := func(cookie *http.Cookie) func() bool {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/ws?project_id="+storage.DefaultProjectID, nil)
		request.AddCookie(cookie)
		resolution := resolveLocalSession(request, store)
		if resolution.User == nil || resolution.Status != "active" {
			t.Fatalf("local session resolution = %#v", resolution)
		}
		request = withAuthenticatedContext(request, authResolution{User: resolution.User})
		return server.websocketAuthorizer(request, storage.DefaultProjectID)
	}
	firstAuthorized := authorizerFor(firstCookie)
	secondAuthorized := authorizerFor(secondCookie)
	if !firstAuthorized() || !secondAuthorized() {
		t.Fatal("fresh local sessions should authorize their websocket snapshots")
	}

	logout := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	logout.AddCookie(firstCookie)
	logout.Header.Set("X-APEXLoad-CSRF-Token", localLogoutCSRFToken(firstCookie.Value))
	logoutResponse := httptest.NewRecorder()
	server.mux.ServeHTTP(logoutResponse, logout)
	if logoutResponse.Code != http.StatusOK {
		t.Fatalf("logout = %d body=%s", logoutResponse.Code, logoutResponse.Body.String())
	}
	if firstAuthorized() {
		t.Fatal("deleted local session continued to authorize an existing websocket")
	}
	if !secondAuthorized() {
		t.Fatal("logging out one local session revoked a separate session")
	}
	if _, err := server.activateLocalAuthGenerationForStartup(); err != nil {
		t.Fatalf("rotate local auth generation: %v", err)
	}
	if secondAuthorized() {
		t.Fatal("controller restart generation continued to authorize an existing websocket")
	}
}

func TestWebSocketAuthorizerRejectsRotatedServiceAccountKey(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	if err := store.SaveProject(&storage.Project{ID: "project-ws", Name: "WebSocket Project", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	account := &storage.ServiceAccount{
		ID:           "svc-ws",
		Name:         "WS",
		KeyHash:      hashAPIKey("initial-ws-transport-key"),
		Active:       true,
		ProjectRoles: map[string][]string{"project-ws": {RoleViewer}},
		Scopes:       []string{"read"},
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := store.SaveServiceAccount(account); err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{Store: store})
	enableTrustedHeaderAuth(t, server)
	serviceIdentity := &AuthenticatedServiceAccount{ID: account.ID, Name: account.Name, Scopes: account.Scopes}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ws?project_id=project-ws", nil)
	req.Header.Set("X-APEXLoad-API-Key", "initial-ws-transport-key")
	req = withAuthenticatedContext(req, authResolution{User: authenticatedUserFromServiceAccount(account), ServiceAccount: serviceIdentity})
	authorized := server.websocketAuthorizer(req, "project-ws")
	if !authorized() {
		t.Fatal("active service account should authorize websocket")
	}
	if err := store.RotateServiceAccountKey(account.ID, hashAPIKey("rotated-ws-transport-key"), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if authorized() {
		t.Fatal("rotated service-account key must revoke websocket")
	}
}

func TestWebSocketAuthorizerRechecksServiceAccountScopes(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	if err := store.SaveProject(&storage.Project{ID: "project-ws", Name: "WebSocket Project", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	account := &storage.ServiceAccount{
		ID:           "svc-ws-scopes",
		Name:         "Scoped WS",
		KeyHash:      hashAPIKey("unchanged-ws-transport-key"),
		Active:       true,
		ProjectRoles: map[string][]string{"project-ws": {RoleViewer}},
		Scopes:       []string{"read"},
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := store.SaveServiceAccount(account); err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{Store: store})
	enableTrustedHeaderAuth(t, server)
	serviceIdentity := &AuthenticatedServiceAccount{ID: account.ID, Name: account.Name, Scopes: account.Scopes}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ws?project_id=project-ws", nil)
	req.Header.Set("X-APEXLoad-API-Key", "unchanged-ws-transport-key")
	req = withAuthenticatedContext(req, authResolution{User: authenticatedUserFromServiceAccount(account), ServiceAccount: serviceIdentity})
	authorized := server.websocketAuthorizer(req, "project-ws")
	if !authorized() {
		t.Fatal("read-scoped service account should authorize websocket")
	}

	account.Scopes = []string{"scripts:write"}
	account.UpdatedAt = now.Add(time.Minute)
	updated, err := store.UpdateServiceAccountMetadata(account, now)
	if err != nil || !updated {
		t.Fatalf("UpdateServiceAccountMetadata = %v, %v", updated, err)
	}
	if authorized() {
		t.Fatal("removing the websocket read scope must revoke the existing socket")
	}
}

func TestHubRejectsRegistrationAfterStop(t *testing.T) {
	hub := NewHub()
	hub.Stop()
	// Stop is intentionally idempotent because shutdown paths can converge.
	hub.Stop()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.serveWS(w, r, "", "")
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return // A handshake-level rejection is also acceptable.
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("websocket registered after hub shutdown")
	}
}

// Authentication succeeds first, then the durable key rotates before socket
// construction. The old request must not acquire the new credential's authority.
func TestWebSocketAuthorizerPinsPresentedCredentialAcrossAdmissionRotation(t *testing.T) {
	for _, header := range []string{"api_key", "bearer"} {
		t.Run(header, func(t *testing.T) {
			store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "websocket-boundary.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			now := time.Now().UTC()
			oldKey, newKey := "old-ws-boundary-key", "new-ws-boundary-key"
			account := &storage.ServiceAccount{ID: "ws-boundary-account", Name: "WebSocket boundary", KeyHash: hashAPIKey(oldKey), Active: true,
				ProjectRoles: map[string][]string{storage.DefaultProjectID: {RoleViewer}}, Scopes: []string{"read"}, CreatedAt: now, UpdatedAt: now}
			if err := store.SaveServiceAccount(account); err != nil {
				t.Fatal(err)
			}
			server := &Server{store: store, runner: &TestRunner{settings: SystemSettings{Auth: AuthSettings{Mode: authModeTrustedHeaders}}}}
			rotate, entered := false, false
			protected := authMiddleware(func() AuthSettings { return AuthSettings{Mode: authModeTrustedHeaders} }, store, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				entered = true
				identity, ok := AuthenticatedServiceAccountFromContext(r.Context())
				if !ok || identity == nil || identity.ID != account.ID {
					t.Fatal("genuine middleware account was not admitted")
				}
				if rotate {
					if err := store.RotateServiceAccountKey(account.ID, hashAPIKey(newKey), now.Add(time.Minute)); err != nil {
						t.Fatal(err)
					}
				}
				if server.websocketAuthorizer(r, storage.DefaultProjectID)() {
					w.WriteHeader(http.StatusOK)
				} else {
					w.WriteHeader(http.StatusForbidden)
				}
			}))
			request := func(key string) int {
				entered = false
				r := httptest.NewRequest(http.MethodGet, "/api/v1/ws?project_id="+storage.DefaultProjectID, nil)
				if header == "api_key" {
					r.Header.Set("X-APEXLoad-API-Key", key)
				} else {
					r.Header.Set("Authorization", "Bearer "+key)
				}
				response := httptest.NewRecorder()
				protected.ServeHTTP(response, r)
				return response.Code
			}
			if status := request(oldKey); !entered || status != http.StatusOK {
				t.Fatalf("fresh admitted key status=%d entered=%v", status, entered)
			}
			rotate = true
			if status := request(oldKey); !entered || status != http.StatusForbidden {
				t.Fatalf("rotated admitted key status=%d entered=%v", status, entered)
			}
			rotate = false
			if status := request(oldKey); entered || status != http.StatusUnauthorized {
				t.Fatalf("revoked credential admission status=%d entered=%v", status, entered)
			}
			if status := request(newKey); !entered || status != http.StatusOK {
				t.Fatalf("new credential admission status=%d entered=%v", status, entered)
			}
			missing := withAuthenticatedContext(httptest.NewRequest(http.MethodGet, "/api/v1/ws", nil), authResolution{User: authenticatedUserFromServiceAccount(account), ServiceAccount: &AuthenticatedServiceAccount{ID: account.ID, Scopes: []string{"read"}}})
			if server.websocketAuthorizer(missing, storage.DefaultProjectID)() {
				t.Fatal("account context without a presented credential minted authority")
			}
		})
	}
}
