package api

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/recordbridge"
)

var debugRunIdentityPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

// debugRunIdentity is the value {{run.id}} resolves to in a debug or replay
// session: the caller's choice, else the session's own identity, so a script
// that tags its requests with the run can be debugged as it runs in a test.
func debugRunIdentity(requested, sessionID string) string {
	return recordbridge.DebugRunIdentity(requested, sessionID)
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
	return recordbridge.DebugContinueOnError(requested, script)
}

func debugFailurePolicy(continueOnError bool) string {
	return recordbridge.DebugFailurePolicy(continueOnError)
}
