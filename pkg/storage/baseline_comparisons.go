package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const MaxComparisonSnapshotBytes = 16 << 20

var ErrComparisonPolicyConflict = errors.New("comparison policy changed; reload before saving")

// ComparisonStore is optional for alternate stores; comparison writes fail closed
// when durable persistence is unavailable.
type ComparisonStore interface {
	SaveBaselineComparison(id, projectID, testID string, payload []byte) error
	GetBaselineComparison(id, projectID, testID string) ([]byte, error)
	GetComparisonPolicy(scenarioID, projectID string) ([]byte, int64, error)
	SaveComparisonPolicy(scenarioID, projectID string, expectedRevision int64, payload []byte) (int64, error)
}

func (s *SQLiteStore) SaveBaselineComparison(id, projectID, testID string, payload []byte) error {
	if id == "" || testID == "" || !json.Valid(payload) || len(payload) > MaxComparisonSnapshotBytes {
		return fmt.Errorf("invalid or oversized comparison snapshot")
	}
	_, err := s.db.Exec(`INSERT INTO baseline_comparison_snapshots (id, project_id, test_id, payload, created_at) VALUES (?, ?, ?, ?, ?)`, id, normalizeProjectID(projectID), testID, string(payload), time.Now().UTC())
	return err
}

func (s *SQLiteStore) GetBaselineComparison(id, projectID, testID string) ([]byte, error) {
	var payload string
	err := s.db.QueryRow(`SELECT payload FROM baseline_comparison_snapshots WHERE id = ? AND project_id = ? AND test_id = ?`, id, normalizeProjectID(projectID), testID).Scan(&payload)
	return []byte(payload), err
}

func (s *SQLiteStore) GetComparisonPolicy(scenarioID, projectID string) ([]byte, int64, error) {
	var payload string
	var revision int64
	err := s.db.QueryRow(`SELECT payload, revision FROM scenario_comparison_policies WHERE scenario_id = ? AND project_id = ?`, scenarioID, normalizeProjectID(projectID)).Scan(&payload, &revision)
	return []byte(payload), revision, err
}

func (s *SQLiteStore) SaveComparisonPolicy(scenarioID, projectID string, expectedRevision int64, payload []byte) (int64, error) {
	if expectedRevision < 0 || !json.Valid(payload) || len(payload) > 128<<10 {
		return 0, fmt.Errorf("invalid comparison policy")
	}
	var result sql.Result
	var err error
	if expectedRevision == 0 {
		result, err = s.db.Exec(`INSERT INTO scenario_comparison_policies (scenario_id, project_id, revision, payload) VALUES (?, ?, 1, ?) ON CONFLICT (scenario_id) DO NOTHING`, scenarioID, normalizeProjectID(projectID), string(payload))
	} else {
		result, err = s.db.Exec(`UPDATE scenario_comparison_policies SET payload = ?, revision = revision + 1 WHERE scenario_id = ? AND project_id = ? AND revision = ?`, string(payload), scenarioID, normalizeProjectID(projectID), expectedRevision)
	}
	if err != nil {
		return 0, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if n != 1 {
		return 0, ErrComparisonPolicyConflict
	}
	return expectedRevision + 1, nil
}
