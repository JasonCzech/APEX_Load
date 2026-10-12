package api

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

var debugRunIdentityPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

// debugRunIdentity is the value {{run.id}} resolves to in a debug or replay
// session: the caller's choice, else the session's own identity, so a script
// that tags its requests with the run can be debugged as it runs in a test.
func debugRunIdentity(requested, sessionID string) string {
	if id := strings.TrimSpace(requested); id != "" {
		return id
	}
	return "debug-" + sessionID
}

// validateDebugRunIdentity rejects a requested run identity a target could
// not carry safely in a URL or header. Empty means "use the session's".
func validateDebugRunIdentity(requested string) error {
	if id := strings.TrimSpace(requested); id != "" && !debugRunIdentityPattern.MatchString(id) {
		return fmt.Errorf("run_id must be 1 to 128 letters, digits, '.', '_', ':' or '-'")
	}
	return nil
}

// debugContinueOnError is what follows a failed action the debugger does not
// pause on: the caller's choice, else what a test run of the script does — its
// own setting, or the scenario default of continuing — so a debug run fails
// the way the load test would.
func debugContinueOnError(requested *bool, script *dsl.Script) bool {
	if requested != nil {
		return *requested
	}
	if script != nil && script.Config.ErrorHandling.ContinueOnErrorSet() {
		return script.Config.ErrorHandling.ContinueOnErrorEnabled()
	}
	return true
}

func debugFailurePolicy(continueOnError bool) string {
	if continueOnError {
		return "continue_on_error"
	}
	return "stop_on_error"
}
