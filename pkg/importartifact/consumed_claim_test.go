package importartifact

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

var nonExpiringTestSession = time.Date(9999, time.December, 31, 23, 59, 59, 0, time.UTC)

func saveAndClaimNonExpiringTestSession(t *testing.T, store *EncryptedStore, id string) SessionDescriptor {
	t.Helper()
	if err := store.SaveSession(context.Background(), SessionDescriptor{
		ID: id, ProjectID: "project-a", Actor: "actor-a", ExpiresAt: nonExpiringTestSession, ExpirationDisabled: true,
	}, strings.NewReader(`{"secret":"committed"}`), 1024); err != nil {
		t.Fatal(err)
	}
	payload, claimed, err := store.ClaimSession(context.Background(), id, "project-a", "actor-a", 1024)
	if err != nil {
		t.Fatal(err)
	}
	clear(payload)
	return claimed
}

func ageTestSessionClaim(t *testing.T, store *EncryptedStore, id string) {
	t.Helper()
	stale := time.Now().UTC().Add(-sessionClaimLease - time.Minute)
	if err := os.Chtimes(filepath.Join(store.root, ".sessions", id+".claim.json"), stale, stale); err != nil {
		t.Fatal(err)
	}
}

// A save that committed its assets but could not finish removing the claimed
// session must not let stale-claim recovery turn that session back into a
// claimable draft: a second consumer would commit the same draft again.
func TestEncryptedStoreCommittedClaimIsNotRecoveredAfterCompletionFails(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("relies on POSIX directory permissions to make payload deletion fail")
	}
	store := newEncryptedArtifactTestStore(t)
	ctx := context.Background()
	claimed := saveAndClaimNonExpiringTestSession(t, store, "committed")
	payloadPath, err := store.pathForBackendKey(claimed.BackendKey)
	if err != nil {
		t.Fatal(err)
	}
	payloadDir := filepath.Dir(payloadPath)
	if err := os.Chmod(payloadDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(payloadDir, 0o700) })
	if err := store.CompleteSessionClaim(ctx, claimed.ID, "project-a", "actor-a", claimed.ClaimToken); err == nil {
		t.Fatal("completion removed a payload in a read-only directory")
	}
	if err := os.Chmod(payloadDir, 0o700); err != nil {
		t.Fatal(err)
	}
	ageTestSessionClaim(t, store, claimed.ID)

	if _, _, err := store.ClaimSession(ctx, claimed.ID, "project-a", "actor-a", 1024); err == nil {
		t.Fatal("a committed session was recovered and claimed again")
	}
	if _, _, err := store.OpenSession(ctx, claimed.ID, "project-a", "actor-a"); err == nil {
		t.Fatal("a committed session was recovered and opened again")
	}
	expired, err := store.DeleteExpiredSessions(ctx, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(expired) != 0 {
		t.Fatalf("committed session reported as expired (its related objects belong to the commit): %#v", expired)
	}
	if _, err := os.Stat(filepath.Join(store.root, ".sessions", claimed.ID+".claim.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("janitor left the committed claim behind: %v", err)
	}
	if _, err := os.Stat(payloadPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("janitor left the committed payload behind: %v", err)
	}
	if _, _, err := store.ClaimSession(ctx, claimed.ID, "project-a", "actor-a", 1024); err == nil {
		t.Fatal("cleaned-up committed session became claimable")
	}
}

func TestEncryptedStoreMarkedConsumedClaimIsNeverRestored(t *testing.T) {
	store := newEncryptedArtifactTestStore(t)
	ctx := context.Background()
	claimed := saveAndClaimNonExpiringTestSession(t, store, "retained")
	if err := store.MarkSessionClaimConsumed(claimed.ID, "project-a", "actor-a", "wrong-token"); err == nil {
		t.Fatal("a stale claim token marked the session consumed")
	}
	if err := store.MarkSessionClaimConsumed(claimed.ID, "project-a", "actor-a", claimed.ClaimToken); err != nil {
		t.Fatal(err)
	}

	// Even a holder's own release cannot hand the committed draft back.
	if err := store.ReleaseSessionClaim(claimed.ID, "project-a", "actor-a", claimed.ClaimToken); !errors.Is(err, ErrSessionConsumed) || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("release of a consumed claim = %v, want ErrSessionConsumed", err)
	}
	// It reads as gone at once, not as a claim still in flight.
	if _, _, err := store.ClaimSession(ctx, claimed.ID, "project-a", "actor-a", 1024); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("claim of a consumed session = %v, want not-exist", err)
	}
	// The janitor leaves a fresh tombstone to its holder.
	if _, err := store.DeleteExpiredSessions(ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	claimPath := filepath.Join(store.root, ".sessions", claimed.ID+".claim.json")
	if _, err := os.Stat(claimPath); err != nil {
		t.Fatalf("janitor removed a consumed claim inside its lease: %v", err)
	}
	ageTestSessionClaim(t, store, claimed.ID)
	if _, _, err := store.OpenSession(ctx, claimed.ID, "project-a", "actor-a"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("open of a stale consumed session = %v, want not-exist", err)
	}
	if _, err := store.DeleteExpiredSessions(ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(claimPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("janitor kept a stale consumed claim: %v", err)
	}
}

func TestEncryptedStoreRestoreFinishesAConsumedClaim(t *testing.T) {
	store := newEncryptedArtifactTestStore(t)
	ctx := context.Background()
	claimed := saveAndClaimNonExpiringTestSession(t, store, "restore-consumed")
	if err := store.MarkSessionClaimConsumed(claimed.ID, "project-a", "actor-a", claimed.ClaimToken); err != nil {
		t.Fatal(err)
	}
	restored, err := store.RestoreSessionClaim(ctx, claimed.ID, "project-a", "actor-a", claimed.ClaimToken, time.Now().UTC())
	if err != nil || restored {
		t.Fatalf("RestoreSessionClaim = %v, %v; want a consumed claim finished, not restored", restored, err)
	}
	if _, err := os.Stat(filepath.Join(store.root, ".sessions", claimed.ID+".claim.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("consumed claim survived restore: %v", err)
	}
	if _, _, err := store.ClaimSession(ctx, claimed.ID, "project-a", "actor-a", 1024); err == nil {
		t.Fatal("restored consumed session became claimable")
	}
}

func TestEncryptedStoreAbandonedUncommittedClaimStillRecovers(t *testing.T) {
	store := newEncryptedArtifactTestStore(t)
	ctx := context.Background()
	claimed := saveAndClaimNonExpiringTestSession(t, store, "abandoned")
	ageTestSessionClaim(t, store, claimed.ID)
	payload, reclaimed, err := store.ClaimSession(ctx, claimed.ID, "project-a", "actor-a", 1024)
	if err != nil {
		t.Fatalf("an abandoned, uncommitted claim was not recovered: %v", err)
	}
	clear(payload)
	if reclaimed.Consumed {
		t.Fatal("recovered claim was marked consumed")
	}
	if err := store.CompleteSessionClaim(ctx, claimed.ID, "project-a", "actor-a", reclaimed.ClaimToken); err != nil {
		t.Fatal(err)
	}
}
