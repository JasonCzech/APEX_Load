package storage

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestProtectedSourceSecretSessionClaimLeaseIsAtomicAndTokenFenced(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC().Truncate(time.Microsecond)
	const projectID = "source-claim-project"
	if err := store.SaveProject(&Project{ID: projectID, Name: "Source claim", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateProtectedSourceSecretSession(&ProtectedSourceSecretSessionCipher{
		ID: "claim-session", ProjectID: projectID, Actor: "admin", Ciphertext: "ciphertext",
		WrappedKeyJSON: `{}`, ExpiresAt: now.Add(time.Hour), CreatedAt: now,
	})
	if err != nil || !created {
		t.Fatalf("create = %t, %v", created, err)
	}
	if _, err := store.ClaimProtectedSourceSecretSession("claim-session", projectID, "admin", "claim-a", now, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimProtectedSourceSecretSession("claim-session", projectID, "admin", "claim-b", now, now.Add(time.Minute)); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("concurrent claim error = %v, want no rows", err)
	}
	if released, err := store.ReleaseProtectedSourceSecretSessionClaim("claim-session", projectID, "admin", "wrong-claim"); err != nil || released {
		t.Fatalf("wrong-token release = %t, %v", released, err)
	}
	if released, err := store.ReleaseProtectedSourceSecretSessionClaim("claim-session", projectID, "admin", "claim-a"); err != nil || !released {
		t.Fatalf("owner release = %t, %v", released, err)
	}
	if _, err := store.ClaimProtectedSourceSecretSession("claim-session", projectID, "admin", "claim-b", now, now.Add(time.Minute)); err != nil {
		t.Fatalf("claim after release: %v", err)
	}
	if finalized, err := store.FinalizeProtectedSourceSecretSession("claim-session", projectID, "admin", "claim-b"); err != nil || !finalized {
		t.Fatalf("finalize = %t, %v", finalized, err)
	}
	if _, err := store.ClaimProtectedSourceSecretSession("claim-session", projectID, "admin", "claim-c", now, now.Add(time.Minute)); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("finalized claim error = %v, want no rows", err)
	}
}

func TestProtectedSourceSecretSessionExpiredClaimCanBeTakenOver(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC().Truncate(time.Microsecond)
	const projectID = "source-claim-takeover"
	if err := store.SaveProject(&Project{ID: projectID, Name: "Source takeover", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateProtectedSourceSecretSession(&ProtectedSourceSecretSessionCipher{
		ID: "takeover-session", ProjectID: projectID, Actor: "admin", Ciphertext: "ciphertext",
		WrappedKeyJSON: `{}`, ExpiresAt: now.Add(time.Hour), CreatedAt: now,
	})
	if err != nil || !created {
		t.Fatalf("create = %t, %v", created, err)
	}
	if _, err := store.ClaimProtectedSourceSecretSession("takeover-session", projectID, "admin", "stale", now, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	later := now.Add(2 * time.Second)
	if _, err := store.ClaimProtectedSourceSecretSession("takeover-session", projectID, "admin", "replacement", later, later.Add(time.Minute)); err != nil {
		t.Fatalf("take over expired claim: %v", err)
	}
	if finalized, err := store.FinalizeProtectedSourceSecretSession("takeover-session", projectID, "admin", "stale"); err != nil || finalized {
		t.Fatalf("stale finalize = %t, %v", finalized, err)
	}
	if finalized, err := store.FinalizeProtectedSourceSecretSession("takeover-session", projectID, "admin", "replacement"); err != nil || !finalized {
		t.Fatalf("replacement finalize = %t, %v", finalized, err)
	}
}
