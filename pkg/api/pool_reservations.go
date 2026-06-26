package api

// Per-pool capacity reservations. A reservation/timeslot scoped to a fleet pool
// (PoolID set) protects capacity within that pool's member envelope, mirroring the
// global shared-pool admission logic (evaluateReservedCapacityBlockLocked) but
// bounded by the pool's generators and counting only that pool's runs. This is an
// ADDITIVE gate: a pool-assigned run must clear both the global reservation check
// and this one.

import (
	"fmt"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// poolCapacityGeneratorsLocked is a fleet pool's capacity envelope: the number of
// member generator targets it can place runs on.
func (tr *TestRunner) poolCapacityGeneratorsLocked(poolID string) int {
	if poolID == "" {
		return 0
	}
	return len(tr.poolMemberTargets(poolID))
}

// activePoolGeneratorsForProjectLocked sums the recommended generators of a
// project's live runs pinned to a pool (excluding excludeTestID).
func (tr *TestRunner) activePoolGeneratorsForProjectLocked(poolID, projectID, excludeTestID string) int {
	projectID = normalizeProjectID(projectID)
	total := 0
	for id, test := range tr.tests {
		if test == nil || id == excludeTestID || !reservationActiveStatus(test.Status) {
			continue
		}
		if testAssignedPoolID(test) != poolID || normalizeProjectID(test.ProjectID) != projectID {
			continue
		}
		total += recommendedGeneratorCountForTest(test)
	}
	return total
}

// poolSharedGeneratorUsageLocked sums every project's draw on a pool's shared
// (unreserved) capacity: each project's active generators on the pool above its
// own pool reservation. Mirrors sharedGeneratorUsageLocked, scoped to one pool.
func (tr *TestRunner) poolSharedGeneratorUsageLocked(poolID, excludeTestID string, at time.Time, activeSlots []*storage.CapacityReservation) int {
	byProject := make(map[string]int)
	for id, test := range tr.tests {
		if test == nil || id == excludeTestID || !reservationActiveStatus(test.Status) {
			continue
		}
		if testAssignedPoolID(test) != poolID {
			continue
		}
		byProject[normalizeProjectID(test.ProjectID)] += recommendedGeneratorCountForTest(test)
	}
	total := 0
	for projectID, active := range byProject {
		_, reserved := poolReservationTotalsWithTimeslots(tr.settings, projectID, poolID, at, activeSlots)
		total += maxInt(0, active-reserved)
	}
	return total
}

// evaluatePoolReservedCapacityBlockLocked holds a pool-pinned run when admitting
// it would push the pool's shared (unreserved) usage past what is left after other
// projects' pool reservations — protecting reserved pool capacity, exactly like the
// global reserved-capacity gate but within the pool's member envelope.
func (tr *TestRunner) evaluatePoolReservedCapacityBlockLocked(test *ManagedTest, excludeTestID string) (bool, string) {
	poolID := testAssignedPoolID(test)
	if poolID == "" {
		return false, ""
	}
	now := time.Now()
	activeSlots, err := tr.boundTimeslotsActiveAtLocked(now)
	if err != nil {
		// Fail closed: a pool reservation we cannot read may be protecting capacity.
		return true, "waiting for reservation state to become available"
	}
	totalReserved, projectReserved := poolReservationTotalsWithTimeslots(tr.settings, normalizeProjectID(test.ProjectID), poolID, now, activeSlots)
	if totalReserved <= 0 {
		return false, "" // no pool reservations -> nothing to protect
	}
	envelope := tr.poolCapacityGeneratorsLocked(poolID)
	if envelope <= 0 {
		return false, "" // unknown pool size -> don't gate on reservations
	}
	if totalReserved > envelope {
		totalReserved = envelope
	}
	shared := maxInt(0, envelope-totalReserved)

	projectActive := tr.activePoolGeneratorsForProjectLocked(poolID, normalizeProjectID(test.ProjectID), excludeTestID)
	g := recommendedGeneratorCountForTest(test)
	sharedDelta := maxInt(0, projectActive+g-projectReserved) - maxInt(0, projectActive-projectReserved)

	sharedUsedTotal := tr.poolSharedGeneratorUsageLocked(poolID, excludeTestID, now, activeSlots)
	if sharedUsedTotal+sharedDelta > shared {
		return true, fmt.Sprintf("waiting for shared generator capacity on pool %q (%d of %d generators reserved for other projects)", poolID, totalReserved, envelope)
	}
	return false, ""
}
