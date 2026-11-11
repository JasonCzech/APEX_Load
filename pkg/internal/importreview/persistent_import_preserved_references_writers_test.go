package importreview

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestValidatePreservedImportCredentialEditsAllowsContainerAndControlFlowEdits(t *testing.T) {
	prior := &dsl.Script{
		Name: "Container edits", Version: "1", Protocol: "http",
		Actions: []dsl.Action{{
			Name: "loop", Type: "control_loop",
			Control: &dsl.ControlFlow{
				Count: 1,
				Steps: []dsl.Action{{
					Name: "orders", Type: "GET", Method: "GET", Target: "https://origin.example.test/orders",
					Headers: map[string]string{"Authorization": "Bearer synthetic-loop-token"},
				}},
			},
		}},
	}
	MarkCredentialPreservingImportScript(prior)

	candidate := CloneScriptDefinition(prior)
	candidate.Actions[0].Control.Count = 5
	candidate.Init = []dsl.Action{{
		Name: "warmup", Type: "GET", Method: "GET", Target: "https://origin.example.test/health",
	}}

	if err := ValidatePreservedImportCredentialEdits(candidate, prior); err != nil {
		t.Fatalf("ValidatePreservedImportCredentialEdits returned unexpected error: %v", err)
	}
}
