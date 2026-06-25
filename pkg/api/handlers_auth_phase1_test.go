package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

func phase1Store(t *testing.T) *storage.SQLiteStore {
	t.Helper()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func seedAdmin(t *testing.T, store *storage.SQLiteStore, subject string) *storage.User {
	t.Helper()
	now := time.Now().UTC()
	user := &storage.User{ID: uuid.New().String(), Subject: subject, Active: true, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveUser(user); err != nil {
		t.Fatalf("SaveUser(%s) returned error: %v", subject, err)
	}
	if err := store.SaveUserRoleBinding(&storage.UserRoleBinding{ID: uuid.New().String(), UserID: user.ID, ScopeType: "global", Role: RoleSystemAdmin, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveUserRoleBinding(%s) returned error: %v", subject, err)
	}
	return user
}

// A1 — a project-scoped admin must only see user-activity for projects they administer.
func TestUserActivityScopedToAdministeredProjects(t *testing.T) {
	store := phase1Store(t)
	for _, id := range []string{"proj-a", "proj-b"} {
		if err := store.SaveProject(&storage.Project{ID: id, Name: id, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}); err != nil {
			t.Fatalf("SaveProject(%s) returned error: %v", id, err)
		}
	}
	seedEvent := func(projectID, action string) {
		if err := store.SaveAuditEvent(&storage.AuditEvent{ID: uuid.New().String(), Action: action, ActorType: "user", ActorSubject: "actor", ProjectID: projectID, ResourceType: "test", ResourceID: "t", Status: "success", CreatedAt: time.Now().UTC()}); err != nil {
			t.Fatalf("SaveAuditEvent returned error: %v", err)
		}
	}
	seedEvent("proj-a", "a.one")
	seedEvent("proj-a", "a.two")
	seedEvent("proj-b", "b.one")

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	scopedActivity := func(t *testing.T, query string, headers map[string]string) (*httptest.ResponseRecorder, AuthUserActivityResponse) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/user-activity"+query, nil)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		var payload AuthUserActivityResponse
		if resp.Code == http.StatusOK {
			if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
				t.Fatalf("unmarshal user-activity: %v", err)
			}
		}
		return resp, payload
	}

	scopedHeaders := map[string]string{"X-Auth-Request-User": "scoped-admin", "X-APEXLoad-Project-Roles": "proj-a=" + RoleProjectAdmin}

	// (a) no project_id → only the administered project's events.
	resp, payload := scopedActivity(t, "", scopedHeaders)
	if resp.Code != http.StatusOK {
		t.Fatalf("scoped activity status = %d, want 200 body=%s", resp.Code, resp.Body.String())
	}
	if payload.Count != 2 {
		t.Fatalf("scoped activity count = %d, want 2 (proj-a only)", payload.Count)
	}
	for _, event := range payload.Events {
		if event.ProjectID != "proj-a" {
			t.Fatalf("scoped activity leaked event for project %q", event.ProjectID)
		}
	}

	// (b) explicit out-of-scope project_id → 403.
	if resp, _ := scopedActivity(t, "?project_id=proj-b", scopedHeaders); resp.Code != http.StatusForbidden {
		t.Fatalf("out-of-scope project_id status = %d, want 403 body=%s", resp.Code, resp.Body.String())
	}

	// (c) global system admin → unrestricted: must see other projects' events
	// (the proj-b event the scoped admin could not), unlike the scoped admin.
	globalHeaders := map[string]string{"X-Auth-Request-User": "global-admin", "X-APEXLoad-Roles": RoleSystemAdmin}
	resp, payload = scopedActivity(t, "", globalHeaders)
	if resp.Code != http.StatusOK {
		t.Fatalf("global activity status = %d, want 200 body=%s", resp.Code, resp.Body.String())
	}
	projectEvents := map[string]int{}
	for _, event := range payload.Events {
		if event.ProjectID != "" {
			projectEvents[event.ProjectID]++
		}
	}
	if projectEvents["proj-a"] != 2 || projectEvents["proj-b"] != 1 {
		t.Fatalf("global activity project events = %#v, want proj-a:2 proj-b:1 (all projects)", projectEvents)
	}
}

// A3 — an administrator cannot disable their own account.
func TestSystemAdminCannotSelfDisable(t *testing.T) {
	store := phase1Store(t)
	admin := seedAdmin(t, store, "admin-self")
	// A second admin so the failure is unambiguously the self-guard, not last-admin.
	seedAdmin(t, store, "admin-other")

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/users/"+admin.ID+"/disable", nil)
	req.Header.Set("X-Auth-Request-User", admin.Subject)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusConflict {
		t.Fatalf("self-disable status = %d, want 409 body=%s", resp.Code, resp.Body.String())
	}
	if reloaded, err := store.GetUser(admin.ID); err != nil || !reloaded.Active {
		t.Fatalf("self-disable should not have disabled the account (err=%v active=%v)", err, reloaded.Active)
	}
}

// A3 — disabling the last active system administrator is blocked; a second admin unblocks it.
func TestCannotDisableLastSystemAdmin(t *testing.T) {
	store := phase1Store(t)
	admin := seedAdmin(t, store, "human-admin")

	key := "phase1-admin-automation-key"
	sum := sha256.Sum256([]byte(key))
	now := time.Now().UTC()
	if err := store.SaveServiceAccount(&storage.ServiceAccount{ID: "svc-admin", Name: "Admin Automation", KeyHash: fmt.Sprintf("%x", sum[:]), Active: true, GlobalRoles: []string{RoleSystemAdmin}, Scopes: []string{"automation:*"}, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveServiceAccount returned error: %v", err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	disable := func() int {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/users/"+admin.ID+"/disable", nil)
		req.Header.Set("X-APEXLoad-API-Key", key)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		return resp.Code
	}

	if code := disable(); code != http.StatusConflict {
		t.Fatalf("disable last admin status = %d, want 409", code)
	}
	// Add a second admin, then the original is no longer the last one.
	seedAdmin(t, store, "backup-admin")
	if code := disable(); code != http.StatusOK {
		t.Fatalf("disable non-last admin status = %d, want 200", code)
	}
}

// A7 — re-creating an existing binding returns the canonical stored row (same id) with 200.
func TestDuplicateBindingReturnsCanonicalID(t *testing.T) {
	store := phase1Store(t)
	now := time.Now().UTC()
	user := &storage.User{ID: uuid.New().String(), Subject: "binding-target", Active: true, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveUser(user); err != nil {
		t.Fatalf("SaveUser returned error: %v", err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"}) // dev bypass — no auth headers needed

	create := func() (*httptest.ResponseRecorder, storage.UserRoleBinding) {
		body := []byte(`{"scope_type":"project","scope_id":"proj-x","role":"editor"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/users/"+user.ID+"/bindings", bytes.NewReader(body))
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		var binding storage.UserRoleBinding
		_ = json.Unmarshal(resp.Body.Bytes(), &binding)
		return resp, binding
	}

	first, created := create()
	if first.Code != http.StatusCreated {
		t.Fatalf("first binding create status = %d, want 201 body=%s", first.Code, first.Body.String())
	}
	if created.ID == "" {
		t.Fatalf("first binding missing id: %#v", created)
	}

	second, duplicate := create()
	if second.Code != http.StatusOK {
		t.Fatalf("duplicate binding create status = %d, want 200 body=%s", second.Code, second.Body.String())
	}
	if duplicate.ID != created.ID {
		t.Fatalf("duplicate binding id = %q, want canonical %q", duplicate.ID, created.ID)
	}
}

// A12 — creating a service account with no scopes is rejected (least privilege is opt-out).
func TestCreateServiceAccountRequiresScopes(t *testing.T) {
	store := phase1Store(t)
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"}) // dev bypass

	noScopes := httptest.NewRequest(http.MethodPost, "/api/v1/auth/service-accounts", bytes.NewReader([]byte(`{"name":"Scopeless"}`)))
	noScopesResp := httptest.NewRecorder()
	server.mux.ServeHTTP(noScopesResp, noScopes)
	if noScopesResp.Code != http.StatusBadRequest {
		t.Fatalf("no-scope create status = %d, want 400 body=%s", noScopesResp.Code, noScopesResp.Body.String())
	}

	withScopes := httptest.NewRequest(http.MethodPost, "/api/v1/auth/service-accounts", bytes.NewReader([]byte(`{"name":"Reader","scopes":["results:read"]}`)))
	withScopesResp := httptest.NewRecorder()
	server.mux.ServeHTTP(withScopesResp, withScopes)
	if withScopesResp.Code != http.StatusCreated {
		t.Fatalf("scoped create status = %d, want 201 body=%s", withScopesResp.Code, withScopesResp.Body.String())
	}
}

// A13 — creating a user with a duplicate subject returns 409, not a raw 500.
func TestCreateUserDuplicateSubjectReturns409(t *testing.T) {
	store := phase1Store(t)
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"}) // dev bypass

	create := func() int {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/users", bytes.NewReader([]byte(`{"subject":"dup@example.com"}`)))
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		return resp.Code
	}
	if code := create(); code != http.StatusCreated {
		t.Fatalf("first create status = %d, want 201", code)
	}
	if code := create(); code != http.StatusConflict {
		t.Fatalf("duplicate create status = %d, want 409", code)
	}
}

// A10 — retention prune deletes rows older than the window and keeps recent ones.
func TestPruneAuditRetention(t *testing.T) {
	t.Setenv("APEXLOAD_AUDIT_RETENTION_DAYS", "1")
	store := phase1Store(t)
	now := time.Now().UTC()
	if err := store.SaveAuditEvent(&storage.AuditEvent{ID: uuid.New().String(), Action: "old", ActorType: "user", ActorSubject: "a", ResourceType: "t", ResourceID: "1", Status: "success", CreatedAt: now.Add(-72 * time.Hour)}); err != nil {
		t.Fatalf("SaveAuditEvent(old): %v", err)
	}
	if err := store.SaveAuditEvent(&storage.AuditEvent{ID: uuid.New().String(), Action: "recent", ActorType: "user", ActorSubject: "a", ResourceType: "t", ResourceID: "2", Status: "success", CreatedAt: now}); err != nil {
		t.Fatalf("SaveAuditEvent(recent): %v", err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	server.pruneAuditRetention(now)

	events, err := store.ListAuditEvents(storage.AuditEventFilter{Limit: 50})
	if err != nil {
		t.Fatalf("ListAuditEvents: %v", err)
	}
	for _, e := range events {
		if e.Action == "old" {
			t.Fatalf("retention prune left an out-of-window event: %#v", e)
		}
	}
	// The recent event plus the meta prune event should remain.
	var sawPrune bool
	for _, e := range events {
		if e.Action == "auth.audit.retention_prune" {
			sawPrune = true
		}
	}
	if !sawPrune {
		t.Fatalf("expected a retention_prune meta audit event, got %d events", len(events))
	}
}
