package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

// C3-MVP — an OIDC session whose epoch is older than the user's current epoch is
// rejected; a current-epoch session is accepted; trusted-header identities (not
// session-scoped) are unaffected by epoch bumps.
func TestSessionEpochRevocation(t *testing.T) {
	store := auditEventStore(t)
	user := seedAdmin(t, store, "epoch-user")

	newEpoch, err := store.BumpUserSessionEpoch(user.ID, time.Now().UTC())
	if err != nil {
		t.Fatalf("BumpUserSessionEpoch: %v", err)
	}

	emptyRoles := func() *AuthenticatedUser {
		return &AuthenticatedUser{
			ID:              user.ID,
			Subject:         user.Subject,
			GlobalRoles:     map[string]struct{}{},
			ProjectRoles:    map[string]map[string]struct{}{},
			DomainRoles:     map[string]map[string]struct{}{},
			RepositoryRoles: map[string]map[string]struct{}{},
		}
	}

	// Stale OIDC session (epoch 0 < current 1) → revoked.
	stale := emptyRoles()
	stale.SessionScoped = true
	stale.SessionEpoch = 0
	if _, err := applyLocalUserBindings(stale, store); err == nil || !strings.Contains(err.Error(), "revoked") {
		t.Fatalf("stale session should be revoked, got err=%v", err)
	}

	// Current OIDC session (epoch == current) → accepted.
	current := emptyRoles()
	current.SessionScoped = true
	current.SessionEpoch = newEpoch
	if _, err := applyLocalUserBindings(current, store); err != nil {
		t.Fatalf("current session should resolve, got err=%v", err)
	}

	// Trusted-header identity (not session-scoped) → unaffected by the bump.
	header := emptyRoles()
	header.SessionScoped = false
	header.SessionEpoch = 0
	if _, err := applyLocalUserBindings(header, store); err != nil {
		t.Fatalf("trusted-header identity should be unaffected by epoch, got err=%v", err)
	}
}

func TestApplyLocalUserBindingsThrottlesLastSeenWrites(t *testing.T) {
	resetUserIdentitySeenThrottleForTests()
	t.Cleanup(resetUserIdentitySeenThrottleForTests)
	store := auditEventStore(t)
	user := seedAdmin(t, store, "seen-user")

	upstream := bootstrapUpstream(user.Subject, RoleViewer)
	if _, err := applyLocalUserBindings(upstream, store); err != nil {
		t.Fatalf("first applyLocalUserBindings returned error: %v", err)
	}
	firstStored, err := store.GetUser(user.ID)
	if err != nil {
		t.Fatalf("GetUser after first apply returned error: %v", err)
	}
	if firstStored.LastSeenAt == nil {
		t.Fatal("first apply should persist last_seen_at")
	}

	if _, err := applyLocalUserBindings(upstream, store); err != nil {
		t.Fatalf("second applyLocalUserBindings returned error: %v", err)
	}
	secondStored, err := store.GetUser(user.ID)
	if err != nil {
		t.Fatalf("GetUser after second apply returned error: %v", err)
	}
	if secondStored.LastSeenAt == nil || !secondStored.LastSeenAt.Equal(*firstStored.LastSeenAt) {
		t.Fatalf("second apply should be throttled; last_seen_at = %v, want %v", secondStored.LastSeenAt, firstStored.LastSeenAt)
	}

	renamed := bootstrapUpstream(user.Subject, RoleViewer)
	renamed.DisplayName = "Renamed User"
	if _, err := applyLocalUserBindings(renamed, store); err != nil {
		t.Fatalf("renamed applyLocalUserBindings returned error: %v", err)
	}
	renamedStored, err := store.GetUser(user.ID)
	if err != nil {
		t.Fatalf("GetUser after rename returned error: %v", err)
	}
	if renamedStored.DisplayName != "Renamed User" {
		t.Fatalf("DisplayName = %q, want profile update despite throttle", renamedStored.DisplayName)
	}
}

func TestApplyLocalUserBindingsCannotResurrectRevokedEpoch(t *testing.T) {
	store := auditEventStore(t)
	user := seedAdmin(t, store, "epoch-race-user")

	upstream := &AuthenticatedUser{
		ID:              user.ID,
		Subject:         user.Subject,
		DisplayName:     "Updated Name",
		Email:           "updated@example.test",
		SessionScoped:   true,
		SessionEpoch:    0,
		GlobalRoles:     map[string]struct{}{},
		ProjectRoles:    map[string]map[string]struct{}{},
		DomainRoles:     map[string]map[string]struct{}{},
		RepositoryRoles: map[string]map[string]struct{}{},
	}
	if _, err := applyLocalUserBindings(upstream, store); err != nil {
		t.Fatalf("initial current-epoch request should resolve: %v", err)
	}

	if _, err := store.BumpUserSessionEpoch(user.ID, time.Now().UTC()); err != nil {
		t.Fatalf("BumpUserSessionEpoch: %v", err)
	}
	reloaded, err := store.GetUser(user.ID)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if reloaded.SessionEpoch != 1 {
		t.Fatalf("session epoch after revoke = %d, want 1", reloaded.SessionEpoch)
	}
	if reloaded.DisplayName != "Updated Name" || reloaded.Email != "updated@example.test" {
		t.Fatalf("identity refresh did not persist profile fields: %#v", reloaded)
	}

	if _, err := applyLocalUserBindings(upstream, store); err == nil || !strings.Contains(err.Error(), "revoked") {
		t.Fatalf("stale session should stay revoked after identity refresh, got err=%v", err)
	}
	final, err := store.GetUser(user.ID)
	if err != nil {
		t.Fatalf("GetUser final: %v", err)
	}
	if final.SessionEpoch != 1 {
		t.Fatalf("stale auth path rewrote session epoch to %d, want 1", final.SessionEpoch)
	}
}

// C3-MVP — the revoke-sessions endpoint bumps the user's epoch and audits it.
func TestRevokeUserSessionsEndpoint(t *testing.T) {
	store := auditEventStore(t)
	now := time.Now().UTC()
	user := &storage.User{ID: uuid.New().String(), Subject: "revoke-target", Active: true, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveUser(user); err != nil {
		t.Fatalf("SaveUser: %v", err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"}) // dev bypass

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/users/"+user.ID+"/revoke-sessions", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("revoke-sessions status = %d, want 200 body=%s", resp.Code, resp.Body.String())
	}
	if reloaded, _ := store.GetUser(user.ID); reloaded.SessionEpoch != 1 {
		t.Fatalf("session epoch after revoke = %d, want 1", reloaded.SessionEpoch)
	}
	events, err := store.ListAuditEvents(storage.AuditEventFilter{Action: "auth.user.sessions.revoke", Limit: 10})
	if err != nil || len(events) != 1 {
		t.Fatalf("revoke audit events = %d (err=%v), want 1", len(events), err)
	}
}
