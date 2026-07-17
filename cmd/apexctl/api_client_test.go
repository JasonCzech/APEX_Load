package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/api"
)

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
	result, err := client.resolveTestResult("test-1", &api.ManagedTest{Status: "COMPLETE"}, time.Second)
	if result == nil || err == nil || !strings.Contains(err.Error(), "fetch SLA status") {
		t.Fatalf("result=%#v error=%v, want explicit SLA retrieval failure", result, err)
	}
}

func TestCreateTestIncludesGoalConfig(t *testing.T) {
	var captured api.CreateTestRequest
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
	testID, err := client.createTest("script-1", 25, "5m", "30s", &api.GoalConfig{
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
