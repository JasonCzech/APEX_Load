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

func TestLibraryTransferResolverUsesObjectProject(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})
	script := libraryTestScript("owned", "Owned", storage.DefaultProjectID, "/")
	if _, err := server.runner.StoreScriptDurable(script); err != nil {
		t.Fatal(err)
	}
	body := marshalLibraryTestJSON(t, LibraryTransferRequest{Kind: "script", ID: script.ID, ProjectID: "project-a"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/library/export", bytes.NewReader(body))
	if projectID, status, _ := resolveLibraryTransferProjectID(req, store); projectID != "" || status != http.StatusBadRequest {
		t.Fatalf("resolver = %q, %d; want project mismatch rejection", projectID, status)
	}
}

func TestLibraryTransferResolverUsesEndpointSpecificObjectAndDestination(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "library-scope.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	if err := store.SaveProject(&storage.Project{ID: "project-b", Name: "Project B", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{Store: store})
	local := libraryTestScript("local", "Local", storage.DefaultProjectID, "/local")
	foreign := libraryTestScript("foreign", "Foreign", "project-b", "/foreign")
	for _, script := range []*dsl.Script{local, foreign} {
		if _, err := server.runner.StoreScriptDurable(script); err != nil {
			t.Fatal(err)
		}
	}

	for _, path := range []string{"/api/v1/library/export", "/api/v1/library/remotes/r/push"} {
		body := marshalLibraryTestJSON(t, LibraryTransferRequest{Kind: "script", ID: foreign.ID, LocalID: local.ID})
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
		if projectID, status, message := resolveLibraryTransferProjectID(req, store); projectID != "project-b" || status != 0 || message != "" {
			t.Fatalf("%s resolver = %q, %d, %q; want project-b", path, projectID, status, message)
		}
	}

	diffBody := marshalLibraryTestJSON(t, LibraryTransferRequest{Kind: "script", LocalID: local.ID, RemoteID: foreign.ID})
	diffReq := httptest.NewRequest(http.MethodPost, "/api/v1/library/remotes/r/diff", bytes.NewReader(diffBody))
	if projectID, status, _ := resolveLibraryTransferProjectID(diffReq, store); projectID != storage.DefaultProjectID || status != 0 {
		t.Fatalf("diff resolver = %q, %d; want local project", projectID, status)
	}

	pullBody := marshalLibraryTestJSON(t, LibraryTransferRequest{Kind: "script", ID: foreign.ID})
	pullReq := httptest.NewRequest(http.MethodPost, "/api/v1/library/remotes/r/pull", bytes.NewReader(pullBody))
	if projectID, status, message := resolveLibraryTransferProjectID(pullReq, store); projectID != "" || status != http.StatusBadRequest || !strings.Contains(message, "project_id is required") {
		t.Fatalf("pull resolver = %q, %d, %q; want explicit destination rejection", projectID, status, message)
	}
}

func TestLibraryImportRequiresExplicitDestinationAndSingleSourceProject(t *testing.T) {
	server := NewServer(ServerConfig{})
	scriptA := libraryTestScript("source-a", "Source A", "source-project-a", "/a")
	scriptB := libraryTestScript("source-b", "Source B", "source-project-b", "/b")
	bundle := LibraryBundle{
		SchemaVersion: libraryBundleSchemaVersion,
		Source:        LibraryBundleSource{InstanceID: "remote", ProjectID: "source-project-a"},
		Items: []LibraryBundleItem{{
			Kind: "script", SourceInstanceID: "remote", SourceItemID: scriptA.ID, ProjectID: "source-project-a", Script: scriptA,
		}},
	}

	missingBody := marshalLibraryTestJSON(t, LibraryImportRequest{Bundle: bundle})
	missingResp := httptest.NewRecorder()
	server.mux.ServeHTTP(missingResp, httptest.NewRequest(http.MethodPost, "/api/v1/library/import", bytes.NewReader(missingBody)))
	if missingResp.Code != http.StatusBadRequest || !strings.Contains(missingResp.Body.String(), "project_id is required") {
		t.Fatalf("missing destination = %d %s", missingResp.Code, missingResp.Body.String())
	}

	bundle.Items = append(bundle.Items, LibraryBundleItem{
		Kind: "script", SourceInstanceID: "remote", SourceItemID: scriptB.ID, ProjectID: "source-project-b", Script: scriptB,
	})
	mixedBody := marshalLibraryTestJSON(t, LibraryImportRequest{Bundle: bundle, ProjectID: "destination-project"})
	mixedResp := httptest.NewRecorder()
	server.mux.ServeHTTP(mixedResp, httptest.NewRequest(http.MethodPost, "/api/v1/library/import", bytes.NewReader(mixedBody)))
	if mixedResp.Code != http.StatusBadRequest || !strings.Contains(mixedResp.Body.String(), "multiple source projects") {
		t.Fatalf("mixed source projects = %d %s", mixedResp.Code, mixedResp.Body.String())
	}
	if len(server.runner.ListScripts()) != 0 {
		t.Fatal("invalid mixed-project bundle wrote assets")
	}
}

func TestLibrarySyncLinkCannotMoveAssetAcrossDestinationProjects(t *testing.T) {
	server := NewServer(ServerConfig{})
	script := libraryTestScript("remote-script", "Remote script", "source-project", "/before")
	item := LibraryBundleItem{
		Kind: "script", SourceInstanceID: "remote-instance", SourceItemID: script.ID,
		ProjectID: "source-project", Name: script.Name, Script: script, ContentHash: hashLibraryScript(script),
	}
	bundle := LibraryBundle{
		SchemaVersion: libraryBundleSchemaVersion,
		BundleID:      "bundle-sync-isolation",
		Source:        LibraryBundleSource{InstanceID: "remote-instance", ProjectID: "source-project"},
		Items:         []LibraryBundleItem{item},
	}
	first, err := server.importLibraryBundle(LibraryImportRequest{Bundle: bundle, ProjectID: "destination-a"}, "remote-a")
	if err != nil || len(first.Items) != 1 {
		t.Fatalf("first import = %#v, %v", first, err)
	}
	localID := first.Items[0].LocalItemID

	second, err := server.importLibraryBundle(LibraryImportRequest{Bundle: bundle, ProjectID: "destination-b"}, "remote-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 0 || len(second.Conflicts) != 1 || !strings.Contains(second.Conflicts[0].Reason, "different destination project") {
		t.Fatalf("cross-project sync result = %#v", second)
	}
	stored, ok := server.runner.GetScript(localID)
	if !ok || stored.ProjectID != "destination-a" {
		t.Fatalf("sync link moved the local asset: %#v", stored)
	}
}

func TestLibraryImportRejectsTamperedPayloadHash(t *testing.T) {
	server := NewServer(ServerConfig{})
	script := libraryTestScript("source", "Source", "source-project", "/original")
	item := LibraryBundleItem{Kind: "script", SourceInstanceID: "remote", SourceItemID: script.ID, ProjectID: script.ProjectID, Script: script, ContentHash: hashLibraryScript(script)}
	item.Script.Actions[0].Target = "/tampered"
	result, err := server.importLibraryBundle(LibraryImportRequest{ProjectID: "destination", Bundle: LibraryBundle{
		SchemaVersion: libraryBundleSchemaVersion, Source: LibraryBundleSource{InstanceID: "remote", ProjectID: "source-project"}, Items: []LibraryBundleItem{item},
	}}, "remote")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Conflicts) != 1 || !strings.Contains(result.Conflicts[0].Reason, "content hash") {
		t.Fatalf("tampered import result = %#v", result)
	}
}

func TestLibraryImportRejectsMissingAndTamperedResultPayloads(t *testing.T) {
	server := NewServer(ServerConfig{})
	missing, err := server.importLibraryBundle(LibraryImportRequest{ProjectID: "destination", Bundle: LibraryBundle{
		SchemaVersion: libraryBundleSchemaVersion,
		Items:         []LibraryBundleItem{{Kind: "script", SourceInstanceID: "remote", SourceItemID: "missing", ContentHash: hashCanonical(nil)}},
	}}, "remote")
	if err != nil {
		t.Fatal(err)
	}
	if len(missing.Conflicts) != 1 || !strings.Contains(missing.Conflicts[0].Reason, "payload is missing") {
		t.Fatalf("missing script result = %#v", missing)
	}

	testRun := &ManagedTest{ID: "result", Name: "Result", Status: "COMPLETE", ProjectID: "source", TotalVUsers: 2, DurationStr: "1m"}
	item := LibraryBundleItem{
		Kind: "result", SourceInstanceID: "remote", SourceItemID: testRun.ID,
		ContentHash: hashLibraryResult(testRun),
		Result: &LibraryResultPayload{Test: testRun, Summary: map[string]any{
			"status": "TAMPERED", "total_vusers": testRun.TotalVUsers, "duration": testRun.DurationStr,
		}},
	}
	tampered, err := server.importLibraryBundle(LibraryImportRequest{ProjectID: "destination", Bundle: LibraryBundle{SchemaVersion: libraryBundleSchemaVersion, Items: []LibraryBundleItem{item}}}, "remote")
	if err != nil {
		t.Fatal(err)
	}
	if len(tampered.Conflicts) != 1 || !strings.Contains(tampered.Conflicts[0].Reason, "content hash") {
		t.Fatalf("tampered result = %#v", tampered)
	}

	item.ContentHash = hashLibraryResultTestOnly(testRun)
	item.Result.Summary = libraryResultSummary(testRun)
	legacy, err := server.importLibraryBundle(LibraryImportRequest{ProjectID: "destination", Bundle: LibraryBundle{SchemaVersion: libraryBundleSchemaVersion, Items: []LibraryBundleItem{item}}}, "remote")
	if err != nil || len(legacy.Conflicts) != 0 || len(legacy.Items) != 1 {
		t.Fatalf("legacy result bundle was not accepted safely: %#v, %v", legacy, err)
	}

	item.Result.Test = nil
	nilTest, err := server.importLibraryBundle(LibraryImportRequest{ProjectID: "destination", Bundle: LibraryBundle{SchemaVersion: libraryBundleSchemaVersion, Items: []LibraryBundleItem{item}}}, "remote")
	if err != nil {
		t.Fatal(err)
	}
	if len(nilTest.Conflicts) != 1 || !strings.Contains(nilTest.Conflicts[0].Reason, "test payload is missing") {
		t.Fatalf("nil result test = %#v", nilTest)
	}
}

func TestLibraryScenarioImportClonesAndVerifiesDependencies(t *testing.T) {
	server := NewServer(ServerConfig{})
	source := libraryTestScript("source-script", "Source", "source", "/source")
	req := CreateScenarioRequest{Name: "Scenario", Duration: "1m", Groups: []CreateGroupEntry{{Name: "g", ScriptID: source.ID, VUsers: 1}}}
	bundle := LibraryBundle{SchemaVersion: libraryBundleSchemaVersion, Items: []LibraryBundleItem{
		{Kind: "script", SourceInstanceID: "remote", SourceItemID: source.ID, ProjectID: "source", Script: source, ContentHash: hashLibraryScript(source)},
		{Kind: "scenario", SourceInstanceID: "remote", SourceItemID: "scenario", ProjectID: "source", ContentHash: hashCanonical(req), Scenario: &LibraryScenarioPayload{ID: "scenario", Request: req}, Dependencies: []LibraryDependency{{Kind: "script", ID: source.ID, ContentHash: hashLibraryScript(source)}}},
	}}
	result, err := server.importLibraryBundle(LibraryImportRequest{ProjectID: "destination", Bundle: bundle}, "remote")
	if err != nil || len(result.Conflicts) != 0 || len(result.Items) != 2 {
		t.Fatalf("scenario import = %#v, %v", result, err)
	}
	if got := bundle.Items[1].Scenario.Request.Groups[0].ScriptID; got != source.ID {
		t.Fatalf("source bundle mutated to local script id %q", got)
	}

	wrong := libraryTestScript(source.ID, "Wrong", "destination", "/wrong")
	server2 := NewServer(ServerConfig{})
	if _, err := server2.runner.StoreScriptDurable(wrong); err != nil {
		t.Fatal(err)
	}
	dependencyOnly := bundle.Items[1]
	mismatch, err := server2.importLibraryBundle(LibraryImportRequest{ProjectID: "destination", Bundle: LibraryBundle{SchemaVersion: libraryBundleSchemaVersion, Items: []LibraryBundleItem{dependencyOnly}}}, "remote")
	if err != nil {
		t.Fatal(err)
	}
	if len(mismatch.Conflicts) != 1 || !strings.Contains(mismatch.Conflicts[0].Reason, "dependency hash") {
		t.Fatalf("dependency mismatch = %#v", mismatch)
	}
}

func TestLibraryArchiveMetadataRequiresDeclaredManifest(t *testing.T) {
	result := &LibraryResultPayload{ArchiveManifestHash: "sha256:declared", ArchiveSizeBytes: 10}
	if reason := validateLibraryArchiveMetadata(result, "", 10); !strings.Contains(reason, "manifest hash") {
		t.Fatalf("missing manifest reason = %q", reason)
	}
}

func TestLibraryRemoteJSONLimitMatchesServerEnvelope(t *testing.T) {
	if libraryRemoteJSONMaxBytes != defaultMaxRequestBodyBytes {
		t.Fatalf("remote JSON limit = %d, server envelope = %d", libraryRemoteJSONMaxBytes, defaultMaxRequestBodyBytes)
	}
}

func TestLibraryScenarioImportRejectsCrossProjectScript(t *testing.T) {
	server := NewServer(ServerConfig{})
	foreign := libraryTestScript("foreign", "Foreign", "project-b", "/foreign")
	if _, err := server.runner.StoreScriptDurable(foreign); err != nil {
		t.Fatal(err)
	}
	req := CreateScenarioRequest{Name: "Cross project", Duration: "1m", Groups: []CreateGroupEntry{{Name: "g", ScriptID: foreign.ID, VUsers: 1}}}
	item := LibraryBundleItem{Kind: "scenario", SourceInstanceID: "remote", SourceItemID: "scenario", ProjectID: "source-project", ContentHash: hashCanonical(req), Scenario: &LibraryScenarioPayload{ID: "scenario", Request: req}}
	result, err := server.importLibraryBundle(LibraryImportRequest{ProjectID: "project-a", Bundle: LibraryBundle{
		SchemaVersion: libraryBundleSchemaVersion, Source: LibraryBundleSource{InstanceID: "remote", ProjectID: "source-project"}, Items: []LibraryBundleItem{item},
	}}, "remote")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Conflicts) != 1 || !strings.Contains(result.Conflicts[0].Reason, "different destination project") {
		t.Fatalf("cross-project scenario result = %#v", result)
	}
}
