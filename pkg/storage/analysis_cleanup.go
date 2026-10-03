package storage

import (
	"errors"
	"time"
)

type AnalysisCleanupJob struct {
	Kind       string
	ProjectID  string
	ResourceID string
	NotBefore  time.Time
}
type AnalysisCleanupStore interface {
	ListAnalysisCleanupJobs(time.Time, int) ([]AnalysisCleanupJob, error)
	CompleteAnalysisCleanupJob(AnalysisCleanupJob) error
}

// The grace period exceeds analytical worker leases and upload deadlines. It
// prevents a late, cancelled writer from recreating an object after cleanup.
func queueAnalysisCleanupTx(tx *storeTx, kind, project, id string) error {
	_, err := tx.Exec(`INSERT INTO analysis_cleanup_jobs(kind,project_id,resource_id,not_before) VALUES(?,?,?,?) ON CONFLICT(kind,resource_id) DO UPDATE SET not_before=excluded.not_before`, kind, project, id, time.Now().UTC().Add(20*time.Minute))
	return err
}

func queueProjectAnalysisCleanupTx(tx *storeTx, project string) error {
	now := time.Now().UTC().Add(20 * time.Minute)
	for _, query := range []string{
		`INSERT INTO analysis_cleanup_jobs(kind,project_id,resource_id,not_before) SELECT 'dataset',project_id,id,? FROM analysis_imports WHERE project_id=? ON CONFLICT(kind,resource_id) DO UPDATE SET not_before=excluded.not_before`,
		`INSERT INTO analysis_cleanup_jobs(kind,project_id,resource_id,not_before) SELECT 'monitor',project_id,test_id,? FROM run_monitor_sessions WHERE project_id=? ON CONFLICT(kind,resource_id) DO UPDATE SET not_before=excluded.not_before`,
	} {
		if _, err := tx.Exec(query, now, project); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) ListAnalysisCleanupJobs(now time.Time, limit int) ([]AnalysisCleanupJob, error) {
	if limit < 1 || limit > 100 {
		return nil, errors.New("invalid analysis cleanup page")
	}
	rows, err := s.db.Query(`SELECT j.kind,j.project_id,j.resource_id,j.not_before FROM analysis_cleanup_jobs j
        LEFT JOIN analysis_imports d ON j.kind='dataset' AND d.id=j.resource_id
        LEFT JOIN tests t ON j.kind='monitor' AND t.id=j.resource_id
        WHERE j.not_before<=? AND ((j.kind='dataset' AND (d.id IS NULL OR d.state IN ('cancelled','deleted','deleting'))) OR (j.kind='monitor' AND t.id IS NULL) OR j.kind='monitor-object')
        ORDER BY j.not_before,j.resource_id LIMIT ?`, now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []AnalysisCleanupJob{}
	for rows.Next() {
		var j AnalysisCleanupJob
		if err := rows.Scan(&j.Kind, &j.ProjectID, &j.ResourceID, &j.NotBefore); err != nil {
			return nil, err
		}
		result = append(result, j)
	}
	return result, rows.Err()
}
func (s *SQLiteStore) CompleteAnalysisCleanupJob(j AnalysisCleanupJob) error {
	_, err := s.db.Exec(`DELETE FROM analysis_cleanup_jobs WHERE kind=? AND project_id=? AND resource_id=? AND not_before=?`, j.Kind, j.ProjectID, j.ResourceID, j.NotBefore)
	return err
}
