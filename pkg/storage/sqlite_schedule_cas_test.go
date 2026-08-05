package storage

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestSQLiteScenarioScheduleRevisionCASPreventsOverwriteAndResurrection(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	if err := store.SaveScenario(&Scenario{ID: "scenario-a", Name: "Scenario A", Duration: "1m", CreatedAt: now}, nil); err != nil {
		t.Fatalf("SaveScenario: %v", err)
	}
	schedule := &ScenarioSchedule{ID: "scenario-cas", ScenarioID: "scenario-a", ProjectID: DefaultProjectID, Revision: 1, Name: "A", CronExpr: "0 * * * *", Enabled: true, CreatedAt: now, UpdatedAt: now}
	created, err := store.CreateScenarioScheduleIfAbsent(schedule)
	if err != nil || !created {
		t.Fatalf("CreateScenarioScheduleIfAbsent = %v, %v", created, err)
	}
	if duplicate, err := store.CreateScenarioScheduleIfAbsent(schedule); err != nil || duplicate {
		t.Fatalf("duplicate create = %v, %v; want false, nil", duplicate, err)
	}
	updated := *schedule
	updated.Name = "B"
	if changed, err := store.UpdateScenarioScheduleIfRevision(&updated, 1); err != nil || !changed {
		t.Fatalf("CAS update = %v, %v", changed, err)
	}
	if updated.Revision != 2 {
		t.Fatalf("CAS update revision = %d, want 2", updated.Revision)
	}
	stale := *schedule
	stale.Name = "stale"
	if changed, err := store.UpdateScenarioScheduleIfRevision(&stale, 1); err != nil || changed {
		t.Fatalf("stale update = %v, %v; want false, nil", changed, err)
	}
	if err := store.DeleteScenarioSchedule(schedule.ID); err != nil {
		t.Fatalf("DeleteScenarioSchedule: %v", err)
	}
	if changed, err := store.UpdateScenarioScheduleIfRevision(&updated, 2); err != nil || changed {
		t.Fatalf("deleted update = %v, %v; want false, nil", changed, err)
	}
	if loaded, err := store.GetScenarioSchedule(schedule.ID); err == nil || loaded != nil {
		t.Fatalf("deleted schedule was resurrected: %#v, %v", loaded, err)
	}
}

func TestSQLiteScenarioScheduleRevisionGuardedDeleteRejectsNewerRevision(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	if err := store.SaveScenario(&Scenario{ID: "scenario-delete-cas", Name: "Scenario", Duration: "1m", CreatedAt: now}, nil); err != nil {
		t.Fatalf("SaveScenario: %v", err)
	}
	schedule := &ScenarioSchedule{ID: "schedule-delete-cas", ScenarioID: "scenario-delete-cas", ProjectID: DefaultProjectID, Revision: 1, Name: "A", CronExpr: "0 * * * *", Enabled: true, CreatedAt: now, UpdatedAt: now}
	if created, err := store.CreateScenarioScheduleIfAbsent(schedule); err != nil || !created {
		t.Fatalf("CreateScenarioScheduleIfAbsent = (%v, %v)", created, err)
	}
	updated := *schedule
	updated.Name = "newer schedule"
	if changed, err := store.UpdateScenarioScheduleIfRevision(&updated, schedule.Revision); err != nil || !changed {
		t.Fatalf("UpdateScenarioScheduleIfRevision = (%v, %v)", changed, err)
	}
	result, err := store.DeleteScenarioScheduleGuardedIfRevision(schedule.ID, schedule.Revision)
	if err != nil || result != ScheduleDeleteMissing {
		t.Fatalf("stale guarded delete = (%q, %v), want missing without deletion", result, err)
	}
	retained, err := store.GetScenarioSchedule(schedule.ID)
	if err != nil || retained == nil || retained.Name != "newer schedule" {
		t.Fatalf("schedule after stale guarded delete = (%#v, %v)", retained, err)
	}
	result, err = store.DeleteScenarioScheduleGuardedIfRevision(schedule.ID, updated.Revision)
	if err != nil || result != ScheduleDeleteDeleted {
		t.Fatalf("current guarded delete = (%q, %v), want deleted", result, err)
	}
}

func TestSQLiteScenarioScheduleApprovalResolutionIsAtomic(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	if err := store.SaveScenario(&Scenario{ID: "scenario-a", Name: "Scenario A", Duration: "1m", CreatedAt: now}, nil); err != nil {
		t.Fatalf("SaveScenario: %v", err)
	}
	schedule := &ScenarioSchedule{ID: "schedule-approval", ScenarioID: "scenario-a", ProjectID: DefaultProjectID, Revision: 1, Name: "A", CronExpr: "0 * * * *", Enabled: true, PendingApprovalID: "approval-a", CreatedAt: now, UpdatedAt: now}
	if created, err := store.CreateScenarioScheduleIfAbsent(schedule); err != nil || !created {
		t.Fatalf("create schedule = %v, %v", created, err)
	}
	pending := &ScenarioScheduleApproval{ID: "approval-a", ScheduleID: schedule.ID, ScenarioID: schedule.ScenarioID, ProjectID: DefaultProjectID, ScheduleRevision: 1, Policy: "project_admin", Status: "pending", RequestedAt: now, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveScenarioScheduleApproval(pending); err != nil {
		t.Fatalf("SaveScenarioScheduleApproval: %v", err)
	}
	type result struct {
		status  string
		changed bool
		err     error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for _, status := range []string{"approved", "rejected"} {
		status := status
		wg.Add(1)
		go func() {
			defer wg.Done()
			approval := *pending
			approval.Status = status
			approval.ResolvedBy = status + "-admin"
			approval.ResolvedAt = &now
			approval.UpdatedAt = now
			updatedSchedule := *schedule
			updatedSchedule.PendingApprovalID = ""
			if status == "rejected" {
				updatedSchedule.NextRequiredAction = "approval_rejected"
				updatedSchedule.LastError = "schedule approval rejected"
			}
			changed, err := store.ResolveScenarioScheduleApproval(&approval, &updatedSchedule, 1)
			results <- result{status: status, changed: changed, err: err}
		}()
	}
	wg.Wait()
	close(results)
	winners := 0
	for result := range results {
		if result.err != nil {
			t.Fatalf("resolve %s: %v", result.status, result.err)
		}
		if result.changed {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("successful resolutions = %d, want 1", winners)
	}
	approval, err := store.GetScenarioScheduleApproval(pending.ID)
	if err != nil {
		t.Fatalf("GetScenarioScheduleApproval: %v", err)
	}
	loadedSchedule, err := store.GetScenarioSchedule(schedule.ID)
	if err != nil {
		t.Fatalf("GetScenarioSchedule: %v", err)
	}
	if loadedSchedule.PendingApprovalID != "" {
		t.Fatalf("pending approval id = %q, want cleared", loadedSchedule.PendingApprovalID)
	}
	if loadedSchedule.Revision != 2 {
		t.Fatalf("approval resolution revision = %d, want 2", loadedSchedule.Revision)
	}
	if approval.Status == "approved" && loadedSchedule.NextRequiredAction != "" {
		t.Fatalf("approved resolution left action %q", loadedSchedule.NextRequiredAction)
	}
	if approval.Status == "rejected" && loadedSchedule.NextRequiredAction != "approval_rejected" {
		t.Fatalf("rejected resolution action = %q", loadedSchedule.NextRequiredAction)
	}
}

func TestSQLiteScheduleUpdateSupersedesPreviousPendingApproval(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	if err := store.SaveScenario(&Scenario{ID: "scenario", Name: "Scenario", Duration: "1m", CreatedAt: now}, nil); err != nil {
		t.Fatal(err)
	}
	firstSchedule := &ScenarioSchedule{ID: "schedule", ScenarioID: "scenario", ProjectID: DefaultProjectID, Revision: 1, Name: "Schedule", CronExpr: "0 * * * *", Enabled: true, PendingApprovalID: "approval-1", CreatedAt: now, UpdatedAt: now}
	firstApproval := &ScenarioScheduleApproval{ID: "approval-1", ScheduleID: "schedule", ScenarioID: "scenario", ProjectID: DefaultProjectID, ScheduleRevision: 1, Policy: "manual", Status: "pending", RequestedAt: now, CreatedAt: now, UpdatedAt: now}
	if changed, err := store.SaveScenarioScheduleWithApproval(firstSchedule, firstApproval, 0); err != nil || !changed {
		t.Fatalf("first save = %v, %v", changed, err)
	}
	secondSchedule := *firstSchedule
	secondSchedule.PendingApprovalID = "approval-2"
	secondSchedule.UpdatedAt = now.Add(time.Minute)
	secondApproval := &ScenarioScheduleApproval{ID: "approval-2", ScheduleID: "schedule", ScenarioID: "scenario", ProjectID: DefaultProjectID, Policy: "manual", Status: "pending", RequestedAt: secondSchedule.UpdatedAt, CreatedAt: secondSchedule.UpdatedAt, UpdatedAt: secondSchedule.UpdatedAt}
	if changed, err := store.SaveScenarioScheduleWithApproval(&secondSchedule, secondApproval, 1); err != nil || !changed {
		t.Fatalf("second save = %v, %v", changed, err)
	}
	old, err := store.GetScenarioScheduleApproval("approval-1")
	if err != nil {
		t.Fatal(err)
	}
	if old.Status != "superseded" || old.ResolvedAt == nil {
		t.Fatalf("old approval = %+v, want superseded and resolved", old)
	}
}

func TestSQLiteExecutionPlanScheduleRevisionCASPreventsOverwriteAndResurrection(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	if err := store.SaveExecutionPlan(&ExecutionPlan{ID: "plan-cas", ProjectID: DefaultProjectID, Name: "Plan", RunMode: "parallel", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveExecutionPlan: %v", err)
	}
	schedule := &ExecutionPlanSchedule{ID: "plan-schedule-cas", PlanID: "plan-cas", ProjectID: DefaultProjectID, Revision: 1, Name: "A", CronExpr: "0 * * * *", Enabled: true, CreatedAt: now, UpdatedAt: now}
	created, err := store.CreateExecutionPlanScheduleIfAbsent(schedule)
	if err != nil || !created {
		t.Fatalf("CreateExecutionPlanScheduleIfAbsent = %v, %v", created, err)
	}
	updated := *schedule
	updated.Name = "B"
	if changed, err := store.UpdateExecutionPlanScheduleIfRevision(&updated, 1); err != nil || !changed {
		t.Fatalf("CAS update = %v, %v", changed, err)
	}
	if updated.Revision != 2 {
		t.Fatalf("CAS update revision = %d, want 2", updated.Revision)
	}
	if changed, err := store.UpdateExecutionPlanScheduleIfRevision(schedule, 1); err != nil || changed {
		t.Fatalf("stale update = %v, %v; want false, nil", changed, err)
	}
	if err := store.DeleteExecutionPlanSchedule(schedule.ID); err != nil {
		t.Fatalf("DeleteExecutionPlanSchedule: %v", err)
	}
	if changed, err := store.UpdateExecutionPlanScheduleIfRevision(&updated, 2); err != nil || changed {
		t.Fatalf("deleted update = %v, %v; want false, nil", changed, err)
	}
}
