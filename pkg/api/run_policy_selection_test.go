package api

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/storage"
)

const selectedBrowserPolicyJSON = `{"version":1,"metrics":[{"metric":"lcp","good":2500,"poor":4000,"weight":1}],"baseline":{"mode":"none"}}`

func TestRunAnalysisPolicySelectionValidationAndStoredFailure(t *testing.T) {
	for _, selection := range []*RunAnalysisPolicySelection{{}, {ID: "x", Revision: 0}, {ID: " x", Revision: 1}, {ID: "x", Revision: -1}} {
		if _, err := normalizeRunAnalysisPolicySelections(&RunAnalysisPolicySelections{Browser: selection}); !errors.Is(err, errRunPolicyConfiguration) {
			t.Fatal("invalid selection accepted", selection, err)
		}
	}
	initial := &RunAnalysisPolicySelections{Browser: &RunAnalysisPolicySelection{ID: "x", Revision: 1}, Journey: &RunAnalysisPolicySelection{ID: "y", Revision: 2}}
	copied, err := normalizeRunAnalysisPolicySelections(initial)
	if err != nil || !reflect.DeepEqual(initial, copied) {
		t.Fatal(copied, err)
	}
	copied.Browser.ID = "changed"
	copied.Journey.Revision = 9
	if initial.Browser.ID != "x" || initial.Journey.Revision != 2 {
		t.Fatal("selection aliases caller")
	}
	for _, raw := range []any{map[string]any{"browser": "bad"}, map[string]any{"browser": map[string]any{"id": "x", "revision": 1, "sha256": "client-minted"}}, map[string]any{"telemetry": map[string]any{"id": "x", "revision": 1}}} {
		if _, err := normalizeRunAnalysisPolicySelections(decodeStoredRunAnalysisPolicySelections(raw)); !errors.Is(err, errRunPolicyConfiguration) {
			t.Fatal("bad stored selection fell back to defaults", raw, err)
		}
	}
	if runAnalysisPolicySelectionsForStorage(nil) != nil || decodeStoredRunAnalysisPolicySelections(nil) != nil {
		t.Fatal("legacy selection is not nil")
	}
}

func TestRunAnalysisPolicySelectionOverridesOnlyChosenKindAndRefusesDeleted(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { _ = store.Close() })
	project := storage.DefaultProjectID
	selected := storage.AnalysisDefinition{ID: "selected-browser", ProjectID: project, Kind: storage.AnalysisDefinitionKindBrowserPolicy, Name: "Selected", Definition: json.RawMessage(selectedBrowserPolicyJSON)}
	if _, err := store.SaveAnalysisDefinition(selected, 0); err != nil {
		t.Fatal(err)
	}
	selected.Name = "Later revision"
	if _, err := store.SaveAnalysisDefinition(selected, 1); err != nil {
		t.Fatal(err)
	}
	defaultPolicy := storage.AnalysisDefinition{ID: "default-browser", ProjectID: project, Kind: storage.AnalysisDefinitionKindBrowserPolicy, Name: "Default", Definition: json.RawMessage(`{"version":1,"apply_by_default":true,"metrics":[{"metric":"lcp","good":2000,"poor":3000,"weight":1}],"baseline":{"mode":"none"}}`)}
	if _, err := store.SaveAnalysisDefinition(defaultPolicy, 0); err != nil {
		t.Fatal(err)
	}
	choice := &RunAnalysisPolicySelections{Browser: &RunAnalysisPolicySelection{ID: selected.ID, Revision: 1}}
	pins, _, err := server.runner.resolveRunPolicyPinsWithSelections(project, choice)
	if err != nil || len(pins) != 1 || pins[0].ID != selected.ID || pins[0].Revision != 1 || pins[0].Name != "Selected" || pins[0].Source != "run_selection" {
		t.Fatal(pins, err)
	}
	defaults, _, err := server.runner.resolveRunPolicyPins(project)
	if err != nil || len(defaults) != 1 || defaults[0].ID != defaultPolicy.ID || defaults[0].Source != "project_default" {
		t.Fatal(defaults, err)
	}
	for _, tc := range []struct {
		project string
		choice  *RunAnalysisPolicySelections
	}{{"foreign", choice}, {project, &RunAnalysisPolicySelections{Journey: choice.Browser}}, {project, &RunAnalysisPolicySelections{Browser: &RunAnalysisPolicySelection{ID: selected.ID, Revision: 99}}}} {
		if _, _, err := server.runner.resolveRunPolicyPinsWithSelections(tc.project, tc.choice); !errors.Is(err, errRunPolicyConfiguration) {
			t.Fatal("invalid selection silently used defaults", err)
		}
	}
	selected.Deleted = true
	if _, err := store.SaveAnalysisDefinition(selected, 2); err != nil {
		t.Fatal(err)
	}
	if _, _, err := server.runner.resolveRunPolicyPinsWithSelections(project, choice); !errors.Is(err, errRunPolicyConfiguration) {
		t.Fatal("deleted selection admitted", err)
	}
	if old, err := store.GetAnalysisDefinition(project, selected.ID, 1); err != nil || old.Deleted {
		t.Fatal("deletion erased historical evidence", old, err)
	}
}

func TestRunAnalysisPolicySelectionSurvivesQueuedRevisionReloadAndAdmission(t *testing.T) {
	t.Setenv(config.EnvArchiveDir, t.TempDir())
	runner, store, source := newPassiveTransitionTestRunner(t, "selected-policy-admission")
	policy := storage.AnalysisDefinition{ID: "chosen", ProjectID: normalizeProjectID(source.ProjectID), Kind: storage.AnalysisDefinitionKindBrowserPolicy, Name: "Chosen", Definition: json.RawMessage(selectedBrowserPolicyJSON)}
	if _, err := store.SaveAnalysisDefinition(policy, 0); err != nil {
		t.Fatal(err)
	}
	selection := &RunAnalysisPolicySelections{Browser: &RunAnalysisPolicySelection{ID: policy.ID, Revision: 1}}
	created, err := runner.CreateTest(CreateTestRequest{Name: "Explicit revision", ScriptID: source.Groups[0].ScriptID, VUsers: 1, Duration: "1m", AnalysisPolicySelections: selection})
	if err != nil {
		t.Fatal(err)
	}
	selection.Browser.Revision = 99
	revised, err := runner.CreateTest(CreateTestRequest{Name: "Revised queued copy", RevisionSourceTestID: created.ID})
	if err != nil {
		t.Fatal(err)
	}
	if revised.AnalysisPolicySelections == nil || revised.AnalysisPolicySelections.Browser.Revision != 1 {
		t.Fatal("queued copy lost selected revision", revised.AnalysisPolicySelections)
	}

	cleared, err := runner.CreateTest(CreateTestRequest{Name: "Explicit project defaults", RevisionSourceTestID: created.ID, AnalysisPolicySelections: &RunAnalysisPolicySelections{}})
	if err != nil {
		t.Fatal(err)
	}
	if cleared.AnalysisPolicySelections == nil || cleared.AnalysisPolicySelections.Browser != nil || cleared.AnalysisPolicySelections.Journey != nil {
		t.Fatal("explicit defaults inherited revision", cleared.AnalysisPolicySelections)
	}
	if err := runner.reloadStoredTests(); err != nil {
		t.Fatal(err)
	}
	read, ok := runner.GetTest(revised.ID)
	if !ok || read == nil || read.AnalysisPolicySelections == nil || read.AnalysisPolicySelections.Browser.Revision != 1 {
		t.Fatal("reload lost selected revision")
	}
	policy.Name = "Revision two"
	if _, err := store.SaveAnalysisDefinition(policy, 1); err != nil {
		t.Fatal(err)
	}
	backend := newTelemetryPolicyBarrierBackend(t)
	runner.setExecutionBackend(backend)
	if err := runner.StartTest(revised.ID); err != nil {
		t.Fatal(err)
	}
	backend.expected.Store(true)
	waitTelemetryBackend(t, backend)
	record, err := store.GetTest(revised.ID)
	if err != nil {
		t.Fatal(err)
	}
	initial := decodeStoredInitialProvenance(record.InitialProvenance)
	if initial == nil || len(initial.PolicyPins) != 1 || initial.PolicyPins[0].Revision != 1 || initial.PolicyPins[0].Source != "run_selection" {
		t.Fatal("admission changed selection", initial)
	}
	finishTelemetryBackend(t, backend)
}

func TestRunAnalysisPolicySelectionJourneyKeepsOtherKindDefault(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { _ = store.Close() })
	project := storage.DefaultProjectID
	browser := storage.AnalysisDefinition{ID: "default-browser", ProjectID: project, Kind: storage.AnalysisDefinitionKindBrowserPolicy, Name: "Browser default", Definition: json.RawMessage(`{"version":1,"apply_by_default":true,"metrics":[{"metric":"lcp","good":2000,"poor":3000,"weight":1}],"baseline":{"mode":"none"}}`)}
	journey := storage.AnalysisDefinition{ID: "selected-journey", ProjectID: project, Kind: storage.AnalysisDefinitionKindJourneyPolicy, Name: "Journey original", Definition: json.RawMessage(`{"version":1,"journeys":[{"id":"checkout","name":"Checkout","start":{"transaction":"Browse"},"success":{"transaction":"Submit"},"weight":1}]}`)}
	for _, definition := range []storage.AnalysisDefinition{browser, journey} {
		if _, err := store.SaveAnalysisDefinition(definition, 0); err != nil {
			t.Fatal(err)
		}
	}
	journey.Name = "Journey edited"
	if _, err := store.SaveAnalysisDefinition(journey, 1); err != nil {
		t.Fatal(err)
	}
	pins, _, err := server.runner.resolveRunPolicyPinsWithSelections(project, &RunAnalysisPolicySelections{Journey: &RunAnalysisPolicySelection{ID: journey.ID, Revision: 1}})
	if err != nil || len(pins) != 2 {
		t.Fatal(pins, err)
	}
	found := map[string]RunPolicyPin{}
	for _, pin := range pins {
		found[pin.Kind] = pin
	}
	if got := found[storage.AnalysisDefinitionKindBrowserPolicy]; got.ID != browser.ID || got.Source != "project_default" {
		t.Fatal("journey choice displaced browser default", got)
	}
	if got := found[storage.AnalysisDefinitionKindJourneyPolicy]; got.ID != journey.ID || got.Revision != 1 || got.Name != "Journey original" || got.Source != "run_selection" {
		t.Fatal("journey choice drifted", got)
	}
	journey.Deleted = true
	if _, err := store.SaveAnalysisDefinition(journey, 2); err != nil {
		t.Fatal(err)
	}
	if _, _, err := server.runner.resolveRunPolicyPinsWithSelections(project, &RunAnalysisPolicySelections{Journey: &RunAnalysisPolicySelection{ID: journey.ID, Revision: 1}}); !errors.Is(err, errRunPolicyConfiguration) {
		t.Fatal("deleted journey selection became a default", err)
	}
}

func TestRunAnalysisPolicySelectionRequestUsesClosedContract(t *testing.T) {
	for _, value := range []string{
		`{"browser":{"id":"chosen","revision":1,"sha256":"client-minted"}}`,
		`{"browser":{"id":"chosen","revision":1,"revision":2}}`,
		`{"browser":null}`,
		`{"telemetry":{"id":"chosen","revision":1}}`,
		`{"browser":{"id":"chosen","revision":1},"browser":{"id":"other","revision":1}}`,
	} {
		var request CreateTestRequest
		if err := json.Unmarshal([]byte(`{"analysis_policy_selections":`+value+`}`), &request); err == nil {
			t.Fatal("invalid authored selection accepted", value)
		}
	}
	for _, value := range []string{`{}`, `{"browser":{"id":"chosen","revision":1}}`, `{"journey":{"id":"chosen","revision":1}}`} {
		var request CreateTestRequest
		if err := json.Unmarshal([]byte(`{"analysis_policy_selections":`+value+`}`), &request); err != nil {
			t.Fatal(value, err)
		}
		if request.AnalysisPolicySelections == nil {
			t.Fatal("explicit selection lost")
		}
	}
}
