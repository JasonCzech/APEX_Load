package api

import (
	"errors"
	"fmt"
	"strings"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	eventhubsprotocol "github.com/jasonczech/apexload/pkg/protocol/eventhubs"
)

func runtimeResourcePlanForGroup(group TestGroup) (engine.RuntimeResourcePlan, error) {
	if group.runtimeResourcePlanKnown {
		if group.runtimeResourcePlanError != "" {
			return engine.RuntimeResourcePlan{}, errors.New(group.runtimeResourcePlanError)
		}
		return group.runtimeResourcePlan, nil
	}
	return engine.NewRuntimeResourcePlan(group.script, group.Initialization)
}

// Source scripts are compacted out of admission snapshots; retain a bounded,
// immutable resource plan so capacity checks never reinterpret a missing script.
func groupRuntimeResourceDemand(group TestGroup, peak int) (protocol.RuntimeResourceDemand, error) {
	plan, err := runtimeResourcePlanForGroup(group)
	if err != nil {
		return protocol.RuntimeResourceDemand{}, err
	}
	return plan.Demand(peak)
}

func protocolRuntimeResources(snapshot generatorLoadSnapshot) (protocol.RuntimeResourceCapacity, bool) {
	var found *protocol.RuntimeResourceCapacity
	for _, capacity := range snapshot.RuntimeCapacities {
		if !strings.EqualFold(strings.TrimSpace(capacity.Protocol), "eventhubs") {
			continue
		}
		if !capacity.Available || capacity.ResourceContract != eventhubsprotocol.FeatureProcessorReservationsV1 || capacity.Resources == nil || !capacity.Resources.Available {
			return protocol.RuntimeResourceCapacity{}, false
		}
		c := *capacity.Resources
		if !c.Valid() {
			return protocol.RuntimeResourceCapacity{}, false
		}
		if found != nil && *found != c {
			return protocol.RuntimeResourceCapacity{}, false
		}
		found = &c
	}
	if found == nil {
		return protocol.RuntimeResourceCapacity{}, false
	}
	return *found, true
}

func runtimeResourcesAllowGroup(group TestGroup, snapshot generatorLoadSnapshot, vusers int) bool {
	demand, err := groupRuntimeResourceDemand(group, vusers)
	if err != nil {
		return false
	}
	if demand.Processors == 0 && demand.Workers == 0 {
		return true
	}
	available, ok := protocolRuntimeResources(snapshot)
	return ok && demand.Processors <= available.AvailableProcessors && demand.Workers <= available.AvailableWorkers && demand.MaxPartitionsPerProcessor <= available.MaxPartitionsPerProcessor
}

func maximumVUsersWithinRuntimeResources(group TestGroup, snapshot generatorLoadSnapshot, upper int) int {
	if upper <= 0 || !runtimeResourcesAllowGroup(group, snapshot, 0) {
		return 0
	}
	low, high := 0, upper
	for low < high {
		mid := low + (high-low+1)/2
		if runtimeResourcesAllowGroup(group, snapshot, mid) {
			low = mid
		} else {
			high = mid - 1
		}
	}
	return low
}

func consumeGroupRuntimeResources(snapshot *generatorLoadSnapshot, group TestGroup, peak int) {
	if snapshot == nil {
		return
	}
	d, err := groupRuntimeResourceDemand(group, peak)
	if err != nil || d.Processors == 0 {
		return
	}
	for i := range snapshot.RuntimeCapacities {
		c := &snapshot.RuntimeCapacities[i]
		if !strings.EqualFold(strings.TrimSpace(c.Protocol), "eventhubs") || c.Resources == nil {
			continue
		}
		c.Resources.AvailableProcessors = max(0, c.Resources.AvailableProcessors-d.Processors)
		c.Resources.AvailableWorkers = max(0, c.Resources.AvailableWorkers-d.Workers)
		c.Resources.ReservedProcessors += d.Processors
		c.Resources.ReservedWorkers += d.Workers
	}
}

func runtimeResourcePlacementReason(group TestGroup, snapshot generatorLoadSnapshot) string {
	demand, err := groupRuntimeResourceDemand(group, group.VUsers)
	if err != nil {
		return "eventhubs processor resource plan invalid"
	}
	if demand.Processors == 0 {
		return ""
	}
	c, ok := protocolRuntimeResources(snapshot)
	if !ok {
		return "eventhubs processor capacity unavailable or incompatible"
	}
	if demand.MaxPartitionsPerProcessor > c.MaxPartitionsPerProcessor {
		return fmt.Sprintf("eventhubs partition ceiling %d exceeds admitted maximum %d", demand.MaxPartitionsPerProcessor, c.MaxPartitionsPerProcessor)
	}
	if demand.Processors > c.AvailableProcessors || demand.Workers > c.AvailableWorkers {
		return fmt.Sprintf("eventhubs requires %d processors/%d workers; available %d/%d, reserved %d/%d, draining %d/%d", demand.Processors, demand.Workers, c.AvailableProcessors, c.AvailableWorkers, c.ReservedProcessors, c.ReservedWorkers, c.DrainingProcessors, c.DrainingWorkers)
	}
	return ""
}

// Resource-bearing groups reserve their lifetime maximum even when optional
// CPU/memory pod reservations are off. Other groups retain existing placement.
func runtimeReservationGroups(test *ManagedTest, allPeaks bool) ([]TestGroup, error) {
	needs := allPeaks
	for _, group := range test.Groups {
		plan, err := runtimeResourcePlanForGroup(group)
		if err != nil {
			return nil, err
		}
		needs = needs || plan.RequiresReservation()
	}
	if !needs {
		return test.Groups, nil
	}
	peaks, err := podReservationGroups(test)
	if err != nil {
		return nil, err
	}
	if allPeaks {
		return peaks, nil
	}
	for i, group := range test.Groups {
		plan, _ := runtimeResourcePlanForGroup(group)
		if !plan.RequiresReservation() {
			peaks[i] = group
		}
	}
	return peaks, nil
}

func shardRuntimeResourceClaim(shard GeneratorShardSummary) *pb.RuntimeResourceClaim {
	return generatorrpc.CloneRuntimeResourceClaim(shard.RuntimeResourceClaim)
}

// Scaling may use only the entitlement already frozen for this shard. Fresh
// free process capacity belongs to other starts; it cannot expand a wire claim.
func boundRuntimeResourceScaleLoad(snapshot *generatorLoadSnapshot, shard GeneratorShardSummary, group TestGroup) error {
	claim := shard.RuntimeResourceClaim
	if claim == nil {
		plan, err := runtimeResourcePlanForGroup(group)
		if err != nil {
			return errors.New("processor resource plan invalid")
		}
		if plan.RequiresReservation() {
			demand, err := plan.Demand(0)
			if err != nil || !demand.IsZero() {
				return errors.New("processor resource admission unavailable")
			}
			// A consuming zero-peak shard has no lease registry entry, but its
			// immutable admission still forbids growth.
			snapshot.AvailableCapacity = 0
		}
		return nil
	}
	if !generatorrpc.ValidRuntimeResourceAdmission(claim, shard.RuntimeResourceAdmission) {
		return errors.New("processor resource admission unavailable")
	}
	if _, ok := protocolRuntimeResources(*snapshot); !ok {
		return errors.New("processor resource capacity unavailable")
	}
	demand := generatorrpc.RuntimeResourceDemandFromProto(claim.Requested)
	if !demand.Valid() || demand.IsZero() {
		return errors.New("invalid processor resource claim")
	}
	snapshot.AvailableCapacity = min(snapshot.AvailableCapacity, claim.PeakVusers)
	for i := range snapshot.RuntimeCapacities {
		c := &snapshot.RuntimeCapacities[i]
		if !strings.EqualFold(strings.TrimSpace(c.Protocol), "eventhubs") {
			continue
		}
		// This is a placement projection of the owned entitlement, not a published
		// observation of the shared pool's active/draining contributors.
		c.Resources = &protocol.RuntimeResourceCapacity{Available: true, MaxProcessors: demand.Processors, AvailableProcessors: demand.Processors, MaxWorkers: demand.Workers, AvailableWorkers: demand.Workers, MaxPartitionsPerProcessor: demand.MaxPartitionsPerProcessor}
	}
	return nil
}

func requiredMessagingFeaturesForGroup(group TestGroup) []string {
	if group.script == nil {
		return append([]string(nil), group.messagingRuntimeFeatures...)
	}
	return dsl.RequiredMessagingRuntimeFeatures(group.script)
}

// localRuntimeResourceTarget names the controller's own process in a local
// run's runtime-resource placement; it is never a generator.
const localRuntimeResourceTarget = "local"

// Local allocation distinguishes the execution's current load from the frozen
// resource peak, just as a distributed shard does. Both remain audit snapshots.
func localRuntimeResourceAllocations(peaks []TestGroup, demands map[string]protocol.RuntimeResourceDemand, currentVUsers map[string]int) []GeneratorShardSummary {
	var allocation []GeneratorShardSummary
	for _, group := range peaks {
		demand := demands[group.Name]
		if demand.IsZero() {
			continue
		}
		claim := &pb.RuntimeResourceClaim{Contract: generatorrpc.FeatureRuntimeResourceReservationsV1, Protocol: "eventhubs", PeakVusers: int32(group.VUsers), Requested: generatorrpc.RuntimeResourceDemandToProto(demand)}
		allocation = append(allocation, GeneratorShardSummary{Group: group.Name, Target: localRuntimeResourceTarget, VUsers: currentVUsers[group.Name], RuntimeResourceClaim: claim, RuntimeResourceAdmission: &pb.RuntimeResourceAdmission{Claim: generatorrpc.CloneRuntimeResourceClaim(claim), Admitted: generatorrpc.RuntimeResourceDemandToProto(demand), Current: true}})
	}
	return allocation
}
