package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

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
