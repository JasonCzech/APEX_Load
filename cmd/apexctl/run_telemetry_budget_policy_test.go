package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jasonczech/apexload/pkg/runcontract"
)

// telemetryBudgetPolicyController records the create request and echoes the
// given policy choices; it counts Start and DELETE calls on the created run.
func telemetryBudgetPolicyController(t *testing.T, echo map[string]string) (*httptest.Server, *runcontract.CreateTestRequest, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	var captured runcontract.CreateTestRequest
	var starts, deletes atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/scripts", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"script-1"}`))
	})
	mux.HandleFunc("POST /api/v1/tests", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Errorf("decode request: %v", err)
		}
		response := map[string]any{"id": "test-1", "status": "QUEUED"}
		for field, value := range echo {
			response[field] = value
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(response)
	})
	mux.HandleFunc("POST /api/v1/tests/test-1/start", func(w http.ResponseWriter, _ *http.Request) {
		starts.Add(1)
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("DELETE /api/v1/tests/test-1", func(w http.ResponseWriter, _ *http.Request) {
		deletes.Add(1)
		w.WriteHeader(http.StatusNoContent)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server, &captured, &starts, &deletes
}

// TEL-B: apexctl run --telemetry-budget-policy sends the choice, beside an
// acceptance choice, and accepts the created run only when the controller
// echoes both.
func TestRunTelemetryBudgetPolicyIsSentAndConfirmed(t *testing.T) {
	server, captured, _, deletes := telemetryBudgetPolicyController(t, map[string]string{"telemetry_budget_policy_id": "tight-budgets", "telemetry_acceptance_policy_id": "strict-gate"})
	client := NewApiClient(server.URL)
	client.TelemetryPolicy = "strict-gate"
	client.TelemetryBudgetPolicy = "tight-budgets"
	testID, err := client.createTest("script-1", 1, "1m", "0s", nil)
	if err != nil || testID != "test-1" {
		t.Fatalf("createTest = %q, %v", testID, err)
	}
	if captured.TelemetryBudgetPolicyID != "tight-budgets" || captured.TelemetryAcceptancePolicyID != "strict-gate" {
		t.Fatalf("request %+v", captured)
	}
	if deletes.Load() != 0 {
		t.Fatal("a confirmed run was deleted")
	}
}

// A controller that does not echo the budget choice (one that predates it,
// or names another) would classify the run by its system threshold settings:
// the queued run is deleted before Start and the command exits 2. An echoed
// acceptance choice does not stand in for the budget echo.
func TestRunTelemetryBudgetPolicyUnconfirmedDeletesTheQueuedRun(t *testing.T) {
	for _, echo := range []map[string]string{
		{},
		{"telemetry_budget_policy_id": "other-budgets"},
		{"telemetry_acceptance_policy_id": "tight-budgets"},
	} {
		server, _, starts, deletes := telemetryBudgetPolicyController(t, echo)
		client := NewApiClient(server.URL)
		client.TelemetryBudgetPolicy = "tight-budgets"
		result, err := client.runPreparedTestContext(t.Context(), "script.json", []byte(`{"name":"test"}`), 1, "1s", "0s", "json", nil)
		var configurationErr *cliConfigurationError
		if !errors.As(err, &configurationErr) || !strings.Contains(err.Error(), `did not confirm --telemetry-budget-policy "tight-budgets"`) {
			t.Fatalf("echo %v: error = %v, want an unconfirmed-budget configuration error", echo, err)
		}
		if result != nil || starts.Load() != 0 || deletes.Load() != 1 {
			t.Fatalf("echo %v: result %#v, starts %d, deletes %d", echo, result, starts.Load(), deletes.Load())
		}
		if code := testRunExitCode(true, "", false, err); code != exitCodeUsageOrConfiguration {
			t.Fatalf("echo %v: exit code = %d, want %d", echo, code, exitCodeUsageOrConfiguration)
		}
	}
}

// A controller that echoes a different budget policy is named in the refusal.
func TestRunTelemetryBudgetPolicyEchoingAnotherPolicyIsNamed(t *testing.T) {
	server, _, _, _ := telemetryBudgetPolicyController(t, map[string]string{"telemetry_budget_policy_id": "other-budgets"})
	client := NewApiClient(server.URL)
	client.TelemetryBudgetPolicy = "tight-budgets"
	if _, err := client.createTest("script-1", 1, "1m", "0s", nil); err == nil || !strings.Contains(err.Error(), `reported the run's budget policy as "other-budgets"`) {
		t.Fatalf("error = %v", err)
	}
}

// An unconfirmed budget choice whose run cannot be deleted names the run and
// exits as a controller failure (3).
func TestRunTelemetryBudgetPolicyUndeletedRunIsAControllerFailure(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/tests", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"test-1","status":"QUEUED"}`))
	})
	mux.HandleFunc("DELETE /api/v1/tests/test-1", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	mux.HandleFunc("GET /api/v1/tests/test-1", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"test-1","status":"QUEUED"}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	client := NewApiClient(server.URL)
	client.TelemetryBudgetPolicy = "tight-budgets"
	_, err := client.createTest("script-1", 1, "1m", "0s", nil)
	if err == nil || !strings.Contains(err.Error(), "unconfirmed queued run test-1 could not be deleted") || !strings.Contains(err.Error(), "--telemetry-budget-policy") {
		t.Fatalf("error = %v", err)
	}
	if code := testRunExitCode(true, "", false, err); code != exitCodeControllerOrTransport {
		t.Fatalf("exit code = %d, want %d", code, exitCodeControllerOrTransport)
	}
}

// A controller that refuses the chosen budget policy (missing, over-long,
// invalid or with another sustained window, in the controller's own wording)
// refuses the command's input: exit 2. A refusal naming the acceptance policy
// when only a budget policy was chosen, or one for another reason, stays 3.
func TestRunTelemetryBudgetPolicyRefusedByTheControllerIsAConfigurationError(t *testing.T) {
	refuse := func(status int, body string, configure func(*ApiClient)) int {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, body, status)
		}))
		defer server.Close()
		client := NewApiClient(server.URL)
		configure(client)
		_, err := client.createTest("script-1", 1, "1m", "0s", nil)
		return testRunExitCode(true, "", false, err)
	}
	budget := func(client *ApiClient) { client.TelemetryBudgetPolicy = "tight-budgets" }
	for _, refusal := range []struct {
		status int
		body   string
	}{
		{http.StatusBadRequest, `{"error":"telemetry budget policy \"tight-budgets\" was not found in the project"}`},
		{http.StatusBadRequest, `{"error":"telemetry_budget_policy_id exceeds 128 bytes"}`},
		{http.StatusConflict, `{"error":"run policy configuration prevents admission: telemetry_policy policy \"Tight\" is invalid: sample_age_ms must be positive"}`},
		{http.StatusConflict, `{"error":"run policy configuration prevents admission: telemetry budget policy \"Tight\" sets a 60 s sustained window; a run's live classification uses 15 s"}`},
	} {
		if code := refuse(refusal.status, refusal.body, budget); code != exitCodeUsageOrConfiguration {
			t.Fatalf("%d %s: exit code = %d", refusal.status, refusal.body, code)
		}
	}
	if code := refuse(http.StatusBadRequest, `{"error":"telemetry acceptance policy \"x\" was not found in the project"}`, budget); code != exitCodeControllerOrTransport {
		t.Fatalf("an acceptance refusal with only a budget choice: exit %d", code)
	}
	if code := refuse(http.StatusBadRequest, `{"error":"invalid duration"}`, budget); code != exitCodeControllerOrTransport {
		t.Fatalf("an unrelated refusal with a budget choice: exit %d", code)
	}
	// With both choices, a refusal of either is the command's input.
	both := func(client *ApiClient) {
		client.TelemetryPolicy, client.TelemetryBudgetPolicy = "strict-gate", "tight-budgets"
	}
	if code := refuse(http.StatusBadRequest, `{"error":"telemetry acceptance policy \"strict-gate\" was not found in the project"}`, both); code != exitCodeUsageOrConfiguration {
		t.Fatalf("an acceptance refusal with both choices: exit %d", code)
	}
}

// Without a budget choice nothing is sent and no budget echo is required.
func TestRunWithoutTelemetryBudgetPolicySendsNone(t *testing.T) {
	server, captured, _, deletes := telemetryBudgetPolicyController(t, map[string]string{"telemetry_budget_policy_id": "server-side"})
	if _, err := NewApiClient(server.URL).createTest("script-1", 1, "1m", "0s", nil); err != nil {
		t.Fatal(err)
	}
	if captured.TelemetryBudgetPolicyID != "" || deletes.Load() != 0 {
		t.Fatalf("request %+v, deletes %d", captured, deletes.Load())
	}
}

func TestParseRunArgsTelemetryBudgetPolicy(t *testing.T) {
	t.Setenv("APEXLOAD_API", "")
	if got := parseRunArgs([]string{"--script", "s.json", "--telemetry-budget-policy", "tight-budgets"}); got.err == nil || !strings.Contains(got.err.Error(), "--telemetry-budget-policy requires --api") {
		t.Fatalf("a local run with --telemetry-budget-policy: %v", got.err)
	}
	if got := parseRunArgs([]string{"--script", "s.json", "--api", "http://127.0.0.1:8080", "--telemetry-budget-policy", " tight-budgets ", "--telemetry-policy", "strict-gate"}); got.err != nil || got.telemetryBudgetPolicy != "tight-budgets" || got.telemetryPolicy != "strict-gate" {
		t.Fatalf("api run: %+v", got)
	}
	// project_default is the system threshold settings, which omitting the
	// flag selects, locally or not.
	if got := parseRunArgs([]string{"--script", "s.json", "--telemetry-budget-policy", "project_default"}); got.err != nil || got.telemetryBudgetPolicy != "" {
		t.Fatalf("project_default: %+v", got)
	}
}
