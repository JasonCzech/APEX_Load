package api

import (
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/engine"
)

// Script-debugger step modes. "into" pauses at the very next leaf (the classic
// single-step); "over" and "loop_end" resume THROUGH a step-path subtree and
// pause at the first leaf that falls outside it.
const (
	scriptStepInto    = "into"
	scriptStepOver    = "over"
	scriptStepLoopEnd = "loop_end"
)

// stepBoundary describes the step-path subtree a step-over / finish-loop resume
// runs through; the session pauses at the first leaf OUTSIDE it. It generalizes
// "resume until we exit subtree P": step-over sets P to the container holding the
// paused leaf, finish-loop sets P to the nearest enclosing control_loop.
type stepBoundary struct {
	Phase       string
	ActionIndex int
	Prefix      []string // step-path segments that must match exactly
	Kind        string   // required container kind at the Prefix position ("" = any)
	WholeAction bool     // membership is the whole (phase, actionIndex), any step path
}

// segmentKind returns the container kind of a step-path segment, e.g.
// "loop[2]" → "loop", "then[0]" → "then", "requests[3]" → "requests".
func segmentKind(segment string) string {
	if i := strings.IndexByte(segment, '['); i >= 0 {
		return segment[:i]
	}
	return segment
}

// contains reports whether the leaf identified by action falls inside the boundary.
func (b *stepBoundary) contains(action engine.ReplayDebugAction) bool {
	if b == nil {
		return false
	}
	if action.Phase != b.Phase || action.ActionIndex != b.ActionIndex {
		return false
	}
	if b.WholeAction {
		return true
	}
	if len(action.StepPath) <= len(b.Prefix) {
		return false
	}
	for i, seg := range b.Prefix {
		if action.StepPath[i] != seg {
			return false
		}
	}
	if b.Kind != "" && segmentKind(action.StepPath[len(b.Prefix)]) != b.Kind {
		return false
	}
	return true
}

// computeStepBoundary derives the resume-through subtree for a step-over /
// finish-loop request from the currently paused action. It returns nil when no
// boundary applies (e.g. a nil paused action) so the caller falls back to
// step-into.
func computeStepBoundary(action *engine.ReplayDebugAction, mode string) *stepBoundary {
	if action == nil {
		return nil
	}
	b := &stepBoundary{Phase: action.Phase, ActionIndex: action.ActionIndex}
	switch mode {
	case scriptStepOver:
		n := len(action.StepPath)
		if n == 0 {
			// A plain top-level leaf: step over the whole action to the next one.
			b.WholeAction = true
			return b
		}
		b.Prefix = append([]string(nil), action.StepPath[:n-1]...)
		b.Kind = segmentKind(action.StepPath[n-1])
		return b
	case scriptStepLoopEnd:
		// Nearest enclosing loop = the deepest "loop" segment in the step path.
		loopIdx := -1
		for i, seg := range action.StepPath {
			if segmentKind(seg) == "loop" {
				loopIdx = i
			}
		}
		if loopIdx < 0 {
			// No enclosing loop: finish the whole action instead.
			b.WholeAction = true
			return b
		}
		b.Prefix = append([]string(nil), action.StepPath[:loopIdx]...)
		b.Kind = "loop"
		return b
	default:
		return nil
	}
}

// conditionEvidenceKey keys condition evidence by a control container's identity
// (phase, action index, static step path) so the dashboard can look it up for the
// matching control_if / control_loop row.
func conditionEvidenceKey(phase string, actionIndex int, stepPath []string) string {
	return fmt.Sprintf("%s#%d#%s", phase, actionIndex, strings.Join(stepPath, "/"))
}

// clearStepLocked resets any active step-over / finish-loop boundary. Callers must
// hold s.mu.
func (s *ScriptDebugSession) clearStepLocked() {
	s.stepMode = ""
	s.stepBoundary = nil
}

// stepBoundaryExitLocked reports whether an active step-over / finish-loop boundary
// is set and the incoming action has left it (so the session should pause).
func (s *ScriptDebugSession) stepBoundaryExitLocked(action engine.ReplayDebugAction) bool {
	return s.stepBoundary != nil && !s.stepBoundary.contains(action)
}
