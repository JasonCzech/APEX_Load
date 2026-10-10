package api

import (
	"encoding/json"
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

func TestFleetDegradedEventsRespectWebSocketScopes(t *testing.T) {
	for _, scope := range []struct {
		name, testID, projectID string
		want                    []string
	}{
		{name: "project", projectID: "project-a", want: []string{"test-a2", "test-a"}},
		{name: "test", testID: "test-a", want: []string{"test-a"}},
		{name: "global", want: []string{"test-b", "test-a2", "test-a"}},
	} {
		t.Run(scope.name, func(t *testing.T) {
			hub := NewHub()
			go hub.Run()
			defer hub.Stop()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hub.serveWS(w, r, scope.testID, scope.projectID)
			}))
			defer server.Close()
			conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			waitForHubClientCount(t, hub, 1)
			backend := &distributedExecutionBackend{}
			runner := &TestRunner{hub: hub}
			for _, run := range []*ManagedTest{
				{ID: "test-b", ProjectID: "project-b", Name: "Foreign test"},
				{ID: "test-a2", ProjectID: "project-a", Name: "Other test in project"},
				{ID: "test-a", ProjectID: "project-a", Name: "Authorized test"},
			} {
				backend.broadcastFleetDegraded(runner, run, "internal.example:9090", errors.New("stream failed"), nil)
			}
			for _, want := range scope.want {
				_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
				_, payload, err := conn.ReadMessage()
				if err != nil {
					t.Fatal(err)
				}
				var event struct {
					Data struct {
						TestID string `json:"test_id"`
					} `json:"data"`
				}
				if err := json.Unmarshal(payload, &event); err != nil {
					t.Fatal(err)
				}
				if event.Data.TestID != want {
					t.Fatalf("subscriber received %s; want next test %s", payload, want)
				}
			}
		})
	}
}

func TestWebSocketAuthorizerRejectsReplacementSubjectIdentity(t *testing.T) {
	for _, sessionScoped := range []bool{false, true} {
		name := "trusted headers"
		if sessionScoped {
			name = "OIDC session"
		}
		t.Run(name, func(t *testing.T) {
			store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "ws.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			now := time.Now().UTC()
			original := &storage.User{ID: "old-user", Subject: "reused-subject", Active: true, CreatedAt: now, UpdatedAt: now}
			if err := store.SaveUser(original); err != nil {
				t.Fatal(err)
			}
			binding := &storage.UserRoleBinding{ID: "old-viewer", UserID: original.ID, ScopeType: "global", Role: RoleViewer, CreatedAt: now, UpdatedAt: now}
			if err := store.SaveUserRoleBinding(binding); err != nil {
				t.Fatal(err)
			}
			original, err = store.GetUserBySubject(original.Subject)
			if err != nil {
				t.Fatal(err)
			}
			identity := authenticatedUserFromStoredUser(original)
			identity.SessionScoped = sessionScoped
			identity.SessionExpiresAt = now.Add(time.Hour)
			server := NewServer(ServerConfig{Store: store})
			enableTrustedHeaderAuth(t, server)
			req := withAuthenticatedContext(httptest.NewRequest(http.MethodGet, "/api/v1/ws", nil), authResolution{User: identity})
			authorized := server.websocketAuthorizer(req, "")
			if !authorized() {
				t.Fatal("initial identity rejected")
			}
			original.Subject = "renamed-subject"
			if err := store.SaveUser(original); err != nil {
				t.Fatal(err)
			}
			replacement := &storage.User{ID: "new-user", Subject: "reused-subject", Active: true, CreatedAt: now, UpdatedAt: now}
			if err := store.SaveUser(replacement); err != nil {
				t.Fatal(err)
			}
			binding.ID, binding.UserID = "replacement-viewer", replacement.ID
			if err := store.SaveUserRoleBinding(binding); err != nil {
				t.Fatal(err)
			}
			if authorized() {
				t.Fatal("old websocket inherited replacement subject's grants")
			}
			stored, err := store.GetUserBySubject(replacement.Subject)
			if err != nil {
				t.Fatal(err)
			}
			req = withAuthenticatedContext(httptest.NewRequest(http.MethodGet, "/api/v1/ws", nil), authResolution{User: authenticatedUserFromStoredUser(stored)})
			if !server.websocketAuthorizer(req, "")() {
				t.Fatal("replacement's own authenticated websocket was rejected")
			}
		})
	}
}
