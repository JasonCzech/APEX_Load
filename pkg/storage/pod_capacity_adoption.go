package storage

import (
	"encoding/json"
	"errors"
	"reflect"
	"time"
)

// AdoptPodCapacity transfers acknowledged live and idle standby reservations
// to the successor term. The caller must confirm each unchanged claim under the
// new epoch and first adopt any live assignment. No capacity is released or
// recalculated by this operation.
func (s *SQLiteStore) AdoptPodCapacity(expected []PodCapacityReservation, leaseName, owner string, generation uint64) error {
	if len(expected) == 0 {
		return errors.New("empty capacity adoption")
	}
	tx, err := s.beginExecutionLeaseTermTransaction(leaseName, owner, generation)
	if err != nil {
		return err
	}
	if tx == nil {
		return errors.New("pod reservation execution owner fenced")
	}
	defer tx.Rollback()
	if err := acquireCapacitySettingsFence(tx, time.Now()); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, before := range expected {
		if before.TestID == "" || before.TestID != expected[0].TestID || before.Target == "" || before.ReservationID == "" || before.Incarnation == "" || before.State != "committed" || before.Generation == 0 || before.Generation >= generation || seen[before.Target] {
			return errors.New("invalid capacity adoption identity or state")
		}
		seen[before.Target] = true
		var raw, state string
		var storedGeneration uint64
		if err := tx.QueryRow(`SELECT reservation_json,state,generation FROM pod_capacity_reservations WHERE target=? AND test_id=?`, before.Target, before.TestID).Scan(&raw, &state, &storedGeneration); err != nil {
			return err
		}
		var current PodCapacityReservation
		if err := json.Unmarshal([]byte(raw), &current); err != nil {
			return err
		}
		current.State = state
		after := before
		after.Generation = generation
		if current.Generation != storedGeneration || (!reflect.DeepEqual(current, before) && !reflect.DeepEqual(current, after)) {
			return errors.New("capacity reservation changed during adoption")
		}
		encoded, err := json.Marshal(after)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE pod_capacity_reservations SET generation=?,reservation_json=?,updated_at=? WHERE target=? AND test_id=?`, generation, string(encoded), time.Now().UTC(), before.Target, before.TestID); err != nil {
			return err
		}
	}
	// A standby inserted after the caller's snapshot must not be silently left
	// with an old owner. Capacity mutations share this transaction fence, so
	// checking all idle dedicated claims here closes that observation race. A
	// standby already reserved and committed under this term (an evacuation
	// rollback) has no old owner.
	rows, err := tx.Query(`SELECT target, reservation_json, generation, state FROM pod_capacity_reservations WHERE test_id=?`, expected[0].TestID)
	if err != nil {
		return err
	}
	var coverageErr error
	for rows.Next() {
		var target, raw, state string
		var storedGeneration uint64
		if err := rows.Scan(&target, &raw, &storedGeneration, &state); err != nil {
			coverageErr = err
			break
		}
		var claim PodCapacityReservation
		if err := json.Unmarshal([]byte(raw), &claim); err != nil {
			coverageErr = err
			break
		}
		if claim.Dedicated && len(claim.GroupPeaks) == 0 && !seen[target] && (storedGeneration != generation || claim.Generation != generation || state != "committed") {
			coverageErr = errors.New("standby capacity changed during adoption")
			break
		}
	}
	if coverageErr == nil {
		coverageErr = rows.Err()
	}
	rows.Close()
	if coverageErr != nil {
		return coverageErr
	}
	return tx.Commit()
}
