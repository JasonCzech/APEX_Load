package storage

import (
	"encoding/json"
	"time"
)

// Source deletion and preparation consumption share the project row lock.
// Checking both sides of the handoff prevents deleting a resource between the
// prepared authorization and its immutable active job.
func monitorResourceInUseTx(tx *storeTx, project, source, connection string) (bool, error) {
	rows, err := tx.Query(`SELECT 'job',payload FROM run_monitor_jobs WHERE project_id=? AND state IN ('collecting','reconciling') UNION ALL SELECT 'preparation',p.payload FROM preparations p JOIN tests t ON t.id=p.test_id AND t.project_id=p.project_id WHERE p.project_id=? AND (p.state IN ('preparing','ready') OR (p.state='consumed' AND t.status IN ('INITIALIZING','RUNNING','PAUSED','STOPPING') AND NOT EXISTS (SELECT 1 FROM run_monitor_jobs j WHERE j.project_id=p.project_id AND j.test_id=p.test_id)))`, project, project)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind, body string
		if err = rows.Scan(&kind, &body); err != nil {
			return false, err
		}
		if kind == "job" {
			var job RunMonitorJob
			if err = json.Unmarshal([]byte(body), &job); err != nil {
				return false, err
			}
			if source != "" && job.Source.ID == source || connection != "" && job.Source.Pack == "elastic" && job.Source.ConnectionID == connection {
				return true, nil
			}
			continue
		}
		var preparation Preparation
		if err = json.Unmarshal([]byte(body), &preparation); err != nil {
			return false, err
		}
		if preparation.Monitors == nil {
			continue
		}
		for _, selected := range preparation.Monitors.Sources {
			if source != "" && selected.ID == source || connection != "" && selected.Pack == "elastic" && selected.ConnectionID == connection {
				return true, nil
			}
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	rows.Close()
	if connection == "" {
		return false, nil
	}
	inspections, err := tx.Query(`SELECT `+elasticInspectionColumns+` FROM elastic_inspections WHERE project_id=? AND connection_id=?`, project, connection)
	if err != nil {
		return false, err
	}
	defer inspections.Close()
	now := time.Now().UTC()
	for inspections.Next() {
		op, err := scanElasticInspection(inspections)
		if err != nil {
			return false, err
		}
		if elasticInspectionNeedsCredentials(op, now) {
			return true, nil
		}
	}
	return false, inspections.Err()
}
