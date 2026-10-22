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
)

func decodeScenarioResponse(t *testing.T, response *httptest.ResponseRecorder) StoredScenario {
	t.Helper()
	var scenario StoredScenario
	if err := json.Unmarshal(response.Body.Bytes(), &scenario); err != nil {
		t.Fatalf("decode scenario: %v (%s)", err, response.Body.String())
	}
	return scenario
}

// TEL-B: a scenario's chosen telemetry acceptance policy is validated when
// the scenario is saved (one of its own project's live, valid policies; 409
// for an invalid one), stored with it and shown to its editors, and every
// run created from it inherits the choice: a direct run, an execution-plan
// entry and a schedule, unless the run names another or project_default.
func TestScenarioTelemetryAcceptancePolicyCarriesThroughEveryEntryPoint(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()
	runner := server.runner
	script := httpScript("scenario-policy-script", "Scenario policy")
	script.ProjectID = storage.DefaultProjectID
	runner.StoreScript(script)
	project := storage.DefaultProjectID
	seedPolicy(t, store, project, "scenario-gate", storage.AnalysisDefinitionKindTelemetryAcceptancePolicy, `{"version":1,"mode":"fail_ci","min_wall_time_coverage":0.9}`)
	seedPolicy(t, store, project, "run-gate", storage.AnalysisDefinitionKindTelemetryAcceptancePolicy, `{"version":1,"min_wall_time_coverage":0.5}`)
	seedPolicy(t, store, project, "broken-gate", storage.AnalysisDefinitionKindTelemetryAcceptancePolicy, `{"version":1}`)
	if err := store.SaveProject(&storage.Project{ID: "another-project", Name: "Another"}); err != nil {
		t.Fatal(err)
	}
	seedPolicy(t, store, "another-project", "foreign-gate", storage.AnalysisDefinitionKindTelemetryAcceptancePolicy, `{"version":1,"min_wall_time_coverage":0.1}`)
	scenarioBody := func(name, policy string) map[string]any {
		return map[string]any{
			"name": name, "duration": "1m", "telemetry_acceptance_policy_id": policy,
			"groups": []map[string]any{{"name": "Shop", "script_id": "scenario-policy-script", "vusers": 1}},
		}
	}

	// Refusals at save: missing and another project's policy alike (400),
	// an invalid one (409).
	missing := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scenarios", scenarioBody("Missing", "missing-gate"), "")
	foreign := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scenarios", scenarioBody("Foreign", "foreign-gate"), "")
	if missing.Code != http.StatusBadRequest || foreign.Code != http.StatusBadRequest || strings.ReplaceAll(foreign.Body.String(), "foreign-gate", "X") != strings.ReplaceAll(missing.Body.String(), "missing-gate", "X") {
		t.Fatalf("missing %d %s, foreign %d %s", missing.Code, missing.Body.String(), foreign.Code, foreign.Body.String())
	}
	if broken := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scenarios", scenarioBody("Broken", "broken-gate"), ""); broken.Code != http.StatusConflict || !strings.Contains(broken.Body.String(), "broken-gate") {
		t.Fatalf("an invalid policy: %d %s", broken.Code, broken.Body.String())
	}
	if explicit := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scenarios", scenarioBody("Explicit default", runPolicyProjectDefault), ""); explicit.Code != http.StatusCreated || decodeScenarioResponse(t, explicit).TelemetryAcceptancePolicyID != "" {
		t.Fatalf("project_default: %d %s", explicit.Code, explicit.Body.String())
	}

	created := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scenarios", scenarioBody("Chosen", " scenario-gate "), "")
	if created.Code != http.StatusCreated {
		t.Fatalf("scenario create: %d %s", created.Code, created.Body.String())
	}
	scenario := decodeScenarioResponse(t, created)
	if scenario.TelemetryAcceptancePolicyID != "scenario-gate" {
		t.Fatalf("scenario choice %q", scenario.TelemetryAcceptancePolicyID)
	}
	if durable, _, err := store.GetScenario(scenario.ID); err != nil || durable.TelemetryAcceptancePolicyID != "scenario-gate" {
		t.Fatalf("stored choice %+v: %v", durable, err)
	}
	authoring := authoringAPIRequest(t, server, http.MethodGet, "/api/v1/scenarios/"+scenario.ID+"?view=authoring", nil, "")
	var projection ScenarioAuthoringProjection
	if err := json.Unmarshal(authoring.Body.Bytes(), &projection); err != nil || authoring.Code != http.StatusOK || projection.Scenario.TelemetryAcceptancePolicyID != "scenario-gate" {
		t.Fatalf("authoring projection: %d %s", authoring.Code, authoring.Body.String())
	}
	if restarted := NewTestRunner(2, NewHub(), store); restarted.scenarios[scenario.ID] == nil || restarted.scenarios[scenario.ID].TelemetryAcceptancePolicyID != "scenario-gate" {
		t.Fatal("a reloaded runner lost the scenario's choice")
	}

	run := func(name, policy string) *ManagedTest {
		t.Helper()
		body := map[string]any{"name": name, "scenario_id": scenario.ID}
		if policy != "" {
			body["telemetry_acceptance_policy_id"] = policy
		}
		response := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/tests", body, "")
		var created ManagedTest
		if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil || response.Code != http.StatusCreated {
			t.Fatalf("%s: %d %s", name, response.Code, response.Body.String())
		}
		return runner.tests[created.ID]
	}
	for _, tc := range []struct{ name, policy, want string }{
		{"Direct", "", "scenario-gate"},
		{"Named", "run-gate", "run-gate"},
		{"Default", runPolicyProjectDefault, ""},
	} {
		if got := run(tc.name, tc.policy); got.TelemetryAcceptancePolicyID != tc.want {
			t.Fatalf("%s run: choice %q, want %q", tc.name, got.TelemetryAcceptancePolicyID, tc.want)
		}
	}

	planRequest, err := buildPlanEntryCreateRequest("Policy plan", project, nil, storage.ExecutionPlanEntry{ScenarioID: scenario.ID})
	if err != nil {
		t.Fatal(err)
	}
	planned, err := runner.CreateTest(planRequest)
	if err != nil || planned.TelemetryAcceptancePolicyID != "scenario-gate" {
		t.Fatalf("plan entry run: %+v %v", planned, err)
	}

	now := time.Now()
	due := now.UTC().Truncate(time.Minute).Add(-time.Minute)
	schedule := &storage.ScenarioSchedule{
		ID: "scenario-policy-schedule", ScenarioID: scenario.ID, ProjectID: scenario.ProjectID, Name: "Policy schedule",
		RunNameTemplate: "Policy scheduled run", CronExpr: "*/5 * * * *", Timezone: "UTC", Enabled: true, NextRunAt: &due,
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
		if candidate.ScenarioID == scenario.ID && candidate.Name == "Policy scheduled run" {
			scheduled = candidate
		}
	}
	runner.mu.RUnlock()
	if scheduled == nil || scheduled.TelemetryAcceptancePolicyID != "scenario-gate" {
		t.Fatalf("scheduled run: %+v", scheduled)
	}
}

// A scenario whose chosen policy was deleted after it was saved refuses runs
// created from it, naming where the choice came from, rather than silently
// judging them by the project's default; a run naming another policy or
// project_default is still accepted. A save that keeps the scenario's choice
// is not judged again (an unrelated edit, an import, a rollback); a save that
// changes it to a missing policy is refused, and clearing it is accepted.
func TestScenarioTelemetryAcceptancePolicyGoneRefusesInheritingRuns(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()
	script := httpScript("scenario-policy-gone-script", "Scenario policy gone")
	script.ProjectID = storage.DefaultProjectID
	server.runner.StoreScript(script)
	project := storage.DefaultProjectID
	seedPolicy(t, store, project, "doomed-gate", storage.AnalysisDefinitionKindTelemetryAcceptancePolicy, `{"version":1,"min_wall_time_coverage":0.9}`)
	body := func(policy string) map[string]any {
		return map[string]any{
			"name": "Doomed", "duration": "1m", "telemetry_acceptance_policy_id": policy,
			"groups": []map[string]any{{"name": "Shop", "script_id": "scenario-policy-gone-script", "vusers": 1}},
		}
	}
	created := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scenarios", body("doomed-gate"), "")
	if created.Code != http.StatusCreated {
		t.Fatalf("scenario create: %d %s", created.Code, created.Body.String())
	}
	scenario := decodeScenarioResponse(t, created)
	deleteSeededPolicy(t, store, project, "doomed-gate")

	refused := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/tests", map[string]any{"name": "Inherits", "scenario_id": scenario.ID}, "")
	if refused.Code != http.StatusBadRequest || !strings.Contains(refused.Body.String(), "scenario's chosen") || !strings.Contains(refused.Body.String(), "doomed-gate") {
		t.Fatalf("a run inheriting a deleted choice: %d %s", refused.Code, refused.Body.String())
	}
	if accepted := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/tests", map[string]any{"name": "Default", "scenario_id": scenario.ID, "telemetry_acceptance_policy_id": runPolicyProjectDefault}, ""); accepted.Code != http.StatusCreated {
		t.Fatalf("a run naming project_default: %d %s", accepted.Code, accepted.Body.String())
	}

	tag := created.Header().Get("ETag")
	edited := body("doomed-gate")
	edited["duration"] = "2m"
	kept := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scenarios/"+scenario.ID, edited, tag)
	if kept.Code != http.StatusOK || decodeScenarioResponse(t, kept).TelemetryAcceptancePolicyID != "doomed-gate" {
		t.Fatalf("an unrelated edit keeping the deleted choice: %d %s", kept.Code, kept.Body.String())
	}
	tag = kept.Header().Get("ETag")
	if changed := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scenarios/"+scenario.ID, body("missing-gate"), tag); changed.Code != http.StatusBadRequest || !strings.Contains(changed.Body.String(), "missing-gate") {
		t.Fatalf("a save choosing a missing policy: %d %s", changed.Code, changed.Body.String())
	}
	cleared := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scenarios/"+scenario.ID, body(""), tag)
	if cleared.Code != http.StatusOK || decodeScenarioResponse(t, cleared).TelemetryAcceptancePolicyID != "" {
		t.Fatalf("a save clearing the choice: %d %s", cleared.Code, cleared.Body.String())
	}
	if durable, _, err := store.GetScenario(scenario.ID); err != nil || durable.TelemetryAcceptancePolicyID != "" {
		t.Fatalf("cleared choice %+v: %v", durable, err)
	}
}

// A scenario's chosen telemetry acceptance policy names one of its own
// project's policies, which a library bundle does not carry: an export omits
// it (so it never changes the content hash), a new import uses its
// destination's default whatever the bundle names, and an import that updates
// a linked scenario keeps that scenario's own choice.
func TestScenarioTelemetryAcceptancePolicyIsNotPortable(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()
	script := httpScript("scenario-policy-library-script", "Scenario policy library")
	script.ProjectID = storage.DefaultProjectID
	server.runner.StoreScript(script)
	seedPolicy(t, store, storage.DefaultProjectID, "library-gate", storage.AnalysisDefinitionKindTelemetryAcceptancePolicy, `{"version":1,"min_wall_time_coverage":0.9}`)
	created := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scenarios", map[string]any{
		"name": "Library", "duration": "1m", "telemetry_acceptance_policy_id": "library-gate",
		"groups": []map[string]any{{"name": "Shop", "script_id": "scenario-policy-library-script", "vusers": 1}},
	}, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("scenario create: %d %s", created.Code, created.Body.String())
	}
	scenario := decodeScenarioResponse(t, created)
	stored, _ := server.runner.GetScenario(scenario.ID)

	item, err := server.libraryScenarioBundleItem(scenario.ID)
	if err != nil {
		t.Fatal(err)
	}
	if item.Scenario.Request.TelemetryAcceptancePolicyID != "" {
		t.Fatalf("an export carried the choice: %q", item.Scenario.Request.TelemetryAcceptancePolicyID)
	}
	unchosen := *stored
	unchosen.TelemetryAcceptancePolicyID = ""
	if item.ContentHash != hashLibraryScenario(&unchosen) || hashLibraryScenario(stored) != hashLibraryScenario(&unchosen) {
		t.Fatal("the choice changed the scenario's library content hash")
	}

	// A bundle that names a policy anyway (another tool, or a later
	// version) is imported with the destination's default.
	crafted := item.Scenario.Request
	crafted.TelemetryAcceptancePolicyID = "library-gate"
	craftedItem := LibraryBundleItem{Kind: "scenario", SourceInstanceID: "elsewhere", SourceItemID: "crafted", ProjectID: storage.DefaultProjectID,
		Scenario: &LibraryScenarioPayload{ID: "crafted", Request: crafted}, ContentHash: hashCanonical(crafted)}
	importedID, _, conflict := server.importLibraryScenario(craftedItem, storage.DefaultProjectID, "", nil)
	if conflict != nil {
		t.Fatalf("import: %+v", conflict)
	}
	if imported, _ := server.runner.GetScenario(importedID); imported == nil || imported.TelemetryAcceptancePolicyID != "" {
		t.Fatalf("a new import kept the bundle's choice: %+v", imported)
	}

	// An import that updates a linked scenario keeps that scenario's choice.
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
	if updated, _ := server.runner.GetScenario(scenario.ID); updated == nil || updated.Duration != "2m" || updated.TelemetryAcceptancePolicyID != "library-gate" {
		t.Fatalf("a linked import changed the scenario's choice: %+v", updated)
	}
}

// Policy failures at scenario save are answered like a run's: 409 for an
// invalid policy on create and update, and 503 (retryable) when the
// project's policies cannot be read.
func TestScenarioTelemetryAcceptancePolicySaveStatuses(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()
	script := httpScript("scenario-policy-status-script", "Scenario policy status")
	script.ProjectID = storage.DefaultProjectID
	server.runner.StoreScript(script)
	seedPolicy(t, store, storage.DefaultProjectID, "status-broken", storage.AnalysisDefinitionKindTelemetryAcceptancePolicy, `{"version":1}`)
	seedPolicy(t, store, storage.DefaultProjectID, "status-gate", storage.AnalysisDefinitionKindTelemetryAcceptancePolicy, `{"version":1,"min_wall_time_coverage":0.9}`)
	body := func(policy string) map[string]any {
		return map[string]any{
			"name": "Statuses", "duration": "1m", "telemetry_acceptance_policy_id": policy,
			"groups": []map[string]any{{"name": "Shop", "script_id": "scenario-policy-status-script", "vusers": 1}},
		}
	}
	created := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scenarios", body("status-gate"), "")
	if created.Code != http.StatusCreated {
		t.Fatalf("scenario create: %d %s", created.Code, created.Body.String())
	}
	scenario := decodeScenarioResponse(t, created)
	tag := created.Header().Get("ETag")
	if broken := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scenarios/"+scenario.ID, body("status-broken"), tag); broken.Code != http.StatusConflict || !strings.Contains(broken.Body.String(), "status-broken") {
		t.Fatalf("an invalid policy on update: %d %s", broken.Code, broken.Body.String())
	}
	server.runner.mu.Lock()
	server.runner.testStore = unavailablePolicyStore{store}
	server.runner.mu.Unlock()
	if outage := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scenarios", body("any-gate"), ""); outage.Code != http.StatusServiceUnavailable {
		t.Fatalf("create during an outage: %d %s", outage.Code, outage.Body.String())
	}
	if outage := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scenarios/"+scenario.ID, body("any-gate"), tag); outage.Code != http.StatusServiceUnavailable {
		t.Fatalf("update during an outage: %d %s", outage.Code, outage.Body.String())
	}
	// A save that keeps the scenario's choice needs no policy read: an edit,
	// and a restore or import update through UpdateScenarioIfUnchanged.
	edited := body("status-gate")
	edited["duration"] = "2m"
	kept := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scenarios/"+scenario.ID, edited, tag)
	if kept.Code != http.StatusOK || decodeScenarioResponse(t, kept).TelemetryAcceptancePolicyID != "status-gate" {
		t.Fatalf("an edit keeping the choice during an outage: %d %s", kept.Code, kept.Body.String())
	}
	current, _ := server.runner.GetScenario(scenario.ID)
	restore := createScenarioRequestFromStored(current)
	restore.Duration = "3m"
	if restored, err := server.runner.UpdateScenarioIfUnchanged(scenario.ID, restore, current.UpdatedAt); err != nil || restored.TelemetryAcceptancePolicyID != "status-gate" {
		t.Fatalf("a restore keeping the choice during an outage: %+v %v", restored, err)
	}
}

// A kept choice is judged again when the scenario's project, derived from
// its scripts, differs from the one the runner has for it (a cached project
// gone stale): the choice names the old project's policy.
func TestScenarioTelemetryAcceptancePolicyKeptChoiceIsJudgedAfterAProjectChange(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()
	script := httpScript("scenario-policy-project-script", "Scenario policy project")
	script.ProjectID = storage.DefaultProjectID
	server.runner.StoreScript(script)
	seedPolicy(t, store, storage.DefaultProjectID, "project-gate", storage.AnalysisDefinitionKindTelemetryAcceptancePolicy, `{"version":1,"min_wall_time_coverage":0.9}`)
	scenario, err := server.runner.StoreScenario(CreateScenarioRequest{Name: "Project change", Duration: "1m", TelemetryAcceptancePolicyID: "project-gate",
		Groups: []CreateGroupEntry{{Name: "Shop", ScriptID: "scenario-policy-project-script", VUsers: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	deleteSeededPolicy(t, store, storage.DefaultProjectID, "project-gate")
	keep := createScenarioRequestFromStored(scenario)
	keep.Duration = "2m"
	// Same project: the kept choice is not judged.
	if _, _, err := server.runner.UpdateScenario(scenario.ID, keep); err != nil {
		t.Fatalf("a kept choice in the same project: %v", err)
	}
	server.runner.mu.Lock()
	server.runner.scenarios[scenario.ID].ProjectID = "another-project"
	server.runner.mu.Unlock()
	keep.Duration = "3m"
	if _, _, err := server.runner.UpdateScenario(scenario.ID, keep); err == nil || !strings.Contains(err.Error(), "project-gate") {
		t.Fatalf("a kept choice after a project change: %v", err)
	}
}

// A schedule whose scenario's chosen policy was deleted records a failed
// occurrence naming it. A policy read that fails while the occurrence (or a
// direct run of the scenario) materializes keeps the scenario's choice, which
// was validated at save and which Start pins or refuses, rather than failing
// the run or delaying it to a later sweep.
func TestScenarioTelemetryAcceptancePolicyScheduleOutcomes(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()
	runner := server.runner
	script := httpScript("scenario-policy-schedule-script", "Scenario policy schedule")
	script.ProjectID = storage.DefaultProjectID
	runner.StoreScript(script)
	seedPolicy(t, store, storage.DefaultProjectID, "schedule-gate", storage.AnalysisDefinitionKindTelemetryAcceptancePolicy, `{"version":1,"min_wall_time_coverage":0.9}`)
	created := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scenarios", map[string]any{
		"name": "Scheduled policy", "duration": "1m", "telemetry_acceptance_policy_id": "schedule-gate",
		"groups": []map[string]any{{"name": "Shop", "script_id": "scenario-policy-schedule-script", "vusers": 1}},
	}, "")
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
		// Created as the API creates one: a governed schedule, whose sweep
		// also builds a conflict preview before the occurrence.
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
	// The run is created with the scenario's choice. Its immediate Start reads
	// the project's policies too (every kind, as for any run), so during the
	// outage that Start fails as it would for any run; this slice changes only
	// creation.
	materialized, _ := sweep("policy-outage-schedule")
	if materialized.ReasonCode != "start_failed" || materialized.TestID == "" {
		t.Fatalf("an outage: occurrence %+v", materialized)
	}
	runner.mu.RLock()
	scheduled := runner.tests[materialized.TestID]
	runner.mu.RUnlock()
	if scheduled == nil || scheduled.TelemetryAcceptancePolicyID != "schedule-gate" {
		t.Fatalf("the run materialized during an outage: %+v", scheduled)
	}
	direct := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/tests", map[string]any{"name": "Direct during outage", "scenario_id": scenario.ID}, "")
	var directRun ManagedTest
	if err := json.Unmarshal(direct.Body.Bytes(), &directRun); err != nil || direct.Code != http.StatusCreated || directRun.TelemetryAcceptancePolicyID != "schedule-gate" {
		t.Fatalf("a direct run during an outage: %d %s", direct.Code, direct.Body.String())
	}
	planRequest, err := buildPlanEntryCreateRequest("Plan during outage", storage.DefaultProjectID, nil, storage.ExecutionPlanEntry{ScenarioID: scenario.ID})
	if err != nil {
		t.Fatal(err)
	}
	planned, err := runner.CreateTest(planRequest)
	if err != nil || planned.TelemetryAcceptancePolicyID != "schedule-gate" {
		t.Fatalf("a plan entry during an outage: %+v %v", planned, err)
	}
	// A run naming its own policy is still judged at creation: 503.
	if named := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/tests", map[string]any{"name": "Named during outage", "scenario_id": scenario.ID, "telemetry_acceptance_policy_id": "schedule-gate"}, ""); named.Code != http.StatusServiceUnavailable {
		t.Fatalf("a run naming a policy during an outage: %d %s", named.Code, named.Body.String())
	}
	runner.mu.Lock()
	runner.testStore = store
	runner.mu.Unlock()
	deleteSeededPolicy(t, store, storage.DefaultProjectID, "schedule-gate")
	failed, err := sweep("policy-gone-schedule")
	if err == nil || failed.Status != "failed" || !strings.Contains(failed.ConflictSummary, "scenario's chosen") || !strings.Contains(failed.ConflictSummary, "schedule-gate") {
		t.Fatalf("a deleted policy: occurrence %+v, error %v", failed, err)
	}
}

// A remote run of a scenario that chose a local telemetry acceptance policy
// must name the remote policy (or project_default): the pushed bundle does
// not carry the choice, so the remote would judge the run by its own copy's
// choice or its project's default, never by the local choice.
func TestRemoteScenarioRunNeedsAnExplicitTelemetryPolicy(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()
	script := httpScript("scenario-policy-remote-script", "Scenario policy remote")
	script.ProjectID = storage.DefaultProjectID
	server.runner.StoreScript(script)
	seedPolicy(t, store, storage.DefaultProjectID, "local-gate", storage.AnalysisDefinitionKindTelemetryAcceptancePolicy, `{"version":1,"min_wall_time_coverage":0.9}`)
	created := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scenarios", map[string]any{
		"name": "Remote policy", "duration": "1m", "telemetry_acceptance_policy_id": "local-gate",
		"groups": []map[string]any{{"name": "Shop", "script_id": "scenario-policy-remote-script", "vusers": 1}},
	}, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("scenario create: %d %s", created.Code, created.Body.String())
	}
	scenario := decodeScenarioResponse(t, created)
	remote := LibraryRemote{ID: "workspace", Name: "Workspace", BaseURL: "http://127.0.0.1:9", DefaultProjectID: "remote-project", ProjectMappings: map[string]string{storage.DefaultProjectID: "remote-project"}, AuthMethod: "none"}
	_, _, _, _, _, err := server.prepareRemoteRunCreateRequest(context.Background(), remote, CreateTestRequest{Name: "Remote", ScenarioID: scenario.ID, ProjectID: storage.DefaultProjectID}, "")
	if !errors.Is(err, errRemoteRunScenarioPolicy) || !strings.Contains(err.Error(), "local-gate") || !strings.Contains(err.Error(), runPolicyProjectDefault) {
		t.Fatalf("a remote scenario run naming no policy: %v", err)
	}
	// Through the route, the refusal is answered 409 before anything is pushed.
	server.libraryMu.Lock()
	server.libraryRemotes[remote.ID] = &remote
	server.libraryMu.Unlock()
	if err := server.persistLibraryRemotes(); err != nil {
		t.Fatal(err)
	}
	body := marshalLibraryTestJSON(t, RemoteRunCreateRequest{CreateTestRequest: CreateTestRequest{Name: "Remote", ScenarioID: scenario.ID, ProjectID: storage.DefaultProjectID}})
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/remotes/workspace/runs", bytes.NewReader(body)))
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "local-gate") {
		t.Fatalf("POST /api/v1/remotes/workspace/runs: %d %s", response.Code, response.Body.String())
	}
	for _, policy := range []string{"remote-gate", runPolicyProjectDefault} {
		_, _, _, _, _, err := server.prepareRemoteRunCreateRequest(context.Background(), remote, CreateTestRequest{Name: "Remote", ScenarioID: scenario.ID, ProjectID: storage.DefaultProjectID, TelemetryAcceptancePolicyID: policy}, "")
		if errors.Is(err, errRemoteRunScenarioPolicy) {
			t.Fatalf("a remote scenario run naming %q was refused for its policy: %v", policy, err)
		}
	}
}

// A run created during a policy-read outage keeps its scenario's choice; once
// policies can be read again, Start pins that choice (run_selection), not the
// project's default.
func TestScenarioTelemetryAcceptancePolicyCreatedDuringOutageIsPinnedAtStart(t *testing.T) {
	t.Setenv(config.EnvArchiveDir, t.TempDir())
	runner, store, test := newPassiveTransitionTestRunner(t, "scenario-policy-outage")
	backend := newTelemetryPolicyBarrierBackend(t)
	runner.setExecutionBackend(backend)
	project := normalizeProjectID(test.ProjectID)
	seedPolicy(t, store, project, "default-gate", storage.AnalysisDefinitionKindTelemetryAcceptancePolicy, `{"version":1,"apply_by_default":true,"min_wall_time_coverage":0.5}`)
	seedPolicy(t, store, project, "scenario-gate", storage.AnalysisDefinitionKindTelemetryAcceptancePolicy, `{"version":1,"mode":"fail_ci","min_wall_time_coverage":0.9}`)
	scenario, err := runner.StoreScenario(CreateScenarioRequest{Name: "Outage scenario", Duration: "1m", TelemetryAcceptancePolicyID: "scenario-gate",
		Groups: []CreateGroupEntry{{Name: "Shop", ScriptID: "scenario-policy-outage-script", VUsers: 1}}})
	if err != nil || scenario.TelemetryAcceptancePolicyID != "scenario-gate" {
		t.Fatalf("scenario: %+v %v", scenario, err)
	}
	runner.mu.Lock()
	runner.testStore = unavailablePolicyStore{store}
	runner.mu.Unlock()
	created, err := runner.CreateTest(CreateTestRequest{Name: "Created during outage", ScenarioID: scenario.ID})
	if err != nil || created.TelemetryAcceptancePolicyID != "scenario-gate" {
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
	if initial == nil || initial.TelemetryAcceptancePolicy == nil || initial.TelemetryAcceptancePolicy.Pin.ID != "scenario-gate" || initial.TelemetryAcceptancePolicy.Pin.Source != runPolicySourceSelected {
		t.Fatalf("admission pinned %+v", initial)
	}
}

// A capacity estimate or preview does not depend on the run's telemetry
// policy, so a lost or unreadable choice never fails one or hides a schedule
// preview's capacity findings; a build that predicts a launch (as launch
// preflight builds) still judges it.
func TestTelemetryAcceptancePolicyIsNotJudgedByCapacityEstimates(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()
	script := httpScript("scenario-policy-estimate-script", "Scenario policy estimate")
	script.ProjectID = storage.DefaultProjectID
	server.runner.StoreScript(script)
	request := CreateTestRequest{Name: "Estimate", ScriptID: "scenario-policy-estimate-script", VUsers: 1, Duration: "1m", TelemetryAcceptancePolicyID: "gone-gate"}
	if _, err := server.runner.EstimateDraftCapacity(request); err != nil {
		t.Fatalf("an estimate naming a missing policy: %v", err)
	}
	if estimate := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/tests/estimate", request, ""); estimate.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/tests/estimate naming a missing policy: %d %s", estimate.Code, estimate.Body.String())
	}
	if _, _, _, err := server.runner.buildManagedTestFromSnapshot(request, "launch-preview", "DRAFT"); err == nil || !strings.Contains(err.Error(), "gone-gate") {
		t.Fatalf("a launch preview naming a missing policy: %v", err)
	}
	// A schedule's capacity preview of a scenario whose choice was lost
	// still builds, so its capacity findings are not hidden.
	seedPolicy(t, store, storage.DefaultProjectID, "preview-gate", storage.AnalysisDefinitionKindTelemetryAcceptancePolicy, `{"version":1,"min_wall_time_coverage":0.9}`)
	scenario, err := server.runner.StoreScenario(CreateScenarioRequest{Name: "Preview", Duration: "1m", TelemetryAcceptancePolicyID: "preview-gate",
		Groups: []CreateGroupEntry{{Name: "Shop", ScriptID: "scenario-policy-estimate-script", VUsers: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	deleteSeededPolicy(t, store, storage.DefaultProjectID, "preview-gate")
	if evidence := server.runner.loadScenarioScheduleCapacityEvidence(scenario.ID, "Preview", projectQuotaEvidence{}); evidence.err != nil || evidence.preview == nil {
		t.Fatalf("a schedule capacity preview of a lost choice: %+v", evidence.err)
	}
	if _, _, _, err := server.runner.buildManagedTestFromSnapshot(CreateTestRequest{Name: "Launch", ScenarioID: scenario.ID}, "launch-preview", "DRAFT"); err == nil || !strings.Contains(err.Error(), "preview-gate") {
		t.Fatalf("a launch preview of a lost choice: %v", err)
	}
	server.runner.mu.Lock()
	server.runner.testStore = unavailablePolicyStore{store}
	server.runner.mu.Unlock()
	if _, err := server.runner.EstimateDraftCapacity(request); err != nil {
		t.Fatalf("an estimate during a policy-read outage: %v", err)
	}
}
