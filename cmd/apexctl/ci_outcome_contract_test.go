package main

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/runcontract"
)

// TestCIExitCodeMatchesSharedOutcomeContract keeps apexctl's exit codes and
// the shared contract the controller's agent brief reads in step.
func TestCIExitCodeMatchesSharedOutcomeContract(t *testing.T) {
	for _, outcome := range append(append([]string(nil), runcontract.CIOutcomes...), "unknown", "") {
		if got, want := ciExitCode(outcome), runcontract.CIOutcomeExitCode(outcome); got != want {
			t.Errorf("ciExitCode(%q) = %d, shared contract says %d", outcome, got, want)
		}
		for _, status := range []string{"COMPLETE", "FAILED", "ABORTED", "NO_RESULTS", "CANCELLED", "TIMED_OUT", "STATUS_FAILED", ""} {
			legacy := ciExitCode(outcome)
			if (outcome == ciOutcomeExecutionError || outcome == ciOutcomeCancelled) && isCITestVerdictFailureStatus(status) {
				legacy = exitCodeTestFailure
			}
			if got := ciResultExitCode(CIResult{Outcome: outcome, Status: status}); got != legacy || got != runcontract.CIResultExitCode(outcome, status) {
				t.Errorf("ciResultExitCode(%q, %q) = %d, want %d", outcome, status, got, legacy)
			}
		}
	}
}
