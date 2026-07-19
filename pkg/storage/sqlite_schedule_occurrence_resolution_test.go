package storage

import (
	"path/filepath"
	"testing"
	"time"
)

func TestScenarioOccurrenceResolutionAtomicallyAdvancesParentAndOccurrence(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	if err := store.SaveScenario(&Scenario{ID: "scenario-resolution", Name: "Resolution", Duration: "1m", CreatedAt: now}, nil); err != nil {
		t.Fatal(err)
	}
	plannedAt := now.Add(-time.Minute)
	schedule := &ScenarioSchedule{ID: "schedule-resolution", ScenarioID: "scenario-resolution", ProjectID: DefaultProjectID, Revision: 1, Name: "Resolution", CronExpr: "*/5 * * * *", Timezone: "UTC", Enabled: true, NextRunAt: &plannedAt, NextRequiredAction: "operator_decision_required", LastError: "blocked", CreatedAt: now, UpdatedAt: now}
	if created, err := store.CreateScenarioScheduleIfAbsent(schedule); err != nil || !created {
		t.Fatalf("create schedule = %v, %v", created, err)
	}
	occurrence := &ScenarioScheduleOccurrence{ID: "scenario-occurrence-resolution", ScheduleID: schedule.ID, PlannedAt: plannedAt, Status: "blocked", ReasonCode: "missed_run_requires_decision", ConflictSummary: "blocked", CreatedAt: now, UpdatedAt: now}
	if claimed, err := store.ClaimScenarioScheduleOccurrenceIfCurrent(occurrence); err != nil || !claimed {
		t.Fatalf("claim occurrence = %v, %v", claimed, err)
	}

	resolvedSchedule := *schedule
	resolvedSchedule.NextRunAt = &plannedAt
	resolvedSchedule.NextRequiredAction = ""
	resolvedSchedule.LastError = ""
	resolvedSchedule.UpdatedAt = now.Add(time.Minute)
	resolvedOccurrence := *occurrence
	resolvedOccurrence.Status = "materializing"
	resolvedOccurrence.ReasonCode = "operator_catch_up"
	resolvedOccurrence.ConflictSummary = ""
	resolvedOccurrence.UpdatedAt = now.Add(time.Minute)
	changed, err := store.ResolveScenarioScheduleOccurrence(&resolvedOccurrence, occurrence.Revision, &resolvedSchedule, schedule.Revision)
	if err != nil || !changed {
		t.Fatalf("ResolveScenarioScheduleOccurrence = %v, %v", changed, err)
	}
	gotSchedule, err := store.GetScenarioSchedule(schedule.ID)
	if err != nil {
		t.Fatal(err)
	}
	gotOccurrence, err := store.GetScenarioScheduleOccurrence(occurrence.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotSchedule.Revision != 2 || gotSchedule.NextRequiredAction != "" || gotOccurrence.Revision != 2 || gotOccurrence.Status != "materializing" {
		t.Fatalf("resolved pair = schedule %#v occurrence %#v", gotSchedule, gotOccurrence)
	}

	staleSchedule := *schedule
	staleOccurrence := *occurrence
	staleOccurrence.Status = "skipped"
	if changed, err := store.ResolveScenarioScheduleOccurrence(&staleOccurrence, 1, &staleSchedule, 1); err != nil || changed {
		t.Fatalf("stale resolution = %v, %v; want false, nil", changed, err)
	}
	gotOccurrence, _ = store.GetScenarioScheduleOccurrence(occurrence.ID)
	if gotOccurrence.Status != "materializing" || gotOccurrence.Revision != 2 {
		t.Fatalf("stale transaction changed occurrence: %#v", gotOccurrence)
	}
}

func TestExecutionPlanOccurrenceResolutionAtomicallyAdvancesParentAndOccurrence(t *testing.T) {
	store := openTimeslotStore(t)
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	plan := &ExecutionPlan{ID: "plan-resolution", ProjectID: "project-resolution", Name: "Resolution", CreatedAt: now, UpdatedAt: now}
	if err := store.SaveExecutionPlan(plan); err != nil {
		t.Fatal(err)
	}
	plannedAt := now.Add(-time.Minute)
	schedule := &ExecutionPlanSchedule{ID: "plan-schedule-resolution", PlanID: plan.ID, ProjectID: plan.ProjectID, Revision: 1, Name: "Resolution", CronExpr: "*/5 * * * *", Timezone: "UTC", Enabled: true, NextRunAt: &plannedAt, NextRequiredAction: "operator_decision_required", LastError: "blocked", CreatedAt: now, UpdatedAt: now}
	if created, err := store.CreateExecutionPlanScheduleIfAbsent(schedule); err != nil || !created {
		t.Fatalf("create schedule = %v, %v", created, err)
	}
	occurrence := &ExecutionPlanScheduleOccurrence{ID: "plan-occurrence-resolution", ScheduleID: schedule.ID, PlannedAt: plannedAt, Status: "blocked", ReasonCode: "missed_run_requires_decision", ConflictSummary: "blocked", CreatedAt: now, UpdatedAt: now}
	if claimed, err := store.ClaimExecutionPlanScheduleOccurrenceIfCurrent(occurrence); err != nil || !claimed {
		t.Fatalf("claim occurrence = %v, %v", claimed, err)
	}
	resolvedSchedule := *schedule
	resolvedSchedule.NextRunAt = &plannedAt
	resolvedSchedule.NextRequiredAction = ""
	resolvedSchedule.LastError = ""
	resolvedSchedule.UpdatedAt = now.Add(time.Minute)
	resolvedOccurrence := *occurrence
	resolvedOccurrence.Status = "materializing"
	resolvedOccurrence.ReasonCode = "operator_retry"
	resolvedOccurrence.RunID = "resolved-run"
	resolvedOccurrence.ConflictSummary = ""
	resolvedOccurrence.UpdatedAt = now.Add(time.Minute)
	changed, err := store.ResolveExecutionPlanScheduleOccurrence(&resolvedOccurrence, occurrence.Revision, &resolvedSchedule, schedule.Revision)
	if err != nil || !changed {
		t.Fatalf("ResolveExecutionPlanScheduleOccurrence = %v, %v", changed, err)
	}
	gotSchedule, err := store.GetExecutionPlanSchedule(schedule.ID)
	if err != nil {
		t.Fatal(err)
	}
	gotOccurrence, err := store.GetExecutionPlanScheduleOccurrenceByScheduleAndPlannedAt(schedule.ID, plannedAt)
	if err != nil {
		t.Fatal(err)
	}
	if gotSchedule.Revision != 2 || gotOccurrence.Revision != 2 || gotOccurrence.Status != "materializing" || gotOccurrence.RunID != "resolved-run" {
		t.Fatalf("resolved pair = schedule %#v occurrence %#v", gotSchedule, gotOccurrence)
	}
}
