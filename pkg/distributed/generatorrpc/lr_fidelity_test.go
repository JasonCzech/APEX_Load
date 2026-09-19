package generatorrpc

import (
	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/dsl"
	"testing"
)

func TestLRAllocationAssignmentFingerprintAndRuntimeFeature(t *testing.T) {
	a := &pb.WorkAssignment{TestId: "test", GroupName: "group", VuserCount: 2, ParameterPopulation: 4, ParameterOffset: 0}
	first := workAssignmentFingerprint(a)
	a.ParameterOffset = 2
	if first == workAssignmentFingerprint(a) {
		t.Fatal("allocation offset omitted from assignment identity")
	}
	first = workAssignmentFingerprint(a)
	a.ParameterPopulation = 6
	if first == workAssignmentFingerprint(a) {
		t.Fatal("allocation population omitted from assignment identity")
	}
	script := &dsl.Script{Protocol: "http", Parameters: []dsl.Parameter{{Selection: "unique", AutoAllocate: true}}}
	if requiredRuntimeFeature(script) != dsl.FeatureLRFidelityV1 {
		t.Fatal("missing generator capability requirement")
	}
	if requiredProtocolRuntimeFeature(script) != "" {
		t.Fatal("engine allocation incorrectly requires plugin runtime capability")
	}
}
