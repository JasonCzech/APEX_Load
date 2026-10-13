package api

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

type scenarioScheduleApprovalFixture struct {
	store     *storage.SQLiteStore
	runner    *TestRunner
	schedule  *storage.ScenarioSchedule
	approval  *storage.ScenarioScheduleApproval
	scenario  *StoredScenario
	backend   *scheduleExecutionBackend
	scheduler *scenarioScheduler
	now       time.Time
}

func newScenarioScheduleApprovalFixture(t *testing.T) *scenarioScheduleApprovalFixture {
	t.Helper()
	store, err := storage.NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	runner := NewTestRunner(2, NewHub(), store)
	backend := &scheduleExecutionBackend{started: make(chan string, 4)}
	runner.setExecutionBackend(backend)
	runner.StoreScript(&dsl.Script{
		ID: "script-scenario-schedule-approval", Name: "Approval", Protocol: "http", Version: "1.0.0",
		Config: dsl.ScriptConfig{BaseURL: "https://example.com"}, Actions: []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})
	scenario, err := runner.StoreScenario(CreateScenarioRequest{
		Name: "Approval scenario", Duration: "1m", Groups: []CreateGroupEntry{{ScriptID: "script-scenario-schedule-approval", VUsers: 1}},
	})
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}
	now := time.Date(2026, time.July, 19, 12, 0, 30, 0, time.UTC)
	plannedAt := now.Truncate(time.Minute)
	schedule := &storage.ScenarioSchedule{
		ID: "scenario-schedule-approval", ScenarioID: scenario.ID, ProjectID: scenario.ProjectID, Revision: 1,
		Name: "Approval schedule", CronExpr: "*/5 * * * *", Timezone: "UTC", Enabled: true,
		ApprovalPolicy: "manual", MissedRunPolicy: "catch_up_once", NextRequiredAction: "approval_required",
		PendingApprovalID: "scenario-approval-a", NextRunAt: &plannedAt, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
	}
	approval := &storage.ScenarioScheduleApproval{
		ID: schedule.PendingApprovalID, ScheduleID: schedule.ID, ScenarioID: scenario.ID, ProjectID: scenario.ProjectID,
		ScheduleRevision: 1, Policy: "manual", Status: "pending", RequestedAt: now.Add(-time.Hour),
		CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
	}
	if changed, err := store.SaveScenarioScheduleWithApproval(schedule, approval, 0); err != nil || !changed {
		t.Fatalf("SaveScenarioScheduleWithApproval = %v, %v", changed, err)
	}
	runner.mu.Lock()
	runner.scenarios[scenario.ID].Schedules = append(runner.scenarios[scenario.ID].Schedules, cloneScenarioSchedule(schedule))
	runner.mu.Unlock()
	scheduler := newScenarioScheduler(runner)
	scheduler.now = func() time.Time { return now }
	return &scenarioScheduleApprovalFixture{store: store, runner: runner, schedule: schedule, approval: approval, scenario: scenario, backend: backend, scheduler: scheduler, now: now}
}

func (fixture *scenarioScheduleApprovalFixture) sweep(t *testing.T) *storage.ScenarioScheduleOccurrence {
	t.Helper()
	if err := fixture.scheduler.RunDueSchedules(); err != nil {
		t.Fatalf("RunDueSchedules: %v", err)
	}
	occurrences, err := fixture.store.ListScenarioScheduleOccurrences(fixture.schedule.ID)
	if err != nil || len(occurrences) != 1 {
		t.Fatalf("ListScenarioScheduleOccurrences = %#v, %v", occurrences, err)
	}
	fixture.schedule, err = fixture.store.GetScenarioSchedule(fixture.schedule.ID)
	if err != nil {
		t.Fatalf("GetScenarioSchedule: %v", err)
	}
	return occurrences[0]
}

func TestScenarioApprovalOccurrenceCannotUseGenericResolution(t *testing.T) {
	fixture := newScenarioScheduleApprovalFixture(t)
	occurrence := fixture.sweep(t)
	if occurrence.Status != "blocked" || occurrence.ReasonCode != "approval_required" {
		t.Fatalf("blocked occurrence = %+v", occurrence)
	}
	_, _, found, err := fixture.runner.ResolveScenarioScheduleOccurrence(
		fixture.scenario.ID, fixture.schedule.ID, occurrence.ID, ScheduleOccurrenceResolutionRequest{Action: "retry"},
	)
	if !found || !errors.Is(err, errScheduleOccurrenceNotResolvable) {
		t.Fatalf("generic resolution = found %v err %v, want approval-workflow conflict", found, err)
	}
	persisted, err := fixture.store.GetScenarioScheduleOccurrence(occurrence.ID)
	if err != nil || persisted.Status != "blocked" || persisted.ApprovalID != fixture.approval.ID {
		t.Fatalf("persisted occurrence after rejected bypass = %+v, %v", persisted, err)
	}
}

func TestScenarioApprovalOccurrenceReconcilesApproveRejectSupersedeAndRemoval(t *testing.T) {
	t.Run("approve resumes the blocked occurrence", func(t *testing.T) {
		fixture := newScenarioScheduleApprovalFixture(t)
		blocked := fixture.sweep(t)
		if _, _, found, err := fixture.runner.ResolveScenarioScheduleApproval(fixture.scenario.ID, fixture.schedule.ID, fixture.approval.ID, true, "reviewer", "approved"); err != nil || !found {
			t.Fatalf("ResolveScenarioScheduleApproval = %v, found=%v", err, found)
		}
		materialized := fixture.sweep(t)
		if materialized.ID != blocked.ID || materialized.Status != "materialized" || materialized.TestID == "" {
			t.Fatalf("approved occurrence = %+v, want same materialized anchor", materialized)
		}
		select {
		case started := <-fixture.backend.started:
			if started != materialized.TestID {
				t.Fatalf("started test = %q, want %q", started, materialized.TestID)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("approved scheduled test was not started")
		}
	})

	t.Run("reject remains blocked without reattaching approval", func(t *testing.T) {
		fixture := newScenarioScheduleApprovalFixture(t)
		blocked := fixture.sweep(t)
		if _, _, found, err := fixture.runner.ResolveScenarioScheduleApproval(fixture.scenario.ID, fixture.schedule.ID, fixture.approval.ID, false, "reviewer", "rejected"); err != nil || !found {
			t.Fatalf("ResolveScenarioScheduleApproval = %v, found=%v", err, found)
		}
		rejected := fixture.sweep(t)
		if rejected.ID != blocked.ID || rejected.Status != "blocked" || rejected.ReasonCode != "approval_rejected" {
			t.Fatalf("rejected occurrence = %+v", rejected)
		}
		persistedSchedule, err := fixture.store.GetScenarioSchedule(fixture.schedule.ID)
		if err != nil || persistedSchedule.PendingApprovalID != "" || persistedSchedule.NextRequiredAction != "approval_rejected" {
			t.Fatalf("rejected schedule = %+v, %v", persistedSchedule, err)
		}
		select {
		case started := <-fixture.backend.started:
			t.Fatalf("rejected approval started test %q", started)
		default:
		}
	})

	t.Run("new revision retargets then policy removal resumes", func(t *testing.T) {
		fixture := newScenarioScheduleApprovalFixture(t)
		blocked := fixture.sweep(t)
		enabled := true
		updated, found, err := fixture.runner.UpdateScenarioSchedule(fixture.scenario.ID, fixture.schedule.ID, CreateScenarioScheduleRequest{
			Name: fixture.schedule.Name + " v2", CronExpr: fixture.schedule.CronExpr, Timezone: fixture.schedule.Timezone,
			Enabled: &enabled, ApprovalPolicy: "manual", MissedRunPolicy: fixture.schedule.MissedRunPolicy,
			ExpectedRevision: &fixture.schedule.Revision,
		})
		if err != nil || !found || updated.PendingApprovalID == "" || updated.PendingApprovalID == fixture.approval.ID {
			t.Fatalf("manual revision update = %+v, found=%v err=%v", updated, found, err)
		}
		fixture.schedule = updated
		retargeted := fixture.sweep(t)
		if retargeted.ID != blocked.ID || retargeted.Status != "blocked" || retargeted.ApprovalID != updated.PendingApprovalID {
			t.Fatalf("retargeted occurrence = %+v, want approval %s", retargeted, updated.PendingApprovalID)
		}
		oldApproval, err := fixture.store.GetScenarioScheduleApproval(fixture.approval.ID)
		if err != nil || oldApproval.Status != "superseded" {
			t.Fatalf("old approval = %+v, %v", oldApproval, err)
		}
		revision := fixture.schedule.Revision
		withoutApproval, found, err := fixture.runner.UpdateScenarioSchedule(fixture.scenario.ID, fixture.schedule.ID, CreateScenarioScheduleRequest{
			Name: updated.Name, CronExpr: updated.CronExpr, Timezone: updated.Timezone, Enabled: &enabled,
			ApprovalPolicy: "auto", MissedRunPolicy: updated.MissedRunPolicy, ExpectedRevision: &revision,
		})
		if err != nil || !found || withoutApproval.PendingApprovalID != "" || withoutApproval.NextRequiredAction == "approval_required" {
			t.Fatalf("approval removal = %+v, found=%v err=%v", withoutApproval, found, err)
		}
		fixture.schedule = withoutApproval
		materialized := fixture.sweep(t)
		if materialized.ID != blocked.ID || materialized.Status != "materialized" || materialized.TestID == "" {
			t.Fatalf("occurrence after approval removal = %+v", materialized)
		}
	})
}

func TestScenarioOccurrenceRetryUsesNewDeterministicAttemptTestID(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	runner := NewTestRunner(2, NewHub(), store)
	backend := &scheduleExecutionBackend{started: make(chan string, 2)}
	runner.setExecutionBackend(backend)
	runner.StoreScript(&dsl.Script{
		ID: "script-retry-attempt", Name: "Retry", Protocol: "http", Version: "1.0.0",
		Config: dsl.ScriptConfig{BaseURL: "https://example.com"}, Actions: []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})
	scenario, err := runner.StoreScenario(CreateScenarioRequest{Name: "Retry", Duration: "1m", Groups: []CreateGroupEntry{{ScriptID: "script-retry-attempt", VUsers: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.July, 19, 12, 0, 30, 0, time.UTC)
	plannedAt := now.Truncate(time.Minute)
	schedule := &storage.ScenarioSchedule{
		ID: "scenario-retry-attempt", ScenarioID: scenario.ID, ProjectID: scenario.ProjectID, Revision: 1,
		Name: "Retry", CronExpr: "*/5 * * * *", Timezone: "UTC", Enabled: true, MissedRunPolicy: "catch_up_once",
		NextRequiredAction: "operator_decision_required", NextRunAt: &plannedAt, CreatedAt: now.Add(-time.Hour), UpdatedAt: now,
	}
	if err := store.SaveScenarioSchedule(schedule); err != nil {
		t.Fatal(err)
	}
	oldTestRequest := CreateTestRequest{Name: "Old failed attempt", ScenarioID: scenario.ID}
	oldTestRequest.projectQuotaEvidence = runner.loadCreateTestProjectQuotaEvidence(oldTestRequest)
	runner.mu.Lock()
	runner.scenarios[scenario.ID].Schedules = append(runner.scenarios[scenario.ID].Schedules, cloneScenarioSchedule(schedule))
	oldID := scenarioScheduleOccurrenceTestID("scenario-retry-occurrence")
	oldTest, err := runner.buildManagedTestLocked(oldTestRequest, oldID, "FAILED")
	if err != nil {
		runner.mu.Unlock()
		t.Fatal(err)
	}
	oldTest.Error = "first attempt failed"
	oldTest.EndedAt = cloneTimePtr(&now)
	runner.tests[oldID] = oldTest
	if err := runner.saveManagedTestRecord(oldTest); err != nil {
		runner.mu.Unlock()
		t.Fatal(err)
	}
	runner.mu.Unlock()
	occurrence := &storage.ScenarioScheduleOccurrence{
		ID: "scenario-retry-occurrence", ScheduleID: schedule.ID, ScenarioID: scenario.ID, ProjectID: scenario.ProjectID,
		ScheduleRevision: schedule.Revision, PlannedAt: plannedAt, Status: "failed", ReasonCode: "start_failed", TestID: oldID,
		ConflictSummary: "first attempt failed", CreatedAt: now.Add(-time.Minute), UpdatedAt: now,
	}
	if err := store.SaveScenarioScheduleOccurrence(occurrence); err != nil {
		t.Fatal(err)
	}
	resolved, _, found, err := runner.ResolveScenarioScheduleOccurrence(scenario.ID, schedule.ID, occurrence.ID, ScheduleOccurrenceResolutionRequest{Action: "retry"})
	if err != nil || !found {
		t.Fatalf("ResolveScenarioScheduleOccurrence = %+v, found=%v err=%v", resolved, found, err)
	}
	wantAttemptID := scenarioScheduleOccurrenceMaterializationTestID(resolved)
	if wantAttemptID == oldID {
		t.Fatalf("retry attempt id %q reused failed test id", wantAttemptID)
	}
	scheduler := newScenarioScheduler(runner)
	scheduler.now = func() time.Time { return now }
	if err := scheduler.RunDueSchedules(); err != nil {
		t.Fatalf("RunDueSchedules: %v", err)
	}
	persisted, err := store.GetScenarioScheduleOccurrence(occurrence.ID)
	if err != nil || persisted.Status != "materialized" || persisted.TestID != wantAttemptID {
		t.Fatalf("retried occurrence = %+v, %v; want test %q", persisted, err, wantAttemptID)
	}
	if _, ok := runner.GetTest(wantAttemptID); !ok {
		t.Fatalf("new retry test %q was not registered", wantAttemptID)
	}
}

type executionPlanScheduledApprovalFixture struct {
	store    *storage.SQLiteStore
	runner   *TestRunner
	plan     *storage.ExecutionPlan
	schedule *storage.ExecutionPlanSchedule
	now      time.Time
}

func newExecutionPlanScheduledApprovalFixture(t *testing.T) *executionPlanScheduledApprovalFixture {
	t.Helper()
	store, err := storage.NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, time.July, 19, 12, 0, 30, 0, time.UTC)
	projectID := "project-scheduled-approval"
	if err := store.SaveProject(&storage.Project{ID: projectID, Name: "Approval", Settings: storage.ProjectSettings{MaxVUsers: 1}, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	runner := NewTestRunner(2, NewHub(), store)
	runner.setExecutionBackend(&scheduleExecutionBackend{started: make(chan string, 4)})
	runner.StoreScript(&dsl.Script{
		ID: "script-execution-plan-scheduled-approval", ProjectID: projectID, Name: "Approval", Protocol: "http", Version: "1.0.0",
		Config: dsl.ScriptConfig{BaseURL: "https://example.com"}, Actions: []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})
	scenario, err := runner.StoreScenario(CreateScenarioRequest{
		Name: "Approval", Duration: "1m", Groups: []CreateGroupEntry{{ScriptID: "script-execution-plan-scheduled-approval", VUsers: 2}},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan := &storage.ExecutionPlan{
		ID: "plan-scheduled-approval", ProjectID: projectID, Name: "Approval", RunMode: "parallel",
		Entries: []storage.ExecutionPlanEntry{{Order: 0, ScenarioID: scenario.ID}}, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
	}
	if err := store.SaveExecutionPlan(plan); err != nil {
		t.Fatal(err)
	}
	plannedAt := now.Truncate(time.Minute)
	schedule := &storage.ExecutionPlanSchedule{
		ID: "execution-plan-scheduled-approval", PlanID: plan.ID, ProjectID: projectID, Revision: 1,
		Name: "Approval", CronExpr: "*/5 * * * *", Timezone: "UTC", Enabled: true, NextRunAt: &plannedAt,
		CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
	}
	if err := store.SaveExecutionPlanSchedule(schedule); err != nil {
		t.Fatal(err)
	}
	return &executionPlanScheduledApprovalFixture{store: store, runner: runner, plan: plan, schedule: schedule, now: now}
}

func (fixture *executionPlanScheduledApprovalFixture) block(t *testing.T) *storage.ExecutionPlanScheduleOccurrence {
	t.Helper()
	if err := fixture.runner.runDueExecutionPlanScheduleOnce(fixture.schedule, fixture.now); err != nil {
		t.Fatalf("runDueExecutionPlanScheduleOnce: %v", err)
	}
	occurrences, err := fixture.store.ListExecutionPlanScheduleOccurrences(fixture.schedule.ID)
	if err != nil || len(occurrences) != 1 {
		t.Fatalf("ListExecutionPlanScheduleOccurrences = %#v, %v", occurrences, err)
	}
	return occurrences[0]
}

func TestScheduledExecutionPlanApprovalResumesOrRejectsCorrelatedOccurrence(t *testing.T) {
	t.Run("approve resumes preassigned run", func(t *testing.T) {
		fixture := newExecutionPlanScheduledApprovalFixture(t)
		blocked := fixture.block(t)
		if blocked.Status != "blocked" || blocked.ReasonCode != "approval_required" || blocked.ApprovalID == "" || blocked.RunID == "" {
			t.Fatalf("blocked occurrence = %+v", blocked)
		}
		held, err := fixture.store.GetExecutionPlanSchedule(fixture.schedule.ID)
		if err != nil || held.NextRunAt == nil || !held.NextRunAt.Equal(blocked.PlannedAt) || held.NextRequiredAction != "approval_required" || held.LastRunRunID != "" {
			t.Fatalf("held schedule = %+v, %v", held, err)
		}
		if _, _, found, err := fixture.runner.ResolveExecutionPlanScheduleOccurrence(fixture.plan.ID, fixture.schedule.ID, blocked.ID, ScheduleOccurrenceResolutionRequest{Action: "retry"}); !found || !errors.Is(err, errScheduleOccurrenceNotResolvable) {
			t.Fatalf("generic approval occurrence resolution = found %v err %v", found, err)
		}
		if _, found, err := fixture.runner.ResolveExecutionPlanApproval(fixture.plan.ID, blocked.ApprovalID, true, "reviewer", "approved"); err != nil || !found {
			t.Fatalf("ResolveExecutionPlanApproval = found %v err %v", found, err)
		}
		held, err = fixture.store.GetExecutionPlanSchedule(fixture.schedule.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := fixture.runner.runDueExecutionPlanScheduleOnce(held, fixture.now); err != nil {
			t.Fatalf("approved schedule sweep: %v", err)
		}
		materialized, err := fixture.store.GetExecutionPlanScheduleOccurrenceByScheduleAndPlannedAt(fixture.schedule.ID, blocked.PlannedAt)
		if err != nil || materialized.Status != "materialized" || materialized.RunID != blocked.RunID || materialized.ApprovalID != blocked.ApprovalID {
			t.Fatalf("materialized occurrence = %+v, %v", materialized, err)
		}
		approval, err := fixture.store.GetExecutionPlanApproval(blocked.ApprovalID)
		if err != nil || approval.RunID != blocked.RunID {
			t.Fatalf("claimed approval = %+v, %v", approval, err)
		}
		advanced, err := fixture.store.GetExecutionPlanSchedule(fixture.schedule.ID)
		if err != nil || advanced.LastRunRunID != blocked.RunID || advanced.NextRunAt == nil || !advanced.NextRunAt.After(blocked.PlannedAt) {
			t.Fatalf("advanced schedule = %+v, %v", advanced, err)
		}
	})

	// A rejected occurrence says first that it was refused, then why the
	// approval was asked for and the reviewer's reason when one was given.
	for _, decision := range []string{"capacity denied", ""} {
		t.Run("reject skips occurrence and advances (reason "+strconv.Quote(decision)+")", func(t *testing.T) {
			fixture := newExecutionPlanScheduledApprovalFixture(t)
			blocked := fixture.block(t)
			if _, found, err := fixture.runner.ResolveExecutionPlanApproval(fixture.plan.ID, blocked.ApprovalID, false, "reviewer", decision); err != nil || !found {
				t.Fatalf("ResolveExecutionPlanApproval = found %v err %v", found, err)
			}
			held, err := fixture.store.GetExecutionPlanSchedule(fixture.schedule.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err := fixture.runner.runDueExecutionPlanScheduleOnce(held, fixture.now); err != nil {
				t.Fatalf("rejected schedule sweep: %v", err)
			}
			skipped, err := fixture.store.GetExecutionPlanScheduleOccurrenceByScheduleAndPlannedAt(fixture.schedule.ID, blocked.PlannedAt)
			if err != nil || skipped.Status != "skipped" || skipped.ReasonCode != "approval_rejected" || skipped.RunID != "" || skipped.ApprovalID != blocked.ApprovalID {
				t.Fatalf("rejected occurrence = %+v, %v", skipped, err)
			}
			if !strings.HasPrefix(skipped.ConflictSummary, "execution-plan approval rejected: Aggregate peak") || strings.Contains(skipped.ConflictSummary, " Decision: "+decision) != (decision != "") {
				t.Fatalf("rejected occurrence summary = %q, want it to say it was rejected, then why", skipped.ConflictSummary)
			}
			advanced, err := fixture.store.GetExecutionPlanSchedule(fixture.schedule.ID)
			if err != nil || advanced.LastRunRunID != "" || advanced.NextRequiredAction != "" || advanced.NextRunAt == nil || !advanced.NextRunAt.After(blocked.PlannedAt) {
				t.Fatalf("schedule after rejection = %+v, %v", advanced, err)
			}
		})
	}
}

func TestSchedulerSweepHealthRecordsOnlyAttemptedDomains(t *testing.T) {
	now := time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC)
	t.Run("complete sweep", func(t *testing.T) {
		scheduler := newScenarioScheduler(NewTestRunner(1, NewHub(), nil))
		scheduler.now = func() time.Time { return now }
		if err := scheduler.RunDueSchedules(); err != nil {
			t.Fatal(err)
		}
		health := scheduler.healthSnapshot()
		for _, domain := range []string{"scenario", "one_time", "execution_plan", "scheduler"} {
			state, ok := health[domain]
			if !ok || state.LastSweepAt == nil || state.LastSuccessfulAt == nil || state.LastError != "" {
				t.Errorf("%s health = %+v, present=%v", domain, state, ok)
			}
		}
	})
	t.Run("secondary lease contention is not a sweep", func(t *testing.T) {
		store := &metricsStoreSpy{leasesByName: map[string]*storage.SchedulerLease{
			"scenario-scheduler": {Name: "scenario-scheduler", OwnerID: "other", ExpiresAt: now.Add(time.Minute)},
		}}
		scheduler := newScenarioScheduler(NewTestRunner(1, NewHub(), store))
		scheduler.ownerID = "this-controller"
		scheduler.now = func() time.Time { return now }
		if err := scheduler.RunDueSchedules(); err != nil {
			t.Fatal(err)
		}
		if health := scheduler.healthSnapshot(); len(health) != 0 {
			t.Fatalf("lease-contended sweep recorded health: %+v", health)
		}
	})
}
