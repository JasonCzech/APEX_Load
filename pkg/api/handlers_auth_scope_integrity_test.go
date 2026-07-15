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
