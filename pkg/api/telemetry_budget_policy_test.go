package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/storage"
	"github.com/jasonczech/apexload/pkg/telemetrypolicy"
)

const tightBudgetPolicy = `{"version":1,"sample_age_ms":1500,"generator_lag_ms":250,"transport_lag_ms":0}`

func systemBudgetReceiptFixture(t *testing.T, distributed bool) *TelemetryBudgetPolicy {
	t.Helper()
	settings := RunArchiveExecutionSettingsSnapshot{Thresholds: normalizeThresholdSettings(ThresholdSettings{})}
	settings.General.MetricsIntervalSeconds = 2
	receipt := captureTelemetryBudgetPolicy(settings, distributed, time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC))
	if receipt == nil {
		t.Fatal("no system receipt")
	}
	return receipt
}

// A chosen telemetry_policy revision replaces the system receipt: it keeps
// the controller's cadence, names the revision and its definition digest,
// takes the policy's stage limits, uses the built-in default for a stage the
// policy leaves unset and disables a stage set to 0.
func TestProjectTelemetryBudgetPolicyReceipt(t *testing.T) {
	system := systemBudgetReceiptFixture(t, false)
	definition := &storage.AnalysisDefinition{ID: "tight-budgets", Name: "Tight", Kind: storage.AnalysisDefinitionKindTelemetryPolicy, Revision: 4, Definition: json.RawMessage(tightBudgetPolicy)}
	receipt, err := projectTelemetryBudgetPolicy(system, definition)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(definition.Definition)
	if receipt.SchemaVersion != telemetrypolicy.VersionProjectPolicy || receipt.PolicyID != "tight-budgets" || receipt.Revision != 4 || receipt.DefinitionSHA256 != hex.EncodeToString(digest[:]) || !validTelemetryBudgetPolicy(receipt) {
		t.Fatalf("receipt identity = %+v", receipt)
	}
	if receipt.ExpectedControllerPublicationIntervalMs != system.ExpectedControllerPublicationIntervalMs || receipt.CadenceScope != system.CadenceScope || !receipt.CapturedAt.Equal(system.CapturedAt) {
		t.Fatalf("the controller's cadence was not kept: %+v", receipt)
	}
	want := []TelemetryStageBudget{
		{Stage: "sample_age", Enabled: true, LimitMs: 1500},
		{Stage: "generator_processing", Enabled: true, LimitMs: 250},
		{Stage: "transport", Enabled: false, LimitMs: 0},
		{Stage: "controller_fanout", Enabled: true, LimitMs: defaultTelemetryControllerFanoutBudgetMs},
		{Stage: "browser_render", Enabled: true, LimitMs: defaultTelemetryBrowserRenderBudgetMs},
	}
	for index, budget := range receipt.Budgets {
		if budget != want[index] {
			t.Fatalf("budget %d = %+v, want %+v", index, budget, want[index])
		}
	}
	thresholds := telemetryPolicyThresholds(receipt)
	if thresholds.MaxTelemetrySampleAgeMs == nil || *thresholds.MaxTelemetrySampleAgeMs != 1500 || thresholds.MaxTelemetryTransportLagMs == nil || *thresholds.MaxTelemetryTransportLagMs != 0 {
		t.Fatalf("live thresholds = %+v", thresholds)
	}
	if evidence := telemetryPolicyEvidenceFromProvenance(&RunArchiveProvenance{TelemetryPolicy: receipt}); evidence.Availability != "available" || evidence.Policy.PolicyID != "tight-budgets" {
		t.Fatalf("receipt evidence = %+v", evidence)
	}
	if !system.CapturedAt.Equal(time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)) || system.SchemaVersion != telemetrypolicy.Version || system.Budgets[0].LimitMs == 1500 {
		t.Fatal("the system receipt was changed in place")
	}
	for name, mutate := range map[string]func(*storage.AnalysisDefinition, **TelemetryBudgetPolicy){
		"other window": func(d *storage.AnalysisDefinition, _ **TelemetryBudgetPolicy) {
			d.Definition = json.RawMessage(`{"version":1,"sustained_window_seconds":30}`)
		},
		"invalid policy": func(d *storage.AnalysisDefinition, _ **TelemetryBudgetPolicy) {
			d.Definition = json.RawMessage(`{"version":1}`)
		},
		"other kind": func(d *storage.AnalysisDefinition, _ **TelemetryBudgetPolicy) {
			d.Kind = storage.AnalysisDefinitionKindTelemetryAcceptancePolicy
		},
		"revision zero":   func(d *storage.AnalysisDefinition, _ **TelemetryBudgetPolicy) { d.Revision = 0 },
		"system id":       func(d *storage.AnalysisDefinition, _ **TelemetryBudgetPolicy) { d.ID = telemetrypolicy.SystemPolicyID },
		"invalid receipt": func(_ *storage.AnalysisDefinition, s **TelemetryBudgetPolicy) { (*s).Hash = "" },
		"chosen receipt":  func(_ *storage.AnalysisDefinition, s **TelemetryBudgetPolicy) { *s = receipt },
	} {
		t.Run(name, func(t *testing.T) {
			copied := *definition
			base := cloneTelemetryBudgetPolicy(system)
			mutate(&copied, &base)
			if _, err := projectTelemetryBudgetPolicy(base, &copied); err == nil {
				t.Fatal("an unusable budget policy formed a receipt")
			}
		})
	}
	same := json.RawMessage(`{"version":1,"sustained_window_seconds":15}`)
	if _, err := projectTelemetryBudgetPolicy(system, &storage.AnalysisDefinition{ID: "window", Name: "Window", Kind: storage.AnalysisDefinitionKindTelemetryPolicy, Revision: 1, Definition: same}); err != nil {
		t.Fatalf("the live window was refused: %v", err)
	}
}

// A run that chose a telemetry budget policy has its live budgets set by it:
// the choice must be one of the run's own project's usable policies, persists
// with the run, is kept by a revised copy unless it names another or
// project_default, and at Start its revision is pinned and becomes the run's
// budget receipt. A run without a choice keeps the system receipt.
func TestRunChosenTelemetryBudgetPolicySetsLiveBudgets(t *testing.T) {
	t.Setenv(config.EnvArchiveDir, t.TempDir())
	runner, store, test := newPassiveTransitionTestRunner(t, "budget-chosen")
	backend := newTelemetryPolicyBarrierBackend(t)
	runner.setExecutionBackend(backend)
	project := normalizeProjectID(test.ProjectID)
	seedPolicy(t, store, project, "default-budgets", storage.AnalysisDefinitionKindTelemetryPolicy, `{"version":1,"apply_by_default":true,"sample_age_ms":3000}`)
	seedPolicy(t, store, project, "tight-budgets", storage.AnalysisDefinitionKindTelemetryPolicy, tightBudgetPolicy)
	seedPolicy(t, store, project, "other-budgets", storage.AnalysisDefinitionKindTelemetryPolicy, `{"version":1,"sample_age_ms":900}`)
	seedPolicy(t, store, project, "slow-window", storage.AnalysisDefinitionKindTelemetryPolicy, `{"version":1,"sustained_window_seconds":60}`)
	if err := store.SaveProject(&storage.Project{ID: "another-project", Name: "Another"}); err != nil {
		t.Fatal(err)
	}
	seedPolicy(t, store, "another-project", "foreign-budgets", storage.AnalysisDefinitionKindTelemetryPolicy, `{"version":1,"sample_age_ms":1}`)
	create := func(name, policy string) (*ManagedTest, error) {
		return runner.CreateTest(CreateTestRequest{Name: name, ScriptID: "budget-chosen-script", VUsers: 1, Duration: "1m", TelemetryBudgetPolicyID: policy})
	}
	_, missing := create("missing", "missing-budgets")
	_, foreign := create("foreign", "foreign-budgets")
	if missing == nil || foreign == nil || strings.ReplaceAll(foreign.Error(), "foreign-budgets", "X") != strings.ReplaceAll(missing.Error(), "missing-budgets", "X") {
		t.Fatalf("missing %v, foreign %v", missing, foreign)
	}
	if _, err := create("long", strings.Repeat("b", 129)); err == nil || !strings.Contains(err.Error(), "telemetry_budget_policy_id") {
		t.Fatalf("an over-long choice: %v", err)
	}
	if _, err := create("window", "slow-window"); !errors.Is(err, errRunPolicyConfiguration) || !strings.Contains(err.Error(), "sustained window") {
		t.Fatalf("a policy with another sustained window: %v", err)
	}
	// An acceptance policy's ID is not a budget policy.
	seedPolicy(t, store, project, "a-gate", storage.AnalysisDefinitionKindTelemetryAcceptancePolicy, `{"version":1,"min_wall_time_coverage":0.5}`)
	if _, err := create("kind", "a-gate"); err == nil || !strings.Contains(err.Error(), "was not found") {
		t.Fatalf("another kind's policy: %v", err)
	}
	chosen, err := create("chosen", " tight-budgets ")
	if err != nil || chosen.TelemetryBudgetPolicyID != "tight-budgets" || chosen.TelemetryAcceptancePolicyID != "" {
		t.Fatalf("create with a chosen budget policy: %+v %v", chosen, err)
	}
	if stored, err := store.GetTest(chosen.ID); err != nil || stored.TelemetryBudgetPolicyID != "tight-budgets" {
		t.Fatalf("the choice did not persist: %+v %v", stored, err)
	}
	restarted := NewTestRunner(2, NewHub(), store)
	if reloaded, ok := restarted.GetTest(chosen.ID); !ok || reloaded.TelemetryBudgetPolicyID != "tight-budgets" {
		t.Fatalf("a reloaded runner lost the choice: %+v", reloaded)
	}
	sourceGroup := 0
	revise := func(name, policy string) (*ManagedTest, error) {
		return runner.CreateTest(CreateTestRequest{Name: name, Duration: "1m", RevisionSourceTestID: chosen.ID, TelemetryBudgetPolicyID: policy,
			Groups: []CreateGroupEntry{{Name: chosen.Groups[0].Name, ScriptID: "budget-chosen-script", RevisionSourceGroupIndex: &sourceGroup, VUsers: 1}}})
	}
	for policy, want := range map[string]string{"": "tight-budgets", runPolicyProjectDefault: "", "other-budgets": "other-budgets"} {
		if revised, err := revise("revised "+policy, policy); err != nil || revised.TelemetryBudgetPolicyID != want {
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
	if initial == nil || initial.TelemetryPolicy == nil {
		t.Fatalf("admission receipt = %+v", initial)
	}
	receipt := initial.TelemetryPolicy
	if receipt.SchemaVersion != telemetrypolicy.VersionProjectPolicy || receipt.PolicyID != "tight-budgets" || receipt.Revision != 1 || receipt.Budgets[0].LimitMs != 1500 || receipt.Budgets[2].Enabled || !validTelemetryBudgetPolicy(receipt) {
		t.Fatalf("the chosen policy did not set the receipt: %+v", receipt)
	}
	var budgetPin *RunPolicyPin
	for index, pin := range initial.PolicyPins {
		if pin.Kind == storage.AnalysisDefinitionKindTelemetryPolicy {
			budgetPin = &initial.PolicyPins[index]
		}
	}
	if budgetPin == nil || budgetPin.ID != "tight-budgets" || budgetPin.Source != runPolicySourceSelected {
		t.Fatalf("budget pin = %+v", initial.PolicyPins)
	}
	if evidence, thresholds := runner.runTelemetryPolicy(runnerTest(t, runner, chosen.ID)); evidence.Policy == nil || evidence.Policy.PolicyID != "tight-budgets" || thresholds.MaxTelemetryGeneratorLagMs == nil || *thresholds.MaxTelemetryGeneratorLagMs != 250 {
		t.Fatalf("live classification uses %+v %+v", evidence, thresholds)
	}

	// Without a choice the run keeps the system receipt, whatever the
	// project's default budget policy says; the default is pinned only.
	plain, err := create("plain", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.StartTest(plain.ID); err != nil {
		t.Fatal(err)
	}
	waitTelemetryBackend(t, backend)
	finishTelemetryBackend(t, backend)
	stored, err = store.GetTest(plain.ID)
	if err != nil {
		t.Fatal(err)
	}
	initial = decodeStoredInitialProvenance(stored.InitialProvenance)
	if initial == nil || initial.TelemetryPolicy == nil || initial.TelemetryPolicy.SchemaVersion != telemetrypolicy.Version || initial.TelemetryPolicy.PolicyID != telemetrypolicy.SystemPolicyID {
		t.Fatalf("an unchosen run's receipt = %+v", initial)
	}
	defaultPinned := false
	for _, pin := range initial.PolicyPins {
		if pin.Kind == storage.AnalysisDefinitionKindTelemetryPolicy {
			if pin.ID != "default-budgets" || pin.Source == runPolicySourceSelected {
				t.Fatalf("an unchosen run's budget pin = %+v", pin)
			}
			defaultPinned = true
		}
	}
	if !defaultPinned {
		t.Fatalf("an unchosen run did not pin the default budget policy: %+v", initial.PolicyPins)
	}
}

func runnerTest(t *testing.T, runner *TestRunner, id string) *ManagedTest {
	t.Helper()
	runner.mu.RLock()
	defer runner.mu.RUnlock()
	test := runner.tests[id]
	if test == nil {
		t.Fatalf("run %s is not loaded", id)
	}
	return test
}

// A chosen budget policy deleted, or changed to a window the live
// classification cannot use, after the run was created refuses Start and
// leaves the run queued; a revised copy that would inherit it is refused,
// naming where the choice came from.
func TestRunChosenTelemetryBudgetPolicyUnusableRefusesStart(t *testing.T) {
	for _, mode := range []string{"deleted", "other window"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv(config.EnvArchiveDir, t.TempDir())
			runner, store, test := newPassiveTransitionTestRunner(t, "budget-gone")
			runner.setExecutionBackend(newTelemetryPolicyBarrierBackend(t))
			project := normalizeProjectID(test.ProjectID)
			seedPolicy(t, store, project, "doomed-budgets", storage.AnalysisDefinitionKindTelemetryPolicy, tightBudgetPolicy)
			chosen, err := runner.CreateTest(CreateTestRequest{Name: "doomed", ScriptID: "budget-gone-script", VUsers: 1, Duration: "1m", TelemetryBudgetPolicyID: "doomed-budgets"})
			if err != nil {
				t.Fatal(err)
			}
			if mode == "deleted" {
				deleteSeededPolicy(t, store, project, "doomed-budgets")
			} else {
				current, err := store.GetAnalysisDefinition(project, "doomed-budgets", 0)
				if err != nil {
					t.Fatal(err)
				}
				changed := *current
				changed.Definition = json.RawMessage(`{"version":1,"sustained_window_seconds":45}`)
				if _, err := store.SaveAnalysisDefinition(changed, current.Revision); err != nil {
					t.Fatal(err)
				}
			}
			if err := runner.StartTest(chosen.ID); !errors.Is(err, errRunPolicyConfiguration) || !strings.Contains(err.Error(), "doomed-budgets") {
				t.Fatalf("Start with an unusable chosen budget policy: %v", err)
			}
			if stored, err := store.GetTest(chosen.ID); err != nil || stored.Status != "QUEUED" || decodeStoredInitialProvenance(stored.InitialProvenance) != nil {
				t.Fatalf("the refused run: %+v %v", stored, err)
			}
			sourceGroup := 0
			_, err = runner.CreateTest(CreateTestRequest{Name: "inherits", Duration: "1m", RevisionSourceTestID: chosen.ID,
				Groups: []CreateGroupEntry{{Name: chosen.Groups[0].Name, ScriptID: "budget-gone-script", RevisionSourceGroupIndex: &sourceGroup, VUsers: 1}}})
			if err == nil || !strings.Contains(err.Error(), "source run's chosen telemetry budget policy") || !strings.Contains(err.Error(), runPolicyProjectDefault) {
				t.Fatalf("a revised copy inheriting an unusable choice: %v", err)
			}
		})
	}
}

// POST /api/v1/tests carries the budget choice: it is echoed, a keyed retry
// spelling it with spaces replays the run, a keyed retry naming another
// policy conflicts, and an unusable policy is answered 409.
func TestCreateTestRouteChosenTelemetryBudgetPolicy(t *testing.T) {
	server, store := testCreateReceiptFixture(t)
	project := normalizeProjectID("")
	seedPolicy(t, store, project, "route-budgets", storage.AnalysisDefinitionKindTelemetryPolicy, tightBudgetPolicy)
	seedPolicy(t, store, project, "route-other", storage.AnalysisDefinitionKindTelemetryPolicy, `{"version":1,"sample_age_ms":900}`)
	seedPolicy(t, store, project, "route-window", storage.AnalysisDefinitionKindTelemetryPolicy, `{"version":1,"sustained_window_seconds":30}`)
	request := receiptRunRequest("Chosen budget run")
	request.TelemetryBudgetPolicyID = "route-budgets"
	first := receiptCreateRequest(t, server, "/api/v1/tests", request, runCreateKey, "")
	if first.Code != http.StatusCreated || decodeCreatedRun(t, first).TelemetryBudgetPolicyID != "route-budgets" {
		t.Fatalf("create: %d %s", first.Code, first.Body.String())
	}
	spaced := request
	spaced.TelemetryBudgetPolicyID = " route-budgets "
	if retry := receiptCreateRequest(t, server, "/api/v1/tests", spaced, runCreateKey, "1"); retry.Code != http.StatusCreated || retry.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("a retry spelling the choice with spaces: %d %s", retry.Code, retry.Body.String())
	}
	other := request
	other.TelemetryBudgetPolicyID = "route-other"
	if changed := receiptCreateRequest(t, server, "/api/v1/tests", other, runCreateKey, ""); changed.Code != http.StatusConflict || idempotencyErrorCode(t, changed) != "idempotency_key_reused" {
		t.Fatalf("a retry naming another policy: %d %s", changed.Code, changed.Body.String())
	}
	window := receiptRunRequest("Window run")
	window.TelemetryBudgetPolicyID = "route-window"
	if refused := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/tests", window, ""); refused.Code != http.StatusConflict || !strings.Contains(refused.Body.String(), "route-window") {
		t.Fatalf("an unusable chosen policy: %d %s", refused.Code, refused.Body.String())
	}
	missing := receiptRunRequest("Missing run")
	missing.TelemetryBudgetPolicyID = "route-missing"
	if refused := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/tests", missing, ""); refused.Code != http.StatusBadRequest || !strings.Contains(refused.Body.String(), "route-missing") {
		t.Fatalf("a missing chosen policy: %d %s", refused.Code, refused.Body.String())
	}
}

// A CI run chooses its budget policy only where the project's CI policy allows
// telemetry policy selection, since a looser budget can loosen a fail_ci
// acceptance policy's breached-time criterion. The choice reaches its run and
// is echoed, is part of the request a retry under the same key must repeat,
// and must exist in the project.
func TestCIRunCarriesTheChosenTelemetryBudgetPolicy(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer target.Close()
	policy := ciRunSourceTestPolicy()
	policy.MaxDurationMinutes = 1
	policy.AllowedTargetHosts = []string{"127.0.0.1"}
	locked, _ := newCIContractTestServer(t, policy)
	policy.AllowTelemetryPolicySelection = true
	server, store := newCIContractTestServer(t, policy)
	seedPolicy(t, store, "project-ci", "ci-budgets", storage.AnalysisDefinitionKindTelemetryPolicy, tightBudgetPolicy)
	seedPolicy(t, store, "project-ci", "ci-other", storage.AnalysisDefinitionKindTelemetryPolicy, `{"version":1,"sample_age_ms":900}`)
	publishCheck := false
	scriptRaw, err := json.Marshal(ciTestScript(target.URL))
	if err != nil {
		t.Fatal(err)
	}
	request := ciRunCreateRequest{
		ProjectID: "project-ci", ScriptPath: "tests/performance.json", Script: scriptRaw,
		Execution: ciRunExecutionRequest{VUsers: 1, Duration: "50ms"}, Source: ciTestRunSource("1"),
		PublishCheck: &publishCheck, TelemetryBudgetPolicyID: "ci-budgets",
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
	if status, _, body := invoke(locked, "octo/apex:performance:9001:1:budget-policy-0000", request); status != http.StatusForbidden || !strings.Contains(body, "telemetry budget policy") || !strings.Contains(body, "allow_telemetry_policy_selection") {
		t.Fatalf("a choice the project's CI policy does not allow: %d %s", status, body)
	}
	byDefault := request
	byDefault.TelemetryBudgetPolicyID = " " + runPolicyProjectDefault + " "
	if status, response, body := invoke(locked, "octo/apex:performance:9001:1:budget-policy-dddd", byDefault); status != http.StatusAccepted || response.TelemetryBudgetPolicyID != "" {
		t.Fatalf("project_default where choosing is not allowed: %d %s", status, body)
	}
	const key = "octo/apex:performance:9001:1:budget-policy-aaaa"
	status, created, body := invoke(server, key, request)
	if status != http.StatusAccepted || created.TestID == "" || created.TelemetryBudgetPolicyID != "ci-budgets" {
		t.Fatalf("CI run: %d %s", status, body)
	}
	if run, ok := server.runner.GetTest(created.TestID); !ok || run.TelemetryBudgetPolicyID != "ci-budgets" {
		t.Fatalf("CI run's chosen budget policy: %+v", run)
	}
	changed := request
	changed.TelemetryBudgetPolicyID = "ci-other"
	if status, _, body := invoke(server, key, changed); status != http.StatusConflict {
		t.Fatalf("a retry naming another policy: %d %s", status, body)
	}
	unknown := request
	unknown.TelemetryBudgetPolicyID = "missing-budgets"
	if status, _, body := invoke(server, "octo/apex:performance:9001:1:budget-policy-bbbb", unknown); status != http.StatusBadRequest || !strings.Contains(body, "missing-budgets") {
		t.Fatalf("an unknown policy: %d %s", status, body)
	}
	project, err := store.GetProject("project-ci")
	if err != nil {
		t.Fatal(err)
	}
	project.Settings.GitHub.Actions.AllowTelemetryPolicySelection = false
	if err := store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	deleteSeededPolicy(t, store, "project-ci", "ci-budgets")
	if status, retried, body := invoke(server, key, request); status != http.StatusAccepted || retried.TestID != created.TestID || retried.TelemetryBudgetPolicyID != "ci-budgets" {
		t.Fatalf("an exact retry after the choice was disallowed and deleted: %d %s", status, body)
	}
}

// A chosen budget policy that cannot be read is answered 503 on both
// run-creating routes.
func TestChosenTelemetryBudgetPolicyStoreOutageIs503(t *testing.T) {
	outage := func(server *Server, store *storage.SQLiteStore) {
		server.runner.mu.Lock()
		server.runner.testStore = unavailablePolicyStore{store}
		server.runner.mu.Unlock()
	}
	direct, directStore := testCreateReceiptFixture(t)
	outage(direct, directStore)
	if _, err := direct.runner.validateRunTelemetryBudgetPolicy("", "any-budgets"); !errors.Is(err, errRunPolicyStoreUnavailable) {
		t.Fatalf("validation during an outage: %v", err)
	}
	request := receiptRunRequest("Outage run")
	request.TelemetryBudgetPolicyID = "any-budgets"
	if recorder := authoringAPIRequest(t, direct, http.MethodPost, "/api/v1/tests", request, ""); recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("POST /api/v1/tests during an outage: %d %s", recorder.Code, recorder.Body.String())
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
		Execution: ciRunExecutionRequest{VUsers: 1, Duration: "50ms"}, Source: ciTestRunSource("1"), PublishCheck: &publishCheck, TelemetryBudgetPolicyID: "any-budgets"})
	if err != nil {
		t.Fatal(err)
	}
	ciRequest := httptest.NewRequest(http.MethodPost, "/api/v1/ci/runs", bytes.NewReader(body))
	ciRequest.Header.Set("Content-Type", "application/json")
	ciRequest.Header.Set("Idempotency-Key", "octo/apex:performance:9001:1:budget-policy-outage")
	recorder := httptest.NewRecorder()
	server.handleCreateCIRun(recorder, ciRequest)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("POST /api/v1/ci/runs during an outage: %d %s", recorder.Code, recorder.Body.String())
	}
}

// A remote controller that does not confirm the chosen budget policy would
// classify the run by its system threshold settings: the remote run is
// deleted, never started, and the create is refused.
func TestRemoteRunNeedsTheChosenBudgetPolicyConfirmed(t *testing.T) {
	t.Setenv(remoteOutboundAllowPrivateHostsEnv, "1")
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "remote-budget.db"))
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
			remoteTestID, forwarded = req.RequestedTestID, req.TelemetryBudgetPolicyID
			test := &ManagedTest{ID: remoteTestID, Status: "QUEUED", ProjectID: "remote-project"}
			if echo {
				test.TelemetryBudgetPolicyID = req.TelemetryBudgetPolicyID
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
	create := func(choice string) *httptest.ResponseRecorder {
		body := marshalLibraryTestJSON(t, RemoteRunCreateRequest{CreateTestRequest: CreateTestRequest{Name: "Remote", Duration: "30s", ProjectID: "project-a", TelemetryBudgetPolicyID: choice}})
		response := httptest.NewRecorder()
		server.mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/remotes/workspace/runs", bytes.NewReader(body)))
		return response
	}
	if response := create("remote-budgets"); response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "remote-budgets") || forwarded != "remote-budgets" || created != 1 || deleted != 1 {
		t.Fatalf("an unconfirmed remote choice: %d %s (forwarded %q, created %d, deleted %d)", response.Code, response.Body.String(), forwarded, created, deleted)
	}
	if response := create(runPolicyProjectDefault); response.Code != http.StatusCreated || created != 2 || deleted != 1 {
		t.Fatalf("project_default needs no confirmation: %d %s (created %d, deleted %d)", response.Code, response.Body.String(), created, deleted)
	}
	echo = true
	if response := create("remote-budgets"); response.Code != http.StatusCreated || created != 3 || deleted != 1 {
		t.Fatalf("a confirmed remote choice: %d %s (created %d, deleted %d)", response.Code, response.Body.String(), created, deleted)
	}
}

func TestRunPolicyChoiceWithoutPolicyStoreNamesThePolicies(t *testing.T) {
	runner := &TestRunner{}
	if _, err := runner.validateRunTelemetryBudgetPolicy("project", "budgets"); err == nil || err.Error() != "telemetry budget policies are unavailable" {
		t.Fatalf("budget choice without a policy store: %v", err)
	}
	if _, err := runner.validateRunTelemetryAcceptancePolicy("project", "gate"); err == nil || err.Error() != "telemetry acceptance policies are unavailable" {
		t.Fatalf("acceptance choice without a policy store: %v", err)
	}
}

// The GitHub Check says a run chose its live telemetry budgets.
func TestCIGitHubCheckNamesChosenTelemetryBudgets(t *testing.T) {
	// The queued check, built before any run response, says so too.
	for _, response := range []ciRunResponse{{Status: "QUEUED", Outcome: "queued"}, {Status: "COMPLETE", Outcome: "passed", TelemetryBudgetPolicyID: "tight-budgets"}} {
		summary := ciGitHubCheckSummary(nil, response, ciRunMetadata{TelemetryBudgetPolicyID: "tight-budgets"})
		if !strings.Contains(summary, "Telemetry budgets: chosen by the run (project policy tight-budgets), not the system threshold settings.") {
			t.Fatalf("summary for %s = %s", response.Status, summary)
		}
	}
	if summary := ciGitHubCheckSummary(nil, ciRunResponse{Status: "COMPLETE", Outcome: "passed"}, ciRunMetadata{}); strings.Contains(summary, "Telemetry budgets") {
		t.Fatalf("an unchosen run named budgets: %s", summary)
	}
}
