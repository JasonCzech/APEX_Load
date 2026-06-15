package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
