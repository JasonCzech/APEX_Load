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
		CreatedAt: now, UpdatedAt: now,
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
	if err != nil || persistedOccurrence.ActorType != "service_account" || persistedOccurrence.ActorSubject != "schedule-runner" || persistedOccurrence.ActorDisplayName != "Schedule Runner" {
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
