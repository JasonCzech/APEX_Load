package main

import (
	"encoding/json"
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

func TestCITelemetryOutcomesFailTheGateOnlyWhenEnforced(t *testing.T) {
	verdict := &CITelemetryVerdict{Outcome: "fail", Mode: "fail_ci", Enforced: true, Reasons: []string{"wall-time coverage 70.0% is below the 90.0% floor"}}
	response := &CIRunResponse{SchemaVersion: 1, TestID: "run", Status: "COMPLETE", Outcome: ciOutcomeTelemetryFailed, Error: "Telemetry acceptance failed", TelemetryVerdict: verdict}
	if err := validateCIRunAPIResponse(response, "run"); err != nil {
		t.Fatal(err)
	}
	if !isTerminalCIResponse(response) {
		t.Fatal("telemetry_failed is not terminal")
	}
	result := ciResultFromResponse(response)
	classifyCIResult(&result)
	if result.Outcome != ciOutcomeTelemetryFailed || ciResultExitCode(result) != exitCodeTestFailure {
		t.Fatalf("telemetry_failed result = %+v, exit %d", result, ciResultExitCode(result))
	}
	verdict.Reasons[0] = "mutated"
	if result.TelemetryVerdict == nil || result.TelemetryVerdict.Reasons[0] == "mutated" {
		t.Fatalf("result verdict aliases the response: %+v", result.TelemetryVerdict)
	}
	if code := ciResultExitCode(CIResult{Outcome: ciOutcomeTelemetryInconclusive, Status: "COMPLETE"}); code != exitCodeUsageOrConfiguration {
		t.Fatalf("an unmapped inconclusive verdict exits %d", code)
	}
	// An advisory verdict never changes the exit code.
	advisory := CIResult{Outcome: ciOutcomePassed, Status: "COMPLETE", TelemetryVerdict: &CITelemetryVerdict{Outcome: "fail", Mode: "advisory"}}
	if code := ciResultExitCode(advisory); code != exitCodeSuccess {
		t.Fatalf("advisory telemetry failure exits %d", code)
	}
	// A telemetry outcome without the enforced verdict that decided it is
	// inconsistent.
	response.TelemetryVerdict = &CITelemetryVerdict{Outcome: "fail", Mode: "advisory"}
	if err := validateCIRunAPIResponse(response, "run"); err == nil {
		t.Fatal("an unenforced verdict cannot decide the outcome")
	}
	response.TelemetryVerdict, response.Status = verdict, "FAILED"
	if err := validateCIRunAPIResponse(response, "run"); err == nil {
		t.Fatal("a failed run cannot be telemetry_failed")
	}
}

func TestCISummaryAndOutputsShowTheTelemetryVerdict(t *testing.T) {
	dir := t.TempDir()
	result := CIResult{Outcome: ciOutcomePassed, Status: "COMPLETE", TelemetryVerdict: &CITelemetryVerdict{Outcome: "inconclusive", Mode: "fail_ci", InconclusivePolicy: "pass", Reasons: []string{"stage end_to_end has no measured distribution"}}}
	summaryPath := filepath.Join(dir, "summary.md")
	if err := writeCISummary(summaryPath, result); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(summaryPath)
	if err != nil {
		t.Fatal(err)
	}
	if text := string(data); !strings.Contains(text, "| Telemetry verdict | inconclusive |") || !strings.Contains(text, "inconclusive maps to pass") || !strings.Contains(text, "stage end_to_end has no measured distribution") {
		t.Fatalf("summary omits the telemetry verdict:\n%s", text)
	}
	outputPath := filepath.Join(dir, "output")
	if err := writeCIOutputs(outputPath, ciArgs{jsonPath: "a.json", junitPath: "a.xml"}, result); err != nil {
		t.Fatal(err)
	}
	if data, err = os.ReadFile(outputPath); err != nil || !strings.Contains(string(data), "telemetry-verdict<<") || !strings.Contains(string(data), "\ninconclusive\n") {
		t.Fatalf("outputs omit the telemetry verdict: %s %v", data, err)
	}
}

func TestParseCIArgsTelemetryInconclusive(t *testing.T) {
	base := []string{"--controller-url", "https://apexload.example", "--project-id", "p", "--script-path", "s.json"}
	args, err := parseCIArgs(append(base, "--telemetry-inconclusive", " Fail "))
	if err != nil || args.telemetryInconclusive != "fail" {
		t.Fatalf("parse = %q %v", args.telemetryInconclusive, err)
	}
	if _, err := parseCIArgs(append(base, "--telemetry-inconclusive", "maybe")); err == nil || !strings.Contains(err.Error(), "pass or fail") {
		t.Fatalf("invalid mapping accepted: %v", err)
	}
	if _, err := parseCIArgs([]string{"--execution-mode", "local", "--publish-check=false", "--script-path", "s.json", "--telemetry-inconclusive", "pass"}); err == nil || !strings.Contains(err.Error(), "controller mode") {
		t.Fatalf("local mode accepted a telemetry mapping: %v", err)
	}
	// An unset mapping is omitted, so existing callers keep their derived
	// idempotency keys.
	encoded, _ := json.Marshal(CIRunRequest{ProjectID: "p"})
	if strings.Contains(string(encoded), "telemetry_inconclusive") {
		t.Fatalf("an unset mapping was sent: %s", encoded)
	}
}

// --telemetry-policy names the project telemetry acceptance policy a
// controller CI run is judged by; it needs controller mode and is omitted when
// unset, so existing callers keep their derived idempotency keys (TEL-B).
func TestParseCIArgsTelemetryPolicy(t *testing.T) {
	base := []string{"--controller-url", "https://apexload.example", "--project-id", "p", "--script-path", "s.json"}
	args, err := parseCIArgs(append(base, "--telemetry-policy", " strict-gate "))
	if err != nil || args.telemetryPolicy != "strict-gate" {
		t.Fatalf("parse = %q %v", args.telemetryPolicy, err)
	}
	// project_default is no choice: omitting the flag selects the default.
	if args, err := parseCIArgs(append(base, "--telemetry-policy", " project_default ")); err != nil || args.telemetryPolicy != "" {
		t.Fatalf("project_default = %q %v", args.telemetryPolicy, err)
	}
	if _, err := parseCIArgs([]string{"--execution-mode", "local", "--publish-check=false", "--script-path", "s.json", "--telemetry-policy", "strict-gate"}); err == nil || !strings.Contains(err.Error(), "controller mode") {
		t.Fatalf("local mode accepted a telemetry policy: %v", err)
	}
	if encoded, _ := json.Marshal(CIRunRequest{ProjectID: "p"}); strings.Contains(string(encoded), "telemetry_acceptance_policy_id") {
		t.Fatalf("an unset policy was sent: %s", encoded)
	}
	if encoded, _ := json.Marshal(CIRunRequest{ProjectID: "p", TelemetryAcceptancePolicyID: "strict-gate"}); !strings.Contains(string(encoded), `"telemetry_acceptance_policy_id":"strict-gate"`) {
		t.Fatalf("the chosen policy was not sent: %s", encoded)
	}
}
