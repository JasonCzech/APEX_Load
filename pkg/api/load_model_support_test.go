package api

import "testing"

func TestLoadModelSupportMatrixReconciledToExecutedShapes(t *testing.T) {
	matrix := loadModelSupportMatrix()
	if len(matrix) == 0 {
		t.Fatal("expected load-model support matrix entries")
	}

	wantExecuted := map[string]bool{
		"closed-vuser":      false,
		"open-arrival-rate": false,
		"stepped-ramp":      false,
		"spike-rendezvous":  false,
		"soak":              false,
		"stress":            false,
		"goal-based":        false,
		"mixed-protocol":    false,
	}
	for _, entry := range matrix {
		if entry.Status != "executed" {
			t.Fatalf("load-model support entry %s status = %q, want executed", entry.ID, entry.Status)
		}
		if _, ok := wantExecuted[entry.ID]; ok {
			wantExecuted[entry.ID] = true
		}
	}
	for id, seen := range wantExecuted {
		if !seen {
			t.Fatalf("support matrix missing %s", id)
		}
	}
}
