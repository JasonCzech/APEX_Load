package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// scenarioCreateThenEditStore commits another writer's change right after the
// create commits, before the create handler answers.
type scenarioCreateThenEditStore struct {
	SQLiteStore *storage.SQLiteStore
	t           *testing.T
	created     *storage.ScenarioAuthoringSnapshot
}

func (s *scenarioCreateThenEditStore) CreateScenarioAuthoringIfAbsent(scenario *storage.Scenario, groups []storage.ScenarioScript, pools []storage.ScenarioDataPoolBinding) (*storage.ScenarioAuthoringMutationResult, error) {
	result, err := s.SQLiteStore.CreateScenarioAuthoringIfAbsent(scenario, groups, pools)
	if err != nil || result == nil || !result.Applied {
		return result, err
	}
	s.created = result.Snapshot
	edited := *result.Snapshot.Scenario
	edited.Name = "Edited by another writer"
	if _, editErr := s.SQLiteStore.UpdateScenarioAuthoringIfCurrent(&edited, result.Snapshot.Scripts, result.Snapshot.DataPools, storage.ScenarioAuthoringPrecondition{
		ProjectID:       result.Snapshot.ProjectID,
		ContentRevision: result.Snapshot.ContentRevision,
		CreatedAt:       result.Snapshot.Scenario.CreatedAt,
	}); editErr != nil {
		s.t.Errorf("concurrent edit: %v", editErr)
	}
	return result, nil
}

func decodeStoredScenario(t *testing.T, body []byte) StoredScenario {
	t.Helper()
	var scenario StoredScenario
	if err := json.Unmarshal(body, &scenario); err != nil {
		t.Fatal(err)
	}
	return scenario
}

// The create response's tag is the revision the create committed, even when
// another writer commits before the response is written. Saving from the
// create body with that tag must fail as stale instead of silently reverting
// the other writer's change.
func TestScenarioCreateTagIsTheCommittedRevisionWhenAnotherWriterFollows(t *testing.T) {
	server, store, _, _ := newScenarioPolicyAvailabilityFixture(t)
	wrapper := &scenarioCreateThenEditStore{SQLiteStore: store, t: t}
	server.runner.scenarioStore = sqliteOverlay(wrapper)
	created := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scenarios", scenarioSaveRequest("Created", 1), "")
	server.runner.scenarioStore = store
	if created.Code != http.StatusCreated || wrapper.created == nil {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	scenario := decodeStoredScenario(t, created.Body.Bytes())
	wantTag, err := scenarioAuthoringETag(wrapper.created.ContentRevision)
	if err != nil {
		t.Fatal(err)
	}
	if created.Header().Get("ETag") != wantTag || scenario.ContentRevision != wrapper.created.ContentRevision || scenario.Name != "Created" {
		t.Fatalf("create answered tag %q revision %q name %q, want the committed create %q", created.Header().Get("ETag"), scenario.ContentRevision, scenario.Name, wantTag)
	}
	_, currentTag, _ := requireScenarioAuthoringView(t, server, scenario.ID)
	if currentTag == wantTag {
		t.Fatal("fixture: the concurrent edit did not rotate the revision")
	}
	stale := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scenarios/"+scenario.ID, scenarioSaveRequest("Created", 2), created.Header().Get("ETag"))
	if stale.Code != http.StatusPreconditionFailed {
		t.Fatalf("save with the create tag after another writer: %d %s", stale.Code, stale.Body.String())
	}
}

// The created baseline is immediately usable: the cache took the committed
// clocks, so a conditioned save with the create tag is admitted.
func TestScenarioCreateTagAdmitsAnImmediateConditionedSave(t *testing.T) {
	server, _, _, _ := newScenarioPolicyAvailabilityFixture(t)
	created := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scenarios", scenarioSaveRequest("Baseline", 1), "")
	if created.Code != http.StatusCreated || created.Header().Get("ETag") == "" {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	scenario := decodeStoredScenario(t, created.Body.Bytes())
	saved := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scenarios/"+scenario.ID, scenarioSaveRequest("Baseline", 3), created.Header().Get("ETag"))
	if saved.Code != http.StatusOK || saved.Header().Get("X-APEX-Scenario-Save-Mode") != scenarioSaveModeConditioned {
		t.Fatalf("conditioned save with the create tag: %d mode=%q %s", saved.Code, saved.Header().Get("X-APEX-Scenario-Save-Mode"), saved.Body.String())
	}
}

// An idempotent replay carries the current validator while the replayed body
// is the durable state, and no validator once this replica's copy is stale.
func TestIdempotentScenarioReplayCarriesTheValidatorOnlyForTheDurableState(t *testing.T) {
	server, store, _, _ := newScenarioPolicyAvailabilityFixture(t)
	body := scenarioSaveRequest("Replayed", 1)
	first := idempotentCreateRequest(t, server, "/api/v1/scenarios", body, scenarioCreateKey)
	if first.Code != http.StatusCreated || first.Header().Get("ETag") == "" {
		t.Fatalf("create: %d %s", first.Code, first.Body.String())
	}
	replay := idempotentCreateRequest(t, server, "/api/v1/scenarios", body, scenarioCreateKey)
	replayed := decodeStoredScenario(t, replay.Body.Bytes())
	if replay.Code != http.StatusCreated || replay.Header().Get("Idempotent-Replayed") != "true" || replay.Header().Get("ETag") != first.Header().Get("ETag") || replayed.AuthoringProjection == nil {
		t.Fatalf("replay: %d etag=%q want %q %s", replay.Code, replay.Header().Get("ETag"), first.Header().Get("ETag"), replay.Body.String())
	}
	// Another replica edits the scenario. Whatever this replica answers, a
	// validator it returns must describe the body it returns.
	snapshot, err := store.GetScenarioAuthoringSnapshot(replayed.ID)
	if err != nil {
		t.Fatal(err)
	}
	edited := *snapshot.Scenario
	edited.Name = "Edited elsewhere"
	if _, err := store.UpdateScenarioAuthoringIfCurrent(&edited, snapshot.Scripts, snapshot.DataPools, storage.ScenarioAuthoringPrecondition{ProjectID: snapshot.ProjectID, ContentRevision: snapshot.ContentRevision, CreatedAt: snapshot.Scenario.CreatedAt}); err != nil {
		t.Fatal(err)
	}
	later := idempotentCreateRequest(t, server, "/api/v1/scenarios", body, scenarioCreateKey)
	laterBody := decodeStoredScenario(t, later.Body.Bytes())
	if later.Code != http.StatusCreated || later.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("later replay: %d %s", later.Code, later.Body.String())
	}
	// The replay reloaded this replica, so its copy is durable again and the
	// validator it carries describes the edited body.
	tag := later.Header().Get("ETag")
	_, currentTag, _ := requireScenarioAuthoringView(t, server, replayed.ID)
	if tag == "" || tag != currentTag || laterBody.Name != "Edited elsewhere" || laterBody.AuthoringProjection == nil || laterBody.AuthoringProjection.Scenario.Name != laterBody.Name {
		t.Fatalf("later replay paired body %q with tag %q (current %q)", laterBody.Name, tag, currentTag)
	}
}

// A replica copy that is not the durable state never receives the durable
// validator, so a client cannot save that copy over a change it did not see.
func TestScenarioValidatorIsOmittedForAStaleCopy(t *testing.T) {
	server, store, _, scenario := newScenarioPolicyAvailabilityFixture(t)
	snapshot, err := store.GetScenarioAuthoringSnapshot(scenario.ID)
	if err != nil {
		t.Fatal(err)
	}
	current, _ := server.runner.GetScenario(scenario.ID)
	matching := httptest.NewRecorder()
	response := maskStoredScenario(current)
	attachScenarioAuthoringValidator(matching, response, current, snapshot)
	if matching.Header().Get("ETag") == "" || response.AuthoringProjection == nil {
		t.Fatal("the durable copy did not receive its validator")
	}
	stale := *current
	stale.UpdatedAt = snapshot.Scenario.UpdatedAt.Add(-time.Second)
	recorder := httptest.NewRecorder()
	staleResponse := maskStoredScenario(&stale)
	attachScenarioAuthoringValidator(recorder, staleResponse, &stale, snapshot)
	if recorder.Header().Get("ETag") != "" || staleResponse.AuthoringProjection != nil || staleResponse.ContentRevision != "" {
		t.Fatalf("stale copy received validator %q", recorder.Header().Get("ETag"))
	}
}

// scenarioCreateLostAckStore commits the create and then reports its outcome
// as unknown, as a connection reset during COMMIT does.
type scenarioCreateLostAckStore struct {
	SQLiteStore *storage.SQLiteStore
}

func (s *scenarioCreateLostAckStore) CreateScenarioAuthoringIfAbsent(scenario *storage.Scenario, groups []storage.ScenarioScript, pools []storage.ScenarioDataPoolBinding) (*storage.ScenarioAuthoringMutationResult, error) {
	if _, err := s.SQLiteStore.CreateScenarioAuthoringIfAbsent(scenario, groups, pools); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("%w: connection reset during commit", storage.ErrScenarioAuthoringOutcomeUnknown)
}

// A create whose commit acknowledgement is lost answers 503, keeps its key
// pinned, makes the committed scenario visible on this replica, and the
// retry adopts it instead of creating another.
func TestScenarioCreateWithALostCommitAckIsAdoptedOnRetry(t *testing.T) {
	server, store, _, _ := newScenarioPolicyAvailabilityFixture(t)
	body := scenarioSaveRequest("Lost ack create", 1)
	server.runner.scenarioStore = sqliteOverlay(&scenarioCreateLostAckStore{SQLiteStore: store})
	first := idempotentCreateRequest(t, server, "/api/v1/scenarios", body, scenarioCreateKey)
	server.runner.scenarioStore = store
	if first.Code != http.StatusServiceUnavailable || !strings.Contains(first.Body.String(), "scenario_save_outcome_unknown") {
		t.Fatalf("lost acknowledgement: %d %s", first.Code, first.Body.String())
	}
	if countScenariosNamed(server, "Lost ack create") != 1 {
		t.Fatal("the committed scenario is not visible on this replica")
	}
	retry := idempotentCreateRequest(t, server, "/api/v1/scenarios", body, scenarioCreateKey)
	if retry.Code != http.StatusCreated || retry.Header().Get("Idempotent-Replayed") != "true" || countScenariosNamed(server, "Lost ack create") != 1 {
		t.Fatalf("retry after a lost acknowledgement: %d %s", retry.Code, retry.Body.String())
	}
}

// A delete refused inside its transaction (the strict policy requires a
// validator) withdraws its tombstone, so the create key still replays.
func TestStrictPolicyRefusedDeleteKeepsTheCreateKeyReplaying(t *testing.T) {
	server, store, _, _ := newScenarioPolicyAvailabilityFixture(t)
	body := scenarioSaveRequest("Strict delete", 1)
	first := idempotentCreateRequest(t, server, "/api/v1/scenarios", body, scenarioCreateKey)
	if first.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", first.Code, first.Body.String())
	}
	setScenarioStrictPolicy(t, store, true)
	if response := authoringAPIRequest(t, server, http.MethodDelete, "/api/v1/scenarios/"+decodeScenarioID(t, first), nil, ""); response.Code != http.StatusPreconditionRequired {
		t.Fatalf("unconditioned delete under the strict policy: %d %s", response.Code, response.Body.String())
	}
	replay := idempotentCreateRequest(t, server, "/api/v1/scenarios", body, scenarioCreateKey)
	if replay.Code != http.StatusCreated || replay.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("create key after a refused delete: %d %s", replay.Code, replay.Body.String())
	}
}

// DELETE resolves ownership before it reads If-Match: an unknown scenario is
// 404 even with a malformed validator.
func TestDeleteOfAnUnknownScenarioIsNotFoundBeforeItsValidator(t *testing.T) {
	server, _, _, _ := newScenarioPolicyAvailabilityFixture(t)
	if response := authoringAPIRequest(t, server, http.MethodDelete, "/api/v1/scenarios/no-such-scenario", nil, "W/\"weak\""); response.Code != http.StatusNotFound {
		t.Fatalf("unknown scenario with a malformed If-Match: %d %s", response.Code, response.Body.String())
	}
}

// A lost-ack create carrying runtime credentials is adopted on retry too:
// the retry replays the committed scenario instead of provisioning the
// credentials again against the first attempt's pending journal.
func TestScenarioCreateWithCredentialsAndALostCommitAckIsAdoptedOnRetry(t *testing.T) {
	server, store, _, _ := newScenarioPolicyAvailabilityFixture(t)
	body := scenarioSaveRequest("Lost ack credentials", 1)
	body.RuntimeSettings = &RuntimeSettingsOverride{Env: map[string]string{"API_TOKEN": "lost-ack-runtime-value"}}
	server.runner.scenarioStore = sqliteOverlay(&scenarioCreateLostAckStore{SQLiteStore: store})
	first := idempotentCreateRequest(t, server, "/api/v1/scenarios", body, scenarioCreateKey)
	server.runner.scenarioStore = store
	if first.Code != http.StatusServiceUnavailable {
		t.Fatalf("lost acknowledgement: %d %s", first.Code, first.Body.String())
	}
	retry := idempotentCreateRequest(t, server, "/api/v1/scenarios", body, scenarioCreateKey)
	if retry.Code != http.StatusCreated || retry.Header().Get("Idempotent-Replayed") != "true" || countScenariosNamed(server, "Lost ack credentials") != 1 {
		t.Fatalf("retry after a lost acknowledgement: %d %s", retry.Code, retry.Body.String())
	}
}

// The default scenario representation carries the durable validator, so an
// editor that builds its draft from it can save it conditioned.
func TestScenarioGetCarriesTheDurableValidator(t *testing.T) {
	server, _, _, scenario := newScenarioPolicyAvailabilityFixture(t)
	response := authoringAPIRequest(t, server, http.MethodGet, "/api/v1/scenarios/"+scenario.ID, nil, "")
	body := decodeStoredScenario(t, response.Body.Bytes())
	_, viewTag, _ := requireScenarioAuthoringView(t, server, scenario.ID)
	if response.Code != http.StatusOK || response.Header().Get("ETag") == "" || response.Header().Get("ETag") != viewTag || body.ContentRevision == "" || body.AuthoringProjection == nil {
		t.Fatalf("GET %d tag %q (view %q) revision %q", response.Code, response.Header().Get("ETag"), viewTag, body.ContentRevision)
	}
	save := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scenarios/"+scenario.ID, scenarioSaveRequest("Saved from GET", 2), response.Header().Get("ETag"))
	if save.Code != http.StatusOK {
		t.Fatalf("conditioned save with the GET's tag: %d %s", save.Code, save.Body.String())
	}
}
