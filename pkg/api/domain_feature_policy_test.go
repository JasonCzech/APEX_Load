package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

type countingRequestBody struct {
	reads int
}

func (b *countingRequestBody) Read([]byte) (int, error) {
	b.reads++
	return 0, nil
}

func (*countingRequestBody) Close() error { return nil }

func TestDomainFeaturePolicyHandlersPreserveUnknownIDs(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	seed, ok, err := store.UpdateDomainFeaturePolicyIfRevision(storage.DefaultDomainID, []string{"future.feature"}, "0")
	if err != nil || !ok {
		t.Fatalf("seed = %t, %v", ok, err)
	}
	server := NewServer(ServerConfig{Store: store})

	get := httptest.NewRequest(http.MethodGet, "/api/v1/domains/default/features", nil)
	get.SetPathValue("id", storage.DefaultDomainID)
	getRecorder := httptest.NewRecorder()
	server.handleGetDomainFeatures(getRecorder, get)
	if getRecorder.Code != http.StatusOK {
		t.Fatalf("GET = %d %s", getRecorder.Code, getRecorder.Body.String())
	}
	var initial domainFeaturePolicyResponse
	if err := json.Unmarshal(getRecorder.Body.Bytes(), &initial); err != nil {
		t.Fatal(err)
	}
	if len(initial.DisabledFeatureIDs) != 0 || initial.Revision != seed.Revision {
		t.Fatalf("initial response = %#v", initial)
	}

	put := httptest.NewRequest(http.MethodPut, "/api/v1/domains/default/features", bytes.NewBufferString(`{"disabled_feature_ids":["script_import.postman"]}`))
	put.SetPathValue("id", storage.DefaultDomainID)
	put.Header.Set("If-Match", `"`+seed.Revision+`"`)
	putRecorder := httptest.NewRecorder()
	server.handleUpdateDomainFeatures(putRecorder, put)
	if putRecorder.Code != http.StatusOK {
		t.Fatalf("PUT = %d %s", putRecorder.Code, putRecorder.Body.String())
	}
	persisted, err := store.GetDomainFeaturePolicy(storage.DefaultDomainID)
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted.DisabledFeatureIDs) != 2 || persisted.DisabledFeatureIDs[0] != "future.feature" || persisted.DisabledFeatureIDs[1] != "script_import.postman" {
		t.Fatalf("persisted disabled IDs = %#v", persisted.DisabledFeatureIDs)
	}
}

func TestDomainFeatureHierarchyRemembersChildState(t *testing.T) {
	policy := domainFeatureResponse(&storage.DomainFeaturePolicy{DomainID: "d", Revision: "r", DisabledFeatureIDs: []string{"script_builder.imports", "script_import.postman"}})
	states := map[string]domainFeatureState{}
	for _, state := range policy.Features {
		states[state.ID] = state
	}
	if states["script_import.openapi"].ConfiguredEnabled != true || states["script_import.openapi"].EffectiveEnabled != false {
		t.Fatalf("openapi state = %#v", states["script_import.openapi"])
	}
	if states["script_import.postman"].ConfiguredEnabled != false || states["script_import.postman"].EffectiveEnabled != false {
		t.Fatalf("postman state = %#v", states["script_import.postman"])
	}
	policy = domainFeatureResponse(&storage.DomainFeaturePolicy{DomainID: "d", Revision: "r2", DisabledFeatureIDs: []string{"script_import.postman"}})
	states = map[string]domainFeatureState{}
	for _, state := range policy.Features {
		states[state.ID] = state
	}
	if !states["script_import.openapi"].EffectiveEnabled || states["script_import.postman"].EffectiveEnabled {
		t.Fatalf("remembered child states = %#v / %#v", states["script_import.openapi"], states["script_import.postman"])
	}
}

func TestDisabledImportFeatureBlocksGlobalAdminPathAfterRBAC(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, ok, err := store.UpdateDomainFeaturePolicyIfRevision(storage.DefaultDomainID, []string{"script_import.postman"}, "0"); err != nil || !ok {
		t.Fatalf("disable postman = %t, %v", ok, err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/postman?project_id=unassigned", bytes.NewBufferString(`{"project_id":"unassigned"}`))
	requirement, ok := accessRequirementForRequest(req)
	if !ok || requirement.DomainFeatureID != "script_import.postman" {
		t.Fatalf("requirement = %#v, %t", requirement, ok)
	}
	recorder := httptest.NewRecorder()
	if domainFeatureAllowsRequest(recorder, req, store, requirement) {
		t.Fatal("disabled postman importer was allowed")
	}
	if recorder.Code != http.StatusForbidden || !bytes.Contains(recorder.Body.Bytes(), []byte(`"code":"domain_feature_disabled"`)) {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestDomainFeatureResolutionDoesNotReadImporterNeutralBody(t *testing.T) {
	body := &countingRequestBody{}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/commit-batch", nil)
	req.Body = body
	if featureID := domainFeatureIDForRequest(req); featureID != "" {
		t.Fatalf("feature ID = %q, want handler-derived feature", featureID)
	}
	if body.reads != 0 {
		t.Fatalf("access resolution read the request body %d times", body.reads)
	}
}

func TestRequestExpectedRevisionAcceptsWeakAndWildcardETags(t *testing.T) {
	weak := httptest.NewRequest(http.MethodPut, "/api/v1/domains/default/features", nil)
	weak.Header.Set("If-Match", `W/"revision-1"`)
	if got := requestExpectedRevision(weak, "ignored"); got != "revision-1" {
		t.Fatalf("weak If-Match = %q", got)
	}
	wildcard := httptest.NewRequest(http.MethodPut, "/api/v1/domains/default/features", nil)
	wildcard.Header.Set("If-Match", "*")
	if got := requestExpectedRevision(wildcard, "ignored"); got != "*" {
		t.Fatalf("wildcard If-Match = %q", got)
	}
}

func TestImportProfilesResolveToGovernedFeatureOrParent(t *testing.T) {
	tests := map[string]string{
		"postman":               "script_import.postman",
		"raw_http":              "script_import.sample",
		"rest_json":             "script_import.sample",
		"curl":                  "script_import.sample",
		"truclient":             "script_import.enterprise_archive",
		"bruno_native":          "script_import.bruno",
		"browser_recording_har": "script_builder.browser_recording",
		"github":                "script_builder.imports",
	}
	for profile, want := range tests {
		if got := featureIDForImportProfile(profile); got != want {
			t.Errorf("featureIDForImportProfile(%q) = %q, want %q", profile, got, want)
		}
	}
	if got := featureIDForImportProfile("legacy-draft-without-profile"); got != "" {
		t.Fatalf("unknown profile feature = %q, want ungoverned", got)
	}
}

func TestScenarioValidationUsesExplicitProjectForGroupLessDraft(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	if err := store.SaveDomain(&storage.Domain{ID: "domain-b", Name: "Domain B", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveApplicationRepository(&storage.ApplicationRepository{ID: "repository-b", DomainID: "domain-b", Name: "Repository B", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRepositoryCategory(&storage.RepositoryCategory{ID: "category-b", DomainID: "domain-b", RepositoryID: "repository-b", Name: "Category B", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveProject(&storage.Project{ID: "project-b", Name: "Project B", DomainID: "domain-b", CategoryID: "category-b", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.UpdateDomainFeaturePolicyIfRevision(storage.DefaultDomainID, []string{"workload_shape.stress"}, "0"); err != nil || !ok {
		t.Fatalf("disable default-domain Stress = %t, %v", ok, err)
	}
	server := NewServer(ServerConfig{Store: store})
	body := bytes.NewBufferString(`{"name":"Draft","project_id":"project-b","workload_shape_id":"stress","duration":"1m","groups":[]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scenarios/validate-load-model", body)
	recorder := httptest.NewRecorder()
	server.handleValidateScenarioLoadModel(recorder, req)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("group-less validation = %d %s, want normal load-model validation error rather than default-domain policy", recorder.Code, recorder.Body.String())
	}
}

func TestScriptBuilderRoutesRetainStaticDomainFeatures(t *testing.T) {
	tests := []struct {
		method string
		path   string
		want   string
	}{
		{http.MethodPost, "/api/v1/scripts/script-1/clone-draft", "script_builder.existing_scripts"},
		{http.MethodGet, "/api/v1/scripts/authoring/templates?project_id=unassigned", "script_builder.starter_templates"},
		{http.MethodGet, "/api/v1/scripts/authoring/components?project_id=unassigned", "script_builder.reusable_blocks"},
		{http.MethodPost, "/api/v1/scripts/import/import-1/assets/upload", "script_builder.imports"},
		{http.MethodPost, "/api/v1/scripts/import/postman/assets/upload", "script_builder.imports"},
		{http.MethodPost, "/api/v1/scripts/import/import-1/java-classpath/upload", "script_builder.imports"},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			requirement, ok := accessRequirementForRequest(req)
			if !ok || requirement.DomainFeatureID != tt.want {
				t.Fatalf("requirement = %#v, %t; want feature %q", requirement, ok, tt.want)
			}
		})
	}
}

func TestBrowserRecorderAttestationUsesRecordingWorkflowFeature(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/browser-recorder/attest", nil)
	requirement, ok := accessRequirementForRequest(req)
	if !ok || requirement.DomainFeatureID != "script_builder.browser_recording" {
		t.Fatalf("requirement = %#v, %t", requirement, ok)
	}
}

func TestDisabledImportFeatureBlocksAuthBypassMode(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, ok, err := store.UpdateDomainFeaturePolicyIfRevision(storage.DefaultDomainID, []string{"script_import.postman"}, "0"); err != nil || !ok {
		t.Fatalf("disable postman = %t, %v", ok, err)
	}
	called := false
	handler := authMiddleware(
		func() AuthSettings { return AuthSettings{Mode: authModeDev} },
		store,
		nil,
	)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/postman?project_id=unassigned", bytes.NewBufferString(`{"project_id":"unassigned"}`))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if called {
		t.Fatal("disabled Postman handler was called in auth bypass mode")
	}
	if recorder.Code != http.StatusForbidden || !bytes.Contains(recorder.Body.Bytes(), []byte(`"code":"domain_feature_disabled"`)) {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestLoadModelCatalogFiltersDisabledShapeByProject(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, ok, err := store.UpdateDomainFeaturePolicyIfRevision(storage.DefaultDomainID, []string{"workload_shape.stress"}, "0"); err != nil || !ok {
		t.Fatalf("disable stress = %t, %v", ok, err)
	}
	server := NewServer(ServerConfig{Store: store})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/scenarios/load-models/support-matrix?project_id=unassigned", nil)
	recorder := httptest.NewRecorder()
	server.handleLoadModelSupportMatrix(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		SupportMatrix []LoadModelSupportEntry `json:"support_matrix"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	for _, shape := range response.SupportMatrix {
		if shape.ID == "stress" {
			t.Fatal("disabled Stress shape remained in project catalog")
		}
	}
}

func TestLibraryScenarioPreservesAndEnforcesWorkloadShapeIdentity(t *testing.T) {
	shapeID := "stress"
	stored := &StoredScenario{Name: "Stress", WorkloadShapeID: shapeID}
	req := createScenarioRequestFromStored(stored)
	if req.WorkloadShapeID == nil || *req.WorkloadShapeID != shapeID {
		t.Fatalf("exported workload shape = %#v", req.WorkloadShapeID)
	}

	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, ok, err := store.UpdateDomainFeaturePolicyIfRevision(storage.DefaultDomainID, []string{"workload_shape.stress"}, "0"); err != nil || !ok {
		t.Fatalf("disable Stress = %t, %v", ok, err)
	}
	server := NewServer(ServerConfig{Store: store})
	payload := LibraryScenarioPayload{ID: "source-scenario", Request: req}
	item := LibraryBundleItem{
		Kind: "scenario", SourceInstanceID: "source", SourceItemID: "source-scenario",
		ProjectID: storage.DefaultProjectID, Scenario: &payload, ContentHash: hashCanonical(req),
	}
	_, _, conflict := server.importLibraryScenario(item, storage.DefaultProjectID, "", nil)
	if conflict == nil || conflict.Code != "domain_feature_disabled" || conflict.FeatureID != "workload_shape.stress" {
		t.Fatalf("library import conflict = %#v", conflict)
	}
}

func TestLibraryImportReturnsConflictPayloadForDisabledShape(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, ok, err := store.UpdateDomainFeaturePolicyIfRevision(storage.DefaultDomainID, []string{"workload_shape.stress"}, "0"); err != nil || !ok {
		t.Fatalf("disable Stress = %t, %v", ok, err)
	}
	server := NewServer(ServerConfig{Store: store})
	shapeID := "stress"
	scenarioRequest := CreateScenarioRequest{Name: "Blocked Stress", WorkloadShapeID: &shapeID, Duration: "1m"}
	item := LibraryBundleItem{
		Kind: "scenario", SourceInstanceID: "source", SourceItemID: "scenario-1", ProjectID: "source-project",
		Name: "Blocked Stress", ContentHash: hashCanonical(scenarioRequest),
		Scenario: &LibraryScenarioPayload{ID: "scenario-1", Request: scenarioRequest},
	}
	req := LibraryImportRequest{
		ProjectID: storage.DefaultProjectID,
		Bundle: LibraryBundle{
			SchemaVersion: libraryBundleSchemaVersion, BundleID: "blocked-shape-bundle", CreatedAt: time.Now().UTC(),
			Source: LibraryBundleSource{InstanceID: "source", ProjectID: "source-project"}, Items: []LibraryBundleItem{item},
		},
	}
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	server.handleLibraryImport(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/library/import", bytes.NewReader(body)))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("library import = %d %s, want conflict payload", recorder.Code, recorder.Body.String())
	}
	var response LibraryImportResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Conflicts) != 1 || response.Conflicts[0].Code != "domain_feature_disabled" {
		t.Fatalf("library conflicts = %#v", response.Conflicts)
	}
}

// The workload-shape catalog filters fail closed for a feature ID this
// controller does not define, so every advertised shape and template must have
// a catalog entry or it would silently disappear from a project-scoped catalog.
func TestWorkloadShapeCatalogMatchesFeatureCatalog(t *testing.T) {
	known := knownDomainFeatureIDs()
	shapes := make(map[string]bool, len(loadModelSupportMatrix()))
	for _, entry := range loadModelSupportMatrix() {
		shapes[entry.ID] = true
		if _, ok := known[workloadShapeFeatureID(entry.ID)]; !ok {
			t.Errorf("support-matrix shape %q has no %q feature", entry.ID, workloadShapeFeatureID(entry.ID))
		}
	}
	for _, template := range loadModelTemplates() {
		if _, ok := known[workloadShapeFeatureID(template.ShapeID)]; !ok {
			t.Errorf("template %q references shape %q with no catalog feature", template.ID, template.ShapeID)
		}
	}
	for _, feature := range domainFeatureCatalog {
		if feature.Category != "workload_shape" {
			continue
		}
		if shapeID := strings.TrimPrefix(feature.ID, "workload_shape."); !shapes[shapeID] {
			t.Errorf("catalog feature %q has no support-matrix shape", feature.ID)
		}
	}
}
