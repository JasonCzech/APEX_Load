package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// MaxTelemetryCheckpointBytes bounds the opaque controller recovery payload.
const MaxTelemetryCheckpointBytes = 8 << 20

// TelemetryCheckpointWrite binds one atomic cursor/effects snapshot to the
// existing test incarnation and execution term. Revision orders concurrent
// writes; a late snapshot can never replace a newer committed snapshot.
type TelemetryCheckpointWrite struct {
	RunIncarnation          string
	ID, RefKey, ProjectID   string
	StartTime               time.Time
	Generation              uint64
	LeaseName, LeaseOwnerID string
	Revision                int64
	Payload                 string
}

// SaveTelemetryCheckpoint updates only controller-owned recovery state. Full
// SaveTest calls cannot clobber it. Deleting the test deletes this state too.
func (s *SQLiteStore) SaveTelemetryCheckpoint(w TelemetryCheckpointWrite) (bool, error) {
	if w.RunIncarnation == "" || w.ID == "" || w.StartTime.IsZero() || w.Revision <= 0 || len(w.Payload) == 0 || len(w.Payload) > MaxTelemetryCheckpointBytes {
		return false, fmt.Errorf("invalid telemetry checkpoint")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if w.Generation > 0 || w.LeaseOwnerID != "" || w.LeaseName != "" {
		if w.Generation == 0 || w.LeaseOwnerID == "" || w.LeaseName == "" {
			return false, nil
		}
		held, err := schedulerLeaseTermHeldInTransaction(tx, s.db.dialect, w.LeaseName, w.LeaseOwnerID, w.Generation)
		if err != nil || !held {
			return false, err
		}
	}
	var provenance string
	query := `SELECT COALESCE(initial_provenance_json, '') FROM tests WHERE id = ?`
	if s.db.dialect == storeDialectPostgres {
		query += ` FOR UPDATE`
	}
	if err := tx.QueryRow(query, w.ID).Scan(&provenance); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	incarnation, err := TestRunIncarnation(json.RawMessage(provenance))
	if err != nil || incarnation != w.RunIncarnation {
		return false, nil
	}
	result, err := tx.Exec(`UPDATE tests SET telemetry_checkpoint_json = ?, telemetry_checkpoint_revision = ?
 WHERE id = ? AND COALESCE(ref_key, '') = ? AND COALESCE(project_id, '') = ? AND start_time = ?
 AND execution_generation = ? AND telemetry_checkpoint_revision < ? AND COALESCE(initial_provenance_json, '') = ?
 AND status IN ('INITIALIZING', 'STARTING', 'RUNNING', 'PAUSED', 'STOPPING')`,
		w.Payload, w.Revision, w.ID, w.RefKey, w.ProjectID, w.StartTime.UTC(), int64(w.Generation), w.Revision, provenance)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// LoadTelemetryCheckpoint reads one test identity; absence is legacy/unknown.
// The controller validates the embedded incarnation and version before restoring.
// The payload is private, never part of ordinary test JSON or list projections.
func (s *SQLiteStore) LoadTelemetryCheckpoint(id, refKey, projectID string, start time.Time) (string, int64, error) {
	var raw string
	var revision int64
	err := s.db.QueryRow(`SELECT COALESCE(telemetry_checkpoint_json, ''), telemetry_checkpoint_revision FROM tests
 WHERE id = ? AND COALESCE(ref_key, '') = ? AND COALESCE(project_id, '') = ? AND start_time = ?`, id, refKey, projectID, start.UTC()).Scan(&raw, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, nil
	}
	if len(raw) > MaxTelemetryCheckpointBytes {
		return "", 0, fmt.Errorf("telemetry checkpoint exceeds limit")
	}
	return raw, revision, err
}
