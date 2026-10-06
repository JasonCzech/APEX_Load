package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

const scenarioCreateKey = "scenario-create-key-0001"

func countScenariosNamed(server *Server, name string) int {
	count := 0
	for _, scenario := range server.runner.ListScenarios() {
		if scenario != nil && scenario.Name == name {
			count++
		}
	}
	return count
}

// claimCrashedScenarioCreate reproduces an attempt that reserved the key and
// durably created the scenario, then crashed before completing the receipt.
func claimCrashedScenarioCreate(t *testing.T, server *Server, store *storage.SQLiteStore, body CreateScenarioRequest, create bool) string {
	t.Helper()
	projectID, err := server.scenarioRequestProjectID(body)
	if err != nil {
		t.Fatal(err)
	}
	probe := httptest.NewRequest(http.MethodPost, "/api/v1/scenarios", nil)
	identity, err := server.authoringReceiptIdentity(probe, storage.AuthoringPurposeScenarioCreate, projectID, scenarioCreateKey, "", body)
	if err != nil {
		t.Fatal(err)
	}
	identity.ResourceType, identity.OwnerToken = "scenario", "crashed-owner"
	if _, err := store.ClaimAuthoringMutationReceipt(identity.AuthoringMutationReceiptClaimInput, time.Now().UTC(), time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if create {
		if _, err := server.runner.StoreScenarioWithIDCreateOnly(identity.ResourceID, body); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(10 * time.Millisecond)
	return identity.ResourceID
}

func TestIdempotentScenarioCreateAdoptsAnUnconfirmedEarlierCreate(t *testing.T) {
	server, store, _, _ := newScenarioPolicyAvailabilityFixture(t)
	body := scenarioSaveRequest("Lost scenario response", 2)
	id := claimCrashedScenarioCreate(t, server, store, body, true)
	retry := idempotentCreateRequest(t, server, "/api/v1/scenarios", body, scenarioCreateKey)
	if retry.Code != http.StatusCreated || retry.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("retry after lost response: %d %s", retry.Code, retry.Body.String())
	}
	// Adoption cannot know the revision the original create produced.
	if original := retry.Header().Get("X-APEX-Original-Content-Revision"); original != "" {
		t.Fatalf("adoption reported an original revision %q", original)
	}
	var replayed StoredScenario
	if err := json.Unmarshal(retry.Body.Bytes(), &replayed); err != nil {
		t.Fatal(err)
	}
	if replayed.ID != id || countScenariosNamed(server, "Lost scenario response") != 1 {
		t.Fatalf("retry did not adopt the earlier scenario: %s (%d)", replayed.ID, countScenariosNamed(server, "Lost scenario response"))
	}
	if again := idempotentCreateRequest(t, server, "/api/v1/scenarios", body, scenarioCreateKey); again.Code != http.StatusCreated || again.Header().Get("Idempotent-Replayed") != "true" || countScenariosNamed(server, "Lost scenario response") != 1 {
		t.Fatalf("receipt was not completed by adoption: %d %s", again.Code, again.Body.String())
	}
}

// A resumed attempt whose own request is then refused must keep the receipt
// while the earlier attempt's scenario exists, so the key is never reused to
// recreate it after a delete.
func TestResumedReceiptIsKeptWhileTheEarlierCreateExists(t *testing.T) {
	server, store, _, _ := newScenarioPolicyAvailabilityFixture(t)
	body := scenarioSaveRequest("Resumed then refused", 2)
	id := claimCrashedScenarioCreate(t, server, store, body, true)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scenarios", nil)
	request.Header.Set("Idempotency-Key", scenarioCreateKey)
	projectID, _ := server.scenarioRequestProjectID(body)
	attempt, outcome := server.claimAuthoringCreate(httptest.NewRecorder(), request, storage.AuthoringPurposeScenarioCreate, "scenario", projectID, "", body)
	if outcome != authoringCreateProceed || attempt == nil || !attempt.resumed {
		t.Fatalf("expected a resumed claim: %v %#v", outcome, attempt)
	}
	attempt.absent = server.durableScenarioAbsent
	// The resumed request is refused before its own create runs.
	attempt.release()
	if response := authoringAPIRequest(t, server, http.MethodDelete, "/api/v1/scenarios/"+id, nil, ""); response.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", response.Code, response.Body.String())
	}
	retry := idempotentCreateRequest(t, server, "/api/v1/scenarios", body, scenarioCreateKey)
	if retry.Code != http.StatusGone || idempotencyErrorCode(t, retry) != "idempotency_resource_deleted" || countScenariosNamed(server, "Resumed then refused") != 0 {
		t.Fatalf("deleted scenario was recreated through a released resumed receipt: %d %s", retry.Code, retry.Body.String())
	}
}

// A resumed receipt whose earlier attempt never created anything is released
// once a durable read proves it, so a corrected request can use the key.
func TestResumedReceiptIsReleasedWhenTheResourceIsDurablyAbsent(t *testing.T) {
	server, store, _, _ := newScenarioPolicyAvailabilityFixture(t)
	body := scenarioSaveRequest("Never created", 2)
	claimCrashedScenarioCreate(t, server, store, body, false)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scenarios", nil)
	request.Header.Set("Idempotency-Key", scenarioCreateKey)
	projectID, _ := server.scenarioRequestProjectID(body)
	attempt, outcome := server.claimAuthoringCreate(httptest.NewRecorder(), request, storage.AuthoringPurposeScenarioCreate, "scenario", projectID, "", body)
	if outcome != authoringCreateProceed || attempt == nil || !attempt.resumed {
		t.Fatalf("expected a resumed claim: %v %#v", outcome, attempt)
	}
	attempt.absent = server.durableScenarioAbsent
	attempt.release()
	corrected := scenarioSaveRequest("Corrected after release", 3)
	if response := idempotentCreateRequest(t, server, "/api/v1/scenarios", corrected, scenarioCreateKey); response.Code != http.StatusCreated || response.Header().Get("Idempotent-Replayed") != "" {
		t.Fatalf("corrected request after a proven-absent release: %d %s", response.Code, response.Body.String())
	}
}

// Scenario validation runs inside the create, after the receipt is marked
// attempted; a rejected request must still release its key.
func TestIdempotentScenarioCreateReleasesKeyAfterARejectedRequest(t *testing.T) {
	server, _, _, _ := newScenarioPolicyAvailabilityFixture(t)
	invalid := scenarioSaveRequest("Rejected scenario", 1)
	invalid.Duration = ""
	if rejected := idempotentCreateRequest(t, server, "/api/v1/scenarios", invalid, scenarioCreateKey); rejected.Code != http.StatusBadRequest {
		t.Fatalf("invalid create: %d %s", rejected.Code, rejected.Body.String())
	}
	if again := idempotentCreateRequest(t, server, "/api/v1/scenarios", invalid, scenarioCreateKey); again.Code != http.StatusBadRequest {
		t.Fatalf("identical rejected request reported %d %s", again.Code, again.Body.String())
	}
	if corrected := idempotentCreateRequest(t, server, "/api/v1/scenarios", scenarioSaveRequest("Rejected scenario", 1), scenarioCreateKey); corrected.Code != http.StatusCreated || corrected.Header().Get("Idempotent-Replayed") != "" {
		t.Fatalf("corrected request with the released key: %d %s", corrected.Code, corrected.Body.String())
	}
}

func TestIdempotentScenarioCreateConcurrentRetriesCreateOneScenario(t *testing.T) {
	server, _, _, _ := newScenarioPolicyAvailabilityFixture(t)
	body := scenarioSaveRequest("Concurrent scenario", 1)
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			response := idempotentCreateRequest(t, server, "/api/v1/scenarios", body, scenarioCreateKey)
			if response.Code != http.StatusCreated && idempotencyErrorCode(t, response) != "idempotency_request_in_progress" {
				t.Errorf("unexpected response: %d %s", response.Code, response.Body.String())
			}
		}()
	}
	wg.Wait()
	if count := countScenariosNamed(server, "Concurrent scenario"); count != 1 {
		t.Fatalf("concurrent retries created %d scenarios", count)
	}
}

// Replay reads the store, so a replica whose cache predates the create
// answers with the scenario instead of claiming it was deleted.
func TestIdempotentScenarioReplayOnAReplicaWithAStaleCache(t *testing.T) {
	path := filepath.Join(t.TempDir(), "replicas.db")
	open := func() *Server {
		store, err := storage.NewSQLiteStore(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { store.Close() })
		return NewServer(ServerConfig{Store: store})
	}
	owner := open()
	owner.runner.StoreScript(projectSecurityScript("scenario-policy-script", storage.DefaultProjectID))
	peer := open()
	body := scenarioSaveRequest("Created on the owner", 1)
	first := idempotentCreateRequest(t, owner, "/api/v1/scenarios", body, scenarioCreateKey)
	if first.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", first.Code, first.Body.String())
	}
	if _, cached := peer.runner.getScenarioForMutation(decodeScenarioID(t, first)); cached {
		t.Fatal("fixture: the peer cache already holds the scenario")
	}
	replay := idempotentCreateRequest(t, peer, "/api/v1/scenarios", body, scenarioCreateKey)
	if replay.Code != http.StatusCreated || replay.Header().Get("Idempotent-Replayed") != "true" || decodeScenarioID(t, replay) != decodeScenarioID(t, first) {
		t.Fatalf("replay on a stale replica: %d %s", replay.Code, replay.Body.String())
	}
}

func decodeScenarioID(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	var scenario StoredScenario
	if err := json.Unmarshal(response.Body.Bytes(), &scenario); err != nil {
		t.Fatal(err)
	}
	return scenario.ID
}

// A definitely refused delete withdraws its tombstone, so the create key keeps
// replaying the live scenario.
func TestRefusedDeleteKeepsTheCreateKeyReplaying(t *testing.T) {
	server, _, _, _ := newScenarioPolicyAvailabilityFixture(t)
	body := scenarioSaveRequest("Refused delete", 1)
	first := idempotentCreateRequest(t, server, "/api/v1/scenarios", body, scenarioCreateKey)
	if first.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", first.Code, first.Body.String())
	}
	id := decodeScenarioID(t, first)
	stale := first.Header().Get("ETag")
	if response := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scenarios/"+id, scenarioSaveRequest("Refused delete", 2), stale); response.Code != http.StatusOK {
		t.Fatalf("save: %d %s", response.Code, response.Body.String())
	}
	if response := authoringAPIRequest(t, server, http.MethodDelete, "/api/v1/scenarios/"+id, nil, stale); response.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale delete: %d %s", response.Code, response.Body.String())
	}
	replay := idempotentCreateRequest(t, server, "/api/v1/scenarios", body, scenarioCreateKey)
	if replay.Code != http.StatusCreated || replay.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("create key after a refused delete: %d %s", replay.Code, replay.Body.String())
	}
}

// A credential-preserving import with no attestation to claim is an ordinary
// create for idempotency: the key fences retries.
func TestIdempotentCredentialPreservingImportWithoutAttestationUsesTheReceipt(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	script := authoringAPIScript("")
	script.Name = "Preserved import copy"
	script.Metadata = &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Source: "har", Preserved: map[string]any{dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve}}}
	body := map[string]any{"script": script}
	first := idempotentCreateRequest(t, server, "/api/v1/scripts", body, scriptCreateKey)
	if first.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", first.Code, first.Body.String())
	}
	retry := idempotentCreateRequest(t, server, "/api/v1/scripts", body, scriptCreateKey)
	if retry.Code != http.StatusCreated || retry.Header().Get("Idempotent-Replayed") != "true" || countScriptsNamed(server, "Preserved import copy") != 1 {
		t.Fatalf("retry of an unattested preserving import: %d %s (%d)", retry.Code, retry.Body.String(), countScriptsNamed(server, "Preserved import copy"))
	}
}

// With authentication enforced, the generic idempotency middleware must leave
// these creates to their own receipts; otherwise a retry gets its duplicate-key
// conflict instead of the original script.
func TestIdempotentScriptCreateReplaysUnderEnforcedAuthentication(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	enableTrustedHeaderAuth(t, server)
	send := func() *httptest.ResponseRecorder {
		encoded, err := json.Marshal(idempotentScriptBody("Authenticated"))
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts", bytes.NewReader(encoded))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", scriptCreateKey)
		request.Header.Set("X-Auth-Request-User", "author")
		request.Header.Set("X-APEXLoad-Project-Roles", storage.DefaultProjectID+"="+RoleEditor)
		response := httptest.NewRecorder()
		server.mux.ServeHTTP(response, request)
		return response
	}
	if first := send(); first.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", first.Code, first.Body.String())
	}
	if retry := send(); retry.Code != http.StatusCreated || retry.Header().Get("Idempotent-Replayed") != "true" || countScriptsNamed(server, "Authenticated") != 1 {
		t.Fatalf("authenticated retry: %d %s", retry.Code, retry.Body.String())
	}
}

func TestIdempotentCreateRefusesAnExpiredKey(t *testing.T) {
	server, store, _, _ := newScenarioPolicyAvailabilityFixture(t)
	body := scenarioSaveRequest("Expired key", 1)
	projectID, err := server.scenarioRequestProjectID(body)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := server.authoringReceiptIdentity(httptest.NewRequest(http.MethodPost, "/api/v1/scenarios", nil), storage.AuthoringPurposeScenarioCreate, projectID, scenarioCreateKey, "", body)
	if err != nil {
		t.Fatal(err)
	}
	identity.ResourceType, identity.OwnerToken, identity.Retention = "scenario", "expired-owner", time.Millisecond
	if _, err := store.ClaimAuthoringMutationReceipt(identity.AuthoringMutationReceiptClaimInput, time.Now().UTC(), time.Minute); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	response := idempotentCreateRequest(t, server, "/api/v1/scenarios", body, scenarioCreateKey)
	if response.Code != http.StatusConflict || idempotencyErrorCode(t, response) != "idempotency_key_expired" || countScenariosNamed(server, "Expired key") != 0 {
		t.Fatalf("expired key: %d %s", response.Code, response.Body.String())
	}
}
