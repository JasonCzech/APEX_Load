package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/dsl"
	recbrowser "github.com/jasonczech/apexload/pkg/recorder/browser"
	"github.com/jasonczech/apexload/pkg/storage"
)

// mutateJMeterScriptBeforeScenarioFailureStore emulates a peer controller or
// editor committing an update after the first imported script is created but
// before the later scenario write fails.
type mutateJMeterScriptBeforeScenarioFailureStore struct {
	*libraryAtomicForwardingStore
	base *storage.SQLiteStore

	mu       sync.Mutex
	scriptID string
}

func (s *mutateJMeterScriptBeforeScenarioFailureStore) CreateScriptIfAbsent(script *dsl.Script) (bool, error) {
	created, err := s.libraryAtomicForwardingStore.CreateScriptIfAbsent(script)
	if created && err == nil {
		s.mu.Lock()
		s.scriptID = script.ID
		s.mu.Unlock()
	}
	return created, err
}

func (s *mutateJMeterScriptBeforeScenarioFailureStore) CreateScenarioWithDataPoolBindingsIfAbsent(*storage.Scenario, []storage.ScenarioScript, []storage.ScenarioDataPoolBinding) (bool, error) {
	s.mu.Lock()
	id := s.scriptID
	s.mu.Unlock()
	current, err := s.base.GetScript(id)
	if err != nil {
		return false, err
	}
	current.Name = "Concurrent JMeter Edit"
	current.Actions[0].Target = "/edited-by-peer"
	if err := s.base.SaveScript(current); err != nil {
		return false, err
	}
	return false, errors.New("injected scenario persistence failure")
}

func TestHandleSaveJMeterImportRollbackPreservesConcurrentMutation(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "jmeter-concurrent-rollback.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	store := &mutateJMeterScriptBeforeScenarioFailureStore{
		libraryAtomicForwardingStore: &libraryAtomicForwardingStore{Store: base},
		base:                         base,
	}
	server := NewServer(ServerConfig{Store: store})
	payload := map[string]any{
		"scripts": []map[string]any{{
			"name": "Scenario_Script", "version": "1.0.0", "protocol": "http",
			"config":  map[string]any{"base_url": "https://api.example.com"},
			"actions": []map[string]any{{"name": "Home", "type": "request", "method": "GET", "target": "/"}},
		}},
		"scenario_draft": map[string]any{
			"name": "Scenario", "duration": "1m", "scenario_mode": "vuser_driven",
			"groups": []map[string]any{{"name": "Users", "script_name": "Scenario_Script", "vusers": 1}},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/jmeter/save", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", response.Code, http.StatusBadRequest, response.Body.String())
	}

	store.mu.Lock()
	scriptID := store.scriptID
	store.mu.Unlock()
	current, err := base.GetScript(scriptID)
	if err != nil {
		t.Fatalf("concurrently edited script was deleted: %v", err)
	}
	if current.Name != "Concurrent JMeter Edit" || len(current.Actions) == 0 || current.Actions[0].Target != "/edited-by-peer" {
		t.Fatalf("durable script = %#v, want concurrent edit preserved", current)
	}
	claims, err := server.loadLibraryImportClaims()
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 0 {
		t.Fatalf("abandoned JMeter transaction claims were not reconciled: %#v", claims)
	}
}

// mutateBrowserScriptBeforeSecondCreateFailureStore forces the second script
// create in a split recording to fail after a peer has edited the first one.
type mutateBrowserScriptBeforeSecondCreateFailureStore struct {
	*libraryAtomicForwardingStore
	base *storage.SQLiteStore

	mu          sync.Mutex
	createCount int
	firstID     string
}

func (s *mutateBrowserScriptBeforeSecondCreateFailureStore) CreateScriptIfAbsent(script *dsl.Script) (bool, error) {
	s.mu.Lock()
	s.createCount++
	createNumber := s.createCount
	firstID := s.firstID
	s.mu.Unlock()
	if createNumber == 2 {
		current, err := s.base.GetScript(firstID)
		if err != nil {
			return false, err
		}
		current.Name = "Concurrent Browser Edit"
		current.Actions[0].Name = "peer-owned-action"
		if err := s.base.SaveScript(current); err != nil {
			return false, err
		}
		return false, errors.New("injected second browser script failure")
	}
	created, err := s.libraryAtomicForwardingStore.CreateScriptIfAbsent(script)
	if created && err == nil && createNumber == 1 {
		s.mu.Lock()
		s.firstID = script.ID
		s.mu.Unlock()
	}
	return created, err
}

func TestHandleConvertBrowserRecordingRollbackPreservesConcurrentMutation(t *testing.T) {
	stubBrowserRecordingDependencyProbe(t, nil)
	if runtime.GOOS == "linux" {
		t.Setenv("DISPLAY", ":0")
	}
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "browser-concurrent-rollback.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	now := time.Now().UTC()
	if err := base.SaveProject(&storage.Project{ID: "proj-1", Name: "Project 1", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	store := &mutateBrowserScriptBeforeSecondCreateFailureStore{
		libraryAtomicForwardingStore: &libraryAtomicForwardingStore{Store: base},
		base:                         base,
	}
	artifact := recordedBrowserArtifact()
	artifact.Steps[0].Masked = false
	artifact.Steps[0].MarkSecret = true
	artifact.Steps[0].Value = "split-rollback-captured-password"
	artifact.Markers = []recbrowser.Marker{
		{Kind: recbrowser.MarkerTransactionStart, Name: "Login", Timestamp: artifact.StartedAt.Add(500 * time.Millisecond)},
		{Kind: recbrowser.MarkerTransactionEnd, Name: "Login", Timestamp: artifact.StartedAt.Add(1500 * time.Millisecond)},
		{Kind: recbrowser.MarkerTransactionStart, Name: "Checkout", Timestamp: artifact.StartedAt.Add(1750 * time.Millisecond)},
		{Kind: recbrowser.MarkerTransactionEnd, Name: "Checkout", Timestamp: artifact.StartedAt.Add(2500 * time.Millisecond)},
	}
	fake := newFakeBrowserRecorder(artifact)
	server := NewServer(ServerConfig{Store: store})
	fake.install(server.browserRecording)
	id := startBrowserRecordingViaAPI(t, server)
	stopBrowserRecordingViaAPI(t, server, id)

	request := httptest.NewRequest(http.MethodPost, "/api/v1/browser-recorder/sessions/"+id+"/convert",
		bytes.NewBufferString(`{"name":"Split Recording","split_at_transactions":true}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d body=%s", response.Code, http.StatusInternalServerError, response.Body.String())
	}

	store.mu.Lock()
	firstID := store.firstID
	store.mu.Unlock()
	current, err := base.GetScript(firstID)
	if err != nil {
		t.Fatalf("concurrently edited browser script was deleted: %v", err)
	}
	if current.Name != "Concurrent Browser Edit" || len(current.Actions) == 0 || current.Actions[0].Name != "peer-owned-action" {
		t.Fatalf("durable script = %#v, want concurrent edit preserved", current)
	}
	currentJSON, err := json.Marshal(current)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(currentJSON, []byte("split-rollback-captured-password")) {
		t.Fatalf("concurrently edited imported script lost captured credential: %s", currentJSON)
	}
	if strings.Contains(response.Body.String(), "split-rollback-captured-password") {
		t.Fatalf("failed atomic import response disclosed captured credential: %s", response.Body.String())
	}
	claims, err := server.loadLibraryImportClaims()
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 0 {
		t.Fatalf("abandoned browser transaction claims were not reconciled: %#v", claims)
	}
}

func TestCreateImportTransactionRecoversScriptAndScenarioAfterCrash(t *testing.T) {
	t.Setenv("APEXLOAD_LIBRARY_IMPORT_CLAIM_TTL", "100ms")
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "direct-import-crash.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	server := NewServer(ServerConfig{Store: base})
	script := libraryTestScript(uuid.NewString(), "Crash Script", storage.DefaultProjectID, "/crash")
	scenarioID := uuid.NewString()
	scenarioRequest := CreateScenarioRequest{
		Name: "Crash Scenario", Duration: "1m", ScenarioMode: "vuser_driven",
		Groups: []CreateGroupEntry{{Name: "Users", ScriptID: script.ID, Role: GroupRoleLoad, VUsers: 1, InitStrategy: "at_runtime"}},
	}
	transaction, err := newCreateImportTransaction(server, "crash-test", []*dsl.Script{script}, scenarioID, &scenarioRequest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.createScript(script); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.createScenario(scenarioID, scenarioRequest); err != nil {
		t.Fatal(err)
	}
	// Simulate abrupt process loss: the heartbeat disappears without a commit or
	// in-process rollback. A replacement controller owns recovery after expiry.
	transaction.claims.stopHeartbeat()
	time.Sleep(150 * time.Millisecond)
	replacement := NewServer(ServerConfig{Store: base})
	if err := replacement.recoverExpiredLibraryImportClaims(); err != nil {
		t.Fatalf("recover partial direct import: %v", err)
	}
	if _, err := base.GetScript(script.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("crash-partial script survived recovery: %v", err)
	}
	if _, _, err := base.GetScenario(scenarioID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("crash-partial scenario survived recovery: %v", err)
	}
	claims, err := replacement.loadLibraryImportClaims()
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 0 {
		t.Fatalf("recovered transaction claims remain: %#v", claims)
	}
}

func TestExpiredDirectImportClaimPreservesCredentialOnlyConcurrentEdit(t *testing.T) {
	t.Setenv("APEXLOAD_LIBRARY_IMPORT_CLAIM_TTL", "100ms")
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "direct-import-credential-edit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	server := NewServer(ServerConfig{Store: base})
	script := libraryTestScript(
		uuid.NewString(),
		"Credential Crash Script",
		storage.DefaultProjectID,
		"/credential",
	)
	script.GeneratedBy = "jmeter-import"
	script.GeneratedFrom = "jmeter"
	script.Metadata = &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
		Source: "jmeter",
		Preserved: map[string]any{
			dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve,
		},
	}}
	script.Config.HTTP.Auth = dsl.AuthConfig{
		Type: "basic", Username: "operator", Password: "original-import-password",
	}
	transaction, err := newCreateImportTransaction(
		server,
		"credential-crash-test",
		[]*dsl.Script{script},
		"",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.createImportedScript(script, "jmeter"); err != nil {
		t.Fatal(err)
	}

	edited, err := base.GetScript(script.ID)
	if err != nil {
		t.Fatal(err)
	}
	edited.Config.HTTP.Auth.Password = "peer-edited-import-password"
	if err := base.SaveScript(edited); err != nil {
		t.Fatal(err)
	}

	// Simulate process loss before the transaction claim is committed. The
	// replacement controller may clean the exact partial create, but a
	// credential-only peer edit is a different asset and must survive.
	transaction.claims.stopHeartbeat()
	time.Sleep(150 * time.Millisecond)
	replacement := NewServer(ServerConfig{Store: base})
	if err := replacement.recoverExpiredLibraryImportClaims(); err != nil {
		t.Fatalf("recover direct import with credential-only edit: %v", err)
	}
	preserved, err := base.GetScript(script.ID)
	if err != nil {
		t.Fatalf("credential-only concurrent edit was deleted: %v", err)
	}
	if preserved.Config.HTTP.Auth.Password != "peer-edited-import-password" {
		t.Fatalf(
			"credential-only concurrent edit changed during recovery: %q",
			preserved.Config.HTTP.Auth.Password,
		)
	}
	claims, err := replacement.loadLibraryImportClaims()
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 0 {
		t.Fatalf("recovered direct-import claims remain: %#v", claims)
	}
}

func TestStaleDirectReceiptRetryCannotBeDeletedByOldClaimRecovery(t *testing.T) {
	t.Setenv("APEXLOAD_LIBRARY_IMPORT_CLAIM_TTL", "100ms")
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "direct-import-stale-retry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	firstServer := NewServer(ServerConfig{Store: base})
	prepared := libraryTestScript(
		"",
		"Stale Retry Script",
		storage.DefaultProjectID,
		"/stale-retry",
	)
	prepared.GeneratedBy = "jmeter-import"
	prepared.GeneratedFrom = "jmeter"
	prepared.Metadata = &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
		Source: "jmeter",
		Preserved: map[string]any{
			dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve,
		},
	}}
	prepared.Config.HTTP.Auth = dsl.AuthConfig{
		Type: "basic", Username: "operator", Password: "stale-retry-password",
	}
	requestDigest, err := firstServer.directImportScriptsRequestDigest(
		[]*dsl.Script{prepared},
		map[string]any{"profile": "jmeter"},
	)
	if err != nil {
		t.Fatal(err)
	}
	const operationID = "stale-retry-operation"
	firstAttempt, replay, err := firstServer.claimDirectImportAttempt(
		storage.DefaultProjectID,
		"jmeter",
		operationID,
		requestDigest,
	)
	if err != nil || replay || firstAttempt == nil || !firstAttempt.created {
		t.Fatalf("first receipt claim = %#v replay=%v err=%v", firstAttempt, replay, err)
	}
	prepared.ID = deterministicDirectImportAssetID(firstAttempt.receipt.ID, "script", 0)
	firstTransaction, err := newCreateImportTransaction(
		firstServer,
		"jmeter",
		[]*dsl.Script{prepared},
		"",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := firstTransaction.createImportedScript(prepared, "jmeter"); err != nil {
		t.Fatal(err)
	}
	// Crash after the deterministic asset is written but before either the
	// write-ahead claims or the receipt are completed.
	firstTransaction.claims.stopHeartbeat()
	time.Sleep(directImportReceiptLease() + 50*time.Millisecond)

	replacement := NewServer(ServerConfig{Store: base})
	retryDraft := cloneScriptDefinition(prepared)
	retryDraft.ID = ""
	retryDraft.CreatedAt = time.Time{}
	retryDraft.UpdatedAt = time.Time{}
	retryDigest, err := replacement.directImportScriptsRequestDigest(
		[]*dsl.Script{retryDraft},
		map[string]any{"profile": "jmeter"},
	)
	if err != nil {
		t.Fatal(err)
	}
	retryAttempt, replay, err := replacement.claimDirectImportAttempt(
		storage.DefaultProjectID,
		"jmeter",
		operationID,
		retryDigest,
	)
	if err != nil || replay || retryAttempt == nil || retryAttempt.created {
		t.Fatalf("stale receipt claim = %#v replay=%v err=%v", retryAttempt, replay, err)
	}

	// This is the handler's stale-takeover ordering: compensate the expired
	// matching claims first, then create and commit a fresh transaction using
	// the same deterministic asset IDs.
	if err := replacement.recoverExpiredLibraryImportClaims(); err != nil {
		t.Fatalf("recover old direct-import claims: %v", err)
	}
	if _, err := base.GetScript(prepared.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("old partial asset survived claim recovery: %v", err)
	}
	if err := retryAttempt.renew(); err != nil {
		t.Fatal(err)
	}
	retryDraft.ID = prepared.ID
	retryTransaction, err := newCreateImportTransaction(
		replacement,
		"jmeter",
		[]*dsl.Script{retryDraft},
		"",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := retryTransaction.createImportedScript(retryDraft, "jmeter")
	if err != nil {
		t.Fatal(err)
	}
	if err := retryTransaction.commit(); err != nil {
		t.Fatal(err)
	}
	if err := retryAttempt.complete([]*StoredScript{stored}, nil); err != nil {
		t.Fatal(err)
	}

	time.Sleep(150 * time.Millisecond)
	if err := replacement.recoverExpiredLibraryImportClaims(); err != nil {
		t.Fatalf("late old-claim recovery: %v", err)
	}
	committed, err := base.GetScript(prepared.ID)
	if err != nil {
		t.Fatalf("committed retry asset was deleted by old recovery: %v", err)
	}
	if committed.Config.HTTP.Auth.Password != "stale-retry-password" {
		t.Fatalf("committed retry credential changed: %q", committed.Config.HTTP.Auth.Password)
	}
}
