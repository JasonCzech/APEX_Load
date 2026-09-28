package api

import "github.com/jasonczech/apexload/pkg/storage"

type capacityReleaseCandidate struct {
	entry GeneratorInventoryEntry
	count int
}

type capacityReleaseInventory struct {
	byTarget map[string]capacityReleaseCandidate
	byPodUID map[string]capacityReleaseCandidate
}

// Index once per reconciliation, keeping the sweep linear in the inventory and
// claim counts. Duplicates remain ambiguous instead of overwriting evidence.
func newCapacityReleaseInventory(entries []GeneratorInventoryEntry) capacityReleaseInventory {
	index := capacityReleaseInventory{byTarget: make(map[string]capacityReleaseCandidate), byPodUID: make(map[string]capacityReleaseCandidate)}
	add := func(dest map[string]capacityReleaseCandidate, key string, entry GeneratorInventoryEntry) {
		candidate := dest[key]
		candidate.count++
		candidate.entry = entry
		dest[key] = candidate
	}
	for _, entry := range entries {
		add(index.byTarget, entry.Target, entry)
		if entry.PodPlacement != nil {
			add(index.byPodUID, entry.PodPlacement.UID, entry)
		}
	}
	return index
}

// observation follows a Kubernetes-bound claim across transport address changes.
// A reused IP or pod name cannot replace the frozen pod UID. Missing, stale or
// ambiguous evidence leaves the durable reservation held.
func (index capacityReleaseInventory) observation(claim storage.PodCapacityReservation) (GeneratorInventoryEntry, bool) {
	proof := claim.RecoveryPlacement
	candidate := index.byTarget[claim.Target]
	if proof != nil {
		if proof.PodUID == "" || proof.Namespace == "" || proof.WorkloadUID == "" || proof.NodeName == "" {
			return GeneratorInventoryEntry{}, false
		}
		candidate = index.byPodUID[proof.PodUID]
	}
	if candidate.count != 1 {
		return GeneratorInventoryEntry{}, false
	}
	entry := candidate.entry
	if proof != nil {
		p := entry.PodPlacement
		if p == nil || p.UID != proof.PodUID || p.Namespace != proof.Namespace || p.NodeName != proof.NodeName || p.scaleLimit.Namespace != proof.Namespace || p.scaleLimit.WorkloadUID != proof.WorkloadUID || (proof.PodName != "" && p.Name != proof.PodName) || !p.Ready || p.Terminating || !recoveryScaleLimitFresh(p.scaleLimit) {
			return GeneratorInventoryEntry{}, false
		}
	}
	if entry.Target == "" || index.byTarget[entry.Target].count != 1 || !generatorInventoryEntryReachable(entry) || entry.PodCapacity == nil || entry.PodCapacity.Incarnation == "" {
		return GeneratorInventoryEntry{}, false
	}
	return entry, true
}
