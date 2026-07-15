package storage

import (
	"path/filepath"
	"testing"
	"time"
)

func TestExecutionPlanMutationsRejectPriorLeaseTermAfterTakeover(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "execution-plan-lease-term.db")
	oldStore, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(old): %v", err)
	}
	defer oldStore.Close()
	newStore, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(new): %v", err)
	}
	defer newStore.Close()

	const (
		leaseName = "controller-execution-leader"
		oldOwner  = "controller-old/term"
		newOwner  = "controller-new/term"
	)
	now := time.Now().UTC()
	acquired, oldTerm, err := oldStore.TryAcquireSchedulerLeaseTerm(leaseName, oldOwner, now, time.Minute)
	if err != nil || !acquired || oldTerm == nil {
		t.Fatalf("acquire old term = (%v, %#v, %v)", acquired, oldTerm, err)
	}

	plan := &ExecutionPlan{ID: "lease-plan", ProjectID: "project-a", Name: "original", RunMode: "sequential", CreatedAt: now, UpdatedAt: now}
	if saved, err := oldStore.SaveExecutionPlanIfLeaseTermHeld(plan, leaseName, oldOwner, oldTerm.Generation); err != nil || !saved {
		t.Fatalf("save plan under old term = (%v, %v)", saved, err)
	}
	schedule := &ExecutionPlanSchedule{
		ID: "lease-schedule", PlanID: plan.ID, ProjectID: plan.ProjectID, Revision: 1, Name: "original",
		CronExpr: "0 * * * *", Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	if created, err := oldStore.CreateExecutionPlanScheduleIfAbsentAndLeaseTermHeld(schedule, leaseName, oldOwner, oldTerm.Generation); err != nil || !created {
		t.Fatalf("create schedule under old term = (%v, %v)", created, err)
	}
	plannedAt := now.Add(time.Hour).Truncate(time.Second)
	occurrence := &ExecutionPlanScheduleOccurrence{
		ID: "lease-occurrence", ScheduleID: schedule.ID, PlanID: plan.ID, ProjectID: plan.ProjectID,
		ScheduleRevision: 1, Revision: 1, PlannedAt: plannedAt, Status: "materializing", ReasonCode: "due",
		RunID: "lease-run", TransitionToken: "claim-token", CreatedAt: now, UpdatedAt: now,
	}
	if created, err := oldStore.CreateExecutionPlanScheduleOccurrenceIfAbsentAndLeaseTermHeld(occurrence, leaseName, oldOwner, oldTerm.Generation); err != nil || !created {
		t.Fatalf("create occurrence under old term = (%v, %v)", created, err)
	}
	run := &ExecutionPlanRun{
		ID: "lease-run", PlanID: plan.ID, ProjectID: plan.ProjectID, RunMode: "sequential", Status: "running",
		Entries:         []ExecutionPlanRunEntry{{Order: 0, ScenarioID: "scenario-a", Status: "pending"}},
		TransitionToken: "anchor-token", CreatedAt: now, UpdatedAt: now,
	}
	if created, err := oldStore.CreateExecutionPlanRunIfAbsentAndLeaseTermHeld(run, leaseName, oldOwner, oldTerm.Generation); err != nil || !created {
		t.Fatalf("create run under old term = (%v, %v)", created, err)
	}
	approval := &ExecutionPlanApproval{
		ID: "lease-approval", PlanID: plan.ID, ProjectID: plan.ProjectID, Policy: "quota_burst", Status: "approved",
		RequestedAt: now, CreatedAt: now, UpdatedAt: now,
	}
	if saved, err := oldStore.SaveExecutionPlanApprovalIfLeaseTermHeld(approval, leaseName, oldOwner, oldTerm.Generation); err != nil || !saved {
		t.Fatalf("save approval under old term = (%v, %v)", saved, err)
	}
	pendingApproval := &ExecutionPlanApproval{
		ID: "lease-pending-approval", PlanID: plan.ID, ProjectID: plan.ProjectID, Policy: "quota_burst", Status: "pending",
		RequestedAt: now, CreatedAt: now, UpdatedAt: now,
	}
	if saved, err := oldStore.SaveExecutionPlanApprovalIfLeaseTermHeld(pendingApproval, leaseName, oldOwner, oldTerm.Generation); err != nil || !saved {
		t.Fatalf("save pending approval under old term = (%v, %v)", saved, err)
	}

	if err := oldStore.ReleaseSchedulerLease(leaseName, oldOwner); err != nil {
		t.Fatalf("release old term: %v", err)
	}
	acquired, newTerm, err := newStore.TryAcquireSchedulerLeaseTerm(leaseName, newOwner, now, time.Minute)
	if err != nil || !acquired || newTerm == nil {
		t.Fatalf("acquire successor term = (%v, %#v, %v)", acquired, newTerm, err)
	}
	if newTerm.Generation <= oldTerm.Generation {
		t.Fatalf("successor generation = %d, want > %d", newTerm.Generation, oldTerm.Generation)
	}

	// Entry claim: use the current run revision so only the obsolete lease term,
	// not revision CAS, can reject the delayed old-controller mutation.
	claim, err := oldStore.GetExecutionPlanRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	claim.Entries[0].Status = "running"
	claim.Entries[0].TestID = "deterministic-entry-test"
	claim.TransitionToken = "old-entry-claim"
	if updated, err := oldStore.CompareAndSwapExecutionPlanRunIfLeaseTermHeld(claim, claim.Revision, leaseName, oldOwner, oldTerm.Generation); err != nil || updated {
		t.Fatalf("stale entry claim = (%v, %v), want fenced", updated, err)
	}
	claim, err = newStore.GetExecutionPlanRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	claim.Entries[0].Status = "running"
	claim.Entries[0].TestID = "deterministic-entry-test"
	claim.TransitionToken = "new-entry-claim"
	if updated, err := newStore.CompareAndSwapExecutionPlanRunIfLeaseTermHeld(claim, claim.Revision, leaseName, newOwner, newTerm.Generation); err != nil || !updated {
		t.Fatalf("successor entry claim = (%v, %v), want updated", updated, err)
	}

	// Entry failure: first commit the successor transition, then give the old
	// controller a freshly loaded revision. The complete lease term must still
	// prevent it from overwriting the successor's result.
	failed, err := newStore.GetExecutionPlanRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	failed.Status = "halted"
	failed.Entries[0].Status = "failed"
	failed.Entries[0].Detail = "successor terminalized entry"
	failed.TransitionToken = "new-entry-failure"
	completedAt := now.Add(time.Second)
	failed.CompletedAt = &completedAt
	if updated, err := newStore.CompareAndSwapExecutionPlanRunIfLeaseTermHeld(failed, failed.Revision, leaseName, newOwner, newTerm.Generation); err != nil || !updated {
		t.Fatalf("successor entry failure = (%v, %v), want updated", updated, err)
	}
	staleFailure, err := oldStore.GetExecutionPlanRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	staleFailure.Status = "running"
	staleFailure.Entries[0].Status = "complete"
	staleFailure.CompletedAt = nil
	staleFailure.TransitionToken = "old-entry-overwrite"
	if updated, err := oldStore.CompareAndSwapExecutionPlanRunIfLeaseTermHeld(staleFailure, staleFailure.Revision, leaseName, oldOwner, oldTerm.Generation); err != nil || updated {
		t.Fatalf("stale entry overwrite = (%v, %v), want fenced", updated, err)
	}
	persistedRun, err := newStore.GetExecutionPlanRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persistedRun.Status != "halted" || persistedRun.Entries[0].Status != "failed" || persistedRun.Entries[0].TestID != "deterministic-entry-test" {
		t.Fatalf("stale term regressed run: %+v", persistedRun)
	}

	// Occurrence finalization is term-fenced even at the current revision.
	staleOccurrence, err := oldStore.GetExecutionPlanScheduleOccurrenceByScheduleAndPlannedAt(schedule.ID, plannedAt)
	if err != nil {
		t.Fatal(err)
	}
	staleOccurrence.Status = "materialized"
	staleOccurrence.ReasonCode = "old-finalize"
	staleOccurrence.TransitionToken = "old-occurrence-finalize"
	if updated, err := oldStore.CompareAndSwapExecutionPlanScheduleOccurrenceIfLeaseTermHeld(staleOccurrence, staleOccurrence.Revision, leaseName, oldOwner, oldTerm.Generation); err != nil || updated {
		t.Fatalf("stale occurrence finalize = (%v, %v), want fenced", updated, err)
	}
	finalizedOccurrence, err := newStore.GetExecutionPlanScheduleOccurrenceByScheduleAndPlannedAt(schedule.ID, plannedAt)
	if err != nil {
		t.Fatal(err)
	}
	finalizedOccurrence.Status = "failed"
	finalizedOccurrence.ReasonCode = "materialization_interrupted"
	finalizedOccurrence.TransitionToken = "new-occurrence-finalize"
	if updated, err := newStore.CompareAndSwapExecutionPlanScheduleOccurrenceIfLeaseTermHeld(finalizedOccurrence, finalizedOccurrence.Revision, leaseName, newOwner, newTerm.Generation); err != nil || !updated {
		t.Fatalf("successor occurrence finalize = (%v, %v), want updated", updated, err)
	}

	// Schedule advance/update is protected by the same term as its occurrence.
	staleSchedule, err := oldStore.GetExecutionPlanSchedule(schedule.ID)
	if err != nil {
		t.Fatal(err)
	}
	staleSchedule.LastRunRunID = "old-run"
	staleSchedule.Name = "stale update"
	if updated, err := oldStore.UpdateExecutionPlanScheduleIfRevisionAndLeaseTermHeld(staleSchedule, staleSchedule.Revision, leaseName, oldOwner, oldTerm.Generation); err != nil || updated {
		t.Fatalf("stale schedule advance = (%v, %v), want fenced", updated, err)
	}
	successorSchedule, err := newStore.GetExecutionPlanSchedule(schedule.ID)
	if err != nil {
		t.Fatal(err)
	}
	successorSchedule.LastRunRunID = run.ID
	successorSchedule.Name = "successor update"
	nextRunAt := plannedAt.Add(time.Hour)
	successorSchedule.NextRunAt = &nextRunAt
	if updated, err := newStore.UpdateExecutionPlanScheduleIfRevisionAndLeaseTermHeld(successorSchedule, successorSchedule.Revision, leaseName, newOwner, newTerm.Generation); err != nil || !updated {
		t.Fatalf("successor schedule advance = (%v, %v), want updated", updated, err)
	}

	// Operator plan and approval mutations are fenced transactionally too.
	stalePlan, err := oldStore.GetExecutionPlan(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	stalePlan.Name = "stale plan update"
	if saved, err := oldStore.SaveExecutionPlanIfLeaseTermHeld(stalePlan, leaseName, oldOwner, oldTerm.Generation); err != nil || saved {
		t.Fatalf("stale plan update = (%v, %v), want fenced", saved, err)
	}
	successorPlan, err := newStore.GetExecutionPlan(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	successorPlan.Name = "successor plan update"
	if saved, err := newStore.SaveExecutionPlanIfLeaseTermHeld(successorPlan, leaseName, newOwner, newTerm.Generation); err != nil || !saved {
		t.Fatalf("successor plan update = (%v, %v), want saved", saved, err)
	}
	if claimed, err := oldStore.ClaimExecutionPlanApprovalIfLeaseTermHeld(approval.ID, run.ID, now, leaseName, oldOwner, oldTerm.Generation); err != nil || claimed {
		t.Fatalf("stale approval claim = (%v, %v), want fenced", claimed, err)
	}
	if claimed, err := newStore.ClaimExecutionPlanApprovalIfLeaseTermHeld(approval.ID, run.ID, now, leaseName, newOwner, newTerm.Generation); err != nil || !claimed {
		t.Fatalf("successor approval claim = (%v, %v), want claimed", claimed, err)
	}
	if resolved, err := oldStore.UpdateExecutionPlanApprovalResolutionAndContentHashIfLeaseTermHeld(pendingApproval.ID, "approved", "old", "stale", "sha256:current", now, leaseName, oldOwner, oldTerm.Generation); err != nil || resolved {
		t.Fatalf("stale approval resolution = (%v, %v), want fenced", resolved, err)
	}
	if resolved, err := newStore.UpdateExecutionPlanApprovalResolutionAndContentHashIfLeaseTermHeld(pendingApproval.ID, "approved", "new", "ok", "sha256:current", now, leaseName, newOwner, newTerm.Generation); err != nil || !resolved {
		t.Fatalf("successor approval resolution = (%v, %v), want updated", resolved, err)
	}
	resolvedApproval, err := newStore.GetExecutionPlanApproval(pendingApproval.ID)
	if err != nil {
		t.Fatal(err)
	}
	if resolvedApproval.Status != "approved" || resolvedApproval.PlanContentHash != "sha256:current" {
		t.Fatalf("atomic approval resolution = %+v", resolvedApproval)
	}
	staleNewApproval := &ExecutionPlanApproval{
		ID: "stale-new-approval", PlanID: plan.ID, ProjectID: plan.ProjectID, Policy: "quota_burst", Status: "pending",
		RequestedAt: now, CreatedAt: now, UpdatedAt: now,
	}
	if saved, err := oldStore.SaveExecutionPlanApprovalIfLeaseTermHeld(staleNewApproval, leaseName, oldOwner, oldTerm.Generation); err != nil || saved {
		t.Fatalf("stale approval create = (%v, %v), want fenced", saved, err)
	}
	if got, err := newStore.GetExecutionPlanApproval(staleNewApproval.ID); err != nil || got != nil {
		t.Fatalf("stale approval create persisted = (%#v, %v)", got, err)
	}

	// Create/delete CRUD paths also reject the obsolete term rather than
	// resurrecting or removing successor-owned objects.
	lateSchedule := &ExecutionPlanSchedule{
		ID: "late-schedule", PlanID: plan.ID, ProjectID: plan.ProjectID, Revision: 1, Name: "late",
		CronExpr: "30 * * * *", Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	if created, err := oldStore.CreateExecutionPlanScheduleIfAbsentAndLeaseTermHeld(lateSchedule, leaseName, oldOwner, oldTerm.Generation); err != nil || created {
		t.Fatalf("stale schedule create = (%v, %v), want fenced", created, err)
	}
	if created, err := newStore.CreateExecutionPlanScheduleIfAbsentAndLeaseTermHeld(lateSchedule, leaseName, newOwner, newTerm.Generation); err != nil || !created {
		t.Fatalf("successor schedule create = (%v, %v), want created", created, err)
	}
	if deleted, err := oldStore.DeleteExecutionPlanScheduleIfLeaseTermHeld(lateSchedule.ID, leaseName, oldOwner, oldTerm.Generation); err != nil || deleted {
		t.Fatalf("stale schedule delete = (%v, %v), want fenced", deleted, err)
	}
	if deleted, err := newStore.DeleteExecutionPlanScheduleIfLeaseTermHeld(lateSchedule.ID, leaseName, newOwner, newTerm.Generation); err != nil || !deleted {
		t.Fatalf("successor schedule delete = (%v, %v), want deleted", deleted, err)
	}
	deletePlan := &ExecutionPlan{ID: "delete-plan", ProjectID: "project-a", Name: "delete", RunMode: "parallel", CreatedAt: now, UpdatedAt: now}
	if saved, err := newStore.SaveExecutionPlanIfLeaseTermHeld(deletePlan, leaseName, newOwner, newTerm.Generation); err != nil || !saved {
		t.Fatalf("successor plan create = (%v, %v), want saved", saved, err)
	}
	if deleted, err := oldStore.DeleteExecutionPlanIfLeaseTermHeld(deletePlan.ID, leaseName, oldOwner, oldTerm.Generation); err != nil || deleted {
		t.Fatalf("stale plan delete = (%v, %v), want fenced", deleted, err)
	}
	if deleted, err := newStore.DeleteExecutionPlanIfLeaseTermHeld(deletePlan.ID, leaseName, newOwner, newTerm.Generation); err != nil || !deleted {
		t.Fatalf("successor plan delete = (%v, %v), want deleted", deleted, err)
	}
}
