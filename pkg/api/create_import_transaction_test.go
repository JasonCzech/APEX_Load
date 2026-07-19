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
