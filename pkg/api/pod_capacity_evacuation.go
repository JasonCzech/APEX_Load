package api

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

func (tr *TestRunner) reservePodEvacuation(ctx context.Context, b *distributedExecutionBackend, testID, source string, next map[string][]GeneratorShardSummary, clients map[string]generatorClient, dryRun bool, standbys map[string]storage.PodCapacityReservation) ([]storage.PodCapacityReservation, error) {
	// Bound the complete heartbeat/activation attempt while topology and capacity
	// mutation locks are held; a missing acknowledgment retains pending claims.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	store, ok := tr.testStore.(podCapacityStore)
	if !ok {
		if len(standbys) > 0 {
			return nil, fmt.Errorf("capacity ledger unavailable for standby activation")
		}
		return nil, nil
	}
	rows, err := store.ListPodCapacityReservations()
	if err != nil {
		return nil, err
	}
	var original *storage.PodCapacityReservation
	var expected []storage.PodCapacityReservation
	for i := range rows {
		if rows[i].TestID == testID && rows[i].Target == source {
			original = &rows[i]
			break
		}
	}
	if original == nil {
		if len(standbys) > 0 {
			return nil, fmt.Errorf("source peak reservation unavailable for standby activation")
		}
		return nil, nil
	}
	settings := tr.GetSystemSettings()
	byTarget := map[string]*storage.PodCapacityReservation{}
	occupied := map[string]protocol.CapacityVector{}
	for group, peak := range original.GroupPeaks {
		if peak <= 0 {
			continue
		}
		var destination string
		for _, shard := range next[group] {
			if shard.Target == source {
				return nil, fmt.Errorf("group %s still needs its source peak reservation", group)
			}
			alreadyPresent := false
			for _, r := range rows {
				if r.TestID == testID && r.Target == shard.Target {
					standby, reserved := standbys[shard.Target]
					alreadyPresent = !reserved || !reflect.DeepEqual(r, standby)
					break
				}
			}
			if !alreadyPresent {
				destination = shard.Target
				break
			}
		}
		if destination == "" {
			return nil, fmt.Errorf("group %s has no reserved replacement", group)
		}
		r := byTarget[destination]
		if r == nil {
			client := clients[destination]
			if client == nil {
				return nil, fmt.Errorf("replacement client unavailable")
			}
			load := b.probeGeneratorLoad(ctx, destination, client)
			capacity := configurePodCapacity(settings, load)
			standby, reserved := standbys[destination]
			if original.RecoveryPlacement != nil || standby.RecoveryPlacement != nil {
				sourceProof, spareProof := original.RecoveryPlacement, standby.RecoveryPlacement
				if !reserved || sourceProof == nil || spareProof == nil || sourceProof.Namespace != spareProof.Namespace || sourceProof.WorkloadUID != spareProof.WorkloadUID || sourceProof.NodeName == spareProof.NodeName || !slices.Contains(spareProof.ReplacementSources, source) || !tr.recoveryStandbyIdentityCurrent(standby) {
					return nil, fmt.Errorf("replacement does not match the frozen worker recovery placement")
				}
			}
			if reserved {
				fresh := standbyEvacuationLoad(load, standby, testID, original.Generation)
				if fresh.AvailableCapacity <= 0 {
					return nil, fmt.Errorf("replacement standby capacity is no longer confirmed")
				}
			}
			if capacity == nil || capacity.State != "ready" || (capacity.Dedicated && !reserved) {
				return nil, fmt.Errorf("replacement peak capacity unavailable")
			}
			r = &storage.PodCapacityReservation{ReservationID: original.ReservationID, Target: destination, TestID: testID, Incarnation: capacity.Incarnation, Generation: original.Generation, State: "pending", ModelVersion: original.ModelVersion, HeadroomPct: capacity.HeadroomPct, Budget: capacity.Schedulable, Dedicated: original.Dedicated, Revision: 1, GroupPeaks: map[string]int32{}, GroupDemands: map[string]protocol.CapacityVector{}}
			byTarget[destination] = r
			occupied[destination] = capacity.Reserved
			if reserved {
				copy := standby
				copy.State = "pending"
				copy.Revision++
				copy.GroupPeaks = map[string]int32{}
				copy.GroupDemands = map[string]protocol.CapacityVector{}
				copy.Demand = protocol.CapacityVector{}
				r = &copy
				byTarget[destination] = r
				occupied[destination] = protocol.CapacityVector{}
				expected = append(expected, standby)
			}
		}
		demand, ok := original.GroupDemands[group]
		if !ok {
			return nil, fmt.Errorf("frozen group estimate unavailable")
		}
		r.Demand = r.Demand.Add(demand)
		r.GroupPeaks[group] = peak
		r.GroupDemands[group] = demand
	}
	var claims []storage.PodCapacityReservation
	for _, r := range byTarget {
		if err := r.Budget.Fits(r.Demand); err != nil {
			return nil, err
		}
		if r.Dedicated {
			r.Demand = r.Budget
		}
		if err := r.Budget.Fits(occupied[r.Target].Add(r.Demand)); err != nil {
			return nil, fmt.Errorf("replacement %s: %w", r.Target, err)
		}
		claims = append(claims, *r)
	}
	sort.Slice(claims, func(i, j int) bool { return claims[i].Target < claims[j].Target })
	if dryRun {
		return nil, nil
	}
	generation, owner, _, err := tr.generatorExecutionClaim()
	if err != nil {
		return nil, err
	}
	if err := tr.reserveEvacuationPodLedger(store, claims, expected, owner, generation); err != nil {
		return nil, err
	}
	for _, r := range claims {
		client, ok := clients[r.Target].(podCapacityClient)
		if !ok {
			return claims, fmt.Errorf("replacement lacks pod capacity capability")
		}
		ack, err := client.ReservePodCapacity(ctx, podClaim(r, false))
		if err != nil {
			return claims, err
		}
		if !podCapacityAckConfirms(ack, r.Incarnation) {
			return claims, fmt.Errorf("replacement %s did not confirm capacity: %s", r.Target, ack.GetErrorMessage())
		}
	}
	return claims, nil
}
