package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestDataPoolRowResponsesArePaginated(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	pool := &storage.SharedDataPool{ID: "api-bounded-rows-pool", Name: "Bounded rows", ProjectID: storage.DefaultProjectID}
	if err := store.SaveDataPool(pool); err != nil {
		t.Fatalf("SaveDataPool: %v", err)
	}
	base := time.Now().UTC().Add(-time.Hour)
	for i, id := range []string{"api-bounded-row-1", "api-bounded-row-2", "api-bounded-row-3"} {
		if err := store.SaveDataPoolRow(&storage.SharedDataPoolRow{ID: id, PoolID: pool.ID, Data: map[string]string{"id": id}, CreatedAt: base.Add(time.Duration(i) * time.Minute)}); err != nil {
			t.Fatalf("SaveDataPoolRow(%s): %v", id, err)
		}
	}
	server := NewServer(ServerConfig{Store: store})
	for _, path := range []string{
		"/api/v1/data-pools/" + pool.ID + "/rows?limit=1&offset=1",
		"/api/v1/data-pools/" + pool.ID + "?limit=1&offset=1",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200 body=%s", path, resp.Code, resp.Body.String())
		}
		var payload struct {
			Rows   []*storage.SharedDataPoolRow `json:"rows"`
			Limit  int                          `json:"limit"`
			Offset int                          `json:"offset"`
		}
		if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		if payload.Limit != 1 || payload.Offset != 1 || len(payload.Rows) != 1 || payload.Rows[0].ID != "api-bounded-row-2" {
			t.Fatalf("GET %s payload = %#v", path, payload)
		}
	}
}

func TestHandleUpdateScenarioDataPoolsRejectsForeignProjectPools(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	projectOne := &storage.Project{ID: "project-1", Name: "Checkout", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	projectTwo := &storage.Project{ID: "project-2", Name: "Search", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveProject(projectOne); err != nil {
		t.Fatalf("SaveProject projectOne returned error: %v", err)
	}
	if err := store.SaveProject(projectTwo); err != nil {
		t.Fatalf("SaveProject projectTwo returned error: %v", err)
	}

	script := &dsl.Script{
		ID:        "script-1",
		Name:      "Checkout",
		Protocol:  "http",
		Version:   "1.0.0",
		ProjectID: projectOne.ID,
		Config:    dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions:   []dsl.Action{{Name: "Open", Type: "GET", Target: "/"}},
	}
	if err := store.SaveScript(script); err != nil {
		t.Fatalf("SaveScript returned error: %v", err)
	}

	scenario := &storage.Scenario{ID: "scenario-1", Name: "Checkout Flow", Duration: "1m", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveScenario(scenario, []storage.ScenarioScript{{ScenarioID: scenario.ID, Order: 0, ScriptID: script.ID, VUsers: 5}}); err != nil {
		t.Fatalf("SaveScenario returned error: %v", err)
	}

	pool := &storage.SharedDataPool{ID: "pool-foreign", Name: "Search Customers", ProjectID: projectTwo.ID, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveDataPool(pool); err != nil {
		t.Fatalf("SaveDataPool returned error: %v", err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	body := bytes.NewBufferString(`{"data_pools":[{"pool_id":"pool-foreign","alias":"customers","access_mode":"read_only","selection_strategy":"sequential"}]}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/scenarios/"+scenario.ID+"/data-pools", body)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "does not belong to scenario project") {
		t.Fatalf("body = %q, want cross-project data pool error", resp.Body.String())
	}
}

func TestHandleUpdateScenarioDataPoolsRefreshesRunnerSnapshot(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC()
	project := &storage.Project{ID: "project-live-bindings", Name: "Live bindings", CreatedAt: now, UpdatedAt: now}
	if err := store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	script := &dsl.Script{
		ID: "script-live-bindings", Name: "Checkout", Protocol: "http", Version: "1.0.0", ProjectID: project.ID,
		Config: dsl.ScriptConfig{BaseURL: "https://example.com"}, Actions: []dsl.Action{{Name: "Open", Type: "GET", Target: "/"}},
	}
	if err := store.SaveScript(script); err != nil {
		t.Fatal(err)
	}
	scenario := &storage.Scenario{ID: "scenario-live-bindings", Name: "Checkout", Duration: "1m", CreatedAt: now, UpdatedAt: now}
	if err := store.SaveScenario(scenario, []storage.ScenarioScript{{ScenarioID: scenario.ID, Order: 0, ScriptID: script.ID, VUsers: 1}}); err != nil {
		t.Fatal(err)
	}
	pool := &storage.SharedDataPool{ID: "pool-live-bindings", Name: "Customers", ProjectID: project.ID, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveDataPool(pool); err != nil {
		t.Fatal(err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	body := bytes.NewBufferString(`{"data_pools":[{"pool_id":"pool-live-bindings","alias":"customers","access_mode":"read_only","selection_strategy":"sequential"}]}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/scenarios/"+scenario.ID+"/data-pools", body)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	live, ok := server.runner.GetScenario(scenario.ID)
	if !ok || len(live.DataPools) != 1 || live.DataPools[0].PoolID != pool.ID || live.DataPools[0].Alias != "customers" {
		t.Fatalf("runner scenario did not refresh bindings: %+v", live)
	}
	// A consumer must not be able to mutate the runner through a returned slice.
	live.DataPools[0].Alias = "tampered"
	again, _ := server.runner.GetScenario(scenario.ID)
	if again.DataPools[0].Alias != "customers" {
		t.Fatalf("returned scenario aliases runner state: %+v", again.DataPools)
	}
}
