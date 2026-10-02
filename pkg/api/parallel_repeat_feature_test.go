package api

import (
	"github.com/jasonczech/apexload/pkg/dsl"
	"strings"
	"testing"
)

func TestParallelRepeatPlacementFeature(t *testing.T) {
	s := &dsl.Script{Protocol: "http", Actions: []dsl.Action{{Type: "control_parallel", Control: &dsl.ControlFlow{Branches: []dsl.ParallelBranch{{Steps: []dsl.Action{{Type: "control_loop", Control: &dsl.ControlFlow{Count: 2, Steps: []dsl.Action{{Type: "control_break"}}}}}}}}}}}
	if !strings.Contains(groupRequiredRuntimeFeature(TestGroup{Protocol: "http", script: s}), dsl.FeatureParallelLoopControlV1) {
		t.Fatal("placement admitted old peer")
	}
}
