package api

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestAuthoringFidelityResultsSectionPrecedence(t *testing.T) {
	include := true
	base := &RuntimeSettingsOverride{Results: &dsl.ResultsConfig{MessageDetail: &dsl.MessageDetailConfig{Mode: "errors", IncludeResources: &include}, PropertyAnalytics: []dsl.PropertyAnalytics{{Name: "root", Extraction: "category", Values: []string{"one"}}}}}
	for _, tc := range []struct {
		name  string
		group *dsl.ResultsConfig
	}{
		{name: "absent inherits"},
		{name: "present empty replaces", group: &dsl.ResultsConfig{}},
		{name: "explicit off false zero replaces", group: &dsl.ResultsConfig{MessageDetail: &dsl.MessageDetailConfig{Mode: "off", IncludeResources: boolPointer(false), SampleRate: 0, MaxItems: 0}}},
		{name: "csv only replaces", group: &dsl.ResultsConfig{CSVExports: []dsl.CSVExport{{ID: "group", FileName: "group.csv", ActionName: "call", Columns: []dsl.CSVExportColumn{{Name: "latency", Metric: "latency_ms"}}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			merged := mergeRuntimeSettings(base, &RuntimeSettingsOverride{Results: tc.group})
			want := tc.group
			if want == nil {
				want = base.Results
			}
			if !reflect.DeepEqual(merged.Results, want) {
				t.Fatalf("Results = %#v, want %#v", merged.Results, want)
			}
			script := &dsl.Script{Config: dsl.ScriptConfig{Results: *base.Results}}
			if err := applyRuntimeOverrides(script, merged); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(script.Config.Results, *want) {
				t.Fatalf("compiled results = %#v, want %#v", script.Config.Results, *want)
			}
			if merged.Results.MessageDetail != nil {
				merged.Results.MessageDetail.Mode = "sampled"
			}
			if base.Results.MessageDetail.Mode != "errors" {
				t.Fatal("merge aliases root Results")
			}
			if tc.group != nil && tc.group.MessageDetail != nil && tc.group.MessageDetail.Mode != "off" {
				t.Fatal("merge aliases group Results")
			}
		})
	}
}

func TestAuthoringFidelityScriptResultsProjection(t *testing.T) {
	for _, results := range []dsl.ResultsConfig{
		{CSVExports: []dsl.CSVExport{{ID: "csv", FileName: "detail.csv", ActionName: "call", Columns: []dsl.CSVExportColumn{{Name: "latency", Metric: "latency_ms"}}}}},
		{PropertyAnalytics: []dsl.PropertyAnalytics{{Name: "category", Extraction: "category", Values: []string{"one", "two"}}}},
		{MessageDetail: &dsl.MessageDetailConfig{Mode: "off", IncludeResources: boolPointer(false)}},
	} {
		projected := runtimeSettingsFromScriptConfig(dsl.ScriptConfig{Results: results})
		if projected == nil || !reflect.DeepEqual(projected.Results, &results) {
			t.Fatalf("Results projection = %#v, want %#v", projected, results)
		}
		if len(projected.Results.PropertyAnalytics) > 0 {
			projected.Results.PropertyAnalytics[0].Values[0] = "changed"
			if results.PropertyAnalytics[0].Values[0] != "one" {
				t.Fatal("projection aliases nested result values")
			}
		}
	}
	if got := runtimeSettingsFromScriptConfig(dsl.ScriptConfig{}); got != nil {
		t.Fatalf("absent config materialized %#v", got)
	}
}

func TestAuthoringFidelityStoredScenarioConversion(t *testing.T) {
	sla, iterations := 1250.0, 3
	stored := &StoredScenario{ID: "scenario", Name: "scenario", WorkloadShapeID: "shape", TerminationMode: "iterations", Scripts: []ScenarioScriptRef{{ScriptID: "script", Name: "group", Iterations: &iterations, TransactionResponseTimeSLAMs: &sla, RuntimeSettings: &RuntimeSettingsOverride{Results: &dsl.ResultsConfig{MessageDetail: &dsl.MessageDetailConfig{Mode: "off"}}, WebSocket: &dsl.WebSocketConfig{ReadTimeoutMs: 0}}}}}
	if row := storedScenarioToStorage(stored); row.WorkloadShapeID != "shape" {
		t.Fatalf("storage conversion shape = %q", row.WorkloadShapeID)
	}
	req := createScenarioRequestFromStored(stored)
	if req.TerminationMode != "iterations" || req.WorkloadShapeID == nil || *req.WorkloadShapeID != "shape" || req.Groups[0].TransactionResponseTimeSLAMs == nil || *req.Groups[0].TransactionResponseTimeSLAMs != sla {
		t.Fatalf("snapshot conversion lost authored fields: %#v groups=%#v", req, req.Groups)
	}
	*req.Groups[0].TransactionResponseTimeSLAMs = 999
	req.Groups[0].RuntimeSettings.Results.MessageDetail.Mode = "errors"
	if *stored.Scripts[0].TransactionResponseTimeSLAMs != sla || stored.Scripts[0].RuntimeSettings.Results.MessageDetail.Mode != "off" {
		t.Fatal("snapshot request aliases stored authored fields")
	}
	stored.WorkloadShapeID = ""
	if clear := createScenarioRequestFromStored(stored); clear.WorkloadShapeID == nil || *clear.WorkloadShapeID != "" {
		t.Fatal("snapshot omitted explicit empty workload shape")
	}
}

func TestAuthoringFidelityLibraryRecoveryHashDefaults(t *testing.T) {
	server := NewServer(ServerConfig{})
	t.Cleanup(func() { shutdownAuthoringFidelityServer(t, server) })
	incoming := CreateScenarioRequest{Name: "Legacy default receipt", Duration: "1m", ScenarioMode: "vuser_driven", Groups: []CreateGroupEntry{{Name: "group", ScriptID: "script", Role: "load", VUsers: 1, InitStrategy: "at_runtime"}}}
	const historicalHash = "sha256:6eeffb5863893f13d4c8e52625205e905a7e0e3b8922ab8bca9d522d65e57eba"
	if got := hashCanonical(incoming); got != historicalHash {
		t.Fatalf("historical default wire golden changed: %s", got)
	}
	stored := &StoredScenario{Name: incoming.Name, Duration: incoming.Duration, ScenarioMode: incoming.ScenarioMode, TerminationMode: "duration", Scripts: []ScenarioScriptRef{{Name: "group", ScriptID: "script", Role: "load", VUsers: 1, Initialization: "at_runtime"}}}
	if got := server.libraryScenarioRequestRecoveryHash(incoming); got != historicalHash {
		t.Fatalf("incoming default recovery hash changed: got %s want %s", got, historicalHash)
	}
	if got := server.libraryScenarioRecoveryHash(stored); got != historicalHash {
		t.Fatalf("full durable default snapshot does not match legacy receipt: got %s want %s", got, historicalHash)
	}
	stored.ID = "portable-default-scenario"
	server.runner.scenarios[stored.ID] = stored
	exported, err := server.libraryScenarioBundleItem(stored.ID)
	if err != nil {
		t.Fatal(err)
	}
	if exported.ContentHash != historicalHash || hashLibraryScenario(stored) != historicalHash || hashCanonical(exported.Scenario.Request) != historicalHash {
		t.Fatal("portable export and local/default legacy hash disagree")
	}
	if exported.Scenario.Request.TerminationMode != "" || exported.Scenario.Request.WorkloadShapeID != nil {
		t.Fatal("portable defaults were not omitted")
	}
	full := createScenarioRequestFromStored(stored)
	if full.TerminationMode != "duration" || full.WorkloadShapeID == nil {
		t.Fatal("recovery normalization leaked into full snapshot")
	}
	full.TerminationMode = "iterations"
	if server.libraryScenarioRequestRecoveryHash(full) == historicalHash {
		t.Fatal("non-default termination mode discarded from recovery ownership evidence")
	}
	full.TerminationMode = "duration"
	shape := "named-shape"
	full.WorkloadShapeID = &shape
	if server.libraryScenarioRequestRecoveryHash(full) == historicalHash {
		t.Fatal("nonempty workload shape discarded from recovery ownership evidence")
	}
}

func TestAuthoringFidelityPortableHashRetainsNondefaults(t *testing.T) {
	stored := &StoredScenario{Name: "Portable", Duration: "1m", ScenarioMode: "vuser_driven", TerminationMode: "duration", Scripts: []ScenarioScriptRef{{Name: "group", ScriptID: "script", Role: "load", VUsers: 1, Initialization: "at_runtime"}}}
	originalHash := hashLibraryScenario(stored)
	for _, tc := range []struct {
		name   string
		change func(*StoredScenario)
	}{
		{name: "iteration termination", change: func(s *StoredScenario) {
			s.TerminationMode = "iterations"
			s.Duration = ""
			count := 3
			s.Scripts[0].Iterations = &count
		}},
		{name: "group SLA", change: func(s *StoredScenario) { s.Scripts[0].TransactionResponseTimeSLAMs = float64Pointer(1250) }},
		{name: "named shape", change: func(s *StoredScenario) { s.WorkloadShapeID = "named-shape" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := cloneStoredScenarioForRead(stored)
			tc.change(changed)
			portable := portableScenarioRequestFromStored(changed)
			if hashLibraryScenario(changed) == originalHash || hashLibraryScenario(changed) != hashCanonical(portable) {
				t.Fatal("portable hash discarded or diverged from non-default authored fields")
			}
			if portable.TerminationMode == "iterations" && portable.Groups[0].Iterations == nil {
				t.Fatal("iteration snapshot lost group count")
			}
			if stored.TerminationMode != "duration" || stored.Scripts[0].TransactionResponseTimeSLAMs != nil || stored.WorkloadShapeID != "" {
				t.Fatal("portable converter mutated source")
			}
		})
	}
}

func TestAuthoringFidelityStartDelayClearValidation(t *testing.T) {
	admitted := &ManagedTest{ID: "queued-source", Status: "QUEUED", StartDelay: "10s"}
	started := *admitted
	now := time.Now().UTC()
	started.StartedAt = &now
	iterationSource := *admitted
	iterationSource.TerminationMode = "iterations"
	unsupportedSource := *admitted
	unsupportedSource.TerminationMode = "unsupported"
	for _, tc := range []struct {
		name, delay, revision, project, mode string
		source                               *ManagedTest
		valid                                bool
	}{
		{name: "ordinary absent", valid: true},
		{name: "ordinary positive", delay: "1ns", valid: true},
		{name: "ordinary iteration positive", delay: "1ns", mode: "iterations", valid: true},
		{name: "ordinary zero rejected", delay: "0s"},
		{name: "client source ID alone rejected", delay: "0s", revision: "queued-source"},
		{name: "admitted revision canonical clear", delay: "0s", revision: "queued-source", source: admitted, valid: true},
		{name: "revision positive", delay: "1ns", revision: "queued-source", source: admitted, valid: true},
		{name: "revision negative rejected", delay: "-1s", revision: "queued-source", source: admitted},
		{name: "revision noncanonical zero rejected", delay: "0ms", revision: "queued-source", source: admitted},
		{name: "revision numeric zero rejected", delay: "0", revision: "queued-source", source: admitted},
		{name: "blank source rejected", delay: "0s", revision: "  ", source: admitted},
		{name: "different source rejected", delay: "0s", revision: "fake-source", source: admitted},
		{name: "foreign source rejected", delay: "0s", revision: "queued-source", project: "foreign", source: admitted},
		{name: "already started source rejected", delay: "0s", revision: "queued-source", source: &started},
		{name: "iteration conversion zero rejected", delay: "0s", revision: "queued-source", mode: "iterations", source: admitted},
		{name: "duration conversion zero rejected", delay: "0s", revision: "queued-source", source: &iterationSource},
		{name: "iteration matching zero clear", delay: "0s", revision: "queued-source", mode: "iterations", source: &iterationSource, valid: true},
		{name: "iteration positive compatibility", delay: "1ns", revision: "queued-source", mode: "iterations", source: &iterationSource, valid: true},
		{name: "unsupported mode source rejected", delay: "0s", revision: "queued-source", source: &unsupportedSource},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := CreateTestRequest{Duration: "1m", StartDelay: tc.delay, RevisionSourceTestID: tc.revision, ProjectID: tc.project, TerminationMode: tc.mode}
			if tc.mode == "iterations" {
				request.Duration = ""
			}
			_, err := validateScenarioRequestWithRevisionSource(request, tc.source)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
			// The ordinary/scenario validator never admits an explicit zero,
			// even when a client sends a source ID resembling an admitted source.
			if tc.delay == "0s" {
				if _, err := validateScenarioRequest(request); err == nil {
					t.Fatal("ordinary validation trusted a client revision ID")
				}
			}
		})
	}
}

func TestAuthoringFidelityScenarioDelayCompatibilityAndForgedRevisionRefusal(t *testing.T) {
	runner := NewTestRunner(10, NewHub(), nil)
	script := httpScript("scenario-delay-source", "Scenario delay source")
	script.ProjectID = "project-a"
	if _, err := runner.StoreScriptDurable(script); err != nil {
		t.Fatal(err)
	}
	iterations := 3
	for _, mode := range []string{dsl.TerminationDuration, dsl.TerminationIterations} {
		t.Run(mode, func(t *testing.T) {
			duration := "1m"
			group := CreateGroupEntry{Name: "orders", ScriptID: script.ID, VUsers: 1}
			if mode == dsl.TerminationIterations {
				duration = ""
				group.Iterations = &iterations
			}
			request := CreateTestRequest{Name: "Positive delay " + mode, ProjectID: script.ProjectID, Duration: duration, TerminationMode: mode, StartDelay: "10s", Groups: []CreateGroupEntry{group}}
			source, err := runner.CreateTest(request)
			if err != nil || source.StartDelay != "10s" || dsl.CanonicalTerminationMode(source.TerminationMode) != mode {
				t.Fatalf("ordinary positive delay rejected or changed: source=%v err=%v", source != nil, err)
			}
			stored, err := runner.StoreScenario(CreateScenarioRequest{Name: "Positive scenario delay " + mode, ProjectID: script.ProjectID, Duration: duration, TerminationMode: mode, StartDelay: "10s", Groups: []CreateGroupEntry{group}})
			if err != nil || stored.StartDelay != "10s" || dsl.CanonicalTerminationMode(stored.TerminationMode) != mode {
				t.Fatalf("scenario positive delay rejected or changed: scenario=%v err=%v", stored != nil, err)
			}
			// Scenario wire input cannot turn the strict validator into revision
			// admission, even by naming an actual owned queued test.
			wire, err := json.Marshal(map[string]any{"name": "Forbidden scenario zero " + mode, "project_id": script.ProjectID, "duration": duration, "termination_mode": mode, "start_delay": "0s", "groups": []CreateGroupEntry{group}, "revision_source_test_id": source.ID})
			if err != nil {
				t.Fatal(err)
			}
			var forgedScenario CreateScenarioRequest
			if err := json.Unmarshal(wire, &forgedScenario); err != nil {
				t.Fatal(err)
			}
			if _, err := runner.StoreScenario(forgedScenario); err == nil || !strings.Contains(err.Error(), "start_delay") {
				t.Fatalf("scenario trusted forged revision field: %v", err)
			}
			request.StartDelay = "0s"
			request.RevisionSourceTestID = "forged-source"
			if copied, err := runner.CreateTest(request); copied != nil || err == nil || !strings.Contains(err.Error(), "not found") {
				t.Fatalf("ordinary zero trusted forged source: created=%v err=%v", copied != nil, err)
			}
			unchanged, found := runner.GetTest(source.ID)
			if !found || unchanged == nil || unchanged.StartDelay != "10s" || unchanged.DurationStr != duration || dsl.CanonicalTerminationMode(unchanged.TerminationMode) != mode {
				t.Fatal("rejected zero request changed the queued source")
			}
		})
	}
}

func TestAuthoringFidelityRevisionDelayClearCannotBypassSourceOwnership(t *testing.T) {
	runner := NewTestRunner(10, NewHub(), nil)
	script := httpScript("delay-source", "Delay source")
	script.ProjectID = "project-a"
	if _, err := runner.StoreScriptDurable(script); err != nil {
		t.Fatal(err)
	}
	source, err := runner.CreateTest(CreateTestRequest{ProjectID: "project-a", Duration: "1m", StartDelay: "10s", Groups: []CreateGroupEntry{{Name: "orders", ScriptID: script.ID, VUsers: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, source, project, contains string }{
		{"missing", "missing-source", "project-a", "not found"},
		{"foreign", source.ID, "project-b", "one project"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := runner.CreateTest(CreateTestRequest{Name: tc.name, RevisionSourceTestID: tc.source, ProjectID: tc.project, StartDelay: "0s"}); err == nil || !strings.Contains(err.Error(), tc.contains) {
				t.Fatalf("source ownership refused incorrectly: %v", err)
			}
		})
	}
	runner.mu.Lock()
	runner.tests[source.ID].Status = "RUNNING"
	runner.mu.Unlock()
	if _, err := runner.CreateTest(CreateTestRequest{RevisionSourceTestID: source.ID, StartDelay: "0s"}); err == nil || !strings.Contains(err.Error(), "must be QUEUED") {
		t.Fatalf("started source admitted zero-delay copy: %v", err)
	}
}

func TestAuthoringFidelityRevisionDelayClearRechecksSourceAtFinalFence(t *testing.T) {
	for _, tc := range []struct {
		name, errorContains string
		change              func(*ManagedTest)
	}{
		{"started", "must be QUEUED", func(source *ManagedTest) { source.Status = "RUNNING"; now := time.Now().UTC(); source.StartedAt = &now }},
		{"ended only", "start_delay", func(source *ManagedTest) { now := time.Now().UTC(); source.EndedAt = &now }},
		{"mode only", "iteration termination", func(source *ManagedTest) { source.TerminationMode = "iterations" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := NewTestRunner(10, NewHub(), nil)
			script := httpScript("delay-fence-source", "Delay fence source")
			if _, err := runner.StoreScriptDurable(script); err != nil {
				t.Fatal(err)
			}
			source, err := runner.CreateTest(CreateTestRequest{Duration: "1m", StartDelay: "10s", Groups: []CreateGroupEntry{{Name: "orders", ScriptID: script.ID, VUsers: 1}}})
			if err != nil {
				t.Fatal(err)
			}
			sequence := &blockingRunSequenceSettingsStore{values: make(map[string]string), entered: make(chan struct{}), release: make(chan struct{})}
			t.Cleanup(func() {
				select {
				case <-sequence.release:
				default:
					close(sequence.release)
				}
			})
			runner.mu.Lock()
			runner.settingsStore = sequence
			runner.runRefSeqAllocator = newRunReferenceSequenceAllocator(runner.envCode, sequence)
			runner.mu.Unlock()
			result := make(chan managedTestCreateResult, 1)
			go func() {
				run, err := runner.CreateTest(CreateTestRequest{Name: "Delayed concurrent revision", RevisionSourceTestID: source.ID, StartDelay: "0s"})
				result <- managedTestCreateResult{test: run, err: err}
			}()
			select {
			case <-sequence.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("revision did not cross validation before the final source fence")
			}
			runner.mu.Lock()
			tc.change(runner.tests[source.ID])
			runner.mu.Unlock()
			close(sequence.release)
			select {
			case got := <-result:
				if got.test != nil || got.err == nil || !strings.Contains(got.err.Error(), tc.errorContains) {
					t.Fatalf("changed source committed a cleared revision: created=%v err=%v", got.test != nil, got.err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("owned revision did not join after source invalidation")
			}
			runner.mu.RLock()
			defer runner.mu.RUnlock()
			if len(runner.tests) != 1 || runner.tests[source.ID].StartDelay != "10s" {
				t.Fatal("refused revision installed a copy or changed source delay")
			}
		})
	}
}
