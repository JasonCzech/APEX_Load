package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/runcontract"
)

// telemetryPolicyController records the create request and answers with the
// given echo; it counts Start and DELETE calls on the created run.
func telemetryPolicyController(t *testing.T, echo string) (*httptest.Server, *runcontract.CreateTestRequest, *atomic.Int32, *atomic.Int32) {
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
		if echo != "" {
			response["telemetry_acceptance_policy_id"] = echo
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

// TEL-B: apexctl run --telemetry-policy sends the choice and accepts the
// created run only when the controller echoes it.
func TestRunTelemetryPolicyIsSentAndConfirmed(t *testing.T) {
	server, captured, _, deletes := telemetryPolicyController(t, "strict-gate")
	client := NewApiClient(server.URL)
	client.TelemetryPolicy = "strict-gate"
	testID, err := client.createTest("script-1", 1, "1m", "0s", nil)
	if err != nil || testID != "test-1" {
		t.Fatalf("createTest = %q, %v", testID, err)
	}
	if captured.TelemetryAcceptancePolicyID != "strict-gate" {
		t.Fatalf("request telemetry_acceptance_policy_id = %q", captured.TelemetryAcceptancePolicyID)
	}
	if deletes.Load() != 0 {
		t.Fatal("a confirmed run was deleted")
	}
}

// A controller that does not echo the choice (one that predates policy
// selection, or names another) would judge the run by the project's default:
// the queued run is deleted before Start and the command is a configuration
// error (exit 2).
func TestRunTelemetryPolicyUnconfirmedDeletesTheQueuedRun(t *testing.T) {
	for _, echo := range []string{"", "other-gate"} {
		t.Run("echo="+echo, func(t *testing.T) {
			server, _, starts, deletes := telemetryPolicyController(t, echo)
			client := NewApiClient(server.URL)
			client.TelemetryPolicy = "strict-gate"
			result, err := client.runPreparedTestContext(t.Context(), "script.json", []byte(`{"name":"test"}`), 1, "1s", "0s", "json", nil)
			var configurationErr *cliConfigurationError
			if !errors.As(err, &configurationErr) || !strings.Contains(err.Error(), `did not confirm --telemetry-policy "strict-gate"`) {
				t.Fatalf("error = %v, want an unconfirmed-policy configuration error", err)
			}
			if result != nil || starts.Load() != 0 || deletes.Load() != 1 {
				t.Fatalf("result %#v, starts %d, deletes %d: the unconfirmed run must be deleted, never started", result, starts.Load(), deletes.Load())
			}
			if code := testRunExitCode(true, "", false, err); code != exitCodeUsageOrConfiguration {
				t.Fatalf("exit code = %d, want %d", code, exitCodeUsageOrConfiguration)
			}
		})
	}
}

// A controller that echoes a different policy is named in the refusal.
func TestRunTelemetryPolicyEchoingAnotherPolicyIsNamed(t *testing.T) {
	server, _, _, _ := telemetryPolicyController(t, "other-gate")
	client := NewApiClient(server.URL)
	client.TelemetryPolicy = "strict-gate"
	if _, err := client.createTest("script-1", 1, "1m", "0s", nil); err == nil || !strings.Contains(err.Error(), `reported the run's policy as "other-gate"`) {
		t.Fatalf("error = %v", err)
	}
}

// When the unconfirmed run cannot be deleted it is still queued on the
// controller: the command names it and exits as a controller failure (3).
func TestRunTelemetryPolicyUndeletedRunIsAControllerFailure(t *testing.T) {
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
	client.TelemetryPolicy = "strict-gate"
	_, err := client.createTest("script-1", 1, "1m", "0s", nil)
	if err == nil || !strings.Contains(err.Error(), "unconfirmed queued run test-1 could not be deleted") {
		t.Fatalf("error = %v", err)
	}
	if code := testRunExitCode(true, "", false, err); code != exitCodeControllerOrTransport {
		t.Fatalf("exit code = %d, want %d", code, exitCodeControllerOrTransport)
	}
}

// A delete answered with anything but success (404 for a run the controller
// will not delete now, 500 after a partial cleanup) deleted the run only when
// the run can no longer be read; one still readable is still queued: exit 3.
func TestRunTelemetryPolicyDeleteNotFoundIsCheckedByRead(t *testing.T) {
	for _, stillReadable := range []bool{false, true} {
		for _, deleteStatus := range []int{http.StatusNotFound, http.StatusInternalServerError} {
			mux := http.NewServeMux()
			mux.HandleFunc("POST /api/v1/tests", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"id":"test-1","status":"QUEUED"}`))
			})
			mux.HandleFunc("DELETE /api/v1/tests/test-1", func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "test not found or currently active", deleteStatus)
			})
			mux.HandleFunc("GET /api/v1/tests/test-1", func(w http.ResponseWriter, _ *http.Request) {
				if !stillReadable {
					http.Error(w, "not found", http.StatusNotFound)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"test-1","status":"QUEUED"}`))
			})
			server := httptest.NewServer(mux)
			client := NewApiClient(server.URL)
			client.TelemetryPolicy = "strict-gate"
			_, err := client.createTest("script-1", 1, "1m", "0s", nil)
			server.Close()
			want := exitCodeUsageOrConfiguration
			if stillReadable {
				want = exitCodeControllerOrTransport
			}
			if code := testRunExitCode(true, "", false, err); code != want {
				t.Fatalf("delete %d, still readable %t: exit %d, want %d (%v)", deleteStatus, stillReadable, code, want, err)
			}
		}
	}
}

// A controller that refuses the chosen policy (missing, over-long or invalid,
// in the controller's own wording) refuses the command's input: exit 2, as
// apexctl ci run reports it.
func TestRunTelemetryPolicyRefusedByTheControllerIsAConfigurationError(t *testing.T) {
	for _, refusal := range []struct {
		status int
		body   string
	}{
		{http.StatusBadRequest, `{"error":"telemetry acceptance policy \"strict-gate\" was not found in the project"}`},
		{http.StatusBadRequest, `{"error":"telemetry_acceptance_policy_id exceeds 128 bytes"}`},
		{http.StatusConflict, `{"error":"run policy configuration prevents admission: telemetry_acceptance_policy policy \"Strict gate\" is invalid: mode must be advisory or fail_ci"}`},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, refusal.body, refusal.status)
		}))
		client := NewApiClient(server.URL)
		client.TelemetryPolicy = "strict-gate"
		_, err := client.createTest("script-1", 1, "1m", "0s", nil)
		server.Close()
		if code := testRunExitCode(true, "", false, err); code != exitCodeUsageOrConfiguration {
			t.Fatalf("%d %s: exit code = %d (%v)", refusal.status, refusal.body, code, err)
		}
	}
	// A refusal for another reason keeps its usual classification.
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"invalid duration"}`, http.StatusBadRequest)
	}))
	defer other.Close()
	otherClient := NewApiClient(other.URL)
	otherClient.TelemetryPolicy = "strict-gate"
	if _, err := otherClient.createTest("script-1", 1, "1m", "0s", nil); testRunExitCode(true, "", false, err) != exitCodeControllerOrTransport {
		t.Fatalf("an unrelated refusal with a choice: %v", err)
	}
	// Without a choice, a refused create stays a controller failure.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "refused", http.StatusConflict)
	}))
	defer server.Close()
	if _, err := NewApiClient(server.URL).createTest("script-1", 1, "1m", "0s", nil); testRunExitCode(true, "", false, err) != exitCodeControllerOrTransport {
		t.Fatalf("an unchosen refused create: %v", err)
	}
}

// A command deadline that passes while the unconfirmed run is being deleted
// does not hide the outcome: an undeleted run is still named.
func TestRunTelemetryPolicyTimeoutKeepsTheUndeletedRun(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/scripts", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"script-1"}`))
	})
	mux.HandleFunc("POST /api/v1/tests", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"test-1","status":"QUEUED"}`))
	})
	mux.HandleFunc("DELETE /api/v1/tests/test-1", func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusInternalServerError)
	})
	mux.HandleFunc("GET /api/v1/tests/test-1", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"test-1","status":"QUEUED"}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	client := NewApiClient(server.URL)
	client.TelemetryPolicy = "strict-gate"
	client.PollTimeout = 100 * time.Millisecond
	_, err := client.RunPreparedTest("script.json", []byte(`{"name":"test"}`), 1, "1s", "0s", "json", nil)
	if err == nil || !strings.Contains(err.Error(), "timed out") || !strings.Contains(err.Error(), "unconfirmed queued run test-1 could not be deleted") {
		t.Fatalf("error = %v", err)
	}
	if code := testRunExitCode(true, "", false, err); code != exitCodeControllerOrTransport {
		t.Fatalf("exit code = %d", code)
	}
}

// Without a choice nothing is sent and no echo is required.
func TestRunWithoutTelemetryPolicySendsNone(t *testing.T) {
	server, captured, _, deletes := telemetryPolicyController(t, "")
	client := NewApiClient(server.URL)
	if _, err := client.createTest("script-1", 1, "1m", "0s", nil); err != nil {
		t.Fatal(err)
	}
	if captured.TelemetryAcceptancePolicyID != "" || deletes.Load() != 0 {
		t.Fatalf("request %+v, deletes %d", captured, deletes.Load())
	}
}

func TestParseRunArgsTelemetryPolicy(t *testing.T) {
	t.Setenv("APEXLOAD_API", "")
	if got := parseRunArgs([]string{"--script", "s.json", "--telemetry-policy", "strict-gate"}); got.err == nil || !strings.Contains(got.err.Error(), "requires --api") {
		t.Fatalf("a local run with --telemetry-policy: %v", got.err)
	}
	if got := parseRunArgs([]string{"--script", "s.json", "--api", "http://127.0.0.1:8080", "--telemetry-policy", " strict-gate "}); got.err != nil || got.telemetryPolicy != "strict-gate" {
		t.Fatalf("api run: %+v", got)
	}
	// project_default is what omitting the flag selects, locally or not.
	if got := parseRunArgs([]string{"--script", "s.json", "--telemetry-policy", "project_default"}); got.err != nil || got.telemetryPolicy != "" {
		t.Fatalf("project_default: %+v", got)
	}
}
