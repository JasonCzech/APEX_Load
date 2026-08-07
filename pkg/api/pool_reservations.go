package api

// Per-pool capacity reservations. A reservation/timeslot scoped to a fleet pool
// (PoolID set) protects capacity within that pool's member envelope, mirroring the
// global shared-pool admission logic (evaluateReservedCapacityBlockLocked) but
// bounded by the pool's generators and counting only that pool's runs. This is an
// ADDITIVE gate: a pool-assigned run must clear both the global reservation check
// and this one.

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// PoolGeneratorEnvelope returns a fleet pool's capacity envelope (its member
// generator count). Safe to call without holding tr.mu; takes the read lock
// internally so callers outside the admission path (e.g. the timeslot approval
// guard) can size a pool-scoped reservation against the same envelope admission
// uses.
func (tr *TestRunner) PoolGeneratorEnvelope(poolID string) int {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	return tr.poolCapacityGeneratorsLocked(poolID)
}

// PoolGeneratorEnvelopeSnapshot resolves every fleet pool's capacity before a
// caller enters a storage transaction. Approval paths must not call back into
// the same SQLite store while holding that transaction's only connection.
func (tr *TestRunner) PoolGeneratorEnvelopeSnapshot() (map[string]int, error) {
	tr.mu.RLock()
	store := tr.fleetStore
	tr.mu.RUnlock()
	if store == nil {
		return map[string]int{}, nil
	}
	pools, err := store.ListFleetPools(0, 0)
	if err != nil {
		return nil, err
	}
	metadata, err := store.ListGeneratorMetadata(0, 0)
	if err != nil {
		return nil, err
	}
	targetsByPool := fleetPoolTargetsByPool(pools, metadata)
	envelopes := make(map[string]int, len(targetsByPool))
	for poolID, targets := range targetsByPool {
		envelopes[poolID] = len(targets)
	}
	return envelopes, nil
}

// FleetPoolExists reports whether a fleet pool with the given id is known. A
// blank id or an unconfigured fleet store reports "exists" (nothing to validate
// against). A definitive not-found reports false; any other (transient) store
// error reports true so a write isn't rejected on a momentary lookup failure.
func (tr *TestRunner) FleetPoolExists(poolID string) bool {
	poolID = strings.TrimSpace(poolID)
	if poolID == "" || tr.fleetStore == nil {
		return true
	}
	pool, err := tr.fleetStore.GetFleetPool(poolID)
	if err != nil {
		return !errors.Is(err, sql.ErrNoRows)
	}
	return pool != nil
}

// validateReservationPools rejects a settings update that references a fleet pool
// which does not exist. Without this a pool-scoped reservation with a typo'd or
// stale PoolID is silently inert — excluded from the global reservation math yet
// bounded by an empty/unknown pool envelope at admission, so it protects nothing.
// Skipped when no fleet store is configured.
func (tr *TestRunner) validateReservationPools(settings SystemSettings) error {
	if tr.fleetStore == nil {
		return nil
	}
	for _, reservation := range settings.CapacityPlanning.Reservations {
		poolID := strings.TrimSpace(reservation.PoolID)
		if poolID == "" {
			continue
		}
		// Zero generators is the delete-row idiom (normalize-on-write drops the
		// row before persisting), so deleting a reservation whose pool has since
		// been removed must not be blocked by the stale pool reference.
		if reservation.ReservedGenerators == 0 {
			continue
		}
		if !tr.FleetPoolExists(poolID) {
			return fmt.Errorf("capacity_planning.reservations references unknown fleet pool %q", poolID)
		}
	}
	return nil
}

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

func (tr *TestRunner) evaluatePoolReservedCapacityBlockWithTimeslotsLocked(test *ManagedTest, excludeTestID string, now time.Time, activeSlots []*storage.CapacityReservation, reservationErr error) (bool, string) {
	poolID := testAssignedPoolID(test)
	if poolID == "" {
		return false, ""
	}
	if reservationErr != nil {
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
	if sharedDelta > 0 && sharedUsedTotal+sharedDelta > shared {
		return true, fmt.Sprintf("waiting for shared generator capacity on pool %q (%d of %d generators reserved for other projects)", poolID, totalReserved, envelope)
	}
	return false, ""
}
