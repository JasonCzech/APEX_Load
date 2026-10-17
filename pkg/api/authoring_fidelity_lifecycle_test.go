package api

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestAuthoringFidelitySQLiteLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fidelity.db")
	runAuthoringFidelityLifecycle(t, func() storage.Store {
		store, err := storage.NewSQLiteStore(path)
		if err != nil {
			t.Fatal(err)
		}
		return store
	})
}

// This exercises the real aggregate writers, runtime-copy operation, library
// snapshot conversion, immutable queued-copy creation and database reopen. It
// never starts a run or contacts the system under test.
func runAuthoringFidelityLifecycle(t *testing.T, openStore func() storage.Store) {
	t.Helper()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_ARCHIVE_STAGING_DIR", t.TempDir())
	// This slice retains configured legacy capture identities; native provider
	// readiness and immutable provider definitions are qualified separately.
	t.Setenv("APEXLOAD_RUN_MONITOR_PROJECTS", "")
	open := func() (*Server, storage.Store) {
		store := openStore()
		t.Cleanup(func() {
			if err := store.Close(); err != nil {
				t.Error(err)
			}
		})
		server := NewServer(ServerConfig{Store: store, MaxConcurrent: 8})
		t.Cleanup(func() { shutdownAuthoringFidelityServer(t, server) })
		return server, store
	}
	server, store := open()
	results := &dsl.ResultsConfig{
		CSVExports:        []dsl.CSVExport{{ID: "call-csv", FileName: "calls.csv", ActionName: "Home", Columns: []dsl.CSVExportColumn{{Name: "latency", Metric: "latency_ms"}}}},
		PropertyAnalytics: []dsl.PropertyAnalytics{{Name: "category", Extraction: "category", Values: []string{"one", "two"}}},
	}
	for _, id := range []string{"fidelity-http-source", "fidelity-http-target"} {
		script := httpScript(id, id)
		script.Config.Results = *cloneViaJSON(results)
		if _, err := server.runner.StoreScriptDurable(script); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"fidelity-ws-source", "fidelity-ws-target"} {
		script := &dsl.Script{ID: id, Name: id, Version: "1.0", Protocol: "websocket", Config: dsl.ScriptConfig{BaseURL: "ws://example.test", WebSocket: dsl.WebSocketConfig{CallbackIntervalMs: 500, Compression: true, Reconnect: true}}, Actions: []dsl.Action{{Name: "Connect", Type: "ws_connect", Target: "ws://example.test"}}}
		if _, err := server.runner.StoreScriptDurable(script); err != nil {
			t.Fatal(err)
		}
	}
	shape, sla := "closed-vuser", 1250.0
	request := CreateScenarioRequest{
		Name: "Fidelity scenario", WorkloadShapeID: &shape, Duration: "1m", ScenarioMode: "vuser_driven",
		GoalConfig:      &GoalConfig{WorkloadShapeConfig: &WorkloadShapeConfig{ClosedVUser: &ClosedVUserConfig{Scope: "scenario", Scenario: &ClosedVUserDefinition{PeakVUsers: 4, RampUp: "10s", Sustain: "50s", RampDownEnabled: false, RampDown: "0s"}}}},
		RuntimeSettings: &RuntimeSettingsOverride{Results: &dsl.ResultsConfig{MessageDetail: &dsl.MessageDetailConfig{Mode: "errors", IncludeResources: boolPointer(true)}}, WebSocket: &dsl.WebSocketConfig{CallbackIntervalMs: 500, ReadTimeoutMs: 1000, Compression: true, Reconnect: true}},
		Groups: []CreateGroupEntry{
			{Name: "HTTP source", ScriptID: "fidelity-http-source", VUsers: 1, InitStrategy: "at_once", RampUp: "3s", RampDown: "4s", StartOffset: "1s", TransactionResponseTimeSLAMs: &sla, RuntimeSettings: &RuntimeSettingsOverride{Results: cloneViaJSON(results)}},
			{Name: "HTTP target", ScriptID: "fidelity-http-target", VUsers: 1, InitStrategy: "at_runtime", StartOffset: "2s", TransactionResponseTimeSLAMs: &sla, RuntimeSettings: &RuntimeSettingsOverride{Results: &dsl.ResultsConfig{MessageDetail: &dsl.MessageDetailConfig{Mode: "off", IncludeResources: boolPointer(false)}}}},
			{Name: "WS source", ScriptID: "fidelity-ws-source", VUsers: 1, InitStrategy: "at_runtime", RuntimeSettings: &RuntimeSettingsOverride{WebSocket: &dsl.WebSocketConfig{CallbackIntervalMs: 0, ReadTimeoutMs: 0, WriteTimeoutMs: 0, MaxMessageSize: 0, Compression: false, Reconnect: false}}},
			{Name: "WS target", ScriptID: "fidelity-ws-target", VUsers: 1, InitStrategy: "at_runtime", Stages: []storage.LoadStage{{Type: "hold", Duration: "50s", TargetVUsers: 1}}, RuntimeSettings: &RuntimeSettingsOverride{WebSocket: &dsl.WebSocketConfig{CallbackIntervalMs: 777, Reconnect: true}}},
		},
	}
	scenario, err := server.runner.StoreScenario(request)
	if err != nil {
		t.Fatal(err)
	}
	updatedRequest := createScenarioRequestFromStored(scenario)
	updatedRequest.Name = "Rename only"
	scenario, found, err := server.runner.UpdateScenario(scenario.ID, updatedRequest)
	if err != nil || !found {
		t.Fatalf("ordinary save found=%v err=%v", found, err)
	}
	assertAuthoringFidelityScenario(t, scenario, request)

	// Both runtime-copy paths rewrite the stored parent and must retain shape,
	// workload intent, ordered group identities/offsets and group SLA.
	for _, copyRequest := range []ApplyRuntimeToGroupsRequest{
		{SourceGroup: 0, TargetGroups: []int{1}, Fields: []string{"results"}},
		{SourceGroup: 2, TargetGroups: []int{3}, Fields: []string{"websocket"}},
	} {
		response, found, err := server.runner.ApplyScenarioRuntimeToGroups(scenario.ID, copyRequest)
		if err != nil || !found || response.Applied != 1 {
			t.Fatalf("runtime copy found=%v response=%#v err=%v", found, response, err)
		}
		scenario = response.Scenario
	}
	request.Groups[1].RuntimeSettings.Results = cloneViaJSON(results)
	request.Groups[3].RuntimeSettings.WebSocket = &dsl.WebSocketConfig{}
	assertAuthoringFidelityScenario(t, scenario, request)
	row, _, err := store.GetScenario(scenario.ID)
	if err != nil || row.WorkloadShapeID != shape {
		t.Fatalf("runtime copy durable shape=%#v err=%v", row, err)
	}

	// Snapshot converters are used by actual library materialization and import
	// rollback. An iteration-based snapshot with an empty shape must reproduce
	// both values rather than preserve whatever a later update wrote.
	iterationScenario := cloneStoredScenarioForRead(scenario)
	iterationScenario.TerminationMode = "iterations"
	iterationScenario.Duration = ""
	iterationScenario.WorkloadShapeID = ""
	iterationScenario.GoalConfig = nil
	for index := range iterationScenario.Scripts {
		count := 3
		iterationScenario.Scripts[index].Iterations = &count
		// Iteration termination forbids stages and ramp-down; the duration-based
		// scenario above independently proves those fields survive ordinary saves.
		iterationScenario.Scripts[index].Stages = nil
		iterationScenario.Scripts[index].RampDown = ""
	}
	iterationRequest := createScenarioRequestFromStored(iterationScenario)
	imported, err := server.runner.StoreScenarioWithIDCreateOnly("fidelity-library-snapshot", iterationRequest)
	if err != nil {
		t.Fatal(err)
	}
	if imported.TerminationMode != "iterations" || imported.WorkloadShapeID != "" || imported.Scripts[0].TransactionResponseTimeSLAMs == nil || *imported.Scripts[0].TransactionResponseTimeSLAMs != sla {
		t.Fatalf("library materialization lost snapshot: %#v", imported)
	}
	changed := createScenarioRequestFromStored(imported)
	changed.WorkloadShapeID = &shape
	changed.Groups[0].TransactionResponseTimeSLAMs = float64Pointer(900)
	after, err := server.runner.UpdateScenarioIfUnchanged(imported.ID, changed, imported.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	rolledBack, err := server.runner.UpdateScenarioIfUnchanged(imported.ID, createScenarioRequestFromStored(imported), after.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if rolledBack.WorkloadShapeID != "" || *rolledBack.Scripts[0].TransactionResponseTimeSLAMs != sla {
		t.Fatal("snapshot rollback did not restore explicit empty shape and SLA")
	}

	libraryTarget := runAuthoringFidelityLibrarySnapshot(t, server, iterationRequest, shape)

	elk := &storage.ELKConnection{ID: "fidelity-elk", ProjectID: storage.DefaultProjectID, Name: "Fidelity logs", Enabled: true, Endpoint: "https://capture.invalid", IndexPattern: "logs-*", TimestampField: "@timestamp", MessageField: "message", AuthType: storage.ELKAuthAPIKey, APIKeySecretName: "fidelity-log-secret"}
	if err := store.(storage.ELKConnectionStore).CreateELKConnection(elk); err != nil {
		t.Fatal(err)
	}
	dynatrace := &storage.DynatraceConnection{ID: "fidelity-dt", ProjectID: storage.DefaultProjectID, Name: "Fidelity SaaS", Enabled: true, EnvironmentURL: "https://fidelity.live.dynatrace.com", EnvironmentTokenSecret: "fidelity-dt-secret", ProblemsEnabled: true, PollIntervalSeconds: 60}
	if err := store.(storage.DynatraceConnectionStore).CreateDynatraceConnection(dynatrace); err != nil {
		t.Fatal(err)
	}
	source, err := server.runner.CreateTest(CreateTestRequest{Name: "Queued fidelity source", ScenarioID: scenario.ID, ELKCapture: &ELKCaptureConfig{ConnectionID: elk.ID}, DynatraceCapture: &DynatraceCaptureConfig{ConnectionID: dynatrace.ID}})
	if err != nil {
		t.Fatalf("create queued fidelity source: %v", err)
	}
	if source.Status != "QUEUED" || source.Groups[0].script.Config.Results.MessageDetail != nil || source.Groups[3].script.Config.WebSocket != (dsl.WebSocketConfig{}) {
		t.Fatalf("compiled section precedence wrong: %s results=%#v websocket=%#v", source.Status, source.Groups[0].script.Config.Results, source.Groups[3].script.Config.WebSocket)
	}
	masked := maskManagedTest(source)
	revision := CreateTestRequest{Name: "Queued fidelity copy", RevisionSourceTestID: source.ID, Duration: source.Duration.String(), ScenarioMode: source.ScenarioMode, GoalConfig: cloneGoalConfig(masked.GoalConfig), RuntimeSettings: cloneRuntimeSettings(masked.RuntimeSettings), ELKCapture: &ELKCaptureConfig{ConnectionID: masked.ELKCapture.ConnectionID}, DynatraceCapture: &DynatraceCaptureConfig{ConnectionID: masked.DynatraceCapture.ConnectionID}}
	for index, group := range masked.Groups {
		indexCopy := index
		revision.Groups = append(revision.Groups, CreateGroupEntry{Name: group.Name, ScriptID: group.ScriptID, Role: group.Role, Iterations: cloneIntPtr(group.IterationOverride), RevisionSourceGroupIndex: &indexCopy, VUsers: group.VUsers, RampUp: group.RampUp, RampDown: group.RampDown, StartOffset: group.StartOffset, Stages: cloneLoadStages(group.Stages), InitStrategy: group.Initialization, RuntimeSettings: cloneRuntimeSettings(group.RuntimeSettings), Assignment: cloneGeneratorAssignmentSettings(group.Assignment), TransactionResponseTimeSLAMs: cloneFloat64Ptr(group.TransactionResponseTimeSLAMs)})
	}
	// JSON is the real request boundary and strips Go-only source/helper state.
	encoded, err := json.Marshal(revision)
	if err != nil {
		t.Fatal(err)
	}
	var transported CreateTestRequest
	if err := json.Unmarshal(encoded, &transported); err != nil {
		t.Fatal(err)
	}
	copyRun, err := server.runner.CreateTest(transported)
	if err != nil {
		t.Fatalf("create queued fidelity copy: %v", err)
	}
	if copyRun.ID == source.ID || source.Name != "Queued fidelity source" || !reflect.DeepEqual(copyRun.GoalConfig.WorkloadShapeConfig, source.GoalConfig.WorkloadShapeConfig) {
		t.Fatal("queued copy changed source or lost actual workload intent")
	}
	assertAuthoringFidelityRun(t, copyRun, source)
	copyRun.RuntimeSettings.Results.MessageDetail.Mode = "off"
	if source.RuntimeSettings.Results.MessageDetail.Mode != "errors" {
		t.Fatal("queued copy aliases source runtime")
	}

	guardrailScenario := runAuthoringFidelityGuardrailModeChange(t, server)
	goalPresenceRuns := runAuthoringFidelityQueuedGoalPresence(t, server)
	completionClearRuns := runAuthoringFidelityQueuedCompletionAndClears(t, server)

	shutdownAuthoringFidelityServer(t, server)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, reopenedStore := open()
	got, found := reopened.runner.GetScenario(scenario.ID)
	if !found {
		t.Fatal("scenario lost after close/reopen")
	}
	assertAuthoringFidelityScenario(t, got, request)
	for _, id := range []string{source.ID, copyRun.ID} {
		if row, err := reopenedStore.GetTest(id); err != nil || row == nil || row.Status != "QUEUED" {
			t.Fatalf("queued record lost on reopen: id=%s row=%#v err=%v", id, row, err)
		}
		run, found := reopened.runner.GetTest(id)
		if !found {
			t.Fatal("queued run lost after close/reopen")
		}
		assertAuthoringFidelityRun(t, run, source)
	}
	for _, before := range goalPresenceRuns {
		after, found := reopened.runner.GetTest(before.ID)
		if !found || after.Status != "QUEUED" || !reflect.DeepEqual(after.GoalConfig, before.GoalConfig) {
			t.Fatalf("queued goal presence lost after reopen: before=%#v after=%#v", before.GoalConfig, after)
		}
		if before.GoalConfig != nil && reflect.DeepEqual(*before.GoalConfig, GoalConfig{}) && (after.ELKCapture != nil || after.DynatraceCapture != nil) {
			t.Fatal("explicit capture Off reappeared after reopen")
		}
	}
	for _, before := range completionClearRuns {
		after, found := reopened.runner.GetTest(before.ID)
		if !found {
			t.Fatal("completion/root clear run missing after reopen")
		}
		mode := dsl.CanonicalTerminationMode(before.TerminationMode)
		// Durable hydration canonicalizes the historical empty duration mode and
		// duration spelling (1m -> 1m0s). Numeric duration remains authoritative;
		// iteration completion must preserve its exact allowed empty duration.
		durationEqual := after.Duration == before.Duration
		if mode == dsl.TerminationIterations {
			durationEqual = durationEqual && after.DurationStr == "" && before.DurationStr == ""
		}
		if after.Status != "QUEUED" || dsl.CanonicalTerminationMode(after.TerminationMode) != mode || !durationEqual || after.StartDelay != before.StartDelay || !reflect.DeepEqual(after.RuntimeSettings, before.RuntimeSettings) || len(after.Groups) != len(before.Groups) {
			t.Fatalf("completion/root clear intent lost after reopen: mode=%q/%q duration=%q/%q delay=%q/%q runtime_equal=%v groups=%d/%d", before.TerminationMode, after.TerminationMode, before.DurationStr, after.DurationStr, before.StartDelay, after.StartDelay, reflect.DeepEqual(before.RuntimeSettings, after.RuntimeSettings), len(before.Groups), len(after.Groups))
		}
		for index, group := range before.Groups {
			reopenedGroup := after.Groups[index]
			if !reflect.DeepEqual(group.RuntimeSettings, reopenedGroup.RuntimeSettings) || !reflect.DeepEqual(group.Assignment, reopenedGroup.Assignment) || !reflect.DeepEqual(group.IterationOverride, reopenedGroup.IterationOverride) {
				t.Fatal("group runtime, placement clear or finite iteration intent lost after reopen")
			}
		}
	}
	if got, found := reopened.runner.GetScenario(guardrailScenario.ID); !found || !reflect.DeepEqual(got.GoalConfig, guardrailScenario.GoalConfig) || got.ScenarioMode != "vuser_driven" || got.WorkloadShapeID != "stepped-ramp" {
		t.Fatalf("common guardrails changed after close/reopen: %#v", got)
	}
	if target, found := reopened.runner.GetScenario(libraryTarget.ID); !found || target.WorkloadShapeID != "" || target.Name != libraryTarget.Name || !reflect.DeepEqual(target.Scripts[0].TransactionResponseTimeSLAMs, libraryTarget.Scripts[0].TransactionResponseTimeSLAMs) {
		t.Fatal("linked library clear/later editor lost after reopen")
	}
	if old, found := reopened.runner.GetScenario(imported.ID); !found || old.WorkloadShapeID != "" || old.TerminationMode != "iterations" || *old.Scripts[0].TransactionResponseTimeSLAMs != sla {
		t.Fatal("rolled-back library snapshot lost after reopen")
	}
}

// This transport boundary is shared by the actual SQLite/PostgreSQL lifecycle.
// Missing goal configuration means source inheritance; {} is the explicit
// authored clear. No provider polling or target execution occurs while queued.
func runAuthoringFidelityQueuedGoalPresence(t *testing.T, server *Server) []*ManagedTest {
	t.Helper()
	create := func(request CreateTestRequest) *ManagedTest {
		t.Helper()
		encoded, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		var wire CreateTestRequest
		if err := json.Unmarshal(encoded, &wire); err != nil {
			t.Fatal(err)
		}
		run, err := server.runner.CreateTest(wire)
		if err != nil {
			t.Fatalf("create %q: %v", request.Name, err)
		}
		if run.Status != "QUEUED" {
			t.Fatal("fixture unexpectedly crossed execution admission")
		}
		return run
	}
	source := create(CreateTestRequest{Name: "P95 source", Duration: "1m", ScenarioMode: "goal_oriented", GoalConfig: &GoalConfig{P95LatencyMs: float64Pointer(1000)}, Groups: []CreateGroupEntry{{Name: "orders", ScriptID: "fidelity-http-source", VUsers: 1}}, ELKCapture: &ELKCaptureConfig{ConnectionID: "fidelity-elk"}, DynatraceCapture: &DynatraceCaptureConfig{ConnectionID: "fidelity-dt"}})
	original := cloneViaJSON(source.GoalConfig)
	inherited := create(CreateTestRequest{Name: "Inherited P95 copy", RevisionSourceTestID: source.ID, ScenarioMode: "vuser_driven"})
	cleared := create(CreateTestRequest{Name: "Explicitly clear P95 and captures", RevisionSourceTestID: source.ID, ScenarioMode: "vuser_driven", GoalConfig: &GoalConfig{}})
	if !reflect.DeepEqual(inherited.GoalConfig, original) || cleared.GoalConfig == nil || !reflect.DeepEqual(*cleared.GoalConfig, GoalConfig{}) || cleared.ELKCapture != nil || cleared.DynatraceCapture != nil {
		t.Fatalf("source inheritance/explicit clear changed: inherited=%#v cleared=%#v", inherited.GoalConfig, cleared.GoalConfig)
	}
	if !reflect.DeepEqual(source.GoalConfig, original) || source.ELKCapture == nil || source.DynatraceCapture == nil {
		t.Fatal("copy modified source guardrails/capture identity")
	}
	absent := create(CreateTestRequest{Name: "Absent goal source", Duration: "1m", ScenarioMode: "vuser_driven", Groups: []CreateGroupEntry{{Name: "orders", ScriptID: "fidelity-http-source", VUsers: 1}}})
	absentCopy := create(CreateTestRequest{Name: "Absent goal copy", RevisionSourceTestID: absent.ID})
	if absent.GoalConfig != nil || absentCopy.GoalConfig != nil {
		t.Fatal("absent goal became a synthetic empty override")
	}
	return []*ManagedTest{source, inherited, cleared, absent, absentCopy}
}

// Supplied groups do not inherit a source group's omitted runtime/placement,
// while root runtime and global delay do. Exercise the actual JSON boundary,
// pristine source recompilation and durable reopen without extra group markers.
func runAuthoringFidelityQueuedCompletionAndClears(t *testing.T, server *Server) []*ManagedTest {
	t.Helper()
	create := func(request CreateTestRequest) *ManagedTest {
		t.Helper()
		encoded, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		var wire CreateTestRequest
		if err := json.Unmarshal(encoded, &wire); err != nil {
			t.Fatal(err)
		}
		run, err := server.runner.CreateTest(wire)
		if err != nil {
			t.Fatalf("create %q: %v", request.Name, err)
		}
		if run.Status != "QUEUED" {
			t.Fatal("completion/clear fixture crossed execution admission")
		}
		return run
	}
	iterations := 3
	iterationSource := create(CreateTestRequest{Name: "Iteration source", TerminationMode: "iterations", ScenarioMode: "vuser_driven", Groups: []CreateGroupEntry{{Name: "orders", ScriptID: "fidelity-http-source", VUsers: 1, Iterations: &iterations}}})
	zero := 0
	iterationCopy := create(CreateTestRequest{Name: "Iteration renamed copy", TerminationMode: "iterations", RevisionSourceTestID: iterationSource.ID, ScenarioMode: "vuser_driven", Groups: []CreateGroupEntry{{Name: "orders", ScriptID: "fidelity-http-source", VUsers: 1, Iterations: &iterations, RevisionSourceGroupIndex: &zero}}})
	if iterationCopy.DurationStr != "" || iterationCopy.TerminationMode != "iterations" || iterationCopy.GoalConfig != nil || iterationCopy.RuntimeSettings != nil || iterationCopy.StartDelay != "" || iterationCopy.Groups[0].IterationOverride == nil || *iterationCopy.Groups[0].IterationOverride != iterations {
		t.Fatal("iteration copy invented duration, delay/root overrides or lost finite iterations")
	}
	iterationDelayed := create(CreateTestRequest{Name: "Delayed iteration source", TerminationMode: "iterations", StartDelay: "10s", ScenarioMode: "vuser_driven", Groups: []CreateGroupEntry{{Name: "orders", ScriptID: "fidelity-http-source", VUsers: 1, Iterations: &iterations}}})
	iterationInherited := create(CreateTestRequest{Name: "Delayed iteration copy", RevisionSourceTestID: iterationDelayed.ID})
	iterationCleared := create(CreateTestRequest{Name: "Cleared iteration delay", TerminationMode: "iterations", RevisionSourceTestID: iterationDelayed.ID, StartDelay: "0s", Groups: []CreateGroupEntry{{Name: "orders", ScriptID: "fidelity-http-source", VUsers: 1, Iterations: &iterations, RevisionSourceGroupIndex: &zero}}})
	for _, run := range []*ManagedTest{iterationDelayed, iterationInherited, iterationCleared} {
		if run.TerminationMode != "iterations" || run.Duration != 0 || run.DurationStr != "" || run.Groups[0].IterationOverride == nil || *run.Groups[0].IterationOverride != iterations || run.GoalConfig != nil {
			t.Fatal("delayed iteration copy lost fixed finite completion intent")
		}
	}
	if iterationDelayed.StartDelay != "10s" || iterationInherited.StartDelay != "10s" || iterationCleared.StartDelay != "0s" {
		t.Fatal("positive iteration delay compatibility or explicit clear changed")
	}
	source := create(CreateTestRequest{Name: "Runtime and delay source", Duration: "1m", StartDelay: "10s", RuntimeSettings: &RuntimeSettingsOverride{LogLevel: "standard"}, Groups: []CreateGroupEntry{{Name: "orders", ScriptID: "fidelity-http-source", VUsers: 1, RuntimeSettings: &RuntimeSettingsOverride{LogLevel: "disabled"}, Assignment: &GeneratorAssignmentSettings{Mode: "auto", RequiredLabels: map[string]string{"zone": "source"}}}}})
	inherited := create(CreateTestRequest{Name: "Root inheritance copy", RevisionSourceTestID: source.ID})
	if inherited.StartDelay != "10s" || !reflect.DeepEqual(inherited.RuntimeSettings, source.RuntimeSettings) || !reflect.DeepEqual(inherited.Groups[0].RuntimeSettings, source.Groups[0].RuntimeSettings) || !reflect.DeepEqual(inherited.Groups[0].Assignment, source.Groups[0].Assignment) {
		t.Fatal("omitting the complete group list changed source inheritance")
	}
	cleared := create(CreateTestRequest{Name: "Root and supplied group clear", RevisionSourceTestID: source.ID, StartDelay: "0s", RuntimeSettings: &RuntimeSettingsOverride{}, Groups: []CreateGroupEntry{{Name: "orders", ScriptID: "fidelity-http-source", VUsers: 1, RevisionSourceGroupIndex: &zero}}})
	if cleared.StartDelay != "0s" || cleared.RuntimeSettings == nil || !reflect.DeepEqual(*cleared.RuntimeSettings, RuntimeSettingsOverride{}) || cleared.Groups[0].RuntimeSettings != nil || cleared.Groups[0].Assignment != nil {
		t.Fatal("explicit root clear or omitted supplied-group clear inherited old settings")
	}
	if source.Groups[0].baseScript == nil || cleared.Groups[0].script == source.Groups[0].script || cleared.Groups[0].script.Config.Log.Level != source.Groups[0].baseScript.Config.Log.Level {
		t.Fatal("runtime clear reused already-overridden source script")
	}
	if source.StartDelay != "10s" || source.RuntimeSettings.LogLevel != "standard" || source.Groups[0].RuntimeSettings.LogLevel != "disabled" || source.Groups[0].Assignment.RequiredLabels["zone"] != "source" || source.Groups[0].script.Config.Log.Level != "disabled" {
		t.Fatal("clear mutated source runtime, delay, placement or compiled script")
	}
	return []*ManagedTest{iterationSource, iterationCopy, iterationDelayed, iterationInherited, iterationCleared, source, inherited, cleared}
}

// Both actual SQLite and PostgreSQL lifecycle fixtures execute this real
// aggregate update. Pointer-valued zero thresholds survive; historical bool
// and slice omitempty projections retain false/empty semantics on reopening.
func runAuthoringFidelityGuardrailModeChange(t *testing.T, server *Server) *StoredScenario {
	t.Helper()
	var goal GoalConfig
	if err := json.Unmarshal([]byte(`{"target_average_tps":25,"min_vusers":1,"max_vusers":30,"transaction_response_time_sla_ms":0.001,"p95_latency_ms":2300,"max_error_pct":0,"max_avg_server_processing_ms":400,"max_avg_transfer_ms":125,"max_error_count":3,"max_lcp_ms":2500,"max_inp_ms":200,"max_cls":0.1,"max_throttled_requests":0,"max_end_to_end_latency_ms":500,"max_partition_skew_pct":0,"max_redelivery_rate_pct":0,"max_dead_letter_rate_pct":0,"stop_on_sla_breach":true,"stop_on_max_errors":true,"sla_breach_window":"7s","transaction_rules":[{"transaction":"orders","metric":"p95_duration_ms","comparator":"lte","threshold":1250}],"monitor_rules":[]}`), &goal); err != nil {
		t.Fatal(err)
	}
	goal.WorkloadShapeConfig = &WorkloadShapeConfig{GoalDriven: &GoalDrivenConfig{Groups: map[string]GoalDrivenGroupDefinition{"group_0": {TargetTPS: 25, ART: &GoalDrivenARTConfig{Basis: "manual", ARTMs: 400}}}}}
	shape := "open-arrival-rate"
	request := CreateScenarioRequest{Name: "Goal to stepped guardrails", Duration: "1m", ScenarioMode: "goal_oriented", WorkloadShapeID: &shape, GoalConfig: &goal, Groups: []CreateGroupEntry{{Name: "orders", ScriptID: "fidelity-http-source", VUsers: 2, InitStrategy: "at_once", TransactionResponseTimeSLAMs: float64Pointer(1250)}}}
	source, err := server.runner.StoreScenario(request)
	if err != nil {
		t.Fatal(err)
	}
	original := cloneStoredScenarioForRead(source)
	update := createScenarioRequestFromStored(source)
	stepped := "stepped-ramp"
	update.WorkloadShapeID = &stepped
	update.ScenarioMode = "vuser_driven"
	update.GoalConfig.TargetAverageTPS, update.GoalConfig.MinVUsers, update.GoalConfig.MaxVUsers = nil, nil, nil
	update.GoalConfig.WorkloadShapeConfig = &WorkloadShapeConfig{SteppedRamp: &SteppedRampConfig{Scope: "scenario", Scenario: &SteppedRampDefinition{StartVUsers: 0, EndVUsers: 14, IncrementBy: "step_count", StepCount: 3, VUsersPerStep: 5}}}
	update.Groups[0].VUsers = 14
	// An explicit replacement can clear prior true flags and a nonempty rule
	// set. Whole GoalConfig replacement is already the public writer contract.
	update.GoalConfig.StopOnSLABreach, update.GoalConfig.StopOnMaxErrors = false, false
	update.GoalConfig.TransactionRules = []TransactionSLARule{}
	encoded, err := json.Marshal(update)
	if err != nil {
		t.Fatal(err)
	}
	var decoded CreateScenarioRequest
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	changed, found, err := server.runner.UpdateScenario(source.ID, decoded)
	if err != nil || !found {
		t.Fatalf("mode change found=%v err=%v", found, err)
	}
	if !reflect.DeepEqual(changed.GoalConfig, decoded.GoalConfig) || changed.GoalConfig.StopOnSLABreach || changed.GoalConfig.StopOnMaxErrors || len(changed.GoalConfig.TransactionRules) != 0 {
		t.Fatalf("common guardrails or explicit clears changed: %#v", changed.GoalConfig)
	}
	if !reflect.DeepEqual(source, original) {
		t.Fatal("mode change mutated the owned source snapshot")
	}
	run, err := server.runner.CreateTest(CreateTestRequest{Name: "Retained VUser guardrails", ScenarioID: changed.ID})
	if err != nil {
		t.Fatalf("create retained VUser guardrails: %v", err)
	}
	if !reflect.DeepEqual(run.GoalConfig, changed.GoalConfig) || run.Status != "QUEUED" || run.ScenarioMode != "vuser_driven" || run.goalControl != nil {
		t.Fatal("retained guardrails fabricated a goal controller or changed queued intent")
	}
	live := &LiveMetrics{Timestamp: time.Now().UTC(), TPS: 2, ActiveVUsers: 14}
	before := cloneViaJSON(live)
	applyRuntimeGoalTargetsToLiveMetrics(run, live)
	if !reflect.DeepEqual(live, before) {
		t.Fatal("retained guardrails enabled goal target publication in VUser mode")
	}
	return changed
}

func shutdownAuthoringFidelityServer(t *testing.T, server *Server) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Errorf("shutdown owned fidelity server: %v", err)
	}
}

func assertAuthoringFidelityScenario(t *testing.T, got *StoredScenario, want CreateScenarioRequest) {
	t.Helper()
	if got.WorkloadShapeID != *want.WorkloadShapeID || len(got.Scripts) != len(want.Groups) || !reflect.DeepEqual(got.GoalConfig.WorkloadShapeConfig, want.GoalConfig.WorkloadShapeConfig) {
		t.Fatalf("root/workload scenario snapshot lost: %#v", got)
	}
	assertAuthoringFidelityRuntime(t, got.RuntimeSettings, want.RuntimeSettings)
	for index, group := range got.Scripts {
		before := want.Groups[index]
		if group.Name != before.Name || group.ScriptID != before.ScriptID || group.StartOffset != before.StartOffset || group.VUsers != before.VUsers || group.RampUp != before.RampUp || group.RampDown != before.RampDown || group.Initialization != before.InitStrategy || !reflect.DeepEqual(group.Stages, before.Stages) || !reflect.DeepEqual(group.TransactionResponseTimeSLAMs, before.TransactionResponseTimeSLAMs) {
			t.Fatalf("group %d fields lost: %#v", index, group)
		}
		assertAuthoringFidelityRuntime(t, group.RuntimeSettings, before.RuntimeSettings)
	}
}

func assertAuthoringFidelityRun(t *testing.T, got, want *ManagedTest) {
	t.Helper()
	if got.Status != "QUEUED" || len(got.Groups) != len(want.Groups) || !reflect.DeepEqual(got.GoalConfig.WorkloadShapeConfig, want.GoalConfig.WorkloadShapeConfig) {
		t.Fatal("queued run workload/groups changed")
	}
	if got.ELKCapture == nil || want.ELKCapture == nil || got.ELKCapture.ConnectionID != want.ELKCapture.ConnectionID || got.ELKCapture.ConnectionName != want.ELKCapture.ConnectionName || got.DynatraceCapture == nil || want.DynatraceCapture == nil || got.DynatraceCapture.ConnectionID != want.DynatraceCapture.ConnectionID || got.DynatraceCapture.ConnectionName != want.DynatraceCapture.ConnectionName {
		t.Fatal("queued copy lost configured capture identity")
	}
	assertAuthoringFidelityRuntime(t, got.RuntimeSettings, want.RuntimeSettings)
	for index, group := range got.Groups {
		before := want.Groups[index]
		if group.Name != before.Name || group.ScriptID != before.ScriptID || group.Role != before.Role || group.VUsers != before.VUsers || group.RampUp != before.RampUp || group.RampDown != before.RampDown || group.StartOffset != before.StartOffset || group.Initialization != before.Initialization || !reflect.DeepEqual(group.Stages, before.Stages) || !reflect.DeepEqual(group.IterationOverride, before.IterationOverride) || !reflect.DeepEqual(group.TransactionResponseTimeSLAMs, before.TransactionResponseTimeSLAMs) {
			t.Fatalf("run group %d fields lost: %#v", index, group)
		}
		assertAuthoringFidelityRuntime(t, group.RuntimeSettings, before.RuntimeSettings)
	}
}

func assertAuthoringFidelityRuntime(t *testing.T, got, want *RuntimeSettingsOverride) {
	t.Helper()
	a, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatalf("authored runtime mismatch: got %s want %s", a, b)
	}
}

// A portable library item is a complete snapshot: its omitted default shape
// means empty, while an ordinary partial update with nil must still preserve it.
// Recovery may compensate only its owned content, never a later editor's write.
func runAuthoringFidelityLibrarySnapshot(t *testing.T, server *Server, request CreateScenarioRequest, shape string) *StoredScenario {
	t.Helper()
	request = cloneScenarioRequestForLibrary(request)
	request.Name = "Linked library target"
	request.WorkloadShapeID = &shape
	before, err := server.runner.StoreScenarioWithIDCreateOnly("fidelity-library-linked", request)
	if err != nil {
		t.Fatal(err)
	}
	ordinary := createScenarioRequestFromStored(before)
	ordinary.Name = "Ordinary nil preserves shape"
	ordinary.WorkloadShapeID = nil
	before, found, err := server.runner.UpdateScenario(before.ID, ordinary)
	if err != nil || !found || before.WorkloadShapeID != shape {
		t.Fatalf("ordinary absent shape changed: found=%v err=%v snapshot=%#v", found, err, before)
	}
	incoming := portableScenarioRequestFromStored(before)
	incoming.Name = "Remote complete clear"
	incoming.WorkloadShapeID = nil
	incoming.Groups[0].TransactionResponseTimeSLAMs = float64Pointer(1400)
	item := LibraryBundleItem{Kind: "scenario", SourceInstanceID: "fidelity-source", SourceItemID: "source-scenario", ProjectID: storage.DefaultProjectID, Scenario: &LibraryScenarioPayload{ID: "source-scenario", Request: incoming}, ContentHash: hashCanonical(incoming), importMutationID: "fidelity-library-owned-clear"}
	const remote = "fidelity-library-remote"
	if err := server.rememberLibraryLink(remote, storage.DefaultProjectID, item.Kind, item.SourceInstanceID, item.SourceItemID, before.ID, item.SourceItemID, hashLibraryScenario(before), hashCanonical(portableScenarioRequestFromStored(before))); err != nil {
		t.Fatal(err)
	}
	linkBefore := server.exactLibraryLink(remote, storage.DefaultProjectID, item.Kind, item.SourceInstanceID, item.SourceItemID)
	invalid := item
	invalid.ContentHash = "sha256:invalid"
	if _, _, conflict := server.importLibraryScenario(invalid, storage.DefaultProjectID, remote, nil); conflict == nil {
		t.Fatal("complete adapter bypassed raw supplied content hash")
	}
	if untouched, found := server.runner.GetScenario(before.ID); !found || untouched.WorkloadShapeID != shape || untouched.Name != before.Name || !untouched.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatal("invalid raw content hash mutated destination")
	}
	importOnce := func() *StoredScenario {
		t.Helper()
		id, action, conflict := server.importLibraryScenario(item, storage.DefaultProjectID, remote, nil)
		if conflict != nil || id != before.ID || action != "updated" {
			t.Fatalf("linked import id=%s action=%s conflict=%#v", id, action, conflict)
		}
		current, found := server.runner.GetScenario(id)
		if !found || current.WorkloadShapeID != "" || current.TerminationMode != "iterations" || current.Scripts[0].TransactionResponseTimeSLAMs == nil || *current.Scripts[0].TransactionResponseTimeSLAMs != 1400 {
			t.Fatalf("portable complete snapshot did not clear/retain authored fields: %#v", current)
		}
		if item.Scenario.Request.WorkloadShapeID != nil || hashCanonical(item.Scenario.Request) != item.ContentHash || before.WorkloadShapeID != shape || *before.Scripts[0].TransactionResponseTimeSLAMs != *request.Groups[0].TransactionResponseTimeSLAMs {
			t.Fatal("import mutated portable source or prior snapshot")
		}
		if server.libraryScenarioRecoveryHash(current) != server.libraryScenarioRequestRecoveryHash(incoming) {
			t.Fatal("materialized clear diverged from portable recovery ownership hash")
		}
		return current
	}
	importOnce()
	claim := libraryImportClaim{Prepared: true, Kind: item.Kind, RemoteID: remote, ProjectID: storage.DefaultProjectID, SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, MutationID: item.importMutationID, LocalItemID: before.ID, BeforeScenario: cloneStoredScenarioForRead(before), BeforeLink: cloneLibrarySyncLink(linkBefore), IntendedHash: server.libraryScenarioRequestRecoveryHash(incoming)}
	if err := server.recoverLibraryImportClaim(claim); err != nil {
		t.Fatal(err)
	}
	restored, found := server.runner.GetScenario(before.ID)
	if !found || restored.WorkloadShapeID != shape || restored.Name != before.Name || !reflect.DeepEqual(restored.Scripts[0].TransactionResponseTimeSLAMs, before.Scripts[0].TransactionResponseTimeSLAMs) {
		t.Fatal("owned library recovery failed to restore full named/SLA snapshot")
	}
	current := importOnce()
	later := createScenarioRequestFromStored(current)
	later.Name = "Later editor owns clear"
	if _, err := server.runner.UpdateScenarioIfUnchanged(current.ID, later, current.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	if err := server.recoverLibraryImportClaim(claim); err != nil {
		t.Fatal(err)
	}
	protected, found := server.runner.GetScenario(before.ID)
	if !found || protected.Name != later.Name || protected.WorkloadShapeID != "" || *protected.Scripts[0].TransactionResponseTimeSLAMs != 1400 {
		t.Fatal("library recovery overwrote later editor")
	}
	return protected
}
