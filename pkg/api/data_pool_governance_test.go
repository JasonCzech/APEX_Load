package api

import (
	"bytes"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

type governanceDataPoolStore struct {
	pool     *storage.SharedDataPool
	rows     []*storage.SharedDataPoolRow
	rowReads int
}

func (s *governanceDataPoolStore) GetDataPool(string) (*storage.SharedDataPool, error) {
	return s.pool, nil
}

func (s *governanceDataPoolStore) ListDataPoolRows(string) ([]*storage.SharedDataPoolRow, error) {
	s.rowReads++
	return s.rows, nil
}

var _ dataPoolPersistence = (*governanceDataPoolStore)(nil)

func TestBuildSharedDataSourcesFiltersInactiveRows(t *testing.T) {
	store := &governanceDataPoolStore{
		pool: &storage.SharedDataPool{ID: "pool-status", Name: "Status rows", ProjectID: storage.DefaultProjectID},
		rows: []*storage.SharedDataPoolRow{
			{ID: "row-active", Data: map[string]string{"customer_id": "C-1"}, Status: "active"},
			{ID: "row-blank", Data: map[string]string{"customer_id": "C-2"}},
			{ID: "row-consumed", Data: map[string]string{"customer_id": "C-3"}, Status: "consumed"},
			{ID: "row-disabled", Data: map[string]string{"customer_id": "C-4"}, Status: "disabled"},
		},
	}
	runner := NewTestRunner(1, NewHub(), nil)
	runner.dataPoolStore = store
	sources, err := runner.buildSharedDataSourcesLocked([]storage.ScenarioDataPoolBinding{{
		PoolID: "pool-status", Alias: "customers", AccessMode: "read_only", SelectionStrategy: "sequential",
	}})
	if err != nil {
		t.Fatalf("buildSharedDataSourcesLocked: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("sources = %#v, want one", sources)
	}
	inline := sources[0].InlineData
	for _, want := range []string{"C-1", "C-2"} {
		if !strings.Contains(inline, want) {
			t.Fatalf("inline CSV missing active row %q: %q", want, inline)
		}
	}
	for _, reject := range []string{"C-3", "C-4"} {
		if strings.Contains(inline, reject) {
			t.Fatalf("inline CSV includes inactive row %q: %q", reject, inline)
		}
	}
}

func TestBuildSharedDataSourcesErrorsWhenAllRowsInactive(t *testing.T) {
	store := &governanceDataPoolStore{
		pool: &storage.SharedDataPool{ID: "pool-consumed", Name: "Consumed rows", ProjectID: storage.DefaultProjectID},
		rows: []*storage.SharedDataPoolRow{
			{ID: "row-1", Data: map[string]string{"id": "1"}, Status: "consumed"},
		},
	}
	runner := NewTestRunner(1, NewHub(), nil)
	runner.dataPoolStore = store
	_, err := runner.buildSharedDataSourcesLocked([]storage.ScenarioDataPoolBinding{{
		PoolID: "pool-consumed", Alias: "customers", AccessMode: "read_only", SelectionStrategy: "sequential",
	}})
	if err == nil || !strings.Contains(err.Error(), "has no active rows") {
		t.Fatalf("buildSharedDataSourcesLocked error = %v, want no-active-rows error", err)
	}
}

func TestBuildSharedDataSourcesRejectsArchivedPoolBeforeReadingRows(t *testing.T) {
	archived := time.Now().UTC()
	store := &governanceDataPoolStore{
		pool: &storage.SharedDataPool{ID: "pool-archived", Name: "Archived rows", ProjectID: storage.DefaultProjectID, ArchivedAt: &archived},
		rows: []*storage.SharedDataPoolRow{{ID: "row-1", Data: map[string]string{"id": "1"}}},
	}
	runner := NewTestRunner(1, NewHub(), nil)
	runner.dataPoolStore = store
	_, err := runner.buildSharedDataSourcesLocked([]storage.ScenarioDataPoolBinding{{
		PoolID: "pool-archived", Alias: "customers", AccessMode: "read_only", SelectionStrategy: "sequential",
	}})
	if err == nil || !strings.Contains(err.Error(), "archived") {
		t.Fatalf("buildSharedDataSourcesLocked error = %v, want archived-pool error", err)
	}
	if store.rowReads != 0 {
		t.Fatalf("archived pool read %d row pages before rejection", store.rowReads)
	}
}

func TestCreateTestRejectsArchivedDataPool(t *testing.T) {
	server, scriptID, poolID := seedDataPoolContractServer(t)
	pool, err := server.store.GetDataPool(poolID)
	if err != nil {
		t.Fatalf("GetDataPool: %v", err)
	}
	archivedAt := time.Now().UTC()
	pool.ArchivedAt = &archivedAt
	if err := server.store.SaveDataPool(pool); err != nil {
		t.Fatalf("SaveDataPool(archive): %v", err)
	}
	_, err = server.runner.CreateTest(directDataPoolContractRequest(scriptID, []storage.ScenarioDataPoolBinding{{
		PoolID: poolID, Alias: "customers", AccessMode: "read_only", SelectionStrategy: "sequential",
	}}))
	if err == nil || !strings.Contains(err.Error(), "archived") {
		t.Fatalf("CreateTest error = %v, want archived-pool rejection", err)
	}
}

func TestHandleUpdateScenarioDataPoolsRejectsArchivedPool(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC()
	project := &storage.Project{ID: "project-archived-pool", Name: "Archived pools", CreatedAt: now, UpdatedAt: now}
	if err := store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	script := &dsl.Script{
		ID: "script-archived-pool", Name: "Checkout", Protocol: "http", Version: "1.0.0", ProjectID: project.ID,
		Config: dsl.ScriptConfig{BaseURL: "https://example.com"}, Actions: []dsl.Action{{Name: "Open", Type: "GET", Target: "/"}},
	}
	if err := store.SaveScript(script); err != nil {
		t.Fatal(err)
	}
	scenario := &storage.Scenario{ID: "scenario-archived-pool", Name: "Checkout", Duration: "1m", CreatedAt: now, UpdatedAt: now}
	if err := store.SaveScenario(scenario, []storage.ScenarioScript{{ScenarioID: scenario.ID, Order: 0, ScriptID: script.ID, VUsers: 1}}); err != nil {
		t.Fatal(err)
	}
	archivedAt := now
	pool := &storage.SharedDataPool{ID: "pool-archived-binding", Name: "Customers", ProjectID: project.ID, CreatedAt: now, UpdatedAt: now, ArchivedAt: &archivedAt}
	if err := store.SaveDataPool(pool); err != nil {
		t.Fatal(err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	body := bytes.NewBufferString(`{"data_pools":[{"pool_id":"pool-archived-binding","alias":"customers","access_mode":"read_only","selection_strategy":"sequential"}]}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/scenarios/"+scenario.ID+"/data-pools", body)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "archived") {
		t.Fatalf("status = %d body=%s, want 400 archived-pool error", resp.Code, resp.Body.String())
	}
}

func TestHandleUpdateDataPoolRowHonorsClientVersion(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	pool := &storage.SharedDataPool{ID: "pool-cas-row", Name: "CAS rows", ProjectID: storage.DefaultProjectID}
	if err := store.SaveDataPool(pool); err != nil {
		t.Fatalf("SaveDataPool: %v", err)
	}
	row := &storage.SharedDataPoolRow{ID: "row-cas", PoolID: pool.ID, Data: map[string]string{"id": "v1"}, Status: "disabled"}
	if err := store.SaveDataPoolRow(row); err != nil {
		t.Fatalf("SaveDataPoolRow: %v", err)
	}

	server := NewServer(ServerConfig{Store: store})
	put := func(payload string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, "/api/v1/data-pools/"+pool.ID+"/rows/"+row.ID, bytes.NewBufferString(payload))
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		return resp
	}

	if resp := put(`{"data":{"id":"v2"},"version":99}`); resp.Code != http.StatusConflict {
		t.Fatalf("stale-version PUT status = %d body=%s, want 409", resp.Code, resp.Body.String())
	}
	if resp := put(`{"version":1}`); resp.Code != http.StatusBadRequest {
		t.Fatalf("missing-data PUT status = %d body=%s, want 400", resp.Code, resp.Body.String())
	}
	if resp := put(`{"data":{"id":"bypass"},"version":-1}`); resp.Code != http.StatusBadRequest {
		t.Fatalf("negative-version PUT status = %d body=%s, want 400", resp.Code, resp.Body.String())
	}
	if resp := put(`{"data":{"id":"v2"},"version":1}`); resp.Code != http.StatusOK {
		t.Fatalf("current-version PUT status = %d body=%s, want 200", resp.Code, resp.Body.String())
	}
	if resp := put(`{"data":{"id":"tokenless"}}`); resp.Code != http.StatusBadRequest {
		t.Fatalf("tokenless PUT status = %d body=%s, want 400", resp.Code, resp.Body.String())
	}
	updated, err := store.GetDataPoolRowFromPool(pool.ID, row.ID)
	if err != nil {
		t.Fatalf("GetDataPoolRowFromPool: %v", err)
	}
	if updated.Data["id"] != "v2" || updated.Version != 2 || updated.Status != "disabled" {
		t.Fatalf("final row = %+v, want data v2 at version 2 with preserved disabled status", updated)
	}
}

func TestHandleUpdateDataPoolHonorsClientUpdatedAt(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	pool := &storage.SharedDataPool{ID: "pool-cas-meta", Name: "CAS meta", ProjectID: storage.DefaultProjectID}
	if err := store.SaveDataPool(pool); err != nil {
		t.Fatalf("SaveDataPool: %v", err)
	}
	current, err := store.GetDataPool(pool.ID)
	if err != nil {
		t.Fatalf("GetDataPool: %v", err)
	}

	server := NewServer(ServerConfig{Store: store})
	put := func(name string, updatedAt time.Time) *httptest.ResponseRecorder {
		payload, err := json.Marshal(map[string]any{"name": name, "updated_at": updatedAt})
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		req := httptest.NewRequest(http.MethodPut, "/api/v1/data-pools/"+pool.ID, bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		return resp
	}

	if resp := put("Stale editor", current.UpdatedAt.Add(-time.Hour)); resp.Code != http.StatusConflict {
		t.Fatalf("stale-token PUT status = %d body=%s, want 409", resp.Code, resp.Body.String())
	}
	tokenlessReq := httptest.NewRequest(http.MethodPut, "/api/v1/data-pools/"+pool.ID, strings.NewReader(`{"name":"Tokenless editor"}`))
	tokenlessReq.Header.Set("Content-Type", "application/json")
	tokenlessResp := httptest.NewRecorder()
	server.mux.ServeHTTP(tokenlessResp, tokenlessReq)
	if tokenlessResp.Code != http.StatusBadRequest {
		t.Fatalf("tokenless PUT status = %d body=%s, want 400", tokenlessResp.Code, tokenlessResp.Body.String())
	}
	if resp := put("Fresh editor", current.UpdatedAt); resp.Code != http.StatusOK {
		t.Fatalf("current-token PUT status = %d body=%s, want 200", resp.Code, resp.Body.String())
	}
	staleArchivePayload, err := json.Marshal(map[string]any{
		"name":        "Fresh editor",
		"archived_at": time.Now().UTC(),
		"updated_at":  current.UpdatedAt,
	})
	if err != nil {
		t.Fatalf("Marshal(stale archive): %v", err)
	}
	staleArchiveReq := httptest.NewRequest(http.MethodPut, "/api/v1/data-pools/"+pool.ID, bytes.NewReader(staleArchivePayload))
	staleArchiveReq.Header.Set("Content-Type", "application/json")
	staleArchiveResp := httptest.NewRecorder()
	server.mux.ServeHTTP(staleArchiveResp, staleArchiveReq)
	if staleArchiveResp.Code != http.StatusConflict {
		t.Fatalf("stale archive status = %d body=%s, want 409", staleArchiveResp.Code, staleArchiveResp.Body.String())
	}
	renamed, err := store.GetDataPool(pool.ID)
	if err != nil {
		t.Fatalf("GetDataPool(after): %v", err)
	}
	if renamed.Name != "Fresh editor" {
		t.Fatalf("pool name = %q, want update from fresh token", renamed.Name)
	}
	if renamed.ArchivedAt != nil {
		t.Fatalf("stale archive request archived the pool at %v", renamed.ArchivedAt)
	}
}

func TestDataPoolStrategyItems(t *testing.T) {
	sources := func(name, strategy string) []dsl.DataSource {
		return []dsl.DataSource{{Name: name, Strategy: strategy}}
	}
	tests := []struct {
		name       string
		vusers     int
		iterations int
		global     int
		sources    []dsl.DataSource
		counts     map[string]int
		wantStatus string
		wantNone   bool
	}{
		{name: "unique underfill blocks", vusers: 5, sources: sources("customers", "unique"), counts: map[string]int{"customers": 3}, wantStatus: "blocked"},
		{name: "unique fit passes", vusers: 3, sources: sources("customers", "unique"), counts: map[string]int{"customers": 3}, wantNone: true},
		{name: "pop shortfall needs review", vusers: 2, iterations: 3, sources: sources("orders", "pop"), counts: map[string]int{"orders": 4}, wantStatus: "needs_review"},
		{name: "pop fit passes", vusers: 2, iterations: 2, sources: sources("orders", "pop"), counts: map[string]int{"orders": 4}, wantNone: true},
		{name: "pop duration-based needs review", vusers: 2, iterations: 0, sources: sources("orders", "pop"), counts: map[string]int{"orders": 4}, wantStatus: "needs_review"},
		{name: "pop global cap bounds per-user demand", vusers: 2, iterations: 3, global: 4, sources: sources("orders", "pop"), counts: map[string]int{"orders": 4}, wantNone: true},
		{name: "pop global cap bounds duration model", vusers: 2, global: 4, sources: sources("orders", "pop"), counts: map[string]int{"orders": 5}, wantNone: true},
		{name: "pop global cap still eagerly loads each vuser", vusers: 100, global: 1, sources: sources("orders", "pop"), counts: map[string]int{"orders": 99}, wantStatus: "needs_review"},
		{name: "pop global cap shortfall", vusers: 2, iterations: 3, global: 3, sources: sources("orders", "pop"), counts: map[string]int{"orders": 2}, wantStatus: "needs_review"},
		{name: "pop multiplication saturates", vusers: math.MaxInt, iterations: 2, sources: sources("orders", "pop"), counts: map[string]int{"orders": math.MaxInt - 1}, wantStatus: "needs_review"},
		{name: "sequential passes", vusers: 50, sources: sources("customers", "sequential"), counts: map[string]int{"customers": 1}, wantNone: true},
		{name: "unknown count skipped", vusers: 50, sources: sources("customers", "unique"), counts: map[string]int{}, wantNone: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			items := dataPoolStrategyItems("Group A", test.vusers, test.iterations, test.global, test.sources, test.counts)
			if test.wantNone {
				if len(items) != 0 {
					t.Fatalf("items = %#v, want none", items)
				}
				return
			}
			if len(items) != 1 || items[0].Status != test.wantStatus || items[0].Dimension != "data_pool_rows" {
				t.Fatalf("items = %#v, want one %s data_pool_rows item", items, test.wantStatus)
			}
			if test.name == "pop global cap shortfall" && items[0].RequiredRows != 4 {
				t.Fatalf("global-cap required rows = %d, want 4", items[0].RequiredRows)
			}
			if test.name == "pop global cap still eagerly loads each vuser" && items[0].RequiredRows != 100 {
				t.Fatalf("eager-load required rows = %d, want 100", items[0].RequiredRows)
			}
			if test.name == "pop multiplication saturates" && items[0].RequiredRows != math.MaxInt {
				t.Fatalf("overflow-safe required rows = %d, want %d", items[0].RequiredRows, math.MaxInt)
			}
		})
	}
	if got := dataPoolStrategyVUsers(TestGroup{
		VUsers: 1,
		Stages: []storage.LoadStage{
			{TargetVUsers: 5},
			{TargetVUsers: 20},
			{TargetVUsers: 0},
		},
	}); got != 20 {
		t.Fatalf("staged data-pool VUser demand = %d, want peak 20", got)
	}
}

func TestValidateScenarioLoadModelFlagsUniqueUnderfill(t *testing.T) {
	server, scriptID, poolID := seedDataPoolContractServer(t)
	report, err := server.runner.ValidateScenarioLoadModel(CreateScenarioRequest{
		Name: "Unique underfill", Duration: "1m",
		Groups: []CreateGroupEntry{{ScriptID: scriptID, VUsers: 3}},
		DataPools: []storage.ScenarioDataPoolBinding{{
			PoolID: poolID, Alias: "customers", AccessMode: "read_only", SelectionStrategy: "unique",
		}},
	})
	if err != nil {
		t.Fatalf("ValidateScenarioLoadModel: %v", err)
	}
	found := false
	for _, item := range report.Items {
		if item.Dimension == "data_pool_rows" && item.Status == "blocked" && strings.Contains(item.Summary, "unique") {
			if item.RequiredRows != 3 || item.AvailableRows != 1 {
				t.Fatalf("unique underfill item = %+v, want required 3 available 1", item)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("no unique-underfill item in report: %s", mustJSON(report.Items))
	}
}
