package storage

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLocalAuthFixtureStartupRejectsNonBcryptStoredCredential(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "local-auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	fixture := localAuthFixtureForTest(time.Now().UTC().Truncate(time.Second))
	if err := store.EnsureLocalAuthFixtures([]LocalAuthFixture{fixture}); err != nil {
		t.Fatalf("seed fixture: %v", err)
	}
	if _, err := store.db.Exec(`UPDATE local_auth_credentials SET password_hash = ? WHERE username = ?`, "plaintext", normalizeLocalUsername(fixture.Credential.Username)); err != nil {
		t.Fatalf("corrupt stored credential: %v", err)
	}

	err = store.EnsureLocalAuthFixtures([]LocalAuthFixture{fixture})
	if err == nil || !strings.Contains(err.Error(), "stored password hash must be bcrypt") {
		t.Fatalf("startup credential validation error = %v", err)
	}
	credential, lookupErr := store.GetLocalAuthCredential(fixture.Credential.Username)
	if lookupErr != nil {
		t.Fatal(lookupErr)
	}
	if credential.PasswordHash != "plaintext" {
		t.Fatalf("startup silently overwrote the stored credential: %q", credential.PasswordHash)
	}
}
