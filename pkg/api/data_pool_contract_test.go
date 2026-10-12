package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func seedDataPoolContractServer(t *testing.T) (*Server, string, string) {
	t.Helper()
	server, store := newRuntimeOverrideTestServer(t)
	now := time.Now().UTC()
	pool := &storage.SharedDataPool{
		ID: "pool-contract", Name: "Contract rows", ProjectID: storage.DefaultProjectID,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.SaveDataPool(pool); err != nil {
		t.Fatalf("SaveDataPool: %v", err)
	}
	if err := store.SaveDataPoolRow(&storage.SharedDataPoolRow{
		ID: "pool-contract-row", PoolID: pool.ID, Data: map[string]string{"customer_id": "C-100"},
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveDataPoolRow: %v", err)
	}
	script := httpScript("script-data-pool-contract", "Data pool contract")
	if _, err := server.runner.StoreScriptDurable(script); err != nil {
		t.Fatalf("StoreScriptDurable: %v", err)
	}
	return server, script.ID, pool.ID
}

func directDataPoolContractRequest(scriptID string, bindings []storage.ScenarioDataPoolBinding) CreateTestRequest {
	return CreateTestRequest{
		Name: "Direct data-pool run", Duration: "1m",
		Groups:    []CreateGroupEntry{{ScriptID: scriptID, VUsers: 1}},
		DataPools: bindings,
	}
}

func TestCreateTestRejectsInvalidDataPoolBindingContract(t *testing.T) {
	server, scriptID, poolID := seedDataPoolContractServer(t)
	tests := []struct {
		name     string
		bindings []storage.ScenarioDataPoolBinding
		want     string
	}{
		{
			name: "read write is not implemented",
			bindings: []storage.ScenarioDataPoolBinding{{
				PoolID: poolID, Alias: "customers", AccessMode: "read_write", SelectionStrategy: "sequential",
			}},
			want: "unsupported data pool access mode \"read_write\"",
		},
		{
			name: "consume is not implemented",
			bindings: []storage.ScenarioDataPoolBinding{{
				PoolID: poolID, Alias: "customers", AccessMode: "consume", SelectionStrategy: "sequential",
			}},
			want: "unsupported data pool access mode \"consume\"",
		},
		{
			name: "unknown access mode",
			bindings: []storage.ScenarioDataPoolBinding{{
				PoolID: poolID, Alias: "customers", AccessMode: "borrow", SelectionStrategy: "sequential",
			}},
			want: "unsupported data pool access mode \"borrow\"",
		},
		{
			name: "unknown selection strategy",
			bindings: []storage.ScenarioDataPoolBinding{{
				PoolID: poolID, Alias: "customers", AccessMode: "read_only", SelectionStrategy: "round_robin",
			}},
			want: "unsupported data pool selection strategy \"round_robin\"",
		},
		{
			name: "duplicate normalized alias",
			bindings: []storage.ScenarioDataPoolBinding{
				{PoolID: poolID, Alias: " Customers ", AccessMode: "read_only", SelectionStrategy: "sequential"},
				{PoolID: poolID, Alias: "customers", AccessMode: "read_only", SelectionStrategy: "random"},
			},
			want: "duplicate data pool binding alias \"customers\"",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			created, err := server.runner.CreateTest(directDataPoolContractRequest(scriptID, test.bindings))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("CreateTest = %#v, err %v, want error containing %q", created, err, test.want)
			}
		})
	}
	if tests := server.runner.ListTests(); len(tests) != 0 {
		t.Fatalf("invalid direct requests materialized tests: %#v", tests)
	}
}

func TestCreateTestCanonicalizesDataPoolWhitespaceAndDefaults(t *testing.T) {
	server, scriptID, poolID := seedDataPoolContractServer(t)
	created, err := server.runner.CreateTest(directDataPoolContractRequest(scriptID, []storage.ScenarioDataPoolBinding{{
		PoolID: "  " + poolID + "\t", Alias: "  customers ", AccessMode: " \t", SelectionStrategy: " ",
	}}))
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	if len(created.DataPools) != 1 {
		t.Fatalf("data pools = %#v, want one canonical binding", created.DataPools)
	}
	got := created.DataPools[0]
	if got.PoolID != poolID || got.Alias != "customers" || got.AccessMode != "read_only" || got.SelectionStrategy != "sequential" {
		t.Fatalf("canonical binding = %+v", got)
	}
}

func TestHandleCreateTestRejectsUnsupportedDataPoolAccessMode(t *testing.T) {
	server, scriptID, poolID := seedDataPoolContractServer(t)
	body, err := json.Marshal(directDataPoolContractRequest(scriptID, []storage.ScenarioDataPoolBinding{{
		PoolID: poolID, Alias: "customers", AccessMode: "consume", SelectionStrategy: "sequential",
	}}))
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tests", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "use \\\"read_only\\\" or \\\"live\\\"") {
		t.Fatalf("status = %d body=%s, want 400 read-only contract error", resp.Code, resp.Body.String())
	}
}

type dataPoolBuildContractSpy struct {
	rowReads int
}

func (s *dataPoolBuildContractSpy) GetDataPool(string) (*storage.SharedDataPool, error) {
	return &storage.SharedDataPool{ID: "pool-contract", ProjectID: storage.DefaultProjectID}, nil
}

func (s *dataPoolBuildContractSpy) ListDataPoolRows(string) ([]*storage.SharedDataPoolRow, error) {
	s.rowReads++
	return []*storage.SharedDataPoolRow{{ID: "row", Data: map[string]string{"id": "1"}}}, nil
}

func TestBuildSharedDataSourcesRejectsLegacyWritableModeBeforeReadingRows(t *testing.T) {
	spy := &dataPoolBuildContractSpy{}
	runner := NewTestRunner(1, NewHub(), nil)
	runner.dataPoolStore = spy
	_, err := runner.buildSharedDataSourcesLocked([]storage.ScenarioDataPoolBinding{{
		PoolID: "pool-contract", Alias: "customers", AccessMode: "read_write", SelectionStrategy: "sequential",
	}})
	if err == nil || !strings.Contains(err.Error(), "invalid executable data pool bindings") || !strings.Contains(err.Error(), "use \"read_only\" or \"live\"") {
		t.Fatalf("buildSharedDataSourcesLocked error = %v, want explicit read-only execution error", err)
	}
	if spy.rowReads != 0 {
		t.Fatalf("legacy writable binding read %d pool rows before rejection", spy.rowReads)
	}
}

func TestScenarioDataPoolNormalizationRejectsWritableModes(t *testing.T) {
	for _, mode := range []string{"read_write", "consume"} {
		t.Run(mode, func(t *testing.T) {
			_, err := normalizeScenarioDataPoolBindings("scenario-contract", []storage.ScenarioDataPoolBinding{{
				PoolID: "pool-contract", Alias: "customers", AccessMode: mode, SelectionStrategy: "sequential",
			}})
			if err == nil || !strings.Contains(err.Error(), "use \"read_only\" or \"live\"") {
				t.Fatalf("normalizeScenarioDataPoolBindings(%q) error = %v", mode, err)
			}
		})
	}
}

var _ dataPoolPersistence = (*dataPoolBuildContractSpy)(nil)

type dataPoolReadCountingStore struct {
	delegate  dataPoolPersistence
	poolReads int
	rowReads  int
}

func (s *dataPoolReadCountingStore) GetDataPool(id string) (*storage.SharedDataPool, error) {
	s.poolReads++
	return s.delegate.GetDataPool(id)
}

func (s *dataPoolReadCountingStore) ListDataPoolRows(poolID string) ([]*storage.SharedDataPoolRow, error) {
	s.rowReads++
	return s.delegate.ListDataPoolRows(poolID)
}

func TestCreateTestCanonicalizesLegacyStoredScenarioBinding(t *testing.T) {
	server, scriptID, poolID := seedDataPoolContractServer(t)
	scenario, err := server.runner.StoreScenario(CreateScenarioRequest{
		Name: "Legacy pool scenario", Duration: "1m",
		Groups: []CreateGroupEntry{{ScriptID: scriptID, VUsers: 1}},
		DataPools: []storage.ScenarioDataPoolBinding{{
			PoolID: poolID, Alias: "customers", AccessMode: "read_only", SelectionStrategy: "sequential",
		}},
	})
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}
	// Simulate a row authored before the read-only contract. The persistence
	// layer intentionally remains capable of loading historical values.
	if err := server.store.SaveScenarioDataPoolBindings(scenario.ID, []storage.ScenarioDataPoolBinding{{
		ID: "legacy-scenario-binding", ScenarioID: scenario.ID, PoolID: poolID, Alias: "customers",
		AccessMode: "consume", SelectionStrategy: "sequential", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}}); err != nil {
		t.Fatalf("SaveScenarioDataPoolBindings(legacy): %v", err)
	}

	restarted := NewTestRunner(2, NewHub(), server.store)
	legacy, ok := restarted.GetScenario(scenario.ID)
	if !ok || len(legacy.DataPools) != 1 || legacy.DataPools[0].AccessMode != "consume" {
		t.Fatalf("legacy scenario was not restored as authored: found=%t scenario=%+v", ok, legacy)
	}
	created, err := restarted.CreateTest(CreateTestRequest{ScenarioID: scenario.ID})
	if err != nil {
		t.Fatalf("CreateTest(legacy scenario): %v", err)
	}
	if len(created.DataPools) != 1 || created.DataPools[0].AccessMode != "read_only" {
		t.Fatalf("legacy scenario run bindings = %+v, want canonical read_only snapshot", created.DataPools)
	}
	durable, err := server.store.GetTest(created.ID)
	if err != nil {
		t.Fatalf("GetTest: %v", err)
	}
	if len(durable.DataPools) != 1 || durable.DataPools[0].AccessMode != "read_only" {
		t.Fatalf("durable run bindings = %+v, want canonical read_only snapshot", durable.DataPools)
	}
}

func TestQueuedRevisionTreatsReorderedLegacyModesAsImmutableReadOnlySnapshot(t *testing.T) {
	server, scriptID, firstPoolID := seedDataPoolContractServer(t)
	now := time.Now().UTC()
	secondPool := &storage.SharedDataPool{
		ID: "pool-contract-secondary", Name: "Secondary rows", ProjectID: storage.DefaultProjectID,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := server.store.SaveDataPool(secondPool); err != nil {
		t.Fatalf("SaveDataPool(second): %v", err)
	}
	if err := server.store.SaveDataPoolRow(&storage.SharedDataPoolRow{
		ID: "pool-contract-secondary-row", PoolID: secondPool.ID, Data: map[string]string{"order_id": "before"},
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveDataPoolRow(second): %v", err)
	}

	original, err := server.runner.CreateTest(directDataPoolContractRequest(scriptID, []storage.ScenarioDataPoolBinding{
		{PoolID: firstPoolID, Alias: "customers", AccessMode: "read_only", SelectionStrategy: "sequential"},
		{PoolID: secondPool.ID, Alias: "orders", AccessMode: "read_only", SelectionStrategy: "unique"},
	}))
	if err != nil {
		t.Fatalf("CreateTest(original): %v", err)
	}
	record, err := server.store.GetTest(original.ID)
	if err != nil {
		t.Fatalf("GetTest(original): %v", err)
	}
	record.DataPools[0].AccessMode = "consume"
	record.DataPools[1].AccessMode = "read_write"
	if err := server.store.SaveTest(record); err != nil {
		t.Fatalf("SaveTest(legacy source): %v", err)
	}

	restarted := NewTestRunner(2, NewHub(), server.store)
	legacySource, ok := restarted.GetTestSummary(original.ID)
	if !ok || len(legacySource.DataPools) != 2 || legacySource.DataPools[0].AccessMode != "consume" || legacySource.DataPools[1].AccessMode != "read_write" {
		t.Fatalf("legacy queued source = %+v, found=%t", legacySource, ok)
	}
	delegate := restarted.dataPoolStore
	reads := &dataPoolReadCountingStore{delegate: delegate}
	restarted.dataPoolStore = reads

	revision, err := restarted.CreateTest(CreateTestRequest{
		Name:                 "Legacy reordered revision",
		RevisionSourceTestID: original.ID,
		DataPools: []storage.ScenarioDataPoolBinding{
			{PoolID: " " + secondPool.ID + " ", Alias: " orders ", AccessMode: "read_only", SelectionStrategy: " unique "},
			{PoolID: " " + firstPoolID, Alias: "customers ", AccessMode: "", SelectionStrategy: ""},
		},
	})
	if err != nil {
		t.Fatalf("CreateTest(revision): %v", err)
	}
	if reads.poolReads != 0 || reads.rowReads != 0 {
		t.Fatalf("unchanged legacy revision reloaded mutable pools: metadata_reads=%d row_reads=%d", reads.poolReads, reads.rowReads)
	}
	if len(revision.DataPools) != 2 || revision.DataPools[0].Alias != "orders" || revision.DataPools[1].Alias != "customers" {
		t.Fatalf("revision did not retain canonical reordered intent: %+v", revision.DataPools)
	}
	for _, binding := range revision.DataPools {
		if binding.AccessMode != "read_only" {
			t.Fatalf("revision binding retained legacy mode: %+v", binding)
		}
	}
}

func TestExecutionPlanAuthoringRejectsWritableModeButLegacyMaterializationCanonicalizes(t *testing.T) {
	server, scriptID, poolID := seedDataPoolContractServer(t)
	plan := &storage.ExecutionPlan{
		Name: "New writable plan", ProjectID: storage.DefaultProjectID,
		Entries: []storage.ExecutionPlanEntry{{
			ScenarioID: "future-scenario",
			DataPools: []storage.ScenarioDataPoolBinding{{
				PoolID: poolID, Alias: "customers", AccessMode: "consume", SelectionStrategy: "sequential",
			}},
		}},
	}
	status, message := server.validateExecutionPlan(plan)
	if status != http.StatusBadRequest || !strings.Contains(message, "use \"read_only\" or \"live\"") {
		t.Fatalf("validateExecutionPlan status=%d message=%q, want strict read-only rejection", status, message)
	}

	scenario, err := server.runner.StoreScenario(CreateScenarioRequest{
		Name: "Legacy plan scenario", Duration: "1m", Groups: []CreateGroupEntry{{ScriptID: scriptID, VUsers: 1}},
	})
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}
	req, err := buildPlanEntryCreateRequest("Legacy plan", storage.DefaultProjectID, nil, storage.ExecutionPlanEntry{
		ScenarioID: scenario.ID,
		DataPools: []storage.ScenarioDataPoolBinding{{
			PoolID: poolID, Alias: "customers", AccessMode: "consume", SelectionStrategy: "sequential",
		}},
	})
	if err != nil {
		t.Fatalf("buildPlanEntryCreateRequest: %v", err)
	}
	created, err := server.runner.CreateTest(req)
	if err != nil {
		t.Fatalf("CreateTest(legacy plan entry): %v", err)
	}
	if len(created.DataPools) != 1 || created.DataPools[0].AccessMode != "read_only" {
		t.Fatalf("legacy plan run bindings = %+v, want canonical read_only", created.DataPools)
	}
}

var _ dataPoolPersistence = (*dataPoolReadCountingStore)(nil)
