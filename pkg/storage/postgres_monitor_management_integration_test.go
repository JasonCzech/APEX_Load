//go:build postgres_integration

package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func managementPostgresFixture(t *testing.T) (*PostgresStore, string) {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("APEXLOAD_TEST_POSTGRES_URL"))
	if dsn == "" {
		t.Skip("APEXLOAD_TEST_POSTGRES_URL is not set")
	}
	admin, e := sql.Open("pgx", dsn)
	if e != nil {
		t.Fatal(e)
	}
	schema := fmt.Sprintf("apexload_management_%d", time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if _, e = admin.ExecContext(ctx, "CREATE SCHEMA "+quotePostgresIdentifier(schema)); e != nil {
		admin.Close()
		t.Fatal(e)
	}
	scoped := postgresIntegrationURLWithSearchPath(t, dsn, schema)
	pg, e := NewPostgresStore(scoped, StoreConnectionOptions{MaxOpenConns: 4}, false)
	if e != nil {
		admin.Exec("DROP SCHEMA " + quotePostgresIdentifier(schema) + " CASCADE")
		admin.Close()
		t.Fatal(e)
	}
	t.Cleanup(func() {
		pg.Close()
		if _, e := admin.Exec("DROP SCHEMA " + quotePostgresIdentifier(schema) + " CASCADE"); e != nil {
			t.Error(e)
		}
		admin.Close()
	})
	return pg, scoped
}
func TestPostgresManagementRelease28LegacyUpgrade(t *testing.T) {
	pg, dsn := managementPostgresFixture(t)
	m := seedLegacyMonitorReceipt(t, pg.SQLiteStore)
	removeManagementFoundationSchema(t, pg.db)
	if e := pg.Close(); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		reopened, e := NewPostgresStore(dsn, StoreConnectionOptions{MaxOpenConns: 4}, false)
		if e != nil {
			t.Fatal(e)
		}
		assertManagementLegacyUpgrade(t, reopened.SQLiteStore, m)
		for _, column := range []string{"evidence_version", "management_snapshot_count", "management_row_count", "collection_generation"} {
			var typ string
			if e = reopened.db.QueryRow(`SELECT data_type FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='run_monitor_batches' AND column_name=?`, column).Scan(&typ); e != nil || typ != "bigint" {
				t.Fatalf("exact count %s = %q %v", column, typ, e)
			}
		}
		reopened.Close()
	}
}
func TestPostgresManagementCohortConcurrentFences(t *testing.T) {
	pg, dsn := managementPostgresFixture(t)
	s := pg.SQLiteStore
	_, _, now := seedMonitorFixture(t, s)
	_, m, _ := managementSeed(t, s, now)
	addManagementRun(t, s, m, "monitor-run-2", now)
	peer, e := NewPostgresStore(dsn, StoreConnectionOptions{MaxOpenConns: 4}, false)
	if e != nil {
		t.Fatal(e)
	}
	defer peer.Close()
	type result struct {
		poll *ManagementPoll
		err  error
	}
	ch := make(chan result, 2)
	var wg sync.WaitGroup
	for i, store := range []*SQLiteStore{s, peer.SQLiteStore} {
		wg.Add(1)
		go func(i int, store *SQLiteStore) {
			defer wg.Done()
			p, e := beginIssuedManagementPoll(store, m.ProjectID, m.CollectorID, "instance-1", "monitor-run:"+m.ID, fmt.Sprintf("compete-%d", i), now)
			ch <- result{p, e}
		}(i, store)
	}
	wg.Wait()
	close(ch)
	var admitted *ManagementPoll
	wins := 0
	for r := range ch {
		if r.err == nil {
			wins++
			admitted = r.poll
		} else if !errors.Is(r.err, ErrManagementPollUnavailable) {
			t.Fatal(r.err)
		}
	}
	if wins != 1 {
		t.Fatalf("concurrent admission winners %d", wins)
	}
	// Terminal publication cannot partially advance the surviving run.
	if e = peer.StopRunMonitors(m.ProjectID, "monitor-run", now); e != nil {
		t.Fatal(e)
	}
	receipts := managementReceipts(admitted, now)
	if e = s.AcceptManagementPoll(m.ProjectID, m.CollectorID, "instance-1", admitted.ID, managementCore, receipts, now); e != nil {
		t.Fatal(e)
	}
	p, e := peer.GetManagementPoll(m.ProjectID, admitted.ID)
	if e != nil || len(p.PublishedReceipts) != 1 {
		t.Fatalf("cohort publication: %#v %v", p, e)
	}
	if e = peer.AcceptManagementPoll(m.ProjectID, m.CollectorID, "instance-1", admitted.ID, managementCore, receipts, now.Add(time.Hour)); e != nil {
		t.Fatalf("peer replay: %v", e)
	}
	// Source counters do not narrow to PostgreSQL int4, and JSON remains exact.
	if _, e = s.db.Exec(`UPDATE monitor_collection_leases SET generation=?,poll_sequence=? WHERE project_id=? AND source_id=?`, int64(1<<33), int64(1<<33), m.ProjectID, m.ID); e != nil {
		t.Fatal(e)
	}
	next, e := beginIssuedManagementPoll(peer.SQLiteStore, m.ProjectID, m.CollectorID, "instance-1", "monitor-run-2:"+m.ID, "large-cursor", now)
	if e != nil || next.Generation != 1<<33 || next.Sequence != (1<<33)+1 {
		t.Fatalf("64-bit cursor: %#v %v", next, e)
	}
	later := now.Add(ManagementPollLifetime + time.Second)
	saveManagementReady(t, s, m, "instance-2", later)
	if _, e = beginIssuedManagementPoll(peer.SQLiteStore, m.ProjectID, m.CollectorID, "instance-2", "monitor-run-2:"+m.ID, "takeover", later); !errors.Is(e, ErrManagementPollUnavailable) {
		t.Fatalf("unproved takeover: %v", e)
	}
	if e = s.AbandonManagementPoll(m.ProjectID, m.CollectorID, "instance-1", next.ID, next.Generation, ManagementDrainProof{Kind: "completed"}, later); e != nil {
		t.Fatal(e)
	}
	takeover, e := beginIssuedManagementPoll(peer.SQLiteStore, m.ProjectID, m.CollectorID, "instance-2", "monitor-run-2:"+m.ID, "takeover", later)
	if e != nil || takeover.Generation <= next.Generation {
		t.Fatalf("confirmed takeover: %#v %v", takeover, e)
	}
	if e = s.DeleteTest("monitor-run-2"); e != nil {
		t.Fatal(e)
	}
	if e = peer.AcceptManagementPoll(m.ProjectID, m.CollectorID, "instance-2", takeover.ID, managementCore, managementReceipts(takeover, later), later); e != nil {
		t.Fatalf("terminal empty cohort audit: %v", e)
	}
	takeover, e = s.GetManagementPoll(m.ProjectID, takeover.ID)
	if e != nil || len(takeover.PublishedReceipts) != 0 {
		t.Fatalf("late deleted run receipt: %#v %v", takeover, e)
	}
}

// Database lock observations synchronize the real lifecycle writer with fresh
// admission/publication, without timing assumptions or application test hooks.
func waitManagementPostgresBlockedBy(t *testing.T, db *storeDB, blocker int) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var pid int
		err := db.QueryRow(`SELECT pid FROM pg_stat_activity WHERE ?=ANY(pg_blocking_pids(pid)) ORDER BY pid LIMIT 1`, blocker).Scan(&pid)
		if err == nil {
			return pid
		}
		if !errors.Is(err, sql.ErrNoRows) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("management lifecycle operation did not retain its PostgreSQL row lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestPostgresManagementTerminalStatusSerializesIssuanceAndPublication(t *testing.T) {
	for _, operation := range []string{"issuance", "publication"} {
		t.Run(operation, func(t *testing.T) {
			pg, _ := managementPostgresFixture(t)
			s := pg.SQLiteStore
			_, _, now := seedMonitorFixture(t, s)
			_, m, _ := managementSeed(t, s, now)
			test, err := s.GetTest("monitor-run")
			if err != nil {
				t.Fatal(err)
			}
			test.StartedAt = &now
			if err := s.SaveTest(test); err != nil {
				t.Fatal(err)
			}
			poll, err := s.BeginManagementPoll(m.ProjectID, m.CollectorID, "instance-1", "monitor-run:"+m.ID, "terminal-race", now)
			if err != nil {
				t.Fatal(err)
			}
			if operation == "publication" {
				if err := s.MarkManagementPollIssued(m.ProjectID, m.CollectorID, "instance-1", poll.ID, poll.Generation, now); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.db.Exec(`INSERT INTO run_archive_failures(test_id,project_id,test_start_time,test_end_time,test_status,reason,recorded_at) VALUES(?,?,?,?,?,?,?)`, test.ID, m.ProjectID, test.StartTime, now, "FAILED", RunArchiveFailureUnconfirmed, now); err != nil {
				t.Fatal(err)
			}
			// The real UpdateTestStatus writes COMPLETE, then waits on its
			// archive marker before commit. Management must wait on that test.
			holder, err := s.db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer holder.Rollback()
			var holderPID int
			if err := holder.QueryRow(`SELECT pg_backend_pid()`).Scan(&holderPID); err != nil {
				t.Fatal(err)
			}
			if _, err := holder.Exec(`UPDATE run_archive_failures SET reason=reason WHERE test_id=?`, test.ID); err != nil {
				t.Fatal(err)
			}
			terminal := make(chan error, 1)
			go func() { terminal <- s.UpdateTestStatus(test.ID, "COMPLETE", &now, "") }()
			terminalPID := waitManagementPostgresBlockedBy(t, s.db, holderPID)
			result := make(chan error, 1)
			go func() {
				if operation == "issuance" {
					result <- s.MarkManagementPollIssued(m.ProjectID, m.CollectorID, "instance-1", poll.ID, poll.Generation, now)
				} else {
					result <- s.AcceptManagementPoll(m.ProjectID, m.CollectorID, "instance-1", poll.ID, managementCore, managementReceipts(poll, now), now)
				}
			}()
			waitManagementPostgresBlockedBy(t, s.db, terminalPID)
			if err := holder.Rollback(); err != nil {
				t.Fatal(err)
			}
			if err := <-terminal; err != nil {
				t.Fatal(err)
			}
			err = <-result
			if operation == "issuance" {
				if !errors.Is(err, ErrManagementPollUnavailable) {
					t.Fatalf("terminal status admitted fresh authority: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			receipts, err := s.ListMonitorBatchReceipts(m.ProjectID, test.ID, "", -1, 10)
			if err != nil || len(receipts) != 0 {
				t.Fatalf("fresh receipt crossed terminal commit: %#v %v", receipts, err)
			}
		})
	}
}

func TestPostgresManagementConcurrentStartsRespectAssignedCapacity(t *testing.T) {
	pg, dsn := managementPostgresFixture(t)
	s := pg.SQLiteStore
	_, _, now := seedMonitorFixture(t, s)
	_, m, _ := managementSeed(t, s, now)
	fillManagementCollectorAssignments(t, s, m, now, MaxCollectorAssignedMonitorJobs-1)
	peer, err := NewPostgresStore(dsn, StoreConnectionOptions{MaxOpenConns: 4}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	for _, id := range []string{"concurrent-start-1", "concurrent-start-2"} {
		if err := s.SaveTest(&TestRecord{ID: id, Name: id, ProjectID: m.ProjectID, ScenarioID: "monitor-scenario", Status: "INITIALIZING", StartTime: now}); err != nil {
			t.Fatal(err)
		}
	}
	results := make(chan error, 2)
	for i, store := range []*SQLiteStore{s, peer.SQLiteStore} {
		go func(i int, store *SQLiteStore) {
			_, err := store.StartRunMonitors(m.ProjectID, fmt.Sprintf("concurrent-start-%d", i+1), "monitor-scenario", now, now.Add(time.Hour))
			results <- err
		}(i, store)
	}
	winners := 0
	for i := 0; i < 2; i++ {
		err := <-results
		if err == nil {
			winners++
		} else if !strings.Contains(err.Error(), "collector_capacity") {
			t.Fatal(err)
		}
	}
	var assigned int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM run_monitor_jobs WHERE project_id=? AND collector_id=? AND state='collecting'`, m.ProjectID, m.CollectorID).Scan(&assigned); err != nil || assigned != MaxCollectorAssignedMonitorJobs || winners != 1 {
		t.Fatalf("concurrent assignments exceeded capacity: %d winners %d error %v", assigned, winners, err)
	}
}

func TestPostgresManagementRetentionCursorSerializesPeers(t *testing.T) {
	pg, dsn := managementPostgresFixture(t)
	s := pg.SQLiteStore
	_, _, now := seedMonitorFixture(t, s)
	_, m, _ := managementSeed(t, s, now)
	polls := []*ManagementPoll{}
	var acceptedReceipts []MonitorBatchReceipt
	for i := 0; i < 3; i++ {
		poll, err := beginIssuedManagementPoll(s, m.ProjectID, m.CollectorID, "instance-1", "monitor-run:"+m.ID, fmt.Sprintf("retention-%d", i), now.Add(time.Duration(i)*time.Millisecond))
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			acceptedReceipts = managementReceipts(poll, now.Add(time.Duration(i)*time.Millisecond))
			err = s.AcceptManagementPoll(m.ProjectID, m.CollectorID, "instance-1", poll.ID, managementCore, acceptedReceipts, now.Add(time.Duration(i)*time.Millisecond))
		} else {
			err = s.AbandonManagementPoll(m.ProjectID, m.CollectorID, "instance-1", poll.ID, poll.Generation, ManagementDrainProof{Kind: "completed"}, now.Add(time.Duration(i)*time.Millisecond))
		}
		if err != nil {
			t.Fatal(err)
		}
		polls = append(polls, poll)
	}
	peer, err := NewPostgresStore(dsn, StoreConnectionOptions{MaxOpenConns: 4}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	type outcome struct {
		scanned, pruned int64
		err             error
	}
	results := make(chan outcome, 2)
	for _, store := range []*SQLiteStore{s, peer.SQLiteStore} {
		go func(store *SQLiteStore) {
			scanned, pruned, err := store.SweepManagementPollRetention(context.Background(), now.Add(time.Hour), 1)
			results <- outcome{scanned, pruned, err}
		}(store)
	}
	var total int64
	for i := 0; i < 2; i++ {
		result := <-results
		if result.err != nil || result.scanned != 1 {
			t.Fatalf("peer bounded cursor sweep: %#v", result)
		}
		total += result.pruned
	}
	if total != 1 {
		t.Fatalf("protected first row blocked cursor or peers repeated page: %d", total)
	}
	if _, err := s.GetManagementPoll(m.ProjectID, polls[0].ID); err != nil {
		t.Fatalf("durable receipt journal pruned: %v", err)
	}
	if err := s.StopRunMonitors(m.ProjectID, "monitor-run", now); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		scanned, _, err := peer.SweepManagementPollRetention(context.Background(), now.Add(time.Hour), 1)
		if err != nil || scanned > 1 {
			t.Fatalf("bounded cursor wrap: %d %v", scanned, err)
		}
		if _, err := s.GetManagementPoll(m.ProjectID, polls[2].ID); errors.Is(err, sql.ErrNoRows) {
			break
		}
	}
	if _, err := s.GetManagementPoll(m.ProjectID, polls[2].ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("obsolete latest cursor not reclaimed: %v", err)
	}
	if err := s.AcceptManagementPoll(m.ProjectID, m.CollectorID, "instance-1", polls[0].ID, managementCore, acceptedReceipts, now.Add(2*time.Hour)); err != nil {
		t.Fatalf("protected receipt replay after retention: %v", err)
	}
}

func TestPostgresManagementRetentionCancellationLeavesNoPartialProgress(t *testing.T) {
	pg, _ := managementPostgresFixture(t)
	s := pg.SQLiteStore
	_, _, now := seedMonitorFixture(t, s)
	_, m, _ := managementSeed(t, s, now)
	poll, err := beginIssuedManagementPoll(s, m.ProjectID, m.CollectorID, "instance-1", "monitor-run:"+m.ID, "retention-cancel", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AbandonManagementPoll(m.ProjectID, m.CollectorID, "instance-1", poll.ID, poll.Generation, ManagementDrainProof{Kind: "completed"}, now); err != nil {
		t.Fatal(err)
	}
	if err := s.StopRunMonitors(m.ProjectID, "monitor-run", now); err != nil {
		t.Fatal(err)
	}
	holder, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback()
	var holderPID int
	if err := holder.QueryRow(`SELECT pg_backend_pid()`).Scan(&holderPID); err != nil {
		t.Fatal(err)
	}
	if _, err := holder.Exec(`UPDATE projects SET updated_at=updated_at WHERE id=?`, m.ProjectID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, _, err := s.SweepManagementPollRetention(ctx, now.Add(time.Hour), 1)
		result <- err
	}()
	waitManagementPostgresBlockedBy(t, s.db, holderPID)
	cancel()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("cancelled maintenance accepted partial cursor progress")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled maintenance waited indefinitely on project fence")
	}
	if err := holder.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetManagementPoll(m.ProjectID, poll.ID); err != nil {
		t.Fatalf("cancelled maintenance deleted journal: %v", err)
	}
	if scanned, pruned, err := s.SweepManagementPollRetention(context.Background(), now.Add(time.Hour), 1); err != nil || scanned != 1 || pruned != 1 {
		t.Fatalf("cancelled maintenance advanced durable cursor: %d %d %v", scanned, pruned, err)
	}
}
