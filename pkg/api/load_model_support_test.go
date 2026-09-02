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
		"chaos-resilience":  false,
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

func TestClosedVUserTemplateDoesNotCapSustainedIterations(t *testing.T) {
	for _, template := range loadModelTemplates() {
		if template.ShapeID != "closed-vuser" {
			continue
		}
		if len(template.Scenario.Groups) != 1 {
			t.Fatalf("Closed VUser template groups = %d, want 1", len(template.Scenario.Groups))
		}
		if template.Scenario.Groups[0].Iterations != 0 {
			t.Fatalf("Closed VUser template iterations = %d, want no iteration cap", template.Scenario.Groups[0].Iterations)
		}
		return
	}
	t.Fatal("Closed VUser template not found")
}
