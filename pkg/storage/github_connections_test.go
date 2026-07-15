package storage

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGitHubConnectionCRUD(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	// Absent connection returns (nil, nil).
	got, err := store.GetGitHubConnection("user-1")
	if err != nil || got != nil {
		t.Fatalf("expected (nil,nil) for absent connection, got (%v, %v)", got, err)
	}

	expires := time.Now().Add(8 * time.Hour).UTC().Truncate(time.Second)
	conn := &GitHubConnection{
		UserID:         "user-1",
		GitHubLogin:    "octocat",
		GitHubUserID:   "583231",
		AccessTokenEnc: "enc-access",
		TokenType:      "bearer",
		Scopes:         "repo",
		ExpiresAt:      &expires,
	}
	if err := store.SaveGitHubConnection(conn); err != nil {
		t.Fatalf("SaveGitHubConnection: %v", err)
	}
	if conn.ConnectedAt.IsZero() || conn.UpdatedAt.IsZero() {
		t.Fatal("Save should stamp connected_at/updated_at")
	}

	got, err = store.GetGitHubConnection("user-1")
	if err != nil {
		t.Fatalf("GetGitHubConnection: %v", err)
	}
	if got == nil || got.GitHubLogin != "octocat" || got.AccessTokenEnc != "enc-access" || got.Scopes != "repo" {
		t.Fatalf("unexpected connection: %#v", got)
	}
	if got.ExpiresAt == nil || !got.ExpiresAt.Equal(expires) {
		t.Fatalf("expires_at round-trip failed: %v want %v", got.ExpiresAt, expires)
	}

	// Upsert updates in place.
	conn.GitHubLogin = "octocat-2"
	conn.AccessTokenEnc = "enc-access-2"
	if err := store.SaveGitHubConnection(conn); err != nil {
		t.Fatalf("re-SaveGitHubConnection: %v", err)
	}
	got, _ = store.GetGitHubConnection("user-1")
	if got.GitHubLogin != "octocat-2" || got.AccessTokenEnc != "enc-access-2" {
		t.Fatalf("upsert did not update: %#v", got)
	}

	if err := store.DeleteGitHubConnection("user-1"); err != nil {
		t.Fatalf("DeleteGitHubConnection: %v", err)
	}
	got, _ = store.GetGitHubConnection("user-1")
	if got != nil {
		t.Fatalf("expected nil after delete, got %#v", got)
	}
}

func TestGitHubConnectionRefreshCannotResurrectDisconnect(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	conn := &GitHubConnection{UserID: "disconnect-race", AccessTokenEnc: "old"}
	if err := store.SaveGitHubConnection(conn); err != nil {
		t.Fatal(err)
	}
	expected := conn.UpdatedAt
	if err := store.DeleteGitHubConnection(conn.UserID); err != nil {
		t.Fatal(err)
	}
	conn.AccessTokenEnc = "new"
	updated, err := store.UpdateGitHubConnectionIfUnchanged(conn, expected)
	if err != nil {
		t.Fatal(err)
	}
	if updated {
		t.Fatal("token refresh recreated a disconnected GitHub connection")
	}
	if got, err := store.GetGitHubConnection(conn.UserID); err != nil || got != nil {
		t.Fatalf("connection after stale refresh = %#v, %v", got, err)
	}
}

func TestGitHubOAuthCompletionCannotResurrectNewerRevocation(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	flowStartedAt := time.Now().UTC().Add(-time.Minute)
	if err := store.RevokeGitHubConnection("revoked-user", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	conn := &GitHubConnection{UserID: "revoked-user", AccessTokenEnc: "ciphertext"}
	saved, err := store.SaveGitHubConnectionIfNotRevokedSince(conn, flowStartedAt)
	if err != nil {
		t.Fatal(err)
	}
	if saved {
		t.Fatal("an OAuth flow older than disconnect recreated the connection")
	}
	if got, err := store.GetGitHubConnection(conn.UserID); err != nil || got != nil {
		t.Fatalf("revoked connection was recreated: %#v, %v", got, err)
	}

	newFlowStartedAt := time.Now().UTC().Add(time.Minute)
	saved, err = store.SaveGitHubConnectionIfNotRevokedSince(conn, newFlowStartedAt)
	if err != nil || !saved {
		t.Fatalf("new OAuth flow after revocation = %v, %v; want true, nil", saved, err)
	}
}

func TestGitHubConnectionJSONNeverExposesEncryptedTokens(t *testing.T) {
	payload, err := json.Marshal(GitHubConnection{UserID: "user", AccessTokenEnc: "access-ciphertext", RefreshTokenEnc: "refresh-ciphertext"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "ciphertext") || strings.Contains(string(payload), "AccessToken") || strings.Contains(string(payload), "RefreshToken") {
		t.Fatalf("GitHub connection JSON exposed encrypted credentials: %s", payload)
	}
}

func TestUserIdentityClaimAndRenameCannotInheritGitHubConnection(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now().UTC()
	// Simulate a credential left by a legacy/offline deletion. Claiming that
	// subject for a new local principal must remove it before the user exists.
	if err := store.SaveGitHubConnection(&GitHubConnection{UserID: "old-subject", GitHubLogin: "prior-user", AccessTokenEnc: "prior-token"}); err != nil {
		t.Fatal(err)
	}
	user := &User{ID: "renamed-user", Subject: "old-subject", Active: true, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveUser(user); err != nil {
		t.Fatal(err)
	}
	if connection, err := store.GetGitHubConnection(user.Subject); err != nil || connection != nil {
		t.Fatalf("new user inherited stale connection: %#v, %v", connection, err)
	}

	for _, identityKey := range []string{user.Subject, "new-subject", user.ID} {
		if err := store.SaveGitHubConnection(&GitHubConnection{UserID: identityKey, GitHubLogin: "prior-user", AccessTokenEnc: "prior-token"}); err != nil {
			t.Fatal(err)
		}
	}
	expected := *user
	updated := *user
	updated.Subject = "new-subject"
	updated.UpdatedAt = now.Add(time.Minute)
	changed, err := store.UpdateUserGuardLastAdminIfUnchanged(&updated, &expected)
	if err != nil || !changed {
		t.Fatalf("rename = %v, %v; want true, nil", changed, err)
	}
	for _, identityKey := range []string{user.Subject, updated.Subject, user.ID} {
		if connection, err := store.GetGitHubConnection(identityKey); err != nil || connection != nil {
			t.Fatalf("renamed identity connection %s = %#v, %v; want revoked", identityKey, connection, err)
		}
		saved, err := store.SaveGitHubConnectionIfNotRevokedSince(&GitHubConnection{UserID: identityKey, AccessTokenEnc: "stale-callback"}, now)
		if err != nil || saved {
			t.Fatalf("stale callback for %s = %v, %v; want false, nil", identityKey, saved, err)
		}
	}

	// Reusing the retired subject for another local principal must not revive the
	// first user's credential.
	replacement := &User{ID: "replacement-user", Subject: user.Subject, Active: true, CreatedAt: now.Add(2 * time.Minute), UpdatedAt: now.Add(2 * time.Minute)}
	if err := store.SaveUser(replacement); err != nil {
		t.Fatal(err)
	}
	if connection, err := store.GetGitHubConnection(replacement.Subject); err != nil || connection != nil {
		t.Fatalf("replacement user inherited connection: %#v, %v", connection, err)
	}
}

func TestBootstrapFirstAdminCannotInheritGitHubConnection(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "github-bootstrap-identity.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now().UTC()
	flowStartedAt := now.Add(-time.Minute)
	user := &User{ID: "bootstrap-user", Subject: "bootstrap-subject", Active: true, CreatedAt: now, UpdatedAt: now}
	for _, identityKey := range []string{user.ID, user.Subject} {
		if err := store.SaveGitHubConnection(&GitHubConnection{UserID: identityKey, GitHubLogin: "retired-user", AccessTokenEnc: "retired-token"}); err != nil {
			t.Fatal(err)
		}
	}
	binding := &UserRoleBinding{ID: "bootstrap-binding", UserID: user.ID, ScopeType: "global", Role: "system_admin", CreatedAt: now, UpdatedAt: now}
	event := &AuditEvent{ID: "bootstrap-event", Action: "auth.bootstrap.admin_granted", ActorType: "system", ActorSubject: user.Subject, ResourceType: "user", ResourceID: user.ID, Status: "success", CreatedAt: now}

	granted, err := store.BootstrapFirstAdmin(user, binding, event)
	if err != nil || !granted {
		t.Fatalf("BootstrapFirstAdmin granted=%v err=%v, want true, nil", granted, err)
	}
	for _, identityKey := range []string{user.ID, user.Subject} {
		if connection, err := store.GetGitHubConnection(identityKey); err != nil || connection != nil {
			t.Fatalf("bootstrap identity connection %s = %#v, %v; want revoked", identityKey, connection, err)
		}
		saved, err := store.SaveGitHubConnectionIfNotRevokedSince(&GitHubConnection{UserID: identityKey, AccessTokenEnc: "stale-callback"}, flowStartedAt)
		if err != nil || saved {
			t.Fatalf("stale callback for bootstrap identity %s = %v, %v; want false, nil", identityKey, saved, err)
		}
	}
}
