package testexec

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var updateRunControlMatrix = flag.Bool("update", false, "update the generated M3 run-control matrix")

type runControlMatrixRow struct {
	Status     string           `json:"status"`
	Control    LifecycleControl `json:"control"`
	Allowed    bool             `json:"allowed"`
	Transition string           `json:"transition,omitempty"`
}

type runControlMatrixArtifact struct {
	SchemaVersion int                   `json:"schema_version"`
	Source        string                `json:"source"`
	Scope         string                `json:"scope"`
	RuntimeGates  []string              `json:"additional_runtime_gates"`
	Totals        map[string]int        `json:"totals"`
	Rows          []runControlMatrixRow `json:"rows"`
}

func TestRunControlMatrixFreshness(t *testing.T) {
	seenStatuses := make(map[string]bool, len(lifecycleStatuses))
	seenControls := make(map[LifecycleControl]bool, len(lifecycleControls))
	rows := make([]runControlMatrixRow, 0, len(lifecycleStatuses)*len(lifecycleControls))
	totals := map[string]int{"statuses": len(lifecycleStatuses), "controls": len(lifecycleControls)}
	for _, status := range lifecycleStatuses {
		if seenStatuses[status] {
			t.Fatalf("lifecycle status %q is registered more than once", status)
		}
		seenStatuses[status] = true
		for _, control := range lifecycleControls {
			allowed := AllowsLifecycleControl(status, control)
			row := runControlMatrixRow{Status: status, Control: control, Allowed: allowed}
			if allowed {
				row.Transition = lifecycleTransition(status, control)
				totals["allowed"]++
			} else {
				totals["denied"]++
			}
			rows = append(rows, row)
		}
	}
	for _, control := range lifecycleControls {
		if seenControls[control] {
			t.Fatalf("lifecycle control %q is registered more than once", control)
		}
		seenControls[control] = true
	}
	totals["rows"] = len(rows)
	if totals["rows"] != totals["statuses"]*totals["controls"] {
		t.Fatalf("run-control matrix has %d rows, want %d x %d", totals["rows"], totals["statuses"], totals["controls"])
	}

	artifact := runControlMatrixArtifact{
		SchemaVersion: 1,
		Source:        "pkg/api/testexec.AllowsLifecycleControl",
		Scope:         "status-only lifecycle controls; runtime adjustment controls are tracked separately until added to this matrix",
		RuntimeGates: []string{
			"execution leadership",
			"passive persistence ownership",
			"distributed finalization ownership",
			"backend control capability and acknowledgement",
			"capacity, quota, and launch-window admission",
		},
		Totals: totals,
		Rows:   rows,
	}
	encoded, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		t.Fatalf("marshal run-control matrix: %v", err)
	}
	encoded = append(encoded, '\n')
	path := filepath.Join("..", "..", "..", "docs", "qa", "evidence", "M3", "run-control-matrix.json")
	if *updateRunControlMatrix {
		if err := os.WriteFile(path, encoded, 0o644); err != nil {
			t.Fatalf("update %s: %v", path, err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v (run go test ./pkg/api/testexec -run TestRunControlMatrixFreshness -update)", path, err)
	}
	if string(want) != string(encoded) {
		t.Fatalf("%s is stale; review the lifecycle policy and regenerate it", path)
	}
}

func lifecycleTransition(status string, control LifecycleControl) string {
	switch control {
	case ControlStart:
		return "admission_pipeline"
	case ControlPause:
		return "PAUSED"
	case ControlResume:
		return "RUNNING"
	case ControlStop, ControlAbort:
		switch status {
		case "COMPLETE", "FAILED", "ABORTED", "STOPPING":
			return status
		case "RUNNING", "PAUSED":
			return "STOPPING"
		default:
			return "ABORTED"
		}
	default:
		return ""
	}
}
