package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/storage"
	"github.com/jasonczech/apexload/pkg/telemetrypolicy"
)

func budgetScenarioBody(name, script, policy string) map[string]any {
	return map[string]any{
		"name": name, "duration": "1m", "telemetry_budget_policy_id": policy,
		"groups": []map[string]any{{"name": "Shop", "script_id": script, "vusers": 1}},
	}
}

func storeBudgetScenarioScript(t *testing.T, server *Server, id string) {
	t.Helper()
	script := httpScript(id, id)
	script.ProjectID = storage.DefaultProjectID
	server.runner.StoreScript(script)
}

// TEL-B: a scenario's chosen telemetry budget policy is validated when the
// scenario is saved (one of its own project's live, valid telemetry_policy
// definitions with live classification's sustained window), stored with it
// beside its acceptance choice and shown to its editors, and every run
// created from it inherits the choice: a direct run, an execution-plan entry
// and a schedule, unless the run names another or project_default.
func TestScenarioTelemetryBudgetPolicyCarriesThroughEveryEntryPoint(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()
	runner := server.runner
	storeBudgetScenarioScript(t, server, "scenario-budget-script")
	project := storage.DefaultProjectID
	seedPolicy(t, store, project, "scenario-budgets", storage.AnalysisDefinitionKindTelemetryPolicy, tightBudgetPolicy)
	seedPolicy(t, store, project, "run-budgets", storage.AnalysisDefinitionKindTelemetryPolicy, `{"version":1,"sample_age_ms":900}`)
	seedPolicy(t, store, project, "slow-window", storage.AnalysisDefinitionKindTelemetryPolicy, `{"version":1,"sustained_window_seconds":60}`)
	seedPolicy(t, store, project, "scenario-gate", storage.AnalysisDefinitionKindTelemetryAcceptancePolicy, `{"version":1,"min_wall_time_coverage":0.9}`)
	if err := store.SaveProject(&storage.Project{ID: "another-project", Name: "Another"}); err != nil {
		t.Fatal(err)
	}
	seedPolicy(t, store, "another-project", "foreign-budgets", storage.AnalysisDefinitionKindTelemetryPolicy, `{"version":1,"sample_age_ms":1}`)
	body := func(name, policy string) map[string]any {
		return budgetScenarioBody(name, "scenario-budget-script", policy)
	}

	// Refusals at save: missing, another project's and another kind's policy
	// alike (400); an invalid one, here another sustained window (409).
	missing := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scenarios", body("Missing", "missing-budgets"), "")
	foreign := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scenarios", body("Foreign", "foreign-budgets"), "")
	if missing.Code != http.StatusBadRequest || foreign.Code != http.StatusBadRequest || strings.ReplaceAll(foreign.Body.String(), "foreign-budgets", "X") != strings.ReplaceAll(missing.Body.String(), "missing-budgets", "X") {
		t.Fatalf("missing %d %s, foreign %d %s", missing.Code, missing.Body.String(), foreign.Code, foreign.Body.String())
	}
	if kind := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scenarios", body("Kind", "scenario-gate"), ""); kind.Code != http.StatusBadRequest || !strings.Contains(kind.Body.String(), "telemetry budget policy") {
		t.Fatalf("an acceptance policy's ID: %d %s", kind.Code, kind.Body.String())
	}
	if window := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scenarios", body("Window", "slow-window"), ""); window.Code != http.StatusConflict || !strings.Contains(window.Body.String(), "sustained window") {
		t.Fatalf("another sustained window: %d %s", window.Code, window.Body.String())
	}
	if long := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scenarios", body("Long", strings.Repeat("b", 129)), ""); long.Code != http.StatusBadRequest || !strings.Contains(long.Body.String(), "telemetry_budget_policy_id") {
		t.Fatalf("an over-long choice: %d %s", long.Code, long.Body.String())
	}
	if explicit := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scenarios", body("Explicit default", runPolicyProjectDefault), ""); explicit.Code != http.StatusCreated || decodeScenarioResponse(t, explicit).TelemetryBudgetPolicyID != "" {
		t.Fatalf("project_default: %d %s", explicit.Code, explicit.Body.String())
	}

	chosenBody := body("Chosen", " scenario-budgets ")
	chosenBody["telemetry_acceptance_policy_id"] = "scenario-gate"
	created := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scenarios", chosenBody, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("scenario create: %d %s", created.Code, created.Body.String())
	}
	scenario := decodeScenarioResponse(t, created)
	if scenario.TelemetryBudgetPolicyID != "scenario-budgets" || scenario.TelemetryAcceptancePolicyID != "scenario-gate" {
		t.Fatalf("scenario choices: budget %q, acceptance %q", scenario.TelemetryBudgetPolicyID, scenario.TelemetryAcceptancePolicyID)
	}
	if durable, _, err := store.GetScenario(scenario.ID); err != nil || durable.TelemetryBudgetPolicyID != "scenario-budgets" {
		t.Fatalf("stored choice %+v: %v", durable, err)
	}
	authoring := authoringAPIRequest(t, server, http.MethodGet, "/api/v1/scenarios/"+scenario.ID+"?view=authoring", nil, "")
	var projection ScenarioAuthoringProjection
	if err := json.Unmarshal(authoring.Body.Bytes(), &projection); err != nil || authoring.Code != http.StatusOK || projection.Scenario.TelemetryBudgetPolicyID != "scenario-budgets" {
		t.Fatalf("authoring projection: %d %s", authoring.Code, authoring.Body.String())
	}
	if restarted := NewTestRunner(2, NewHub(), store); restarted.scenarios[scenario.ID] == nil || restarted.scenarios[scenario.ID].TelemetryBudgetPolicyID != "scenario-budgets" {
		t.Fatal("a reloaded runner lost the scenario's choice")
	}

	run := func(name, policy string) *ManagedTest {
		t.Helper()
		body := map[string]any{"name": name, "scenario_id": scenario.ID}
		if policy != "" {
			body["telemetry_budget_policy_id"] = policy
		}
		response := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/tests", body, "")
		var created ManagedTest
		if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil || response.Code != http.StatusCreated {
			t.Fatalf("%s: %d %s", name, response.Code, response.Body.String())
		}
		if created.TelemetryBudgetPolicyID != runner.tests[created.ID].TelemetryBudgetPolicyID {
			t.Fatalf("%s: response echoed %q", name, created.TelemetryBudgetPolicyID)
		}
		return runner.tests[created.ID]
	}
	for _, tc := range []struct{ name, policy, want string }{
		{"Direct", "", "scenario-budgets"},
		{"Named", "run-budgets", "run-budgets"},
		{"Default", runPolicyProjectDefault, ""},
	} {
		got := run(tc.name, tc.policy)
		if got.TelemetryBudgetPolicyID != tc.want {
			t.Fatalf("%s run: choice %q, want %q", tc.name, got.TelemetryBudgetPolicyID, tc.want)
		}
		// The two choices are independent: the acceptance choice is inherited
		// whatever the run names for its budgets.
		if got.TelemetryAcceptancePolicyID != "scenario-gate" {
			t.Fatalf("%s run: acceptance choice %q", tc.name, got.TelemetryAcceptancePolicyID)
		}
	}

	planRequest, err := buildPlanEntryCreateRequest("Budget plan", project, nil, storage.ExecutionPlanEntry{ScenarioID: scenario.ID})
	if err != nil {
		t.Fatal(err)
	}
	planned, err := runner.CreateTest(planRequest)
	if err != nil || planned.TelemetryBudgetPolicyID != "scenario-budgets" {
		t.Fatalf("plan entry run: %+v %v", planned, err)
	}

	now := time.Now()
	due := now.UTC().Truncate(time.Minute).Add(-time.Minute)
	schedule := &storage.ScenarioSchedule{
		ID: "scenario-budget-schedule", ScenarioID: scenario.ID, ProjectID: scenario.ProjectID, Name: "Budget schedule",
		RunNameTemplate: "Budget scheduled run", CronExpr: "*/5 * * * *", Timezone: "UTC", Enabled: true, NextRunAt: &due,
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
	for _, candidate := range runner.tests {
		if candidate.ScenarioID == scenario.ID && candidate.Name == "Budget scheduled run" {
			scheduled = candidate
		}
	}
	runner.mu.RUnlock()
	if scheduled == nil || scheduled.TelemetryBudgetPolicyID != "scenario-budgets" {
		t.Fatalf("scheduled run: %+v", scheduled)
	}
}

// A scenario whose chosen budget policy was deleted, or changed to another
// sustained window, after it was saved refuses runs created from it, naming
// where the choice came from, rather than silently classifying them by the
// system threshold settings; a run naming project_default is still accepted.
// A save that keeps the choice is not judged again; a save that changes it to
// a missing policy is refused, and clearing it is accepted.
func TestScenarioTelemetryBudgetPolicyGoneRefusesInheritingRuns(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()
	storeBudgetScenarioScript(t, server, "scenario-budget-gone-script")
	project := storage.DefaultProjectID
	seedPolicy(t, store, project, "doomed-budgets", storage.AnalysisDefinitionKindTelemetryPolicy, tightBudgetPolicy)
	seedPolicy(t, store, project, "drifting-budgets", storage.AnalysisDefinitionKindTelemetryPolicy, tightBudgetPolicy)
	body := func(policy string) map[string]any {
		return budgetScenarioBody("Doomed", "scenario-budget-gone-script", policy)
	}
	created := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scenarios", body("doomed-budgets"), "")
	if created.Code != http.StatusCreated {
		t.Fatalf("scenario create: %d %s", created.Code, created.Body.String())
	}
	scenario := decodeScenarioResponse(t, created)
	deleteSeededPolicy(t, store, project, "doomed-budgets")

	refused := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/tests", map[string]any{"name": "Inherits", "scenario_id": scenario.ID}, "")
	if refused.Code != http.StatusBadRequest || !strings.Contains(refused.Body.String(), "scenario's chosen telemetry budget policy") || !strings.Contains(refused.Body.String(), "doomed-budgets") {
		t.Fatalf("a run inheriting a deleted choice: %d %s", refused.Code, refused.Body.String())
	}
	if accepted := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/tests", map[string]any{"name": "Default", "scenario_id": scenario.ID, "telemetry_budget_policy_id": runPolicyProjectDefault}, ""); accepted.Code != http.StatusCreated {
		t.Fatalf("a run naming project_default: %d %s", accepted.Code, accepted.Body.String())
	}

	tag := created.Header().Get("ETag")
	edited := body("doomed-budgets")
	edited["duration"] = "2m"
	kept := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scenarios/"+scenario.ID, edited, tag)
	if kept.Code != http.StatusOK || decodeScenarioResponse(t, kept).TelemetryBudgetPolicyID != "doomed-budgets" {
		t.Fatalf("an unrelated edit keeping the deleted choice: %d %s", kept.Code, kept.Body.String())
	}
	tag = kept.Header().Get("ETag")
	if changed := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scenarios/"+scenario.ID, body("missing-budgets"), tag); changed.Code != http.StatusBadRequest || !strings.Contains(changed.Body.String(), "missing-budgets") {
		t.Fatalf("a save choosing a missing policy: %d %s", changed.Code, changed.Body.String())
	}
	drifting := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scenarios/"+scenario.ID, body("drifting-budgets"), tag)
	if drifting.Code != http.StatusOK {
		t.Fatalf("a save choosing another policy: %d %s", drifting.Code, drifting.Body.String())
	}
	tag = drifting.Header().Get("ETag")
	// A policy revised to another sustained window refuses inheriting runs
	// with 409, naming the scenario's choice.
	policy, err := store.GetAnalysisDefinition(project, "drifting-budgets", 0)
	if err != nil || policy == nil {
		t.Fatalf("policy: %+v %v", policy, err)
	}
	policy.Definition = json.RawMessage(`{"version":1,"sustained_window_seconds":45}`)
	if _, err := store.SaveAnalysisDefinition(*policy, policy.Revision); err != nil {
		t.Fatal(err)
	}
	if window := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/tests", map[string]any{"name": "Window", "scenario_id": scenario.ID}, ""); window.Code != http.StatusConflict || !strings.Contains(window.Body.String(), "scenario's chosen telemetry budget policy") || !strings.Contains(window.Body.String(), "sustained window") {
		t.Fatalf("a run inheriting a choice with another window: %d %s", window.Code, window.Body.String())
	}
	cleared := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scenarios/"+scenario.ID, body(""), tag)
	if cleared.Code != http.StatusOK || decodeScenarioResponse(t, cleared).TelemetryBudgetPolicyID != "" {
		t.Fatalf("a save clearing the choice: %d %s", cleared.Code, cleared.Body.String())
	}
	if durable, _, err := store.GetScenario(scenario.ID); err != nil || durable.TelemetryBudgetPolicyID != "" {
		t.Fatalf("cleared choice %+v: %v", durable, err)
	}
}

// Policy failures at scenario save are answered like a run's: 409 for an
// unusable policy on update, and 503 (retryable) on create and update when
// the project's policies cannot be read. A save that keeps the choice needs
// no policy read.
func TestScenarioTelemetryBudgetPolicySaveStatuses(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()
	storeBudgetScenarioScript(t, server, "scenario-budget-status-script")
	seedPolicy(t, store, storage.DefaultProjectID, "status-window", storage.AnalysisDefinitionKindTelemetryPolicy, `{"version":1,"sustained_window_seconds":30}`)
	seedPolicy(t, store, storage.DefaultProjectID, "status-budgets", storage.AnalysisDefinitionKindTelemetryPolicy, tightBudgetPolicy)
	body := func(policy string) map[string]any {
		return budgetScenarioBody("Statuses", "scenario-budget-status-script", policy)
	}
	created := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scenarios", body("status-budgets"), "")
	if created.Code != http.StatusCreated {
		t.Fatalf("scenario create: %d %s", created.Code, created.Body.String())
	}
	scenario := decodeScenarioResponse(t, created)
	tag := created.Header().Get("ETag")
	if window := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scenarios/"+scenario.ID, body("status-window"), tag); window.Code != http.StatusConflict || !strings.Contains(window.Body.String(), "status-window") {
		t.Fatalf("an unusable policy on update: %d %s", window.Code, window.Body.String())
	}
	server.runner.mu.Lock()
	server.runner.testStore = unavailablePolicyStore{store}
	server.runner.mu.Unlock()
	if outage := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scenarios", body("any-budgets"), ""); outage.Code != http.StatusServiceUnavailable {
		t.Fatalf("create during an outage: %d %s", outage.Code, outage.Body.String())
	}
	if outage := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scenarios/"+scenario.ID, body("any-budgets"), tag); outage.Code != http.StatusServiceUnavailable {
		t.Fatalf("update during an outage: %d %s", outage.Code, outage.Body.String())
	}
	edited := body("status-budgets")
	edited["duration"] = "2m"
	kept := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scenarios/"+scenario.ID, edited, tag)
	if kept.Code != http.StatusOK || decodeScenarioResponse(t, kept).TelemetryBudgetPolicyID != "status-budgets" {
		t.Fatalf("an edit keeping the choice during an outage: %d %s", kept.Code, kept.Body.String())
	}
	current, _ := server.runner.GetScenario(scenario.ID)
	restore := createScenarioRequestFromStored(current)
	restore.Duration = "3m"
	if restored, err := server.runner.UpdateScenarioIfUnchanged(scenario.ID, restore, current.UpdatedAt); err != nil || restored.TelemetryBudgetPolicyID != "status-budgets" {
		t.Fatalf("a restore keeping the choice during an outage: %+v %v", restored, err)
	}
}

// A kept budget choice is judged again when the scenario's project, derived
// from its scripts, differs from the one the runner has for it.
func TestScenarioTelemetryBudgetPolicyKeptChoiceIsJudgedAfterAProjectChange(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()
	storeBudgetScenarioScript(t, server, "scenario-budget-project-script")
	seedPolicy(t, store, storage.DefaultProjectID, "project-budgets", storage.AnalysisDefinitionKindTelemetryPolicy, tightBudgetPolicy)
	scenario, err := server.runner.StoreScenario(CreateScenarioRequest{Name: "Budget project change", Duration: "1m", TelemetryBudgetPolicyID: "project-budgets",
		Groups: []CreateGroupEntry{{Name: "Shop", ScriptID: "scenario-budget-project-script", VUsers: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	deleteSeededPolicy(t, store, storage.DefaultProjectID, "project-budgets")
	keep := createScenarioRequestFromStored(scenario)
	keep.Duration = "2m"
	if _, _, err := server.runner.UpdateScenario(scenario.ID, keep); err != nil {
		t.Fatalf("a kept choice in the same project: %v", err)
	}
	server.runner.mu.Lock()
	server.runner.scenarios[scenario.ID].ProjectID = "another-project"
	server.runner.mu.Unlock()
	keep.Duration = "3m"
	if _, _, err := server.runner.UpdateScenario(scenario.ID, keep); err == nil || !strings.Contains(err.Error(), "project-budgets") {
		t.Fatalf("a kept choice after a project change: %v", err)
	}
}

// A schedule whose scenario's chosen budget policy was deleted records a
// failed occurrence naming it. A policy read that fails while the occurrence,
// a plan entry or a direct run of the scenario materializes keeps the
// scenario's choice, which Start pins or refuses; a run naming its own budget
// policy is still answered 503.
func TestScenarioTelemetryBudgetPolicyScheduleOutcomes(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()
	runner := server.runner
	storeBudgetScenarioScript(t, server, "scenario-budget-schedule-script")
	seedPolicy(t, store, storage.DefaultProjectID, "schedule-budgets", storage.AnalysisDefinitionKindTelemetryPolicy, tightBudgetPolicy)
	created := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scenarios", budgetScenarioBody("Scheduled budgets", "scenario-budget-schedule-script", "schedule-budgets"), "")
	if created.Code != http.StatusCreated {
		t.Fatalf("scenario create: %d %s", created.Code, created.Body.String())
	}
	scenario := decodeScenarioResponse(t, created)
	t.Cleanup(func() {
		runner.mu.Lock()
		runner.scenarios[scenario.ID].Schedules = nil
		runner.mu.Unlock()
	})
	sweep := func(id string) (*storage.ScenarioScheduleOccurrence, error) {
		t.Helper()
		now := time.Now()
		due := now.UTC().Truncate(time.Minute).Add(-time.Minute)
		created, err := runner.CreateScenarioSchedule(scenario.ID, CreateScenarioScheduleRequest{Name: id, CronExpr: "*/5 * * * *", Timezone: "UTC", RunNameTemplate: id})
		if err != nil || created.Governance == nil {
			t.Fatalf("schedule %s: %+v %v", id, created, err)
		}
		schedule := *created
		schedule.NextRunAt = &due
		runner.mu.Lock()
		err = runner.saveScenarioScheduleLocked(&schedule)
		runner.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		runner.mu.Lock()
		_, runErr := runner.runScenarioScheduleLocked(&schedule, now)
		runner.mu.Unlock()
		occurrences, err := store.ListScenarioScheduleOccurrences(schedule.ID)
		if err != nil || len(occurrences) != 1 {
			t.Fatalf("occurrences of %s: %+v %v", id, occurrences, err)
		}
		return occurrences[0], runErr
	}
	runner.mu.Lock()
	runner.testStore = unavailablePolicyStore{store}
	runner.mu.Unlock()
	materialized, _ := sweep("budget-outage-schedule")
	if materialized.ReasonCode != "start_failed" || materialized.TestID == "" {
		t.Fatalf("an outage: occurrence %+v", materialized)
	}
	runner.mu.RLock()
	scheduled := runner.tests[materialized.TestID]
	runner.mu.RUnlock()
	if scheduled == nil || scheduled.TelemetryBudgetPolicyID != "schedule-budgets" {
		t.Fatalf("the run materialized during an outage: %+v", scheduled)
	}
	direct := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/tests", map[string]any{"name": "Direct during outage", "scenario_id": scenario.ID}, "")
	var directRun ManagedTest
	if err := json.Unmarshal(direct.Body.Bytes(), &directRun); err != nil || direct.Code != http.StatusCreated || directRun.TelemetryBudgetPolicyID != "schedule-budgets" {
		t.Fatalf("a direct run during an outage: %d %s", direct.Code, direct.Body.String())
	}
	planRequest, err := buildPlanEntryCreateRequest("Plan during outage", storage.DefaultProjectID, nil, storage.ExecutionPlanEntry{ScenarioID: scenario.ID})
	if err != nil {
		t.Fatal(err)
	}
	planned, err := runner.CreateTest(planRequest)
	if err != nil || planned.TelemetryBudgetPolicyID != "schedule-budgets" {
		t.Fatalf("a plan entry during an outage: %+v %v", planned, err)
	}
	if named := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/tests", map[string]any{"name": "Named during outage", "scenario_id": scenario.ID, "telemetry_budget_policy_id": "schedule-budgets"}, ""); named.Code != http.StatusServiceUnavailable {
		t.Fatalf("a run naming a budget policy during an outage: %d %s", named.Code, named.Body.String())
	}
	runner.mu.Lock()
	runner.testStore = store
	runner.mu.Unlock()
	deleteSeededPolicy(t, store, storage.DefaultProjectID, "schedule-budgets")
	failed, err := sweep("budget-gone-schedule")
	if err == nil || failed.Status != "failed" || !strings.Contains(failed.ConflictSummary, "scenario's chosen telemetry budget policy") || !strings.Contains(failed.ConflictSummary, "schedule-budgets") {
		t.Fatalf("a deleted policy: occurrence %+v, error %v", failed, err)
	}
}

// A remote run of a scenario that chose a local telemetry budget policy must
// name the remote policy (or project_default): the pushed bundle does not
// carry the choice, so the remote would classify the run by its own copy's
// choice or its system threshold settings, never by the local choice.
func TestRemoteScenarioRunNeedsAnExplicitTelemetryBudgetPolicy(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()
	storeBudgetScenarioScript(t, server, "scenario-budget-remote-script")
	seedPolicy(t, store, storage.DefaultProjectID, "local-budgets", storage.AnalysisDefinitionKindTelemetryPolicy, tightBudgetPolicy)
	created := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scenarios", budgetScenarioBody("Remote budgets", "scenario-budget-remote-script", "local-budgets"), "")
	if created.Code != http.StatusCreated {
		t.Fatalf("scenario create: %d %s", created.Code, created.Body.String())
	}
	scenario := decodeScenarioResponse(t, created)
	remote := LibraryRemote{ID: "workspace", Name: "Workspace", BaseURL: "http://127.0.0.1:9", DefaultProjectID: "remote-project", ProjectMappings: map[string]string{storage.DefaultProjectID: "remote-project"}, AuthMethod: "none"}
	_, _, _, _, _, err := server.prepareRemoteRunCreateRequest(context.Background(), remote, CreateTestRequest{Name: "Remote", ScenarioID: scenario.ID, ProjectID: storage.DefaultProjectID}, "")
	if !errors.Is(err, errRemoteRunScenarioPolicy) || !strings.Contains(err.Error(), "local-budgets") || !strings.Contains(err.Error(), "telemetry_budget_policy_id") {
		t.Fatalf("a remote scenario run naming no budget policy: %v", err)
	}
	server.libraryMu.Lock()
	server.libraryRemotes[remote.ID] = &remote
	server.libraryMu.Unlock()
	if err := server.persistLibraryRemotes(); err != nil {
		t.Fatal(err)
	}
	body := marshalLibraryTestJSON(t, RemoteRunCreateRequest{CreateTestRequest: CreateTestRequest{Name: "Remote", ScenarioID: scenario.ID, ProjectID: storage.DefaultProjectID}})
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/remotes/workspace/runs", bytes.NewReader(body)))
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "local-budgets") {
		t.Fatalf("POST /api/v1/remotes/workspace/runs: %d %s", response.Code, response.Body.String())
	}
	for _, policy := range []string{"remote-budgets", runPolicyProjectDefault} {
		_, _, _, _, _, err := server.prepareRemoteRunCreateRequest(context.Background(), remote, CreateTestRequest{Name: "Remote", ScenarioID: scenario.ID, ProjectID: storage.DefaultProjectID, TelemetryBudgetPolicyID: policy}, "")
		if errors.Is(err, errRemoteRunScenarioPolicy) {
			t.Fatalf("a remote scenario run naming %q was refused for its policy: %v", policy, err)
		}
	}
}

// A run created during a policy-read outage keeps its scenario's budget
// choice; once policies can be read again, Start pins that choice
// (run_selection) and its version 2 receipt sets the run's budgets.
func TestScenarioTelemetryBudgetPolicyCreatedDuringOutageIsPinnedAtStart(t *testing.T) {
	t.Setenv(config.EnvArchiveDir, t.TempDir())
	runner, store, test := newPassiveTransitionTestRunner(t, "scenario-budget-outage")
	backend := newTelemetryPolicyBarrierBackend(t)
	runner.setExecutionBackend(backend)
	project := normalizeProjectID(test.ProjectID)
	seedPolicy(t, store, project, "default-budgets", storage.AnalysisDefinitionKindTelemetryPolicy, `{"version":1,"apply_by_default":true,"sample_age_ms":3000}`)
	seedPolicy(t, store, project, "scenario-budgets", storage.AnalysisDefinitionKindTelemetryPolicy, tightBudgetPolicy)
	scenario, err := runner.StoreScenario(CreateScenarioRequest{Name: "Budget outage scenario", Duration: "1m", TelemetryBudgetPolicyID: "scenario-budgets",
		Groups: []CreateGroupEntry{{Name: "Shop", ScriptID: "scenario-budget-outage-script", VUsers: 1}}})
	if err != nil || scenario.TelemetryBudgetPolicyID != "scenario-budgets" {
		t.Fatalf("scenario: %+v %v", scenario, err)
	}
	runner.mu.Lock()
	runner.testStore = unavailablePolicyStore{store}
	runner.mu.Unlock()
	created, err := runner.CreateTest(CreateTestRequest{Name: "Created during outage", ScenarioID: scenario.ID})
	if err != nil || created.TelemetryBudgetPolicyID != "scenario-budgets" {
		t.Fatalf("a run created during an outage: %+v %v", created, err)
	}
	runner.mu.Lock()
	runner.testStore = store
	runner.mu.Unlock()
	if err := runner.StartTest(created.ID); err != nil {
		t.Fatal(err)
	}
	backend.expected.Store(true)
	waitTelemetryBackend(t, backend)
	finishTelemetryBackend(t, backend)
	stored, err := store.GetTest(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	initial := decodeStoredInitialProvenance(stored.InitialProvenance)
	if initial == nil || initial.TelemetryPolicy == nil || initial.TelemetryPolicy.SchemaVersion != telemetrypolicy.VersionProjectPolicy || initial.TelemetryPolicy.PolicyID != "scenario-budgets" || initial.TelemetryPolicy.Budgets[0].LimitMs != 1500 {
		t.Fatalf("admission receipt %+v", initial)
	}
	var pinned bool
	for _, pin := range initial.PolicyPins {
		if pin.Kind == storage.AnalysisDefinitionKindTelemetryPolicy {
			pinned = pin.ID == "scenario-budgets" && pin.Source == runPolicySourceSelected
		}
	}
	if !pinned {
		t.Fatalf("budget pin %+v", initial.PolicyPins)
	}
}

// A capacity estimate or preview does not depend on the run's budget policy,
// so a lost or unreadable choice never fails one or hides a schedule
// preview's capacity findings; a launch preview still judges it.
func TestTelemetryBudgetPolicyIsNotJudgedByCapacityEstimates(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()
	storeBudgetScenarioScript(t, server, "scenario-budget-estimate-script")
	request := CreateTestRequest{Name: "Estimate", ScriptID: "scenario-budget-estimate-script", VUsers: 1, Duration: "1m", TelemetryBudgetPolicyID: "gone-budgets"}
	if _, err := server.runner.EstimateDraftCapacity(request); err != nil {
		t.Fatalf("an estimate naming a missing budget policy: %v", err)
	}
	if estimate := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/tests/estimate", request, ""); estimate.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/tests/estimate naming a missing budget policy: %d %s", estimate.Code, estimate.Body.String())
	}
	if _, _, _, err := server.runner.buildManagedTestFromSnapshot(request, "launch-preview", "DRAFT"); err == nil || !strings.Contains(err.Error(), "gone-budgets") {
		t.Fatalf("a launch preview naming a missing budget policy: %v", err)
	}
	seedPolicy(t, store, storage.DefaultProjectID, "preview-budgets", storage.AnalysisDefinitionKindTelemetryPolicy, tightBudgetPolicy)
	scenario, err := server.runner.StoreScenario(CreateScenarioRequest{Name: "Budget preview", Duration: "1m", TelemetryBudgetPolicyID: "preview-budgets",
		Groups: []CreateGroupEntry{{Name: "Shop", ScriptID: "scenario-budget-estimate-script", VUsers: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	deleteSeededPolicy(t, store, storage.DefaultProjectID, "preview-budgets")
	if evidence := server.runner.loadScenarioScheduleCapacityEvidence(scenario.ID, "Budget preview", projectQuotaEvidence{}); evidence.err != nil || evidence.preview == nil {
		t.Fatalf("a schedule capacity preview of a lost choice: %+v", evidence.err)
	}
	if _, _, _, err := server.runner.buildManagedTestFromSnapshot(CreateTestRequest{Name: "Launch", ScenarioID: scenario.ID}, "launch-preview", "DRAFT"); err == nil || !strings.Contains(err.Error(), "preview-budgets") {
		t.Fatalf("a launch preview of a lost choice: %v", err)
	}
	server.runner.mu.Lock()
	server.runner.testStore = unavailablePolicyStore{store}
	server.runner.mu.Unlock()
	if _, err := server.runner.EstimateDraftCapacity(request); err != nil {
		t.Fatalf("an estimate during a policy-read outage: %v", err)
	}
}

// A scenario's chosen budget policy names one of its own project's policies,
// which a library bundle does not carry: an export omits it (so it never
// changes the content hash), a new import uses the system threshold settings
// whatever the bundle names, and an import that updates a linked scenario
// keeps that scenario's own choice.
func TestScenarioTelemetryBudgetPolicyIsNotPortable(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()
	storeBudgetScenarioScript(t, server, "scenario-budget-library-script")
	seedPolicy(t, store, storage.DefaultProjectID, "library-budgets", storage.AnalysisDefinitionKindTelemetryPolicy, tightBudgetPolicy)
	created := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scenarios", budgetScenarioBody("Library budgets", "scenario-budget-library-script", "library-budgets"), "")
	if created.Code != http.StatusCreated {
		t.Fatalf("scenario create: %d %s", created.Code, created.Body.String())
	}
	scenario := decodeScenarioResponse(t, created)
	stored, _ := server.runner.GetScenario(scenario.ID)

	item, err := server.libraryScenarioBundleItem(scenario.ID)
	if err != nil {
		t.Fatal(err)
	}
	if item.Scenario.Request.TelemetryBudgetPolicyID != "" {
		t.Fatalf("an export carried the choice: %q", item.Scenario.Request.TelemetryBudgetPolicyID)
	}
	unchosen := *stored
	unchosen.TelemetryBudgetPolicyID = ""
	if item.ContentHash != hashLibraryScenario(&unchosen) || hashLibraryScenario(stored) != hashLibraryScenario(&unchosen) {
		t.Fatal("the choice changed the scenario's library content hash")
	}

	crafted := item.Scenario.Request
	crafted.TelemetryBudgetPolicyID = "library-budgets"
	craftedItem := LibraryBundleItem{Kind: "scenario", SourceInstanceID: "elsewhere", SourceItemID: "crafted", ProjectID: storage.DefaultProjectID,
		Scenario: &LibraryScenarioPayload{ID: "crafted", Request: crafted}, ContentHash: hashCanonical(crafted)}
	importedID, _, conflict := server.importLibraryScenario(craftedItem, storage.DefaultProjectID, "", nil)
	if conflict != nil {
		t.Fatalf("import: %+v", conflict)
	}
	if imported, _ := server.runner.GetScenario(importedID); imported == nil || imported.TelemetryBudgetPolicyID != "" {
		t.Fatalf("a new import kept the bundle's choice: %+v", imported)
	}

	if err := server.rememberLibraryLink("", storage.DefaultProjectID, "scenario", "elsewhere", "linked", scenario.ID, "linked", hashLibraryScenario(stored), "old-remote-hash"); err != nil {
		t.Fatal(err)
	}
	update := item.Scenario.Request
	update.Duration = "2m"
	updateItem := LibraryBundleItem{Kind: "scenario", SourceInstanceID: "elsewhere", SourceItemID: "linked", ProjectID: storage.DefaultProjectID,
		Scenario: &LibraryScenarioPayload{ID: "linked", Request: update}, ContentHash: hashCanonical(update)}
	updatedID, action, conflict := server.importLibraryScenario(updateItem, storage.DefaultProjectID, "", nil)
	if conflict != nil || updatedID != scenario.ID || action != "updated" {
		t.Fatalf("linked import: %q %q %+v", updatedID, action, conflict)
	}
	if updated, _ := server.runner.GetScenario(scenario.ID); updated == nil || updated.Duration != "2m" || updated.TelemetryBudgetPolicyID != "library-budgets" {
		t.Fatalf("a linked import changed the scenario's choice: %+v", updated)
	}
}
