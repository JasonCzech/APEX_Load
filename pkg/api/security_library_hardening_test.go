package api

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/scrub"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestLibraryResultCatalogAndExportAllowOnlyTerminalStatuses(t *testing.T) {
	server := NewServer(ServerConfig{})
	statuses := []string{"QUEUED", "RESERVED", "INITIALIZING", "PENDING", "RUNNING", "PAUSED", "SHAKEOUT_REVIEW", "COMPLETE", "FAILED", "ABORTED", "UNKNOWN"}
	server.runner.mu.Lock()
	for index, status := range statuses {
		id := "status-" + strings.ToLower(status)
		server.runner.tests[id] = &ManagedTest{ID: id, Name: status, Status: status, ProjectID: "project-a", CreatedAt: time.Unix(int64(index+1), 0).UTC()}
	}
	server.runner.mu.Unlock()

	catalog := server.libraryCatalogItems("project-a", "result")
	if len(catalog) != 3 {
		t.Fatalf("terminal result catalog count = %d, want 3: %#v", len(catalog), catalog)
	}
	for _, status := range statuses {
		_, err := server.libraryResultBundleItem("status-" + strings.ToLower(status))
		if isTerminalTestStatus(status) && err != nil {
			t.Errorf("terminal status %s was rejected: %v", status, err)
		}
		if !isTerminalTestStatus(status) && err == nil {
			t.Errorf("nonterminal status %s was exported", status)
		}
	}
}

func TestLibraryCatalogPaginationBoundsResultHashing(t *testing.T) {
	server := NewServer(ServerConfig{})
	server.runner = NewTestRunner(2, NewHub(), nil)
	base := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	server.runner.mu.Lock()
	for _, id := range []string{"result-c", "result-a", "result-b", "result-e", "result-d"} {
		server.runner.tests[id] = &ManagedTest{
			ID: id, Name: "same-name", Status: "COMPLETE", ProjectID: "project-a", CreatedAt: base,
			History: []*LiveMetrics{{Timestamp: base, TPS: 1}, {Timestamp: base.Add(time.Second), TPS: 2}},
		}
	}
	server.runner.mu.Unlock()

	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/api/v1/library/catalog?kind=result&project_id=project-a&limit=2&offset=1", nil))
	if resp.Code != http.StatusOK {
		t.Fatalf("catalog page status = %d body=%s", resp.Code, resp.Body.String())
	}
	var page LibraryCatalogResponse
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	if page.Count != 2 || page.Total != 5 || page.Limit != 2 || page.Offset != 1 || !page.HasMore {
		t.Fatalf("catalog pagination metadata = %#v", page)
	}
	if len(page.Items) != 2 || page.Items[0].ID != "result-b" || page.Items[1].ID != "result-c" {
		t.Fatalf("deterministic catalog page = %#v, want result-b/result-c", page.Items)
	}
	bundleItem, err := server.libraryResultBundleItem(page.Items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if page.Items[0].ContentHash != bundleItem.ContentHash {
		t.Fatalf("paged catalog hash = %q, export hash = %q", page.Items[0].ContentHash, bundleItem.ContentHash)
	}

	oversized := httptest.NewRecorder()
	server.mux.ServeHTTP(oversized, httptest.NewRequest(http.MethodGet, "/api/v1/library/catalog?kind=result&limit=101", nil))
	if oversized.Code != http.StatusBadRequest {
		t.Fatalf("oversized catalog page status = %d, want 400; body=%s", oversized.Code, oversized.Body.String())
	}
}

func TestExpandedLibraryBundleRequiresEveryServiceAccountScope(t *testing.T) {
	bundle := &LibraryBundle{Items: []LibraryBundleItem{{Kind: "scenario"}, {Kind: "script"}, {Kind: "result"}}}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/library/remotes/remote/push", nil)
	req = withAuthenticatedContext(req, authResolution{ServiceAccount: &AuthenticatedServiceAccount{
		ID: "narrow", Scopes: []string{"tests:run"},
	}})
	resp := httptest.NewRecorder()
	if requireServiceAccountLibraryBundleScopes(resp, req, bundle) {
		t.Fatal("narrow service account authorized an expanded mixed-kind bundle")
	}
	if resp.Code != http.StatusForbidden {
		t.Fatalf("narrow bundle status = %d, want 403", resp.Code)
	}

	req = withAuthenticatedContext(httptest.NewRequest(http.MethodPost, "/api/v1/library/remotes/remote/push", nil), authResolution{ServiceAccount: &AuthenticatedServiceAccount{
		ID: "complete", Scopes: []string{"tests:run", "scripts:write", "results:write"},
	}})
	if !requireServiceAccountLibraryBundleScopes(httptest.NewRecorder(), req, bundle) {
		t.Fatal("service account with every expanded bundle scope was rejected")
	}

	server := NewServer(ServerConfig{})
	script := server.runner.StoreScript(libraryTestScript("scope-script", "Scope Script", "project-a", "/"))
	scenario, err := server.runner.StoreScenario(CreateScenarioRequest{
		Name: "Scope Scenario", Duration: "1m",
		Groups: []CreateGroupEntry{{Name: "group", ScriptID: script.ID, VUsers: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	exportBody, _ := json.Marshal(LibraryTransferRequest{Kind: "scenario", ID: scenario.ID, IncludeDependencies: true})
	exportReq := httptest.NewRequest(http.MethodPost, "/api/v1/library/export", bytes.NewReader(exportBody))
	exportReq = withAuthenticatedContext(exportReq, authResolution{ServiceAccount: &AuthenticatedServiceAccount{ID: "scenario-only", Scopes: []string{"tests:run"}}})
	exportResp := httptest.NewRecorder()
	server.handleLibraryExport(exportResp, exportReq)
	if exportResp.Code != http.StatusForbidden {
		t.Fatalf("dependency-expanded direct export status = %d, want 403; body=%s", exportResp.Code, exportResp.Body.String())
	}

	ctx := context.WithValue(context.Background(), authServiceAccountContextKey{}, &AuthenticatedServiceAccount{ID: "remote-run", Scopes: []string{"tests:run"}})
	_, err = server.pushRemoteWorkspaceItem(ctx, LibraryRemote{ID: "remote", BaseURL: "https://remote.example"}, "script", script.ID, "project-a", "project-a", false)
	if err == nil || !strings.Contains(err.Error(), "expanded library bundle") {
		t.Fatalf("remote-run implicit script push error = %v, want expanded-scope denial", err)
	}
}

func TestImportedArchiveVerificationRejectsTamperAndSmuggledArtifact(t *testing.T) {
	server := NewServer(ServerConfig{})
	testRun := &ManagedTest{ID: "integrity-run", Name: "Integrity", Status: "COMPLETE", ProjectID: "project-a"}
	server.runner.mu.Lock()
	server.runner.tests[testRun.ID] = testRun
	server.runner.mu.Unlock()
	record := attachLibraryArchiveFixture(t, server, testRun)
	result := &LibraryResultPayload{
		ArchiveManifestHash: hashArchiveManifest(record),
		ArchiveSizeBytes:    archiveDirectorySize(record.ArchivePath),
	}

	transactionsPath := filepath.Join(record.ArchivePath, "transactions.jsonl")
	original, err := os.ReadFile(transactionsPath)
	if err != nil {
		t.Fatal(err)
	}
	tampered := bytes.Replace(original, []byte(`"duration_ms":42`), []byte(`"duration_ms":43`), 1)
	if len(tampered) != len(original) || bytes.Equal(tampered, original) {
		t.Fatal("test fixture did not produce a same-size content change")
	}
	if err := os.WriteFile(transactionsPath, tampered, 0o644); err != nil {
		t.Fatal(err)
	}
	if reason := verifyImportedLibraryArchive(result, record.ArchivePath); !strings.Contains(reason, "integrity verification failed") {
		t.Fatalf("same-size tamper reason = %q", reason)
	}

	if err := os.WriteFile(transactionsPath, original, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(record.ArchivePath, "smuggled.secret"), []byte("credential"), 0o600); err != nil {
		t.Fatal(err)
	}
	if reason := verifyImportedLibraryArchive(result, record.ArchivePath); !strings.Contains(reason, "undeclared or unhashed artifact smuggled.secret") {
		t.Fatalf("smuggled artifact reason = %q", reason)
	}
	if err := os.Remove(filepath.Join(record.ArchivePath, "smuggled.secret")); err != nil {
		t.Fatal(err)
	}
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Artifacts = nil
	writeLibraryArchiveFile(t, record.ManifestPath, manifest)
	result.ArchiveManifestHash = hashArchiveManifest(record)
	if reason := verifyImportedLibraryArchive(result, record.ArchivePath); !strings.Contains(reason, "no per-artifact SHA-256 entries") {
		t.Fatalf("legacy hash-less archive reason = %q", reason)
	}
}

func TestPortableLibraryArchiveRecordStripsInternalLocationsWhenMaterializationFails(t *testing.T) {
	server := NewServer(ServerConfig{})
	record := &storage.RunArchiveRecord{
		TestID: "portable-run", SchemaVersion: archiveSchemaVersion, ArchiveBackend: "unsupported-internal",
		ArchivePath: "/srv/apexload/private/run", ManifestPath: "/srv/apexload/private/run/manifest.json",
		ArchiveURI: "azblob://internal/private/run", ManifestURI: "azblob://internal/private/run/manifest.json",
		ManifestSHA256: "internal-root-hash",
	}
	testRun := &ManagedTest{ID: record.TestID, Name: "Portable", Status: "COMPLETE", ProjectID: "project-a", Archive: record}
	server.runner.mu.Lock()
	server.runner.tests[testRun.ID] = testRun
	server.runner.archives[testRun.ID] = record
	server.runner.mu.Unlock()

	item, err := server.libraryResultBundleItem(testRun.ID)
	if err != nil {
		t.Fatal(err)
	}
	for label, archive := range map[string]*storage.RunArchiveRecord{"result": item.Result.Archive, "test": item.Result.Test.Archive} {
		if archive == nil {
			t.Fatalf("%s archive is nil", label)
		}
		if archive.ArchiveBackend != "" || archive.ArchivePath != "" || archive.ManifestPath != "" || archive.ArchiveURI != "" || archive.ManifestURI != "" || archive.ManifestSHA256 != "" {
			t.Fatalf("%s archive leaked internal location after materialization failure: %#v", label, archive)
		}
	}
	if record.ArchivePath == "" || record.ArchiveURI == "" {
		t.Fatal("portable export mutated the stored archive record")
	}
}

func TestLibraryResultImportMasksUntrustedRuntimeAndArchiveLocations(t *testing.T) {
	const secret = "untrusted-import-secret"
	server := NewServer(ServerConfig{})
	archive := &storage.RunArchiveRecord{
		TestID: "untrusted-run", ArchiveBackend: "s3", ArchivePath: "/private/source/run", ManifestPath: "/private/source/run/manifest.json",
		ArchiveURI: "s3://private/source/run", ManifestURI: "s3://private/source/run/manifest.json", ManifestSHA256: strings.Repeat("a", 64),
	}
	testRun := &ManagedTest{
		ID: "untrusted-run", Name: "Untrusted", Status: "COMPLETE", ProjectID: "source", Archive: archive,
		RuntimeSettings: &RuntimeSettingsOverride{
			Env:  map[string]string{"API_TOKEN": secret},
			HTTP: &dsl.HTTPConfig{DefaultHeaders: map[string]string{"Authorization": secret}},
		},
	}
	summary := libraryResultSummary(testRun)
	item := LibraryBundleItem{
		Kind: "result", SourceInstanceID: "untrusted", SourceItemID: testRun.ID, Name: testRun.Name,
		ContentHash: hashLibraryResultWithSummary(testRun, summary), ContentHashVersion: libraryResultHashVersionCurrent,
		Result: &LibraryResultPayload{Test: testRun, Archive: archive, Summary: summary},
	}
	result, err := server.importLibraryBundle(LibraryImportRequest{ProjectID: "destination", Bundle: LibraryBundle{
		SchemaVersion: libraryBundleSchemaVersion, Items: []LibraryBundleItem{item},
	}}, "remote")
	if err != nil || len(result.Conflicts) != 0 {
		t.Fatalf("import result = %#v, %v", result, err)
	}
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/api/v1/results/imports?project_id=destination", nil))
	if resp.Code != http.StatusOK {
		t.Fatalf("result import list status = %d body=%s", resp.Code, resp.Body.String())
	}
	body := resp.Body.String()
	for _, leaked := range []string{secret, archive.ArchivePath, archive.ManifestPath, archive.ArchiveURI, archive.ManifestURI} {
		if strings.Contains(body, leaked) {
			t.Fatalf("result import list leaked %q: %s", leaked, body)
		}
	}
	if !strings.Contains(body, scrub.Redacted) {
		t.Fatalf("result import list omitted the redaction sentinel: %s", body)
	}
}

func TestHistoricalArchiveSecretSurfacesAreMasked(t *testing.T) {
	const secret = "raw-historical-runtime-secret"
	server := NewServer(ServerConfig{})
	// Keep this disclosure-boundary regression independent of HA lease refreshes;
	// it exercises the same server handlers with a local in-memory runner.
	server.runner = NewTestRunner(2, NewHub(), nil)
	script := server.runner.StoreScript(libraryTestScript("secret-script", "Secret Script", "project-a", "/"))
	testRun, err := server.runner.CreateTest(CreateTestRequest{
		Name: "Historical Secret", Duration: "1m", ProjectID: "project-a",
		Groups: []CreateGroupEntry{{Name: "group", ScriptID: script.ID, VUsers: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	record := attachLibraryArchiveFixture(t, server, testRun)
	runtimeSettings := &RuntimeSettingsOverride{
		BaseURL: "https://" + secret + ":" + secret + "@example.test",
		Env:     map[string]string{"API_TOKEN": secret},
		HTTP: &dsl.HTTPConfig{
			Auth:           dsl.AuthConfig{Password: secret, Token: secret},
			Proxy:          dsl.ProxyConfig{URL: "http://" + secret + ":" + secret + "@proxy.test:8080", Username: secret, Password: secret},
			DefaultHeaders: map[string]string{"Authorization": secret, "Cookie": secret, "X-API-Key": secret},
		},
		BrowserConfig: &dsl.BrowserConfig{
			HTTPCredentials: dsl.BrowserHTTPCredentials{Password: secret},
			Proxy:           dsl.BrowserProxyConfig{Server: "http://" + secret + ":" + secret + "@browser-proxy.test:8080", Username: secret, Password: secret},
			ClientCertificates: []dsl.BrowserClientCertificate{{
				Origin: "https://example.test", PfxPath: "client.pfx", Passphrase: secret,
			}},
		},
	}
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Provenance.RuntimeSettings = runtimeSettings
	manifest.Provenance.Groups = []TestGroup{{Name: "group", RuntimeSettings: runtimeSettings}}
	manifest.TestManifestFile = testRunManifestFileName
	testManifest := &TestRunManifest{
		SchemaVersion: testRunManifestSchemaVersion, TestID: testRun.ID, TestName: testRun.Name, Status: "COMPLETE", Source: "historical",
		InitialConfiguration: TestRunManifestConfiguration{RuntimeSettings: runtimeSettings, Groups: []TestGroup{{Name: "group", RuntimeSettings: runtimeSettings}}},
		CurrentConfiguration: TestRunManifestConfiguration{RuntimeSettings: runtimeSettings, Groups: []TestGroup{{Name: "group", RuntimeSettings: runtimeSettings}}},
		Scripts:              []TestRunManifestScript{{GroupName: "group", RuntimeSettings: runtimeSettings}},
		Archive:              manifestArchiveFromRecord(record, manifest),
	}
	writeLibraryArchiveFile(t, filepath.Join(record.ArchivePath, testRunManifestFileName), testManifest)
	if err := updateRunArchiveArtifactIntegrity(record.ArchivePath, manifest); err != nil {
		t.Fatal(err)
	}
	writeLibraryArchiveFile(t, record.ManifestPath, manifest)
	record.ManifestSHA256 = strings.TrimPrefix(hashArchiveManifest(record), "sha256:")

	assertSafeResponse := func(method, path string, body io.Reader) []byte {
		t.Helper()
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, httptest.NewRequest(method, path, body))
		if resp.Code != http.StatusOK {
			t.Fatalf("%s %s status = %d body=%s", method, path, resp.Code, resp.Body.String())
		}
		data := resp.Body.Bytes()
		if bytes.Contains(data, []byte(secret)) {
			t.Fatalf("%s %s leaked historical runtime secret: %s", method, path, data)
		}
		for _, leaked := range []string{record.ArchivePath, record.ManifestPath, record.ArchiveURI, record.ManifestURI} {
			if leaked != "" && bytes.Contains(data, []byte(leaked)) {
				t.Fatalf("%s %s leaked archive location %q: %s", method, path, leaked, data)
			}
		}
		if !bytes.Contains(data, []byte(scrub.Redacted)) {
			t.Fatalf("%s %s did not contain the redaction sentinel: %s", method, path, data)
		}
		return append([]byte(nil), data...)
	}

	manifestResponse := assertSafeResponse(http.MethodGet, "/api/v1/tests/"+testRun.ID+"/manifest", nil)
	if bytes.Contains(manifestResponse, []byte(`"archive_backend"`)) {
		t.Fatalf("test manifest disclosed its archive backend: %s", manifestResponse)
	}
	assertSafeResponse(http.MethodGet, "/api/v1/tests/"+testRun.ID+"/archive/manifest", nil)
	assertSafeResponse(http.MethodGet, "/api/v1/tests/"+testRun.ID+"/archive/files/"+runArchiveManifestFileName, nil)
	assertSafeResponse(http.MethodGet, "/api/v1/tests/"+testRun.ID+"/archive/files/"+testRunManifestFileName, nil)
	assertSafeResponse(http.MethodGet, "/api/v1/tests/"+testRun.ID+"/archive/report", nil)
	batchBody, _ := json.Marshal(map[string]any{"ids": []string{testRun.ID}})
	assertSafeResponse(http.MethodPost, "/api/v1/tests/archive/reports?project_id=project-a", bytes.NewReader(batchBody))

	assertNoArchiveLocation := func(path string) {
		t.Helper()
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, path, nil))
		if resp.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d body=%s", path, resp.Code, resp.Body.String())
		}
		for _, leaked := range []string{record.ArchivePath, record.ManifestPath, record.ArchiveURI, record.ManifestURI} {
			if leaked != "" && bytes.Contains(resp.Body.Bytes(), []byte(leaked)) {
				t.Fatalf("GET %s leaked archive location %q: %s", path, leaked, resp.Body.String())
			}
		}
	}
	assertNoArchiveLocation("/api/v1/tests/" + testRun.ID)
	assertNoArchiveLocation("/api/v1/tests?project_id=project-a")

	assertSafeZip := func(path string) {
		t.Helper()
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, path, nil))
		if resp.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d body=%s", path, resp.Code, resp.Body.String())
		}
		reader, err := zip.NewReader(bytes.NewReader(resp.Body.Bytes()), int64(resp.Body.Len()))
		if err != nil {
			t.Fatalf("open %s zip: %v", path, err)
		}
		var structured bytes.Buffer
		for _, file := range reader.File {
			if file.Name != runArchiveManifestFileName && file.Name != testRunManifestFileName && file.Name != "browser_diagnostics.json" {
				continue
			}
			opened, err := file.Open()
			if err != nil {
				t.Fatal(err)
			}
			_, copyErr := io.Copy(&structured, opened)
			closeErr := opened.Close()
			if copyErr != nil {
				t.Fatal(copyErr)
			}
			if closeErr != nil {
				t.Fatal(closeErr)
			}
		}
		if strings.Contains(structured.String(), secret) {
			t.Fatalf("GET %s zip leaked historical runtime secret: %s", path, structured.String())
		}
		for _, leaked := range []string{record.ArchivePath, record.ManifestPath, record.ArchiveURI, record.ManifestURI} {
			if leaked != "" && strings.Contains(structured.String(), leaked) {
				t.Fatalf("GET %s zip leaked archive location %q: %s", path, leaked, structured.String())
			}
		}
		if !strings.Contains(structured.String(), scrub.Redacted) {
			t.Fatalf("GET %s zip did not contain redaction sentinel", path)
		}
	}
	assertSafeZip("/api/v1/tests/" + testRun.ID + "/archive")
	assertSafeZip("/api/v1/library/results/" + testRun.ID + "/archive")
	assertSafeZip("/api/v1/tests/" + testRun.ID + "/archive/browser-diagnostics")

	exportBody, _ := json.Marshal(LibraryTransferRequest{Kind: "result", ID: testRun.ID})
	exportResp := httptest.NewRecorder()
	server.mux.ServeHTTP(exportResp, httptest.NewRequest(http.MethodPost, "/api/v1/library/export", bytes.NewReader(exportBody)))
	if exportResp.Code != http.StatusOK {
		t.Fatalf("library export status = %d body=%s", exportResp.Code, exportResp.Body.String())
	}
	exported := exportResp.Body.Bytes()
	for _, leaked := range []string{secret, record.ArchivePath, record.ManifestPath, record.ArchiveURI, record.ManifestURI} {
		if leaked != "" && bytes.Contains(exported, []byte(leaked)) {
			t.Fatalf("library bundle leaked archive runtime data %q: %s", leaked, exported)
		}
	}

	rawManifest, err := os.ReadFile(record.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(rawManifest, []byte(secret)) {
		t.Fatal("historical fixture was mutated instead of being sanitized at the read boundary")
	}

	// The disclosure-safe manifest and ZIP are a derived export, with integrity
	// metadata adjusted to the sanitized test manifest rather than reusing stale
	// hashes from the historical raw artifact.
	safeManifest, err := sanitizedRunArchiveManifest(context.Background(), record, manifest)
	if err != nil {
		t.Fatal(err)
	}
	for _, artifact := range safeManifest.Artifacts {
		if artifact.Name == testRunManifestFileName && artifact.SHA256 == "" {
			t.Fatal("safe test manifest artifact lost its integrity hash")
		}
	}
}
