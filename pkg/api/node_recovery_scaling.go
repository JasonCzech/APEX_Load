package api

import (
	"math"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/kubeops"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Before reservation, VUser demand alone can round down to too few whole pods
// for an indivisible source plus its spare. This bounded, conservative floor
// requests up to one unused replacement per planned source. Actual admission
// still requires the Kubernetes identity and contingency matching transaction.
func (tr *TestRunner) recoveryAdmissionReplicaFloor(tests []*ManagedTest, settings SystemSettings, base int, now time.Time) (int, bool) {
	pending := false
	for _, test := range tests {
		if recoverySpareVUsers(test) > 0 && (hasQueuedScalingDemand(test, now) || test.Status == "INITIALIZING" || test.Status == "STARTING") {
			pending = true
			break
		}
	}
	if !pending {
		return base, true
	}
	b, ok := tr.execution.(*distributedExecutionBackend)
	if !ok || !settings.CapacityPlanning.PodReservationsEnabled {
		return base, false
	}
	entries, at := tr.cachedGeneratorInventorySnapshot()
	if at.IsZero() || len(entries) > maxRecoveryCapacityPlanPods {
		return base, false
	}
	loads := map[string]generatorLoadSnapshot{}
	placements := map[string]*GeneratorPodPlacement{}
	var observedPods []storage.PodCapacityReservation
	var limit kubeops.StatefulSetAutoscalingLimit
	for _, e := range entries {
		p := e.PodPlacement
		if p == nil || !p.Ready || p.Terminating || !recoveryScaleLimitFresh(p.scaleLimit) {
			continue
		}
		if limit.WorkloadUID != "" && limit != p.scaleLimit {
			return base, false
		}
		limit = p.scaleLimit
		observedPods = append(observedPods, storage.PodCapacityReservation{Target: p.Name, RecoveryPlacement: &storage.PodCapacityRecoveryPlacement{PodName: p.Name}})
		if !generatorInventoryEntryReachable(e) || e.Cordoned || e.DrainState != "active" || e.CompatibilityState != "compatible" || b.generatorDrainState(e.Target) != "active" {
			continue
		}
		placements[e.Target] = p
		loads[e.Target] = generatorLoadSnapshot{Target: e.Target, HeartbeatOK: generatorInventoryEntryReachable(e), PodCapacity: e.PodCapacity, CPULimitCores: e.CPULimitCores, MemoryLimitBytes: e.MemoryLimitBytes, MaxVUsers: e.MaxVUsers, SupportedProtocols: e.SupportedProtocols, CompatibleFeatures: e.CompatibleFeatures, RuntimeCapacities: e.RuntimeCapacities}
	}
	if limit.MaximumReplicas <= 0 {
		return base, false
	}
	configurePodLoads(settings, loads)
	compatibility := newGeneratorCompatibilityIndex(tr.enrichGeneratorTargetSpecsWithLabels(b.configuredTargetSpecs()), loads)
	maximum := limit.MaximumReplicas
	observedFloor := max(len(observedPods), reservedReplicaFloor(observedPods, len(observedPods)))
	floor := min(base, maximum)
	for _, test := range tests {
		if recoverySpareVUsers(test) == 0 || !(hasQueuedScalingDemand(test, now) || test.Status == "INITIALIZING" || test.Status == "STARTING") {
			continue
		}
		if validateRecoverySpareWorkload(test) != nil {
			return base, false
		}
		allowed, err := b.allowedTargetsForTest(test)
		if err != nil {
			return base, false
		}
		groups, err := podReservationGroups(test)
		if err != nil || len(groups) == 0 {
			return base, false
		}
		sources := 0
		eligiblePods := map[string]*GeneratorPodPlacement{}
		var retained []storage.PodCapacityReservation
		for _, group := range groups {
			demand := podGroupDemand(group)
			if demand.Validate(false) != nil {
				return base, false
			}
			needed := 0
			for _, target := range allowed {
				load := loads[target]
				capacity := load.PodCapacity
				if !load.HeartbeatOK || capacity == nil || capacity.State != "ready" || capacity.Schedulable.Validate(true) != nil || !compatibility.targetCanRun(target, group.Protocol, groupRequiredRuntimeFeature(group), effectiveGroupRequiredLabels(group, test.Assignment)) {
					continue
				}
				dimensions := capacity.Schedulable.Dimensions()
				ratio := 0.0
				compatible := true
				for dimension, value := range demand.Dimensions() {
					if value <= 0 {
						continue
					}
					budget := dimensions[dimension]
					if budget <= 0 {
						compatible = false
						break
					}
					ratio = math.Max(ratio, value/budget)
				}
				if !compatible {
					continue
				}
				needed = max(needed, int(math.Ceil(math.Min(float64(maximum), ratio))))
				eligiblePods[target] = placements[target]
				// Retain the observed StatefulSet ordinal even when discovery uses IPs.
				retained = append(retained, storage.PodCapacityReservation{Target: target, RecoveryPlacement: &storage.PodCapacityRecoveryPlacement{PodName: placements[target].Name}})
			}
			if needed == 0 {
				return base, false
			}
			sources = min(maximum, sources+needed)
		}
		floor = min(maximum, floor+min(maximum, 2*sources))
		// Existing pods can all fit the VUser envelope but share one worker.
		// Ask for new ordinals beyond those already observed; otherwise a small
		// queued run can wait forever for a cross-worker spare that KEDA never
		// creates. This is only a request: admission still checks actual placement.
		if extra := singleWorkerRecoveryReplicaFloor(eligiblePods, observedFloor, sources, maximum); extra > floor {
			floor = extra
		}
		floor = max(floor, min(maximum, reservedReplicaFloor(retained, 0)))
	}
	return max(base, floor), true
}

// Unknown node names cannot establish a single-worker observation. observedFloor
// includes ineligible ready pods too: an existing incompatible pod does not count
// as a spare, but asking for its ordinal again cannot create a usable replacement.
func singleWorkerRecoveryReplicaFloor(pods map[string]*GeneratorPodPlacement, observedFloor, sources, maximum int) int {
	if len(pods) == 0 || sources <= 0 || maximum <= 0 {
		return 0
	}
	node := ""
	for _, p := range pods {
		if p == nil || strings.TrimSpace(p.NodeName) == "" {
			return 0
		}
		if node != "" && node != p.NodeName {
			return 0
		}
		node = p.NodeName
	}
	present := min(maximum, max(len(pods), observedFloor))
	return present + min(maximum-present, sources)
}
