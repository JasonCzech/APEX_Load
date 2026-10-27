package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

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
		var scheduleRequest CreateScenarioScheduleRequest
		if err := json.Unmarshal([]byte(`{"name":"Nightly","cron_expr":"0 9 * * *","analysis_policy_selections":`+value+`}`), &scheduleRequest); err != nil {
			t.Fatal(value, err)
		}
		if scheduleRequest.AnalysisPolicySelections == nil {
			t.Fatal("explicit schedule selection lost")
		}
	}
}

func TestScenarioScheduleAnalysisPolicySelectionValidationMaterializationAndTombstone(t *testing.T) {
	t.Setenv(config.EnvArchiveDir, t.TempDir())
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { _ = store.Close() })
	runner := server.runner
	script := httpScript("schedule-analysis-policy-script", "Schedule analysis policy")
	script.ProjectID = storage.DefaultProjectID
	runner.StoreScript(script)

	browser := storage.AnalysisDefinition{
		ID:         "chosen-browser",
		ProjectID:  storage.DefaultProjectID,
		Kind:       storage.AnalysisDefinitionKindBrowserPolicy,
		Name:       "Chosen Browser v1",
		Definition: json.RawMessage(selectedBrowserPolicyJSON),
	}
	if _, err := store.SaveAnalysisDefinition(browser, 0); err != nil {
		t.Fatal(err)
	}
	browser.Name = "Chosen Browser v2"
	if _, err := store.SaveAnalysisDefinition(browser, 1); err != nil {
		t.Fatal(err)
	}
	journey := storage.AnalysisDefinition{
		ID:         "chosen-journey",
		ProjectID:  storage.DefaultProjectID,
		Kind:       storage.AnalysisDefinitionKindJourneyPolicy,
		Name:       "Chosen Journey v1",
		Definition: json.RawMessage(`{"version":1,"journeys":[{"id":"checkout","name":"Checkout","start":{"transaction":"Browse"},"success":{"transaction":"Submit"},"weight":1}]}`),
	}
	if _, err := store.SaveAnalysisDefinition(journey, 0); err != nil {
		t.Fatal(err)
	}

	createdScenario := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scenarios", map[string]any{
		"name":     "Scheduled analysis policy scenario",
		"duration": "1m",
		"groups":   []map[string]any{{"name": "Shop", "script_id": script.ID, "vusers": 1}},
	}, "")
	if createdScenario.Code != http.StatusCreated {
		t.Fatalf("scenario create: %d %s", createdScenario.Code, createdScenario.Body.String())
	}
	scenario := decodeScenarioResponse(t, createdScenario)
	t.Cleanup(func() {
		runner.mu.Lock()
		if runner.scenarios[scenario.ID] != nil {
			runner.scenarios[scenario.ID].Schedules = nil
		}
		runner.mu.Unlock()
	})

	for _, badSelection := range []map[string]any{
		{"browser": map[string]any{"id": browser.ID, "revision": 0}},
		{"browser": map[string]any{"id": browser.ID, "revision": 99}},
		{"browser": map[string]any{"id": journey.ID, "revision": 1}},
		{"journey": map[string]any{"id": browser.ID, "revision": 1}},
		{"browser": map[string]any{"id": "missing-policy", "revision": 1}},
	} {
		previewResp := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scenarios/"+scenario.ID+"/schedules/preview", map[string]any{
			"name":                       "Invalid preview",
			"cron_expr":                  "0 9 * * *",
			"timezone":                   "UTC",
			"analysis_policy_selections": badSelection,
		}, "")
		if previewResp.Code != http.StatusConflict {
			t.Fatalf("invalid schedule preview selection %+v: got %d %s, want 409", badSelection, previewResp.Code, previewResp.Body.String())
		}
		createResp := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scenarios/"+scenario.ID+"/schedules", map[string]any{
			"name":                       "Invalid create",
			"cron_expr":                  "0 9 * * *",
			"timezone":                   "UTC",
			"analysis_policy_selections": badSelection,
		}, "")
		if createResp.Code != http.StatusConflict {
			t.Fatalf("invalid schedule create selection %+v: got %d %s, want 409", badSelection, createResp.Code, createResp.Body.String())
		}
	}

	validSelections := map[string]any{
		"browser": map[string]any{"id": browser.ID, "revision": 1},
		"journey": map[string]any{"id": journey.ID, "revision": 1},
	}
	runner.mu.Lock()
	runner.testStore = unavailablePolicyStore{store}
	runner.mu.Unlock()
	outagePreview := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scenarios/"+scenario.ID+"/schedules/preview", map[string]any{
		"name":                       "Outage preview",
		"cron_expr":                  "0 9 * * *",
		"timezone":                   "UTC",
		"analysis_policy_selections": validSelections,
	}, "")
	if outagePreview.Code != http.StatusServiceUnavailable {
		t.Fatalf("outage schedule preview: got %d %s, want 503", outagePreview.Code, outagePreview.Body.String())
	}
	runner.mu.Lock()
	runner.testStore = store
	runner.mu.Unlock()

	previewResp := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scenarios/"+scenario.ID+"/schedules/preview", map[string]any{
		"name":                       "Nightly pinned",
		"cron_expr":                  "0 9 * * *",
		"timezone":                   "UTC",
		"analysis_policy_selections": validSelections,
	}, "")
	if previewResp.Code != http.StatusOK {
		t.Fatalf("valid schedule preview: %d %s", previewResp.Code, previewResp.Body.String())
	}

	createResp := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scenarios/"+scenario.ID+"/schedules", map[string]any{
		"name":                       "Nightly pinned",
		"cron_expr":                  "0 9 * * *",
		"timezone":                   "UTC",
		"analysis_policy_selections": validSelections,
	}, "")
	if createResp.Code != http.StatusCreated {
		t.Fatalf("valid schedule create: %d %s", createResp.Code, createResp.Body.String())
	}
	var createdSchedule storage.ScenarioSchedule
	if err := json.Unmarshal(createResp.Body.Bytes(), &createdSchedule); err != nil {
		t.Fatal(err)
	}
	decodedCreated := decodeStoredRunAnalysisPolicySelections(createdSchedule.AnalysisPolicySelections)
	if decodedCreated == nil || decodedCreated.Browser == nil || decodedCreated.Browser.ID != browser.ID || decodedCreated.Browser.Revision != 1 ||
		decodedCreated.Journey == nil || decodedCreated.Journey.ID != journey.ID || decodedCreated.Journey.Revision != 1 {
		t.Fatalf("created schedule lost policy selections: %+v", createdSchedule.AnalysisPolicySelections)
	}

	// Partial update omitting analysis_policy_selections preserves existing selections.
	preserveResp := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scenarios/"+scenario.ID+"/schedules/"+createdSchedule.ID, map[string]any{
		"name":              "Nightly pinned renamed",
		"cron_expr":         "0 9 * * *",
		"timezone":          "UTC",
		"expected_revision": createdSchedule.Revision,
	}, "")
	if preserveResp.Code != http.StatusOK {
		t.Fatalf("schedule preserve update: %d %s", preserveResp.Code, preserveResp.Body.String())
	}
	var preservedSchedule storage.ScenarioSchedule
	if err := json.Unmarshal(preserveResp.Body.Bytes(), &preservedSchedule); err != nil {
		t.Fatal(err)
	}
	decodedPreserved := decodeStoredRunAnalysisPolicySelections(preservedSchedule.AnalysisPolicySelections)
	if decodedPreserved == nil || decodedPreserved.Browser == nil || decodedPreserved.Browser.Revision != 1 || decodedPreserved.Journey == nil || decodedPreserved.Journey.Revision != 1 {
		t.Fatalf("omitted analysis_policy_selections failed to preserve existing selection: %+v", preservedSchedule.AnalysisPolicySelections)
	}

	// Explicit empty object {} clears selections back to project defaults (nil).
	clearResp := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scenarios/"+scenario.ID+"/schedules/"+createdSchedule.ID, map[string]any{
		"name":                       "Nightly defaults",
		"cron_expr":                  "0 9 * * *",
		"timezone":                   "UTC",
		"expected_revision":          preservedSchedule.Revision,
		"analysis_policy_selections": map[string]any{},
	}, "")
	if clearResp.Code != http.StatusOK {
		t.Fatalf("schedule clear update: %d %s", clearResp.Code, clearResp.Body.String())
	}
	var clearedSchedule storage.ScenarioSchedule
	if err := json.Unmarshal(clearResp.Body.Bytes(), &clearedSchedule); err != nil {
		t.Fatal(err)
	}
	if clearedSchedule.AnalysisPolicySelections != nil {
		t.Fatalf("expected nil AnalysisPolicySelections after explicit empty selection, got %+v", clearedSchedule.AnalysisPolicySelections)
	}

	// Re-apply explicit revision 1 selections for materialization.
	restoreResp := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scenarios/"+scenario.ID+"/schedules/"+createdSchedule.ID, map[string]any{
		"name":                       "Nightly pinned",
		"cron_expr":                  "0 9 * * *",
		"timezone":                   "UTC",
		"expected_revision":          clearedSchedule.Revision,
		"analysis_policy_selections": validSelections,
	}, "")
	if restoreResp.Code != http.StatusOK {
		t.Fatalf("schedule restore update: %d %s", restoreResp.Code, restoreResp.Body.String())
	}
	createdSchedule = storage.ScenarioSchedule{}
	if err := json.Unmarshal(restoreResp.Body.Bytes(), &createdSchedule); err != nil {
		t.Fatal(err)
	}

	backend := newTelemetryPolicyBarrierBackend(t)
	runner.setExecutionBackend(backend)
	now := time.Now().UTC()
	due := now.Truncate(time.Minute).Add(-time.Minute)
	schedule := createdSchedule
	schedule.NextRunAt = &due
	runner.mu.Lock()
	if err := runner.saveScenarioScheduleLocked(&schedule); err != nil {
		runner.mu.Unlock()
		t.Fatal(err)
	}
	prepared, runErr := runner.runScenarioScheduleLocked(&schedule, now)
	runner.mu.Unlock()
	if runErr != nil || prepared == nil {
		t.Fatalf("scheduled materialization with pinned revision 1 failed: prepared=%+v err=%v", prepared, runErr)
	}
	go runner.runScenarioScheduledTestWithTerm(prepared, executionLeaseTerm{})
	backend.expected.Store(true)
	waitTelemetryBackend(t, backend)
	record, err := store.GetTest(prepared.ID)
	if err != nil {
		t.Fatal(err)
	}
	initial := decodeStoredInitialProvenance(record.InitialProvenance)
	if initial == nil || len(initial.PolicyPins) != 2 {
		t.Fatalf("expected 2 pinned policies on materialized scheduled run, got %+v", initial)
	}
	for _, pin := range initial.PolicyPins {
		if pin.Source != "run_selection" || pin.Revision != 1 {
			t.Fatalf("materialized scheduled run did not freeze selected revision 1: %+v", pin)
		}
	}
	finishTelemetryBackend(t, backend)

	// Tombstone the chosen browser policy; preview/update must reject with 409,
	// and the next due occurrence must record Status="failed", ReasonCode="analysis_policy_unavailable".
	browser.Deleted = true
	if _, err := store.SaveAnalysisDefinition(browser, 2); err != nil {
		t.Fatal(err)
	}
	tombstonedPreview := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scenarios/"+scenario.ID+"/schedules/preview", map[string]any{
		"name":                       "Nightly pinned",
		"cron_expr":                  "0 9 * * *",
		"timezone":                   "UTC",
		"analysis_policy_selections": validSelections,
	}, "")
	if tombstonedPreview.Code != http.StatusConflict {
		t.Fatalf("tombstoned policy preview: got %d %s, want 409", tombstonedPreview.Code, tombstonedPreview.Body.String())
	}
	tombstonedUpdate := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scenarios/"+scenario.ID+"/schedules/"+schedule.ID, map[string]any{
		"name":              "Nightly pinned",
		"cron_expr":         "0 9 * * *",
		"timezone":          "UTC",
		"expected_revision": schedule.Revision,
	}, "")
	if tombstonedUpdate.Code != http.StatusConflict {
		t.Fatalf("tombstoned policy update: got %d %s, want 409", tombstonedUpdate.Code, tombstonedUpdate.Body.String())
	}

	nextDue := now.Truncate(time.Minute)
	schedule.NextRunAt = &nextDue
	runner.mu.Lock()
	if err := runner.saveScenarioScheduleLocked(&schedule); err != nil {
		runner.mu.Unlock()
		t.Fatal(err)
	}
	_, tombstonedRunErr := runner.runScenarioScheduleLocked(&schedule, now.Add(time.Minute))
	runner.mu.Unlock()
	if !errors.Is(tombstonedRunErr, errRunPolicyConfiguration) || !errors.Is(tombstonedRunErr, errAnalysisPolicySelectionUnavailable) {
		t.Fatalf("expected analysis_policy_unavailable error on tombstoned scheduled occurrence, got %v", tombstonedRunErr)
	}
	failedOccurrence, err := store.GetScenarioScheduleOccurrenceByScheduleAndPlannedAt(schedule.ID, nextDue)
	if err != nil || failedOccurrence == nil {
		t.Fatalf("GetScenarioScheduleOccurrenceByScheduleAndPlannedAt: %+v %v", failedOccurrence, err)
	}
	if failedOccurrence.Status != "failed" || failedOccurrence.ReasonCode != "analysis_policy_unavailable" {
		t.Fatalf("expected failed occurrence with ReasonCode=analysis_policy_unavailable, got %+v", failedOccurrence)
	}
}

func TestCIRunCarriesTheChosenAnalysisPolicySelections(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer target.Close()
	policy := ciRunSourceTestPolicy()
	policy.MaxDurationMinutes = 1
	policy.AllowedTargetHosts = []string{"127.0.0.1"}
	locked, _ := newCIContractTestServer(t, policy)
	policy.AllowTelemetryPolicySelection = true
	server, store := newCIContractTestServer(t, policy)

	browser := storage.AnalysisDefinition{
		ID: "ci-browser", ProjectID: "project-ci", Kind: storage.AnalysisDefinitionKindBrowserPolicy,
		Name: "CI Browser v1", Definition: json.RawMessage(selectedBrowserPolicyJSON),
	}
	if _, err := store.SaveAnalysisDefinition(browser, 0); err != nil {
		t.Fatal(err)
	}
	browser.Name = "CI Browser v2"
	if _, err := store.SaveAnalysisDefinition(browser, 1); err != nil {
		t.Fatal(err)
	}
	journey := storage.AnalysisDefinition{
		ID: "ci-journey", ProjectID: "project-ci", Kind: storage.AnalysisDefinitionKindJourneyPolicy,
		Name: "CI Journey v1", Definition: json.RawMessage(`{"version":1,"journeys":[{"id":"checkout","name":"Checkout","start":{"transaction":"Browse"},"success":{"transaction":"Submit"},"weight":1}]}`),
	}
	if _, err := store.SaveAnalysisDefinition(journey, 0); err != nil {
		t.Fatal(err)
	}

	publishCheck := false
	scriptRaw, err := json.Marshal(ciTestScript(target.URL))
	if err != nil {
		t.Fatal(err)
	}
	request := ciRunCreateRequest{
		ProjectID: "project-ci", ScriptPath: "tests/performance.json", Script: scriptRaw,
		Execution: ciRunExecutionRequest{VUsers: 1, Duration: "50ms"}, Source: ciTestRunSource("1"),
		PublishCheck: &publishCheck,
		AnalysisPolicySelections: &RunAnalysisPolicySelections{
			Browser: &RunAnalysisPolicySelection{ID: browser.ID, Revision: 1},
			Journey: &RunAnalysisPolicySelection{ID: journey.ID, Revision: 1},
		},
	}
	invoke := func(srv *Server, key string, req ciRunCreateRequest) (int, ciRunResponse, string) {
		t.Helper()
		body, err := json.Marshal(req)
		if err != nil {
			t.Fatal(err)
		}
		httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/ci/runs", bytes.NewReader(body))
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Idempotency-Key", key)
		recorder := httptest.NewRecorder()
		srv.handleCreateCIRun(recorder, httpReq)
		var response ciRunResponse
		if recorder.Code == http.StatusAccepted {
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
		}
		return recorder.Code, response, recorder.Body.String()
	}

	// Disallowed when project's GitHub Actions policy does not set allow_telemetry_policy_selection.
	if status, _, body := invoke(locked, "octo/apex:performance:9001:1:analysis-policy-0000", request); status != http.StatusForbidden || !strings.Contains(body, "allow_telemetry_policy_selection") {
		t.Fatalf("expected 403 when allow_telemetry_policy_selection is false, got %d %s", status, body)
	}

	// Empty selection {} needs no permission.
	emptyReq := request
	emptyReq.AnalysisPolicySelections = &RunAnalysisPolicySelections{}
	if status, resp, body := invoke(locked, "octo/apex:performance:9001:1:analysis-policy-empty", emptyReq); status != http.StatusAccepted || resp.AnalysisPolicySelections != nil {
		t.Fatalf("expected 202 for empty selection without permission, got %d %s", status, body)
	}

	// Valid selection succeeds and echoes in ciRunResponse and on the created ManagedTest.
	const key = "octo/apex:performance:9001:1:analysis-policy-aaaa"
	status, created, body := invoke(server, key, request)
	if status != http.StatusAccepted || created.TestID == "" || created.AnalysisPolicySelections == nil ||
		created.AnalysisPolicySelections.Browser == nil || created.AnalysisPolicySelections.Browser.ID != browser.ID || created.AnalysisPolicySelections.Browser.Revision != 1 ||
		created.AnalysisPolicySelections.Journey == nil || created.AnalysisPolicySelections.Journey.ID != journey.ID || created.AnalysisPolicySelections.Journey.Revision != 1 {
		t.Fatalf("CI run with analysis_policy_selections: %d %s", status, body)
	}
	if run, ok := server.runner.GetTest(created.TestID); !ok || !reflect.DeepEqual(run.AnalysisPolicySelections, request.AnalysisPolicySelections) {
		t.Fatalf("CI run's stored AnalysisPolicySelections: %+v", run)
	}

	// Retry under the same Idempotency-Key with a different revision returns 409 Conflict.
	changed := request
	changed.AnalysisPolicySelections = &RunAnalysisPolicySelections{
		Browser: &RunAnalysisPolicySelection{ID: browser.ID, Revision: 2},
		Journey: &RunAnalysisPolicySelection{ID: journey.ID, Revision: 1},
	}
	if status, _, body := invoke(server, key, changed); status != http.StatusConflict {
		t.Fatalf("expected 409 on idempotency retry with different analysis policy revision, got %d %s", status, body)
	}

	// Missing policy revision returns 409 Conflict.
	missing := request
	missing.AnalysisPolicySelections = &RunAnalysisPolicySelections{
		Browser: &RunAnalysisPolicySelection{ID: browser.ID, Revision: 99},
	}
	if status, _, body := invoke(server, "octo/apex:performance:9001:1:analysis-policy-bbbb", missing); status != http.StatusConflict {
		t.Fatalf("expected 409 on missing analysis policy revision, got %d %s", status, body)
	}

	// Exact retry returns 202 even after policy selection is disabled and policy is tombstoned.
	project, err := store.GetProject("project-ci")
	if err != nil {
		t.Fatal(err)
	}
	project.Settings.GitHub.Actions.AllowTelemetryPolicySelection = false
	if err := store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	browser.Deleted = true
	if _, err := store.SaveAnalysisDefinition(browser, 2); err != nil {
		t.Fatal(err)
	}
	if status, retried, body := invoke(server, key, request); status != http.StatusAccepted || retried.TestID != created.TestID ||
		retried.AnalysisPolicySelections == nil || retried.AnalysisPolicySelections.Browser == nil || retried.AnalysisPolicySelections.Browser.Revision != 1 {
		t.Fatalf("exact idempotency retry after tombstone/lockdown failed: %d %s", status, body)
	}
}

