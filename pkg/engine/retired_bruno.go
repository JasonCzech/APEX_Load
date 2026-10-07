package engine

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
)

// RetiredBrunoRuntimeError rejects legacy runner scripts before resolving
// secrets, staging artifacts, or reserving execution capacity. Native scripts
// imported from Bruno use ordinary protocols and remain executable.
func RetiredBrunoRuntimeError(script *dsl.Script) error {
	if script == nil {
		return nil
	}
	if strings.EqualFold(strings.TrimSpace(script.Protocol), "bruno") {
		return errors.New(dsl.BrunoRunnerRetiredMessage)
	}
	return dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		if ref.Action != nil && strings.EqualFold(strings.TrimSpace(ref.Action.Type), "bruno_run") {
			return fmt.Errorf("%s: %s", ref.Path, dsl.BrunoRunnerRetiredMessage)
		}
		return nil
	})
}
