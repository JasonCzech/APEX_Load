package storage

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestExecutionPlanScheduleActorRoundTripsAndCASUpdates(t *testing.T) {
	store := openTimeslotStore(t)
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	if err := store.SaveExecutionPlan(&ExecutionPlan{ID: "plan-actor", ProjectID: "proj-a", Name: "Plan", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	schedule := &ExecutionPlanSchedule{
		ID: "schedule-actor", PlanID: "plan-actor", ProjectID: "proj-a", Name: "Schedule", CronExpr: "0 * * * *",
		Enabled: true, ActorType: "service_account", ActorSubject: "schedule-runner", ActorDisplayName: "Schedule Runner",
		NextRunAt: &now, CreatedAt: now, UpdatedAt: now,
	}
	created, err := store.CreateExecutionPlanScheduleIfAbsent(schedule)
	if err != nil || !created {
		t.Fatalf("CreateExecutionPlanScheduleIfAbsent = %v, %v", created, err)
	}
	got, err := store.GetExecutionPlanSchedule(schedule.ID)
	if err != nil || got.ActorType != "service_account" || got.ActorSubject != "schedule-runner" || got.ActorDisplayName != "Schedule Runner" {
		t.Fatalf("stored actor = %#v, %v", got, err)
	}
	occurrence := &ExecutionPlanScheduleOccurrence{
		ID: "occurrence-actor", ScheduleID: got.ID, PlannedAt: now, Status: "materializing", RunID: "run-actor",
		ApprovalID: "approval-actor", CreatedAt: now, UpdatedAt: now,
	}
	claimed, err := store.ClaimExecutionPlanScheduleOccurrenceIfCurrent(occurrence)
	if err != nil || !claimed {
		t.Fatalf("ClaimExecutionPlanScheduleOccurrenceIfCurrent = %v, %v", claimed, err)
	}
	if occurrence.ActorType != "service_account" || occurrence.ActorSubject != "schedule-runner" || occurrence.ActorDisplayName != "Schedule Runner" {
		t.Fatalf("claimed occurrence actor = %#v", occurrence)
	}
	got.ActorType = "user"
	got.ActorSubject = "human-editor"
	got.ActorDisplayName = "Human Editor"
	got.UpdatedAt = now.Add(time.Minute)
	updated, err := store.UpdateExecutionPlanScheduleIfRevision(got, got.Revision)
	if err != nil || !updated {
		t.Fatalf("UpdateExecutionPlanScheduleIfRevision = %v, %v", updated, err)
	}
	got, err = store.GetExecutionPlanSchedule(schedule.ID)
	if err != nil || got.ActorType != "user" || got.ActorSubject != "human-editor" || got.ActorDisplayName != "Human Editor" {
		t.Fatalf("updated actor = %#v, %v", got, err)
	}
	tamperedOccurrence := *occurrence
	tamperedOccurrence.ActorType = "user"
	tamperedOccurrence.ActorSubject = "human-editor"
	tamperedOccurrence.ActorDisplayName = "Human Editor"
	if changed, err := store.CompareAndSwapExecutionPlanScheduleOccurrence(&tamperedOccurrence, occurrence.Revision); err != nil || changed {
		t.Fatalf("tampered occurrence actor CAS = %v, %v; want false, nil", changed, err)
	}
	occurrence.Status = "failed"
	occurrence.UpdatedAt = now.Add(2 * time.Minute)
	updated, err = store.CompareAndSwapExecutionPlanScheduleOccurrence(occurrence, occurrence.Revision)
	if err != nil || !updated {
		t.Fatalf("CompareAndSwapExecutionPlanScheduleOccurrence = %v, %v", updated, err)
	}
	persistedOccurrence, err := store.GetExecutionPlanScheduleOccurrenceByScheduleAndPlannedAt(schedule.ID, now)
	if err != nil || persistedOccurrence.ActorType != "service_account" || persistedOccurrence.ActorSubject != "schedule-runner" || persistedOccurrence.ActorDisplayName != "Schedule Runner" || persistedOccurrence.ApprovalID != "approval-actor" {
		t.Fatalf("immutable occurrence actor = %#v, %v", persistedOccurrence, err)
	}
}

func TestLegacyExecutionPlanScheduleMigrationUsesFailClosedActor(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.db")
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = raw.Exec(`CREATE TABLE execution_plan_schedules (
		id TEXT PRIMARY KEY, plan_id TEXT NOT NULL, project_id TEXT, revision INTEGER DEFAULT 1,
		name TEXT NOT NULL, cron_expr TEXT NOT NULL, timezone TEXT, run_name_template TEXT,
		enabled INTEGER DEFAULT 1, missed_run_policy TEXT, conflict_summary TEXT, next_required_action TEXT,
		last_run_run_id TEXT, last_run_at DATETIME, next_run_at DATETIME, disabled_from DATETIME,
		disabled_until DATETIME, last_error TEXT, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL
	)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = raw.Exec(`CREATE TABLE execution_plan_schedule_occurrences (
		id TEXT PRIMARY KEY, schedule_id TEXT NOT NULL, plan_id TEXT NOT NULL, project_id TEXT,
		schedule_revision INTEGER DEFAULT 1, revision INTEGER NOT NULL DEFAULT 1, transition_token TEXT NOT NULL DEFAULT '',
		planned_at DATETIME NOT NULL, materialized_at DATETIME, status TEXT NOT NULL, reason_code TEXT,
		run_id TEXT, conflict_summary TEXT, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL,
		UNIQUE(schedule_id, planned_at)
	)`)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := raw.Exec(`INSERT INTO execution_plan_schedules (id, plan_id, project_id, name, cron_expr, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"legacy-schedule", "legacy-plan", DefaultProjectID, "Legacy", "0 * * * *", now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO execution_plan_schedule_occurrences (id, schedule_id, plan_id, project_id, planned_at, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"legacy-occurrence", "legacy-schedule", "legacy-plan", DefaultProjectID, now, "materializing", now, now); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	schedule, err := store.GetExecutionPlanSchedule("legacy-schedule")
	if err != nil {
		t.Fatal(err)
	}
	if schedule.ActorType != "legacy" || schedule.ActorSubject != "controller:execution-plan-schedule-legacy" {
		t.Fatalf("legacy schedule actor = %s/%s, want fail-closed legacy attribution", schedule.ActorType, schedule.ActorSubject)
	}
	occurrence, err := store.GetExecutionPlanScheduleOccurrenceByScheduleAndPlannedAt(schedule.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if occurrence.ActorType != "legacy" || occurrence.ActorSubject != "controller:execution-plan-schedule-occurrence-legacy" {
		t.Fatalf("legacy occurrence actor = %s/%s, want fail-closed legacy attribution", occurrence.ActorType, occurrence.ActorSubject)
	}
	if occurrence.ApprovalID != "" {
		t.Fatalf("legacy occurrence approval id = %q, want empty migration default", occurrence.ApprovalID)
	}
}

func TestCreateExecutionPlanScheduleOccurrenceIfAbsent(t *testing.T) {
	store := openTimeslotStore(t)
	now := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	if err := store.SaveExecutionPlan(&ExecutionPlan{ID: "plan-a", ProjectID: "proj-a", Name: "Plan A", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveExecutionPlanSchedule(&ExecutionPlanSchedule{ID: "schedule-a", PlanID: "plan-a", ProjectID: "proj-a", Name: "Schedule A", CronExpr: "0 * * * *", Enabled: true, NextRunAt: &now, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	occurrence := &ExecutionPlanScheduleOccurrence{
		ID:               "occ-first",
		ScheduleID:       "schedule-a",
		PlanID:           "plan-a",
		ProjectID:        "proj-a",
		ScheduleRevision: 1,
		PlannedAt:        now,
		Status:           "materializing",
		ReasonCode:       "due",
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	inserted, err := store.CreateExecutionPlanScheduleOccurrenceIfAbsent(occurrence)
	if err != nil || !inserted {
		t.Fatalf("first insert = (%v, %v), want (true, nil)", inserted, err)
	}
	duplicate := *occurrence
	duplicate.ID = "occ-second"
	duplicate.Status = "materialized"
	inserted, err = store.CreateExecutionPlanScheduleOccurrenceIfAbsent(&duplicate)
	if err != nil || inserted {
		t.Fatalf("duplicate insert = (%v, %v), want (false, nil)", inserted, err)
	}
	got, err := store.GetExecutionPlanScheduleOccurrenceByScheduleAndPlannedAt("schedule-a", now)
	if err != nil {
		t.Fatalf("get occurrence: %v", err)
	}
	if got == nil || got.ID != "occ-first" || got.Status != "materializing" {
		t.Fatalf("occurrence = %+v, want first row preserved", got)
	}
}

func TestExecutionPlanScheduleOccurrenceClaimRejectsStaleParent(t *testing.T) {
	store := openTimeslotStore(t)
	now := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	if err := store.SaveExecutionPlan(&ExecutionPlan{ID: "plan-stale-claim", ProjectID: "proj-a", Name: "Plan A", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	schedule := &ExecutionPlanSchedule{
		ID: "schedule-stale-claim", PlanID: "plan-stale-claim", ProjectID: "proj-a", Revision: 1,
		Name: "Schedule A", CronExpr: "0 * * * *", Enabled: true, NextRunAt: &now, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.SaveExecutionPlanSchedule(schedule); err != nil {
		t.Fatal(err)
	}
	staleOccurrence := &ExecutionPlanScheduleOccurrence{
		ID: "occurrence-stale-claim", ScheduleID: schedule.ID, PlanID: schedule.PlanID, ProjectID: schedule.ProjectID,
		ScheduleRevision: schedule.Revision, PlannedAt: now, Status: "materializing", ReasonCode: "due", CreatedAt: now, UpdatedAt: now,
	}

	disabled := *schedule
	disabled.Enabled = false
	disabled.NextRunAt = nil
	disabled.UpdatedAt = now.Add(time.Second)
	changed, err := store.UpdateExecutionPlanScheduleIfRevision(&disabled, schedule.Revision)
	if err != nil || !changed {
		t.Fatalf("disable schedule = (%v, %v), want (true, nil)", changed, err)
	}

	inserted, err := store.ClaimExecutionPlanScheduleOccurrenceIfCurrent(staleOccurrence)
	if err != nil {
		t.Fatalf("claim stale occurrence: %v", err)
	}
	if inserted {
		t.Fatal("stale occurrence was inserted after the parent schedule was disabled")
	}
	stored, err := store.GetExecutionPlanScheduleOccurrenceByScheduleAndPlannedAt(schedule.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if stored != nil {
		t.Fatalf("stale occurrence persisted: %+v", stored)
	}
}

func TestExecutionPlanScheduleDeletionPreservesActiveOccurrenceAnchor(t *testing.T) {
	store := openTimeslotStore(t)
	now := time.Now().UTC()
	if err := store.SaveExecutionPlan(&ExecutionPlan{ID: "plan-delete-guard", ProjectID: "proj-a", Name: "Guard", RunMode: "parallel", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	schedule := &ExecutionPlanSchedule{ID: "plan-schedule-delete-guard", PlanID: "plan-delete-guard", ProjectID: "proj-a", Name: "Guard", CronExpr: "* * * * *", Enabled: true, NextRunAt: &now, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveExecutionPlanSchedule(schedule); err != nil {
		t.Fatal(err)
	}
	occurrence := &ExecutionPlanScheduleOccurrence{ID: "plan-occurrence-delete-guard", ScheduleID: schedule.ID, PlanID: schedule.PlanID, ProjectID: "proj-a", PlannedAt: now, Status: "materializing", CreatedAt: now, UpdatedAt: now}
	if err := store.SaveExecutionPlanScheduleOccurrence(occurrence); err != nil {
		t.Fatal(err)
	}
	if got, err := store.DeleteExecutionPlanScheduleGuarded(schedule.ID); err != nil || got != ScheduleDeleteBlockedByActiveOccurrence {
		t.Fatalf("active schedule delete = (%q, %v), want blocked", got, err)
	}
	occurrence.Status = "materialized"
	materializedAt := now.Add(time.Second)
	occurrence.MaterializedAt = &materializedAt
	if err := store.SaveExecutionPlanScheduleOccurrence(occurrence); err != nil {
		t.Fatal(err)
	}
	if got, err := store.DeleteExecutionPlanScheduleGuarded(schedule.ID); err != nil || got != ScheduleDeleteDeleted {
		t.Fatalf("terminal schedule delete = (%q, %v), want deleted", got, err)
	}
}

func TestLeaseFencedCASMethodsRejectNilInputsWithoutPanicking(t *testing.T) {
	store := openTimeslotStore(t)
	now := time.Now().UTC()
	acquired, lease, err := store.TryAcquireSchedulerLeaseTerm("execution", "leader-a", now, time.Minute)
	if err != nil || !acquired || lease == nil {
		t.Fatalf("TryAcquireSchedulerLeaseTerm = (%v, %+v, %v)", acquired, lease, err)
	}

	if _, err := store.UpdateExecutionPlanScheduleIfRevisionAndLeaseTermHeld(nil, 1, "execution", "leader-a", lease.Generation); err == nil {
		t.Fatal("UpdateExecutionPlanScheduleIfRevisionAndLeaseTermHeld(nil) succeeded, want error")
	}
	if _, err := store.CompareAndSwapExecutionPlanScheduleOccurrenceIfLeaseTermHeld(nil, 1, "execution", "leader-a", lease.Generation); err == nil {
		t.Fatal("CompareAndSwapExecutionPlanScheduleOccurrenceIfLeaseTermHeld(nil) succeeded, want error")
	}
	if _, err := store.ResolveExecutionPlanScheduleOccurrenceIfLeaseTermHeld(nil, 1, nil, 1, "execution", "leader-a", lease.Generation); err == nil {
		t.Fatal("ResolveExecutionPlanScheduleOccurrenceIfLeaseTermHeld(nil, nil) succeeded, want error")
	}
	if _, err := store.ResolveExecutionPlanScheduleOccurrence(nil, 1, nil, 1); err == nil {
		t.Fatal("ResolveExecutionPlanScheduleOccurrence(nil, nil) succeeded, want error")
	}
	if _, err := store.CompareAndSwapExecutionPlanIfLeaseTermHeld(nil, 1, "execution", "leader-a", lease.Generation); err == nil {
		t.Fatal("CompareAndSwapExecutionPlanIfLeaseTermHeld(nil) succeeded, want error")
	}
	if _, err := store.CreateExecutionPlanRunIfAbsentAndLeaseTermHeld(nil, "execution", "leader-a", lease.Generation); err == nil {
		t.Fatal("CreateExecutionPlanRunIfAbsentAndLeaseTermHeld(nil) succeeded, want error")
	}
	if _, err := store.CompareAndSwapExecutionPlanRunIfLeaseTermHeld(nil, 1, "execution", "leader-a", lease.Generation); err == nil {
		t.Fatal("CompareAndSwapExecutionPlanRunIfLeaseTermHeld(nil) succeeded, want error")
	}
	if _, err := store.UpdateScenarioScheduleIfRevisionAndLeaseHeld(nil, 1, "execution", "leader-a"); err == nil {
		t.Fatal("UpdateScenarioScheduleIfRevisionAndLeaseHeld(nil) succeeded, want error")
	}
	if _, err := store.UpdateScenarioScheduleIfRevisionAndLeaseTermHeld(nil, 1, "execution", "leader-a", lease.Generation); err == nil {
		t.Fatal("UpdateScenarioScheduleIfRevisionAndLeaseTermHeld(nil) succeeded, want error")
	}
	if _, err := store.CompareAndSwapScenarioScheduleOccurrenceIfLeaseTermHeld(nil, 1, "execution", "leader-a", lease.Generation); err == nil {
		t.Fatal("CompareAndSwapScenarioScheduleOccurrenceIfLeaseTermHeld(nil) succeeded, want error")
	}
	if _, err := store.ResolveScenarioScheduleOccurrenceIfLeaseTermHeld(nil, 1, nil, 1, "execution", "leader-a", lease.Generation); err == nil {
		t.Fatal("ResolveScenarioScheduleOccurrenceIfLeaseTermHeld(nil, nil) succeeded, want error")
	}
	if _, err := store.ResolveScenarioScheduleOccurrence(nil, 1, nil, 1); err == nil {
		t.Fatal("ResolveScenarioScheduleOccurrence(nil, nil) succeeded, want error")
	}
	if _, err := store.SaveScenarioScheduleOccurrenceIfLeaseHeld(nil, "execution", "leader-a"); err == nil {
		t.Fatal("SaveScenarioScheduleOccurrenceIfLeaseHeld(nil) succeeded, want error")
	}
	uncommitted := &ScenarioScheduleOccurrence{
		ID:         "occ-uncommitted",
		ScheduleID: "missing-schedule",
		PlannedAt:  now,
		Status:     "materializing",
		Revision:   4,
	}
	if changed, err := store.SaveScenarioScheduleOccurrenceIfLeaseHeld(uncommitted, "execution", "wrong-leader"); err != nil || changed {
		t.Fatalf("SaveScenarioScheduleOccurrenceIfLeaseHeld with wrong leader = (%v, %v), want (false, nil)", changed, err)
	}
	if uncommitted.Revision != 4 {
		t.Fatalf("SaveScenarioScheduleOccurrenceIfLeaseHeld mutated Revision on failed lease check = %d, want 4", uncommitted.Revision)
	}

	plan := &ExecutionPlan{ID: "plan-rev-preserve", ProjectID: "proj-a", Name: "Plan", CreatedAt: now, UpdatedAt: now}
	if created, err := store.CreateExecutionPlanIfAbsent(plan); err != nil || !created || plan.Revision != 1 {
		t.Fatalf("CreateExecutionPlanIfAbsent first = (%v, %v, rev=%d), want (true, nil, 1)", created, err, plan.Revision)
	}
	conflictingPlan := &ExecutionPlan{ID: "plan-rev-preserve", ProjectID: "proj-a", Name: "Plan 2", Revision: 7, CreatedAt: now, UpdatedAt: now}
	if created, err := store.CreateExecutionPlanIfAbsent(conflictingPlan); err != nil || created || conflictingPlan.Revision != 7 {
		t.Fatalf("CreateExecutionPlanIfAbsent conflict = (%v, %v, rev=%d), want (false, nil, 7)", created, err, conflictingPlan.Revision)
	}
	crossProjectPlan := &ExecutionPlan{ID: "plan-rev-preserve", ProjectID: "proj-b", Name: "Cross Project", Revision: 0, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveExecutionPlan(crossProjectPlan); err == nil || crossProjectPlan.Revision != 0 {
		t.Fatalf("SaveExecutionPlan cross-project with rev=0 = (%v, rev=%d), want (non-nil, 0)", err, crossProjectPlan.Revision)
	}
	if changed, err := store.SaveExecutionPlanIfLeaseTermHeld(crossProjectPlan, "execution", "leader-a", lease.Generation); err == nil || changed || crossProjectPlan.Revision != 0 {
		t.Fatalf("SaveExecutionPlanIfLeaseTermHeld cross-project with rev=0 = (%v, %v, rev=%d), want (false, non-nil, 0)", changed, err, crossProjectPlan.Revision)
	}

	run := &ExecutionPlanRun{
		ID:        "run-rev-preserve",
		PlanID:    plan.ID,
		ProjectID: "proj-a",
		RunMode:   "parallel",
		Status:    "running",
		Entries:   []ExecutionPlanRunEntry{{ScenarioID: "s-1", ScenarioName: "S1", Order: 1, Status: "pending"}},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if created, err := store.CreateExecutionPlanRunIfAbsent(run); err != nil || !created || run.Revision != 1 {
		t.Fatalf("CreateExecutionPlanRunIfAbsent first = (%v, %v, rev=%d), want (true, nil, 1)", created, err, run.Revision)
	}
	conflictingRun := *run
	conflictingRun.Revision = 9
	if created, err := store.CreateExecutionPlanRunIfAbsent(&conflictingRun); err != nil || created || conflictingRun.Revision != 9 {
		t.Fatalf("CreateExecutionPlanRunIfAbsent conflict = (%v, %v, rev=%d), want (false, nil, 9)", created, err, conflictingRun.Revision)
	}
	zeroRevRun := *run
	zeroRevRun.Revision = 0
	if updated, err := store.CompareAndSwapExecutionPlanRun(&zeroRevRun, 99); err != nil || updated || zeroRevRun.Revision != 0 {
		t.Fatalf("CompareAndSwapExecutionPlanRun mismatch with rev=0 = (%v, %v, rev=%d), want (false, nil, 0)", updated, err, zeroRevRun.Revision)
	}
	wrongPlanRun := *run
	wrongPlanRun.PlanID = "other-plan"
	wrongPlanRun.Revision = 0
	if err := store.SaveExecutionPlanRun(&wrongPlanRun); err == nil || wrongPlanRun.Revision != 0 {
		t.Fatalf("SaveExecutionPlanRun wrong plan with rev=0 = (%v, rev=%d), want (non-nil, 0)", err, wrongPlanRun.Revision)
	}

	planSchedule := &ExecutionPlanSchedule{
		ID: "plan-sched-rev-preserve", PlanID: plan.ID, ProjectID: "proj-a",
		Name: "Schedule", CronExpr: "0 * * * *", Enabled: true, NextRunAt: &now, CreatedAt: now, UpdatedAt: now,
	}
	if created, err := store.CreateExecutionPlanScheduleIfAbsent(planSchedule); err != nil || !created || planSchedule.Revision != 1 {
		t.Fatalf("CreateExecutionPlanScheduleIfAbsent first = (%v, %v, rev=%d), want (true, nil, 1)", created, err, planSchedule.Revision)
	}
	duplicatePlanSchedule := *planSchedule
	duplicatePlanSchedule.Revision = 0
	if created, err := store.CreateExecutionPlanScheduleIfAbsent(&duplicatePlanSchedule); err != nil || created || duplicatePlanSchedule.Revision != 0 {
		t.Fatalf("CreateExecutionPlanScheduleIfAbsent conflict with rev=0 = (%v, %v, rev=%d), want (false, nil, 0)", created, err, duplicatePlanSchedule.Revision)
	}
	if created, err := store.CreateExecutionPlanScheduleIfAbsentAndLeaseTermHeld(&duplicatePlanSchedule, "execution", "leader-a", lease.Generation); err != nil || created || duplicatePlanSchedule.Revision != 0 {
		t.Fatalf("CreateExecutionPlanScheduleIfAbsentAndLeaseTermHeld conflict with rev=0 = (%v, %v, rev=%d), want (false, nil, 0)", created, err, duplicatePlanSchedule.Revision)
	}
	if updated, err := store.UpdateExecutionPlanScheduleIfRevision(&duplicatePlanSchedule, 99); err != nil || updated || duplicatePlanSchedule.Revision != 0 {
		t.Fatalf("UpdateExecutionPlanScheduleIfRevision mismatch with rev=0 = (%v, %v, rev=%d), want (false, nil, 0)", updated, err, duplicatePlanSchedule.Revision)
	}
	if updated, err := store.UpdateExecutionPlanScheduleIfRevisionAndLeaseTermHeld(&duplicatePlanSchedule, 99, "execution", "leader-a", lease.Generation); err != nil || updated || duplicatePlanSchedule.Revision != 0 {
		t.Fatalf("UpdateExecutionPlanScheduleIfRevisionAndLeaseTermHeld mismatch with rev=0 = (%v, %v, rev=%d), want (false, nil, 0)", updated, err, duplicatePlanSchedule.Revision)
	}
	missingPlanSchedule := *planSchedule
	missingPlanSchedule.ID = "plan-sched-missing-plan"
	missingPlanSchedule.PlanID = "missing-plan-id"
	missingPlanSchedule.Revision = 0
	if err := store.SaveExecutionPlanSchedule(&missingPlanSchedule); err == nil || missingPlanSchedule.Revision != 0 {
		t.Fatalf("SaveExecutionPlanSchedule missing plan with rev=0 = (%v, rev=%d), want (non-nil, 0)", err, missingPlanSchedule.Revision)
	}

	planOcc := &ExecutionPlanScheduleOccurrence{
		ID: "plan-occ-rev-preserve", ScheduleID: planSchedule.ID, PlanID: plan.ID, ProjectID: "proj-a",
		PlannedAt: now, Status: "materializing", CreatedAt: now, UpdatedAt: now,
	}
	if created, err := store.CreateExecutionPlanScheduleOccurrenceIfAbsent(planOcc); err != nil || !created || planOcc.Revision != 1 || planOcc.ScheduleRevision != 1 {
		t.Fatalf("CreateExecutionPlanScheduleOccurrenceIfAbsent first = (%v, %v, rev=%d, schedRev=%d), want (true, nil, 1, 1)", created, err, planOcc.Revision, planOcc.ScheduleRevision)
	}
	zeroRevPlanOcc := *planOcc
	zeroRevPlanOcc.Revision = 0
	zeroRevPlanOcc.ScheduleRevision = 0
	if created, err := store.CreateExecutionPlanScheduleOccurrenceIfAbsent(&zeroRevPlanOcc); err != nil || created || zeroRevPlanOcc.Revision != 0 || zeroRevPlanOcc.ScheduleRevision != 0 {
		t.Fatalf("CreateExecutionPlanScheduleOccurrenceIfAbsent conflict with rev=0 = (%v, %v, rev=%d, schedRev=%d), want (false, nil, 0, 0)", created, err, zeroRevPlanOcc.Revision, zeroRevPlanOcc.ScheduleRevision)
	}
	if updated, err := store.CompareAndSwapExecutionPlanScheduleOccurrence(&zeroRevPlanOcc, 99); err != nil || updated || zeroRevPlanOcc.Revision != 0 || zeroRevPlanOcc.ScheduleRevision != 0 {
		t.Fatalf("CompareAndSwapExecutionPlanScheduleOccurrence mismatch with rev=0 = (%v, %v, rev=%d, schedRev=%d), want (false, nil, 0, 0)", updated, err, zeroRevPlanOcc.Revision, zeroRevPlanOcc.ScheduleRevision)
	}
	if claimed, err := store.ClaimExecutionPlanScheduleOccurrenceIfCurrent(&zeroRevPlanOcc); err != nil || claimed || zeroRevPlanOcc.Revision != 0 || zeroRevPlanOcc.ScheduleRevision != 0 {
		t.Fatalf("ClaimExecutionPlanScheduleOccurrenceIfCurrent mismatch with rev=0 = (%v, %v, rev=%d, schedRev=%d), want (false, nil, 0, 0)", claimed, err, zeroRevPlanOcc.Revision, zeroRevPlanOcc.ScheduleRevision)
	}
	if resolved, err := store.ResolveExecutionPlanScheduleOccurrence(&zeroRevPlanOcc, 99, planSchedule, 99); err != nil || resolved || zeroRevPlanOcc.Revision != 0 || zeroRevPlanOcc.ScheduleRevision != 0 {
		t.Fatalf("ResolveExecutionPlanScheduleOccurrence mismatch with rev=0 = (%v, %v, rev=%d, schedRev=%d), want (false, nil, 0, 0)", resolved, err, zeroRevPlanOcc.Revision, zeroRevPlanOcc.ScheduleRevision)
	}

	if err := store.SaveScenario(&Scenario{ID: "scenario-rev-preserve", Name: "Scenario", Duration: "1m", CreatedAt: now, UpdatedAt: now}, nil); err != nil {
		t.Fatal(err)
	}
	scenarioSchedule := &ScenarioSchedule{
		ID: "scen-sched-rev-preserve", ScenarioID: "scenario-rev-preserve", ProjectID: DefaultProjectID,
		Name: "Scenario Schedule", CronExpr: "0 * * * *", Enabled: true, NextRunAt: &now, CreatedAt: now, UpdatedAt: now,
	}
	if created, err := store.CreateScenarioScheduleIfAbsent(scenarioSchedule); err != nil || !created || scenarioSchedule.Revision != 1 {
		t.Fatalf("CreateScenarioScheduleIfAbsent first = (%v, %v, rev=%d), want (true, nil, 1)", created, err, scenarioSchedule.Revision)
	}
	duplicateScenarioSchedule := *scenarioSchedule
	duplicateScenarioSchedule.Revision = 0
	if created, err := store.CreateScenarioScheduleIfAbsent(&duplicateScenarioSchedule); err != nil || created || duplicateScenarioSchedule.Revision != 0 {
		t.Fatalf("CreateScenarioScheduleIfAbsent conflict with rev=0 = (%v, %v, rev=%d), want (false, nil, 0)", created, err, duplicateScenarioSchedule.Revision)
	}
	if created, err := store.CreateScenarioScheduleIfAbsentAndLeaseHeld(&duplicateScenarioSchedule, "execution", "leader-a"); err != nil || created || duplicateScenarioSchedule.Revision != 0 {
		t.Fatalf("CreateScenarioScheduleIfAbsentAndLeaseHeld conflict with rev=0 = (%v, %v, rev=%d), want (false, nil, 0)", created, err, duplicateScenarioSchedule.Revision)
	}
	if created, err := store.CreateScenarioScheduleIfAbsentAndLeaseTermHeld(&duplicateScenarioSchedule, "execution", "leader-a", lease.Generation); err != nil || created || duplicateScenarioSchedule.Revision != 0 {
		t.Fatalf("CreateScenarioScheduleIfAbsentAndLeaseTermHeld conflict with rev=0 = (%v, %v, rev=%d), want (false, nil, 0)", created, err, duplicateScenarioSchedule.Revision)
	}
	if updated, err := store.UpdateScenarioScheduleIfRevision(&duplicateScenarioSchedule, 99); err != nil || updated || duplicateScenarioSchedule.Revision != 0 {
		t.Fatalf("UpdateScenarioScheduleIfRevision mismatch with rev=0 = (%v, %v, rev=%d), want (false, nil, 0)", updated, err, duplicateScenarioSchedule.Revision)
	}
	if updated, err := store.UpdateScenarioScheduleIfRevisionAndLeaseHeld(&duplicateScenarioSchedule, 99, "execution", "leader-a"); err != nil || updated || duplicateScenarioSchedule.Revision != 0 {
		t.Fatalf("UpdateScenarioScheduleIfRevisionAndLeaseHeld mismatch with rev=0 = (%v, %v, rev=%d), want (false, nil, 0)", updated, err, duplicateScenarioSchedule.Revision)
	}
	if updated, err := store.UpdateScenarioScheduleIfRevisionAndLeaseTermHeld(&duplicateScenarioSchedule, 99, "execution", "leader-a", lease.Generation); err != nil || updated || duplicateScenarioSchedule.Revision != 0 {
		t.Fatalf("UpdateScenarioScheduleIfRevisionAndLeaseTermHeld mismatch with rev=0 = (%v, %v, rev=%d), want (false, nil, 0)", updated, err, duplicateScenarioSchedule.Revision)
	}
	missingScenarioSchedule := *scenarioSchedule
	missingScenarioSchedule.ID = "scen-sched-missing-scenario"
	missingScenarioSchedule.ScenarioID = "missing-scenario-id"
	missingScenarioSchedule.Revision = 0
	if err := store.SaveScenarioSchedule(&missingScenarioSchedule); err == nil || missingScenarioSchedule.Revision != 0 {
		t.Fatalf("SaveScenarioSchedule missing scenario with rev=0 = (%v, rev=%d), want (non-nil, 0)", err, missingScenarioSchedule.Revision)
	}
	invalidApproval := &ScenarioScheduleApproval{
		ID:               "scen-approval-invalid",
		ScheduleID:       scenarioSchedule.ID,
		ScenarioID:       scenarioSchedule.ScenarioID,
		ProjectID:        DefaultProjectID,
		ScheduleRevision: 0,
		Policy:           "",
		Status:           "pending",
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := store.SaveScenarioScheduleApproval(invalidApproval); err == nil || invalidApproval.ScheduleRevision != 0 {
		t.Fatalf("SaveScenarioScheduleApproval invalid policy with schedRev=0 = (%v, schedRev=%d), want (non-nil, 0)", err, invalidApproval.ScheduleRevision)
	}

	scenOcc := &ScenarioScheduleOccurrence{
		ID: "scen-occ-rev-preserve", ScheduleID: scenarioSchedule.ID, ScenarioID: scenarioSchedule.ScenarioID, ProjectID: "proj-a",
		PlannedAt: now, Status: "materializing", CreatedAt: now, UpdatedAt: now,
	}
	if created, err := store.CreateScenarioScheduleOccurrenceIfAbsent(scenOcc); err != nil || !created || scenOcc.Revision != 1 || scenOcc.ScheduleRevision != 1 {
		t.Fatalf("CreateScenarioScheduleOccurrenceIfAbsent first = (%v, %v, rev=%d, schedRev=%d), want (true, nil, 1, 1)", created, err, scenOcc.Revision, scenOcc.ScheduleRevision)
	}
	zeroRevScenOcc := *scenOcc
	zeroRevScenOcc.Revision = 0
	zeroRevScenOcc.ScheduleRevision = 0
	if created, err := store.CreateScenarioScheduleOccurrenceIfAbsent(&zeroRevScenOcc); err != nil || created || zeroRevScenOcc.Revision != 0 || zeroRevScenOcc.ScheduleRevision != 0 {
		t.Fatalf("CreateScenarioScheduleOccurrenceIfAbsent conflict with rev=0 = (%v, %v, rev=%d, schedRev=%d), want (false, nil, 0, 0)", created, err, zeroRevScenOcc.Revision, zeroRevScenOcc.ScheduleRevision)
	}
	if updated, err := store.CompareAndSwapScenarioScheduleOccurrence(&zeroRevScenOcc, 99); err != nil || updated || zeroRevScenOcc.Revision != 0 || zeroRevScenOcc.ScheduleRevision != 0 {
		t.Fatalf("CompareAndSwapScenarioScheduleOccurrence mismatch with rev=0 = (%v, %v, rev=%d, schedRev=%d), want (false, nil, 0, 0)", updated, err, zeroRevScenOcc.Revision, zeroRevScenOcc.ScheduleRevision)
	}
	if claimed, err := store.ClaimScenarioScheduleOccurrenceIfCurrent(&zeroRevScenOcc); err != nil || claimed || zeroRevScenOcc.Revision != 0 || zeroRevScenOcc.ScheduleRevision != 0 {
		t.Fatalf("ClaimScenarioScheduleOccurrenceIfCurrent mismatch with rev=0 = (%v, %v, rev=%d, schedRev=%d), want (false, nil, 0, 0)", claimed, err, zeroRevScenOcc.Revision, zeroRevScenOcc.ScheduleRevision)
	}
	if resolved, err := store.ResolveScenarioScheduleOccurrence(&zeroRevScenOcc, 99, scenarioSchedule, 99); err != nil || resolved || zeroRevScenOcc.Revision != 0 || zeroRevScenOcc.ScheduleRevision != 0 {
		t.Fatalf("ResolveScenarioScheduleOccurrence mismatch with rev=0 = (%v, %v, rev=%d, schedRev=%d), want (false, nil, 0, 0)", resolved, err, zeroRevScenOcc.Revision, zeroRevScenOcc.ScheduleRevision)
	}
}
