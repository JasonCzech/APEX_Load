package api

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

type podCapacityCompareStore interface {
	ReservePodCapacityIfUnchanged([]storage.PodCapacityReservation, []storage.PodCapacityReservation, string, string, uint64) error
}

func (tr *TestRunner) standbyEvacuationReservations(testID string, generation uint64) (map[string]storage.PodCapacityReservation, error) {
	store, ok := tr.testStore.(podCapacityStore)
	if !ok {
		return nil, nil
	}
	rows, err := store.ListPodCapacityReservations()
	if err != nil {
		return nil, err
	}
	result := map[string]storage.PodCapacityReservation{}
	for _, row := range rows {
		if row.TestID != testID || !standbyCapacityReservation(row) {
			continue
		}
		if row.State != "committed" || row.Generation != generation || generation == 0 {
			return nil, fmt.Errorf("standby %s has unconfirmed capacity ownership", row.Target)
		}
		result[row.Target] = row
		if len(result) > maxRecoveryCapacityPlanPods {
			return nil, errors.New("standby fleet exceeds bounded recovery capacity")
		}
	}
	return result, nil
}

// This is private to evacuation. A standby's budget is never exposed as free
// capacity to ordinary admission or to a different test. Callers supply a fresh
// heartbeat; activation still requires the exact ledger CAS and generator RPC.
func standbyEvacuationLoad(load generatorLoadSnapshot, claim storage.PodCapacityReservation, testID string, generation uint64) generatorLoadSnapshot {
	if claim.TestID == "" {
		return load
	}
	load.AvailableCapacity = 0
	capacity := load.PodCapacity
	if claim.TestID != testID || claim.Target != load.Target || claim.Generation != generation || generation == 0 ||
		claim.State != "committed" || !standbyCapacityReservation(claim) || claim.ReservationID == "" || claim.Incarnation == "" || claim.Revision == 0 || claim.Revision >= math.MaxUint64-1 ||
		!load.HeartbeatOK || load.Stats.ActiveVUsers != 0 || load.MaxVUsers <= 0 ||
		!stringSliceContainsEqualFold(load.CompatibleFeatures, generatorrpc.FeaturePodCapacityStandbyV1) || !stringSliceContainsEqualFold(load.CompatibleFeatures, generatorrpc.FeaturePodCapacityV1) ||
		capacity == nil || capacity.State != "ready" || !capacity.Dedicated || capacity.Incarnation != claim.Incarnation || capacity.HeadroomPct != claim.HeadroomPct ||
		len(capacity.ReportedTestIDs) != 1 || capacity.ReportedTestIDs[0] != testID || claim.Budget.Validate(true) != nil || claim.ModelVersion != protocol.CapacityReservationModel ||
		!sameCapacityVector(claim.Budget, claim.Demand) || !sameCapacityVector(claim.Budget, capacity.Schedulable) || !sameCapacityVector(claim.Demand, capacity.Reserved) {
		return load
	}
	observed := time.UnixMilli(capacity.ObservedAtUnixMS)
	if capacity.ObservedAtUnixMS <= 0 || observed.After(time.Now()) || time.Since(observed) > defaultGeneratorSnapshotTTL {
		return load
	}
	load.AvailableCapacity = int32(min(claim.Budget.VUsers, float64(load.MaxVUsers)))
	return load
}

func sameCapacityVector(a, b protocol.CapacityVector) bool {
	return reflect.DeepEqual(a.Scale(1), b.Scale(1))
}

func (tr *TestRunner) reserveEvacuationPodLedger(store podCapacityStore, claims, expected []storage.PodCapacityReservation, owner string, generation uint64) error {
	if len(expected) == 0 {
		return tr.reservePodLedger(store, claims, executionLeaseName, owner, generation)
	}
	compare, ok := store.(podCapacityCompareStore)
	if !ok {
		return errors.New("capacity store cannot atomically activate standbys")
	}
	return tr.trackDurableWrite(func() error {
		return compare.ReservePodCapacityIfUnchanged(claims, expected, executionLeaseName, owner, generation)
	})
}

// Rollback restores a standby's full idle reservation instead of making its
// promised budget available to another run. Uncertain RPCs or a changed ledger
// retain the complete budget for reconciliation under the execution fence.
func (tr *TestRunner) rollbackEvacuationPodClaims(b *distributedExecutionBackend, claims []storage.PodCapacityReservation, standbys map[string]storage.PodCapacityReservation) error {
	var ordinary []storage.PodCapacityReservation
	var reserved []storage.PodCapacityReservation
	for _, claim := range claims {
		if _, ok := standbys[claim.Target]; ok {
			reserved = append(reserved, claim)
		} else {
			ordinary = append(ordinary, claim)
		}
	}
	if len(ordinary) > 0 {
		tr.releasePodClaimsWithDial(b, ordinary)
	}
	if len(reserved) == 0 {
		return nil
	}
	store, ok := tr.testStore.(podCapacityStore)
	if !ok {
		return errors.New("capacity ledger unavailable during standby rollback")
	}
	compare, ok := store.(podCapacityCompareStore)
	if !ok {
		return errors.New("capacity ledger cannot restore standbys atomically")
	}
	generation, owner, _, err := tr.generatorExecutionClaim()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var failures []error
	for _, claim := range reserved {
		if claim.Generation != generation || claim.Revision == math.MaxUint64 {
			failures = append(failures, errors.New("standby rollback owner or revision changed"))
			continue
		}
		if err := ctx.Err(); err != nil {
			failures = append(failures, err)
			break
		}
		client, err := b.dial(ctx, claim.Target)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		func() {
			defer client.Close()
			capacity, ok := client.(podCapacityClient)
			if !ok {
				failures = append(failures, errors.New("standby rollback RPC unavailable"))
				return
			}
			// A lost activation response leaves the generator at either revision.
			// Confirm activation idempotently before submitting the idle next revision.
			ack, err := capacity.ReservePodCapacity(ctx, podClaim(claim, false))
			if err != nil || !podCapacityAckConfirms(ack, claim.Incarnation) {
				failures = append(failures, errors.New("standby activation remains unconfirmed during rollback"))
				return
			}
			idle := claim
			idle.Revision++
			idle.GroupPeaks = nil
			idle.GroupDemands = nil
			idle.Demand = idle.Budget
			idle.State = "pending"
			ack, err = capacity.ReservePodCapacity(ctx, podClaim(idle, false))
			if err != nil || !podCapacityAckConfirms(ack, claim.Incarnation) {
				failures = append(failures, errors.New("standby idle rollback remains unconfirmed"))
				return
			}
			if err := tr.trackDurableWrite(func() error {
				return compare.ReservePodCapacityIfUnchanged([]storage.PodCapacityReservation{idle}, []storage.PodCapacityReservation{claim}, executionLeaseName, owner, generation)
			}); err != nil {
				failures = append(failures, err)
				return
			}
			if err := tr.transitionPodLedger(store, idle.Target, idle.TestID, idle.Incarnation, idle.ReservationID, "committed", executionLeaseName, owner, generation); err != nil {
				failures = append(failures, err)
			}
		}()
	}
	return errors.Join(failures...)
}
