package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

// A CI run's create judges its telemetry policy choices again after the
// handler's pre-check. A policy read that fails in between stays retryable
// (503), and a policy changed in between to one that cannot set the run's
// budgets stays a conflict (409), as the pre-check would answer, never 400.
func TestCIRunCreateKeepsPolicyStatusesAfterThePreCheck(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer target.Close()
	policy := ciRunSourceTestPolicy()
	policy.MaxDurationMinutes = 1
	policy.AllowedTargetHosts = []string{"127.0.0.1"}
	policy.AllowTelemetryPolicySelection = true
	server, store := newCIContractTestServer(t, policy)
	seedPolicy(t, store, "project-ci", "ci-budgets", storage.AnalysisDefinitionKindTelemetryPolicy, tightBudgetPolicy)
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
	invoke := func(key string) (int, string) {
		t.Helper()
		body, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/ci/runs", bytes.NewReader(body))
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Idempotency-Key", key)
		recorder := httptest.NewRecorder()
		server.handleCreateCIRun(recorder, httpReq)
		return recorder.Code, recorder.Body.String()
	}

	// The policies become unreadable after the pre-check.
	server.beforeCIRunMaterialize = func(CreateTestRequest) {
		server.runner.mu.Lock()
		server.runner.testStore = unavailablePolicyStore{store}
		server.runner.mu.Unlock()
	}
	if status, body := invoke("octo/apex:performance:9001:1:status-outage"); status != http.StatusServiceUnavailable {
		t.Fatalf("an outage after the pre-check: %d %s", status, body)
	}
	server.runner.mu.Lock()
	server.runner.testStore = store
	server.runner.mu.Unlock()

	// The policy is revised to another sustained window after the pre-check.
	server.beforeCIRunMaterialize = func(CreateTestRequest) {
		current, err := store.GetAnalysisDefinition("project-ci", "ci-budgets", 0)
		if err != nil || current == nil {
			t.Errorf("policy: %+v %v", current, err)
			return
		}
		current.Definition = json.RawMessage(`{"version":1,"sustained_window_seconds":45}`)
		if _, err := store.SaveAnalysisDefinition(*current, current.Revision); err != nil {
			t.Error(err)
		}
	}
	if status, body := invoke("octo/apex:performance:9001:1:status-window"); status != http.StatusConflict || !strings.Contains(body, "sustained window") {
		t.Fatalf("a policy revised after the pre-check: %d %s", status, body)
	}
}

// Launch preflight judges a run's telemetry policy choices as its create
// does, and answers as POST /api/v1/tests would: 400 for a policy the
// project lacks, 409 for one that is unusable, 503 when policies cannot be
// read.
func TestLaunchPreflightAnswersTelemetryPolicyFailuresLikeCreate(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()
	storeBudgetScenarioScript(t, server, "preflight-policy-script")
	seedPolicy(t, store, storage.DefaultProjectID, "preflight-window", storage.AnalysisDefinitionKindTelemetryPolicy, `{"version":1,"sustained_window_seconds":30}`)
	seedPolicy(t, store, storage.DefaultProjectID, "preflight-broken", storage.AnalysisDefinitionKindTelemetryAcceptancePolicy, `{"version":1}`)
	preflight := func(fields map[string]any) (int, string) {
		t.Helper()
		body := map[string]any{"name": "Preflight", "script_id": "preflight-policy-script", "vusers": 1, "duration": "1m"}
		for key, value := range fields {
			body[key] = value
		}
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		recorder := httptest.NewRecorder()
		server.handlePreflightTest(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/tests/preflight", bytes.NewReader(raw)))
		return recorder.Code, recorder.Body.String()
	}
	if status, body := preflight(map[string]any{"telemetry_budget_policy_id": "missing-budgets"}); status != http.StatusBadRequest || !strings.Contains(body, "missing-budgets") {
		t.Fatalf("a missing budget policy: %d %s", status, body)
	}
	if status, body := preflight(map[string]any{"telemetry_budget_policy_id": "preflight-window"}); status != http.StatusConflict || !strings.Contains(body, "sustained window") {
		t.Fatalf("a budget policy with another window: %d %s", status, body)
	}
	if status, body := preflight(map[string]any{"telemetry_acceptance_policy_id": "preflight-broken"}); status != http.StatusConflict || !strings.Contains(body, "preflight-broken") {
		t.Fatalf("an invalid acceptance policy: %d %s", status, body)
	}
	if status, body := preflight(nil); status != http.StatusOK {
		t.Fatalf("a preflight without choices: %d %s", status, body)
	}
	server.runner.mu.Lock()
	server.runner.testStore = unavailablePolicyStore{store}
	server.runner.mu.Unlock()
	for _, field := range []string{"telemetry_budget_policy_id", "telemetry_acceptance_policy_id"} {
		if status, body := preflight(map[string]any{field: "any-policy"}); status != http.StatusServiceUnavailable {
			t.Fatalf("%s during an outage: %d %s", field, status, body)
		}
	}
}
