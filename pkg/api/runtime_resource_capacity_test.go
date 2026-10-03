package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

func processorResourceGroup(name string, users, partitions int) TestGroup {
	return TestGroup{Name: name, Protocol: "eventhubs", VUsers: users, Initialization: "at_runtime", script: &dsl.Script{Protocol: "eventhubs", Config: dsl.ScriptConfig{EventHubs: dsl.EventHubsConfig{ConsumerMode: "processor", ProcessorMaxPartitions: partitions}}, Actions: []dsl.Action{{Type: "eventhubs_consume"}}}}
}
func processorResourceLoad(processors, workers int64) generatorLoadSnapshot {
	return generatorLoadSnapshot{AvailableCapacity: 100, HeartbeatOK: true, SupportedProtocols: []string{"eventhubs"}, CompatibleFeatures: []string{"eventhubs_processor_checkpoints_v1", generatorrpc.FeatureRuntimeResourceReservationsV1}, RuntimeCapacities: []generatorrpc.ProtocolRuntimeCapacity{{Protocol: "eventhubs", Available: true, ResourceContract: generatorrpc.FeatureRuntimeResourceReservationsV1, Resources: &protocol.RuntimeResourceCapacity{Available: true, MaxProcessors: 4, AvailableProcessors: processors, ReservedProcessors: 4 - processors, MaxWorkers: 32, AvailableWorkers: workers, ReservedWorkers: 32 - workers, MaxPartitionsPerProcessor: 32}}}}
}
func TestRuntimeResourcePlacementUsesBothDimensionsAndDeclaredCeiling(t *testing.T) {
	group := processorResourceGroup("consumer", 3, 8)
	for _, tc := range []struct {
		name string
		load generatorLoadSnapshot
		want bool
	}{
		{"fits", processorResourceLoad(3, 24), true}, {"processors exhausted", processorResourceLoad(2, 32), false},
		{"workers exhausted", processorResourceLoad(4, 23), false}, {"old peer", generatorLoadSnapshot{AvailableCapacity: 100}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := runtimeCapacityAllowsGroup(group, tc.load, 3); got != tc.want {
				t.Fatalf("allowed=%v", got)
			}
		})
	}
	load := processorResourceLoad(4, 32)
	load.RuntimeCapacities[0].Resources.MaxPartitionsPerProcessor = 4
	if runtimeCapacityAllowsGroup(group, load, 1) {
		t.Fatal("partition ceiling was divided across processors")
	}
	group.script.Actions = []dsl.Action{{Type: "eventhubs_produce"}}
	if !runtimeCapacityAllowsGroup(group, generatorLoadSnapshot{AvailableCapacity: 100}, 100) {
		t.Fatal("producer required processor capacity")
	}
}
func TestRuntimeResourcePlacementConservesOverlappingGroupBudget(t *testing.T) {
	groups := []TestGroup{processorResourceGroup("first", 2, 8), processorResourceGroup("second", 3, 8)}
	loads := map[string]generatorLoadSnapshot{"a": processorResourceLoad(4, 32), "b": processorResourceLoad(4, 32)}
	got := planDistributedGroupShards(groups, loads, []string{"a", "b"}, nil, nil)
	if len(got["first"]) != 1 || len(got["second"]) != 1 || got["first"][0].Target == got["second"][0].Target {
		t.Fatalf("overcommitted placement: %+v", got)
	}
	if loads["a"].RuntimeCapacities[0].Resources.AvailableWorkers != 32 {
		t.Fatal("planner mutated inventory")
	}
	clones := cloneProtocolRuntimeCapacities(loads["a"].RuntimeCapacities)
	clones[0].Resources.AvailableWorkers = 0
	if loads["a"].RuntimeCapacities[0].Resources.AvailableWorkers != 32 {
		t.Fatal("capacity clone aliases pool state")
	}
}
func TestRuntimeResourceWeightedPlacementAndPhaseOnlyZeroShards(t *testing.T) {
	group := processorResourceGroup("consumer", 3, 8)
	loads := map[string]generatorLoadSnapshot{"a": processorResourceLoad(1, 8), "b": processorResourceLoad(2, 16)}
	shards := capacityWeightedShardsForGroup(group, []string{"a", "b"}, loads, "")
	if len(shards) != 2 || shards[0].VUsers != 1 || shards[1].VUsers != 2 {
		t.Fatalf("weighted processor bound: %+v", shards)
	}
	group.VUsers = 0
	group.Initialization = "at_once"
	group.script.Init = group.script.Actions
	group.script.Actions = nil
	loads["b"] = processorResourceLoad(0, 0)
	if got := capacityWeightedShardsForGroupIncludingZero(group, []string{"a", "b"}, loads, ""); got != nil {
		t.Fatalf("zero shard ignored init demand: %+v", got)
	}
	if got := capacityWeightedShardsForGroupIncludingZero(group, []string{"a"}, loads, ""); len(got) != 1 || got[0].VUsers != 0 {
		t.Fatalf("phase-only zero shard rejected: %+v", got)
	}
}
func TestRuntimeResourcePeakReservationIsIndependentOfPodSetting(t *testing.T) {
	group := processorResourceGroup("consumer", 1, 4)
	group.Stages = []storage.LoadStage{{TargetVUsers: 3}}
	http := TestGroup{Name: "http", Protocol: "http", VUsers: 1, Stages: []storage.LoadStage{{TargetVUsers: 20}}, script: &dsl.Script{Protocol: "http"}}
	run := &ManagedTest{Groups: []TestGroup{group, http}}
	got, err := runtimeReservationGroups(run, false)
	if err != nil || got[0].VUsers != 3 || got[1].VUsers != 1 {
		t.Fatalf("peak resources=%+v err=%v", got, err)
	}
	maximum := 4
	run.ScenarioMode = "goal_oriented"
	run.GoalConfig = &GoalConfig{MaxVUsers: &maximum}
	got, err = runtimeReservationGroups(run, false)
	if err != nil || got[0].VUsers != 4 || got[1].VUsers != 1 {
		t.Fatalf("adaptive resources=%+v err=%v", got, err)
	}
	compact := cloneManagedTestCapacitySnapshotLocked(run)
	if compact.Groups[0].script != nil {
		t.Fatal("capacity projection retained executable input")
	}
	got, err = runtimeReservationGroups(compact, false)
	if err != nil || got[0].VUsers != 4 || !strings.Contains(groupRequiredRuntimeFeature(compact.Groups[0]), generatorrpc.FeatureRuntimeResourceReservationsV1) {
		t.Fatalf("compact plan lost capability/peak: %+v %v", got, err)
	}
}
func TestRuntimeResourceScaleUsesFrozenEntitlement(t *testing.T) {
	group := processorResourceGroup("consumer", 1, 4)
	claim, err := generatorrpc.NewRuntimeResourceClaim(group.script, "at_runtime", 3)
	if err != nil {
		t.Fatal(err)
	}
	shard := GeneratorShardSummary{Group: group.Name, Target: "a", VUsers: 1, RuntimeResourceClaim: claim, RuntimeResourceAdmission: &pb.RuntimeResourceAdmission{Claim: generatorrpc.CloneRuntimeResourceClaim(claim), Admitted: claim.Requested, Current: true}}
	load := processorResourceLoad(0, 0)
	if err := boundRuntimeResourceScaleLoad(&load, shard, group); err != nil {
		t.Fatal(err)
	}
	if load.AvailableCapacity != 3 || !runtimeResourcesAllowGroup(group, load, 3) || runtimeResourcesAllowGroup(group, load, 4) {
		t.Fatalf("expanded frozen claim: %+v", load)
	}
	next, err := preserveShardCoordinatesByTarget([]GeneratorShardSummary{{Target: "a", VUsers: 2}}, []GeneratorShardSummary{shard})
	if err != nil {
		t.Fatal(err)
	}
	next[0].RuntimeResourceClaim.Requested.Workers = 999
	if shard.RuntimeResourceClaim.Requested.Workers != 12 {
		t.Fatal("scale clone mutates frozen claim")
	}
	shard.RuntimeResourceAdmission.Current = false
	if boundRuntimeResourceScaleLoad(&load, shard, group) == nil {
		t.Fatal("unconfirmed resource admission accepted")
	}
}

type processorResourceReadinessClient struct {
	*preparationFixtureClient
	claims func(map[string]*pb.RuntimeResourceClaim) (map[string]*pb.RuntimeResourceAdmission, error)
}

func (c *processorResourceReadinessClient) CheckPreparationResources(_ context.Context, _ string, _ uint64, _ map[string]int32, _ []byte, claims map[string]*pb.RuntimeResourceClaim) (map[string]*pb.RuntimeResourceAdmission, error) {
	return c.claims(claims)
}
func TestRuntimeResourcePreparationRechecksExactAdmission(t *testing.T) {
	tr := NewTestRunner(1, NewHub(), nil)
	b := &distributedExecutionBackend{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &ManagedTest{ID: "prepared-resources", Status: "INITIALIZING", preparationID: "prep", executionCtx: ctx, executionBackend: b}
	tr.tests[run.ID] = run
	group := processorResourceGroup("consumer", 1, 4)
	claim, _ := generatorrpc.NewRuntimeResourceClaim(group.script, "at_runtime", 2)
	admission := &pb.RuntimeResourceAdmission{Claim: generatorrpc.CloneRuntimeResourceClaim(claim), Admitted: claim.Requested, Current: true}
	shards := map[string][]GeneratorShardSummary{group.Name: {{Group: group.Name, Target: "a", VUsers: 1, RuntimeResourceClaim: claim, RuntimeResourceAdmission: admission}}}
	mode := "valid"
	peer := &processorResourceReadinessClient{preparationFixtureClient: &preparationFixtureClient{check: func(context.Context, string, uint64, map[string]int32, []byte) error {
		return errors.New("legacy path must not verify resources")
	}}, claims: func(expected map[string]*pb.RuntimeResourceClaim) (map[string]*pb.RuntimeResourceAdmission, error) {
		out := map[string]*pb.RuntimeResourceAdmission{group.Name: generatorrpc.CloneRuntimeResourceAdmission(admission)}
		if expected[group.Name].PeakVusers != 2 {
			t.Error("readiness lost peak")
		}
		expected[group.Name].PeakVusers = 99
		switch mode {
		case "released":
			out[group.Name].Current = false
		case "changed":
			out[group.Name].Admitted.Workers++
		case "extra":
			out["unknown"] = out[group.Name]
		case "error":
			return nil, errors.New("secret transport error")
		}
		return out, nil
	}}
	if err := b.installPreparationReadiness(tr, run, shards, map[string]generatorClient{"a": peer}, 7, []byte{1}); err != nil {
		t.Fatal(err)
	}
	allocation, _ := json.Marshal(run.GeneratorShards)
	p := &storage.Preparation{ID: "prep", State: "ready", Allocation: allocation}
	for _, next := range []string{"valid", "released", "changed", "extra", "error", "valid"} {
		mode = next
		if got := tr.preparationReservationValid(run.ID, p); got != (next == "valid") {
			t.Fatalf("%s readiness=%v", next, got)
		}
	}
	if claim.PeakVusers != 2 {
		t.Fatal("readiness mutates durable claim")
	}
	if err := b.installPreparationReadiness(tr, run, shards, map[string]generatorClient{"a": peer.preparationFixtureClient}, 7, nil); err == nil {
		t.Fatal("old readiness peer accepted")
	}
}

type processorResourceAssignmentClient struct {
	generatorClient
	accept    bool
	admission *pb.RuntimeResourceAdmission
	seen      *pb.RuntimeResourceClaim
}

func (c *processorResourceAssignmentClient) AssignWork(_ context.Context, a generatorrpc.Assignment) (generatorrpc.AssignmentResult, error) {
	c.seen = a.RuntimeResourceClaim
	return generatorrpc.AssignmentResult{Accepted: c.accept, RuntimeResourceAdmission: c.admission}, nil
}
func TestRuntimeResourceAssignmentRequiresExactAcknowledgement(t *testing.T) {
	group := processorResourceGroup("consumer", 1, 4)
	claim, _ := generatorrpc.NewRuntimeResourceClaim(group.script, "at_runtime", 3)
	for _, valid := range []bool{false, true} {
		peer := &processorResourceAssignmentClient{accept: true}
		if valid {
			peer.admission = &pb.RuntimeResourceAdmission{Claim: generatorrpc.CloneRuntimeResourceClaim(claim), Admitted: claim.Requested, Current: true}
		}
		b := &distributedExecutionBackend{timeout: time.Second}
		plans := []distributedTargetAssignmentPlan{{target: "a", client: peer, shards: []GeneratorShardSummary{{Group: group.Name, Target: "a", VUsers: 1, RuntimeResourceClaim: claim}}}}
		outcomes := b.assignDistributedGroupTargets(context.Background(), plans, generatorrpc.Script{}, generatorrpc.Assignment{GroupName: group.Name}, "source", nil, nil)
		if (outcomes[0].err == nil) != valid || peer.seen.PeakVusers != 3 {
			t.Fatalf("valid=%v result=%+v claim=%+v", valid, outcomes, peer.seen)
		}
	}
}

func TestRuntimeResourceScalingKeepsNilZeroPeakShardBounded(t *testing.T) {
	group := processorResourceGroup("consumer", 3, 4)
	claim, _ := generatorrpc.NewRuntimeResourceClaim(group.script, "at_runtime", 2)
	admitted := GeneratorShardSummary{Group: group.Name, Target: "a", RuntimeResourceClaim: claim, RuntimeResourceAdmission: &pb.RuntimeResourceAdmission{Claim: claim, Admitted: claim.Requested, Current: true}}
	zero := GeneratorShardSummary{Group: group.Name, Target: "b"}
	loads := map[string]generatorLoadSnapshot{"a": processorResourceLoad(4, 32), "b": processorResourceLoad(4, 32)}
	for _, shard := range []GeneratorShardSummary{admitted, zero} {
		load := loads[shard.Target]
		if err := boundRuntimeResourceScaleLoad(&load, shard, group); err != nil {
			t.Fatal(err)
		}
		loads[shard.Target] = load
	}
	if shards := capacityWeightedShardsForGroupIncludingZero(group, []string{"a", "b"}, loads, ""); shards != nil {
		t.Fatalf("growth escaped nil zero-peak claim: %+v", shards)
	}
	group.script.Actions = []dsl.Action{{Type: "eventhubs_produce"}}
	load := processorResourceLoad(4, 32)
	if err := boundRuntimeResourceScaleLoad(&load, zero, group); err != nil || load.AvailableCapacity != 100 {
		t.Fatalf("producer scale capped: %+v %v", load, err)
	}
}

func TestRuntimeResourceProcessorRecoveryRequiresExplicitContract(t *testing.T) {
	group := processorResourceGroup("processor", 1, 4)
	group.Stages = []storage.LoadStage{{TargetVUsers: 3}}
	if allowed, reason := groupAllowsGeneratorLossReshard(group); allowed || !strings.Contains(reason, "checkpoint progress") {
		t.Fatalf("processor recovery unexpectedly eligible: %v %s", allowed, reason)
	}
	compact := cloneManagedTestCapacitySnapshotLocked(&ManagedTest{Groups: []TestGroup{group}})
	if allowed, _ := groupAllowsGeneratorLossReshard(compact.Groups[0]); allowed {
		t.Fatal("compact processor plan lost recovery restriction")
	}
	group.script.Actions = []dsl.Action{{Type: "eventhubs_produce"}}
	if allowed, reason := groupAllowsGeneratorLossReshard(group); !allowed {
		t.Fatalf("producer-only recovery blocked: %s", reason)
	}
}

func TestLocalRuntimeResourceAllocationSeparatesCurrentLoadFromReservedPeak(t *testing.T) {
	group := processorResourceGroup("consumer", 1, 4)
	group.Stages = []storage.LoadStage{{TargetVUsers: 3}}
	peaks, err := runtimeReservationGroups(&ManagedTest{Groups: []TestGroup{group}}, false)
	if err != nil {
		t.Fatal(err)
	}
	demand, err := groupRuntimeResourceDemand(peaks[0], peaks[0].VUsers)
	if err != nil {
		t.Fatal(err)
	}
	for _, current := range []int{0, 1, 3} {
		allocation := localRuntimeResourceAllocations(peaks, map[string]protocol.RuntimeResourceDemand{group.Name: demand}, map[string]int{group.Name: current})
		if len(allocation) != 1 || allocation[0].VUsers != current || allocation[0].RuntimeResourceClaim.PeakVusers != 3 || allocation[0].RuntimeResourceAdmission.Admitted.Processors != 3 {
			t.Fatalf("current=%d allocation=%+v", current, allocation)
		}
	}
	if got := localRuntimeResourceAllocations(peaks, nil, map[string]int{group.Name: 1}); len(got) != 0 {
		t.Fatalf("zero demand manufactured admission: %+v", got)
	}
}

func TestRuntimeResourceWeightedPlacementPreservesNonProcessorCapacityRatios(t *testing.T) {
	for _, mode := range []string{"http", "observer", "processor producer"} {
		t.Run(mode, func(t *testing.T) {
			group := processorResourceGroup("buyers", 10, 4)
			switch mode {
			case "http":
				group.Protocol = "http"
				group.script = nil
			case "observer":
				group.script.Config.EventHubs.ConsumerMode = "observer"
			case "processor producer":
				group.script.Actions = []dsl.Action{{Type: "eventhubs_produce"}}
			}
			loads := map[string]generatorLoadSnapshot{"gen-a": {AvailableCapacity: 90}, "gen-b": {AvailableCapacity: 10}}
			shards := capacityWeightedShardsForGroup(group, []string{"gen-a", "gen-b"}, loads, "")
			if len(shards) != 2 || shards[0].VUsers != 8 || shards[1].VUsers != 2 {
				t.Fatalf("resource-free weights changed: %+v", shards)
			}
		})
	}
}
