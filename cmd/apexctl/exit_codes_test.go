package main

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTestRunExitCodeContract(t *testing.T) {
	runErr := errors.New("run failed")
	tests := []struct {
		name             string
		controllerBacked bool
		terminalStatus   string
		breached         bool
		runErr           error
		want             int
	}{
		{name: "passed", want: exitCodeSuccess},
		{name: "SLA breach", breached: true, want: exitCodeTestFailure},
		{name: "local execution failure", runErr: runErr, want: exitCodeTestFailure},
		{name: "completed remote test failure", controllerBacked: true, terminalStatus: "FAILED", runErr: runErr, want: exitCodeTestFailure},
		{name: "aborted remote test", controllerBacked: true, terminalStatus: "ABORTED", runErr: runErr, want: exitCodeTestFailure},
		{name: "complete test with unavailable SLA", controllerBacked: true, terminalStatus: "COMPLETE", runErr: runErr, want: exitCodeControllerOrTransport},
		{name: "partial remote evidence", controllerBacked: true, runErr: runErr, want: exitCodeControllerOrTransport},
		{name: "remote transport failure", controllerBacked: true, runErr: runErr, want: exitCodeControllerOrTransport},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := testRunExitCode(test.controllerBacked, test.terminalStatus, test.breached, test.runErr); got != test.want {
				t.Fatalf("testRunExitCode() = %d, want %d", got, test.want)
			}
		})
	}
}

func TestTestRunOutcomeContract(t *testing.T) {
	runErr := errors.New("run failed")
	tests := []struct {
		name             string
		controllerBacked bool
		terminalStatus   string
		breached         bool
		runErr           error
		want             string
	}{
		{name: "passed", want: "success"},
		{name: "SLA breach", breached: true, want: "failed"},
		{name: "local execution failure", runErr: runErr, want: "failed"},
		{name: "remote failed verdict", controllerBacked: true, terminalStatus: "FAILED", runErr: runErr, want: "failed"},
		{name: "remote observation error", controllerBacked: true, terminalStatus: "COMPLETE", runErr: runErr, want: "error"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := testRunOutcome(test.controllerBacked, test.terminalStatus, test.breached, test.runErr); got != test.want {
				t.Fatalf("testRunOutcome() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestImportCommandExitCodeContract(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "local input", err: cliInputErrorf("bad path"), want: exitCodeUsageOrConfiguration},
		{name: "review required", err: &brunoCLIReviewRequiredError{}, want: exitCodeUsageOrConfiguration},
		{name: "controller validation", err: &brunoCLIHTTPError{statusCode: http.StatusUnprocessableEntity}, want: exitCodeUsageOrConfiguration},
		{name: "rate limited", err: &brunoCLIHTTPError{statusCode: http.StatusTooManyRequests}, want: exitCodeControllerOrTransport},
		{name: "server error", err: &brunoCLIHTTPError{statusCode: http.StatusServiceUnavailable}, want: exitCodeControllerOrTransport},
		{name: "transport", err: errors.New("connection refused"), want: exitCodeControllerOrTransport},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := importCommandExitCode(test.err); got != test.want {
				t.Fatalf("importCommandExitCode(%v) = %d, want %d", test.err, got, test.want)
			}
		})
	}
}

func TestCIExitCodeContract(t *testing.T) {
	tests := []struct {
		name   string
		result CIResult
		want   int
	}{
		{name: "passed", result: CIResult{Outcome: ciOutcomePassed, Status: "COMPLETE"}, want: exitCodeSuccess},
		{name: "normalized passed", result: CIResult{Outcome: " PASSED ", Status: "COMPLETE"}, want: exitCodeSuccess},
		{name: "SLA breach", result: CIResult{Outcome: ciOutcomeSLABreached, Status: "COMPLETE"}, want: exitCodeTestFailure},
		{name: "configuration", result: CIResult{Outcome: ciOutcomeConfigurationError, Status: "INVALID"}, want: exitCodeUsageOrConfiguration},
		{name: "completed execution failure", result: CIResult{Outcome: ciOutcomeExecutionError, Status: "FAILED"}, want: exitCodeTestFailure},
		{name: "completed aborted run", result: CIResult{Outcome: ciOutcomeCancelled, Status: "ABORTED"}, want: exitCodeTestFailure},
		{name: "complete run with observation error", result: CIResult{Outcome: ciOutcomeExecutionError, Status: "COMPLETE"}, want: exitCodeControllerOrTransport},
		{name: "controller failure", result: CIResult{Outcome: ciOutcomeExecutionError, Status: "STATUS_FAILED"}, want: exitCodeControllerOrTransport},
		{name: "timeout", result: CIResult{Outcome: ciOutcomeTimedOut, Status: "TIMED_OUT"}, want: exitCodeControllerOrTransport},
		{name: "cancelled", result: CIResult{Outcome: ciOutcomeCancelled, Status: "CANCELLED"}, want: exitCodeControllerOrTransport},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ciResultExitCode(test.result); got != test.want {
				t.Fatalf("ciResultExitCode(%#v) = %d, want %d", test.result, got, test.want)
			}
		})
	}
}

func TestCIRunInvalidOutcomeFailsTheGate(t *testing.T) {
	response := &CIRunResponse{
		SchemaVersion: 1, TestID: "run", Status: "COMPLETE", Outcome: ciOutcomeRunInvalid, Error: "Run validity is invalid",
		Validity: &CIRunValidity{Verdict: "invalid", Reasons: []string{"dropped_results"}, Gate: "invalid", Enforced: true},
	}
	if err := validateCIRunAPIResponse(response, "run"); err != nil {
		t.Fatal(err)
	}
	if !isTerminalCIResponse(response) {
		t.Fatal("run_invalid is not terminal")
	}
	result := ciResultFromResponse(response)
	classifyCIResult(&result)
	if result.Outcome != ciOutcomeRunInvalid || ciResultExitCode(result) != exitCodeTestFailure {
		t.Fatalf("run_invalid result = %+v, exit %d", result, ciResultExitCode(result))
	}
	response.Validity.Reasons[0] = "mutated"
	if result.Validity == nil || !result.Validity.Enforced || result.Validity.Reasons[0] != "dropped_results" {
		t.Fatalf("result validity = %+v", result.Validity)
	}
	response.Status = "FAILED"
	if err := validateCIRunAPIResponse(response, "run"); err == nil {
		t.Fatal("a failed run cannot be run_invalid")
	}
}

func TestCISummaryShowsRunValidity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "summary.md")
	result := CIResult{Outcome: ciOutcomeRunInvalid, Status: "COMPLETE", Validity: &CIRunValidity{Verdict: "invalid", Gate: "suspect", Enforced: true}}
	if err := writeCISummary(path, result); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if text := string(data); !strings.Contains(text, "| Run validity | invalid |") || !strings.Contains(text, "| Validity gate | suspect |") || !strings.Contains(text, "❌ run_invalid") {
		t.Fatalf("summary omits validity:\n%s", text)
	}
}
