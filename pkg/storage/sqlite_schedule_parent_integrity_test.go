package storage

import (
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestScheduleChildrenUseDurableParentOwnership(t *testing.T) {
	store := openTimeslotStore(t)
	now := time.Now().UTC()
	for _, projectID := range []string{"schedule-project-a", "schedule-project-b"} {
		if err := store.SaveProject(&Project{ID: projectID, Name: projectID, CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
		script := &dsl.Script{ID: "script-" + projectID, ProjectID: projectID, Name: projectID, Protocol: "http"}
		if err := store.SaveScript(script); err != nil {
			t.Fatal(err)
		}
		if err := store.SaveScenario(&Scenario{ID: "scenario-" + projectID, Name: projectID, Duration: "1m", CreatedAt: now}, []ScenarioScript{{ScriptID: script.ID, VUsers: 1}}); err != nil {
			t.Fatal(err)
		}
	}

	schedule := &ScenarioSchedule{
		ID: "durable-parent-scenario-schedule", ScenarioID: "scenario-schedule-project-a", ProjectID: "schedule-project-b",
		Name: "Owned by A", CronExpr: "* * * * *", Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.SaveScenarioSchedule(schedule); err != nil {
		t.Fatalf("SaveScenarioSchedule: %v", err)
	}
	if schedule.ProjectID != "schedule-project-a" {
		t.Fatalf("schedule project = %q, want durable parent project", schedule.ProjectID)
	}
	forgedSchedule := *schedule
	forgedSchedule.ScenarioID = "scenario-schedule-project-b"
	if err := store.SaveScenarioSchedule(&forgedSchedule); err == nil {
		t.Fatal("SaveScenarioSchedule allowed schedule ID relocation")
	}
	occurrence := &ScenarioScheduleOccurrence{
		ID: "durable-parent-scenario-occurrence", ScheduleID: schedule.ID,
		ScenarioID: "scenario-schedule-project-b", ProjectID: "schedule-project-b",
		PlannedAt: now, Status: "materialized", CreatedAt: now, UpdatedAt: now,
	}
	if err := store.SaveScenarioScheduleOccurrence(occurrence); err != nil {
		t.Fatalf("SaveScenarioScheduleOccurrence: %v", err)
	}
	if occurrence.ScenarioID != schedule.ScenarioID || occurrence.ProjectID != schedule.ProjectID {
		t.Fatalf("occurrence retained forged parent metadata: %#v", occurrence)
	}
	approval := &ScenarioScheduleApproval{
		ID: "durable-parent-scenario-approval", ScheduleID: schedule.ID,
		ScenarioID: "scenario-schedule-project-b", ProjectID: "schedule-project-b",
		Policy: "manual", Status: "pending", RequestedAt: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.SaveScenarioScheduleApproval(approval); err != nil {
		t.Fatalf("SaveScenarioScheduleApproval: %v", err)
	}
	if approval.ScenarioID != schedule.ScenarioID || approval.ProjectID != schedule.ProjectID {
		t.Fatalf("approval retained forged parent metadata: %#v", approval)
	}

	for _, plan := range []*ExecutionPlan{
		{ID: "schedule-plan-a", ProjectID: "schedule-project-a", Name: "A", RunMode: "parallel", CreatedAt: now, UpdatedAt: now},
		{ID: "schedule-plan-b", ProjectID: "schedule-project-b", Name: "B", RunMode: "parallel", CreatedAt: now, UpdatedAt: now},
	} {
		if err := store.SaveExecutionPlan(plan); err != nil {
			t.Fatal(err)
		}
	}
	planSchedule := &ExecutionPlanSchedule{
		ID: "durable-parent-plan-schedule", PlanID: "schedule-plan-a", ProjectID: "schedule-project-b",
		Name: "Owned by A", CronExpr: "* * * * *", Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.SaveExecutionPlanSchedule(planSchedule); err != nil {
		t.Fatalf("SaveExecutionPlanSchedule: %v", err)
	}
	if planSchedule.ProjectID != "schedule-project-a" {
		t.Fatalf("plan schedule project = %q, want durable plan project", planSchedule.ProjectID)
	}
	forgedPlanSchedule := *planSchedule
	forgedPlanSchedule.PlanID = "schedule-plan-b"
	if err := store.SaveExecutionPlanSchedule(&forgedPlanSchedule); err == nil {
		t.Fatal("SaveExecutionPlanSchedule allowed schedule ID relocation")
	}
	planOccurrence := &ExecutionPlanScheduleOccurrence{
		ID: "durable-parent-plan-occurrence", ScheduleID: planSchedule.ID,
		PlanID: "schedule-plan-b", ProjectID: "schedule-project-b",
		PlannedAt: now, Status: "materialized", CreatedAt: now, UpdatedAt: now,
	}
	if err := store.SaveExecutionPlanScheduleOccurrence(planOccurrence); err != nil {
		t.Fatalf("SaveExecutionPlanScheduleOccurrence: %v", err)
	}
	if planOccurrence.PlanID != planSchedule.PlanID || planOccurrence.ProjectID != planSchedule.ProjectID {
		t.Fatalf("plan occurrence retained forged parent metadata: %#v", planOccurrence)
	}
	planApproval := &ExecutionPlanApproval{
		ID: "durable-parent-plan-approval", PlanID: "schedule-plan-a", ProjectID: "schedule-project-b",
		Policy: "manual", Status: "pending", RequestedAt: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.SaveExecutionPlanApproval(planApproval); err != nil {
		t.Fatalf("SaveExecutionPlanApproval: %v", err)
	}
	if planApproval.ProjectID != "schedule-project-a" {
		t.Fatalf("plan approval project = %q, want durable plan project", planApproval.ProjectID)
	}
	forgedPlanApproval := *planApproval
	forgedPlanApproval.PlanID = "schedule-plan-b"
	if err := store.SaveExecutionPlanApproval(&forgedPlanApproval); err == nil {
		t.Fatal("SaveExecutionPlanApproval allowed approval ID relocation")
	}
}
