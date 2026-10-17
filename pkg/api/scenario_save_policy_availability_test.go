package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func newScenarioPolicyAvailabilityFixture(t *testing.T) (*Server, *storage.SQLiteStore, *runtimeProjectionSecretStore, *StoredScenario) {
	t.Helper()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "scenario.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	secrets := &runtimeProjectionSecretStore{values: map[string]string{}, attemptStore: store}
	server := NewServer(ServerConfig{Store: store, ProjectSecretStore: secrets})
	server.runner.StoreScript(projectSecurityScript("scenario-policy-script", storage.DefaultProjectID))
	scenario, err := server.runner.StoreScenario(CreateScenarioRequest{Name: "Original", Duration: "1m", Groups: []CreateGroupEntry{{ScriptID: "scenario-policy-script", VUsers: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	return server, store, secrets, scenario
}

func scenarioSaveRequest(name string, vusers int) CreateScenarioRequest {
	return CreateScenarioRequest{Name: name, Duration: "1m", Groups: []CreateGroupEntry{{ScriptID: "scenario-policy-script", VUsers: vusers}}}
}

func setScenarioStrictPolicy(t *testing.T, store *storage.SQLiteStore, strict bool) {
	t.Helper()
	project, err := store.GetProject(storage.DefaultProjectID)
	if err != nil {
		t.Fatal(err)
	}
	project.Settings.Authoring = &storage.ProjectAuthoringPolicy{RequireScenarioSaveIfMatch: strict}
	if err := store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
}

func requireNoScenarioSaveSideEffects(t *testing.T, store *storage.SQLiteStore, secrets *runtimeProjectionSecretStore, before *storage.ScenarioAuthoringSnapshot) {
	t.Helper()
	after, err := store.GetScenarioAuthoringSnapshot(before.Scenario.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("refused scenario save mutated authored state: %v", err)
	}
	secrets.mu.Lock()
	calls, count := secrets.createCalls, len(secrets.values)
	secrets.mu.Unlock()
	attempts, err := store.ListRuntimeSecretProvisioningAttempts()
	if err != nil || calls != 0 || count != 0 || len(attempts) != 0 {
		t.Fatalf("refused save provisioned secrets/journals: calls=%d values=%d attempts=%d err=%v", calls, count, len(attempts), err)
	}
}

func TestScenarioStrictPolicyProjectRoutesAcceptEnablement(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "policy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})
	strict := &storage.ProjectAuthoringPolicy{RequireScenarioSaveIfMatch: true}
	create := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/projects", projectRequest{Name: "Strict", Settings: storage.ProjectSettings{Authoring: strict}}, "")
	if create.Code != http.StatusCreated {
		t.Fatalf("strict project create: %d %s", create.Code, create.Body.String())
	}
	var project storage.Project
	if err := json.Unmarshal(create.Body.Bytes(), &project); err != nil {
		t.Fatal(err)
	}
	update := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/projects/"+project.ID, projectRequest{Name: "Strict both", Settings: storage.ProjectSettings{Authoring: &storage.ProjectAuthoringPolicy{RequireScenarioSaveIfMatch: true, RequireScriptSaveIfMatch: true}}}, "")
	if update.Code != http.StatusOK {
		t.Fatalf("strict project update: %d %s", update.Code, update.Body.String())
	}
	stored, err := store.GetProject(project.ID)
	if err != nil || stored.Settings.Authoring == nil || !stored.Settings.Authoring.RequireScenarioSaveIfMatch || !stored.Settings.Authoring.RequireScriptSaveIfMatch {
		t.Fatalf("strict policy not retained: %#v %v", stored, err)
	}
}

func TestScenarioSavePolicyProjectRoutesPreserveSupportedSettings(t *testing.T) {
	for _, tc := range []struct {
		name      string
		authoring *storage.ProjectAuthoringPolicy
	}{
		{"omitted", nil},
		{"explicit false", &storage.ProjectAuthoringPolicy{}},
		{"script strict", &storage.ProjectAuthoringPolicy{RequireScriptSaveIfMatch: true}},
		{"scenario strict", &storage.ProjectAuthoringPolicy{RequireScenarioSaveIfMatch: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "supported.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			server := NewServer(ServerConfig{Store: store})
			response := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/projects", projectRequest{Name: "Supported", Settings: storage.ProjectSettings{Authoring: tc.authoring}}, "")
			if response.Code != http.StatusCreated {
				t.Fatalf("project create: %d %s", response.Code, response.Body.String())
			}
			var project storage.Project
			if err := json.Unmarshal(response.Body.Bytes(), &project); err != nil {
				t.Fatal(err)
			}
			response = authoringAPIRequest(t, server, http.MethodPut, "/api/v1/projects/"+project.ID, projectRequest{Name: "Supported update", Settings: storage.ProjectSettings{Authoring: tc.authoring}}, "")
			if response.Code != http.StatusOK {
				t.Fatalf("project update: %d %s", response.Code, response.Body.String())
			}
			stored, err := store.GetProject(project.ID)
			if err != nil || stored.Name != "Supported update" || !reflect.DeepEqual(stored.Settings.Authoring, tc.authoring) {
				t.Fatalf("supported policy not retained: %#v %v", stored, err)
			}
		})
	}
}

func TestScenarioConditionedSaveCommitsAndReturnsNewBaseline(t *testing.T) {
	server, store, _, scenario := newScenarioPolicyAvailabilityFixture(t)
	_, tag, _ := requireScenarioAuthoringView(t, server, scenario.ID)
	response := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scenarios/"+scenario.ID, scenarioSaveRequest("Conditioned", 3), tag)
	if response.Code != http.StatusOK || response.Header().Get("X-APEX-Scenario-Save-Mode") != scenarioSaveModeConditioned {
		t.Fatalf("conditioned save: %d %s", response.Code, response.Body.String())
	}
	var saved StoredScenario
	if err := json.Unmarshal(response.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	newTag := response.Header().Get("ETag")
	if saved.AuthoringProjection == nil || saved.ContentRevision != saved.AuthoringProjection.ContentRevision || newTag == tag {
		t.Fatalf("save did not return a new baseline: %q %#v", newTag, saved.AuthoringProjection)
	}
	if want, _ := scenarioAuthoringETag(saved.ContentRevision); newTag != want {
		t.Fatalf("returned ETag %q does not match the returned projection %q", newTag, want)
	}
	if saved.AuthoringProjection.Scenario.Name != "Conditioned" || saved.AuthoringProjection.Scenario.Groups[0].VUsers != 3 {
		t.Fatalf("returned projection is not the committed state: %#v", saved.AuthoringProjection.Scenario)
	}
	durable, _, err := store.GetScenario(scenario.ID)
	if err != nil {
		t.Fatal(err)
	}
	cached, visible := server.runner.GetScenario(scenario.ID)
	if !visible || cached.ProjectID != storage.DefaultProjectID || !cached.UpdatedAt.Equal(durable.UpdatedAt) {
		t.Fatalf("cached scenario diverged from the committed row: visible=%v %#v", visible, cached)
	}
	current, currentTag, _ := requireScenarioAuthoringView(t, server, scenario.ID)
	if currentTag != newTag || !reflect.DeepEqual(current, saved.AuthoringProjection) {
		t.Fatal("returned baseline differs from a fresh authoring read")
	}
	before, err := store.GetScenarioAuthoringSnapshot(scenario.ID)
	if err != nil {
		t.Fatal(err)
	}
	stale := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scenarios/"+scenario.ID, scenarioSaveRequest("Stale overwrite", 9), tag)
	if stale.Code != http.StatusPreconditionFailed || stale.Header().Get("ETag") != "" {
		t.Fatalf("stale validator: %d %s", stale.Code, stale.Body.String())
	}
	if after, err := store.GetScenarioAuthoringSnapshot(scenario.ID); err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("stale save mutated state: %v", err)
	}
	chained := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scenarios/"+scenario.ID, scenarioSaveRequest("Chained", 4), newTag)
	if chained.Code != http.StatusOK {
		t.Fatalf("save with the returned ETag: %d %s", chained.Code, chained.Body.String())
	}
}

func TestScenarioConcurrentConditionedSavesHaveExactlyOneWinner(t *testing.T) {
	server, store, _, scenario := newScenarioPolicyAvailabilityFixture(t)
	_, tag, _ := requireScenarioAuthoringView(t, server, scenario.ID)
	codes := make([]int, 2)
	var wg sync.WaitGroup
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i] = authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scenarios/"+scenario.ID, scenarioSaveRequest([]string{"Writer A", "Writer B"}[i], i+2), tag).Code
		}(i)
	}
	wg.Wait()
	sort.Ints(codes)
	if !reflect.DeepEqual(codes, []int{http.StatusOK, http.StatusPreconditionFailed}) {
		t.Fatalf("concurrent conditioned saves: %v", codes)
	}
	stored, _, err := store.GetScenario(scenario.ID)
	if err != nil || (stored.Name != "Writer A" && stored.Name != "Writer B") {
		t.Fatalf("winner not durable: %#v %v", stored, err)
	}
}

func TestScenarioStrictPolicyRequiresIfMatchWithoutSideEffects(t *testing.T) {
	server, store, secrets, scenario := newScenarioPolicyAvailabilityFixture(t)
	setScenarioStrictPolicy(t, store, true)
	before, err := store.GetScenarioAuthoringSnapshot(scenario.ID)
	if err != nil {
		t.Fatal(err)
	}
	request := scenarioSaveRequest("Must not save", 1)
	request.RuntimeSettings = &RuntimeSettingsOverride{Env: map[string]string{"API_TOKEN": "must-not-provision"}}
	response := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scenarios/"+scenario.ID, request, "")
	if response.Code != http.StatusPreconditionRequired {
		t.Fatalf("strict unconditioned save: %d %s", response.Code, response.Body.String())
	}
	requireNoScenarioSaveSideEffects(t, store, secrets, before)
	_, tag, _ := requireScenarioAuthoringView(t, server, scenario.ID)
	if response := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scenarios/"+scenario.ID, scenarioSaveRequest("Conditioned under policy", 1), tag); response.Code != http.StatusOK {
		t.Fatalf("strict conditioned save: %d %s", response.Code, response.Body.String())
	}
}

// strictPolicyAfterAdmissionStore reports the project as non-strict to the
// handler's early check while the durable row is already strict, standing in
// for a policy enabled between admission and the save transaction.
type strictPolicyAfterAdmissionStore struct {
	SQLiteStore *storage.SQLiteStore
}

func (s *strictPolicyAfterAdmissionStore) GetProject(id string) (*storage.Project, error) {
	project, err := s.SQLiteStore.GetProject(id)
	if err == nil && project != nil {
		project.Settings.Authoring = nil
	}
	return project, err
}

func TestScenarioStrictPolicyEnabledDuringSaveIsFencedInTransaction(t *testing.T) {
	server, store, _, scenario := newScenarioPolicyAvailabilityFixture(t)
	setScenarioStrictPolicy(t, store, true)
	server.store = sqliteOverlay(&strictPolicyAfterAdmissionStore{SQLiteStore: store})
	before, err := store.GetScenarioAuthoringSnapshot(scenario.ID)
	if err != nil {
		t.Fatal(err)
	}
	response := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scenarios/"+scenario.ID, scenarioSaveRequest("Raced policy", 2), "")
	if response.Code != http.StatusPreconditionRequired {
		t.Fatalf("policy enabled during save: %d %s", response.Code, response.Body.String())
	}
	if after, err := store.GetScenarioAuthoringSnapshot(scenario.ID); err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("fenced save mutated state: %v", err)
	}
	events, err := store.ListAuditEvents(storage.AuditEventFilter{Action: "scenario.update", ResourceID: scenario.ID})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event.Details["outcome"] == "not_committed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("refused compatibility save was not audited as not committed: %#v", events)
	}
}

func TestScenarioLegacySaveIsAuditedAndReturnsBaseline(t *testing.T) {
	server, store, _, scenario := newScenarioPolicyAvailabilityFixture(t)
	for _, authoring := range []*storage.ProjectAuthoringPolicy{nil, {}, {RequireScriptSaveIfMatch: true}} {
		project, err := store.GetProject(storage.DefaultProjectID)
		if err != nil {
			t.Fatal(err)
		}
		project.Settings.Authoring = authoring
		if err := store.SaveProject(project); err != nil {
			t.Fatal(err)
		}
		response := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scenarios/"+scenario.ID, scenarioSaveRequest("Saved", 1), "")
		if response.Code != http.StatusOK || response.Header().Get("X-APEX-Scenario-Save-Mode") != scenarioSaveModeLegacy || response.Header().Get("ETag") == "" {
			t.Fatalf("compatible scenario PUT: %d %v %s", response.Code, response.Header(), response.Body.String())
		}
		stored, _, err := store.GetScenario(scenario.ID)
		if err != nil || stored.Name != "Saved" {
			t.Fatalf("compatible save not durable: %#v %v", stored, err)
		}
	}
	events, err := store.ListAuditEvents(storage.AuditEventFilter{Action: "scenario.update", ResourceID: scenario.ID})
	if err != nil {
		t.Fatal(err)
	}
	committed := 0
	for _, event := range events {
		if event.Details["outcome"] == "committed" && event.Details["save_mode"] == scenarioSaveModeLegacy {
			committed++
		}
	}
	if committed != 3 {
		t.Fatalf("legacy saves audited as committed: %d of 3 (%d events)", committed, len(events))
	}
}

type scenarioPolicyAvailabilityFaultStore struct {
	Store           storage.Store
	failProject     bool
	failScenario    bool
	wrongScenarioID bool
}

func (s *scenarioPolicyAvailabilityFaultStore) GetProject(id string) (*storage.Project, error) {
	if s.failProject {
		return nil, errors.New("private persistence detail")
	}
	return s.Store.GetProject(id)
}
func (s *scenarioPolicyAvailabilityFaultStore) GetScenario(id string) (*storage.Scenario, []storage.ScenarioScript, error) {
	if s.failScenario {
		return nil, nil, errors.New("private persistence detail")
	}
	scenario, groups, err := s.Store.GetScenario(id)
	if err == nil && scenario != nil && s.wrongScenarioID {
		scenario.ID = "unrelated-private-scenario"
	}
	return scenario, groups, err
}

func TestScenarioSaveWithoutDurableRevisionsRefusesConditionsExplicitly(t *testing.T) {
	for _, tc := range []struct {
		name   string
		tag    string
		strict bool
	}{
		{name: "presented tag", tag: `"scenario-authoring-v1:scn-v1-` + strings.Repeat("a", 64) + `"`},
		{name: "strict policy", strict: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, store, secrets, scenario := newScenarioPolicyAvailabilityFixture(t)
			setScenarioStrictPolicy(t, store, tc.strict)
			before, err := store.GetScenarioAuthoringSnapshot(scenario.ID)
			if err != nil {
				t.Fatal(err)
			}
			server.store = storeOverlay(&scenarioPolicyAvailabilityFaultStore{Store: store})
			request := scenarioSaveRequest("Must not save", 1)
			request.RuntimeSettings = &RuntimeSettingsOverride{Env: map[string]string{"API_TOKEN": "must-not-provision"}}
			response := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scenarios/"+scenario.ID, request, tc.tag)
			wantMessage, wantCode := "scenario If-Match saves are unavailable without durable scenario revisions", "scenario_save_if_match_unavailable"
			if tc.strict {
				wantMessage = "scenario If-Match saves are unavailable without durable scenario revisions; the configured strict policy prevents this save"
				wantCode = "scenario_save_strict_policy_unavailable"
			}
			requireScenarioSaveCapabilityResponse(t, response, wantMessage, wantCode)
			requireNoScenarioSaveSideEffects(t, store, secrets, before)
		})
	}
}

func TestScenarioSaveOwnershipAndReadFailuresRefuse(t *testing.T) {
	for _, tc := range []struct {
		name            string
		failProject     bool
		failScenario    bool
		wrongScenarioID bool
		scoped          bool
		want            int
	}{
		{name: "foreign scope", scoped: true, want: http.StatusNotFound},
		{name: "project unavailable", failProject: true, want: http.StatusServiceUnavailable},
		{name: "legacy durable scenario unavailable", failScenario: true, want: http.StatusServiceUnavailable},
		{name: "legacy wrong returned identity", wrongScenarioID: true, want: http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, store, _, scenario := newScenarioPolicyAvailabilityFixture(t)
			before, err := store.GetScenarioAuthoringSnapshot(scenario.ID)
			if err != nil {
				t.Fatal(err)
			}
			server.store = storeOverlay(&scenarioPolicyAvailabilityFaultStore{Store: store, failProject: tc.failProject, failScenario: tc.failScenario, wrongScenarioID: tc.wrongScenarioID})
			body, err := json.Marshal(scenarioSaveRequest("Must not save", 1))
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPut, "/api/v1/scenarios/"+scenario.ID, bytes.NewReader(body))
			if tc.scoped {
				bindAuthorizedProjectToRequest(req, "unrelated-project")
			}
			response := httptest.NewRecorder()
			server.mux.ServeHTTP(response, req)
			if response.Code != tc.want || strings.Contains(response.Body.String(), "private persistence detail") || strings.Contains(response.Body.String(), storage.DefaultProjectID) {
				t.Fatalf("guard refusal: %d %s", response.Code, response.Body.String())
			}
			after, err := store.GetScenarioAuthoringSnapshot(scenario.ID)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("refused save changed state: %v", err)
			}
		})
	}
}

func TestScenarioSaveDurableDeletionAndCachedOwnerRefuse(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		t.Run(map[bool]string{false: "cached owner changed", true: "durably deleted"}[deleted], func(t *testing.T) {
			server, store, _, scenario := newScenarioPolicyAvailabilityFixture(t)
			before, err := store.GetScenarioAuthoringSnapshot(scenario.ID)
			if err != nil {
				t.Fatal(err)
			}
			if deleted {
				if err := store.DeleteScenario(scenario.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				server.runner.mu.Lock()
				server.runner.scenarios[scenario.ID].ProjectID = "unrelated-project"
				server.runner.mu.Unlock()
			}
			response := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scenarios/"+scenario.ID, scenarioSaveRequest("Must not save", 1), "")
			if response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), "unrelated-project") {
				t.Fatalf("ownership guard: %d %s", response.Code, response.Body.String())
			}
			if deleted {
				if _, err := store.GetScenarioAuthoringSnapshot(scenario.ID); !errors.Is(err, storage.ErrScenarioAuthoringNotFound) {
					t.Fatalf("deleted scenario recreated: %v", err)
				}
			} else {
				after, err := store.GetScenarioAuthoringSnapshot(scenario.ID)
				if err != nil || !reflect.DeepEqual(before, after) {
					t.Fatalf("cached owner refusal changed durable state: %v", err)
				}
			}
		})
	}
}

func TestScenarioSaveMalformedConditionsNeverFallThrough(t *testing.T) {
	tag := `"scenario-authoring-v1:scn-v1-` + strings.Repeat("a", 64) + `"`
	for _, memoryOnly := range []bool{false, true} {
		for _, tc := range []struct {
			name   string
			header http.Header
		}{
			{"empty", http.Header{"If-Match": []string{""}}},
			{"multiple", http.Header{"If-Match": []string{tag, tag}}},
			{"weak", http.Header{"If-Match": []string{"W/" + tag}}},
			{"wildcard", http.Header{"If-Match": []string{"*"}}},
			{"noncanonical presence", http.Header{"if-match": []string{}}},
		} {
			t.Run(map[bool]string{false: "durable", true: "memory"}[memoryOnly]+"/"+tc.name, func(t *testing.T) {
				var server *Server
				var store *storage.SQLiteStore
				var secrets *runtimeProjectionSecretStore
				var scenario *StoredScenario
				if memoryOnly {
					secrets = &runtimeProjectionSecretStore{values: map[string]string{}}
					server = NewServer(ServerConfig{ProjectSecretStore: secrets})
					server.runner.StoreScript(projectSecurityScript("scenario-policy-script", storage.DefaultProjectID))
					var err error
					scenario, err = server.runner.StoreScenario(scenarioSaveRequest("Original", 1))
					if err != nil {
						t.Fatal(err)
					}
				} else {
					server, store, secrets, scenario = newScenarioPolicyAvailabilityFixture(t)
				}
				before, ok := server.runner.GetScenario(scenario.ID)
				if !ok {
					t.Fatal("missing fixture scenario")
				}
				var durableBefore *storage.ScenarioAuthoringSnapshot
				if store != nil {
					var err error
					durableBefore, err = store.GetScenarioAuthoringSnapshot(scenario.ID)
					if err != nil {
						t.Fatal(err)
					}
				}
				request := scenarioSaveRequest("Must not save", 1)
				request.RuntimeSettings = &RuntimeSettingsOverride{Env: map[string]string{"API_TOKEN": "must-not-provision"}}
				body, err := json.Marshal(request)
				if err != nil {
					t.Fatal(err)
				}
				req := httptest.NewRequest(http.MethodPut, "/api/v1/scenarios/"+scenario.ID, bytes.NewReader(body))
				req.Header = tc.header.Clone()
				response := httptest.NewRecorder()
				server.mux.ServeHTTP(response, req)
				if memoryOnly {
					requireScenarioSaveCapabilityResponse(t, response, "scenario If-Match saves are unavailable without durable scenario revisions", "scenario_save_if_match_unavailable")
				} else if response.Code != http.StatusBadRequest {
					t.Fatalf("malformed durable condition: %d %s", response.Code, response.Body.String())
				}
				after, ok := server.runner.GetScenario(scenario.ID)
				if !ok || !reflect.DeepEqual(before, after) {
					t.Fatal("refused condition mutated cached state")
				}
				if store != nil {
					requireNoScenarioSaveSideEffects(t, store, secrets, durableBefore)
				} else {
					secrets.mu.Lock()
					calls, count := secrets.createCalls, len(secrets.values)
					secrets.mu.Unlock()
					if calls != 0 || count != 0 {
						t.Fatalf("refused condition provisioned credentials: calls=%d count=%d", calls, count)
					}
				}
			})
		}
	}
}

func TestScenarioSaveMemoryOwnershipPrecedesPresentedCondition(t *testing.T) {
	server := NewServer(ServerConfig{})
	server.runner.StoreScript(projectSecurityScript("scenario-policy-script", storage.DefaultProjectID))
	scenario, err := server.runner.StoreScenario(scenarioSaveRequest("Original", 1))
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(scenarioSaveRequest("Must not save", 1))
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPut, "/api/v1/scenarios/"+scenario.ID, bytes.NewReader(body))
	req.Header["If-Match"] = []string{""}
	bindAuthorizedProjectToRequest(req, "unrelated-project")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, req)
	if response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), "If-Match") {
		t.Fatalf("foreign condition disclosed capability: %d %s", response.Code, response.Body.String())
	}
}

// Every scenario-scoped route either honors a strong authoring validator or
// owns an independent revision contract. A new route must be classified here
// before it can ship; writers outside this URL prefix are classified by the
// storage writers they reach in TestEveryScenarioAuthoredWriterCallerIsClassified.
func TestEveryScenarioWriterClassifiesIfMatch(t *testing.T) {
	independent := map[string]string{
		"POST /api/v1/scenarios/{id}/archive":                   "lifecycle state, not authored content",
		"POST /api/v1/scenarios/{id}/restore":                   "lifecycle state, not authored content",
		"POST /api/v1/scenarios/{id}/schedules":                 "schedules carry their own revision CAS",
		"PUT /api/v1/scenarios/{id}/schedules/{schedule_id}":    "schedules carry their own revision CAS",
		"DELETE /api/v1/scenarios/{id}/schedules/{schedule_id}": "schedules carry their own revision CAS",
		"PUT /api/v1/scenarios/{id}/comparison-policy":          "comparison policy carries its own revision CAS",
	}
	routes, err := discoverControllerRoutes("server.go")
	if err != nil {
		t.Fatal(err)
	}
	var unclassified []string
	for _, route := range routes {
		key := route.Method + " " + route.Path
		if route.Method == http.MethodGet || !strings.HasPrefix(route.Path, "/api/v1/scenarios/{id}") {
			continue
		}
		if key == "PUT /api/v1/scenarios/{id}" || scenarioGuardedWriterRoutes[key] {
			continue
		}
		if _, ok := independent[key]; ok || strings.HasPrefix(route.Path, "/api/v1/scenarios/{id}/schedules/") {
			continue
		}
		unclassified = append(unclassified, key)
	}
	sort.Strings(unclassified)
	if len(unclassified) > 0 {
		t.Fatalf("scenario writers without an If-Match classification: %v", unclassified)
	}
	for key := range scenarioGuardedWriterRoutes {
		found := false
		for _, route := range routes {
			found = found || route.Method+" "+route.Path == key
		}
		if !found {
			t.Fatalf("classified writer %s is not a registered route", key)
		}
	}
}

func requireScenarioSaveCapabilityResponse(t *testing.T, response *httptest.ResponseRecorder, wantMessage, wantCode string) {
	t.Helper()
	var body struct {
		Error string `json:"error"`
		Code  string `json:"code"`
		Ref   string `json:"ref"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusServiceUnavailable || body.Error != wantMessage || body.Code != wantCode || body.Ref == "" {
		t.Fatalf("capability refusal: %d %s", response.Code, response.Body.String())
	}
}

func TestScenarioConditionedSaveRefreshesStaleReplicaCache(t *testing.T) {
	server, store, _, scenario := newScenarioPolicyAvailabilityFixture(t)
	// HA mode enables replica cache refresh. Another replica saves directly to
	// the shared store; this replica's cache still holds the earlier state.
	server.runner.leadership = &executionLeadership{}
	external, children, err := store.GetScenario(scenario.ID)
	if err != nil {
		t.Fatal(err)
	}
	external.Name = "Saved by another replica"
	external.UpdatedAt = external.UpdatedAt.Add(time.Second)
	if err := store.SaveScenario(external, children); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.GetScenarioAuthoringSnapshot(scenario.ID)
	if err != nil {
		t.Fatal(err)
	}
	tag, err := scenarioAuthoringETag(snapshot.ContentRevision)
	if err != nil {
		t.Fatal(err)
	}
	conditioned := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scenarios/"+scenario.ID, scenarioSaveRequest("Conditioned after refresh", 2), tag)
	if conditioned.Code != http.StatusOK {
		t.Fatalf("conditioned save against the current durable revision: %d %s", conditioned.Code, conditioned.Body.String())
	}
	stored, _, err := store.GetScenario(scenario.ID)
	if err != nil || stored.Name != "Conditioned after refresh" {
		t.Fatalf("conditioned save not durable: %#v %v", stored, err)
	}
}
