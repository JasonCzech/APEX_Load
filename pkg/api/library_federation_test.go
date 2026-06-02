package api

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
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
		Name:             "Team Host",
		BaseURL:          "https://host.example.test",
		DefaultProjectID: "team-project",
		AuthMethod:       "service_account",
		APIKey:           "secret-token",
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
	if listed.Remotes[0].APIKey != "" {
		t.Fatal("remote list response leaked api key")
	}
}

func TestLibraryResultArchiveStreamsAndImportsMultipart(t *testing.T) {
	host := NewServer(ServerConfig{})
	target := NewServer(ServerConfig{})
	script := host.runner.StoreScript(libraryTestScript("archive-script", "Archive Script", "team-project", "/archive"))
	testRun, err := host.runner.CreateTest(CreateTestRequest{
		Name:      "Archive Result",
		Duration:  "1m",
		ProjectID: "team-project",
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
	result := bundle.Items[0].Result
	if result.ArchiveTransfer != "stream" {
		t.Fatalf("archive_transfer = %q, want stream", result.ArchiveTransfer)
	}
	if result.ArchiveZipB64 != "" {
		t.Fatal("streaming result export should not populate legacy archive_zip_b64")
	}
	if result.ArchiveManifestHash != hashArchiveManifest(record) {
		t.Fatalf("manifest hash = %q, want %q", result.ArchiveManifestHash, hashArchiveManifest(record))
	}
	if result.ArchiveSizeBytes != archiveDirectorySize(record.ArchivePath) {
		t.Fatalf("archive size = %d, want %d", result.ArchiveSizeBytes, archiveDirectorySize(record.ArchivePath))
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
	archivePath, _ := raw["archive_path"].(string)
	if archivePath == "" {
		t.Fatal("imported raw result did not record archive_path")
	}
	if _, err := os.Stat(filepath.Join(archivePath, "manifest.json")); err != nil {
		t.Fatalf("imported archive manifest missing: %v", err)
	}
}

func createLibraryRemoteForTest(t *testing.T, server *Server, baseURL string, projectID string) LibraryRemote {
	t.Helper()
	server.libraryCreds = NewMemoryLibraryCredentialStore()
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

func attachLibraryArchiveFixture(t *testing.T, server *Server, testRun *ManagedTest) *storage.RunArchiveRecord {
	t.Helper()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
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
	writeLibraryArchiveFile(t, filepath.Join(archiveDir, "manifest.json"), manifest)
	writeLibraryArchiveText(t, filepath.Join(archiveDir, "transactions.jsonl"), `{"action_name":"archive","duration_ms":42}`+"\n")
	writeLibraryArchiveText(t, filepath.Join(archiveDir, "resources.jsonl"), "")
	writeLibraryArchiveText(t, filepath.Join(archiveDir, "interval_summaries.jsonl"), "")

	record := &storage.RunArchiveRecord{
		TestID:           testRun.ID,
		SchemaVersion:    archiveSchemaVersion,
		ArchivePath:      archiveDir,
		ManifestPath:     filepath.Join(archiveDir, "manifest.json"),
		TransactionCount: 1,
		CreatedAt:        now,
		CompletedAt:      now,
	}
	server.runner.mu.Lock()
	stored := server.runner.tests[testRun.ID]
	if stored == nil {
		server.runner.mu.Unlock()
		t.Fatalf("test %s not found in runner", testRun.ID)
	}
	stored.Status = "COMPLETE"
	stored.Archive = record
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
