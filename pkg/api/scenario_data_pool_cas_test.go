package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

type scenarioBindingConflictStore struct {
	storage.Store
}

func (s *scenarioBindingConflictStore) UpdateScenarioDataPoolBindingsIfUnchanged(string, []storage.ScenarioDataPoolBinding, time.Time) (time.Time, bool, error) {
	return time.Time{}, false, nil
}

func TestScenarioDataPoolHandlerReturnsConflictForStaleAggregate(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	now := time.Now().UTC()
	projectID := "scenario-handler-cas-project"
	if err := store.SaveProject(&storage.Project{ID: projectID, Name: projectID, CreatedAt: now}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	if err := store.SaveScript(&dsl.Script{ID: "scenario-handler-cas-script", Name: "Script", Protocol: "http", ProjectID: projectID, CreatedAt: now}); err != nil {
		t.Fatalf("SaveScript: %v", err)
	}
	for _, poolID := range []string{"scenario-handler-cas-pool-1", "scenario-handler-cas-pool-2"} {
		if err := store.SaveDataPool(&storage.SharedDataPool{ID: poolID, Name: poolID, ProjectID: projectID, CreatedAt: now}); err != nil {
			t.Fatalf("SaveDataPool(%s): %v", poolID, err)
		}
	}
	scenario := &storage.Scenario{ID: "scenario-handler-cas", Name: "CAS", Duration: "1m", CreatedAt: now}
	if err := store.SaveScenarioWithDataPoolBindings(scenario,
		[]storage.ScenarioScript{{ScenarioID: scenario.ID, ScriptID: "scenario-handler-cas-script"}},
		[]storage.ScenarioDataPoolBinding{{ID: "scenario-handler-binding-1", PoolID: "scenario-handler-cas-pool-1", Alias: "users", AccessMode: "read_only", SelectionStrategy: "sequential"}},
	); err != nil {
		t.Fatalf("SaveScenarioWithDataPoolBindings: %v", err)
	}

	server := NewServer(ServerConfig{Store: &scenarioBindingConflictStore{Store: store}})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/scenarios/"+scenario.ID+"/data-pools", bytes.NewBufferString(`{"data_pools":[{"pool_id":"scenario-handler-cas-pool-2","alias":"users","access_mode":"read_only","selection_strategy":"sequential"}]}`))
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusConflict, resp.Body.String())
	}
	bindings, err := store.ListScenarioDataPoolBindings(scenario.ID)
	if err != nil {
		t.Fatalf("ListScenarioDataPoolBindings: %v", err)
	}
	if len(bindings) != 1 || bindings[0].PoolID != "scenario-handler-cas-pool-1" {
		t.Fatalf("conflicted request changed durable bindings: %#v", bindings)
	}
}

func TestScenarioUpdateHandlerRejectsStaleHASnapshot(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	script := &dsl.Script{
		ID: "scenario-update-cas-script", Name: "Script", Protocol: "http", ProjectID: storage.DefaultProjectID,
		Config:  dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{Name: "Open", Type: "request", Method: "GET", Target: "/"}},
	}
	if err := store.SaveScript(script); err != nil {
		t.Fatalf("SaveScript: %v", err)
	}
	scenario := &storage.Scenario{ID: "scenario-update-cas", Name: "Original", Duration: "1m"}
	children := []storage.ScenarioScript{{ScenarioID: scenario.ID, ScriptID: script.ID, VUsers: 1}}
	if err := store.SaveScenario(scenario, children); err != nil {
		t.Fatalf("SaveScenario: %v", err)
	}

	server := NewServer(ServerConfig{Store: store})
	external, externalChildren, err := store.GetScenario(scenario.ID)
	if err != nil {
		t.Fatalf("GetScenario: %v", err)
	}
	external.Name = "Concurrent editor wins"
	if err := store.SaveScenario(external, externalChildren); err != nil {
		t.Fatalf("external SaveScenario: %v", err)
	}

	body := bytes.NewBufferString(`{"name":"Stale client edit","duration":"2m","groups":[{"script_id":"scenario-update-cas-script","vusers":2}]}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/scenarios/"+scenario.ID, body)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusConflict, resp.Body.String())
	}

	durable, durableChildren, err := store.GetScenario(scenario.ID)
	if err != nil {
		t.Fatalf("GetScenario after conflict: %v", err)
	}
	if durable.Name != "Concurrent editor wins" || durable.Duration != "1m" {
		t.Fatalf("concurrent scenario edit was overwritten: %+v", durable)
	}
	if len(durableChildren) != 1 || durableChildren[0].VUsers != 1 {
		t.Fatalf("stale request changed scenario children: %+v", durableChildren)
	}
}
