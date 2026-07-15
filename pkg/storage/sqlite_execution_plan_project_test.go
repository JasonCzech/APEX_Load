package storage

import (
	"path/filepath"
	"testing"
	"time"
)

func TestSQLiteExecutionPlanProjectOwnershipIsImmutable(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	now := time.Now().UTC()
	for _, projectID := range []string{"execution-plan-project-a", "execution-plan-project-b"} {
		if err := store.SaveProject(&Project{ID: projectID, Name: projectID, CreatedAt: now}); err != nil {
			t.Fatalf("SaveProject(%s): %v", projectID, err)
		}
	}
	plan := &ExecutionPlan{ID: "execution-plan-owned-a", ProjectID: "execution-plan-project-a", Name: "Owned A", RunMode: "parallel"}
	if err := store.SaveExecutionPlan(plan); err != nil {
		t.Fatalf("SaveExecutionPlan create: %v", err)
	}
	forged := *plan
	forged.ProjectID = "execution-plan-project-b"
	forged.Name = "Forged B"
	if err := store.SaveExecutionPlan(&forged); err == nil {
		t.Fatal("SaveExecutionPlan allowed project ownership rewrite")
	}
	got, err := store.GetExecutionPlan(plan.ID)
	if err != nil {
		t.Fatalf("GetExecutionPlan: %v", err)
	}
	if got.ProjectID != "execution-plan-project-a" || got.Name != "Owned A" {
		t.Fatalf("rejected rewrite changed durable plan: %#v", got)
	}
	forged.Revision = got.Revision
	if updated, err := store.CompareAndSwapExecutionPlan(&forged, got.Revision); err != nil || updated {
		t.Fatalf("cross-project CAS = (%v, %v), want false, nil", updated, err)
	}
	got, err = store.GetExecutionPlan(plan.ID)
	if err != nil {
		t.Fatalf("GetExecutionPlan after CAS: %v", err)
	}
	if got.ProjectID != "execution-plan-project-a" || got.Name != "Owned A" {
		t.Fatalf("rejected CAS changed durable plan: %#v", got)
	}
}

func TestSQLiteExecutionPlanRunUsesDurablePlanOwnership(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	now := time.Now().UTC()
	for _, plan := range []*ExecutionPlan{
		{ID: "run-plan-a", ProjectID: "run-project-a", Name: "A", RunMode: "parallel", CreatedAt: now, UpdatedAt: now},
		{ID: "run-plan-b", ProjectID: "run-project-b", Name: "B", RunMode: "parallel", CreatedAt: now, UpdatedAt: now},
	} {
		if err := store.SaveExecutionPlan(plan); err != nil {
			t.Fatal(err)
		}
	}
	run := &ExecutionPlanRun{ID: "owned-run", PlanID: "run-plan-a", ProjectID: "run-project-b", RunMode: "parallel", Status: "running", CreatedAt: now}
	if err := store.SaveExecutionPlanRun(run); err != nil {
		t.Fatalf("SaveExecutionPlanRun: %v", err)
	}
	if run.ProjectID != "run-project-a" {
		t.Fatalf("run project = %q, want durable plan project", run.ProjectID)
	}
	forged := *run
	forged.PlanID = "run-plan-b"
	if err := store.SaveExecutionPlanRun(&forged); err == nil {
		t.Fatal("SaveExecutionPlanRun allowed run ID relocation")
	}
	got, err := store.GetExecutionPlanRun(run.ID)
	if err != nil || got.PlanID != "run-plan-a" || got.ProjectID != "run-project-a" {
		t.Fatalf("durable run = %#v, err=%v", got, err)
	}
}
