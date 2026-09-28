package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"time"
)

// ReservePodCapacityIfUnchanged replaces the exact expected claims and reserves
// any new targets in one execution-lease-fenced capacity transaction. Every
// preexisting claim must be supplied; additional targets must still be absent.
// Releasing claims cannot be revived. Replacements become pending until their
// generator acknowledgment is committed through TransitionPodCapacity.
func (s *SQLiteStore) ReservePodCapacityIfUnchanged(claims, expected []PodCapacityReservation, leaseName, owner string, generation uint64) error {
	return s.reservePodCapacity(claims, expected, true, leaseName, owner, generation)
}

// The caller holds the common capacity-settings fence in the transaction. Both
// ordinary reservations and releases acquire it, including on PostgreSQL.
func comparePodCapacityPlacement(tx *storeTx, claims, expected []PodCapacityReservation, generation uint64) error {
	proposed := make(map[string]PodCapacityReservation, len(claims))
	for _, claim := range claims {
		if claim.Target == "" || claim.TestID == "" {
			return errors.New("invalid capacity replacement identity")
		}
		if _, exists := proposed[claim.Target]; exists {
			return errors.New("duplicate capacity replacement target")
		}
		proposed[claim.Target] = claim
	}
	before := make(map[string]PodCapacityReservation, len(expected))
	for _, claim := range expected {
		next, exists := proposed[claim.Target]
		if !exists || next.TestID != claim.TestID || claim.Generation != generation || (claim.State != "pending" && claim.State != "committed") {
			return errors.New("invalid expected capacity reservation")
		}
		if _, duplicate := before[claim.Target]; duplicate {
			return errors.New("duplicate expected capacity reservation")
		}
		before[claim.Target] = claim
	}
	for _, next := range claims {
		var raw, state string
		var storedGeneration uint64
		err := tx.QueryRow(`SELECT reservation_json,state,generation FROM pod_capacity_reservations WHERE target=? AND test_id=?`, next.Target, next.TestID).Scan(&raw, &state, &storedGeneration)
		want, replacing := before[next.Target]
		if errors.Is(err, sql.ErrNoRows) {
			if replacing {
				return errors.New("capacity reservation disappeared before replacement")
			}
			continue
		}
		if err != nil {
			return err
		}
		if !replacing {
			return errors.New("unexpected existing capacity reservation")
		}
		var current PodCapacityReservation
		if err := json.Unmarshal([]byte(raw), &current); err != nil {
			return err
		}
		current.State = state
		if current.Generation != storedGeneration || !reflect.DeepEqual(current, want) {
			return errors.New("capacity reservation changed before replacement")
		}
		if _, err := tx.Exec(`UPDATE pod_capacity_reservations SET state='pending',updated_at=? WHERE target=? AND test_id=?`, time.Now().UTC(), next.Target, next.TestID); err != nil {
			return err
		}
	}
	return nil
}
