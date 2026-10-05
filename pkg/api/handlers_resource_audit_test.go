package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func newAuditTestServer(t *testing.T) (*Server, *storage.SQLiteStore) {
	t.Helper()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	return server, store
}

func requireAuditActions(t *testing.T, store *storage.SQLiteStore, projectID string, want []string) {
	t.Helper()
	events, err := store.ListAuditEvents(storage.AuditEventFilter{Limit: len(want) + 5, ProjectID: projectID})
	if err != nil {
		t.Fatalf("ListAuditEvents returned error: %v", err)
	}
	if len(events) != len(want) {
		t.Fatalf("audit event count = %d, want %d", len(events), len(want))
	}
	for index, action := range want {
		if events[index].Action != action {
			t.Fatalf("audit action[%d] = %q, want %q", index, events[index].Action, action)
		}
	}
}

func TestHandleApplicationAndReleaseWritesAuditEvents(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()

	project := &storage.Project{ID: "project-app-release", Name: "Checkout", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}

	createAppReq := httptest.NewRequest(http.MethodPost, "/api/v1/applications", bytes.NewBufferString(`{"name":"Checkout App","project_id":"project-app-release"}`))
	createAppReq.Header.Set("Content-Type", "application/json")
	createAppResp := httptest.NewRecorder()
	server.mux.ServeHTTP(createAppResp, createAppReq)
	if createAppResp.Code != http.StatusCreated {
		t.Fatalf("application create status = %d, want %d body=%s", createAppResp.Code, http.StatusCreated, createAppResp.Body.String())
	}

	var application storage.Application
	if err := json.Unmarshal(createAppResp.Body.Bytes(), &application); err != nil {
		t.Fatalf("json.Unmarshal application returned error: %v", err)
	}

	updateAppBody, err := json.Marshal(map[string]any{"name": "Checkout App Renamed", "updated_at": application.UpdatedAt})
	if err != nil {
		t.Fatal(err)
	}
	updateAppReq := httptest.NewRequest(http.MethodPut, "/api/v1/applications/"+application.ID, bytes.NewReader(updateAppBody))
	updateAppReq.Header.Set("Content-Type", "application/json")
	updateAppResp := httptest.NewRecorder()
	server.mux.ServeHTTP(updateAppResp, updateAppReq)
	if updateAppResp.Code != http.StatusOK {
		t.Fatalf("application update status = %d, want %d body=%s", updateAppResp.Code, http.StatusOK, updateAppResp.Body.String())
	}

	createReleaseReq := httptest.NewRequest(http.MethodPost, "/api/v1/applications/"+application.ID+"/releases", bytes.NewBufferString(`{"name":"2026.05"}`))
	createReleaseReq.Header.Set("Content-Type", "application/json")
	createReleaseResp := httptest.NewRecorder()
	server.mux.ServeHTTP(createReleaseResp, createReleaseReq)
	if createReleaseResp.Code != http.StatusCreated {
		t.Fatalf("release create status = %d, want %d body=%s", createReleaseResp.Code, http.StatusCreated, createReleaseResp.Body.String())
	}

	var release storage.Release
	if err := json.Unmarshal(createReleaseResp.Body.Bytes(), &release); err != nil {
		t.Fatalf("json.Unmarshal release returned error: %v", err)
	}

	updateReleaseBody, err := json.Marshal(map[string]any{"name": "2026.06", "updated_at": release.UpdatedAt})
	if err != nil {
		t.Fatal(err)
	}
	updateReleaseReq := httptest.NewRequest(http.MethodPut, "/api/v1/releases/"+release.ID, bytes.NewReader(updateReleaseBody))
	updateReleaseReq.Header.Set("Content-Type", "application/json")
	updateReleaseResp := httptest.NewRecorder()
	server.mux.ServeHTTP(updateReleaseResp, updateReleaseReq)
	if updateReleaseResp.Code != http.StatusOK {
		t.Fatalf("release update status = %d, want %d body=%s", updateReleaseResp.Code, http.StatusOK, updateReleaseResp.Body.String())
	}

	archiveReleaseReq := httptest.NewRequest(http.MethodPost, "/api/v1/releases/"+release.ID+"/archive", nil)
	archiveReleaseResp := httptest.NewRecorder()
	server.mux.ServeHTTP(archiveReleaseResp, archiveReleaseReq)
	if archiveReleaseResp.Code != http.StatusOK {
		t.Fatalf("release archive status = %d, want %d body=%s", archiveReleaseResp.Code, http.StatusOK, archiveReleaseResp.Body.String())
	}

	deleteReleaseReq := httptest.NewRequest(http.MethodDelete, "/api/v1/releases/"+release.ID, nil)
	deleteReleaseResp := httptest.NewRecorder()
	server.mux.ServeHTTP(deleteReleaseResp, deleteReleaseReq)
	if deleteReleaseResp.Code != http.StatusOK {
		t.Fatalf("release delete status = %d, want %d body=%s", deleteReleaseResp.Code, http.StatusOK, deleteReleaseResp.Body.String())
	}

	deleteAppReq := httptest.NewRequest(http.MethodDelete, "/api/v1/applications/"+application.ID, nil)
	deleteAppResp := httptest.NewRecorder()
	server.mux.ServeHTTP(deleteAppResp, deleteAppReq)
	if deleteAppResp.Code != http.StatusOK {
		t.Fatalf("application delete status = %d, want %d body=%s", deleteAppResp.Code, http.StatusOK, deleteAppResp.Body.String())
	}

	requireAuditActions(t, store, project.ID, []string{
		"application.delete",
		"release.delete",
		"release.archive",
		"release.update",
		"release.create",
		"application.update",
		"application.create",
	})
}

func TestHandleFolderWritesAuditEvents(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()

	project := &storage.Project{ID: "project-folder", Name: "Folders", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}

	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+project.ID+"/folders", bytes.NewBufferString(`{"name":"Artifacts","category":"results"}`))
	createReq.Header.Set("Content-Type", "application/json")
	createResp := httptest.NewRecorder()
	server.mux.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("folder create status = %d, want %d body=%s", createResp.Code, http.StatusCreated, createResp.Body.String())
	}

	var folder storage.Folder
	if err := json.Unmarshal(createResp.Body.Bytes(), &folder); err != nil {
		t.Fatalf("json.Unmarshal folder returned error: %v", err)
	}

	updateReq := httptest.NewRequest(http.MethodPut, "/api/v1/folders/"+folder.ID, bytes.NewBufferString(`{"name":"Archived Artifacts"}`))
	updateReq.Header.Set("Content-Type", "application/json")
	updateResp := httptest.NewRecorder()
	server.mux.ServeHTTP(updateResp, updateReq)
	if updateResp.Code != http.StatusOK {
		t.Fatalf("folder update status = %d, want %d body=%s", updateResp.Code, http.StatusOK, updateResp.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/folders/"+folder.ID, nil)
	deleteResp := httptest.NewRecorder()
	server.mux.ServeHTTP(deleteResp, deleteReq)
	if deleteResp.Code != http.StatusOK {
		t.Fatalf("folder delete status = %d, want %d body=%s", deleteResp.Code, http.StatusOK, deleteResp.Body.String())
	}

	requireAuditActions(t, store, project.ID, []string{
		"folder.delete",
		"folder.update",
		"folder.create",
	})
}

func TestHandleDataPoolAndRowWritesAuditEvents(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()

	project := &storage.Project{ID: "project-pool", Name: "Data Pools", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}

	createPoolReq := httptest.NewRequest(http.MethodPost, "/api/v1/data-pools", bytes.NewBufferString(`{"name":"Customers","project_id":"project-pool"}`))
	createPoolReq.Header.Set("Content-Type", "application/json")
	createPoolResp := httptest.NewRecorder()
	server.mux.ServeHTTP(createPoolResp, createPoolReq)
	if createPoolResp.Code != http.StatusCreated {
		t.Fatalf("data pool create status = %d, want %d body=%s", createPoolResp.Code, http.StatusCreated, createPoolResp.Body.String())
	}

	var pool storage.SharedDataPool
	if err := json.Unmarshal(createPoolResp.Body.Bytes(), &pool); err != nil {
		t.Fatalf("json.Unmarshal pool returned error: %v", err)
	}

	updatePoolBody, err := json.Marshal(map[string]any{
		"name":       "Customers Updated",
		"project_id": "project-pool",
		"updated_at": pool.UpdatedAt,
	})
	if err != nil {
		t.Fatalf("json.Marshal pool update returned error: %v", err)
	}
	updatePoolReq := httptest.NewRequest(http.MethodPut, "/api/v1/data-pools/"+pool.ID, bytes.NewReader(updatePoolBody))
	updatePoolReq.Header.Set("Content-Type", "application/json")
	updatePoolResp := httptest.NewRecorder()
	server.mux.ServeHTTP(updatePoolResp, updatePoolReq)
	if updatePoolResp.Code != http.StatusOK {
		t.Fatalf("data pool update status = %d, want %d body=%s", updatePoolResp.Code, http.StatusOK, updatePoolResp.Body.String())
	}

	createRowReq := httptest.NewRequest(http.MethodPost, "/api/v1/data-pools/"+pool.ID+"/rows", bytes.NewBufferString(`{"data":{"customer_id":"1"},"status":"ready","version":1}`))
	createRowReq.Header.Set("Content-Type", "application/json")
	createRowResp := httptest.NewRecorder()
	server.mux.ServeHTTP(createRowResp, createRowReq)
	if createRowResp.Code != http.StatusCreated {
		t.Fatalf("data pool row create status = %d, want %d body=%s", createRowResp.Code, http.StatusCreated, createRowResp.Body.String())
	}

	var row storage.SharedDataPoolRow
	if err := json.Unmarshal(createRowResp.Body.Bytes(), &row); err != nil {
		t.Fatalf("json.Unmarshal row returned error: %v", err)
	}

	// version echoes the value read from the create response: it is the editor's
	// optimistic-concurrency token, not the desired next version.
	updateRowReq := httptest.NewRequest(http.MethodPut, "/api/v1/data-pools/"+pool.ID+"/rows/"+row.ID, bytes.NewBufferString(`{"data":{"customer_id":"2"},"status":"used","version":1}`))
	updateRowReq.Header.Set("Content-Type", "application/json")
	updateRowResp := httptest.NewRecorder()
	server.mux.ServeHTTP(updateRowResp, updateRowReq)
	if updateRowResp.Code != http.StatusOK {
		t.Fatalf("data pool row update status = %d, want %d body=%s", updateRowResp.Code, http.StatusOK, updateRowResp.Body.String())
	}

	deleteRowReq := httptest.NewRequest(http.MethodDelete, "/api/v1/data-pools/"+pool.ID+"/rows/"+row.ID, nil)
	deleteRowResp := httptest.NewRecorder()
	server.mux.ServeHTTP(deleteRowResp, deleteRowReq)
	if deleteRowResp.Code != http.StatusOK {
		t.Fatalf("data pool row delete status = %d, want %d body=%s", deleteRowResp.Code, http.StatusOK, deleteRowResp.Body.String())
	}

	deletePoolReq := httptest.NewRequest(http.MethodDelete, "/api/v1/data-pools/"+pool.ID, nil)
	deletePoolResp := httptest.NewRecorder()
	server.mux.ServeHTTP(deletePoolResp, deletePoolReq)
	if deletePoolResp.Code != http.StatusOK {
		t.Fatalf("data pool delete status = %d, want %d body=%s", deletePoolResp.Code, http.StatusOK, deletePoolResp.Body.String())
	}

	requireAuditActions(t, store, project.ID, []string{
		"data_pool.delete",
		"data_pool.row.delete",
		"data_pool.row.update",
		"data_pool.row.create",
		"data_pool.update",
		"data_pool.create",
	})
}

func TestHandleScenarioDataPoolBindingWritesAuditEvent(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()

	project := &storage.Project{ID: "project-scenario-pool", Name: "Scenario Pool", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}
	if err := store.SaveDataPool(&storage.SharedDataPool{ID: "pool-bind", Name: "Customers", ProjectID: project.ID, CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("SaveDataPool returned error: %v", err)
	}
	server.runner.StoreScript(&dsl.Script{
		ID:        "script-scenario-pool",
		Name:      "Checkout Script",
		ProjectID: project.ID,
		Protocol:  "http",
		Version:   "1.0.0",
		Config:    dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions:   []dsl.Action{{Name: "Open", Type: "request", Target: "/"}},
	})
	scenario, err := server.runner.StoreScenario(CreateScenarioRequest{
		Name:     "Checkout Scenario",
		Duration: "2m",
		Groups: []CreateGroupEntry{{
			ScriptID: "script-scenario-pool",
			VUsers:   1,
		}},
	})
	if err != nil {
		t.Fatalf("StoreScenario returned error: %v", err)
	}

	updateReq := httptest.NewRequest(http.MethodPut, "/api/v1/scenarios/"+scenario.ID+"/data-pools", bytes.NewBufferString(`{"data_pools":[{"pool_id":"pool-bind","alias":"customers","access_mode":"read_only","selection_strategy":"sequential"}]}`))
	updateReq.Header.Set("Content-Type", "application/json")
	updateResp := httptest.NewRecorder()
	server.mux.ServeHTTP(updateResp, updateReq)
	if updateResp.Code != http.StatusOK {
		t.Fatalf("scenario data pool update status = %d, want %d body=%s", updateResp.Code, http.StatusOK, updateResp.Body.String())
	}

	requireAuditActions(t, store, project.ID, []string{"scenario.data_pools.update"})
}

func TestHandleScriptCRUDWritesAuditEvents(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()
	now := time.Now().UTC()
	if err := store.SaveProject(&storage.Project{ID: "project-script-audit", Name: "Script Audit", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}

	if err := store.SaveFolder(&storage.Folder{ID: "folder-script-audit", ProjectID: "project-script-audit", Name: "Library", Category: "general", CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("SaveFolder returned error: %v", err)
	}

	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts", bytes.NewBufferString(`{"project_id":"project-script-audit","script":{"name":"Checkout Script","version":"1.0.0","protocol":"http","config":{"base_url":"https://example.com"},"actions":[{"name":"Open","type":"request","method":"GET","target":"/"}]}}`))
	createReq.Header.Set("Content-Type", "application/json")
	createResp := httptest.NewRecorder()
	server.mux.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("script create status = %d, want %d body=%s", createResp.Code, http.StatusCreated, createResp.Body.String())
	}

	var script StoredScript
	if err := json.Unmarshal(createResp.Body.Bytes(), &script); err != nil {
		t.Fatalf("json.Unmarshal script returned error: %v", err)
	}

	updateReq := httptest.NewRequest(http.MethodPut, "/api/v1/scripts/"+script.ID, bytes.NewBufferString(`{"script":{"id":"`+script.ID+`","name":"Checkout Script Updated","project_id":"project-script-audit","version":"1.0.0","protocol":"http","config":{"base_url":"https://example.com"},"actions":[{"name":"Open","type":"request","method":"GET","target":"/checkout"}]}}`))
	updateReq.Header.Set("Content-Type", "application/json")
	updateResp := httptest.NewRecorder()
	server.mux.ServeHTTP(updateResp, updateReq)
	if updateResp.Code != http.StatusOK {
		t.Fatalf("script update status = %d, want %d body=%s", updateResp.Code, http.StatusOK, updateResp.Body.String())
	}

	moveReq := httptest.NewRequest(http.MethodPatch, "/api/v1/scripts/"+script.ID+"/move", bytes.NewBufferString(`{"folder_id":"folder-script-audit"}`))
	moveReq.Header.Set("Content-Type", "application/json")
	moveResp := httptest.NewRecorder()
	server.mux.ServeHTTP(moveResp, moveReq)
	if moveResp.Code != http.StatusOK {
		t.Fatalf("script move status = %d, want %d body=%s", moveResp.Code, http.StatusOK, moveResp.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/scripts/"+script.ID, nil)
	deleteResp := httptest.NewRecorder()
	server.mux.ServeHTTP(deleteResp, deleteReq)
	if deleteResp.Code != http.StatusOK {
		t.Fatalf("script delete status = %d, want %d body=%s", deleteResp.Code, http.StatusOK, deleteResp.Body.String())
	}

	requireAuditActions(t, store, "project-script-audit", []string{
		"script.delete",
		"script.move",
		"script.update",
		"script.update",
		"script.create",
	})
}
