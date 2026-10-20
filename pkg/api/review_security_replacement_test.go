package api

import (
	"database/sql"
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

type reviewSecurityReplacementStore struct {
	*storage.SQLiteStore
	path string
}

const reviewSecurityTestID = "e7d015b4-9925-4553-b260-0a331973af70"
const reviewSecurityProjectA = "review-security-project-a"
const reviewSecurityProjectB = "review-security-project-b"
const reviewSecurityAPIKey = "review-security-project-a-api-key"

func reviewSecurityReplacementFixture(t *testing.T) (*Server, *reviewSecurityReplacementStore) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "review-security.db")
	sqlite, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlite.Close() })
	store := &reviewSecurityReplacementStore{SQLiteStore: sqlite, path: dbPath}
	now := time.Now().UTC()
	for _, projectID := range []string{reviewSecurityProjectA, reviewSecurityProjectB} {
		if err := store.SaveProject(&storage.Project{ID: projectID, Name: projectID, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2})
	enableTrustedHeaderAuth(t, server)
	for _, projectID := range []string{reviewSecurityProjectA, reviewSecurityProjectB} {
		if _, err := server.runner.StoreScriptDurable(libraryTestScript("script-"+projectID, "Script "+projectID, projectID, "/review")); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SaveServiceAccount(&storage.ServiceAccount{
		ID: "review-security-account", Name: "Project A only", KeyHash: hashAPIKey(reviewSecurityAPIKey), Active: true,
		ProjectRoles: map[string][]string{reviewSecurityProjectA: {RoleEditor}}, Scopes: []string{"automation:*"}, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	reviewSecurityCreateTest(t, server, reviewSecurityProjectA, "original authorized A run")
	return server, store
}

func reviewSecurityCreateTest(t *testing.T, server *Server, projectID, name string) *ManagedTest {
	t.Helper()
	run, err := server.runner.CreateTest(CreateTestRequest{
		RequestedTestID: reviewSecurityTestID, Name: name, Duration: "30s", ProjectID: projectID,
		ScriptID: "script-" + projectID, VUsers: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func reviewSecurityDeleteOriginal(t *testing.T, server *Server) {
	t.Helper()
	deleted, err := server.runner.DeleteTestWithError(reviewSecurityTestID)
	if err != nil || !deleted {
		t.Fatalf("delete original = %v, %v", deleted, err)
	}
}

// reviewSecurityLegacyReplaceTest models an ID deleted before deleted-test
// identities were recorded: nothing durable keeps it in project A, so project B
// can reuse it.
func reviewSecurityLegacyReplaceTest(t *testing.T, server *Server, store *reviewSecurityReplacementStore) {
	t.Helper()
	reviewSecurityDeleteOriginal(t, server)
	db, err := sql.Open("sqlite", store.path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`DELETE FROM deleted_test_identities WHERE test_id = ?`, reviewSecurityTestID); err != nil {
		t.Fatal(err)
	}
	reviewSecurityCreateTest(t, server, reviewSecurityProjectB, "FOREIGN PROJECT B PRIVATE RUN")
}

func reviewSecurityRequest(server *Server, method, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("X-APEXLoad-API-Key", reviewSecurityAPIKey)
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, req)
	return response
}

// A deleted test's ID cannot move to another project, so a request authorized
// against project A's run can never reach a project B run under the same ID.
// Re-creation in the same project (CI idempotency keys) still works.
func TestReviewSecurityDeletedTestIDCannotMoveToAnotherProject(t *testing.T) {
	server, _ := reviewSecurityReplacementFixture(t)
	reviewSecurityDeleteOriginal(t, server)
	_, err := server.runner.CreateTest(CreateTestRequest{
		RequestedTestID: reviewSecurityTestID, Name: "FOREIGN PROJECT B PRIVATE RUN", Duration: "30s", ProjectID: reviewSecurityProjectB,
		ScriptID: "script-" + reviewSecurityProjectB, VUsers: 1,
	})
	if !errors.Is(err, errTestIDOwnedByAnotherProject) {
		t.Fatalf("cross-project ID reuse = %v, want refusal", err)
	}
	body := `{"requested_test_id":"` + reviewSecurityTestID + `","name":"via API","duration":"30s","project_id":"` + reviewSecurityProjectB + `","script_id":"script-` + reviewSecurityProjectB + `","vusers":1}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tests", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.handleCreateTest(response, req)
	if response.Code != http.StatusConflict {
		t.Fatalf("API cross-project ID reuse = %d %s, want 409", response.Code, response.Body.String())
	}
	if again := reviewSecurityCreateTest(t, server, reviewSecurityProjectA, "same project again"); again.ProjectID != reviewSecurityProjectA {
		t.Fatalf("same-project re-creation = %+v", again)
	}
}

// A test stream stays bound to the project it was authorized for, even for an
// ID reused across projects before deleted-test identities were recorded.
func TestReviewSecurityTestStreamRejectsCrossProjectIDReuse(t *testing.T) {
	server, store := reviewSecurityReplacementFixture(t)
	go server.hub.Run()
	defer server.hub.Stop()
	httpServer := httptest.NewServer(server.mux)
	defer httpServer.Close()
	headers := http.Header{"X-APEXLoad-API-Key": []string{reviewSecurityAPIKey}}
	connection, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(httpServer.URL, "http")+"/api/v1/tests/"+reviewSecurityTestID+"/stream", headers)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	waitForHubClientCount(t, server.hub, 1)
	reviewSecurityLegacyReplaceTest(t, server, store)
	fresh := reviewSecurityRequest(server, http.MethodGet, "/api/v1/tests/"+reviewSecurityTestID)
	if fresh.Code != http.StatusForbidden {
		t.Fatalf("foreign GET = %d body=%s", fresh.Code, fresh.Body.String())
	}
	// The same authorizer closure used by the live connection still grants A,
	// proving this leak persists after the periodic revalidation interval.
	server.hub.clientsMu.RLock()
	var authorized func() bool
	for client := range server.hub.clients {
		authorized = client.authorized
	}
	server.hub.clientsMu.RUnlock()
	if authorized == nil || !authorized() {
		t.Fatal("fixture authorizer does not retain project A grant")
	}
	_ = connection.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		_, payload, err := connection.ReadMessage()
		if err != nil {
			return
		}
		var event struct {
			ProjectID string `json:"project_id"`
		}
		if err := json.Unmarshal(payload, &event); err != nil {
			t.Fatal(err)
		}
		if event.ProjectID == reviewSecurityProjectB {
			t.Fatalf("project-A-only stream disclosed replacement B event: %s", payload)
		}
	}
}

func TestReviewSecurityProjectSocketDoesNotReceiveAdminFleetEvents(t *testing.T) {
	server, store := reviewSecurityReplacementFixture(t)
	denied := reviewSecurityRequest(server, http.MethodGet, "/api/v1/fleet/pools")
	if denied.Code != http.StatusForbidden {
		t.Fatalf("fleet pools GET = %d: %s", denied.Code, denied.Body.String())
	}
	now := time.Now().UTC()
	const adminKey = "review-security-admin-api-key"
	if err := store.SaveServiceAccount(&storage.ServiceAccount{
		ID: "review-security-admin", Name: "Admin", KeyHash: hashAPIKey(adminKey), Active: true,
		GlobalRoles: []string{RoleSystemAdmin}, Scopes: []string{"automation:*"}, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	go server.hub.Run()
	defer server.hub.Stop()
	httpServer := httptest.NewServer(server.mux)
	defer httpServer.Close()
	headers := http.Header{"X-APEXLoad-API-Key": []string{reviewSecurityAPIKey}}
	connection, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(httpServer.URL, "http")+"/api/v1/ws?project_id="+reviewSecurityProjectA, headers)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	waitForHubClientCount(t, server.hub, 1)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/fleet/pools", strings.NewReader(`{"name":"PRIVATE OTHER TENANT FLEET POOL","cloud_provider":"kubernetes"}`))
	req.Header.Set("X-APEXLoad-API-Key", adminKey)
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, req)
	if response.Code != http.StatusCreated {
		t.Fatalf("admin pool creation = %d: %s", response.Code, response.Body.String())
	}
	_ = connection.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		_, payload, err := connection.ReadMessage()
		if err != nil {
			return
		}
		if strings.Contains(string(payload), "PRIVATE OTHER TENANT FLEET POOL") {
			t.Fatalf("project-only viewer received admin-only fleet pool: %s", payload)
		}
	}
}
