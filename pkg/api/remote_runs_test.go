package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestRemoteRunProxyPushesDependenciesCreatesStartsAndPolls(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	local := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: "127.0.0.1:8080", LibraryCredentials: NewMemoryLibraryCredentialStore()})
	script := local.runner.StoreScript(libraryTestScript("script-local", "Checkout Script", "local-project", "/checkout"))
	scenario, err := local.runner.StoreScenario(CreateScenarioRequest{
		Name:     "Checkout Scenario",
		Duration: "30s",
		Groups: []CreateGroupEntry{{
			Name:     "checkout",
			ScriptID: script.ID,
			VUsers:   1,
		}},
	})
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}

	var sawImport bool
	var sawTranslatedCreate bool
	var sawStart bool
	remoteHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer remote-token" {
			t.Errorf("Authorization = %q, want bearer token", r.Header.Get("Authorization"))
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/library/import":
			sawImport = true
			var req LibraryImportRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode import: %v", err)
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			if req.ProjectID != "remote-project" {
				t.Errorf("import project = %q, want remote-project", req.ProjectID)
			}
			response := LibraryImportResponse{SchemaVersion: libraryBundleSchemaVersion}
			for _, item := range req.Bundle.Items {
				response.Items = append(response.Items, LibraryImportResultItem{
					Kind:             item.Kind,
					SourceInstanceID: item.SourceInstanceID,
					SourceItemID:     item.SourceItemID,
					LocalItemID:      "remote-" + normalizeLibraryKind(item.Kind),
					ContentHash:      item.ContentHash,
					Action:           "created",
				})
			}
			writeJSON(w, http.StatusCreated, response)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/tests":
			var req CreateTestRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode create test: %v", err)
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			if req.ScenarioID != "remote-scenario" {
				t.Errorf("remote create scenario_id = %q, want remote-scenario", req.ScenarioID)
			} else {
				sawTranslatedCreate = true
			}
			writeJSON(w, http.StatusCreated, &ManagedTest{ID: "remote-test-1", Name: req.Name, ProjectID: req.ProjectID, ScenarioID: req.ScenarioID, Status: "QUEUED"})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/tests/remote-test-1/start":
			sawStart = true
			writeJSON(w, http.StatusOK, map[string]any{"status": "started", "test_id": "remote-test-1"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/tests/remote-test-1":
			writeJSON(w, http.StatusOK, &ManagedTest{ID: "remote-test-1", Name: "Remote Checkout", ProjectID: "remote-project", ScenarioID: "remote-scenario", Status: "RUNNING"})
		default:
			writeError(w, http.StatusNotFound, r.Method+" "+r.URL.Path)
		}
	}))
	defer remoteHTTP.Close()

	ref, err := local.libraryCreds.PutRemoteAPIKey("workspace-1", "remote-token")
	if err != nil {
		t.Fatalf("PutRemoteAPIKey: %v", err)
	}
	local.libraryMu.Lock()
	local.libraryRemotes["workspace-1"] = &LibraryRemote{
		ID:                   "workspace-1",
		Name:                 "Hosted K8s",
		BaseURL:              remoteHTTP.URL,
		DefaultProjectID:     "remote-project",
		AuthMethod:           "service_account",
		CredentialRef:        ref,
		CredentialConfigured: true,
	}
	local.libraryMu.Unlock()

	body := marshalLibraryTestJSON(t, RemoteRunCreateRequest{
		CreateTestRequest: CreateTestRequest{
			Name:       "Remote Checkout",
			Duration:   "30s",
			ScenarioID: scenario.ID,
			ProjectID:  "local-project",
		},
		StartImmediately: true,
	})
	createResp := httptest.NewRecorder()
	local.mux.ServeHTTP(createResp, httptest.NewRequest(http.MethodPost, "/api/v1/remotes/workspace-1/runs", bytes.NewReader(body)))
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create remote run status = %d body=%s", createResp.Code, createResp.Body.String())
	}
	var created RemoteRunProxyResponse
	if err := json.NewDecoder(createResp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if !sawImport || !sawTranslatedCreate || !sawStart {
		t.Fatalf("remote workflow incomplete: import=%t create=%t start=%t", sawImport, sawTranslatedCreate, sawStart)
	}
	if created.Link.RemoteTestID != "remote-test-1" || created.Link.Status != "RUNNING" {
		t.Fatalf("created link = %#v, want remote-test-1 RUNNING", created.Link)
	}

	getResp := httptest.NewRecorder()
	local.mux.ServeHTTP(getResp, httptest.NewRequest(http.MethodGet, "/api/v1/remotes/workspace-1/runs/remote-test-1", nil))
	if getResp.Code != http.StatusOK {
		t.Fatalf("get remote run status = %d body=%s", getResp.Code, getResp.Body.String())
	}
	rawRunLinks, err := store.GetSetting(remoteRunLinksSettingKey)
	if err != nil {
		t.Fatalf("GetSetting remote run links: %v", err)
	}
	if !strings.Contains(rawRunLinks, "remote-test-1") {
		t.Fatalf("remote run links = %q, want remote-test-1", rawRunLinks)
	}
	rawLibraryLinks, err := store.GetSetting(librarySyncLinksSettingKey)
	if err != nil {
		t.Fatalf("GetSetting library sync links: %v", err)
	}
	if rawLibraryLinks == "" || rawLibraryLinks == rawRunLinks {
		t.Fatalf("library sync links missing/collided: library=%q run=%q", rawLibraryLinks, rawRunLinks)
	}
}

func TestRemoteRunLinksPersistSeparatelyFromLibrarySyncLinks(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	if err := store.SaveSetting(librarySyncLinksSettingKey, `[{"RemoteID":"workspace-a","Kind":"script","SourceItemID":"script-a"}]`); err != nil {
		t.Fatalf("SaveSetting library links: %v", err)
	}
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: "127.0.0.1:8080"})

	link := server.upsertRemoteRunLink(RemoteRunLink{
		RemoteID:        "workspace-a",
		RemoteTestID:    "remote-test-a",
		RemoteURL:       "https://apex.example/#/command-center/remote-test-a",
		LocalProjectID:  "project-a",
		LocalScenarioID: "scenario-a",
		RemoteProjectID: "remote-project-a",
		Status:          "QUEUED",
	})
	if link.LocalRunID == "" {
		t.Fatalf("LocalRunID is empty")
	}

	storedRunLinks, err := store.GetSetting(remoteRunLinksSettingKey)
	if err != nil {
		t.Fatalf("GetSetting remote run links: %v", err)
	}
	if storedRunLinks == "" {
		t.Fatalf("remote run links were not persisted")
	}
	storedLibraryLinks, err := store.GetSetting(librarySyncLinksSettingKey)
	if err != nil {
		t.Fatalf("GetSetting library sync links: %v", err)
	}
	if storedLibraryLinks == "" || storedLibraryLinks == storedRunLinks {
		t.Fatalf("library sync links changed or collided: library=%q remote=%q", storedLibraryLinks, storedRunLinks)
	}
}

func TestRemoteRunPageURLUsesHostedCommandCenterRoute(t *testing.T) {
	got := remoteRunPageURL("https://apex.example/base", "test 1")
	want := "https://apex.example/base#/command-center/test%201"
	if got != want {
		t.Fatalf("remoteRunPageURL = %q, want %q", got, want)
	}
}
