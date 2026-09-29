package api

import (
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/kubeops"
	"github.com/jasonczech/apexload/pkg/storage"
)

type recoveryPodSnapshot struct {
	pods       []KubePodRef
	limit      kubeops.StatefulSetAutoscalingLimit
	observedAt time.Time
	available  bool
	oversized  bool
}

// The Kubernetes list includes occupied ordinals that headless DNS omits.
// Bind every counted pod to the same StatefulSet incarnation as the HPA/KEDA
// limit; a name alone cannot prove which workload owns an ordinal.
func observedAutoscalingPodReservations(snapshot recoveryPodSnapshot, limit kubeops.StatefulSetAutoscalingLimit) ([]storage.PodCapacityReservation, map[string]string, bool) {
	if snapshot.oversized || !snapshot.available || snapshot.limit != limit {
		return nil, nil, false
	}
	reservations := make([]storage.PodCapacityReservation, 0, len(snapshot.pods))
	identities := make(map[string]string, len(snapshot.pods))
	for _, pod := range snapshot.pods {
		if pod.Namespace != limit.Namespace || pod.StatefulSetUID != limit.WorkloadUID || pod.StatefulSetName != limit.Workload {
			continue
		}
		ordinal, err := strconv.Atoi(strings.TrimPrefix(pod.Name, limit.Workload+"-"))
		if pod.UID == "" || err != nil || ordinal < 0 || ordinal >= maxRecoveryCapacityPlanPods || pod.Name != limit.Workload+"-"+strconv.Itoa(ordinal) {
			return nil, nil, false
		}
		if _, duplicate := identities[pod.Name]; duplicate {
			return nil, nil, false
		}
		identities[pod.Name] = pod.UID
		reservations = append(reservations, storage.PodCapacityReservation{Target: pod.Name, RecoveryPlacement: &storage.PodCapacityRecoveryPlacement{PodName: pod.Name}})
	}
	return reservations, identities, true
}

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
	tr.liveInventoryMu.RLock()
	podSnapshot := tr.livePodSnapshot
	podSnapshot.pods = cloneKubePodRefs(podSnapshot.pods)
	tr.liveInventoryMu.RUnlock()
	if podSnapshot.available && (podSnapshot.observedAt != at || podSnapshot.oversized) {
		return base, false
	}
	loads := map[string]generatorLoadSnapshot{}
	placements := map[string]*GeneratorPodPlacement{}
	var observedPods []storage.PodCapacityReservation
	var limit kubeops.StatefulSetAutoscalingLimit
	for _, e := range entries {
		p := e.PodPlacement
		if p == nil || !recoveryScaleLimitFresh(p.scaleLimit) {
			continue
		}
		if limit.WorkloadUID != "" && limit != p.scaleLimit {
			return base, false
		}
		limit = p.scaleLimit
		// An identified NotReady or terminating ordinal still occupies a StatefulSet replica.
		// Count it before checking execution eligibility so a request for one
		// more usable spare asks KEDA for a genuinely new ordinal.
		observedPods = append(observedPods, storage.PodCapacityReservation{Target: p.Name, RecoveryPlacement: &storage.PodCapacityRecoveryPlacement{PodName: p.Name}})
		if !p.Ready || p.Terminating || !generatorInventoryEntryReachable(e) || e.Cordoned || e.DrainState != "active" || e.CompatibilityState != "compatible" || b.generatorDrainState(e.Target) != "active" {
			continue
		}
		placements[e.Target] = p
		loads[e.Target] = generatorLoadSnapshot{Target: e.Target, HeartbeatOK: generatorInventoryEntryReachable(e), PodCapacity: e.PodCapacity, CPULimitCores: e.CPULimitCores, MemoryLimitBytes: e.MemoryLimitBytes, MaxVUsers: e.MaxVUsers, SupportedProtocols: e.SupportedProtocols, CompatibleFeatures: e.CompatibleFeatures, RuntimeCapacities: e.RuntimeCapacities}
	}
	if limit.MaximumReplicas <= 0 {
		return base, false
	}
	if podSnapshot.available {
		var identities map[string]string
		var complete bool
		observedPods, identities, complete = observedAutoscalingPodReservations(podSnapshot, limit)
		if !complete {
			return base, false
		}
		for _, entry := range entries {
			p := entry.PodPlacement
			if p != nil && p.scaleLimit == limit && identities[p.Name] != p.UID {
				return base, false
			}
		}
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
		// The ordinary 2*sources floor counts replicas, not usable pods. When
		// existing incompatible or NotReady ordinals already occupy that floor,
		// request enough additional ordinals to cover the missing eligible pods.
		if missing := min(maximum, 2*sources) - len(eligiblePods); missing > 0 {
			floor = max(floor, min(maximum, observedFloor+missing))
		}
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
