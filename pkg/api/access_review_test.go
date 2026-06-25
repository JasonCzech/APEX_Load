package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

// C4-MVP — the access-review report flags stale/unbound/disabled/admin users and
// overdue/expired/broad-scope/never-used service accounts, and omits healthy ones.
func TestAccessReviewReport(t *testing.T) {
	now := time.Now().UTC()
	stale := now.AddDate(0, 0, -200)
	recent := now.AddDate(0, 0, -1)

	users := []*storage.User{
		{ID: "u-stale", Subject: "stale", Active: true, LastSeenAt: &stale, Bindings: []*storage.UserRoleBinding{{ScopeType: "global", Role: RoleViewer}}},
		{ID: "u-nobind", Subject: "nobind", Active: true, LastSeenAt: &recent},
		{ID: "u-admin", Subject: "admin", Active: true, LastSeenAt: &recent, Bindings: []*storage.UserRoleBinding{{ScopeType: "global", Role: RoleSystemAdmin}}},
		{ID: "u-healthy", Subject: "healthy", Active: true, LastSeenAt: &recent, Bindings: []*storage.UserRoleBinding{{ScopeType: "project", ScopeID: "p", Role: RoleEditor}}},
	}
	overdue := now.AddDate(0, 0, -1)
	expired := now.AddDate(0, 0, -1)
	accounts := []*storage.ServiceAccount{
		{ID: "sa-overdue", Name: "overdue", Active: true, LastUsedAt: &recent, RotationDueAt: &overdue, Scopes: []string{"results:read"}},
		{ID: "sa-expired", Name: "expired", Active: true, LastUsedAt: &recent, ExpiresAt: &expired, Scopes: []string{"results:read"}},
		{ID: "sa-broad", Name: "broad", Active: true, LastUsedAt: &recent, Scopes: []string{"automation:*"}},
		{ID: "sa-healthy", Name: "healthy", Active: true, LastUsedAt: &recent, Scopes: []string{"results:read"}},
	}

	report := buildAccessReview(users, accounts, now)

	userFlags := map[string][]string{}
	for _, u := range report.Users {
		userFlags[u.ID] = u.Flags
	}
	if _, ok := userFlags["u-healthy"]; ok {
		t.Fatalf("healthy user should not appear in the review")
	}
	if !hasFlag(userFlags["u-stale"], "stale") {
		t.Fatalf("u-stale missing 'stale' flag: %v", userFlags["u-stale"])
	}
	if !hasFlag(userFlags["u-nobind"], "no_bindings") {
		t.Fatalf("u-nobind missing 'no_bindings' flag: %v", userFlags["u-nobind"])
	}
	if !hasFlag(userFlags["u-admin"], "system_admin") {
		t.Fatalf("u-admin missing 'system_admin' flag: %v", userFlags["u-admin"])
	}

	saFlags := map[string][]string{}
	for _, a := range report.ServiceAccounts {
		saFlags[a.ID] = a.Flags
	}
	if _, ok := saFlags["sa-healthy"]; ok {
		t.Fatalf("healthy service account should not appear in the review")
	}
	if !hasFlag(saFlags["sa-overdue"], "overdue_rotation") {
		t.Fatalf("sa-overdue missing 'overdue_rotation': %v", saFlags["sa-overdue"])
	}
	if !hasFlag(saFlags["sa-expired"], "expired") {
		t.Fatalf("sa-expired missing 'expired': %v", saFlags["sa-expired"])
	}
	if !hasFlag(saFlags["sa-broad"], "broad_scope") {
		t.Fatalf("sa-broad missing 'broad_scope': %v", saFlags["sa-broad"])
	}
	if report.Summary["stale"] < 1 || report.Summary["broad_scope"] < 1 {
		t.Fatalf("summary missing counts: %#v", report.Summary)
	}
}

// C4-MVP — the endpoint is system-admin gated and returns the report.
func TestAccessReviewEndpointGated(t *testing.T) {
	store := auditEventStore(t)
	now := time.Now().UTC()
	if err := store.SaveUser(&storage.User{ID: uuid.New().String(), Subject: "known", Active: true, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveUser: %v", err)
	}
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	// Non-admin (viewer) → 403.
	viewer := httptest.NewRequest(http.MethodGet, "/api/v1/auth/access-review", nil)
	viewer.Header.Set("X-Auth-Request-User", "known")
	viewerResp := httptest.NewRecorder()
	server.mux.ServeHTTP(viewerResp, viewer)
	if viewerResp.Code != http.StatusForbidden {
		t.Fatalf("viewer access-review status = %d, want 403 body=%s", viewerResp.Code, viewerResp.Body.String())
	}

	// System admin (bootstrap via header on empty-ish store — 'known' has no admin
	// binding, so use a global-roles header subject that bootstraps as admin only when
	// store empty; here store has a user, so seed an admin instead).
	admin := seedAdmin(t, store, "review-admin")
	adminReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/access-review", nil)
	adminReq.Header.Set("X-Auth-Request-User", admin.Subject)
	adminResp := httptest.NewRecorder()
	server.mux.ServeHTTP(adminResp, adminReq)
	if adminResp.Code != http.StatusOK {
		t.Fatalf("admin access-review status = %d, want 200 body=%s", adminResp.Code, adminResp.Body.String())
	}
	var payload AccessReviewResponse
	if err := json.Unmarshal(adminResp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal access-review: %v", err)
	}
	if payload.StaleAfterDays <= 0 {
		t.Fatalf("expected a positive stale-after-days, got %d", payload.StaleAfterDays)
	}
}

func hasFlag(flags []string, target string) bool {
	for _, f := range flags {
		if f == target {
			return true
		}
	}
	return false
}
