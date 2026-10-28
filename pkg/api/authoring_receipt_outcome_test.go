package api

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/keywrap"
	"github.com/jasonczech/apexload/pkg/storage"
	"gopkg.in/yaml.v3"
)

// receiptCreateRequest is a keyed create under the given receipt contract
// ("" sends no contract header).
func receiptCreateRequest(t *testing.T, s *Server, path string, body any, key, version string) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(encoded))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	if version != "" {
		req.Header.Set(authoringReceiptVersionHeader, version)
	}
	response := httptest.NewRecorder()
	s.mux.ServeHTTP(response, req)
	return response
}

func decodeReceiptOutcome(t *testing.T, response *httptest.ResponseRecorder) AuthoringReceiptOutcome {
	t.Helper()
	var outcome AuthoringReceiptOutcome
	if err := json.Unmarshal(response.Body.Bytes(), &outcome); err != nil {
		t.Fatal(err)
	}
	return outcome
}

func TestScriptCreateReceiptV2ReplaysTheOriginalOutcome(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	body := idempotentScriptBody("Original outcome")
	first := receiptCreateRequest(t, server, "/api/v1/scripts", body, scriptCreateKey, "2")
	if first.Code != http.StatusCreated || first.Header().Get(authoringReceiptVersionHeader) != "2" || first.Header().Get("Idempotent-Replayed") != "" {
		t.Fatalf("first create: %d %v %s", first.Code, first.Header(), first.Body.String())
	}
	var created StoredScript
	if err := json.Unmarshal(first.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	// Edit the script after its create. A replay must not adopt the edit.
	_, tag := requireAuthoringAPIProjection(t, server, created.ID)
	edited := authoringAPIScript(created.ID)
	edited.Name = "Edited since"
	if update := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scripts/"+created.ID, map[string]any{"script": edited}, tag); update.Code != http.StatusOK {
		t.Fatalf("edit: %d %s", update.Code, update.Body.String())
	}
	current, _ := requireAuthoringAPIProjection(t, server, created.ID)

	retry := receiptCreateRequest(t, server, "/api/v1/scripts", body, scriptCreateKey, "2")
	if retry.Code != http.StatusCreated || retry.Header().Get("Idempotent-Replayed") != "true" || retry.Header().Get(authoringReceiptVersionHeader) != "2" ||
		retry.Header().Get("X-APEX-Original-Content-Revision") != created.ContentRevision || retry.Header().Get("ETag") != "" {
		t.Fatalf("replay: %d %v %s", retry.Code, retry.Header(), retry.Body.String())
	}
	outcome := decodeReceiptOutcome(t, retry)
	var projection ScriptAuthoringProjection
	if err := json.Unmarshal(outcome.AuthoringProjection, &projection); err != nil {
		t.Fatal(err)
	}
	if outcome.ReceiptVersion != 2 || outcome.ResourceType != "script" || outcome.ResourceID != created.ID || outcome.ContentRevision != created.ContentRevision ||
		outcome.ContentRevision == current.ContentRevision || projection.Script.Name != "Original outcome" || outcome.ProjectionOmitted != "" {
		t.Fatalf("replayed outcome is not the original: %#v %s", outcome, projection.Script.Name)
	}
	if countScriptsNamed(server, "Original outcome")+countScriptsNamed(server, "Edited since") != 1 {
		t.Fatal("replay created another script")
	}

	// A legacy client retrying the key keeps the legacy replay.
	legacy := receiptCreateRequest(t, server, "/api/v1/scripts", body, scriptCreateKey, "")
	var legacyBody StoredScript
	if err := json.Unmarshal(legacy.Body.Bytes(), &legacyBody); err != nil {
		t.Fatal(err)
	}
	if legacy.Code != http.StatusCreated || legacy.Header().Get(authoringReceiptVersionHeader) != "1" || legacyBody.Name != "Edited since" {
		t.Fatalf("legacy replay of a version 2 key: %d %v %s", legacy.Code, legacy.Header(), legacy.Body.String())
	}

	// A deleted original is never returned.
	if response := authoringAPIRequest(t, server, http.MethodDelete, "/api/v1/scripts/"+created.ID, nil, ""); response.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", response.Code, response.Body.String())
	}
	if gone := receiptCreateRequest(t, server, "/api/v1/scripts", body, scriptCreateKey, "2"); gone.Code != http.StatusGone || idempotencyErrorCode(t, gone) != "idempotency_resource_deleted" {
		t.Fatalf("replay after delete: %d %s", gone.Code, gone.Body.String())
	}
}

func TestScriptCreateReceiptV2KeepsALegacyReceiptsContract(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	body := idempotentScriptBody("Legacy receipt")
	if first := receiptCreateRequest(t, server, "/api/v1/scripts", body, scriptCreateKey, ""); first.Code != http.StatusCreated || first.Header().Get(authoringReceiptVersionHeader) != "1" {
		t.Fatalf("legacy create: %d %v %s", first.Code, first.Header(), first.Body.String())
	}
	retry := receiptCreateRequest(t, server, "/api/v1/scripts", body, scriptCreateKey, "2")
	var replayed StoredScript
	if err := json.Unmarshal(retry.Body.Bytes(), &replayed); err != nil {
		t.Fatal(err)
	}
	if retry.Code != http.StatusCreated || retry.Header().Get("Idempotent-Replayed") != "true" || retry.Header().Get(authoringReceiptVersionHeader) != "1" || replayed.Name != "Legacy receipt" {
		t.Fatalf("version 2 retry of a legacy key: %d %v %s", retry.Code, retry.Header(), retry.Body.String())
	}
	if countScriptsNamed(server, "Legacy receipt") != 1 {
		t.Fatal("version 2 retry reissued a legacy create")
	}
}

func TestCreateReceiptVersionNegotiation(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	for _, version := range []string{"3", "0", "two", " "} {
		response := receiptCreateRequest(t, server, "/api/v1/scripts", idempotentScriptBody("Bad version"), scriptCreateKey, version)
		if response.Code != http.StatusBadRequest || idempotencyErrorCode(t, response) != "idempotency_receipt_version_unsupported" {
			t.Fatalf("version %q: %d %s", version, response.Code, response.Body.String())
		}
	}
	encoded, _ := json.Marshal(idempotentScriptBody("Bad version"))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts", bytes.NewReader(encoded))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", scriptCreateKey)
	req.Header.Add(authoringReceiptVersionHeader, "2")
	req.Header.Add(authoringReceiptVersionHeader, "2")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, req)
	if response.Code != http.StatusBadRequest || idempotencyErrorCode(t, response) != "idempotency_receipt_version_unsupported" {
		t.Fatalf("repeated version header: %d %s", response.Code, response.Body.String())
	}
	if countScriptsNamed(server, "Bad version") != 0 {
		t.Fatal("a refused contract created a script")
	}
	// The contract applies only to keyed creates.
	if plain := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scripts", idempotentScriptBody("Unkeyed"), ""); plain.Code != http.StatusCreated || plain.Header().Get(authoringReceiptVersionHeader) != "" {
		t.Fatalf("unkeyed create: %d %v", plain.Code, plain.Header())
	}
	memory := NewServer(ServerConfig{})
	if response := receiptCreateRequest(t, memory, "/api/v1/scripts", idempotentScriptBody("Memory"), scriptCreateKey, "2"); response.Code != http.StatusServiceUnavailable || countScriptsNamed(memory, "Memory") != 0 {
		t.Fatalf("version 2 without durable receipts: %d %s", response.Code, response.Body.String())
	}
}

func TestScriptCreateReceiptV2AdoptsAnOlderControllersOrphan(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	body := idempotentScriptBody("Orphaned by an older controller")
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	var request scriptCreateFingerprint
	if err := json.Unmarshal(encoded, &request); err != nil {
		t.Fatal(err)
	}
	// A version 2 claim that an older controller resumed, created under the
	// derived ID with its own adoption logic, and never completed.
	probe := httptest.NewRequest(http.MethodPost, "/api/v1/scripts", nil)
	identity, err := server.authoringReceiptIdentity(probe, storage.AuthoringPurposeScriptCreate, request.Script.ProjectID, scriptCreateKey, "", authoringReceiptFingerprint{request: request})
	if err != nil {
		t.Fatal(err)
	}
	identity.ResourceType, identity.OwnerToken, identity.OutcomeVersion = "script", "older-controller", storage.AuthoringReceiptOutcomeOriginal
	if _, err := store.ClaimAuthoringMutationReceipt(identity.AuthoringMutationReceiptClaimInput, time.Now().UTC(), time.Millisecond); err != nil {
		t.Fatal(err)
	}
	orphan := request.Script
	orphan.ID = identity.ResourceID
	if _, err := server.runner.CreateScriptAuthoringDurable(&orphan); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	response := receiptCreateRequest(t, server, "/api/v1/scripts", body, scriptCreateKey, "2")
	if response.Code != http.StatusCreated || response.Header().Get("Idempotent-Replayed") != "true" || response.Header().Get(authoringReceiptVersionHeader) != "1" {
		t.Fatalf("retry over an orphaned create: %d %v %s", response.Code, response.Header(), response.Body.String())
	}
	if countScriptsNamed(server, "Orphaned by an older controller") != 1 {
		t.Fatal("the orphan was duplicated")
	}
	again := receiptCreateRequest(t, server, "/api/v1/scripts", body, scriptCreateKey, "2")
	if again.Code != http.StatusCreated || again.Header().Get("Idempotent-Replayed") != "true" || again.Header().Get(authoringReceiptVersionHeader) != "1" {
		t.Fatalf("adoption did not complete the receipt: %d %v %s", again.Code, again.Header(), again.Body.String())
	}
}

func TestScenarioCreateReceiptV2ReplaysTheOriginalOutcome(t *testing.T) {
	server, store, _, _ := newScenarioPolicyAvailabilityFixture(t)
	const key = "scenario-create-key-0002"
	body := scenarioSaveRequest("Original scenario", 2)
	first := receiptCreateRequest(t, server, "/api/v1/scenarios", body, key, "2")
	if first.Code != http.StatusCreated || first.Header().Get(authoringReceiptVersionHeader) != "2" {
		t.Fatalf("first create: %d %v %s", first.Code, first.Header(), first.Body.String())
	}
	var created StoredScenario
	if err := json.Unmarshal(first.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if update := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scenarios/"+created.ID, scenarioSaveRequest("Edited scenario", 3), first.Header().Get("ETag")); update.Code != http.StatusOK {
		t.Fatalf("edit: %d %s", update.Code, update.Body.String())
	}
	retry := receiptCreateRequest(t, server, "/api/v1/scenarios", body, key, "2")
	if retry.Code != http.StatusCreated || retry.Header().Get("Idempotent-Replayed") != "true" || retry.Header().Get(authoringReceiptVersionHeader) != "2" || retry.Header().Get("ETag") != "" {
		t.Fatalf("replay: %d %v %s", retry.Code, retry.Header(), retry.Body.String())
	}
	outcome := decodeReceiptOutcome(t, retry)
	var projection ScenarioAuthoringProjection
	if err := json.Unmarshal(outcome.AuthoringProjection, &projection); err != nil {
		t.Fatal(err)
	}
	if outcome.ResourceType != "scenario" || outcome.ResourceID != created.ID || outcome.ContentRevision != created.ContentRevision ||
		projection.Scenario.Name != "Original scenario" || projection.Scenario.Groups[0].VUsers != 2 {
		t.Fatalf("replayed scenario outcome is not the original: %#v %#v", outcome, projection.Scenario)
	}
	snapshot, err := store.GetScenarioAuthoringSnapshot(created.ID)
	if err != nil || snapshot.Scenario.Name != "Edited scenario" {
		t.Fatalf("replay changed the scenario: %#v %v", snapshot, err)
	}
	if response := authoringAPIRequest(t, server, http.MethodDelete, "/api/v1/scenarios/"+created.ID, nil, ""); response.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", response.Code, response.Body.String())
	}
	if gone := receiptCreateRequest(t, server, "/api/v1/scenarios", body, key, "2"); gone.Code != http.StatusGone || idempotencyErrorCode(t, gone) != "idempotency_resource_deleted" {
		t.Fatalf("replay after delete: %d %s", gone.Code, gone.Body.String())
	}
}

func TestRenderAuthoringReceiptOutcomeOmitsAnOversizedProjection(t *testing.T) {
	large := map[string]string{"blob": string(bytes.Repeat([]byte("x"), storage.MaxAuthoringReceiptOutcomeBytes))}
	rendered, err := renderAuthoringReceiptOutcome("script", "id", "", "rev", time.Now(), large)
	if err != nil {
		t.Fatal(err)
	}
	var outcome AuthoringReceiptOutcome
	if err := json.Unmarshal([]byte(rendered), &outcome); err != nil {
		t.Fatal(err)
	}
	if len(rendered) > storage.MaxAuthoringReceiptOutcomeBytes || outcome.AuthoringProjection != nil || outcome.ProjectionOmitted != "exceeds_receipt_outcome_bound" || outcome.ProjectID != storage.DefaultProjectID {
		t.Fatalf("oversized projection: %d bytes %#v", len(rendered), outcome)
	}
}

func TestScriptReceiptOutcomeOmitsAProjectionItsStoredContentCannotFit(t *testing.T) {
	script := httpScript("bounded-outcome-script", "Bounded outcome")
	script.ProjectID = storage.DefaultProjectID
	snapshot := &storage.ScriptAuthoringSnapshot{Script: script, ContentRevision: "scr-v1-" + strings.Repeat("a", 64)}
	render := func(contentBytes int) AuthoringReceiptOutcome {
		t.Helper()
		snapshot.ContentBytes = contentBytes
		rendered, err := renderScriptReceiptOutcome(snapshot)
		if err != nil {
			t.Fatal(err)
		}
		var outcome AuthoringReceiptOutcome
		if err := json.Unmarshal([]byte(rendered), &outcome); err != nil {
			t.Fatal(err)
		}
		return outcome
	}
	if small := render(1024); small.AuthoringProjection == nil || small.ProjectionOmitted != "" {
		t.Fatalf("a small script's projection was omitted: %#v", small)
	}
	// Content beyond the bound omits the projection before building it.
	large := render(authoringReceiptProjectionBound + 1)
	if large.AuthoringProjection != nil || large.ProjectionOmitted != "exceeds_receipt_outcome_bound" ||
		large.ResourceID != script.ID || large.ContentRevision != snapshot.ContentRevision || large.ProjectID != storage.DefaultProjectID {
		t.Fatalf("an oversized script's outcome: %#v", large)
	}
	snapshot.ContentRevision = "not-a-revision"
	if _, err := renderScriptReceiptOutcome(snapshot); err == nil {
		t.Fatal("an invalid revision rendered an outcome")
	}

	// The store measures the content it reads back.
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "content-bytes.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	stored := httpScript("measured-script", "Measured")
	if _, err := store.CreateScriptAuthoringIfAbsent(stored); err != nil {
		t.Fatal(err)
	}
	measured, err := store.GetScriptAuthoringSnapshot("measured-script")
	if err != nil {
		t.Fatal(err)
	}
	actions, _ := json.Marshal(measured.Script.Actions)
	if measured.ContentBytes < len(actions) {
		t.Fatalf("stored content bytes %d below the actions' own %d", measured.ContentBytes, len(actions))
	}
}

func TestCreateReceiptVersionOpenAPIContract(t *testing.T) {
	data, err := GenerateOpenAPIYAML("server.go")
	if err != nil {
		t.Fatal(err)
	}
	var doc oasDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if _, ok := doc.Components.Schemas["AuthoringReceiptOutcome"]; !ok {
		t.Fatal("AuthoringReceiptOutcome is not a component schema")
	}
	for _, path := range []string{"/api/v1/scripts", "/api/v1/scenarios"} {
		operation := doc.Paths[path]["post"]
		if !operationHasParameter(operation, "header", authoringReceiptVersionHeader) {
			t.Fatalf("%s does not document %s", path, authoringReceiptVersionHeader)
		}
		created := operation.Responses["201"]
		if _, ok := created.Headers[authoringReceiptVersionHeader]; !ok {
			t.Fatalf("%s does not echo the receipt contract", path)
		}
		if oneOf := created.Content["application/json"].Schema.OneOf; len(oneOf) != 2 || oneOf[1].Ref != "#/components/schemas/AuthoringReceiptOutcome" {
			t.Fatalf("%s 201 does not describe the original-outcome replay", path)
		}
		for status, code := range map[string]string{"400": "idempotency_receipt_version_unsupported", "503": "idempotency_receipt_version_unavailable"} {
			if !slices.Contains(operation.Responses[status].Content["application/json"].Schema.Properties["code"].Enum, code) {
				t.Fatalf("%s %s does not document %s", path, status, code)
			}
		}
	}
}

func TestScriptCreateReceiptV2WithoutAnOutcomeKeepsTheLegacyReplay(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	body := idempotentScriptBody("Completed by an older controller")
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	var request scriptCreateFingerprint
	if err := json.Unmarshal(encoded, &request); err != nil {
		t.Fatal(err)
	}
	// A version 2 claim that an older controller resumed and completed
	// without recording an outcome, during a rolling upgrade.
	probe := httptest.NewRequest(http.MethodPost, "/api/v1/scripts", nil)
	identity, err := server.authoringReceiptIdentity(probe, storage.AuthoringPurposeScriptCreate, request.Script.ProjectID, scriptCreateKey, "", authoringReceiptFingerprint{request: request})
	if err != nil {
		t.Fatal(err)
	}
	identity.ResourceType, identity.OwnerToken, identity.OutcomeVersion = "script", "older-controller", storage.AuthoringReceiptOutcomeOriginal
	claim, err := store.ClaimAuthoringMutationReceipt(identity.AuthoringMutationReceiptClaimInput, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	created := request.Script
	created.ID = identity.ResourceID
	if _, err := server.runner.CreateScriptAuthoringDurable(&created); err != nil {
		t.Fatal(err)
	}
	if done, err := store.CompleteAuthoringMutationReceipt(claim.Receipt.ID, "older-controller", "", time.Now().UTC()); err != nil || !done {
		t.Fatalf("legacy completion: %v %v", done, err)
	}
	retry := receiptCreateRequest(t, server, "/api/v1/scripts", body, scriptCreateKey, "2")
	var replayed StoredScript
	if err := json.Unmarshal(retry.Body.Bytes(), &replayed); err != nil {
		t.Fatal(err)
	}
	if retry.Code != http.StatusCreated || retry.Header().Get("Idempotent-Replayed") != "true" || retry.Header().Get(authoringReceiptVersionHeader) != "1" || replayed.ID != identity.ResourceID {
		t.Fatalf("replay of an outcome-less version 2 receipt: %d %v %s", retry.Code, retry.Header(), retry.Body.String())
	}
}

func credentialReceiptFixture(t *testing.T, wrapped bool) (*Server, *storage.SQLiteStore) {
	t.Helper()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	config := ServerConfig{Store: store, ProjectSecretStore: &runtimeProjectionSecretStore{values: map[string]string{}, attemptStore: store}}
	if wrapped {
		wrapper, err := keywrap.NewLocalAESKeyWrapper(keywrap.LocalKeyOptions{Key: bytes.Repeat([]byte("w"), 32), KeyID: "test"})
		if err != nil {
			t.Fatal(err)
		}
		config.BrunoArtifactWrapper = wrapper
	}
	server := NewServer(config)
	server.runner.StoreScript(httpScript("credential-receipt-script", "Credential receipt"))
	return server, store
}

func credentialScenarioRequest(name, token string) CreateScenarioRequest {
	return CreateScenarioRequest{
		Name: name, Duration: "1m",
		RuntimeSettings: &RuntimeSettingsOverride{Env: map[string]string{"API_TOKEN": token}},
		Groups:          []CreateGroupEntry{{ScriptID: "credential-receipt-script", VUsers: 1}},
	}
}

func TestScenarioCreateReceiptFingerprintsCredentialsUnderTheWrappedKey(t *testing.T) {
	const key = "scenario-credential-key-01"
	const secret = "receipt-credential-value"
	server, store := credentialReceiptFixture(t, true)
	request := credentialScenarioRequest("Credential receipt", secret)
	first := receiptCreateRequest(t, server, "/api/v1/scenarios", request, key, "2")
	if first.Code != http.StatusCreated {
		t.Fatalf("keyed credential create: %d %s", first.Code, first.Body.String())
	}
	if retry := receiptCreateRequest(t, server, "/api/v1/scenarios", request, key, "2"); retry.Code != http.StatusCreated || retry.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("retry: %d %v %s", retry.Code, retry.Header(), retry.Body.String())
	}
	if changed := receiptCreateRequest(t, server, "/api/v1/scenarios", credentialScenarioRequest("Credential receipt", "another-value"), key, "2"); changed.Code != http.StatusConflict || idempotencyErrorCode(t, changed) != "idempotency_key_reused" {
		t.Fatalf("a changed credential must not replay: %d %s", changed.Code, changed.Body.String())
	}

	// The credential key is stored only wrapped.
	credentialKey, err := server.authoringReceiptCredentialKey(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	stored, err := store.GetSetting(authoringReceiptCredentialKeySetting)
	if err != nil || stored == "" {
		t.Fatalf("credential key setting: %q %v", stored, err)
	}
	for _, encoding := range []string{base64.RawURLEncoding.EncodeToString(credentialKey), base64.StdEncoding.EncodeToString(credentialKey), hex.EncodeToString(credentialKey)} {
		if strings.Contains(stored, encoding) {
			t.Fatal("the credential key is stored unwrapped")
		}
	}
	// The request digest is not the unwrapped key's digest of the raw request.
	probe := httptest.NewRequest(http.MethodPost, "/api/v1/scenarios", nil)
	raw, err := server.authoringReceiptIdentity(probe, storage.AuthoringPurposeScenarioCreate, "", key, "", authoringReceiptFingerprint{request: request})
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := scenarioCreateReceiptFingerprint(request)
	if err != nil {
		t.Fatal(err)
	}
	current, err := server.authoringReceiptIdentity(probe, storage.AuthoringPurposeScenarioCreate, "", key, "", fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if current.RequestDigest == raw.RequestDigest || current.LegacyRequestDigest != raw.RequestDigest {
		t.Fatal("credential values entered the unwrapped request digest")
	}
	if encoded, _ := json.Marshal(fingerprint.request); strings.Contains(string(encoded), secret) {
		t.Fatalf("the request identity carries the credential value: %s", encoded)
	}
}

func TestScenarioCreateReceiptRecognizesRetriesOfEarlierCredentialDigests(t *testing.T) {
	const key = "scenario-credential-key-02"
	server, store := credentialReceiptFixture(t, true)
	request := credentialScenarioRequest("Earlier digest", "earlier-digest-value")
	// A receipt an earlier controller claimed, digesting the raw request.
	probe := httptest.NewRequest(http.MethodPost, "/api/v1/scenarios", nil)
	identity, err := server.authoringReceiptIdentity(probe, storage.AuthoringPurposeScenarioCreate, "", key, "", authoringReceiptFingerprint{request: request})
	if err != nil {
		t.Fatal(err)
	}
	identity.ResourceType, identity.OwnerToken = "scenario", "earlier-controller"
	claim, err := store.ClaimAuthoringMutationReceipt(identity.AuthoringMutationReceiptClaimInput, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := server.runner.CreateScenarioAuthoringDurable(identity.ResourceID, request); err != nil {
		t.Fatal(err)
	}
	if done, err := store.CompleteAuthoringMutationReceipt(claim.Receipt.ID, "earlier-controller", "", time.Now().UTC()); err != nil || !done {
		t.Fatalf("complete: %v %v", done, err)
	}
	retry := receiptCreateRequest(t, server, "/api/v1/scenarios", request, key, "")
	if retry.Code != http.StatusCreated || retry.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("retry of an earlier-scheme receipt: %d %s", retry.Code, retry.Body.String())
	}
}

func TestScenarioCreateReceiptWithoutKeyWrappingKeepsTheEarlierDigest(t *testing.T) {
	const key = "scenario-credential-key-03"
	server, _ := credentialReceiptFixture(t, false)
	request := credentialScenarioRequest("Unwrapped", "unwrapped-value")
	if first := receiptCreateRequest(t, server, "/api/v1/scenarios", request, key, "2"); first.Code != http.StatusCreated {
		t.Fatalf("keyed credential create without a wrapper: %d %s", first.Code, first.Body.String())
	}
	if retry := receiptCreateRequest(t, server, "/api/v1/scenarios", request, key, "2"); retry.Code != http.StatusCreated || retry.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("retry: %d %s", retry.Code, retry.Body.String())
	}
	if changed := receiptCreateRequest(t, server, "/api/v1/scenarios", credentialScenarioRequest("Unwrapped", "changed-value"), key, "2"); changed.Code != http.StatusConflict || idempotencyErrorCode(t, changed) != "idempotency_key_reused" {
		t.Fatalf("a changed credential must not replay: %d %s", changed.Code, changed.Body.String())
	}
	fingerprint, err := scenarioCreateReceiptFingerprint(request)
	if err != nil {
		t.Fatal(err)
	}
	probe := httptest.NewRequest(http.MethodPost, "/api/v1/scenarios", nil)
	fallback, err := server.authoringReceiptIdentity(probe, storage.AuthoringPurposeScenarioCreate, "", key, "", fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	earlier, err := server.authoringReceiptIdentity(probe, storage.AuthoringPurposeScenarioCreate, "", key, "", authoringReceiptFingerprint{request: request})
	if err != nil || fallback.RequestDigest != earlier.RequestDigest {
		t.Fatalf("without a wrapper the digest is the earlier scheme's: %v", err)
	}
}

func credentialRunRequest(name, token string) CreateTestRequest {
	return CreateTestRequest{
		Name: name, Duration: "1m",
		RuntimeSettings: &RuntimeSettingsOverride{Env: map[string]string{"API_TOKEN": token}},
		Groups:          []CreateGroupEntry{{Name: "Primary", ScriptID: "credential-receipt-script", VUsers: 1}},
	}
}

func TestRunCreateReceiptWithoutKeyWrappingRefusesCredentialValues(t *testing.T) {
	const key = "run-credential-key-000001"
	server, _ := credentialReceiptFixture(t, false)
	refused := receiptCreateRequest(t, server, "/api/v1/tests", credentialRunRequest("Unwrapped run", "unwrapped-run-value"), key, "")
	if refused.Code != http.StatusUnprocessableEntity || idempotencyErrorCode(t, refused) != "idempotency_credentials_unprotected" ||
		!strings.Contains(refused.Body.String(), "secret references") || countTestsNamed(server, "Unwrapped run") != 0 {
		t.Fatalf("a keyed run create carrying a credential without a wrapper: %d %s", refused.Code, refused.Body.String())
	}
	// Nothing was claimed: the key still creates a run that carries none.
	plain := CreateTestRequest{Name: "Plain run", Duration: "1m", Groups: []CreateGroupEntry{{Name: "Primary", ScriptID: "credential-receipt-script", VUsers: 1}}}
	if created := receiptCreateRequest(t, server, "/api/v1/tests", plain, key, ""); created.Code != http.StatusCreated {
		t.Fatalf("the refused key: %d %s", created.Code, created.Body.String())
	}
	// With a wrapper the same request is keyed, its value fingerprinted under
	// the wrapped key.
	wrapped, _ := credentialReceiptFixture(t, true)
	request := credentialRunRequest("Wrapped run", "wrapped-run-value")
	if created := receiptCreateRequest(t, wrapped, "/api/v1/tests", request, key, ""); created.Code != http.StatusCreated {
		t.Fatalf("a keyed run create carrying a credential with a wrapper: %d %s", created.Code, created.Body.String())
	}
	if retry := receiptCreateRequest(t, wrapped, "/api/v1/tests", request, key, ""); retry.Code != http.StatusCreated || retry.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("retry: %d %s", retry.Code, retry.Body.String())
	}
}

// noSwapStore hides the settings compare-and-swap the credential key needs.
type noSwapStore struct{ storage.Store }

func TestReceiptCredentialKeyNeverFallsBackWhenAWrapperCannotStoreIt(t *testing.T) {
	server, store := credentialReceiptFixture(t, true)
	server.store = noSwapStore{store}
	if _, err := server.authoringReceiptCredentialKey(t.Context()); err == nil || errors.Is(err, errAuthoringReceiptCredentialKeyUnavailable) {
		t.Fatalf("a wrapper on a store without compare-and-swap: %v", err)
	}
	probe := httptest.NewRequest(http.MethodPost, "/api/v1/scenarios", nil)
	fingerprint, err := scenarioCreateReceiptFingerprint(credentialScenarioRequest("No swap", "no-swap-value"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.authoringReceiptIdentity(probe, storage.AuthoringPurposeScenarioCreate, "", "scenario-credential-key-04", "", fingerprint); err == nil {
		t.Fatal("the credential values fell back to the unwrapped digest")
	}
}

func TestAuthoringReceiptCrashBoundariesAndKeyRotation(t *testing.T) {
	runAuthoringReceiptCrashBoundariesAndKeyRotation(t, func() storage.Store {
		store, err := storage.NewSQLiteStore(":memory:")
		if err != nil {
			t.Fatal(err)
		}
		return store
	})
}

func runAuthoringReceiptCrashBoundariesAndKeyRotation(t *testing.T, newStore func() storage.Store) {
	t.Helper()
	boundaries := []storage.AuthoringReceiptFaultBoundary{
		storage.AuthoringReceiptFaultPreClaim,
		storage.AuthoringReceiptFaultPostClaimPreResourceInsert,
		storage.AuthoringReceiptFaultPostResourceInsertPreBinding,
		storage.AuthoringReceiptFaultPostCommitDroppedResponse,
	}

	for _, boundary := range boundaries {
		boundary := boundary
		t.Run("crash_boundary_"+string(boundary), func(t *testing.T) {
			store := newStore()
			t.Cleanup(func() { _ = store.Close() })
			wrapperV1, err := keywrap.NewLocalAESKeyWrapper(keywrap.LocalKeyOptions{Key: []byte("0123456789abcdef0123456789abcdef"), KeyID: "test-master-v1"})
			if err != nil {
				t.Fatal(err)
			}
			attemptStore, _ := store.(storage.RuntimeSecretProvisioningAttemptStore)
			server := NewServer(ServerConfig{
				Store:                store,
				ProjectSecretStore:   &runtimeProjectionSecretStore{values: map[string]string{}, attemptStore: attemptStore},
				BrunoArtifactWrapper: wrapperV1,
			})

			// 1. Contract v2 Script Create across crash boundary + edit preservation
			scriptKey := "crash-script-v2-" + string(boundary)
			scriptReq := idempotentScriptBody("Crash Boundary Script " + string(boundary))
			faulted := false
			restore := storage.SetAuthoringReceiptFaultHook(func(b storage.AuthoringReceiptFaultBoundary, purpose, _ string) error {
				if !faulted && b == boundary && purpose == storage.AuthoringPurposeScriptCreate {
					faulted = true
					return errors.New("injected script fault at " + string(boundary))
				}
				return nil
			})
			firstScript := receiptCreateRequest(t, server, "/api/v1/scripts", scriptReq, scriptKey, "2")
			restore()
			if !faulted {
				t.Fatalf("expected fault hook to fire for script create at %s", boundary)
			}
			if firstScript.Code != http.StatusServiceUnavailable {
				t.Fatalf("script faulted attempt status = %d, want 503: %s", firstScript.Code, firstScript.Body.String())
			}

			var editedScriptID string
			if boundary == storage.AuthoringReceiptFaultPostCommitDroppedResponse {
				scripts := server.runner.ListScripts()
				for _, s := range scripts {
					if s.Name == "Crash Boundary Script "+string(boundary) {
						editedScriptID = s.ID
						break
					}
				}
				if editedScriptID == "" {
					t.Fatalf("expected committed script after %s", boundary)
				}
				_, tag := requireAuthoringAPIProjection(t, server, editedScriptID)
				edited := authoringAPIScript(editedScriptID)
				edited.Name = "Edited Script After Commit " + string(boundary)
				putRec := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scripts/"+editedScriptID, map[string]any{"script": edited}, tag)
				if putRec.Code != http.StatusOK {
					t.Fatalf("PUT script after post-commit drop = %d: %s", putRec.Code, putRec.Body.String())
				}
			}

			retryScript := receiptCreateRequest(t, server, "/api/v1/scripts", scriptReq, scriptKey, "2")
			if retryScript.Code != http.StatusCreated {
				t.Fatalf("script retry status = %d, want 201: %s", retryScript.Code, retryScript.Body.String())
			}
			if got := retryScript.Header().Get(authoringReceiptVersionHeader); got != "2" {
				t.Fatalf("script retry receipt version = %q, want 2", got)
			}
			var scriptOutcomeID, scriptOutcomeName string
			if retryScript.Header().Get("Idempotent-Replayed") == "true" {
				scriptOutcome := decodeReceiptOutcome(t, retryScript)
				var scriptProjection ScriptAuthoringProjection
				if err := json.Unmarshal(scriptOutcome.AuthoringProjection, &scriptProjection); err != nil {
					t.Fatal(err)
				}
				scriptOutcomeID = scriptOutcome.ResourceID
				scriptOutcomeName = scriptProjection.Script.Name
			} else {
				var created StoredScript
				if err := json.Unmarshal(retryScript.Body.Bytes(), &created); err != nil {
					t.Fatal(err)
				}
				scriptOutcomeID = created.ID
				scriptOutcomeName = created.Name
			}
			if scriptOutcomeName != "Crash Boundary Script "+string(boundary) {
				t.Fatalf("script outcome name = %q, want original name", scriptOutcomeName)
			}
			if boundary == storage.AuthoringReceiptFaultPostCommitDroppedResponse {
				if retryScript.Header().Get("Idempotent-Replayed") != "true" {
					t.Fatalf("expected Idempotent-Replayed=true after post-commit drop")
				}
				live, found, err := server.runner.GetScriptAuthoringDurable(scriptOutcomeID)
				if err != nil || !found || live == nil {
					t.Fatalf("load live script after replay: found=%v err=%v", found, err)
				}
				if live.Name != "Edited Script After Commit "+string(boundary) {
					t.Fatalf("replay overwrote newer script edit: live name = %q", live.Name)
				}
			}
			matchingScripts := 0
			for _, s := range server.runner.ListScripts() {
				if s.ID == scriptOutcomeID || strings.HasSuffix(s.Name, string(boundary)) {
					matchingScripts++
				}
			}
			if matchingScripts != 1 {
				t.Fatalf("expected exactly 1 script for boundary %s, got %d", boundary, matchingScripts)
			}

			// 2. Contract v2 Scenario Create (with runtime secret binding)
			scenarioKey := "crash-scenario-v2-" + string(boundary)
			scenarioReq := CreateScenarioRequest{
				Name:     "Crash Boundary Scenario " + string(boundary),
				Duration: "30s",
				RuntimeSettings: &RuntimeSettingsOverride{
					Env: map[string]string{"API_TOKEN": "scenario-secret-" + string(boundary)},
				},
				Groups: []CreateGroupEntry{{
					Name:     "Group1",
					ScriptID: scriptOutcomeID,
					VUsers:   2,
				}},
			}
			faulted = false
			restore = storage.SetAuthoringReceiptFaultHook(func(b storage.AuthoringReceiptFaultBoundary, purpose, _ string) error {
				if !faulted && b == boundary && purpose == storage.AuthoringPurposeScenarioCreate {
					faulted = true
					return errors.New("injected scenario fault at " + string(boundary))
				}
				return nil
			})
			firstScenario := receiptCreateRequest(t, server, "/api/v1/scenarios", scenarioReq, scenarioKey, "2")
			restore()
			if !faulted {
				t.Fatalf("expected fault hook to fire for scenario create at %s", boundary)
			}
			if firstScenario.Code != http.StatusServiceUnavailable {
				t.Fatalf("scenario faulted attempt status = %d, want 503: %s", firstScenario.Code, firstScenario.Body.String())
			}

			if boundary == storage.AuthoringReceiptFaultPostCommitDroppedResponse {
				var committedID string
				for _, sc := range server.runner.ListScenarios() {
					if sc.Name == "Crash Boundary Scenario "+string(boundary) {
						committedID = sc.ID
						break
					}
				}
				if committedID == "" {
					t.Fatalf("expected committed scenario after %s", boundary)
				}
				getRec := authoringAPIRequest(t, server, http.MethodGet, "/api/v1/scenarios/"+committedID, nil, "")
				if getRec.Code != http.StatusOK || getRec.Header().Get("ETag") == "" {
					t.Fatalf("GET scenario after post-commit drop = %d (ETag=%q): %s", getRec.Code, getRec.Header().Get("ETag"), getRec.Body.String())
				}
				editedScenarioReq := CreateScenarioRequest{
					Name:     "Edited Scenario After Commit " + string(boundary),
					Duration: "45s",
					Groups: []CreateGroupEntry{{
						Name:     "Group1",
						ScriptID: scriptOutcomeID,
						VUsers:   4,
					}},
				}
				putRec := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scenarios/"+committedID, editedScenarioReq, getRec.Header().Get("ETag"))
				if putRec.Code != http.StatusOK {
					t.Fatalf("PUT scenario after post-commit drop = %d: %s", putRec.Code, putRec.Body.String())
				}
			}

			retryScenario := receiptCreateRequest(t, server, "/api/v1/scenarios", scenarioReq, scenarioKey, "2")
			if retryScenario.Code != http.StatusCreated {
				t.Fatalf("scenario retry status = %d, want 201: %s", retryScenario.Code, retryScenario.Body.String())
			}
			if got := retryScenario.Header().Get(authoringReceiptVersionHeader); got != "2" {
				t.Fatalf("scenario retry receipt version = %q, want 2", got)
			}
			var scenarioOutcomeID, scenarioOutcomeName string
			if retryScenario.Header().Get("Idempotent-Replayed") == "true" {
				scenarioOutcome := decodeReceiptOutcome(t, retryScenario)
				var scenarioProjection ScenarioAuthoringProjection
				if err := json.Unmarshal(scenarioOutcome.AuthoringProjection, &scenarioProjection); err != nil {
					t.Fatal(err)
				}
				scenarioOutcomeID = scenarioOutcome.ResourceID
				scenarioOutcomeName = scenarioProjection.Scenario.Name
			} else {
				var created StoredScenario
				if err := json.Unmarshal(retryScenario.Body.Bytes(), &created); err != nil {
					t.Fatal(err)
				}
				scenarioOutcomeID = created.ID
				scenarioOutcomeName = created.Name
			}
			if scenarioOutcomeName != "Crash Boundary Scenario "+string(boundary) {
				t.Fatalf("scenario outcome name = %q, want original name", scenarioOutcomeName)
			}
			if boundary == storage.AuthoringReceiptFaultPostCommitDroppedResponse {
				if retryScenario.Header().Get("Idempotent-Replayed") != "true" {
					t.Fatalf("expected Idempotent-Replayed=true for scenario after post-commit drop")
				}
				liveScenario, found := server.runner.GetScenario(scenarioOutcomeID)
				if !found || liveScenario == nil {
					t.Fatalf("expected live scenario %s", scenarioOutcomeID)
				}
				if liveScenario.Name != "Edited Scenario After Commit "+string(boundary) || liveScenario.Duration != "45s" {
					t.Fatalf("replay overwrote newer scenario edit: got name=%q duration=%q", liveScenario.Name, liveScenario.Duration)
				}
			}
			if got := len(server.runner.ListScenarios()); got != 1 {
				t.Fatalf("expected 1 scenario in store for boundary %s, got %d", boundary, got)
			}

			// 3. Contract v1 Queued Revise-Copy Run Create across crash boundary
			runKey := "crash-run-v1-" + string(boundary)
			runReq := CreateTestRequest{
				Name:     "Crash Boundary Run " + string(boundary),
				Duration: "15s",
				RuntimeSettings: &RuntimeSettingsOverride{
					Env: map[string]string{"API_TOKEN": "run-secret-" + string(boundary)},
				},
				Groups: []CreateGroupEntry{{
					Name:     "RunGroup",
					ScriptID: scriptOutcomeID,
					VUsers:   1,
				}},
			}
			faulted = false
			restore = storage.SetAuthoringReceiptFaultHook(func(b storage.AuthoringReceiptFaultBoundary, purpose, _ string) error {
				if !faulted && b == boundary && purpose == storage.AuthoringPurposeTestCreate {
					faulted = true
					return errors.New("injected test fault at " + string(boundary))
				}
				return nil
			})
			firstRun := receiptCreateRequest(t, server, "/api/v1/tests", runReq, runKey, "1")
			restore()
			if !faulted {
				t.Fatalf("expected fault hook to fire for test create at %s", boundary)
			}
			if firstRun.Code != http.StatusServiceUnavailable {
				t.Fatalf("test faulted attempt status = %d, want 503: %s", firstRun.Code, firstRun.Body.String())
			}
			retryRun := receiptCreateRequest(t, server, "/api/v1/tests", runReq, runKey, "1")
			if retryRun.Code != http.StatusCreated {
				t.Fatalf("test retry status = %d, want 201: %s", retryRun.Code, retryRun.Body.String())
			}
			if got := retryRun.Header().Get(authoringReceiptVersionHeader); got != "1" {
				t.Fatalf("test retry receipt version = %q, want 1", got)
			}
			if boundary == storage.AuthoringReceiptFaultPostResourceInsertPreBinding || boundary == storage.AuthoringReceiptFaultPostCommitDroppedResponse {
				if retryRun.Header().Get("Idempotent-Replayed") != "true" {
					t.Fatalf("expected Idempotent-Replayed=true for test retry at %s", boundary)
				}
			}
			tests, total := server.runner.listTestSummaryPage(testSummaryListQuery{limit: 50})
			if total != 1 || len(tests) != 1 {
				t.Fatalf("expected 1 test for boundary %s, got total=%d len=%d", boundary, total, len(tests))
			}

			// 4. Authoring Session Approval across crash boundary
			sessionDraft := httpScript("", "Approved Session Script "+string(boundary))
			sessionReqBody, _ := json.Marshal(map[string]any{
				"source": "openapi",
				"draft":  sessionDraft,
			})
			createSessReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/authoring/sessions", bytes.NewReader(sessionReqBody))
			createSessReq.Header.Set("Content-Type", "application/json")
			createSessRec := httptest.NewRecorder()
			server.mux.ServeHTTP(createSessRec, createSessReq)
			if createSessRec.Code != http.StatusCreated {
				t.Fatalf("create authoring session = %d: %s", createSessRec.Code, createSessRec.Body.String())
			}
			var createdSess storage.AuthoringSession
			if err := json.Unmarshal(createSessRec.Body.Bytes(), &createdSess); err != nil {
				t.Fatal(err)
			}

			faulted = false
			restore = storage.SetAuthoringReceiptFaultHook(func(b storage.AuthoringReceiptFaultBoundary, purpose, _ string) error {
				if !faulted && b == boundary && purpose == "authoring_session_approve" {
					faulted = true
					return errors.New("injected session approval fault at " + string(boundary))
				}
				return nil
			})
			approveReq1 := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/authoring/sessions/"+createdSess.ID+"/approve", nil)
			approveRec1 := httptest.NewRecorder()
			server.mux.ServeHTTP(approveRec1, approveReq1)
			restore()
			if !faulted {
				t.Fatalf("expected fault hook to fire for session approval at %s", boundary)
			}
			if approveRec1.Code != http.StatusServiceUnavailable {
				t.Fatalf("session approval faulted status = %d, want 503: %s", approveRec1.Code, approveRec1.Body.String())
			}

			approveReq2 := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/authoring/sessions/"+createdSess.ID+"/approve", nil)
			approveRec2 := httptest.NewRecorder()
			server.mux.ServeHTTP(approveRec2, approveReq2)
			if approveRec2.Code != http.StatusOK {
				t.Fatalf("session approval retry status = %d, want 200: %s", approveRec2.Code, approveRec2.Body.String())
			}
			var approvalResp authoringSessionApprovalResponse
			if err := json.Unmarshal(approveRec2.Body.Bytes(), &approvalResp); err != nil {
				t.Fatal(err)
			}
			if approvalResp.Session == nil || approvalResp.Session.Status != "approved" || approvalResp.Script == nil {
				t.Fatalf("unexpected approval response: %+v", approvalResp)
			}
			listSessRec := authoringAPIRequest(t, server, http.MethodGet, "/api/v1/scripts/authoring/sessions", nil, "")
			if listSessRec.Code != http.StatusOK {
				t.Fatalf("GET /api/v1/scripts/authoring/sessions = %d: %s", listSessRec.Code, listSessRec.Body.String())
			}
			var listedSessions struct {
				Sessions []storage.AuthoringSession `json:"sessions"`
			}
			if err := json.Unmarshal(listSessRec.Body.Bytes(), &listedSessions); err != nil || len(listedSessions.Sessions) != 1 {
				t.Fatalf("expected 1 authoring session for boundary %s, got %d (err=%v)", boundary, len(listedSessions.Sessions), err)
			}
			if scripts := server.runner.ListScripts(); len(scripts) != 2 {
				t.Fatalf("expected 2 total scripts (1 direct + 1 approved session) for boundary %s, got %d", boundary, len(scripts))
			}
		})
	}

	t.Run("keywrap_master_key_rotation_preserves_credential_receipt_replay", func(t *testing.T) {
		store := newStore()
		t.Cleanup(func() { _ = store.Close() })
		wrapperV1, err := keywrap.NewLocalAESKeyWrapper(keywrap.LocalKeyOptions{Key: []byte("0123456789abcdef0123456789abcdef"), KeyID: "master-key-v1"})
		if err != nil {
			t.Fatal(err)
		}
		wrapperV2, err := keywrap.NewLocalAESKeyWrapper(keywrap.LocalKeyOptions{Key: []byte("fedcba9876543210fedcba9876543210"), KeyID: "master-key-v2"})
		if err != nil {
			t.Fatal(err)
		}
		attemptStore, _ := store.(storage.RuntimeSecretProvisioningAttemptStore)
		sharedSecrets := &runtimeProjectionSecretStore{values: map[string]string{}, attemptStore: attemptStore}
		server := NewServer(ServerConfig{
			Store:                store,
			ProjectSecretStore:   sharedSecrets,
			BrunoArtifactWrapper: wrapperV1,
		})
		if _, err := server.runner.CreateScriptDurable(httpScript("rotation-script", "Rotation Script")); err != nil {
			t.Fatal(err)
		}

		scenarioKey := "rotation-scenario-key-01"
		scenarioReq := CreateScenarioRequest{
			Name:     "Rotation Scenario",
			Duration: "30s",
			RuntimeSettings: &RuntimeSettingsOverride{
				Env: map[string]string{"API_TOKEN": "secret-token-before-rotation"},
			},
			Groups: []CreateGroupEntry{{Name: "Group", ScriptID: "rotation-script", VUsers: 1}},
		}
		createdScenario := receiptCreateRequest(t, server, "/api/v1/scenarios", scenarioReq, scenarioKey, "2")
		if createdScenario.Code != http.StatusCreated {
			t.Fatalf("create credential scenario under v1 key = %d: %s", createdScenario.Code, createdScenario.Body.String())
		}

		runKey := "rotation-run-key-01"
		runReq := CreateTestRequest{
			Name:     "Rotation Run",
			Duration: "15s",
			RuntimeSettings: &RuntimeSettingsOverride{
				Env: map[string]string{"API_TOKEN": "run-token-before-rotation"},
			},
			Groups: []CreateGroupEntry{{Name: "Group", ScriptID: "rotation-script", VUsers: 1}},
		}
		createdRun := receiptCreateRequest(t, server, "/api/v1/tests", runReq, runKey, "1")
		if createdRun.Code != http.StatusCreated {
			t.Fatalf("create credential run under v1 key = %d: %s", createdRun.Code, createdRun.Body.String())
		}

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			for range 8 {
				_, _ = server.authoringReceiptCredentialKey(t.Context())
				_ = server.clusterSecretWrapper
			}
		}()
		go func() {
			defer wg.Done()
			if err := server.RotateAuthoringReceiptCredentialKey(t.Context(), wrapperV2); err != nil {
				t.Errorf("RotateAuthoringReceiptCredentialKey: %v", err)
			}
		}()
		wg.Wait()
		if server.clusterSecretWrapper != wrapperV1 {
			t.Fatalf("RotateAuthoringReceiptCredentialKey mutated clusterSecretWrapper: got %#v, want wrapperV1", server.clusterSecretWrapper)
		}
		if replayOnLive := receiptCreateRequest(t, server, "/api/v1/scenarios", scenarioReq, scenarioKey, "2"); replayOnLive.Code != http.StatusCreated || replayOnLive.Header().Get("Idempotent-Replayed") != "true" {
			t.Fatalf("replay scenario on rotated live server = %d: %s", replayOnLive.Code, replayOnLive.Body.String())
		}

		// Replay against a fresh server instance configured only with wrapperV2.
		rotatedServer := NewServer(ServerConfig{
			Store:                store,
			ProjectSecretStore:   sharedSecrets,
			BrunoArtifactWrapper: wrapperV2,
		})

		replayScenario := receiptCreateRequest(t, rotatedServer, "/api/v1/scenarios", scenarioReq, scenarioKey, "2")
		if replayScenario.Code != http.StatusCreated || replayScenario.Header().Get("Idempotent-Replayed") != "true" {
			t.Fatalf("replay scenario after key rotation = %d (replayed=%q): %s",
				replayScenario.Code, replayScenario.Header().Get("Idempotent-Replayed"), replayScenario.Body.String())
		}
		changedScenarioReq := scenarioReq
		changedScenarioReq.RuntimeSettings = &RuntimeSettingsOverride{
			Env: map[string]string{"API_TOKEN": "different-secret-token"},
		}
		conflictScenario := receiptCreateRequest(t, rotatedServer, "/api/v1/scenarios", changedScenarioReq, scenarioKey, "2")
		if conflictScenario.Code != http.StatusConflict {
			t.Fatalf("changed credential scenario after key rotation = %d, want 409: %s", conflictScenario.Code, conflictScenario.Body.String())
		}

		replayRun := receiptCreateRequest(t, rotatedServer, "/api/v1/tests", runReq, runKey, "1")
		if replayRun.Code != http.StatusCreated || replayRun.Header().Get("Idempotent-Replayed") != "true" {
			t.Fatalf("replay run after key rotation = %d (replayed=%q): %s",
				replayRun.Code, replayRun.Header().Get("Idempotent-Replayed"), replayRun.Body.String())
		}
		changedRunReq := runReq
		changedRunReq.RuntimeSettings = &RuntimeSettingsOverride{
			Env: map[string]string{"API_TOKEN": "different-run-token"},
		}
		conflictRun := receiptCreateRequest(t, rotatedServer, "/api/v1/tests", changedRunReq, runKey, "1")
		if conflictRun.Code != http.StatusConflict {
			t.Fatalf("changed credential run after key rotation = %d, want 409: %s", conflictRun.Code, conflictRun.Body.String())
		}
	})
}
