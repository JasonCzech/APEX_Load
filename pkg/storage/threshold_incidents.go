package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ThresholdIncident is one breach episode. Acknowledgement does not resolve a breach.
type ThresholdIncident struct {
	ID             string         `json:"id"`
	ProjectID      string         `json:"project_id"`
	TestID         string         `json:"test_id"`
	TestName       string         `json:"test_name"`
	Key            string         `json:"key"`
	Severity       string         `json:"severity"`
	Payload        map[string]any `json:"payload"`
	Active         bool           `json:"active"`
	FirstSeenAt    time.Time      `json:"first_seen_at"`
	LastSeenAt     time.Time      `json:"last_seen_at"`
	ResolvedAt     *time.Time     `json:"resolved_at,omitempty"`
	AcknowledgedBy string         `json:"acknowledged_by,omitempty"`
	AcknowledgedAt *time.Time     `json:"acknowledged_at,omitempty"`
}

type ThresholdIncidentFilter struct {
	Status, Severity, Query string
	Since                   *time.Time
}

type ThresholdIncidentStore interface {
	RecordThresholdEvaluation(projectID, testID, testName string, at time.Time, covered bool, incidents []ThresholdIncident) error
	ListThresholdIncidents(projectID string, limit, offset int, filter ThresholdIncidentFilter) ([]ThresholdIncident, error)
	GetThresholdIncident(id string) (*ThresholdIncident, error)
	AcknowledgeThresholdIncident(id, projectID, actor string, acknowledged bool, at time.Time) (bool, error)
	ThresholdIncidentCounts(projectID string) (active, unacknowledged, danger int, err error)
	LastThresholdEvaluation(projectID string) (*time.Time, error)
}

func (s *SQLiteStore) RecordThresholdEvaluation(projectID, testID, testName string, at time.Time, covered bool, incidents []ThresholdIncident) error {
	if projectID == "" || testID == "" || at.IsZero() {
		return errors.New("project, test and evaluation time are required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = lockProjectForOwnedMutation(tx, projectID); err != nil {
		return err
	}
	var owner, status string
	if err = tx.QueryRow(`SELECT project_id, status FROM tests WHERE id = ?`, testID).Scan(&owner, &status); err != nil {
		return err
	}
	if normalizeProjectID(owner) != normalizeProjectID(projectID) {
		return errors.New("test belongs to another project")
	}
	coverage := 0
	if covered {
		coverage = 1
	}
	result, err := tx.Exec(`INSERT INTO threshold_evaluations(test_id, project_id, evaluated_at, covered) VALUES (?, ?, ?, ?)
 ON CONFLICT(test_id) DO UPDATE SET evaluated_at=excluded.evaluated_at, covered=excluded.covered WHERE threshold_evaluations.evaluated_at < excluded.evaluated_at`, testID, projectID, at, coverage)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return tx.Commit()
	}
	rows, err := tx.Query(`SELECT id, incident_key FROM threshold_incidents WHERE test_id = ? AND active = 1`, testID)
	if err != nil {
		return err
	}
	existing := map[string]string{}
	for rows.Next() {
		var id, key string
		if err = rows.Scan(&id, &key); err != nil {
			rows.Close()
			return err
		}
		existing[key] = id
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	terminal := status == "COMPLETE" || status == "FAILED" || status == "ABORTED" || status == "STOPPED" || status == "ERROR"
	seen := map[string]bool{}
	for _, incident := range incidents {
		if incident.Key == "" || seen[incident.Key] {
			continue
		}
		seen[incident.Key] = true
		payload, e := json.Marshal(incident.Payload)
		if e != nil {
			return e
		}
		severity := "warning"
		if incident.Severity == "danger" {
			severity = "danger"
		}
		if id := existing[incident.Key]; id != "" {
			_, err = tx.Exec(`UPDATE threshold_incidents SET payload_json=?, severity=?, last_seen_at=? WHERE id=?`, string(payload), severity, at, id)
		} else {
			_, err = tx.Exec(`INSERT INTO threshold_incidents(id, project_id, test_id, test_name, incident_key, severity, payload_json, active, first_seen_at, last_seen_at) VALUES (?, ?, ?, ?, ?, ?, ?, TRUE, ?, ?)`, uuid.NewString(), projectID, testID, testName, incident.Key, severity, string(payload), at, at)
		}
		if err != nil {
			return err
		}
		delete(existing, incident.Key)
	}
	for _, id := range existing {
		if _, err = tx.Exec(`UPDATE threshold_incidents SET active=FALSE, resolved_at=? WHERE id=?`, at, id); err != nil {
			return err
		}
	}
	if terminal {
		if _, err = tx.Exec(`UPDATE threshold_incidents SET active=FALSE, resolved_at=? WHERE test_id=? AND active=1`, at, testID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

const thresholdIncidentSelect = `SELECT i.id, i.project_id, i.test_id, i.test_name, i.incident_key, i.severity, i.payload_json,
 CASE WHEN t.status IN ('COMPLETE','FAILED','ABORTED','STOPPED','ERROR') THEN FALSE ELSE i.active END,
 i.first_seen_at, i.last_seen_at, i.resolved_at, i.acknowledged_by, i.acknowledged_at
 FROM threshold_incidents i JOIN tests t ON t.id=i.test_id`

func scanThresholdIncident(row capacityRowScanner) (*ThresholdIncident, error) {
	var i ThresholdIncident
	var payload string
	err := row.Scan(&i.ID, &i.ProjectID, &i.TestID, &i.TestName, &i.Key, &i.Severity, &payload, &i.Active, &i.FirstSeenAt, &i.LastSeenAt, &i.ResolvedAt, &i.AcknowledgedBy, &i.AcknowledgedAt)
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal([]byte(payload), &i.Payload); err != nil {
		return nil, err
	}
	return &i, nil
}
func (s *SQLiteStore) ListThresholdIncidents(projectID string, limit, offset int, filter ThresholdIncidentFilter) ([]ThresholdIncident, error) {
	if strings.TrimSpace(projectID) == "" {
		return nil, errors.New("project is required")
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	where := ` WHERE i.project_id=?`
	args := []any{projectID}
	active := `(i.active=1 AND t.status NOT IN ('COMPLETE','FAILED','ABORTED','STOPPED','ERROR'))`
	switch filter.Status {
	case "active":
		where += " AND " + active
	case "resolved":
		where += " AND NOT " + active
	case "unacknowledged":
		where += " AND " + active + " AND i.acknowledged_at IS NULL"
	case "acknowledged":
		where += " AND i.acknowledged_at IS NOT NULL"
	}
	if filter.Severity != "" {
		where += " AND i.severity=?"
		args = append(args, filter.Severity)
	}
	if filter.Query != "" {
		where += ` AND (LOWER(i.test_name) LIKE ? OR LOWER(i.test_id) LIKE ? OR LOWER(i.payload_json) LIKE ?)`
		q := "%" + strings.ToLower(filter.Query) + "%"
		args = append(args, q, q, q)
	}
	if filter.Since != nil {
		where += " AND i.last_seen_at>=?"
		args = append(args, *filter.Since)
	}
	args = append(args, limit, offset)
	rows, err := s.db.Query(thresholdIncidentSelect+where+` ORDER BY i.last_seen_at DESC, i.id DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []ThresholdIncident{}
	for rows.Next() {
		i, e := scanThresholdIncident(rows)
		if e != nil {
			return nil, e
		}
		result = append(result, *i)
	}
	return result, rows.Err()
}
func (s *SQLiteStore) GetThresholdIncident(id string) (*ThresholdIncident, error) {
	return scanThresholdIncident(s.db.QueryRow(thresholdIncidentSelect+` WHERE i.id=?`, id))
}
func (s *SQLiteStore) AcknowledgeThresholdIncident(id, projectID, actor string, acknowledged bool, at time.Time) (bool, error) {
	if acknowledged && strings.TrimSpace(actor) == "" {
		return false, fmt.Errorf("acknowledgement actor is required")
	}
	var stamp any
	if acknowledged {
		stamp = at
	} else {
		actor = ""
	}
	res, err := s.db.Exec(`UPDATE threshold_incidents SET acknowledged_by=?, acknowledged_at=? WHERE id=? AND project_id=?`, actor, stamp, id, projectID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}
func (s *SQLiteStore) LastThresholdEvaluation(projectID string) (*time.Time, error) {
	var at time.Time
	err := s.db.QueryRow(`SELECT evaluated_at FROM threshold_evaluations WHERE project_id=? AND covered=1 ORDER BY evaluated_at DESC LIMIT 1`, projectID).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &at, nil
}

func (s *SQLiteStore) ThresholdIncidentCounts(projectID string) (active, unacknowledged, danger int, err error) {
	err = s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(CASE WHEN acknowledged_at IS NULL THEN 1 ELSE 0 END),0), COALESCE(SUM(CASE WHEN severity='danger' THEN 1 ELSE 0 END),0) FROM threshold_incidents i JOIN tests t ON t.id=i.test_id WHERE i.project_id=? AND i.active=1 AND t.status NOT IN ('COMPLETE','FAILED','ABORTED','STOPPED','ERROR')`, projectID).Scan(&active, &unacknowledged, &danger)
	return
}
