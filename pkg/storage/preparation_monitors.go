package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
)

// PreparationMonitorSnapshot is immutable execution evidence. Definitions may
// change after consumption; the execution commit must still use this snapshot.
type PreparationMonitorSnapshot struct {
	ScenarioID string                   `json:"scenario_id,omitempty"`
	Bindings   MonitorBindings          `json:"bindings"`
	Sources    map[string]MonitorSource `json:"sources"`
}

func preparationMonitorSnapshotTx(tx *storeTx, project, run string) (*PreparationMonitorSnapshot, error) {
	snapshot := &PreparationMonitorSnapshot{Sources: map[string]MonitorSource{}}
	if err := tx.QueryRow(`SELECT COALESCE(scenario_id,'') FROM tests WHERE id=? AND project_id=?`, run, project).Scan(&snapshot.ScenarioID); err != nil {
		return nil, err
	}
	if snapshot.ScenarioID == "" {
		return snapshot, nil
	}
	var body string
	err := tx.QueryRow(`SELECT payload FROM monitor_bindings WHERE project_id=? AND scenario_id=?`, project, snapshot.ScenarioID).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return snapshot, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal([]byte(body), &snapshot.Bindings); err != nil {
		return nil, err
	}
	for _, binding := range snapshot.Bindings.Sources {
		err = tx.QueryRow(`SELECT payload FROM monitor_sources WHERE project_id=? AND id=?`, project, binding.SourceID).Scan(&body)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var source MonitorSource
		if err = json.Unmarshal([]byte(body), &source); err != nil {
			return nil, err
		}
		snapshot.Sources[source.ID] = source
	}
	return snapshot, nil
}

func consumedMonitorSnapshotTx(tx *storeTx, project, run string) (*PreparationMonitorSnapshot, error) {
	var body string
	err := tx.QueryRow(`SELECT payload FROM preparations WHERE project_id=? AND test_id=? AND state='consumed' ORDER BY created_at DESC,id DESC LIMIT 1`, project, run).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var p Preparation
	if err = json.Unmarshal([]byte(body), &p); err != nil {
		return nil, err
	}
	if p.Monitors == nil {
		return nil, ErrPreparationConflict
	}
	return p.Monitors, nil
}
