package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func requireScenarioAuthoringView(t *testing.T, s *Server, id string) (*ScenarioAuthoringProjection, string, string) {
	t.Helper()
	response := authoringAPIRequest(t, s, http.MethodGet, "/api/v1/scenarios/"+id+"?view=authoring", nil, "")
	if response.Code != http.StatusOK {
		t.Fatalf("scenario authoring GET: %d %s", response.Code, response.Body.String())
	}
	var projection ScenarioAuthoringProjection
	if err := json.Unmarshal(response.Body.Bytes(), &projection); err != nil {
		t.Fatal(err)
	}
	return &projection, response.Header().Get("ETag"), response.Body.String()
}

func TestScenarioAuthoringViewServesStrongTagForAuthoredDocument(t *testing.T) {
	server, _, _, scenario := newScenarioPolicyAvailabilityFixture(t)
	projection, tag, body := requireScenarioAuthoringView(t, server, scenario.ID)
	if projection.ProjectionVersion != scenarioAuthoringProjectionVersion || projection.ID != scenario.ID || projection.ProjectID != storage.DefaultProjectID {
		t.Fatalf("projection identity: %#v", projection)
	}
	if !storage.ValidScenarioContentRevision(projection.ContentRevision) {
		t.Fatalf("content revision %q", projection.ContentRevision)
	}
	if want, _ := scenarioAuthoringETag(projection.ContentRevision); tag != want {
		t.Fatalf("ETag %q, want %q", tag, want)
	}
	if len(projection.Scenario.Groups) != 1 || projection.Scenario.Groups[0].ScriptID != "scenario-policy-script" || projection.Scenario.Groups[0].VUsers != 1 {
		t.Fatalf("groups: %#v", projection.Scenario.Groups)
	}
	if projection.Scenario.DataPools == nil || projection.LibraryPlacement.ReleaseIDs == nil || projection.MonitorSelection.Sources == nil {
		t.Fatal("empty collections must serialize as arrays")
	}
	// Clocks, joined display names and run context have independent lifecycles.
	for _, field := range []string{`"created_at"`, `"updated_at"`, `"script_name"`, `"protocol"`, `"schedules"`, `"result_count"`, `"activity"`, `"pool_name"`} {
		if strings.Contains(body, field) {
			t.Fatalf("authoring view leaked %s: %s", field, body)
		}
	}
}

func TestScenarioAuthoringViewTagIgnoresReadsAndRotatesOnAuthoredChange(t *testing.T) {
	server, _, _, scenario := newScenarioPolicyAvailabilityFixture(t)
	first, firstTag, _ := requireScenarioAuthoringView(t, server, scenario.ID)
	if response := authoringAPIRequest(t, server, http.MethodGet, "/api/v1/scenarios/"+scenario.ID, nil, ""); response.Code != http.StatusOK {
		t.Fatalf("legacy GET: %d", response.Code)
	}
	again, againTag, _ := requireScenarioAuthoringView(t, server, scenario.ID)
	if againTag != firstTag || !reflect.DeepEqual(first, again) {
		t.Fatal("reads changed the authoring representation or its tag")
	}
	if _, _, err := server.runner.UpdateScenario(scenario.ID, CreateScenarioRequest{Name: "Renamed", Duration: "1m", Groups: []CreateGroupEntry{{ScriptID: "scenario-policy-script", VUsers: 2}}}); err != nil {
		t.Fatal(err)
	}
	changed, changedTag, _ := requireScenarioAuthoringView(t, server, scenario.ID)
	if changedTag == firstTag || changed.ContentRevision == first.ContentRevision {
		t.Fatal("authored change did not rotate the tag")
	}
	if changed.Scenario.Name != "Renamed" || changed.Scenario.Groups[0].VUsers != 2 {
		t.Fatalf("projection not current: %#v", changed.Scenario)
	}
}

func TestScenarioAuthoringViewMasksRuntimeCredentialsLikeOrdinaryRead(t *testing.T) {
	server, _, _, scenario := newScenarioPolicyAvailabilityFixture(t)
	runtime := &RuntimeSettingsOverride{Env: map[string]string{"API_TOKEN": "s3cr3t-value", "REGION": "us-east"}}
	if _, _, err := server.runner.UpdateScenario(scenario.ID, CreateScenarioRequest{Name: "Secrets", Duration: "1m", RuntimeSettings: runtime, Groups: []CreateGroupEntry{{ScriptID: "scenario-policy-script", VUsers: 1, RuntimeSettings: runtime}}}); err != nil {
		t.Fatal(err)
	}
	projection, _, body := requireScenarioAuthoringView(t, server, scenario.ID)
	if strings.Contains(body, "s3cr3t-value") {
		t.Fatal("authoring view disclosed a runtime credential")
	}
	legacy := authoringAPIRequest(t, server, http.MethodGet, "/api/v1/scenarios/"+scenario.ID, nil, "")
	var stored StoredScenario
	if err := json.Unmarshal(legacy.Body.Bytes(), &stored); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(projection.Scenario.RuntimeSettings, stored.RuntimeSettings) || !reflect.DeepEqual(projection.Scenario.Groups[0].RuntimeSettings, stored.Scripts[0].RuntimeSettings) {
		t.Fatalf("authoring disclosure differs from the ordinary read:\n%#v\n%#v", projection.Scenario.RuntimeSettings, stored.RuntimeSettings)
	}
}

func TestScenarioAuthoringViewRefusesUnsupportedViewsAndMissingScenarios(t *testing.T) {
	server, _, _, scenario := newScenarioPolicyAvailabilityFixture(t)
	for _, query := range []string{"?view=other", "?view=", "?view=authoring&view=authoring"} {
		response := authoringAPIRequest(t, server, http.MethodGet, "/api/v1/scenarios/"+scenario.ID+query, nil, "")
		if response.Code != http.StatusBadRequest || response.Header().Get("ETag") != "" {
			t.Fatalf("%s: %d %s", query, response.Code, response.Body.String())
		}
	}
	response := authoringAPIRequest(t, server, http.MethodGet, "/api/v1/scenarios/missing-scenario?view=authoring", nil, "")
	if response.Code != http.StatusNotFound || response.Header().Get("ETag") != "" {
		t.Fatalf("missing: %d %s", response.Code, response.Body.String())
	}
}

func TestScenarioAuthoringViewHidesForeignProjectBeforeTag(t *testing.T) {
	server, _, _, scenario := newScenarioPolicyAvailabilityFixture(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/scenarios/"+scenario.ID+"?view=authoring", nil)
	req.SetPathValue("id", scenario.ID)
	req = req.WithContext(context.WithValue(req.Context(), authorizedProjectContextKey{}, "another-project"))
	response := httptest.NewRecorder()
	server.handleGetScenario(response, req)
	if response.Code != http.StatusNotFound || response.Header().Get("ETag") != "" {
		t.Fatalf("foreign scoped read: %d %s %q", response.Code, response.Body.String(), response.Header().Get("ETag"))
	}
}

func TestScenarioAuthoringViewUnavailableWithoutDurableRevisions(t *testing.T) {
	server := NewServer(ServerConfig{})
	server.runner.StoreScript(projectSecurityScript("memory-scenario-script", storage.DefaultProjectID))
	scenario, err := server.runner.StoreScenario(CreateScenarioRequest{Name: "Memory", Duration: "1m", Groups: []CreateGroupEntry{{ScriptID: "memory-scenario-script", VUsers: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	response := authoringAPIRequest(t, server, http.MethodGet, "/api/v1/scenarios/"+scenario.ID+"?view=authoring", nil, "")
	if response.Code != http.StatusServiceUnavailable || response.Header().Get("ETag") != "" {
		t.Fatalf("memory-only authoring view: %d %s", response.Code, response.Body.String())
	}
}

func TestParseScenarioAuthoringIfMatch(t *testing.T) {
	valid := `"scenario-authoring-v1:scn-v1-` + strings.Repeat("a", 64) + `"`
	revision, present, err := parseScenarioAuthoringIfMatch(http.Header{"If-Match": {valid}})
	if err != nil || !present || revision != "scn-v1-"+strings.Repeat("a", 64) {
		t.Fatalf("valid tag: %q %v %v", revision, present, err)
	}
	if _, present, err := parseScenarioAuthoringIfMatch(http.Header{}); present || err != nil {
		t.Fatal("absent header")
	}
	for _, header := range []http.Header{
		{"If-Match": {"*"}},
		{"If-Match": {"W/" + valid}},
		{"If-Match": {valid + ", " + valid}},
		{"If-Match": {valid, valid}},
		{"If-Match": {valid}, "if-match": {valid}},
		{"If-Match": {""}},
		{"If-Match": {`"script-authoring-v1:scr-v1-` + strings.Repeat("a", 64) + `"`}},
		{"If-Match": {`"scenario-authoring-v1:scn-v1-` + strings.Repeat("A", 64) + `"`}},
		{"If-Match": {valid + "\r\n"}},
	} {
		if _, present, err := parseScenarioAuthoringIfMatch(header); !present || err == nil {
			t.Fatalf("accepted %#v", header)
		}
	}
}

// The authoring view is a fixed strong-ETag body: object activity
// (creator, modifier, archive state) has its own lifecycle and never enters
// it, even for scenarios created and archived through the API.
func TestScenarioAuthoringViewIgnoresActivityAndArchiveLifecycle(t *testing.T) {
	server, _, _, _ := newScenarioPolicyAvailabilityFixture(t)
	created := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scenarios", scenarioSaveRequest("Lifecycle", 1), "")
	if created.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	var scenario StoredScenario
	if err := json.Unmarshal(created.Body.Bytes(), &scenario); err != nil {
		t.Fatal(err)
	}
	_, firstTag, firstBody := requireScenarioAuthoringView(t, server, scenario.ID)
	// The create response already carries the baseline and its tag.
	if created.Header().Get("ETag") != firstTag || scenario.AuthoringProjection == nil || scenario.AuthoringProjection.ContentRevision != scenario.ContentRevision {
		t.Fatalf("create response baseline: tag %q want %q, projection %#v", created.Header().Get("ETag"), firstTag, scenario.AuthoringProjection)
	}
	if strings.Contains(firstBody, `"activity"`) {
		t.Fatalf("authoring view carried activity: %s", firstBody)
	}
	for _, action := range []string{"archive", "restore"} {
		current, ok := server.runner.GetScenario(scenario.ID)
		if !ok {
			t.Fatal("scenario vanished")
		}
		if response := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scenarios/"+scenario.ID+"/"+action, map[string]any{"updated_at": current.UpdatedAt}, ""); response.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", action, response.Code, response.Body.String())
		}
		_, tag, body := requireScenarioAuthoringView(t, server, scenario.ID)
		if tag != firstTag || body != firstBody {
			t.Fatalf("%s changed the authoring representation:\n%s\n%s", action, firstBody, body)
		}
	}
}

// Every authored part rotates the strong tag: pool bindings, library
// placement and monitor selection, not only the root and groups.
func TestScenarioAuthoringViewRotatesForEveryAuthoredPart(t *testing.T) {
	server, store, _, scenario := newScenarioPolicyAvailabilityFixture(t)
	now := time.Now().UTC()
	if err := store.SaveDataPool(&storage.SharedDataPool{ID: "authoring-pool", Name: "Customers", ProjectID: storage.DefaultProjectID, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveApplication(&storage.Application{ID: "authoring-app", Name: "Application", ProjectID: storage.DefaultProjectID}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRelease(&storage.Release{ID: "authoring-release", Name: "Release", ApplicationID: "authoring-app"}); err != nil {
		t.Fatal(err)
	}
	_, tag, _ := requireScenarioAuthoringView(t, server, scenario.ID)
	rotate := func(part string, mutate func()) {
		t.Helper()
		mutate()
		next, nextTag, _ := requireScenarioAuthoringView(t, server, scenario.ID)
		if nextTag == tag {
			t.Fatalf("%s change did not rotate the tag", part)
		}
		tag = nextTag
		_ = next
	}
	rotate("data pool binding", func() {
		body := map[string]any{"data_pools": []map[string]any{{"pool_id": "authoring-pool", "alias": "customers", "access_mode": "read_only", "selection_strategy": "sequential"}}}
		if response := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scenarios/"+scenario.ID+"/data-pools", body, ""); response.Code != http.StatusOK {
			t.Fatalf("data pools: %d %s", response.Code, response.Body.String())
		}
	})
	rotate("library placement", func() {
		body := map[string]any{"application_id": "authoring-app", "release_ids": []string{"authoring-release"}, "expected_revision": 0}
		if response := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scenarios/"+scenario.ID+"/library-assignment", body, ""); response.Code != http.StatusOK {
			t.Fatalf("library assignment: %d %s", response.Code, response.Body.String())
		}
	})
	rotate("monitor selection", func() {
		if err := store.SaveMonitoringCollector(&storage.MonitoringCollector{ID: "authoring-collector", Name: "collector", ProjectID: storage.DefaultProjectID, TokenHash: hashAPIKey("authoring-token"), Health: "healthy", LastSeenAt: &now, AllowedMetricPacks: []string{"go-runtime"}}); err != nil {
			t.Fatal(err)
		}
		source, err := store.SaveMonitorSource(storage.MonitorSource{ID: "authoring-source", ProjectID: storage.DefaultProjectID, Name: "runtime", Service: "orders", CollectorID: "authoring-collector", Pack: "go-runtime", IntervalSeconds: 15, Enabled: true}, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.SaveMonitorBindings(storage.MonitorBindings{ScenarioID: scenario.ID, ProjectID: storage.DefaultProjectID, Sources: []storage.MonitorBinding{{SourceID: source.ID, Required: true}}}, 0); err != nil {
			t.Fatal(err)
		}
	})
	view, _, _ := requireScenarioAuthoringView(t, server, scenario.ID)
	if len(view.Scenario.DataPools) != 1 || len(view.LibraryPlacement.ReleaseIDs) != 1 || len(view.MonitorSelection.Sources) != 1 {
		t.Fatalf("projection missing authored parts: %#v", view)
	}
}

// Through the real route, a caller with roles only in another project gets
// no representation and no tag for the scenario.
func TestScenarioAuthoringViewRefusesForeignProjectCallerOnRealRoute(t *testing.T) {
	server, store, _, scenario := newScenarioPolicyAvailabilityFixture(t)
	if err := store.SaveProject(&storage.Project{ID: "foreign-project", Name: "Foreign"}); err != nil {
		t.Fatal(err)
	}
	enableTrustedHeaderAuth(t, server)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/scenarios/"+scenario.ID+"?view=authoring", nil)
	request.Header.Set("X-Auth-Request-User", "foreign-viewer")
	request.Header.Set("X-APEXLoad-Project-Roles", "foreign-project="+RoleViewer)
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden && response.Code != http.StatusNotFound || response.Header().Get("ETag") != "" || strings.Contains(response.Body.String(), scenario.Name) {
		t.Fatalf("foreign caller: %d etag=%q %s", response.Code, response.Header().Get("ETag"), response.Body.String())
	}
	member := httptest.NewRequest(http.MethodGet, "/api/v1/scenarios/"+scenario.ID+"?view=authoring", nil)
	member.Header.Set("X-Auth-Request-User", "member-viewer")
	member.Header.Set("X-APEXLoad-Project-Roles", storage.DefaultProjectID+"="+RoleViewer)
	allowed := httptest.NewRecorder()
	server.mux.ServeHTTP(allowed, member)
	if allowed.Code != http.StatusOK || allowed.Header().Get("ETag") == "" {
		t.Fatalf("member viewer: %d %s", allowed.Code, allowed.Body.String())
	}
}

// A scenario whose direct-import publication is still pending stays
// invisible to the authoring view, as on the ordinary read.
func TestScenarioAuthoringViewHidesPendingImportPublication(t *testing.T) {
	server, _, _, scenario := newScenarioPolicyAvailabilityFixture(t)
	server.runner.importPublicationMu.Lock()
	server.runner.importPublicationSnapshot = func() (map[string]struct{}, error) {
		return map[string]struct{}{importPublicationAssetKey("scenario", scenario.ID): {}}, nil
	}
	server.runner.importPublicationMu.Unlock()
	response := authoringAPIRequest(t, server, http.MethodGet, "/api/v1/scenarios/"+scenario.ID+"?view=authoring", nil, "")
	if response.Code != http.StatusNotFound || response.Header().Get("ETag") != "" {
		t.Fatalf("pending publication: %d %s", response.Code, response.Body.String())
	}
}
