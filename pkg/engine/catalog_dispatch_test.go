package engine

import (
	"context"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// TestEngineDispatchCoversCatalogEngineActions cross-checks the dsl action
// catalog against the engine's dispatch: every engine-action spec must be
// executable by the VUser loop, so a script that validates cannot hit an
// unknown engine type at runtime. Control-flow blocks run in the iteration
// loop rather than handleEngineAction, and review_required is an authoring
// placeholder that launch gating resolves before execution — those are the
// only permitted exceptions.
func TestEngineDispatchCoversCatalogEngineActions(t *testing.T) {
	dispatchedElsewhere := map[string]string{
		"control_if":      "executed by the iteration loop's control-flow handler",
		"control_loop":    "executed by the iteration loop's control-flow handler",
		"review_required": "authoring placeholder; resolved before launch, never dispatched",
	}

	for _, spec := range dsl.AllActionSpecs() {
		if !spec.EngineAction() {
			continue
		}
		if reason, ok := dispatchedElsewhere[spec.Type]; ok {
			t.Logf("%s: skipped (%s)", spec.Type, reason)
			continue
		}
		vu := &VUser{
			ID:       1,
			Group:    "cross-check",
			Script:   &dsl.Script{Name: "catalog-cross-check"},
			Variables: map[string]string{},
		}
		action := dsl.Action{Name: "step", Type: spec.Type}
		handled, err := vu.handleEngineAction(context.Background(), action)
		if !handled {
			t.Errorf("%s: catalog engine action not handled by handleEngineAction", spec.Type)
		}
		if err != nil {
			t.Errorf("%s: handleEngineAction returned error: %v", spec.Type, err)
		}
	}
}

// TestEngineDispatchTypesAreInCatalog is the reverse direction: every type
// handleEngineAction dispatches must be declared as an engine action in the
// catalog, so nothing executable is invisible to validation, schema, and
// editor tooling. The list mirrors the switch in vuser.go's
// handleEngineAction plus the iteration-loop control-flow types.
func TestEngineDispatchTypesAreInCatalog(t *testing.T) {
	dispatched := []string{
		"rendezvous", "error_message",
		"source_message", "source_runtime", "source_transaction", "source_timer",
		"source_metric", "source_parameter", "source_json", "source_xml",
		"source_database", "source_secret", "source_external", "source_review",
		"custom_call",
		"control_if", "control_loop",
	}
	for _, actionType := range dispatched {
		spec, ok := dsl.SpecForActionType(actionType)
		if !ok {
			t.Errorf("%s: dispatched by engine but missing from the dsl action catalog", actionType)
			continue
		}
		if !spec.EngineAction() {
			t.Errorf("%s: dispatched by engine but not declared as an engine action (protocols %v)", actionType, spec.Protocols)
		}
	}
}
