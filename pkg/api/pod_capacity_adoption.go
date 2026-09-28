package api

import (
	"context"
	"fmt"

	"github.com/jasonczech/apexload/pkg/storage"
)

// adoptPodCapacity confirms the generator's atomic assignment/claim transfer
// before changing durable ownership. An older generator rejects the unchanged
// claim with a new generation, so mixed versions fail closed. Completed and
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
		if row.TestID == testID && adopted[row.Target] != nil {
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
		if !ack.GetAccepted() || ack.GetSnapshot().GetIncarnation() != claim.Incarnation {
			return fmt.Errorf("generator %s did not confirm adopted capacity", claim.Target)
		}
	}
	return tr.trackDurableWrite(func() error {
		return adopter.AdoptPodCapacity(claims, executionLeaseName, owner, generation)
	})
}
