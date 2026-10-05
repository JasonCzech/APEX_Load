package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/jasonczech/apexload/pkg/engine"
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

type importPublicationListCountingStore struct {
	*libraryAtomicForwardingStore
	mu          sync.Mutex
	listScripts int
}

type directImportClaimWriteCountingStore struct {
	*libraryAtomicForwardingStore
	mu          sync.Mutex
	claimWrites int
}

func (s *directImportClaimWriteCountingStore) CompareAndSwapSetting(key, expectedValue, nextValue string) (bool, error) {
	if key == libraryImportClaimsSettingKey {
		s.mu.Lock()
		s.claimWrites++
		s.mu.Unlock()
	}
	return s.libraryAtomicForwardingStore.CompareAndSwapSetting(key, expectedValue, nextValue)
}

func (s *directImportClaimWriteCountingStore) CompareAndSwapLibraryImportClaims(expected, value string) (bool, error) {
	s.mu.Lock()
	s.claimWrites++
	s.mu.Unlock()
	return s.libraryAtomicForwardingStore.CompareAndSwapLibraryImportClaims(expected, value)
}

func (s *directImportClaimWriteCountingStore) writes() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.claimWrites
}

func (s *importPublicationListCountingStore) ListScripts(projectID string, folderID *string) ([]*dsl.Script, error) {
	s.mu.Lock()
	s.listScripts++
	s.mu.Unlock()
	return s.libraryAtomicForwardingStore.ListScripts(projectID, folderID)
}

func (s *importPublicationListCountingStore) resetListScripts() {
	s.mu.Lock()
	s.listScripts = 0
	s.mu.Unlock()
}

func (s *importPublicationListCountingStore) scriptListCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listScripts
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

func TestCreateImportTransactionPublishesBatchOnlyAfterCommit(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "direct-import-publication.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()

	server := NewServer(ServerConfig{Store: base})
	first := libraryTestScript(uuid.NewString(), "First staged script", storage.DefaultProjectID, "/first")
	second := libraryTestScript(uuid.NewString(), "Second staged script", storage.DefaultProjectID, "/second")
	scenarioID := uuid.NewString()
	scenarioRequest := CreateScenarioRequest{
		Name: "Staged scenario", Duration: "1m", ScenarioMode: "vuser_driven",
		Groups: []CreateGroupEntry{
			{Name: "First", ScriptID: first.ID, Role: GroupRoleLoad, VUsers: 1, InitStrategy: "at_runtime"},
			{Name: "Second", ScriptID: second.ID, Role: GroupRoleLoad, VUsers: 1, InitStrategy: "at_runtime"},
		},
	}
	transaction, err := newCreateImportTransaction(
		server,
		"publication-test",
		[]*dsl.Script{first, second},
		scenarioID,
		&scenarioRequest,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.rollback()

	if _, err := transaction.createScript(first); err != nil {
		t.Fatal(err)
	}
	// The row is durable for recovery, but no user-facing runner operation may
	// observe it while the rest of the batch is absent.
	if _, err := base.GetScript(first.ID); err != nil {
		t.Fatalf("staged durable script missing: %v", err)
	}
	if _, err := getPublishedImportScript(base, first.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("published durable lookup exposed staged script: %v", err)
	}
	if scripts, err := listPublishedImportScripts(base, storage.DefaultProjectID, nil); err != nil || len(scripts) != 0 {
		t.Fatalf("published durable listing = %#v, %v; want empty", scripts, err)
	}
	if _, ok := server.runner.GetScript(first.ID); ok {
		t.Fatal("GetScript published the first staged script")
	}
	if scripts := server.runner.ListScripts(); len(scripts) != 0 {
		t.Fatalf("ListScripts published a partial batch: %#v", scripts)
	}
	if items, total := server.libraryCatalogPage(storage.DefaultProjectID, "script", 0, 10); len(items) != 0 || total != 0 {
		t.Fatalf("library catalog published a staged script: items=%#v total=%d", items, total)
	}
	if _, err := server.runner.ReplayScript(first.ID, engine.ReplayOptions{}); err == nil || !strings.Contains(err.Error(), "script not found") {
		t.Fatalf("ReplayScript error = %v, want unpublished script not found", err)
	}
	if _, err := server.runner.CreateTest(CreateTestRequest{
		Name: "must not compile staged script", Duration: "1m",
		Groups: []CreateGroupEntry{{Name: "Users", ScriptID: first.ID, VUsers: 1}},
	}); err == nil || !strings.Contains(err.Error(), "script not found") {
		t.Fatalf("CreateTest error = %v, want staged script rejected", err)
	}

	// A second controller constructed while the owner heartbeat is live must not
	// steal the transaction, and its boot-loaded cache must apply the same fence.
	observer := NewServer(ServerConfig{Store: base})
	if _, ok := observer.runner.GetScript(first.ID); ok {
		t.Fatal("replacement reader published a live staged script")
	}

	const readers = 8
	errCh := make(chan error, readers)
	start := make(chan struct{})
	var readersWG sync.WaitGroup
	for reader := 0; reader < readers; reader++ {
		readersWG.Add(1)
		go func() {
			defer readersWG.Done()
			<-start
			for attempt := 0; attempt < 25; attempt++ {
				if err := observer.runner.reloadStoredScripts(); err != nil {
					errCh <- err
					return
				}
				if _, ok := observer.runner.GetScript(first.ID); ok {
					errCh <- errors.New("concurrent GetScript published a staged asset")
					return
				}
				if scripts := observer.runner.ListScripts(); len(scripts) != 0 {
					errCh <- fmt.Errorf("concurrent ListScripts published %d staged assets", len(scripts))
					return
				}
			}
		}()
	}
	close(start)
	if _, err := transaction.createScript(second); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.createScenario(scenarioID, scenarioRequest); err != nil {
		t.Fatal(err)
	}
	readersWG.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
	if _, ok := server.runner.GetScenario(scenarioID); ok {
		t.Fatal("GetScenario published a staged scenario")
	}
	if scenarios := server.runner.ListScenarios(); len(scenarios) != 0 {
		t.Fatalf("ListScenarios published a staged scenario: %#v", scenarios)
	}
	if items, total := server.libraryCatalogPage(storage.DefaultProjectID, "scenario", 0, 10); len(items) != 0 || total != 0 {
		t.Fatalf("library catalog published a staged scenario: items=%#v total=%d", items, total)
	}

	if err := transaction.commit(); err != nil {
		t.Fatal(err)
	}
	if err := observer.runner.reloadStoredScripts(); err != nil {
		t.Fatal(err)
	}
	if err := observer.runner.reloadStoredScenarios(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{first.ID, second.ID} {
		if _, ok := observer.runner.GetScript(id); !ok {
			t.Fatalf("committed script %s is not published", id)
		}
		if _, err := getPublishedImportScript(base, id); err != nil {
			t.Fatalf("committed durable script %s is not published: %v", id, err)
		}
	}
	if scripts := observer.runner.ListScripts(); len(scripts) != 2 {
		t.Fatalf("committed script count = %d, want 2", len(scripts))
	}
	if _, ok := observer.runner.GetScenario(scenarioID); !ok {
		t.Fatal("committed scenario is not published")
	}
}

func TestDirectImportClaimCheckpointsUseConstantDurableWrites(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "direct-import-bulk-claims.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	store := &directImportClaimWriteCountingStore{
		libraryAtomicForwardingStore: &libraryAtomicForwardingStore{Store: base},
	}
	server := NewServer(ServerConfig{Store: store})
	const itemCount = 1000
	scripts := make([]*dsl.Script, 0, itemCount)
	for index := 0; index < itemCount; index++ {
		scripts = append(scripts, libraryTestScript(
			fmt.Sprintf("bulk-claim-%04d", index),
			fmt.Sprintf("Bulk claim %04d", index),
			storage.DefaultProjectID,
			fmt.Sprintf("/bulk/%04d", index),
		))
	}
	transaction, err := newCreateImportTransaction(server, "bulk-claim-test", scripts, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, script := range scripts {
		if _, err := transaction.createScript(script); err != nil {
			t.Fatal(err)
		}
	}
	if err := transaction.commit(); err != nil {
		t.Fatal(err)
	}
	// Acquire, bulk prepare, bulk applied, terminal commit, and best-effort
	// cleanup are the only claim-setting writes. This bound is independent of N.
	if writes := store.writes(); writes > 5 {
		t.Fatalf("1000-item direct import used %d durable claim writes, want <= 5", writes)
	}
}

func TestCreateImportTransactionCannotCommitReservedSubset(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "direct-import-partial-commit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	server := NewServer(ServerConfig{Store: base})
	first := libraryTestScript(uuid.NewString(), "Applied", storage.DefaultProjectID, "/applied")
	second := libraryTestScript(uuid.NewString(), "Reserved only", storage.DefaultProjectID, "/reserved")
	transaction, err := newCreateImportTransaction(server, "partial-commit", []*dsl.Script{first, second}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.createScript(first); err != nil {
		t.Fatal(err)
	}
	if err := transaction.commit(); err == nil || !strings.Contains(err.Error(), "1 of 2 reserved assets were applied") {
		t.Fatalf("partial commit error = %v", err)
	}
	if _, ok := server.runner.GetScript(first.ID); ok {
		t.Fatal("failed partial commit published its applied prefix")
	}
	claims, err := server.loadLibraryImportClaims()
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 2 || claims[0].Committed || claims[1].Committed {
		t.Fatalf("partial commit terminalized claims: %#v", claims)
	}
	if err := transaction.rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := base.GetScript(first.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("partial commit rollback left applied script: %v", err)
	}
}

func TestLibraryCatalogPaginatesAndCountsOnlyPublishedAssets(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "direct-import-catalog-page.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	server := NewServer(ServerConfig{Store: base})
	published := libraryTestScript(uuid.NewString(), "Zulu published", storage.DefaultProjectID, "/published")
	if _, err := server.runner.StoreScriptDurable(published); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := base.SaveProject(&storage.Project{ID: "foreign-project", Name: "Foreign", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	foreign := libraryTestScript(uuid.NewString(), "Aardvark foreign", "foreign-project", "/foreign")
	if _, err := server.runner.StoreScriptDurable(foreign); err != nil {
		t.Fatal(err)
	}
	staged := libraryTestScript(uuid.NewString(), "Alpha staged", storage.DefaultProjectID, "/staged")
	transaction, err := newCreateImportTransaction(server, "catalog-page", []*dsl.Script{staged}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.rollback()
	if _, err := transaction.createScript(staged); err != nil {
		t.Fatal(err)
	}
	page, total := server.libraryCatalogPage(storage.DefaultProjectID, "script", 0, 1)
	if total != 1 || len(page) != 1 || page[0].ID != published.ID {
		t.Fatalf("catalog page/total included staged asset: page=%#v total=%d", page, total)
	}
	page, total = server.libraryCatalogPage(storage.DefaultProjectID, "script", 1, 1)
	if total != 1 || len(page) != 0 {
		t.Fatalf("catalog offset counted staged asset: page=%#v total=%d", page, total)
	}
}

func TestLibraryCatalogRejectsScenarioCommittedBetweenCandidatesAndClaimSnapshot(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "direct-import-catalog-scenario-race.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	server := NewServer(ServerConfig{Store: base})
	script := libraryTestScript(uuid.NewString(), "Raced script", storage.DefaultProjectID, "/raced")
	scenarioID := uuid.NewString()
	scenarioRequest := CreateScenarioRequest{
		Name: "Raced scenario", Duration: "1m", ScenarioMode: "vuser_driven",
		Groups: []CreateGroupEntry{{Name: "Users", ScriptID: script.ID, Role: GroupRoleLoad, VUsers: 1, InitStrategy: "at_runtime"}},
	}
	transaction, err := newCreateImportTransaction(server, "catalog-scenario-race", []*dsl.Script{script}, scenarioID, &scenarioRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.rollback()
	if _, err := transaction.createScript(script); err != nil {
		t.Fatal(err)
	}

	var publishErr error
	var once sync.Once
	server.runner.setImportPublicationSnapshot(base, func() (map[string]struct{}, error) {
		once.Do(func() {
			if _, err := transaction.createScenario(scenarioID, scenarioRequest); err != nil {
				publishErr = err
				return
			}
			publishErr = transaction.commit()
		})
		return server.pendingDirectImportAssets()
	})
	page, total := server.libraryCatalogPage(storage.DefaultProjectID, "", 0, 10)
	if publishErr != nil {
		t.Fatalf("commit between candidates and claim snapshot: %v", publishErr)
	}
	if len(page) != 0 || total != 0 {
		t.Fatalf("catalog returned a script-only slice of an atomically committed batch: page=%#v total=%d", page, total)
	}

	page, total = server.libraryCatalogPage(storage.DefaultProjectID, "", 0, 10)
	if len(page) != 2 || total != 2 {
		t.Fatalf("fresh catalog did not return the complete committed batch: page=%#v total=%d", page, total)
	}
}

func TestBrowserDirectImportRetainsCommittedClaimsUntilExplicitCleanup(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "browser-retained-claims.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	server := NewServer(ServerConfig{Store: base})
	script := libraryTestScript(uuid.NewString(), "Browser retained", storage.DefaultProjectID, "/browser")
	transaction, err := newCreateImportTransaction(server, "browser-recording", []*dsl.Script{script}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.createScript(script); err != nil {
		t.Fatal(err)
	}
	if err := transaction.commitRetainingClaims(); err != nil {
		t.Fatal(err)
	}
	if _, ok := server.runner.GetScript(script.ID); !ok {
		t.Fatal("durably committed retained claim did not publish its script")
	}
	claims, err := server.loadLibraryImportClaims()
	if err != nil || len(claims) != 1 || !claims[0].Committed {
		t.Fatalf("retained terminal claims = %#v, %v", claims, err)
	}
	if err := server.recoverExpiredLibraryImportClaims(); err != nil {
		t.Fatal(err)
	}
	claims, err = server.loadLibraryImportClaims()
	if err != nil || len(claims) != 1 || !claims[0].Committed {
		t.Fatalf("generic recovery removed browser adoption evidence: %#v, %v", claims, err)
	}
	if err := transaction.cleanupCommittedClaims(); err != nil {
		t.Fatal(err)
	}
	if err := transaction.cleanupCommittedClaims(); err != nil {
		t.Fatalf("idempotent committed cleanup: %v", err)
	}
	claims, err = server.loadLibraryImportClaims()
	if err != nil || len(claims) != 0 {
		t.Fatalf("explicit committed cleanup left claims: %#v, %v", claims, err)
	}
}

func TestUnrelatedClaimAcquisitionPreservesCommittedBrowserPublicationEvidence(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "browser-retained-claims-unrelated.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	server := NewServer(ServerConfig{Store: base})
	browserScript := libraryTestScript(uuid.NewString(), "Browser retained", storage.DefaultProjectID, "/browser")
	transaction, err := newCreateImportTransaction(server, "browser-recording", []*dsl.Script{browserScript}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.createScript(browserScript); err != nil {
		t.Fatal(err)
	}
	if err := transaction.commitRetainingClaims(); err != nil {
		t.Fatal(err)
	}

	unrelatedItem := LibraryBundleItem{
		Kind: "script", SourceInstanceID: "unrelated-source", SourceItemID: "unrelated-script",
		ProjectID: storage.DefaultProjectID, ContentHash: "sha256:unrelated-claim",
	}
	unrelated, err := server.acquireLibraryImportClaims(
		LibraryBundle{Items: []LibraryBundleItem{unrelatedItem}},
		"direct-import:unrelated",
		storage.DefaultProjectID,
	)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := server.loadLibraryImportClaims()
	if err != nil {
		t.Fatal(err)
	}
	retainedBrowser := 0
	unrelatedActive := 0
	for _, claim := range claims {
		switch {
		case claim.RemoteID == "direct-import:browser-recording" && claim.Committed:
			retainedBrowser++
		case claim.OwnerID == unrelated.owner && !claim.Committed:
			unrelatedActive++
		}
	}
	if retainedBrowser != 1 || unrelatedActive != 1 || len(claims) != 2 {
		t.Fatalf("claims after unrelated acquisition = %#v, want retained browser evidence plus unrelated owner", claims)
	}

	if err := unrelated.abandon(); err != nil {
		t.Fatal(err)
	}
	if err := server.recoverExpiredLibraryImportClaims(); err != nil {
		t.Fatal(err)
	}
	if err := transaction.cleanupCommittedClaims(); err != nil {
		t.Fatal(err)
	}
}

func TestCreateImportTransactionRecoversImmediatelyOnSingleControllerRestart(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "direct-import-immediate-restart.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()

	firstServer := NewServer(ServerConfig{Store: base})
	script := libraryTestScript(uuid.NewString(), "Crash-visible script", storage.DefaultProjectID, "/crash-visible")
	scenarioID := uuid.NewString()
	scenarioRequest := CreateScenarioRequest{
		Name: "Crash-visible scenario", Duration: "1m", ScenarioMode: "vuser_driven",
		Groups: []CreateGroupEntry{{Name: "Users", ScriptID: script.ID, Role: GroupRoleLoad, VUsers: 1, InitStrategy: "at_runtime"}},
	}
	transaction, err := newCreateImportTransaction(
		firstServer,
		"immediate-restart-test",
		[]*dsl.Script{script},
		scenarioID,
		&scenarioRequest,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.createScript(script); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.createScenario(scenarioID, scenarioRequest); err != nil {
		t.Fatal(err)
	}

	// Simulate process loss without waiting for the normal claim TTL. The new
	// single-controller instance has no live owner registration and reconciles
	// the abandoned batch during construction, before it can serve a read.
	transaction.stopClaimHeartbeat()
	transaction.closeWithoutAssetCompensation()
	replacement := NewServer(ServerConfig{Store: base})
	if _, err := base.GetScript(script.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("staged script survived immediate restart recovery: %v", err)
	}
	if _, _, err := base.GetScenario(scenarioID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("staged scenario survived immediate restart recovery: %v", err)
	}
	if _, ok := replacement.runner.GetScript(script.ID); ok {
		t.Fatal("replacement runner published the recovered script")
	}
	if _, ok := replacement.runner.GetScenario(scenarioID); ok {
		t.Fatal("replacement runner published the recovered scenario")
	}
	claims, err := replacement.loadLibraryImportClaims()
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 0 {
		t.Fatalf("immediate restart left claims: %#v", claims)
	}
}

func TestCreateImportTransactionRollbackCannotPublishStaleReadSnapshot(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "direct-import-rollback-read-race.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()

	server := NewServer(ServerConfig{Store: base})
	script := libraryTestScript(uuid.NewString(), "Rollback race script", storage.DefaultProjectID, "/rollback-race")
	transaction, err := newCreateImportTransaction(server, "rollback-read-race", []*dsl.Script{script}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.createScript(script); err != nil {
		t.Fatal(err)
	}

	snapshotRead := make(chan struct{})
	continueRead := make(chan struct{})
	var once sync.Once
	server.runner.afterImportPublicationSnapshot = func() {
		once.Do(func() {
			close(snapshotRead)
			<-continueRead
		})
	}
	readResult := make(chan bool, 1)
	go func() {
		_, ok := server.runner.GetScript(script.ID)
		readResult <- ok
	}()
	select {
	case <-snapshotRead:
	case <-time.After(2 * time.Second):
		t.Fatal("reader did not reach publication barrier")
	}
	if err := transaction.rollback(); err != nil {
		t.Fatal(err)
	}
	close(continueRead)
	select {
	case visible := <-readResult:
		if visible {
			t.Fatal("reader returned a stale script snapshot after rollback removed the row and claim")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reader remained blocked after rollback")
	}
	if _, err := base.GetScript(script.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("rollback left durable script: %v", err)
	}
}

func TestImportPublicationListRevalidationUsesBoundedStoreScans(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "direct-import-list-scan.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	store := &importPublicationListCountingStore{
		libraryAtomicForwardingStore: &libraryAtomicForwardingStore{Store: base},
	}
	for index := 0; index < 64; index++ {
		script := libraryTestScript(uuid.NewString(), fmt.Sprintf("Published %d", index), storage.DefaultProjectID, fmt.Sprintf("/%d", index))
		if err := base.SaveScript(script); err != nil {
			t.Fatal(err)
		}
	}
	server := NewServer(ServerConfig{Store: store})
	store.resetListScripts()
	if got := len(server.runner.ListScripts()); got != 64 {
		t.Fatalf("published script count = %d, want 64", got)
	}
	// One scan revalidates the full snapshot; a per-candidate Get/List loop would
	// make this grow with library size and regress 100k-script installations.
	if calls := store.scriptListCalls(); calls != 1 {
		t.Fatalf("ListScripts storage calls = %d, want exactly 1", calls)
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
