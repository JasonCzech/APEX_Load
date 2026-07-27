package api

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// staleSnapshotSourceScript is the durable script the repair path recompiles
// from. It is valid under the current validator.
func staleSnapshotSourceScript() *dsl.Script {
	return &dsl.Script{
		ID:       "script-1",
		Name:     "orders",
		Protocol: "http",
		Config: dsl.ScriptConfig{
			BaseURL: "https://api.example.test",
			Pacing:  dsl.PacingConfig{Type: "fixed_delay", DelayMs: 100},
		},
		Actions: []dsl.Action{{
			Name:   "request",
			Type:   "GET",
			Method: "GET",
			Target: "/orders",
		}},
	}
}

// TestRestoreManagedTestRuntimeRepairsSnapshotThatNoLongerValidates pins the
// grandfather path for stored compiled snapshots.
//
// dsl.ValidateScriptIssues now applies pacing, run-logic, and think-time
// resource limits to every protocol rather than only the messaging families, so
// a snapshot written before that widening can fail validation without the test
// or its source script changing. restoreStoredEngineGroupSpecs re-validates
// stored snapshots and is reached from startup reload and from launching a
// managed test, so a hard failure there would make existing tests unlaunchable.
// The snapshot must instead be treated as stale and recompiled from source.
func TestRestoreManagedTestRuntimeRepairsSnapshotThatNoLongerValidates(t *testing.T) {
	source := staleSnapshotSourceScript()

	// A snapshot that was valid when written but carries a pacing type outside
	// the current allowlist.
	staleSnapshot := staleSnapshotSourceScript()
	staleSnapshot.Config.Pacing = dsl.PacingConfig{Type: "start_new_iteration_at_unknown_cadence"}
	if issues := dsl.ValidateScriptIssues(staleSnapshot); len(issues) == 0 {
		t.Fatal("fixture snapshot is still valid; the test would not exercise the repair path")
	}

	tr := &TestRunner{
		tests: map[string]*ManagedTest{},
		scripts: map[string]*StoredScript{
			source.ID: {
				ID:       source.ID,
				Name:     source.Name,
				Protocol: source.Protocol,
				Script:   source,
			},
		},
	}
	test := &ManagedTest{
		ID:          "test-1",
		TotalVUsers: 2,
		Groups: []TestGroup{{
			Name:     "group-1",
			ScriptID: source.ID,
			Protocol: source.Protocol,
			VUsers:   2,
			script:   staleSnapshot,
		}},
	}

	if err := tr.restoreManagedTestRuntime(test); err != nil {
		t.Fatalf("restoreManagedTestRuntime rejected a stale-but-repairable snapshot: %v", err)
	}
	if !test.runtimeSnapshotRepairRequired {
		t.Fatal("stale snapshot did not request a durable repair")
	}
	if len(test.groupSpecs) != 1 {
		t.Fatalf("group specs = %d, want 1 recompiled group", len(test.groupSpecs))
	}
	restored := test.groupSpecs[0].Script
	if restored == nil {
		t.Fatal("recompiled group has no script")
	}
	if restored.Config.Pacing.Type != "fixed_delay" {
		t.Fatalf("recompiled pacing = %q, want the durable source value", restored.Config.Pacing.Type)
	}
	if issues := dsl.ValidateScriptIssues(restored); len(issues) != 0 {
		t.Fatalf("recompiled script is invalid: %v", issues)
	}
}

// TestRestoreManagedTestRuntimeStillRejectsUnrepairableSnapshots keeps the
// grandfather narrow: it covers snapshots that can be rebuilt from a durable
// source, not structural corruption. A group whose script id does not match its
// snapshot is a genuine integrity failure and must still surface.
func TestRestoreManagedTestRuntimeStillRejectsUnrepairableSnapshots(t *testing.T) {
	snapshot := staleSnapshotSourceScript()
	snapshot.ID = "script-other"

	tr := &TestRunner{
		tests:   map[string]*ManagedTest{},
		scripts: map[string]*StoredScript{},
	}
	test := &ManagedTest{
		ID:          "test-2",
		TotalVUsers: 1,
		Groups: []TestGroup{{
			Name:     "group-1",
			ScriptID: "script-1",
			VUsers:   1,
			script:   snapshot,
		}},
	}

	if err := tr.restoreManagedTestRuntime(test); err == nil {
		t.Fatal("mismatched snapshot script id was accepted")
	}
}
