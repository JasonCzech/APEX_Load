package storage

import (
	"encoding/json"
	"testing"
)

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
