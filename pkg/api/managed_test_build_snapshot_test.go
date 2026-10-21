package api

import (
	"reflect"
	"strings"
	"testing"
	"time"

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

func TestManagedTestCapacitySnapshotPreservesEveryRequiredFeature(t *testing.T) {
	parallel := dsl.Action{Type: "control_parallel", Control: &dsl.ControlFlow{Branches: []dsl.ParallelBranch{{Name: "one", Steps: []dsl.Action{{Type: "control_loop", Control: &dsl.ControlFlow{Count: 3, Steps: []dsl.Action{{Type: "control_break"}}}}}}}}}
	cases := []struct {
		name     string
		script   *dsl.Script
		required []string
	}{
		{"no script", nil, nil},
		{"plain HTTP", &dsl.Script{Protocol: "http"}, nil},
		{"property analytics", &dsl.Script{Protocol: "http", Config: dsl.ScriptConfig{Results: dsl.ResultsConfig{PropertyAnalytics: []dsl.PropertyAnalytics{{Name: "category", Extraction: "category", Values: []string{"one"}}}}}}, []string{dsl.FeaturePropertyAnalyticsV1}},
		{"sampled detail", &dsl.Script{Protocol: "http", Config: dsl.ScriptConfig{Results: dsl.ResultsConfig{MessageDetail: &dsl.MessageDetailConfig{Mode: "sampled"}}}}, []string{generatorrpc.FeatureMessageDetailCaptureV1}},
		{"adaptive detail", &dsl.Script{Protocol: "http", Config: dsl.ScriptConfig{Results: dsl.ResultsConfig{MessageDetail: &dsl.MessageDetailConfig{Mode: "adaptive"}}}}, []string{generatorrpc.FeatureMessageDetailCaptureV1}},
		{"parallel composition", &dsl.Script{Protocol: "http", Actions: []dsl.Action{parallel}}, []string{generatorrpc.FeatureControlParallelV1, dsl.FeatureParallelLoopControlV1}},
		{"detail with parallel", &dsl.Script{Protocol: "http", Config: dsl.ScriptConfig{Results: dsl.ResultsConfig{MessageDetail: &dsl.MessageDetailConfig{Mode: "sampled"}}}, Init: []dsl.Action{parallel}}, []string{generatorrpc.FeatureMessageDetailControlParallelV1, dsl.FeatureParallelLoopControlV1}},
		{"JMeter source", &dsl.Script{Protocol: "http", Config: dsl.ScriptConfig{JMeter: &dsl.JMeterCompatibility{Profile: dsl.JMeterMessagingProfile}}}, []string{dsl.FeatureJMeterMessagingV1}},
		{"LoadRunner resources", &dsl.Script{Protocol: "http", Actions: []dsl.Action{{Type: "request", Params: map[string]any{"resources": []any{}}}}}, []string{dsl.FeatureLRFidelityV1}},
		{"Java lifecycle", &dsl.Script{Protocol: "java_vuser", Actions: []dsl.Action{{Type: "java_vuser_run", Params: map[string]any{"method": "actions"}}}}, []string{protocol.FeatureJavaVuserPersistentLifecycleV1}},
		{"Event Hubs processor", &dsl.Script{Protocol: "eventhubs", Config: dsl.ScriptConfig{EventHubs: dsl.EventHubsConfig{ConsumerMode: "processor", ProcessorMaxPartitions: 2, ObservePartitionTail: true}}, Actions: []dsl.Action{{Type: "eventhubs_consume"}}}, []string{"eventhubs_processor_checkpoints_v1", "eventhubs_processor_reservations_v1", "messaging_progress_v1"}},
		{"Kafka seek and EOS", &dsl.Script{Protocol: "kafka", Config: dsl.ScriptConfig{Kafka: dsl.KafkaConfig{ConsumerTransactions: true}}, Actions: []dsl.Action{{Type: "kafka_seek"}}}, []string{"kafka_real_seek_v1", "kafka_consumer_transactions_v1"}},
		{"browser documents", &dsl.Script{Protocol: "browser", Config: dsl.ScriptConfig{Browser: dsl.BrowserConfig{CaptureDocumentMetrics: boolPointer(true)}}}, []string{dsl.FeatureBrowserDocumentEvidenceV2}},
		{"browser interactions", &dsl.Script{Protocol: "browser", Config: dsl.ScriptConfig{Browser: dsl.BrowserConfig{CaptureInteractions: boolPointer(true)}}}, []string{dsl.FeatureBrowserInteractionEvidenceV2}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			group := TestGroup{Protocol: "http", VUsers: 2, script: tc.script}
			if tc.script != nil {
				group.Protocol = tc.script.Protocol
			}
			original := &ManagedTest{ID: "feature-peer", Status: "QUEUED", CreatedAt: time.Now().UTC(), Groups: []TestGroup{group}}
			want := managedTestBuildTestFingerprint(original)
			for _, feature := range tc.required {
				if !strings.Contains(want.Groups[0].RequiredRuntimeFeature, feature) {
					t.Fatalf("live feature contract %q lacks %q", want.Groups[0].RequiredRuntimeFeature, feature)
				}
			}
			if len(tc.required) == 0 && want.Groups[0].RequiredRuntimeFeature != "" {
				t.Fatalf("plain group invented feature %q", want.Groups[0].RequiredRuntimeFeature)
			}
			snapshot := cloneManagedTestCapacitySnapshotLocked(original)
			if snapshot.Groups[0].script != nil || snapshot.Groups[0].baseScript != nil {
				t.Fatal("lightweight snapshot retained executable source")
			}
			if got := managedTestBuildTestFingerprint(snapshot); !reflect.DeepEqual(got, want) {
				t.Fatalf("lightweight snapshot changed feature fingerprint: got=%#v want=%#v", got, want)
			}
		})
	}
}

func TestManagedTestCapacitySnapshotFeaturesStayFrozenAndFenceLiveChanges(t *testing.T) {
	runner := NewTestRunner(10, NewHub(), nil)
	script := httpScript("feature-peer-script", "Feature peer")
	script.Config.Results.PropertyAnalytics = []dsl.PropertyAnalytics{{Name: "category", Extraction: "category", Values: []string{"one"}}}
	peer := &ManagedTest{ID: "feature-peer", Status: "QUEUED", Groups: []TestGroup{{Protocol: "http", VUsers: 1, script: script}}}
	runner.mu.Lock()
	runner.tests[peer.ID] = peer
	snapshot, _, fence := runner.managedTestBuildSnapshotLocked(CreateTestRequest{})
	if !runner.managedTestBuildFenceCurrentLocked(fence) {
		runner.mu.Unlock()
		t.Fatal("unchanged feature peer invalidated admission")
	}
	frozen := snapshot.tests[peer.ID].Groups[0]
	peer.Groups[0].script.Config.Results.PropertyAnalytics = nil
	if runner.managedTestBuildFenceCurrentLocked(fence) {
		runner.mu.Unlock()
		t.Fatal("live feature change failed to invalidate admission")
	}
	runner.mu.Unlock()
	if got := groupRequiredRuntimeFeature(frozen); got != dsl.FeaturePropertyAnalyticsV1 {
		t.Fatalf("snapshot changed with live script: %q", got)
	}
	// A newly attached executable source is authoritative even on a copied row.
	// Snapshot-only cache fields must never mask later compilation changes.
	frozen.script = &dsl.Script{Protocol: "http"}
	if got := groupRequiredRuntimeFeature(frozen); got != "" {
		t.Fatalf("new executable script reused stale snapshot feature: %q", got)
	}
}

func TestCreateSequentialPropertyAnalyticsQueuedRunsAndClearRevision(t *testing.T) {
	runner := NewTestRunner(10, NewHub(), nil)
	script := httpScript("property-analytics-source", "Property analytics")
	script.Config.Results.PropertyAnalytics = []dsl.PropertyAnalytics{{Name: "category", Extraction: "category", Values: []string{"one"}}}
	if _, err := runner.StoreScriptDurable(script); err != nil {
		t.Fatal(err)
	}
	first, err := runner.CreateTest(CreateTestRequest{Name: "First property analytics", Duration: "1m", Groups: []CreateGroupEntry{{Name: "orders", ScriptID: script.ID, VUsers: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := runner.CreateTest(CreateTestRequest{Name: "Second property analytics", Duration: "1m", Groups: []CreateGroupEntry{{Name: "orders", ScriptID: script.ID, VUsers: 1}}})
	if err != nil {
		t.Fatalf("second immutable queued run falsely failed admission: %v", err)
	}
	if second.Status != "QUEUED" || first.ID == second.ID {
		t.Fatal("sequential creates did not produce separate queued runs")
	}
	zero := 0
	copy, err := runner.CreateTest(CreateTestRequest{Name: "Cleared property analytics copy", RevisionSourceTestID: first.ID, Groups: []CreateGroupEntry{{Name: "orders", ScriptID: script.ID, VUsers: 1, RevisionSourceGroupIndex: &zero, RuntimeSettings: &RuntimeSettingsOverride{Results: &dsl.ResultsConfig{}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := groupRequiredRuntimeFeature(copy.Groups[0]); got != "" {
		t.Fatalf("materialized copy retained cleared feature %q", got)
	}
	if got := groupRequiredRuntimeFeature(first.Groups[0]); got != dsl.FeaturePropertyAnalyticsV1 {
		t.Fatalf("copy changed source feature %q", got)
	}
}

func TestManagedTestCapacitySnapshotPreservesCompletionMode(t *testing.T) {
	for _, mode := range []string{"", "duration", "iterations"} {
		t.Run(mode, func(t *testing.T) {
			original := &ManagedTest{ID: "mode-peer", Status: "QUEUED", TerminationMode: mode}
			clone := cloneManagedTestCapacitySnapshotLocked(original)
			if clone.TerminationMode != mode {
				t.Fatalf("snapshot lost completion mode %q: %q", mode, clone.TerminationMode)
			}
			if !reflect.DeepEqual(managedTestBuildTestFingerprint(original), managedTestBuildTestFingerprint(clone)) {
				t.Fatal("snapshot changed completion mode fingerprint")
			}
		})
	}
}
