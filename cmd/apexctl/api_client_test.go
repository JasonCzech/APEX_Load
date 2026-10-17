package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/runcontract"
)

func TestDoStatusReadContextStopsDuringRetryAfterDelay(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "5")
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	client := NewApiClient(server.URL)
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	resp, err := client.doStatusReadContext(ctx, "/retry")
	if resp != nil {
		_ = resp.Body.Close()
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("retry error = %v, want context deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("retry ignored context deadline: elapsed %s", elapsed)
	}
}

func TestWaitForTestDeadlineCancelsInFlightStatusRead(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()

	client := NewApiClient(server.URL)
	client.Client.Timeout = 5 * time.Second
	started := time.Now()
	_, err := client.WaitForTest("blocked", "json", 100*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timed out after") {
		t.Fatalf("wait error = %v, want timeout", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("wait ignored wall-clock deadline: elapsed %s", elapsed)
	}
}

func TestWaitForTestDeadlineIncludesSLARead(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/tests/t1", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"t1","status":"COMPLETE"}`))
	})
	mux.HandleFunc("/api/v1/tests/t1/sla-status", func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := NewApiClient(server.URL)
	client.Client.Timeout = 5 * time.Second
	started := time.Now()
	result, err := client.WaitForTest("t1", "json", 100*time.Millisecond)
	if result == nil || err == nil || !strings.Contains(err.Error(), "timed out after") {
		t.Fatalf("result=%#v error=%v, want partial result plus timeout", result, err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("SLA read ignored wall-clock deadline: elapsed %s", elapsed)
	}
}

func TestRunPreparedTestRetainsTestIdentityOnPollTimeout(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/scripts", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"script-1"}`))
	})
	mux.HandleFunc("/api/v1/tests", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"test-1"}`))
	})
	mux.HandleFunc("/api/v1/tests/test-1/start", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := NewApiClient(server.URL)
	client.PollTimeout = 10 * time.Millisecond
	// Enter the polling timeout deterministically after preparation has created
	// the durable test. A real 10 ms command-wide deadline can expire during the
	// three preparation requests under full-suite CPU pressure, which tests a
	// different contract and cannot possibly return a test identity.
	client.pollWait = func(context.Context, time.Duration) error {
		return context.DeadlineExceeded
	}
	result, err := client.runPreparedTestContext(t.Context(), "script.json", []byte(`{"name":"test"}`), 1, "1s", "0s", "json", nil)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("poll error = %v, want timeout", err)
	}
	if result == nil || result.TestID != "test-1" || result.Status != "" || result.Duration <= 0 {
		t.Fatalf("partial run evidence = %#v, want durable test identity and elapsed time", result)
	}
}

func TestRunPreparedTestTimeoutCoversPreparationRequests(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()

	client := NewApiClient(server.URL)
	client.Client.Timeout = 5 * time.Second
	client.PollTimeout = 100 * time.Millisecond
	started := time.Now()
	result, err := client.RunPreparedTest("script.json", []byte(`{"name":"test"}`), 1, "1s", "0s", "json", nil)
	// A canceled HTTP/1 client may return before the test server observes EOF.
	// Release the intentionally blocked handler before closing the server.
	close(release)
	if result != nil || err == nil || !strings.Contains(err.Error(), "timed out after 100ms during upload script") {
		t.Fatalf("preparation timeout result/error = %#v / %v", result, err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("preparation request ignored command timeout: %s", elapsed)
	}
}

func TestRunPreparedTestRetainsTestIdentityWhenStartFails(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/scripts", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"script-1"}`))
	})
	mux.HandleFunc("/api/v1/tests", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"test-1"}`))
	})
	mux.HandleFunc("/api/v1/tests/test-1/start", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not leader", http.StatusServiceUnavailable)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := NewApiClient(server.URL)
	result, err := client.RunPreparedTest("script.json", []byte(`{"name":"test"}`), 1, "1s", "0s", "json", nil)
	if result == nil || result.TestID != "test-1" || err == nil || !strings.Contains(err.Error(), "start test") {
		t.Fatalf("start-failure evidence/error = %#v / %v", result, err)
	}
}

func TestWaitForTestRecoversAfterExhaustedStatusReadCycle(t *testing.T) {
	var statusCalls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/tests/t1", func(w http.ResponseWriter, _ *http.Request) {
		if statusCalls.Add(1) <= 4 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"t1","status":"COMPLETE"}`))
	})
	mux.HandleFunc("/api/v1/tests/t1/sla-status", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"test_id":"t1","status":"COMPLETE","sla_breached":false}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := NewApiClient(server.URL)
	// Zero exercises the default-duration bootstrap read, which must tolerate
	// the same transient cycle as subsequent status polls.
	result, err := client.WaitForTest("t1", "json", 0)
	if err != nil {
		t.Fatalf("WaitForTest did not recover from one exhausted retry cycle: %v", err)
	}
	if result == nil || statusCalls.Load() != 5 {
		t.Fatalf("result/calls = %#v/%d, want terminal result after 5 status calls", result, statusCalls.Load())
	}
}

func TestApiClientRetriesTransientStatusesAndFailsFastOnPermanent4xx(t *testing.T) {
	var transientCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/transient":
			call := transientCalls.Add(1)
			if call < 3 {
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			w.WriteHeader(http.StatusOK)
		case "/permanent":
			w.WriteHeader(http.StatusBadRequest)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewApiClient(server.URL)
	response, err := client.doStatusRead("/transient")
	if err != nil {
		t.Fatalf("transient request: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK || transientCalls.Load() != 3 {
		t.Fatalf("transient status/calls = %d/%d, want 200/3", response.StatusCode, transientCalls.Load())
	}
	response, err = client.doStatusRead("/permanent")
	if err != nil {
		t.Fatalf("permanent request: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("permanent status = %d, want 400", response.StatusCode)
	}
}

// TestPollTestFailsFastOn404 locks in that pollTest surfaces a 404 (test deleted)
// instead of looping forever with an empty status.
func TestPollTestFailsFastOn404(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := NewApiClient(server.URL)
	if _, err := client.pollTest("test-x", "json", false); err == nil {
		t.Fatal("expected error when test status returns 404")
	} else if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v, want not found", err)
	}
}

func TestRunPollingFailsFastOnPermanentClientStatus(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "invalid API key", http.StatusUnauthorized)
	}))
	defer server.Close()

	client := NewApiClient(server.URL)
	client.PollTimeout = 5 * time.Second
	_, err := client.pollTest("test-x", "json", false)
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("poll error = %v, want permanent 401", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("permanent run-poll status calls = %d, want 1", calls.Load())
	}
}

func TestWaitFailsFastOnPermanentClientStatus(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer server.Close()

	client := NewApiClient(server.URL)
	started := time.Now()
	_, err := client.WaitForTest("test-x", "json", 5*time.Second)
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("wait error = %v, want permanent 403", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("permanent wait status calls = %d, want 1", calls.Load())
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("permanent wait status was retried for %s", elapsed)
	}
}

func TestApiClientRefusesCrossOriginRedirectWithoutReplayingCredentialsOrBody(t *testing.T) {
	var targetHit bool
	var gotAPIKey, gotAuthorization, gotBody string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHit = true
		gotAPIKey = r.Header.Get("X-APEXLoad-API-Key")
		gotAuthorization = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"stolen","token_type":"Bearer"}`))
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer source.Close()

	client := NewApiClientWithAuth(source.URL, "key-123", "token-456")
	_, err := client.ExchangeGitHubOIDC(t.Context(), CIExchangeRequest{
		IDToken:   "github-id-token",
		ProjectID: "project-1",
	})
	if err == nil || !strings.Contains(err.Error(), "cross-origin redirect") {
		t.Fatalf("ExchangeGitHubOIDC error = %v, want cross-origin redirect refusal", err)
	}
	if targetHit || gotAPIKey != "" || gotAuthorization != "" || gotBody != "" {
		t.Fatalf("cross-origin target received request data: hit=%t api-key=%q authorization=%q body=%q",
			targetHit, gotAPIKey, gotAuthorization, gotBody)
	}
}

func TestApiClientAllowsSameOriginRedirect(t *testing.T) {
	var redirected bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/tests/test-1":
			http.Redirect(w, r, "/redirected", http.StatusTemporaryRedirect)
		case "/redirected":
			redirected = true
			if got := r.Header.Get("X-APEXLoad-API-Key"); got != "key-123" {
				t.Fatalf("same-origin API key = %q, want key-123", got)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer token-456" {
				t.Fatalf("same-origin authorization = %q, want bearer token", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"test-1","status":"COMPLETE"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewApiClientWithAuth(server.URL, "key-123", "token-456")
	if _, err := client.GetTestStatus("test-1"); err != nil {
		t.Fatalf("GetTestStatus: %v", err)
	}
	if !redirected {
		t.Fatal("same-origin redirect was not followed")
	}
}

func TestApiClientRefusesHTTPSDowngradeRedirect(t *testing.T) {
	targetHit := false
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { targetHit = true }))
	defer target.Close()
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer source.Close()

	client := NewApiClientWithAuth(source.URL, "key-123", "token-456")
	client.Client = source.Client()
	client.Client.Timeout = 2 * time.Second
	if _, err := client.GetTestStatus("test-1"); err == nil || !strings.Contains(err.Error(), "HTTPS-to-HTTP") {
		t.Fatalf("downgrade error = %v", err)
	}
	if targetHit {
		t.Fatal("downgrade redirect reached the plaintext target")
	}
}

func TestResolveTestResultFailsWhenSLAStatusUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	client := NewApiClient(server.URL)
	result, err := client.resolveTestResult("test-1", &runcontract.TestStatus{Status: "COMPLETE"}, time.Second)
	if result == nil || err == nil || !strings.Contains(err.Error(), "fetch SLA status") {
		t.Fatalf("result=%#v error=%v, want explicit SLA retrieval failure", result, err)
	}
	if result.Status != "COMPLETE" {
		t.Fatalf("partial result status = %q, want COMPLETE", result.Status)
	}
}

func TestCreateTestIncludesGoalConfig(t *testing.T) {
	var captured runcontract.CreateTestRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/tests" {
			t.Fatalf("path = %q, want /api/v1/tests", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"test-1"}`))
	}))
	defer server.Close()

	client := NewApiClient(server.URL)
	p95LatencyMs := 800.0
	maxErrorPct := 2.5
	testID, err := client.createTest("script-1", 25, "5m", "30s", &runcontract.GoalConfig{
		P95LatencyMs: &p95LatencyMs,
		MaxErrorPct:  &maxErrorPct,
	})
	if err != nil {
		t.Fatalf("createTest returned error: %v", err)
	}
	if testID != "test-1" {
		t.Fatalf("testID = %q, want test-1", testID)
	}
	if captured.ScenarioMode != "goal_oriented" {
		t.Fatalf("scenario_mode = %q, want goal_oriented", captured.ScenarioMode)
	}
	if captured.GoalConfig == nil || captured.GoalConfig.P95LatencyMs == nil || *captured.GoalConfig.P95LatencyMs != 800 {
		t.Fatalf("goal_config = %#v, want p95 latency 800", captured.GoalConfig)
	}
	if captured.GoalConfig.MaxErrorPct == nil || *captured.GoalConfig.MaxErrorPct != 2.5 {
		t.Fatalf("goal_config = %#v, want max error pct 2.5", captured.GoalConfig)
	}
}

func TestCreateTestRejectsMalformedOrIdentitylessResponses(t *testing.T) {
	for _, body := range []string{`not-json`, `{}`} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()

			client := NewApiClient(server.URL)
			if _, err := client.createTest("script-1", 1, "1m", "0s", nil); err == nil {
				t.Fatalf("createTest accepted response %q", body)
			}
		})
	}
}

func TestApiClientInjectsExplicitAuthHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-APEXLoad-API-Key"); got != "key-123" {
			t.Fatalf("api key header = %q, want key-123", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token-456" {
			t.Fatalf("authorization header = %q, want bearer token", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"test-1","status":"COMPLETE"}`))
	}))
	defer server.Close()

	client := NewApiClientWithAuth(server.URL, "key-123", "token-456")
	if _, err := client.GetTestStatus("test-1"); err != nil {
		t.Fatalf("GetTestStatus returned error: %v", err)
	}
}

func TestApiClientReadsAuthHeadersFromEnvironment(t *testing.T) {
	t.Setenv("APEXLOAD_API_KEY", "env-key")
	t.Setenv("APEXLOAD_BEARER_TOKEN", "env-token")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-APEXLoad-API-Key"); got != "env-key" {
			t.Fatalf("api key header = %q, want env-key", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer env-token" {
			t.Fatalf("authorization header = %q, want bearer env-token", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"test-1","status":"COMPLETE"}`))
	}))
	defer server.Close()

	client := NewApiClient(server.URL)
	if _, err := client.GetTestStatus("test-1"); err != nil {
		t.Fatalf("GetTestStatus returned error: %v", err)
	}
}
