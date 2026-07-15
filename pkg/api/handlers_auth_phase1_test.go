package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
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

// A1 — a stale scoped-admin grant must not degrade to an unrestricted activity read.
func TestUserActivityEmptyScopedAdminScopeDoesNotLeak(t *testing.T) {
	store := phase1Store(t)
	now := time.Now().UTC()
	for _, id := range []string{"proj-a", "proj-b"} {
		if err := store.SaveProject(&storage.Project{ID: id, Name: id, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatalf("SaveProject(%s) returned error: %v", id, err)
		}
		if err := store.SaveAuditEvent(&storage.AuditEvent{ID: uuid.New().String(), Action: "auth.test", ActorType: "user", ActorSubject: "actor", ProjectID: id, ResourceType: "test", ResourceID: id, Status: "success", CreatedAt: now}); err != nil {
			t.Fatalf("SaveAuditEvent(%s) returned error: %v", id, err)
		}
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	for _, tc := range []struct {
		name   string
		header string
		value  string
	}{
		{name: "stale project", header: "X-APEXLoad-Project-Roles", value: "deleted-project=" + RoleProjectAdmin},
		{name: "stale domain", header: "X-APEXLoad-Domain-Roles", value: "deleted-domain=" + RoleProjectAdmin},
		{name: "stale repository", header: "X-APEXLoad-Application-Repository-Roles", value: "deleted-repo=" + RoleProjectAdmin},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/user-activity", nil)
			req.Header.Set("X-Auth-Request-User", "stale-admin")
			req.Header.Set(tc.header, tc.value)
			resp := httptest.NewRecorder()
			server.mux.ServeHTTP(resp, req)
			if resp.Code != http.StatusOK {
				t.Fatalf("stale scoped activity status = %d, want 200 body=%s", resp.Code, resp.Body.String())
			}
			var payload AuthUserActivityResponse
			if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
				t.Fatalf("unmarshal stale scoped activity: %v", err)
			}
			if payload.Count != 0 || len(payload.Events) != 0 {
				t.Fatalf("stale scoped activity leaked events: count=%d events=%#v", payload.Count, payload.Events)
			}

			exportReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/user-activity/export", nil)
			exportReq.Header.Set("X-Auth-Request-User", "stale-admin")
			exportReq.Header.Set(tc.header, tc.value)
			exportResp := httptest.NewRecorder()
			server.mux.ServeHTTP(exportResp, exportReq)
			if exportResp.Code != http.StatusOK {
				t.Fatalf("stale scoped export status = %d, want 200 body=%s", exportResp.Code, exportResp.Body.String())
			}
			rows, err := csv.NewReader(strings.NewReader(exportResp.Body.String())).ReadAll()
			if err != nil {
				t.Fatalf("read stale scoped export csv: %v", err)
			}
			if len(rows) != 1 {
				t.Fatalf("stale scoped export rows = %#v, want header only", rows)
			}

			req = httptest.NewRequest(http.MethodGet, "/api/v1/auth/user-activity?project_id=proj-a", nil)
			req.Header.Set("X-Auth-Request-User", "stale-admin")
			req.Header.Set(tc.header, tc.value)
			resp = httptest.NewRecorder()
			server.mux.ServeHTTP(resp, req)
			if resp.Code != http.StatusForbidden {
				t.Fatalf("stale scoped explicit project status = %d, want 403 body=%s", resp.Code, resp.Body.String())
			}
		})
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
	if err := store.SaveProject(&storage.Project{ID: "proj-x", Name: "proj-x", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
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

func TestOnboardUserCreatesUserAndInitialBindings(t *testing.T) {
	store := phase1Store(t)
	now := time.Now().UTC()
	if err := store.SaveProject(&storage.Project{ID: "proj-onboard", Name: "Onboard", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"}) // dev bypass

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/users/onboard", bytes.NewReader([]byte(`{
		"subject":"oidc|new-user",
		"email":"new@example.com",
		"display_name":"New User",
		"bindings":[{"scope_type":"project","scope_id":"proj-onboard","role":"viewer"}]
	}`)))
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("onboard status = %d, want 201 body=%s", resp.Code, resp.Body.String())
	}
	var user storage.User
	if err := json.Unmarshal(resp.Body.Bytes(), &user); err != nil {
		t.Fatalf("unmarshal onboard user: %v", err)
	}
	if user.ID == "" || user.Subject != "oidc|new-user" || len(user.Bindings) != 1 || user.Bindings[0].ScopeID != "proj-onboard" {
		t.Fatalf("onboard user = %#v, want user with project binding", user)
	}
	if reloaded, err := store.GetUserBySubject("oidc|new-user"); err != nil || len(reloaded.Bindings) != 1 {
		t.Fatalf("persisted onboard user = %#v err=%v, want one binding", reloaded, err)
	}

	duplicateReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/users/onboard", bytes.NewReader([]byte(`{"subject":"oidc|new-user","bindings":[{"scope_type":"project","scope_id":"proj-onboard","role":"viewer"}]}`)))
	duplicateResp := httptest.NewRecorder()
	server.mux.ServeHTTP(duplicateResp, duplicateReq)
	if duplicateResp.Code != http.StatusConflict {
		t.Fatalf("duplicate onboard status = %d, want 409 body=%s", duplicateResp.Code, duplicateResp.Body.String())
	}

	invalidReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/users/onboard", bytes.NewReader([]byte(`{"subject":"oidc|bad-scope","bindings":[{"scope_type":"project","scope_id":"missing-project","role":"viewer"}]}`)))
	invalidResp := httptest.NewRecorder()
	server.mux.ServeHTTP(invalidResp, invalidReq)
	if invalidResp.Code != http.StatusBadRequest {
		t.Fatalf("invalid onboard status = %d, want 400 body=%s", invalidResp.Code, invalidResp.Body.String())
	}
	if _, err := store.GetUserBySubject("oidc|bad-scope"); err == nil {
		t.Fatalf("invalid onboard should not create the user")
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

func TestCreateUserConcurrentDuplicateSubjectReturns409(t *testing.T) {
	store := phase1Store(t)
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"}) // dev bypass

	start := make(chan struct{})
	statuses := make(chan int, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/users", bytes.NewReader([]byte(`{"subject":"race@example.com"}`)))
			resp := httptest.NewRecorder()
			server.mux.ServeHTTP(resp, req)
			statuses <- resp.Code
		}()
	}
	close(start)
	got := []int{<-statuses, <-statuses}
	created, conflict := 0, 0
	for _, status := range got {
		switch status {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			conflict++
		default:
			t.Fatalf("concurrent create statuses = %v, want one 201 and one 409", got)
		}
	}
	if created != 1 || conflict != 1 {
		t.Fatalf("concurrent create statuses = %v, want one 201 and one 409", got)
	}
}

func TestUpdateUserRejectsSelfSubjectRename(t *testing.T) {
	store := phase1Store(t)
	admin := seedAdmin(t, store, "rename-self")
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	req := trustedHeaderJSONRequest(http.MethodPatch, "/api/v1/auth/users/"+admin.ID, `{"subject":"renamed-self"}`, "rename-self")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusConflict {
		t.Fatalf("self subject rename status = %d, want 409 body=%s", resp.Code, resp.Body.String())
	}
}

func TestUpdateUserRejectsLastAdminSubjectRename(t *testing.T) {
	store := phase1Store(t)
	admin := seedAdmin(t, store, "last-rename-admin")
	key := "last-rename-key"
	sum := sha256.Sum256([]byte(key))
	now := time.Now().UTC()
	if err := store.SaveServiceAccount(&storage.ServiceAccount{ID: "svc-last-rename", Name: "Rename Automation", KeyHash: fmt.Sprintf("%x", sum[:]), Active: true, GlobalRoles: []string{RoleSystemAdmin}, Scopes: []string{"automation:*"}, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveServiceAccount: %v", err)
	}
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/auth/users/"+admin.ID, bytes.NewReader([]byte(`{"subject":"renamed-last-admin"}`)))
	req.Header.Set("X-APEXLoad-API-Key", key)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusConflict {
		t.Fatalf("last admin subject rename status = %d, want 409 body=%s", resp.Code, resp.Body.String())
	}

	flowStartedAt := time.Now().UTC()
	for _, identityKey := range []string{admin.Subject, "renamed-last-admin", admin.ID} {
		if err := store.SaveGitHubConnection(&storage.GitHubConnection{UserID: identityKey, GitHubLogin: "prior-user", AccessTokenEnc: "encrypted-prior-token"}); err != nil {
			t.Fatalf("SaveGitHubConnection(%s): %v", identityKey, err)
		}
	}
	seedAdmin(t, store, "backup-rename-admin")
	req = httptest.NewRequest(http.MethodPatch, "/api/v1/auth/users/"+admin.ID, bytes.NewReader([]byte(`{"subject":"renamed-last-admin"}`)))
	req.Header.Set("X-APEXLoad-API-Key", key)
	resp = httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("non-last admin subject rename status = %d, want 200 body=%s", resp.Code, resp.Body.String())
	}
	for _, identityKey := range []string{admin.Subject, "renamed-last-admin", admin.ID} {
		if connection, err := store.GetGitHubConnection(identityKey); err != nil || connection != nil {
			t.Fatalf("GitHub connection %s after rename = %#v, %v; want revoked", identityKey, connection, err)
		}
		saved, err := store.SaveGitHubConnectionIfNotRevokedSince(&storage.GitHubConnection{UserID: identityKey, AccessTokenEnc: "stale-callback"}, flowStartedAt)
		if err != nil || saved {
			t.Fatalf("stale OAuth callback for %s after rename = %v, %v; want false, nil", identityKey, saved, err)
		}
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
