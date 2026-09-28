package api

import (
	"math"
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
		loads[e.Target] = generatorLoadSnapshot{Target: e.Target, HeartbeatOK: generatorInventoryEntryReachable(e), PodCapacity: e.PodCapacity, CPULimitCores: e.CPULimitCores, MemoryLimitBytes: e.MemoryLimitBytes, MaxVUsers: e.MaxVUsers, SupportedProtocols: e.SupportedProtocols, CompatibleFeatures: e.CompatibleFeatures, RuntimeCapacities: e.RuntimeCapacities}
	}
	if limit.MaximumReplicas <= 0 {
		return base, false
	}
	configurePodLoads(settings, loads)
	maximum := limit.MaximumReplicas
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
				if capacity == nil || capacity.State != "ready" || capacity.Schedulable.Validate(true) != nil {
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
				retained = append(retained, storage.PodCapacityReservation{Target: target})
			}
			if needed == 0 {
				return base, false
			}
			sources = min(maximum, sources+needed)
		}
		floor = min(maximum, floor+min(maximum, 2*sources))
		floor = max(floor, min(maximum, reservedReplicaFloor(retained, 0)))
	}
	return max(base, floor), true
}
