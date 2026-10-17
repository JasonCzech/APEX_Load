package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestRemoteRunProxyPushesDependenciesCreatesStartsAndPolls(t *testing.T) {
	// Remote target is a loopback httptest server; opt past the SSRF guard.
	t.Setenv(remoteOutboundAllowPrivateHostsEnv, "1")
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	now := time.Now().UTC()
	if err := store.SaveProject(&storage.Project{ID: "local-project", Name: "Local Project", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}
	local := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: "127.0.0.1:8080", LibraryCredentials: NewMemoryLibraryCredentialStore()})
	script, err := local.runner.StoreScriptDurable(libraryTestScript("script-local", "Checkout Script", "local-project", "/checkout"))
	if err != nil {
		t.Fatalf("StoreScriptDurable: %v", err)
	}
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
	var remoteTestID string
	const remoteSecret = "raw-remote-runtime-secret"
	remoteTest := func(status string) *ManagedTest {
		return &ManagedTest{
			ID: remoteTestID, Name: "Remote Checkout", ProjectID: "remote-project", ScenarioID: "remote-scenario", Status: status,
			RuntimeSettings: &RuntimeSettingsOverride{
				Env: map[string]string{"API_TOKEN": remoteSecret},
				HTTP: &dsl.HTTPConfig{
					Auth:           dsl.AuthConfig{Password: remoteSecret},
					DefaultHeaders: map[string]string{"Authorization": remoteSecret},
				},
				BrowserConfig: &dsl.BrowserConfig{HTTPCredentials: dsl.BrowserHTTPCredentials{Password: remoteSecret}},
			},
		}
	}
	remoteHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("Authorization = %q, want none for unauthenticated HTTP fixture", r.Header.Get("Authorization"))
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
			remoteTestID = req.RequestedTestID
			writeJSON(w, http.StatusCreated, remoteTest("QUEUED"))
		case r.Method == http.MethodPost && remoteTestID != "" && r.URL.Path == "/api/v1/tests/"+remoteTestID+"/start":
			sawStart = true
			writeJSON(w, http.StatusOK, map[string]any{"status": "started", "test_id": remoteTestID, "test": remoteTest("RUNNING")})
		case r.Method == http.MethodPost && remoteTestID != "" && r.URL.Path == "/api/v1/tests/"+remoteTestID+"/pause":
			writeJSON(w, http.StatusOK, map[string]any{"status": "paused", "test": remoteTest("PAUSED")})
		case r.Method == http.MethodGet && remoteTestID != "" && r.URL.Path == "/api/v1/tests/"+remoteTestID:
			writeJSON(w, http.StatusOK, remoteTest("RUNNING"))
		default:
			writeError(w, http.StatusNotFound, r.Method+" "+r.URL.Path)
		}
	}))
	defer remoteHTTP.Close()

	local.libraryMu.Lock()
	local.libraryRemotes["workspace-1"] = &LibraryRemote{
		ID:                   "workspace-1",
		Name:                 "Hosted K8s",
		BaseURL:              remoteHTTP.URL,
		DefaultProjectID:     "remote-project",
		ProjectMappings:      map[string]string{"local-project": "remote-project"},
		AuthMethod:           "none",
		CredentialConfigured: false,
	}
	local.libraryMu.Unlock()
	if err := local.persistLibraryRemotes(); err != nil {
		t.Fatalf("persist remote fixture: %v", err)
	}

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
	if strings.Contains(createResp.Body.String(), remoteSecret) {
		t.Fatalf("create remote run response leaked untrusted remote secret: %s", createResp.Body.String())
	}
	var created RemoteRunProxyResponse
	if err := json.NewDecoder(createResp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if !sawImport || !sawTranslatedCreate || !sawStart {
		t.Fatalf("remote workflow incomplete: import=%t create=%t start=%t", sawImport, sawTranslatedCreate, sawStart)
	}
	if created.Link.RemoteTestID == "" || created.Link.RemoteTestID != remoteTestID || created.Link.Status != "RUNNING" {
		t.Fatalf("created link = %#v, want %s RUNNING", created.Link, remoteTestID)
	}

	getResp := httptest.NewRecorder()
	local.mux.ServeHTTP(getResp, httptest.NewRequest(http.MethodGet, "/api/v1/remotes/workspace-1/runs/"+remoteTestID, nil))
	if getResp.Code != http.StatusOK {
		t.Fatalf("get remote run status = %d body=%s", getResp.Code, getResp.Body.String())
	}
	if strings.Contains(getResp.Body.String(), remoteSecret) {
		t.Fatalf("get remote run response leaked untrusted remote secret: %s", getResp.Body.String())
	}
	pauseResp := httptest.NewRecorder()
	local.mux.ServeHTTP(pauseResp, httptest.NewRequest(http.MethodPost, "/api/v1/remotes/workspace-1/runs/"+remoteTestID+"/pause", nil))
	if pauseResp.Code != http.StatusOK {
		t.Fatalf("pause remote run status = %d body=%s", pauseResp.Code, pauseResp.Body.String())
	}
	if strings.Contains(pauseResp.Body.String(), remoteSecret) {
		t.Fatalf("action remote run response leaked untrusted remote secret: %s", pauseResp.Body.String())
	}
	rawRunLinks, err := store.GetSetting(remoteRunLinksSettingKey)
	if err != nil {
		t.Fatalf("GetSetting remote run links: %v", err)
	}
	if !strings.Contains(rawRunLinks, remoteTestID) {
		t.Fatalf("remote run links = %q, want %s", rawRunLinks, remoteTestID)
	}
	rawLibraryLinks, err := store.GetSetting(librarySyncLinksSettingKey)
	if err != nil {
		t.Fatalf("GetSetting library sync links: %v", err)
	}
	if rawLibraryLinks == "" || rawLibraryLinks == rawRunLinks {
		t.Fatalf("library sync links missing/collided: library=%q run=%q", rawLibraryLinks, rawRunLinks)
	}
}

func TestRemoteRunCreateDerivesAndEnforcesOneLocalAssetProject(t *testing.T) {
	t.Setenv(remoteOutboundAllowPrivateHostsEnv, "1")
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "remote-projects.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	for _, id := range []string{"project-a", "project-b"} {
		if err := store.SaveProject(&storage.Project{ID: id, Name: id, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	server := NewServer(ServerConfig{Store: store, LibraryCredentials: NewMemoryLibraryCredentialStore()})
	scriptA, err := server.runner.StoreScriptDurable(libraryTestScript("script-a", "A", "project-a", "/a"))
	if err != nil {
		t.Fatal(err)
	}
	scriptB, err := server.runner.StoreScriptDurable(libraryTestScript("script-b", "B", "project-b", "/b"))
	if err != nil {
		t.Fatal(err)
	}
	scenarioB, err := server.runner.StoreScenario(CreateScenarioRequest{Name: "B scenario", Duration: "30s", Groups: []CreateGroupEntry{{Name: "b", ScriptID: scriptB.ID, VUsers: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	remoteImports := 0
	remoteCreates := 0
	remoteHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/library/import":
			remoteImports++
			var req LibraryImportRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			response := LibraryImportResponse{SchemaVersion: libraryBundleSchemaVersion}
			for _, item := range req.Bundle.Items {
				response.Items = append(response.Items, LibraryImportResultItem{Kind: item.Kind, SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID, LocalItemID: "remote-" + normalizeLibraryKind(item.Kind), ContentHash: item.ContentHash, Action: "created"})
			}
			writeJSON(w, http.StatusCreated, response)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/tests":
			remoteCreates++
			var req CreateTestRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, http.StatusCreated, &ManagedTest{ID: req.RequestedTestID, Status: "QUEUED", ProjectID: "remote-project"})
		default:
			writeError(w, http.StatusNotFound, r.Method+" "+r.URL.Path)
		}
	}))
	defer remoteHTTP.Close()
	server.libraryMu.Lock()
	server.libraryRemotes["workspace"] = &LibraryRemote{ID: "workspace", Name: "Workspace", BaseURL: remoteHTTP.URL, DefaultProjectID: "remote-project", ProjectMappings: map[string]string{"project-b": "remote-project"}, AuthMethod: "none"}
	server.libraryMu.Unlock()
	if err := server.persistLibraryRemotes(); err != nil {
		t.Fatal(err)
	}

	validBody := marshalLibraryTestJSON(t, RemoteRunCreateRequest{CreateTestRequest: CreateTestRequest{Name: "valid", Duration: "30s", ScenarioID: scenarioB.ID}})
	valid := httptest.NewRecorder()
	server.mux.ServeHTTP(valid, httptest.NewRequest(http.MethodPost, "/api/v1/remotes/workspace/runs", bytes.NewReader(validBody)))
	if valid.Code != http.StatusCreated {
		t.Fatalf("derived project create = %d %s", valid.Code, valid.Body.String())
	}
	var created RemoteRunProxyResponse
	if err := json.NewDecoder(valid.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.Link.LocalProjectID != "project-b" {
		t.Fatalf("link project = %q, want project-b", created.Link.LocalProjectID)
	}
	if created.Link.RemoteProjectID != "remote-project" {
		t.Fatalf("remote link project = %q, want configured remote-project", created.Link.RemoteProjectID)
	}

	for name, request := range map[string]CreateTestRequest{
		"explicit mismatch": {Name: "mismatch", Duration: "30s", ProjectID: "project-a", ScenarioID: scenarioB.ID},
		"mixed groups":      {Name: "mixed", Duration: "30s", Groups: []CreateGroupEntry{{ScriptID: scriptA.ID, VUsers: 1}, {ScriptID: scriptB.ID, VUsers: 1}}},
		"unmapped project":  {Name: "unmapped", Duration: "30s", ScriptID: scriptA.ID, VUsers: 1},
	} {
		beforeImports, beforeCreates := remoteImports, remoteCreates
		body := marshalLibraryTestJSON(t, RemoteRunCreateRequest{CreateTestRequest: request})
		response := httptest.NewRecorder()
		server.mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/remotes/workspace/runs", bytes.NewReader(body)))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d body=%s", name, response.Code, response.Body.String())
		}
		if remoteImports != beforeImports || remoteCreates != beforeCreates {
			t.Fatalf("%s caused remote side effects: imports %d->%d creates %d->%d", name, beforeImports, remoteImports, beforeCreates, remoteCreates)
		}
	}

	beforeImports, beforeCreates := remoteImports, remoteCreates
	overrideBody := marshalLibraryTestJSON(t, RemoteRunCreateRequest{
		CreateTestRequest: CreateTestRequest{Name: "redirect", Duration: "30s", ScenarioID: scenarioB.ID},
		RemoteProjectID:   "remote-project-b",
		StartImmediately:  true,
	})
	override := httptest.NewRecorder()
	server.mux.ServeHTTP(override, httptest.NewRequest(http.MethodPost, "/api/v1/remotes/workspace/runs", bytes.NewReader(overrideBody)))
	if override.Code != http.StatusBadRequest {
		t.Fatalf("remote project override status = %d, want %d body=%s", override.Code, http.StatusBadRequest, override.Body.String())
	}
	if remoteImports != beforeImports || remoteCreates != beforeCreates {
		t.Fatalf("rejected remote project override caused side effects: imports %d->%d creates %d->%d", beforeImports, remoteImports, beforeCreates, remoteCreates)
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

	link, err := server.upsertRemoteRunLink(RemoteRunLink{
		RemoteID:        "workspace-a",
		RemoteTestID:    "remote-test-a",
		RemoteURL:       "https://apex.example/#/command-center/remote-test-a",
		LocalProjectID:  "project-a",
		LocalScenarioID: "scenario-a",
		RemoteProjectID: "remote-project-a",
		Status:          "QUEUED",
	})
	if err != nil {
		t.Fatalf("upsertRemoteRunLink: %v", err)
	}
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

func TestRemoteRunProxyRequiresExistingProjectScopedLink(t *testing.T) {
	t.Setenv(remoteOutboundAllowPrivateHostsEnv, "1")
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	remoteCalls := 0
	remoteHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		remoteCalls++
		writeJSON(w, http.StatusOK, &ManagedTest{ID: "unlinked", Status: "RUNNING"})
	}))
	defer remoteHTTP.Close()
	server := NewServer(ServerConfig{Store: store, LibraryCredentials: NewMemoryLibraryCredentialStore()})
	server.libraryMu.Lock()
	server.libraryRemotes["workspace"] = &LibraryRemote{ID: "workspace", BaseURL: remoteHTTP.URL, ProjectMappings: map[string]string{"project-a": "project-a"}, AuthMethod: "none"}
	server.libraryMu.Unlock()
	if err := server.persistLibraryRemotes(); err != nil {
		t.Fatal(err)
	}

	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/api/v1/remotes/workspace/runs/unlinked", nil),
		httptest.NewRequest(http.MethodPost, "/api/v1/remotes/workspace/runs/unlinked/pause", nil),
	} {
		response := httptest.NewRecorder()
		server.mux.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s %s status = %d body=%s", request.Method, request.URL.Path, response.Code, response.Body.String())
		}
	}
	if remoteCalls != 0 {
		t.Fatalf("unlinked requests reached remote workspace %d times", remoteCalls)
	}
	if links := server.loadRemoteRunLinks(); len(links) != 0 {
		t.Fatalf("unlinked proxy request created authorization links: %#v", links)
	}

	// Legacy records without a local project are not claimable through a read.
	server.upsertRemoteRunLink(RemoteRunLink{RemoteID: "workspace", RemoteTestID: "legacy", Status: "RUNNING"})
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/remotes/workspace/runs/legacy", nil))
	if response.Code != http.StatusNotFound || remoteCalls != 0 {
		t.Fatalf("legacy unscoped link status/calls = %d/%d body=%s", response.Code, remoteCalls, response.Body.String())
	}
}

func TestRemoteRunProxyRefreshesRemoteRevocationAcrossReplicas(t *testing.T) {
	t.Setenv(remoteOutboundAllowPrivateHostsEnv, "1")
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "remote-refresh.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var firstCalls, secondCalls int
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		firstCalls++
		writeJSON(w, http.StatusOK, &ManagedTest{ID: "remote-test", Status: "RUNNING"})
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondCalls++
		writeJSON(w, http.StatusOK, &ManagedTest{ID: "remote-test", Status: "PAUSED"})
	}))
	defer second.Close()
	saveRemote := func(baseURL string) {
		payload, err := json.Marshal([]LibraryRemote{{ID: "workspace", Name: "Workspace", BaseURL: baseURL, AuthMethod: "none"}})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SaveSetting(libraryRemotesSettingKey, string(payload)); err != nil {
			t.Fatal(err)
		}
	}
	saveRemote(first.URL)
	serverA := NewServer(ServerConfig{Store: store})
	serverB := NewServer(ServerConfig{Store: store})
	if _, err := serverA.upsertRemoteRunLink(RemoteRunLink{LocalRunID: "local", RemoteID: "workspace", RemoteTestID: "remote-test", LocalProjectID: storage.DefaultProjectID}); err != nil {
		t.Fatal(err)
	}
	request := func() int {
		response := httptest.NewRecorder()
		serverB.mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/remotes/workspace/runs/remote-test", nil))
		return response.Code
	}
	if status := request(); status != http.StatusOK || firstCalls != 1 || secondCalls != 0 {
		t.Fatalf("initial status/calls = %d/%d/%d", status, firstCalls, secondCalls)
	}
	saveRemote(second.URL)
	if status := request(); status != http.StatusOK || firstCalls != 1 || secondCalls != 1 {
		t.Fatalf("updated status/calls = %d/%d/%d", status, firstCalls, secondCalls)
	}
	if err := store.SaveSetting(libraryRemotesSettingKey, `[]`); err != nil {
		t.Fatal(err)
	}
	if status := request(); status != http.StatusNotFound || firstCalls != 1 || secondCalls != 1 {
		t.Fatalf("revoked status/calls = %d/%d/%d", status, firstCalls, secondCalls)
	}
}

func TestResolveRemoteRunLinkProjectIDFailsClosed(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})
	server.upsertRemoteRunLink(RemoteRunLink{RemoteID: "workspace", RemoteTestID: "run-a", LocalProjectID: "project-a"})

	request := httptest.NewRequest(http.MethodGet, "/api/v1/remotes/workspace/runs/run-a", nil)
	request.SetPathValue("id", "workspace")
	request.SetPathValue("remote_test_id", "run-a")
	projectID, status, message := resolveRemoteRunLinkProjectID(request, store)
	if projectID != "project-a" || status != 0 || message != "" {
		t.Fatalf("scoped resolver = (%q, %d, %q)", projectID, status, message)
	}

	request.SetPathValue("remote_test_id", "unknown")
	if _, status, _ := resolveRemoteRunLinkProjectID(request, store); status != http.StatusNotFound {
		t.Fatalf("unknown resolver status = %d, want %d", status, http.StatusNotFound)
	}
}

func TestRemoteRunPageURLUsesHostedCommandCenterRoute(t *testing.T) {
	got := remoteRunPageURL("https://apex.example/base", "test 1")
	want := "https://apex.example/base#/command-center/test%201"
	if got != want {
		t.Fatalf("remoteRunPageURL = %q, want %q", got, want)
	}
}

func TestUpsertRemoteRunLinkConcurrentNoLostUpdate(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})

	const n = 25
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := server.upsertRemoteRunLink(RemoteRunLink{
				RemoteID:     fmt.Sprintf("remote-%d", i),
				RemoteTestID: fmt.Sprintf("test-%d", i),
				Status:       "running",
			})
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("upsertRemoteRunLink: %v", err)
		}
	}

	// Without serialization, concurrent read-modify-write of the single links blob
	// would lose updates and persist fewer than n links.
	if links := server.loadRemoteRunLinks(); len(links) != n {
		t.Fatalf("persisted %d remote-run links, want %d (lost update)", len(links), n)
	}
}

type conflictOnceRemoteRunLinkStore struct {
	storage.Store
	once    sync.Once
	seedErr error
}

func (s *conflictOnceRemoteRunLinkStore) CompareAndSwapSetting(key, expected, value string) (bool, error) {
	conflicted := false
	if key == remoteRunLinksSettingKey {
		s.once.Do(func() {
			conflicted = true
			seed, _ := json.Marshal([]RemoteRunLink{{
				LocalRunID:     "other-local-run",
				RemoteID:       "other-workspace",
				RemoteTestID:   "other-test",
				LocalProjectID: "other-project",
			}})
			s.seedErr = s.Store.SaveSetting(key, string(seed))
		})
	}
	if s.seedErr != nil {
		return false, s.seedErr
	}
	if conflicted {
		return false, nil
	}
	cas, ok := s.Store.(interface {
		CompareAndSwapSetting(string, string, string) (bool, error)
	})
	if !ok {
		return false, errors.New("wrapped store does not support setting CAS")
	}
	return cas.CompareAndSwapSetting(key, expected, value)
}

func TestUpsertRemoteRunLinkRetriesCASWithoutLosingOtherReplica(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: &conflictOnceRemoteRunLinkStore{Store: store}})
	if _, err := server.upsertRemoteRunLink(RemoteRunLink{
		LocalRunID:     "local-run",
		RemoteID:       "workspace",
		RemoteTestID:   "test",
		LocalProjectID: "project",
	}); err != nil {
		t.Fatalf("upsertRemoteRunLink: %v", err)
	}
	links := server.loadRemoteRunLinks()
	if len(links) != 2 {
		t.Fatalf("persisted links = %#v, want both concurrent entries", links)
	}
}

type noCASSettingsStore struct{ Store storage.Store }

func TestRemoteRunLinkMutationFailsClosedWithoutCASInHA(t *testing.T) {
	baseStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "remote-ha-cas.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer baseStore.Close()
	server := NewServer(ServerConfig{Store: storeOverlay(&noCASSettingsStore{Store: baseStore}), HAExecutionLeadership: true})
	if _, err := server.upsertRemoteRunLink(RemoteRunLink{LocalRunID: "run", RemoteID: "workspace", RemoteTestID: "test", LocalProjectID: storage.DefaultProjectID}); err == nil || !strings.Contains(err.Error(), "HA storage must support compare-and-swap") {
		t.Fatalf("HA non-CAS mutation error = %v", err)
	}
}

func TestUpsertRemoteRunLinkRefusesAuthorizationAnchorReassignment(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})
	if _, err := server.upsertRemoteRunLink(RemoteRunLink{
		LocalRunID: "run-a", RemoteID: "workspace", RemoteTestID: "test", LocalProjectID: "project-a",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := server.upsertRemoteRunLink(RemoteRunLink{
		LocalRunID: "run-b", RemoteID: "workspace", RemoteTestID: "test", LocalProjectID: "project-b",
	}); !errors.Is(err, errRemoteRunLinkIdentityConflict) {
		t.Fatalf("conflicting upsert error = %v, want identity conflict", err)
	}
	link, ok := server.existingRemoteRunLink("workspace", "test")
	if !ok || link.LocalRunID != "run-a" || link.LocalProjectID != "project-a" {
		t.Fatalf("authorization anchor changed after conflicting upsert: %#v, ok=%t", link, ok)
	}
}

type failingRemoteRunLinkCASStore struct {
	storage.Store
	onFailure func()
	once      sync.Once
}

func (s *failingRemoteRunLinkCASStore) CompareAndSwapSetting(key, expected, value string) (bool, error) {
	if key == remoteRunLinksSettingKey {
		s.once.Do(func() {
			if s.onFailure != nil {
				s.onFailure()
			}
		})
		return false, errors.New("injected remote link persistence failure")
	}
	cas, ok := s.Store.(interface {
		CompareAndSwapSetting(string, string, string) (bool, error)
	})
	if !ok {
		return false, errors.New("wrapped store does not support setting CAS")
	}
	return cas.CompareAndSwapSetting(key, expected, value)
}

func TestCreateRemoteRunDoesNotStartWithoutDurableAuthorizationLink(t *testing.T) {
	t.Setenv(remoteOutboundAllowPrivateHostsEnv, "1")
	baseStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer baseStore.Close()
	var created, started, deleted int
	var remoteTestID string
	remoteHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/tests":
			created++
			var req CreateTestRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			remoteTestID = req.RequestedTestID
			writeJSON(w, http.StatusCreated, &ManagedTest{ID: remoteTestID, Status: "QUEUED"})
		case r.Method == http.MethodPost && remoteTestID != "" && r.URL.Path == "/api/v1/tests/"+remoteTestID+"/start":
			started++
			writeJSON(w, http.StatusOK, map[string]string{"status": "started"})
		case r.Method == http.MethodDelete && remoteTestID != "" && r.URL.Path == "/api/v1/tests/"+remoteTestID:
			deleted++
			w.WriteHeader(http.StatusNoContent)
		default:
			writeError(w, http.StatusNotFound, r.Method+" "+r.URL.Path)
		}
	}))
	defer remoteHTTP.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := NewServer(ServerConfig{Store: &failingRemoteRunLinkCASStore{Store: baseStore, onFailure: cancel}, LibraryCredentials: NewMemoryLibraryCredentialStore()})
	server.libraryMu.Lock()
	server.libraryRemotes["workspace"] = &LibraryRemote{ID: "workspace", BaseURL: remoteHTTP.URL, ProjectMappings: map[string]string{"project-a": "project-a"}, AuthMethod: "none"}
	server.libraryMu.Unlock()
	if err := server.persistLibraryRemotes(); err != nil {
		t.Fatal(err)
	}
	body := marshalLibraryTestJSON(t, RemoteRunCreateRequest{
		CreateTestRequest: CreateTestRequest{Name: "Remote", Duration: "30s", ProjectID: "project-a"},
		StartImmediately:  true,
	})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/remotes/workspace/runs", bytes.NewReader(body)).WithContext(ctx)
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusBadGateway {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if created != 1 || deleted != 1 || started != 0 {
		t.Fatalf("remote calls create/delete/start = %d/%d/%d, want 1/1/0", created, deleted, started)
	}
}

func TestRemoteRunCleanupOutboxRetriesAfterRestart(t *testing.T) {
	t.Setenv(remoteOutboundAllowPrivateHostsEnv, "1")
	baseStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "remote-cleanup.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer baseStore.Close()
	store := &failingRemoteRunLinkCASStore{Store: baseStore}
	var remoteMu sync.Mutex
	allowDelete := false
	deleteCalls := 0
	var remoteTestID string
	remoteHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/tests":
			var req CreateTestRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			remoteTestID = req.RequestedTestID
			writeJSON(w, http.StatusCreated, &ManagedTest{ID: remoteTestID, Status: "QUEUED"})
		case r.Method == http.MethodDelete && remoteTestID != "" && r.URL.Path == "/api/v1/tests/"+remoteTestID:
			remoteMu.Lock()
			deleteCalls++
			allowed := allowDelete
			remoteMu.Unlock()
			if !allowed {
				writeError(w, http.StatusServiceUnavailable, "retry")
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			writeError(w, http.StatusNotFound, r.Method+" "+r.URL.Path)
		}
	}))
	defer remoteHTTP.Close()
	server := NewServer(ServerConfig{Store: store})
	defer server.stopRemoteRunCleanupRetry(context.Background())
	server.libraryMu.Lock()
	server.libraryRemotes["workspace"] = &LibraryRemote{ID: "workspace", Name: "Workspace", BaseURL: remoteHTTP.URL, ProjectMappings: map[string]string{storage.DefaultProjectID: storage.DefaultProjectID}, AuthMethod: "none"}
	server.libraryMu.Unlock()
	if err := server.persistLibraryRemotes(); err != nil {
		t.Fatal(err)
	}
	body := marshalLibraryTestJSON(t, RemoteRunCreateRequest{CreateTestRequest: CreateTestRequest{Name: "Remote", Duration: "30s"}})
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/remotes/workspace/runs", bytes.NewReader(body)))
	if response.Code != http.StatusBadGateway {
		t.Fatalf("create status = %d body=%s", response.Code, response.Body.String())
	}
	pending, err := server.loadRemoteRunCleanups()
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending cleanup = %#v, %v", pending, err)
	}
	remoteMu.Lock()
	allowDelete = true
	remoteMu.Unlock()
	restarted := NewServer(ServerConfig{Store: store})
	defer restarted.stopRemoteRunCleanupRetry(context.Background())
	restarted.remoteCleanupRetryMu.Lock()
	constructorStartedRetry := restarted.remoteCleanupCancel != nil
	restarted.remoteCleanupRetryMu.Unlock()
	if constructorStartedRetry {
		t.Fatal("NewServer started remote cleanup before the server lifecycle began")
	}
	// Startup, rather than construction, owns asynchronous outbox recovery.
	restarted.startPendingRemoteRunCleanupRetry()
	deadline := time.Now().Add(3 * time.Second)
	for {
		pending, err = restarted.loadRemoteRunCleanups()
		if err != nil {
			t.Fatal(err)
		}
		if len(pending) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("cleanup outbox was not reconciled after restart: %#v", pending)
		}
		time.Sleep(10 * time.Millisecond)
	}
	remoteMu.Lock()
	deletes := deleteCalls
	remoteMu.Unlock()
	if deletes < 2 {
		t.Fatalf("delete calls = %d, want initial failure plus restart retry", deletes)
	}
}

func TestRemoteRunPersistsCleanupIntentBeforeCreateAndKeepsLinkedRun(t *testing.T) {
	t.Setenv(remoteOutboundAllowPrivateHostsEnv, "1")
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "remote-intent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var (
		remoteTestID string
		sawIntent    bool
		deleteCalls  int
	)
	remoteHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/tests":
			var req CreateTestRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			remoteTestID = req.RequestedTestID
			raw, err := store.GetSetting(remoteRunCleanupSettingKey)
			sawIntent = err == nil && remoteTestID != "" && strings.Contains(raw, remoteTestID)
			writeJSON(w, http.StatusCreated, &ManagedTest{ID: remoteTestID, Status: "QUEUED", ProjectID: "remote-project"})
		case r.Method == http.MethodDelete && remoteTestID != "" && r.URL.Path == "/api/v1/tests/"+remoteTestID:
			deleteCalls++
			w.WriteHeader(http.StatusNoContent)
		default:
			writeError(w, http.StatusNotFound, r.Method+" "+r.URL.Path)
		}
	}))
	defer remoteHTTP.Close()

	server := NewServer(ServerConfig{Store: store})
	remote := LibraryRemote{ID: "workspace", Name: "Workspace", BaseURL: remoteHTTP.URL, DefaultProjectID: "remote-project", ProjectMappings: map[string]string{"project-a": "remote-project"}, AuthMethod: "none"}
	server.libraryMu.Lock()
	server.libraryRemotes[remote.ID] = &remote
	server.libraryMu.Unlock()
	if err := server.persistLibraryRemotes(); err != nil {
		t.Fatal(err)
	}
	body := marshalLibraryTestJSON(t, RemoteRunCreateRequest{CreateTestRequest: CreateTestRequest{Name: "Remote", Duration: "30s", ProjectID: "project-a"}})
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/remotes/workspace/runs", bytes.NewReader(body)))
	if response.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", response.Code, response.Body.String())
	}
	if !sawIntent {
		t.Fatal("remote POST was observed before its cleanup intent was durable")
	}

	// Model a crash after the link commit but before intent removal. Restart
	// reconciliation must remove only the stale intent, not the linked run.
	stale := newRemoteRunCleanupRecord(remote, remoteTestID)
	if err := server.enqueueRemoteRunCleanup(stale); err != nil {
		t.Fatal(err)
	}
	if err := server.attemptRemoteRunCleanup(stale); err != nil {
		t.Fatal(err)
	}
	if deleteCalls != 0 {
		t.Fatalf("linked remote run received %d cleanup DELETE calls", deleteCalls)
	}
	pending, err := server.loadRemoteRunCleanups()
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending cleanup = %#v, %v; want empty", pending, err)
	}
}

func TestRemoteRunCleanupOutboxRetriesWithoutRestart(t *testing.T) {
	t.Setenv(remoteOutboundAllowPrivateHostsEnv, "1")
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "remote-cleanup-live-retry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var calls atomic.Int32
	remoteHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/api/v1/tests/remote-test" {
			writeError(w, http.StatusNotFound, r.Method+" "+r.URL.Path)
			return
		}
		if calls.Add(1) == 1 {
			writeError(w, http.StatusServiceUnavailable, "transient")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer remoteHTTP.Close()

	server := NewServer(ServerConfig{Store: store})
	record := newRemoteRunCleanupRecord(LibraryRemote{ID: "workspace", BaseURL: remoteHTTP.URL, AuthMethod: "none"}, "remote-test")
	if err := server.enqueueRemoteRunCleanup(record); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		server.runRemoteRunCleanupRetry(ctx, 5*time.Millisecond)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("live cleanup retry did not finish")
	}
	pending, err := server.loadRemoteRunCleanups()
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending cleanup = %#v, %v; want empty", pending, err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("delete calls = %d, want one transient failure and one live retry", got)
	}
}

func TestPendingRemoteRunCleanupFencesRemoteCredentialMutationAndDeletion(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "remote-cleanup-config-fence.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})
	remote := LibraryRemote{
		ID: "workspace", Name: "Workspace", BaseURL: "https://remote.example", AuthMethod: "none",
		ProjectMappings: map[string]string{"local-project": "remote-project"},
	}
	server.libraryMu.Lock()
	server.libraryRemotes[remote.ID] = &remote
	server.libraryMu.Unlock()
	if err := server.persistLibraryRemotes(); err != nil {
		t.Fatal(err)
	}
	if err := server.enqueueRemoteRunCleanup(newRemoteRunCleanupRecord(remote, "remote-test")); err != nil {
		t.Fatal(err)
	}

	updateBody := marshalLibraryTestJSON(t, LibraryRemoteRequest{BaseURL: "https://different.example"})
	update := httptest.NewRecorder()
	server.mux.ServeHTTP(update, httptest.NewRequest(http.MethodPut, "/api/v1/library/remotes/workspace", bytes.NewReader(updateBody)))
	if update.Code != http.StatusConflict {
		t.Fatalf("update status = %d, want %d body=%s", update.Code, http.StatusConflict, update.Body.String())
	}
	remove := httptest.NewRecorder()
	server.mux.ServeHTTP(remove, httptest.NewRequest(http.MethodDelete, "/api/v1/library/remotes/workspace", nil))
	if remove.Code != http.StatusConflict {
		t.Fatalf("delete status = %d, want %d body=%s", remove.Code, http.StatusConflict, remove.Body.String())
	}
	stored, ok := server.freshLibraryRemote(remote.ID)
	if !ok || stored.BaseURL != remote.BaseURL {
		t.Fatalf("remote changed while cleanup pending: %#v", stored)
	}
}

func TestRemoteRunNetworkWaitDoesNotHoldRemoteConfigLock(t *testing.T) {
	t.Setenv(remoteOutboundAllowPrivateHostsEnv, "1")
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "remote-config-progress.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	requestEntered := make(chan struct{})
	releaseRequest := make(chan struct{})
	remoteHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(requestEntered)
		<-releaseRequest
		writeJSON(w, http.StatusOK, &ManagedTest{ID: "remote-run", Status: "RUNNING"})
	}))
	defer remoteHTTP.Close()
	server := NewServer(ServerConfig{Store: store})
	remote := LibraryRemote{ID: "workspace", Name: "Workspace", BaseURL: remoteHTTP.URL, AuthMethod: "none"}
	server.libraryMu.Lock()
	server.libraryRemotes[remote.ID] = &remote
	server.libraryMu.Unlock()
	if err := server.persistLibraryRemotes(); err != nil {
		t.Fatal(err)
	}
	if _, err := server.upsertRemoteRunLink(RemoteRunLink{
		LocalRunID: "local-run", RemoteID: remote.ID, RemoteTestID: "remote-run", LocalProjectID: storage.DefaultProjectID,
	}); err != nil {
		t.Fatal(err)
	}

	responseDone := make(chan int, 1)
	go func() {
		response := httptest.NewRecorder()
		server.mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/remotes/workspace/runs/remote-run", nil))
		responseDone <- response.Code
	}()
	select {
	case <-requestEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("remote request did not start")
	}
	writerAcquired := make(chan struct{})
	go func() {
		server.remoteConfigMu.Lock()
		close(writerAcquired)
		server.remoteConfigMu.Unlock()
	}()
	select {
	case <-writerAcquired:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("remote config writer was blocked by stalled outbound request")
	}
	close(releaseRequest)
	select {
	case status := <-responseDone:
		if status != http.StatusOK {
			t.Fatalf("remote run response status = %d", status)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("remote run response did not complete")
	}
}

func TestRemoteRunCreateFencesRemoteMutationBeforeCleanupIntent(t *testing.T) {
	t.Setenv(remoteOutboundAllowPrivateHostsEnv, "1")
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "remote-create-fence.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	if err := store.SaveProject(&storage.Project{ID: "local-project", Name: "Local", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{Store: store, LibraryCredentials: NewMemoryLibraryCredentialStore()})
	script, err := server.runner.StoreScriptDurable(libraryTestScript("create-fence-script", "Create Fence", "local-project", "/fence"))
	if err != nil {
		t.Fatal(err)
	}

	importEntered := make(chan struct{})
	releaseImport := make(chan struct{})
	var enterOnce sync.Once
	var releaseOnce sync.Once
	releaseBlockedImport := func() { releaseOnce.Do(func() { close(releaseImport) }) }
	t.Cleanup(releaseBlockedImport)
	remoteHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/library/import":
			var req LibraryImportRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			enterOnce.Do(func() { close(importEntered) })
			<-releaseImport
			response := LibraryImportResponse{SchemaVersion: libraryBundleSchemaVersion}
			for _, item := range req.Bundle.Items {
				response.Items = append(response.Items, LibraryImportResultItem{
					Kind: item.Kind, SourceInstanceID: item.SourceInstanceID, SourceItemID: item.SourceItemID,
					LocalItemID: "remote-" + normalizeLibraryKind(item.Kind), ContentHash: item.ContentHash, Action: "created",
				})
			}
			writeJSON(w, http.StatusCreated, response)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/tests":
			var req CreateTestRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, http.StatusCreated, &ManagedTest{ID: req.RequestedTestID, Status: "QUEUED", ProjectID: "remote-project"})
		default:
			writeError(w, http.StatusNotFound, r.Method+" "+r.URL.Path)
		}
	}))
	defer remoteHTTP.Close()
	remote := LibraryRemote{
		ID: "workspace", Name: "Workspace", BaseURL: remoteHTTP.URL, AuthMethod: "none", DefaultProjectID: "remote-project",
		ProjectMappings: map[string]string{"local-project": "remote-project"},
	}
	server.libraryMu.Lock()
	server.libraryRemotes[remote.ID] = &remote
	server.libraryMu.Unlock()
	if err := server.persistLibraryRemotes(); err != nil {
		t.Fatal(err)
	}

	body := marshalLibraryTestJSON(t, RemoteRunCreateRequest{CreateTestRequest: CreateTestRequest{
		Name: "fenced", Duration: "30s", ScriptID: script.ID, VUsers: 1,
	}})
	createDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		response := httptest.NewRecorder()
		server.mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/remotes/workspace/runs", bytes.NewReader(body)))
		createDone <- response
	}()
	select {
	case <-importEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("remote create did not reach the pre-cleanup preparation window")
	}

	remove := httptest.NewRecorder()
	server.mux.ServeHTTP(remove, httptest.NewRequest(http.MethodDelete, "/api/v1/library/remotes/workspace", nil))
	if remove.Code != http.StatusConflict {
		t.Fatalf("delete during create = %d, want %d body=%s", remove.Code, http.StatusConflict, remove.Body.String())
	}
	update := httptest.NewRecorder()
	updateBody := marshalLibraryTestJSON(t, LibraryRemoteRequest{BaseURL: remoteHTTP.URL})
	server.mux.ServeHTTP(update, httptest.NewRequest(http.MethodPut, "/api/v1/library/remotes/workspace", bytes.NewReader(updateBody)))
	if update.Code != http.StatusConflict {
		t.Fatalf("transport update during create = %d, want %d body=%s", update.Code, http.StatusConflict, update.Body.String())
	}

	releaseBlockedImport()
	select {
	case response := <-createDone:
		if response.Code != http.StatusCreated {
			t.Fatalf("remote create = %d body=%s", response.Code, response.Body.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("remote create did not complete")
	}
	server.remoteConfigMu.Lock()
	inFlight := server.remoteRunCreateInFlightLocked(remote.ID)
	server.remoteConfigMu.Unlock()
	if inFlight {
		t.Fatal("remote create reservation was not retired after cleanup intent became durable")
	}
}

func TestCreateTestRequestedIDCannotReplaceExistingLifecycleRow(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "requested-test-id.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	if err := store.SaveProject(&storage.Project{ID: "project-a", Name: "Project A", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{Store: store})
	script, err := server.runner.StoreScriptDurable(libraryTestScript("requested-id-script", "Requested ID", "project-a", "/requested"))
	if err != nil {
		t.Fatal(err)
	}
	requestedID := uuid.NewString()
	req := CreateTestRequest{
		RequestedTestID: requestedID,
		Name:            "first",
		Duration:        "30s",
		ProjectID:       "project-a",
		ScriptID:        script.ID,
		VUsers:          1,
	}
	created, err := server.runner.CreateTest(req)
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != requestedID {
		t.Fatalf("created id = %q, want %q", created.ID, requestedID)
	}
	endedAt := time.Now().UTC()
	if err := store.UpdateTestStatus(requestedID, "COMPLETE", &endedAt, ""); err != nil {
		t.Fatal(err)
	}
	req.Name = "replacement"
	if _, err := server.runner.CreateTest(req); !errors.Is(err, errTestIdentityCollision) {
		t.Fatalf("duplicate requested id error = %v", err)
	}
	stored, err := store.GetTest(requestedID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Name != "first" || stored.Status != "COMPLETE" || stored.EndTime == nil {
		t.Fatalf("existing lifecycle row was replaced: %#v", stored)
	}
}
