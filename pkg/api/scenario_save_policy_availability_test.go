package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestScenarioSavePolicyUnavailableProjectRoutesRefuseEnablement(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "policy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})
	beforeProjects, err := store.ListProjects()
	if err != nil {
		t.Fatal(err)
	}
	beforeApplications, err := store.ListApplications()
	if err != nil {
		t.Fatal(err)
	}
	create := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/projects", projectRequest{Name: "Not created", Settings: storage.ProjectSettings{Authoring: &storage.ProjectAuthoringPolicy{RequireScenarioSaveIfMatch: true}}}, "")
	if create.Code != http.StatusBadRequest || !strings.Contains(create.Body.String(), "require_scenario_save_if_match is unavailable") {
		t.Fatalf("strict project create: %d %s", create.Code, create.Body.String())
	}
	afterProjects, err := store.ListProjects()
	if err != nil {
		t.Fatal(err)
	}
	afterApplications, err := store.ListApplications()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(beforeProjects, afterProjects) || !reflect.DeepEqual(beforeApplications, afterApplications) {
		t.Fatal("refused create persisted a project or its hierarchy")
	}
	before, err := store.GetProject(storage.DefaultProjectID)
	if err != nil {
		t.Fatal(err)
	}
	update := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/projects/"+before.ID, projectRequest{Name: "Must not rename", Settings: storage.ProjectSettings{Authoring: &storage.ProjectAuthoringPolicy{RequireScenarioSaveIfMatch: true, RequireScriptSaveIfMatch: true}}}, "")
	if update.Code != http.StatusBadRequest || !strings.Contains(update.Body.String(), "require_scenario_save_if_match is unavailable") {
		t.Fatalf("strict project update: %d %s", update.Code, update.Body.String())
	}
	after, err := store.GetProject(before.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("refused project update persisted state: %v", err)
	}
}

func TestScenarioSavePolicyUnavailableProjectRoutesPreserveSupportedSettings(t *testing.T) {
	for _, tc := range []struct {
		name      string
		authoring *storage.ProjectAuthoringPolicy
	}{
		{"omitted", nil},
		{"explicit false", &storage.ProjectAuthoringPolicy{}},
		{"script strict", &storage.ProjectAuthoringPolicy{RequireScriptSaveIfMatch: true}},
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

func TestScenarioSavePolicyUnavailableSeededStrictRefusesWithoutSideEffects(t *testing.T) {
	for _, tc := range []struct {
		name          string
		tag           string
		legacyAdapter bool
	}{
		{name: "coherent unconditioned"},
		{name: "coherent presented tag", tag: `"scenario-authoring-v1:scn-v1-` + strings.Repeat("a", 64) + `"`},
		{name: "legacy adapter", legacyAdapter: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, store, secrets, scenario := newScenarioPolicyAvailabilityFixture(t)
			// This bypasses public project validation deliberately, representing a
			// preconfigured policy. The route must read it durably, not cached state.
			project, err := store.GetProject(storage.DefaultProjectID)
			if err != nil {
				t.Fatal(err)
			}
			project.Settings.Authoring = &storage.ProjectAuthoringPolicy{RequireScenarioSaveIfMatch: true}
			if err := store.SaveProject(project); err != nil {
				t.Fatal(err)
			}
			before, err := store.GetScenarioAuthoringSnapshot(scenario.ID)
			if err != nil {
				t.Fatal(err)
			}
			request := CreateScenarioRequest{Name: "Must not save", Duration: "1m", RuntimeSettings: &RuntimeSettingsOverride{Env: map[string]string{"API_TOKEN": "must-not-provision"}}, Groups: []CreateGroupEntry{{ScriptID: "scenario-policy-script", VUsers: 1}}}
			if tc.legacyAdapter {
				server.store = &scenarioPolicyAvailabilityFaultStore{Store: store}
			}
			response := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scenarios/"+scenario.ID, request, tc.tag)
			wantMessage := "scenario If-Match saves are unavailable"
			wantCode := "scenario_save_if_match_unavailable"
			if tc.tag == "" {
				wantMessage = "scenario If-Match saves are unavailable; the configured strict policy prevents this save"
				wantCode = "scenario_save_strict_policy_unavailable"
			}
			requireScenarioSaveCapabilityResponse(t, response, wantMessage, wantCode)
			after, err := store.GetScenarioAuthoringSnapshot(scenario.ID)
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
		})
	}
}

type scenarioPolicyAvailabilityFaultStore struct {
	storage.Store
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

func TestScenarioSavePolicyUnavailableOwnershipAndReadFailuresRefuse(t *testing.T) {
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
			server.store = &scenarioPolicyAvailabilityFaultStore{Store: store, failProject: tc.failProject, failScenario: tc.failScenario, wrongScenarioID: tc.wrongScenarioID}
			body, err := json.Marshal(CreateScenarioRequest{Name: "Must not save", Duration: "1m", Groups: []CreateGroupEntry{{ScriptID: "scenario-policy-script", VUsers: 1}}})
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

func TestScenarioSavePolicyUnavailableFalseAndScriptPolicyAllowLegacySave(t *testing.T) {
	for _, authoring := range []*storage.ProjectAuthoringPolicy{nil, {}, {RequireScriptSaveIfMatch: true}} {
		server, store, _, scenario := newScenarioPolicyAvailabilityFixture(t)
		project, err := store.GetProject(storage.DefaultProjectID)
		if err != nil {
			t.Fatal(err)
		}
		project.Settings.Authoring = authoring
		if err := store.SaveProject(project); err != nil {
			t.Fatal(err)
		}
		response := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scenarios/"+scenario.ID, CreateScenarioRequest{Name: "Saved", Duration: "1m", Groups: []CreateGroupEntry{{ScriptID: "scenario-policy-script", VUsers: 1}}}, "")
		if response.Code != http.StatusOK {
			t.Fatalf("compatible scenario PUT: %d %s", response.Code, response.Body.String())
		}
		stored, _, err := store.GetScenario(scenario.ID)
		if err != nil || stored.Name != "Saved" {
			t.Fatalf("compatible save not durable: %#v %v", stored, err)
		}
	}
}

func TestScenarioSavePolicyUnavailableDurableDeletionAndCachedOwnerRefuse(t *testing.T) {
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
			response := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scenarios/"+scenario.ID, CreateScenarioRequest{Name: "Must not save", Duration: "1m", Groups: []CreateGroupEntry{{ScriptID: "scenario-policy-script", VUsers: 1}}}, "")
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

func TestScenarioSavePolicyUnavailablePresentedIfMatchNeverFallsThrough(t *testing.T) {
	tag := `"scenario-authoring-v1:scn-v1-` + strings.Repeat("a", 64) + `"`
	for _, memoryOnly := range []bool{false, true} {
		for _, tc := range []struct {
			name   string
			header http.Header
		}{
			{"valid", http.Header{"If-Match": []string{tag}}},
			{"empty", http.Header{"If-Match": []string{""}}},
			{"multiple", http.Header{"If-Match": []string{tag, tag}}},
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
					scenario, err = server.runner.StoreScenario(CreateScenarioRequest{Name: "Original", Duration: "1m", Groups: []CreateGroupEntry{{ScriptID: "scenario-policy-script", VUsers: 1}}})
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
				body, err := json.Marshal(CreateScenarioRequest{Name: "Must not save", Duration: "1m", RuntimeSettings: &RuntimeSettingsOverride{Env: map[string]string{"API_TOKEN": "must-not-provision"}}, Groups: []CreateGroupEntry{{ScriptID: "scenario-policy-script", VUsers: 1}}})
				if err != nil {
					t.Fatal(err)
				}
				req := httptest.NewRequest(http.MethodPut, "/api/v1/scenarios/"+scenario.ID, bytes.NewReader(body))
				req.Header = tc.header.Clone()
				response := httptest.NewRecorder()
				server.mux.ServeHTTP(response, req)
				requireScenarioSaveCapabilityResponse(t, response, "scenario If-Match saves are unavailable", "scenario_save_if_match_unavailable")
				after, ok := server.runner.GetScenario(scenario.ID)
				if !ok || !reflect.DeepEqual(before, after) {
					t.Fatal("unsupported condition mutated cached state")
				}
				if store != nil {
					durableAfter, err := store.GetScenarioAuthoringSnapshot(scenario.ID)
					if err != nil || !reflect.DeepEqual(durableBefore, durableAfter) {
						t.Fatalf("unsupported condition mutated durable state: %v", err)
					}
				}
				secrets.mu.Lock()
				calls, count := secrets.createCalls, len(secrets.values)
				secrets.mu.Unlock()
				if calls != 0 || count != 0 {
					t.Fatalf("unsupported condition provisioned credentials: calls=%d count=%d", calls, count)
				}
				if store != nil {
					attempts, err := store.ListRuntimeSecretProvisioningAttempts()
					if err != nil || len(attempts) != 0 {
						t.Fatalf("unsupported condition created journals: %d %v", len(attempts), err)
					}
				}
			})
		}
	}
}

func TestScenarioSavePolicyUnavailableMemoryOwnershipPrecedesPresentedCondition(t *testing.T) {
	server := NewServer(ServerConfig{})
	server.runner.StoreScript(projectSecurityScript("scenario-policy-script", storage.DefaultProjectID))
	scenario, err := server.runner.StoreScenario(CreateScenarioRequest{Name: "Original", Duration: "1m", Groups: []CreateGroupEntry{{ScriptID: "scenario-policy-script", VUsers: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(CreateScenarioRequest{Name: "Must not save", Duration: "1m", Groups: []CreateGroupEntry{{ScriptID: "scenario-policy-script", VUsers: 1}}})
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
	if strings.Contains(response.Body.String(), "must-not-provision") || strings.Contains(response.Body.String(), "scn-v1-") {
		t.Fatal("capability response disclosed incoming evidence")
	}
}
