package storage

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func openSharedReplaySessionLeaseStores(t *testing.T) (*SQLiteStore, *SQLiteStore) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "shared-replay-session-leases.db")
	first, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("open first store: %v", err)
	}
	second, err := NewSQLiteStore(path)
	if err != nil {
		_ = first.Close()
		t.Fatalf("open second store: %v", err)
	}
	t.Cleanup(func() {
		_ = second.Close()
		_ = first.Close()
	})
	return first, second
}

func TestReplaySessionLeaseEnforcesDistributedProjectAndGlobalCapacity(t *testing.T) {
	first, second := openSharedReplaySessionLeaseStores(t)
	ctx := context.Background()
	const ttl = time.Minute

	if got, err := first.TryAcquireReplaySessionLease(ctx, "session-a", "owner-a", "project-a", ttl, 2, 1); err != nil || got != ReplaySessionLeaseAcquired {
		t.Fatalf("first acquisition = (%q, %v)", got, err)
	}
	if got, err := second.TryAcquireReplaySessionLease(ctx, "session-b", "owner-b", "project-a", ttl, 2, 1); err != nil || got != ReplaySessionLeaseProjectCapacity {
		t.Fatalf("same-project acquisition = (%q, %v), want project capacity", got, err)
	}
	if got, err := second.TryAcquireReplaySessionLease(ctx, "session-b", "owner-b", "project-b", ttl, 2, 1); err != nil || got != ReplaySessionLeaseAcquired {
		t.Fatalf("second-project acquisition = (%q, %v)", got, err)
	}
	if got, err := first.TryAcquireReplaySessionLease(ctx, "session-c", "owner-c", "project-c", ttl, 2, 1); err != nil || got != ReplaySessionLeaseGlobalCapacity {
		t.Fatalf("over-global acquisition = (%q, %v), want global capacity", got, err)
	}
}

func TestReplaySessionLeaseReleaseIsOwnerFencedAfterTakeover(t *testing.T) {
	first, second := openSharedReplaySessionLeaseStores(t)
	ctx := context.Background()
	const ttl = time.Minute

	if got, err := first.TryAcquireReplaySessionLease(ctx, "shared-session", "old-owner", "project-a", ttl, 4, 2); err != nil || got != ReplaySessionLeaseAcquired {
		t.Fatalf("old-owner acquisition = (%q, %v)", got, err)
	}
	if released, err := first.ReleaseReplaySessionLease(ctx, "shared-session", "old-owner", "project-a"); err != nil || !released {
		t.Fatalf("old-owner release = (%v, %v)", released, err)
	}
	if got, err := second.TryAcquireReplaySessionLease(ctx, "shared-session", "new-owner", "project-b", ttl, 4, 2); err != nil || got != ReplaySessionLeaseAcquired {
		t.Fatalf("new-owner acquisition = (%q, %v)", got, err)
	}
	if released, err := first.ReleaseReplaySessionLease(ctx, "shared-session", "old-owner", "project-a"); err != nil || released {
		t.Fatalf("stale release = (%v, %v), want fenced no-op", released, err)
	}
	if renewed, err := second.RenewReplaySessionLease(ctx, "shared-session", "new-owner", "project-b", ttl); err != nil || !renewed {
		t.Fatalf("successor renewal after stale release = (%v, %v)", renewed, err)
	}
}

func TestReplaySessionRetainedTombstoneRejectsReuseWithoutConsumingCapacity(t *testing.T) {
	first, second := openSharedReplaySessionLeaseStores(t)
	ctx := context.Background()
	const ttl = time.Minute

	if got, err := first.TryAcquireReplaySessionLease(ctx, "terminal-session", "owner-a", "project-a", ttl, 1, 1); err != nil || got != ReplaySessionLeaseAcquired {
		t.Fatalf("acquisition = (%q, %v)", got, err)
	}
	if retained, err := first.RetainReplaySessionLease(ctx, "terminal-session", "owner-a", "project-a", ttl); err != nil || !retained {
		t.Fatalf("retain = (%v, %v)", retained, err)
	}
	if got, err := second.TryAcquireReplaySessionLease(ctx, "terminal-session", "owner-b", "project-b", ttl, 1, 1); err != nil || got != ReplaySessionLeaseConflict {
		t.Fatalf("retained-id acquisition = (%q, %v), want conflict", got, err)
	}
	if got, err := second.TryAcquireReplaySessionLease(ctx, "active-session", "owner-b", "project-a", ttl, 1, 1); err != nil || got != ReplaySessionLeaseAcquired {
		t.Fatalf("capacity with tombstone = (%q, %v), want acquired", got, err)
	}
	if released, err := first.ReleaseRetainedReplaySessionLease(ctx, "terminal-session", "wrong-owner", "project-a"); err != nil || released {
		t.Fatalf("stale tombstone release = (%v, %v), want fenced no-op", released, err)
	}
	if released, err := first.ReleaseRetainedReplaySessionLease(ctx, "terminal-session", "owner-a", "project-a"); err != nil || !released {
		t.Fatalf("tombstone release = (%v, %v)", released, err)
	}
	if released, err := second.ReleaseReplaySessionLease(ctx, "active-session", "owner-b", "project-a"); err != nil || !released {
		t.Fatalf("active release = (%v, %v)", released, err)
	}
	if got, err := second.TryAcquireReplaySessionLease(ctx, "terminal-session", "owner-c", "project-b", ttl, 1, 1); err != nil || got != ReplaySessionLeaseAcquired {
		t.Fatalf("reuse after prune = (%q, %v), want acquired", got, err)
	}
}

func TestReplaySessionLeaseReleaseDoesNotAccumulateHistoricalRows(t *testing.T) {
	store, _ := openSharedReplaySessionLeaseStores(t)
	ctx := context.Background()
	const ttl = time.Minute

	for i := 0; i < replaySessionCleanupBatch+10; i++ {
		sessionID := fmt.Sprintf("session-%d", i)
		ownerID := fmt.Sprintf("owner-%d", i)
		if got, err := store.TryAcquireReplaySessionLease(ctx, sessionID, ownerID, "project-a", ttl, 1, 1); err != nil || got != ReplaySessionLeaseAcquired {
			t.Fatalf("acquire %d = (%q, %v)", i, got, err)
		}
		if released, err := store.ReleaseReplaySessionLease(ctx, sessionID, ownerID, "project-a"); err != nil || !released {
			t.Fatalf("release %d = (%v, %v)", i, released, err)
		}
	}

	var rows int
	if err := store.db.db.QueryRow(`SELECT COUNT(*) FROM replay_session_leases`).Scan(&rows); err != nil {
		t.Fatalf("count replay session lease rows: %v", err)
	}
	if rows != 0 {
		t.Fatalf("replay session lease rows = %d, want 0 after releases", rows)
	}
}

func TestReplaySessionLeaseAdmissionBoundsExpiredCleanupBatch(t *testing.T) {
	store, _ := openSharedReplaySessionLeaseStores(t)
	ctx := context.Background()
	expiredAt := time.Now().UTC().Add(-time.Hour)
	for i := 0; i < replaySessionCleanupBatch+10; i++ {
		if _, err := store.db.db.Exec(`INSERT INTO replay_session_leases
			(session_id, owner_token, project_id, state, generation, expires_at, updated_at)
			VALUES (?, ?, ?, ?, 1, ?, ?)`,
			fmt.Sprintf("expired-%d", i), fmt.Sprintf("owner-%d", i), "project-a",
			replaySessionStateRetained, expiredAt, expiredAt); err != nil {
			t.Fatalf("insert expired row %d: %v", i, err)
		}
	}

	if got, err := store.TryAcquireReplaySessionLease(ctx, "fresh", "fresh-owner", "project-a", time.Minute, 1, 1); err != nil || got != ReplaySessionLeaseAcquired {
		t.Fatalf("fresh acquisition = (%q, %v)", got, err)
	}
	var expiredRows int
	if err := store.db.db.QueryRow(`SELECT COUNT(*) FROM replay_session_leases WHERE expires_at <= ?`, time.Now().UTC()).Scan(&expiredRows); err != nil {
		t.Fatalf("count expired rows: %v", err)
	}
	if expiredRows != 10 {
		t.Fatalf("expired rows after admission = %d, want 10 after bounded %d-row cleanup", expiredRows, replaySessionCleanupBatch)
	}
}
