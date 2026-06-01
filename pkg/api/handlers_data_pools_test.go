package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

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
