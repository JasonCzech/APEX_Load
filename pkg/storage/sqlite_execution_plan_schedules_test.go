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
	if err := store.SaveExecutionPlanSchedule(&ExecutionPlanSchedule{ID: "schedule-a", PlanID: "plan-a", ProjectID: "proj-a", Name: "Schedule A", CronExpr: "0 * * * *", CreatedAt: now, UpdatedAt: now}); err != nil {
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
