package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Per-script recorded baselines. The recorded exchanges are stored as a JSON
// blob in a dedicated table (not in the hot scripts table) so they are loaded
// only on demand, when diagnosing record-vs-replay correlation.

// maxRecordingObservations caps how many exchanges a baseline persists, to keep
// the blob bounded regardless of recording size.
const maxRecordingObservations = 500

// SaveScriptRecording upserts the recorded baseline for a script.
func (s *SQLiteStore) SaveScriptRecording(record *ScriptRecordingRecord) error {
	if record == nil {
		return fmt.Errorf("script recording is required")
	}
	record.ScriptID = strings.TrimSpace(record.ScriptID)
	if record.ScriptID == "" {
		return fmt.Errorf("script recording script_id is required")
	}
	if record.SchemaVersion == "" {
		record.SchemaVersion = scriptRecordingSchemaVersion
	}
	if len(record.Observations) > maxRecordingObservations {
		record.Observations = record.Observations[:maxRecordingObservations]
	}
	if len(record.CorrelationObservations) > maxRecordingObservations {
		record.CorrelationObservations = record.CorrelationObservations[:maxRecordingObservations]
	}
	if len(record.CorrelationObservations) > 0 {
		record.RequestCount = len(record.CorrelationObservations)
	} else {
		record.RequestCount = len(record.Observations)
	}
	now := time.Now().UTC()
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	record.UpdatedAt = now

	observationsJSON, err := json.Marshal(record.Observations)
	if err != nil {
		return fmt.Errorf("marshal recording observations: %w", err)
	}
	correlationObservationsJSON, err := json.Marshal(record.CorrelationObservations)
	if err != nil {
		return fmt.Errorf("marshal recording correlation observations: %w", err)
	}
	_, err = s.db.Exec(`INSERT INTO script_recordings (script_id, schema_version, source, sensitivity, request_count, observations_json, correlation_observations_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(script_id) DO UPDATE SET
			schema_version=excluded.schema_version,
			source=excluded.source,
			sensitivity=excluded.sensitivity,
			request_count=excluded.request_count,
			observations_json=excluded.observations_json,
			correlation_observations_json=excluded.correlation_observations_json,
			updated_at=excluded.updated_at`,
		record.ScriptID,
		record.SchemaVersion,
		record.Source,
		record.Sensitivity,
		record.RequestCount,
		string(observationsJSON),
		string(correlationObservationsJSON),
		record.CreatedAt,
		record.UpdatedAt,
	)
	return err
}

// GetScriptRecording returns the recorded baseline for a script, or (nil, nil)
// when none exists.
func (s *SQLiteStore) GetScriptRecording(scriptID string) (*ScriptRecordingRecord, error) {
	scriptID = strings.TrimSpace(scriptID)
	if scriptID == "" {
		return nil, nil
	}
	var (
		record                      ScriptRecordingRecord
		observationsJSON            string
		correlationObservationsJSON string
	)
	err := s.db.QueryRow(`SELECT script_id, COALESCE(schema_version, ''), COALESCE(source, ''), COALESCE(sensitivity, ''), COALESCE(request_count, 0), COALESCE(observations_json, ''), COALESCE(correlation_observations_json, ''), created_at, updated_at FROM script_recordings WHERE script_id = ?`, scriptID).Scan(
		&record.ScriptID,
		&record.SchemaVersion,
		&record.Source,
		&record.Sensitivity,
		&record.RequestCount,
		&observationsJSON,
		&correlationObservationsJSON,
		&record.CreatedAt,
		&record.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if observationsJSON != "" {
		if err := json.Unmarshal([]byte(observationsJSON), &record.Observations); err != nil {
			return nil, fmt.Errorf("unmarshal recording observations: %w", err)
		}
	}
	if correlationObservationsJSON != "" {
		if err := json.Unmarshal([]byte(correlationObservationsJSON), &record.CorrelationObservations); err != nil {
			return nil, fmt.Errorf("unmarshal recording correlation observations: %w", err)
		}
	}
	return &record, nil
}

// DeleteScriptRecording removes the recorded baseline for a script.
func (s *SQLiteStore) DeleteScriptRecording(scriptID string) error {
	scriptID = strings.TrimSpace(scriptID)
	if scriptID == "" {
		return nil
	}
	_, err := s.db.Exec(`DELETE FROM script_recordings WHERE script_id = ?`, scriptID)
	return err
}
