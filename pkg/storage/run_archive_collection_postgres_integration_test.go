//go:build postgres_integration

package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPostgresRunArchiveCollectionDurability(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("APEXLOAD_TEST_POSTGRES_URL"))
	if databaseURL == "" {
		t.Skip("APEXLOAD_TEST_POSTGRES_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	schema := fmt.Sprintf("apexload_archive_collection_%d", time.Now().UnixNano())
	quoted := quotePostgresIdentifier(schema)
	if _, err := db.ExecContext(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if _, err := db.ExecContext(cleanupCtx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	store, err := NewPostgresStore(postgresIntegrationURLWithSearchPath(t, databaseURL, schema), StoreConnectionOptions{}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	checkArchiveFinalizationTerminalIdentity(t, store.SQLiteStore)
	checkArchiveRecoveredPublicationRefreshIsAtomic(t, store.SQLiteStore)
	checkPostgresArchiveCompletionLeaseExpiryWhileWaiting(t, store.SQLiteStore, false)
	checkPostgresArchiveCompletionLeaseExpiryWhileWaiting(t, store.SQLiteStore, true)
	checkArchiveOrphanIncarnationFence(t, store.SQLiteStore)
	checkArchiveOrphanExplicitIncarnation(t, store.SQLiteStore)
	checkArchiveFinalizationCASRejectsReplacedIncarnation(t, store.SQLiteStore)
	checkRunArchiveCollectionCache(t, store.SQLiteStore)
	checkRunArchiveCollectionDurability(t, store.SQLiteStore)
	checkRunArchiveCollectionPublicationPrecedence(t, store.SQLiteStore)
}

// The completion transaction locks the lease before the run row. Waiting for a
// busy run must not implicitly extend the lease while holding its row lock.
func checkPostgresArchiveCompletionLeaseExpiryWhileWaiting(t *testing.T, s *SQLiteStore, orphan bool) {
	t.Helper()
	now := time.Now().UTC()
	id := fmt.Sprintf("completion-expiry-%d", now.UnixNano())
	row := &TestRecord{ID: id, Name: id, ProjectID: DefaultProjectID, StartTime: now.Add(-time.Minute), EndTime: &now, Status: "COMPLETE"}
	if err := s.SaveTest(row); err != nil {
		t.Fatal(err)
	}
	record := RunArchiveRecord{TestID: id, CreatedAt: row.StartTime, CompletedAt: now}
	job := &ArchiveFinalizationJob{TestID: id, ProjectID: row.ProjectID, ArchiveRecord: record}
	if !orphan {
		if err := s.SaveArchiveFinalizationJob(job); err != nil {
			t.Fatal(err)
		}
	}
	held, lease, err := s.TryAcquireSchedulerLeaseTerm(id, "owner", time.Now(), 2*time.Second)
	if err != nil || !held || lease == nil {
		t.Fatal(err)
	}
	blocker, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback()
	var locked string
	var blockerPID int
	if err := blocker.QueryRow(`SELECT id FROM tests WHERE id = ? FOR UPDATE`, id).Scan(&locked); err != nil {
		t.Fatal(err)
	}
	if err := blocker.QueryRow(`SELECT pg_backend_pid()`).Scan(&blockerPID); err != nil {
		t.Fatal(err)
	}
	type completionResult struct {
		committed bool
		err       error
	}
	done := make(chan completionResult, 1)
	go func() {
		var ok bool
		var err error
		if orphan {
			ok, err = s.SaveArchiveFinalizationJobIfOrphanForRun(job, row.StartTime, row.Status, id, "owner")
		} else {
			ok, err = s.CompleteArchiveFinalizationForRun(id, job.IntentToken, "", &record, ArchiveFinalizationRunIdentity{ProjectID: row.ProjectID, StartTime: row.StartTime, EndTime: now, Status: row.Status, LeaseName: id, LeaseOwner: "owner"})
		}
		done <- completionResult{ok, err}
	}()
	defer func() { _ = blocker.Rollback() }()
	deadline := time.Now().Add(time.Second)
	blocked := false
	for time.Now().Before(deadline) {
		var count int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM pg_stat_activity WHERE ? = ANY(pg_blocking_pids(pid))`, blockerPID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count > 0 {
			blocked = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("completion did not wait for the locked run row")
	}
	if delay := time.Until(lease.ExpiresAt.Add(50 * time.Millisecond)); delay > 0 {
		time.Sleep(delay)
	}
	if err := blocker.Rollback(); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		if result.err != nil || result.committed {
			t.Fatalf("expired waiting completion=%v: %v", result.committed, result.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("completion did not release after run unlock")
	}
	if _, err := s.GetRunArchive(id); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("expired transaction published catalog", err)
	}
	_, err = s.GetArchiveFinalizationJob(id)
	if orphan {
		if !errors.Is(err, sql.ErrNoRows) {
			t.Fatal("expired orphan transaction queued publication", err)
		}
	} else if err != nil {
		t.Fatal("expired transaction erased pending job", err)
	}
}
