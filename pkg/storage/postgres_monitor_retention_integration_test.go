//go:build postgres_integration

package storage

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestPostgresManagementPollRetentionSweepProjectPrefix(t *testing.T) {
	pg, _ := managementPostgresFixture(t)
	testManagementPollRetentionProjectPrefix(t, pg.SQLiteStore)
}

func TestPostgresManagementPollRetentionSweepConcurrentCursor(t *testing.T) {
	pg, dsn := managementPostgresFixture(t)
	s := pg.SQLiteStore
	_, _, now := seedMonitorFixture(t, s)
	_, m, _ := managementSeed(t, s, now)
	poll, err := beginIssuedManagementPoll(s, m.ProjectID, m.CollectorID, "instance-1", "monitor-run:"+m.ID, "retention-receipt", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AcceptManagementPoll(m.ProjectID, m.CollectorID, "instance-1", poll.ID, managementCore, managementReceipts(poll, now), now); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		seedManagementRetentionJournal(t, s, m.ProjectID, fmt.Sprintf("concurrent-retention-%d", i), "abandoned", now.Add(time.Duration(i+1)*time.Second))
	}
	peer, err := NewPostgresStore(dsn, StoreConnectionOptions{MaxOpenConns: 4}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	type result struct {
		scanned, pruned int64
		err             error
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	start := make(chan struct{})
	results := make(chan result, 2)
	for _, store := range []*SQLiteStore{s, peer.SQLiteStore} {
		go func(store *SQLiteStore) {
			<-start
			scanned, pruned, err := store.SweepManagementPollRetention(ctx, now.Add(time.Hour), 3)
			results <- result{scanned, pruned, err}
		}(store)
	}
	close(start)
	var scanned, pruned int64
	for i := 0; i < 2; i++ {
		r := <-results
		if r.err != nil || r.scanned != 3 || r.pruned < 2 || r.pruned > 3 {
			t.Fatalf("concurrent bounded page: %#v", r)
		}
		scanned, pruned = scanned+r.scanned, pruned+r.pruned
	}
	if scanned != 6 || pruned != 5 {
		t.Fatalf("replicas lost or repeated cursor progress: scanned=%d pruned=%d", scanned, pruned)
	}
	if cursor := managementRetentionCursor(t, s); cursor.ID != "concurrent-retention-4" {
		t.Fatalf("unexpected durable concurrent cursor: %#v", cursor)
	}
	if !managementRetentionJournalExists(t, s, poll.ID) {
		t.Fatal("concurrent retention deleted accepted receipt evidence")
	}
	if scanned, pruned, err := peer.SweepManagementPollRetention(ctx, now.Add(time.Hour), 3); err != nil || scanned != 1 || pruned != 1 {
		t.Fatalf("remaining concurrent suffix: scanned=%d pruned=%d err=%v", scanned, pruned, err)
	}
}

func TestPostgresManagementPollRetentionSweepCancelsLockWaits(t *testing.T) {
	for _, lock := range []string{"cursor", "project"} {
		t.Run(lock, func(t *testing.T) {
			pg, _ := managementPostgresFixture(t)
			s := pg.SQLiteStore
			_, m, now := seedMonitorFixture(t, s)
			if scanned, pruned, err := s.SweepManagementPollRetention(context.Background(), now.Add(time.Hour), 1); err != nil || scanned != 0 || pruned != 0 {
				t.Fatalf("cursor initialization: scanned=%d pruned=%d err=%v", scanned, pruned, err)
			}
			seedManagementRetentionJournal(t, s, m.ProjectID, "blocked-retention", "abandoned", now)
			holder, err := s.db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer holder.Rollback()
			var pid int
			if err := holder.QueryRow(`SELECT pg_backend_pid()`).Scan(&pid); err != nil {
				t.Fatal(err)
			}
			query, key := `UPDATE app_settings SET value=value WHERE key=?`, managementPollRetentionCursorKey
			if lock == "project" {
				query, key = `UPDATE projects SET updated_at=updated_at WHERE id=?`, m.ProjectID
			}
			if _, err := holder.Exec(query, key); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			results := make(chan error, 1)
			go func() {
				_, _, err := s.SweepManagementPollRetention(ctx, now.Add(time.Hour), 1)
				results <- err
			}()
			waitManagementPostgresBlockedBy(t, s.db, pid)
			cancel()
			select {
			case err := <-results:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("retention %s lock ignored cancellation: %v", lock, err)
				}
			case <-time.After(3 * time.Second):
				t.Fatalf("retention %s lock wait did not cancel", lock)
			}
			if err := holder.Rollback(); err != nil {
				t.Fatal(err)
			}
			if !managementRetentionJournalExists(t, s, "blocked-retention") {
				t.Fatal("cancelled sweep committed deletion")
			}
			if cursor := managementRetentionCursor(t, s); cursor.ID != "" {
				t.Fatalf("cancelled sweep committed cursor advance: %#v", cursor)
			}
			// The abandoned transaction must release its cursor fence so that a
			// subsequent controller can complete the same page.
			resume, stop := context.WithTimeout(context.Background(), 3*time.Second)
			defer stop()
			if scanned, pruned, err := s.SweepManagementPollRetention(resume, now.Add(time.Hour), 1); err != nil || scanned != 1 || pruned != 1 {
				t.Fatalf("cancelled fence leaked: scanned=%d pruned=%d err=%v", scanned, pruned, err)
			}
		})
	}
}
