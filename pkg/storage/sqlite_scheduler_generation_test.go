package storage

import (
	"math"
	"path/filepath"
	"testing"
	"time"
)

func TestSchedulerLeaseGenerationAdvancesOnlyOnTakeover(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	acquired, first, err := store.TryAcquireSchedulerLeaseTerm("execution", "controller-a/1", time.Now(), time.Minute)
	if err != nil || !acquired {
		t.Fatalf("first acquire = (%v, %#v, %v)", acquired, first, err)
	}
	if first.Generation != 1 {
		t.Fatalf("first generation = %d, want 1", first.Generation)
	}
	acquired, renewed, err := store.TryAcquireSchedulerLeaseTerm("execution", "controller-a/1", time.Now(), time.Minute)
	if err != nil || !acquired {
		t.Fatalf("renew = (%v, %#v, %v)", acquired, renewed, err)
	}
	if renewed.Generation != first.Generation {
		t.Fatalf("renew generation = %d, want %d", renewed.Generation, first.Generation)
	}
	if !renewed.AcquiredAt.Equal(first.AcquiredAt) {
		t.Fatalf("renew changed acquired_at from %s to %s", first.AcquiredAt, renewed.AcquiredAt)
	}
	if acquired, blocked, err := store.TryAcquireSchedulerLeaseTerm("execution", "controller-b/1", time.Now(), time.Minute); err != nil || acquired || blocked != nil {
		t.Fatalf("blocked contender = (%v, %#v, %v), want false/nil/nil", acquired, blocked, err)
	}
	if err := store.ReleaseSchedulerLease("execution", "controller-a/1"); err != nil {
		t.Fatal(err)
	}
	acquired, takeover, err := store.TryAcquireSchedulerLeaseTerm("execution", "controller-b/1", time.Now(), time.Minute)
	if err != nil || !acquired {
		t.Fatalf("takeover = (%v, %#v, %v)", acquired, takeover, err)
	}
	if takeover.Generation != 2 {
		t.Fatalf("takeover generation = %d, want 2", takeover.Generation)
	}
	// A delayed release by the old owner cannot expire the successor.
	if err := store.ReleaseSchedulerLease("execution", "controller-a/1"); err != nil {
		t.Fatal(err)
	}
	lease, err := store.GetSchedulerLease("execution")
	if err != nil {
		t.Fatal(err)
	}
	if lease.OwnerID != "controller-b/1" || lease.Generation != 2 || !lease.ExpiresAt.After(lease.UpdatedAt) {
		t.Fatalf("successor lease changed by delayed release: %#v", lease)
	}
}

func TestSchedulerLeaseGenerationOverflowFailsClosed(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if acquired, _, err := store.TryAcquireSchedulerLeaseTerm("execution", "controller-a/1", time.Now(), time.Minute); err != nil || !acquired {
		t.Fatalf("seed lease = (%v, %v)", acquired, err)
	}
	if _, err := store.db.Exec(`UPDATE scheduler_leases SET generation = ?, expires_at = ? WHERE name = ?`, int64(math.MaxInt64), time.Now().Add(-time.Minute), "execution"); err != nil {
		t.Fatal(err)
	}
	if acquired, lease, err := store.TryAcquireSchedulerLeaseTerm("execution", "controller-b/1", time.Now(), time.Minute); err == nil || acquired || lease != nil {
		t.Fatalf("overflow takeover = (%v, %#v, %v), want fail closed", acquired, lease, err)
	}
	lease, err := store.GetSchedulerLease("execution")
	if err != nil {
		t.Fatal(err)
	}
	if lease.OwnerID != "controller-a/1" || lease.Generation != math.MaxInt64 {
		t.Fatalf("overflow attempt mutated lease: %#v", lease)
	}
}
