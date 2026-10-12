package storage

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExecutionPlanWorkspaceStorage(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	exerciseExecutionPlanWorkspaceStorage(t, store)
}

// exerciseExecutionPlanWorkspaceStorage round-trips where a plan files its
// runs, its overlap policy, a schedule's release and a run's plan link, and
// checks that a release a plan or schedule files under cannot be deleted.
func exerciseExecutionPlanWorkspaceStorage(t *testing.T, store *SQLiteStore) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	if err := store.SaveProject(&Project{ID: "plan-ws-project", Name: "Plans", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	if err := store.SaveApplication(&Application{ID: "plan-ws-app", ProjectID: "plan-ws-project", Name: "Storefront", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveApplication: %v", err)
	}
	for _, id := range []string{"plan-ws-release", "plan-ws-schedule-release"} {
		if err := store.SaveRelease(&Release{ID: id, ApplicationID: "plan-ws-app", ProjectID: "plan-ws-project", Name: id, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatalf("SaveRelease(%s): %v", id, err)
		}
	}
	plan := &ExecutionPlan{
		ID: "plan-ws", ProjectID: "plan-ws-project", Name: "Nightly", RunMode: "sequential",
		ApplicationID: " plan-ws-app ", ReleaseID: "plan-ws-release", OverlapPolicy: " Queue ",
		Entries: []ExecutionPlanEntry{{Order: 0, ScenarioID: "scenario"}}, CreatedAt: now, UpdatedAt: now,
	}
	created, err := store.CreateExecutionPlanIfAbsent(plan)
	if err != nil || !created {
		t.Fatalf("CreateExecutionPlanIfAbsent = %v, %v", created, err)
	}
	loaded, err := store.GetExecutionPlan(plan.ID)
	if err != nil {
		t.Fatalf("GetExecutionPlan: %v", err)
	}
	if loaded.ApplicationID != "plan-ws-app" || loaded.ReleaseID != "plan-ws-release" || loaded.OverlapPolicy != ExecutionPlanOverlapQueue {
		t.Fatalf("stored plan placement = %q/%q overlap %q", loaded.ApplicationID, loaded.ReleaseID, loaded.OverlapPolicy)
	}
	loaded.OverlapPolicy = ExecutionPlanOverlapSkip
	loaded.ReleaseID, loaded.ApplicationID = "", ""
	if swapped, err := store.CompareAndSwapExecutionPlan(loaded, loaded.Revision); err != nil || !swapped {
		t.Fatalf("CompareAndSwapExecutionPlan = %v, %v", swapped, err)
	}
	if again, _ := store.GetExecutionPlan(plan.ID); again.ReleaseID != "" || again.OverlapPolicy != ExecutionPlanOverlapSkip {
		t.Fatalf("after clearing the release = %+v", again)
	}
	loaded.ReleaseID, loaded.ApplicationID = "plan-ws-release", "plan-ws-app"
	if err := store.SaveExecutionPlan(loaded); err != nil {
		t.Fatalf("SaveExecutionPlan: %v", err)
	}

	next := now.Add(time.Hour)
	schedule := &ExecutionPlanSchedule{
		ID: "plan-ws-schedule", PlanID: plan.ID, ProjectID: "plan-ws-project", Name: "Nightly", CronExpr: "0 2 * * *",
		Timezone: "UTC", ReleaseID: "plan-ws-schedule-release", Enabled: true, NextRunAt: &next, CreatedAt: now, UpdatedAt: now,
	}
	if created, err := store.CreateExecutionPlanScheduleIfAbsent(schedule); err != nil || !created {
		t.Fatalf("CreateExecutionPlanScheduleIfAbsent = %v, %v", created, err)
	}
	storedSchedule, err := store.GetExecutionPlanSchedule(schedule.ID)
	if err != nil || storedSchedule.ReleaseID != "plan-ws-schedule-release" {
		t.Fatalf("stored schedule = %+v, %v", storedSchedule, err)
	}
	storedSchedule.ReleaseID = ""
	if updated, err := store.UpdateExecutionPlanScheduleIfRevision(storedSchedule, storedSchedule.Revision); err != nil || !updated {
		t.Fatalf("UpdateExecutionPlanScheduleIfRevision = %v, %v", updated, err)
	}
	if again, _ := store.GetExecutionPlanSchedule(schedule.ID); again.ReleaseID != "" {
		t.Fatalf("schedule release after clearing = %q", again.ReleaseID)
	}
	storedSchedule.ReleaseID = "plan-ws-schedule-release"
	if err := store.SaveExecutionPlanSchedule(storedSchedule); err != nil {
		t.Fatalf("SaveExecutionPlanSchedule: %v", err)
	}

	link := map[string]any{"plan_id": plan.ID, "plan_name": "Nightly", "plan_run_id": "plan-ws-run", "entry_order": 0, "entry_number": 1, "entry_count": 1}
	if err := store.SaveTest(&TestRecord{
		ID: "plan-ws-test", Name: "Nightly / scenario", ProjectID: "plan-ws-project", ApplicationID: "plan-ws-app", ReleaseID: "plan-ws-release",
		Status: "COMPLETE", StartTime: now, Groups: []any{}, ExecutionPlan: link,
	}); err != nil {
		t.Fatalf("SaveTest: %v", err)
	}
	record, err := store.GetTest("plan-ws-test")
	if err != nil {
		t.Fatalf("GetTest: %v", err)
	}
	stored, ok := record.ExecutionPlan.(map[string]any)
	if !ok || stored["plan_run_id"] != "plan-ws-run" || stored["plan_name"] != "Nightly" {
		t.Fatalf("stored plan link = %#v", record.ExecutionPlan)
	}
	unlinked := *record
	unlinked.ID, unlinked.ExecutionPlan = "plan-ws-unlinked", nil
	if err := store.SaveTest(&unlinked); err != nil {
		t.Fatalf("SaveTest(unlinked): %v", err)
	}
	if again, _ := store.GetTest("plan-ws-unlinked"); again.ExecutionPlan != nil {
		t.Fatalf("a run without a plan decoded a link: %#v", again.ExecutionPlan)
	}

	// The schedule's release cannot be deleted while the schedule files under it,
	// nor the application while its plan does.
	if err := store.DeleteRelease("plan-ws-schedule-release"); err == nil || !strings.Contains(err.Error(), "schedules") {
		t.Fatalf("deleting a schedule's release = %v, want refused", err)
	}
	if err := store.ArchiveRelease("plan-ws-schedule-release"); err == nil {
		t.Fatal("archiving a schedule's release should be refused")
	}
	if err := store.DeleteApplication("plan-ws-app"); err == nil {
		t.Fatal("deleting an application a plan files under should be refused")
	}
}
