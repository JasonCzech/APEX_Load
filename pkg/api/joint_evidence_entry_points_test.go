package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

// jointEntryScript promotes one bounded property category, so a projection
// may group by it.
func jointEntryScript(id string) *dsl.Script {
	script := httpScript(id, "Joint entry")
	script.ProjectID = storage.DefaultProjectID
	script.Config.Results.PropertyAnalytics = []dsl.PropertyAnalytics{{Name: "operation", Extraction: "operation_type", Values: []string{"checkout", "search"}}}
	return script
}

var jointEntryProjection = &engine.JointProjection{Population: "action", Dimensions: []string{"name", "group", "property:operation"}}

// requireRunProjection checks a run's projection and the one Start freezes
// into its provenance.
func requireRunProjection(t *testing.T, entry string, test *ManagedTest, want *engine.JointProjection) {
	t.Helper()
	if test == nil {
		t.Fatalf("%s: no run", entry)
	}
	if !reflect.DeepEqual(test.JointEvidence, want) {
		t.Fatalf("%s: run projection %+v, want %+v", entry, test.JointEvidence, want)
	}
	if provenance := buildRunArchiveProvenanceFromSnapshot(test); !reflect.DeepEqual(provenance.JointEvidence, want) {
		t.Fatalf("%s: frozen projection %+v, want %+v", entry, provenance.JointEvidence, want)
	}
}

// NA-B: one scenario fixture yields the same joint evidence projection,
// and so the same captured evidence, through every entry point that runs a
// scenario: a direct run, an execution-plan entry and a schedule.
func TestJointEvidenceCarriesThroughEveryScenarioEntryPoint(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()
	runner := server.runner
	runner.StoreScript(jointEntryScript("joint-entry-script"))

	created := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scenarios", map[string]any{
		"name": "Joint entry", "duration": "1m", "joint_evidence": jointEntryProjection,
		"groups": []map[string]any{{"name": "Shop", "script_id": "joint-entry-script", "vusers": 1}},
	}, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("scenario create: %d %s", created.Code, created.Body.String())
	}
	var scenario StoredScenario
	if err := json.Unmarshal(created.Body.Bytes(), &scenario); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(scenario.JointEvidence, jointEntryProjection) {
		t.Fatalf("scenario projection %+v", scenario.JointEvidence)
	}
	// The scenario keeps it durably and shows it to its editors.
	durable, _, err := store.GetScenario(scenario.ID)
	if err != nil || !reflect.DeepEqual(decodeStoredJointEvidence(durable.JointEvidence), jointEntryProjection) {
		t.Fatalf("stored projection %+v: %v", durable, err)
	}
	authoring := authoringAPIRequest(t, server, http.MethodGet, "/api/v1/scenarios/"+scenario.ID+"?view=authoring", nil, "")
	var projection ScenarioAuthoringProjection
	if err := json.Unmarshal(authoring.Body.Bytes(), &projection); err != nil || authoring.Code != http.StatusOK || !reflect.DeepEqual(projection.Scenario.JointEvidence, jointEntryProjection) {
		t.Fatalf("authoring projection: %d %s", authoring.Code, authoring.Body.String())
	}

	// A direct run of the scenario.
	direct := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/tests", map[string]any{"name": "Direct", "scenario_id": scenario.ID}, "")
	var directRun ManagedTest
	if err := json.Unmarshal(direct.Body.Bytes(), &directRun); err != nil || direct.Code != http.StatusCreated {
		t.Fatalf("direct run: %d %s", direct.Code, direct.Body.String())
	}
	requireRunProjection(t, "direct run", runner.tests[directRun.ID], jointEntryProjection)

	// A run that names its own projection keeps it.
	other := &engine.JointProjection{Population: "transaction", Dimensions: []string{"group"}}
	overridden := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/tests", map[string]any{"name": "Overridden", "scenario_id": scenario.ID, "joint_evidence": other}, "")
	var overriddenRun ManagedTest
	if err := json.Unmarshal(overridden.Body.Bytes(), &overriddenRun); err != nil || overridden.Code != http.StatusCreated {
		t.Fatalf("overridden run: %d %s", overridden.Code, overridden.Body.String())
	}
	requireRunProjection(t, "overridden run", runner.tests[overriddenRun.ID], other)

	// An execution-plan entry.
	planRequest, err := buildPlanEntryCreateRequest("Joint plan", storage.DefaultProjectID, nil, storage.ExecutionPlanEntry{ScenarioID: scenario.ID})
	if err != nil {
		t.Fatal(err)
	}
	planned, err := runner.CreateTest(planRequest)
	if err != nil {
		t.Fatalf("plan entry run: %v", err)
	}
	requireRunProjection(t, "plan entry", runner.tests[planned.ID], jointEntryProjection)

	// A schedule occurrence, prepared without dispatching a backend.
	now := time.Now()
	due := now.UTC().Truncate(time.Minute).Add(-time.Minute)
	schedule := &storage.ScenarioSchedule{
		ID: "joint-entry-schedule", ScenarioID: scenario.ID, ProjectID: scenario.ProjectID, Name: "Joint schedule",
		RunNameTemplate: "Joint scheduled run", CronExpr: "*/5 * * * *", Timezone: "UTC", Enabled: true, NextRunAt: &due,
		CreatedAt: due.Add(-time.Hour), UpdatedAt: due.Add(-time.Hour),
	}
	runner.mu.Lock()
	err = runner.saveScenarioScheduleLocked(schedule)
	runner.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		runner.mu.Lock()
		for id, run := range runner.tests {
			if run.ScenarioID == scenario.ID && run.cancel != nil {
				run.cancel()
			}
			if run.ScenarioID == scenario.ID {
				delete(runner.tests, id)
			}
		}
		runner.scenarios[scenario.ID].Schedules = nil
		runner.mu.Unlock()
	})
	runner.mu.Lock()
	_, err = runner.runScenarioScheduleLocked(schedule, now)
	runner.mu.Unlock()
	if err != nil {
		t.Fatalf("materialize schedule: %v", err)
	}
	var scheduled *ManagedTest
	runner.mu.RLock()
	for _, run := range runner.tests {
		if run.ScenarioID == scenario.ID && run.Name == "Joint scheduled run" {
			scheduled = run
		}
	}
	runner.mu.RUnlock()
	requireRunProjection(t, "schedule", scheduled, jointEntryProjection)
}

// NA-B: only a property category one of the run's scripts promotes (after
// runtime overrides) may be projected, on a scenario or a run.
func TestJointEvidenceAdmitsOnlyPromotedPropertyCategories(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()
	server.runner.StoreScript(jointEntryScript("joint-promoted-script"))
	plain := httpScript("joint-plain-script", "Plain")
	plain.ProjectID = storage.DefaultProjectID
	server.runner.StoreScript(plain)
	unpromoted := &engine.JointProjection{Population: "action", Dimensions: []string{"name", "property:tier"}}

	scenario := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scenarios", map[string]any{
		"name": "Unpromoted", "duration": "1m", "joint_evidence": unpromoted,
		"groups": []map[string]any{{"script_id": "joint-promoted-script", "vusers": 1}},
	}, "")
	if scenario.Code != http.StatusBadRequest || !strings.Contains(scenario.Body.String(), `property category \"tier\" is not promoted`) {
		t.Fatalf("scenario with an unpromoted category: %d %s", scenario.Code, scenario.Body.String())
	}
	run := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/tests", map[string]any{
		"name": "Unpromoted run", "duration": "1m", "joint_evidence": jointEntryProjection,
		"groups": []map[string]any{{"script_id": "joint-plain-script", "vusers": 1}},
	}, "")
	if run.Code != http.StatusBadRequest || !strings.Contains(run.Body.String(), `property category \"operation\" is not promoted`) {
		t.Fatalf("run whose scripts promote no category: %d %s", run.Code, run.Body.String())
	}
	// A run's runtime settings may promote the category for its groups.
	promoted := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/tests", map[string]any{
		"name": "Promoted by override", "duration": "1m", "joint_evidence": unpromoted,
		"runtime_settings": map[string]any{"results": map[string]any{"property_analytics": []map[string]any{{"name": "tier", "extraction": "plan_tier", "values": []string{"gold", "silver"}}}}},
		"groups":           []map[string]any{{"script_id": "joint-plain-script", "vusers": 1}},
	}, "")
	var promotedRun ManagedTest
	if err := json.Unmarshal(promoted.Body.Bytes(), &promotedRun); err != nil || promoted.Code != http.StatusCreated || !reflect.DeepEqual(promotedRun.JointEvidence, unpromoted) {
		t.Fatalf("run promoting the category by override: %d %s", promoted.Code, promoted.Body.String())
	}
	// The engine's own cold/warm browser iteration category needs no promotion.
	browserIteration := &engine.JointProjection{Population: "transaction", Dimensions: []string{"name", "property:browser_iteration"}}
	if run := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/tests", map[string]any{
		"name": "Browser iteration", "duration": "1m", "joint_evidence": browserIteration,
		"groups": []map[string]any{{"script_id": "joint-plain-script", "vusers": 1}},
	}, ""); run.Code != http.StatusCreated {
		t.Fatalf("run grouped by browser_iteration: %d %s", run.Code, run.Body.String())
	}

	// A revised copy keeps the projection its source run was admitted with,
	// even one an earlier controller admitted unpromoted.
	server.runner.mu.Lock()
	server.runner.tests[promotedRun.ID].JointEvidence = jointEntryProjection.Clone()
	server.runner.mu.Unlock()
	revised := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/tests", map[string]any{"name": "Revised", "revision_source_test_id": promotedRun.ID}, "")
	var revisedRun ManagedTest
	if err := json.Unmarshal(revised.Body.Bytes(), &revisedRun); err != nil || revised.Code != http.StatusCreated || !reflect.DeepEqual(revisedRun.JointEvidence, jointEntryProjection) {
		t.Fatalf("revised copy of a run admitted with an unpromoted category: %d %s", revised.Code, revised.Body.String())
	}
	// A copy of a run whose groups promoted its category cannot drop the
	// promotion and keep the projection.
	admitted := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/tests", map[string]any{
		"name": "Admitted", "duration": "1m", "joint_evidence": unpromoted,
		"runtime_settings": map[string]any{"results": map[string]any{"property_analytics": []map[string]any{{"name": "tier", "extraction": "plan_tier", "values": []string{"gold", "silver"}}}}},
		"groups":           []map[string]any{{"script_id": "joint-plain-script", "vusers": 1}},
	}, "")
	var admittedRun ManagedTest
	if err := json.Unmarshal(admitted.Body.Bytes(), &admittedRun); err != nil || admitted.Code != http.StatusCreated {
		t.Fatalf("admitted run: %d %s", admitted.Code, admitted.Body.String())
	}
	dropped := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/tests", map[string]any{
		"name": "Dropped promotion", "revision_source_test_id": admittedRun.ID,
		"runtime_settings": map[string]any{"results": map[string]any{"message_detail": map[string]any{"mode": "off"}}},
	}, "")
	if dropped.Code != http.StatusBadRequest || !strings.Contains(dropped.Body.String(), `property category \"tier\" is not promoted`) {
		t.Fatalf("revised copy that drops its source's promotion: %d %s", dropped.Code, dropped.Body.String())
	}
}

// NA-B: copying runtime settings between a scenario's groups cannot drop a
// promotion its joint evidence groups by.
func TestJointEvidenceRuntimeApplyCannotDropAPromotedCategory(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()
	plain := httpScript("joint-apply-script", "Plain")
	plain.ProjectID = storage.DefaultProjectID
	server.runner.StoreScript(plain)
	tier := &engine.JointProjection{Population: "action", Dimensions: []string{"name", "property:tier"}}
	created := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scenarios", map[string]any{
		"name": "Apply runtime", "duration": "1m", "joint_evidence": tier,
		"groups": []map[string]any{
			{"name": "Promotes", "script_id": "joint-apply-script", "vusers": 1, "runtime_settings": map[string]any{"results": map[string]any{"property_analytics": []map[string]any{{"name": "tier", "extraction": "plan_tier", "values": []string{"gold"}}}}}},
			{"name": "Plain", "script_id": "joint-apply-script", "vusers": 1, "runtime_settings": map[string]any{"results": map[string]any{"message_detail": map[string]any{"mode": "off"}}}},
		},
	}, "")
	var scenario StoredScenario
	if err := json.Unmarshal(created.Body.Bytes(), &scenario); err != nil || created.Code != http.StatusCreated {
		t.Fatalf("scenario: %d %s", created.Code, created.Body.String())
	}
	applied := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scenarios/"+scenario.ID+"/runtime/apply-to-groups", map[string]any{"source_group": 1, "target_groups": []int{0}, "fields": []string{"results"}}, "")
	if applied.Code < 400 || !strings.Contains(applied.Body.String(), `property category \"tier\" is not promoted`) {
		t.Fatalf("applying results that drop the promotion: %d %s", applied.Code, applied.Body.String())
	}
	durable, _, err := store.GetScenario(scenario.ID)
	if err != nil || !reflect.DeepEqual(decodeStoredJointEvidence(durable.JointEvidence), tier) {
		t.Fatalf("stored scenario after the refusal: %v", err)
	}
}

// NA-B: a CI run has no scenario, so it names its projection on the request;
// the projection is part of the request a retry under its key must repeat.
func TestJointEvidenceCarriesThroughCIRuns(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer target.Close()
	policy := ciRunSourceTestPolicy()
	policy.MaxDurationMinutes = 1
	policy.AllowedTargetHosts = []string{"127.0.0.1"}
	server, _ := newCIContractTestServer(t, policy)
	publishCheck := false
	scriptRaw, err := json.Marshal(ciTestScript(target.URL))
	if err != nil {
		t.Fatal(err)
	}
	projection := &engine.JointProjection{Population: "action", Dimensions: []string{"name", "group"}}
	request := ciRunCreateRequest{
		ProjectID: "project-ci", ScriptPath: "tests/performance.json", Script: scriptRaw,
		Execution: ciRunExecutionRequest{VUsers: 1, Duration: "50ms"}, Source: ciTestRunSource("1"),
		PublishCheck: &publishCheck, JointEvidence: projection,
	}
	invoke := func(key string, req ciRunCreateRequest) (int, ciRunResponse, string) {
		t.Helper()
		body, err := json.Marshal(req)
		if err != nil {
			t.Fatal(err)
		}
		httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/ci/runs", bytes.NewReader(body))
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Idempotency-Key", key)
		recorder := httptest.NewRecorder()
		server.handleCreateCIRun(recorder, httpReq)
		var response ciRunResponse
		if recorder.Code == http.StatusAccepted {
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
		}
		return recorder.Code, response, recorder.Body.String()
	}
	const key = "octo/apex:performance:9001:1:joint-evidence-aaaa"
	status, created, body := invoke(key, request)
	if status != http.StatusAccepted || created.TestID == "" {
		t.Fatalf("CI run: %d %s", status, body)
	}
	run, ok := server.runner.GetTest(created.TestID)
	if !ok {
		t.Fatal("CI run not found")
	}
	requireRunProjection(t, "CI run", run, projection)
	changed := request
	changed.JointEvidence = &engine.JointProjection{Population: "transaction", Dimensions: []string{"group"}}
	if status, _, body := invoke(key, changed); status != http.StatusConflict {
		t.Fatalf("a retry naming another projection: %d %s", status, body)
	}
	invalid := request
	invalid.JointEvidence = &engine.JointProjection{Population: "action", Dimensions: []string{"host"}}
	if status, _, body := invoke("octo/apex:performance:9001:1:joint-evidence-bbbb", invalid); status != http.StatusBadRequest || !strings.Contains(body, "joint_evidence") {
		t.Fatalf("an invalid projection: %d %s", status, body)
	}
}
