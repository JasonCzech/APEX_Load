package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

type observedDataPoolCursorStore struct {
	SQLiteStore       *storage.SQLiteStore
	pageStateReads    int
	fullRevisionReads int
}

func (s *observedDataPoolCursorStore) DataPoolRowsPageState(poolID string) (string, int, error) {
	s.pageStateReads++
	return s.SQLiteStore.DataPoolRowsPageState(poolID)
}

func (s *observedDataPoolCursorStore) DataPoolRowsRevision(poolID string) (string, int, error) {
	s.fullRevisionReads++
	return s.SQLiteStore.DataPoolRowsRevision(poolID)
}

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

func TestDataPoolRowCursorPaginationUsesStableKeysetAndRejectsDrift(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	pool := &storage.SharedDataPool{ID: "api-cursor-rows-pool", Name: "Cursor rows", ProjectID: storage.DefaultProjectID}
	if err := store.SaveDataPool(pool); err != nil {
		t.Fatalf("SaveDataPool: %v", err)
	}
	base := time.Now().UTC().Add(-time.Hour)
	for i := 1; i <= 5; i++ {
		id := fmt.Sprintf("cursor-row-%d", i)
		if err := store.SaveDataPoolRow(&storage.SharedDataPoolRow{
			ID: id, PoolID: pool.ID, Data: map[string]string{"id": id}, CreatedAt: base.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("SaveDataPoolRow(%s): %v", id, err)
		}
	}
	observedStore := &observedDataPoolCursorStore{SQLiteStore: store}
	server := NewServer(ServerConfig{Store: sqliteOverlay(observedStore)})
	type cursorPayload struct {
		Rows       []*storage.SharedDataPoolRow `json:"rows"`
		Total      int                          `json:"total"`
		Offset     int                          `json:"offset"`
		Revision   string                       `json:"revision"`
		HasMore    bool                         `json:"has_more"`
		NextCursor string                       `json:"next_cursor"`
	}
	getPage := func(path string) (*httptest.ResponseRecorder, cursorPayload) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		var payload cursorPayload
		if resp.Code == http.StatusOK {
			if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
				t.Fatalf("decode %s: %v", path, err)
			}
		}
		return resp, payload
	}

	firstResponse, first := getPage("/api/v1/data-pools/" + pool.ID + "?limit=2&cursor=")
	if firstResponse.Code != http.StatusOK {
		t.Fatalf("first cursor page status = %d body=%s", firstResponse.Code, firstResponse.Body.String())
	}
	if len(first.Rows) != 2 || first.Rows[0].ID != "cursor-row-1" || first.Rows[1].ID != "cursor-row-2" ||
		first.Total != 5 || first.Offset != 0 || !first.HasMore || first.NextCursor == "" || first.Revision == "" {
		t.Fatalf("first cursor page = %#v", first)
	}
	secondPath := "/api/v1/data-pools/" + pool.ID + "/rows?limit=2&cursor=" + url.QueryEscape(first.NextCursor)
	secondResponse, second := getPage(secondPath)
	if secondResponse.Code != http.StatusOK {
		t.Fatalf("second cursor page status = %d body=%s", secondResponse.Code, secondResponse.Body.String())
	}
	if len(second.Rows) != 2 || second.Rows[0].ID != "cursor-row-3" || second.Rows[1].ID != "cursor-row-4" ||
		second.Offset != 2 || !second.HasMore || second.NextCursor == "" || second.Revision != first.Revision {
		t.Fatalf("second cursor page = %#v", second)
	}

	if deleted, err := store.DeleteDataPoolRowFromPool(pool.ID, "cursor-row-1"); err != nil || !deleted {
		t.Fatalf("concurrent delete deleted=%v err=%v", deleted, err)
	}
	stalePath := "/api/v1/data-pools/" + pool.ID + "/rows?limit=2&cursor=" + url.QueryEscape(second.NextCursor)
	staleResponse, _ := getPage(stalePath)
	if staleResponse.Code != http.StatusConflict || !strings.Contains(staleResponse.Body.String(), "reload from the first page") {
		t.Fatalf("stale cursor status = %d body=%s, want 409 reload guidance", staleResponse.Code, staleResponse.Body.String())
	}

	freshResponse, fresh := getPage("/api/v1/data-pools/" + pool.ID + "/rows?limit=2&cursor=")
	if freshResponse.Code != http.StatusOK {
		t.Fatalf("fresh cursor page status = %d body=%s", freshResponse.Code, freshResponse.Body.String())
	}
	if len(fresh.Rows) != 2 || fresh.Rows[0].ID != "cursor-row-2" || fresh.Rows[1].ID != "cursor-row-3" || fresh.Total != 4 {
		t.Fatalf("fresh page after delete = %#v", fresh)
	}
	if observedStore.fullRevisionReads != 0 {
		t.Fatalf("cursor paging performed %d full-pool revision scans, want 0", observedStore.fullRevisionReads)
	}
	if observedStore.pageStateReads == 0 {
		t.Fatal("cursor paging did not read the O(1) page state")
	}

	for _, path := range []string{
		"/api/v1/data-pools/" + pool.ID + "/rows?limit=2&cursor=not-base64",
		"/api/v1/data-pools/" + pool.ID + "/rows?limit=2&offset=1&cursor=",
	} {
		response, _ := getPage(path)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("GET %s status = %d body=%s, want 400", path, response.Code, response.Body.String())
		}
	}
	otherPool := &storage.SharedDataPool{ID: "api-other-cursor-rows-pool", Name: "Other cursor rows", ProjectID: storage.DefaultProjectID}
	if err := store.SaveDataPool(otherPool); err != nil {
		t.Fatalf("SaveDataPool(other): %v", err)
	}
	wrongPoolPath := "/api/v1/data-pools/" + otherPool.ID + "/rows?limit=2&cursor=" + url.QueryEscape(first.NextCursor)
	wrongPoolResponse, _ := getPage(wrongPoolPath)
	if wrongPoolResponse.Code != http.StatusBadRequest {
		t.Fatalf("wrong-pool cursor status = %d body=%s, want 400", wrongPoolResponse.Code, wrongPoolResponse.Body.String())
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
