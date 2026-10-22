package api

import (
	"context"
	"fmt"
	"sort"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/storage"
)

// adoptPodCapacity confirms each live or standby claim under the new epoch
// before changing durable ownership. Live assignments have already been adopted.
// An older generator rejects the unchanged claim with a new generation, so mixed
// versions fail closed. Completed and
// retired participants keep their reservations until ordinary confirmed release.
func (tr *TestRunner) adoptPodCapacity(ctx context.Context, testID string, adopted map[string]generatorClient, generation uint64, owner string) error {
	store, ok := tr.testStore.(podCapacityStore)
	if !ok {
		return nil
	}
	rows, err := store.ListPodCapacityReservations()
	if err != nil {
		return err
	}
	var claims []storage.PodCapacityReservation
	for _, row := range rows {
		if row.TestID == testID && standbyCapacityReservation(row) && adopted[row.Target] == nil {
			return fmt.Errorf("standby %s was not included in the epoch claim", row.Target)
		}
		if row.TestID == testID && adopted[row.Target] != nil {
			if standbyCapacityReservation(row) && row.State == "committed" && row.Generation == generation {
				// An evacuation rollback already reserved this idle standby
				// under the new epoch and confirmed it with the generator.
				continue
			}
			if row.State != "committed" || row.Generation == 0 || row.Generation >= generation {
				return fmt.Errorf("reservation for %s has an unexpected state or owner", row.Target)
			}
			claims = append(claims, row)
		}
	}
	if len(claims) == 0 {
		return nil
	}
	adopter, ok := store.(interface {
		AdoptPodCapacity([]storage.PodCapacityReservation, string, string, uint64) error
	})
	if !ok {
		return fmt.Errorf("capacity store cannot transfer reservation ownership")
	}
	for _, claim := range claims {
		client, ok := adopted[claim.Target].(podCapacityClient)
		if !ok {
			return fmt.Errorf("generator %s cannot confirm capacity ownership", claim.Target)
		}
		request := podClaim(claim, false)
		request.ExecutionGeneration = generation
		ack, err := client.ReservePodCapacity(ctx, request)
		if err != nil {
			return fmt.Errorf("generator %s: %w", claim.Target, err)
		}
		if !podCapacityAckConfirms(ack, claim.Incarnation) {
			return fmt.Errorf("generator %s did not confirm adopted capacity", claim.Target)
		}
	}
	return tr.trackDurableWrite(func() error {
		return adopter.AdoptPodCapacity(claims, executionLeaseName, owner, generation)
	})
}

// A standby holds a dedicated budget without executable groups.
func standbyCapacityReservation(claim storage.PodCapacityReservation) bool {
	return claim.Dedicated && len(claim.GroupPeaks) == 0
}

// Add idle standbys to the same first-epoch batch as executable participants.
// A separate epoch claim could otherwise fence another run whose adoption is
// present in that batch. The caller keeps these transports out of run shards.
func (tr *TestRunner) addStandbyCapacityAdoptionTargets(candidates []*runAdoptionCandidate, byTarget map[string][]generatorrpc.RunAdoption) error {
	store, ok := tr.testStore.(podCapacityStore)
	if !ok {
		return nil
	}
	rows, err := store.ListPodCapacityReservations()
	if err != nil {
		return fmt.Errorf("read standby reservations before epoch claims: %w", err)
	}
	byTest := make(map[string]*runAdoptionCandidate, len(candidates))
	for _, candidate := range candidates {
		byTest[candidate.test.ID] = candidate
	}
	for _, claim := range rows {
		candidate := byTest[claim.TestID]
		if candidate == nil || !standbyCapacityReservation(claim) {
			continue
		}
		candidate.standbyTargets = append(candidate.standbyTargets, claim.Target)
		if _, exists := byTarget[claim.Target]; !exists {
			byTarget[claim.Target] = nil
		}
	}
	for _, candidate := range candidates {
		sort.Strings(candidate.standbyTargets)
	}
	return nil
}
