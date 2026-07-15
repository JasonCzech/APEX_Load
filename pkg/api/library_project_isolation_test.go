package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
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

func TestAllowedRemoteProjectIDRejectsCallerRedirect(t *testing.T) {
	configured := LibraryRemote{ProjectMappings: map[string]string{"local-project": "remote-approved"}}
	if got, err := allowedRemoteProjectID(configured, "local-project", ""); err != nil || got != "remote-approved" {
		t.Fatalf("configured mapping = %q, %v; want remote-approved", got, err)
	}
	if got, err := allowedRemoteProjectID(configured, "local-project", "remote-approved"); err != nil || got != "remote-approved" {
		t.Fatalf("matching compatibility override = %q, %v; want remote-approved", got, err)
	}
	if _, err := allowedRemoteProjectID(configured, "local-project", "remote-forbidden"); err == nil {
		t.Fatal("caller-controlled remote project redirect was accepted")
	}

	if _, err := allowedRemoteProjectID(LibraryRemote{DefaultProjectID: "remote-approved"}, "local-project", ""); err == nil {
		t.Fatal("legacy default_project_id implicitly authorized an unmapped local project")
	}
	if _, err := allowedRemoteProjectID(LibraryRemote{}, "local-project", "local-project"); err == nil {
		t.Fatal("unconfigured remote accepted an implicit identity mapping")
	}
	if _, err := allowedRemoteProjectID(configured, "", ""); err == nil {
		t.Fatal("remote catalog access without an authorized local project was accepted")
	}
}

func TestLibraryTransfersRejectStaleCrossProjectReplicaObjects(t *testing.T) {
	t.Setenv(remoteOutboundAllowPrivateHostsEnv, "1")
	t.Setenv("APEXLOAD_LIBRARY_ALLOW_INSECURE_HTTP", "1")
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "stale-library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	for _, projectID := range []string{"project-a", "project-b"} {
		if err := store.SaveProject(&storage.Project{ID: projectID, Name: projectID, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}

	server := NewServer(ServerConfig{Store: store, LibraryCredentials: NewMemoryLibraryCredentialStore()})
	stale := libraryTestScript("reused-script", "Stale A", "project-a", "/stale-a")
	if _, err := server.runner.StoreScriptDurable(stale); err != nil {
		t.Fatal(err)
	}
	// Simulate another replica deleting/recreating or moving the durable ID while
	// this replica still has the prior project-A object in memory.
	current := libraryTestScript(stale.ID, "Current B", "project-b", "/current-b")
	if err := store.DeleteScript(stale.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveScript(current); err != nil {
		t.Fatal(err)
	}

	remoteCalls := 0
	remoteHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		remoteCalls++
		writeError(w, http.StatusInternalServerError, "stale object escaped local validation")
	}))
	defer remoteHTTP.Close()
	server.libraryMu.Lock()
	server.libraryRemotes["workspace"] = &LibraryRemote{
		ID:         "workspace",
		Name:       "Workspace",
		BaseURL:    remoteHTTP.URL,
		AuthMethod: "none",
		ProjectMappings: map[string]string{
			"project-a": "remote-a",
			"project-b": "remote-b",
		},
	}
	server.libraryMu.Unlock()
	if err := server.persistLibraryRemotes(); err != nil {
		t.Fatal(err)
	}

	requests := []struct {
		name string
		path string
		body any
	}{
		{name: "direct export", path: "/api/v1/library/export", body: LibraryTransferRequest{Kind: "script", ID: stale.ID}},
		{name: "remote push", path: "/api/v1/library/remotes/workspace/push", body: LibraryTransferRequest{Kind: "script", ID: stale.ID}},
		{name: "remote diff", path: "/api/v1/library/remotes/workspace/diff", body: LibraryTransferRequest{Kind: "script", LocalID: stale.ID, RemoteID: stale.ID}},
		{name: "remote run", path: "/api/v1/remotes/workspace/runs", body: RemoteRunCreateRequest{CreateTestRequest: CreateTestRequest{Name: "stale", Duration: "30s", ScriptID: stale.ID, VUsers: 1}}},
	}
	for _, tc := range requests {
		t.Run(tc.name, func(t *testing.T) {
			before := remoteCalls
			response := httptest.NewRecorder()
			server.mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, tc.path, bytes.NewReader(marshalLibraryTestJSON(t, tc.body))))
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
			}
			if remoteCalls != before {
				t.Fatalf("stale cross-project object caused %d outbound request(s)", remoteCalls-before)
			}
		})
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

func TestLibraryImportRejectsAmbiguousCrossSourceScriptIdentity(t *testing.T) {
	server := NewServer(ServerConfig{})
	first := libraryTestScript("shared", "First", "source-project", "/first")
	second := libraryTestScript("shared", "Second", "source-project", "/second")
	bundle := LibraryBundle{SchemaVersion: libraryBundleSchemaVersion, Items: []LibraryBundleItem{
		{Kind: "script", SourceInstanceID: "source-a", SourceItemID: "shared", ProjectID: "source-project", Script: first, ContentHash: hashLibraryScript(first)},
		{Kind: "script", SourceInstanceID: "source-b", SourceItemID: "shared", ProjectID: "source-project", Script: second, ContentHash: hashLibraryScript(second)},
	}}
	result, err := server.importLibraryBundle(LibraryImportRequest{ProjectID: "destination", Bundle: bundle}, "remote")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Conflicts) == 0 || !strings.Contains(result.Conflicts[0].Reason+result.Conflicts[len(result.Conflicts)-1].Reason, "ambiguous script source_item_id") {
		t.Fatalf("conflicts = %#v, want ambiguous script identity", result.Conflicts)
	}
	if got := server.runner.ListScripts(); len(got) != 0 {
		t.Fatalf("ambiguous bundle wrote scripts: %#v", got)
	}
}

type concurrentLibraryScriptEditStore struct {
	storage.Store
	once sync.Once
}

func (s *concurrentLibraryScriptEditStore) CreateScriptIfAbsent(script *dsl.Script) (bool, error) {
	return s.Store.(interface {
		CreateScriptIfAbsent(*dsl.Script) (bool, error)
	}).CreateScriptIfAbsent(script)
}

func (s *concurrentLibraryScriptEditStore) UpdateScriptIfUnchanged(script *dsl.Script, expected time.Time) (bool, error) {
	s.once.Do(func() {
		current, err := s.Store.GetScript(script.ID)
		if err != nil {
			return
		}
		current.Name = "concurrent local edit"
		_ = s.Store.SaveScript(current)
	})
	return s.Store.(interface {
		UpdateScriptIfUnchanged(*dsl.Script, time.Time) (bool, error)
	}).UpdateScriptIfUnchanged(script, expected)
}

func TestLibraryScriptUpdateDoesNotOverwriteConcurrentLocalEdit(t *testing.T) {
	baseStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "library-cas.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer baseStore.Close()
	now := time.Now().UTC()
	if err := baseStore.SaveProject(&storage.Project{ID: "destination", Name: "Destination", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	store := &concurrentLibraryScriptEditStore{Store: baseStore}
	server := NewServer(ServerConfig{Store: store})
	source := libraryTestScript("source", "initial", "source-project", "/initial")
	source.RefKey = "SOURCE-REF-MUST-NOT-BE-COPIED"
	item := LibraryBundleItem{Kind: "script", SourceInstanceID: "remote", SourceItemID: source.ID, ProjectID: source.ProjectID, Script: source, ContentHash: hashLibraryScript(source)}
	bundle := LibraryBundle{SchemaVersion: libraryBundleSchemaVersion, Items: []LibraryBundleItem{item}}
	first, err := server.importLibraryBundle(LibraryImportRequest{ProjectID: "destination", Bundle: bundle}, "remote")
	if err != nil || len(first.Conflicts) != 0 || len(first.Items) != 1 {
		t.Fatalf("initial import = %#v, %v", first, err)
	}
	localID := first.Items[0].LocalItemID
	created, err := baseStore.GetScript(localID)
	if err != nil {
		t.Fatal(err)
	}
	if created.RefKey == source.RefKey || created.RefKey == "" {
		t.Fatalf("destination ref_key = %q, want fresh local reference", created.RefKey)
	}

	updatedSource := cloneScriptForLibrary(source)
	updatedSource.Name = "remote update"
	bundle.Items[0].Script = updatedSource
	bundle.Items[0].ContentHash = hashLibraryScript(updatedSource)
	second, err := server.importLibraryBundle(LibraryImportRequest{ProjectID: "destination", Bundle: bundle}, "remote")
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Conflicts) != 1 || !strings.Contains(second.Conflicts[0].Reason, "changed concurrently") {
		t.Fatalf("concurrent update result = %#v", second)
	}
	current, err := baseStore.GetScript(localID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Name != "concurrent local edit" {
		t.Fatalf("concurrent local edit was overwritten: %#v", current)
	}
}

type concurrentLibraryScenarioEditStore struct {
	storage.Store
	once sync.Once
}

func (s *concurrentLibraryScenarioEditStore) CreateScenarioWithDataPoolBindingsIfAbsent(scenario *storage.Scenario, scripts []storage.ScenarioScript, bindings []storage.ScenarioDataPoolBinding) (bool, error) {
	return s.Store.(interface {
		CreateScenarioWithDataPoolBindingsIfAbsent(*storage.Scenario, []storage.ScenarioScript, []storage.ScenarioDataPoolBinding) (bool, error)
	}).CreateScenarioWithDataPoolBindingsIfAbsent(scenario, scripts, bindings)
}

func (s *concurrentLibraryScenarioEditStore) UpdateScenarioWithDataPoolBindingsIfUnchanged(scenario *storage.Scenario, scripts []storage.ScenarioScript, bindings []storage.ScenarioDataPoolBinding, expected time.Time) (bool, error) {
	s.once.Do(func() {
		current, currentScripts, err := s.Store.GetScenario(scenario.ID)
		if err != nil {
			return
		}
		current.Name = "concurrent local scenario edit"
		_ = s.Store.SaveScenario(current, currentScripts)
	})
	return s.Store.(interface {
		UpdateScenarioWithDataPoolBindingsIfUnchanged(*storage.Scenario, []storage.ScenarioScript, []storage.ScenarioDataPoolBinding, time.Time) (bool, error)
	}).UpdateScenarioWithDataPoolBindingsIfUnchanged(scenario, scripts, bindings, expected)
}

func (s *concurrentLibraryScenarioEditStore) DeleteScenarioIfUnchanged(id string, expected time.Time) (bool, error) {
	return s.Store.(interface {
		DeleteScenarioIfUnchanged(string, time.Time) (bool, error)
	}).DeleteScenarioIfUnchanged(id, expected)
}

func TestLibraryScenarioUpdateDoesNotOverwriteConcurrentLocalEdit(t *testing.T) {
	baseStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "library-scenario-cas.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer baseStore.Close()
	now := time.Now().UTC()
	if err := baseStore.SaveProject(&storage.Project{ID: "destination", Name: "Destination", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	localScript := libraryTestScript("destination-script", "Destination Script", "destination", "/")
	if err := baseStore.SaveScript(localScript); err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{Store: &concurrentLibraryScenarioEditStore{Store: baseStore}})
	req := CreateScenarioRequest{Name: "initial remote scenario", Duration: "1m", Groups: []CreateGroupEntry{{Name: "group", ScriptID: localScript.ID, VUsers: 1}}}
	item := LibraryBundleItem{
		Kind: "scenario", SourceInstanceID: "remote", SourceItemID: "scenario", ProjectID: "source-project",
		Scenario: &LibraryScenarioPayload{ID: "scenario", Request: req}, ContentHash: hashCanonical(req),
		Dependencies: []LibraryDependency{{Kind: "script", ID: localScript.ID, ContentHash: hashLibraryScript(localScript)}},
	}
	bundle := LibraryBundle{SchemaVersion: libraryBundleSchemaVersion, Items: []LibraryBundleItem{item}}
	first, err := server.importLibraryBundle(LibraryImportRequest{ProjectID: "destination", Bundle: bundle}, "remote")
	if err != nil || len(first.Conflicts) != 0 || len(first.Items) != 1 {
		t.Fatalf("initial scenario import = %#v, %v", first, err)
	}
	localID := first.Items[0].LocalItemID
	req.Name = "remote scenario update"
	bundle.Items[0].Scenario = &LibraryScenarioPayload{ID: "scenario", Request: req}
	bundle.Items[0].ContentHash = hashCanonical(req)
	second, err := server.importLibraryBundle(LibraryImportRequest{ProjectID: "destination", Bundle: bundle}, "remote")
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Conflicts) != 1 || !strings.Contains(second.Conflicts[0].Reason, "changed concurrently") {
		t.Fatalf("concurrent scenario result = %#v", second)
	}
	current, _, err := baseStore.GetScenario(localID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Name != "concurrent local scenario edit" {
		t.Fatalf("concurrent local scenario edit was overwritten: %#v", current)
	}
}

func TestLibrarySyncLinkCreatesIndependentAssetsAcrossDestinationProjects(t *testing.T) {
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
	if len(second.Items) != 1 || len(second.Conflicts) != 0 {
		t.Fatalf("cross-project sync result = %#v", second)
	}
	if second.Items[0].LocalItemID == localID {
		t.Fatalf("cross-project imports reused local id %q", localID)
	}
	stored, ok := server.runner.GetScript(localID)
	if !ok || stored.ProjectID != "destination-a" {
		t.Fatalf("sync link moved the local asset: %#v", stored)
	}
	secondStored, ok := server.runner.GetScript(second.Items[0].LocalItemID)
	if !ok || secondStored.ProjectID != "destination-b" {
		t.Fatalf("second destination asset = %#v", secondStored)
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
	unversionedLegacy, err := server.importLibraryBundle(LibraryImportRequest{ProjectID: "destination", Bundle: LibraryBundle{SchemaVersion: libraryBundleSchemaVersion, Items: []LibraryBundleItem{item}}}, "remote")
	if err != nil {
		t.Fatal(err)
	}
	if len(unversionedLegacy.Conflicts) != 1 || !strings.Contains(unversionedLegacy.Conflicts[0].Reason, "content hash") {
		t.Fatalf("unversioned legacy hash was not rejected clearly: %#v", unversionedLegacy)
	}
	item.ContentHashVersion = libraryResultHashVersionLegacyV1
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
	declared := "sha256:" + strings.Repeat("a", 64)
	tests := []struct {
		name       string
		result     *LibraryResultPayload
		actualHash string
		actualSize int64
		want       string
	}{
		{name: "missing declaration", result: &LibraryResultPayload{ArchiveSizeBytes: 10}, actualSize: 10, want: "archive_manifest_hash is required"},
		{name: "unversioned hash", result: &LibraryResultPayload{ArchiveManifestHash: strings.Repeat("a", 64), ArchiveSizeBytes: 10}, actualHash: strings.Repeat("a", 64), actualSize: 10, want: "64 hex characters"},
		{name: "missing actual hash", result: &LibraryResultPayload{ArchiveManifestHash: declared, ArchiveSizeBytes: 10}, actualSize: 10, want: "manifest hash"},
		{name: "missing size", result: &LibraryResultPayload{ArchiveManifestHash: declared}, actualHash: declared, actualSize: 10, want: "archive_size_bytes is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if reason := validateLibraryArchiveMetadata(tt.result, tt.actualHash, tt.actualSize); !strings.Contains(reason, tt.want) {
				t.Fatalf("reason = %q, want %q", reason, tt.want)
			}
		})
	}
	mixedCase := &LibraryResultPayload{ArchiveManifestHash: "Sha256:" + strings.Repeat("A", 64), ArchiveSizeBytes: 10}
	if reason := validateLibraryArchiveMetadata(mixedCase, declared, 10); reason != "" {
		t.Fatalf("mixed-case SHA-256 declaration was not normalized: %q", reason)
	}
}

func TestLibraryRemoteJSONLimitIsIndependentFromCRUDEnvelope(t *testing.T) {
	if libraryRemoteJSONMaxBytes != 128<<20 {
		t.Fatalf("remote JSON limit = %d, want 128 MiB", libraryRemoteJSONMaxBytes)
	}
	if libraryRemoteJSONMaxBytes <= defaultMaxRequestBodyBytes {
		t.Fatalf("remote JSON limit = %d, want larger than CRUD envelope %d", libraryRemoteJSONMaxBytes, defaultMaxRequestBodyBytes)
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
