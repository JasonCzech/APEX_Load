package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func newExecutionPlanSecretAuthorityFixture(t *testing.T, suffix string, withRuntimeSecret bool) (*Server, *storage.SQLiteStore, *runtimeProjectionSecretStore, *storage.ExecutionPlan) {
	t.Helper()
	records, err := storage.NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = records.Close() })
	secrets := &runtimeProjectionSecretStore{values: make(map[string]string), attemptStore: records}
	server := NewServer(ServerConfig{Store: records, MaxConcurrent: 2, ProjectSecretStore: secrets})
	t.Cleanup(server.runner.beginShutdown)
	// These tests exercise materialization and secret authority, not the load
	// backend. Close execution admission so a successfully staged test remains
	// deterministic and cannot outlive its temporary database.
	server.runner.mu.Lock()
	server.runner.shuttingDown = true
	server.runner.mu.Unlock()

	scriptID := "execution-plan-secret-script-" + suffix
	script := httpScript(scriptID, "Execution plan secret")
	script.Actions[0].Target = "/resource?token={{env.API_TOKEN}}"
	server.runner.StoreScript(script)
	scenarioRequest := CreateScenarioRequest{
		Name:     "Execution plan secret scenario " + suffix,
		Duration: "1m",
		Groups:   []CreateGroupEntry{{ScriptID: scriptID, VUsers: 1}},
	}
	scenario, err := server.runner.StoreScenario(scenarioRequest)
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}
	if withRuntimeSecret {
		// Seed a pre-governed legacy snapshot so these tests continue exercising
		// execution-plan actor authority. Normal scenario saves now provision
		// values under the scenario editor's actor and persist references only.
		legacyRuntime := &RuntimeSettingsOverride{
			Env: map[string]string{"API_TOKEN": "execution-plan-secret-" + suffix},
		}
		server.runner.mu.Lock()
		server.runner.scenarios[scenario.ID].RuntimeSettings = cloneRuntimeSettings(legacyRuntime)
		legacyStored := cloneStoredScenarioForReadLocked(server.runner.scenarios[scenario.ID])
		server.runner.mu.Unlock()
		if err := records.SaveScenario(storedScenarioToStorage(legacyStored), storedScenarioRefsToStorage(legacyStored)); err != nil {
			t.Fatalf("SaveScenario(legacy runtime): %v", err)
		}
	}
	now := time.Now().UTC()
	plan := &storage.ExecutionPlan{
		ID:        "execution-plan-secret-" + suffix,
		ProjectID: storage.DefaultProjectID,
		Name:      "Execution plan secret " + suffix,
		RunMode:   "parallel",
		Entries:   []storage.ExecutionPlanEntry{{Order: 0, ScenarioID: scenario.ID}},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := records.SaveExecutionPlan(plan); err != nil {
		t.Fatalf("SaveExecutionPlan: %v", err)
	}
	return server, records, secrets, plan
}

func runExecutionPlanAs(t *testing.T, server *Server, planID string, resolution authResolution) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/execution-plans/"+planID+"/run", nil)
	request.SetPathValue("id", planID)
	request = withAuthenticatedContext(request, resolution)
	response := httptest.NewRecorder()
	server.handleMaterializeAndRunExecutionPlan(response, request)
	return response
}

func createExecutionPlanScheduleAs(t *testing.T, server *Server, planID, name string, resolution authResolution) *storage.ExecutionPlanSchedule {
	t.Helper()
	body, err := json.Marshal(CreateExecutionPlanScheduleRequest{Name: name, CronExpr: "* * * * *", Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/execution-plans/"+planID+"/schedules", bytes.NewReader(body))
	request.SetPathValue("id", planID)
	request = withAuthenticatedContext(request, resolution)
	response := httptest.NewRecorder()
	server.handleCreateExecutionPlanSchedule(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("create execution-plan schedule = %d %s", response.Code, response.Body.String())
	}
	var schedule storage.ExecutionPlanSchedule
	if err := json.Unmarshal(response.Body.Bytes(), &schedule); err != nil {
		t.Fatalf("decode execution-plan schedule: %v", err)
	}
	return &schedule
}

func TestServiceAccountExecutionPlanRunRejectsRuntimeSecretBeforeSideEffects(t *testing.T) {
	server, records, secrets, plan := newExecutionPlanSecretAuthorityFixture(t, "service-direct", true)
	response := runExecutionPlanAs(t, server, plan.ID, authResolution{ServiceAccount: &AuthenticatedServiceAccount{
		ID: "plan-runner", Name: "Plan runner", Scopes: []string{"tests:run"},
	}})
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "service accounts cannot create project secrets") {
		t.Fatalf("service-account execution-plan run = %d %s, want explicit forbidden", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "execution-plan-secret-service-direct") {
		t.Fatalf("service-account denial disclosed the runtime secret: %s", response.Body.String())
	}
	secrets.mu.Lock()
	createCalls := secrets.createCalls
	secrets.mu.Unlock()
	if createCalls != 0 {
		t.Fatalf("service-account execution-plan denial occurred after %d provider Create calls", createCalls)
	}
	runs, err := records.ListExecutionPlanRuns(plan.ID, 20, 0)
	if err != nil || len(runs) != 0 {
		t.Fatalf("service-account denial persisted run anchors = %#v, %v", runs, err)
	}
	attempts, err := records.ListRuntimeSecretProvisioningAttempts()
	if err != nil || len(attempts) != 0 {
		t.Fatalf("service-account denial provisioning journals = %#v, %v", attempts, err)
	}
}

func TestUnattributedExecutionPlanRunCannotInferHumanSecretAuthority(t *testing.T) {
	server, records, secrets, plan := newExecutionPlanSecretAuthorityFixture(t, "unattributed-direct", true)
	result, err := server.runner.MaterializeAndRunExecutionPlan(plan, "claimed-human-subject", "Claimed Human")
	if result != nil || !errors.Is(err, errUnattributedRuntimeSecretWrite) {
		t.Fatalf("unattributed execution-plan run = %#v, %v, want fail-closed actor error", result, err)
	}
	secrets.mu.Lock()
	createCalls := secrets.createCalls
	secrets.mu.Unlock()
	if createCalls != 0 {
		t.Fatalf("unattributed execution-plan denial occurred after %d provider Create calls", createCalls)
	}
	runs, listErr := records.ListExecutionPlanRuns(plan.ID, 20, 0)
	if listErr != nil || len(runs) != 0 {
		t.Fatalf("unattributed execution-plan denial persisted run anchors = %#v, %v", runs, listErr)
	}
}

func TestServiceAccountExecutionPlanRunWithoutPlaintextSecretsStillMaterializes(t *testing.T) {
	server, _, secrets, plan := newExecutionPlanSecretAuthorityFixture(t, "service-reference", false)
	result, err := server.runner.MaterializeAndRunExecutionPlan(plan, "plan-runner", "Plan runner", testCreationActor{
		Type: "service_account", Subject: "plan-runner", DisplayName: "Plan runner",
	})
	if err != nil || result == nil || result.RunID == "" {
		t.Fatalf("non-secret service-account execution-plan run = %#v, %v", result, err)
	}
	secrets.mu.Lock()
	createCalls := secrets.createCalls
	secrets.mu.Unlock()
	if createCalls != 0 {
		t.Fatalf("non-secret service-account execution-plan run made %d provider Create calls", createCalls)
	}
}

func TestHumanExecutionPlanRunAttributesRuntimeSecretAuditToCaller(t *testing.T) {
	server, records, _, plan := newExecutionPlanSecretAuthorityFixture(t, "human-direct", true)
	response := runExecutionPlanAs(t, server, plan.ID, authResolution{User: &AuthenticatedUser{
		Subject: "plan-human", DisplayName: "Plan Human",
	}})
	if response.Code != http.StatusOK {
		t.Fatalf("human execution-plan run = %d %s", response.Code, response.Body.String())
	}
	events, err := records.ListAuditEvents(storage.AuditEventFilter{Action: "project_secret.runtime_override.create", Limit: 20})
	if err != nil || len(events) < 2 {
		t.Fatalf("human execution-plan secret audit events = %#v, %v", events, err)
	}
	for _, event := range events {
		if event.ActorType != "user" || event.ActorSubject != "plan-human" || event.ActorDisplayName != "Plan Human" {
			t.Fatalf("human execution-plan audit actor = %s/%s/%s", event.ActorType, event.ActorSubject, event.ActorDisplayName)
		}
	}
}

func TestScheduledExecutionPlanUsesDurableActorForRuntimeSecretAuthority(t *testing.T) {
	t.Run("service account is denied", func(t *testing.T) {
		server, records, secrets, plan := newExecutionPlanSecretAuthorityFixture(t, "service-schedule", true)
		schedule := createExecutionPlanScheduleAs(t, server, plan.ID, "Service schedule", authResolution{ServiceAccount: &AuthenticatedServiceAccount{
			ID: "schedule-runner", Name: "Schedule Runner", Scopes: []string{"tests:run"},
		}})
		persisted, err := records.GetExecutionPlanSchedule(schedule.ID)
		if err != nil || persisted.ActorType != "service_account" || persisted.ActorSubject != "service-account:schedule-runner" {
			t.Fatalf("persisted schedule actor = %#v, %v", persisted, err)
		}
		now := time.Now().UTC()
		due := now.Add(-time.Minute)
		persisted.NextRunAt = &due
		if err := records.SaveExecutionPlanSchedule(persisted); err != nil {
			t.Fatalf("SaveExecutionPlanSchedule: %v", err)
		}
		server.runner.sweepDueExecutionPlanSchedules(now)

		occurrences, err := records.ListExecutionPlanScheduleOccurrences(schedule.ID)
		if err != nil || len(occurrences) != 1 {
			t.Fatalf("service schedule occurrences = %#v, %v", occurrences, err)
		}
		if occurrences[0].Status != "failed" || occurrences[0].ReasonCode != "materialize_failed" {
			t.Fatalf("service schedule occurrence = %#v, want secret-write denial", occurrences[0])
		}
		updatedSchedule, err := records.GetExecutionPlanSchedule(schedule.ID)
		if err != nil || !strings.Contains(updatedSchedule.LastError, "service accounts cannot create project secrets") {
			t.Fatalf("service schedule outcome = %#v, %v, want explicit secret-write denial", updatedSchedule, err)
		}
		if strings.Contains(updatedSchedule.LastError, "execution-plan-secret-service-schedule") {
			t.Fatalf("scheduled denial disclosed the runtime secret: %s", updatedSchedule.LastError)
		}
		secrets.mu.Lock()
		createCalls := secrets.createCalls
		secrets.mu.Unlock()
		if createCalls != 0 {
			t.Fatalf("scheduled service-account denial occurred after %d provider Create calls", createCalls)
		}
	})

	t.Run("human actor survives scheduling", func(t *testing.T) {
		server, records, _, plan := newExecutionPlanSecretAuthorityFixture(t, "human-schedule", true)
		schedule := createExecutionPlanScheduleAs(t, server, plan.ID, "Human schedule", authResolution{User: &AuthenticatedUser{
			Subject: "schedule-human", DisplayName: "Schedule Human",
		}})
		now := time.Now().UTC()
		due := now.Add(-time.Minute)
		schedule.NextRunAt = &due
		if err := records.SaveExecutionPlanSchedule(schedule); err != nil {
			t.Fatalf("SaveExecutionPlanSchedule: %v", err)
		}
		server.runner.sweepDueExecutionPlanSchedules(now)

		events, err := records.ListAuditEvents(storage.AuditEventFilter{Action: "project_secret.runtime_override.create", Limit: 20})
		if err != nil || len(events) < 2 {
			t.Fatalf("scheduled human secret audit events = %#v, %v", events, err)
		}
		for _, event := range events {
			if event.ActorType != "user" || event.ActorSubject != "schedule-human" || event.ActorDisplayName != "Schedule Human" {
				t.Fatalf("scheduled human audit actor = %s/%s/%s", event.ActorType, event.ActorSubject, event.ActorDisplayName)
			}
		}
		persisted, err := records.GetExecutionPlanSchedule(schedule.ID)
		if err != nil || persisted.ActorType != "user" || persisted.ActorSubject != "schedule-human" {
			t.Fatalf("schedule actor after sweep = %#v, %v", persisted, err)
		}
	})
}

func TestClaimedExecutionPlanOccurrenceKeepsActorAcrossScheduleUpdateAndRecovery(t *testing.T) {
	server, records, secrets, plan := newExecutionPlanSecretAuthorityFixture(t, "claimed-actor", true)
	schedule := createExecutionPlanScheduleAs(t, server, plan.ID, "Claimed actor schedule", authResolution{ServiceAccount: &AuthenticatedServiceAccount{
		ID: "original-schedule-runner", Name: "Original Schedule Runner", Scopes: []string{"tests:run"},
	}})
	now := time.Now().UTC()
	plannedAt := now.Add(-time.Minute)
	schedule.NextRunAt = &plannedAt
	if err := records.SaveExecutionPlanSchedule(schedule); err != nil {
		t.Fatalf("SaveExecutionPlanSchedule: %v", err)
	}
	schedule, err := records.GetExecutionPlanSchedule(schedule.ID)
	if err != nil {
		t.Fatalf("GetExecutionPlanSchedule: %v", err)
	}
	occurrence := &storage.ExecutionPlanScheduleOccurrence{
		ID: "claimed-actor-occurrence", ScheduleID: schedule.ID, PlanID: plan.ID, ProjectID: plan.ProjectID,
		ScheduleRevision: schedule.Revision, PlannedAt: plannedAt, Status: "materializing", ReasonCode: "due",
		RunID: "exec-plan-run-claimed-actor", CreatedAt: now, UpdatedAt: now,
	}
	stampExecutionPlanScheduleOccurrenceActor(occurrence, schedule)
	claimed, err := records.ClaimExecutionPlanScheduleOccurrenceIfCurrent(occurrence)
	if err != nil || !claimed {
		t.Fatalf("ClaimExecutionPlanScheduleOccurrenceIfCurrent = %v, %v", claimed, err)
	}

	// An active claim remains a deletion fence, but schedule configuration can be
	// edited. The edit must affect only future occurrences, never this claim.
	if found, deleteErr := server.runner.DeleteExecutionPlanScheduleForPlan(plan.ID, schedule.ID); !found || !errors.Is(deleteErr, errScheduleHasActiveOccurrence) {
		t.Fatalf("delete claimed schedule = found %v error %v, want active-occurrence conflict", found, deleteErr)
	}
	enabled := true
	updated, found, err := server.runner.UpdateExecutionPlanSchedule(plan.ID, schedule.ID, CreateExecutionPlanScheduleRequest{
		Name: "Claimed actor schedule updated", CronExpr: schedule.CronExpr, Timezone: schedule.Timezone,
		RunNameTemplate: schedule.RunNameTemplate, Enabled: &enabled, MissedRunPolicy: schedule.MissedRunPolicy,
	}, testCreationActor{Type: "user", Subject: "later-human", DisplayName: "Later Human"})
	if err != nil || !found {
		t.Fatalf("UpdateExecutionPlanSchedule = %#v, %v, %v", updated, found, err)
	}
	if updated.ActorType != "user" || updated.ActorSubject != "later-human" {
		t.Fatalf("updated schedule actor = %#v", updated)
	}
	persistedOccurrence, err := records.GetExecutionPlanScheduleOccurrenceByScheduleAndPlannedAt(schedule.ID, plannedAt)
	if err != nil || persistedOccurrence.ActorType != "service_account" || persistedOccurrence.ActorSubject != "service-account:original-schedule-runner" {
		t.Fatalf("claimed occurrence actor after schedule update = %#v, %v", persistedOccurrence, err)
	}

	// Model controller restart after the claim and schedule PUT. Recovery must use
	// the occurrence's original service-account authority and deny secret writes.
	recovered := NewTestRunner(2, NewHub(), records, WithProjectSecretStore(secrets))
	t.Cleanup(recovered.beginShutdown)
	recovered.mu.Lock()
	recovered.shuttingDown = true
	recovered.mu.Unlock()
	if err := recovered.runDueExecutionPlanScheduleOnce(updated, now); err != nil {
		t.Fatalf("recover claimed execution-plan occurrence: %v", err)
	}
	persistedOccurrence, err = records.GetExecutionPlanScheduleOccurrenceByScheduleAndPlannedAt(schedule.ID, plannedAt)
	if err != nil || persistedOccurrence.Status != "failed" || persistedOccurrence.ReasonCode != "materialize_failed" {
		t.Fatalf("recovered occurrence = %#v, %v", persistedOccurrence, err)
	}
	if persistedOccurrence.ActorType != "service_account" || persistedOccurrence.ActorSubject != "service-account:original-schedule-runner" {
		t.Fatalf("recovery changed claimed occurrence actor: %#v", persistedOccurrence)
	}
	secrets.mu.Lock()
	createCalls := secrets.createCalls
	secrets.mu.Unlock()
	if createCalls != 0 {
		t.Fatalf("recovered service-account occurrence made %d provider Create calls", createCalls)
	}
	updated, err = records.GetExecutionPlanSchedule(schedule.ID)
	if err != nil || !strings.Contains(updated.LastError, "service accounts cannot create project secrets") {
		t.Fatalf("recovered schedule outcome = %#v, %v", updated, err)
	}
}

func TestExecutionPlanScheduleActorFieldsCannotBeSuppliedByRequestJSON(t *testing.T) {
	var request CreateExecutionPlanScheduleRequest
	if err := json.NewDecoder(bytes.NewBufferString(`{"name":"N","cron_expr":"* * * * *","actor_type":"system","actor_subject":"forged"}`)).Decode(&request); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "actor_type") || strings.Contains(string(encoded), "actor_subject") {
		t.Fatalf("request accepted server-owned actor fields: %s", encoded)
	}
}
