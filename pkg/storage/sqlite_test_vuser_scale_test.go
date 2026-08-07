package storage

import (
	"path/filepath"
	"testing"
	"time"
)

// TestUpdateTestVUserScaleWritesOnlyWorkloadColumns pins the narrow durable
// write a VUser scale reservation uses instead of the full-row upsert: the
// workload columns move, every other lifecycle column is left exactly as the
// previous full-row write left it, and a missing row reports "not updated"
// instead of resurrecting a deleted run.
func TestUpdateTestVUserScaleWritesOnlyWorkloadColumns(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "vuser-scale.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC().Truncate(time.Second)
	record := &TestRecord{
		ID:          "scale-narrow",
		Name:        "Scale Narrow",
		Status:      "RUNNING",
		ProjectID:   DefaultProjectID,
		StartTime:   now,
		TotalVUsers: 10,
		Error:       "",
		Groups:      []any{map[string]any{"name": "buyers", "vusers": float64(10)}},
	}
	if err := store.SaveTest(record); err != nil {
		t.Fatalf("SaveTest: %v", err)
	}

	updated, err := store.UpdateTestVUserScale(TestVUserScaleUpdate{
		ID:          "scale-narrow",
		TotalVUsers: 42,
		Groups:      []any{map[string]any{"name": "buyers", "vusers": float64(42)}},
	})
	if err != nil {
		t.Fatalf("UpdateTestVUserScale: %v", err)
	}
	if !updated {
		t.Fatal("UpdateTestVUserScale reported no matching row for an existing test")
	}

	stored, err := store.GetTest("scale-narrow")
	if err != nil {
		t.Fatalf("GetTest: %v", err)
	}
	if stored.TotalVUsers != 42 {
		t.Fatalf("total_vusers = %d, want 42", stored.TotalVUsers)
	}
	groups, ok := stored.Groups.([]any)
	if !ok || len(groups) != 1 {
		t.Fatalf("groups = %#v, want one decoded group", stored.Groups)
	}
	group, ok := groups[0].(map[string]any)
	if !ok || group["vusers"] != float64(42) {
		t.Fatalf("group workload = %#v, want the scaled vusers", groups[0])
	}
	// Lifecycle identity must be untouched by a workload-only write.
	if stored.Name != "Scale Narrow" || stored.Status != "RUNNING" || !stored.StartTime.Equal(now) {
		t.Fatalf("narrow scale write disturbed lifecycle columns: %#v", stored)
	}

	missing, err := store.UpdateTestVUserScale(TestVUserScaleUpdate{ID: "does-not-exist", TotalVUsers: 1, Groups: []any{}})
	if err != nil {
		t.Fatalf("UpdateTestVUserScale(missing): %v", err)
	}
	if missing {
		t.Fatal("narrow scale write reported success for a row that does not exist")
	}
	if _, err := store.GetTest("does-not-exist"); err == nil {
		t.Fatal("narrow scale write resurrected a test row that was never created")
	}
}

// TestUpdateTestVUserScaleIfLeaseHeldRejectsLostEpoch keeps the narrow write
// under the same execution-lease fence as the full-row save it replaces.
func TestUpdateTestVUserScaleIfLeaseHeldRejectsLostEpoch(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "vuser-scale-lease.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC().Truncate(time.Second)
	if err := store.SaveTest(&TestRecord{
		ID: "scale-fenced", Name: "Fenced", Status: "RUNNING", ProjectID: DefaultProjectID,
		StartTime: now, TotalVUsers: 5, Groups: []any{},
	}); err != nil {
		t.Fatalf("SaveTest: %v", err)
	}

	updated, err := store.UpdateTestVUserScaleIfLeaseHeld(TestVUserScaleUpdate{
		ID: "scale-fenced", TotalVUsers: 9, Groups: []any{},
	}, "controller-execution-leader", "stale-owner")
	if err != nil {
		t.Fatalf("UpdateTestVUserScaleIfLeaseHeld: %v", err)
	}
	if updated {
		t.Fatal("a controller without the execution lease published a VUser scale")
	}
	stored, err := store.GetTest("scale-fenced")
	if err != nil {
		t.Fatalf("GetTest: %v", err)
	}
	if stored.TotalVUsers != 5 {
		t.Fatalf("total_vusers = %d, want the unfenced write to be rejected (5)", stored.TotalVUsers)
	}
}
