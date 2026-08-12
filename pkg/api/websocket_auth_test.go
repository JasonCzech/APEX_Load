package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jasonczech/apexload/pkg/storage"
)

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
		KeyHash:      "initial-key-hash",
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
	req = withAuthenticatedContext(req, authResolution{User: authenticatedUserFromServiceAccount(account), ServiceAccount: serviceIdentity})
	authorized := server.websocketAuthorizer(req, "project-ws")
	if !authorized() {
		t.Fatal("active service account should authorize websocket")
	}
	if err := store.RotateServiceAccountKey(account.ID, "rotated-key-hash", now.Add(time.Minute)); err != nil {
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
		KeyHash:      "unchanged-key-hash",
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
