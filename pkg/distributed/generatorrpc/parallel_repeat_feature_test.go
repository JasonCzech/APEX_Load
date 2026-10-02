package generatorrpc

import (
	"github.com/jasonczech/apexload/pkg/dsl"
	"strings"
	"testing"
)

func TestParallelRepeatRequiredFeature(t *testing.T) {
	script := &dsl.Script{Protocol: "http", Actions: []dsl.Action{{Type: "control_parallel", Control: &dsl.ControlFlow{Branches: []dsl.ParallelBranch{{Steps: []dsl.Action{{Type: "control_loop", Control: &dsl.ControlFlow{Count: 2, Steps: []dsl.Action{{Type: "control_continue"}}}}}}}}}}}
	if !strings.Contains(requiredRuntimeFeature(script), dsl.FeatureParallelLoopControlV1) {
		t.Fatal("new semantics accepted by old peer")
	}
	if requiredProtocolRuntimeFeature(script) != "" {
		t.Fatal("engine feature incorrectly required from HTTP plugin")
	}
}
