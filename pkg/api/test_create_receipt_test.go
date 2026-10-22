package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
	"gopkg.in/yaml.v3"
)

func testCreateReceiptFixture(t *testing.T) (*Server, *storage.SQLiteStore) {
	t.Helper()
	server, store := newRuntimeOverrideTestServer(t)
	sqlite, ok := store.(*storage.SQLiteStore)
	if !ok {
		t.Fatalf("store is %T", store)
	}
	if _, err := server.runner.StoreScriptDurable(httpScript("receipt-run-script", "Receipt run")); err != nil {
		t.Fatal(err)
	}
	return server, sqlite
}

func receiptRunRequest(name string) CreateTestRequest {
	return CreateTestRequest{Name: name, Duration: "1m", Groups: []CreateGroupEntry{{Name: "Primary", ScriptID: "receipt-run-script", VUsers: 1}}}
}

func countTestsNamed(server *Server, name string) int {
	count := 0
	for _, test := range server.runner.ListTests() {
		if test != nil && test.Name == name {
			count++
		}
	}
	return count
}

func decodeCreatedRun(t *testing.T, response *httptest.ResponseRecorder) ManagedTest {
	t.Helper()
	var run ManagedTest
	if err := json.Unmarshal(response.Body.Bytes(), &run); err != nil {
		t.Fatalf("decode run: %v (%s)", err, response.Body.String())
	}
	return run
}

const runCreateKey = "run-create-key-000001"

func TestKeyedRunCreateReplaysTheRunItCreated(t *testing.T) {
	server, _ := testCreateReceiptFixture(t)
	request := receiptRunRequest("Keyed run")
	first := receiptCreateRequest(t, server, "/api/v1/tests", request, runCreateKey, "")
	if first.Code != http.StatusCreated || first.Header().Get("Idempotent-Replayed") != "" || first.Header().Get(authoringReceiptVersionHeader) != "1" {
		t.Fatalf("first create: %d %v %s", first.Code, first.Header(), first.Body.String())
	}
	created := decodeCreatedRun(t, first)
	retry := receiptCreateRequest(t, server, "/api/v1/tests", request, runCreateKey, "1")
	if retry.Code != http.StatusCreated || retry.Header().Get("Idempotent-Replayed") != "true" || decodeCreatedRun(t, retry).ID != created.ID {
		t.Fatalf("retry: %d %v %s", retry.Code, retry.Header(), retry.Body.String())
	}
	if countTestsNamed(server, "Keyed run") != 1 {
		t.Fatal("a retry created another run")
	}
	if changed := receiptCreateRequest(t, server, "/api/v1/tests", receiptRunRequest("Changed run"), runCreateKey, ""); changed.Code != http.StatusConflict || idempotencyErrorCode(t, changed) != "idempotency_key_reused" || countTestsNamed(server, "Changed run") != 0 {
		t.Fatalf("changed request: %d %s", changed.Code, changed.Body.String())
	}
	if v2 := receiptCreateRequest(t, server, "/api/v1/tests", request, "run-create-key-000002", "2"); v2.Code != http.StatusBadRequest || idempotencyErrorCode(t, v2) != "idempotency_receipt_version_unsupported" {
		t.Fatalf("contract 2 on a run create: %d %s", v2.Code, v2.Body.String())
	}
	if unkeyed := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/tests", receiptRunRequest("Unkeyed run"), ""); unkeyed.Code != http.StatusCreated || unkeyed.Header().Get(authoringReceiptVersionHeader) != "" {
		t.Fatalf("unkeyed create: %d %v %s", unkeyed.Code, unkeyed.Header(), unkeyed.Body.String())
	}
}

func TestKeyedRunCreateNeverRecreatesADeletedRun(t *testing.T) {
	server, _ := testCreateReceiptFixture(t)
	request := receiptRunRequest("Deleted run")
	first := receiptCreateRequest(t, server, "/api/v1/tests", request, runCreateKey, "")
	if first.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", first.Code, first.Body.String())
	}
	created := decodeCreatedRun(t, first)
	if response := authoringAPIRequest(t, server, http.MethodDelete, "/api/v1/tests/"+created.ID, nil, ""); response.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", response.Code, response.Body.String())
	}
	retry := receiptCreateRequest(t, server, "/api/v1/tests", request, runCreateKey, "")
	if retry.Code != http.StatusGone || idempotencyErrorCode(t, retry) != "idempotency_resource_deleted" || countTestsNamed(server, "Deleted run") != 0 {
		t.Fatalf("retry after delete: %d %s", retry.Code, retry.Body.String())
	}
}

func TestKeyedRunCreateAdoptsAnUnconfirmedEarlierCreate(t *testing.T) {
	server, store := testCreateReceiptFixture(t)
	request := receiptRunRequest("Lost run response")
	fingerprint, err := testCreateReceiptFingerprint(request)
	if err != nil {
		t.Fatal(err)
	}
	// An attempt that reserved the key and created the run, then crashed
	// before completing the receipt or responding.
	probe := httptest.NewRequest(http.MethodPost, "/api/v1/tests", nil)
	identity, err := server.authoringReceiptIdentity(probe, storage.AuthoringPurposeTestCreate, storage.DefaultProjectID, runCreateKey, "", fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	identity.ResourceType, identity.OwnerToken = "test", "crashed-owner"
	if _, err := store.ClaimAuthoringMutationReceipt(identity.AuthoringMutationReceiptClaimInput, time.Now().UTC(), time.Millisecond); err != nil {
		t.Fatal(err)
	}
	crashed := request
	crashed.RequestedTestID = identity.ResourceID
	if _, err := server.runner.CreateTest(crashed); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	retry := receiptCreateRequest(t, server, "/api/v1/tests", request, runCreateKey, "")
	if retry.Code != http.StatusCreated || retry.Header().Get("Idempotent-Replayed") != "true" || decodeCreatedRun(t, retry).ID != identity.ResourceID {
		t.Fatalf("retry after a lost response: %d %v %s", retry.Code, retry.Header(), retry.Body.String())
	}
	if countTestsNamed(server, "Lost run response") != 1 {
		t.Fatal("the lost run was duplicated")
	}
	again := receiptCreateRequest(t, server, "/api/v1/tests", request, runCreateKey, "")
	if again.Code != http.StatusCreated || again.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("adoption did not complete the receipt: %d %s", again.Code, again.Body.String())
	}
}

func TestKeyedQueuedRevisionCopyCreatesOneCopyAndLeavesTheSourceUnchanged(t *testing.T) {
	server, _ := testCreateReceiptFixture(t)
	source, err := server.runner.CreateTest(receiptRunRequest("Queued source"))
	if err != nil {
		t.Fatal(err)
	}
	if source.Status != "QUEUED" {
		t.Fatalf("source status %s", source.Status)
	}
	sourceIndex := 0
	revision := CreateTestRequest{
		Name: "Revised copy", Duration: "2m", RevisionSourceTestID: source.ID,
		Groups: []CreateGroupEntry{{Name: "Primary", ScriptID: "receipt-run-script", VUsers: 2, RevisionSourceGroupIndex: &sourceIndex}},
	}
	first := receiptCreateRequest(t, server, "/api/v1/tests", revision, runCreateKey, "")
	retry := receiptCreateRequest(t, server, "/api/v1/tests", revision, runCreateKey, "")
	if first.Code != http.StatusCreated || retry.Code != http.StatusCreated || retry.Header().Get("Idempotent-Replayed") != "true" || decodeCreatedRun(t, first).ID != decodeCreatedRun(t, retry).ID {
		t.Fatalf("revised copy: %d %d %s", first.Code, retry.Code, retry.Body.String())
	}
	if countTestsNamed(server, "Revised copy") != 1 {
		t.Fatal("the revised copy was duplicated")
	}
	after, found := server.runner.GetTest(source.ID)
	if !found || after.Status != "QUEUED" || after.Duration != source.Duration || after.Name != "Queued source" {
		t.Fatalf("the source run changed: %+v", after)
	}
}

func TestKeyedRunCreateReplaysUnderRealAuthentication(t *testing.T) {
	server, _ := testCreateReceiptFixture(t)
	enableTrustedHeaderAuth(t, server)
	body, err := json.Marshal(receiptRunRequest("Authenticated keyed run"))
	if err != nil {
		t.Fatal(err)
	}
	send := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/tests", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", runCreateKey)
		req.Header.Set("X-Auth-Request-User", "editor-user")
		req.Header.Set("X-APEXLoad-Project-Roles", storage.DefaultProjectID+"="+RoleEditor)
		response := httptest.NewRecorder()
		server.mux.ServeHTTP(response, req)
		return response
	}
	first := send()
	retry := send()
	// The generic duplicate-request fence must not intercept the receipt.
	if first.Code != http.StatusCreated || retry.Code != http.StatusCreated || retry.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("keyed run create under authentication: %d then %d %s", first.Code, retry.Code, retry.Body.String())
	}
	if countTestsNamed(server, "Authenticated keyed run") != 1 {
		t.Fatal("an authenticated retry created another run")
	}
}

func TestKeyedRunCreateRefusesACallerChosenIDThatIsInUse(t *testing.T) {
	server, _ := testCreateReceiptFixture(t)
	existing := receiptRunRequest("Existing run")
	existing.RequestedTestID = "6f1c8a1e-3b8f-4c37-9a51-2f4f7d4c9a11"
	if _, err := server.runner.CreateTest(existing); err != nil {
		t.Fatal(err)
	}
	request := receiptRunRequest("Caller chosen ID")
	request.RequestedTestID = existing.RequestedTestID
	response := receiptCreateRequest(t, server, "/api/v1/tests", request, runCreateKey, "")
	if response.Code != http.StatusConflict || idempotencyErrorCode(t, response) != "test_identity_in_use" || response.Header().Get("Idempotent-Replayed") != "" {
		t.Fatalf("caller-chosen ID in use: %d %s", response.Code, response.Body.String())
	}
}

// lostRunCommitStore commits a run and then reports a failure, with reads
// failing too: the create's outcome cannot be confirmed.
type lostRunCommitStore struct {
	*storage.SQLiteStore
	broken bool
}

func (s *lostRunCommitStore) CreateTestIfAbsent(test *storage.TestRecord) (bool, error) {
	if _, err := s.SQLiteStore.CreateTestIfAbsent(test); err != nil {
		return false, err
	}
	s.broken = true
	return false, errors.New("connection reset after commit")
}

func (s *lostRunCommitStore) GetTest(id string) (*storage.TestRecord, error) {
	if s.broken {
		return nil, errors.New("connection reset")
	}
	return s.SQLiteStore.GetTest(id)
}

func TestKeyedRunCreateWithAnUnconfirmedCommitAnswersUnknownAndRetriesToTheSameRun(t *testing.T) {
	server, store := testCreateReceiptFixture(t)
	request := receiptRunRequest("Unconfirmed run")
	lost := &lostRunCommitStore{SQLiteStore: store}
	server.runner.testStore = lost
	first := receiptCreateRequest(t, server, "/api/v1/tests", request, runCreateKey, "")
	server.runner.testStore = store
	if first.Code != http.StatusServiceUnavailable || idempotencyErrorCode(t, first) != "test_create_outcome_unknown" {
		t.Fatalf("unconfirmed commit: %d %s", first.Code, first.Body.String())
	}
	retry := receiptCreateRequest(t, server, "/api/v1/tests", request, runCreateKey, "")
	if retry.Code != http.StatusCreated {
		t.Fatalf("retry after an unconfirmed commit: %d %s", retry.Code, retry.Body.String())
	}
	records, err := store.ListTests(storage.DefaultProjectID, nil)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, record := range records {
		if record.Name == "Unconfirmed run" {
			count++
		}
	}
	if count != 1 || decodeCreatedRun(t, retry).Name != "Unconfirmed run" {
		t.Fatalf("the unconfirmed run was duplicated or lost: %d", count)
	}
}

func TestKeyedRunCreateKeepsARefusalWhenACallerChosenIDIsInUse(t *testing.T) {
	server, _ := testCreateReceiptFixture(t)
	existing := receiptRunRequest("Unrelated run")
	existing.RequestedTestID = "0b7c2f5e-6a39-4f0e-8d1c-5d2e9b7a4c10"
	if _, err := server.runner.CreateTest(existing); err != nil {
		t.Fatal(err)
	}
	invalid := receiptRunRequest("Invalid request")
	invalid.RequestedTestID = existing.RequestedTestID
	invalid.Duration = "bogus"
	for attempt := 0; attempt < 2; attempt++ {
		response := receiptCreateRequest(t, server, "/api/v1/tests", invalid, runCreateKey, "")
		if response.Code != http.StatusBadRequest || idempotencyErrorCode(t, response) == "test_create_outcome_unknown" {
			t.Fatalf("attempt %d: a refusal with an unrelated run at the chosen ID: %d %s", attempt, response.Code, response.Body.String())
		}
	}
}

func TestKeyedRunCreateOpenAPIContract(t *testing.T) {
	data, err := GenerateOpenAPIYAML("server.go")
	if err != nil {
		t.Fatal(err)
	}
	var doc oasDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	operation := doc.Paths["/api/v1/tests"]["post"]
	if !operationHasParameter(operation, "header", "Idempotency-Key") || !operationHasParameter(operation, "header", authoringReceiptVersionHeader) {
		t.Fatal("POST /api/v1/tests does not document its retry key and contract")
	}
	if _, ok := operation.Responses["201"].Headers["Idempotent-Replayed"]; !ok {
		t.Fatal("POST /api/v1/tests does not document replays")
	}
	if len(operation.Responses["201"].Content["application/json"].Schema.OneOf) != 0 {
		t.Fatal("run creates have no original-outcome replay")
	}
}
