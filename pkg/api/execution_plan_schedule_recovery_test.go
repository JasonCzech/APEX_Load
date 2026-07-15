package api

import (
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func newExecutionPlanScheduleRecoveryFixture(t *testing.T, runMode string) (*storage.SQLiteStore, *storage.ExecutionPlan, *storage.ExecutionPlanSchedule, time.Time) {
	t.Helper()
	store, err := storage.NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, time.July, 14, 12, 0, 0, 0, time.UTC)
	plannedAt := now.Add(-time.Minute)
	plan := &storage.ExecutionPlan{
		ID: "plan-occurrence-recovery", ProjectID: "project-a", Name: "Recovery", RunMode: runMode,
		CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
	}
	if err := store.SaveExecutionPlan(plan); err != nil {
		t.Fatalf("SaveExecutionPlan: %v", err)
	}
	schedule := &storage.ExecutionPlanSchedule{
		ID: "schedule-occurrence-recovery", PlanID: plan.ID, ProjectID: plan.ProjectID, Name: "Recovery",
		CronExpr: "*/5 * * * *", Timezone: "UTC", Enabled: true, NextRunAt: &plannedAt,
		CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
	}
	if err := store.SaveExecutionPlanSchedule(schedule); err != nil {
		t.Fatalf("SaveExecutionPlanSchedule: %v", err)
	}
	return store, plan, schedule, now
}

func saveMaterializingExecutionPlanOccurrence(t *testing.T, store *storage.SQLiteStore, schedule *storage.ExecutionPlanSchedule, runID string, now time.Time) *storage.ExecutionPlanScheduleOccurrence {
	t.Helper()
	occurrence := &storage.ExecutionPlanScheduleOccurrence{
		ID: "occurrence-recovery", ScheduleID: schedule.ID, PlanID: schedule.PlanID, ProjectID: schedule.ProjectID,
		ScheduleRevision: schedule.Revision, PlannedAt: schedule.NextRunAt.UTC(), Status: "materializing",
		ReasonCode: "due", RunID: runID, CreatedAt: now.Add(-time.Minute), UpdatedAt: now.Add(-time.Minute),
	}
	inserted, err := store.CreateExecutionPlanScheduleOccurrenceIfAbsent(occurrence)
	if err != nil {
		t.Fatalf("CreateExecutionPlanScheduleOccurrenceIfAbsent: %v", err)
	}
	if !inserted {
		t.Fatal("materializing occurrence was not inserted")
	}
	return occurrence
}

func TestExecutionPlanScheduleRecoveryResumesClaimBeforeRunAnchor(t *testing.T) {
	store, _, schedule, now := newExecutionPlanScheduleRecoveryFixture(t, "parallel")
	runID := "exec-plan-run-preassigned"
	claimedOccurrence := saveMaterializingExecutionPlanOccurrence(t, store, schedule, runID, now)

	// A fresh runner models controller restart/leadership acquisition after the
	// occurrence claim committed but before the plan-run anchor was written.
	runner := NewTestRunner(1, NewHub(), store)
	if err := runner.runDueExecutionPlanScheduleOnce(schedule, now); err != nil {
		t.Fatalf("runDueExecutionPlanScheduleOnce: %v", err)
	}
	run, err := store.GetExecutionPlanRun(runID)
	if err != nil {
		t.Fatalf("GetExecutionPlanRun: %v", err)
	}
	if run.ID != runID || run.Status != "completed" {
		t.Fatalf("recovered run = %+v, want exact preassigned id and completed status", run)
	}
	occurrence, err := store.GetExecutionPlanScheduleOccurrenceByScheduleAndPlannedAt(schedule.ID, claimedOccurrence.PlannedAt)
	if err != nil {
		t.Fatalf("GetExecutionPlanScheduleOccurrenceByScheduleAndPlannedAt: %v", err)
	}
	if occurrence.Status != "materialized" || occurrence.RunID != runID || occurrence.MaterializedAt == nil {
		t.Fatalf("recovered occurrence = %+v", occurrence)
	}
	persistedSchedule, err := store.GetExecutionPlanSchedule(schedule.ID)
	if err != nil {
		t.Fatalf("GetExecutionPlanSchedule: %v", err)
	}
	if persistedSchedule.LastRunRunID != runID || persistedSchedule.NextRunAt == nil || !persistedSchedule.NextRunAt.After(occurrence.PlannedAt) {
		t.Fatalf("recovered schedule = %+v", persistedSchedule)
	}
}

func TestExecutionPlanScheduleRecoveryFinalizesPersistedRunWithoutRematerializing(t *testing.T) {
	store, plan, schedule, now := newExecutionPlanScheduleRecoveryFixture(t, "parallel")
	runID := "exec-plan-run-already-persisted"
	occurrence := saveMaterializingExecutionPlanOccurrence(t, store, schedule, runID, now)
	completedAt := now.Add(-30 * time.Second)
	wantEntry := storage.ExecutionPlanRunEntry{Order: 7, ScenarioID: "scenario-original", TestID: "test-original", Status: "created"}
	if err := store.SaveExecutionPlanRun(&storage.ExecutionPlanRun{
		ID: runID, PlanID: plan.ID, ProjectID: plan.ProjectID, RunMode: "parallel", Status: "completed",
		Entries: []storage.ExecutionPlanRunEntry{wantEntry}, CreatedAt: now.Add(-time.Minute), UpdatedAt: completedAt, CompletedAt: &completedAt,
	}); err != nil {
		t.Fatalf("SaveExecutionPlanRun: %v", err)
	}

	runner := NewTestRunner(1, NewHub(), store)
	if err := runner.runDueExecutionPlanScheduleOnce(schedule, now); err != nil {
		t.Fatalf("runDueExecutionPlanScheduleOnce: %v", err)
	}
	run, err := store.GetExecutionPlanRun(runID)
	if err != nil {
		t.Fatalf("GetExecutionPlanRun: %v", err)
	}
	if len(run.Entries) != 1 || run.Entries[0].Order != wantEntry.Order || run.Entries[0].ScenarioID != wantEntry.ScenarioID ||
		run.Entries[0].TestID != wantEntry.TestID || run.Entries[0].Status != wantEntry.Status {
		t.Fatalf("persisted run was rematerialized: %+v", run)
	}
	recovered, err := store.GetExecutionPlanScheduleOccurrenceByScheduleAndPlannedAt(schedule.ID, occurrence.PlannedAt)
	if err != nil {
		t.Fatalf("GetExecutionPlanScheduleOccurrenceByScheduleAndPlannedAt: %v", err)
	}
	if recovered.Status != "materialized" || recovered.ReasonCode != "recovered" || recovered.RunID != runID {
		t.Fatalf("recovered occurrence = %+v", recovered)
	}
}

func TestExecutionPlanScheduleRecoveryFailsClosedForInterruptedParallelAnchor(t *testing.T) {
	store, plan, schedule, now := newExecutionPlanScheduleRecoveryFixture(t, "parallel")
	runID := "exec-plan-run-interrupted-parallel"
	occurrence := saveMaterializingExecutionPlanOccurrence(t, store, schedule, runID, now)
	if err := store.SaveExecutionPlanRun(&storage.ExecutionPlanRun{
		ID: runID, PlanID: plan.ID, ProjectID: plan.ProjectID, RunMode: "parallel", Status: "running",
		Entries:   []storage.ExecutionPlanRunEntry{{Order: 0, ScenarioID: "scenario-a", Status: "pending"}},
		CreatedAt: now.Add(-time.Minute), UpdatedAt: now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("SaveExecutionPlanRun: %v", err)
	}

	runner := NewTestRunner(1, NewHub(), store)
	if err := runner.runDueExecutionPlanScheduleOnce(schedule, now); err != nil {
		t.Fatalf("runDueExecutionPlanScheduleOnce: %v", err)
	}
	run, err := store.GetExecutionPlanRun(runID)
	if err != nil {
		t.Fatalf("GetExecutionPlanRun: %v", err)
	}
	if run.Status != "failed" || run.CompletedAt == nil || run.Entries[0].Status != "failed" {
		t.Fatalf("interrupted parallel run was not terminalized: %+v", run)
	}
	recovered, err := store.GetExecutionPlanScheduleOccurrenceByScheduleAndPlannedAt(schedule.ID, occurrence.PlannedAt)
	if err != nil {
		t.Fatalf("GetExecutionPlanScheduleOccurrenceByScheduleAndPlannedAt: %v", err)
	}
	if recovered.Status != "failed" || recovered.ReasonCode != "materialization_interrupted" || recovered.RunID != runID {
		t.Fatalf("interrupted occurrence = %+v", recovered)
	}
	runner.mu.RLock()
	testCount := len(runner.tests)
	runner.mu.RUnlock()
	if testCount != 0 {
		t.Fatalf("interrupted parallel recovery created %d duplicate tests", testCount)
	}
}

func TestExecutionPlanScheduleRecoveryReconstructsAndStartsCompleteParallelSnapshot(t *testing.T) {
	store, plan, schedule, now := newExecutionPlanScheduleRecoveryFixture(t, "parallel")
	if err := store.SaveProject(&storage.Project{ID: plan.ProjectID, Name: "Recovery", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	runner := NewTestRunner(2, NewHub(), store)
	backend := &executionBackendSpy{called: make(chan string, 1)}
	runner.setExecutionBackend(backend)
	if _, err := runner.StoreScriptDurable(&dsl.Script{
		ID: "script-recovered-parallel", ProjectID: plan.ProjectID, Name: "Recovered", Protocol: "http", Version: "1.0.0",
		Config:  dsl.ScriptConfig{BaseURL: "https://example.test", RunLogic: dsl.RunLogicConfig{Iterations: 1}},
		Actions: []dsl.Action{{Name: "Home", Type: "GET", Target: "/"}},
	}); err != nil {
		t.Fatalf("StoreScriptDurable: %v", err)
	}
	scenario, err := runner.StoreScenario(CreateScenarioRequest{
		Name: "Recovered", Duration: "30s",
		Groups: []CreateGroupEntry{{Name: "G", ScriptID: "script-recovered-parallel", VUsers: 1, RampUp: "1s"}},
	})
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}
	plan.Entries = []storage.ExecutionPlanEntry{{Order: 0, ScenarioID: scenario.ID}}
	if err := store.SaveExecutionPlan(plan); err != nil {
		t.Fatalf("SaveExecutionPlan(entries): %v", err)
	}
	runID := "exec-plan-run-reconstructable-parallel"
	testID := executionPlanEntryTestID(runID, 0, 0)
	runEntry := executionPlanRunEntrySnapshot(plan.Entries[0], plan.Name, "", "", nil, nil)
	runEntry.TestID = testID
	runEntry.MaterializationKey = executionPlanEntryMaterializationKey(runID, 0, 0)
	runEntry.Status = "pending"
	occurrence := saveMaterializingExecutionPlanOccurrence(t, store, schedule, runID, now)
	if err := store.SaveExecutionPlanRun(&storage.ExecutionPlanRun{
		ID: runID, PlanID: plan.ID, ProjectID: plan.ProjectID, RunMode: "parallel", Status: "running",
		Entries: []storage.ExecutionPlanRunEntry{runEntry}, CreatedAt: now.Add(-time.Minute), UpdatedAt: now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("SaveExecutionPlanRun: %v", err)
	}

	if err := runner.runDueExecutionPlanScheduleOnce(schedule, now); err != nil {
		t.Fatalf("runDueExecutionPlanScheduleOnce: %v", err)
	}
	startedID, started := waitForStart(t, backend, time.Second)
	if !started || startedID != testID {
		t.Fatalf("recovered backend start = (%q, %v), want %q", startedID, started, testID)
	}
	recoveredRun, err := store.GetExecutionPlanRun(runID)
	if err != nil {
		t.Fatalf("GetExecutionPlanRun: %v", err)
	}
	if recoveredRun.Status != "completed" || recoveredRun.Entries[0].Status != "created" {
		t.Fatalf("recovered run = %+v, want completed reconstructed entry", recoveredRun)
	}
	recoveredOccurrence, err := store.GetExecutionPlanScheduleOccurrenceByScheduleAndPlannedAt(schedule.ID, occurrence.PlannedAt)
	if err != nil {
		t.Fatalf("GetExecutionPlanScheduleOccurrenceByScheduleAndPlannedAt: %v", err)
	}
	if recoveredOccurrence.Status != "materialized" || recoveredOccurrence.ReasonCode != "recovered" {
		t.Fatalf("recovered occurrence = %+v, want materialized/recovered", recoveredOccurrence)
	}
}

func TestExecutionPlanScheduleRecoveryBlocksLegacyUncorrelatedOccurrence(t *testing.T) {
	store, _, schedule, now := newExecutionPlanScheduleRecoveryFixture(t, "parallel")
	occurrence := saveMaterializingExecutionPlanOccurrence(t, store, schedule, "", now)

	runner := NewTestRunner(1, NewHub(), store)
	if err := runner.runDueExecutionPlanScheduleOnce(schedule, now); err != nil {
		t.Fatalf("runDueExecutionPlanScheduleOnce: %v", err)
	}
	recovered, err := store.GetExecutionPlanScheduleOccurrenceByScheduleAndPlannedAt(schedule.ID, occurrence.PlannedAt)
	if err != nil {
		t.Fatalf("GetExecutionPlanScheduleOccurrenceByScheduleAndPlannedAt: %v", err)
	}
	if recovered.Status != "blocked" || recovered.ReasonCode != "recovery_correlation_missing" || recovered.RunID != "" {
		t.Fatalf("legacy occurrence = %+v", recovered)
	}
	persistedSchedule, err := store.GetExecutionPlanSchedule(schedule.ID)
	if err != nil {
		t.Fatalf("GetExecutionPlanSchedule: %v", err)
	}
	if persistedSchedule.NextRunAt == nil || !persistedSchedule.NextRunAt.After(occurrence.PlannedAt) {
		t.Fatalf("legacy occurrence left schedule wedged: %+v", persistedSchedule)
	}
}

func TestExecutionPlanScheduleOutcomeMergePreservesConcurrentPUTAndFailure(t *testing.T) {
	store, _, original, now := newExecutionPlanScheduleRecoveryFixture(t, "parallel")
	plannedAt := original.NextRunAt.UTC()

	// Model a concurrent operator PUT that changes user-owned recurrence fields
	// and advances next_run_at before occurrence finalization links its outcome.
	latest, err := store.GetExecutionPlanSchedule(original.ID)
	if err != nil {
		t.Fatalf("GetExecutionPlanSchedule: %v", err)
	}
	latest.CronExpr = "0 * * * *"
	latest.Timezone = "America/New_York"
	latest.MissedRunPolicy = "catch_up_once"
	advanced := plannedAt.Add(2 * time.Hour)
	latest.NextRunAt = &advanced
	latest.UpdatedAt = now.Add(-time.Second)
	if err := store.SaveExecutionPlanSchedule(latest); err != nil {
		t.Fatalf("SaveExecutionPlanSchedule(concurrent PUT): %v", err)
	}
	latest, err = store.GetExecutionPlanSchedule(original.ID)
	if err != nil {
		t.Fatalf("reload latest schedule: %v", err)
	}
	latestRevision := latest.Revision

	runner := NewTestRunner(1, NewHub(), store)
	term, err := runner.captureExecutionLeaseTerm()
	if err != nil {
		t.Fatalf("captureExecutionLeaseTerm: %v", err)
	}
	occurrence := &storage.ExecutionPlanScheduleOccurrence{
		ID: "occurrence-failed-after-put", ScheduleID: latest.ID, PlanID: latest.PlanID,
		ProjectID: latest.ProjectID, PlannedAt: plannedAt, Status: "failed",
		ReasonCode: "materialization_interrupted", RunID: "run-failed-after-put",
		ConflictSummary: "materialization failed after durable claim",
	}
	if err := runner.advanceExecutionPlanScheduleAfterOccurrence(latest, occurrence, plannedAt, now, occurrence.ConflictSummary, term); err != nil {
		t.Fatalf("advanceExecutionPlanScheduleAfterOccurrence: %v", err)
	}

	persisted, err := store.GetExecutionPlanSchedule(original.ID)
	if err != nil {
		t.Fatalf("GetExecutionPlanSchedule(persisted): %v", err)
	}
	if persisted.CronExpr != "0 * * * *" || persisted.Timezone != "America/New_York" || persisted.MissedRunPolicy != "catch_up_once" || persisted.NextRunAt == nil || !persisted.NextRunAt.Equal(advanced) {
		t.Fatalf("concurrent PUT fields regressed: %+v", persisted)
	}
	if persisted.Revision != latestRevision+1 {
		t.Fatalf("revision = %d, want %d", persisted.Revision, latestRevision+1)
	}
	if persisted.LastRunRunID != occurrence.RunID || persisted.LastError != occurrence.ConflictSummary || persisted.NextRequiredAction != "operator_decision_required" {
		t.Fatalf("occurrence outcome was not merged: %+v", persisted)
	}
}

func TestExecutionPlanSchedulePausedAdvanceUsesOccurrenceOutcome(t *testing.T) {
	for _, tc := range []struct {
		name             string
		status           string
		conflictSummary  string
		wantLastError    string
		materializedTime bool
	}{
		{name: "materialized clears stale error", status: "materialized", materializedTime: true},
		{name: "failed replaces stale error", status: "failed", conflictSummary: "materialization interrupted", wantLastError: "materialization interrupted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, _, schedule, now := newExecutionPlanScheduleRecoveryFixture(t, "parallel")
			plannedAt := schedule.NextRunAt.UTC()
			disabledFrom := now.Add(-time.Minute)
			schedule.DisabledFrom = &disabledFrom
			schedule.DisabledUntil = nil
			schedule.LastError = "stale transient scheduler failure"
			if err := store.SaveExecutionPlanSchedule(schedule); err != nil {
				t.Fatalf("SaveExecutionPlanSchedule: %v", err)
			}
			schedule, err := store.GetExecutionPlanSchedule(schedule.ID)
			if err != nil {
				t.Fatalf("GetExecutionPlanSchedule: %v", err)
			}
			occurrence := &storage.ExecutionPlanScheduleOccurrence{
				ID: "paused-outcome-" + tc.status, ScheduleID: schedule.ID, PlanID: schedule.PlanID,
				ProjectID: schedule.ProjectID, PlannedAt: plannedAt, Status: tc.status,
				RunID: "paused-run-" + tc.status, ConflictSummary: tc.conflictSummary,
			}
			if tc.materializedTime {
				materializedAt := now.Add(-30 * time.Second)
				occurrence.MaterializedAt = &materializedAt
			}
			runner := NewTestRunner(1, NewHub(), store)
			term, err := runner.captureExecutionLeaseTerm()
			if err != nil {
				t.Fatalf("captureExecutionLeaseTerm: %v", err)
			}
			if err := runner.advanceExecutionPlanScheduleAfterOccurrence(schedule, occurrence, plannedAt, now, schedule.LastError, term); err != nil {
				t.Fatalf("advanceExecutionPlanScheduleAfterOccurrence: %v", err)
			}
			persisted, err := store.GetExecutionPlanSchedule(schedule.ID)
			if err != nil {
				t.Fatalf("GetExecutionPlanSchedule(persisted): %v", err)
			}
			if persisted.NextRunAt != nil || persisted.NextRequiredAction != "schedule_paused" || persisted.LastError != tc.wantLastError {
				t.Fatalf("paused schedule outcome = %+v, want last_error=%q and schedule_paused", persisted, tc.wantLastError)
			}
		})
	}
}

func TestExecutionPlanScheduleCatchUpAdvanceUsesPolicyAnchor(t *testing.T) {
	for _, tc := range []struct {
		policy string
		want   time.Time
	}{
		{policy: "catch_up_all", want: time.Date(2026, time.July, 14, 11, 45, 0, 0, time.UTC)},
		{policy: "catch_up_once", want: time.Date(2026, time.July, 14, 12, 5, 0, 0, time.UTC)},
	} {
		t.Run(tc.policy, func(t *testing.T) {
			store, _, schedule, now := newExecutionPlanScheduleRecoveryFixture(t, "parallel")
			plannedAt := now.Add(-20 * time.Minute)
			schedule.NextRunAt = &plannedAt
			schedule.MissedRunPolicy = tc.policy
			schedule.LastError = "stale transient error"
			if err := store.SaveExecutionPlanSchedule(schedule); err != nil {
				t.Fatalf("SaveExecutionPlanSchedule: %v", err)
			}
			schedule, err := store.GetExecutionPlanSchedule(schedule.ID)
			if err != nil {
				t.Fatalf("GetExecutionPlanSchedule: %v", err)
			}
			materializedAt := now.Add(-time.Minute)
			occurrence := &storage.ExecutionPlanScheduleOccurrence{
				ID: "occurrence-" + tc.policy, ScheduleID: schedule.ID, PlanID: schedule.PlanID,
				ProjectID: schedule.ProjectID, PlannedAt: plannedAt, Status: "materialized",
				ReasonCode: "recovered", RunID: "run-" + tc.policy, MaterializedAt: &materializedAt,
			}
			runner := NewTestRunner(1, NewHub(), store)
			term, err := runner.captureExecutionLeaseTerm()
			if err != nil {
				t.Fatalf("captureExecutionLeaseTerm: %v", err)
			}
			if err := runner.advanceExecutionPlanScheduleAfterOccurrence(schedule, occurrence, plannedAt, now, schedule.LastError, term); err != nil {
				t.Fatalf("advanceExecutionPlanScheduleAfterOccurrence: %v", err)
			}
			persisted, err := store.GetExecutionPlanSchedule(schedule.ID)
			if err != nil {
				t.Fatalf("GetExecutionPlanSchedule(persisted): %v", err)
			}
			if persisted.NextRunAt == nil || !persisted.NextRunAt.Equal(tc.want) {
				t.Fatalf("next_run_at = %v, want %v", persisted.NextRunAt, tc.want)
			}
			if persisted.LastError != "" || persisted.NextRequiredAction != "" {
				t.Fatalf("successful occurrence retained stale outcome: %+v", persisted)
			}
		})
	}
}

func TestExecutionPlanScheduleRecoveryReusesApprovalClaimedByPreassignedRun(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	if err := store.SaveProject(&storage.Project{
		ID: "project-approval-recovery", Name: "Recovery", Settings: storage.ProjectSettings{MaxVUsers: 1}, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	runner := NewTestRunner(1, NewHub(), store)
	runner.StoreScript(&dsl.Script{
		ID: "script-approval-recovery", ProjectID: "project-approval-recovery", Name: "Recovery", Protocol: "http", Version: "1.0.0",
		Config: dsl.ScriptConfig{BaseURL: "https://example.com"}, Actions: []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})
	scenario, err := runner.StoreScenario(CreateScenarioRequest{
		Name: "Recovery", Duration: "1m", Groups: []CreateGroupEntry{{ScriptID: "script-approval-recovery", VUsers: 2}},
	})
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}
	plan := &storage.ExecutionPlan{
		ID: "plan-approval-recovery", ProjectID: "project-approval-recovery", Name: "Recovery", RunMode: "parallel",
		Entries: []storage.ExecutionPlanEntry{{Order: 0, ScenarioID: scenario.ID}}, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.SaveExecutionPlan(plan); err != nil {
		t.Fatalf("SaveExecutionPlan: %v", err)
	}
	runID := "exec-plan-run-approval-recovery"
	approval := &storage.ExecutionPlanApproval{
		ID: "approval-recovery", PlanID: plan.ID, ProjectID: plan.ProjectID, Policy: "quota_burst", Status: "approved",
		RequestedAt: now, PlanContentHash: runner.executionPlanApprovalContentHash(plan), RunID: runID, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.SaveExecutionPlanApproval(approval); err != nil {
		t.Fatalf("SaveExecutionPlanApproval: %v", err)
	}

	got, blocked, err := runner.executionPlanRunGate(plan, "", runID)
	if err != nil {
		t.Fatalf("executionPlanRunGate: %v", err)
	}
	if blocked || got == nil || got.ID != approval.ID || got.RunID != runID {
		t.Fatalf("recovery gate = (approval=%+v, blocked=%v), want same claimed approval", got, blocked)
	}
	other, otherBlocked, err := runner.executionPlanRunGate(plan, "", "exec-plan-run-other")
	if err != nil {
		t.Fatalf("executionPlanRunGate(other): %v", err)
	}
	if !otherBlocked || other == nil || other.ID == approval.ID {
		t.Fatalf("other run reused single-use approval: approval=%+v blocked=%v", other, otherBlocked)
	}
}
