package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestConcurrentLibraryLinkWritesPersistEveryLink(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "links.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})
	const count = 24
	var wg sync.WaitGroup
	errs := make(chan error, count)
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			id := fmt.Sprintf("source-%d", index)
			errs <- server.rememberLibraryLink("remote", "project-a", "script", "instance", id, "local-"+id, id, "local-hash", "remote-hash")
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	server.libraryStateMutationMu.Lock()
	server.loadLibraryLinksState()
	server.libraryStateMutationMu.Unlock()
	server.libraryMu.Lock()
	defer server.libraryMu.Unlock()
	if len(server.libraryLinks) != count {
		t.Fatalf("persisted links = %d, want %d", len(server.libraryLinks), count)
	}
}

func TestLibraryExportImportScenarioIncludesScriptDependency(t *testing.T) {
	source := NewServer(ServerConfig{})
	target := NewServer(ServerConfig{})

	scriptDef := libraryTestScript("script-checkout", "Checkout API", "project-a", "/checkout")
	scriptDef.Protocol = "browser"
	scriptDef.Actions[0].Type = "browser_navigate"
	script := source.runner.StoreScript(scriptDef)
	iter := 7
	scenario, err := source.runner.StoreScenario(CreateScenarioRequest{
		Name:     "Checkout Smoke",
		Duration: "1m",
		Groups: []CreateGroupEntry{{
			Name:       "checkout",
			ScriptID:   script.ID,
			Role:       GroupRoleObserver,
			VUsers:     3,
			Iterations: &iter,
			RuntimeSettings: &RuntimeSettingsOverride{
				LogLevel: "extended",
			},
			Assignment: &GeneratorAssignmentSettings{
				Mode:            generatorAssignmentModeSpecificHosts,
				SelectedTargets: []string{"gen-jvm:9090"},
				RequiredLabels:  map[string]string{"jvm": "17"},
			},
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
	importedScenario := target.runner.ListScenariosFiltered("project-local")[0]
	if len(importedScenario.Scripts) != 1 {
		t.Fatalf("imported scenario scripts = %d, want 1", len(importedScenario.Scripts))
	}
	importedGroup := importedScenario.Scripts[0]
	if importedGroup.Role != GroupRoleObserver {
		t.Fatalf("imported role = %q, want observer", importedGroup.Role)
	}
	if importedGroup.RuntimeSettings == nil || importedGroup.RuntimeSettings.LogLevel != "extended" {
		t.Fatalf("imported runtime settings = %+v, want log_level extended", importedGroup.RuntimeSettings)
	}
	if importedGroup.Iterations == nil || *importedGroup.Iterations != iter {
		t.Fatalf("imported iterations = %v, want %d", importedGroup.Iterations, iter)
	}
	if importedGroup.Assignment == nil || importedGroup.Assignment.Mode != generatorAssignmentModeSpecificHosts || len(importedGroup.Assignment.SelectedTargets) != 1 || importedGroup.Assignment.SelectedTargets[0] != "gen-jvm:9090" {
		t.Fatalf("imported assignment = %+v, want specific host assignment", importedGroup.Assignment)
	}
}

func TestLibraryRemotePullAndPushScriptBetweenControllers(t *testing.T) {
	// Federation targets are loopback httptest servers; opt past the SSRF guard.
	t.Setenv(remoteOutboundAllowPrivateHostsEnv, "1")
	host := NewServer(ServerConfig{})
	local := NewServer(ServerConfig{})
	hostScript := host.runner.StoreScript(libraryTestScript("host-script", "Hosted Checkout", "team-project", "/hosted"))

	hostHTTP := httptest.NewServer(host.mux)
	defer hostHTTP.Close()
	remote := createLibraryRemoteForTest(t, local, hostHTTP.URL, "team-project")

	pullBody := marshalLibraryTestJSON(t, LibraryTransferRequest{Kind: "script", ID: hostScript.ID, ProjectID: "team-project"})
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

func TestLibraryRemotePushScenarioHonorsIncludeDependencies(t *testing.T) {
	t.Setenv(remoteOutboundAllowPrivateHostsEnv, "1")

	for _, testCase := range []struct {
		name                string
		includeDependencies bool
		wantStatus          int
		wantBundleItems     int
		wantHostScripts     int
		wantHostScenarios   int
	}{
		{
			name:                "includes referenced scripts",
			includeDependencies: true,
			wantStatus:          http.StatusOK,
			wantBundleItems:     2,
			wantHostScripts:     1,
			wantHostScenarios:   1,
		},
		{
			name:                "scenario only",
			includeDependencies: false,
			wantStatus:          http.StatusConflict,
			wantBundleItems:     1,
			wantHostScripts:     0,
			wantHostScenarios:   0,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			host := NewServer(ServerConfig{})
			local := NewServer(ServerConfig{})
			localScript := local.runner.StoreScript(libraryTestScript("scenario-script", "Scenario Script", "team-project", "/scenario"))
			localScenario, err := local.runner.StoreScenario(CreateScenarioRequest{
				Name:     "Scenario Transfer",
				Duration: "1m",
				Groups: []CreateGroupEntry{{
					Name:     "load",
					ScriptID: localScript.ID,
					VUsers:   1,
				}},
			})
			if err != nil {
				t.Fatalf("StoreScenario: %v", err)
			}

			hostHTTP := httptest.NewServer(host.mux)
			defer hostHTTP.Close()
			remote := createLibraryRemoteForTest(t, local, hostHTTP.URL, "team-project")
			pushBody := marshalLibraryTestJSON(t, LibraryTransferRequest{
				Kind:                "scenario",
				ID:                  localScenario.ID,
				IncludeDependencies: testCase.includeDependencies,
			})
			pushResp := httptest.NewRecorder()
			local.mux.ServeHTTP(pushResp, httptest.NewRequest(http.MethodPost, "/api/v1/library/remotes/"+remote.ID+"/push", bytes.NewReader(pushBody)))
			if pushResp.Code != testCase.wantStatus {
				t.Fatalf("push status = %d body=%s, want %d", pushResp.Code, pushResp.Body.String(), testCase.wantStatus)
			}

			var pushed LibraryRemoteTransferResponse
			if err := json.NewDecoder(pushResp.Body).Decode(&pushed); err != nil {
				t.Fatalf("decode push: %v", err)
			}
			if pushed.Bundle == nil || len(pushed.Bundle.Items) != testCase.wantBundleItems {
				t.Fatalf("pushed bundle items = %#v, want %d", pushed.Bundle, testCase.wantBundleItems)
			}
			if got := len(host.runner.ListScriptsFiltered("team-project", nil)); got != testCase.wantHostScripts {
				t.Fatalf("host scripts = %d, want %d", got, testCase.wantHostScripts)
			}
			if got := len(host.runner.ListScenariosFiltered("team-project")); got != testCase.wantHostScenarios {
				t.Fatalf("host scenarios = %d, want %d", got, testCase.wantHostScenarios)
			}
		})
	}
}

func TestLibraryRemoteJSONHonorsConfiguredTimeout(t *testing.T) {
	t.Setenv("APEXLOAD_LIBRARY_REMOTE_JSON_TIMEOUT", "10ms")
	t.Setenv(remoteOutboundAllowPrivateHostsEnv, "1") // loopback target; test the timeout, not the SSRF guard
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		writeJSON(w, http.StatusOK, LibraryCapabilitiesResponse{})
	}))
	defer slow.Close()
	server := NewServer(ServerConfig{})

	if _, err := server.remoteLibraryCapabilities(context.Background(), LibraryRemote{BaseURL: slow.URL, AuthMethod: "none"}); err == nil {
		t.Fatal("remoteLibraryCapabilities returned nil error, want timeout")
	}
}

func TestLibraryRemoteCapabilitiesHasSmallResponseLimit(t *testing.T) {
	t.Setenv(remoteOutboundAllowPrivateHostsEnv, "1")
	remoteHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(bytes.Repeat([]byte(" "), int(libraryRemoteCapabilitiesMaxBytes)+1))
	}))
	defer remoteHTTP.Close()

	server := NewServer(ServerConfig{})
	_, err := server.remoteLibraryCapabilities(context.Background(), LibraryRemote{BaseURL: remoteHTTP.URL, AuthMethod: "none"})
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("remoteLibraryCapabilities error = %v, want response limit error", err)
	}
}

func TestLibraryRemoteArchiveDownloadHonorsConfiguredTimeout(t *testing.T) {
	t.Setenv("APEXLOAD_LIBRARY_REMOTE_ARCHIVE_TIMEOUT", "10ms")
	t.Setenv(remoteOutboundAllowPrivateHostsEnv, "1") // loopback target; test the timeout, not the SSRF guard
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("zip"))
	}))
	defer slow.Close()
	server := NewServer(ServerConfig{})

	archiveImport, cleanup, err := server.remoteLibraryDownloadArchive(context.Background(), LibraryRemote{BaseURL: slow.URL, AuthMethod: "none"}, "run-1")
	if cleanup != nil {
		defer cleanup()
	}
	if err == nil {
		t.Fatalf("remoteLibraryDownloadArchive = %#v, want timeout error", archiveImport)
	}
}

func TestBuildLibraryRemoteRequiresHTTPURL(t *testing.T) {
	if _, err := buildLibraryRemote(LibraryRemoteRequest{Name: "FTP", BaseURL: "ftp://example.test/library"}); err == nil {
		t.Fatal("buildLibraryRemote accepted ftp URL, want error")
	}
	if _, err := buildLibraryRemote(LibraryRemoteRequest{Name: "HTTPS", BaseURL: "https://example.test/library"}); err != nil {
		t.Fatalf("buildLibraryRemote returned error for https URL: %v", err)
	}
}

func TestLibraryPullDetectsLocalAndRemoteConflict(t *testing.T) {
	// Federation targets are loopback httptest servers; opt past the SSRF guard.
	t.Setenv(remoteOutboundAllowPrivateHostsEnv, "1")
	host := NewServer(ServerConfig{})
	local := NewServer(ServerConfig{})
	hostScript := host.runner.StoreScript(libraryTestScript("host-conflict", "Hosted Conflict", "team-project", "/v1"))

	hostHTTP := httptest.NewServer(host.mux)
	defer hostHTTP.Close()
	remote := createLibraryRemoteForTest(t, local, hostHTTP.URL, "team-project")

	pullBody := marshalLibraryTestJSON(t, LibraryTransferRequest{Kind: "script", ID: hostScript.ID, ProjectID: "team-project"})
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

func TestLibraryRemoteStatePersistsThroughSettingsStore(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "apexload.db")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	creds := NewMemoryLibraryCredentialStore()
	server := NewServer(ServerConfig{Store: store, LibraryCredentials: creds})

	body := marshalLibraryTestJSON(t, LibraryRemoteRequest{
		Name:                "Team Host",
		BaseURL:             "https://host.example.test",
		DefaultProjectID:    "team-project",
		ProjectMappings:     map[string]string{"local-team": "team-project"},
		ApplicationMappings: map[string]string{" local-storefront ": " remote-storefront "},
		ReleaseMappings:     map[string]string{" local-sprint-24 ": " remote-sprint-24 "},
		AuthMethod:          "service_account",
		APIKey:              "secret-token",
	})
	createResp := httptest.NewRecorder()
	server.mux.ServeHTTP(createResp, httptest.NewRequest(http.MethodPost, "/api/v1/library/remotes", bytes.NewReader(body)))
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create remote status = %d body=%s", createResp.Code, createResp.Body.String())
	}
	var created LibraryRemote
	if err := json.NewDecoder(createResp.Body).Decode(&created); err != nil {
		t.Fatalf("decode created remote: %v", err)
	}
	if created.APIKey != "" {
		t.Fatal("remote create response leaked api key")
	}
	raw, err := store.GetSetting(libraryRemotesSettingKey)
	if err != nil {
		t.Fatalf("GetSetting remotes: %v", err)
	}
	if !strings.Contains(raw, created.ID) {
		t.Fatalf("persisted remotes did not include created id %q: %s", created.ID, raw)
	}
	if strings.Contains(raw, "secret-token") {
		t.Fatalf("persisted remote settings leaked api key: %s", raw)
	}

	reopened := NewServer(ServerConfig{Store: store, LibraryCredentials: creds})
	listResp := httptest.NewRecorder()
	reopened.mux.ServeHTTP(listResp, httptest.NewRequest(http.MethodGet, "/api/v1/library/remotes", nil))
	if listResp.Code != http.StatusOK {
		t.Fatalf("list remotes status = %d body=%s", listResp.Code, listResp.Body.String())
	}
	var listed LibraryRemoteListResponse
	if err := json.NewDecoder(listResp.Body).Decode(&listed); err != nil {
		t.Fatalf("decode listed remotes: %v", err)
	}
	if len(listed.Remotes) != 1 {
		t.Fatalf("listed remotes = %d, want 1", len(listed.Remotes))
	}
	if listed.Remotes[0].ID != created.ID || !listed.Remotes[0].CredentialConfigured {
		t.Fatalf("listed remote = %#v, want persisted remote with credential configured", listed.Remotes[0])
	}
	if listed.Remotes[0].ProjectMappings["local-team"] != "team-project" {
		t.Fatalf("listed project mappings = %#v, want persisted local-team mapping", listed.Remotes[0].ProjectMappings)
	}
	if listed.Remotes[0].ApplicationMappings["local-storefront"] != "remote-storefront" {
		t.Fatalf("listed application mappings = %#v, want persisted local-storefront mapping", listed.Remotes[0].ApplicationMappings)
	}
	if listed.Remotes[0].ReleaseMappings["local-sprint-24"] != "remote-sprint-24" {
		t.Fatalf("listed release mappings = %#v, want persisted local-sprint-24 mapping", listed.Remotes[0].ReleaseMappings)
	}
	if listed.Remotes[0].APIKey != "" || listed.Remotes[0].CredentialRef != "" {
		t.Fatalf("remote list response leaked credential material: %#v", listed.Remotes[0])
	}

	updateBody := marshalLibraryTestJSON(t, LibraryRemoteRequest{
		ApplicationMappings: map[string]string{"local-storefront": "remote-storefront-v2"},
		ReleaseMappings:     map[string]string{"local-sprint-24": "remote-sprint-24-v2"},
	})
	updateResp := httptest.NewRecorder()
	reopened.mux.ServeHTTP(updateResp, httptest.NewRequest(http.MethodPut, "/api/v1/library/remotes/"+created.ID, bytes.NewReader(updateBody)))
	if updateResp.Code != http.StatusOK {
		t.Fatalf("update remote status = %d body=%s", updateResp.Code, updateResp.Body.String())
	}
	var updated LibraryRemote
	if err := json.NewDecoder(updateResp.Body).Decode(&updated); err != nil {
		t.Fatalf("decode updated remote: %v", err)
	}
	if updated.ApplicationMappings["local-storefront"] != "remote-storefront-v2" || updated.ReleaseMappings["local-sprint-24"] != "remote-sprint-24-v2" || updated.ProjectMappings["local-team"] != "team-project" {
		t.Fatalf("updated mappings = applications %#v releases %#v projects %#v", updated.ApplicationMappings, updated.ReleaseMappings, updated.ProjectMappings)
	}

	reopenedAfterUpdate := NewServer(ServerConfig{Store: store, LibraryCredentials: creds})
	updatedListResp := httptest.NewRecorder()
	reopenedAfterUpdate.mux.ServeHTTP(updatedListResp, httptest.NewRequest(http.MethodGet, "/api/v1/library/remotes", nil))
	var updatedList LibraryRemoteListResponse
	if err := json.NewDecoder(updatedListResp.Body).Decode(&updatedList); err != nil {
		t.Fatalf("decode updated remote list: %v", err)
	}
	if len(updatedList.Remotes) != 1 || updatedList.Remotes[0].ApplicationMappings["local-storefront"] != "remote-storefront-v2" || updatedList.Remotes[0].ReleaseMappings["local-sprint-24"] != "remote-sprint-24-v2" {
		t.Fatalf("persisted updated application/release mappings = %#v", updatedList.Remotes)
	}
}

func TestLibraryResultArchiveStreamsAndImportsMultipart(t *testing.T) {
	host := NewServer(ServerConfig{})
	target := NewServer(ServerConfig{})
	script := host.runner.StoreScript(libraryTestScript("archive-script", "Archive Script", "team-project", "/archive"))
	testRun, err := host.runner.CreateTest(CreateTestRequest{
		Name:          "Archive Result",
		Duration:      "1m",
		ProjectID:     "team-project",
		ApplicationID: "application-archive",
		ReleaseID:     "release-archive",
		Groups: []CreateGroupEntry{{
			Name:     "archive",
			ScriptID: script.ID,
			VUsers:   1,
		}},
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	record := attachLibraryArchiveFixture(t, host, testRun)
	record.ArchiveBackend = archiveBackendFilesystem
	record.ArchiveURI = "azblob://internal-container/private-prefix/" + testRun.ID
	record.ManifestURI = record.ArchiveURI + "/manifest.json"

	exportBody := marshalLibraryTestJSON(t, LibraryTransferRequest{Kind: "result", ID: testRun.ID})
	exportResp := httptest.NewRecorder()
	host.mux.ServeHTTP(exportResp, httptest.NewRequest(http.MethodPost, "/api/v1/library/export", bytes.NewReader(exportBody)))
	if exportResp.Code != http.StatusOK {
		t.Fatalf("export result status = %d body=%s", exportResp.Code, exportResp.Body.String())
	}
	var bundle LibraryBundle
	if err := json.NewDecoder(exportResp.Body).Decode(&bundle); err != nil {
		t.Fatalf("decode result bundle: %v", err)
	}
	if len(bundle.Items) != 1 || bundle.Items[0].Result == nil {
		t.Fatalf("bundle items = %#v, want one result item", bundle.Items)
	}
	if bundle.Items[0].ContentHashVersion != libraryResultHashVersionCurrent {
		t.Fatalf("result content_hash_version = %q, want %q", bundle.Items[0].ContentHashVersion, libraryResultHashVersionCurrent)
	}
	result := bundle.Items[0].Result
	for label, archive := range map[string]*storage.RunArchiveRecord{"result.archive": result.Archive, "result.test.archive": result.Test.Archive} {
		if archive == nil {
			t.Fatalf("%s is nil", label)
		}
		if archive.ArchivePath != "" || archive.ManifestPath != "" || archive.ArchiveURI != "" || archive.ManifestURI != "" || archive.ArchiveBackend != "" {
			t.Fatalf("%s leaked internal archive location: %#v", label, archive)
		}
	}
	if result.ArchiveTransfer != "stream" {
		t.Fatalf("archive_transfer = %q, want stream", result.ArchiveTransfer)
	}
	if result.ArchiveZipB64 != "" {
		t.Fatal("streaming result export should not populate legacy archive_zip_b64")
	}
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	wantManifestHash, wantArchiveSize, err := newArchiveReadCaches().sanitizedRunArchiveMetadata(context.Background(), record, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if result.ArchiveManifestHash != wantManifestHash {
		t.Fatalf("manifest hash = %q, want %q", result.ArchiveManifestHash, wantManifestHash)
	}
	if result.ArchiveSizeBytes != wantArchiveSize {
		t.Fatalf("archive size = %d, want %d", result.ArchiveSizeBytes, wantArchiveSize)
	}

	catalogResp := httptest.NewRecorder()
	host.mux.ServeHTTP(catalogResp, httptest.NewRequest(http.MethodGet, "/api/v1/library/catalog?kind=result&project_id=team-project", nil))
	if catalogResp.Code != http.StatusOK {
		t.Fatalf("catalog result status = %d body=%s", catalogResp.Code, catalogResp.Body.String())
	}
	var catalog LibraryCatalogResponse
	if err := json.NewDecoder(catalogResp.Body).Decode(&catalog); err != nil {
		t.Fatalf("decode result catalog: %v", err)
	}
	if len(catalog.Items) != 1 || catalog.Items[0].ReleaseID != "release-archive" {
		t.Fatalf("catalog items = %#v, want result release metadata", catalog.Items)
	}
	if catalog.Items[0].ApplicationID != "application-archive" {
		t.Fatalf("catalog application_id = %q, want application-archive", catalog.Items[0].ApplicationID)
	}
	if catalog.Items[0].ContentHash != bundle.Items[0].ContentHash {
		t.Fatalf("catalog result hash = %q, export hash = %q", catalog.Items[0].ContentHash, bundle.Items[0].ContentHash)
	}
	wantRunDate := time.Date(2026, 6, 1, 11, 55, 0, 0, time.UTC)
	if catalog.Items[0].Status != "COMPLETE" || !catalog.Items[0].ExecutionRunDate.Equal(wantRunDate) {
		t.Fatalf("catalog result status/run date = %q/%s, want COMPLETE/%s", catalog.Items[0].Status, catalog.Items[0].ExecutionRunDate, wantRunDate)
	}

	archiveResp := httptest.NewRecorder()
	host.mux.ServeHTTP(archiveResp, httptest.NewRequest(http.MethodGet, "/api/v1/library/results/"+testRun.ID+"/archive", nil))
	if archiveResp.Code != http.StatusOK {
		t.Fatalf("archive stream status = %d body=%s", archiveResp.Code, archiveResp.Body.String())
	}
	if contentType := archiveResp.Header().Get("Content-Type"); contentType != "application/zip" {
		t.Fatalf("archive content-type = %q, want application/zip", contentType)
	}
	if archiveResp.Body.Len() == 0 {
		t.Fatal("archive stream was empty")
	}

	importBody, contentType := multipartArchiveImportBody(t, bundle, "mapped-project", archiveResp.Body.Bytes())
	importReq := httptest.NewRequest(http.MethodPost, "/api/v1/library/import/archive", importBody)
	importReq.Header.Set("Content-Type", contentType)
	importResp := httptest.NewRecorder()
	target.mux.ServeHTTP(importResp, importReq)
	if importResp.Code != http.StatusCreated {
		t.Fatalf("archive import status = %d body=%s", importResp.Code, importResp.Body.String())
	}
	var imported LibraryImportResponse
	if err := json.NewDecoder(importResp.Body).Decode(&imported); err != nil {
		t.Fatalf("decode archive import: %v", err)
	}
	if len(imported.Items) != 1 || imported.Items[0].Kind != "result" {
		t.Fatalf("imported items = %#v, want one result import", imported.Items)
	}
	imports, err := target.listExternalResultImports(storage.ExternalResultImportFilter{ProjectID: "mapped-project", Provider: "apexload"})
	if err != nil {
		t.Fatalf("list external result imports: %v", err)
	}
	if len(imports) != 1 {
		t.Fatalf("external result imports = %d, want 1", len(imports))
	}
	raw := imports[0].RawResult
	if raw["archive_transfer"] != "stream" {
		t.Fatalf("raw archive_transfer = %#v, want stream", raw["archive_transfer"])
	}
	if raw["imported_archive_manifest_hash"] != result.ArchiveManifestHash {
		t.Fatalf("imported manifest hash = %#v, want %q", raw["imported_archive_manifest_hash"], result.ArchiveManifestHash)
	}
	if _, exists := raw["archive_path"]; exists {
		t.Fatalf("imported raw result exposed internal archive_path: %#v", raw["archive_path"])
	}
	archivePath := libraryImportedArchiveDir(imports[0].ID)
	if _, err := os.Stat(filepath.Join(archivePath, "manifest.json")); err != nil {
		t.Fatalf("imported archive manifest missing: %v", err)
	}
	listResp := httptest.NewRecorder()
	target.mux.ServeHTTP(listResp, httptest.NewRequest(http.MethodGet, "/api/v1/results/imports?project_id=mapped-project", nil))
	if listResp.Code != http.StatusOK {
		t.Fatalf("list imported results status = %d body=%s", listResp.Code, listResp.Body.String())
	}
	for _, forbidden := range []string{archivePath, `"archive_path"`, `"manifest_path"`, `"archive_backend"`, `"archive_uri"`, `"manifest_uri"`} {
		if strings.Contains(listResp.Body.String(), forbidden) {
			t.Fatalf("list imported results exposed internal archive location %q: %s", forbidden, listResp.Body.String())
		}
	}
	corruptBody, corruptType := multipartArchiveImportBody(t, bundle, "mapped-project", []byte("not-a-zip"))
	corruptReq := httptest.NewRequest(http.MethodPost, "/api/v1/library/import/archive", corruptBody)
	corruptReq.Header.Set("Content-Type", corruptType)
	corruptResp := httptest.NewRecorder()
	target.mux.ServeHTTP(corruptResp, corruptReq)
	if corruptResp.Code != http.StatusConflict {
		t.Fatalf("corrupt retry status = %d body=%s", corruptResp.Code, corruptResp.Body.String())
	}
	if _, err := os.Stat(filepath.Join(archivePath, "manifest.json")); err != nil {
		t.Fatalf("corrupt retry destroyed prior imported archive: %v", err)
	}
}

func TestDeterministicLibraryImportIDIncludesDestinationProjectAndStructuredIdentity(t *testing.T) {
	item := LibraryBundleItem{Kind: "result", SourceInstanceID: "source/instance", SourceItemID: "item/id"}
	first, err := deterministicLibraryImportID("remote", "project-a", item)
	if err != nil {
		t.Fatal(err)
	}
	second, err := deterministicLibraryImportID("remote", "project-b", item)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("destination projects produced the same import id %q", first)
	}
	item.SourceItemID = ""
	if _, err := deterministicLibraryImportID("remote", "project-a", item); err == nil {
		t.Fatal("empty source identity was accepted")
	}
}

func TestLibraryResultHashAcceptsPreviousV1PayloadWithSummary(t *testing.T) {
	server := NewServer(ServerConfig{})
	testRun := &ManagedTest{ID: "run-v1", Name: "Prior Bundle", Status: "COMPLETE", ProjectID: "source", DurationStr: "1m", TotalVUsers: 1}
	summary := libraryResultSummary(testRun)
	item := LibraryBundleItem{
		Kind:               "result",
		SourceInstanceID:   "source-instance",
		SourceItemID:       testRun.ID,
		Name:               testRun.Name,
		ContentHash:        hashLibraryResultPreviousV1WithSummary(testRun, summary),
		ContentHashVersion: libraryResultHashVersionLegacyV1,
		Result:             &LibraryResultPayload{Test: testRun, Summary: summary},
	}
	if _, _, conflict := server.importLibraryResult(item, "destination", "remote", nil); conflict != nil {
		t.Fatalf("previous v1 result hash was rejected: %+v", conflict)
	}
	item.ContentHashVersion = "apexload.library_result_hash.v999"
	if _, _, conflict := server.importLibraryResult(item, "destination", "remote", nil); conflict == nil || !strings.Contains(conflict.Reason, "unsupported result content_hash_version") {
		t.Fatalf("unsupported result hash version conflict = %+v", conflict)
	}
}

func TestLibraryCatalogTreatsEqualCurrentScenarioHashesAsSyncedAfterHashMigration(t *testing.T) {
	server := NewServer(ServerConfig{})
	script := server.runner.StoreScript(libraryTestScript("hash-script", "Hash Script", "project-a", "/"))
	scenario, err := server.runner.StoreScenarioWithID("hash-scenario", CreateScenarioRequest{
		Name:     "Hash Scenario",
		Duration: "1m",
		Groups:   []CreateGroupEntry{{Name: "group", ScriptID: script.ID, VUsers: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	currentHash := hashLibraryScenario(scenario)
	if err := server.rememberLibraryLink("remote", "project-a", "scenario", "source", "scenario", scenario.ID, "remote-scenario", "legacy-local-hash", "legacy-remote-hash"); err != nil {
		t.Fatal(err)
	}
	item := server.annotateRemoteCatalogItem("remote", "project-a", LibraryCatalogItem{
		Kind: "scenario", SourceInstanceID: "source", SourceItemID: "scenario", ContentHash: currentHash,
	})
	if item.SyncStatus != "synced" {
		t.Fatalf("sync status = %q, want synced", item.SyncStatus)
	}
}

func TestLibraryRemoteCatalogAnnotatesLinksWithLocalMappedProjectID(t *testing.T) {
	t.Setenv(remoteOutboundAllowPrivateHostsEnv, "1")
	t.Setenv("APEXLOAD_LIBRARY_ALLOW_INSECURE_HTTP", "1")
	host := NewServer(ServerConfig{})
	local := NewServer(ServerConfig{})
	hostScript := host.runner.StoreScript(libraryTestScript("host-mapped-script", "Mapped Script", "remote-project", "/mapped"))
	localScript := local.runner.StoreScript(libraryTestScript("local-mapped-script", "Mapped Script", "local-project", "/mapped"))
	hostHTTP := httptest.NewServer(host.mux)
	defer hostHTTP.Close()

	remote := createMappedLibraryRemoteForTest(t, local, hostHTTP.URL, "local-project", "remote-project", nil, nil)
	localHash := hashLibraryScript(localScript.Script)
	remoteHash := hashLibraryScript(hostScript.Script)
	if err := local.rememberLibraryLink(remote.ID, "local-project", "script", host.controllerID, hostScript.ID, localScript.ID, hostScript.ID, localHash, remoteHash); err != nil {
		t.Fatalf("remember local-project link: %v", err)
	}

	resp := httptest.NewRecorder()
	local.mux.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/api/v1/library/remotes/"+remote.ID+"/catalog?kind=script&project_id=local-project", nil))
	if resp.Code != http.StatusOK {
		t.Fatalf("remote catalog status = %d body=%s", resp.Code, resp.Body.String())
	}
	var catalogResponse LibraryRemoteCatalogResponse
	if err := json.NewDecoder(resp.Body).Decode(&catalogResponse); err != nil {
		t.Fatalf("decode remote catalog: %v", err)
	}
	if len(catalogResponse.Catalog.Items) != 1 {
		t.Fatalf("catalog items = %#v, want one", catalogResponse.Catalog.Items)
	}
	item := catalogResponse.Catalog.Items[0]
	if item.LocalItemID != localScript.ID || item.RemoteItemID != hostScript.ID || item.SyncStatus != "synced" {
		t.Fatalf("annotated mapped-project item = %#v, want local link and synced status", item)
	}
}

func TestLibraryRemotePullAndPushResultArchivesBetweenControllers(t *testing.T) {
	for _, incarnation := range []string{"", "controller-admitted-incarnation"} {
		name := "legacy"
		if incarnation != "" {
			name = "explicit incarnation"
		}
		t.Run(name, func(t *testing.T) { checkLibraryRemotePullAndPushResultArchivesBetweenControllers(t, incarnation) })
	}
}

func checkLibraryRemotePullAndPushResultArchivesBetweenControllers(t *testing.T, incarnation string) {
	// Federation targets are loopback httptest servers; opt past the SSRF guard.
	t.Setenv(remoteOutboundAllowPrivateHostsEnv, "1")
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	host := NewServer(ServerConfig{})
	local := NewServer(ServerConfig{})
	t.Cleanup(func() { _ = host.Shutdown(context.Background()); _ = local.Shutdown(context.Background()) })

	hostScript := host.runner.StoreScript(libraryTestScript("host-archive-script", "Host Archive Script", "team-project", "/host-archive"))
	hostTest, err := host.runner.CreateTest(CreateTestRequest{
		Name:          "Hosted Archive Result",
		Duration:      "1m",
		ProjectID:     "team-project",
		ApplicationID: "remote-application",
		ReleaseID:     "remote-release",
		Groups: []CreateGroupEntry{{
			Name:     "host",
			ScriptID: hostScript.ID,
			VUsers:   1,
		}},
	})
	if err != nil {
		t.Fatalf("CreateTest host: %v", err)
	}
	attachLibraryArchiveFixture(t, host, hostTest, incarnation)

	hostHTTP := httptest.NewServer(host.mux)
	defer hostHTTP.Close()
	remote := createMappedLibraryRemoteForTest(
		t,
		local,
		hostHTTP.URL,
		"team-project",
		"team-project",
		map[string]string{"local-application": "remote-application"},
		map[string]string{"local-release": "remote-release"},
	)

	pullBody := marshalLibraryTestJSON(t, LibraryTransferRequest{
		Kind: "result", ID: hostTest.ID, ProjectID: "team-project",
		ApplicationID: "local-application", ReleaseID: "local-release",
	})
	pullResp := httptest.NewRecorder()
	local.mux.ServeHTTP(pullResp, httptest.NewRequest(http.MethodPost, "/api/v1/library/remotes/"+remote.ID+"/pull", bytes.NewReader(pullBody)))
	if pullResp.Code != http.StatusCreated {
		t.Fatalf("pull result status = %d body=%s", pullResp.Code, pullResp.Body.String())
	}
	var pulled LibraryRemoteTransferResponse
	if err := json.NewDecoder(pullResp.Body).Decode(&pulled); err != nil {
		t.Fatalf("decode pulled result: %v", err)
	}
	if pulled.Bundle == nil || len(pulled.Bundle.Items) != 1 || pulled.Bundle.Items[0].Result == nil {
		t.Fatalf("pulled bundle = %#v, want one result item", pulled.Bundle)
	}
	assertLibraryArchiveImport(t, local, "team-project", pulled.Bundle.Items[0].Result.ArchiveManifestHash, 1, incarnation)

	localScript := local.runner.StoreScript(libraryTestScript("local-archive-script", "Local Archive Script", "team-project", "/local-archive"))
	localTest, err := local.runner.CreateTest(CreateTestRequest{
		Name:          "Desktop Archive Result",
		Duration:      "1m",
		ProjectID:     "team-project",
		ApplicationID: "local-application",
		ReleaseID:     "local-release",
		Groups: []CreateGroupEntry{{
			Name:     "local",
			ScriptID: localScript.ID,
			VUsers:   1,
		}},
	})
	if err != nil {
		t.Fatalf("CreateTest local: %v", err)
	}
	attachLibraryArchiveFixture(t, local, localTest, incarnation)

	pushBody := marshalLibraryTestJSON(t, LibraryTransferRequest{Kind: "result", ID: localTest.ID})
	pushResp := httptest.NewRecorder()
	local.mux.ServeHTTP(pushResp, httptest.NewRequest(http.MethodPost, "/api/v1/library/remotes/"+remote.ID+"/push", bytes.NewReader(pushBody)))
	if pushResp.Code != http.StatusOK {
		t.Fatalf("push result status = %d body=%s", pushResp.Code, pushResp.Body.String())
	}
	var pushed LibraryRemoteTransferResponse
	if err := json.NewDecoder(pushResp.Body).Decode(&pushed); err != nil {
		t.Fatalf("decode pushed result: %v", err)
	}
	if pushed.Bundle == nil || len(pushed.Bundle.Items) != 1 || pushed.Bundle.Items[0].Result == nil {
		t.Fatalf("pushed bundle = %#v, want one result item", pushed.Bundle)
	}
	if pushed.Bundle.Items[0].Result.ArchiveZipB64 != "" {
		t.Fatal("remote result push should stream archives instead of base64 encoding them")
	}
	assertLibraryArchiveImport(t, host, "team-project", pushed.Bundle.Items[0].Result.ArchiveManifestHash, 1, incarnation)
}

func TestLibraryRemotePushMapsResultDestinationIdentity(t *testing.T) {
	t.Setenv(remoteOutboundAllowPrivateHostsEnv, "1")
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	host := NewServer(ServerConfig{})
	local := NewServer(ServerConfig{})

	localScript := local.runner.StoreScript(libraryTestScript("mapped-push-script", "Mapped Push Script", "local-project", "/mapped-push"))
	localTest, err := local.runner.CreateTest(CreateTestRequest{
		Name:          "Mapped Push Result",
		Duration:      "1m",
		ProjectID:     "local-project",
		ApplicationID: "local-application",
		ReleaseID:     "local-release",
		Groups: []CreateGroupEntry{{
			Name:     "local",
			ScriptID: localScript.ID,
			VUsers:   1,
		}},
	})
	if err != nil {
		t.Fatalf("CreateTest local: %v", err)
	}
	attachLibraryArchiveFixture(t, local, localTest)

	hostHTTP := httptest.NewServer(host.mux)
	defer hostHTTP.Close()
	remote := createMappedLibraryRemoteForTest(
		t,
		local,
		hostHTTP.URL,
		"local-project",
		"remote-project",
		map[string]string{"local-application": "remote-application"},
		map[string]string{"local-release": "remote-release"},
	)

	pushBody := marshalLibraryTestJSON(t, LibraryTransferRequest{Kind: "result", ID: localTest.ID})
	pushResp := httptest.NewRecorder()
	local.mux.ServeHTTP(pushResp, httptest.NewRequest(http.MethodPost, "/api/v1/library/remotes/"+remote.ID+"/push", bytes.NewReader(pushBody)))
	if pushResp.Code != http.StatusOK {
		t.Fatalf("push result status = %d body=%s", pushResp.Code, pushResp.Body.String())
	}

	imports, err := host.listExternalResultImports(storage.ExternalResultImportFilter{ProjectID: "remote-project", Provider: "apexload"})
	if err != nil {
		t.Fatalf("list pushed results: %v", err)
	}
	if len(imports) != 1 {
		t.Fatalf("pushed result imports = %d, want 1", len(imports))
	}
	if imports[0].ApplicationID != "remote-application" || imports[0].ReleaseID != "remote-release" {
		t.Fatalf("pushed result destination identity = application %q release %q", imports[0].ApplicationID, imports[0].ReleaseID)
	}
}

func TestLibraryResultPushDestinationRejectsIncompleteMappings(t *testing.T) {
	tests := []struct {
		name          string
		remote        LibraryRemote
		applicationID string
		releaseID     string
		wantError     string
	}{
		{
			name:          "missing application mapping",
			remote:        LibraryRemote{ReleaseMappings: map[string]string{"local-release": "remote-release"}},
			applicationID: "local-application",
			releaseID:     "local-release",
			wantError:     "has no mapping",
		},
		{
			name:          "missing release mapping",
			remote:        LibraryRemote{ApplicationMappings: map[string]string{"local-application": "remote-application"}},
			applicationID: "local-application",
			releaseID:     "local-release",
			wantError:     "has no mapping",
		},
		{
			name:          "partial result identity",
			remote:        LibraryRemote{},
			applicationID: "local-application",
			wantError:     "required together",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bundle := &LibraryBundle{Items: []LibraryBundleItem{{
				Kind: "result",
				Result: &LibraryResultPayload{Test: &ManagedTest{
					ApplicationID: tt.applicationID,
					ReleaseID:     tt.releaseID,
				}},
			}}}
			_, _, err := libraryResultPushDestination(tt.remote, bundle)
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("libraryResultPushDestination() error = %v, want containing %q", err, tt.wantError)
			}
		})
	}
}

func TestPulledResultRetainsDestinationIdentityArchiveAndHashAfterRemoteDisconnect(t *testing.T) {
	t.Setenv(remoteOutboundAllowPrivateHostsEnv, "1")
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	now := time.Now().UTC()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "local.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.SaveProject(&storage.Project{ID: "local-project", Name: "Local", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	if err := store.SaveApplication(&storage.Application{ID: "local-application", ProjectID: "local-project", Name: "Storefront", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveApplication: %v", err)
	}
	if err := store.SaveRelease(&storage.Release{ID: "local-release", ApplicationID: "local-application", ProjectID: "local-project", Name: "Sprint 24", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveRelease: %v", err)
	}

	host := NewServer(ServerConfig{})
	local := NewServer(ServerConfig{Store: store})
	hostScript := host.runner.StoreScript(libraryTestScript("host-durable-script", "Host Durable Script", "remote-project", "/durable"))
	hostTest, err := host.runner.CreateTest(CreateTestRequest{
		Name:          "Durable Remote Evidence",
		Duration:      "1m",
		ProjectID:     "remote-project",
		ApplicationID: "remote-application",
		ReleaseID:     "remote-release-a",
		Groups: []CreateGroupEntry{{
			Name:     "host",
			ScriptID: hostScript.ID,
			VUsers:   1,
		}},
	})
	if err != nil {
		t.Fatalf("CreateTest host: %v", err)
	}
	attachLibraryArchiveFixture(t, host, hostTest)
	hostHTTP := httptest.NewServer(host.mux)
	defer hostHTTP.Close()
	remote := createMappedLibraryRemoteForTest(
		t,
		local,
		hostHTTP.URL,
		"local-project",
		"remote-project",
		map[string]string{"local-application": "remote-application"},
		map[string]string{
			"local-release":           "remote-release-a",
			"local-duplicate-release": "remote-release-b",
		},
	)

	pullBody := marshalLibraryTestJSON(t, LibraryTransferRequest{
		Kind:          "result",
		ID:            hostTest.ID,
		ProjectID:     "local-project",
		ApplicationID: "local-application",
		ReleaseID:     "local-release",
	})
	pullResp := httptest.NewRecorder()
	local.mux.ServeHTTP(pullResp, httptest.NewRequest(http.MethodPost, "/api/v1/library/remotes/"+remote.ID+"/pull", bytes.NewReader(pullBody)))
	if pullResp.Code != http.StatusCreated {
		t.Fatalf("pull result status = %d body=%s", pullResp.Code, pullResp.Body.String())
	}
	var pulled LibraryRemoteTransferResponse
	if err := json.NewDecoder(pullResp.Body).Decode(&pulled); err != nil {
		t.Fatalf("decode pull response: %v", err)
	}
	if pulled.Import == nil || len(pulled.Import.Items) != 1 {
		t.Fatalf("pull import = %#v, want one item", pulled.Import)
	}
	importID := pulled.Import.Items[0].LocalItemID
	imported, err := store.GetExternalResultImport(importID)
	if err != nil {
		t.Fatalf("GetExternalResultImport: %v", err)
	}
	if imported.ProjectID != "local-project" || imported.ApplicationID != "local-application" || imported.ReleaseID != "local-release" {
		t.Fatalf("import destination identity = %#v", imported)
	}

	diffBody := marshalLibraryTestJSON(t, LibraryTransferRequest{
		Kind:      "result",
		LocalID:   importID,
		RemoteID:  hostTest.ID,
		ProjectID: "local-project",
	})
	diffResp := httptest.NewRecorder()
	local.mux.ServeHTTP(diffResp, httptest.NewRequest(http.MethodPost, "/api/v1/library/remotes/"+remote.ID+"/diff", bytes.NewReader(diffBody)))
	if diffResp.Code != http.StatusOK {
		t.Fatalf("diff imported result status = %d body=%s", diffResp.Code, diffResp.Body.String())
	}
	var diff struct {
		LocalHash  string `json:"local_hash"`
		RemoteHash string `json:"remote_hash"`
		Status     string `json:"status"`
	}
	if err := json.NewDecoder(diffResp.Body).Decode(&diff); err != nil {
		t.Fatalf("decode diff: %v", err)
	}
	if diff.Status != "synced" || diff.LocalHash == "" || diff.LocalHash != diff.RemoteHash {
		t.Fatalf("imported result diff = %#v, want matching durable hashes", diff)
	}

	deleteResp := httptest.NewRecorder()
	local.mux.ServeHTTP(deleteResp, httptest.NewRequest(http.MethodDelete, "/api/v1/library/remotes/"+remote.ID, nil))
	if deleteResp.Code != http.StatusNoContent {
		t.Fatalf("delete remote status = %d body=%s", deleteResp.Code, deleteResp.Body.String())
	}
	reopened := NewServer(ServerConfig{Store: store})
	listResp := httptest.NewRecorder()
	reopened.mux.ServeHTTP(listResp, httptest.NewRequest(http.MethodGet, "/api/v1/results/imports?project_id=local-project&release_id=local-release", nil))
	if listResp.Code != http.StatusOK {
		t.Fatalf("list durable imports status = %d body=%s", listResp.Code, listResp.Body.String())
	}
	var listed struct {
		Imports []storage.ExternalResultImport `json:"imports"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&listed); err != nil {
		t.Fatalf("decode durable imports: %v", err)
	}
	if len(listed.Imports) != 1 || listed.Imports[0].ID != importID || listed.Imports[0].ApplicationID != "local-application" || !listed.Imports[0].HasArchive || listed.Imports[0].ContentHash == "" {
		t.Fatalf("durable import list = %#v", listed.Imports)
	}

	archiveResp := httptest.NewRecorder()
	reopened.mux.ServeHTTP(archiveResp, httptest.NewRequest(http.MethodGet, "/api/v1/library/results/"+importID+"/archive", nil))
	if archiveResp.Code != http.StatusOK || archiveResp.Header().Get("Content-Type") != "application/zip" || archiveResp.Body.Len() == 0 {
		t.Fatalf("durable archive status=%d content-type=%q bytes=%d body=%s", archiveResp.Code, archiveResp.Header().Get("Content-Type"), archiveResp.Body.Len(), archiveResp.Body.String())
	}
}

func createLibraryRemoteForTest(t *testing.T, server *Server, baseURL string, projectID string) LibraryRemote {
	return createMappedLibraryRemoteForTest(t, server, baseURL, projectID, projectID, nil, nil)
}

func createMappedLibraryRemoteForTest(t *testing.T, server *Server, baseURL string, localProjectID string, remoteProjectID string, applicationMappings map[string]string, releaseMappings map[string]string) LibraryRemote {
	t.Helper()
	t.Setenv("APEXLOAD_LIBRARY_ALLOW_INSECURE_HTTP", "1")
	server.libraryCreds = NewMemoryLibraryCredentialStore()
	body := marshalLibraryTestJSON(t, LibraryRemoteRequest{
		Name:                "K8s Host",
		BaseURL:             baseURL,
		DefaultProjectID:    remoteProjectID,
		ProjectMappings:     map[string]string{localProjectID: remoteProjectID},
		ApplicationMappings: applicationMappings,
		ReleaseMappings:     releaseMappings,
		AuthMethod:          "service_account",
		APIKey:              "test-key",
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

func assertLibraryArchiveImport(t *testing.T, server *Server, projectID string, manifestHash string, wantCount int, incarnations ...string) {
	t.Helper()
	imports, err := server.listExternalResultImports(storage.ExternalResultImportFilter{ProjectID: projectID, Provider: "apexload"})
	if err != nil {
		t.Fatalf("list external result imports: %v", err)
	}
	if len(imports) != wantCount {
		t.Fatalf("external result imports = %d, want %d", len(imports), wantCount)
	}
	lastImport := imports[len(imports)-1]
	raw := lastImport.RawResult
	if raw["archive_transfer"] != "stream" {
		t.Fatalf("raw archive_transfer = %#v, want stream", raw["archive_transfer"])
	}
	if raw["archive_zip_b64"] != nil {
		t.Fatalf("raw archive_zip_b64 = %#v, want absent", raw["archive_zip_b64"])
	}
	if raw["imported_archive_manifest_hash"] != manifestHash {
		t.Fatalf("imported manifest hash = %#v, want %q", raw["imported_archive_manifest_hash"], manifestHash)
	}
	if _, exists := raw["archive_path"]; exists {
		t.Fatalf("imported raw result exposed internal archive_path: %#v", raw["archive_path"])
	}
	archivePath := libraryImportedArchiveDir(lastImport.ID)
	if _, err := os.Stat(filepath.Join(archivePath, "manifest.json")); err != nil {
		t.Fatalf("imported archive manifest missing: %v", err)
	}
	if len(incarnations) > 0 {
		manifest, err := loadRunArchiveManifest(filepath.Join(archivePath, "manifest.json"))
		if err != nil {
			t.Fatal(err)
		}
		incarnation, err := archiveManifestRunIncarnation(manifest)
		if err != nil || incarnation != incarnations[0] {
			t.Fatalf("imported source incarnation=%q want=%q err=%v", incarnation, incarnations[0], err)
		}
		response := httptest.NewRecorder()
		server.mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/library/results/"+lastImport.ID+"/archive", nil))
		if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/zip" || response.Body.Len() == 0 {
			t.Fatalf("imported token-bearing archive unreadable: status=%d body=%s", response.Code, response.Body.String())
		}
	}
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

func attachLibraryArchiveFixture(t *testing.T, server *Server, testRun *ManagedTest, incarnations ...string) *storage.RunArchiveRecord {
	t.Helper()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	started := now.Add(-5 * time.Minute)
	archiveDir := filepath.Join(t.TempDir(), "archive")
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		t.Fatalf("create archive dir: %v", err)
	}
	manifest := RunArchiveManifest{
		SchemaVersion:         archiveSchemaVersion,
		TestID:                testRun.ID,
		TestName:              testRun.Name,
		CreatedAt:             now,
		CompletedAt:           now,
		Status:                "COMPLETE",
		TransactionsFile:      "transactions.jsonl",
		ResourceSamplesFile:   "resources.jsonl",
		IntervalSummariesFile: "interval_summaries.jsonl",
		TransactionCount:      1,
		ResourceSampleCount:   0,
		IntervalSummaryCount:  0,
		SLABreached:           false,
	}
	if len(incarnations) > 0 && incarnations[0] != "" {
		initial := RunArchiveProvenance{RunIncarnation: incarnations[0], ProjectID: testRun.ProjectID, RequestedAt: testRun.CreatedAt}
		manifest.Provenance = initial
		manifest.InitialProvenance = &initial
	}
	writeLibraryArchiveText(t, filepath.Join(archiveDir, "transactions.jsonl"), `{"action_name":"archive","duration_ms":42}`+"\n")
	writeLibraryArchiveText(t, filepath.Join(archiveDir, "resources.jsonl"), "")
	writeLibraryArchiveText(t, filepath.Join(archiveDir, "interval_summaries.jsonl"), "")
	if err := updateRunArchiveArtifactIntegrity(archiveDir, &manifest); err != nil {
		t.Fatalf("update archive integrity: %v", err)
	}
	writeLibraryArchiveFile(t, filepath.Join(archiveDir, "manifest.json"), manifest)

	record := &storage.RunArchiveRecord{
		TestID:           testRun.ID,
		SchemaVersion:    archiveSchemaVersion,
		ArchivePath:      archiveDir,
		ManifestPath:     filepath.Join(archiveDir, "manifest.json"),
		TransactionCount: 1,
		CreatedAt:        now,
		CompletedAt:      now,
		ManifestSHA256:   hashArchiveManifestPath(filepath.Join(archiveDir, "manifest.json"))[len("sha256:"):],
	}
	server.runner.mu.Lock()
	stored := server.runner.tests[testRun.ID]
	if stored == nil {
		server.runner.mu.Unlock()
		t.Fatalf("test %s not found in runner", testRun.ID)
	}
	stored.Status = "COMPLETE"
	stored.initialArchiveProvenance = manifest.InitialProvenance
	stored.Archive = record
	stored.StartedAt = &started
	stored.EndedAt = &now
	server.runner.archives[testRun.ID] = record
	server.runner.mu.Unlock()
	return record
}

func writeLibraryArchiveFile(t *testing.T, path string, value any) {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal archive fixture: %v", err)
	}
	writeLibraryArchiveText(t, path, string(payload))
}

func writeLibraryArchiveText(t *testing.T, path string, value string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
		t.Fatalf("write archive fixture %s: %v", path, err)
	}
}

func TestLibraryArchiveImportRejectsMismatchedFormProjectID(t *testing.T) {
	// A form project_id that differs from the auth-validated query project_id must
	// be rejected (otherwise a caller authorized for project-a could import into
	// project-b). A matching value is accepted.
	mismatch, ct := multipartArchiveImportBody(t, LibraryBundle{}, "project-b", []byte("zipbytes"))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/library/import/archive?project_id=project-a", mismatch)
	req.Header.Set("Content-Type", ct)
	_, _, cleanup, err := readLibraryArchiveImportRequest(req)
	if cleanup != nil {
		cleanup()
	}
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("mismatched form project_id err = %v, want a does-not-match rejection", err)
	}

	match, ct2 := multipartArchiveImportBody(t, LibraryBundle{}, "project-a", []byte("zipbytes"))
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/library/import/archive?project_id=project-a", match)
	req2.Header.Set("Content-Type", ct2)
	got, _, cleanup2, err2 := readLibraryArchiveImportRequest(req2)
	if cleanup2 != nil {
		cleanup2()
	}
	if err2 != nil {
		t.Fatalf("matching project_id should be accepted, got err %v", err2)
	}
	if got.ProjectID != "project-a" {
		t.Fatalf("resolved project_id = %q, want project-a (the authorized query value)", got.ProjectID)
	}
}

func TestLibraryArchiveImportRejectsDuplicateArchiveAndCleansTemporaryFile(t *testing.T) {
	temp := t.TempDir()
	t.Setenv("TMPDIR", temp)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	bundlePart, err := writer.CreateFormField("bundle")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(bundlePart).Encode(LibraryBundle{}); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 2; index++ {
		part, err := writer.CreateFormFile("archive", fmt.Sprintf("archive-%d.zip", index))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte("zip")); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/library/import/archive?project_id=project-a", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	_, _, cleanup, err := readLibraryArchiveImportRequest(req)
	if cleanup != nil {
		cleanup()
	}
	if err == nil || !strings.Contains(err.Error(), "duplicate archive") {
		t.Fatalf("duplicate archive error = %v", err)
	}
	entries, err := os.ReadDir(temp)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary archive leaked after duplicate rejection: %v", entries)
	}
}

func multipartArchiveImportBody(t *testing.T, bundle LibraryBundle, projectID string, archiveZip []byte) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	bundlePart, err := writer.CreateFormField("bundle")
	if err != nil {
		t.Fatalf("create bundle part: %v", err)
	}
	if err := json.NewEncoder(bundlePart).Encode(bundle); err != nil {
		t.Fatalf("write bundle part: %v", err)
	}
	if err := writer.WriteField("project_id", projectID); err != nil {
		t.Fatalf("write project_id part: %v", err)
	}
	archivePart, err := writer.CreateFormFile("archive", "archive.zip")
	if err != nil {
		t.Fatalf("create archive part: %v", err)
	}
	if _, err := io.Copy(archivePart, bytes.NewReader(archiveZip)); err != nil {
		t.Fatalf("write archive part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return &body, writer.FormDataContentType()
}
