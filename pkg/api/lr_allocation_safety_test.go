package api

import (
	"context"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	"testing"
	"time"
)

func TestLRAllocationRejectsOlderGeneratorsAndResharding(t *testing.T) {
	for _, p := range []dsl.Parameter{{Selection: "unique", AutoAllocate: true}, {Selection: "unique", BlockSize: 2, Update: "once"}} {
		g := TestGroup{script: &dsl.Script{Protocol: "http", Parameters: []dsl.Parameter{p}}}
		required := groupRequiredRuntimeFeature(g)
		if required == "" || protocol.RuntimeFeaturesSatisfy(nil, required) {
			t.Fatal("older generator accepted new allocation contract")
		}
		if !protocol.RuntimeFeaturesSatisfy([]string{dsl.FeatureLRFidelityV1}, required) {
			t.Fatal("current generator rejected")
		}
		if allowed, _ := groupAllowsGeneratorLossReshard(g); allowed {
			t.Fatal("failover would lose frozen allocation state")
		}
	}
}

func TestDistributedAllocationOffsetsPreserveScenarioPopulation(t *testing.T) {
	left, right := &startupAssignmentFakeClient{}, &startupAssignmentFakeClient{}
	plans := []distributedTargetAssignmentPlan{
		{target: "later", client: right, shards: []GeneratorShardSummary{{ShardOrdinal: 9, VUsers: 2, VUserIDOffset: 9000000}}},
		{target: "earlier", client: left, shards: []GeneratorShardSummary{{ShardOrdinal: 3, VUsers: 1, VUserIDOffset: 3000000}}},
	}
	b := newDistributedExecutionBackend(nil, time.Second, nil)
	results := b.assignDistributedGroupTargets(context.Background(), plans, generatorrpc.Script{}, generatorrpc.Assignment{ParameterPopulation: 7, ParameterOffset: 4}, "script", nil, nil)
	for _, result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
	}
	if left.assignments[0].ParameterOffset != 4 || right.assignments[0].ParameterOffset != 5 {
		t.Fatalf("offsets left=%d right=%d", left.assignments[0].ParameterOffset, right.assignments[0].ParameterOffset)
	}
	if left.assignments[0].ParameterPopulation != 7 || right.assignments[0].ParameterPopulation != 7 {
		t.Fatal("global population was reduced to a shard/group size")
	}
}
