package storage

import (
	"path/filepath"
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
