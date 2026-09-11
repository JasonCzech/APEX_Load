package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestTerminalSLAStatusPrefersArchivedTransactionRuleEvidence(t *testing.T) {
	server := NewServer(ServerConfig{})
	testID := "terminal-sla-archive"
	server.runner.mu.Lock()
	server.runner.tests[testID] = &ManagedTest{
		ID: testID, Status: "FAILED",
		GoalConfig:  &GoalConfig{TransactionRules: []TransactionSLARule{{Transaction: "Checkout", Metric: "error_count", Comparator: "lte", Threshold: 0, Severity: "fail"}}},
		LiveMetrics: &LiveMetrics{GoalStatus: &GoalStatus{}},
	}
	server.runner.mu.Unlock()

	root := t.TempDir()
	manifestPath := filepath.Join(root, runArchiveManifestFileName)
	manifest := RunArchiveManifest{
		SchemaVersion: archiveSchemaVersion,
		TestID:        testID, Status: "FAILED", SLAEvaluated: true, SLABreached: true,
		TransactionRuleResults:            []TransactionSLARuleResult{{Status: "fail", MatchedCount: 2, Message: "archive result"}},
		TransactionResponseTimeSLAResults: []TransactionResponseTimeSLAResult{{Group: "buyers", Transaction: "Checkout", ThresholdMs: 500, EligibleSuccessCount: 2, Status: "pass"}},
	}
	content, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	server.runner.mu.Lock()
	server.runner.archives[testID] = &storage.RunArchiveRecord{TestID: testID, ArchivePath: root, ManifestPath: manifestPath}
	server.runner.mu.Unlock()

	request := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testID+"/sla-status", nil)
	request.SetPathValue("id", testID)
	response := httptest.NewRecorder()
	server.handleGetTestSLAStatus(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	var report SLABreachReport
	if err := json.Unmarshal(response.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if !report.SLABreached || len(report.TransactionRuleResults) != 1 || report.TransactionRuleResults[0].Message != "archive result" || len(report.TransactionResponseTimeSLAResults) != 1 || report.TransactionResponseTimeSLAResults[0].Group != "buyers" {
		t.Fatalf("report = %#v", report)
	}
}

func TestLiveSLAStatusIncludesProvisionalTransactionResponseRows(t *testing.T) {
	server := NewServer(ServerConfig{})
	testID := "live-response-time-sla"
	threshold := 500.0
	server.runner.mu.Lock()
	server.runner.tests[testID] = &ManagedTest{
		ID: testID, Status: "RUNNING",
		GoalConfig: &GoalConfig{TransactionResponseTimeSLAMs: &threshold},
		LiveMetrics: &LiveMetrics{TransactionResponseTimeSLA: []TransactionResponseTimeSLAResult{{
			Group: "buyers", Transaction: "Checkout", ThresholdMs: threshold,
			EligibleSuccessCount: 20, MissedCount: 1, MetPct: 95, Status: "pass",
		}}},
	}
	server.runner.mu.Unlock()

	request := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testID+"/sla-status", nil)
	request.SetPathValue("id", testID)
	response := httptest.NewRecorder()
	server.handleGetTestSLAStatus(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	var report SLABreachReport
	if err := json.Unmarshal(response.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.SLAEvaluated || len(report.TransactionResponseTimeSLAResults) != 1 || report.TransactionResponseTimeSLAResults[0].Transaction != "Checkout" {
		t.Fatalf("report = %#v", report)
	}
}

func TestTerminalTransactionSLAStatusWaitsForArchiveEvidence(t *testing.T) {
	server := NewServer(ServerConfig{})
	testID := "terminal-sla-pending"
	server.runner.mu.Lock()
	server.runner.tests[testID] = &ManagedTest{
		ID: testID, Status: "COMPLETE",
		GoalConfig: &GoalConfig{TransactionRules: []TransactionSLARule{{Transaction: "Checkout", Metric: "minimum_count", Comparator: "gte", Threshold: 1, Severity: "fail"}}},
	}
	server.runner.mu.Unlock()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testID+"/sla-status", nil)
	request.SetPathValue("id", testID)
	response := httptest.NewRecorder()
	server.handleGetTestSLAStatus(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
}

func TestTerminalResponseTimeSLAStatusWaitsForArchiveEvidence(t *testing.T) {
	server := NewServer(ServerConfig{})
	testID := "terminal-response-time-sla-pending"
	threshold := 500.0
	server.runner.mu.Lock()
	server.runner.tests[testID] = &ManagedTest{
		ID: testID, Status: "COMPLETE",
		Groups: []TestGroup{{Name: "buyers", TransactionResponseTimeSLAMs: &threshold}},
	}
	server.runner.mu.Unlock()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testID+"/sla-status", nil)
	request.SetPathValue("id", testID)
	response := httptest.NewRecorder()
	server.handleGetTestSLAStatus(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
}
