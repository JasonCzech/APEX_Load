package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

type cliInputError struct{ cause error }

func (e *cliInputError) Error() string {
	if e == nil || e.cause == nil {
		return "invalid CLI input"
	}
	return e.cause.Error()
}

func (e *cliInputError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func cliInputErrorf(format string, values ...any) error {
	return &cliInputError{cause: fmt.Errorf(format, values...)}
}

// cliConfigurationError is a run the controller accepted but could not run as
// asked (such as an unconfirmed telemetry policy choice): exit 2, not a
// controller or transport failure.
type cliConfigurationError struct{ cause error }

func (e *cliConfigurationError) Error() string { return e.cause.Error() }

func (e *cliConfigurationError) Unwrap() error { return e.cause }

func cliConfigurationErrorf(format string, values ...any) error {
	return &cliConfigurationError{cause: fmt.Errorf(format, values...)}
}

// apexctl's public exit-code contract lets automation distinguish a failed
// load-test verdict from bad inputs and from a controller/transport failure.
const (
	exitCodeSuccess = iota
	exitCodeTestFailure
	exitCodeUsageOrConfiguration
	exitCodeControllerOrTransport
)

// testRunExitCode classifies run/wait outcomes. Receiving partial metrics does
// not turn an SLA-read timeout into a test verdict: only a failed terminal
// status or an evaluated SLA breach is exit 1. Other remote observation errors
// are controller/transport failures. Local engine errors remain test failures
// because no controller or transport is involved.
func testRunExitCode(controllerBacked bool, terminalStatus string, breached bool, runErr error) int {
	if breached || isFailedTestStatus(terminalStatus) {
		return exitCodeTestFailure
	}
	if runErr == nil {
		return exitCodeSuccess
	}
	var configurationErr *cliConfigurationError
	if errors.As(runErr, &configurationErr) {
		return exitCodeUsageOrConfiguration
	}
	if controllerBacked {
		return exitCodeControllerOrTransport
	}
	return exitCodeTestFailure
}

func testRunOutcome(controllerBacked bool, terminalStatus string, breached bool, runErr error) string {
	if breached || isFailedTestStatus(terminalStatus) || (!controllerBacked && runErr != nil) {
		return "failed"
	}
	if runErr != nil {
		return "error"
	}
	return "success"
}

func isFailedTestStatus(status string) bool {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "FAILED", "ABORTED":
		return true
	default:
		return false
	}
}

func validateCLIOutputFormat(format string) error {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "text", "json":
		return nil
	default:
		return fmt.Errorf("--format must be text or json")
	}
}

func validateCLIControllerURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (!strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https")) {
		return fmt.Errorf("--api must be an absolute http or https URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("--api must not contain user info, a query, or a fragment")
	}
	return nil
}

func importCommandExitCode(err error) int {
	if err == nil {
		return exitCodeSuccess
	}
	var inputErr *cliInputError
	if errors.As(err, &inputErr) {
		return exitCodeUsageOrConfiguration
	}
	var reviewErr *brunoCLIReviewRequiredError
	if errors.As(err, &reviewErr) {
		return exitCodeUsageOrConfiguration
	}
	var responseErr *brunoCLIHTTPError
	if errors.As(err, &responseErr) && responseErr.statusCode >= 400 && responseErr.statusCode < 500 &&
		responseErr.statusCode != http.StatusRequestTimeout && responseErr.statusCode != http.StatusTooManyRequests {
		return exitCodeUsageOrConfiguration
	}
	return exitCodeControllerOrTransport
}

func ciResultExitCode(result CIResult) int {
	outcome := strings.ToLower(strings.TrimSpace(result.Outcome))
	if (outcome == ciOutcomeExecutionError || outcome == ciOutcomeCancelled) && isCITestVerdictFailureStatus(result.Status) {
		return exitCodeTestFailure
	}
	return ciExitCode(outcome)
}

func isCITestVerdictFailureStatus(status string) bool {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "FAILED", "ABORTED", "NO_RESULTS":
		return true
	default:
		return false
	}
}
