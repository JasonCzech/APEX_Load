package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/storage"
)

// deleteSeededPolicy deletes a policy the way the policy API does: a new
// revision marked deleted.
func deleteSeededPolicy(t *testing.T, store *storage.SQLiteStore, project, id string) {
	t.Helper()
	current, err := store.GetAnalysisDefinition(project, id, 0)
	if err != nil {
		t.Fatal(err)
	}
	deleted := *current
	deleted.Deleted = true
	if _, err := store.SaveAnalysisDefinition(deleted, current.Revision); err != nil {
		t.Fatal(err)
	}
}

// A run that chose a telemetry acceptance policy is judged by it, not by its
// project's default: the choice must be one of the run's own project's
// policies, persists with the run (and is reloaded with it), is kept by a
// revised copy unless it names another or project_default, and is pinned at
// Start with its source (TEL-B).
func TestRunChosenTelemetryAcceptancePolicyIsPinnedAtStart(t *testing.T) {
	t.Setenv(config.EnvArchiveDir, t.TempDir())
	runner, store, test := newPassiveTransitionTestRunner(t, "policy-chosen")
	backend := newTelemetryPolicyBarrierBackend(t)
	runner.setExecutionBackend(backend)
	project := normalizeProjectID(test.ProjectID)
	seedPolicy(t, store, project, "default-gate", storage.AnalysisDefinitionKindTelemetryAcceptancePolicy, `{"version":1,"apply_by_default":true,"min_wall_time_coverage":0.5}`)
	seedPolicy(t, store, project, "strict-gate", storage.AnalysisDefinitionKindTelemetryAcceptancePolicy, `{"version":1,"mode":"fail_ci","min_wall_time_coverage":0.99}`)
	seedPolicy(t, store, project, "other-gate", storage.AnalysisDefinitionKindTelemetryAcceptancePolicy, `{"version":1,"min_wall_time_coverage":0.7}`)
	if err := store.SaveProject(&storage.Project{ID: "another-project", Name: "Another"}); err != nil {
		t.Fatal(err)
	}
	seedPolicy(t, store, "another-project", "foreign-gate", storage.AnalysisDefinitionKindTelemetryAcceptancePolicy, `{"version":1,"min_wall_time_coverage":0.1}`)
	create := func(name, policy string) (*ManagedTest, error) {
		return runner.CreateTest(CreateTestRequest{Name: name, ScriptID: "policy-chosen-script", VUsers: 1, Duration: "1m", TelemetryAcceptancePolicyID: policy})
	}
	// Another project's policy is refused exactly like a missing one.
	_, missing := create("missing", "missing-gate")
	_, foreign := create("foreign", "foreign-gate")
	if missing == nil || foreign == nil || strings.ReplaceAll(foreign.Error(), "foreign-gate", "X") != strings.ReplaceAll(missing.Error(), "missing-gate", "X") {
		t.Fatalf("missing %v, foreign %v", missing, foreign)
	}
	if _, err := create("long", strings.Repeat("p", 129)); err == nil || strings.Contains(err.Error(), strings.Repeat("p", 129)) {
		t.Fatalf("an over-long choice: %v", err)
	}
	chosen, err := create("chosen", " strict-gate ")
	if err != nil || chosen.TelemetryAcceptancePolicyID != "strict-gate" {
		t.Fatalf("create with a chosen policy: %+v %v", chosen, err)
	}
	if stored, err := store.GetTest(chosen.ID); err != nil || stored.TelemetryAcceptancePolicyID != "strict-gate" {
		t.Fatalf("the choice did not persist: %+v %v", stored, err)
	}
	restarted := NewTestRunner(2, NewHub(), store)
	if reloaded, ok := restarted.GetTest(chosen.ID); !ok || reloaded.TelemetryAcceptancePolicyID != "strict-gate" {
		t.Fatalf("a reloaded runner lost the choice: %+v", reloaded)
	}
	sourceGroup := 0
	revise := func(name, policy string) (*ManagedTest, error) {
		return runner.CreateTest(CreateTestRequest{Name: name, Duration: "1m", RevisionSourceTestID: chosen.ID, TelemetryAcceptancePolicyID: policy,
			Groups: []CreateGroupEntry{{Name: chosen.Groups[0].Name, ScriptID: "policy-chosen-script", RevisionSourceGroupIndex: &sourceGroup, VUsers: 1}}})
	}
	for policy, want := range map[string]string{"": "strict-gate", runPolicyProjectDefault: "", "other-gate": "other-gate"} {
		if revised, err := revise("revised "+policy, policy); err != nil || revised.TelemetryAcceptancePolicyID != want {
			t.Fatalf("a revised copy naming %q: %+v %v", policy, revised, err)
		}
	}
	if err := runner.StartTest(chosen.ID); err != nil {
		t.Fatal(err)
	}
	backend.expected.Store(true)
	waitTelemetryBackend(t, backend)
	finishTelemetryBackend(t, backend)
	stored, err := store.GetTest(chosen.ID)
	if err != nil {
		t.Fatal(err)
	}
	initial := decodeStoredInitialProvenance(stored.InitialProvenance)
	if initial == nil || initial.TelemetryAcceptancePolicy == nil || initial.TelemetryAcceptancePolicy.Pin.ID != "strict-gate" || initial.TelemetryAcceptancePolicy.Pin.Source != runPolicySourceSelected || initial.TelemetryAcceptancePolicy.Definition.EffectiveMode() != "fail_ci" {
		t.Fatalf("admission pinned %+v", initial)
	}
	for _, pin := range initial.PolicyPins {
		if pin.ID == "default-gate" {
			t.Fatalf("the project's default was pinned beside the chosen policy: %+v", initial.PolicyPins)
		}
	}
}

// A chosen policy deleted after the run was created refuses Start rather than
// falling back to the project's default, and leaves the run queued; a revised
// copy that would inherit it is refused, naming where the choice came from.
func TestRunChosenTelemetryAcceptancePolicyGoneRefusesStart(t *testing.T) {
	t.Setenv(config.EnvArchiveDir, t.TempDir())
	runner, store, test := newPassiveTransitionTestRunner(t, "policy-chosen-gone")
	runner.setExecutionBackend(newTelemetryPolicyBarrierBackend(t))
	project := normalizeProjectID(test.ProjectID)
	seedPolicy(t, store, project, "default-gate", storage.AnalysisDefinitionKindTelemetryAcceptancePolicy, `{"version":1,"apply_by_default":true}`)
	seedPolicy(t, store, project, "doomed-gate", storage.AnalysisDefinitionKindTelemetryAcceptancePolicy, `{"version":1,"min_wall_time_coverage":0.9}`)
	chosen, err := runner.CreateTest(CreateTestRequest{Name: "doomed", ScriptID: "policy-chosen-gone-script", VUsers: 1, Duration: "1m", TelemetryAcceptancePolicyID: "doomed-gate"})
	if err != nil {
		t.Fatal(err)
	}
	deleteSeededPolicy(t, store, project, "doomed-gate")
	if err := runner.StartTest(chosen.ID); !errors.Is(err, errRunPolicyConfiguration) || !strings.Contains(err.Error(), "doomed-gate") {
		t.Fatalf("Start with a deleted chosen policy: %v", err)
	}
	if stored, err := store.GetTest(chosen.ID); err != nil || stored.Status != "QUEUED" || decodeStoredInitialProvenance(stored.InitialProvenance) != nil {
		t.Fatalf("the refused run: %+v %v", stored, err)
	}
	sourceGroup := 0
	_, err = runner.CreateTest(CreateTestRequest{Name: "inherits", Duration: "1m", RevisionSourceTestID: chosen.ID,
		Groups: []CreateGroupEntry{{Name: chosen.Groups[0].Name, ScriptID: "policy-chosen-gone-script", RevisionSourceGroupIndex: &sourceGroup, VUsers: 1}}})
	if err == nil || !strings.Contains(err.Error(), "source run's chosen") || !strings.Contains(err.Error(), "doomed-gate") || !strings.Contains(err.Error(), runPolicyProjectDefault) {
		t.Fatalf("a revised copy inheriting a deleted choice: %v", err)
	}
}

// POST /api/v1/tests carries the choice: a keyed retry spelling it with
// spaces replays the run, a keyed retry naming another policy conflicts, and
// an invalid chosen policy is answered 409.
func TestCreateTestRouteChosenTelemetryAcceptancePolicy(t *testing.T) {
	server, store := testCreateReceiptFixture(t)
	project := normalizeProjectID("")
	seedPolicy(t, store, project, "route-gate", storage.AnalysisDefinitionKindTelemetryAcceptancePolicy, `{"version":1,"min_wall_time_coverage":0.9}`)
	seedPolicy(t, store, project, "route-other", storage.AnalysisDefinitionKindTelemetryAcceptancePolicy, `{"version":1,"min_wall_time_coverage":0.5}`)
	seedPolicy(t, store, project, "route-broken", storage.AnalysisDefinitionKindTelemetryAcceptancePolicy, `{"version":1}`)
	request := receiptRunRequest("Chosen policy run")
	request.TelemetryAcceptancePolicyID = "route-gate"
	first := receiptCreateRequest(t, server, "/api/v1/tests", request, runCreateKey, "")
	if first.Code != http.StatusCreated || decodeCreatedRun(t, first).TelemetryAcceptancePolicyID != "route-gate" {
		t.Fatalf("create: %d %s", first.Code, first.Body.String())
	}
	spaced := request
	spaced.TelemetryAcceptancePolicyID = " route-gate "
	if retry := receiptCreateRequest(t, server, "/api/v1/tests", spaced, runCreateKey, "1"); retry.Code != http.StatusCreated || retry.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("a retry spelling the choice with spaces: %d %s", retry.Code, retry.Body.String())
	}
	other := request
	other.TelemetryAcceptancePolicyID = "route-other"
	if changed := receiptCreateRequest(t, server, "/api/v1/tests", other, runCreateKey, ""); changed.Code != http.StatusConflict || idempotencyErrorCode(t, changed) != "idempotency_key_reused" {
		t.Fatalf("a retry naming another policy: %d %s", changed.Code, changed.Body.String())
	}
	broken := receiptRunRequest("Broken policy run")
	broken.TelemetryAcceptancePolicyID = "route-broken"
	if refused := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/tests", broken, ""); refused.Code != http.StatusConflict || !strings.Contains(refused.Body.String(), "route-broken") {
		t.Fatalf("an invalid chosen policy: %d %s", refused.Code, refused.Body.String())
	}
}

// CI judges the inconclusive-mapping requirement by the policy the run chose:
// a chosen fail_ci policy needs telemetry_inconclusive although the default
// is advisory, a chosen advisory policy does not although the default is
// fail_ci, and an unknown choice is refused.
func TestCITelemetryInconclusiveRequirementFollowsTheChosenPolicy(t *testing.T) {
	server, store := newCIContractTestServer(t, ciRunSourceTestPolicy())
	seedPolicy(t, store, "project-ci", "default-gate", storage.AnalysisDefinitionKindTelemetryAcceptancePolicy, `{"version":1,"apply_by_default":true,"mode":"fail_ci","min_wall_time_coverage":0.9}`)
	seedPolicy(t, store, "project-ci", "advisory-gate", storage.AnalysisDefinitionKindTelemetryAcceptancePolicy, `{"version":1,"min_wall_time_coverage":0.9}`)
	if status, _ := server.ciTelemetryInconclusiveRequirement("project-ci", "", ""); status != http.StatusBadRequest {
		t.Fatalf("fail_ci default = %d", status)
	}
	if status, message := server.ciTelemetryInconclusiveRequirement("project-ci", "", "advisory-gate"); status != 0 {
		t.Fatalf("chosen advisory policy = %d %s", status, message)
	}
	if status, message := server.ciTelemetryInconclusiveRequirement("project-ci", "", "missing-gate"); status != http.StatusBadRequest || !strings.Contains(message, "missing-gate") {
		t.Fatalf("unknown chosen policy = %d %s", status, message)
	}
	seedPolicy(t, store, "project-ci", "strict-gate", storage.AnalysisDefinitionKindTelemetryAcceptancePolicy, `{"version":1,"mode":"fail_ci","min_wall_time_coverage":0.99}`)
	if status, message := server.ciTelemetryInconclusiveRequirement("project-ci", "", "strict-gate"); status != http.StatusBadRequest || !strings.Contains(message, "chosen") {
		t.Fatalf("chosen fail_ci policy = %d %s", status, message)
	}
	if status, _ := server.ciTelemetryInconclusiveRequirement("project-ci", "pass", "strict-gate"); status != 0 {
		t.Fatal("an explicit mapping was refused")
	}
}

// A CI run chooses its policy only where the project's CI policy allows it
// (project_default, the default, is no choice and needs no permission); then
// the choice reaches its run and is echoed, is part of the request a retry
// under the same key must repeat, and must exist in the project. An exact
// retry returns the accepted run even after its choice is no longer allowed
// or no longer exists.
func TestCIRunCarriesTheChosenTelemetryAcceptancePolicy(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer target.Close()
	policy := ciRunSourceTestPolicy()
	policy.MaxDurationMinutes = 1
	policy.AllowedTargetHosts = []string{"127.0.0.1"}
	locked, _ := newCIContractTestServer(t, policy)
	policy.AllowTelemetryPolicySelection = true
	server, store := newCIContractTestServer(t, policy)
	seedPolicy(t, store, "project-ci", "advisory-gate", storage.AnalysisDefinitionKindTelemetryAcceptancePolicy, `{"version":1,"min_wall_time_coverage":0.9}`)
	seedPolicy(t, store, "project-ci", "other-gate", storage.AnalysisDefinitionKindTelemetryAcceptancePolicy, `{"version":1,"min_wall_time_coverage":0.5}`)
	publishCheck := false
	scriptRaw, err := json.Marshal(ciTestScript(target.URL))
	if err != nil {
		t.Fatal(err)
	}
	request := ciRunCreateRequest{
		ProjectID: "project-ci", ScriptPath: "tests/performance.json", Script: scriptRaw,
		Execution: ciRunExecutionRequest{VUsers: 1, Duration: "50ms"}, Source: ciTestRunSource("1"),
		PublishCheck: &publishCheck, TelemetryAcceptancePolicyID: "advisory-gate",
	}
	invoke := func(server *Server, key string, req ciRunCreateRequest) (int, ciRunResponse, string) {
		t.Helper()
		body, err := json.Marshal(req)
		if err != nil {
			t.Fatal(err)
		}
		httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/ci/runs", bytes.NewReader(body))
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Idempotency-Key", key)
		recorder := httptest.NewRecorder()
		server.handleCreateCIRun(recorder, httpReq)
		var response ciRunResponse
		if recorder.Code == http.StatusAccepted {
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
		}
		return recorder.Code, response, recorder.Body.String()
	}
	if status, _, body := invoke(locked, "octo/apex:performance:9001:1:telemetry-policy-0000", request); status != http.StatusForbidden || !strings.Contains(body, "allow_telemetry_policy_selection") {
		t.Fatalf("a choice the project's CI policy does not allow: %d %s", status, body)
	}
	byDefault := request
	byDefault.TelemetryAcceptancePolicyID = " " + runPolicyProjectDefault + " "
	if status, response, body := invoke(locked, "octo/apex:performance:9001:1:telemetry-policy-dddd", byDefault); status != http.StatusAccepted || response.TelemetryAcceptancePolicyID != "" {
		t.Fatalf("project_default where choosing is not allowed: %d %s", status, body)
	}
	const key = "octo/apex:performance:9001:1:telemetry-policy-aaaa"
	status, created, body := invoke(server, key, request)
	if status != http.StatusAccepted || created.TestID == "" || created.TelemetryAcceptancePolicyID != "advisory-gate" {
		t.Fatalf("CI run: %d %s", status, body)
	}
	if run, ok := server.runner.GetTest(created.TestID); !ok || run.TelemetryAcceptancePolicyID != "advisory-gate" {
		t.Fatalf("CI run's chosen policy: %+v", run)
	}
	changed := request
	changed.TelemetryAcceptancePolicyID = "other-gate"
	if status, _, body := invoke(server, key, changed); status != http.StatusConflict {
		t.Fatalf("a retry naming another policy: %d %s", status, body)
	}
	unknown := request
	unknown.TelemetryAcceptancePolicyID = "missing-gate"
	if status, _, body := invoke(server, "octo/apex:performance:9001:1:telemetry-policy-bbbb", unknown); status != http.StatusBadRequest || !strings.Contains(body, "missing-gate") {
		t.Fatalf("an unknown policy: %d %s", status, body)
	}
	unknown.TelemetryInconclusive = "pass"
	if status, _, body := invoke(server, "octo/apex:performance:9001:1:telemetry-policy-cccc", unknown); status != http.StatusBadRequest || !strings.Contains(body, "missing-gate") {
		t.Fatalf("an unknown policy with a mapping: %d %s", status, body)
	}
	project, err := store.GetProject("project-ci")
	if err != nil {
		t.Fatal(err)
	}
	project.Settings.GitHub.Actions.AllowTelemetryPolicySelection = false
	if err := store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	deleteSeededPolicy(t, store, "project-ci", "advisory-gate")
	if status, retried, body := invoke(server, key, request); status != http.StatusAccepted || retried.TestID != created.TestID || retried.TelemetryAcceptancePolicyID != "advisory-gate" {
		t.Fatalf("an exact retry after the choice was disallowed and deleted: %d %s", status, body)
	}
}

// unavailablePolicyStore fails every listing of the project's analysis
// definitions, as a database outage would.
type unavailablePolicyStore struct{ *storage.SQLiteStore }

func (unavailablePolicyStore) ListAnalysisDefinitions(string, string, int, int) ([]storage.AnalysisDefinition, error) {
	return nil, errors.New("database is locked")
}

func (unavailablePolicyStore) ListLatestAnalysisDefinitions(string, string) ([]storage.AnalysisDefinition, error) {
	return nil, errors.New("database is locked")
}

func (unavailablePolicyStore) GetAnalysisPolicyRevision(string, string, int64) (*storage.AnalysisDefinition, *storage.AnalysisDefinition, error) {
	return nil, nil, errors.New("database is locked")
}

// A chosen policy that cannot be read is answered 503, retryable, on both
// run-creating routes, never as the caller's mistake.
func TestChosenTelemetryAcceptancePolicyStoreOutageIs503(t *testing.T) {
	outage := func(server *Server, store *storage.SQLiteStore) {
		server.runner.mu.Lock()
		server.runner.testStore = unavailablePolicyStore{store}
		server.runner.mu.Unlock()
	}
	direct, directStore := testCreateReceiptFixture(t)
	outage(direct, directStore)
	if _, err := direct.runner.validateRunTelemetryAcceptancePolicy("", "any-gate"); !errors.Is(err, errRunPolicyStoreUnavailable) {
		t.Fatalf("validation during an outage: %v", err)
	}
	request := receiptRunRequest("Outage run")
	request.TelemetryAcceptancePolicyID = "any-gate"
	if recorder := authoringAPIRequest(t, direct, http.MethodPost, "/api/v1/tests", request, ""); recorder.Code != http.StatusServiceUnavailable || strings.Contains(recorder.Body.String(), "database is locked") {
		t.Fatalf("POST /api/v1/tests during an outage: %d %s", recorder.Code, recorder.Body.String())
	}
	if recorder := authoringAPIRequest(t, direct, http.MethodPost, "/api/v1/tests/preflight", LaunchPreflightRequest{CreateTestRequest: request}, ""); recorder.Code != http.StatusServiceUnavailable || strings.Contains(recorder.Body.String(), "database is locked") {
		t.Fatalf("POST /api/v1/tests/preflight during an outage: %d %s", recorder.Code, recorder.Body.String())
	}
	policy := ciRunSourceTestPolicy()
	policy.AllowTelemetryPolicySelection = true
	server, store := newCIContractTestServer(t, policy)
	outage(server, store)
	scriptRaw, err := json.Marshal(ciTestScript("http://127.0.0.1:9"))
	if err != nil {
		t.Fatal(err)
	}
	publishCheck := false
	body, err := json.Marshal(ciRunCreateRequest{ProjectID: "project-ci", ScriptPath: "tests/performance.json", Script: scriptRaw,
		Execution: ciRunExecutionRequest{VUsers: 1, Duration: "50ms"}, Source: ciTestRunSource("1"), PublishCheck: &publishCheck, TelemetryAcceptancePolicyID: "any-gate"})
	if err != nil {
		t.Fatal(err)
	}
	ciRequest := httptest.NewRequest(http.MethodPost, "/api/v1/ci/runs", bytes.NewReader(body))
	ciRequest.Header.Set("Content-Type", "application/json")
	ciRequest.Header.Set("Idempotency-Key", "octo/apex:performance:9001:1:telemetry-policy-outage")
	recorder := httptest.NewRecorder()
	server.handleCreateCIRun(recorder, ciRequest)
	if recorder.Code != http.StatusServiceUnavailable || strings.Contains(recorder.Body.String(), "database is locked") {
		t.Fatalf("POST /api/v1/ci/runs during an outage: %d %s", recorder.Code, recorder.Body.String())
	}
}

// The GitHub Check names a policy the run chose.
func TestCIGitHubCheckNamesAChosenPolicy(t *testing.T) {
	verdict := ciTelemetryVerdict{TelemetryAcceptanceVerdict: TelemetryAcceptanceVerdict{Outcome: TelemetryAcceptancePass, Mode: "fail_ci",
		Policy: &RunPolicyPin{ID: "strict-gate", Name: "Strict gate", Revision: 3, Source: runPolicySourceSelected}}}
	if text := strings.Join(ciGitHubCheckTelemetryLines(&verdict), "\n"); !strings.Contains(text, "Strict gate revision 3 (chosen by the run)") {
		t.Fatalf("check lines = %s", text)
	}
	verdict.Policy.Source = "project_default"
	if text := strings.Join(ciGitHubCheckTelemetryLines(&verdict), "\n"); strings.Contains(text, "chosen by the run") {
		t.Fatalf("a default policy was called chosen: %s", text)
	}
}

// A remote controller that does not confirm the chosen policy would judge
// the run by its project's default: the remote run is deleted, never
// started, and the create is refused.
func TestRemoteRunNeedsTheChosenPolicyConfirmed(t *testing.T) {
	t.Setenv(remoteOutboundAllowPrivateHostsEnv, "1")
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "remote-policy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var echo bool
	var created, deleted int
	var remoteTestID, forwarded string
	remoteHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/tests":
			created++
			var req CreateTestRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			remoteTestID, forwarded = req.RequestedTestID, req.TelemetryAcceptancePolicyID
			test := &ManagedTest{ID: remoteTestID, Status: "QUEUED", ProjectID: "remote-project"}
			if echo {
				test.TelemetryAcceptancePolicyID = req.TelemetryAcceptancePolicyID
			}
			writeJSON(w, http.StatusCreated, test)
		case r.Method == http.MethodDelete && remoteTestID != "" && r.URL.Path == "/api/v1/tests/"+remoteTestID:
			deleted++
			w.WriteHeader(http.StatusNoContent)
		default:
			writeError(w, http.StatusNotFound, r.Method+" "+r.URL.Path)
		}
	}))
	defer remoteHTTP.Close()
	server := NewServer(ServerConfig{Store: store})
	remote := LibraryRemote{ID: "workspace", Name: "Workspace", BaseURL: remoteHTTP.URL, DefaultProjectID: "remote-project", ProjectMappings: map[string]string{"project-a": "remote-project"}, AuthMethod: "none"}
	server.libraryMu.Lock()
	server.libraryRemotes[remote.ID] = &remote
	server.libraryMu.Unlock()
	if err := server.persistLibraryRemotes(); err != nil {
		t.Fatal(err)
	}
	create := func() *httptest.ResponseRecorder {
		body := marshalLibraryTestJSON(t, RemoteRunCreateRequest{CreateTestRequest: CreateTestRequest{Name: "Remote", Duration: "30s", ProjectID: "project-a", TelemetryAcceptancePolicyID: "remote-gate"}})
		response := httptest.NewRecorder()
		server.mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/remotes/workspace/runs", bytes.NewReader(body)))
		return response
	}
	if response := create(); response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "remote-gate") || forwarded != "remote-gate" || created != 1 || deleted != 1 {
		t.Fatalf("an unconfirmed remote choice: %d %s (forwarded %q, created %d, deleted %d)", response.Code, response.Body.String(), forwarded, created, deleted)
	}
	echo = true
	if response := create(); response.Code != http.StatusCreated || created != 2 || deleted != 1 {
		t.Fatalf("a confirmed remote choice: %d %s (created %d, deleted %d)", response.Code, response.Body.String(), created, deleted)
	}
}
