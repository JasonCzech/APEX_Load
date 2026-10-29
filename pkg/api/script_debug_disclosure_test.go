package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jasonczech/apexload/pkg/storage"
)

type debugRecordingFanout struct{ messages chan []byte }

func (b *debugRecordingFanout) Publish(_ context.Context, _ string, data []byte) error {
	b.messages <- append([]byte(nil), data...)
	return nil
}
func (*debugRecordingFanout) Subscribe(context.Context, string) (FanoutSubscription, error) {
	return nil, errors.New("not used")
}

func TestDebuggerWebSocketEvidenceNeverEntersSharedFanout(t *testing.T) {
	hub := NewHub()
	defer hub.Stop()
	bus := &debugRecordingFanout{messages: make(chan []byte, 4)}
	hub.SetFanout(bus, "controller-a", "events")
	hub.BroadcastJSON(map[string]any{"type": "script_debug_event", "project_id": "project-a", "event": map[string]string{"token": "raw-debug-token"}})
	hub.BroadcastScopedJSON("", "project-a", map[string]any{"type": "script_debug_paused", "project_id": "project-a", "session": map[string]string{"secret": "another-token"}})
	hub.BroadcastJSON(map[string]any{"type": "metrics", "project_id": "project-a"})
	hub.fanoutWG.Wait()
	if len(bus.messages) != 1 {
		t.Fatalf("fanout received %d messages, want only ordinary metrics", len(bus.messages))
	}
	if payload := string(<-bus.messages); strings.Contains(payload, "raw-debug-token") || strings.Contains(payload, "another-token") || !strings.Contains(payload, "metrics") {
		t.Fatalf("unexpected shared fanout payload %s", payload)
	}
}

func TestScriptDebugRawSessionsRequireProjectEditor(t *testing.T) {
	server, _ := baselineTestServer(t)
	enableTrustedHeaderAuth(t, server)
	session := retainedDebugHARSession()
	session.ProjectID = storage.DefaultProjectID
	session.FinalReport.FinalVariables = map[string]string{"token": "private-debug-token"}
	server.scriptDebug.sessions[session.ID] = session

	for _, route := range []string{"script-debug", "replay"} {
		for _, suffix := range []string{"", "/export?format=json"} {
			for _, tc := range []struct {
				name, roles string
				want        int
			}{
				{"viewer", storage.DefaultProjectID + "=" + RoleViewer, http.StatusForbidden},
				{"editor", storage.DefaultProjectID + "=" + RoleEditor, http.StatusOK},
				{"foreign editor", "other-project=" + RoleEditor, http.StatusForbidden},
			} {
				t.Run(route+suffix+tc.name, func(t *testing.T) {
					req := httptest.NewRequest(http.MethodGet, "/api/v1/"+route+"/sessions/"+session.ID+suffix, nil)
					req.Header.Set("X-Auth-Request-User", "operator")
					req.Header.Set("X-APEXLoad-Project-Roles", tc.roles)
					response := httptest.NewRecorder()
					server.mux.ServeHTTP(response, req)
					if response.Code != tc.want {
						t.Fatalf("status %d: %s", response.Code, response.Body.String())
					}
					if got := strings.Contains(response.Body.String(), "private-debug-token"); got != (tc.want == http.StatusOK) {
						t.Fatalf("raw token disclosure = %v, status %d", got, response.Code)
					}
				})
			}
		}
	}
}

func TestDebuggerWebSocketFiltersLiveMessages(t *testing.T) {
	hub := NewHub()
	go hub.Run()
	defer hub.Stop()
	var permitted atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.serveProjectWS(w, r, "project-a", func() bool { return true }, func(_ context.Context, projectID string) bool {
			return permitted.Load() && projectID == "project-a"
		})
	}))
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	waitForHubClientCount(t, hub, 1)
	read := func() string {
		t.Helper()
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		_, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	debug := map[string]any{"type": "script_debug_event", "project_id": "project-a", "secret": "raw-token"}
	normal := map[string]any{"type": "metrics", "project_id": "project-a"}
	hub.BroadcastJSON(debug)
	hub.BroadcastJSON(normal)
	if got := read(); strings.Contains(got, "raw-token") || !strings.Contains(got, "metrics") {
		t.Fatalf("viewer received %s", got)
	}
	permitted.Store(true)
	hub.BroadcastJSON(debug)
	if got := read(); !strings.Contains(got, "raw-token") {
		t.Fatalf("editor did not receive debug event: %s", got)
	}
	permitted.Store(false)
	hub.BroadcastJSON(debug)
	hub.BroadcastJSON(normal)
	if got := read(); strings.Contains(got, "raw-token") || !strings.Contains(got, "metrics") {
		t.Fatalf("revoked editor received %s", got)
	}
}

func TestDebuggerWebSocketDeliveryRechecksProjectEditor(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "debug-ws.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	for _, id := range []string{"project-a", "project-b"} {
		if err := store.SaveProject(&storage.Project{ID: id, Name: id, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	user := &storage.User{ID: "debug-user", Subject: "debug-user", Active: true, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveUser(user); err != nil {
		t.Fatal(err)
	}
	binding := &storage.UserRoleBinding{ID: "debug-binding", UserID: user.ID, ScopeType: "project", ScopeID: "project-a", Role: RoleViewer, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveUserRoleBinding(binding); err != nil {
		t.Fatal(err)
	}
	stored, err := store.GetUserBySubject(user.Subject)
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{Store: store})
	enableTrustedHeaderAuth(t, server)
	req := withAuthenticatedContext(httptest.NewRequest(http.MethodGet, "/api/v1/ws?project_id=project-a", nil), authResolution{User: authenticatedUserFromStoredUser(stored)})
	authorize := server.websocketScopedAuthorizer(req)
	client := &Client{debugAuthorized: func(_ context.Context, projectID string) bool { return authorize(projectID, true) }}
	debug := newHubBroadcast([]byte(`{"type":"script_debug_event","project_id":"project-a","event":{"value":"secret"}}`))
	other := newHubBroadcast([]byte(`{"type":"script_debug_event","project_id":"project-b","event":{"value":"secret"}}`))
	normal := newHubBroadcast([]byte(`{"type":"metrics","project_id":"project-a"}`))
	if !authorize("project-a", false) || client.mayDeliverMessage(debug) || !client.mayDeliverMessage(normal) {
		t.Fatal("viewer must receive ordinary events but not raw debugger events")
	}
	if err := store.DeleteUserRoleBinding(binding.ID); err != nil {
		t.Fatal(err)
	}
	binding.ID = "debug-editor-binding"
	binding.Role = RoleEditor
	if err := store.SaveUserRoleBinding(binding); err != nil {
		t.Fatal(err)
	}
	if !client.mayDeliverMessage(debug) || client.mayDeliverMessage(other) {
		t.Fatal("editor should receive only debugger events for authorized projects")
	}
	if err := store.DeleteUserRoleBinding(binding.ID); err != nil {
		t.Fatal(err)
	}
	if client.mayDeliverMessage(debug) {
		t.Fatal("raw queued event was delivered after role revocation")
	}
	if (&Client{}).mayDeliverMessage(debug) {
		t.Fatal("a connection without a debug authorizer must fail closed")
	}
}

func TestDebuggerWebSocketHonorsDevPreviewProject(t *testing.T) {
	server := NewServer(ServerConfig{})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/ws", nil)
	request.Header.Set(devViewHeader, devViewModeUser)
	request.Header.Set(devProjectIDHeader, "project-a")
	authorize := server.websocketScopedAuthorizer(request)
	if !authorize("project-a", true) || authorize("project-b", true) || authorize("", true) {
		t.Fatal("dev user preview must receive raw events only for its selected editor project")
	}
	request.Header.Set(devViewHeader, devViewModeAdmin)
	admin := server.websocketScopedAuthorizer(request)
	if !admin("project-a", true) || !admin("project-b", true) {
		t.Fatal("dev admin preview should retain its unrestricted access")
	}
}

func TestDebuggerWebSocketTrustedHeaderBootstrapEndsWhenUserIsOnboarded(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "bootstrap-ws.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})
	enableTrustedHeaderAuth(t, server)
	identity := &AuthenticatedUser{Subject: "bootstrap", GlobalRoles: map[string]struct{}{RoleEditor: {}}}
	request := withAuthenticatedContext(httptest.NewRequest(http.MethodGet, "/api/v1/ws", nil), authResolution{User: identity})
	authorize := server.websocketScopedAuthorizer(request)
	if !authorize("project-a", true) {
		t.Fatal("non-production bootstrap editor was rejected")
	}
	now := time.Now().UTC()
	if err := store.SaveUser(&storage.User{ID: "onboarded", Subject: "onboarded", Active: true, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if authorize("project-a", true) {
		t.Fatal("bootstrap socket retained access after onboarding closed the window")
	}
}

func TestDebuggerWebSocketOIDCBootstrapChecksExpiryAndOnboarding(t *testing.T) {
	t.Setenv("APEXLOAD_OIDC_CLIENT_SECRET", "test-client-secret")
	t.Setenv("APEXLOAD_AUTH_SESSION_SECRET", "test-session-secret")
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "bootstrap-oidc-ws.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})
	enableOIDCAuth(t, server, "http://127.0.0.1:9999")
	now := time.Now().UTC()
	identity := &AuthenticatedUser{Subject: "oidc-bootstrap", SessionScoped: true, SessionExpiresAt: now.Add(time.Hour), GlobalRoles: map[string]struct{}{RoleEditor: {}}}
	authorizer := func(user *AuthenticatedUser) func(string, bool) bool {
		request := withAuthenticatedContext(httptest.NewRequest(http.MethodGet, "/api/v1/ws", nil), authResolution{User: user})
		return server.websocketScopedAuthorizer(request)
	}
	if !authorizer(identity)("project-a", true) {
		t.Fatal("unexpired development OIDC bootstrap editor was rejected")
	}
	expired := *identity
	expired.SessionExpiresAt = now.Add(-time.Second)
	if authorizer(&expired)("project-a", true) {
		t.Fatal("expired OIDC session retained access")
	}
	missingExpiry := *identity
	missingExpiry.SessionExpiresAt = time.Time{}
	if authorizer(&missingExpiry)("project-a", true) {
		t.Fatal("OIDC session without expiry retained bootstrap access")
	}
	if err := store.SaveUser(&storage.User{ID: "onboarded", Subject: "onboarded", Active: true, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if authorizer(identity)("project-a", true) {
		t.Fatal("OIDC bootstrap socket retained access after onboarding")
	}
}
