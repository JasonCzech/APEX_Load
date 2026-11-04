package runcontract

import "strings"

// CI run outcomes: the terminal outcome a CI run reports and apexctl gates on.
const (
	CIOutcomePassed                = "passed"
	CIOutcomeSLABreached           = "sla_breached"
	CIOutcomeExecutionError        = "execution_error"
	CIOutcomeConfigurationError    = "configuration_error"
	CIOutcomeTimedOut              = "timed_out"
	CIOutcomeCancelled             = "cancelled"
	CIOutcomeInconclusive          = "inconclusive"
	CIOutcomeMonitoringIncomplete  = "monitoring_incomplete"
	CIOutcomeRunInvalid            = "run_invalid"
	CIOutcomeTelemetryFailed       = "telemetry_failed"
	CIOutcomeTelemetryInconclusive = "telemetry_inconclusive"
)

// CIOutcomes lists every terminal CI outcome.
var CIOutcomes = []string{
	CIOutcomePassed, CIOutcomeSLABreached, CIOutcomeExecutionError, CIOutcomeConfigurationError,
	CIOutcomeTimedOut, CIOutcomeCancelled, CIOutcomeInconclusive, CIOutcomeMonitoringIncomplete,
	CIOutcomeRunInvalid, CIOutcomeTelemetryFailed, CIOutcomeTelemetryInconclusive,
}

// IsCIOutcome says outcome is one of the terminal CI outcomes.
func IsCIOutcome(outcome string) bool {
	outcome = strings.ToLower(strings.TrimSpace(outcome))
	for _, known := range CIOutcomes {
		if outcome == known {
			return true
		}
	}
	return false
}

// CIOutcomeExitCode is the process exit code `apexctl ci run` returns for an
// outcome: 0 passed, 1 a test verdict failure, 2 a usage or configuration
// problem the caller must fix, 3 everything else. The controller's agent
// brief uses it to say whether an outcome blocks, so the brief and the CI gate
// cannot disagree.
func CIOutcomeExitCode(outcome string) int {
	switch strings.ToLower(strings.TrimSpace(outcome)) {
	case CIOutcomePassed:
		return 0
	case CIOutcomeSLABreached, CIOutcomeRunInvalid, CIOutcomeTelemetryFailed:
		return 1
	case CIOutcomeTelemetryInconclusive, CIOutcomeConfigurationError:
		return 2
	default:
		return 3
	}
}

// CIResultExitCode is the exit code for an outcome and the run status beside
// it: a run that executed and then failed or was aborted (execution_error or
// cancelled with status FAILED, ABORTED or NO_RESULTS) is a test failure, 1,
// rather than a controller or transport problem.
func CIResultExitCode(outcome, status string) int {
	outcome = strings.ToLower(strings.TrimSpace(outcome))
	if outcome == CIOutcomeExecutionError || outcome == CIOutcomeCancelled {
		switch strings.ToUpper(strings.TrimSpace(status)) {
		case "FAILED", "ABORTED", "NO_RESULTS":
			return 1
		}
	}
	return CIOutcomeExitCode(outcome)
}
