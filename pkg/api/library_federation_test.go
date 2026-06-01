package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestLibraryExportImportScenarioIncludesScriptDependency(t *testing.T) {
	source := NewServer(ServerConfig{})
	target := NewServer(ServerConfig{})

	script := source.runner.StoreScript(libraryTestScript("script-checkout", "Checkout API", "project-a", "/checkout"))
	scenario, err := source.runner.StoreScenario(CreateScenarioRequest{
		Name:     "Checkout Smoke",
		Duration: "1m",
		Groups: []CreateGroupEntry{{
			Name:     "checkout",
			ScriptID: script.ID,
			VUsers:   3,
		}},
	})
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}

	exportBody := marshalLibraryTestJSON(t, LibraryTransferRequest{Kind: "scenario", ID: scenario.ID, IncludeDependencies: true})
	exportResp := httptest.NewRecorder()
	source.mux.ServeHTTP(exportResp, httptest.NewRequest(http.MethodPost, "/api/v1/library/export", bytes.NewReader(exportBody)))
	if exportResp.Code != http.StatusOK {
		t.Fatalf("export status = %d body=%s", exportResp.Code, exportResp.Body.String())
	}
	var bundle LibraryBundle
	if err := json.NewDecoder(exportResp.Body).Decode(&bundle); err != nil {
		t.Fatalf("decode export: %v", err)
	}
	if len(bundle.Items) != 2 {
		t.Fatalf("bundle items = %d, want scenario plus script dependency", len(bundle.Items))
	}

	importBody := marshalLibraryTestJSON(t, LibraryImportRequest{Bundle: bundle, ProjectID: "project-local"})
	importResp := httptest.NewRecorder()
	target.mux.ServeHTTP(importResp, httptest.NewRequest(http.MethodPost, "/api/v1/library/import", bytes.NewReader(importBody)))
	if importResp.Code != http.StatusCreated {
		t.Fatalf("import status = %d body=%s", importResp.Code, importResp.Body.String())
	}
	var imported LibraryImportResponse
	if err := json.NewDecoder(importResp.Body).Decode(&imported); err != nil {
		t.Fatalf("decode import: %v", err)
	}
	if len(imported.Items) != 2 {
		t.Fatalf("imported items = %d, want script and scenario", len(imported.Items))
	}
	if len(target.runner.ListScriptsFiltered("project-local", nil)) != 1 {
		t.Fatalf("target scripts = %d, want 1", len(target.runner.ListScriptsFiltered("project-local", nil)))
	}
	if len(target.runner.ListScenariosFiltered("project-local")) != 1 {
		t.Fatalf("target scenarios = %d, want 1", len(target.runner.ListScenariosFiltered("project-local")))
	}
}

func TestLibraryRemotePullAndPushScriptBetweenControllers(t *testing.T) {
	host := NewServer(ServerConfig{})
	local := NewServer(ServerConfig{})
	hostScript := host.runner.StoreScript(libraryTestScript("host-script", "Hosted Checkout", "team-project", "/hosted"))

	hostHTTP := httptest.NewServer(host.mux)
	defer hostHTTP.Close()
	remote := createLibraryRemoteForTest(t, local, hostHTTP.URL, "team-project")

	pullBody := marshalLibraryTestJSON(t, LibraryTransferRequest{Kind: "script", ID: hostScript.ID})
	pullResp := httptest.NewRecorder()
	local.mux.ServeHTTP(pullResp, httptest.NewRequest(http.MethodPost, "/api/v1/library/remotes/"+remote.ID+"/pull", bytes.NewReader(pullBody)))
	if pullResp.Code != http.StatusCreated {
		t.Fatalf("pull status = %d body=%s", pullResp.Code, pullResp.Body.String())
	}
	if got := len(local.runner.ListScriptsFiltered("team-project", nil)); got != 1 {
		t.Fatalf("local pulled scripts = %d, want 1", got)
	}

	localScript := local.runner.StoreScript(libraryTestScript("local-script", "Desktop Draft", "team-project", "/desktop"))
	pushBody := marshalLibraryTestJSON(t, LibraryTransferRequest{Kind: "script", ID: localScript.ID})
	pushResp := httptest.NewRecorder()
	local.mux.ServeHTTP(pushResp, httptest.NewRequest(http.MethodPost, "/api/v1/library/remotes/"+remote.ID+"/push", bytes.NewReader(pushBody)))
	if pushResp.Code != http.StatusOK {
		t.Fatalf("push status = %d body=%s", pushResp.Code, pushResp.Body.String())
	}
	var pushed LibraryRemoteTransferResponse
	if err := json.NewDecoder(pushResp.Body).Decode(&pushed); err != nil {
		t.Fatalf("decode push: %v", err)
	}
	hostCopyID := ""
	for _, item := range pushed.Import.Items {
		if item.Kind == "script" {
			hostCopyID = item.LocalItemID
		}
	}
	if hostCopyID == "" {
		t.Fatal("push did not return host copy id")
	}
	if got := len(host.runner.ListScriptsFiltered("team-project", nil)); got != 2 {
		t.Fatalf("host scripts after push = %d, want 2", got)
	}

	if _, ok := local.runner.UpdateScript(localScript.ID, libraryTestScript(localScript.ID, "Desktop Draft", "team-project", "/desktop-v2")); !ok {
		t.Fatal("update local script failed")
	}
	pushAgainResp := httptest.NewRecorder()
	local.mux.ServeHTTP(pushAgainResp, httptest.NewRequest(http.MethodPost, "/api/v1/library/remotes/"+remote.ID+"/push", bytes.NewReader(pushBody)))
	if pushAgainResp.Code != http.StatusOK {
		t.Fatalf("second push status = %d body=%s", pushAgainResp.Code, pushAgainResp.Body.String())
	}
	if got := len(host.runner.ListScriptsFiltered("team-project", nil)); got != 2 {
		t.Fatalf("host scripts after second push = %d, want same host copy count", got)
	}
	hostCopy, ok := host.runner.GetScript(hostCopyID)
	if !ok || hostCopy.Script.Actions[0].Target != "/desktop-v2" {
		t.Fatalf("host copy target = %#v, want updated /desktop-v2", hostCopy)
	}
}

func TestLibraryPullDetectsLocalAndRemoteConflict(t *testing.T) {
	host := NewServer(ServerConfig{})
	local := NewServer(ServerConfig{})
	hostScript := host.runner.StoreScript(libraryTestScript("host-conflict", "Hosted Conflict", "team-project", "/v1"))

	hostHTTP := httptest.NewServer(host.mux)
	defer hostHTTP.Close()
	remote := createLibraryRemoteForTest(t, local, hostHTTP.URL, "team-project")

	pullBody := marshalLibraryTestJSON(t, LibraryTransferRequest{Kind: "script", ID: hostScript.ID})
	pullResp := httptest.NewRecorder()
	local.mux.ServeHTTP(pullResp, httptest.NewRequest(http.MethodPost, "/api/v1/library/remotes/"+remote.ID+"/pull", bytes.NewReader(pullBody)))
	if pullResp.Code != http.StatusCreated {
		t.Fatalf("initial pull status = %d body=%s", pullResp.Code, pullResp.Body.String())
	}
	var pulled LibraryRemoteTransferResponse
	if err := json.NewDecoder(pullResp.Body).Decode(&pulled); err != nil {
		t.Fatalf("decode pull: %v", err)
	}
	localID := ""
	for _, item := range pulled.Import.Items {
		if item.Kind == "script" {
			localID = item.LocalItemID
		}
	}
	if localID == "" {
		t.Fatal("pull did not return imported script id")
	}

	local.runner.UpdateScript(localID, libraryTestScript(localID, "Local Edited Conflict", "team-project", "/local-v2"))
	host.runner.UpdateScript(hostScript.ID, libraryTestScript(hostScript.ID, "Remote Edited Conflict", "team-project", "/remote-v2"))

	conflictResp := httptest.NewRecorder()
	local.mux.ServeHTTP(conflictResp, httptest.NewRequest(http.MethodPost, "/api/v1/library/remotes/"+remote.ID+"/pull", bytes.NewReader(pullBody)))
	if conflictResp.Code != http.StatusConflict {
		t.Fatalf("conflict pull status = %d body=%s", conflictResp.Code, conflictResp.Body.String())
	}
	var conflict LibraryRemoteTransferResponse
	if err := json.NewDecoder(conflictResp.Body).Decode(&conflict); err != nil {
		t.Fatalf("decode conflict: %v", err)
	}
	if conflict.Import == nil || len(conflict.Import.Conflicts) != 1 {
		t.Fatalf("conflicts = %#v, want one conflict", conflict.Import)
	}
}

func createLibraryRemoteForTest(t *testing.T, server *Server, baseURL string, projectID string) LibraryRemote {
	t.Helper()
	body := marshalLibraryTestJSON(t, LibraryRemoteRequest{
		Name:             "K8s Host",
		BaseURL:          baseURL,
		DefaultProjectID: projectID,
		AuthMethod:       "service_account",
		APIKey:           "test-key",
	})
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, httptest.NewRequest(http.MethodPost, "/api/v1/library/remotes", bytes.NewReader(body)))
	if resp.Code != http.StatusCreated {
		t.Fatalf("create remote status = %d body=%s", resp.Code, resp.Body.String())
	}
	var remote LibraryRemote
	if err := json.NewDecoder(resp.Body).Decode(&remote); err != nil {
		t.Fatalf("decode remote: %v", err)
	}
	if remote.APIKey != "" {
		t.Fatal("remote response leaked api key")
	}
	return remote
}

func libraryTestScript(id string, name string, projectID string, target string) *dsl.Script {
	return &dsl.Script{
		ID:        id,
		Name:      name,
		Protocol:  "http",
		Version:   "1.0.0",
		ProjectID: projectID,
		Config:    dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{
			Name:   "request",
			Type:   "GET",
			Target: target,
		}},
	}
}

func marshalLibraryTestJSON(t *testing.T, value any) []byte {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return payload
}
