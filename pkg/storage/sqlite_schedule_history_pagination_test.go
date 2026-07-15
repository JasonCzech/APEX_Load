package storage

import (
	"fmt"
	"testing"
	"time"
)

func TestScheduleHistoryPagination(t *testing.T) {
	store := openTimeslotStore(t)
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)

	if err := store.SaveScenario(&Scenario{ID: "history-scenario", Name: "History", Duration: "1m", CreatedAt: now, UpdatedAt: now}, nil); err != nil {
		t.Fatalf("SaveScenario: %v", err)
	}
	scenarioSchedule := &ScenarioSchedule{
		ID: "history-scenario-schedule", ScenarioID: "history-scenario", ProjectID: DefaultProjectID,
		Name: "History", CronExpr: "* * * * *", Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.SaveScenarioSchedule(scenarioSchedule); err != nil {
		t.Fatalf("SaveScenarioSchedule: %v", err)
	}
	for i := 0; i < 3; i++ {
		at := now.Add(time.Duration(i) * time.Minute)
		if err := store.SaveScenarioScheduleOccurrence(&ScenarioScheduleOccurrence{
			ID: fmt.Sprintf("history-scenario-occurrence-%d", i), ScheduleID: scenarioSchedule.ID,
			ScenarioID: scenarioSchedule.ScenarioID, ProjectID: DefaultProjectID, PlannedAt: at,
			Status: "materialized", CreatedAt: at, UpdatedAt: at,
		}); err != nil {
			t.Fatalf("SaveScenarioScheduleOccurrence(%d): %v", i, err)
		}
		if err := store.SaveScenarioScheduleApproval(&ScenarioScheduleApproval{
			ID: fmt.Sprintf("history-scenario-approval-%d", i), ScheduleID: scenarioSchedule.ID,
			ScenarioID: scenarioSchedule.ScenarioID, ProjectID: DefaultProjectID, Policy: "manual",
			Status: "approved", RequestedAt: at, CreatedAt: at, UpdatedAt: at,
		}); err != nil {
			t.Fatalf("SaveScenarioScheduleApproval(%d): %v", i, err)
		}
	}
	occurrences, err := store.ListScenarioScheduleOccurrencesPage(scenarioSchedule.ID, 1, 1)
	if err != nil || len(occurrences) != 1 || occurrences[0].ID != "history-scenario-occurrence-1" {
		t.Fatalf("scenario occurrence page = %#v, err=%v", occurrences, err)
	}
	approvals, err := store.ListScenarioScheduleApprovalsPage(scenarioSchedule.ID, 1, 1)
	if err != nil || len(approvals) != 1 || approvals[0].ID != "history-scenario-approval-1" {
		t.Fatalf("scenario approval page = %#v, err=%v", approvals, err)
	}

	plan := &ExecutionPlan{ID: "history-plan", ProjectID: DefaultProjectID, Name: "History", RunMode: "parallel", CreatedAt: now, UpdatedAt: now}
	if err := store.SaveExecutionPlan(plan); err != nil {
		t.Fatalf("SaveExecutionPlan: %v", err)
	}
	planSchedule := &ExecutionPlanSchedule{
		ID: "history-plan-schedule", PlanID: plan.ID, ProjectID: DefaultProjectID,
		Name: "History", CronExpr: "* * * * *", Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.SaveExecutionPlanSchedule(planSchedule); err != nil {
		t.Fatalf("SaveExecutionPlanSchedule: %v", err)
	}
	for i := 0; i < 3; i++ {
		at := now.Add(time.Duration(i) * time.Minute)
		if err := store.SaveExecutionPlanScheduleOccurrence(&ExecutionPlanScheduleOccurrence{
			ID: fmt.Sprintf("history-plan-occurrence-%d", i), ScheduleID: planSchedule.ID,
			PlanID: plan.ID, ProjectID: DefaultProjectID, PlannedAt: at,
			Status: "materialized", CreatedAt: at, UpdatedAt: at,
		}); err != nil {
			t.Fatalf("SaveExecutionPlanScheduleOccurrence(%d): %v", i, err)
		}
		if err := store.SaveExecutionPlanApproval(&ExecutionPlanApproval{
			ID: fmt.Sprintf("history-plan-approval-%d", i), PlanID: plan.ID, ProjectID: DefaultProjectID,
			Policy: "manual", Status: "approved", RequestedAt: at, CreatedAt: at, UpdatedAt: at,
		}); err != nil {
			t.Fatalf("SaveExecutionPlanApproval(%d): %v", i, err)
		}
	}
	planOccurrences, err := store.ListExecutionPlanScheduleOccurrencesPage(planSchedule.ID, 1, 1)
	if err != nil || len(planOccurrences) != 1 || planOccurrences[0].ID != "history-plan-occurrence-1" {
		t.Fatalf("execution-plan occurrence page = %#v, err=%v", planOccurrences, err)
	}
	planApprovals, err := store.ListExecutionPlanApprovalsPage(plan.ID, 1, 1)
	if err != nil || len(planApprovals) != 1 || planApprovals[0].ID != "history-plan-approval-1" {
		t.Fatalf("execution-plan approval page = %#v, err=%v", planApprovals, err)
	}
}
