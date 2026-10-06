package storage

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

// RetainElasticInspectionCleanup keeps executor-owned handles in a private
// column. Cancellation preserves the owner token so the interrupted executor
// can still hand off cleanup, while a replaced executor cannot overwrite it.
func (s *SQLiteStore) RetainElasticInspectionCleanup(id, owner string, cursors []*analysis.ProviderContinuation, now time.Time) (bool, error) {
	if id == "" || owner == "" || now.IsZero() || len(cursors) > 32 {
		return false, errors.New("invalid inspection cleanup ownership")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var project string
	if err = tx.QueryRow(`SELECT project_id FROM elastic_inspections WHERE id=?`, id).Scan(&project); err != nil {
		return false, err
	}
	if _, err = lockProjectForOwnedMutation(tx, project); err != nil {
		return false, err
	}
	op, err := scanElasticInspection(tx.QueryRow(`SELECT `+elasticInspectionColumns+` FROM elastic_inspections WHERE id=?`, id))
	if err != nil {
		return false, err
	}
	if op.OwnerToken != owner {
		return false, nil
	}
	retained := RunMonitorJob{ElasticRetiredContinuations: op.CleanupContinuations}
	// Every cursor is independently validated. Inspection queries can open
	// several capability probes, so their windows need not be identical.
	for _, cursor := range cursors {
		if cursor == nil || cursor.Validate() != nil || cursor.PITID == "" && cursor.AsyncID == "" {
			return false, errors.New("invalid inspection owned handle")
		}
		if err = retainElasticCleanupContinuations(&retained, []*analysis.ProviderContinuation{cursor}, cursor.WindowFrom, cursor.WindowUntil, now); err != nil {
			return false, err
		}
	}
	if len(cursors) == 0 {
		if err = retainElasticCleanupContinuations(&retained, nil, time.Time{}, time.Time{}, now); err != nil {
			return false, err
		}
	}
	body, err := marshalInspectionCleanup(retained.ElasticRetiredContinuations)
	if err != nil {
		return false, err
	}
	result, err := tx.Exec(`UPDATE elastic_inspections SET cleanup_json=? WHERE id=? AND owner_token=?`, body, id, owner)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if n != 1 {
		return false, nil
	}
	return true, tx.Commit()
}

func marshalInspectionCleanup(cursors []*analysis.ProviderContinuation) (string, error) {
	if len(cursors) == 0 {
		return "", nil
	}
	body, err := json.Marshal(cursors)
	if err != nil || len(body) > maxElasticCleanupBytes {
		return "", errors.New("inspection cleanup exceeds private bound")
	}
	return string(body), nil
}

// ListElasticInspectionCleanup includes completed/cancelled and expired
// operations. Only known upstream expiry can end cleanup ownership without an
// acknowledged DELETE. The collector filter never expands to other collectors.
func (s *SQLiteStore) ListElasticInspectionCleanup(project, collector string, limit int) ([]*ElasticInspection, error) {
	if limit < 1 || limit > 100 {
		return nil, errors.New("invalid inspection cleanup list limit")
	}
	rows, err := s.db.Query(`SELECT `+elasticInspectionColumns+` FROM elastic_inspections WHERE collector_id=? AND (?='' OR project_id=?) AND cleanup_json<>'' ORDER BY created_at,id LIMIT 500`, collector, project, project)
	if err != nil {
		return nil, err
	}
	out := []*ElasticInspection{}
	expired := []*ElasticInspection{}
	now := time.Now().UTC()
	for rows.Next() {
		op, e := scanElasticInspection(rows)
		if e != nil {
			rows.Close()
			return nil, e
		}
		if elasticInspectionNeedsCleanup(op, now) {
			if len(out) < limit {
				out = append(out, op)
			}
		} else {
			expired = append(expired, op)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, op := range expired {
		if _, err = s.ClearElasticInspectionCleanup(op.ID, op.CleanupContinuations); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Clear acknowledges exactly the handles supplied by a cleanup assignment.
// A concurrent retention or replacement remains recoverable by the next poll.
func (s *SQLiteStore) ClearElasticInspectionCleanup(id string, expected []*analysis.ProviderContinuation) (bool, error) {
	if id == "" || len(expected) == 0 || len(expected) > 32 {
		return false, errors.New("expected inspection cleanup required")
	}
	for _, cursor := range expected {
		if cursor == nil || cursor.Validate() != nil {
			return false, errors.New("invalid expected inspection cleanup")
		}
	}
	body, err := marshalInspectionCleanup(expected)
	if err != nil {
		return false, err
	}
	result, err := s.db.Exec(`UPDATE elastic_inspections SET cleanup_json='' WHERE id=? AND cleanup_json=?`, id, body)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

func elasticInspectionNeedsCleanup(op *ElasticInspection, now time.Time) bool {
	if op == nil {
		return false
	}
	for _, cursor := range op.CleanupContinuations {
		if cursor != nil && (cursor.PITID != "" || cursor.AsyncID != "") && (cursor.KeepAliveUntil.IsZero() || cursor.KeepAliveUntil.After(now)) {
			return true
		}
	}
	return false
}

func elasticInspectionNeedsCredentials(op *ElasticInspection, now time.Time) bool {
	return op != nil && ((op.State == "pending" || op.State == "running") && op.ExpiresAt.After(now) || elasticInspectionNeedsCleanup(op, now))
}

func elasticInspectionConnectionSnapshot(payload string) (*ExpandedElasticConnectionSnapshot, error) {
	var work struct {
		Connection *ExpandedElasticConnectionSnapshot `json:"connection"`
	}
	if err := json.Unmarshal([]byte(payload), &work); err != nil {
		return nil, err
	}
	return work.Connection, nil
}
