package api

import (
	"context"
	"fmt"
	"sort"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

func (tr *TestRunner) reservePodEvacuation(ctx context.Context, b *distributedExecutionBackend, testID, source string, next map[string][]GeneratorShardSummary, clients map[string]generatorClient, dryRun bool) ([]storage.PodCapacityReservation, error) {
	store, ok := tr.testStore.(podCapacityStore)
	if !ok {
		return nil, nil
	}
	rows, err := store.ListPodCapacityReservations()
	if err != nil {
		return nil, err
	}
	var original *storage.PodCapacityReservation
	for i := range rows {
		if rows[i].TestID == testID && rows[i].Target == source {
			original = &rows[i]
			break
		}
	}
	if original == nil {
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
					alreadyPresent = true
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
			if capacity == nil || capacity.State != "ready" || capacity.Dedicated {
				return nil, fmt.Errorf("replacement peak capacity unavailable")
			}
			r = &storage.PodCapacityReservation{ReservationID: original.ReservationID, Target: destination, TestID: testID, Incarnation: capacity.Incarnation, Generation: original.Generation, State: "pending", ModelVersion: original.ModelVersion, HeadroomPct: capacity.HeadroomPct, Budget: capacity.Schedulable, Dedicated: original.Dedicated, Revision: 1, GroupPeaks: map[string]int32{}, GroupDemands: map[string]protocol.CapacityVector{}}
			byTarget[destination] = r
			occupied[destination] = capacity.Reserved
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
	if err := tr.reservePodLedger(store, claims, executionLeaseName, owner, generation); err != nil {
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
		if !ack.Accepted {
			return claims, fmt.Errorf("replacement %s: %s", r.Target, ack.ErrorMessage)
		}
	}
	return claims, nil
}
