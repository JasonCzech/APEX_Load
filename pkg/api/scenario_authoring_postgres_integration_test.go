//go:build postgres_integration

package api

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

// Two replicas share one PostgreSQL schema. PostgreSQL keeps microsecond
// clocks, so this also proves cached and durable updated_at stay comparable
// after a conditioned save.
func TestPostgresScenarioAuthoringConditionedSaveAcrossReplicas(t *testing.T) {
	dsn := newPostgresScriptAuthoringAPIFixture(t)
	owner, store := openPostgresScriptAuthoringAPI(t, dsn)
	peer, _ := openPostgresScriptAuthoringAPI(t, dsn)
	create := authoringAPIRequest(t, owner, http.MethodPost, "/api/v1/scripts", map[string]any{"script": authoringAPIScript("pg-scenario-script")}, "")
	if create.Code != http.StatusCreated {
		t.Fatalf("script create: %d %s", create.Code, create.Body.String())
	}
	var script StoredScript
	if err := json.Unmarshal(create.Body.Bytes(), &script); err != nil {
		t.Fatal(err)
	}
	request := func(name string, vusers int) CreateScenarioRequest {
		return CreateScenarioRequest{Name: name, Duration: "1m", Groups: []CreateGroupEntry{{ScriptID: script.ID, VUsers: vusers}}}
	}
	created := authoringAPIRequest(t, owner, http.MethodPost, "/api/v1/scenarios", request("Original", 1), "")
	if created.Code != http.StatusCreated {
		t.Fatalf("scenario create: %d %s", created.Code, created.Body.String())
	}
	var scenario StoredScenario
	if err := json.Unmarshal(created.Body.Bytes(), &scenario); err != nil {
		t.Fatal(err)
	}
	first, tag, _ := requireScenarioAuthoringView(t, owner, scenario.ID)
	other, otherTag, _ := requireScenarioAuthoringView(t, peer, scenario.ID)
	if tag != otherTag || !reflect.DeepEqual(first, other) {
		t.Fatal("replicas returned different authoring baselines")
	}
	saved := authoringAPIRequest(t, peer, http.MethodPut, "/api/v1/scenarios/"+scenario.ID, request("Saved on peer", 2), tag)
	if saved.Code != http.StatusOK || saved.Header().Get("X-APEX-Scenario-Save-Mode") != scenarioSaveModeConditioned {
		t.Fatalf("conditioned save on peer: %d %s", saved.Code, saved.Body.String())
	}
	newTag := saved.Header().Get("ETag")
	durable, _, err := store.GetScenario(scenario.ID)
	if err != nil || durable.Name != "Saved on peer" {
		t.Fatalf("peer save not durable: %#v %v", durable, err)
	}
	cached, visible := peer.runner.GetScenario(scenario.ID)
	if !visible || !cached.UpdatedAt.Equal(durable.UpdatedAt) {
		t.Fatalf("peer cache diverged from PostgreSQL clock: visible=%v cached=%v durable=%v", visible, cached, durable.UpdatedAt)
	}
	if stale := authoringAPIRequest(t, owner, http.MethodPut, "/api/v1/scenarios/"+scenario.ID, request("Stale owner overwrite", 9), tag); stale.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale validator on owner: %d %s", stale.Code, stale.Body.String())
	}
	if chained := authoringAPIRequest(t, owner, http.MethodPut, "/api/v1/scenarios/"+scenario.ID, request("Chained on owner", 3), newTag); chained.Code != http.StatusOK {
		t.Fatalf("owner save with the peer's returned ETag: %d %s", chained.Code, chained.Body.String())
	}
	project, err := store.GetProject(storage.DefaultProjectID)
	if err != nil {
		t.Fatal(err)
	}
	project.Settings.Authoring = &storage.ProjectAuthoringPolicy{RequireScenarioSaveIfMatch: true}
	if err := store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	if strict := authoringAPIRequest(t, peer, http.MethodPut, "/api/v1/scenarios/"+scenario.ID, request("Unconditioned under policy", 4), ""); strict.Code != http.StatusPreconditionRequired {
		t.Fatalf("strict policy on peer: %d %s", strict.Code, strict.Body.String())
	}
}

func newPostgresScenarioAuthoringReplicas(t *testing.T) (*Server, *Server, *storage.PostgresStore, *storage.PostgresStore, string, func(string, int) CreateScenarioRequest) {
	t.Helper()
	dsn := newPostgresScriptAuthoringAPIFixture(t)
	owner, ownerStore := openPostgresScriptAuthoringAPI(t, dsn)
	peer, peerStore := openPostgresScriptAuthoringAPI(t, dsn)
	create := authoringAPIRequest(t, owner, http.MethodPost, "/api/v1/scripts", map[string]any{"script": authoringAPIScript("pg-scenario-race-script")}, "")
	if create.Code != http.StatusCreated {
		t.Fatalf("script create: %d %s", create.Code, create.Body.String())
	}
	var script StoredScript
	if err := json.Unmarshal(create.Body.Bytes(), &script); err != nil {
		t.Fatal(err)
	}
	request := func(name string, vusers int) CreateScenarioRequest {
		return CreateScenarioRequest{Name: name, Duration: "1m", Groups: []CreateGroupEntry{{ScriptID: script.ID, VUsers: vusers}}}
	}
	created := authoringAPIRequest(t, owner, http.MethodPost, "/api/v1/scenarios", request("Original", 1), "")
	if created.Code != http.StatusCreated {
		t.Fatalf("scenario create: %d %s", created.Code, created.Body.String())
	}
	var scenario StoredScenario
	if err := json.Unmarshal(created.Body.Bytes(), &scenario); err != nil {
		t.Fatal(err)
	}
	return owner, peer, ownerStore, peerStore, scenario.ID, request
}

// Both replicas pass admission with the same validator and meet inside
// PostgreSQL; the row lock and in-transaction revision compare let exactly one
// commit.
func TestPostgresScenarioConditionedSaveRaceAcrossReplicas(t *testing.T) {
	owner, peer, ownerStore, peerStore, scenarioID, request := newPostgresScenarioAuthoringReplicas(t)
	exerciseScenarioReplicaRace(t, owner, peer, ownerStore.SQLiteStore, peerStore.SQLiteStore, scenarioID, request)
}

// The strict policy is enabled after the handler's early read; the PostgreSQL
// transaction re-reads it under the project row lock and refuses both the
// ordinary save and a guarded secondary writer.
func TestPostgresScenarioStrictPolicyFlipIsFencedInTransaction(t *testing.T) {
	owner, _, ownerStore, _, scenarioID, request := newPostgresScenarioAuthoringReplicas(t)
	project, err := ownerStore.GetProject(storage.DefaultProjectID)
	if err != nil {
		t.Fatal(err)
	}
	project.Settings.Authoring = &storage.ProjectAuthoringPolicy{RequireScenarioSaveIfMatch: true}
	if err := ownerStore.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	owner.store = sqliteOverlay(&strictPolicyAfterAdmissionStore{SQLiteStore: ownerStore.SQLiteStore})
	before, err := ownerStore.GetScenarioAuthoringSnapshot(scenarioID)
	if err != nil {
		t.Fatal(err)
	}
	if response := authoringAPIRequest(t, owner, http.MethodPut, "/api/v1/scenarios/"+scenarioID, request("Raced policy", 2), ""); response.Code != http.StatusPreconditionRequired {
		t.Fatalf("ordinary save after policy flip: %d %s", response.Code, response.Body.String())
	}
	if response := authoringAPIRequest(t, owner, http.MethodPut, "/api/v1/scenarios/"+scenarioID+"/data-pools", map[string]any{"data_pools": []any{}}, ""); response.Code != http.StatusPreconditionRequired {
		t.Fatalf("data-pool writer after policy flip: %d %s", response.Code, response.Body.String())
	}
	if after, err := ownerStore.GetScenarioAuthoringSnapshot(scenarioID); err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("fenced writes mutated the scenario: %v", err)
	}
}

// PostgreSQL returns a group SLA from its 4-byte REAL column; the commit
// fingerprint recovery recomputes from it still matches.
func TestPostgresScenarioRuntimeCommitFingerprintSurvivesStorage(t *testing.T) {
	owner, _, ownerStore, _, _, request := newPostgresScenarioAuthoringReplicas(t)
	exerciseScenarioRuntimeCommitFingerprint(t, owner, ownerStore.SQLiteStore, request("unused", 1).Groups[0].ScriptID)
}
