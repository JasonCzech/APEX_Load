package api

import (
	"context"
	"fmt"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

func clonePodReservation(r storage.PodCapacityReservation) storage.PodCapacityReservation {
	out := r
	out.Budget = r.Budget.Scale(1)
	out.Demand = r.Demand.Scale(1)
	out.GroupPeaks = make(map[string]int32, len(r.GroupPeaks))
	for name, n := range r.GroupPeaks {
		out.GroupPeaks[name] = n
	}
	out.GroupDemands = make(map[string]protocol.CapacityVector, len(r.GroupDemands))
	for name, d := range r.GroupDemands {
		out.GroupDemands[name] = d.Scale(1)
	}
	return out
}

// expandPodScale reserves the full delta before any scale RPC. The caller holds
// podCapacityMutationMu through its scale/rollback, serializing sibling groups.
func (tr *TestRunner) expandPodScale(ctx context.Context, testID, group string, shards []GeneratorShardSummary, clients map[string]generatorClient) (func(), error) {
	noop := func() {}
	store, ok := tr.testStore.(podCapacityStore)
	if !ok {
		return noop, nil
	}
	rows, err := store.ListPodCapacityReservations()
	if err != nil {
		return noop, err
	}
	byTarget := map[string]storage.PodCapacityReservation{}
	for _, r := range rows {
		if r.TestID == testID {
			byTarget[r.Target] = r
		}
	}
	if len(byTarget) == 0 {
		return noop, nil
	}
	generation, owner, _, err := tr.generatorExecutionClaim()
	if err != nil {
		return noop, err
	}
	var previous, next []storage.PodCapacityReservation
	for _, shard := range shards {
		r, ok := byTarget[shard.Target]
		if !ok {
			return noop, fmt.Errorf("generator %s has no peak reservation", shard.Target)
		}
		peak, ok := r.GroupPeaks[group]
		if !ok || peak <= 0 {
			return noop, fmt.Errorf("group %s has no frozen peak estimate", group)
		}
		if shard.VUsers <= int(peak) {
			continue
		}
		if r.Generation != generation {
			return noop, fmt.Errorf("reservation belongs to a previous execution owner")
		}
		previous = append(previous, r)
		updated := clonePodReservation(r)
		unit, known := r.GroupDemands[group]
		if !known {
			return noop, fmt.Errorf("group %s frozen estimate unavailable", group)
		}
		newDemand := unit.Scale(float64(shard.VUsers) / float64(peak))
		if !r.Dedicated {
			updated.Demand = updated.Demand.Add(newDemand.Add(unit.Scale(-1)))
		}
		updated.GroupDemands[group] = newDemand
		updated.GroupPeaks[group] = int32(shard.VUsers)
		updated.Revision++
		var groupDemand protocol.CapacityVector
		for _, d := range updated.GroupDemands {
			groupDemand = groupDemand.Add(d)
		}
		if err := updated.Budget.Fits(groupDemand); err != nil {
			return noop, err
		}
		next = append(next, updated)
	}
	if len(next) == 0 {
		return noop, nil
	}
	if err := tr.reservePodLedger(store, next, executionLeaseName, owner, generation); err != nil {
		return noop, err
	}
	rollback := func() {
		// Confirm the proposed revision first so an ambiguous earlier RPC cannot
		// make a rollback skip an incarnation/revision. Failure retains capacity.
		for i, r := range next {
			client, ok := clients[r.Target].(podCapacityClient)
			if !ok {
				continue
			}
			callCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			ack, err := client.ReservePodCapacity(callCtx, podClaim(r, false))
			if err == nil && podCapacityAckConfirms(ack, r.Incarnation) {
				restore := clonePodReservation(previous[i])
				restore.Revision = r.Revision + 1
				ack, err = client.ReservePodCapacity(callCtx, podClaim(restore, false))
				if err == nil && podCapacityAckConfirms(ack, r.Incarnation) {
					_ = tr.reservePodLedger(store, []storage.PodCapacityReservation{restore}, executionLeaseName, owner, generation)
				}
			}
			cancel()
		}
	}
	for _, r := range next {
		client, ok := clients[r.Target].(podCapacityClient)
		if !ok {
			rollback()
			return noop, fmt.Errorf("generator reservation client unavailable")
		}
		callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		ack, err := client.ReservePodCapacity(callCtx, podClaim(r, false))
		cancel()
		if err != nil {
			rollback()
			return noop, err
		}
		if !podCapacityAckConfirms(ack, r.Incarnation) {
			rollback()
			return noop, fmt.Errorf("generator %s did not confirm capacity: %s", r.Target, ack.GetErrorMessage())
		}
	}
	return rollback, nil
}
