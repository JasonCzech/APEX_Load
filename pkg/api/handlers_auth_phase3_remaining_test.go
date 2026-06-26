package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// C12 — a service account carries owner/contact metadata through create and partial update.
func TestServiceAccountOwnerMetadataRoundtrip(t *testing.T) {
	store := phase1Store(t)
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"}) // dev bypass

	body := `{"name":"Owned Automation","owner_id":"team-platform","contact_email":"oncall@example.com","global_roles":["viewer"],"scopes":["results:read"]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/service-accounts", bytes.NewReader([]byte(body)))
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201 body=%s", resp.Code, resp.Body.String())
	}
	var created AuthServiceAccountCreateResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal create: %v", err)
	}
	if created.ServiceAccount == nil {
		t.Fatalf("create returned nil service account")
	}
	if created.ServiceAccount.OwnerID != "team-platform" || created.ServiceAccount.ContactEmail != "oncall@example.com" {
		t.Fatalf("create did not echo owner metadata: %#v", created.ServiceAccount)
	}
	id := created.ServiceAccount.ID

	reloaded, err := store.GetServiceAccount(id)
	if err != nil {
		t.Fatalf("GetServiceAccount: %v", err)
	}
	if reloaded.OwnerID != "team-platform" || reloaded.ContactEmail != "oncall@example.com" {
		t.Fatalf("owner metadata not persisted: %#v", reloaded)
	}

	// PATCH reassigns the owner and clears the contact.
	patch := httptest.NewRequest(http.MethodPatch, "/api/v1/auth/service-accounts/"+id, bytes.NewReader([]byte(`{"owner_id":"team-sre","contact_email":""}`)))
	presp := httptest.NewRecorder()
	server.mux.ServeHTTP(presp, patch)
	if presp.Code != http.StatusOK {
		t.Fatalf("patch status = %d, want 200 body=%s", presp.Code, presp.Body.String())
	}
	reloaded, err = store.GetServiceAccount(id)
	if err != nil {
		t.Fatalf("GetServiceAccount after patch: %v", err)
	}
	if reloaded.OwnerID != "team-sre" || reloaded.ContactEmail != "" {
		t.Fatalf("owner metadata not updated by patch: %#v", reloaded)
	}
}

func countAllowedOps(resp AuthEffectiveAccessResponse) int {
	allowed := 0
	for _, resource := range resp.Resources {
		for _, op := range resource.Operations {
			if op.Allowed {
				allowed++
			}
		}
	}
	return allowed
}

// C11 — the effective-access simulator answers for an arbitrary user principal, reflecting
// that principal's own roles (not the caller's).
func TestEffectiveAccessSimulatorUser(t *testing.T) {
	store := phase1Store(t)
	now := time.Now().UTC()
	admin := &storage.User{ID: "u-admin-target", Subject: "admin-target", Active: true, CreatedAt: now, UpdatedAt: now,
		Bindings: []*storage.UserRoleBinding{{ID: "b-admin", UserID: "u-admin-target", ScopeType: "global", Role: RoleSystemAdmin, CreatedAt: now, UpdatedAt: now}}}
	if err := store.SaveUser(admin); err != nil {
		t.Fatalf("SaveUser admin: %v", err)
	}
	if err := store.SaveUserRoleBinding(admin.Bindings[0]); err != nil {
		t.Fatalf("SaveUserRoleBinding: %v", err)
	}
	none := &storage.User{ID: "u-none", Subject: "no-roles", Active: true, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveUser(none); err != nil {
		t.Fatalf("SaveUser none: %v", err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"}) // dev bypass → caller is admin

	simulate := func(principalID string) AuthEffectiveAccessResponse {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/effective-access/principal?principal_type=user&principal_id="+principalID, nil)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Fatalf("simulate %s status = %d, want 200 body=%s", principalID, resp.Code, resp.Body.String())
		}
		var payload AuthEffectiveAccessResponse
		if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
			t.Fatalf("unmarshal simulate: %v", err)
		}
		return payload
	}

	adminView := simulate("u-admin-target")
	if !adminView.Simulated || adminView.PrincipalType != "user" || adminView.PrincipalID != "u-admin-target" {
		t.Fatalf("admin view missing simulator markers: %#v", adminView)
	}
	if countAllowedOps(adminView) == 0 {
		t.Fatalf("a global system_admin principal should have allowed operations")
	}

	noneView := simulate("u-none")
	if countAllowedOps(noneView) != 0 {
		t.Fatalf("a principal with no roles should have zero allowed operations, got %d", countAllowedOps(noneView))
	}
}

// C11 — the simulator resolves service-account principals and flags scope-narrowing.
func TestEffectiveAccessSimulatorServiceAccount(t *testing.T) {
	store := phase1Store(t)
	now := time.Now().UTC()
	if err := store.SaveServiceAccount(&storage.ServiceAccount{ID: "sa-sim", Name: "Sim SA", KeyHash: "sim-hash", Active: true, GlobalRoles: []string{RoleViewer}, Scopes: []string{"results:read"}, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveServiceAccount: %v", err)
	}
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/effective-access/principal?principal_type=service_account&principal_id=sa-sim", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("simulate SA status = %d, want 200 body=%s", resp.Code, resp.Body.String())
	}
	var payload AuthEffectiveAccessResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload.PrincipalType != "service_account" || payload.PrincipalName != "Sim SA" {
		t.Fatalf("SA principal markers wrong: %#v", payload)
	}
	if !payload.ScopesNarrowApiAccess {
		t.Fatalf("SA with scopes should set scopes_narrow_api_access")
	}
}

// C11 — bad input is rejected; an unknown principal is 404.
func TestEffectiveAccessSimulatorBadInput(t *testing.T) {
	store := phase1Store(t)
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	cases := []struct {
		name  string
		query string
		want  int
	}{
		{"missing principal_id", "?principal_type=user", http.StatusBadRequest},
		{"bad principal_type", "?principal_type=robot&principal_id=x", http.StatusBadRequest},
		{"unknown user", "?principal_type=user&principal_id=nope", http.StatusNotFound},
		{"unknown service account", "?principal_type=service_account&principal_id=nope", http.StatusNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/effective-access/principal"+c.query, nil)
			resp := httptest.NewRecorder()
			server.mux.ServeHTTP(resp, req)
			if resp.Code != c.want {
				t.Fatalf("status = %d, want %d body=%s", resp.Code, c.want, resp.Body.String())
			}
		})
	}
}

// C11 — a non-admin cannot use the simulator (it leaks others' access otherwise).
func TestEffectiveAccessSimulatorRequiresAdmin(t *testing.T) {
	store := phase1Store(t)
	now := time.Now().UTC()
	viewer := &storage.User{ID: "u-viewer", Subject: "viewer-user", Active: true, CreatedAt: now, UpdatedAt: now,
		Bindings: []*storage.UserRoleBinding{{ID: "b-viewer", UserID: "u-viewer", ScopeType: "global", Role: RoleViewer, CreatedAt: now, UpdatedAt: now}}}
	if err := store.SaveUser(viewer); err != nil {
		t.Fatalf("SaveUser: %v", err)
	}
	if err := store.SaveUserRoleBinding(viewer.Bindings[0]); err != nil {
		t.Fatalf("SaveUserRoleBinding: %v", err)
	}
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/effective-access/principal?principal_type=user&principal_id=u-viewer", nil)
	req.Header.Set("X-Auth-Request-User", "viewer-user")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("non-admin simulator status = %d, want 403 body=%s", resp.Code, resp.Body.String())
	}
}

// Notifications — computeAccessReview folds in a recent auth-denial count.
func TestComputeAccessReviewDenialCount(t *testing.T) {
	store := phase1Store(t)
	now := time.Now().UTC()
	for i := 0; i < 3; i++ {
		if err := store.SaveAuditEvent(&storage.AuditEvent{ID: "denial-" + string(rune('a'+i)), Action: auditActionAccessDenied, Status: "failure", ActorType: "anonymous", ActorSubject: "anon", ResourceType: "api_route", ResourceID: "GET /x", CreatedAt: now.Add(-time.Duration(i) * time.Minute)}); err != nil {
			t.Fatalf("SaveAuditEvent: %v", err)
		}
	}
	// An old denial outside the default 24h window must not be counted.
	if err := store.SaveAuditEvent(&storage.AuditEvent{ID: "denial-old", Action: auditActionAccessDenied, Status: "failure", ActorType: "anonymous", ActorSubject: "anon", ResourceType: "api_route", ResourceID: "GET /y", CreatedAt: now.AddDate(0, 0, -3)}); err != nil {
		t.Fatalf("SaveAuditEvent old: %v", err)
	}
	// A non-denial event must not be counted.
	if err := store.SaveAuditEvent(&storage.AuditEvent{ID: "ok-1", Action: "auth.session.login", Status: "success", ActorType: "user", ActorSubject: "a", CreatedAt: now, ResourceType: "session", ResourceID: "s"}); err != nil {
		t.Fatalf("SaveAuditEvent ok: %v", err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	review, err := server.computeAccessReview(now)
	if err != nil {
		t.Fatalf("computeAccessReview: %v", err)
	}
	if review.RecentDenials != 3 {
		t.Fatalf("recent denials = %d, want 3", review.RecentDenials)
	}
	if review.RecentDenialWindowHours != defaultAccessReviewDenialWindowHours {
		t.Fatalf("window hours = %d, want %d", review.RecentDenialWindowHours, defaultAccessReviewDenialWindowHours)
	}
}

// Notifications — the signature is stable for the same posture and changes when flags change.
func TestAccessReviewSignatureAndActionable(t *testing.T) {
	empty := AccessReviewResponse{}
	if accessReviewActionable(empty) {
		t.Fatalf("an empty review is not actionable")
	}
	a := AccessReviewResponse{
		ServiceAccounts: []AccessReviewServiceAccount{{ID: "sa1", Flags: []string{"overdue_rotation"}}},
		RecentDenials:   2,
	}
	if !accessReviewActionable(a) {
		t.Fatalf("a review with flagged accounts is actionable")
	}
	if accessReviewSignature(a) != accessReviewSignature(a) {
		t.Fatalf("signature must be deterministic")
	}
	b := a
	b.RecentDenials = 5
	if accessReviewSignature(a) == accessReviewSignature(b) {
		t.Fatalf("a changed denial count must change the signature")
	}
	denialsOnly := AccessReviewResponse{RecentDenials: 1}
	if !accessReviewActionable(denialsOnly) {
		t.Fatalf("recent denials alone are actionable")
	}
}

// Notifications — the notifier pings on a new actionable posture, stays quiet when unchanged,
// and resets to quiet when nothing is flagged.
func TestNotifyAccessReviewChange(t *testing.T) {
	store := phase1Store(t)
	now := time.Now().UTC()
	overdue := now.AddDate(0, 0, -1)
	if err := store.SaveServiceAccount(&storage.ServiceAccount{ID: "sa-overdue", Name: "overdue", Active: true, RotationDueAt: &overdue, Scopes: []string{"results:read"}, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveServiceAccount: %v", err)
	}
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	if server.hub == nil {
		t.Fatalf("server hub is nil; notifier cannot broadcast")
	}

	drainPing := func() bool {
		select {
		case msg := <-server.hub.broadcast:
			var env struct {
				Type         string `json:"type"`
				BroadcastAll bool   `json:"broadcast_all"`
			}
			if err := json.Unmarshal(msg, &env); err != nil {
				t.Fatalf("unmarshal ping: %v", err)
			}
			if env.Type != "access_review_changed" || !env.BroadcastAll {
				t.Fatalf("unexpected broadcast: %s", string(msg))
			}
			return true
		default:
			return false
		}
	}

	// First evaluation: actionable + changed → ping.
	sig := server.notifyAccessReviewChange(now, "")
	if sig == "" {
		t.Fatalf("expected a non-empty signature for an actionable posture")
	}
	if !drainPing() {
		t.Fatalf("expected a ping on the first actionable evaluation")
	}

	// Unchanged posture → no ping.
	sig2 := server.notifyAccessReviewChange(now, sig)
	if sig2 != sig {
		t.Fatalf("signature changed unexpectedly: %q -> %q", sig, sig2)
	}
	if drainPing() {
		t.Fatalf("did not expect a ping when the posture is unchanged")
	}
}

// C11 — the simulator narrows by scope: a project-scoped role grants operations only when
// the query names that project, and nowhere else.
func TestEffectiveAccessSimulatorScopedRoles(t *testing.T) {
	store := phase1Store(t)
	now := time.Now().UTC()
	user := &storage.User{ID: "u-proj", Subject: "proj-editor", Active: true, CreatedAt: now, UpdatedAt: now,
		Bindings: []*storage.UserRoleBinding{{ID: "b-proj", UserID: "u-proj", ScopeType: "project", ScopeID: "project-1", Role: RoleEditor, CreatedAt: now, UpdatedAt: now}}}
	if err := store.SaveUser(user); err != nil {
		t.Fatalf("SaveUser: %v", err)
	}
	if err := store.SaveUserRoleBinding(user.Bindings[0]); err != nil {
		t.Fatalf("SaveUserRoleBinding: %v", err)
	}
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})

	simulate := func(query string) AuthEffectiveAccessResponse {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/effective-access/principal?principal_type=user&principal_id=u-proj"+query, nil)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Fatalf("simulate %q status = %d, want 200 body=%s", query, resp.Code, resp.Body.String())
		}
		var payload AuthEffectiveAccessResponse
		if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return payload
	}

	if got := countAllowedOps(simulate("&project_id=project-1")); got == 0 {
		t.Fatalf("project-scoped editor should have allowed operations in its own project, got 0")
	}
	if got := countAllowedOps(simulate("&project_id=project-2")); got != 0 {
		t.Fatalf("editor in project-1 must have no access in project-2, got %d allowed", got)
	}
	if got := countAllowedOps(simulate("")); got != 0 {
		t.Fatalf("a project-scoped role must not grant global access, got %d allowed", got)
	}
}

// C12 — owner/contact metadata is length-bounded on both create and update.
func TestServiceAccountOwnerMetadataLengthCapped(t *testing.T) {
	store := phase1Store(t)
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	long := strings.Repeat("x", maxServiceAccountMetadataLen+1)

	// Create with an oversized owner_id → 400.
	createBody := `{"name":"Too Long","owner_id":"` + long + `","global_roles":["viewer"],"scopes":["results:read"]}`
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/service-accounts", bytes.NewReader([]byte(createBody)))
	createResp := httptest.NewRecorder()
	server.mux.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusBadRequest {
		t.Fatalf("oversized owner_id create = %d, want 400 body=%s", createResp.Code, createResp.Body.String())
	}

	// A valid account, then a PATCH with an oversized contact_email → 400.
	now := time.Now().UTC()
	if err := store.SaveServiceAccount(&storage.ServiceAccount{ID: "sa-meta", Name: "Meta", KeyHash: "meta-hash", Active: true, GlobalRoles: []string{RoleViewer}, Scopes: []string{"results:read"}, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveServiceAccount: %v", err)
	}
	patchReq := httptest.NewRequest(http.MethodPatch, "/api/v1/auth/service-accounts/sa-meta", bytes.NewReader([]byte(`{"contact_email":"`+long+`"}`)))
	patchResp := httptest.NewRecorder()
	server.mux.ServeHTTP(patchResp, patchReq)
	if patchResp.Code != http.StatusBadRequest {
		t.Fatalf("oversized contact_email patch = %d, want 400 body=%s", patchResp.Code, patchResp.Body.String())
	}
}
