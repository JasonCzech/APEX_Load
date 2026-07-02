package storage

import (
	"encoding/json"
	"testing"
	"time"
)

func TestExecutionPlanApprovalAtomicCAS(t *testing.T) {
	store := openTimeslotStore(t)
	now := time.Now().UTC()
	approval := &ExecutionPlanApproval{ID: "appr-1", PlanID: "plan-1", ProjectID: "p", Policy: "quota_burst", Status: "pending", PlanContentHash: "sha256:abc", RequestedAt: now, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveExecutionPlanApproval(approval); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Resolve CAS: first pending->approved wins; a second resolution loses.
	won, err := store.UpdateExecutionPlanApprovalResolution("appr-1", "approved", "user:admin", "ok", now)
	if err != nil || !won {
		t.Fatalf("first resolve = (%v, %v), want won", won, err)
	}
	won, err = store.UpdateExecutionPlanApprovalResolution("appr-1", "rejected", "user:other", "no", now)
	if err != nil || won {
		t.Fatalf("second resolve = (%v, %v), want lost (already resolved)", won, err)
	}

	// Claim CAS: an approved, unconsumed approval can be claimed exactly once.
	claimed, err := store.ClaimExecutionPlanApproval("appr-1", "run-A", now)
	if err != nil || !claimed {
		t.Fatalf("first claim = (%v, %v), want claimed", claimed, err)
	}
	claimed, err = store.ClaimExecutionPlanApproval("appr-1", "run-B", now)
	if err != nil || claimed {
		t.Fatalf("second claim = (%v, %v), want lost (single-use)", claimed, err)
	}
	got, _ := store.GetExecutionPlanApproval("appr-1")
	if got == nil || got.RunID != "run-A" || got.Status != "approved" || got.PlanContentHash != "sha256:abc" {
		t.Fatalf("approval after claim = %+v, want run-A / approved / plan hash", got)
	}
}

func TestExecutionPlanRoundTrip(t *testing.T) {
	store := openTimeslotStore(t)
	disabled := false
	plan := &ExecutionPlan{
		ID:        "exec-plan-1",
		ProjectID: "proj-a",
		Name:      "nightly regression",
		RunMode:   "sequential",
		Entries: []ExecutionPlanEntry{
			{Order: 0, ScenarioID: "scn-1", ScenarioName: "checkout", RuntimeOverrides: json.RawMessage(`{"think_time_multiplier":2}`)},
			{Order: 1, ScenarioID: "scn-2", Enabled: &disabled, FailurePolicy: "skip"},
		},
	}
	if err := store.SaveExecutionPlan(plan); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := store.GetExecutionPlan("exec-plan-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.RunMode != "sequential" || got.Status != "draft" || len(got.Entries) != 2 {
		t.Fatalf("plan not preserved: %+v", got)
	}
	if !got.Entries[0].IsEnabled() {
		t.Fatalf("entry 0 should be enabled by default")
	}
	if got.Entries[1].IsEnabled() {
		t.Fatalf("entry 1 was explicitly disabled but IsEnabled()=true")
	}
	if string(got.Entries[0].RuntimeOverrides) == "" {
		t.Fatalf("runtime_overrides not preserved")
	}

	// Project isolation in list.
	if err := store.SaveExecutionPlan(&ExecutionPlan{ID: "exec-plan-2", ProjectID: "proj-b", Name: "other"}); err != nil {
		t.Fatalf("save other: %v", err)
	}
	plans, err := store.ListExecutionPlans("proj-a", 10, 0)
	if err != nil || len(plans) != 1 || plans[0].ID != "exec-plan-1" {
		t.Fatalf("list = %+v (err %v), want only exec-plan-1", plans, err)
	}

	if err := store.DeleteExecutionPlan("exec-plan-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := store.GetExecutionPlan("exec-plan-1"); err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestExecutionPlanEntryDataPoolAndTimeslot(t *testing.T) {
	store := openTimeslotStore(t)
	slot := "slot-x"
	plan := &ExecutionPlan{
		ID:        "exec-plan-dp",
		ProjectID: "proj-a",
		Name:      "per-entry overrides",
		RunMode:   "sequential",
		Entries: []ExecutionPlanEntry{
			{
				Order:      0,
				ScenarioID: "scn-1",
				TimeslotID: &slot,
				DataPools: []ScenarioDataPoolBinding{
					{ID: "b1", PoolID: "pool-9", Alias: "users", AccessMode: "shared", SelectionStrategy: "sequential"},
				},
			},
		},
	}
	if err := store.SaveExecutionPlan(plan); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := store.GetExecutionPlan("exec-plan-dp")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.Entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(got.Entries))
	}
	e := got.Entries[0]
	if e.TimeslotID == nil || *e.TimeslotID != "slot-x" {
		t.Fatalf("per-entry timeslot not preserved: %+v", e.TimeslotID)
	}
	if len(e.DataPools) != 1 || e.DataPools[0].PoolID != "pool-9" || e.DataPools[0].Alias != "users" {
		t.Fatalf("per-entry data pools not preserved: %+v", e.DataPools)
	}
}

func TestExecutionPlanRunRoundTrip(t *testing.T) {
	store := openTimeslotStore(t)
	run := &ExecutionPlanRun{
		ID:        "epr-1",
		PlanID:    "exec-plan-1",
		ProjectID: "proj-a",
		RunMode:   "sequential",
		Status:    "running",
		Entries: []ExecutionPlanRunEntry{
			{Order: 0, ScenarioID: "scn-1", ScenarioName: "checkout", TestID: "test-1", Status: "running"},
			{Order: 1, ScenarioID: "scn-2", Status: "pending"},
		},
	}
	if err := store.SaveExecutionPlanRun(run); err != nil {
		t.Fatalf("save run: %v", err)
	}
	got, err := store.GetExecutionPlanRun("epr-1")
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got.Status != "running" || got.PlanID != "exec-plan-1" || len(got.Entries) != 2 {
		t.Fatalf("run not preserved: %+v", got)
	}
	if got.Entries[0].TestID != "test-1" || got.Entries[0].Status != "running" {
		t.Fatalf("entry 0 not preserved: %+v", got.Entries[0])
	}
	if got.CompletedAt != nil {
		t.Fatalf("CompletedAt should be nil for a running run")
	}

	// Upsert: mark complete with a terminal timestamp.
	done := got.CreatedAt
	got.Status = "completed"
	got.Entries[1].Status = "complete"
	got.Entries[1].TestID = "test-2"
	got.CompletedAt = &done
	if err := store.SaveExecutionPlanRun(got); err != nil {
		t.Fatalf("save run (update): %v", err)
	}
	reloaded, err := store.GetExecutionPlanRun("epr-1")
	if err != nil {
		t.Fatalf("get run after update: %v", err)
	}
	if reloaded.Status != "completed" || reloaded.CompletedAt == nil || reloaded.Entries[1].Status != "complete" {
		t.Fatalf("run update not preserved: %+v", reloaded)
	}

	// List by plan, with isolation.
	if err := store.SaveExecutionPlanRun(&ExecutionPlanRun{ID: "epr-2", PlanID: "other-plan", ProjectID: "proj-a", Status: "running"}); err != nil {
		t.Fatalf("save other run: %v", err)
	}
	runs, err := store.ListExecutionPlanRuns("exec-plan-1", 10, 0)
	if err != nil || len(runs) != 1 || runs[0].ID != "epr-1" {
		t.Fatalf("list runs = %+v (err %v), want only epr-1", runs, err)
	}
}
