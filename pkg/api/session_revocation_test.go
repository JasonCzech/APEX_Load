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
