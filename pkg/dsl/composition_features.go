package dsl

import "strings"

const FeatureParallelLoopControlV1 = "parallel_loop_control_v1"

// RequiredCompositionRuntimeFeatures admits newer branch-local repeat semantics.
func RequiredCompositionRuntimeFeatures(s *Script) []string {
	if s == nil {
		return nil
	}
	requested := false
	visitScriptActions(s, func(_ string, a Action) {
		if CanonicalActionType(strings.ToLower(strings.TrimSpace(a.Type))) != "control_parallel" || a.Control == nil {
			return
		}
		var visit func([]Action)
		visit = func(actions []Action) {
			for _, child := range actions {
				kind := CanonicalActionType(strings.ToLower(strings.TrimSpace(child.Type)))
				if kind == "control_break" || kind == "control_continue" {
					requested = true
				}
				if child.Control != nil {
					for _, b := range child.Control.ActionBranches() {
						visit(b.Actions)
					}
				}
			}
		}
		for _, b := range a.Control.Branches {
			visit(b.Steps)
		}
	})
	if requested {
		return []string{FeatureParallelLoopControlV1}
	}
	return nil
}
