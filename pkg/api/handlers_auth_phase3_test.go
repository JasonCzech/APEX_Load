package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

// A6 — a service account's scopes/roles/metadata can be edited in place (key unchanged);
// invalid scopes are rejected.
func TestUpdateServiceAccountEditable(t *testing.T) {
	store := phase1Store(t)
	now := time.Now().UTC()
	acct := &storage.ServiceAccount{ID: "svc-edit", Name: "Editable", KeyHash: "keep-this-hash", Active: true, GlobalRoles: []string{"viewer"}, Scopes: []string{"results:read"}, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveServiceAccount(acct); err != nil {
		t.Fatalf("SaveServiceAccount: %v", err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"}) // dev bypass

	patch := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/auth/service-accounts/svc-edit", bytes.NewReader([]byte(body)))
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		return resp
	}

	resp := patch(`{"description":"updated","scopes":["tests:run","results:read"],"global_roles":["editor"]}`)
	if resp.Code != http.StatusOK {
		t.Fatalf("edit status = %d, want 200 body=%s", resp.Code, resp.Body.String())
	}
	reloaded, err := store.GetServiceAccount("svc-edit")
	if err != nil {
		t.Fatalf("GetServiceAccount: %v", err)
	}
	if reloaded.KeyHash != "keep-this-hash" {
		t.Fatalf("edit changed the key hash to %q", reloaded.KeyHash)
	}
	if reloaded.Description != "updated" || len(reloaded.GlobalRoles) != 1 || reloaded.GlobalRoles[0] != "editor" {
		t.Fatalf("edit did not apply metadata/roles: %#v", reloaded)
	}
	foundTestsRun := false
	for _, s := range reloaded.Scopes {
		if s == "tests:run" {
			foundTestsRun = true
		}
	}
	if !foundTestsRun {
		t.Fatalf("edit did not apply scopes: %#v", reloaded.Scopes)
	}

	if r := patch(`{"scopes":["bogus:scope"]}`); r.Code != http.StatusBadRequest {
		t.Fatalf("invalid scope edit status = %d, want 400", r.Code)
	}
	if r := patch(`{"scopes":[]}`); r.Code != http.StatusBadRequest {
		t.Fatalf("empty scope edit status = %d, want 400", r.Code)
	}
}

// A7 — a binding with an unknown role is rejected server-side.
func TestCreateUserBindingRejectsUnknownRole(t *testing.T) {
	store := phase1Store(t)
	now := time.Now().UTC()
	user := &storage.User{ID: uuid.New().String(), Subject: "binding-role-target", Active: true, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveUser(user); err != nil {
		t.Fatalf("SaveUser: %v", err)
	}
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"}) // dev bypass

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/users/"+user.ID+"/bindings", bytes.NewReader([]byte(`{"scope_type":"global","role":"superuser"}`)))
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("unknown-role binding status = %d, want 400 body=%s", resp.Code, resp.Body.String())
	}
}

// A7 — scoped bindings must target existing project/domain/repository scopes.
func TestCreateUserBindingRejectsUnknownScopes(t *testing.T) {
	store := phase1Store(t)
	now := time.Now().UTC()
	user := &storage.User{ID: uuid.New().String(), Subject: "binding-scope-target", Active: true, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveUser(user); err != nil {
		t.Fatalf("SaveUser: %v", err)
	}
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"}) // dev bypass

	cases := map[string]string{
		"missing project":    `{"scope_type":"project","scope_id":"missing-project","role":"viewer"}`,
		"blank project":      `{"scope_type":"project","role":"viewer"}`,
		"missing domain":     `{"scope_type":"domain","scope_id":"missing-domain","role":"viewer"}`,
		"missing repository": `{"scope_type":"application_repository","scope_id":"missing-repository","role":"viewer"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/users/"+user.ID+"/bindings", bytes.NewReader([]byte(body)))
			resp := httptest.NewRecorder()
			server.mux.ServeHTTP(resp, req)
			if resp.Code != http.StatusBadRequest {
				t.Fatalf("binding status = %d, want 400 body=%s", resp.Code, resp.Body.String())
			}
		})
	}
}

func TestOnboardUserRejectsUnknownInitialScopes(t *testing.T) {
	store := phase1Store(t)
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"}) // dev bypass

	cases := map[string]string{
		"missing project":    `{"subject":"missing-project-subject","bindings":[{"scope_type":"project","scope_id":"missing-project","role":"viewer"}]}`,
		"missing domain":     `{"subject":"missing-domain-subject","bindings":[{"scope_type":"domain","scope_id":"missing-domain","role":"viewer"}]}`,
		"missing repository": `{"subject":"missing-repository-subject","bindings":[{"scope_type":"application_repository","scope_id":"missing-repository","role":"viewer"}]}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/users/onboard", bytes.NewReader([]byte(body)))
			resp := httptest.NewRecorder()
			server.mux.ServeHTTP(resp, req)
			if resp.Code != http.StatusBadRequest {
				t.Fatalf("onboard status = %d, want 400 body=%s", resp.Code, resp.Body.String())
			}
			var payload struct {
				Subject string `json:"subject"`
			}
			if err := json.Unmarshal([]byte(body), &payload); err != nil {
				t.Fatalf("unmarshal test body: %v", err)
			}
			if _, err := store.GetUserBySubject(payload.Subject); err == nil {
				t.Fatalf("invalid onboarding persisted user %q", payload.Subject)
			}
		})
	}
}

// A8 — a user can be hard-deleted; the last system admin cannot.
func TestDeleteUserEndpoint(t *testing.T) {
	store := phase1Store(t)
	now := time.Now().UTC()
	user := &storage.User{ID: uuid.New().String(), Subject: "delete-me", Active: true, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveUser(user); err != nil {
		t.Fatalf("SaveUser: %v", err)
	}
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"}) // dev bypass

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/auth/users/"+user.ID, nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("delete status = %d, want 200 body=%s", resp.Code, resp.Body.String())
	}
	if _, err := store.GetUser(user.ID); err == nil {
		t.Fatalf("user should be deleted")
	}
}

func TestDeleteLastSystemAdminBlocked(t *testing.T) {
	store := phase1Store(t)
	admin := seedAdmin(t, store, "delete-admin")
	key := "phase3-delete-admin-key"
	sum := sha256.Sum256([]byte(key))
	now := time.Now().UTC()
	if err := store.SaveServiceAccount(&storage.ServiceAccount{ID: "svc-del", Name: "Del Automation", KeyHash: fmt.Sprintf("%x", sum[:]), Active: true, GlobalRoles: []string{RoleSystemAdmin}, Scopes: []string{"automation:*"}, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveServiceAccount: %v", err)
	}
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	del := func() int {
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/auth/users/"+admin.ID, nil)
		req.Header.Set("X-APEXLoad-API-Key", key)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		return resp.Code
	}
	if code := del(); code != http.StatusConflict {
		t.Fatalf("delete last admin status = %d, want 409", code)
	}
	seedAdmin(t, store, "delete-admin-backup")
	if code := del(); code != http.StatusOK {
		t.Fatalf("delete non-last admin status = %d, want 200", code)
	}
}

// A9 — the user list reports a real total and HasMore across pages.
func TestListUsersReportsTotal(t *testing.T) {
	store := phase1Store(t)
	now := time.Now().UTC()
	for i := 0; i < 3; i++ {
		u := &storage.User{ID: uuid.New().String(), Subject: fmt.Sprintf("user-%d", i), Active: true, CreatedAt: now, UpdatedAt: now}
		if err := store.SaveUser(u); err != nil {
			t.Fatalf("SaveUser: %v", err)
		}
	}
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"}) // dev bypass

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/users?limit=2&offset=0", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200 body=%s", resp.Code, resp.Body.String())
	}
	var payload AuthUserListResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload.Count != 2 || payload.Total != 3 || !payload.HasMore {
		t.Fatalf("pagination = count:%d total:%d hasMore:%v, want 2/3/true", payload.Count, payload.Total, payload.HasMore)
	}
}
