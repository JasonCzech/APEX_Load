package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
	"gopkg.in/yaml.v3"
)

func idempotentCreateRequest(t *testing.T, s *Server, path string, body any, key string) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(encoded))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	response := httptest.NewRecorder()
	s.mux.ServeHTTP(response, req)
	return response
}

func idempotencyErrorCode(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	var body AuthoringIdempotencyError
	_ = json.Unmarshal(response.Body.Bytes(), &body)
	return body.Code
}

func countScriptsNamed(server *Server, name string) int {
	count := 0
	for _, script := range server.runner.ListScripts() {
		if script != nil && script.Name == name {
			count++
		}
	}
	return count
}

func idempotentScriptBody(name string) map[string]any {
	script := authoringAPIScript("")
	script.Name = name
	return map[string]any{"script": script}
}

const scriptCreateKey = "script-create-key-0001"

func TestIdempotentScriptCreateReplaysTheOriginalScript(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	first := idempotentCreateRequest(t, server, "/api/v1/scripts", idempotentScriptBody("Idempotent"), scriptCreateKey)
	if first.Code != http.StatusCreated || first.Header().Get("Idempotent-Replayed") != "" {
		t.Fatalf("first create: %d %s", first.Code, first.Body.String())
	}
	var created StoredScript
	if err := json.Unmarshal(first.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	retry := idempotentCreateRequest(t, server, "/api/v1/scripts", idempotentScriptBody("Idempotent"), scriptCreateKey)
	if retry.Code != http.StatusCreated || retry.Header().Get("Idempotent-Replayed") != "true" || retry.Header().Get("X-APEX-Original-Content-Revision") != created.ContentRevision {
		t.Fatalf("retry: %d %v %s", retry.Code, retry.Header(), retry.Body.String())
	}
	var replayed StoredScript
	if err := json.Unmarshal(retry.Body.Bytes(), &replayed); err != nil {
		t.Fatal(err)
	}
	if replayed.ID != created.ID || countScriptsNamed(server, "Idempotent") != 1 {
		t.Fatalf("retry created another script: %s vs %s (%d)", replayed.ID, created.ID, countScriptsNamed(server, "Idempotent"))
	}
	changed := idempotentCreateRequest(t, server, "/api/v1/scripts", idempotentScriptBody("Different"), scriptCreateKey)
	if changed.Code != http.StatusConflict || idempotencyErrorCode(t, changed) != "idempotency_key_reused" || countScriptsNamed(server, "Different") != 0 {
		t.Fatalf("changed request with the same key: %d %s", changed.Code, changed.Body.String())
	}
	if other := idempotentCreateRequest(t, server, "/api/v1/scripts", idempotentScriptBody("Idempotent"), "script-create-key-0002"); other.Code != http.StatusCreated || countScriptsNamed(server, "Idempotent") != 2 {
		t.Fatalf("a new key must create a new script: %d %s", other.Code, other.Body.String())
	}
}

func TestIdempotentScriptCreateConcurrentRetriesCreateOneScript(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	codes := make([]int, 4)
	var wg sync.WaitGroup
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			response := idempotentCreateRequest(t, server, "/api/v1/scripts", idempotentScriptBody("Concurrent"), scriptCreateKey)
			codes[i] = response.Code
			if response.Code == http.StatusConflict && idempotencyErrorCode(t, response) != "idempotency_request_in_progress" {
				t.Errorf("unexpected conflict: %s", response.Body.String())
			}
		}(i)
	}
	wg.Wait()
	if countScriptsNamed(server, "Concurrent") != 1 {
		t.Fatalf("concurrent retries created %d scripts (%v)", countScriptsNamed(server, "Concurrent"), codes)
	}
}

func TestIdempotentScriptCreateAdoptsAnUnconfirmedEarlierCreate(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	body := idempotentScriptBody("Lost response")
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	var request scriptCreateFingerprint
	if err := json.Unmarshal(encoded, &request); err != nil {
		t.Fatal(err)
	}
	// Reproduce an attempt that reserved the key and durably created the
	// script, then crashed before completing the receipt or responding.
	probe := httptest.NewRequest(http.MethodPost, "/api/v1/scripts", nil)
	identity, err := server.authoringReceiptIdentity(probe, storage.AuthoringPurposeScriptCreate, request.Script.ProjectID, scriptCreateKey, "", authoringReceiptFingerprint{request: request})
	if err != nil {
		t.Fatal(err)
	}
	identity.ResourceType, identity.OwnerToken = "script", "crashed-owner"
	if _, err := store.ClaimAuthoringMutationReceipt(identity.AuthoringMutationReceiptClaimInput, time.Now().UTC(), time.Millisecond); err != nil {
		t.Fatal(err)
	}
	crashed := request.Script
	crashed.ID = identity.ResourceID
	if _, err := server.runner.CreateScriptAuthoringDurable(&crashed); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	retry := idempotentCreateRequest(t, server, "/api/v1/scripts", body, scriptCreateKey)
	if retry.Code != http.StatusCreated || retry.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("retry after lost response: %d %s", retry.Code, retry.Body.String())
	}
	var replayed StoredScript
	if err := json.Unmarshal(retry.Body.Bytes(), &replayed); err != nil {
		t.Fatal(err)
	}
	if replayed.ID != identity.ResourceID || countScriptsNamed(server, "Lost response") != 1 {
		t.Fatalf("retry after a lost response did not adopt the earlier script: %s (%d)", replayed.ID, countScriptsNamed(server, "Lost response"))
	}
	again := idempotentCreateRequest(t, server, "/api/v1/scripts", body, scriptCreateKey)
	if again.Code != http.StatusCreated || again.Header().Get("Idempotent-Replayed") != "true" || countScriptsNamed(server, "Lost response") != 1 {
		t.Fatalf("receipt was not completed by adoption: %d %s", again.Code, again.Body.String())
	}
}

func TestIdempotentScriptCreateNeverRecreatesADeletedScript(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	first := idempotentCreateRequest(t, server, "/api/v1/scripts", idempotentScriptBody("Deleted later"), scriptCreateKey)
	var created StoredScript
	if err := json.Unmarshal(first.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if response := authoringAPIRequest(t, server, http.MethodDelete, "/api/v1/scripts/"+created.ID, nil, ""); response.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", response.Code, response.Body.String())
	}
	retry := idempotentCreateRequest(t, server, "/api/v1/scripts", idempotentScriptBody("Deleted later"), scriptCreateKey)
	if retry.Code != http.StatusGone || idempotencyErrorCode(t, retry) != "idempotency_resource_deleted" || countScriptsNamed(server, "Deleted later") != 0 {
		t.Fatalf("retry after delete: %d %s", retry.Code, retry.Body.String())
	}
}

func TestIdempotentScriptCreateReleasesKeyAfterARejectedRequest(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	invalid := authoringAPIScript("")
	invalid.Name = "Rejected"
	invalid.Actions = nil
	rejected := idempotentCreateRequest(t, server, "/api/v1/scripts", map[string]any{"script": invalid}, scriptCreateKey)
	if rejected.Code != http.StatusBadRequest {
		t.Fatalf("invalid create: %d %s", rejected.Code, rejected.Body.String())
	}
	corrected := idempotentCreateRequest(t, server, "/api/v1/scripts", idempotentScriptBody("Rejected"), scriptCreateKey)
	if corrected.Code != http.StatusCreated || corrected.Header().Get("Idempotent-Replayed") != "" {
		t.Fatalf("corrected request with the released key: %d %s", corrected.Code, corrected.Body.String())
	}
}

func TestIdempotentCreateRefusesInvalidKeysAndUnsupportedStores(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	for _, key := range []string{"short", "has a space in it!!"} {
		response := idempotentCreateRequest(t, server, "/api/v1/scripts", idempotentScriptBody("Bad key"), key)
		if response.Code != http.StatusBadRequest || idempotencyErrorCode(t, response) != "idempotency_key_invalid" {
			t.Fatalf("key %q: %d %s", key, response.Code, response.Body.String())
		}
	}
	memory := NewServer(ServerConfig{})
	response := idempotentCreateRequest(t, memory, "/api/v1/scripts", idempotentScriptBody("Memory"), scriptCreateKey)
	if response.Code != http.StatusServiceUnavailable || idempotencyErrorCode(t, response) != "idempotency_unavailable" || countScriptsNamed(memory, "Memory") != 0 {
		t.Fatalf("memory-only idempotent create: %d %s", response.Code, response.Body.String())
	}
	if plain := idempotentCreateRequest(t, memory, "/api/v1/scripts", idempotentScriptBody("Memory"), ""); plain.Code != http.StatusCreated {
		t.Fatalf("memory-only create without a key: %d %s", plain.Code, plain.Body.String())
	}
}

func TestIdempotentScenarioCreateReplaysAndRespectsDeletion(t *testing.T) {
	server, store, _, _ := newScenarioPolicyAvailabilityFixture(t)
	const key = "scenario-create-key-0001"
	body := scenarioSaveRequest("Idempotent scenario", 2)
	first := idempotentCreateRequest(t, server, "/api/v1/scenarios", body, key)
	if first.Code != http.StatusCreated {
		t.Fatalf("first create: %d %s", first.Code, first.Body.String())
	}
	var created StoredScenario
	if err := json.Unmarshal(first.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	retry := idempotentCreateRequest(t, server, "/api/v1/scenarios", body, key)
	var replayed StoredScenario
	if err := json.Unmarshal(retry.Body.Bytes(), &replayed); err != nil {
		t.Fatal(err)
	}
	if retry.Code != http.StatusCreated || retry.Header().Get("Idempotent-Replayed") != "true" || replayed.ID != created.ID {
		t.Fatalf("retry: %d %s", retry.Code, retry.Body.String())
	}
	countNamed := func(name string) int {
		count := 0
		for _, scenario := range server.runner.ListScenarios() {
			if scenario.Name == name {
				count++
			}
		}
		return count
	}
	if countNamed("Idempotent scenario") != 1 {
		t.Fatal("retry created another scenario")
	}
	if changed := idempotentCreateRequest(t, server, "/api/v1/scenarios", scenarioSaveRequest("Changed scenario", 2), key); changed.Code != http.StatusConflict || idempotencyErrorCode(t, changed) != "idempotency_key_reused" {
		t.Fatalf("changed scenario request: %d %s", changed.Code, changed.Body.String())
	}
	if response := authoringAPIRequest(t, server, http.MethodDelete, "/api/v1/scenarios/"+created.ID, nil, ""); response.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", response.Code, response.Body.String())
	}
	if _, err := store.GetScenarioAuthoringSnapshot(created.ID); err == nil {
		t.Fatal("scenario not deleted")
	}
	afterDelete := idempotentCreateRequest(t, server, "/api/v1/scenarios", body, key)
	if afterDelete.Code != http.StatusGone || idempotencyErrorCode(t, afterDelete) != "idempotency_resource_deleted" || countNamed("Idempotent scenario") != 0 {
		t.Fatalf("retry after delete: %d %s", afterDelete.Code, afterDelete.Body.String())
	}
}

func TestIdempotentCreateOpenAPIContract(t *testing.T) {
	data, err := GenerateOpenAPIYAML("server.go")
	if err != nil {
		t.Fatal(err)
	}
	var doc oasDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/scripts", "/api/v1/scenarios"} {
		operation := doc.Paths[path]["post"]
		if !operationHasParameter(operation, "header", "Idempotency-Key") {
			t.Fatalf("%s does not document Idempotency-Key", path)
		}
		if _, ok := operation.Responses["201"].Headers["Idempotent-Replayed"]; !ok {
			t.Fatalf("%s does not document replay responses", path)
		}
		for status, code := range map[string]string{"409": "idempotency_key_reused", "410": "idempotency_resource_deleted", "503": "idempotency_unavailable"} {
			if !slices.Contains(operation.Responses[status].Content["application/json"].Schema.Properties["code"].Enum, code) {
				t.Fatalf("%s %s does not document %s", path, status, code)
			}
		}
	}
}
