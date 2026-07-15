package storage

import (
	"testing"
	"time"
)

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
