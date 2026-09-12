package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

type PodCapacityReservation struct {
	ReservationID string                             `json:"reservation_id"`
	Revision      uint64                             `json:"revision"`
	GroupDemands  map[string]protocol.CapacityVector `json:"group_demands"`
	Target        string                             `json:"target"`
	TestID        string                             `json:"test_id"`
	Incarnation   string                             `json:"incarnation"`
	Generation    uint64                             `json:"generation"`
	State         string                             `json:"state"`
	ModelVersion  string                             `json:"model_version"`
	HeadroomPct   float64                            `json:"headroom_pct"`
	Budget        protocol.CapacityVector            `json:"budget"`
	Demand        protocol.CapacityVector            `json:"demand"`
	Dedicated     bool                               `json:"dedicated"`
	GroupPeaks    map[string]int32                   `json:"group_peaks"`
}

// ReservePodCapacity atomically claims the entire placement, serialized with
// other capacity decisions in both SQLite and PostgreSQL. Preview never calls it.
func (s *SQLiteStore) ReservePodCapacity(claims []PodCapacityReservation, leaseName, owner string, generation uint64) error {
	if len(claims) == 0 {
		return errors.New("empty pod reservation placement")
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
	var settings string
	if err := tx.QueryRow(`SELECT value FROM app_settings WHERE key='system_settings'`).Scan(&settings); err != nil && err != sql.ErrNoRows {
		return err
	}
	if settings != "" {
		enabled, err := podReservationsEnabled(settings)
		if err != nil {
			return err
		}
		if !enabled {
			return errors.New("peak pod reservations are disabled")
		}
	}
	seen := map[string]bool{}
	for _, claim := range claims {
		if claim.ReservationID == "" || claim.Target == "" || claim.TestID == "" || claim.Incarnation == "" || claim.Generation != generation || claim.ModelVersion != protocol.CapacityReservationModel || seen[claim.Target] {
			return errors.New("invalid pod reservation identity")
		}
		seen[claim.Target] = true
		if err := claim.Budget.Fits(claim.Demand); err != nil {
			return err
		}
		rows, err := tx.Query(`SELECT reservation_json FROM pod_capacity_reservations WHERE target=?`, claim.Target)
		if err != nil {
			return err
		}
		var existing []PodCapacityReservation
		for rows.Next() {
			var raw string
			if err = rows.Scan(&raw); err != nil {
				break
			}
			var r PodCapacityReservation
			if err = json.Unmarshal([]byte(raw), &r); err != nil {
				break
			}
			existing = append(existing, r)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return err
		}
		used := protocol.CapacityVector{}
		for _, r := range existing {
			if r.Incarnation != claim.Incarnation {
				return fmt.Errorf("generator %s requires incarnation reconciliation", claim.Target)
			}
			if r.TestID == claim.TestID {
				if r.ReservationID != claim.ReservationID {
					return errors.New("previous reservation attempt requires release")
				}
				r.State = "pending"
				copy := claim
				copy.State = "pending"
				if !reflect.DeepEqual(r, copy) && (claim.Revision != r.Revision+1 || claim.Revision == 0 || r.Generation != claim.Generation || r.Dedicated != claim.Dedicated || r.ModelVersion != claim.ModelVersion || r.HeadroomPct != claim.HeadroomPct || !reflect.DeepEqual(r.Budget, claim.Budget)) {
					return errors.New("reservation revision or frozen model changed")
				}
				continue
			}
			if r.Dedicated || claim.Dedicated {
				return fmt.Errorf("generator %s is dedicated", claim.Target)
			}
			if !reflect.DeepEqual(r.Budget, claim.Budget) || r.HeadroomPct != claim.HeadroomPct {
				return errors.New("reserved generator budget changed")
			}
			used = used.Add(r.Demand)
		}
		if claim.Dedicated && !reflect.DeepEqual(claim.Demand, claim.Budget) {
			return errors.New("dedicated claim must reserve full budget")
		}
		if err := claim.Budget.Fits(used.Add(claim.Demand)); err != nil {
			return fmt.Errorf("generator %s: %w", claim.Target, err)
		}
		claim.State = "pending"
		raw, err := json.Marshal(claim)
		if err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO pod_capacity_reservations(target,test_id,incarnation,generation,state,reservation_json,updated_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(target,test_id) DO UPDATE SET reservation_json=excluded.reservation_json,updated_at=excluded.updated_at`, claim.Target, claim.TestID, claim.Incarnation, generation, claim.State, string(raw), time.Now().UTC())
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func podReservationsEnabled(raw string) (bool, error) {
	var settings struct {
		CapacityPlanning struct {
			Enabled bool `json:"pod_reservations_enabled"`
		} `json:"capacity_planning"`
	}
	err := json.Unmarshal([]byte(raw), &settings)
	return settings.CapacityPlanning.Enabled, err
}

func (s *SQLiteStore) ListPodCapacityReservations() ([]PodCapacityReservation, error) {
	rows, err := s.db.Query(`SELECT reservation_json,state FROM pod_capacity_reservations ORDER BY target,test_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PodCapacityReservation
	for rows.Next() {
		var raw, state string
		if err := rows.Scan(&raw, &state); err != nil {
			return nil, err
		}
		var r PodCapacityReservation
		if err := json.Unmarshal([]byte(raw), &r); err != nil {
			return nil, err
		}
		r.State = state
		out = append(out, r)
	}
	return out, rows.Err()
}

// TransitionPodCapacity requires the current controller term even for release.
// Callers may release only after generator acknowledgement or incarnation fencing.
func (s *SQLiteStore) TransitionPodCapacity(target, testID, incarnation, reservationID, state, leaseName, owner string, generation uint64) error {
	if state != "committed" && state != "releasing" && state != "released" {
		return errors.New("invalid pod reservation state")
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
	var raw string
	if err := tx.QueryRow(`SELECT reservation_json FROM pod_capacity_reservations WHERE target=? AND test_id=? AND incarnation=?`, target, testID, incarnation).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	var current PodCapacityReservation
	if err := json.Unmarshal([]byte(raw), &current); err != nil {
		return err
	}
	if reservationID == "" || current.ReservationID != reservationID {
		return errors.New("reservation attempt changed")
	}
	if state == "released" {
		_, err = tx.Exec(`DELETE FROM pod_capacity_reservations WHERE target=? AND test_id=? AND incarnation=?`, target, testID, incarnation)
	} else {
		_, err = tx.Exec(`UPDATE pod_capacity_reservations SET state=?,updated_at=? WHERE target=? AND test_id=? AND incarnation=?`, state, time.Now().UTC(), target, testID, incarnation)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}
