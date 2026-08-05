package api

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestManagedTestBuildSnapshotExcludesUnrelatedHistoryAndScripts(t *testing.T) {
	runner := NewTestRunner(10, NewHub(), nil)
	selectedScript := &StoredScript{
		ID:        "selected-script",
		RefKey:    "LOC-SCR-0001-v1",
		Name:      "Selected",
		Protocol:  "http",
		ProjectID: "project-a",
		Script: &dsl.Script{
			ID:        "selected-script",
			Name:      "Selected",
			Protocol:  "http",
			ProjectID: "project-a",
			Actions:   []dsl.Action{{Name: "selected", Type: "request", Method: "GET", Target: "/selected"}},
		},
	}
	largeUnrelatedPayload := strings.Repeat("unrelated-history-and-script-payload", 32*1024)
	unrelatedScript := &StoredScript{
		ID:        "unrelated-script",
		RefKey:    "LOC-SCR-0002-v1",
		Name:      "Unrelated",
		Protocol:  "http",
		ProjectID: "project-b",
		Script: &dsl.Script{
			ID:        "unrelated-script",
			Name:      "Unrelated",
			Protocol:  "http",
			ProjectID: "project-b",
			Actions:   []dsl.Action{{Name: largeUnrelatedPayload, Type: "request", Method: "GET", Target: "/unrelated"}},
		},
	}
	selectedScenario := &StoredScenario{
		ID:        "selected-scenario",
		RefKey:    "LOC-SCN-0001",
		Name:      "Selected scenario",
		ProjectID: "project-a",
		Duration:  "30s",
		Scripts: []ScenarioScriptRef{{
			ScriptID:   selectedScript.ID,
			ScriptName: selectedScript.Name,
			Protocol:   selectedScript.Protocol,
			VUsers:     1,
		}},
	}
	unrelatedScenario := &StoredScenario{ID: "unrelated-scenario", RefKey: "LOC-SCN-0002", Name: largeUnrelatedPayload}
	unrelatedHistory := &LiveMetrics{ErrorBreakdown: map[string]int64{largeUnrelatedPayload: 1}}
	unrelatedTest := &ManagedTest{
		ID:          "unrelated-test",
		Name:        "Unrelated run",
		Status:      "RUNNING",
		ProjectID:   "project-b",
		TotalVUsers: 25,
		LiveMetrics: unrelatedHistory,
		History:     []*LiveMetrics{unrelatedHistory},
		DurationStr: "1m",
		Groups: []TestGroup{{
			Name:              "Unrelated group",
			ScriptID:          unrelatedScript.ID,
			ScriptName:        unrelatedScript.Name,
			Protocol:          unrelatedScript.Protocol,
			VUsers:            25,
			RampDown:          "5s",
			script:            unrelatedScript.Script,
			baseScript:        unrelatedScript.Script,
			sharedDataSources: []dsl.DataSource{{Name: largeUnrelatedPayload}},
		}},
	}
	revisionSource := &ManagedTest{
		ID:          "selected-revision-source",
		RefKey:      "LOC-RUN-20260805-0001",
		Name:        "Selected revision source",
		Status:      "QUEUED",
		ProjectID:   "project-a",
		ScenarioID:  selectedScenario.ID,
		DurationStr: "30s",
		LiveMetrics: unrelatedHistory,
		History:     []*LiveMetrics{unrelatedHistory},
		Groups: []TestGroup{{
			Name:       "Selected revision group",
			ScriptID:   selectedScript.ID,
			ScriptName: selectedScript.Name,
			Protocol:   selectedScript.Protocol,
			VUsers:     1,
			script:     selectedScript.Script,
			baseScript: selectedScript.Script,
		}},
	}

	runner.mu.Lock()
	runner.scripts[selectedScript.ID] = selectedScript
	runner.scripts[unrelatedScript.ID] = unrelatedScript
	runner.scenarios[selectedScenario.ID] = selectedScenario
	runner.scenarios[unrelatedScenario.ID] = unrelatedScenario
	runner.tests[unrelatedTest.ID] = unrelatedTest
	runner.tests[revisionSource.ID] = revisionSource
	runner.indexRefKeyLocked(selectedScript.RefKey, selectedScript.ID)
	runner.indexRefKeyLocked(unrelatedScript.RefKey, unrelatedScript.ID)
	runner.indexRefKeyLocked(selectedScenario.RefKey, selectedScenario.ID)
	runner.indexRefKeyLocked(unrelatedScenario.RefKey, unrelatedScenario.ID)
	runner.indexRefKeyLocked(revisionSource.RefKey, revisionSource.ID)
	runner.settings.CapacityPlanning.Reservations = []CapacityReservation{{ProjectID: "project-a", ReservedGenerators: 2}}
	snapshot, prepared, _ := runner.managedTestBuildSnapshotLocked(CreateTestRequest{ScenarioID: selectedScenario.RefKey})
	revisionSnapshot, revisionPrepared, _ := runner.managedTestBuildSnapshotLocked(CreateTestRequest{RevisionSourceTestID: revisionSource.RefKey})
	runner.settings.CapacityPlanning.Reservations[0].ProjectID = "mutated-after-snapshot"
	runner.mu.Unlock()

	if prepared.ScenarioID != selectedScenario.ID {
		t.Fatalf("prepared scenario id = %q, want %q", prepared.ScenarioID, selectedScenario.ID)
	}
	if len(snapshot.scripts) != 1 || snapshot.scripts[selectedScript.ID] == nil {
		t.Fatalf("snapshot scripts = %#v, want only selected script", snapshot.scripts)
	}
	if snapshot.scripts[unrelatedScript.ID] != nil {
		t.Fatal("request-scoped snapshot retained an unrelated full script")
	}
	if snapshot.scripts[selectedScript.ID] == selectedScript || snapshot.scripts[selectedScript.ID].Script == selectedScript.Script {
		t.Fatal("selected script was not deeply cloned")
	}
	if len(snapshot.scenarios) != 1 || snapshot.scenarios[selectedScenario.ID] == nil {
		t.Fatalf("snapshot scenarios = %#v, want only selected scenario", snapshot.scenarios)
	}
	if snapshot.scenarios[selectedScenario.ID] == selectedScenario {
		t.Fatal("selected scenario was not cloned")
	}
	if snapshot.scenarios[unrelatedScenario.ID] != nil {
		t.Fatal("request-scoped snapshot retained an unrelated scenario")
	}
	capacityRow := snapshot.tests[unrelatedTest.ID]
	if capacityRow == nil {
		t.Fatal("snapshot omitted unrelated test capacity row")
	}
	if capacityRow == unrelatedTest {
		t.Fatal("snapshot retained the live unrelated test pointer")
	}
	if capacityRow.LiveMetrics != nil || len(capacityRow.History) != 0 {
		t.Fatal("capacity row retained unrelated live metrics or history")
	}
	if len(capacityRow.Groups) != 1 || capacityRow.Groups[0].script != nil || capacityRow.Groups[0].baseScript != nil || len(capacityRow.Groups[0].sharedDataSources) != 0 {
		t.Fatalf("capacity row retained full group script state: %#v", capacityRow.Groups)
	}
	if got := snapshot.settings.CapacityPlanning.Reservations[0].ProjectID; got != "project-a" {
		t.Fatalf("snapshot settings aliased live reservations: got %q", got)
	}
	if revisionPrepared.RevisionSourceTestID != revisionSource.ID {
		t.Fatalf("prepared revision source id = %q, want %q", revisionPrepared.RevisionSourceTestID, revisionSource.ID)
	}
	revisionRow := revisionSnapshot.tests[revisionSource.ID]
	if revisionRow == nil || len(revisionRow.Groups) != 1 || revisionRow.Groups[0].script == nil || revisionRow.Groups[0].baseScript == nil {
		t.Fatalf("selected revision source lost immutable group state: %#v", revisionRow)
	}
	if revisionRow.LiveMetrics != nil || len(revisionRow.History) != 0 || len(revisionRow.Sparkline) != 0 {
		t.Fatal("selected revision source retained metric history")
	}
}

func TestManagedTestCapacitySnapshotPreservesJavaRuntimePlanWithoutScript(t *testing.T) {
	runner := NewTestRunner(10, NewHub(), nil)
	javaScript := &dsl.Script{
		Protocol: "java_vuser",
		Actions: []dsl.Action{{
			Type:   "java_vuser_run",
			Params: map[string]any{"method": "actions"},
		}},
	}
	runner.mu.Lock()
	runner.tests["active-java"] = &ManagedTest{
		ID: "active-java", Status: "RUNNING", TotalVUsers: 7,
		Groups: []TestGroup{{Protocol: "java_vuser", VUsers: 7, script: javaScript}},
	}
	snapshot, _, _ := runner.managedTestBuildSnapshotLocked(CreateTestRequest{})
	runner.mu.Unlock()

	group := snapshot.tests["active-java"].Groups[0]
	if group.script != nil {
		t.Fatal("capacity snapshot retained full Java script")
	}
	if got := javaVuserOccupancyModeForGroup(group); got != generatorrpc.RuntimeOccupancyModePersistent {
		t.Fatalf("Java occupancy mode = %q, want persistent", got)
	}
	if got := groupRequiredRuntimeFeature(group); got != protocol.FeatureJavaVuserPersistentLifecycleV1 {
		t.Fatalf("Java required runtime feature = %q", got)
	}
	if got := javaVuserCapacityPlanForGroup(group).Demand(group.VUsers); got != 7 {
		t.Fatalf("Java runtime demand = %d, want 7", got)
	}
}
