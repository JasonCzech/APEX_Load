package api

import (
	"errors"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// Recovery must identify the exact transition that committed even when its
// reply was lost. These exercises use real storage; PostgreSQL truncates the
// timestamps that SQLite retains. The integration entrypoint runs the same
// exercises against PostgreSQL.
func TestExecutionPlanTimestampPrecisionAPI(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()
	exerciseExecutionPlanTimestampPrecisionAPI(t, server, store)
}

type precisionPlanRunStore struct {
	*storage.SQLiteStore
	rejectFirst bool
	writes      []*storage.ExecutionPlanRun
}

func (s *precisionPlanRunStore) CompareAndSwapExecutionPlanRun(run *storage.ExecutionPlanRun, expectedRevision int64) (bool, error) {
	// Retain the stamp before storage mutates the caller's revision or the
	// reconciliation read replaces it with PostgreSQL's copy.
	snapshot := *run
	if run.Entries != nil {
		snapshot.Entries = make([]storage.ExecutionPlanRunEntry, len(run.Entries))
		copy(snapshot.Entries, run.Entries)
	}
	snapshot.CompletedAt = cloneTimePtr(run.CompletedAt)
	s.writes = append(s.writes, &snapshot)
	if s.rejectFirst && len(s.writes) == 1 {
		return false, nil
	}
	updated, err := s.SQLiteStore.CompareAndSwapExecutionPlanRun(run, expectedRevision)
	if err == nil && updated {
		return false, errors.New("simulated lost execution-plan commit reply")
	}
	return updated, err
}

type precisionPlanOccurrenceStore struct {
	*storage.SQLiteStore
	intended *storage.ExecutionPlanScheduleOccurrence
}

func (s *precisionPlanOccurrenceStore) CompareAndSwapExecutionPlanScheduleOccurrence(occurrence *storage.ExecutionPlanScheduleOccurrence, expectedRevision int64) (bool, error) {
	copy := *occurrence
	copy.MaterializedAt = cloneTimePtr(occurrence.MaterializedAt)
	s.intended = &copy
	updated, err := s.SQLiteStore.CompareAndSwapExecutionPlanScheduleOccurrence(occurrence, expectedRevision)
	if err == nil && updated {
		return false, errors.New("simulated lost execution-plan occurrence commit reply")
	}
	return updated, err
}

func exerciseExecutionPlanTimestampPrecisionAPI(t *testing.T, server *Server, store *storage.SQLiteStore) {
	newRunner := func(t *testing.T) (*TestRunner, *precisionPlanRunStore) {
		t.Helper()
		runner := NewTestRunner(1, NewHub(), store)
		t.Cleanup(runner.beginShutdown)
		writes := &precisionPlanRunStore{SQLiteStore: store}
		runner.execPlanRunStore = writes
		return runner, writes
	}
	newPlan := func(t *testing.T, id, mode string) *storage.ExecutionPlan {
		t.Helper()
		plan := &storage.ExecutionPlan{ID: id, ProjectID: storage.DefaultProjectID, Name: id, RunMode: mode}
		if err := store.SaveExecutionPlan(plan); err != nil {
			t.Fatal(err)
		}
		return plan
	}
	newRun := func(t *testing.T, plan *storage.ExecutionPlan, entries []storage.ExecutionPlanRunEntry) *storage.ExecutionPlanRun {
		t.Helper()
		run := &storage.ExecutionPlanRun{ID: plan.ID + "-run", PlanID: plan.ID, ProjectID: plan.ProjectID, RunMode: plan.RunMode, Status: "running", Entries: entries}
		if created, err := store.CreateExecutionPlanRunIfAbsent(run); err != nil || !created {
			t.Fatalf("create plan run = %v, %v", created, err)
		}
		return run
	}
	requireWrites := func(t *testing.T, writes *precisionPlanRunStore, wantStatus string, wantWrites int) {
		t.Helper()
		if len(writes.writes) != wantWrites {
			t.Fatalf("CAS calls = %d, want %d", len(writes.writes), wantWrites)
		}
		for _, intended := range writes.writes {
			if intended.CompletedAt == nil || intended.CompletedAt.Location() != time.UTC {
				t.Fatalf("terminal completed_at = %v, want a UTC timestamp", intended.CompletedAt)
			}
			requireAPIStoredTimestamp(t, "intended plan completed_at", *intended.CompletedAt, intended.CompletedAt.Truncate(time.Microsecond))
		}
		intended := writes.writes[len(writes.writes)-1]
		stored, err := store.GetExecutionPlanRun(intended.ID)
		if err != nil || stored == nil {
			t.Fatalf("read plan run = %+v, %v", stored, err)
		}
		if stored.Status != wantStatus || stored.CompletedAt == nil {
			t.Fatalf("durable plan run = %+v, want %s with completed_at", stored, wantStatus)
		}
		requireAPIStoredTimestamp(t, "plan completed_at", *intended.CompletedAt, *stored.CompletedAt)
		if !executionPlanRunTransitionMatches(stored, intended, intended.Revision) {
			t.Fatal("committed terminal transition did not match the intended snapshot")
		}
		// A genuinely different instant must still fail the recovery match.
		wrong := *intended
		different := wrong.CompletedAt.Add(time.Nanosecond)
		wrong.CompletedAt = &different
		if executionPlanRunTransitionMatches(stored, &wrong, intended.Revision) {
			t.Fatal("recovery accepted a different completion timestamp")
		}
	}

	t.Run("occurrence_materialized_at", func(t *testing.T) {
		runner, _ := newRunner(t)
		plan := newPlan(t, "precision-plan-occurrence", "parallel")
		now := time.Date(2026, time.October, 18, 12, 0, 0, 123456789, time.FixedZone("EST", -5*60*60))
		plannedAt := now.UTC().Truncate(time.Minute)
		schedule := &storage.ExecutionPlanSchedule{ID: plan.ID + "-schedule", PlanID: plan.ID, ProjectID: plan.ProjectID, Name: "Precision", CronExpr: "*/5 * * * *", Timezone: "UTC", Enabled: true, NextRunAt: &plannedAt}
		if err := store.SaveExecutionPlanSchedule(schedule); err != nil {
			t.Fatal(err)
		}
		occurrence := &storage.ExecutionPlanScheduleOccurrence{ID: plan.ID + "-occurrence", ScheduleID: schedule.ID, PlanID: plan.ID, ProjectID: plan.ProjectID, PlannedAt: plannedAt, Status: "materializing", RunID: plan.ID + "-run"}
		if created, err := store.CreateExecutionPlanScheduleOccurrenceIfAbsent(occurrence); err != nil || !created {
			t.Fatalf("create occurrence = %v, %v", created, err)
		}
		writes := &precisionPlanOccurrenceStore{SQLiteStore: store}
		runner.execPlanScheduleStore = writes
		if err := runner.finalizeExecutionPlanScheduleOccurrence(schedule, occurrence, now, "materialized", "started", occurrence.RunID, "", executionLeaseTerm{}); err != nil {
			t.Fatalf("recover committed occurrence after its reply was lost: %v", err)
		}
		stored, err := store.GetExecutionPlanScheduleOccurrenceByScheduleAndPlannedAt(schedule.ID, plannedAt)
		if err != nil || stored == nil || stored.MaterializedAt == nil || writes.intended == nil || writes.intended.MaterializedAt == nil {
			t.Fatalf("read materialized occurrence = %+v, %v", stored, err)
		}
		requireAPIStoredTimestamp(t, "plan occurrence materialized_at", *writes.intended.MaterializedAt, *stored.MaterializedAt)
		if writes.intended.MaterializedAt.Location() != time.UTC {
			t.Fatal("plan occurrence materialized_at changed its UTC zone")
		}
		if !executionPlanScheduleOccurrenceTransitionMatches(stored, writes.intended, writes.intended.Revision) {
			t.Fatal("committed occurrence did not match the intended snapshot")
		}
		different := writes.intended.MaterializedAt.Add(time.Nanosecond)
		writes.intended.MaterializedAt = &different
		if executionPlanScheduleOccurrenceTransitionMatches(stored, writes.intended, writes.intended.Revision) {
			t.Fatal("recovery accepted a different materialization timestamp")
		}
	})

	for _, status := range []string{"completed", "halted", "failed"} {
		t.Run("sequential_"+status, func(t *testing.T) {
			runner, writes := newRunner(t)
			plan := newPlan(t, "precision-sequential-"+status, "sequential")
			run := newRun(t, plan, []storage.ExecutionPlanRunEntry{{Order: 0, ScenarioID: "precision-scenario", TestID: plan.ID + "-test", Status: "running"}})
			entry := run.Entries[0]
			entry.Status = "complete"
			if status != "completed" {
				entry.Status = "failed"
			}
			state := &seqPlanRunState{runID: run.ID, planID: plan.ID, projectID: plan.ProjectID, status: status, revision: run.Revision, createdAt: run.CreatedAt,
				statuses: []storage.ExecutionPlanRunEntry{entry}}
			if err := runner.persistExecutionPlanRunDurable(state.toStorageRun()); err != nil {
				t.Fatalf("recover sequential terminal transition after its reply was lost: %v", err)
			}
			requireWrites(t, writes, status, 1)
		})
	}
	for _, rollback := range []bool{false, true} {
		name := "parallel_completed"
		if rollback {
			name = "parallel_rollback"
		}
		t.Run(name, func(t *testing.T) {
			runner, writes := newRunner(t)
			writes.rejectFirst = rollback
			plan := newPlan(t, "precision-"+name, "parallel")
			// Keep a real entry snapshot while avoiding workload execution. Empty
			// slices have independent nil/[] JSON semantics outside this audit.
			disabled := false
			plan.Entries = []storage.ExecutionPlanEntry{{Order: 0, ScenarioID: "precision-disabled-scenario", Enabled: &disabled}}
			if err := store.SaveExecutionPlan(plan); err != nil {
				t.Fatal(err)
			}
			_, err := runner.MaterializeAndRunExecutionPlan(plan, "", "")
			if rollback {
				if !errors.Is(err, errExecutionPlanRunConflict) || errors.Is(err, errExecutionPlanRunCommitUnknown) {
					t.Fatalf("rollback should retain the initial conflict and reconcile its lost terminal reply: %v", err)
				}
				requireWrites(t, writes, "failed", 2)
			} else {
				if err != nil {
					t.Fatalf("recover parallel completion after its reply was lost: %v", err)
				}
				requireWrites(t, writes, "completed", 1)
			}
		})
	}
	for _, mode := range []string{"parallel", "sequential"} {
		t.Run(mode+"_recovery", func(t *testing.T) {
			runner, writes := newRunner(t)
			plan := newPlan(t, "precision-"+mode+"-recovery", mode)
			run := newRun(t, plan, []storage.ExecutionPlanRunEntry{{Order: 0, Status: "pending"}})
			var err error
			wantStatus := "halted"
			if mode == "parallel" {
				err = runner.failInterruptedParallelExecutionPlanRun(run, executionLeaseTerm{})
				wantStatus = "failed"
			} else {
				err = runner.haltUnrecoverableSequentialExecutionPlanRun(run, errors.New("incomplete snapshot"), executionLeaseTerm{})
			}
			if err != nil {
				t.Fatalf("recover terminal transition after its reply was lost: %v", err)
			}
			requireWrites(t, writes, wantStatus, 1)
		})
	}
	t.Run("parallel_admission_failure", func(t *testing.T) {
		runner, writes := newRunner(t)
		plan := newPlan(t, "precision-admission-failure", "parallel")
		test := &ManagedTest{ID: plan.ID + "-test", ProjectID: plan.ProjectID, Status: "FAILED", Error: "admission failed"}
		runner.tests[test.ID] = test
		run := newRun(t, plan, []storage.ExecutionPlanRunEntry{{Order: 0, TestID: test.ID, Status: "created"}})
		if err := runner.handoffParallelExecutionPlanTestsOnceWithTerm(run, executionLeaseTerm{}); err != nil {
			t.Fatalf("recover admission failure after its reply was lost: %v", err)
		}
		requireWrites(t, writes, "failed", 1)
	})
	t.Run("staged_test_ended_at", func(t *testing.T) {
		storePreflightScript(server.runner, "precision-plan-staged-script", storage.DefaultProjectID)
		run, err := server.runner.CreateTest(CreateTestRequest{Name: "staged precision", ScriptID: "precision-plan-staged-script", VUsers: 1, Duration: "1m", MaterializationKey: "precision-plan-stage"})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			server.runner.mu.Lock()
			delete(server.runner.tests, run.ID)
			server.runner.mu.Unlock()
		})
		if err := server.runner.endExecutionPlanStagedTestWithTerm(run.ID, "ABORTED", "plan stopped", executionLeaseTerm{}, run.MaterializationKey); err != nil {
			t.Fatal(err)
		}
		server.runner.mu.RLock()
		run = cloneManagedTestForReadLocked(server.runner.tests[run.ID])
		server.runner.mu.RUnlock()
		stored, err := store.GetTest(run.ID)
		if err != nil || stored == nil || stored.EndTime == nil || run.EndedAt == nil {
			t.Fatalf("read terminal staged test = %+v, %v", stored, err)
		}
		requireAPIStoredTimestamp(t, "staged run ended_at", *run.EndedAt, *stored.EndTime)
		if run.EndedAt.Location() != time.UTC {
			t.Fatal("staged run ended_at changed its UTC zone")
		}
	})
}
