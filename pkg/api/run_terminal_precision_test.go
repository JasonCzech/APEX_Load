package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// Terminal timestamps are annotation bounds and immutable history identities.
// Exercise the actual lifecycle transitions here and through the PostgreSQL
// integration entrypoint so the leader's held value matches a follower's read.
func TestRunTerminalPrecisionAPI(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()
	exerciseRunTerminalPrecisionAPI(t, server, store)
}

func exerciseRunTerminalPrecisionAPI(t *testing.T, server *Server, store *storage.SQLiteStore) {
	newRun := func(t *testing.T, id, status string, started bool) *ManagedTest {
		t.Helper()
		createdAt := atMicrosecond(time.Now().Add(-2 * time.Minute))
		record := &storage.TestRecord{
			ID: id, Name: id, ProjectID: storage.DefaultProjectID,
			Status: status, StartTime: createdAt, Duration: int64(time.Minute),
		}
		if started {
			startedAt := createdAt.Add(time.Second)
			record.StartedAt = &startedAt
		}
		if err := store.SaveTest(record); err != nil {
			t.Fatal(err)
		}
		run := managedTestFromRecord(record)
		server.runner.mu.Lock()
		server.runner.tests[id] = run
		server.runner.mu.Unlock()
		t.Cleanup(func() {
			server.runner.mu.Lock()
			delete(server.runner.tests, id)
			server.runner.mu.Unlock()
			if err := store.DeleteTest(id); err != nil {
				t.Errorf("delete precision run: %v", err)
			}
		})
		return run
	}
	checkTerminal := func(t *testing.T, run *ManagedTest, status string) *ManagedTest {
		t.Helper()
		record, err := store.GetTest(run.ID)
		if err != nil || record == nil {
			t.Fatalf("read terminal run = %+v, %v", record, err)
		}
		if run.Status != status || record.Status != status || run.EndedAt == nil || record.EndTime == nil {
			t.Fatalf("terminal state held=%s/%v, stored=%s/%v; want %s with end time", run.Status, run.EndedAt, record.Status, record.EndTime, status)
		}
		requireAPIStoredTimestamp(t, "run ended_at", *run.EndedAt, *record.EndTime)
		follower := managedTestFromRecord(record)
		if !sameStoredTestHistoryIdentity(run, follower) || storedTestHistoryCacheKey(run) != storedTestHistoryCacheKey(follower) {
			t.Error("leader and follower disagree on terminal history identity")
		}
		different := *follower
		differentEnd := follower.EndedAt.Add(time.Nanosecond)
		different.EndedAt = &differentEnd
		if sameStoredTestHistoryIdentity(run, &different) {
			t.Error("terminal history identity accepted a different end time")
		}
		return follower
	}
	checkAnnotation := func(t *testing.T, leader, follower *ManagedTest) {
		t.Helper()
		// Keep the endpoint timestamp returned by the leader, then serve the
		// annotation request from a run hydrated from the durable follower row.
		endpoint := *leader.EndedAt
		server.runner.mu.Lock()
		server.runner.tests[leader.ID] = follower
		server.runner.mu.Unlock()
		defer func() {
			server.runner.mu.Lock()
			server.runner.tests[leader.ID] = leader
			server.runner.mu.Unlock()
		}()
		created := precisionAnnotationRequest(t, server, leader.ID, endpoint, 0)
		if created.Code != http.StatusCreated {
			t.Fatalf("annotate the leader's endpoint against the follower: %d %s", created.Code, created.Body.String())
		}
		outside := precisionAnnotationRequest(t, server, leader.ID, endpoint.Add(time.Microsecond), 1)
		if outside.Code != http.StatusBadRequest {
			t.Fatalf("annotate after the executed run: %d %s", outside.Code, outside.Body.String())
		}
	}

	for _, name := range []string{"monotonic_clock", "non_utc_zone"} {
		t.Run("completion_"+name, func(t *testing.T) {
			run := newRun(t, "precision-terminal-"+name, "RUNNING", true)
			completedAt := time.Now().Add(-time.Second)
			// Force the precision loss on every platform without removing the
			// monotonic reading used by elapsed-time calculations.
			completedAt = completedAt.Add(789 - time.Duration(completedAt.Nanosecond()%int(time.Microsecond)))
			if name == "non_utc_zone" {
				completedAt = completedAt.In(time.FixedZone("MST", -7*60*60))
			}
			if err := server.runner.completeTestRun(run, "COMPLETE", completedAt, "", nil); err != nil {
				t.Fatal(err)
			}
			follower := checkTerminal(t, run, "COMPLETE")
			if run.EndedAt.Location() != completedAt.Location() {
				t.Errorf("completion changed zone from %s to %s", completedAt.Location(), run.EndedAt.Location())
			}
			if !run.EndedAt.Equal(completedAt.Add(-789 * time.Nanosecond)) {
				t.Errorf("completion timestamp = %s, want the original microsecond", run.EndedAt.Format(time.RFC3339Nano))
			}
			if name == "monotonic_clock" && *run.EndedAt == run.EndedAt.Round(0) {
				t.Error("completion discarded the monotonic clock reading")
			}
			checkAnnotation(t, run, follower)
		})
	}
	t.Run("failure_before_run", func(t *testing.T) {
		run := newRun(t, "precision-terminal-failure", "INITIALIZING", false)
		if err := server.runner.failTestBeforeRun(run, "precision preflight failure"); err != nil {
			t.Fatal(err)
		}
		checkTerminal(t, run, "FAILED")
		if *run.EndedAt == run.EndedAt.Round(0) {
			t.Error("pre-run failure discarded the monotonic clock reading")
		}
	})
	t.Run("cancellation_before_start", func(t *testing.T) {
		run := newRun(t, "precision-terminal-cancel", "QUEUED", false)
		if err := server.runner.StopTest(run.ID); err != nil {
			t.Fatal(err)
		}
		checkTerminal(t, run, "ABORTED")
		if run.EndedAt.Location() != time.UTC {
			t.Error("pre-start cancellation changed its UTC timestamp zone")
		}
	})
	t.Run("interrupted_local_run", func(t *testing.T) {
		run := newRun(t, "precision-terminal-reconcile", "RUNNING", true)
		// Recovery reconciles a hydrated run before adding it to the runner.
		server.runner.mu.Lock()
		delete(server.runner.tests, run.ID)
		server.runner.mu.Unlock()
		if err := server.runner.reconcileStoredActiveTest(run); err != nil {
			t.Fatal(err)
		}
		follower := checkTerminal(t, run, "FAILED")
		if run.EndedAt.Location() != time.UTC {
			t.Error("interruption recovery changed its UTC timestamp zone")
		}
		server.runner.mu.Lock()
		server.runner.tests[run.ID] = run
		server.runner.mu.Unlock()
		checkAnnotation(t, run, follower)
	})
}
