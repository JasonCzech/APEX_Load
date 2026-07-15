package storage

import (
	"strings"
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

func TestScenarioScheduleReleaseUsesDurableProjectParent(t *testing.T) {
	store := openTimeslotStore(t)
	now := time.Now().UTC()
	for _, projectID := range []string{"schedule-release-project-a", "schedule-release-project-b"} {
		if err := store.SaveProject(&Project{ID: projectID, Name: projectID, CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
		applicationID := "application-" + projectID
		if err := store.SaveApplication(&Application{ID: applicationID, ProjectID: projectID, Name: applicationID, CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
		for _, suffix := range []string{"active", "archived"} {
			releaseID := "release-" + projectID + "-" + suffix
			if err := store.SaveRelease(&Release{ID: releaseID, ApplicationID: applicationID, ProjectID: projectID, Name: releaseID, CreatedAt: now}); err != nil {
				t.Fatal(err)
			}
			if suffix == "archived" {
				if err := store.ArchiveRelease(releaseID); err != nil {
					t.Fatal(err)
				}
			}
		}
		script := &dsl.Script{ID: "script-" + projectID, ProjectID: projectID, Name: projectID, Protocol: "http"}
		if err := store.SaveScript(script); err != nil {
			t.Fatal(err)
		}
		if err := store.SaveScenario(&Scenario{ID: "scenario-" + projectID, Name: projectID, Duration: "1m", CreatedAt: now}, []ScenarioScript{{ScriptID: script.ID, VUsers: 1}}); err != nil {
			t.Fatal(err)
		}
	}

	base := ScenarioSchedule{
		ID: "schedule-release-parent", ScenarioID: "scenario-schedule-release-project-a",
		ProjectID: "schedule-release-project-b", ReleaseID: "release-schedule-release-project-a-active",
		Name: "Project A schedule", CronExpr: "* * * * *", Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.SaveScenarioSchedule(&base); err != nil {
		t.Fatalf("SaveScenarioSchedule(valid release): %v", err)
	}
	if base.ProjectID != "schedule-release-project-a" {
		t.Fatalf("schedule project = %q, want durable scenario/release project", base.ProjectID)
	}

	for _, tc := range []struct {
		name      string
		releaseID string
		want      string
	}{
		{name: "missing", releaseID: "missing-release", want: "does not exist"},
		{name: "foreign project", releaseID: "release-schedule-release-project-b-active", want: "does not belong"},
		{name: "archived", releaseID: "release-schedule-release-project-a-archived", want: "is archived"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := base
			candidate.ID = "schedule-release-" + strings.ReplaceAll(tc.name, " ", "-")
			candidate.ReleaseID = tc.releaseID
			if err := store.SaveScenarioSchedule(&candidate); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("SaveScenarioSchedule error = %v, want %q", err, tc.want)
			}
		})
	}

	approvalSchedule := base
	approvalSchedule.ID = "schedule-release-approval"
	approvalSchedule.ReleaseID = "release-schedule-release-project-b-active"
	approvalSchedule.PendingApprovalID = "schedule-release-approval-request"
	approval := &ScenarioScheduleApproval{
		ID: approvalSchedule.PendingApprovalID, ScheduleID: approvalSchedule.ID,
		Policy: "manual", Status: "pending", RequestedAt: now, CreatedAt: now, UpdatedAt: now,
	}
	if changed, err := store.SaveScenarioScheduleWithApproval(&approvalSchedule, approval, 0); err == nil || changed || !strings.Contains(err.Error(), "does not belong") {
		t.Fatalf("SaveScenarioScheduleWithApproval = (%v, %v), want foreign-release rejection", changed, err)
	}
}
