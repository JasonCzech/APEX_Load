package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAppDynamicsCommandRoutesAndValidation(t *testing.T) {
	var calls []string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.RequestURI())
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"operation","status":"complete"}`))
	}))
	defer api.Close()
	for _, tc := range []struct {
		args  []string
		route string
	}{
		{[]string{"list"}, "GET /api/v1/projects/p/monitoring/appdynamics-connections"},
		{[]string{"delete", "--id", "c", "--revision", "3"}, "DELETE /api/v1/projects/p/monitoring/appdynamics-connections/c?revision=3"},
		{[]string{"discover", "--id", "c", "--kind", "metrics", "--application-id", "42", "--metric-path", "Business Transactions|Checkout"}, "GET /api/v1/projects/p/monitoring/appdynamics-connections/c/metrics?application_id=42&metric_path=Business+Transactions%7CCheckout"},
		{[]string{"test", "--id", "c", "--wait"}, "POST /api/v1/projects/p/monitoring/appdynamics-connections/c/test"},
	} {
		var output bytes.Buffer
		args := append(tc.args, "--api", api.URL, "--project", "p")
		if err := executeAppDynamicsCommand(context.Background(), args, &output); err != nil {
			t.Fatal(err)
		}
		if calls[len(calls)-1] != tc.route {
			t.Fatalf("route %q want %q", calls[len(calls)-1], tc.route)
		}
	}
	before := len(calls)
	for _, args := range [][]string{{"delete", "--id", "c"}, {"discover", "--id", "c", "--kind", "tiers"}, {"test", "--id", "c", "--wait", "--timeout", "2m"}} {
		err := executeAppDynamicsCommand(context.Background(), append(args, "--api", api.URL, "--project", "p"), &bytes.Buffer{})
		if err == nil {
			t.Fatal("invalid input accepted")
		}
	}
	if len(calls) != before {
		t.Fatal("invalid command performed API action")
	}
	if err := executeAppDynamicsCommand(context.Background(), []string{"unsupported", "--project", "p"}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatal("unsupported action accepted")
	}
}

func TestAppDynamicsCIWaitsForMonitoringAndRejectsInconclusivePass(t *testing.T) {
	pending := &CIRunResponse{SchemaVersion: 1, TestID: "run", Status: "COMPLETE", Outcome: "monitoring_pending"}
	if err := validateCIRunAPIResponse(pending, "run"); err != nil {
		t.Fatal(err)
	}
	if isTerminalCIResponse(pending) {
		t.Fatal("monitoring drain treated as terminal acceptance")
	}
	incomplete := &CIRunResponse{SchemaVersion: 1, TestID: "run", Status: "COMPLETE", Outcome: ciOutcomeInconclusive}
	if err := validateCIRunAPIResponse(incomplete, "run"); err != nil {
		t.Fatal(err)
	}
	if !isTerminalCIResponse(incomplete) {
		t.Fatal("inconclusive not terminal")
	}
	result := ciResultFromResponse(incomplete)
	classifyCIResult(&result)
	if result.Outcome != ciOutcomeInconclusive || ciExitCode(result.Outcome) == 0 {
		t.Fatal("inconclusive result became pass")
	}
}

func TestAppDynamicsCIIncompleteMonitoringOutcomeCannotPass(t *testing.T) {
	response := &CIRunResponse{SchemaVersion: 1, TestID: "run", Status: "COMPLETE", Outcome: ciOutcomeMonitoringIncomplete, MonitoringStatus: "incomplete", Error: "Required metric buckets unavailable"}
	if err := validateCIRunAPIResponse(response, "run"); err != nil {
		t.Fatal(err)
	}
	if !isTerminalCIResponse(response) {
		t.Fatal("incomplete monitoring remained pending")
	}
	result := ciResultFromResponse(response)
	classifyCIResult(&result)
	if result.Outcome != ciOutcomeMonitoringIncomplete || ciExitCode(result.Outcome) == 0 || result.MonitoringStatus != "incomplete" {
		t.Fatalf("incomplete result became passing %+v", result)
	}
}
