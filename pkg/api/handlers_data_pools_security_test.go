package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestDataPoolRowMutationRequiresPathPoolMembership(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC()
	for _, project := range []*storage.Project{
		{ID: "project-a", Name: "Project A", CreatedAt: now, UpdatedAt: now},
		{ID: "project-b", Name: "Project B", CreatedAt: now, UpdatedAt: now},
	} {
		if err := store.SaveProject(project); err != nil {
			t.Fatalf("SaveProject(%s) returned error: %v", project.ID, err)
		}
	}
	for _, pool := range []*storage.SharedDataPool{
		{ID: "pool-a", ProjectID: "project-a", Name: "Pool A", CreatedAt: now, UpdatedAt: now},
		{ID: "pool-b", ProjectID: "project-b", Name: "Pool B", CreatedAt: now, UpdatedAt: now},
	} {
		if err := store.SaveDataPool(pool); err != nil {
			t.Fatalf("SaveDataPool(%s) returned error: %v", pool.ID, err)
		}
	}
	if err := store.SaveDataPoolRow(&storage.SharedDataPoolRow{
		ID: "row-b", PoolID: "pool-b", Data: map[string]string{"owner": "project-b"}, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveDataPoolRow returned error: %v", err)
	}

	server := NewServer(ServerConfig{Store: store})
	updateReq := httptest.NewRequest(http.MethodPut, "/api/v1/data-pools/pool-a/rows/row-b", bytes.NewBufferString(`{"data":{"owner":"project-a"}}`))
	updateResp := httptest.NewRecorder()
	server.mux.ServeHTTP(updateResp, updateReq)
	if updateResp.Code != http.StatusNotFound {
		t.Fatalf("cross-pool update status = %d, want %d body=%s", updateResp.Code, http.StatusNotFound, updateResp.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/data-pools/pool-a/rows/row-b", nil)
	deleteResp := httptest.NewRecorder()
	server.mux.ServeHTTP(deleteResp, deleteReq)
	if deleteResp.Code != http.StatusNotFound {
		t.Fatalf("cross-pool delete status = %d, want %d body=%s", deleteResp.Code, http.StatusNotFound, deleteResp.Body.String())
	}

	rows, err := store.ListDataPoolRows("pool-b")
	if err != nil {
		t.Fatalf("ListDataPoolRows returned error: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != "row-b" || rows[0].Data["owner"] != "project-b" {
		t.Fatalf("project B row changed through project A path: %#v", rows)
	}

	validDeleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/data-pools/pool-b/rows/row-b", nil)
	validDeleteResp := httptest.NewRecorder()
	server.mux.ServeHTTP(validDeleteResp, validDeleteReq)
	if validDeleteResp.Code != http.StatusOK {
		t.Fatalf("owning-pool delete status = %d, want %d body=%s", validDeleteResp.Code, http.StatusOK, validDeleteResp.Body.String())
	}
}

func TestRunnerRejectsForeignDataPoolsForScenarioAndTestMaterialization(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	for _, project := range []*storage.Project{
		{ID: "project-owner", Name: "Owner", CreatedAt: now, UpdatedAt: now},
		{ID: "project-foreign", Name: "Foreign", CreatedAt: now, UpdatedAt: now},
	} {
		if err := store.SaveProject(project); err != nil {
			t.Fatal(err)
		}
	}
	pool := &storage.SharedDataPool{ID: "pool-secret", ProjectID: "project-foreign", Name: "Foreign secrets", CreatedAt: now, UpdatedAt: now}
	if err := store.SaveDataPool(pool); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveDataPoolRow(&storage.SharedDataPoolRow{ID: "secret-row", PoolID: pool.ID, Data: map[string]string{"secret": "must-not-cross-projects"}, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	runner := NewTestRunner(2, NewHub(), store)
	stored, err := runner.StoreScriptDurable(&dsl.Script{
		ID: "owner-script", ProjectID: "project-owner", Name: "Owner script", Protocol: "http", Version: "1",
		Config: dsl.ScriptConfig{BaseURL: "https://example.test"}, Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	binding := storage.ScenarioDataPoolBinding{PoolID: pool.ID, Alias: "foreign", AccessMode: "read_only", SelectionStrategy: "sequential"}
	if _, err := runner.StoreScenario(CreateScenarioRequest{
		Name: "Invalid scenario", Duration: "1m", Groups: []CreateGroupEntry{{ScriptID: stored.ID, VUsers: 1}}, DataPools: []storage.ScenarioDataPoolBinding{binding},
	}); err == nil || !strings.Contains(err.Error(), "does not belong to project") {
		t.Fatalf("StoreScenario foreign pool error = %v", err)
	}
	if _, err := runner.CreateTest(CreateTestRequest{
		Name: "Invalid run", ProjectID: "project-owner", Duration: "1m", Groups: []CreateGroupEntry{{ScriptID: stored.ID, VUsers: 1}}, DataPools: []storage.ScenarioDataPoolBinding{binding},
	}); err == nil || !strings.Contains(err.Error(), "does not belong to project") {
		t.Fatalf("CreateTest foreign pool error = %v", err)
	}
	if tests := runner.ListTests(); len(tests) != 0 {
		t.Fatalf("foreign-pool run was materialized: %#v", tests)
	}
}
