package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// disappearingRoleScopeStore models the narrow race in which request-level
// validation observes a scope, but the durable transaction finds that a
// concurrent deletion committed first. The embedded store supplies all reads;
// only the final fenced writes are intercepted.
type disappearingRoleScopeStore struct {
	storage.Store
}

func (s *disappearingRoleScopeStore) SaveUserRoleBinding(*storage.UserRoleBinding) error {
	return storage.ErrRoleScopeNotFound
}

func (s *disappearingRoleScopeStore) SaveUserWithRoleBindings(*storage.User, []*storage.UserRoleBinding) error {
	return storage.ErrRoleScopeNotFound
}

func TestServiceAccountHandlersRejectMissingRoleScopes(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})

	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/service-accounts", strings.NewReader(`{
		"name":"invalid scoped account",
		"project_roles":{"missing-project":["viewer"]},
		"scopes":["read"]
	}`))
	createResp := httptest.NewRecorder()
	server.handleCreateServiceAccount(createResp, createReq)
	if createResp.Code != http.StatusBadRequest {
		t.Fatalf("create status = %d, want %d body=%s", createResp.Code, http.StatusBadRequest, createResp.Body.String())
	}
	accounts, err := store.ListServiceAccounts()
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 0 {
		t.Fatalf("invalid service account persisted: %#v", accounts)
	}

	now := time.Now().UTC()
	account := &storage.ServiceAccount{
		ID: "valid-global-account", Name: "valid global account", KeyHash: "valid-global-hash", Active: true,
		GlobalRoles: []string{RoleViewer}, Scopes: []string{"read"}, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.SaveServiceAccount(account); err != nil {
		t.Fatal(err)
	}
	updateReq := httptest.NewRequest(http.MethodPatch, "/api/v1/auth/service-accounts/"+account.ID, strings.NewReader(`{
		"project_roles":{"missing-project":["viewer"]}
	}`))
	updateReq.SetPathValue("id", account.ID)
	updateResp := httptest.NewRecorder()
	server.handleUpdateServiceAccount(updateResp, updateReq)
	if updateResp.Code != http.StatusBadRequest {
		t.Fatalf("update status = %d, want %d body=%s", updateResp.Code, http.StatusBadRequest, updateResp.Body.String())
	}
	stored, err := store.GetServiceAccount(account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.ProjectRoles) != 0 || stored.Name != account.Name {
		t.Fatalf("invalid scoped update mutated account: %#v", stored)
	}
}

func TestUserBindingHandlersReportConcurrentRoleScopeDeletionAsConflict(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now().UTC()
	project := &storage.Project{ID: "role-scope-race-project", Name: "Race", CreatedAt: now, UpdatedAt: now}
	user := &storage.User{ID: "role-scope-race-user", Subject: "role-scope-race-user", Active: true, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveUser(user); err != nil {
		t.Fatal(err)
	}

	server := NewServer(ServerConfig{Store: &disappearingRoleScopeStore{Store: store}})
	bindingReq := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/users/"+user.ID+"/bindings",
		strings.NewReader(`{"scope_type":"project","scope_id":"`+project.ID+`","role":"viewer"}`),
	)
	bindingResp := httptest.NewRecorder()
	server.mux.ServeHTTP(bindingResp, bindingReq)
	if bindingResp.Code != http.StatusConflict {
		t.Fatalf("binding status = %d, want %d body=%s", bindingResp.Code, http.StatusConflict, bindingResp.Body.String())
	}

	onboardReq := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/users/onboard",
		strings.NewReader(`{"subject":"new-race-user","bindings":[{"scope_type":"project","scope_id":"`+project.ID+`","role":"viewer"}]}`),
	)
	onboardResp := httptest.NewRecorder()
	server.mux.ServeHTTP(onboardResp, onboardReq)
	if onboardResp.Code != http.StatusConflict {
		t.Fatalf("onboard status = %d, want %d body=%s", onboardResp.Code, http.StatusConflict, onboardResp.Body.String())
	}
	if _, err := store.GetUserBySubject("new-race-user"); err == nil {
		t.Fatal("failed onboarding persisted a user")
	}
}
