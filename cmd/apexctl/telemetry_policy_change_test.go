package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// telemetryPolicyChangeController answers the segments list with a latest
// sequence of 2 and records the change request.
func telemetryPolicyChangeController(t *testing.T, status int) (*httptest.Server, *map[string]any, *atomic.Int32) {
	t.Helper()
	var posted map[string]any
	var lists atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/tests/run-1/telemetry-policy-segments" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			lists.Add(1)
			_, _ = w.Write([]byte(`{"segments":[{"sequence":1,"source":"admission"},{"sequence":2,"source":"run_selection"}]}`))
		case http.MethodPost:
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &posted)
			if status >= 400 {
				http.Error(w, `{"error":"the run's telemetry policy cannot change now"}`, status)
				return
			}
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"sequence":3,"source":"system_settings","policy":{"hash":"abc"}}`))
		}
	}))
	t.Cleanup(server.Close)
	return server, &posted, &lists
}

// TEL-B: apexctl analysis telemetry-policy-change sends the change with the
// run's latest segment (read when not given) and a request ID (generated when
// not given), and prints the new segment.
func TestTelemetryPolicyChangeSendsTheChange(t *testing.T) {
	server, posted, lists := telemetryPolicyChangeController(t, http.StatusCreated)
	var out bytes.Buffer
	if err := executeTelemetryPolicyChangeCommand(context.Background(), []string{"--api", server.URL, "--id", "run-1", "--policy", "project_default", "--interval", "5"}, &out); err != nil {
		t.Fatal(err)
	}
	request := *posted
	if request["telemetry_budget_policy_id"] != "project_default" || request["expected_sequence"] != float64(2) || request["controller_publication_interval_seconds"] != float64(5) || strings.TrimSpace(request["request_id"].(string)) == "" || lists.Load() != 1 {
		t.Fatalf("request %+v (lists %d)", request, lists.Load())
	}
	if !strings.Contains(out.String(), `"sequence": 3`) {
		t.Fatalf("output %s", out.String())
	}
	// An explicit expected sequence and request ID are sent as given, with no read.
	server, posted, lists = telemetryPolicyChangeController(t, http.StatusOK)
	if err := executeTelemetryPolicyChangeCommand(context.Background(), []string{"--api", server.URL, "--id", "run-1", "--policy", "tight", "--expected-sequence", "7", "--request-id", "retry-1"}, &out); err != nil {
		t.Fatal(err)
	}
	if request := *posted; request["expected_sequence"] != float64(7) || request["request_id"] != "retry-1" || request["telemetry_budget_policy_id"] != "tight" || lists.Load() != 0 {
		t.Fatalf("request %+v (lists %d)", request, lists.Load())
	}
}

// A refused change fails with the controller's answer; missing or invalid
// flags fail before any request.
func TestTelemetryPolicyChangeRefusalsAndFlags(t *testing.T) {
	server, _, _ := telemetryPolicyChangeController(t, http.StatusConflict)
	if err := executeTelemetryPolicyChangeCommand(context.Background(), []string{"--api", server.URL, "--id", "run-1", "--policy", "tight", "--expected-sequence", "2"}, io.Discard); err == nil || !strings.Contains(err.Error(), "409") {
		t.Fatalf("a refused change: %v", err)
	}
	for _, args := range [][]string{
		{"--api", server.URL, "--policy", "tight"},
		{"--api", server.URL, "--id", "run-1"},
		{"--api", server.URL, "--id", "run-1", "--policy", "tight", "--interval", "-1"},
		{"--api", server.URL, "--id", "run-1", "--policy", "tight", "extra"},
	} {
		if err := executeTelemetryPolicyChangeCommand(context.Background(), args, io.Discard); err == nil {
			t.Fatalf("args %v were accepted", args)
		}
	}
}

// telemetry-reevaluate --segment selects one segment per named run.
func TestTelemetryReevaluateSelectsSegments(t *testing.T) {
	var created map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &created)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"id":"job-1","state":"queued"}`))
	}))
	t.Cleanup(server.Close)
	base := []string{"--api", server.URL, "--project", "proj", "--runs", "run-a,run-b", "--policy", "common", "--revision", "2"}
	if err := executeTelemetryReevaluateCommand(context.Background(), append(base, "--segment", "run-a=2"), io.Discard); err != nil {
		t.Fatal(err)
	}
	request, _ := created["request"].(map[string]any)
	if segments, _ := request["segments"].(map[string]any); len(segments) != 1 || segments["run-a"] != float64(2) {
		t.Fatalf("request %+v", request)
	}
	for _, extra := range [][]string{{"--segment", "run-c=1"}, {"--segment", "run-a=0"}, {"--segment", "run-a"}, {"--segment", "run-a=1", "--segment", "run-a=2"}} {
		if err := executeTelemetryReevaluateCommand(context.Background(), append(append([]string(nil), base...), extra...), io.Discard); err == nil {
			t.Fatalf("%v was accepted", extra)
		}
	}
}
