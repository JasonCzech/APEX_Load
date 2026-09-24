package api

import (
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	"testing"
)

func TestSourceAffinityPlacementIsAtomic(t *testing.T) {
	groups := []TestGroup{{Name: "kafka", Protocol: "http", VUsers: 100}, {Name: "east", Protocol: "http", VUsers: 50}, {Name: "west", Protocol: "http", VUsers: 50}}
	assignment := &GeneratorAssignmentSettings{Mode: generatorAssignmentModeAuto, SourceAffinity: "single_generator"}
	for _, capacity := range []int32{199, 200} {
		loads := map[string]generatorLoadSnapshot{
			"small": {Target: "small", AvailableCapacity: 150, SupportedProtocols: []string{"http"}},
			"large": {Target: "large", AvailableCapacity: capacity, SupportedProtocols: []string{"http"}},
		}
		got := planDistributedGroupShards(groups, loads, []string{"small", "large"}, assignment, nil)
		if capacity < 200 {
			if len(got) != 0 {
				t.Fatalf("split or partial placement: %v", got)
			}
			continue
		}
		for _, group := range groups {
			if shards := got[group.Name]; len(shards) != 1 || shards[0].Target != "large" || shards[0].VUsers != group.VUsers {
				t.Fatalf("invalid source placement: %v", got)
			}
		}
		if loads["large"].AvailableCapacity != capacity {
			t.Fatal("mutated inventory")
		}
	}
}
func TestJMeterContractRejectsOldGeneratorsAndRecovery(t *testing.T) {
	for _, script := range []*dsl.Script{
		{Config: dsl.ScriptConfig{JMeter: &dsl.JMeterCompatibility{Profile: dsl.JMeterMessagingProfile}}},
		{Config: dsl.ScriptConfig{SamplerSchedules: []dsl.SamplerSchedule{{ID: "timer"}}}},
		{Parameters: []dsl.Parameter{{Scope: "source_engine"}}},
		{Actions: []dsl.Action{{Type: "kafka_produce", PacingIDs: []string{"timer"}}}},
	} {
		group := TestGroup{script: script}
		feature := groupRequiredRuntimeFeature(group)
		if feature == "" || protocol.RuntimeFeaturesSatisfy(nil, feature) {
			t.Fatal("old generator accepted source-session contract")
		}
		if allowed, _ := groupAllowsGeneratorLossReshard(group); allowed {
			t.Fatal("source state could be replayed after loss")
		}
	}
}
